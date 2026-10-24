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


if __name__ == "__main__":
    unittest.main()
