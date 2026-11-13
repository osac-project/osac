# Copyright: (c) 2026, VAST Data
# Apache License 2.0 (see LICENSE or https://www.apache.org/licenses/LICENSE-2.0)
# SPDX-License-Identifier: Apache-2.0
"""Normalize VMS network-settings reads and construct complete write bodies."""

from typing import Any, Dict, List

HOST_ECHO_KEYS = ("id", "hostname", "nb_eth_mtu", "nb_ib_mtu", "mgmt_ip", "ipmi_ip")
WRITE_ECHO_KEYS = (
    "management_vips",
    "external_gateways",
    "ntp",
    "dns",
    "ext_netmask",
    "auto_ports_ext_iface",
    "b2b_ipmi",
    "eth_mtu",
    "ib_mtu",
    "ipmi_gateway",
    "ipmi_netmask",
)


def unwrap_data_envelope(value: Any) -> Any:
    """Unwrap a singleton ``{"data": {...}}`` response envelope."""
    if isinstance(value, dict) and "data" in value and isinstance(value["data"], dict):
        return value["data"]
    return value


def hosts_from_boxes(current: Dict[str, Any]) -> List[Dict[str, Any]]:
    """Flatten GET ``boxes[].hosts`` into the identity fields accepted on write."""
    hosts: List[Dict[str, Any]] = []
    for box in current.get("boxes") or []:
        for host in box.get("hosts") or []:
            entry = {key: host[key] for key in HOST_ECHO_KEYS if host.get(key) is not None}
            if entry:
                hosts.append(entry)
    return hosts


def network_settings_response_normalizer(results: List[Any]) -> List[Dict[str, Any]]:
    """Unwrap network-settings envelopes and expose write-compatible hosts."""
    normalized: List[Dict[str, Any]] = []
    for item in results:
        resource = unwrap_data_envelope(item)
        if not isinstance(resource, dict):
            continue
        resource = dict(resource)
        if "hosts" not in resource:
            resource["hosts"] = hosts_from_boxes(resource)
        normalized.append(resource)
    return normalized


def build_write_body(current: Dict[str, Any], desired: Dict[str, Any]) -> Dict[str, Any]:
    """Merge desired values into the complete body required by VMS."""
    settings = unwrap_data_envelope(current)
    if not isinstance(settings, dict):
        settings = {}
    body = {key: settings[key] for key in WRITE_ECHO_KEYS if settings.get(key) is not None}
    body["hosts"] = settings.get("hosts") or hosts_from_boxes(settings)
    body.update(desired)
    return body
