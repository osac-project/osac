"""Focused dispatch tests for osac-ui's image-build Make target."""

import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path


MAKEFILE = Path(__file__).resolve().parents[2] / "osac-ui" / "Makefile"


class ImageBuildDispatchTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.ui = self.root / "osac-ui"
        self.ui.mkdir()
        shutil.copyfile(MAKEFILE, self.ui / "Makefile")
        subprocess.run(["git", "init", "-q", str(self.root)], check=True)
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
            ["make", "--no-print-directory", "-C", str(self.ui), *([target] if target else []), *args],
            env=env,
            text=True,
            capture_output=True,
            check=False,
        )

    def calls(self):
        return self.log.read_text().splitlines() if self.log.exists() else []

    def test_openshell_calls_only_fixed_ui_action(self):
        self.helper()
        result = self.make(openshell=True, project_dir=True, target=None)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.calls(), ["helper ui"])

    def test_openshell_fails_when_helper_is_missing(self):
        result = self.make(openshell=True, project_dir=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("host-build", result.stderr)
        self.assertEqual(self.calls(), [])

    def test_openshell_rejects_all_make_overrides_before_dispatch(self):
        self.helper()
        for override in (
            "IMG=ghcr.io/example/other:latest",
            "CONTAINERFILE=Otherfile",
            "CONTAINER_TOOL=docker",
            "IMG=foo; touch /tmp/should-not-run",
        ):
            with self.subTest(override=override):
                result = self.make(override, openshell=True, project_dir=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("override", result.stderr)
                self.assertEqual(self.calls(), [])

        result = self.make(openshell=True, extra_env={"IMG": "other-image"})
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("override", result.stderr)
        self.assertEqual(self.calls(), [])

    def test_host_and_distrobox_use_direct_podman(self):
        for project_dir in (False, True):
            with self.subTest(project_dir=project_dir):
                self.log.unlink(missing_ok=True)
                result = self.make(project_dir=project_dir)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(
                    self.calls(),
                    ["podman build -f Containerfile -t ghcr.io/osac-project/osac-ui:latest .."],
                )


if __name__ == "__main__":
    unittest.main()
