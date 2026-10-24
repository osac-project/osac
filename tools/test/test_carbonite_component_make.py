"""Check component image targets at the Carbonite/host boundary."""

import os
import pathlib
import shutil
import subprocess
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]


class ComponentImageTargetsTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = pathlib.Path(self.temp.name)
        self.log = self.root / "calls"
        self.bin = self.root / "bin"
        self.bin.mkdir()
        for command in ("podman", "docker"):
            script = self.bin / command
            script.write_text('#!/bin/sh\nprintf "%s %s\\n" "' + command + '" "$*" >> "$CALL_LOG"\n')
            script.chmod(0o755)
        go = self.bin / "go"
        go.write_text('#!/bin/sh\nif [ "$2" = GOPATH ]; then printf "/tmp/go\\n"; fi\n')
        go.chmod(0o755)
        git = self.bin / "git"
        git.write_text('#!/bin/sh\nif [ "$1" = rev-parse ]; then printf "%s\\n" "$FAKE_REPO_ROOT"; fi\n')
        git.chmod(0o755)
        (self.root / "tools").mkdir()
        (self.root / "tools/golangci-lint.mk").write_text("")
        self.operator = self.root / "osac-operator"
        self.devstack = self.root / "osac-installer/charts/osac-devstack"
        self.operator.mkdir()
        self.devstack.mkdir(parents=True)
        shutil.copyfile(ROOT / "osac-operator/Makefile", self.operator / "Makefile")
        shutil.copyfile(ROOT / "osac-installer/charts/osac-devstack/Makefile", self.devstack / "Makefile")
        kind_load = self.root / "osac-installer/scripts/dev-full/kind-load-image.mk"
        kind_load.parent.mkdir(parents=True)
        shutil.copyfile(ROOT / "osac-installer/scripts/dev-full/kind-load-image.mk", kind_load)

    def helper(self):
        helper = self.root / "tools/carbonite/host-build"
        helper.parent.mkdir(parents=True)
        helper.write_text('#!/bin/sh\nprintf "helper %s\\n" "$*" >> "$CALL_LOG"\n')
        helper.chmod(0o755)

    def make(self, directory, target, *args, sandbox=False, extra_env=None):
        env = os.environ.copy()
        env.pop("OPENSHELL_SANDBOX", None)
        if sandbox:
            env["OPENSHELL_SANDBOX"] = "osac-carbonite"
        env["PATH"] = f"{self.bin}:{env['PATH']}"
        env["CALL_LOG"] = str(self.log)
        env["FAKE_REPO_ROOT"] = str(self.root)
        env.update(extra_env or {})
        return subprocess.run(["make", "--no-print-directory", "-C", str(directory), target, *args],
                              env=env, text=True, capture_output=True)

    def calls(self):
        return self.log.read_text().splitlines() if self.log.exists() else []

    def test_operator_sandbox_build_uses_fixed_action(self):
        self.helper()
        result = self.make(self.operator, "image-build", sandbox=True)
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertEqual(["helper operator"], self.calls())

    def test_operator_sandbox_rejects_overrides_before_any_build(self):
        self.helper()
        for override in ("IMG=ghcr.io/example/other:latest", "CONTAINERFILE=Otherfile",
                         "CONTAINER_TOOL=docker", "IMG=foo; /tmp/unwanted"):
            with self.subTest(override=override):
                result = self.make(self.operator, "image-build", override, sandbox=True)
                self.assertNotEqual(0, result.returncode)
                self.assertIn("override", result.stderr)
                self.assertEqual([], self.calls())
        result = self.make(self.operator, "image-build", sandbox=True, extra_env={"IMG": "other"})
        self.assertNotEqual(0, result.returncode)
        self.assertEqual([], self.calls())

    def test_operator_sandbox_requires_helper(self):
        result = self.make(self.operator, "image-build", sandbox=True)
        self.assertNotEqual(0, result.returncode)
        self.assertIn("host-build", result.stderr)
        self.assertEqual([], self.calls())

    def test_operator_host_build_retains_original_recipe(self):
        result = self.make(self.operator, "image-build")
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertEqual(["podman build -t ghcr.io/osac-project/osac-operator:latest -f Containerfile .."],
                         self.calls())

    def test_operator_host_build_still_accepts_existing_overrides(self):
        result = self.make(self.operator, "image-build", "CONTAINER_TOOL=docker",
                           "IMG=example/operator:test", "CONTAINERFILE=Alternatefile")
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertEqual(["docker build -t example/operator:test -f Alternatefile .."], self.calls())

    def test_devstack_sandbox_build_uses_fixed_action(self):
        self.helper()
        result = self.make(self.devstack, "build-cli-tools", sandbox=True)
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertEqual(["helper devstack-cli-tools"], self.calls())

    def test_devstack_sandbox_rejects_tag_override_and_missing_helper(self):
        self.helper()
        result = self.make(self.devstack, "build-cli-tools", "CLI_TOOLS_IMAGE=other", sandbox=True)
        self.assertNotEqual(0, result.returncode)
        self.assertIn("override", result.stderr)
        self.assertEqual([], self.calls())
        (self.root / "tools/carbonite/host-build").unlink()
        result = self.make(self.devstack, "build-cli-tools", sandbox=True)
        self.assertNotEqual(0, result.returncode)
        self.assertIn("host-build", result.stderr)
        self.assertEqual([], self.calls())

    def test_devstack_sandbox_build_push_fails_before_build(self):
        self.helper()
        result = self.make(self.devstack, "build-push-cli-tools", "-j4", sandbox=True)
        self.assertNotEqual(0, result.returncode)
        self.assertIn("unsupported", result.stderr)
        self.assertEqual([], self.calls())

    def test_devstack_sandbox_push_is_explicitly_unsupported(self):
        result = self.make(self.devstack, "push-cli-tools", sandbox=True)
        self.assertNotEqual(0, result.returncode)
        self.assertIn("unsupported", result.stderr)
        self.assertEqual([], self.calls())

    def test_devstack_host_build_and_push_retain_original_recipes(self):
        result = self.make(self.devstack, "build-push-cli-tools")
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertEqual([
            "podman build --platform linux/amd64,linux/arm64 --manifest ghcr.io/osac-project/cli-tools:latest -f Containerfile.cli-tools .",
            "podman manifest push --all ghcr.io/osac-project/cli-tools:latest",
        ], self.calls())


if __name__ == "__main__":
    unittest.main()
