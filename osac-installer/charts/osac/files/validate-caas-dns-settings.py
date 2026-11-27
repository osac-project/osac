#!/usr/bin/env python3
"""Guard CaaS DNS settings against live Clusters before an upgrade."""

from __future__ import annotations

import json
import os
import ssl
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request

CLUSTER_FULFILLMENT_CONFIG = "cluster-fulfillment-ig"
FIELDS = ("EXTERNAL_ACCESS_BASE_DOMAIN", "DNS_ZONE", "DNS_CLASS")
DEFAULT_DNS_CLASS = "dns.route53.dns"


def effective_settings(values: dict[str, str]) -> dict[str, str]:
    external_domain = values.get("EXTERNAL_ACCESS_BASE_DOMAIN", "")
    return {
        "EXTERNAL_ACCESS_BASE_DOMAIN": external_domain,
        "DNS_ZONE": values.get("DNS_ZONE") or external_domain,
        "DNS_CLASS": values.get("DNS_CLASS") or DEFAULT_DNS_CLASS,
    }


def changed_settings(previous: dict[str, str], proposed: dict[str, str]) -> list[str]:
    old = effective_settings(previous)
    new = effective_settings(proposed)
    return [field for field in FIELDS if old[field] != new[field]]


def read_previous_settings(namespace: str) -> dict[str, str] | None:
    result = subprocess.run(
        ["oc", "get", "configmap", CLUSTER_FULFILLMENT_CONFIG, "-n", namespace, "-o", "json"],
        capture_output=True,
        text=True,
        check=False,
        timeout=30,
    )
    if result.returncode != 0:
        if "NotFound" in result.stderr or "not found" in result.stderr.lower():
            return None
        raise RuntimeError("could not read the running cluster-fulfillment configuration")
    try:
        data = json.loads(result.stdout).get("data", {})
    except (TypeError, ValueError) as err:
        raise RuntimeError("running cluster-fulfillment configuration is unreadable") from err
    return {field: str(data.get(field, "")) for field in FIELDS}


def has_live_clusters() -> bool:
    base_url = os.environ.get("FULFILLMENT_URL", "").rstrip("/")
    token = os.environ.get("AUTH_TOKEN", "")
    if not base_url or not token:
        raise RuntimeError("Fulfillment API credentials are unavailable")
    query = urllib.parse.urlencode({"limit": 1})
    request = urllib.request.Request(f"{base_url}/api/private/v1/clusters?{query}")
    request.add_header("Authorization", f"Bearer {token}")
    ca_file = os.environ.get("FULFILLMENT_CA_FILE")
    context = ssl.create_default_context(cafile=ca_file or None)
    try:
        with urllib.request.urlopen(request, timeout=30, context=context) as response:
            data = json.loads(response.read().decode() or "{}")
    except (urllib.error.URLError, TimeoutError, ValueError) as err:
        raise RuntimeError("could not list Clusters through the Fulfillment API") from err
    return bool(data.get("items")) or int(data.get("total", 0) or 0) > 0


def validate_settings(
    previous: dict[str, str] | None,
    proposed: dict[str, str],
    live_clusters: bool,
) -> None:
    if previous is None:
        if live_clusters:
            raise RuntimeError(
                "cannot verify the previous CaaS DNS settings while Clusters exist; "
                "delete every Cluster before installing these settings"
            )
        return
    changes = changed_settings(previous, proposed)
    if live_clusters and changes:
        field = changes[0]
        raise RuntimeError(f"{field} cannot change while Clusters exist; delete every Cluster first")


def main() -> int:
    namespace = os.environ.get("RELEASE_NAMESPACE", "")
    if not namespace:
        print("ERROR: installer release namespace is unavailable", file=sys.stderr)
        return 1
    proposed = {field: os.environ.get(field, "") for field in FIELDS}
    if len(sys.argv) > 1 and sys.argv[1] == "--check-unchanged":
        try:
            previous = read_previous_settings(namespace)
        except Exception:
            return 2
        if previous is not None and not changed_settings(previous, proposed):
            return 0
        return 2
    try:
        previous = read_previous_settings(namespace)
        if previous is not None and not changed_settings(previous, proposed):
            print("CaaS DNS settings are unchanged; skipping the live Cluster check.")
            return 0
        live_clusters = has_live_clusters()
        validate_settings(previous, proposed, live_clusters)
    except Exception as err:  # noqa: BLE001 - fail closed with a sanitized message
        print(f"ERROR: CaaS DNS settings validation failed: {err}", file=sys.stderr)
        return 1
    print("CaaS DNS settings are safe to apply.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
