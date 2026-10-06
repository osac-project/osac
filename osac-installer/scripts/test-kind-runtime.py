#!/usr/bin/env python3
"""Exercise Kind host commands with fake runtimes and sudo credentials."""

import os
from pathlib import Path
import shlex
import socket
import subprocess
import tempfile
import unittest


INSTALLER = Path(__file__).resolve().parents[1]
REPO = INSTALLER.parent
RUNTIME = INSTALLER / "scripts/dev-full/kind-runtime.sh"


class KindRuntimeTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="osac-kind-runtime-test-")
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name)
        self.bin = self.directory / "bin"
        self.bin.mkdir()
        self.log = self.directory / "commands.log"
        self.cache = self.directory / "sudo-cache"
        self.env = {
            **os.environ,
            "PATH": f"{self.bin}:/usr/bin:/bin",
            "KIND_PROFILE": "dev-full",
            "KIND_EXPERIMENTAL_PROVIDER": "podman",
            "ROOTFUL_SOCKET": str(self.directory / "podman.sock"),
            "MOCK_LOG": str(self.log),
            "MOCK_CACHE": str(self.cache),
            "MOCK_OS": "Linux",
            "MOCK_UID": "1000",
            "MOCK_AUTH_DENIED": "0",
            "MOCK_NOPASSWD": "0",
            "MOCK_FORCE_ROOTLESS": "0",
            "MOCK_KIND_NODES": "osac-dev-control-plane",
            "MOCK_KIND_NODES_FAILURE": "0",
            "MOCK_KIND_DELETE_FAILURE": "0",
            "MOCK_KIND_CREATED": str(self.directory / "kind-created"),
            "MOCK_DOCKER_INFO_FAILURE": "0",
            "GOTOOLCHAIN": "local",
        }
        for name in ("CONTAINER_HOST", "MOCK_AS_ROOT", "PROFILE"):
            self.env.pop(name, None)
        self.write_command("uname", 'if [[ "${1:-}" == -m ]]; then echo x86_64; else echo "$MOCK_OS"; fi')
        self.write_command("id", 'echo "$MOCK_UID"')
        self.write_command("go", 'echo /tmp/osac-test-go')
        self.write_command("helm", 'echo v3.0.0')
        self.write_command("jq", 'exit 0')
        self.write_command("kubectl", 'printf "kubectl %s\\n" "$*" >> "$MOCK_LOG"; exit 1')
        self.write_command("sudo", """
printf 'sudo %s\n' "$*" >> "$MOCK_LOG"
if [[ "${1:-}" == -n ]]; then
  shift
  if [[ ! -f "$MOCK_CACHE" && "$MOCK_NOPASSWD" != 1 ]]; then exit 1; fi
elif [[ ! -f "$MOCK_CACHE" && "$MOCK_NOPASSWD" != 1 ]]; then
  echo 'sudo prompt' >> "$MOCK_LOG"
  if [[ "$MOCK_AUTH_DENIED" == 1 ]]; then
    echo 'sudo: authentication failed' >&2
    exit 1
  fi
  touch "$MOCK_CACHE"
fi
while [[ "${1:-}" == *=* ]]; do export "$1"; shift; done
MOCK_AS_ROOT=1 "$@"
""")
        self.write_command("podman", """
mode=user
if [[ -n "${CONTAINER_HOST:-}" ]]; then
  mode=socket
elif [[ "${MOCK_AS_ROOT:-0}" == 1 || "$MOCK_UID" == 0 ]]; then
  mode=root
fi
printf 'podman %s %s\n' "$mode" "$*" >> "$MOCK_LOG"
if [[ "${1:-}" == info ]]; then
  if [[ "$mode" != user && "$MOCK_FORCE_ROOTLESS" != 1 ]]; then echo false; else echo true; fi
elif [[ "${1:-}" == load && -n "${MOCK_LOAD_PATH:-}" ]]; then
  cat > "$MOCK_LOAD_PATH"
elif [[ "${1:-}" == save ]]; then
  echo 'stub archive'
fi
""")
        self.write_command("kind", """
printf 'kind %s %s\n' "${MOCK_AS_ROOT:-0}" "$*" >> "$MOCK_LOG"
if [[ "$*" == 'get nodes --name osac-dev' ]]; then
  if [[ "$MOCK_KIND_NODES_FAILURE" == 1 ]]; then
    printf '%s\n' "$MOCK_KIND_NODES"
    exit 1
  fi
  if [[ -f "$MOCK_KIND_CREATED" ]]; then
    echo osac-dev-control-plane
  else
    printf '%s\n' "$MOCK_KIND_NODES"
  fi
elif [[ "${1:-}" == create ]]; then
  touch "$MOCK_KIND_CREATED"
elif [[ "$*" == 'delete cluster --name osac-dev' && "$MOCK_KIND_DELETE_FAILURE" == 1 ]]; then
  exit 1
elif [[ "${1:-}" == load ]]; then
  [[ "$(cat "$3")" == 'stub archive' ]]
fi
""")
        self.write_command("docker", """
printf 'docker %s\n' "$*" >> "$MOCK_LOG"
if [[ "${1:-}" == info && "$MOCK_DOCKER_INFO_FAILURE" == 1 ]]; then
  echo 'installation preflight must not run while loading an image' >&2
  exit 1
elif [[ "${1:-}" == save ]]; then
  echo 'stub archive'
fi
""")

    def write_command(self, name, body):
        path = self.bin / name
        path.write_text("#!/usr/bin/env bash\nset -euo pipefail\n" + body + "\n")
        path.chmod(0o755)
        return path

    def run_shell(self, command, *, stdin=None):
        return subprocess.run(
            ["/bin/bash", "-c", f"source {shlex.quote(str(RUNTIME))}; {command}"],
            env=self.env, input=stdin, text=True, capture_output=True, timeout=15,
        )

    def commands(self):
        return self.log.read_text().splitlines() if self.log.exists() else []

    def assert_success(self, result):
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_expired_credentials_prompt_before_selecting_rootful_podman(self):
        result = self.run_shell("container_cmd ps")
        self.assert_success(result)
        self.assertEqual(self.commands().count("sudo prompt"), 1)
        self.assertIn("podman root ps", self.commands())
        self.assertNotIn("podman user ps", self.commands())

    def test_cached_credentials_do_not_prompt(self):
        self.cache.touch()
        self.assert_success(self.run_shell("container_cmd ps"))
        self.assertNotIn("sudo prompt", self.commands())
        self.assertIn("podman root ps", self.commands())

    def test_container_commands_reauthenticate_after_expiry(self):
        self.assert_success(self.run_shell('container_cmd ps; rm "$MOCK_CACHE"; container_cmd images'))
        self.assertEqual(self.commands().count("sudo prompt"), 2)
        self.assertIn("podman root images", self.commands())

    def test_kind_commands_reauthenticate_after_expiry(self):
        self.assert_success(self.run_shell('container_cmd ps; rm "$MOCK_CACHE"; kind_cmd get nodes --name osac-dev'))
        self.assertEqual(self.commands().count("sudo prompt"), 2)
        self.assertIn("kind 1 get nodes --name osac-dev", self.commands())

    def test_authentication_failure_stops_without_rootless_fallback(self):
        self.env["MOCK_AUTH_DENIED"] = "1"
        result = self.run_shell("container_cmd ps")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("sudo: authentication failed", result.stderr)
        self.assertNotIn("podman user ps", self.commands())
        self.assertNotIn("podman root ps", self.commands())

    def test_rootful_engine_is_checked_after_authentication(self):
        self.env["MOCK_FORCE_ROOTLESS"] = "1"
        result = self.run_shell("container_cmd ps")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("requires a reachable rootful Podman engine", result.stderr)
        self.assertNotIn("podman user ps", self.commands())

    def test_authentication_does_not_consume_container_input(self):
        output = self.directory / "loaded-image"
        self.env["MOCK_LOAD_PATH"] = str(output)
        self.assert_success(self.run_shell("container_cmd load", stdin="image archive input\n"))
        self.assertEqual(output.read_text(), "image archive input\n")
        self.assertEqual(self.commands().count("sudo prompt"), 1)

    def test_command_specific_passwordless_sudo_does_not_prompt(self):
        self.env["MOCK_NOPASSWD"] = "1"
        self.assert_success(self.run_shell("container_cmd ps"))
        self.assertNotIn("sudo prompt", self.commands())
        self.assertIn("podman root ps", self.commands())

    def test_rootful_socket_needs_no_sudo(self):
        with socket.socket(socket.AF_UNIX) as service:
            service.bind(self.env["ROOTFUL_SOCKET"])
            self.assert_success(self.run_shell("container_cmd ps"))
        self.assertIn("podman socket ps", self.commands())
        self.assertFalse(any(line.startswith("sudo ") for line in self.commands()))

    def test_root_user_needs_no_sudo(self):
        self.env["MOCK_UID"] = "0"
        self.assert_success(self.run_shell("container_cmd ps"))
        self.assertIn("podman root ps", self.commands())
        self.assertFalse(any(line.startswith("sudo ") for line in self.commands()))

    def test_rootless_dev_profile_does_not_prompt(self):
        self.env["KIND_PROFILE"] = "dev"
        self.assert_success(self.run_shell("container_cmd ps"))
        self.assertIn("podman user ps", self.commands())
        self.assertNotIn("sudo prompt", self.commands())

    def test_docker_needs_no_sudo(self):
        self.env["KIND_EXPERIMENTAL_PROVIDER"] = "docker"
        self.assert_success(self.run_shell("container_cmd ps"))
        self.assertIn("docker ps", self.commands())
        self.assertFalse(any(line.startswith("sudo ") for line in self.commands()))

    def test_podman_desktop_needs_no_host_sudo(self):
        self.env["MOCK_OS"] = "Darwin"
        self.assert_success(self.run_shell("container_cmd ps"))
        self.assertIn("podman user ps", self.commands())
        self.assertFalse(any(line.startswith("sudo ") for line in self.commands()))

    def test_cleanup_has_permissions_for_pod_owned_directories(self):
        arguments = self.directory / "cleanup-arguments"
        self.env["MOCK_RUN_ARGS"] = str(arguments)
        self.assert_success(self.run_shell('''
container_cmd() {
  if [[ "$1" == run ]]; then printf '%s\\0' "$@" > "$MOCK_RUN_ARGS"; fi
}
delete_topolvm_runtime
'''))
        args = arguments.read_bytes().decode().rstrip("\0").split("\0")
        self.assertIn("--cap-drop=ALL", args)
        self.assertIn("--cap-add=DAC_OVERRIDE", args)
        self.assertIn("--cap-add=FOWNER", args)
        self.assertIn("--security-opt", args)
        self.assertIn("label=disable", args)
        self.assertIn("/var/lib/osac-dev-full/topolvm:/runtime", args)
        self.assertNotIn("--privileged", args)
        self.assertTrue(args[-1].endswith("rm -rf /runtime/*"))

    def run_create_cluster(self):
        kubeconfig = self.directory / "cluster.kubeconfig"
        return self.run_shell(f'''
ensure_topolvm_runtime() {{ echo 'topolvm prepare' >> "$MOCK_LOG"; }}
check_topolvm_cluster_mounts() {{ echo 'topolvm check mounts' >> "$MOCK_LOG"; }}
create_cluster osac-dev config.yaml {shlex.quote(str(kubeconfig))}
''')

    def test_create_keeps_credentials_when_cluster_lookup_fails(self):
        self.env["MOCK_KIND_NODES_FAILURE"] = "1"
        for output in ("", "osac-dev-control-plane"):
            with self.subTest(lookup_output=output):
                self.env["MOCK_KIND_NODES"] = output
                result = self.run_create_cluster()
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("refusing to reset kubelet credentials", result.stderr)
                self.assertNotIn("topolvm prepare", self.commands())
                self.assertFalse(any(cmd.startswith("podman root run ")
                                     or cmd.startswith("kind 1 create ")
                                     or cmd.startswith("kind 1 get kubeconfig ")
                                     for cmd in self.commands()))
                self.assertFalse((self.directory / "cluster.kubeconfig").exists())

    def test_create_reuses_existing_cluster_without_resetting_credentials(self):
        self.assert_success(self.run_create_cluster())
        self.assertIn("topolvm check mounts", self.commands())
        self.assertIn("topolvm prepare", self.commands())
        self.assertFalse(any(cmd.startswith("podman root run ")
                             or cmd.startswith("kind 1 create ")
                             for cmd in self.commands()))

    def test_create_resets_credentials_only_after_confirming_cluster_absence(self):
        self.env["MOCK_KIND_NODES"] = ""
        self.assert_success(self.run_create_cluster())
        commands = self.commands()
        lookup = commands.index("kind 1 get nodes --name osac-dev")
        reset = next(i for i, cmd in enumerate(commands) if cmd.endswith("rm -rf /kubelet/pki"))
        creation = commands.index("kind 1 create cluster --name osac-dev --config config.yaml --wait 60s")
        self.assertLess(lookup, reset)
        self.assertLess(reset, creation)

    def test_uninstall_deletes_cluster_before_storage_cleanup(self):
        self.assert_success(self.run_shell("delete_cluster osac-dev"))
        commands = self.commands()
        deletion = commands.index("kind 1 delete cluster --name osac-dev")
        cleanup = next(i for i, cmd in enumerate(commands) if cmd.startswith("podman root run "))
        self.assertLess(deletion, cleanup)
        self.assertNotIn("kind 1 get clusters", commands)

    def test_uninstall_retries_cleanup_when_cluster_is_already_absent(self):
        self.env["MOCK_KIND_NODES"] = ""
        self.assert_success(self.run_shell("delete_cluster osac-dev"))
        self.assertNotIn("kind 1 delete cluster --name osac-dev", self.commands())
        self.assertTrue(any(cmd.startswith("podman root run ") for cmd in self.commands()))

    def test_uninstall_keeps_storage_when_cluster_lookup_fails(self):
        self.env["MOCK_KIND_NODES_FAILURE"] = "1"
        result = self.run_shell("delete_cluster osac-dev")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("refusing to remove its storage", result.stderr)
        self.assertFalse(any(cmd.startswith("podman root rm ") or cmd.startswith("podman root run ")
                             for cmd in self.commands()))

    def test_uninstall_keeps_storage_when_cluster_deletion_fails(self):
        self.env["MOCK_KIND_DELETE_FAILURE"] = "1"
        result = self.run_shell("delete_cluster osac-dev")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("refusing to remove its storage", result.stderr)
        self.assertFalse(any(cmd.startswith("podman root rm ") or cmd.startswith("podman root run ")
                             for cmd in self.commands()))

    def test_standalone_virtualization_setup_authenticates(self):
        self.env.pop("KIND_PROFILE")
        result = subprocess.run(
            ["/bin/bash", str(INSTALLER / "scripts/dev-full/install-virt-node-setup.sh"), "osac-dev"],
            env=self.env, text=True, capture_output=True, timeout=15,
        )
        self.assert_success(result)
        self.assertEqual(self.commands().count("sudo prompt"), 1)
        self.assertIn("podman root inspect osac-dev-control-plane", self.commands())
        self.assertTrue(any(line.startswith("podman root exec osac-dev-control-plane ") for line in self.commands()))

    def test_all_component_load_targets_select_dev_full(self):
        for component in ("fulfillment-service", "osac-operator", "osac-csi-driver", "bare-metal-fulfillment-operator"):
            with self.subTest(component=component):
                result = subprocess.run(
                    ["make", "--no-print-directory", "-n", "-C", str(REPO / component), "kind-load-image", "NS=osac"],
                    env=self.env, text=True, capture_output=True, timeout=15,
                )
                self.assert_success(result)
                self.assertIn('KIND_PROFILE="dev-full"', result.stdout)

    def test_operator_legacy_load_target_uses_shared_loader(self):
        result = subprocess.run(
            ["make", "--no-print-directory", "-n", "-C", str(REPO / "osac-operator"), "kind-load", "NS=osac"],
            env=self.env, text=True, capture_output=True, timeout=15,
        )
        self.assert_success(result)
        self.assertIn("kind-load-image.sh", result.stdout)
        self.assertIn('KIND_PROFILE="dev-full"', result.stdout)
        self.assertNotIn("kind load docker-image", result.stdout)

    def run_image_load_target(self, target, profile, container_tool):
        return subprocess.run(
            ["make", "--no-print-directory", "-C", str(REPO / "osac-operator"),
             "-o", "image-build", target, f"PROFILE={profile}",
             f"CONTAINER_TOOL={container_tool}", "IMG=test/operator:latest", "NS=osac",
             f"KUBECONFIG={self.directory / 'cluster.kubeconfig'}"],
            env=self.env, text=True, capture_output=True, timeout=15,
        )

    def test_operator_dev_image_load_needs_no_virtualization_preflight(self):
        self.env.pop("KIND_EXPERIMENTAL_PROVIDER")
        self.env["MOCK_DOCKER_INFO_FAILURE"] = "1"
        self.assert_success(self.run_image_load_target("kind-load", "dev", "docker"))
        self.assertIn("docker save test/operator:latest", self.commands())
        self.assertIn("kind 0 get nodes --name osac-dev", self.commands())
        self.assertTrue(any(cmd.startswith("kind 0 load image-archive ") for cmd in self.commands()))
        self.assertFalse(any(cmd.startswith("docker info") or cmd.startswith("podman ")
                             or cmd.startswith("sudo ") for cmd in self.commands()))

    def test_component_dev_full_image_load_still_authenticates_rootful_podman(self):
        self.env.pop("KIND_EXPERIMENTAL_PROVIDER")
        self.assert_success(self.run_image_load_target("kind-load-image", "dev-full", "podman"))
        self.assertEqual(self.commands().count("sudo prompt"), 1)
        self.assertIn("podman user save test/operator:latest", self.commands())
        self.assertTrue(any(cmd.startswith("kind 1 load image-archive ") for cmd in self.commands()))

    def test_image_load_reports_authentication_failure_before_saving(self):
        self.env["MOCK_AUTH_DENIED"] = "1"
        result = self.run_image_load_target("kind-load-image", "dev-full", "podman")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("sudo: authentication failed", result.stderr)
        self.assertIn("Could not check Kind cluster", result.stderr)
        self.assertNotIn("podman user save test/operator:latest", self.commands())
        self.assertFalse(any(" load image-archive " in cmd for cmd in self.commands()))

    def test_image_load_stops_when_cluster_is_absent(self):
        self.env["MOCK_KIND_NODES"] = ""
        result = self.run_image_load_target("kind-load-image", "dev-full", "podman")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("does not exist", result.stderr)
        self.assertNotIn("podman user save test/operator:latest", self.commands())
        self.assertFalse(any(" load image-archive " in cmd for cmd in self.commands()))

    def test_image_load_honors_explicit_kind_provider(self):
        self.env["KIND_EXPERIMENTAL_PROVIDER"] = "docker"
        self.assert_success(self.run_image_load_target("kind-load-image", "dev", "podman"))
        self.assertIn("podman user save test/operator:latest", self.commands())
        self.assertTrue(any(cmd.startswith("kind 0 load image-archive ") for cmd in self.commands()))
        self.assertFalse(any(cmd.startswith("sudo ") for cmd in self.commands()))

    def test_installer_ui_load_propagates_the_selected_profile(self):
        result = subprocess.run(
            ["make", "--no-print-directory", "-n", "-C", str(INSTALLER), "kind-load-images", "PLATFORM=kind", "PROFILE=dev-full", "NS=osac"],
            env=self.env, text=True, capture_output=True, timeout=15,
        )
        self.assert_success(result)
        self.assertIn('KIND_PROFILE=dev-full KIND_OSAC_NAMESPACE=osac', result.stdout)


if __name__ == "__main__":
    unittest.main(verbosity=2)
