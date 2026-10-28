"""Security tests for the opt-in Carbonite host build broker.

These tests use a temporary HOME and never modify the developer's SSH state.
"""

import importlib.util
import contextlib
import hashlib
import io
import json
import os
import pathlib
import random
import subprocess
import tempfile
from types import SimpleNamespace
import unittest
from unittest import mock


ROOT = pathlib.Path(__file__).resolve().parents[2]
DISPATCHER = ROOT / "tools/carbonite/host_build_dispatcher.py"
MANAGER = ROOT / "tools/carbonite/host_build_manager.py"
HELPER = ROOT / "tools/carbonite/host-build"
ENABLER = ROOT / "tools/carbonite/enable-host-build.sh"


def load_module(path, name):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class DispatcherTests(unittest.TestCase):
    def setUp(self):
        self.dispatcher = load_module(DISPATCHER, "host_build_dispatcher")
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.checkout = pathlib.Path(self.temp.name) / "checkout with ' quote"
        (self.checkout / "osac-ui").mkdir(parents=True)
        (self.checkout / "osac-ui/Containerfile").write_text("FROM scratch\n")
        self.calls = []
        patcher = mock.patch.object(self.dispatcher.pwd, "getpwuid",
                                    return_value=SimpleNamespace(pw_name="testuser", pw_dir=str(pathlib.Path(self.temp.name) / "home")))
        patcher.start()
        self.addCleanup(patcher.stop)

    def run_command(self, argv, **kwargs):
        self.calls.append((argv, kwargs))
        return subprocess.CompletedProcess(argv, 0)

    def test_probe_is_side_effect_free(self):
        output = io.StringIO()
        self.assertEqual(0, self.dispatcher.dispatch("probe", self.checkout, self.run_command, output))
        self.assertEqual("OSAC_HOST_BUILD_READY\n", output.getvalue())
        self.assertEqual([], self.calls)

    def test_sandbox_helper_rejects_unknown_action_before_ssh(self):
        for command in ("id", "ui;id", "ui --tag evil", "probe\nfoo"):
            with self.subTest(command=command):
                result = subprocess.run([str(HELPER), command], text=True, capture_output=True)
                self.assertNotEqual(0, result.returncode)
                self.assertIn("unsupported", result.stderr)

    def test_sandbox_helper_honors_project_dir(self):
        """The checkout root comes from PROJECT_DIR, not a hardcoded path."""
        if pathlib.Path("/sandbox/.ssh/osac-host-build").exists():
            self.skipTest("host builds are enabled here; the helper would reach the host")
        with tempfile.TemporaryDirectory() as temp:
            base = pathlib.Path(temp)
            (base / "osac-ui").mkdir()
            env = {**os.environ, "PROJECT_DIR": str(base)}
            result = subprocess.run([str(HELPER), "ui"], cwd=base / "osac-ui",
                                    env=env, text=True, capture_output=True)
        self.assertNotIn("unsupported worktree", result.stderr)
        self.assertIn("host build is not enabled", result.stderr)

    def test_sandbox_helper_rejects_other_checkout_before_ssh(self):
        for action in ("ui", "fulfillment", "operator", "metering-service", "metering-m360",
                       "metering-echo", "devstack-cli-tools"):
            with self.subTest(action=action):
                result = subprocess.run([str(HELPER), action], cwd=ROOT, text=True, capture_output=True)
                self.assertNotEqual(0, result.returncode)
                self.assertIn("unsupported worktree", result.stderr)

    def test_ui_build_has_exact_argv_cwd_and_clean_environment(self):
        seen = []

        def record(argv, **kwargs):
            # dispatch closes its dir descriptors on return, so the context
            # inode (the checkout root, not the osac-ui subdirectory) can
            # only be observed here, at call time.
            seen.append(os.stat(kwargs["cwd"]).st_ino)
            self.calls.append((argv, kwargs))
            return subprocess.CompletedProcess(argv, 0)

        with mock.patch.dict(os.environ, {
            "PATH": str(self.checkout), "BASH_ENV": "malicious", "CONTAINER_HOST": "tcp://evil",
            "DOCKER_HOST": "tcp://evil", "PYTHONPATH": str(self.checkout), "SSH_ORIGINAL_COMMAND": "ui",
            "IFS": "/", "ENV": str(self.checkout), "LD_PRELOAD": str(self.checkout / "libevil.so"),
            "XDG_CONFIG_HOME": str(self.checkout), "CONTAINERS_CONF_OVERRIDE": str(self.checkout),
            "GIT_CONFIG_SYSTEM": str(self.checkout), "PYTHONSTARTUP": str(self.checkout),
        }):
            self.assertEqual(0, self.dispatcher.dispatch("ui", self.checkout, record, io.StringIO()))
        self.assertEqual([self.checkout.stat().st_ino], seen)
        self.assertEqual(1, len(self.calls))
        argv, kwargs = self.calls[0]
        self.assertEqual(["/usr/bin/podman", "build", "-f", "-", "-t", "ghcr.io/osac-project/osac-ui:latest", "."], argv)
        self.assertEqual(b"FROM scratch\n", kwargs["input"])
        self.assertIn("/proc/self/fd/", kwargs["cwd"])
        self.assertTrue(kwargs["pass_fds"])
        self.assertFalse(kwargs.get("shell", False))
        for name in ("BASH_ENV", "CONTAINER_HOST", "DOCKER_HOST", "PYTHONPATH", "SSH_ORIGINAL_COMMAND",
                     "IFS", "ENV", "LD_PRELOAD", "XDG_CONFIG_HOME", "CONTAINERS_CONF_OVERRIDE",
                     "GIT_CONFIG_SYSTEM", "PYTHONSTARTUP"):
            self.assertNotIn(name, kwargs["env"])
        self.assertNotEqual(str(self.checkout), kwargs["env"]["PATH"])

    def test_other_fixed_build_actions_use_exact_context_file_and_arguments(self):
        # The context column is the directory podman must build from, asserted
        # by inode below -- a wrong BUILD_ACTIONS context (the shipped bug:
        # component subdirectory where the Containerfile COPYs repo-root
        # paths) redirects the build while every argv stays identical.
        recipes = (
            ("fulfillment", "fulfillment-service/Containerfile", ".",
             ["/usr/bin/podman", "build", "-f", "-", "-t", "ghcr.io/osac-project/fulfillment-service:latest",
              "--build-arg", "VERSION=", "."]),
            ("operator", "osac-operator/Containerfile", ".",
             ["/usr/bin/podman", "build", "-f", "-", "-t", "ghcr.io/osac-project/osac-operator:latest", "."]),
            ("metering-service", "osac-metering/metering-service/Containerfile", ".",
             ["/usr/bin/podman", "build", "-f", "-", "-t", "ghcr.io/osac-project/metering-service:latest", "."]),
            ("metering-m360", "osac-metering/adapters/Containerfile.m360-adapter", ".",
             ["/usr/bin/podman", "build", "-f", "-", "-t", "ghcr.io/osac-project/metering-m360-adapter:latest", "."]),
            ("metering-echo", "osac-metering/adapters/Containerfile.echo-adapter", ".",
             ["/usr/bin/podman", "build", "-f", "-", "-t", "ghcr.io/osac-project/metering-echo-adapter:latest", "."]),
            ("devstack-cli-tools", "Containerfile.cli-tools", "osac-installer/charts/osac-devstack",
             ["/usr/bin/podman", "build", "--platform", "linux/amd64,linux/arm64", "-f", "-", "--manifest",
              "ghcr.io/osac-project/cli-tools:latest", "."]),
        )
        for action, containerfile, context, expected_argv in recipes:
            with self.subTest(action=action):
                context_dir = self.checkout / context
                source = context_dir / containerfile
                source.parent.mkdir(parents=True, exist_ok=True)
                source.write_text("FROM scratch\n# " + action + "\n")
                seen = []

                def record(argv, **kwargs):
                    # dispatch closes its dir descriptors on return, so the
                    # context inode can only be observed here, at call time.
                    seen.append(os.stat(kwargs["cwd"]).st_ino)
                    self.calls.append((argv, kwargs))
                    return subprocess.CompletedProcess(argv, 0)

                self.calls.clear()
                self.assertEqual(0, self.dispatcher.dispatch(action, self.checkout, record, io.StringIO()))
                self.assertEqual([context_dir.stat().st_ino], seen)
                self.assertEqual(1, len(self.calls))
                argv, kwargs = self.calls[0]
                self.assertEqual(expected_argv, argv)
                self.assertEqual(source.read_bytes(), kwargs["input"])
                self.assertIn("/proc/self/fd/", kwargs["cwd"])
                self.assertTrue(kwargs["pass_fds"])
                self.assertFalse(kwargs.get("shell", False))

    def test_other_actions_reject_symlinked_containerfiles_and_contexts(self):
        outside = pathlib.Path(self.temp.name) / "outside"
        outside.mkdir()
        (outside / "Containerfile").write_text("FROM evil\n")
        context = self.checkout / "osac-metering"
        context.symlink_to(outside, target_is_directory=True)
        with self.assertRaises((ValueError, OSError)):
            self.dispatcher.dispatch("metering-m360", self.checkout, self.run_command, io.StringIO())
        self.assertEqual([], self.calls)

    def test_context_rename_and_symlink_swap_cannot_redirect_podman(self):
        outside = pathlib.Path(self.temp.name) / "outside"
        outside.mkdir()
        (outside / "Containerfile").write_text("FROM evil\n")
        # devstack-cli-tools is the one action whose context is a checkout
        # subdirectory, so a rename+symlink swap of that directory is the
        # redirect attempt this test must survive.
        context = self.checkout / "osac-installer/charts/osac-devstack"
        context.parent.mkdir(parents=True, exist_ok=True)
        context.mkdir()
        (context / "Containerfile.cli-tools").write_text("FROM scratch\n")
        original_inode = context.stat().st_ino
        seen = []

        def swap_before_podman(argv, **kwargs):
            context.rename(self.checkout / "former-devstack")
            context.symlink_to(outside, target_is_directory=True)
            seen.append((os.stat(kwargs["cwd"]).st_ino, kwargs["input"]))
            return subprocess.CompletedProcess(argv, 0)

        self.assertEqual(0, self.dispatcher.dispatch("devstack-cli-tools", self.checkout,
                                                     swap_before_podman, io.StringIO()))
        self.assertEqual([(original_inode, b"FROM scratch\n")], seen)

    def test_malformed_actions_never_start_host_command(self):
        attacks = [None, "", "ui ", " ui", "ui\n", "ui;id", "ui|id", "ui && id", "ui$(id)",
                   "ui`id`", "ui > /tmp/out", "ui --privileged", "ui --volume /:/host",
                   "ui --secret x", "ui --network host", "ui --tag evil", "ui\x00id", "uı", "UI",
                   "podman", "sh", "id", "push", "run", "login", "a" * 10000]
        generator = random.Random(0)
        attack_alphabet = " ;|&$`'\"<>\\\n\t*?()[]{}é"
        attacks.extend("ui" + "".join(generator.choices(attack_alphabet, k=generator.randint(1, 8)))
                       for _ in range(256))
        for attack in attacks:
            with self.subTest(attack=repr(attack)):
                with self.assertRaises(ValueError):
                    self.dispatcher.dispatch(attack, self.checkout, self.run_command, io.StringIO())
        self.assertEqual([], self.calls)

    def test_symlinked_context_or_containerfile_is_rejected(self):
        (self.checkout / "osac-ui/Containerfile").unlink()
        (self.checkout / "osac-ui/Containerfile").symlink_to("/etc/passwd")
        with self.assertRaises((ValueError, OSError)):
            self.dispatcher.dispatch("ui", self.checkout, self.run_command, io.StringIO())
        self.assertEqual([], self.calls)

    def test_forced_python_process_reads_only_literal_ssh_command(self):
        installed_parent = pathlib.Path(self.temp.name) / "host/.local/libexec"
        installed_parent.mkdir(parents=True, mode=0o700)
        installed_parent.parent.chmod(0o700)
        installed = installed_parent / "osac-host-build"
        installed.write_bytes(DISPATCHER.read_bytes())
        installed.chmod(0o700)
        config = installed_parent / "osac-host-build.json"
        config.write_text(__import__("json").dumps({"checkout": str(self.checkout)}))
        config.chmod(0o600)
        for action, expected_rc in (("probe", 0), ("ui;id", 2), ("sh", 2)):
            with self.subTest(action=action):
                env = {"SSH_ORIGINAL_COMMAND": action, "PYTHONPATH": str(self.checkout),
                       "BASH_ENV": str(self.checkout), "PATH": str(self.checkout)}
                result = subprocess.run(["/usr/bin/python3", "-I", "-S", str(installed)],
                                        env=env, text=True, capture_output=True)
                self.assertEqual(expected_rc, result.returncode)
                self.assertEqual("OSAC_HOST_BUILD_READY\n" if expected_rc == 0 else "", result.stdout)


class ManagerTests(unittest.TestCase):
    def setUp(self):
        self.manager = load_module(MANAGER, "host_build_manager")
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.home = pathlib.Path(self.temp.name) / "home"
        self.home.mkdir(mode=0o700)
        self.checkout = pathlib.Path(self.temp.name) / "checkout"
        self.checkout.mkdir()
        self.ssh = self.home / ".ssh"
        self.ssh.mkdir(mode=0o700)
        self.authorized = self.ssh / "authorized_keys"
        self.authorized.write_text("ssh-ed25519 AAAAunrelated unrelated\n")
        patcher = mock.patch.object(self.manager.pwd, "getpwuid",
                                    return_value=SimpleNamespace(pw_name="testuser", pw_dir=str(self.home)))
        patcher.start()
        self.addCleanup(patcher.stop)

    def test_authorized_key_install_is_idempotent_and_preserves_other_keys(self):
        public = "ssh-ed25519 AAAAfeature osac-host-build"
        dispatcher = self.home / ".local/libexec/osac-host-build"
        dispatcher.parent.mkdir(parents=True)
        dispatcher.write_text("# installed\n")
        self.manager.install_authorized_key(self.authorized, public, dispatcher)
        self.manager.install_authorized_key(self.authorized, public, dispatcher)
        contents = self.authorized.read_text()
        self.assertEqual(1, contents.count("AAAAfeature"))
        self.assertIn("AAAAunrelated unrelated", contents)
        self.assertIn("restrict,command=", contents)

    def test_authorized_key_rejects_symlink_and_conflicting_marker(self):
        target = self.home / "target"
        target.write_text("ssh-ed25519 AAAAother\n")
        self.authorized.unlink()
        self.authorized.symlink_to(target)
        with self.assertRaises(ValueError):
            self.manager.install_authorized_key(self.authorized, "ssh-ed25519 AAAAfeature", self.home / "dispatcher")
        self.assertEqual("ssh-ed25519 AAAAother\n", target.read_text())

    def test_authorized_key_rejects_unsafe_forced_command_path(self):
        for path in (self.home / 'bad"quote', self.home / "path with space", self.home / "comma,option"):
            with self.subTest(path=path), self.assertRaises(ValueError):
                self.manager.install_authorized_key(self.authorized, "ssh-ed25519 AAAAfeature", path)
        self.assertEqual("ssh-ed25519 AAAAunrelated unrelated\n", self.authorized.read_text())

    def test_authorized_key_rejects_unsafe_parent_directory(self):
        self.ssh.chmod(0o777)
        with self.assertRaises(ValueError):
            self.manager.install_authorized_key(self.authorized, "ssh-ed25519 AAAAfeature", self.home / "dispatcher")
        self.assertEqual("ssh-ed25519 AAAAunrelated unrelated\n", self.authorized.read_text())

    def test_authorized_key_rejects_duplicate_key_with_unrestricted_entry(self):
        self.authorized.write_text("ssh-ed25519 AAAAfeature unrestricted\n")
        with self.assertRaises(ValueError):
            self.manager.install_authorized_key(self.authorized, "ssh-ed25519 AAAAfeature", self.home / "dispatcher")
        self.assertEqual("ssh-ed25519 AAAAfeature unrestricted\n", self.authorized.read_text())

    def test_revoke_removes_only_feature_key(self):
        dispatcher = self.home / "dispatcher"
        dispatcher.write_text("# installed\n")
        self.manager.install_authorized_key(self.authorized, "ssh-ed25519 AAAAfeature", dispatcher)
        self.manager.remove_authorized_key(self.authorized)
        self.assertEqual("ssh-ed25519 AAAAunrelated unrelated\n", self.authorized.read_text())

    def test_explicit_upgrade_requires_revocation_and_replaces_reviewed_code(self):
        paths = self.manager._paths(self.home)
        paths["libexec"].mkdir(parents=True, mode=0o700)
        old_manager = b"# reviewed old manager\n"
        old_dispatcher = b"# reviewed old dispatcher\n"
        paths["manager"].write_bytes(old_manager)
        paths["dispatcher"].write_bytes(old_dispatcher)
        for name in ("manager", "dispatcher"):
            paths[name].chmod(0o700)
        old_receipt = {"manager_sha256": hashlib.sha256(old_manager).hexdigest(),
                       "dispatcher_sha256": hashlib.sha256(old_dispatcher).hexdigest()}
        paths["receipt"].write_text(json.dumps(old_receipt))
        paths["receipt"].chmod(0o600)
        with mock.patch.dict(os.environ, {"HOME": str(self.home)}):
            paths["config"].write_text("{}")
            paths["config"].chmod(0o600)
            with self.assertRaises(ValueError):
                self.manager.upgrade(paths)
            self.assertEqual(old_dispatcher, paths["dispatcher"].read_bytes())
            paths["config"].unlink()
            self.authorized.write_text("ssh-ed25519 AAAAfeature " + self.manager.MARKER + "\n")
            with self.assertRaises(ValueError):
                self.manager.upgrade(paths)
            self.authorized.write_text("ssh-ed25519 AAAAunrelated unrelated\n")
            self.manager.upgrade(paths)
            self.manager.upgrade(paths)
        cli = subprocess.run([str(ENABLER), "--upgrade"],
                             env={"HOME": str(self.home), "PATH": "/usr/bin:/bin"},
                             text=True, capture_output=True)
        self.assertEqual(0, cli.returncode, cli.stderr)
        self.assertEqual(MANAGER.read_bytes(), paths["manager"].read_bytes())
        self.assertEqual(DISPATCHER.read_bytes(), paths["dispatcher"].read_bytes())
        receipt = json.loads(paths["receipt"].read_text())
        self.assertEqual(hashlib.sha256(DISPATCHER.read_bytes()).hexdigest(),
                         receipt["dispatcher_sha256"])

    def test_upgrade_recovers_partial_copy_but_rejects_unrecognized_tampering(self):
        paths = self.manager._paths(self.home)
        paths["libexec"].mkdir(parents=True, mode=0o700)
        old_manager = b"# reviewed old manager\n"
        old_dispatcher = b"# reviewed old dispatcher\n"
        paths["manager"].write_bytes(MANAGER.read_bytes())  # Interrupted after manager replacement.
        paths["dispatcher"].write_bytes(old_dispatcher)
        for name in ("manager", "dispatcher"):
            paths[name].chmod(0o700)
        receipt = {"manager_sha256": hashlib.sha256(old_manager).hexdigest(),
                   "dispatcher_sha256": hashlib.sha256(old_dispatcher).hexdigest()}
        paths["receipt"].write_text(json.dumps(receipt))
        paths["receipt"].chmod(0o600)
        with mock.patch.dict(os.environ, {"HOME": str(self.home)}):
            self.manager.upgrade(paths)
            self.assertEqual(DISPATCHER.read_bytes(), paths["dispatcher"].read_bytes())
            paths["dispatcher"].write_text("# attacker changed installed code\n")
            with self.assertRaises(ValueError):
                self.manager.upgrade(paths)

    def test_existing_binding_reenable_rejects_tampered_dispatcher_before_authorization(self):
        paths = self.manager._paths(self.home)
        with mock.patch.dict(os.environ, {"HOME": str(self.home)}), \
             mock.patch.object(self.manager, "_gateway", return_value="https://127.0.0.1:17670"), \
             mock.patch.object(self.manager, "_host_key", return_value="osac-host-build ssh-ed25519 AAAAhost\n"), \
             mock.patch.object(self.manager, "_run", side_effect=self._fake_keygen):
            self.manager.enable(paths, "openshell", "default", "osac-carbonite", self.checkout)
            self.manager.remove_authorized_key(paths["authorized"])
            paths["dispatcher"].write_text("# unexpected modification\n")
            with self.assertRaises(ValueError):
                self.manager.enable(paths, "openshell", "default", "osac-carbonite", self.checkout)
        self.assertNotIn(self.manager.MARKER, paths["authorized"].read_text())

    def _fake_keygen(self, argv, *, capture=False):
        if argv[0] == "/usr/bin/ssh-keygen":
            key = pathlib.Path(argv[-1])
            key.write_text("private-test-key\n")
            key.with_suffix(".pub").write_text("ssh-ed25519 AAAAfeature test\n")
        return subprocess.CompletedProcess(argv, 0, stdout="")

    def test_opt_in_installs_outside_checkout_and_rerun_is_idempotent(self):
        paths = self.manager._paths(self.home)
        with mock.patch.dict(os.environ, {"HOME": str(self.home)}), \
             mock.patch.object(self.manager, "_gateway", return_value="https://127.0.0.1:17670"), \
             mock.patch.object(self.manager, "_host_key", return_value="osac-host-build ssh-ed25519 AAAAhost\n"), \
             mock.patch.object(self.manager, "_run", side_effect=self._fake_keygen):
            self.manager.enable(paths, "openshell", "default", "osac-carbonite", self.checkout)
            self.manager.enable(paths, "openshell", "default", "osac-carbonite", self.checkout)
        self.assertTrue(paths["manager"].is_file())
        self.assertTrue(paths["dispatcher"].is_file())
        self.assertFalse(self.checkout in paths["key"].parents)
        self.assertEqual(0o600, paths["key"].stat().st_mode & 0o777)
        self.assertEqual(1, self.authorized.read_text().count("AAAAfeature"))
        self.assertEqual("ssh-ed25519 AAAAunrelated unrelated\n", self.authorized.read_text().split("restrict,")[0])

    def test_real_keygen_stays_in_temporary_home(self):
        # Some CI/user-namespace sandboxes omit an NSS entry for the mapped
        # uid; ssh-keygen itself then refuses to start before opening a key.
        if subprocess.run(["/usr/bin/getent", "passwd", str(os.getuid())],
                          stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode != 0:
            self.skipTest("current uid has no NSS passwd entry")
        paths = self.manager._paths(self.home)
        with mock.patch.dict(os.environ, {"HOME": str(self.home)}), \
             mock.patch.object(self.manager, "_gateway", return_value="https://127.0.0.1:17670"), \
             mock.patch.object(self.manager, "_host_key", return_value="osac-host-build ssh-ed25519 AAAAhost\n"):
            self.manager.enable(paths, "openshell", "default", "osac-carbonite", self.checkout)
        self.assertEqual(0o600, paths["key"].stat().st_mode & 0o777)
        fingerprint = subprocess.run(["/usr/bin/ssh-keygen", "-lf", str(paths["key"].with_suffix(".pub"))],
                                     text=True, capture_output=True, check=True)
        self.assertIn("ED25519", fingerprint.stdout)

    def test_modified_installed_dispatcher_fails_binding_check(self):
        paths = self.manager._paths(self.home)
        with mock.patch.dict(os.environ, {"HOME": str(self.home)}), \
             mock.patch.object(self.manager, "_gateway", return_value="https://127.0.0.1:17670"), \
             mock.patch.object(self.manager, "_host_key", return_value="osac-host-build ssh-ed25519 AAAAhost\n"), \
             mock.patch.object(self.manager, "_run", side_effect=self._fake_keygen):
            self.manager.enable(paths, "openshell", "default", "osac-carbonite", self.checkout)
            paths["dispatcher"].write_text(paths["dispatcher"].read_text() + "# tampered\n")
            with self.assertRaises(ValueError):
                self.manager._bound(paths, "openshell", "default", "osac-carbonite", self.checkout)

    def test_bound_reports_missing_authorization_with_recovery_command(self):
        paths = self.manager._paths(self.home)
        with mock.patch.dict(os.environ, {"HOME": str(self.home)}), \
             mock.patch.object(self.manager, "_gateway", return_value="https://127.0.0.1:17670"), \
             mock.patch.object(self.manager, "_host_key", return_value="osac-host-build ssh-ed25519 AAAAhost\n"), \
             mock.patch.object(self.manager, "_run", side_effect=self._fake_keygen):
            self.manager.enable(paths, "openshell", "default", "osac-carbonite", self.checkout)
            self.manager.remove_authorized_key(paths["authorized"])
            with self.assertRaises(ValueError) as raised:
                self.manager._bound(paths, "openshell", "default", "osac-carbonite", self.checkout)
        message = str(raised.exception)
        self.assertIn("the host-build key authorization is missing", message)
        self.assertIn(" ".join([str(paths["manager"]), "enable", "--gateway", "openshell",
                                "--workspace", "default", "--sandbox", "osac-carbonite",
                                "--checkout", str(self.checkout)]), message)
        self.assertIn("carbonite-host-build.md", message)

    def test_probe_failure_hides_host_details(self):
        """A classified failure keeps the cause and drops identifiers."""
        leak = "jsalomon@internal-host.corp.example: Permission denied (publickey)."
        with mock.patch.object(self.manager, "_run",
                               side_effect=subprocess.CalledProcessError(255, "probe", stderr=leak)):
            with self.assertRaises(ValueError) as raised:
                self.manager._probe("openshell", "osac-kind", "osac-carbonite-kind", ["probe"])
        message = str(raised.exception)
        self.assertIn("Permission denied", message)
        for leaked in ("jsalomon", "internal-host", "corp.example"):
            self.assertNotIn(leaked, message)

    def test_probe_retry_messages_hide_host_details(self):
        """Retry logging carries the category, never the raw ssh diagnostics."""
        leak = "jsalomon@internal-host.corp.example: Permission denied (publickey)."

        def always_fail(argv, *, capture=False):
            raise subprocess.CalledProcessError(255, "probe", stderr=leak)

        stderr = io.StringIO()
        with mock.patch.object(self.manager, "_run", side_effect=always_fail), \
             mock.patch.object(self.manager.time, "sleep"), \
             contextlib.redirect_stderr(stderr):
            with self.assertRaises(ValueError):
                self.manager._probe_with_retries(
                    "openshell", "osac-kind", "osac-carbonite-kind", ["probe"])
        text = stderr.getvalue()
        self.assertIn("Permission denied", text)
        for leaked in ("jsalomon", "internal-host", "corp.example"):
            self.assertNotIn(leaked, text)

    def test_bound_rejects_authorization_without_the_forced_command(self):
        """A hand-repaired line without the forced command must not pass."""
        paths = self.manager._paths(self.home)
        with mock.patch.dict(os.environ, {"HOME": str(self.home)}), \
             mock.patch.object(self.manager, "_gateway", return_value="https://127.0.0.1:17670"), \
             mock.patch.object(self.manager, "_host_key", return_value="osac-host-build ssh-ed25519 AAAAhost\n"), \
             mock.patch.object(self.manager, "_run", side_effect=self._fake_keygen):
            self.manager.enable(paths, "openshell", "default", "osac-carbonite", self.checkout)
            line = next(text for text in paths["authorized"].read_text().splitlines()
                        if "osac-host-build" in text)
            fields = line.split()
            paths["authorized"].write_text(f"ssh-ed25519 {fields[-2]} {fields[-1]}\n")
            with self.assertRaises(ValueError) as raised:
                self.manager._bound(paths, "openshell", "default", "osac-carbonite", self.checkout)
        self.assertIn("forced command", str(raised.exception))

    def test_bound_reports_not_enabled_after_revoke(self):
        """Revoke removes the config; status must say so, not raise ENOENT."""
        paths = self.manager._paths(self.home)
        paths["libexec"].mkdir(parents=True)
        with self.assertRaises(ValueError) as raised:
            self.manager._bound(paths, "openshell", "default", "osac-carbonite", self.checkout)
        message = str(raised.exception)
        self.assertIn("not enabled", message)
        self.assertNotIn("No such file", message)

    def test_gateway_tolerates_a_null_gateway_list(self):
        result = subprocess.CompletedProcess([], 0, stdout='{"gateways": null}')
        with mock.patch.object(self.manager, "_run", return_value=result):
            with self.assertRaises(ValueError) as raised:
                self.manager._gateway("openshell")
        self.assertIn("not found", str(raised.exception))

    def test_sandbox_policy_tolerates_a_null_policy(self):
        result = subprocess.CompletedProcess(
            [], 0, stdout='{"name": "sb", "workspace": "ws", "policy": null}')
        with mock.patch.object(self.manager, "_run", return_value=result):
            self.assertFalse(self.manager._sandbox_policy("g", "ws", "sb"))

    def test_bound_reports_stale_authorized_key_with_recovery_command(self):
        paths = self.manager._paths(self.home)
        with mock.patch.dict(os.environ, {"HOME": str(self.home)}), \
             mock.patch.object(self.manager, "_gateway", return_value="https://127.0.0.1:17670"), \
             mock.patch.object(self.manager, "_host_key", return_value="osac-host-build ssh-ed25519 AAAAhost\n"), \
             mock.patch.object(self.manager, "_run", side_effect=self._fake_keygen):
            self.manager.enable(paths, "openshell", "default", "osac-carbonite", self.checkout)
            self.manager.install_authorized_key(paths["authorized"], "ssh-ed25519 AAAAstale test",
                                                 paths["dispatcher"])
            with self.assertRaises(ValueError) as raised:
                self.manager._bound(paths, "openshell", "default", "osac-carbonite", self.checkout)
        message = str(raised.exception)
        self.assertIn("does not match the installed host-build key", message)
        self.assertIn("enable --gateway openshell", message)

    def test_reenable_after_revoke_rotates_key_and_keeps_unrelated_ssh(self):
        paths = self.manager._paths(self.home)
        issued = 0

        def keygen(argv, *, capture=False):
            nonlocal issued
            if argv[0] == "/usr/bin/ssh-keygen":
                issued += 1
                key = pathlib.Path(argv[-1])
                key.write_text("private-test-key-" + str(issued))
                key.with_suffix(".pub").write_text(f"ssh-ed25519 AAAAfeature{issued} test\n")
            return subprocess.CompletedProcess(argv, 0, stdout="")

        with mock.patch.dict(os.environ, {"HOME": str(self.home)}), \
             mock.patch.object(self.manager, "_gateway", return_value="https://127.0.0.1:17670"), \
             mock.patch.object(self.manager, "_host_key", return_value="osac-host-build ssh-ed25519 AAAAhost\n"), \
             mock.patch.object(self.manager, "_run", side_effect=keygen), \
             mock.patch.object(self.manager, "_cleanup_sandbox"):
            self.manager.enable(paths, "openshell", "default", "osac-carbonite", self.checkout)
            self.manager.revoke(paths)
            self.manager.enable(paths, "openshell", "default", "osac-carbonite", self.checkout)
        contents = self.authorized.read_text()
        self.assertEqual(2, issued)
        self.assertIn("AAAAunrelated unrelated", contents)
        self.assertIn("AAAAfeature2", contents)
        self.assertNotIn("AAAAfeature1", contents)

    def test_reenable_rejects_tampered_host_only_dispatcher(self):
        paths = self.manager._paths(self.home)
        with mock.patch.dict(os.environ, {"HOME": str(self.home)}), \
             mock.patch.object(self.manager, "_gateway", return_value="https://127.0.0.1:17670"), \
             mock.patch.object(self.manager, "_host_key", return_value="osac-host-build ssh-ed25519 AAAAhost\n"), \
             mock.patch.object(self.manager, "_run", side_effect=self._fake_keygen), \
             mock.patch.object(self.manager, "_cleanup_sandbox"):
            self.manager.enable(paths, "openshell", "default", "osac-carbonite", self.checkout)
            self.manager.revoke(paths)
            paths["dispatcher"].write_text(paths["dispatcher"].read_text() + "# edited after revoke\n")
            with self.assertRaises(ValueError):
                self.manager.enable(paths, "openshell", "default", "osac-carbonite", self.checkout)
        self.assertEqual("ssh-ed25519 AAAAunrelated unrelated\n", self.authorized.read_text())

    def test_revoke_uses_stored_nondefault_binding_and_rejects_override(self):
        paths = self.manager._paths(self.home)
        cleaned = []
        with mock.patch.dict(os.environ, {"HOME": str(self.home)}), \
             mock.patch.object(self.manager, "_gateway", return_value="https://127.0.0.1:17670"), \
             mock.patch.object(self.manager, "_host_key", return_value="osac-host-build ssh-ed25519 AAAAhost\n"), \
             mock.patch.object(self.manager, "_run", side_effect=self._fake_keygen), \
             mock.patch.object(self.manager, "_cleanup_sandbox", side_effect=lambda *args: cleaned.append(args)):
            self.manager.enable(paths, "custom-local", "custom-workspace", "custom-sandbox", self.checkout)
            with contextlib.redirect_stderr(io.StringIO()):
                self.assertEqual(1, self.manager.main(["revoke", "--gateway", "wrong-gateway"]))
            self.assertTrue(paths["config"].exists())
            self.assertEqual([], cleaned)
            self.assertEqual("ssh-ed25519 AAAAunrelated unrelated\n", self.authorized.read_text())
            self.assertEqual(0, self.manager.main(["revoke"]))
        self.assertEqual([("custom-local", "custom-workspace", "custom-sandbox")], cleaned)
        self.assertEqual("ssh-ed25519 AAAAunrelated unrelated\n", self.authorized.read_text())

    def test_revoke_corrupt_config_still_removes_host_authorization(self):
        paths = self.manager._paths(self.home)
        paths["libexec"].mkdir(parents=True)
        paths["config"].write_text("{invalid json")
        dispatcher = paths["dispatcher"]
        dispatcher.write_text("installed")
        self.manager.install_authorized_key(self.authorized, "ssh-ed25519 AAAAfeature", dispatcher)
        with mock.patch.dict(os.environ, {"HOME": str(self.home)}), contextlib.redirect_stderr(io.StringIO()):
            self.assertEqual(1, self.manager.main(["revoke"]))
        self.assertEqual("ssh-ed25519 AAAAunrelated unrelated\n", self.authorized.read_text())

    def test_failed_keygen_rolls_back_partial_install_and_preserves_ssh(self):
        paths = self.manager._paths(self.home)
        with mock.patch.dict(os.environ, {"HOME": str(self.home)}), \
             mock.patch.object(self.manager, "_gateway", return_value="https://127.0.0.1:17670"), \
             mock.patch.object(self.manager, "_host_key", return_value="osac-host-build ssh-ed25519 AAAAhost\n"), \
             mock.patch.object(self.manager, "_run", side_effect=subprocess.CalledProcessError(1, "ssh-keygen")):
            with self.assertRaises(subprocess.CalledProcessError):
                self.manager.enable(paths, "openshell", "default", "osac-carbonite", self.checkout)
        self.assertEqual("ssh-ed25519 AAAAunrelated unrelated\n", self.authorized.read_text())
        for name in ("manager", "dispatcher", "key", "config", "known_hosts"):
            self.assertFalse(paths[name].exists(), name)

    def test_locality_rejects_remote_or_repointed_gateway(self):
        for item in ({"name": "openshell", "type": "remote", "endpoint": "https://gw.example:443", "auth": "oidc"},
                     {"name": "openshell", "type": "local", "endpoint": "https://192.0.2.1:17670", "auth": "mtls"},
                     {"name": "openshell", "type": "local", "endpoint": "https://127.0.0.1:17670", "auth": "oidc"}):
            with self.subTest(item=item), mock.patch.object(self.manager, "_run", return_value=subprocess.CompletedProcess([], 0, stdout=__import__("json").dumps([item]))):
                with self.assertRaises(ValueError):
                    self.manager._gateway("openshell")

    def test_sandbox_identity_requires_matching_local_podman_container(self):
        item = {"name": "osac-carbonite", "workspace": "default", "phase": "Ready", "id": "abc-123"}
        get = subprocess.CompletedProcess([], 0, stdout=__import__("json").dumps(item))
        ps = subprocess.CompletedProcess([], 0, stdout="openshell-default--other-abc-123\n")
        with mock.patch.object(self.manager, "_run", side_effect=[get, ps]):
            with self.assertRaises(ValueError):
                self.manager._sandbox_matches("openshell", "default", "osac-carbonite")

    def test_disabled_prepare_removes_residual_rule_without_upload(self):
        paths = self.manager._paths(self.home)
        commands = []
        rule_present = True

        def fake_run(argv, *, capture=False):
            nonlocal rule_present
            commands.append(argv)
            if "--remove-rule" in argv:
                rule_present = False
            if "sandbox" in argv and "get" in argv:
                return subprocess.CompletedProcess(argv, 0, stdout=__import__("json").dumps({
                    "name": "osac-carbonite", "workspace": "default", "phase": "Ready", "id": "sandbox-id",
                    "policy": {"network_policies": {"host": {"name": "osac-host-build"}} if rule_present else {}},
                }))
            return subprocess.CompletedProcess(argv, 0, stdout="")

        with mock.patch.object(self.manager, "_gateway", return_value="https://127.0.0.1:17670"), \
             mock.patch.object(self.manager, "_run", side_effect=fake_run):
            self.manager.prepare(paths, "openshell", "default", "osac-carbonite", self.checkout)
        self.assertTrue(any("--remove-rule" in cmd for cmd in commands))
        self.assertFalse(any("upload" in cmd for cmd in commands))

    def test_prepare_uses_exact_policy_upload_and_probe_argv_on_reentry(self):
        paths = self.manager._paths(self.home)
        paths["libexec"].mkdir(parents=True)
        paths["config"].write_text("{}")
        commands = []

        def fake_run(argv, *, capture=False):
            commands.append(argv)
            return subprocess.CompletedProcess(argv, 0, stdout="OSAC_HOST_BUILD_READY\n" if "probe" in argv else "")

        with mock.patch.object(self.manager, "_bound", return_value={}), \
             mock.patch.object(self.manager, "_sandbox_matches", return_value="abc-123"), \
             mock.patch.object(self.manager, "_run", side_effect=fake_run):
            self.manager.prepare(paths, "openshell", "default", "osac-carbonite", self.checkout)
            self.manager.prepare(paths, "openshell", "default", "osac-carbonite", self.checkout)

        policy = [cmd for cmd in commands if "--add-endpoint" in cmd]
        self.assertEqual(2, len(policy))
        for argv in policy:
            self.assertEqual(["/usr/bin/openshell", "--gateway", "openshell", "--workspace", "default",
                              "policy", "update", "osac-carbonite", "--add-endpoint",
                              "host.openshell.internal:22::tcp", "--binary", "/usr/bin/ssh",
                              "--rule-name", "osac-host-build", "--wait"], argv)
        uploaded = [cmd for cmd in commands if "upload" in cmd]
        self.assertEqual(6, len(uploaded))
        self.assertEqual({"/sandbox/.ssh/osac-host-build", "/sandbox/.ssh/osac-host-build-known_hosts",
                          "/sandbox/.ssh/osac-host-build-user"}, {cmd[-1] for cmd in uploaded})
        for upload in uploaded:
            position = commands.index(upload)
            before = commands[:position]
            self.assertTrue(any(cmd[-4:] == ["/usr/bin/test", "!", "-L", upload[-1]]
                                for cmd in before), upload)
        probes = [cmd for cmd in commands if cmd[-1:] == ["probe"]]
        self.assertEqual(2, len(probes))
        for command in probes:
            self.assertIn("StrictHostKeyChecking=yes", command)
            self.assertIn("ConnectTimeout=5", command)
            self.assertIn("HostKeyAlias=osac-host-build", command)
            self.assertIn("ClearAllForwardings=yes", command)
            self.assertNotIn("sh", command)
            self.assertNotIn("uv", command)
        execs = [cmd for cmd in commands if "sandbox" in cmd and "exec" in cmd]
        self.assertTrue(execs)
        for command in execs:
            self.assertIn("--timeout", command)
            self.assertIn("30", command)

    def test_prepare_probe_failure_surfaces_ssh_stderr(self):
        paths = self.manager._paths(self.home)
        paths["libexec"].mkdir(parents=True)
        paths["config"].write_text("{}")
        error = subprocess.CalledProcessError(
            255,
            "probe",
            stderr="ssh: connect to host host.openshell.internal port 22: Permission denied\n",
        )

        def fail_probe(argv, *, capture=False):
            if argv[-1:] == ["probe"]:
                raise error
            return subprocess.CompletedProcess(argv, 0, stdout="")

        with mock.patch.object(self.manager, "_bound", return_value={}), \
             mock.patch.object(self.manager, "_sandbox_matches", return_value="abc-123"), \
             mock.patch.object(self.manager, "_run", side_effect=fail_probe), \
             mock.patch.object(self.manager.time, "sleep"), \
             mock.patch.object(self.manager, "_cleanup_sandbox"):
            with self.assertRaises(ValueError) as raised:
                self.manager.prepare(paths, "openshell", "default", "osac-carbonite", self.checkout)

        self.assertIn("Permission denied", str(raised.exception))

    def test_prepare_failure_revokes_host_key_before_sandbox_cleanup(self):
        paths = self.manager._paths(self.home)
        paths["libexec"].mkdir(parents=True)
        paths["config"].write_text("{}")
        dispatcher = self.home / ".local/libexec/osac-host-build"
        dispatcher.write_text("installed")
        self.manager.install_authorized_key(self.authorized, "ssh-ed25519 AAAAfeature", dispatcher)
        key_at_cleanup = []

        def fake_run(argv, *, capture=False):
            if "upload" in argv:
                raise subprocess.CalledProcessError(1, argv)
            return subprocess.CompletedProcess(argv, 0, stdout="")

        def cleanup(*_args):
            key_at_cleanup.append(self.authorized.read_text())

        with mock.patch.object(self.manager, "_bound", return_value={}), \
             mock.patch.object(self.manager, "_sandbox_matches", return_value="abc-123"), \
             mock.patch.object(self.manager, "_run", side_effect=fake_run), \
             mock.patch.object(self.manager, "_cleanup_sandbox", side_effect=cleanup):
            with self.assertRaises(subprocess.CalledProcessError):
                self.manager.prepare(paths, "openshell", "default", "osac-carbonite", self.checkout)
        self.assertEqual(["ssh-ed25519 AAAAunrelated unrelated\n"], key_at_cleanup)

    def test_prepare_fails_fast_without_side_effects_when_authorization_is_missing(self):
        paths = self.manager._paths(self.home)
        commands = []
        with mock.patch.dict(os.environ, {"HOME": str(self.home)}), \
             mock.patch.object(self.manager, "_gateway", return_value="https://127.0.0.1:17670"), \
             mock.patch.object(self.manager, "_host_key", return_value="osac-host-build ssh-ed25519 AAAAhost\n"), \
             mock.patch.object(self.manager, "_run", side_effect=self._fake_keygen):
            self.manager.enable(paths, "openshell", "default", "osac-carbonite", self.checkout)
            self.manager.remove_authorized_key(paths["authorized"])

        def record(argv, *, capture=False):
            commands.append(argv)
            return subprocess.CompletedProcess(argv, 0, stdout="")

        with mock.patch.object(self.manager, "_gateway", return_value="https://127.0.0.1:17670"), \
             mock.patch.object(self.manager, "_sandbox_matches", return_value="abc-123"), \
             mock.patch.object(self.manager, "_run", side_effect=record):
            with self.assertRaises(ValueError) as raised:
                self.manager.prepare(paths, "openshell", "default", "osac-carbonite", self.checkout)
        self.assertIn("the host-build key authorization is missing", str(raised.exception))
        self.assertEqual([], commands)

    def test_failed_prepare_prints_recovery_command_for_removed_authorization(self):
        paths = self.manager._paths(self.home)
        error = subprocess.CalledProcessError(
            255, "probe",
            stderr="jsalomon@host.openshell.internal: Permission denied (publickey).\n")

        def fail_probe(argv, *, capture=False):
            if argv[-1:] == ["probe"]:
                raise error
            return subprocess.CompletedProcess(argv, 0, stdout="")

        stderr = io.StringIO()
        with mock.patch.dict(os.environ, {"HOME": str(self.home)}), \
             mock.patch.object(self.manager, "_gateway", return_value="https://127.0.0.1:17670"), \
             mock.patch.object(self.manager, "_host_key", return_value="osac-host-build ssh-ed25519 AAAAhost\n"), \
             mock.patch.object(self.manager, "_run", side_effect=self._fake_keygen):
            self.manager.enable(paths, "openshell", "default", "osac-carbonite", self.checkout)
        with mock.patch.object(self.manager, "_gateway", return_value="https://127.0.0.1:17670"), \
             mock.patch.object(self.manager, "_sandbox_matches", return_value="abc-123"), \
             mock.patch.object(self.manager, "_run", side_effect=fail_probe), \
             mock.patch.object(self.manager.time, "sleep"), \
             mock.patch.object(self.manager, "_cleanup_sandbox"):
            with contextlib.redirect_stderr(stderr):
                with self.assertRaises(ValueError) as raised:
                    self.manager.prepare(paths, "openshell", "default", "osac-carbonite", self.checkout)
        self.assertIn("Permission denied", str(raised.exception))
        self.assertNotIn(self.manager.MARKER, paths["authorized"].read_text())
        notice = stderr.getvalue()
        self.assertIn("removed the host-build key authorization", notice)
        self.assertIn(" ".join([str(paths["manager"]), "enable", "--gateway", "openshell",
                                "--workspace", "default", "--sandbox", "osac-carbonite",
                                "--checkout", str(self.checkout)]), notice)

    def test_prepare_probe_retry_absorbs_transient_failure(self):
        paths = self.manager._paths(self.home)
        paths["libexec"].mkdir(parents=True)
        paths["config"].write_text("{}")
        dispatcher = self.home / ".local/libexec/osac-host-build"
        dispatcher.write_text("installed")
        self.manager.install_authorized_key(self.authorized, "ssh-ed25519 AAAAfeature", dispatcher)
        attempts = []

        def fail_once_then_ready(argv, *, capture=False):
            if argv[-1:] == ["probe"]:
                attempts.append(argv)
                if len(attempts) == 1:
                    raise subprocess.CalledProcessError(
                        255, "probe",
                        stderr="ssh: connect to host host.openshell.internal port 22: "
                               "Connection timed out during banner exchange\n")
                return subprocess.CompletedProcess(argv, 0, stdout="OSAC_HOST_BUILD_READY\n")
            return subprocess.CompletedProcess(argv, 0, stdout="")

        stderr = io.StringIO()
        sleeps = []
        with mock.patch.object(self.manager, "_bound", return_value={}), \
             mock.patch.object(self.manager, "_sandbox_matches", return_value="abc-123"), \
             mock.patch.object(self.manager, "_run", side_effect=fail_once_then_ready), \
             mock.patch.object(self.manager.time, "sleep", side_effect=sleeps.append), \
             contextlib.redirect_stderr(stderr):
            self.manager.prepare(paths, "openshell", "default", "osac-carbonite", self.checkout)
        self.assertEqual(2, len(attempts))
        self.assertEqual([self.manager.PROBE_RETRY_DELAY_SECONDS], sleeps)
        self.assertIn(self.manager.MARKER, self.authorized.read_text())
        self.assertIn("retrying", stderr.getvalue())

    def test_prepare_probe_fails_closed_after_all_attempts(self):
        paths = self.manager._paths(self.home)
        paths["libexec"].mkdir(parents=True)
        paths["config"].write_text("{}")
        dispatcher = self.home / ".local/libexec/osac-host-build"
        dispatcher.write_text("installed")
        self.manager.install_authorized_key(self.authorized, "ssh-ed25519 AAAAfeature", dispatcher)
        attempts = []

        def fail_probe(argv, *, capture=False):
            if argv[-1:] == ["probe"]:
                attempts.append(argv)
                raise subprocess.CalledProcessError(
                    255, "probe",
                    stderr="ssh: connect to host host.openshell.internal port 22: "
                           "Connection timed out during banner exchange\n")
            return subprocess.CompletedProcess(argv, 0, stdout="")

        stderr = io.StringIO()
        with mock.patch.object(self.manager, "_bound", return_value={}), \
             mock.patch.object(self.manager, "_sandbox_matches", return_value="abc-123"), \
             mock.patch.object(self.manager, "_run", side_effect=fail_probe), \
             mock.patch.object(self.manager.time, "sleep"), \
             mock.patch.object(self.manager, "_cleanup_sandbox"), \
             contextlib.redirect_stderr(stderr):
            with self.assertRaises(ValueError) as raised:
                self.manager.prepare(paths, "openshell", "default", "osac-carbonite", self.checkout)
        self.assertEqual(self.manager.PROBE_ATTEMPTS, len(attempts))
        self.assertIn(f"after {self.manager.PROBE_ATTEMPTS} attempts", str(raised.exception))
        self.assertIn("banner exchange", str(raised.exception))
        self.assertNotIn(self.manager.MARKER, self.authorized.read_text())
        self.assertIn("removed the host-build key authorization", stderr.getvalue())

    def test_prepare_timeout_failure_prints_network_change_hint(self):
        paths = self.manager._paths(self.home)
        paths["libexec"].mkdir(parents=True)
        paths["config"].write_text("{}")
        dispatcher = self.home / ".local/libexec/osac-host-build"
        dispatcher.write_text("installed")
        self.manager.install_authorized_key(self.authorized, "ssh-ed25519 AAAAfeature", dispatcher)

        def fail_probe(argv, *, capture=False):
            if argv[-1:] == ["probe"]:
                raise subprocess.CalledProcessError(
                    255, "probe",
                    stderr="ssh: connect to host host.openshell.internal port 22: "
                           "Connection timed out during banner exchange\n")
            return subprocess.CompletedProcess(argv, 0, stdout="")

        stderr = io.StringIO()
        with mock.patch.object(self.manager, "_bound", return_value={}), \
             mock.patch.object(self.manager, "_sandbox_matches", return_value="abc-123"), \
             mock.patch.object(self.manager, "_run", side_effect=fail_probe), \
             mock.patch.object(self.manager.time, "sleep"), \
             mock.patch.object(self.manager, "_cleanup_sandbox"), \
             contextlib.redirect_stderr(stderr):
            with self.assertRaises(ValueError):
                self.manager.prepare(paths, "openshell", "default", "osac-carbonite", self.checkout)
        notice = stderr.getvalue()
        self.assertIn("removed the host-build key authorization", notice)
        self.assertIn("make fix-carb", notice)
        self.assertIn("make enable-carb", notice)
        self.assertIn("make rebuild-carb", notice)

    def test_prepare_permission_failure_prints_no_network_hint(self):
        paths = self.manager._paths(self.home)
        paths["libexec"].mkdir(parents=True)
        paths["config"].write_text("{}")
        dispatcher = self.home / ".local/libexec/osac-host-build"
        dispatcher.write_text("installed")
        self.manager.install_authorized_key(self.authorized, "ssh-ed25519 AAAAfeature", dispatcher)

        def fail_probe(argv, *, capture=False):
            if argv[-1:] == ["probe"]:
                raise subprocess.CalledProcessError(
                    255, "probe",
                    stderr="jsalomon@host.openshell.internal: Permission denied (publickey).\n")
            return subprocess.CompletedProcess(argv, 0, stdout="")

        stderr = io.StringIO()
        with mock.patch.object(self.manager, "_bound", return_value={}), \
             mock.patch.object(self.manager, "_sandbox_matches", return_value="abc-123"), \
             mock.patch.object(self.manager, "_run", side_effect=fail_probe), \
             mock.patch.object(self.manager.time, "sleep"), \
             mock.patch.object(self.manager, "_cleanup_sandbox"), \
             contextlib.redirect_stderr(stderr):
            with self.assertRaises(ValueError):
                self.manager.prepare(paths, "openshell", "default", "osac-carbonite", self.checkout)
        notice = stderr.getvalue()
        self.assertIn("removed the host-build key authorization", notice)
        self.assertNotIn("make fix-carb", notice)
        self.assertNotIn("network change", notice)

    def test_prepare_probe_retries_unexpected_response(self):
        paths = self.manager._paths(self.home)
        paths["libexec"].mkdir(parents=True)
        paths["config"].write_text("{}")
        attempts = []

        def wrong_then_ready(argv, *, capture=False):
            if argv[-1:] == ["probe"]:
                attempts.append(argv)
                if len(attempts) == 1:
                    return subprocess.CompletedProcess(argv, 0, stdout="unexpected\n")
                return subprocess.CompletedProcess(argv, 0, stdout="OSAC_HOST_BUILD_READY\n")
            return subprocess.CompletedProcess(argv, 0, stdout="")

        with mock.patch.object(self.manager, "_bound", return_value={}), \
             mock.patch.object(self.manager, "_sandbox_matches", return_value="abc-123"), \
             mock.patch.object(self.manager, "_run", side_effect=wrong_then_ready), \
             mock.patch.object(self.manager.time, "sleep"):
            self.manager.prepare(paths, "openshell", "default", "osac-carbonite", self.checkout)
        self.assertEqual(2, len(attempts))

    def test_status_surfaces_missing_authorization_recovery_command(self):
        paths = self.manager._paths(self.home)
        stderr = io.StringIO()
        with mock.patch.dict(os.environ, {"HOME": str(self.home)}), \
             mock.patch.object(self.manager, "_gateway", return_value="https://127.0.0.1:17670"), \
             mock.patch.object(self.manager, "_host_key", return_value="osac-host-build ssh-ed25519 AAAAhost\n"), \
             mock.patch.object(self.manager, "_run", side_effect=self._fake_keygen):
            self.manager.enable(paths, "openshell", "osac-kind", "osac-carbonite-kind", self.checkout)
            self.manager.remove_authorized_key(paths["authorized"])
            with contextlib.redirect_stderr(stderr):
                self.assertEqual(1, self.manager.main(["status", "--checkout", str(self.checkout)]))
        self.assertIn("the host-build key authorization is missing", stderr.getvalue())
        self.assertIn("enable --gateway openshell", stderr.getvalue())

    def test_enable_is_idempotent_when_already_authorized(self):
        paths = self.manager._paths(self.home)
        stderr = io.StringIO()
        with mock.patch.dict(os.environ, {"HOME": str(self.home)}), \
             mock.patch.object(self.manager, "_gateway", return_value="https://127.0.0.1:17670"), \
             mock.patch.object(self.manager, "_host_key", return_value="osac-host-build ssh-ed25519 AAAAhost\n"), \
             mock.patch.object(self.manager, "_run", side_effect=self._fake_keygen), \
             contextlib.redirect_stderr(stderr):
            self.manager.enable(paths, "openshell", "default", "osac-carbonite", self.checkout)
            self.manager.enable(paths, "openshell", "default", "osac-carbonite", self.checkout)
        self.assertEqual(1, paths["authorized"].read_text().count(self.manager.MARKER))


if __name__ == "__main__":
    unittest.main()
