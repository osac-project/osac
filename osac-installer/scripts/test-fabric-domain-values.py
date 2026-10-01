#!/usr/bin/env python3
"""Render-only contracts for Phase 1 FabricDomain installer configuration.

Requires Helm and PyYAML, like validate-keycloak-credentials.sh.
Run after rebuilding umbrella dependencies with make helm-deps.
"""

import json
import os
from pathlib import Path
import subprocess
import unittest

import yaml


ROOT = Path(__file__).resolve().parents[2]
UMBRELLA = ROOT / "osac-installer/charts/osac"
OPERATOR = ROOT / "osac-operator/charts/operator"
INVENTORY_NAME = "osac-fabric-domain-inventory"
NAMESPACE = "fabric-networking"
HOSTS = {
    "GPU-01.example.com": "gpu-bmit-id",
    "gpu-01.example.com": "another-bmit-id",
    "gpu_02": "00042",
}


def render(chart, values=None):
    command = [
        os.environ.get("HELM_BIN", "helm"),
        "template", "phase1-test", str(chart),
        "--namespace", NAMESPACE,
    ]
    if chart == UMBRELLA:
        command.extend(["--values", str(UMBRELLA / "ci/default-values.yaml")])
    command.extend(["--values", "-"])
    return subprocess.run(
        command, input=json.dumps(values or {}), text=True, capture_output=True,
        check=False,
    )


def inventory_values(chart, inventory):
    values = {"fabricDomainInventory": inventory}
    return {"operator": values} if chart == UMBRELLA else values


class FabricDomainValuesTest(unittest.TestCase):
    def manifests(self, chart, values=None):
        result = render(chart, values)
        self.assertEqual(result.returncode, 0, result.stderr)
        return [doc for doc in yaml.safe_load_all(result.stdout) if doc]

    def test_inventory_is_opt_in(self):
        for chart in (UMBRELLA, OPERATOR):
            for values in ({}, inventory_values(chart, {})):
                with self.subTest(chart=chart.name, values=values):
                    docs = self.manifests(chart, values)
                    self.assertFalse(any(
                        doc["metadata"].get("name") == INVENTORY_NAME for doc in docs
                    ))

    def test_exact_hostname_to_id_map_in_networking_namespace(self):
        for chart in (UMBRELLA, OPERATOR):
            with self.subTest(chart=chart.name):
                docs = self.manifests(chart, inventory_values(chart, HOSTS))
                inventory = [
                    doc for doc in docs
                    if doc["metadata"].get("name") == INVENTORY_NAME
                ]
                self.assertEqual(len(inventory), 1)
                self.assertEqual(inventory[0]["apiVersion"], "v1")
                self.assertEqual(inventory[0]["kind"], "ConfigMap")
                self.assertEqual(inventory[0]["metadata"]["namespace"], NAMESPACE)
                self.assertEqual(inventory[0]["data"], HOSTS)
                # Namespace must agree with the operator's downward-API setting.
                managers = [
                    doc for doc in docs if doc["kind"] == "Deployment"
                    and doc["metadata"].get("labels", {}).get("app.kubernetes.io/name")
                    == inventory[0]["metadata"]["labels"]["app.kubernetes.io/name"]
                    and any(
                        container["name"] == "manager"
                        for container in doc["spec"]["template"]["spec"]["containers"]
                    )
                ]
                self.assertEqual(len(managers), 1)
                manager = managers[0]
                self.assertEqual(manager["metadata"]["namespace"], NAMESPACE)
                env = [
                    item
                    for container in manager["spec"]["template"]["spec"]["containers"]
                    for item in container.get("env", [])
                    if item["name"] == "OSAC_NETWORKING_NAMESPACE"
                ]
                self.assertEqual(len(env), 1)
                self.assertEqual(
                    env[0]["valueFrom"]["fieldRef"]["fieldPath"], "metadata.namespace"
                )

    def test_invalid_inventory_rejected(self):
        invalid = {
            "scalar": "gpu-bmit-id",
            "list": ["gpu-01"],
            "numeric-id": {"gpu-01": 42},
            "boolean-id": {"gpu-01": True},
            "empty-id": {"gpu-01": ""},
            "blank-id": {"gpu-01": " \t"},
            "nested-binding": {"gpu-01": {"template_id": "42"}},
            "empty-hostname": {"": "gpu-bmit-id"},
            "invalid-hostname": {"gpu/01": "gpu-bmit-id"},
            "long-hostname": {"x" * 254: "gpu-bmit-id"},
        }
        for chart in (UMBRELLA, OPERATOR):
            for name, inventory in invalid.items():
                with self.subTest(chart=chart.name, case=name):
                    result = render(chart, inventory_values(chart, inventory))
                    self.assertNotEqual(result.returncode, 0)
                    self.assertIn("fabricDomainInventory", result.stderr)
                    if chart == UMBRELLA:
                        self.assertIn("schema", result.stderr)

    def test_umbrella_operator_schema_stays_closed(self):
        result = render(UMBRELLA, {"operator": {"fabricDomainInventroy": HOSTS}})
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("fabricDomainInventroy", result.stderr)
        self.assertIn("schema", result.stderr)

    def test_network_class_template_configuration_rejected(self):
        # PR #1271's NC scalar forwarding must not return on either values path.
        removed = (
            {"east_west_config": {"ethernet_ew": {"template_id": "42"}}},
            {"template_id": "42"},
            {"templateId": "42"},
        )
        for config in removed:
            for expert in (False, True):
                with self.subTest(config=config, expert=expert):
                    values = (
                        {
                            "global": {"expertOverrides": {"networkClass": True}},
                            "networkClass": {
                                "enabled": True, "title": "Private networking",
                                "k8sManager": "k8s_only", **config,
                            },
                        }
                        if expert else {"global": {"networking": {"networkClass": config}}}
                    )
                    result = render(UMBRELLA, values)
                    self.assertNotEqual(result.returncode, 0)
                    self.assertIn("schema", result.stderr)
                    self.assertIn(next(iter(config)), result.stderr)

    def test_fabricdomain_crd_in_umbrella(self):
        docs = self.manifests(UMBRELLA)
        crd = next(
            doc for doc in docs if doc["kind"] == "CustomResourceDefinition"
            and doc["metadata"]["name"] == "fabricdomains.osac.openshift.io"
        )
        self.assertEqual(crd["spec"]["names"]["kind"], "FabricDomain")
        self.assertEqual(crd["spec"]["scope"], "Namespaced")
        for version in crd["spec"]["versions"]:
            spec = version["schema"]["openAPIV3Schema"]["properties"]["spec"]
            self.assertNotIn("instance_type", spec["properties"])
            self.assertNotIn("instanceType", spec["properties"])
        docs = self.manifests(UMBRELLA, {"operatorCrds": {"install": False}})
        self.assertFalse(any(
            doc["metadata"].get("name") == "fabricdomains.osac.openshift.io"
            for doc in docs
        ))


if __name__ == "__main__":
    unittest.main(verbosity=2)
