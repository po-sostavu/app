"""Isolated Linux Docker test. No production SSH, GHCR writes or host service edits."""

import json
import os
from pathlib import Path
import secrets
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.request

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "production"))
import deploy


class TestDeployment(deploy.Deployment):
    def image(self, release, name):
        return f"posostavu-{name}:ci"

    def preflight(self, first):
        self.run("network", "inspect", self.config["edge_network"])

    def pull(self, release):
        for name in deploy.IMAGES:
            self.run("image", "inspect", self.image(release, name))

    def route_document(self, color):
        data = super().route_document(color)
        # The fixture checks file-provider switching over loopback HTTP.
        # Real ACME and TLS must be verified separately on the target host.
        del data["http"]["routers"][self.prefix + "-http"]
        del data["http"]["routers"][self.prefix]["tls"]
        return data

    def public_check(self, release):
        for path in ("/healthz", "/api/readyz", "/", "/manifest.webmanifest", "/sw.js"):
            with self.get(path) as response:
                deploy.require(response.status == 200, "Integration route failed")
                if path == "/healthz":
                    deploy.require(response.headers.get("X-Release-Sha") == release["sha"], "Wrong color")

    def get(self, path):
        request = urllib.request.Request(self.base_url + path, headers={"Host": "test.local"})
        return urllib.request.urlopen(request, timeout=10)


def main():
    deploy.require(sys.platform == "linux", "Run integration on an isolated Linux Docker runner")
    repository = Path(__file__).resolve().parents[2]
    prefix = "posostavu-ci-" + secrets.token_hex(4)
    with tempfile.TemporaryDirectory(prefix=prefix) as temporary:
        root = Path(temporary)
        files = root / "deploy"
        files.mkdir()
        secret_dir = root / "secrets"
        secret_dir.mkdir()
        routes = root / "routes"
        routes.mkdir(mode=0o755)
        for name in ("compose.application.yaml", "compose.database.yaml", "Caddyfile"):
            shutil.copyfile(repository / "infra/production" / name, files / name)
        shutil.copyfile(repository / "infra/postgres/init.sh", files / "init.sh")
        passwords = {key: secrets.token_hex(24) + "$literal" for key in ("POSTGRES_PASSWORD", "APP_DB_PASSWORD", "MIGRATOR_DB_PASSWORD")}
        (secret_dir / "postgres.env").write_text("".join(f"{key}={value}\n" for key, value in passwords.items()))
        (secret_dir / "api.env").write_text("DB_PASSWORD=" + passwords["APP_DB_PASSWORD"] + "\n")
        (secret_dir / "migrate.env").write_text("DB_PASSWORD=" + passwords["MIGRATOR_DB_PASSWORD"] + "\n")
        config = dict(traefik_directory=str(routes), edge_network=prefix + "-edge", domain="test.local",
                      https_entrypoint="web", http_entrypoint="unused", cert_resolver="unused",
                      observe_seconds=1, reserve_disk_mib=64)
        engine = TestDeployment(root, config, prefix)
        first = dict(schema=1, sha="1" * 40, run_id=1, rollback_compatible=True,
                     images={name: "sha256:" + "1" * 64 for name in deploy.IMAGES})
        second = dict(first, sha="2" * 40, run_id=2)
        traefik = prefix + "-traefik"
        edge_created = False
        try:
            engine.run("network", "create", config["edge_network"])
            edge_created = True
            engine.run("run", "-d", "--name", traefik, "--network", config["edge_network"],
                       "--publish", "127.0.0.1::8080", "--mount", f"type=bind,source={routes},target=/routes,readonly",
                       os.environ["TRAEFIK_TEST_IMAGE"], "--entrypoints.web.address=:8080",
                       "--providers.file.directory=/routes", "--providers.file.watch=true",
                       "--log.level=ERROR")
            info = json.loads(engine.run("inspect", traefik))[0]
            port = info["NetworkSettings"]["Ports"]["8080/tcp"][0]["HostPort"]
            engine.base_url = "http://127.0.0.1:" + port
            for color in (None, "blue", "green"):
                engine.compose(first, color, "config", "--quiet")
            engine.execute(first)
            database_id = engine.compose(first, None, "ps", "-q", "postgres")
            engine.compose(first, None, "exec", "-T", "--user", "postgres", "postgres", "psql", "-d", "posostavu",
                           "-v", "ON_ERROR_STOP=1", "-c", "CREATE TABLE cd_probe (value text); INSERT INTO cd_probe VALUES ('preserved');")
            assets = sorted(engine.assets.glob("assets/*.js"))
            deploy.require(assets, "Missing JavaScript assets")
            old_asset = "/assets/" + assets[0].name
            with engine.get(old_asset) as response:
                original_asset = response.read()
            engine.execute(second)
            deploy.require(engine.compose(first, None, "ps", "-q", "postgres") == database_id, "Shared database was recreated")
            engine.execute(dict(operation="rollback", expected_sha=second["sha"]))
            engine.public_check(first)
            with engine.get(old_asset) as response:
                deploy.require(response.read() == original_asset, "Old PWA asset disappeared")
            value = engine.compose(first, None, "exec", "-T", "--user", "postgres", "postgres", "psql", "-d", "posostavu", "-Atc", "SELECT value FROM cd_probe")
            deploy.require(value == "preserved", "Database contents changed during rollback")
            deploy.require(len(list((root / "backups").glob("*.dump"))) == 2, "Backup count mismatch")
            print("PASS: migrations, raw secrets, blue/green, Traefik switch, rollback, database and old assets")
        finally:
            # Only resources with this invocation's random project prefix are removed.
            for color in ("blue", "green", None):
                try:
                    engine.compose(first, color, "down", "--volumes", "--remove-orphans")
                except ValueError:
                    pass
            try:
                engine.run("rm", "--force", traefik)
            except ValueError:
                pass
            if edge_created:
                engine.run("network", "rm", config["edge_network"])


if __name__ == "__main__":
    main()
