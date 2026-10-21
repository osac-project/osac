#!/usr/bin/env python3
"""Exercise Kind host commands with fake runtimes and sudo credentials."""

import json
import os
from pathlib import Path
import shlex
import shutil
import socket
import subprocess
import tempfile
import unittest


INSTALLER = Path(__file__).resolve().parents[1]
REPO = INSTALLER.parent
RUNTIME = INSTALLER / "scripts/dev-full/kind-runtime.sh"
JQ = shutil.which("jq")


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
            "MOCK_NODE_STATE": "running",
            "MOCK_RUNNING_NODE": "",
            "MOCK_NODE_INSPECT_FAILURE": "0",
            "MOCK_NODE_START_FAILURE": "0",
            "MOCK_MOUNTS_FAILURE": "0",
            "MOCK_READY_FAILURES": "0",
            "MOCK_READY_COUNT": str(self.directory / "ready-count"),
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
        self.write_command("kubectl", """
printf 'kubectl %s\n' "$*" >> "$MOCK_LOG"
if [[ "$*" == *'get --raw=/readyz' ]]; then
  attempt=0
  if [[ -f "$MOCK_READY_COUNT" ]]; then attempt=$(cat "$MOCK_READY_COUNT"); fi
  attempt=$((attempt + 1))
  echo "$attempt" > "$MOCK_READY_COUNT"
  if (( attempt > MOCK_READY_FAILURES )); then echo ok; exit 0; fi
fi
exit 1
""")
        self.write_command("sleep", 'printf "sleep %s\\n" "$*" >> "$MOCK_LOG"')
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
elif [[ "${1:-}" == inspect && "${2:-}" == --format ]]; then
  if [[ "$MOCK_NODE_INSPECT_FAILURE" == 1 ]]; then exit 1; fi
  if [[ "$4" == "$MOCK_RUNNING_NODE" ]]; then echo running; else echo "$MOCK_NODE_STATE"; fi
elif [[ "${1:-}" == start && "$MOCK_NODE_START_FAILURE" == 1 ]]; then
  exit 1
elif [[ "${1:-}" == run && "${!#}" == *'dmsetup version'* ]]; then
  echo 252
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
elif [[ "$*" == 'get kubeconfig --name osac-dev' ]]; then
  echo 'stub kubeconfig'
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
elif [[ "${1:-}" == inspect && "${2:-}" == --format ]]; then
  if [[ "$MOCK_NODE_INSPECT_FAILURE" == 1 ]]; then exit 1; fi
  if [[ "$4" == "$MOCK_RUNNING_NODE" ]]; then echo running; else echo "$MOCK_NODE_STATE"; fi
elif [[ "${1:-}" == start && "$MOCK_NODE_START_FAILURE" == 1 ]]; then
  exit 1
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
configure_topolvm_device_access() { :; }
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

    def test_runtime_dir_creation_disables_selinux_labeling(self):
        arguments = self.directory / "runtime-dir-arguments"
        self.env["MOCK_OS"] = "Darwin"
        self.env["MOCK_RUN_ARGS"] = str(arguments)
        self.assert_success(self.run_shell('''
container_cmd() { printf '%s\\0' "$@" > "$MOCK_RUN_ARGS"; }
ensure_topolvm_runtime_dir
'''))
        args = arguments.read_bytes().decode().rstrip("\0").split("\0")
        self.assertEqual(args[:2], ["run", "--rm"])
        self.assertEqual(args[2:4], ["--security-opt", "label=disable"])
        self.assertIn("/var/lib:/host-var-lib", args)
        self.assertIn("/host-var-lib/osac-dev-full/topolvm/socket", args)
        self.assertIn("/host-var-lib/osac-dev-full/topolvm/kubelet", args)

    def configure_storage_stubs(self, devices="Block devices:\n252 device-mapper\n", *, rule=None,
                                provider="podman", image="osac-dev/topolvm-lvmd:0.41.1-container-v2"):
        devices_file = self.directory / "proc-devices"
        devices_file.write_text(devices)
        inspect_file = self.directory / "lvmd-inspect.json"
        config = {
            "Image": image,
            "Cmd": ["/usr/local/bin/lvmd", "--config=/etc/topolvm/lvmd.yaml"],
        }
        host_config = {}
        if provider == "podman":
            config["CreateCommand"] = ["podman", "run", "--device-cgroup-rule", rule]
        elif rule is not None:
            host_config["DeviceCgroupRules"] = [rule]
        inspect_file.write_text(json.dumps([{
            "Config": config, "HostConfig": host_config, "State": {"Running": True},
        }]))
        self.env.update({
            "MOCK_UID": "0",
            "MOCK_PROC_DEVICES": str(devices_file),
            "MOCK_LVMD_INSPECT": str(inspect_file),
            "MOCK_LVMD_EXISTS": "1" if rule is not None else "0",
            "MOCK_DMSETUP_FAILURE": "0",
            "MOCK_RUN_ARGS": str(self.directory / "storage-run-arguments"),
            "KIND_EXPERIMENTAL_PROVIDER": provider,
        })
        self.assertIsNotNone(JQ, "jq must be installed for device-access tests")
        self.write_command("jq", f'exec {shlex.quote(JQ)} "$@"')
        self.write_command("awk", 'exec /usr/bin/awk "$1" "$MOCK_PROC_DEVICES"')
        self.write_command("dmsetup", '[[ "$MOCK_DMSETUP_FAILURE" == 0 ]]')
        body = '''
if [[ "$1" == info ]]; then echo false; exit 0; fi
printf 'runtime %s\\n' "$1" >> "$MOCK_LOG"
case "$1" in
  inspect)
    if [[ "$MOCK_LVMD_EXISTS" == 0 ]]; then exit 1; fi
    cat "$MOCK_LVMD_INSPECT"
    ;;
  run)
    printf '%s\\0' "$@" > "$MOCK_RUN_ARGS"
    if [[ "${!#}" == *'dmsetup version'* ]]; then bash -euo pipefail -c "${!#}"; fi
    ;;
esac
'''
        self.write_command(provider, body)

    def storage_run_arguments(self):
        return Path(self.env["MOCK_RUN_ARGS"]).read_bytes().decode().rstrip("\0").split("\0")

    def test_device_mapper_major_comes_from_selected_engine(self):
        for provider in ("podman", "docker"):
            for major in (252, 253, 254):
                with self.subTest(provider=provider, major=major):
                    self.configure_storage_stubs(
                        f"Character devices:\n199 device-mapper\nBlock devices:\n7 loop\n{major} device-mapper\n",
                        provider=provider,
                    )
                    result = self.run_shell('configure_topolvm_device_access; printf "%s\\n" "${TOPOLVM_LVM_DEVICE_ARGS[@]}"')
                    self.assert_success(result)
                    rules = [line for line in result.stdout.splitlines() if line.startswith("b ")]
                    self.assertEqual(rules, ["b 7:* rwm", f"b {major}:* rwm"])
                    self.assertIn("/dev:/dev", self.storage_run_arguments())
                    self.assertNotIn("--privileged", self.storage_run_arguments())

    def test_invalid_device_mapper_major_stops_before_storage_setup(self):
        for devices in ("", "Character devices:\n252 device-mapper\n", "Block devices:\ninvalid device-mapper\n",
                        "Block devices:\n252 device-mapper\n253 device-mapper\n"):
            with self.subTest(devices=devices):
                self.log.write_text("")
                self.configure_storage_stubs(devices)
                result = self.run_shell('ensure_topolvm_image() { :; }; ensure_topolvm_runtime')
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("valid device-mapper block-device major", result.stderr)
                self.assertEqual(self.commands(), ["runtime run"])

    def test_device_mapper_probe_failure_stops_before_storage_setup(self):
        self.configure_storage_stubs()
        self.env["MOCK_DMSETUP_FAILURE"] = "1"
        result = self.run_shell('ensure_topolvm_image() { :; }; ensure_topolvm_runtime')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Could not determine", result.stderr)
        self.assertEqual(self.commands(), ["runtime run"])

    def test_lvmd_uses_detected_major_and_replaces_stale_permissions(self):
        for provider in ("podman", "docker"):
            for rule in (None, "b 253:* rwm", "b 252:* rwm"):
                with self.subTest(provider=provider, rule=rule):
                    self.log.write_text("")
                    self.configure_storage_stubs(rule=rule, provider=provider)
                    result = self.run_shell('configure_topolvm_device_access; start_topolvm_lvmd')
                    self.assert_success(result)
                    commands = self.commands()
                    if rule == "b 252:* rwm":
                        self.assertNotIn("runtime rm", commands)
                        self.assertEqual(commands.count("runtime run"), 1)
                    else:
                        self.assertEqual("runtime rm" in commands, rule is not None)
                        self.assertEqual(commands.count("runtime run"), 2)
                        self.assertIn("b 252:* rwm", self.storage_run_arguments())
                        self.assertNotIn("b 253:* rwm", self.storage_run_arguments())

    def test_storage_setup_and_cleanup_use_detected_major(self):
        for operation in ("prepare_topolvm_storage", "delete_topolvm_runtime"):
            with self.subTest(operation=operation):
                self.configure_storage_stubs()
                command = f'configure_topolvm_device_access; {operation}' if operation == "prepare_topolvm_storage" else operation
                result = self.run_shell(command)
                self.assert_success(result)
                self.assertIn("b 252:* rwm", self.storage_run_arguments())
                self.assertNotIn("b 253:* rwm", self.storage_run_arguments())
                self.assertNotIn("--privileged", self.storage_run_arguments())

    def test_lvmd_replaces_image_with_udev_dependent_configuration(self):
        for provider in ("podman", "docker"):
            with self.subTest(provider=provider):
                self.log.write_text("")
                self.configure_storage_stubs(rule="b 252:* rwm", provider=provider,
                                             image="osac-dev/topolvm-lvmd:0.41.1-container")
                result = self.run_shell('configure_topolvm_device_access; start_topolvm_lvmd')
                self.assert_success(result)
                self.assertIn("runtime rm", self.commands())
                self.assertIn("osac-dev/topolvm-lvmd:0.41.1-container-v2", self.storage_run_arguments())
                self.assertIn("b 252:* rwm", self.storage_run_arguments())

    def run_create_cluster(self):
        kubeconfig = self.directory / "cluster.kubeconfig"
        return self.run_shell(f'''
ensure_topolvm_runtime() {{ echo 'topolvm prepare' >> "$MOCK_LOG"; }}
check_topolvm_cluster_mounts() {{
  echo 'topolvm check mounts' >> "$MOCK_LOG"
  [[ "$MOCK_MOUNTS_FAILURE" == 0 ]]
}}
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
                             or cmd.startswith("podman root start ")
                             or cmd.startswith("kubectl ")
                             for cmd in self.commands()))

    def test_reuse_starts_stopped_nodes_after_storage_before_exporting_kubeconfig(self):
        for provider, runtime, kind_user in (("podman", "podman root", "1"), ("docker", "docker", "0")):
            for state in ("created", "exited", "stopped"):
                with self.subTest(provider=provider, state=state):
                    self.log.write_text("")
                    self.env["KIND_EXPERIMENTAL_PROVIDER"] = provider
                    self.env["MOCK_NODE_STATE"] = state
                    self.env["MOCK_KIND_NODES"] = "osac-dev-control-plane\nosac-dev-worker"
                    self.env["MOCK_RUNNING_NODE"] = "osac-dev-worker"
                    self.assert_success(self.run_create_cluster())
                    commands = self.commands()
                    storage = commands.index("topolvm prepare")
                    start = commands.index(f"{runtime} start osac-dev-control-plane")
                    export = commands.index(f"kind {kind_user} get kubeconfig --name osac-dev")
                    ready = next(i for i, cmd in enumerate(commands) if cmd.startswith("kubectl "))
                    self.assertLess(storage, start)
                    self.assertLess(start, export)
                    self.assertLess(export, ready)
                    self.assertNotIn(f"{runtime} start osac-dev-worker", commands)
                    self.assertFalse(any(" restart " in cmd or " create cluster " in cmd
                                         or "rm -rf /kubelet/pki" in cmd for cmd in commands))
                    kubeconfig = self.directory / "cluster.kubeconfig"
                    self.assertEqual(kubeconfig.read_text(), "stub kubeconfig\n")
                    self.assertEqual(kubeconfig.stat().st_mode & 0o777, 0o600)
                    self.assertIn(f"--kubeconfig {kubeconfig} --namespace osac", commands[ready])

    def test_reuse_keeps_kubeconfig_when_node_inspection_or_start_fails(self):
        kubeconfig = self.directory / "cluster.kubeconfig"
        for failure, message in (("MOCK_NODE_INSPECT_FAILURE", "Could not inspect Kind node"),
                                 ("MOCK_NODE_START_FAILURE", "Could not start Kind node")):
            with self.subTest(failure=failure):
                self.log.write_text("")
                kubeconfig.write_text("existing kubeconfig\n")
                self.env["MOCK_NODE_INSPECT_FAILURE"] = "0"
                self.env["MOCK_NODE_START_FAILURE"] = "0"
                self.env[failure] = "1"
                self.env["MOCK_NODE_STATE"] = "exited"
                result = self.run_create_cluster()
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(message, result.stderr)
                self.assertEqual(kubeconfig.read_text(), "existing kubeconfig\n")
                self.assertNotIn("kind 1 get kubeconfig --name osac-dev", self.commands())
                self.assertFalse(any(" create cluster " in cmd or "rm -rf /kubelet/pki" in cmd
                                     for cmd in self.commands()))

    def test_reuse_rejects_unsupported_node_states(self):
        for state in ("paused", "restarting", "removing", "unknown"):
            with self.subTest(state=state):
                self.log.write_text("")
                self.env["MOCK_NODE_STATE"] = state
                result = self.run_create_cluster()
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(f"is in state '{state}'", result.stderr)
                self.assertNotIn("podman root start osac-dev-control-plane", self.commands())
                self.assertNotIn("kind 1 get kubeconfig --name osac-dev", self.commands())

    def test_reuse_checks_storage_mounts_before_starting_nodes(self):
        self.env["MOCK_NODE_STATE"] = "exited"
        self.env["MOCK_MOUNTS_FAILURE"] = "1"
        result = self.run_create_cluster()
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn("topolvm prepare", self.commands())
        self.assertNotIn("podman root start osac-dev-control-plane", self.commands())
        self.assertNotIn("kind 1 get kubeconfig --name osac-dev", self.commands())

    def test_reuse_waits_for_api_to_recover_after_starting_nodes(self):
        self.env["MOCK_NODE_STATE"] = "exited"
        self.env["MOCK_READY_FAILURES"] = "1"
        self.assert_success(self.run_create_cluster())
        probes = [cmd for cmd in self.commands() if cmd.startswith("kubectl ")]
        self.assertEqual(len(probes), 2)
        self.assertIn("--request-timeout=5s get --raw=/readyz", probes[0])
        self.assertEqual(self.commands().count("sleep 2"), 1)

    def test_reuse_fails_when_api_does_not_recover(self):
        self.env["MOCK_NODE_STATE"] = "exited"
        self.env["MOCK_READY_FAILURES"] = "30"
        result = self.run_create_cluster()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Kubernetes API did not become ready", result.stderr)
        self.assertEqual(sum(cmd.startswith("kubectl ") for cmd in self.commands()), 30)

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

    def prepare_helm_dependencies(self):
        installer = self.directory / "installer"
        infra = installer / "charts/osac-infra"
        infra.mkdir(parents=True)
        shutil.copy(INSTALLER / "Makefile", installer / "Makefile")
        shutil.copy(INSTALLER / "charts/osac-infra/Chart.yaml", infra / "Chart.yaml")
        # One pinned archive per dependency declared in Chart.yaml; the Makefile
        # derives its build targets from the same list.
        deps = []
        for line in (infra / "Chart.yaml").read_text().splitlines():
            if line.startswith("  - name:"):
                deps.append({"name": line.split()[2]})
            elif line.startswith("    version:") and deps and "version" not in deps[-1]:
                deps[-1]["version"] = line.split()[1].strip('"')
        self.archives = [infra / "charts" / f"{d['name']}-{d['version']}.tgz" for d in deps]
        self.env["MOCK_HELM_ARCHIVES"] = " ".join(str(a) for a in self.archives)
        self.archive = self.archives[0]
        self.write_command("helm", """
if [[ "$*" == 'version --short' ]]; then echo v3.19.0; exit 0; fi
printf 'helm %s\n' "$*" >> "$MOCK_LOG"
if [[ "$*" == "${MOCK_HELM_FAILURE:-}" ]]; then exit 1; fi
if [[ "$*" == 'dependency build charts/osac-infra' ]]; then
  for archive in $MOCK_HELM_ARCHIVES; do
    mkdir -p "$(dirname "$archive")"
    touch "$archive"
  done
fi
""")
        return installer

    def test_general_helm_dependencies_do_not_download_topolvm(self):
        installer = self.prepare_helm_dependencies()
        result = subprocess.run(
            ["make", "--no-print-directory", "-C", str(installer),
             "helm-deps", "CONTAINER_TOOL=podman"],
            env=self.env, text=True, capture_output=True, timeout=15,
        )
        self.assert_success(result)
        self.assertFalse(self.archive.exists())
        self.assertFalse(any("topolvm" in cmd or "charts/osac-infra" in cmd
                             for cmd in self.commands()))

    def test_helm_lint_downloads_missing_topolvm_and_reuses_it(self):
        installer = self.prepare_helm_dependencies()
        # Stale archives must not satisfy the versions pinned in Chart.yaml.
        self.archive.parent.mkdir()
        for archive in self.archives:
            (archive.parent / f"{archive.name.rsplit('-', 1)[0]}-0.0.0.tgz").touch()
        for _ in range(2):
            result = subprocess.run(
                ["make", "--no-print-directory", "-C", str(installer),
                 "helm-lint", "CONTAINER_TOOL=podman"],
                env=self.env, text=True, capture_output=True, timeout=15,
            )
            self.assert_success(result)
            self.assertTrue(self.archive.is_file())
        self.assertEqual(self.commands().count("helm dependency build charts/osac-infra"), 1)
        self.assertEqual(self.commands().count(
            "helm repo add topolvm https://topolvm.github.io/topolvm --force-update"), 1)
        self.assertFalse(any("dependency update charts/osac-infra" in cmd for cmd in self.commands()))

    def test_topolvm_dependency_download_failure_stops_install(self):
        installer = self.prepare_helm_dependencies()
        for command in ("repo add topolvm https://topolvm.github.io/topolvm --force-update",
                        "dependency build charts/osac-infra"):
            with self.subTest(command=command):
                self.env["MOCK_HELM_FAILURE"] = command
                result = subprocess.run(
                    ["make", "--no-print-directory", "-C", str(installer), "install-infra",
                     "PLATFORM=kind", "PROFILE=dev-full", "NS=osac", "CONTAINER_TOOL=podman"],
                    env=self.env, text=True, capture_output=True, timeout=15,
                )
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(f"helm {command}", self.commands())
                self.assertFalse(self.archive.exists())
                self.assertFalse(any("upgrade --install" in cmd for cmd in self.commands()))

    def test_installer_helm_apply_flags_match_each_release(self):
        for version in ("v3.19.0", "v4.2.2"):
            self.write_command("helm", f"echo {version}")
            for target, release in (("install-devstack", "osac-devstack"), ("install-osac", "osac")):
                with self.subTest(version=version, target=target):
                    result = subprocess.run(
                        ["make", "--no-print-directory", "-n", "-C", str(INSTALLER),
                         "-o", "helm-devstack-deps", target, "PLATFORM=kind",
                         "PROFILE=dev-full", "NS=osac"],
                        env=self.env, text=True, capture_output=True, timeout=15,
                    )
                    self.assert_success(result)
                    commands = [shlex.split(line) for line in result.stdout.replace("\\\n", " ").splitlines()
                                if line.startswith("helm upgrade --install ")]
                    self.assertEqual(len(commands), 1, result.stdout)
                    args = commands[0]
                    self.assertEqual(args[3], release)
                    if release == "osac-devstack":
                        self.assertNotIn("--force-conflicts", args)
                        if version.startswith("v4."):
                            self.assertIn("--server-side=false", args)
                            self.assertEqual(args[args.index("--post-renderer") + 1], "osac-dev-full-awx-image")
                        else:
                            self.assertFalse(any(arg.startswith("--server-side") for arg in args))
                    else:
                        self.assertNotIn("--server-side=false", args)
                        self.assertEqual("--force-conflicts" in args, version.startswith("v4."))

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

    def test_macos_image_load_uses_a_portable_temporary_file(self):
        self.env["MOCK_OS"] = "Darwin"
        self.env["KIND_EXPERIMENTAL_PROVIDER"] = "docker"
        self.write_command("mktemp", '''
[[ "${!#}" == *XXXXXX ]] || { echo 'invalid BSD mktemp template' >&2; exit 1; }
exec /usr/bin/mktemp "$@"
''')
        self.assert_success(self.run_image_load_target("kind-load-image", "dev-full", "docker"))
        self.assertIn("docker save test/operator:latest", self.commands())
        self.assertTrue(any(cmd.startswith("kind 0 load image-archive ") for cmd in self.commands()))

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
