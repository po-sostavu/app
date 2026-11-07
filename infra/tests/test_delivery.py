import io
import json
from pathlib import Path
import sys
import tarfile
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "production"))
sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "cd"))
import deploy
import release


def manifest(number=1):
    return dict(schema=1, sha=f"{number:040x}", run_id=number,
                images={name: "sha256:" + f"{number:064x}" for name in deploy.IMAGES},
                rollback_compatible=True)


def archive(entries):
    stream = io.BytesIO()
    with tarfile.open(fileobj=stream, mode="w") as output:
        for name, content in entries:
            info = tarfile.TarInfo(name)
            if content is None:
                info.type = tarfile.SYMTYPE
                info.linkname = "/etc/passwd"
                output.addfile(info)
            else:
                info.size = len(content)
                output.addfile(info, io.BytesIO(content))
    stream.seek(0)
    return stream


class InputTests(unittest.TestCase):
    def test_rejects_invalid_requests_before_docker(self):
        invalid = [b"[]", b"{}", b"x" * 4097, b'{"schema":1,"schema":1}']
        for key, value in (("sha", "$(id)"), ("run_id", True), ("images", {}),
                           ("rollback_compatible", False), ("command", "whoami")):
            data = manifest()
            data[key] = value
            invalid.append(json.dumps(data).encode())
        for data in invalid:
            with self.subTest(data=data[:80]), self.assertRaises((ValueError, TypeError)):
                deploy.parse_request(data)

    def test_accepts_release_and_guarded_rollback(self):
        self.assertEqual(deploy.parse_request(json.dumps(manifest())), manifest())
        self.assertEqual(deploy.parse_request(json.dumps(dict(operation="rollback", expected_sha="a" * 40)))["operation"], "rollback")

    def test_artifact_revision_and_checksum_are_checked_before_load(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for name in release.IMAGES:
                folder = root / name
                folder.mkdir()
                (folder / "image.tar.gz").write_bytes(b"invalid")
                (folder / "metadata.json").write_text(json.dumps(dict(
                    image=name, sha="a" * 40, bytes=7, sha256="bad")))
            with patch.object(release, "docker") as docker:
                for sha in ("a" * 40, "b" * 40):
                    with self.assertRaises(ValueError):
                        release.load_images(root, sha)
                docker.assert_not_called()

    def test_existing_tag_cannot_be_replaced(self):
        with patch.object(release, "load_images", return_value={name: "sha256:new" for name in release.IMAGES}), \
                patch.object(release, "registry_manifest", return_value=({"config": {"digest": "sha256:old"}}, "sha256:digest")), \
                patch.object(release, "docker") as docker:
            with self.assertRaisesRegex(ValueError, "another image"):
                release.publish(Path("unused"), "a" * 40, 1, Path("unused"))
            docker.assert_not_called()

    def test_publication_reuses_matching_tags(self):
        with tempfile.TemporaryDirectory() as directory, \
                patch.object(release, "load_images", return_value={name: "sha256:same" for name in release.IMAGES}), \
                patch.object(release, "registry_manifest", return_value=({"config": {"digest": "sha256:same"}}, "sha256:digest")), \
                patch.object(release, "docker") as docker:
            output = Path(directory) / "release.json"
            release.publish(Path("unused"), "a" * 40, 123, output)
            docker.assert_not_called()
            self.assertEqual(set(json.loads(output.read_text())["images"]), set(release.IMAGES))


class AssetsTests(unittest.TestCase):
    def test_preserves_both_releases_without_overwriting_entrypoint(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            deploy.retain_assets(archive([("assets/old.js", b"old"), ("index.html", b"old index")]), root)
            deploy.retain_assets(archive([("assets/new.js", b"new"), ("workbox-123.js", b"worker")]), root)
            self.assertEqual((root / "assets/old.js").read_bytes(), b"old")
            self.assertTrue((root / "assets/new.js").exists())
            self.assertFalse((root / "index.html").exists())
            with self.assertRaisesRegex(ValueError, "collision"):
                deploy.retain_assets(archive([("assets/old.js", b"different")]), root)
            self.assertEqual((root / "assets/old.js").read_bytes(), b"old")

    def test_rejects_traversal_and_links(self):
        for name, value in [("../escape", b"x"), ("/absolute", b"x"), ("assets/link", None)]:
            with self.subTest(name=name), tempfile.TemporaryDirectory() as directory:
                with self.assertRaises(ValueError):
                    deploy.retain_assets(archive([(name, value)]), Path(directory))


class FakeDeployment(deploy.Deployment):
    def __init__(self, root):
        (root / "deploy").mkdir()
        (root / "routes").mkdir()
        super().__init__(root, dict(traefik_directory=str(root / "routes"), domain="example.test",
                                   https_entrypoint="https", http_entrypoint="http", cert_resolver="test"))
        self.operations = []
        self.failure = None

    def preflight(self, first):
        self.operations.append("preflight")
        if self.failure == "resources":
            raise ValueError("Insufficient resources")

    def pull(self, request):
        self.operations.append("pull")

    def backup(self, database, sha):
        self.operations.append("backup")

    def extract_assets(self, release):
        self.operations.append("assets")

    def inspect_candidate(self, release, color):
        self.operations.append("candidate")
        if self.failure == "candidate":
            raise ValueError("Candidate failed")

    def observe(self, release):
        self.operations.append("public")
        if self.failure == "public":
            raise ValueError("Public check failed")

    def public_check(self, release):
        self.operations.append("restored-check")

    def compose(self, release, color, *args, **kwargs):
        self.operations.append((color, args))
        if "migrate" in args and self.failure == "migration":
            raise ValueError("Migration failed")
        return ""


@unittest.skipUnless(sys.platform == "linux", "Atomic directory fsync is tested on Linux CI")
class DeploymentTests(unittest.TestCase):
    def test_failed_state_commit_restores_route(self):
        with tempfile.TemporaryDirectory() as directory:
            engine = FakeDeployment(Path(directory))
            engine.execute(manifest())
            route = engine.route.read_bytes()
            original_write = deploy.write_json

            def fail_state(path, data):
                if path == engine.state_path:
                    raise OSError("Disk full")
                original_write(path, data)

            with patch.object(deploy, "write_json", side_effect=fail_state):
                with self.assertRaises(OSError):
                    engine.execute(manifest(2))
            self.assertEqual(engine.route.read_bytes(), route)
            self.assertTrue(engine.journal.exists())

    def test_two_colors_idempotence_rollback_and_shared_database(self):
        with tempfile.TemporaryDirectory() as directory:
            engine = FakeDeployment(Path(directory))
            engine.execute(manifest())
            engine.execute(manifest(2))
            state = json.loads(engine.state_path.read_text())
            self.assertEqual(state["color"], "green")
            self.assertEqual(state["database"], manifest())
            operations = list(engine.operations)
            engine.execute(manifest(2))
            self.assertEqual(operations, engine.operations)
            engine.execute(dict(operation="rollback", expected_sha=manifest(2)["sha"]))
            state = json.loads(engine.state_path.read_text())
            self.assertEqual(state["color"], "blue")
            self.assertEqual(state["highest_run_id"], 2)
            self.assertEqual(sum(isinstance(op, tuple) and "migrate" in op[1] for op in engine.operations), 2)
            self.assertFalse(any(isinstance(op, tuple) and "down" in op[1] for op in engine.operations))
            with self.assertRaisesRegex(ValueError, "Stale"):
                engine.execute(manifest(2))

    def test_failures_preserve_route_and_database(self):
        for failure in ("resources", "migration", "candidate", "public"):
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as directory:
                engine = FakeDeployment(Path(directory))
                engine.execute(manifest())
                route = engine.route.read_bytes()
                state = engine.state_path.read_bytes()
                engine.failure = failure
                with self.assertRaises(ValueError):
                    engine.execute(manifest(2))
                self.assertEqual(route, engine.route.read_bytes())
                self.assertEqual(state, engine.state_path.read_bytes())
                if failure != "resources":
                    with self.assertRaisesRegex(ValueError, "Unfinished"):
                        engine.execute(manifest(3))

    def test_deployment_lock_rejects_second_owner(self):
        import fcntl
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "lock"
            with path.open("a") as first, path.open("a") as second:
                fcntl.flock(first, fcntl.LOCK_EX | fcntl.LOCK_NB)
                with self.assertRaises(BlockingIOError):
                    fcntl.flock(second, fcntl.LOCK_EX | fcntl.LOCK_NB)


if __name__ == "__main__":
    unittest.main()
