#!/usr/bin/env python3
"""Render tests for the osac chart's networking values (global.networking).

Covers the values schema, the render-time validation messages, the NetworkClass
step input, the cudn_evpn registration, the ValidatingAdmissionPolicy, and the
networking values the subcharts receive.
"""

from __future__ import annotations

import json
import os
import pathlib
import subprocess
import tempfile
import unittest
from typing import Any

import yaml

INSTALLER = pathlib.Path(__file__).resolve().parents[1]
CHART = INSTALLER / "charts/osac"
DEFAULT_VALUES = CHART / "ci/default-values.yaml"
HELM = os.environ.get("HELM_BIN", "helm")

PLAN_PATH = "global.networking.networkClass.addressPlan"
DEFAULTS_PATH = "global.networking.networkClass.defaults"


def services(vmaas: bool, bmaas: bool, caas: bool) -> dict:
    return {
        "global": {
            "services": {
                "vmaas": {"enabled": vmaas},
                "bmaas": {"enabled": bmaas},
                "caas": {"enabled": caas},
                "maas": {"enabled": caas},
            }
        }
    }


def networking(**values) -> dict:
    return {"global": {"networking": values}}


def merge(*overrides: dict) -> dict:
    result: dict = {}
    for override in overrides:
        _merge(result, override)
    return result


def _merge(dst: dict, src: dict) -> None:
    for key, value in src.items():
        if isinstance(value, dict) and isinstance(dst.get(key), dict):
            _merge(dst[key], value)
        else:
            dst[key] = value


def helm(*overrides: dict, args: tuple[str, ...] = ()) -> subprocess.CompletedProcess:
    with tempfile.NamedTemporaryFile("w", suffix=".json") as extra:
        json.dump(merge(*overrides), extra)
        extra.flush()
        return subprocess.run(
            [
                HELM,
                "template",
                "osac",
                str(CHART),
                "--namespace",
                "osac",
                "--values",
                str(DEFAULT_VALUES),
                "--values",
                extra.name,
                *args,
            ],
            capture_output=True,
            text=True,
        )


def render(*overrides: dict, args: tuple[str, ...] = ()) -> list[dict[str, Any]]:
    result = helm(*overrides, args=args)
    if result.returncode != 0:
        raise AssertionError(f"render failed: {result.stderr}")
    return [doc for doc in yaml.safe_load_all(result.stdout) if doc]


def find(docs: list[dict], kind: str, name: str) -> dict:
    for doc in docs:
        if doc.get("kind") == kind and doc["metadata"]["name"] == name:
            return doc
    raise AssertionError(f"{kind}/{name} not rendered")


def container_env(doc: dict, container: str | None = None) -> dict[str, str]:
    containers = doc["spec"]["template"]["spec"]["containers"]
    c = next(c for c in containers if container is None or c["name"] == container)
    return {e["name"]: e.get("value") for e in c.get("env", [])}


def step_input(docs: list[dict]) -> dict:
    return json.loads(container_env(find(docs, "Job", "create-network-class"))["NETWORK_CLASS_STEP_INPUT"])


class RenderFailureCase(unittest.TestCase):
    def assert_fails(self, message: str, *overrides: dict, args: tuple[str, ...] = ()):
        result = helm(*overrides, args=args)
        self.assertNotEqual(result.returncode, 0, f"rendered, expected failure containing {message!r}")
        if message == "missing properties":
            # Helm 3: "missing properties 'fabric', 'lbVip'"; Helm 4: "<name> is required".
            for name in ("fabric", "lbVip"):
                self.assertTrue(
                    f"'{name}'" in result.stderr or f"{name} is required" in result.stderr,
                    f"expected {name} to be reported missing in {result.stderr!r}",
                )
        elif message.startswith("/"):
            # Helm 3 reports dotted paths; Helm 4 reports JSON pointers.
            dotted_path = message.lstrip("/").replace("/", ".")
            self.assertTrue(
                message in result.stderr or dotted_path in result.stderr,
                f"expected {message!r} or {dotted_path!r} in {result.stderr!r}",
            )
        else:
            self.assertIn(message, result.stderr)


class RemovedKeysTest(RenderFailureCase):
    def test_removed_keys_are_rejected(self):
        cases = {
            "global.networking.netris": networking(netris={"controllerUrl": "https://netris.example.com"}),
            "global.networking.networkClass.enabled": networking(networkClass={"enabled": True}),
            "top-level networkClass": {"networkClass": {"enabled": True}},
            "global.expertOverrides": {"global": {"expertOverrides": {"aap": True}}},
            "operator.controllers.networkingProvisioning": {
                "operator": {"controllers": {"networkingProvisioning": False}}
            },
            "defaults.enableNatGateway": networking(networkClass={"defaults": {"enableNatGateway": True}}),
            "k8sManager k8s_only": networking(k8sManager="k8s_only"),
        }
        for name, override in cases.items():
            with self.subTest(name):
                self.assert_fails("schema", override)


class ManagerRulesTest(RenderFailureCase):
    def test_fabric_manager_is_required(self):
        self.assert_fails("/global/networking/fabricManager", networking(fabricManager=""))
        self.assert_fails("/global/networking/fabricManager", networking(fabricManager="Netris"))

    def test_k8s_manager_values(self):
        self.assert_fails("/global/networking/k8sManager", networking(k8sManager="cudn_net"))

    def test_vmaas_needs_k8s_manager(self):
        self.assert_fails(
            "global.networking.k8sManager is required when VMaaS is enabled (set cudn_evpn)", networking(k8sManager="")
        )

    def test_at_least_one_service_is_required(self):
        self.assert_fails("/global/services", services(False, False, False), networking(k8sManager="cudn_evpn"))

    def test_provisioning_needs_network_fulfillment(self):
        self.assert_fails(
            "global.networking.provisioningEnabled requires aap.instanceGroups.networkFulfillment.enabled=true",
            {"aap": {"instanceGroups": {"networkFulfillment": {"enabled": False}}}},
        )
        render(
            networking(provisioningEnabled=False),
            {"aap": {"instanceGroups": {"networkFulfillment": {"enabled": False}}}},
        )


class AddressPlanTest(RenderFailureCase):
    @staticmethod
    def plan(overlay, fabric, lb_vip) -> dict:
        return networking(networkClass={"addressPlan": {"overlay": overlay, "fabric": fabric, "lbVip": lb_vip}})

    def test_partial_plan(self):
        self.assert_fails("missing properties", networking(networkClass={"addressPlan": {"overlay": 0.5}}))

    def test_share_values(self):
        for overlay in (0.3, -0.25, 1.25):
            with self.subTest(overlay=overlay):
                self.assert_fails("/global/networking/networkClass/addressPlan/overlay", self.plan(overlay, 0.5, 0.25))

    def test_rules(self):
        cases = [
            ((0.5, 0.25, 0.125), f"{PLAN_PATH} shares must sum to exactly 1 (got 0.875)"),
            (
                (0, 0.75, 0.25),
                f"{PLAN_PATH}.overlay must be greater than 0 exactly when global.networking.k8sManager is set",
            ),
            ((0.5, 0, 0.5), f"{PLAN_PATH}.fabric must be greater than 0 when BMaaS or CaaS is enabled"),
            ((0.5, 0.5, 0), f"{PLAN_PATH}.lbVip must be greater than 0 when CaaS is enabled"),
        ]
        for plan, message in cases:
            with self.subTest(plan=plan):
                self.assert_fails(message, self.plan(*plan))

    def test_overlay_without_k8s_manager(self):
        self.assert_fails(
            f"{PLAN_PATH}.overlay must be greater than 0 exactly when global.networking.k8sManager is set",
            services(vmaas=False, bmaas=True, caas=False),
            networking(k8sManager=""),
            self.plan(0.5, 0.5, 0),
        )

    def test_derived_plans(self):
        cases = [
            ((True, True, True), "cudn_evpn", (0.5, 0.25, 0.25)),
            ((True, True, False), "cudn_evpn", (0.5, 0.5, 0)),
            ((True, False, False), "cudn_evpn", (0.75, 0.25, 0)),
            ((False, True, True), "cudn_evpn", (0.5, 0.25, 0.25)),
            ((False, True, False), "cudn_evpn", (0.5, 0.5, 0)),
            ((False, True, True), "", (0, 0.75, 0.25)),
            ((False, True, False), "", (0, 1, 0)),
        ]
        for enabled, k8s_manager, plan in cases:
            with self.subTest(services=enabled, k8s_manager=k8s_manager):
                inp = step_input(render(services(*enabled), networking(k8sManager=k8s_manager)))
                self.assertEqual(inp["address_plan"], {"overlay": plan[0], "fabric": plan[1], "lb_vip": plan[2]})
                self.assertFalse(inp["address_plan_explicit"])

    def test_prefix_limits(self):
        cases = [
            # (plan, services, limit)
            ((0.5625, 0.1875, 0.25), (True, True, True), 24),
            ((0.375, 0.375, 0.25), (True, True, True), 25),
            ((0.5, 0.25, 0.25), (True, True, True), 26),
            ((0.5, 0.5, 0), (True, True, False), 27),
        ]
        for plan, enabled, limit in cases:
            with self.subTest(limit=limit):
                values = (services(*enabled), self.plan(*plan))
                ok = networking(networkClass={"defaults": {"subnetIPv4CIDR": f"10.200.0.0/{limit}"}})
                render(*values, ok)
                too_long = networking(networkClass={"defaults": {"subnetIPv4CIDR": f"10.200.0.0/{limit + 1}"}})
                self.assert_fails(
                    f"{DEFAULTS_PATH}.subnetIPv4CIDR prefix /{limit + 1} is longer than "
                    f"the address plan's prefix limit /{limit}",
                    *values,
                    too_long,
                )
        # No k8sManager and BMaaS only: plan 0/1/0, limit /28.
        bmaas_only = (services(False, True, False), networking(k8sManager=""))
        render(*bmaas_only, networking(networkClass={"defaults": {"subnetIPv4CIDR": "10.200.0.0/28"}}))
        self.assert_fails(
            f"{DEFAULTS_PATH}.subnetIPv4CIDR prefix /29 is longer than the address plan's prefix limit /28",
            *bmaas_only,
            networking(networkClass={"defaults": {"subnetIPv4CIDR": "10.200.0.0/29"}}),
        )

    def test_explicit_plan_reaches_step(self):
        inp = step_input(render(self.plan(0.5, 0.25, 0.25)))
        self.assertEqual(inp["address_plan"], {"overlay": 0.5, "fabric": 0.25, "lb_vip": 0.25})
        self.assertTrue(inp["address_plan_explicit"])


class DefaultsTest(RenderFailureCase):
    @staticmethod
    def defaults(**values) -> dict:
        return networking(networkClass={"defaults": values})

    def test_cidrs(self):
        self.assert_fails(
            f"{DEFAULTS_PATH}.virtualNetworkIPv4CIDR must be a canonical IPv4 CIDR (got 10.200.0.1/16)",
            self.defaults(virtualNetworkIPv4CIDR="10.200.0.1/16"),
        )
        self.assert_fails(
            f"{DEFAULTS_PATH}.subnetIPv4CIDR must be inside virtualNetworkIPv4CIDR",
            self.defaults(subnetIPv4CIDR="10.201.0.0/20"),
        )
        self.assert_fails(
            "/global/networking/networkClass/defaults/subnetIPv4CIDR", self.defaults(subnetIPv4CIDR="10.200.0.0")
        )

    def test_rule_shapes(self):
        cases = {
            "ipv4Cidr required": {"protocol": "PROTOCOL_ALL"},
            "tcp needs ports": {"protocol": "PROTOCOL_TCP", "ipv4Cidr": "0.0.0.0/0"},
            "icmp has no ports": {"protocol": "PROTOCOL_ICMP", "ipv4Cidr": "0.0.0.0/0", "portFrom": 1, "portTo": 1},
            "all has no ports": {"protocol": "PROTOCOL_ALL", "ipv4Cidr": "0.0.0.0/0", "portFrom": 1, "portTo": 1},
            "protocol enum": {"protocol": "TCP", "ipv4Cidr": "0.0.0.0/0", "portFrom": 1, "portTo": 1},
            "unknown key": {"protocol": "PROTOCOL_ALL", "ipv4Cidr": "0.0.0.0/0", "ipv6Cidr": "::/0"},
        }
        for name, rule in cases.items():
            with self.subTest(name):
                self.assert_fails(
                    "/global/networking/networkClass/defaults/ingressRules/0", self.defaults(ingressRules=[rule])
                )

    def test_port_order(self):
        self.assert_fails(
            f"{DEFAULTS_PATH}.egressRules[0]: portFrom (80) must be <= portTo (79)",
            self.defaults(
                egressRules=[{"protocol": "PROTOCOL_UDP", "ipv4Cidr": "0.0.0.0/0", "portFrom": 80, "portTo": 79}]
            ),
        )

    def test_icmp_default_rule_fails_with_cudn_evpn(self):
        icmp = {"protocol": "PROTOCOL_ICMP", "ipv4Cidr": "10.0.0.0/8"}
        enabled = {"aap": {"instanceGroups": {"networkFulfillment": {"enabled": True}}}}
        for field in ("ingressRules", "egressRules"):
            with self.subTest(field):
                self.assert_fails(
                    f"{DEFAULTS_PATH}.{field}[0]: PROTOCOL_ICMP needs the security_group_icmp capability, "
                    "which cudn_evpn does not declare; use PROTOCOL_ALL",
                    networking(provisioningEnabled=True, k8sManager="cudn_evpn"),
                    self.defaults(**{field: [icmp]}),
                    enabled,
                )

    def test_icmp_default_rule_renders_without_cudn_evpn(self):
        render(
            services(False, True, False),
            networking(provisioningEnabled=True, k8sManager=""),
            self.defaults(ingressRules=[{"protocol": "PROTOCOL_ICMP", "ipv4Cidr": "10.0.0.0/8"}]),
            {"aap": {"instanceGroups": {"networkFulfillment": {"enabled": True}}}},
        )

    def test_icmp_default_rule_renders_with_provisioning_disabled(self):
        render(
            networking(provisioningEnabled=False, k8sManager="cudn_evpn"),
            self.defaults(ingressRules=[{"protocol": "PROTOCOL_ICMP", "ipv4Cidr": "10.0.0.0/8"}]),
        )

    def test_step_input(self):
        docs = render(
            networking(
                excludedIpv4Cidrs=["192.0.2.0/24"],
                networkClass={
                    "defaults": {
                        "ingressRules": [
                            {"protocol": "PROTOCOL_TCP", "ipv4Cidr": "0.0.0.0/0", "portFrom": 22, "portTo": 22}
                        ]
                    },
                    "eastWestCapabilities": {"supportsEastWestEthernet": True},
                },
            ),
        )
        self.assertEqual(
            step_input(docs),
            {
                "fabric_manager": "netris",
                "k8s_manager": "cudn_evpn",
                "defaults": {
                    "virtual_network_ipv4_cidr": "10.200.0.0/16",
                    "subnet_ipv4_cidr": "10.200.0.0/20",
                    "ingress_rules": [
                        {"protocol": "PROTOCOL_TCP", "ipv4_cidr": "0.0.0.0/0", "port_from": 22, "port_to": 22}
                    ],
                    "egress_rules": [{"protocol": "PROTOCOL_ALL", "ipv4_cidr": "0.0.0.0/0"}],
                },
                "east_west_capabilities": {"supports_east_west_ethernet": True},
                "address_plan": {"overlay": 0.5, "fabric": 0.25, "lb_vip": 0.25},
                "address_plan_explicit": False,
                "services": {"vmaas": True, "bmaas": True, "caas": True},
                "excluded_ipv4_cidrs": ["192.0.2.0/24"],
                "provisioning_enabled": True,
            },
        )
        step_cm = find(docs, "ConfigMap", "network-class-step")
        self.assertIn("def run(", step_cm["data"]["network-class-step.py"])

    def test_step_input_ignores_the_removed_resolver_setting(self):
        removed = "CUDN_EVPN_DNS_RESOLVERS"  # removed with the per-resource DNS
        config = {"aap": {"instanceGroups": {"networkFulfillment": {"config": {removed: "192.0.2.53"}}}}}
        self.assertEqual(step_input(render(config)), step_input(render()))

    def test_step_absent_without_fulfillment(self):
        docs = render({"service": {"enabled": False}})
        kinds = {(d["kind"], d["metadata"]["name"]) for d in docs}
        self.assertNotIn(("Job", "create-network-class"), kinds)
        self.assertNotIn(("ValidatingAdmissionPolicy", "osac-osac-managed-resources"), kinds)


class ManifestsTest(unittest.TestCase):
    def test_disabling_operator_manager_registration_keeps_installer_managers(self):
        docs = render({"operator": {"networkManagers": {"enabled": False}}})
        cudn_registration = find(docs, "ConfigMap", "osac-network-k8s-manager-cudn-evpn")
        self.assertEqual(cudn_registration["data"]["name"], "cudn_evpn")
        netris_registration = find(docs, "ConfigMap", "osac-network-fabric-manager-netris")
        self.assertEqual(netris_registration["data"]["name"], "netris")
        for doc in docs:
            labels = doc.get("metadata", {}).get("labels") or {}
            if doc["metadata"]["name"] != "osac-network-fabric-manager-netris":
                self.assertNotIn("osac.openshift.io/network-fabric-manager", labels, doc["metadata"]["name"])
            if doc["metadata"]["name"] != "osac-network-k8s-manager-cudn-evpn":
                self.assertNotIn("osac.openshift.io/network-k8s-manager", labels, doc["metadata"]["name"])

    def test_cudn_evpn_registration_is_contract_format(self):
        docs = render(networking(k8sManager="cudn_evpn"))
        registration = find(docs, "ConfigMap", "osac-network-k8s-manager-cudn-evpn")
        self.assertEqual(registration["metadata"]["namespace"], "osac")
        self.assertEqual(registration["metadata"]["labels"]["osac.openshift.io/network-k8s-manager"], "true")
        self.assertNotIn("osac.openshift.io/network-fabric-manager", registration["metadata"]["labels"])
        self.assertIs(registration["immutable"], True)
        self.assertEqual(
            registration["data"],
            {"name": "cudn_evpn", "ansible_role": "osac.templates.cudn_evpn", "network_type": "evpn"},
        )
        registrations = [
            doc for doc in docs
            if doc["kind"] == "ConfigMap"
            and (doc["metadata"].get("labels") or {}).get("osac.openshift.io/network-k8s-manager") == "true"
            and (doc.get("data") or {}).get("name") == "cudn_evpn"
        ]
        self.assertEqual(len(registrations), 1)

    def test_no_cudn_evpn_registration_without_the_k8s_manager(self):
        docs = render(services(False, True, False), networking(k8sManager=""))
        names = {doc["metadata"]["name"] for doc in docs if doc["kind"] == "ConfigMap"}
        self.assertNotIn("osac-network-k8s-manager-cudn-evpn", names)

    def test_netris_registration_is_contract_format(self):
        docs = render(networking(fabricManager="netris", k8sManager="cudn_evpn"))
        registration = find(docs, "ConfigMap", "osac-network-fabric-manager-netris")
        self.assertEqual(registration["metadata"]["namespace"], "osac")
        self.assertEqual(registration["metadata"]["labels"]["osac.openshift.io/network-fabric-manager"], "true")
        self.assertNotIn("osac.openshift.io/network-k8s-manager", registration["metadata"]["labels"])
        self.assertIs(registration["immutable"], True)
        self.assertEqual(
            registration["data"],
            {
                "name": "netris",
                "description": (
                    "Netris SDN controller for physical fabric management. Provides EVPN "
                    "networking and Ethernet east-west connectivity for bare-metal servers."
                ),
                "ansible_role": "osac.templates.netris",
                "network_types": "evpn",
                "capabilities": "eastWestEthernet,security_group_icmp",
            },
        )
        registrations = [
            doc for doc in docs
            if doc["kind"] == "ConfigMap"
            and (doc["metadata"].get("labels") or {}).get("osac.openshift.io/network-fabric-manager") == "true"
            and (doc.get("data") or {}).get("name") == "netris"
        ]
        self.assertEqual(len(registrations), 1)

    def test_no_netris_registration_for_another_fabric_manager(self):
        docs = render(networking(fabricManager="test_fabric", k8sManager="cudn_evpn"))
        names = {doc["metadata"]["name"] for doc in docs if doc["kind"] == "ConfigMap"}
        self.assertNotIn("osac-network-fabric-manager-netris", names)

    def test_admission_policy(self):
        docs = render()
        policy = find(docs, "ValidatingAdmissionPolicy", "osac-osac-managed-resources")
        binding = find(docs, "ValidatingAdmissionPolicyBinding", "osac-osac-managed-resources")
        operator_sa = next(d for d in docs if d["kind"] == "Deployment" and d["metadata"]["name"] == "osac-operator")
        bmf_sa = next(d for d in docs if d["kind"] == "Deployment" and "bmf" in d["metadata"]["name"])
        identities = policy["spec"]["variables"][0]["expression"]
        for sa in (
            "hub-access",
            operator_sa["spec"]["template"]["spec"]["serviceAccountName"],
            bmf_sa["spec"]["template"]["spec"]["serviceAccountName"],
            # AAP job pods remove the finalizers their delete playbooks own.
            "osac-sa",
        ):
            self.assertIn(f'"system:serviceaccount:osac:{sa}"', identities)
        self.assertEqual(
            policy["spec"]["validations"][0]["message"],
            "OSAC manages this resource; change it through the fulfillment API",
        )
        resources = policy["spec"]["matchConstraints"]["resourceRules"][0]["resources"]
        networking_resources = (
            "networkclasses",
            "externalippools",
            "virtualnetworks",
            "subnets",
            "securitygroups",
            "externalips",
            "externalipattachments",
            "natgateways",
            "fabricdomains",
            "networkattachments",
        )
        expected_resources = [
            resource
            for name in networking_resources
            for resource in (name, f"{name}/status")
        ]
        self.assertEqual(resources, expected_resources)
        self.assertEqual(binding["spec"]["policyName"], policy["metadata"]["name"])
        self.assertEqual(binding["spec"]["validationActions"], ["Deny"])
        self.assertEqual(
            binding["spec"]["matchResources"]["namespaceSelector"]["matchLabels"],
            {"kubernetes.io/metadata.name": "osac"},
        )

    def test_admission_policy_follows_service_account_overrides(self):
        docs = render(
            {
                "operator": {"serviceAccount": {"name": "custom-operator"}},
                "bmf": {"serviceAccount": {"name": "custom-bmf"}},
            }
        )
        identities = find(docs, "ValidatingAdmissionPolicy", "osac-osac-managed-resources")["spec"]["variables"][0][
            "expression"
        ]
        self.assertIn('"system:serviceaccount:osac:custom-operator"', identities)
        self.assertIn('"system:serviceaccount:osac:custom-bmf"', identities)

    def test_hub_access_can_manage_network_classes(self):
        docs = render({"hubAccess": {"enabled": True}})
        role = find(docs, "ClusterRole", "osac-hub-access")
        self.assertIn("networkclasses", role["rules"][0]["resources"])
        self.assertIn("create", role["rules"][0]["verbs"])


class AapSettingsTest(unittest.TestCase):
    def test_network_fulfillment_settings_written_verbatim(self):
        ca = "-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n"
        docs = render(
            networking(fabricManager="test_fabric"),
            {
                "aap": {
                    "instanceGroups": {
                        "networkFulfillment": {
                            "config": {"ACME_FABRIC_URL": "https://fabric.example.com", "ACME_FABRIC_CA": ca},
                            "secret": {"ACME_FABRIC_TOKEN": "s3cr3t"},
                        }
                    }
                }
            }
        )
        cm = find(docs, "ConfigMap", "network-fulfillment-ig")
        self.assertEqual(cm["data"], {"ACME_FABRIC_URL": "https://fabric.example.com", "ACME_FABRIC_CA": ca})
        secret = find(docs, "Secret", "network-fulfillment-ig")
        self.assertEqual(set(secret["data"]), {"ACME_FABRIC_TOKEN"})
        for doc in docs:
            if doc["kind"] in ("ConfigMap", "Secret"):
                self.assertFalse(
                    any(key.startswith("NETRIS_") for key in (doc.get("data") or {})),
                    f"{doc['kind']}/{doc['metadata']['name']} has derived NETRIS_ settings",
                )

    def test_network_fulfillment_netris_settings_are_written_verbatim(self):
        config = {
            "NETRIS_CONTROLLER_URL": "https://netris.example.com",
            "NETRIS_VALIDATE_CERTS": "false",
        }
        docs = render(
            networking(fabricManager="netris"),
            {
                "aap": {
                    "instanceGroups": {
                        "networkFulfillment": {
                            "config": config,
                            "secret": {"NETRIS_PASSWORD": "test-password"},
                        }
                    }
                }
            },
        )
        cm = find(docs, "ConfigMap", "network-fulfillment-ig")
        self.assertEqual(cm["data"], config)
        secret = find(docs, "Secret", "network-fulfillment-ig")
        self.assertEqual(set(secret["data"]), {"NETRIS_PASSWORD"})

    def test_caas_netris_settings_reach_cluster_fulfillment(self):
        # CaaS on Netris (NETWORK_CLASS=netris): the installation-owned
        # connection settings come from the Netris settings under
        # networkFulfillment and reach config-as-code, which sets them on the
        # cluster-fulfillment container group; the Netris identity and
        # password stay in the cluster-fulfillment ConfigMap and Secret.
        docs = render(
            networking(fabricManager="netris"),
            {
                "aap": {
                    "instanceGroups": {
                        "clusterFulfillment": {
                            "enabled": True,
                            "config": {
                                "NETWORK_CLASS": "netris",
                                "NETWORK_STEPS_COLLECTION": "netris.steps",
                                "NETRIS_USERNAME": "netris",
                                "NETRIS_SITE_ID": "5",
                                "NETRIS_TENANT_ID": "1",
                                "NETRIS_TENANT_NAME": "Admin",
                            },
                            "secret": {"NETRIS_PASSWORD": "test-password"},
                        },
                        "networkFulfillment": {
                            "config": {
                                "NETRIS_CONTROLLER_URL": "https://netris.example.com",
                                "NETRIS_VALIDATE_CERTS": "false",
                                "NETRIS_CA_CONFIGMAP": "customer.netris-ca",
                            },
                        },
                    }
                }
            },
        )
        config_as_code = find(docs, "Secret", "config-as-code-ig")["stringData"]
        self.assertEqual(config_as_code["NETRIS_CONTROLLER_URL"], "https://netris.example.com")
        self.assertEqual(config_as_code["NETRIS_VALIDATE_CERTS"], "false")
        self.assertEqual(config_as_code["NETRIS_CA_CONFIGMAP"], "customer.netris-ca")
        cluster = find(docs, "ConfigMap", "cluster-fulfillment-ig")["data"]
        self.assertEqual(cluster["NETWORK_CLASS"], "netris")
        self.assertEqual(cluster["NETWORK_STEPS_COLLECTION"], "netris.steps")
        self.assertEqual(cluster["NETRIS_USERNAME"], "netris")
        self.assertNotIn("NETRIS_CONTROLLER_URL", cluster)
        secret = find(docs, "Secret", "cluster-fulfillment-ig")
        self.assertEqual(set(secret.get("data") or secret.get("stringData")), {"NETRIS_PASSWORD"})

    def test_cluster_fulfillment_rejects_installation_owned_netris_settings(self):
        for key in ("NETRIS_CONTROLLER_URL", "NETRIS_VALIDATE_CERTS", "NETRIS_CA_PATH"):
            with self.subTest(key=key):
                result = helm(
                    {"aap": {"instanceGroups": {"clusterFulfillment": {"enabled": True, "config": {key: "x"}}}}}
                )
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(key, result.stderr)

    def test_invalid_netris_ca_configmap_name_fails(self):
        result = helm(
            {
                "aap": {
                    "instanceGroups": {
                        "networkFulfillment": {"config": {"NETRIS_CA_CONFIGMAP": ".customer-netris-ca"}}
                    }
                }
            }
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("NETRIS_CA_CONFIGMAP must be a ConfigMap name", result.stderr)

    def test_netris_connection_settings_are_validated_for_cluster_fulfillment(self):
        def nf_config(config: dict) -> dict:
            return {"aap": {"instanceGroups": {"networkFulfillment": {"config": config}}}}

        rejected = {
            "NETRIS_VALIDATE_CERTS": ["", "flase", "no"],
            "NETRIS_CONTROLLER_URL": ["http://netris.example.com", "https://netris.example.com/a b"],
        }
        messages = {
            "NETRIS_VALIDATE_CERTS": 'NETRIS_VALIDATE_CERTS must be "true" or "false"',
            "NETRIS_CONTROLLER_URL": "NETRIS_CONTROLLER_URL must be an https:// URL",
        }
        for key, values in rejected.items():
            for value in values:
                with self.subTest(key=key, value=value):
                    result = helm(nf_config({key: value}))
                    self.assertNotEqual(result.returncode, 0)
                    self.assertIn(messages[key], result.stderr)
        docs = render(
            nf_config({"NETRIS_CONTROLLER_URL": "https://netris.example.com:8443/", "NETRIS_VALIDATE_CERTS": "True"})
        )
        config_as_code = find(docs, "Secret", "config-as-code-ig")["stringData"]
        self.assertEqual(config_as_code["NETRIS_CONTROLLER_URL"], "https://netris.example.com:8443/")
        self.assertEqual(config_as_code["NETRIS_VALIDATE_CERTS"], "true")

    def test_setting_names(self):
        result = helm({"aap": {"instanceGroups": {"networkFulfillment": {"config": {"lower_case": "x"}}}}})
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("lower_case", result.stderr)

    def test_schema_advertises_free_form_settings(self):
        schema = json.loads((CHART / "values.schema.json").read_text())
        nf = schema["properties"]["aap"]["properties"]["instanceGroups"]["properties"]["networkFulfillment"]
        self.assertIn("network-fulfillment-ig", nf["description"])
        settings = schema["$defs"]["managerSettings"]
        for key in ("config", "secret"):
            self.assertEqual(nf["properties"][key]["$ref"], "#/$defs/managerSettings")
        self.assertEqual(settings["additionalProperties"], {"type": "string"})
        self.assertNotIn("properties", settings)
        self.assertNotIn("aapNetrisConfig", schema["$defs"])


class SubchartPlumbingTest(unittest.TestCase):
    """Networking installation settings reach the operator, BMF and fulfillment service."""

    def test_generic_manager_registration_is_passed_to_operator_subchart(self):
        result = helm(
            networking(fabricManager="test_fabric"),
            {
                "operator": {
                    "networkManagers": {
                        "fabricManagers": {
                            "test_fabric": {
                                "enabled": True,
                                "description": "Contract test fabric manager",
                                "capabilities": ["ipv4"],
                            }
                        }
                    }
                }
            },
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        docs = [doc for doc in yaml.safe_load_all(result.stdout) if doc]
        config_map = find(docs, "ConfigMap", "osac-network-fabric-manager-test-fabric")
        self.assertEqual(config_map["data"]["name"], "test_fabric")
        self.assertEqual(config_map["data"]["capabilities"], "ipv4")

    def test_k8s_only_advertises_external_ip_contract_v1(self):
        # OSAC-6779: MetalLB allocate/release on k8s_only uses the v1 result contract.
        result = helm(
            {
                "operator": {
                    "networkManagers": {
                        "k8sManagers": {
                            "k8s_only": {
                                "enabled": True,
                            }
                        }
                    }
                }
            },
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        docs = [doc for doc in yaml.safe_load_all(result.stdout) if doc]
        config_map = find(docs, "ConfigMap", "osac-network-k8s-manager-k8s-only")
        self.assertEqual(config_map["data"]["name"], "k8s_only")
        self.assertEqual(config_map["data"]["externalIPContractVersion"], "v1")

    @staticmethod
    def deployment_env(docs, name_part: str) -> dict:
        envs = {}
        for doc in docs:
            if doc["kind"] == "Deployment" and name_part in doc["metadata"]["name"]:
                for c in doc["spec"]["template"]["spec"]["containers"]:
                    envs.update({e["name"]: e.get("value") for e in c.get("env", [])})
        return envs

    @staticmethod
    def fulfillment_args(docs) -> list[str]:
        args = []
        for doc in docs:
            if doc["kind"] == "Deployment" and doc["metadata"]["name"].startswith("fulfillment-"):
                for c in doc["spec"]["template"]["spec"]["containers"]:
                    args.extend(c.get("args") or [])
                    args.extend(c.get("command") or [])
        return args

    def test_provisioning_enabled(self):
        for enabled in (True, False):
            with self.subTest(enabled=enabled):
                docs = render(
                    networking(provisioningEnabled=enabled, excludedIpv4Cidrs=["192.0.2.0/24", "198.51.100.0/24"])
                )
                value = str(enabled).lower()
                self.assertEqual(self.deployment_env(docs, "osac-operator").get("OSAC_PROVISIONING_ENABLED"), value)
                args = self.fulfillment_args(docs)
                self.assertIn(f"--networking-provisioning-enabled={value}", args)
                self.assertIn("--networking-excluded-ipv4-cidrs=192.0.2.0/24,198.51.100.0/24", args)


LAB_OVERRIDES = {
    "global": {
        "clusterDomain": "example.com",
        "networking": {
            "excludedIpv4Cidrs": ["10.0.0.0/8", "192.168.16.0/20"],
            "networkClass": {
                "defaults": {"virtualNetworkIPv4CIDR": "192.168.101.0/24", "subnetIPv4CIDR": "192.168.101.0/24"}
            },
        },
    },
    "service": {"externalHostname": "api.example.com", "internalHostname": "internal-api.example.com"},
    "ui": {"auth": {"oidcIssuerURL": "https://keycloak.example.com/realms/osac"}},
    "aap": {
        "instanceGroups": {
            "networkFulfillment": {
                "config": {"NETRIS_CONTROLLER_URL": "https://netris.example.com", "NETRIS_SITE_ID": "1"},
                "secret": {"NETRIS_PASSWORD": "test"},
            }
        }
    },
}


class NetrisOverlayTest(unittest.TestCase):
    """values/<profile>/netris.yaml on its base profile, with lab-shaped overrides."""

    def overlay_docs(self, profile: str) -> list[dict[str, Any]]:
        with tempfile.NamedTemporaryFile("w", suffix=".json") as lab:
            json.dump(LAB_OVERRIDES, lab)
            lab.flush()
            result = subprocess.run(
                [
                    HELM,
                    "template",
                    "osac",
                    str(CHART),
                    "--namespace",
                    "osac",
                    "--values",
                    str(INSTALLER / "values" / profile / "instance.yaml"),
                    "--values",
                    str(INSTALLER / "values" / profile / "netris.yaml"),
                    "--values",
                    lab.name,
                ],
                capture_output=True,
                text=True,
            )
        self.assertEqual(result.returncode, 0, result.stderr)
        return [doc for doc in yaml.safe_load_all(result.stdout) if doc]

    def test_overlays_select_the_managers_of_each_service(self):
        cases = {
            "vmaas-ci": ("cudn_evpn", {"overlay": 0.75, "fabric": 0.25, "lb_vip": 0}),
            "bmaas-ci": ("", {"overlay": 0, "fabric": 1, "lb_vip": 0}),
            "full-ci": ("cudn_evpn", {"overlay": 0.5, "fabric": 0.25, "lb_vip": 0.25}),
        }
        for profile, (k8s_manager, plan) in cases.items():
            with self.subTest(profile):
                docs = self.overlay_docs(profile)
                step = step_input(docs)
                self.assertEqual(step["fabric_manager"], "netris")
                self.assertEqual(step["k8s_manager"], k8s_manager)
                self.assertEqual(step["address_plan"], plan)
                self.assertIs(step["provisioning_enabled"], True)
                netris = find(docs, "ConfigMap", "osac-network-fabric-manager-netris")["data"]
                self.assertEqual(netris["capabilities"], "eastWestEthernet,security_group_icmp")
                settings = find(docs, "ConfigMap", "network-fulfillment-ig")["data"]
                self.assertEqual(settings["NETRIS_VALIDATE_CERTS"], "false")
                if k8s_manager:
                    find(docs, "ConfigMap", "osac-network-k8s-manager-cudn-evpn")
                else:
                    # BMaaS keeps no Kubernetes Manager, so its ICMP rules stay valid.
                    names = {d["metadata"]["name"] for d in docs if d.get("kind") == "ConfigMap"}
                    self.assertNotIn("osac-network-k8s-manager-cudn-evpn", names)

    def test_vmaas_overlay_polls_aap_status_every_five_seconds(self):
        # An unparsable value silently falls back to 30s, so check the rendered variable, not just the value.
        docs = self.overlay_docs("vmaas-ci")
        values = {}
        for doc in docs:
            if doc.get("kind") != "Deployment":
                continue
            for container in doc["spec"]["template"]["spec"]["containers"]:
                for env in container.get("env", []):
                    if env["name"] == "OSAC_AAP_STATUS_POLL_INTERVAL":
                        values[doc["metadata"]["name"]] = env["value"]
        self.assertEqual(list(values.values()), ["5s"], values)


if __name__ == "__main__":
    unittest.main()
