"""Exercise osac-installer Makefile variables without touching a cluster.

Every check runs `make -C osac-installer -n`, so recipes are printed rather
than executed: nothing is built, loaded or installed.

Run: python3 tools/test/test_installer_make.py
"""

import os
import re
import shutil
import subprocess
import tempfile
import time
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
INSTALLER = ROOT / "osac-installer"

BASE_ARGS = ["PLATFORM=kind", "PROFILE=dev", "NS=osac"]


class InstallerMakeTest(unittest.TestCase):
    def make(self, target, *args, **env):
        """Run make with -n so recipes are printed, never executed."""
        return subprocess.run(
            [shutil.which("make"), "-C", str(INSTALLER), "-n", target, *args],
            env={**os.environ, **env},
            capture_output=True,
            text=True,
            timeout=120,
        )

    def install(self, *extra, **env):
        """Dry-run install-osac with the base arguments."""
        return self.make("install-osac", *BASE_ARGS, *extra, **env)

    def local_image(self, *extra, **env):
        """Dry-run install-osac with the local operator image enabled."""
        return self.install("USE_LOCAL_OPERATOR=true", *extra, **env)

    def built_image(self, output):
        """Return the image reference the build step would produce."""
        match = re.search(r"image-build IMG='?(localhost/osac-operator:[^\s']+)", output)
        self.assertIsNotNone(match, output)
        return match.group(1)

    def helm_tag(self, output):
        """Return the operator image tag passed to Helm."""
        match = re.search(r"--set(?:-string)? operator\.image\.tag=([^\s\\]+)", output)
        self.assertIsNotNone(match, output)
        return match.group(1)

    # -- CONTAINER_TOOL and NS validation -------------------------------

    def test_accepts_a_plain_tool_name_and_an_absolute_path(self):
        """A bare name and an absolute path are both accepted."""
        for tool in ("podman", "docker", "/usr/bin/podman"):
            with self.subTest(tool=tool):
                result = self.install(CONTAINER_TOOL=tool)
                self.assertEqual(result.returncode, 0, result.stderr)

    def test_rejects_shell_syntax_in_the_container_tool(self):
        """Shell syntax in CONTAINER_TOOL fails before anything runs."""
        with tempfile.TemporaryDirectory() as temp:
            marker = Path(temp) / "pwned"
            for tool in (
                f"podman; touch {marker} #",
                f"podman && touch {marker}",
                "podman x",
                "./podman",
                "podman\nx",
                "",
            ):
                with self.subTest(tool=tool):
                    result = self.install(CONTAINER_TOOL=tool)
                    self.assertNotEqual(result.returncode, 0, result.stdout)
                    self.assertIn("CONTAINER_TOOL", result.stderr)
                    self.assertFalse(marker.exists())

    def test_rejects_an_invalid_namespace(self):
        """An invalid or injected namespace is rejected."""
        with tempfile.TemporaryDirectory() as temp:
            marker = Path(temp) / "pwned"
            for namespace in (
                f"osac; touch {marker} #", "Osac", "-osac", "osac-", "x" * 64, "osac\nx",
            ):
                with self.subTest(namespace=namespace):
                    result = self.make(
                        "install-osac", "PLATFORM=kind", "PROFILE=dev", f"NS={namespace}"
                    )
                    self.assertNotEqual(result.returncode, 0, result.stdout)
                    self.assertIn("NS", result.stderr)
                    self.assertFalse(marker.exists())

    def test_rejects_shell_syntax_in_the_kind_provider(self):
        """Only docker and podman are accepted as the kind provider."""
        with tempfile.TemporaryDirectory() as temp:
            marker = Path(temp) / "pwned"
            for provider in (f"podman; touch {marker} #", "podman x", "nerdctl"):
                with self.subTest(provider=provider):
                    result = self.make(
                        "install-infra", *BASE_ARGS, KIND_EXPERIMENTAL_PROVIDER=provider
                    )
                    self.assertNotEqual(result.returncode, 0, result.stdout)
                    self.assertIn("KIND_EXPERIMENTAL_PROVIDER", result.stderr)
                    self.assertFalse(marker.exists())

    def test_accepts_the_supported_kind_providers(self):
        """Unset, docker and podman providers are accepted."""
        for provider in ("", "docker", "podman"):
            with self.subTest(provider=provider):
                result = self.make(
                    "install-infra", *BASE_ARGS, KIND_EXPERIMENTAL_PROVIDER=provider
                )
                self.assertEqual(result.returncode, 0, result.stderr)

    def test_local_operator_image_validates_the_namespace(self):
        """NS is rejected on the local image target too."""
        with tempfile.TemporaryDirectory() as temp:
            marker = Path(temp) / "pwned"
            result = self.make(
                "local-operator-image", "PLATFORM=kind", "PROFILE=dev",
                f"NS=osac; touch {marker} #",
            )
            self.assertNotEqual(result.returncode, 0, result.stdout)
            self.assertIn("NS", result.stderr)
            self.assertFalse(marker.exists())

    def test_local_operator_image_requires_the_shared_arguments(self):
        """The target is gated by the same PLATFORM/PROFILE/NS validation."""
        result = self.make("local-operator-image")
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn("PLATFORM", result.stderr)

    def test_local_operator_image_validates_the_profile(self):
        """PROFILE is rejected on the local image target too."""
        with tempfile.TemporaryDirectory() as temp:
            marker = Path(temp) / "pwned"
            result = self.make(
                "local-operator-image", "PLATFORM=kind", "NS=osac",
                f"PROFILE=x; touch {marker} #",
            )
            self.assertNotEqual(result.returncode, 0, result.stdout)
            self.assertIn("PROFILE", result.stderr)
            self.assertFalse(marker.exists())

    def test_rejects_shell_syntax_in_the_cluster_name(self):
        """The kind cluster name reaches shell recipes; reject shell syntax."""
        with tempfile.TemporaryDirectory() as temp:
            marker = Path(temp) / "pwned"
            for name in (f"x; touch {marker} #", "x y", "x/y"):
                with self.subTest(name=name):
                    result = self.make(
                        "install-infra", *BASE_ARGS, KIND_CLUSTER_NAME=name
                    )
                    self.assertNotEqual(result.returncode, 0, result.stdout)
                    self.assertIn("KIND_CLUSTER_NAME", result.stderr)
                    self.assertFalse(marker.exists())

    def test_accepts_a_plain_cluster_name_and_profile(self):
        """The default-looking cluster name and a known profile pass."""
        result = self.make(
            "install-infra", *BASE_ARGS, KIND_CLUSTER_NAME="osac-dev"
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        result = self.make(
            "local-operator-image", "PROFILE=dev-full", "NS=osac",
            "PLATFORM=kind",
        )
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_local_operator_image_rejects_openshift(self):
        """The target loads into Kind, so it refuses any other platform."""
        result = self.make(
            "local-operator-image", "PLATFORM=openshift", "PROFILE=dev", "NS=osac"
        )
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn("PLATFORM=kind", result.stderr)

    # -- Local operator image -------------------------------------------

    def test_tag_is_passed_to_helm_as_a_string(self):
        """The tag reaches Helm through --set-string."""
        result = self.local_image()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("--set-string operator.image.tag=", result.stdout)

    def test_rejects_tags_the_runtime_cannot_use(self):
        """Tags the runtime cannot use are rejected."""
        for tag in (".", "-it", "a" * 129, "a,b", "a b", "a\nb"):
            with self.subTest(tag=tag):
                result = self.local_image(f"OPERATOR_IMAGE_TAG={tag}")
                self.assertNotEqual(result.returncode, 0, result.stdout)
                self.assertIn("OPERATOR_IMAGE_TAG", result.stderr)

    def test_build_load_and_helm_share_one_fresh_tag_per_run(self):
        """One tag per run, different in each run."""
        first = self.local_image()
        self.assertEqual(first.returncode, 0, first.stderr)
        time.sleep(1.1)
        second = self.local_image()
        self.assertEqual(second.returncode, 0, second.stderr)

        for run in (first, second):
            image = self.built_image(run.stdout)
            self.assertEqual(self.helm_tag(run.stdout), image.split(":", 1)[1])

        self.assertNotEqual(self.helm_tag(first.stdout), self.helm_tag(second.stdout))

    def test_suites_keep_a_stable_tag(self):
        """The integration suites keep the stable "it" tag."""
        result = self.make("test", *BASE_ARGS, "SUITE=operator")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.helm_tag(result.stdout), "it")
        self.assertIn("localhost/osac-operator:it", result.stdout)

    def test_dev_full_builds_through_the_kind_runtime(self):
        """dev-full builds through the kind runtime, not the rootless tool."""
        result = self.make(
            "install-osac", "PLATFORM=kind", "PROFILE=dev-full", "NS=osac",
            "USE_LOCAL_OPERATOR=true",
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        build = [line for line in result.stdout.splitlines() if "image-build" in line or "container build" in line]
        self.assertTrue(build, result.stdout)
        self.assertTrue(any("kind-runtime.sh container build" in line for line in build), build)

    # -- Kind provider --------------------------------------------------

    def test_install_infra_uses_the_selected_kind_provider(self):
        """Cluster creation uses the selected kind provider."""
        result = self.make("install-infra", *BASE_ARGS)
        self.assertEqual(result.returncode, 0, result.stderr)
        for command in ("kind get nodes", "kind create cluster", "kind export kubeconfig"):
            with self.subTest(command=command):
                matching = [
                    line for line in result.stdout.splitlines() if command in line
                ]
                self.assertTrue(matching, result.stdout)
                for line in matching:
                    self.assertIn(f"KIND_EXPERIMENTAL_PROVIDER=podman {command}", line)

    def test_uninstall_infra_uses_the_selected_kind_provider(self):
        """Cluster deletion uses the selected kind provider."""
        result = self.make("uninstall-infra", *BASE_ARGS)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("KIND_EXPERIMENTAL_PROVIDER=podman kind delete cluster", result.stdout)

    # -- Dry run --------------------------------------------------------

    def test_dry_run_renders_the_local_operator_image(self):
        """The dry run renders the same local operator image as the install."""
        result = self.make("install-dry-run", *BASE_ARGS, "USE_LOCAL_OPERATOR=true")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("--set-string operator.image.tag=", result.stdout)
        self.assertIn("operator.image.pullPolicy=Never", result.stdout)


if __name__ == "__main__":
    unittest.main()
