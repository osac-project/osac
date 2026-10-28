"""Exercise Carbonite Make targets without building images or contacting a gateway.

Run: python3 tools/test/test_carbonite_make.py
"""

import json
import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]

CLIENT = r'''#!/usr/bin/env python3
import json
import os
import sys
from pathlib import Path

args = sys.argv[1:]
tool = Path(sys.argv[0]).name
with Path(os.environ["OSAC_CARB_LOG"]).open("a") as log:
    log.write(json.dumps([tool, *args]) + "\n")
if tool == "engine":
    volume_name = args[2] if args[:2] == ["volume", "inspect"] else args[-1]
    volume_path = Path(os.environ[
        "OSAC_CARB_KUBE_VOLUME_STATE" if volume_name.endswith("-kubeconfig")
        else "OSAC_CARB_VOLUME_STATE"
    ])
    if args[:2] == ["volume", "create"] and not volume_path.exists():
        opts = [args[i + 1].split("=", 1) for i, arg in enumerate(args) if arg == "--opt"]
        labels = [args[i + 1].split("=", 1) for i, arg in enumerate(args) if arg == "--label"]
        volume_path.write_text(json.dumps({"Options": dict(opts), "Labels": dict(labels)}))
    elif args[:2] == ["volume", "inspect"]:
        print(volume_path.read_text())
    elif args == ["info"] and os.environ.get("OSAC_CARB_FAIL_ENGINE"):
        print("container engine unavailable", file=sys.stderr)
        sys.exit(1)
    sys.exit(1 if os.environ.get("OSAC_CARB_FAIL_BUILD") and args[0] == "build" else 0)
if args[4:] == ["status", "--output", "json"]:
    assert args[0] == "--gateway" and args[2] == "--workspace", args
    if os.environ.get("OSAC_CARB_FAIL_STATUS"):
        print("gateway unavailable", file=sys.stderr)
        sys.exit(1)
    print(os.environ.get("OSAC_CARB_STATUS", json.dumps({
        "authentication": {"provider": "mTLS transport", "status": "authenticated"},
        "gateway": args[1], "server": "https://127.0.0.1:17670",
        "status": "connected", "version": "0.1.2",
    })))
    sys.exit(0)
if tool == "distrobox":
    sys.exit(0)
assert args[0] == "--gateway" and args[2] == "--workspace" and args[4] == "sandbox", args
command, options = args[5], args[6:]
state_path = Path(os.environ["OSAC_CARB_STATE"])
state = json.loads(state_path.read_text())
def option(key):
    return options[options.index(key) + 1]
if command == "list":
    if os.environ.get("OSAC_CARB_BARE_LIST"):
        print("{}")
        sys.exit(0)
    assert "--limit" not in options
    assert option("--page-size") == "1000"
    if os.environ.get("OSAC_CARB_FAIL_LIST"):
        print("gateway unavailable", file=sys.stderr)
        sys.exit(1)
    if os.environ.get("OSAC_CARB_PAGINATE") and not option("--page-token"):
        print(json.dumps({"sandboxes": [{"name": "unrelated", "phase": "Ready"}], "next_page_token": "next"}))
    else:
        print(json.dumps({
            "sandboxes": [{"name": name, "phase": phase} for name, phase in state.items()],
            "next_page_token": "",
        }))
        state = {name: phase for name, phase in state.items() if phase != "Deleting"}
elif command == "create":
    name = option("--name")
    assert name not in state, "Duplicate sandbox creation"
    assert option("--output") == "json", "Creation must detach after provisioning"
    assert "--detach" in options, "A trailing command with JSON output must detach"
    state[name] = "Ready"
elif command == "upload":
    assert options[0] in state
    assert state[options[0]] == "Ready", "Upload before readiness"
    if os.environ.get("OSAC_CARB_FAIL_UPLOAD"):
        print("upload failed", file=sys.stderr)
        sys.exit(1)
elif command in ("start", "stop", "delete"):
    name = options[0]
    assert name in state
    state[name] = {"start": "Ready", "stop": "Stopped", "delete": "Deleting"}[command]
elif command in ("exec", "get"):
    name = option("--name") if command == "exec" else options[0]
    assert name in state
    if command == "exec":
        assert state[name] == "Ready", "Exec before readiness"
        if os.environ.get("OSAC_CARB_FAIL_KUBE_PROBE") and any(
            "/sandbox/kube/config" in part for part in options
        ):
            sys.exit(32)
        sys.exit(int(os.environ.get("OSAC_CARB_EXEC_EXIT", "0")))
else:
    raise AssertionError(command)
state_path.write_text(json.dumps(state))
'''


class CarboniteMakeTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="osac-carb-make-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.checkout = self.root / "checkout with spaces"
        self.checkout.mkdir()
        shutil.copy(REPO_ROOT / "Makefile", self.checkout / "Makefile")
        self.state = self.root / "state.json"
        self.state.write_text("{}")
        self.log = self.root / "calls.jsonl"
        self.volume = self.root / "volume.json"
        self.kube_volume = self.root / "kube-volume.json"
        self.home = self.root / "host home"
        self.home.mkdir()
        self.state_home = self.root / "state home"
        self.state_dir = self.state_home / "osac-carbonite-kind"
        self.default_kube_dir = self.state_dir / "kube"
        self.default_kube_dir.mkdir(parents=True, mode=0o700)
        self.policy = self.state_dir / "policy-kind.yaml"
        self.policy.write_text("version: 1\n")
        default_kubeconfig = self.default_kube_dir / "config"
        default_kubeconfig.write_text("apiVersion: v1\nkind: Config\n")
        default_kubeconfig.chmod(0o600)
        for name in ("engine", "openshell", "distrobox"):
            client = self.root / name
            client.write_text(CLIENT)
            client.chmod(0o755)
        self.env = {
            **os.environ,
            "PATH": f"{self.root}{os.pathsep}{os.environ.get('PATH', '')}",
            "OSAC_CARB_LOG": str(self.log),
            "OSAC_CARB_STATE": str(self.state),
            "OSAC_CARB_VOLUME_STATE": str(self.volume),
            "OSAC_CARB_KUBE_VOLUME_STATE": str(self.kube_volume),
            "HOME": str(self.home),
            "XDG_STATE_HOME": str(self.state_home),
        }

    def install_manager(self):
        manager = self.home / ".local/libexec/osac-host-build-manager"
        manager.parent.mkdir(parents=True)
        manager.write_text('''#!/usr/bin/env python3
import json
import os
import sys
from pathlib import Path
with Path(os.environ["OSAC_CARB_LOG"]).open("a") as log:
    log.write(json.dumps(["manager", *sys.argv[1:]]) + "\\n")
if os.environ.get("OSAC_CARB_FAIL_MANAGER"):
    sys.exit(44)
''')
        manager.chmod(0o700)
        return manager

    def write_host_shim(self, name, body):
        shim = self.root / name
        shim.write_text(
            '#!/usr/bin/env python3\n'
            'import json\n'
            'import os\n'
            'import sys\n'
            'from pathlib import Path\n'
            'with Path(os.environ["OSAC_CARB_LOG"]).open("a") as log:\n'
            f'    log.write(json.dumps(["{name}", *sys.argv[1:]]) + "\\n")\n'
            f'{body}\n')
        shim.chmod(0o755)
        return shim

    def install_fix_carb(self, pinned="192.168.1.242"):
        tools = self.checkout / "tools/carbonite"
        tools.mkdir(parents=True)
        shutil.copy(REPO_ROOT / "tools/carbonite/update-host-gateway-ip.sh",
                    tools / "update-host-gateway-ip.sh")
        self.write_host_shim(
            "ip", 'print("1.1.1.1 via 10.0.0.1 dev eth0 src 192.168.4.5")')
        self.write_host_shim("systemctl", 'print("active")')
        config = self.home / ".config/openshell/gateway.toml"
        config.parent.mkdir(parents=True)
        config.write_text(
            '[openshell]\n'
            'compute_driver = "podman"\n'
            '\n'
            '[openshell.drivers.podman]\n'
            f'host_gateway_ip = "{pinned}"\n'
            'health_check_interval_secs = 10\n')
        return config

    def make(self, *args, succeeds=True, container_cmd=None, openshell_cmd=None, policy=None, **env):
        result = subprocess.run(
            [
                shutil.which("make"),
                "--no-print-directory",
                *args,
                f"CONTAINER_CMD={container_cmd or self.root / 'engine'}",
                f"OPENSHELL_CMD={openshell_cmd or self.root / 'openshell'}",
                *([f"CARBONITE_POLICY={policy}"] if policy else []),
            ],
            cwd=self.checkout,
            env={**self.env, **env},
            text=True,
            capture_output=True,
            timeout=15,
        )
        if succeeds:
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        else:
            self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        return result

    def calls(self):
        if not self.log.exists():
            return []
        return [json.loads(line) for line in self.log.read_text().splitlines()]

    def operations(self):
        operations = []
        for call in self.calls():
            if call[0] == "engine":
                if call[1:] != ["info"]:
                    operations.append("-".join(call[1:3]) if call[1] == "volume" else call[1])
            elif call[0] == "openshell":
                if call[5] != "status":
                    operations.append(call[6])
            elif call[0] == "distrobox":
                operations.append(call[1])
        return operations

    def interactive_execs(self):
        return [
            call for call in self.calls()
            if call[0] == "openshell" and call[6] == "exec" and "--tty" in call
        ]

    def test_check_is_read_only_and_uses_the_selected_gateway(self):
        self.make("check-carb", "CARBONITE_GATEWAY=other-local", "CARBONITE_WORKSPACE=development")
        self.assertEqual(json.loads(self.state.read_text()), {})
        self.assertEqual(self.operations(), [])
        self.assertIn([
            "openshell", "--gateway", "other-local", "--workspace", "development",
            "status", "--output", "json",
        ], self.calls())

    def test_check_reports_optional_host_build_manager_when_absent(self):
        result = self.make("check-carb")
        self.assertIn("Carbonite host-build manager not installed", result.stdout)
        self.assertIn("host builds are optional", result.stdout)

    def test_check_validates_installed_host_build_manager_binding(self):
        self.install_manager()
        result = self.make(
            "check-carb",
            "CARBONITE_GATEWAY=other-local",
            "CARBONITE_WORKSPACE=development",
        )
        self.assertIn([
            "manager", "status", "--gateway", "other-local", "--workspace", "development",
            "--sandbox", "osac-carbonite-kind", "--checkout", str(self.checkout),
        ], self.calls())
        self.assertIn("Carbonite host-build manager OK", result.stdout)

    def test_check_rejects_unsafe_host_build_manager_installations(self):
        manager = self.home / ".local/libexec/osac-host-build-manager"
        manager.parent.mkdir(parents=True)
        target = self.root / "manager-target"
        target.write_text("#!/bin/sh\nexit 0\n")
        target.chmod(0o755)
        manager.symlink_to(target)
        result = self.make("check-carb", succeeds=False)
        self.assertIn("must not be a symlink", result.stderr)

        manager.unlink()
        manager.write_text("#!/bin/sh\nexit 0\n")
        manager.chmod(0o644)
        result = self.make("check-carb", succeeds=False)
        self.assertIn("not an executable file", result.stderr)

    def test_check_fails_when_host_build_manager_status_fails(self):
        self.install_manager()
        result = self.make("check-carb", succeeds=False, OSAC_CARB_FAIL_MANAGER="1")
        self.assertIn("Carbonite host-build manager check failed", result.stderr)
        self.assertEqual(self.calls()[0][:2], ["manager", "status"])

    def test_missing_commands_fail_before_building(self):
        for variable in ("container_cmd", "openshell_cmd"):
            with self.subTest(variable=variable):
                self.log.write_text("")
                missing = str(self.root / "not-installed")
                result = self.make("-j4", "enter-carb", succeeds=False, **{variable: missing})
                self.assertIn(missing, result.stderr)
                self.assertEqual(self.operations(), [])
        # An empty search path with only make isolates the missing jq dependency.
        commands = self.root / "commands"
        commands.mkdir()
        (commands / "make").symlink_to(shutil.which("make"))
        self.log.write_text("")
        result = self.make("enter-carb", succeeds=False, PATH=str(commands))
        self.assertIn("jq", result.stderr)
        self.assertEqual(self.operations(), [])

    def test_engine_failure_prevents_build_or_sandbox_deletion(self):
        self.state.write_text('{"osac-carbonite-kind": "Ready"}')
        for target in ("image-carb", "enter-carb", "rebuild-carb"):
            with self.subTest(target=target):
                self.log.write_text("")
                self.make("-j4", target, succeeds=False, OSAC_CARB_FAIL_ENGINE="1")
                self.assertEqual(self.operations(), [])
                self.assertEqual(json.loads(self.state.read_text()), {"osac-carbonite-kind": "Ready"})

    def test_gateway_failure_prevents_build_and_sandbox_operations(self):
        self.state.write_text('{"osac-carbonite-kind": "Ready"}')
        for target in ("enter-carb", "claude-carb", "stop-carb", "rm-carb", "rebuild-carb", "status-carb"):
            with self.subTest(target=target):
                self.log.write_text("")
                result = self.make("-j4", target, succeeds=False, OSAC_CARB_FAIL_STATUS="1")
                self.assertIn("openshell", result.stderr)
                self.assertEqual(self.operations(), [])
                self.assertEqual(json.loads(self.state.read_text()), {"osac-carbonite-kind": "Ready"})

    def test_gateway_status_fails_closed_even_with_a_zero_exit_code(self):
        for status in (
            '{"status": "disconnected", "authentication": {"status": "authenticated"}}',
            '{"status": "connected", "authentication": {"status": "unauthenticated"}}',
            '{}', 'invalid json',
        ):
            with self.subTest(status=status):
                self.log.write_text("")
                self.make("enter-carb", succeeds=False, OSAC_CARB_STATUS=status)
                self.assertEqual(self.operations(), [])

    def test_image_build_does_not_require_openshell_or_a_gateway(self):
        self.make("image-carb", openshell_cmd=str(self.root / "not-installed"), OSAC_CARB_FAIL_STATUS="1")
        self.assertEqual(self.operations(), ["build"])
        self.assertTrue(any(call[1:] == ["info"] for call in self.calls()))

    def test_all_counterparts_are_listed_and_help_remains_default(self):
        result = self.make()
        for target in ("image", "enter", "claude", "stop", "rm", "rebuild", "status"):
            self.assertIn(f"{target}-carb", result.stdout)
        self.assertNotIn("help-carb", result.stdout)
        self.assertEqual(self.calls(), [])

    def test_selector_dispatches_every_ordinary_target_to_carbonite(self):
        for target in ("image", "enter", "claude", "stop", "rm", "rebuild", "status"):
            with self.subTest(target=target):
                self.log.write_text("")
                self.make(target, "USE_CARBONITE=true")
                calls = self.calls()
                self.assertEqual([call for call in calls if call[0] == "distrobox"], [])
                if target == "image":
                    self.assertTrue(any(
                        call[0] == "engine" and call[1] == "build" and "osac-dev-carbonite" in call
                        for call in calls
                    ))
                else:
                    self.assertTrue(any(call[0] == "openshell" for call in calls))

    def test_selector_defaults_to_distrobox(self):
        for target in ("image", "enter", "claude", "stop", "rm", "rebuild", "status"):
            with self.subTest(target=target):
                self.log.write_text("")
                self.make(target)
                calls = self.calls()
                self.assertEqual([call for call in calls if call[0] == "openshell"], [])
                if target == "image":
                    self.assertTrue(any(
                        call[0] == "engine" and call[1] == "build" and "osac-dev" in call
                        for call in calls
                    ))
                else:
                    self.assertTrue(any(call[0] == "distrobox" for call in calls))

    def test_environment_selects_carbonite_and_command_line_false_wins(self):
        self.make("status", OSAC_USE_CARBONITE="true")
        self.assertTrue(any(call[0] == "openshell" for call in self.calls()))
        self.log.write_text("")
        self.make("status", "USE_CARBONITE=true", OSAC_USE_CARBONITE="false")
        self.assertTrue(any(call[0] == "openshell" for call in self.calls()))
        self.log.write_text("")
        self.make("status", "USE_CARBONITE=true", OSAC_USE_CARBONITE="maybe")
        self.assertTrue(any(call[0] == "openshell" for call in self.calls()))
        self.log.write_text("")
        self.make("status", "USE_CARBONITE=false", OSAC_USE_CARBONITE="true")
        calls = self.calls()
        self.assertEqual([call for call in calls if call[0] == "openshell"], [])
        self.assertTrue(any(call[0] == "distrobox" for call in calls))

    def test_explicit_carbonite_target_ignores_the_selector(self):
        self.make("status-carb", "USE_CARBONITE=false", OSAC_USE_CARBONITE="false")
        calls = self.calls()
        self.assertEqual([call for call in calls if call[0] == "distrobox"], [])
        self.assertTrue(any(call[0] == "openshell" for call in calls))

    def test_carbonite_entry_defaults_to_kind_access(self):
        self.make("enter-carb")

        create = next(call for call in self.calls() if call[0] == "openshell" and call[6] == "create")
        self.assertEqual(create[create.index("--name") + 1], "osac-carbonite-kind")
        self.assertIn("KUBECONFIG=/sandbox/kube/config", create)
        mounts = json.loads(create[create.index("--driver-config-json") + 1])["podman"]["mounts"]
        self.assertEqual(mounts[1], {
            "type": "volume",
            "source": "osac-carbonite-kind-kubeconfig",
            "target": "/sandbox/kube",
            "read_only": True,
        })

    def test_selector_rejects_invalid_values(self):
        for variable in ("USE_CARBONITE", "OSAC_USE_CARBONITE"):
            with self.subTest(variable=variable):
                self.log.write_text("")
                result = self.make("status", succeeds=False, **{variable: "maybe"})
                self.assertIn("USE_CARBONITE must be true or false", result.stderr)
                self.assertEqual(self.calls(), [])

    def test_enter_creates_once_and_reuses_the_project_bind(self):
        self.make("enter-carb")
        self.make("enter-carb")
        self.assertEqual(self.operations().count("create"), 1)
        create = next(
            call for call in self.calls()
            if call[0] == "openshell" and call[6] == "create"
        )
        self.assertEqual(create[create.index("--from") + 1], "osac-dev-carbonite:latest")
        self.assertEqual(create[create.index("--policy") + 1], str(self.policy))
        mount = json.loads(create[create.index("--driver-config-json") + 1])["podman"]["mounts"]
        self.assertEqual(mount, [{
            "type": "volume",
            "source": "osac-carbonite-kind-workspace",
            "target": "/sandbox/osac",
            "read_only": False,
        }, {
            "type": "volume",
            "source": "osac-carbonite-kind-kubeconfig",
            "target": "/sandbox/kube",
            "read_only": True,
        }])
        volume = json.loads(self.volume.read_text())
        self.assertEqual(volume["Options"]["device"], str(self.checkout))
        self.assertEqual(volume["Labels"]["openshell.ai/sandbox-attachable"], "true")
        relabel = next(
            call for call in self.calls()
            if call[:2] == ["engine", "run"] and f"{self.checkout}:/project:z" in call
        )
        self.assertEqual(relabel[relabel.index("--volume") + 1], f"{self.checkout}:/project:z")
        self.assertEqual(self.calls()[-1][-2:], ["/bin/bash", "-il"])

    def test_kind_kubeconfig_is_mounted_read_only_and_selected_for_kubectl(self):
        kube_dir = self.root / "kind credentials"
        kube_dir.mkdir(mode=0o700)
        config = kube_dir / "config"
        config.write_text("apiVersion: v1\nkind: Config\n")
        config.chmod(0o600)

        self.make("enter-carb", f"CARBONITE_KUBECONFIG_DIR={kube_dir}", "CARBONITE_WORKSPACE=kind-dev")

        create = next(call for call in self.calls() if call[0] == "openshell" and call[6] == "create")
        mounts = json.loads(create[create.index("--driver-config-json") + 1])["podman"]["mounts"]
        self.assertEqual(mounts[1], {
            "type": "volume", "source": "osac-carbonite-kind-kubeconfig",
            "target": "/sandbox/kube", "read_only": True,
        })
        volume = json.loads(self.kube_volume.read_text())
        self.assertEqual(volume["Options"]["device"], str(kube_dir))
        self.assertEqual(volume["Labels"]["openshell.ai/sandbox-attachable"], "true")
        self.assertEqual(volume["Labels"]["openshell.ai/sandbox-attachable-workspace"], "kind-dev")
        kube_probe = next(
            call for call in self.calls()
            if call[:2] == ["engine", "run"] and any("/sandbox/kube" in arg for arg in call)
        )
        self.assertIn("--userns=keep-id:uid=1000,gid=1000", kube_probe)
        self.assertIn(f"{kube_dir}:/sandbox/kube:ro,z", kube_probe)
        self.assertIn("KUBECONFIG=/sandbox/kube/config", create)

    def test_kind_kubeconfig_rejects_missing_or_checkout_owned_credentials_before_mounting(self):
        inside = self.checkout / "credentials"
        inside.mkdir(mode=0o700)
        (inside / "config").write_text("apiVersion: v1\n")
        (inside / "config").chmod(0o600)
        for kube_dir in (self.root / "missing", inside):
            with self.subTest(kube_dir=kube_dir):
                self.log.write_text("")
                self.make("enter-carb", f"CARBONITE_KUBECONFIG_DIR={kube_dir}", succeeds=False)
                self.assertFalse(any(call[1:3] == ["volume", "create"] for call in self.calls()))
                self.assertFalse(any(call[0] == "openshell" and call[6] == "create" for call in self.calls()))

    def test_kind_kubeconfig_volume_with_other_source_is_rejected(self):
        kube_dir = self.root / "kind credentials"
        kube_dir.mkdir(mode=0o700)
        (kube_dir / "config").write_text("apiVersion: v1\n")
        (kube_dir / "config").chmod(0o600)
        self.kube_volume.write_text(json.dumps({
            "Options": {"device": "/different/credentials", "type": "none", "o": "bind"},
            "Labels": {
                "openshell.ai/sandbox-attachable": "true",
                "openshell.ai/sandbox-attachable-workspace": "osac-kind",
            },
        }))

        self.make("enter-carb", f"CARBONITE_KUBECONFIG_DIR={kube_dir}", succeeds=False)
        self.assertFalse(any(call[0] == "openshell" and call[6] == "create" for call in self.calls()))

    def test_kind_access_requires_host_only_policy(self):
        kube_dir = self.root / "kind credentials"
        kube_dir.mkdir(mode=0o700)
        (kube_dir / "config").write_text("apiVersion: v1\n")
        (kube_dir / "config").chmod(0o600)
        project_policy = self.checkout / "policy.yaml"
        project_policy.write_text("version: 1\n")

        self.make(
            "enter-carb", f"CARBONITE_KUBECONFIG_DIR={kube_dir}",
            policy=project_policy, succeeds=False,
        )
        self.assertFalse(any(call[1:3] == ["volume", "create"] for call in self.calls()))
        self.assertFalse(any(call[0] == "openshell" and call[6] == "create" for call in self.calls()))

    def test_kind_kubeconfig_rejects_a_colon_in_host_source_path(self):
        kube_dir = self.root / "credentials:unexpected-volume-field"
        kube_dir.mkdir(mode=0o700)
        (kube_dir / "config").write_text("apiVersion: v1\n")
        (kube_dir / "config").chmod(0o600)

        self.make("enter-carb", f"CARBONITE_KUBECONFIG_DIR={kube_dir}", succeeds=False)
        self.assertFalse(any(call[1:3] == ["volume", "create"] for call in self.calls()))

    def test_unreadable_sandbox_kubeconfig_blocks_shell_entry(self):
        kube_dir = self.root / "kind credentials"
        kube_dir.mkdir(mode=0o700)
        (kube_dir / "config").write_text("apiVersion: v1\n")
        (kube_dir / "config").chmod(0o600)

        result = self.make(
            "enter-carb", f"CARBONITE_KUBECONFIG_DIR={kube_dir}",
            succeeds=False, OSAC_CARB_FAIL_KUBE_PROBE="1",
        )
        self.assertIn("mounted kubeconfig", result.stderr)
        self.assertFalse(any(call[-2:] == ["/bin/bash", "-il"] for call in self.calls()))

    def test_opt_in_manager_prepares_once_after_sandbox_ready_before_entry(self):
        self.install_manager()
        self.make("enter-carb", "CARBONITE_GATEWAY=local gateway", "CARBONITE_WORKSPACE=dev space")
        calls = self.calls()
        manager_calls = [call for call in calls if call[0] == "manager"]
        self.assertEqual(manager_calls, [[
            "manager", "prepare", "--gateway", "local gateway",
            "--workspace", "dev space", "--sandbox", "osac-carbonite-kind",
            "--checkout", str(self.checkout),
        ]])
        self.assertLess(next(i for i, call in enumerate(calls) if call[6:7] == ["create"]),
                        calls.index(manager_calls[0]))
        self.assertLess(calls.index(manager_calls[0]), calls.index(self.interactive_execs()[0]))

    def test_opt_in_manager_prepares_claude_and_restarted_sandbox(self):
        self.install_manager()
        self.state.write_text('{"osac-carbonite-kind": "Stopped"}')
        self.make("claude-carb")
        calls = self.calls()
        self.assertEqual(sum(call[0] == "manager" for call in calls), 1)
        self.assertLess(next(i for i, call in enumerate(calls) if call[6:7] == ["start"]),
                        next(i for i, call in enumerate(calls) if call[0] == "manager"))
        self.assertLess(next(i for i, call in enumerate(calls) if call[0] == "manager"),
                        len(calls) - 1)

    def test_opt_in_manager_failure_blocks_shell_entry(self):
        self.install_manager()
        result = self.make("enter-carb", succeeds=False, OSAC_CARB_FAIL_MANAGER="1")
        self.assertEqual(sum(call[0] == "manager" for call in self.calls()), 1)
        self.assertEqual(self.interactive_execs(), [])
        self.assertIn("Error 44", result.stderr)

    def test_manager_failure_points_at_the_troubleshooting_guide(self):
        self.install_manager()
        result = self.make("enter-carb", succeeds=False, OSAC_CARB_FAIL_MANAGER="1")
        self.assertIn("network change", result.stderr)
        self.assertIn("carbonite-host-build.md", result.stderr)

    def test_fix_carb_repins_and_restarts_the_gateway(self):
        config = self.install_fix_carb()

        result = self.make("fix-carb")

        updated = config.read_text()
        self.assertIn(["ip", "-4", "route", "get", "1.1.1.1"], self.calls())
        self.assertIn('host_gateway_ip = "192.168.4.5"', updated)
        self.assertIn("compute_driver = ", updated)
        self.assertIn("health_check_interval_secs = 10", updated)
        self.assertIn(["systemctl", "--user", "restart", "openshell-gateway"], self.calls())
        self.assertIn(["systemctl", "--user", "is-active", "openshell-gateway"], self.calls())
        self.assertIn("make enable-carb", result.stdout)
        self.assertIn("make rebuild-carb", result.stdout)
        backup = config.with_name(config.name + ".before-host-gateway-ip")
        self.assertIn('host_gateway_ip = "192.168.1.242"', backup.read_text())

    def test_fix_carb_skips_restart_when_address_unchanged(self):
        config = self.install_fix_carb(pinned="192.168.4.5")

        result = self.make("fix-carb")

        self.assertEqual([], [call for call in self.calls() if call[0] == "systemctl"])
        self.assertIn('host_gateway_ip = "192.168.4.5"', config.read_text())
        self.assertIn("already", result.stdout)

    def test_fix_and_enable_select_carbonite_when_use_carbonite_is_set(self):
        config = self.install_fix_carb()
        self.install_manager()

        self.make("fix", "USE_CARBONITE=true")
        self.make("enable", "USE_CARBONITE=true")

        self.assertIn('host_gateway_ip = "192.168.4.5"', config.read_text())
        self.assertIn(["manager", "enable", "--gateway", "openshell",
                       "--workspace", "osac-kind", "--sandbox", "osac-carbonite-kind",
                       "--checkout", str(self.checkout)], self.calls())

    def test_fix_and_enable_are_informational_for_distrobox(self):
        for target in ("fix", "enable"):
            with self.subTest(target=target):
                self.log.write_text("")
                result = self.make(target)
                self.assertIn("Distrobox", result.stdout)
                self.assertEqual([], self.calls())

    def test_enable_carb_runs_the_manager_with_the_bound_binding(self):
        self.install_manager()
        self.make("enable-carb", "CARBONITE_GATEWAY=local gateway",
                  "CARBONITE_WORKSPACE=dev space")
        self.assertIn(["manager", "enable", "--gateway", "local gateway",
                       "--workspace", "dev space", "--sandbox", "osac-carbonite-kind",
                       "--checkout", str(self.checkout)], self.calls())

    def test_enable_carb_failure_fails_the_target(self):
        self.install_manager()
        self.make("enable-carb", succeeds=False, OSAC_CARB_FAIL_MANAGER="1")
        self.assertEqual(1, sum(call[0] == "manager" for call in self.calls()))

    def test_enable_carb_without_an_installed_manager_is_an_optional_noop(self):
        result = self.make("enable-carb")
        self.assertIn("host builds are optional", result.stdout)
        self.assertEqual([], [call for call in self.calls() if call[0] == "manager"])

    def test_enable_carb_rejects_unsafe_manager_files(self):
        manager = self.install_manager()
        manager.chmod(0o600)
        self.make("enable-carb", succeeds=False)
        manager.unlink()
        manager.symlink_to(self.root / "engine")
        self.make("enable-carb", succeeds=False)

    def test_non_executable_or_symlinked_manager_fails_closed(self):
        manager = self.install_manager()
        manager.chmod(0o600)
        self.make("enter-carb", succeeds=False)
        self.assertEqual(self.interactive_execs(), [])
        manager.unlink()
        manager.symlink_to(self.root / "engine")
        self.log.write_text("")
        self.make("enter-carb", succeeds=False)
        self.assertEqual(self.interactive_execs(), [])

    def test_stopped_sandbox_restarts_and_claude_arguments_are_preserved(self):
        self.state.write_text('{"osac-carbonite-kind": "Stopped"}')
        self.make("claude-carb", 'ARGS=--model sonnet -p "hello world"')
        self.assertEqual(self.operations(), ["build", "list", "start", "exec", "exec"])
        self.assertEqual(self.calls()[-1][-4:], ["--model", "sonnet", "-p", "hello world"])

    def test_existing_sandbox_on_a_later_page_is_reused(self):
        self.state.write_text('{"osac-carbonite-kind": "Ready"}')
        self.make("enter-carb", OSAC_CARB_PAGINATE="1")
        self.assertEqual(self.operations(), ["build", "list", "list", "exec", "exec"])
        self.assertNotIn("create", self.operations())

    def test_volume_with_a_different_source_cannot_be_attached(self):
        self.volume.write_text('{"Options": {"device": "/other/checkout"}, "Labels": {}}')
        self.make("enter-carb", succeeds=False)
        self.assertNotIn("create", self.operations())
        self.assertNotIn("exec", self.operations())

    def test_stop_preserves_workspace_and_remove_waits_for_deletion(self):
        self.state.write_text('{"osac-carbonite-kind": "Ready"}')
        self.make("stop-carb")
        self.make("stop-carb")
        self.assertEqual(json.loads(self.state.read_text()), {"osac-carbonite-kind": "Stopped"})
        self.make("rm-carb")
        self.make("rm-carb")
        self.assertEqual(json.loads(self.state.read_text()), {})
        self.assertEqual(self.operations().count("stop"), 1)
        self.assertEqual(self.operations().count("delete"), 1)

    def test_parallel_rebuild_deletes_before_building_and_entering(self):
        self.state.write_text('{"osac-carbonite-kind": "Ready"}')
        self.make("-j4", "rebuild-carb")
        operations = self.operations()
        self.assertLess(operations.index("delete"), operations.index("build"))
        self.assertLess(operations.index("build"), operations.index("create"))
        self.assertLess(operations.index("create"), operations.index("exec"))
        self.assertEqual(json.loads(self.state.read_text()), {"osac-carbonite-kind": "Ready"})

    def test_failures_do_not_mutate_or_enter_an_unavailable_sandbox(self):
        for target in ("enter-carb", "stop-carb", "rm-carb", "status-carb"):
            with self.subTest(target=target):
                self.log.write_text("")
                self.make(target, succeeds=False, OSAC_CARB_FAIL_LIST="1")
                self.assertFalse(
                    set(self.operations()) & {"create", "start", "stop", "delete", "exec"}
                )
        self.state.write_text('{"osac-carbonite-kind": "Error"}')
        self.log.write_text("")
        self.make("enter-carb", succeeds=False)
        self.assertEqual(self.operations(), ["build", "list"])
        self.state.write_text("{}")
        self.policy.unlink()
        self.make("enter-carb", succeeds=False)
        self.assertNotIn("create", self.operations())

    def test_custom_options_and_status_use_the_selected_sandbox(self):
        options = (
            "CARBONITE_NAME=other-carb",
            "CARBONITE_GATEWAY=other-local",
            "CARBONITE_IMAGE_NAME=localhost/project:dev",
            "CARBONITE_WORKDIR=/sandbox/project",
            "CARBONITE_CREATE_ARGS=--provider approved-provider",
        )
        self.make("claude-carb", *options)
        create = next(
            call for call in self.calls()
            if call[0] == "openshell" and call[6] == "create"
        )
        self.assertEqual(create[create.index("--from") + 1], "localhost/project:dev")
        self.assertEqual(create[create.index("--provider") + 1], "approved-provider")
        execution = self.calls()[-1]
        self.assertEqual(execution[execution.index("--workdir") + 1], "/sandbox/project")
        self.make("status-carb", *options)
        self.assertEqual(self.calls()[-1], [
            "openshell", "--gateway", "other-local", "--workspace", "osac-kind",
            "sandbox", "get", "other-carb",
        ])
        self.make("claude-carb", *options, succeeds=False, OSAC_CARB_EXEC_EXIT="23")


    def install_render_policy(self):
        tools = self.checkout / "tools/carbonite"
        tools.mkdir(parents=True, exist_ok=True)
        shutil.copy(REPO_ROOT / "tools/carbonite/render-policy.sh",
                    tools / "render-policy.sh")
        self.policy.write_text("- host: jira.internal.example\n")

    def test_create_renders_internal_hosts_from_the_environment(self):
        self.install_render_policy()
        self.make("enter-carb", CARBONITE_JIRA_HOST="jira.corp.test")

        create = next(call for call in self.calls() if call[0] == "openshell" and call[6] == "create")
        rendered = self.state_dir / "policy-rendered.yaml"
        self.assertEqual(create[create.index("--policy") + 1], str(rendered))
        self.assertEqual(rendered.read_text(), "- host: jira.corp.test\n")
        self.assertEqual(self.policy.read_text(), "- host: jira.internal.example\n")

    def test_create_uses_the_policy_unchanged_without_host_variables(self):
        self.install_render_policy()
        self.make("enter-carb")

        create = next(call for call in self.calls() if call[0] == "openshell" and call[6] == "create")
        self.assertEqual(create[create.index("--policy") + 1], str(self.policy))
        self.assertFalse((self.state_dir / "policy-rendered.yaml").exists())

    def test_create_fails_before_the_sandbox_on_an_invalid_host(self):
        self.install_render_policy()
        result = self.make("enter-carb", succeeds=False, CARBONITE_JIRA_HOST="bad host")

        self.assertIn("CARBONITE_JIRA_HOST", result.stderr)
        self.assertNotIn("create", self.operations())


    def write_gcloud_adc(self, text='{"type": "authorized_user", "refresh_token": "rt"}'):
        adc = self.home / ".config/gcloud/application_default_credentials.json"
        adc.parent.mkdir(parents=True, exist_ok=True)
        adc.write_text(text)
        return adc

    def test_check_warns_that_claude_api_keys_are_untested(self):
        result = self.make("check-carb")
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertIn("API-key authentication inside the sandbox is not tested yet",
                      result.stderr)
        self.assertIn("WARNING:", result.stderr)
        # Not a terminal in tests: no escape sequences.
        self.assertNotIn("\033[", result.stderr)

    def test_check_is_quiet_about_claude_with_vertex_enabled(self):
        result = self.make("check-carb", CARBONITE_VERTEX="1",
                           CARBONITE_VERTEX_PROJECT_ID="my-project")
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertNotIn("not tested yet", result.stderr)

    def test_state_probe_treats_a_missing_sandbox_list_as_absent(self):
        result = self.make("status-carb", OSAC_CARB_BARE_LIST="1")
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertIn("(not created)", result.stdout)

    def test_kubeconfig_volume_name_can_come_from_the_environment(self):
        self.make("enter-carb", CARBONITE_KUBECONFIG_VOLUME="my-kubeconfig")
        creates = [call for call in self.calls()
                   if call[0] == "engine" and call[1:3] == ["volume", "create"]]
        self.assertTrue(any("my-kubeconfig" in call for call in creates), creates)

    def test_check_accepts_a_custom_workdir(self):
        """The policy paths are workdir-relative, so no override warning is needed."""
        result = self.make("check-carb", CARBONITE_WORKDIR="/sandbox/project")
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertNotIn("CARBONITE_WORKDIR", result.stderr)

    def test_check_does_not_warn_for_the_default_workdir(self):
        result = self.make("check-carb")
        self.assertNotIn("CARBONITE_WORKDIR differs", result.stderr)

    def test_rejects_a_wildcard_vertex_value(self):
        for value in ("1%", "0%", "%", "1 0"):
            with self.subTest(value=value):
                result = self.make("enter-carb", succeeds=False, CARBONITE_VERTEX=value)
                self.assertIn("CARBONITE_VERTEX must be 0 or 1", result.stderr)

    def test_fix_carb_reports_a_missing_default_route(self):
        self.install_fix_carb()
        self.write_host_shim("ip", "import sys\nsys.exit(1)")
        result = self.make("fix-carb", succeeds=False)
        self.assertIn("Could not derive the host's current IPv4 address", result.stderr)

    def test_create_passes_the_vertex_environment(self):
        self.make("enter-carb", CARBONITE_VERTEX="1",
                  CARBONITE_VERTEX_PROJECT_ID="my-project")
        create = next(call for call in self.calls()
                      if call[0] == "openshell" and call[6] == "create")
        self.assertIn("CLAUDE_CODE_USE_VERTEX=1", create)
        self.assertIn("CLOUD_ML_REGION=global", create)
        self.assertIn("ANTHROPIC_VERTEX_PROJECT_ID=my-project", create)

    def test_vertex_requires_a_project_id(self):
        result = self.make("enter-carb", succeeds=False, CARBONITE_VERTEX="1")
        self.assertIn("CARBONITE_VERTEX_PROJECT_ID", result.stderr)
        self.assertNotIn("create", self.operations())

    def test_vertex_copies_the_gcloud_credentials_once_ready(self):
        adc = self.write_gcloud_adc()
        self.make("enter-carb", CARBONITE_VERTEX="1",
                  CARBONITE_VERTEX_PROJECT_ID="my-project")
        self.assertIn([
            "openshell", "--gateway", "openshell", "--workspace", "osac-kind",
            "sandbox", "upload", "osac-carbonite-kind", str(adc),
            "/sandbox/.config/gcloud/application_default_credentials.json",
            "--no-git-ignore",
        ], self.calls())
        operations = self.operations()
        self.assertLess(operations.index("create"), operations.index("upload"))

    def test_create_without_vertex_sets_no_vertex_environment(self):
        self.write_gcloud_adc()
        self.make("enter-carb")
        create = next(call for call in self.calls()
                      if call[0] == "openshell" and call[6] == "create")
        self.assertFalse(any("CLAUDE_CODE_USE_VERTEX" in arg for arg in create))
        self.assertEqual(self.uploads(), [])

    def test_create_attaches_each_listed_provider(self):
        self.make("enter-carb", CARBONITE_PROVIDERS="osac-github osac-jira")

        create = next(call for call in self.calls() if call[0] == "openshell" and call[6] == "create")
        providers = [create[i + 1] for i, arg in enumerate(create) if arg == "--provider"]
        self.assertEqual(providers, ["osac-github", "osac-jira"])
        self.assertIn("KUBECONFIG=/sandbox/kube/config", create)

    def test_create_attaches_no_provider_by_default(self):
        self.make("enter-carb")

        create = next(call for call in self.calls() if call[0] == "openshell" and call[6] == "create")
        self.assertNotIn("--provider", create)

    def test_create_rejects_provider_names_that_are_not_plain(self):
        for value in ("a;b", "a$b", "-x", "a/b", "a'b"):
            with self.subTest(value=value):
                self.log.write_text("")
                result = self.make("enter-carb", succeeds=False, CARBONITE_PROVIDERS=value)
                self.assertIn("CARBONITE_PROVIDERS", result.stderr)
                self.assertNotIn("create", self.operations())


    def write_host_jira_config(self, text="server: https://jira.corp.test\nlogin: dev@corp.test\n"):
        config = self.home / ".config/.jira/.config.yml"
        config.parent.mkdir(parents=True)
        config.write_text(text)
        return config

    def uploads(self):
        return [call for call in self.calls() if call[0] == "openshell" and call[6] == "upload"]

    def test_create_uploads_the_host_jira_config(self):
        config = self.write_host_jira_config()
        self.make("enter-carb")

        self.assertEqual(self.uploads(), [[
            "openshell", "--gateway", "openshell", "--workspace", "osac-kind",
            "sandbox", "upload", "osac-carbonite-kind", str(config),
            "/sandbox/.config/.jira/.config.yml", "--no-git-ignore",
        ]])
        operations = self.operations()
        self.assertLess(operations.index("create"), operations.index("upload"))

    def test_jira_config_location_follows_jira_config_file_then_override(self):
        default = self.write_host_jira_config()
        custom = self.root / "custom-jira.yml"
        custom.write_text("server: https://jira.corp.test\n")
        self.make("enter-carb", JIRA_CONFIG_FILE=str(custom))
        self.assertEqual(self.uploads()[0][8], str(custom))

        self.state.write_text("{}")
        self.log.write_text("")
        override = self.root / "override-jira.yml"
        override.write_text("server: https://jira.corp.test\n")
        self.make("enter-carb", JIRA_CONFIG_FILE=str(custom), CARBONITE_JIRA_CONFIG=str(override))
        self.assertEqual(self.uploads()[0][8], str(override))
        self.assertTrue(default.exists())

    def test_create_without_a_host_jira_config_uploads_nothing(self):
        self.make("enter-carb")
        self.assertEqual(self.uploads(), [])

    def test_reentry_does_not_upload_the_jira_config_again(self):
        self.write_host_jira_config()
        self.make("enter-carb")
        self.make("enter-carb")
        self.assertEqual(len(self.uploads()), 1)

    def test_jira_config_with_a_credential_is_refused_before_create(self):
        for text in (
            "server: x\napi_token: abc\n",
            "server: x\n  password: abc\n",
            "server: x\nlogin: a\nnote: ATATT3xFfGF0abcdef\n",
            "server: x\nnote: openshell:resolve:env:v1_JIRA_API_TOKEN\n",
        ):
            with self.subTest(text=text):
                self.log.write_text("")
                config = self.home / ".config/.jira/.config.yml"
                config.parent.mkdir(parents=True, exist_ok=True)
                config.write_text(text)
                result = self.make("enter-carb", succeeds=False)
                self.assertIn(str(config), result.stderr)
                self.assertIn("credential", result.stderr)
                self.assertNotIn("create", self.operations())

    def test_jira_config_that_is_not_a_regular_file_is_refused(self):
        config = self.home / ".config/.jira/.config.yml"
        config.mkdir(parents=True)
        result = self.make("enter-carb", succeeds=False)
        self.assertIn("regular file", result.stderr)
        self.assertNotIn("create", self.operations())

    def test_failed_jira_config_upload_warns_but_keeps_the_sandbox(self):
        self.write_host_jira_config()
        result = self.make("enter-carb", OSAC_CARB_FAIL_UPLOAD="1")
        self.assertIn("jira config", result.stderr)
        self.assertEqual(self.operations().count("create"), 1)


if __name__ == "__main__":
    unittest.main()
