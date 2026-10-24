"""Check the operator integration preflight without a live Kubernetes cluster."""

import os
import subprocess
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "osac-operator/hack/check-integration-cluster.sh"


class OperatorIntegrationPreflightTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="osac-kind-preflight-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.bin_dir = self.root / "bin"
        self.bin_dir.mkdir()
        kubectl = self.bin_dir / "kubectl"
        kubectl.write_text(
            '#!/bin/sh\n'
            'printf "%s\\n" "$*" >> "$KUBECTL_CALLS"\n'
            'case "$*" in\n'
            '  *--raw=/readyz*) [ "${FAKE_API_DOWN:-}" != 1 ] ;;\n'
            '  *"get pods"*) [ "${FAKE_PODS_DOWN:-}" != 1 ] || exit 1; '
            '[ "${FAKE_OPERATOR_PRESENT:-}" != 1 ] || echo pod/operator ;;\n'
            '  *) exit 3 ;;\n'
            'esac\n'
        )
        kubectl.chmod(0o755)
        self.calls = self.root / "calls"
        self.config = self.root / "config"
        self.config.write_text("apiVersion: v1\nkind: Config\n")

    def run_preflight(self, **overrides):
        env = {
            **os.environ,
            "PATH": f"{self.bin_dir}:{os.environ['PATH']}",
            "OPENSHELL_SANDBOX": "",
            "KUBECONFIG": str(self.config),
            "KUBECTL_CALLS": str(self.calls),
            "FAKE_OPERATOR_PRESENT": "1",
            **overrides,
        }
        return subprocess.run(["bash", str(SCRIPT)], env=env, capture_output=True, text=True, timeout=10)

    def test_missing_mounted_kubeconfig_stops_before_kubectl(self):
        result = self.run_preflight(OPENSHELL_SANDBOX="1", KUBECONFIG="")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("KUBECONFIG", result.stderr)
        self.assertFalse(self.calls.exists())

    def test_unreachable_api_fails_without_leaking_kubectl_error(self):
        result = self.run_preflight(FAKE_API_DOWN="1")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Kubernetes API", result.stderr)
        self.assertNotIn("https://", result.stderr)
        self.assertEqual(len(self.calls.read_text().splitlines()), 1)

    def test_missing_operator_fails_without_five_minute_poll(self):
        result = self.run_preflight(FAKE_OPERATOR_PRESENT="0")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("operator", result.stderr)
        self.assertEqual(len(self.calls.read_text().splitlines()), 2)

    def test_ready_cluster_passes(self):
        result = self.run_preflight()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(len(self.calls.read_text().splitlines()), 2)


if __name__ == "__main__":
    unittest.main()
