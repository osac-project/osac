# Copyright (c) 2026 Yoni Bettan.
# Licensed under the Apache License, Version 2.0.

"""Normalize provider switch output into provider-neutral state.

Switch providers return command output in their own format.  These filters turn
that output into the normalized structures that callers of this collection
consume, so callers never parse provider output themselves:

``cumulus_vlans``
    Normalize ``bridge -j vlan show``, the NVUE reserved VLAN ranges and the
    ``switchd.conf`` reserved VLAN range into interface memberships plus the
    VLAN IDs that are in use and reserved on the device.

``cumulus_port``
    Normalize ``bridge -j vlan show dev <port>`` into the state of one port.
"""

from __future__ import annotations

import json
import re
from typing import Any

MIN_VLAN_ID = 1
MAX_VLAN_ID = 4094
_VLAN_RANGE = re.compile(r"^([0-9]+)-([0-9]+)$")
_SWITCHD_RANGE = re.compile(r"^resv_vlan_range\s*=\s*([0-9]+)-([0-9]+)$")
_NVUE_VARIANTS = {"operational", "applied"}


class SwitchStateError(ValueError):
    """Provider switch output could not be normalized."""


def _vlan_id(value: Any, context: str) -> int:
    if isinstance(value, bool) or not isinstance(value, int):
        raise SwitchStateError(f"{context} must be an integer VLAN ID")
    if not MIN_VLAN_ID <= value <= MAX_VLAN_ID:
        raise SwitchStateError(f"{context} is outside the supported VLAN range")
    return value


def _vlan_range(value: Any, context: str) -> tuple[int, int]:
    if isinstance(value, bool):
        raise SwitchStateError(f"{context} range is malformed")
    if isinstance(value, int):
        first = last = _vlan_id(value, context)
        return first, last
    if not isinstance(value, str):
        raise SwitchStateError(f"{context} range is malformed")
    match = _VLAN_RANGE.fullmatch(value.strip())
    if match is None:
        raise SwitchStateError(f"{context} range is malformed")
    first = _vlan_id(int(match.group(1)), context)
    last = _vlan_id(int(match.group(2)), context)
    if first > last:
        raise SwitchStateError(f"{context} range is reversed")
    return first, last


def _switchd_range(value: Any) -> tuple[int, int]:
    if not isinstance(value, str) or not value.strip():
        raise SwitchStateError("cumulus switchd internal reserved VLAN range is missing")
    match = _SWITCHD_RANGE.fullmatch(value.strip())
    if match is None:
        raise SwitchStateError("cumulus switchd internal reserved VLAN range is malformed")
    first = _vlan_id(int(match.group(1)), "switchd internal reserved VLAN")
    last = _vlan_id(int(match.group(2)), "switchd internal reserved VLAN")
    if first > last:
        raise SwitchStateError("cumulus switchd internal reserved VLAN range is reversed")
    return first, last


def _nvue_variants(value: Any, context: str) -> dict[str, Any]:
    if isinstance(value, dict):
        if not value or not set(value).issubset(_NVUE_VARIANTS):
            raise SwitchStateError(f"cumulus {context} JSON is malformed")
        return value
    return {"value": value}


def _load_json(value: Any, context: str) -> Any:
    if isinstance(value, (bytes, bytearray)):
        value = value.decode()
    if not isinstance(value, str):
        raise SwitchStateError(f"cumulus {context} output is missing")
    try:
        return json.loads(value)
    except (TypeError, json.JSONDecodeError) as error:
        raise SwitchStateError(f"cumulus {context} returned invalid JSON") from error


def _bridge_entries(bridge_output: Any, *, allow_empty: bool = False) -> list[dict[str, Any]]:
    interfaces = _load_json(bridge_output, "VLAN discovery")
    if not isinstance(interfaces, list) or (not interfaces and not allow_empty):
        raise SwitchStateError("cumulus VLAN discovery returned no interfaces")
    seen: set[str] = set()
    entries: list[dict[str, Any]] = []
    for interface in interfaces:
        if not isinstance(interface, dict):
            raise SwitchStateError("cumulus VLAN interface entry is malformed")
        name = interface.get("ifname")
        memberships = interface.get("vlans")
        if (
            not isinstance(name, str)
            or not name
            or name in seen
            or not isinstance(memberships, list)
        ):
            raise SwitchStateError("cumulus VLAN interface entry is malformed")
        seen.add(name)
        entries.append(interface)
    return entries


def _port_memberships(name: str, memberships: list[Any]) -> dict[str, Any]:
    vlan_ids: set[int] = set()
    tagged: set[int] = set()
    untagged: set[int] = set()
    pvid: int | None = None
    for membership in memberships:
        if not isinstance(membership, dict):
            raise SwitchStateError("cumulus VLAN membership entry is malformed")
        first = _vlan_id(membership.get("vlan"), "cumulus VLAN membership")
        last = _vlan_id(membership.get("vlanEnd", first), "cumulus VLAN range end")
        if first > last:
            raise SwitchStateError("cumulus VLAN membership entry is malformed")
        flags = membership.get("flags", [])
        if not isinstance(flags, list) or any(not isinstance(flag, str) for flag in flags):
            raise SwitchStateError("cumulus VLAN membership entry is malformed")
        ports = set(range(first, last + 1))
        vlan_ids.update(ports)
        if "PVID" in flags:
            if first != last:
                raise SwitchStateError("cumulus PVID membership cannot span a VLAN range")
            if pvid is not None and pvid != first:
                raise SwitchStateError(
                    f"cumulus port {name} reports more than one PVID"
                )
            pvid = first
            untagged.update(ports)
        if "Egress Untagged" in flags:
            untagged.update(ports)
        if "PVID" not in flags and "Egress Untagged" not in flags:
            tagged.update(ports)
    return {
        "name": name,
        "vlan_ids": sorted(vlan_ids),
        "tagged_vlan_ids": sorted(tagged),
        "untagged_vlan_ids": sorted(untagged),
        "pvid": pvid,
    }


def _reserved_vlan_ids(reserved_output: Any, switchd_output: Any) -> set[int]:
    reserved = _load_json(reserved_output, "reserved VLAN")
    if not isinstance(reserved, dict):
        raise SwitchStateError("cumulus reserved VLAN JSON is malformed")
    internal = reserved.get("internal", {})
    l3_vni = reserved.get("l3-vni-vlan")
    if not isinstance(internal, dict):
        raise SwitchStateError("cumulus internal reserved VLAN JSON is malformed")
    if not isinstance(l3_vni, dict) or "begin" not in l3_vni or "end" not in l3_vni:
        raise SwitchStateError("cumulus L3-VNI reserved VLAN range is missing")

    reserved_ids: set[int] = set()
    if "range" in internal:
        ranges = [
            _vlan_range(value, "cumulus internal reserved VLAN")
            for value in _nvue_variants(internal["range"], "internal reserved VLAN").values()
        ]
    else:
        ranges = [_switchd_range(switchd_output)]
    for first, last in ranges:
        reserved_ids.update(range(first, last + 1))

    begins = _nvue_variants(l3_vni["begin"], "L3-VNI reserved VLAN begin")
    ends = _nvue_variants(l3_vni["end"], "L3-VNI reserved VLAN end")
    for variant in set(begins) | set(ends):
        begin_value = begins.get(variant, begins.get("value"))
        end_value = ends.get(variant, ends.get("value"))
        if begin_value is None or end_value is None:
            raise SwitchStateError("cumulus L3-VNI reserved VLAN range is incomplete")
        first = _vlan_id(begin_value, "cumulus L3-VNI reserved VLAN begin")
        last = _vlan_id(end_value, "cumulus L3-VNI reserved VLAN end")
        if first > last:
            raise SwitchStateError("cumulus L3-VNI reserved VLAN range is reversed")
        reserved_ids.update(range(first, last + 1))
    return reserved_ids


def cumulus_vlans(
    bridge_output: Any,
    reserved_output: Any,
    switchd_output: Any = None,
) -> dict[str, Any]:
    """Return the normalized Cumulus VLAN state of one switch.

    ``used_vlan_ids`` lists every VLAN ID present on a bridge interface, so a
    caller that allocates VLANs can treat them all as taken.  ``reserved_vlan_ids``
    lists the VLAN IDs the device reserves for internal and L3-VNI use.
    """
    interfaces = []
    used_vlan_ids: set[int] = set()
    for interface in _bridge_entries(bridge_output):
        memberships = interface.get("vlans")
        if not isinstance(memberships, list):
            raise SwitchStateError("cumulus VLAN interface entry is malformed")
        port = _port_memberships(interface["ifname"], memberships)
        used_vlan_ids.update(port["vlan_ids"])
        interfaces.append(port)
    return {
        "interfaces": interfaces,
        "used_vlan_ids": sorted(used_vlan_ids),
        "reserved_vlan_ids": sorted(_reserved_vlan_ids(reserved_output, switchd_output)),
    }


def cumulus_port(bridge_output: Any, port_name: Any) -> dict[str, Any]:
    """Return the normalized Cumulus state of one bridge port."""
    if not isinstance(port_name, str) or not port_name.strip():
        raise SwitchStateError("cumulus port name is required")
    port_name = port_name.strip()
    matching = [
        interface
        for interface in _bridge_entries(bridge_output, allow_empty=True)
        if interface["ifname"] == port_name
    ]
    if not matching:
        return {
            "name": port_name,
            "present": False,
            "vlan_ids": [],
            "tagged_vlan_ids": [],
            "untagged_vlan_ids": [],
            "pvid": None,
        }
    memberships = matching[0].get("vlans")
    if not isinstance(memberships, list):
        raise SwitchStateError("cumulus VLAN interface entry is malformed")
    port = _port_memberships(port_name, memberships)
    port["present"] = True
    return port


class FilterModule:
    """Expose the provider normalizers to this collection's roles."""

    def filters(self) -> dict[str, Any]:
        return {
            "cumulus_vlans": cumulus_vlans,
            "cumulus_port": cumulus_port,
        }
