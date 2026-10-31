"""Unit tests for the installer networking integration-check helpers."""

from __future__ import annotations

import json
import runpy
import subprocess
import unittest
from pathlib import Path


CHECKS = runpy.run_path(str(Path(__file__).with_name("it-networking-checks.py")))


class ManifestsToJsonTests(unittest.TestCase):
    def test_parses_multiple_json_documents_from_kubectl(self) -> None:
        expected = [
            {"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": "registration"}},
            {"apiVersion": "batch/v1", "kind": "Job", "metadata": {"name": "network-class"}},
        ]
        manifest = """\
apiVersion: v1
kind: ConfigMap
metadata:
  name: registration
---
apiVersion: batch/v1
kind: Job
metadata:
  name: network-class
"""
        kubectl_output = "\n".join(json.dumps(doc, indent=2) for doc in expected) + "\n"
        script_globals = CHECKS["manifests_to_json"].__globals__
        original_run = script_globals["run"]

        def fake_run(args: list[str], stdin: str | None = None, check: bool = True) -> subprocess.CompletedProcess:
            self.assertEqual(args, ["kubectl", "create", "--dry-run=client", "-o", "json", "-f", "-"])
            self.assertEqual(stdin, manifest)
            return subprocess.CompletedProcess(args, 0, stdout=kubectl_output, stderr="")

        script_globals["run"] = fake_run
        try:
            self.assertEqual(CHECKS["manifests_to_json"](manifest), expected)
        finally:
            script_globals["run"] = original_run


class RefusalPodTests(unittest.TestCase):
    def deployment(self, flag: str = "--networking-provisioning-enabled=true") -> dict:
        return {
            "metadata": {"name": "fulfillment-grpc-server", "namespace": "osac", "generation": 3},
            "spec": {"template": {"spec": {
                "volumes": [{"name": "database", "projected": {"sources": []}}],
                "serviceAccountName": "fulfillment-grpc-server",
                "containers": [{
                    "name": "grpc-server",
                    "image": "localhost/fulfillment-service:it",
                    "env": [{"name": "X", "value": "1"}],
                    "args": ["start", "grpc-server", flag, "--networking-excluded-ipv4-cidrs=192.0.2.0/24"],
                    "livenessProbe": {"httpGet": {"path": "/", "port": 8000}},
                    "volumeMounts": [{"name": "database", "mountPath": "/secrets/database"}],
                }],
            }}},
        }

    def test_inverts_true_to_false_and_keeps_the_rest(self) -> None:
        dep = self.deployment()
        pod = CHECKS["refusal_pod_manifest"](dep, "p1")
        ctr = pod["spec"]["containers"][0]
        self.assertEqual(ctr["args"][2], "--networking-provisioning-enabled=false")
        self.assertEqual(ctr["args"][0:2], ["start", "grpc-server"])
        self.assertEqual(ctr["args"][3], "--networking-excluded-ipv4-cidrs=192.0.2.0/24")
        self.assertEqual(ctr["image"], "localhost/fulfillment-service:it")
        self.assertEqual(ctr["env"], [{"name": "X", "value": "1"}])
        self.assertEqual(ctr["volumeMounts"][0]["name"], "database")
        self.assertEqual(pod["spec"]["volumes"], dep["spec"]["template"]["spec"]["volumes"])
        self.assertEqual(pod["spec"]["serviceAccountName"], "fulfillment-grpc-server")
        self.assertEqual(pod["spec"]["restartPolicy"], "Never")
        self.assertNotIn("livenessProbe", ctr)
        self.assertEqual(pod["metadata"]["namespace"], "osac")
        self.assertNotIn("app", pod["metadata"]["labels"])

    def test_inverts_the_flag_in_the_rendered_chart_deployment(self) -> None:
        # Fixture: helm template of fulfillment-service/charts/service (ci-values.yaml,
        # mcp.externalHostname=mcp.osac.localhost, excludedIpv4Cidrs=[192.0.2.0/24]),
        # templates/grpc-server/deployment.yaml. The chart puts the flags in `command`.
        dep = json.loads(Path(__file__).with_name("testdata").joinpath("grpc-server-deployment.json").read_text())
        pod = CHECKS["refusal_pod_manifest"](dep, "p4")
        ctr = pod["spec"]["containers"][0]
        orig = dep["spec"]["template"]["spec"]["containers"][0]
        self.assertIsNone(orig.get("args"))
        self.assertIn("--networking-provisioning-enabled=true", orig["command"])
        self.assertIn("--networking-provisioning-enabled=false", ctr["command"])
        self.assertNotIn("--networking-provisioning-enabled=true", ctr["command"])
        self.assertEqual(
            [a for a in orig["command"] if "provisioning" not in a],
            [a for a in ctr["command"] if "provisioning" not in a],
        )
        self.assertIn("--networking-excluded-ipv4-cidrs=192.0.2.0/24", ctr["command"])
        self.assertEqual(ctr["image"], orig["image"])
        self.assertNotIn("livenessProbe", ctr)

    def test_inverts_false_to_true_and_does_not_mutate_the_deployment(self) -> None:
        dep = self.deployment("--networking-provisioning-enabled=false")
        snapshot = json.dumps(dep, sort_keys=True)
        pod = CHECKS["refusal_pod_manifest"](dep, "p2")
        self.assertEqual(pod["spec"]["containers"][0]["args"][2], "--networking-provisioning-enabled=true")
        self.assertEqual(json.dumps(dep, sort_keys=True), snapshot)

    def test_missing_flag_is_an_error(self) -> None:
        dep = self.deployment()
        dep["spec"]["template"]["spec"]["containers"][0]["args"] = ["start"]
        with self.assertRaises(ValueError):
            CHECKS["refusal_pod_manifest"](dep, "p3")


if __name__ == "__main__":
    unittest.main()
