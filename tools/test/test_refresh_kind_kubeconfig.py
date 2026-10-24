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
        self.command("kubectl", '#!/bin/sh\nprintf "%s\\n" "$*" > "$TEST_ARGS"\nprintf "%s\\n" "$TEST_TOKEN"\n')
        self.command("getenforce", "#!/bin/sh\nprintf 'Disabled\\n'\n")

    def command(self, name: str, body: str):
        path = self.bin_dir / name
        path.write_text(body)
        path.chmod(0o700)

    def write_config(self, config):
        self.config_path.write_text(json.dumps(config))
        self.config_path.chmod(0o600)

    def run_script(self):
        return subprocess.run(
            [sys.executable, str(SCRIPT)], env=self.env, capture_output=True, text=True
        )

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


if __name__ == "__main__":
    unittest.main()
