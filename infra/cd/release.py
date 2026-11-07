"""Transfer CI images without rebuilding. Only standard-library dependencies."""

import argparse
import base64
import gzip
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request

IMAGES = ("pwa", "api", "migrate", "postgres")
SOURCE = "https://github.com/po-sostavu/app"
REGISTRY = "ghcr.io/po-sostavu/app-"
MAX_ARCHIVE = 512 * 1024 * 1024


def require(condition, message):
    if not condition:
        raise ValueError(message)


def docker(*args):
    return subprocess.check_output(["docker", *args], text=True).strip()


def checksum(path):
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def validate_sha(sha):
    require(re.fullmatch(r"[a-f0-9]{40}", sha), "Invalid commit SHA")


def export_image(name, sha, directory):
    validate_sha(sha)
    directory.mkdir(parents=True, exist_ok=False)
    tag = f"posostavu-{name}:ci"
    info = json.loads(docker("image", "inspect", tag))[0]
    validate_image(info, sha)
    started = time.monotonic()
    archive = directory / "image.tar.gz"
    with archive.open("wb") as output:
        with gzip.GzipFile(fileobj=output, mode="wb", compresslevel=1, mtime=0) as compressed:
            process = subprocess.Popen(["docker", "image", "save", tag], stdout=subprocess.PIPE)
            try:
                shutil.copyfileobj(process.stdout, compressed)
            finally:
                process.stdout.close()
            require(process.wait() == 0, "Docker export failed")
    require(archive.stat().st_size <= MAX_ARCHIVE, "Image artifact exceeds 512 MiB")
    metadata = dict(image=name, sha=sha, image_id=info["Id"],
                    sha256=checksum(archive), bytes=archive.stat().st_size,
                    export_seconds=round(time.monotonic() - started, 2))
    (directory / "metadata.json").write_text(json.dumps(metadata) + "\n", encoding="utf-8")
    print(json.dumps(metadata))


def validate_image(info, sha):
    require(info["Os"] == "linux" and info["Architecture"] == "amd64", "Wrong image platform")
    labels = info["Config"].get("Labels") or {}
    require(labels.get("org.opencontainers.image.revision") == sha, "Image revision mismatch")
    require(labels.get("org.opencontainers.image.source") == SOURCE, "Image source mismatch")


def load_images(directory, sha):
    validate_sha(sha)
    files = list(directory.glob("*/metadata.json"))
    require(len(files) == 4, "Expected four image artifacts")
    result = {}
    for metadata_path in files:
        data = json.loads(metadata_path.read_text(encoding="utf-8"))
        name = data["image"]
        require(name in IMAGES and name not in result, "Unexpected or duplicate image")
        require(data["sha"] == sha, "Artifact revision mismatch")
        archive = metadata_path.parent / "image.tar.gz"
        require(0 < archive.stat().st_size == data["bytes"] <= MAX_ARCHIVE, "Invalid archive size")
        require(checksum(archive) == data["sha256"], "Archive checksum mismatch")
        docker("image", "load", "--input", str(archive))
        info = json.loads(docker("image", "inspect", f"posostavu-{name}:ci"))[0]
        require(info["Id"] == data["image_id"], "Image digest mismatch")
        validate_image(info, sha)
        result[name] = info["Id"]
    return result


def registry_manifest(name, reference, authenticated=False):
    """Return the manifest and its digest. A 404 is distinct from access failure."""
    require(name in IMAGES and re.fullmatch(r"(?:[a-f0-9]{40}|sha256:[a-f0-9]{64})", reference),
            "Invalid registry reference")
    repository = f"po-sostavu/app-{name}"
    query = urllib.parse.urlencode(dict(service="ghcr.io", scope=f"repository:{repository}:pull"))
    request = urllib.request.Request(f"https://ghcr.io/token?{query}")
    if authenticated:
        credentials = f"{os.environ['GITHUB_ACTOR']}:{os.environ['GHCR_TOKEN']}".encode()
        request.add_header("Authorization", "Basic " + base64.b64encode(credentials).decode())
    with urllib.request.urlopen(request, timeout=30) as response:
        token = json.load(response)["token"]
    request = urllib.request.Request(f"https://ghcr.io/v2/{repository}/manifests/{reference}", headers={
        "Authorization": "Bearer " + token,
        "Accept": "application/vnd.oci.image.index.v1+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json, application/vnd.docker.distribution.manifest.list.v2+json",
    })
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            raw = response.read(1024 * 1024 + 1)
            require(len(raw) <= 1024 * 1024, "Registry manifest too large")
            digest = "sha256:" + hashlib.sha256(raw).hexdigest()
            require(response.headers.get("Docker-Content-Digest") == digest, "Registry digest mismatch")
            return json.loads(raw), digest
    except urllib.error.HTTPError as error:
        if error.code == 404:
            return None
        raise


def image_config(name, manifest, authenticated):
    if "config" in manifest:
        return manifest["config"]["digest"]
    variants = [item for item in manifest.get("manifests", [])
                if item.get("platform", {}).get("os") == "linux"
                and item.get("platform", {}).get("architecture") == "amd64"]
    require(len(variants) == 1, "Expected one linux/amd64 image")
    child = registry_manifest(name, variants[0]["digest"], authenticated)
    require(child is not None, "Missing platform manifest")
    return child[0]["config"]["digest"]


def publish(directory, sha, run_id, output):
    policy = json.loads(Path("infra/production/release-policy.json").read_text(encoding="utf-8"))
    require(policy.get("rollback_compatible") is True, "Automatic deployment requires a compatible rollback")
    loaded = load_images(directory, sha)
    # Check every tag before pushing anything. Never replace a published commit.
    existing = {name: registry_manifest(name, sha, True) for name in IMAGES}
    for name, manifest in existing.items():
        if manifest:
            require(image_config(name, manifest[0], True) == loaded[name], "Commit tag already has another image")
    images = {}
    for name in IMAGES:
        if not existing[name]:
            tag = f"{REGISTRY}{name}:{sha}"
            docker("tag", loaded[name], tag)
            docker("push", tag)
        manifest = registry_manifest(name, sha, True)
        require(manifest is not None, "Published manifest missing")
        require(image_config(name, manifest[0], True) == loaded[name], "Published image changed")
        images[name] = manifest[1]
    output.write_text(json.dumps(dict(schema=1, sha=sha, run_id=int(run_id), images=images,
                                     rollback_compatible=True), indent=2) + "\n", encoding="utf-8")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    export = commands.add_parser("export")
    export.add_argument("image", choices=IMAGES)
    export.add_argument("sha")
    export.add_argument("directory", type=Path)
    for command in ("load", "publish"):
        sub = commands.add_parser(command)
        sub.add_argument("directory", type=Path)
        sub.add_argument("sha")
        if command == "publish":
            sub.add_argument("run_id", type=int)
            sub.add_argument("output", type=Path)
    public = commands.add_parser("check-public")
    public.add_argument("release", type=Path)
    args = parser.parse_args()
    if args.command == "export":
        export_image(args.image, args.sha, args.directory)
    elif args.command == "load":
        print(json.dumps(load_images(args.directory, args.sha)))
    elif args.command == "publish":
        publish(args.directory, args.sha, args.run_id, args.output)
    else:
        data = json.loads(args.release.read_text(encoding="utf-8"))
        for name in IMAGES:
            require(registry_manifest(name, data["images"][name]) is not None,
                    "Image is not publicly available")


if __name__ == "__main__":
    main()
