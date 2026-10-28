"""Focused dispatch tests for fulfillment-service's image-build target."""

import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path


MAKEFILE = Path(__file__).resolve().parents[2] / "fulfillment-service" / "Makefile"


class ImageBuildDispatchTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.component = self.root / "fulfillment-service"
        self.component.mkdir()
        shutil.copyfile(MAKEFILE, self.component / "Makefile")
        subprocess.run(["git", "init", "-q", str(self.root)], check=True)
        openshell = self.root / "tools/openshell.mk"
        openshell.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(MAKEFILE.parents[1] / "tools/openshell.mk", openshell)
        kind_load = self.root / "osac-installer/scripts/dev-full/kind-load-image.mk"
        kind_load.parent.mkdir(parents=True)
        shutil.copyfile(MAKEFILE.parents[1] / "osac-installer/scripts/dev-full/kind-load-image.mk", kind_load)
        self.log = self.root / "calls"
        self.bin = self.root / "bin"
        self.bin.mkdir()
        podman = self.bin / "podman"
        podman.write_text('#!/bin/sh\nprintf "podman %s\\n" "$*" >> "$CALL_LOG"\n')
        podman.chmod(0o755)

    def helper(self):
        helper = self.root / "tools" / "carbonite" / "host-build"
        helper.parent.mkdir(parents=True)
        helper.write_text('#!/bin/sh\nprintf "helper %s\\n" "$*" >> "$CALL_LOG"\n')
        helper.chmod(0o755)
        return helper

    def make(self, *args, openshell=False, project_dir=False, target="image-build", extra_env=None):
        env = os.environ.copy()
        for key in ("OPENSHELL_SANDBOX", "PROJECT_DIR", "IMG", "CONTAINERFILE", "CONTAINER_TOOL", "VERSION"):
            env.pop(key, None)
        if openshell:
            env["OPENSHELL_SANDBOX"] = "osac-carbonite"
        if project_dir:
            env["PROJECT_DIR"] = "/sandbox/osac"
        env["CALL_LOG"] = str(self.log)
        env["PATH"] = f"{self.bin}:{env['PATH']}"
        env.update(extra_env or {})
        return subprocess.run(
            ["make", "--no-print-directory", "-C", str(self.component), *([target] if target else []), *args],
            env=env,
            text=True,
            capture_output=True,
            check=False,
        )

    def calls(self):
        return self.log.read_text().splitlines() if self.log.exists() else []

    def test_openshell_requests_only_fixed_fulfillment_action(self):
        self.helper()
        result = self.make(openshell=True, project_dir=True, target=None)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.calls(), ["helper fulfillment"])

    def test_openshell_fails_when_helper_is_missing(self):
        result = self.make(openshell=True, project_dir=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("host-build", result.stderr)
        self.assertEqual(self.calls(), [])

    def test_openshell_rejects_all_recipe_overrides_before_dispatch(self):
        self.helper()
        for override in (
            "IMG=ghcr.io/example/other:latest",
            "CONTAINERFILE=Otherfile",
            "CONTAINER_TOOL=docker",
            "VERSION=v1.2.3",
            "VERSION=; touch /tmp/should-not-run",
        ):
            with self.subTest(override=override):
                result = self.make(override, openshell=True, project_dir=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("override", result.stderr)
                self.assertEqual(self.calls(), [])

        for key in ("IMG", "CONTAINERFILE", "CONTAINER_TOOL", "VERSION"):
            with self.subTest(environment=key):
                result = self.make(openshell=True, extra_env={key: "other"})
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("override", result.stderr)
                self.assertEqual(self.calls(), [])

    def test_host_and_distrobox_keep_repo_root_context_and_version_arg(self):
        for project_dir in (False, True):
            with self.subTest(project_dir=project_dir):
                self.log.unlink(missing_ok=True)
                result = self.make(project_dir=project_dir)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(
                    self.calls(),
                    ["podman build -t ghcr.io/osac-project/fulfillment-service:latest -f Containerfile --build-arg VERSION= .."],
                )


if __name__ == "__main__":
    unittest.main()
