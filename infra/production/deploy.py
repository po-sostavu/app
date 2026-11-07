#!/usr/bin/python3 -I
"""Root-owned forced SSH entry point. Read one release JSON from stdin."""

import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import select
import shutil
import ssl
import stat
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.request

ROOT = Path("/opt/po-sostavu")
IMAGES = ("pwa", "api", "migrate", "postgres")
REGISTRY = "ghcr.io/po-sostavu/app-"
SOURCE = "https://github.com/po-sostavu/app"


def require(condition, message):
    if not condition:
        raise ValueError(message)


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        require(key not in result, "Duplicate JSON field")
        result[key] = value
    return result


def parse_request(raw):
    require(0 < len(raw) <= 4096, "Request must contain at most 4096 bytes")
    data = json.loads(raw, object_pairs_hook=unique_object)
    require(isinstance(data, dict), "Expected a JSON object")
    if data.get("operation") == "rollback":
        require(set(data) == {"operation", "expected_sha"}, "Invalid rollback request")
        require(re.fullmatch(r"[a-f0-9]{40}", data["expected_sha"]), "Invalid rollback SHA")
        return data
    require(set(data) == {"schema", "sha", "run_id", "images", "rollback_compatible"}, "Unexpected release fields")
    require(type(data["schema"]) is int and data["schema"] == 1, "Unsupported schema")
    require(isinstance(data["sha"], str) and re.fullmatch(r"[a-f0-9]{40}", data["sha"]), "Invalid release SHA")
    require(type(data["run_id"]) is int and 0 < data["run_id"] < 10**15, "Invalid CI run ID")
    require(data["rollback_compatible"] is True, "Rollback compatibility must be confirmed")
    require(isinstance(data["images"], dict) and set(data["images"]) == set(IMAGES), "Expected four images")
    for digest in data["images"].values():
        require(isinstance(digest, str) and re.fullmatch(r"sha256:[a-f0-9]{64}", digest), "Invalid image digest")
    return data


def read_request():
    # A forced command must not wait forever for a client that never closes stdin.
    deadline = time.monotonic() + 15
    data = bytearray()
    while True:
        ready, _, _ = select.select([sys.stdin.buffer], [], [], max(0, deadline - time.monotonic()))
        require(ready, "Timed out reading request")
        chunk = os.read(sys.stdin.fileno(), 4097 - len(data))
        if not chunk:
            return parse_request(bytes(data))
        data.extend(chunk)
        require(len(data) <= 4096, "Request too large")


def trusted(path, private=False):
    """Every ancestor is root-owned, not a symlink and not writable by others."""
    path = Path(path).absolute()
    for item in (path, *path.parents):
        info = item.lstat()
        require(not stat.S_ISLNK(info.st_mode), "Symlink in deployment path")
        require(info.st_uid == 0 and not info.st_mode & 0o022, "Deployment path is not root-controlled")
    if private:
        require(path.is_file() and path.stat().st_mode & 0o077 == 0, "Secret file must have mode 600")


def atomic_write(path, data, mode=0o600):
    descriptor, name = tempfile.mkstemp(prefix=".pending-", dir=path.parent)
    try:
        with os.fdopen(descriptor, "wb") as stream:
            stream.write(data)
            stream.flush()
            os.fsync(stream.fileno())
        os.chmod(name, mode)
        os.replace(name, path)
        parent = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(parent)
        finally:
            os.close(parent)
    finally:
        if os.path.exists(name):
            os.unlink(name)


def write_json(path, data):
    atomic_write(path, (json.dumps(data, indent=2) + "\n").encode())


def sha256(path):
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.digest()


def retain_assets(stream, directory):
    """Copy immutable regular files only. Never follow archive links."""
    total = 0
    count = 0
    with tarfile.open(fileobj=stream, mode="r|*") as archive:
        for member in archive:
            path = PurePosixPath(member.name)
            require(not path.is_absolute() and ".." not in path.parts, "Unsafe archive path")
            require(member.isdir() or member.isfile(), "Archive links and special files are forbidden")
            if not member.isfile():
                continue
            total += member.size
            count += 1
            require(total <= 256 * 1024 * 1024 and count <= 10000, "Static archive exceeds limits")
            if not (path.parts[0] == "assets" or (len(path.parts) == 1 and re.fullmatch(r"workbox-[a-zA-Z0-9_-]+\.js", path.name))):
                continue
            require(re.fullmatch(r"[a-zA-Z0-9_@+./-]+", str(path)), "Invalid asset filename")
            target = directory.joinpath(*path.parts)
            for ancestor in (target.parent, *target.parent.parents):
                require(not ancestor.is_symlink(), "Asset directory symlink")
            target.parent.mkdir(parents=True, exist_ok=True, mode=0o755)
            for parent in (target.parent, *target.parent.parents):
                if parent == directory.parent:
                    break
                os.chmod(parent, 0o755)
            require(not target.is_symlink(), "Asset symlink")
            with tempfile.NamedTemporaryFile(dir=target.parent, delete=False) as output:
                temporary = Path(output.name)
                shutil.copyfileobj(archive.extractfile(member), output)
            try:
                if target.exists():
                    require(sha256(target) == sha256(temporary), "Immutable asset collision")
                else:
                    os.chmod(temporary, 0o644)
                    os.replace(temporary, target)
            finally:
                temporary.unlink(missing_ok=True)
    require(count > 0, "Empty static archive")


class Deployment:
    def __init__(self, root, config, prefix="posostavu"):
        self.root = root
        self.files = root / "deploy"
        self.config = config
        self.prefix = prefix
        self.state_path = self.files / "state.json"
        self.journal = self.files / "in-progress.json"
        self.assets = root / "assets"
        self.route = Path(config["traefik_directory"]) / f"{prefix}.yml"
        self.environment = {
            "PATH": "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
            "HOME": "/root", "LANG": "C.UTF-8",
            "DOCKER_CONFIG": str(self.files / "docker-client"),
            "COMPOSE_DISABLE_ENV_FILE": "1",
        }

    def run(self, *args, env=None, timeout=300):
        result = subprocess.run(["/usr/bin/docker", *args], env=env or self.environment,
                                stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=timeout)
        # Docker/Compose errors can echo configuration. Do not forward them to Actions.
        require(result.returncode == 0, f"Docker operation failed: {args[0]}")
        return result.stdout.decode().strip()

    def image(self, release, name):
        return REGISTRY + name + "@" + release["images"][name]

    def compose_args(self, color=None):
        kind = "application" if color else "database"
        return ["compose", "--project-name", f"{self.prefix}-{color or 'db'}",
                "--file", str(self.files / f"compose.{kind}.yaml")]

    def env(self, release, color=None):
        env = dict(self.environment, DEPLOY_SECRETS=str(self.root / "secrets"),
                   DATABASE_NETWORK=f"{self.prefix}-database", EDGE_NETWORK=self.config["edge_network"],
                   ASSET_DIRECTORY=str(self.assets), RELEASE_SHA=release["sha"],
                   GATEWAY_ALIAS=f"{self.prefix}-{color}-gateway")
        env.update({name.upper() + "_IMAGE": self.image(release, name) for name in IMAGES})
        return env

    def compose(self, release, color, *args, timeout=300):
        return self.run(*self.compose_args(color), *args, env=self.env(release, color), timeout=timeout)

    def preflight(self, first):
        memory = dict(line.split(":", 1) for line in Path("/proc/meminfo").read_text().splitlines())
        available_mib = int(memory["MemAvailable"].split()[0]) // 1024
        required = 512 + self.config["reserve_memory_mib"] + (512 if first else 0)
        require(available_mib >= required, "Insufficient available memory for both colors")
        reserve = self.config["reserve_disk_mib"] * 1024 * 1024
        for location in (self.root, Path("/var/lib/docker")):
            require(shutil.disk_usage(location).free >= reserve, "Insufficient free disk space")
        self.run("network", "inspect", self.config["edge_network"])

    def pull(self, release):
        for name in IMAGES:
            image = self.image(release, name)
            self.run("pull", image, timeout=300)
            info = json.loads(self.run("image", "inspect", image))[0]
            require(info["Os"] == "linux" and info["Architecture"] == "amd64", "Unsupported image platform")
            labels = info["Config"].get("Labels") or {}
            require(labels.get("org.opencontainers.image.revision") == release["sha"], "Image revision mismatch")
            require(labels.get("org.opencontainers.image.source") == SOURCE, "Image source mismatch")

    def extract_assets(self, release):
        container = self.run("create", "--network", "none", self.image(release, "pwa"))
        try:
            process = subprocess.Popen(["/usr/bin/docker", "cp", container + ":/srv/.", "-"],
                                       env=self.environment, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
            try:
                retain_assets(process.stdout, self.assets)
                require(process.wait(timeout=30) == 0, "Cannot extract PWA assets")
            finally:
                process.stdout.close()
                if process.poll() is None:
                    process.kill()
                    process.wait()
        finally:
            self.run("rm", container)

    def backup(self, database_release, sha):
        size = int(self.compose(database_release, None, "exec", "-T", "--user", "postgres", "postgres",
                                "psql", "-d", "posostavu", "-Atc", "SELECT pg_database_size('posostavu')"))
        free = shutil.disk_usage(self.root).free
        require(free > size * 2 + self.config["reserve_disk_mib"] * 1024 * 1024, "Insufficient space for backup")
        target = self.root / "backups" / f"{time.time_ns()}-{sha}.dump"
        args = self.compose_args() + ["exec", "-T", "--user", "postgres", "postgres", "pg_dump", "-Fc", "-d", "posostavu"]
        with target.open("xb") as stream:
            os.chmod(target, 0o600)
            result = subprocess.run(["/usr/bin/docker", *args], env=self.env(database_release),
                                    stdout=stream, stderr=subprocess.DEVNULL, timeout=180)
        require(result.returncode == 0 and target.stat().st_size > 0, "Database backup failed")
        args = self.compose_args() + ["exec", "-T", "--user", "postgres", "postgres", "pg_restore", "--list"]
        with target.open("rb") as stream:
            result = subprocess.run(["/usr/bin/docker", *args], env=self.env(database_release), stdin=stream,
                                    stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=30)
        require(result.returncode == 0, "Backup archive validation failed")

    def inspect_candidate(self, release, color):
        for path in ("/healthz", "/api/readyz", "/", "/manifest.webmanifest", "/sw.js"):
            self.compose(release, color, "exec", "-T", "gateway", "wget", "-q", "-O", "/dev/null",
                         "http://127.0.0.1:8080" + path, timeout=30)

    def route_document(self, color):
        name = self.prefix
        rule = f"Host(`{self.config['domain']}`)"
        return {"http": {
            "routers": {
                name: {"rule": rule, "entryPoints": [self.config["https_entrypoint"]],
                       "service": name, "tls": {"certResolver": self.config["cert_resolver"]}},
                name + "-http": {"rule": rule, "entryPoints": [self.config["http_entrypoint"]],
                                 "service": name, "middlewares": [name + "-https"]},
            },
            "middlewares": {name + "-https": {"redirectScheme": {"scheme": "https", "permanent": True}}},
            "services": {name: {"loadBalancer": {"servers": [
                {"url": f"http://{name}-{color}-gateway:8080"}
            ]}}},
        }}

    def public_check(self, release):
        context = ssl.create_default_context()
        for path in ("/healthz", "/api/readyz", "/"):
            request = urllib.request.Request(f"https://{self.config['domain']}{path}",
                                             headers={"Cache-Control": "no-cache"})
            with urllib.request.urlopen(request, timeout=10, context=context) as response:
                require(response.status == 200, "Public healthcheck failed")
                if path == "/healthz":
                    require(response.headers.get("X-Release-Sha") == release["sha"], "Route has not switched to expected release")

    def observe(self, release):
        # Allow the watched Traefik configuration to propagate first.
        deadline = time.monotonic() + 30
        while True:
            try:
                self.public_check(release)
                break
            except (OSError, ValueError):
                if time.monotonic() >= deadline:
                    raise
                time.sleep(2)
        deadline = time.monotonic() + self.config["observe_seconds"]
        while time.monotonic() < deadline:
            time.sleep(5)
            self.public_check(release)

    def switch(self, release, color):
        previous = self.route.read_bytes() if self.route.exists() else None
        try:
            atomic_write(self.route, json.dumps(self.route_document(color)).encode(), 0o644)
            self.observe(release)
        except BaseException:
            if previous is None:
                self.route.unlink(missing_ok=True)
            else:
                atomic_write(self.route, previous, 0o644)
            raise

    def execute(self, request):
        require(not self.journal.exists(), "Unfinished deployment requires administrator review")
        state = json.loads(self.state_path.read_text()) if self.state_path.exists() else None
        if request.get("operation") == "rollback":
            require(state and state.get("previous"), "No previous release")
            require(state["release"]["sha"] == request["expected_sha"], "Active release changed")
            release = state["previous"]["release"]
            color = state["previous"]["color"]
            rollback = True
        else:
            release = request
            rollback = False
            if state and state["release"] == release:
                print("Release already active")
                return
            require(not state or release["run_id"] > state["highest_run_id"], "Stale release rejected")
            color = "green" if state and state["color"] == "blue" else "blue"
        self.preflight(state is None)
        for directory, mode in ((self.assets, 0o755), (self.root / "backups", 0o700),
                                (self.files / "docker-client", 0o700)):
            directory.mkdir(mode=mode, exist_ok=True)
            os.chmod(directory, mode)
        write_json(self.journal, {"release": release, "color": color, "phase": "preparation"})
        migration_started = False
        switch_started = False
        committed = False
        previous_route = self.route.read_bytes() if self.route.exists() else None
        try:
            self.pull(release)
            database = state["database"] if state else release
            if not state:
                self.compose(database, None, "up", "-d", "--no-build", "--wait", "--wait-timeout", "120", "postgres")
            else:
                # Never recreate a running shared database as part of an app release.
                self.compose(database, None, "exec", "-T", "postgres", "pg_isready", "-U", "postgres", "-d", "posostavu")
            self.compose(release, color, "stop", "gateway", "api")
            if not rollback:
                self.backup(database, release["sha"])
                migration_started = True
                write_json(self.journal, {"release": release, "color": color, "phase": "migration"})
                self.compose(release, color, "run", "--rm", "--no-deps", "migrate", "up", timeout=180)
            self.extract_assets(release)
            self.compose(release, color, "up", "-d", "--no-build", "--wait", "--wait-timeout", "120", "api", "gateway")
            self.inspect_candidate(release, color)
            write_json(self.journal, {"release": release, "color": color, "phase": "switch"})
            switch_started = True
            self.switch(release, color)
            new_state = dict(release=release, color=color, database=database,
                             highest_run_id=max(release["run_id"], state["highest_run_id"] if state else 0),
                             previous={"release": state["release"], "color": state["color"]} if state else None)
            write_json(self.state_path, new_state)
            committed = True
            self.journal.unlink()
        except BaseException:
            if switch_started and not committed:
                if previous_route is None:
                    self.route.unlink(missing_ok=True)
                else:
                    atomic_write(self.route, previous_route, 0o644)
            if switch_started and not committed and state:
                # switch() restores the previous route before propagating an error.
                # Keep the journal even if the restored route is healthy.
                try:
                    self.public_check(state["release"])
                except (OSError, ValueError):
                    print("Restored route needs administrator verification", file=sys.stderr)
            if not migration_started and not rollback:
                self.journal.unlink(missing_ok=True)
            raise
        print(json.dumps({"sha": release["sha"], "color": color, "status": "active"}))
        if state:
            # Observe includes the drain window. Compose gives remaining requests 30s.
            self.compose(state["release"], state["color"], "stop", "gateway", "api")


def load_config(root):
    files = root / "deploy"
    for name in ("config.json", "compose.database.yaml", "compose.application.yaml", "Caddyfile", "init.sh"):
        trusted(files / name)
    secret_values = {}
    for name in ("postgres", "api", "migrate"):
        path = root / "secrets" / (name + ".env")
        trusted(path, private=True)
        values = dict(line.split("=", 1) for line in path.read_text().splitlines()
                      if line and not line.startswith("#"))
        expected = {"POSTGRES_PASSWORD", "APP_DB_PASSWORD", "MIGRATOR_DB_PASSWORD"} if name == "postgres" else {"DB_PASSWORD"}
        require(set(values) == expected and all(len(value) >= 24 for value in values.values()), "Missing or weak database secret")
        secret_values[name] = values
    passwords = secret_values["postgres"]
    require(len(set(passwords.values())) == 3, "Database roles must have different passwords")
    require(secret_values["api"]["DB_PASSWORD"] == passwords["APP_DB_PASSWORD"], "API database secret mismatch")
    require(secret_values["migrate"]["DB_PASSWORD"] == passwords["MIGRATOR_DB_PASSWORD"], "Migration database secret mismatch")
    config = json.loads((files / "config.json").read_text(), object_pairs_hook=unique_object)
    require(config.get("enabled") is True and config.get("recovery_verified") is True, "Production has not been enabled and recovery verified")
    require(re.fullmatch(r"[a-z0-9]+(?:[.-][a-z0-9]+)*", config["domain"]), "Invalid domain")
    for key in ("edge_network", "http_entrypoint", "https_entrypoint", "cert_resolver"):
        require(re.fullmatch(r"[a-zA-Z0-9_-]+", config[key]) and not config[key].startswith("REPLACE"), "Configure Traefik names first")
    for key, low, high in (("observe_seconds", 30, 600), ("reserve_memory_mib", 128, 8192), ("reserve_disk_mib", 1024, 102400)):
        require(type(config[key]) is int and low <= config[key] <= high, "Invalid resource or observation limit")
    trusted(Path(config["traefik_directory"]))
    for path in (root / "assets", root / "backups", files / "docker-client", files / "state.json", files / "in-progress.json"):
        if path.exists() or path.is_symlink():
            trusted(path)
    return config


def main():
    import fcntl

    require(os.geteuid() == 0, "Run through the root-owned forced command")
    require(len(sys.argv) == 1, "Command arguments are forbidden")
    os.umask(0o077)
    request = read_request()
    config = load_config(ROOT)
    lock_path = ROOT / "deploy" / "deployment.lock"
    if lock_path.exists() or lock_path.is_symlink():
        trusted(lock_path)
    with lock_path.open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        Deployment(ROOT, config).execute(request)


if __name__ == "__main__":
    try:
        main()
    except (ValueError, OSError, subprocess.SubprocessError):
        # Do not leak environment values or Docker output to the SSH client.
        print("Deployment stopped. Check root-owned state and server logs before retrying.", file=sys.stderr)
        sys.exit(1)
