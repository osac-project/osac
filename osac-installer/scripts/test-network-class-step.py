#!/usr/bin/env python3
"""Unit tests for the installer's NetworkClass step (charts/osac/files/network-class-step.py)."""

from __future__ import annotations

import copy
import http.server
import importlib.util
import json
import pathlib
import threading
import unittest

SCRIPT = pathlib.Path(__file__).resolve().parents[1] / "charts/osac/files/network-class-step.py"
spec = importlib.util.spec_from_file_location("network_class_step", SCRIPT)
step = importlib.util.module_from_spec(spec)
spec.loader.exec_module(step)

INPUT = {
    "fabric_manager": "netris",
    "k8s_manager": "cudn_evpn",
    "defaults": {
        "virtual_network_ipv4_cidr": "10.200.0.0/16",
        "subnet_ipv4_cidr": "10.200.0.0/20",
        "ingress_rules": [],
        "egress_rules": [{"protocol": "PROTOCOL_ALL", "ipv4_cidr": "0.0.0.0/0"}],
    },
    "east_west_capabilities": {"supports_east_west_ethernet": False},
    "address_plan": {"overlay": 0.5, "fabric": 0.25, "lb_vip": 0.25},
    "address_plan_explicit": False,
    "services": {"vmaas": True, "bmaas": True, "caas": True},
    "excluded_ipv4_cidrs": ["192.0.2.0/24"],
    "provisioning_enabled": True,
}


def stored_class(**changes) -> dict:
    """A NetworkClass as the private API returns it for INPUT."""
    nc = {
        "id": "nc-1",
        "title": step.TITLE,
        "fabric_manager": "netris",
        "k8s_manager": "cudn_evpn",
        "spec": {
            "defaults": {
                "virtual_network_ipv4_cidr": "10.200.0.0/16",
                "subnet_ipv4_cidr": "10.200.0.0/20",
                "egress_rules": [{"protocol": "PROTOCOL_ALL", "ipv4_cidr": "0.0.0.0/0"}],
            },
            "east_west_capabilities": {},
            "address_plan": {"overlay": 0.5, "fabric": 0.25, "lb_vip": 0.25},
        },
    }
    nc.update(changes)
    return nc


class FakeAPI:
    """A fake fulfillment private API served over HTTP."""

    def __init__(self):
        self.network_classes: list[dict] = []
        self.virtual_networks: list[dict] = []
        self.requests: list[tuple[str, str, dict | None]] = []
        # Queued error responses per method: (HTTP status, gRPC code, message).
        self.errors: dict[str, list[tuple[int, int, str]]] = {}
        # Called after a PATCH error is returned, to model a VirtualNetwork appearing.
        api = self

        class Handler(http.server.BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def _reply(self, status: int, body: dict):
                data = json.dumps(body).encode()
                self.send_response(status)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(data)))
                self.end_headers()
                self.wfile.write(data)

            def _handle(self, method: str):
                length = int(self.headers.get("Content-Length") or 0)
                body = json.loads(self.rfile.read(length)) if length else None
                api.requests.append((method, self.path, body))
                assert self.headers["Authorization"] == "Bearer token"
                queued = api.errors.get(method) or []
                if queued:
                    status, code, message = queued.pop(0)
                    return self._reply(status, {"code": code, "message": message})
                path = self.path.split("?")[0]
                if method == "GET" and path == step.NETWORK_CLASSES:
                    return self._reply(200, {"items": api.network_classes} if api.network_classes else {})
                if method == "GET" and path == step.VIRTUAL_NETWORKS:
                    return self._reply(200, {"items": api.virtual_networks, "total": len(api.virtual_networks)})
                if method == "POST" and path == step.NETWORK_CLASSES:
                    created = dict(body, id="nc-new")
                    api.network_classes.append(created)
                    return self._reply(200, created)
                if method == "PATCH" and path == f"{step.NETWORK_CLASSES}/nc-1":
                    api.network_classes[0]["spec"]["address_plan"] = body["spec"]["address_plan"]
                    return self._reply(200, api.network_classes[0])
                return self._reply(404, {"code": 5, "message": "not found"})

            def do_GET(self):
                self._handle("GET")

            def do_POST(self):
                self._handle("POST")

            def do_PATCH(self):
                self._handle("PATCH")

        self.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.client = step.Client(f"http://127.0.0.1:{self.server.server_port}", "token")

    def close(self):
        self.server.shutdown()
        self.server.server_close()

    def methods(self) -> list[str]:
        return [method for method, _, _ in self.requests]


class NetworkClassStepTest(unittest.TestCase):
    def setUp(self):
        self.api = FakeAPI()
        self.addCleanup(self.api.close)
        self.input = copy.deepcopy(INPUT)
        self.sleeps: list[float] = []

    def run_step(self, attempts: int = 5) -> str:
        return step.run(self.api.client, self.input, attempts=attempts, delay=1, sleep=self.sleeps.append)

    def assert_fails(self, message: str):
        with self.assertRaises(step.StepError) as ctx:
            self.run_step()
        self.assertEqual(str(ctx.exception), message)

    def test_creates_when_none_exists(self):
        self.assertEqual(self.run_step(), "created")
        method, path, body = self.api.requests[-1]
        self.assertEqual((method, path), ("POST", step.NETWORK_CLASSES))
        self.assertEqual(body["fabric_manager"], "netris")
        self.assertEqual(body["k8s_manager"], "cudn_evpn")
        self.assertEqual(body["title"], step.TITLE)
        self.assertEqual(body["spec"]["defaults"], INPUT["defaults"])
        self.assertEqual(body["spec"]["east_west_capabilities"], {"supports_east_west_ethernet": False})
        self.assertEqual(body["spec"]["address_plan"], {"overlay": 0.5, "fabric": 0.25, "lb_vip": 0.25})

    def test_create_sends_plan_as_floats_and_omits_empty_k8s_manager(self):
        self.input.update(
            k8s_manager="",
            address_plan={"overlay": 0, "fabric": 1, "lb_vip": 0},
            services={"vmaas": False, "bmaas": True, "caas": False},
        )
        self.run_step()
        body = self.api.requests[-1][2]
        self.assertNotIn("k8s_manager", body)
        for share in ("overlay", "fabric", "lb_vip"):
            self.assertIsInstance(body["spec"]["address_plan"][share], float)

    def test_create_retries_on_failed_precondition(self):
        self.api.errors["POST"] = [(400, step.FAILED_PRECONDITION, "previous NetworkClass is deleting")] * 2
        self.assertEqual(self.run_step(), "created")
        self.assertEqual(self.api.methods().count("POST"), 3)
        self.assertEqual(self.sleeps, [1, 1])

    def test_create_retries_on_conflict(self):
        self.api.errors["POST"] = [(409, step.ALREADY_EXISTS, "exists")]
        self.assertEqual(self.run_step(), "created")
        self.assertEqual(len(self.sleeps), 1)

    def test_create_gives_up_after_attempts(self):
        self.api.errors["POST"] = [(400, step.FAILED_PRECONDITION, "deleting")] * 5
        with self.assertRaises(step.StepError) as ctx:
            self.run_step(attempts=3)
        self.assertIn("creating the NetworkClass failed", str(ctx.exception))

    def test_create_surfaces_invalid_argument(self):
        self.api.errors["POST"] = [(400, 3, "virtual_network_ipv4_cidr overlaps excludedIpv4Cidrs")]
        with self.assertRaises(step.StepError) as ctx:
            self.run_step()
        self.assertIn("virtual_network_ipv4_cidr overlaps excludedIpv4Cidrs", str(ctx.exception))
        self.assertEqual(self.sleeps, [])

    def test_unchanged_class_is_left_alone(self):
        self.api.network_classes = [stored_class()]
        self.assertEqual(self.run_step(), "unchanged")
        self.assertEqual(self.api.methods(), ["GET"])

    def test_one_changed_field(self):
        self.api.network_classes = [stored_class(fabric_manager="agentless_net")]
        self.assert_fails(
            "NetworkClass fabric_manager cannot change after installation; see Replacing the NetworkClass"
        )

    def test_changed_fields_are_listed_in_order(self):
        nc = stored_class(k8s_manager="")
        nc["spec"]["east_west_capabilities"] = {"supports_east_west_ethernet": True}
        nc["spec"]["defaults"]["subnet_ipv4_cidr"] = "10.200.0.0/24"
        self.api.network_classes = [nc]
        self.assert_fails(
            "NetworkClass k8s_manager, spec.defaults, spec.east_west_capabilities cannot change after "
            "installation; see Replacing the NetworkClass"
        )

    def test_changed_rule_is_a_defaults_change(self):
        nc = stored_class()
        nc["spec"]["defaults"]["egress_rules"] = [
            {"protocol": "PROTOCOL_TCP", "ipv4_cidr": "0.0.0.0/0", "port_from": 1, "port_to": 2}
        ]
        self.api.network_classes = [nc]
        self.assert_fails("NetworkClass spec.defaults cannot change after installation; see Replacing the NetworkClass")

    def test_plan_change_fails_without_virtual_networks(self):
        # The NetworkClass spec, address plan included, is immutable after creation.
        nc = stored_class()
        nc["spec"]["address_plan"] = {"overlay": 0.75, "fabric": 0.25}
        self.api.network_classes = [nc]
        self.assert_fails(
            "NetworkClass spec.address_plan cannot change after installation; see Replacing the NetworkClass"
        )
        self.assertNotIn("PATCH", self.api.methods())

    def test_plan_change_fails_with_other_changes(self):
        nc = stored_class(k8s_manager=None)
        nc["spec"]["address_plan"] = {"overlay": 0.75, "fabric": 0.25}
        self.api.network_classes = [nc]
        self.assert_fails(
            "NetworkClass k8s_manager, spec.address_plan cannot change after installation; "
            "see Replacing the NetworkClass"
        )

    def test_derived_plan_change_also_fails(self):
        nc = stored_class()
        nc["spec"]["address_plan"] = {"overlay": 0.75, "fabric": 0.25}
        self.api.network_classes = [nc]
        self.input["address_plan_explicit"] = False
        self.assert_fails(
            "NetworkClass spec.address_plan cannot change after installation; see Replacing the NetworkClass"
        )

    def test_more_than_one_class_fails(self):
        self.api.network_classes = [stored_class(), stored_class(id="nc-2")]
        self.assert_fails("expected at most one NetworkClass, found 2")


class ValidationTest(unittest.TestCase):
    def setUp(self):
        self.input = copy.deepcopy(INPUT)

    def assert_invalid(self, message: str):
        with self.assertRaises(step.StepError) as ctx:
            step.validate(self.input)
        self.assertEqual(str(ctx.exception), message)

    def test_valid_input(self):
        step.validate(self.input)

    def test_removed_resolver_input_is_ignored(self):
        self.input["cudn_evpn_dns_resolvers"] = "dns.example.com"  # removed input
        step.validate(self.input)

    def test_plan_rules(self):
        cases = [
            ({"overlay": 0.5, "fabric": 0.25, "lb_vip": 0.125}, "shares must sum to exactly 1 (got 0.875)"),
            (
                {"overlay": 0.3, "fabric": 0.5, "lb_vip": 0.2},
                ".overlay must be a multiple of 0.0625 from 0 to 1 (got 0.3)",
            ),
            (
                {"overlay": -0.25, "fabric": 1, "lb_vip": 0.25},
                ".overlay must be a multiple of 0.0625 from 0 to 1 (got -0.25)",
            ),
            (
                {"overlay": float("nan"), "fabric": 0.5, "lb_vip": 0.5},
                ".overlay must be a multiple of 0.0625 from 0 to 1 (got nan)",
            ),
            (
                {"overlay": 0, "fabric": 0.75, "lb_vip": 0.25},
                ".overlay must be greater than 0 exactly when global.networking.k8sManager is set",
            ),
            (
                {"overlay": 0.5, "fabric": 0, "lb_vip": 0.5},
                ".fabric must be greater than 0 when BMaaS or CaaS is enabled",
            ),
            ({"overlay": 0.5, "fabric": 0.5, "lb_vip": 0}, ".lbVip must be greater than 0 when CaaS is enabled"),
        ]
        for plan, suffix in cases:
            with self.subTest(plan=plan):
                self.input["address_plan"] = plan
                self.assert_invalid(step.PLAN_PATH + (" " if suffix.startswith("shares") else "") + suffix)

    def test_prefix_limits(self):
        for plan, limit in [
            ({"overlay": 0, "fabric": 1, "lb_vip": 0}, 28),
            ({"overlay": 0.5, "fabric": 0.5, "lb_vip": 0}, 27),
            ({"overlay": 0.5, "fabric": 0.25, "lb_vip": 0.25}, 26),
            ({"overlay": 0.375, "fabric": 0.375, "lb_vip": 0.25}, 25),
            ({"overlay": 0.5625, "fabric": 0.1875, "lb_vip": 0.25}, 24),
        ]:
            with self.subTest(plan=plan):
                self.assertEqual(step.prefix_limit(plan), limit)

    def test_subnet_prefix_over_limit(self):
        self.input["defaults"]["subnet_ipv4_cidr"] = "10.200.0.0/27"
        self.assert_invalid(
            f"{step.DEFAULTS_PATH}.subnetIPv4CIDR prefix /27 is longer than the address plan's prefix limit /26"
        )

    def test_cidrs(self):
        self.input["defaults"]["virtual_network_ipv4_cidr"] = "10.200.0.1/16"
        self.assert_invalid(
            f"{step.DEFAULTS_PATH}.virtualNetworkIPv4CIDR must be a canonical IPv4 CIDR (got 10.200.0.1/16)"
        )
        self.input["defaults"]["virtual_network_ipv4_cidr"] = "10.201.0.0/16"
        self.assert_invalid(f"{step.DEFAULTS_PATH}.subnetIPv4CIDR must be inside virtualNetworkIPv4CIDR")

    def test_virtual_network_overlaps_excluded_range(self):
        self.input["excluded_ipv4_cidrs"] = ["192.0.2.0/24", "10.200.10.0/24"]
        self.assert_invalid(
            "excludedIpv4Cidrs range 10.200.10.0/24 overlaps the default VirtualNetwork CIDR 10.200.0.0/16"
        )

    def test_rule_ports(self):
        self.input["defaults"]["ingress_rules"] = [
            {"protocol": "PROTOCOL_TCP", "ipv4_cidr": "0.0.0.0/0", "port_from": 80, "port_to": 79}
        ]
        self.assert_invalid(f"{step.DEFAULTS_PATH}.ingressRules[0]: portFrom (80) must be <= portTo (79)")


if __name__ == "__main__":
    unittest.main()
