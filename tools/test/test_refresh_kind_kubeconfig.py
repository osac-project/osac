"""Host-only renewal must preserve the scoped config and avoid leaking tokens."""

import base64
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import unittest


SCRIPT = Path(__file__).resolve().parents[1] / "carbonite/refresh-kind-kubeconfig.py"


def jwt(expiry: int) -> str:
    payload = base64.urlsafe_b64encode(json.dumps({"exp": expiry}).encode()).rstrip(b"=")
    return f"header.{payload.decode()}.signature"


class RefreshKindKubeconfigTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.bin_dir = self.root / "bin"
        self.bin_dir.mkdir()
        self.state_dir = self.root / "state/osac-carbonite-kind"
        self.kube_dir = self.state_dir / "kube"
        self.kube_dir.mkdir(parents=True, mode=0o700)
        (self.root / ".kube").mkdir()
        (self.root / ".kube/osac-dev-kind.kubeconfig").write_text("host-admin-placeholder")
        self.config_path = self.kube_dir / "config"
        self.original = {
            "apiVersion": "v1",
            "kind": "Config",
            "clusters": [{"name": "kind-test", "cluster": {
                "server": "https://host.openshell.internal:18443",
                "certificate-authority-data": "synthetic-ca",
                "tls-server-name": "synthetic-api",
            }}],
            "users": [{"name": "sandbox-test", "user": {"token": "old-token"}}],
            "contexts": [{"name": "kind-test", "context": {
                "cluster": "kind-test", "user": "sandbox-test", "namespace": "osac",
            }}],
            "current-context": "kind-test",
        }
        self.write_config(self.original)
        self.new_token = jwt(int(time.time()) + 12 * 3600)
        self.env = os.environ.copy()
        self.env.update({
            "HOME": str(self.root),
            "XDG_STATE_HOME": str(self.root / "state"),
            "PATH": f"{self.bin_dir}:{os.environ['PATH']}",
            "TEST_TOKEN": self.new_token,
            "TEST_ARGS": str(self.root / "kubectl-args"),
            "TEST_CHCON": str(self.root / "chcon-called"),
        })
        self.env.pop("OPENSHELL_SANDBOX", None)
        # The Makefile exports this; default-path tests must not inherit it.
        self.env.pop("CARBONITE_KUBECONFIG_DIR", None)
        self.command("kubectl", '#!/bin/sh\nprintf "%s\\n" "$*" > "$TEST_ARGS"\nprintf "%s\\n" "$TEST_TOKEN"\n')
        self.command("getenforce", "#!/bin/sh\nprintf 'Disabled\\n'\n")

    def command(self, name: str, body: str):
        path = self.bin_dir / name
        path.write_text(body)
        path.chmod(0o700)

    def write_config(self, config):
        self.config_path.write_text(json.dumps(config))
        self.config_path.chmod(0o600)

    def run_script(self, **env):
        return subprocess.run(
            [sys.executable, str(SCRIPT)], env={**self.env, **env},
            capture_output=True, text=True,
        )

    def place_config(self, kube_dir):
        kube_dir.mkdir(parents=True, mode=0o700, exist_ok=True)
        path = kube_dir / "config"
        path.write_text(json.dumps(self.original))
        path.chmod(0o600)
        return path

    def test_renews_twelve_hour_token_without_exposing_it(self):
        result = self.run_script()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("12.0 hours", result.stdout)
        self.assertNotIn(self.new_token, result.stdout + result.stderr)
        self.assertIn("--duration=12h", (self.root / "kubectl-args").read_text())
        updated = json.loads(self.config_path.read_text())
        self.assertEqual(updated["users"][0]["user"]["token"], self.new_token)
        self.assertEqual(updated["clusters"], self.original["clusters"])
        self.assertEqual(self.config_path.stat().st_mode & 0o777, 0o600)
        self.assertEqual(list(self.state_dir.glob("config.*")), [])

    def test_selinux_label_failure_keeps_existing_config(self):
        self.command("getenforce", "#!/bin/sh\nprintf 'Enforcing\\n'\n")
        self.command("chcon", "#!/bin/sh\nexit 1\n")
        result = self.run_script()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(json.loads(self.config_path.read_text()), self.original)
        self.assertEqual(list(self.state_dir.glob("config.*")), [])
        self.assertNotIn(self.new_token, result.stdout + result.stderr)

    def test_selinux_label_is_set_before_replacement(self):
        self.command("getenforce", "#!/bin/sh\nprintf 'Enforcing\\n'\n")
        self.command("chcon", '#!/bin/sh\nprintf "%s\\n" "$*" > "$TEST_CHCON"\n')
        result = self.run_script()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(f"--reference={self.kube_dir}",
                      (self.root / "chcon-called").read_text())
        self.assertEqual(json.loads(self.config_path.read_text())["users"][0]["user"]["token"],
                         self.new_token)

    def test_rejects_unexpected_cluster_before_token_request(self):
        invalid = json.loads(json.dumps(self.original))
        invalid["clusters"][0]["cluster"]["server"] = "https://other.example:443"
        self.write_config(invalid)
        result = self.run_script()
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.root / "kubectl-args").exists())
        self.assertEqual(json.loads(self.config_path.read_text()), invalid)

    def test_rejects_execution_from_sandbox(self):
        self.env["OPENSHELL_SANDBOX"] = "1"
        result = self.run_script()
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.root / "kubectl-args").exists())


    def test_renewal_failure_reports_the_kubectl_diagnostic(self):
        """A denied token request must surface kubectl's reason, never stdout."""
        self.command("kubectl", '#!/bin/sh\n'
                     'echo "Error from server (Forbidden): users \\"sandbox-test\\" is forbidden" >&2\n'
                     'exit 1\n')
        result = self.run_script()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("forbidden", result.stderr)
        self.assertNotIn("sandbox-test", result.stderr)
        self.assertNotIn(self.new_token, result.stdout + result.stderr)

    def test_unrelated_text_does_not_masquerade_as_a_missing_object(self):
        """A "not found" mentioned in passing is not a NotFound status."""
        self.command("kubectl", '#!/bin/sh\n'
                     'echo "exec: credential helper not found" >&2\n'
                     'exit 1\n')
        result = self.run_script()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("without a recognised diagnostic", result.stderr)
        self.assertNotIn("ServiceAccount", result.stderr)

    def test_recognises_the_notfound_status_token(self):
        self.command("kubectl", '#!/bin/sh\n'
                     'echo "Error from server (NotFound): serviceaccounts \\"x\\" not found" >&2\n'
                     'exit 1\n')
        result = self.run_script()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("not found (ServiceAccount or namespace missing)", result.stderr)

    def test_recognises_an_unreachable_cluster(self):
        self.command("kubectl", '#!/bin/sh\n'
                     'echo "The connection to the server 127.0.0.1:18443 was refused" >&2\n'
                     'exit 1\n')
        result = self.run_script()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("cannot reach the cluster", result.stderr)

    def test_failure_never_reprints_stdout_even_when_it_holds_the_token(self):
        """A failing kubectl that also wrote a token to stdout must not leak it."""
        self.command("kubectl", '#!/bin/sh\n'
                     'printf "%s\\n" "$TEST_TOKEN"\n'
                     'echo "Error from server (Forbidden): users \\"sandbox-test\\" is forbidden" >&2\n'
                     'exit 1\n')
        result = self.run_script()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("forbidden", result.stderr)
        self.assertNotIn(self.new_token, result.stdout + result.stderr)

    def test_renews_the_directory_named_by_the_makefile_override(self):
        """Make may mount another directory; renew the file the sandbox reads."""
        custom = self.root / "custom/kube"
        path = self.place_config(custom)
        result = self.run_script(CARBONITE_KUBECONFIG_DIR=str(custom))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(path.read_text())["users"][0]["user"]["token"],
                         self.new_token)
        self.assertEqual(json.loads(self.config_path.read_text()), self.original)
        self.assertEqual(list(custom.glob("config.*")), [])

    def test_empty_xdg_state_home_falls_back_to_home(self):
        fallback = self.root / ".local/state/osac-carbonite-kind/kube"
        path = self.place_config(fallback)
        result = self.run_script(XDG_STATE_HOME="")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(path.read_text())["users"][0]["user"]["token"],
                         self.new_token)

if __name__ == "__main__":
    unittest.main()
