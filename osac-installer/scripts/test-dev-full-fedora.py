#!/usr/bin/env python3
"""Mac guest-image build/load and development catalog contracts (fake commands)."""

import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


INSTALLER = Path(__file__).resolve().parents[1]
BUILD = INSTALLER / "scripts/dev-full/build-fedora-image.sh"
SEED = INSTALLER / "charts/osac-devstack/files/seed-catalog-simple.sh"
IMAGE = "localhost/osac-dev/fedora-arm64:pci-nomsi-v1"


class FedoraImageTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name)
        self.log = self.directory / "calls.jsonl"
        self.env = {**os.environ, "CALLS": str(self.log), "TMPDIR": str(self.directory)}

    def command(self, name, code):
        path = self.directory / name
        path.write_text(f"#!{shutil.which('python3')}\n" + code)
        path.chmod(0o755)
        return path

    def calls(self):
        return [json.loads(line) for line in self.log.read_text().splitlines()]

    def build(self, failure=""):
        runtime = self.command("runtime", '''
import json, os, pathlib, sys
args = sys.argv[1:]
with open(os.environ["CALLS"], "a") as stream:
    stream.write(json.dumps(args) + "\\n")
if args[0] == os.environ.get("FAIL_COMMAND") or (
    args[:2] == ["container", "save"] and os.environ.get("FAIL_COMMAND") == "save"
):
    sys.exit(17)
if args[:2] == ["container", "save"]:
    pathlib.Path(args[args.index("-o") + 1]).write_bytes(b"image archive")
if args[0] == "load":
    assert pathlib.Path(args[2]).read_bytes() == b"image archive"
''')
        return subprocess.run(
            ["bash", str(BUILD), "test-cluster", IMAGE],
            env={**self.env, "KIND_RUNTIME": str(runtime), "FAIL_COMMAND": failure},
            capture_output=True, text=True, timeout=10,
        )

    def test_native_build_precedes_archive_load_and_cleans_up(self):
        result = self.build()
        self.assertEqual(result.returncode, 0, result.stderr)
        calls = self.calls()
        self.assertEqual(calls[0], [
            "container-build-file", str(BUILD.with_name("Containerfile.fedora-arm64")),
            "--platform", "linux/arm64", "-t", IMAGE,
        ])
        self.assertEqual(calls[1][:3], ["container", "save", IMAGE])
        archive = calls[1][-1]
        self.assertEqual(calls[2], ["load", "image-archive", archive, "--name", "test-cluster"])
        self.assertFalse(Path(archive).exists())

    def test_failed_steps_never_continue_or_leave_an_archive(self):
        for failure, count in (("container-build-file", 1), ("save", 2), ("load", 3)):
            with self.subTest(failure=failure):
                self.log.unlink(missing_ok=True)
                result = self.build(failure)
                self.assertEqual(result.returncode, 17, result.stderr)
                self.assertEqual(len(self.calls()), count)
                self.assertEqual(list(self.directory.glob("osac-fedora-image-*")), [])

    def seed(self, existing=False, mac=False, failure=""):
        auth = self.directory / "auth.sh"
        auth.write_text("get_fulfillment_access_token() { echo test-token; }\n")
        script = self.directory / "seed.sh"
        script.write_text(SEED.read_text().replace(
            "source /scripts/fulfillment-auth.sh", f"source '{auth}'"
        ))
        self.command("kubectl", "import time\ntime.sleep(30)\n")
        self.command("curl", '''
import json, os, sys
args = sys.argv[1:]
method = args[args.index("-X") + 1] if "-X" in args else "GET"
payload = json.loads(args[args.index("-d") + 1]) if "-d" in args else None
url = next(arg for arg in args if arg.startswith("https://"))
with open(os.environ["CALLS"], "a") as stream:
    stream.write(json.dumps({"method": method, "payload": payload, "url": url}) + "\\n")
status = "200"
body = {"id": "created"}
if "--get" in args:
    body = {"items": [] if os.environ.get("FAIL_SEED") == "lookup" else [
        {"id": "catalog-123", "metadata": {"name": "linux-vm", "version": "9"}}
    ]}
    if os.environ.get("FAIL_SEED") == "zero-version":
        body["items"][0]["metadata"].pop("version")
elif method == "POST" and os.environ.get("EXISTING") == "1":
    status, body = "409", {"code": 6}
if os.environ.get("FAIL_SEED") == "create" and method == "POST":
    status, body = "500", {"code": 13}
if os.environ.get("FAIL_SEED") == "patch" and method == "PATCH":
    sys.exit(22)
if "-o" in args and args[args.index("-o") + 1] != "/dev/null":
    with open(args[args.index("-o") + 1], "w") as stream:
        json.dump(body, stream)
if "-w" in args:
    print(status, end="")
''')
        env = {
            **self.env, "PATH": f"{self.directory}:{os.environ['PATH']}",
            "EXISTING": "1" if existing else "0", "FAIL_SEED": failure,
        }
        if mac:
            env.update({
                "DEV_FEDORA_IMAGE_NAME": "fedora-mac-arm64",
                "DEV_FEDORA_IMAGE_SOURCE": IMAGE,
                "DEV_FEDORA_IMAGE_ARCHITECTURES": '["ARCHITECTURE_ARM64"]',
            })
        return subprocess.run(["bash", str(script), "osac"], env=env,
                              capture_output=True, text=True, timeout=10)

    def test_fresh_catalog_uses_selected_image_in_every_reference(self):
        for mac in (False, True):
            with self.subTest(mac=mac):
                self.log.unlink(missing_ok=True)
                result = self.seed(mac=mac)
                self.assertEqual(result.returncode, 0, result.stderr)
                posts = {call["url"].rsplit("/", 1)[-1]: call["payload"]
                         for call in self.calls() if call["method"] == "POST"}
                name = "fedora-mac-arm64" if mac else "fedora"
                disk = posts["disk_images"]
                self.assertEqual(disk["metadata"], {"name": name, "tenant": "shared"})
                self.assertEqual(disk["spec"]["source_ref"], IMAGE if mac else "quay.io/containerdisks/fedora:latest")
                self.assertEqual(disk["spec"]["architecture"], ["ARCHITECTURE_ARM64"] if mac else ["ARCHITECTURE_AMD64", "ARCHITECTURE_ARM64"])
                self.assertEqual(posts["compute_instance_templates"]["spec_defaults"]["disk_image"], {"name": name})
                self.assertEqual(posts["compute_instance_catalog_items"]["fields"]["disk_image"], {
                    "editable": {"default_value": {"name": name, "shared": True}},
                })

    def test_rerun_updates_only_catalog_disk_default_with_version_lock(self):
        result = self.seed(existing=True, mac=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        patches = [call for call in self.calls() if call["method"] == "PATCH"]
        self.assertEqual(len(patches), 1)
        self.assertTrue(patches[0]["url"].endswith(
            "/compute_instance_catalog_items/catalog-123?update_mask=fields.disk_image&lock=true"
        ))
        self.assertEqual(patches[0]["payload"], {
            "metadata": {"version": "9"},
            "fields": {"disk_image": {"editable": {"default_value": {
                "name": "fedora-mac-arm64", "shared": True,
            }}}},
        })
        self.assertNotIn("test-token", result.stdout + result.stderr)

    def test_failed_create_lookup_or_patch_fails_seeding(self):
        for failure in ("create", "lookup", "patch"):
            with self.subTest(failure=failure):
                result = self.seed(existing=True, mac=True, failure=failure)
                self.assertNotEqual(result.returncode, 0)
                self.assertNotIn("ready to create compute instances", result.stdout)
                self.assertNotIn("test-token", result.stdout + result.stderr)

    def test_protojson_omitted_zero_version_is_preserved_for_locking(self):
        result = self.seed(existing=True, mac=True, failure="zero-version")
        self.assertEqual(result.returncode, 0, result.stderr)
        patch = next(call for call in self.calls() if call["method"] == "PATCH")
        self.assertEqual(patch["payload"]["metadata"]["version"], 0)

    def test_linux_rerun_preserves_existing_catalog_defaults(self):
        result = self.seed(existing=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(any(call["method"] == "PATCH" for call in self.calls()))


if __name__ == "__main__":
    unittest.main(verbosity=2)
