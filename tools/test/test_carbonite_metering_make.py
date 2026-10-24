"""Verify fixed host-build dispatch for the metering image Make targets."""

import json
import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path


REPO = Path(__file__).resolve().parents[2]
SERVICE = "osac-metering/metering-service"
ADAPTERS = "osac-metering/adapters"

STUB = '''#!/usr/bin/env python3
import json
import os
import sys
from pathlib import Path
with Path(os.environ["CALL_LOG"]).open("a") as log:
    log.write(json.dumps([Path(sys.argv[0]).name, os.getcwd(), *sys.argv[1:]]) + "\\n")
'''


class MeteringImageDispatchTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="osac-metering-build-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        subprocess.run(["git", "init", "-q", str(self.root)], check=True)
        for component in (SERVICE, ADAPTERS):
            path = self.root / component
            path.mkdir(parents=True)
            shutil.copyfile(REPO / component / "Makefile", path / "Makefile")
        shared = self.root / "tools"
        shared.mkdir()
        shutil.copyfile(REPO / "tools/golangci-lint.mk", shared / "golangci-lint.mk")
        kind_load = self.root / "osac-installer/scripts/dev-full/kind-load-image.mk"
        kind_load.parent.mkdir(parents=True)
        shutil.copyfile(REPO / "osac-installer/scripts/dev-full/kind-load-image.mk", kind_load)
        self.bin = self.root / "bin"
        self.bin.mkdir()
        for engine in ("podman", "docker"):
            script = self.bin / engine
            script.write_text(STUB)
            script.chmod(0o755)
        self.log = self.root / "calls.jsonl"

    def helper(self):
        path = self.root / "tools/carbonite/host-build"
        path.parent.mkdir(exist_ok=True)
        path.write_text(STUB)
        path.chmod(0o755)
        return path

    def make(self, component, target, *variables, openshell=False, project_dir=False, extra_env=None):
        env = os.environ.copy()
        env.pop("OPENSHELL_SANDBOX", None)
        env.pop("PROJECT_DIR", None)
        if openshell:
            env["OPENSHELL_SANDBOX"] = "osac-carbonite"
        if project_dir:
            env["PROJECT_DIR"] = "/sandbox/osac"
        env["CALL_LOG"] = str(self.log)
        env["PATH"] = f"{self.bin}:{env['PATH']}"
        env.update(extra_env or {})
        return subprocess.run(
            ["make", "--no-print-directory", "-C", str(self.root / component), target, *variables],
            env=env, text=True, capture_output=True, check=False,
        )

    def calls(self):
        if not self.log.exists():
            return []
        return [json.loads(line) for line in self.log.read_text().splitlines()]

    def test_sandbox_builds_use_only_literal_helper_actions_from_component_cwd(self):
        self.helper()
        for component, target, action in (
            (SERVICE, "image-build", "metering-service"),
            (ADAPTERS, "image-build-m360-adapter", "metering-m360"),
            (ADAPTERS, "image-build-echo-adapter", "metering-echo"),
        ):
            with self.subTest(target=target):
                self.log.unlink(missing_ok=True)
                result = self.make(component, target, openshell=True, project_dir=True)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(self.calls(), [["host-build", str(self.root / component), action]])

    def test_sandbox_builds_fail_before_podman_if_helper_absent(self):
        for component, target in (
            (SERVICE, "image-build"),
            (ADAPTERS, "image-build-m360-adapter"),
            (ADAPTERS, "image-build-echo-adapter"),
        ):
            with self.subTest(target=target):
                result = self.make(component, target, openshell=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("host-build", result.stderr)
                self.assertEqual(self.calls(), [])

    def test_sandbox_rejects_image_and_engine_overrides_before_dispatch(self):
        self.helper()
        for component, target, overrides in (
            (SERVICE, "image-build", ("IMG=evil", "CONTAINER_TOOL=docker")),
            (ADAPTERS, "image-build-m360-adapter", ("IMG_M360_ADAPTER=evil", "CONTAINER_TOOL=docker")),
            (ADAPTERS, "image-build-echo-adapter", ("IMG_ECHO_ADAPTER=evil", "CONTAINER_TOOL=docker")),
        ):
            for override in overrides:
                with self.subTest(target=target, override=override):
                    result = self.make(component, target, override, openshell=True)
                    self.assertNotEqual(result.returncode, 0)
                    self.assertIn("override", result.stderr)
                    self.assertEqual(self.calls(), [])

        result = self.make(SERVICE, "image-build", openshell=True, extra_env={"IMG": "evil"})
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.calls(), [])

    def test_host_and_distrobox_keep_direct_engine_recipes(self):
        cases = (
            (SERVICE, "image-build", "ghcr.io/osac-project/metering-service:latest", "Containerfile", "../.."),
            (ADAPTERS, "image-build-m360-adapter", "ghcr.io/osac-project/metering-m360-adapter:latest", "Containerfile.m360-adapter", "../.."),
            (ADAPTERS, "image-build-echo-adapter", "ghcr.io/osac-project/metering-echo-adapter:latest", "Containerfile.echo-adapter", "../.."),
        )
        for component, target, tag, containerfile, context in cases:
            for project_dir in (False, True):
                with self.subTest(target=target, project_dir=project_dir):
                    self.log.unlink(missing_ok=True)
                    result = self.make(component, target, project_dir=project_dir)
                    self.assertEqual(result.returncode, 0, result.stderr)
                    self.assertEqual(self.calls(), [["podman", str(self.root / component),
                                                     "build", "-t", tag, "-f", containerfile, context]])

    def test_push_targets_remain_direct_engine_commands(self):
        self.helper()
        for target, tag in (
            ("image-push-m360-adapter", "ghcr.io/osac-project/metering-m360-adapter:latest"),
            ("image-push-echo-adapter", "ghcr.io/osac-project/metering-echo-adapter:latest"),
        ):
            with self.subTest(target=target):
                self.log.unlink(missing_ok=True)
                result = self.make(ADAPTERS, target, openshell=True)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(self.calls(), [["podman", str(self.root / ADAPTERS), "push", tag]])


if __name__ == "__main__":
    unittest.main()
