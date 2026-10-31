#!/usr/bin/env python3
"""The installer's NetworkClass step.

Creates the NetworkClass through the fulfillment private API when none exists.
When one exists, it fails on a changed fabric_manager, k8s_manager,
spec.defaults, spec.east_west_capabilities or spec.address_plan: the
NetworkClass spec is immutable after creation.

Input: the JSON in NETWORK_CLASS_STEP_INPUT (rendered by the osac chart's
osac.networking.stepInput helper), the access token in AUTH_TOKEN, the API URL
in FULFILLMENT_URL and the CA bundle in FULFILLMENT_CA_FILE.
"""

from __future__ import annotations

import ipaddress
import json
import os
import ssl
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

NETWORK_CLASSES = "/api/private/v1/network_classes"
TITLE = "OSAC NetworkClass"
DESCRIPTION = "Default NetworkClass created by the OSAC installer."

# gRPC status codes in grpc-gateway error bodies.
ALREADY_EXISTS = 6
FAILED_PRECONDITION = 9

DEFAULTS_PATH = "global.networking.networkClass.defaults"
PLAN_PATH = "global.networking.networkClass.addressPlan"
PLAN_SHARES = (("overlay", "overlay"), ("fabric", "fabric"), ("lb_vip", "lbVip"))


class StepError(Exception):
    """A failure that ends the step with its message."""


class APIError(Exception):
    def __init__(self, status: int, body: dict):
        self.status = status
        self.code = body.get("code") if isinstance(body, dict) else None
        self.message = body.get("message", "") if isinstance(body, dict) else ""
        super().__init__(f"HTTP {status}: {self.message or body}")

    @property
    def retryable(self) -> bool:
        return self.status == 409 or self.code in (ALREADY_EXISTS, FAILED_PRECONDITION)

    @property
    def failed_precondition(self) -> bool:
        return self.code == FAILED_PRECONDITION


class Client:
    def __init__(self, base_url: str, token: str, ca_file: str | None = None):
        self.base_url = base_url.rstrip("/")
        self.token = token
        self.context = ssl.create_default_context(cafile=ca_file) if ca_file else None

    def request(self, method: str, path: str, body: dict | None = None) -> dict:
        data = json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request(self.base_url + path, data=data, method=method)
        req.add_header("Authorization", f"Bearer {self.token}")
        if data is not None:
            req.add_header("Content-Type", "application/json")
        try:
            with urllib.request.urlopen(req, timeout=30, context=self.context) as resp:
                text = resp.read().decode()
        except urllib.error.HTTPError as err:
            text = err.read().decode()
            try:
                parsed = json.loads(text)
            except ValueError:
                parsed = {"message": text}
            raise APIError(err.code, parsed) from None
        return json.loads(text) if text else {}


def _network(value: str, field: str) -> ipaddress.IPv4Network:
    try:
        return ipaddress.IPv4Network(value, strict=True)
    except (ValueError, TypeError):
        raise StepError(f"{field} must be a canonical IPv4 CIDR (got {value})") from None


def prefix_limit(plan: dict) -> int:
    sixteenths = [round(plan[share] * 16) for share, _ in PLAN_SHARES if plan[share] > 0]
    for exp, m in reversed(list(enumerate((1, 2, 4, 8, 16)))):
        if all(n % m == 0 for n in sixteenths):
            return 24 + exp
    return 24


def normalize_plan(plan: dict | None) -> dict:
    plan = plan or {}
    return {share: float(plan.get(share) or 0) for share, _ in PLAN_SHARES}


def validate(inp: dict) -> None:
    """Re-check the values the chart validated, plus the rules render time cannot."""
    services = inp.get("services", {})
    k8s_manager = inp.get("k8s_manager") or ""
    plan = normalize_plan(inp["address_plan"])
    for share, name in PLAN_SHARES:
        value = plan[share]
        if value != value or not 0 <= value <= 1 or value * 16 != int(value * 16):
            raise StepError(f"{PLAN_PATH}.{name} must be a multiple of 0.0625 from 0 to 1 (got {value})")
    total = sum(plan.values())
    if total != 1:
        raise StepError(f"{PLAN_PATH} shares must sum to exactly 1 (got {total})")
    if (plan["overlay"] > 0) != bool(k8s_manager):
        raise StepError(f"{PLAN_PATH}.overlay must be greater than 0 exactly when global.networking.k8sManager is set")
    if services.get("vmaas") and not k8s_manager:
        raise StepError("global.networking.k8sManager is required when VMaaS is enabled (set cudn_evpn)")
    if (services.get("bmaas") or services.get("caas")) and plan["fabric"] == 0:
        raise StepError(f"{PLAN_PATH}.fabric must be greater than 0 when BMaaS or CaaS is enabled")
    if services.get("caas") and plan["lb_vip"] == 0:
        raise StepError(f"{PLAN_PATH}.lbVip must be greater than 0 when CaaS is enabled")

    defaults = inp["defaults"]
    vn = _network(defaults["virtual_network_ipv4_cidr"], f"{DEFAULTS_PATH}.virtualNetworkIPv4CIDR")
    subnet = _network(defaults["subnet_ipv4_cidr"], f"{DEFAULTS_PATH}.subnetIPv4CIDR")
    if not subnet.subnet_of(vn):
        raise StepError(f"{DEFAULTS_PATH}.subnetIPv4CIDR must be inside virtualNetworkIPv4CIDR")
    limit = prefix_limit(plan)
    if subnet.prefixlen > limit:
        raise StepError(
            f"{DEFAULTS_PATH}.subnetIPv4CIDR prefix /{subnet.prefixlen} is longer than "
            f"the address plan's prefix limit /{limit}"
        )
    for direction, name in (("ingress_rules", "ingressRules"), ("egress_rules", "egressRules")):
        for i, rule in enumerate(defaults.get(direction) or []):
            _network(rule.get("ipv4_cidr"), f"{DEFAULTS_PATH}.{name}[{i}].ipv4Cidr")
            if "port_from" in rule and "port_to" in rule and rule["port_from"] > rule["port_to"]:
                raise StepError(
                    f"{DEFAULTS_PATH}.{name}[{i}]: portFrom ({rule['port_from']}) must be <= portTo ({rule['port_to']})"
                )

    excluded = [
        _network(cidr, "global.networking.excludedIpv4Cidrs entry") for cidr in inp.get("excluded_ipv4_cidrs") or []
    ]
    for cidr in excluded:
        if cidr.overlaps(vn):
            raise StepError(f"excludedIpv4Cidrs range {cidr} overlaps the default VirtualNetwork CIDR {vn}")


def _rules(rules: list | None) -> list:
    return [
        {
            "protocol": rule.get("protocol", ""),
            "ipv4_cidr": rule.get("ipv4_cidr", ""),
            "port_from": rule.get("port_from"),
            "port_to": rule.get("port_to"),
        }
        for rule in rules or []
    ]


def _defaults(defaults: dict | None) -> dict:
    defaults = defaults or {}
    return {
        "virtual_network_ipv4_cidr": defaults.get("virtual_network_ipv4_cidr", ""),
        "subnet_ipv4_cidr": defaults.get("subnet_ipv4_cidr", ""),
        "ingress_rules": _rules(defaults.get("ingress_rules")),
        "egress_rules": _rules(defaults.get("egress_rules")),
    }


def changed_fields(stored: dict, inp: dict) -> list[str]:
    spec = stored.get("spec") or {}
    stored_ew = bool((spec.get("east_west_capabilities") or {}).get("supports_east_west_ethernet"))
    wanted_ew = bool(inp["east_west_capabilities"].get("supports_east_west_ethernet"))
    fields = []
    if (stored.get("fabric_manager") or "") != inp["fabric_manager"]:
        fields.append("fabric_manager")
    if (stored.get("k8s_manager") or "") != (inp.get("k8s_manager") or ""):
        fields.append("k8s_manager")
    if _defaults(spec.get("defaults")) != _defaults(inp["defaults"]):
        fields.append("spec.defaults")
    if stored_ew != wanted_ew:
        fields.append("spec.east_west_capabilities")
    return fields


def create_body(inp: dict) -> dict:
    body = {
        "title": TITLE,
        "description": DESCRIPTION,
        "fabric_manager": inp["fabric_manager"],
        "spec": {
            "defaults": inp["defaults"],
            "east_west_capabilities": inp["east_west_capabilities"],
            "address_plan": normalize_plan(inp["address_plan"]),
        },
    }
    if inp.get("k8s_manager"):
        body["k8s_manager"] = inp["k8s_manager"]
    return body


def run(client: Client, inp: dict, attempts: int = 30, delay: float = 10, sleep=time.sleep) -> str:
    """Run the step; return what it did, or raise StepError."""
    validate(inp)
    plan = normalize_plan(inp["address_plan"])
    for attempt in range(1, attempts + 1):
        items = client.request("GET", NETWORK_CLASSES).get("items") or []
        if len(items) > 1:
            raise StepError(f"expected at most one NetworkClass, found {len(items)}")
        if not items:
            try:
                client.request("POST", NETWORK_CLASSES, create_body(inp))
                return "created"
            except APIError as err:
                # A previous NetworkClass may still be deleting (Replacing the NetworkClass).
                if err.retryable and attempt < attempts:
                    print(f"NetworkClass create not possible yet ({err}); retrying", file=sys.stderr)
                    sleep(delay)
                    continue
                raise StepError(f"creating the NetworkClass failed: {err}") from None

        stored = items[0]
        fields = changed_fields(stored, inp)
        stored_plan = normalize_plan((stored.get("spec") or {}).get("address_plan"))
        if stored_plan != plan:
            # The plan in force (explicit or derived from the enabled services) must match: the NetworkClass spec,
            # address plan included, never changes after installation.
            fields.append("spec.address_plan")
        if fields:
            raise StepError(
                f"NetworkClass {', '.join(fields)} cannot change after installation; see Replacing the NetworkClass"
            )
        return "unchanged"
    raise StepError("NetworkClass step did not finish")


def main() -> int:
    inp = json.loads(os.environ["NETWORK_CLASS_STEP_INPUT"])
    client = Client(
        os.environ.get("FULFILLMENT_URL", "https://fulfillment-internal-api:8001"),
        os.environ["AUTH_TOKEN"],
        os.environ.get("FULFILLMENT_CA_FILE") or None,
    )
    try:
        result = run(client, inp)
    except StepError as err:
        print(f"ERROR: {err}", file=sys.stderr)
        return 1
    print(f"NetworkClass {result}.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
