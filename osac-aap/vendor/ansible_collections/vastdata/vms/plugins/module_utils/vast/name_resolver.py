# -*- coding: utf-8 -*-
"""Framework-agnostic name -> id resolution for VAST resources.

Shared by the ``vast_name_to_id`` and ``vast_target_cluster_to_remote_target_id``
lookup plugins. Kept free of any Ansible plugin imports so the logic is unit
testable on its own and raises the collection's own ``VastError`` (the lookup
wrappers translate it to ``AnsibleError``).
"""

from __future__ import absolute_import, division, print_function

__metaclass__ = type

import ipaddress

from ansible_collections.vastdata.vms.plugins.module_utils.vast.auth import connection_from_vms
from ansible_collections.vastdata.vms.plugins.module_utils.vast.client import VastClient
from ansible_collections.vastdata.vms.plugins.module_utils.vast.errors import VastAPIError, VastError


def _get_nested(mapping, field):
    value = mapping
    for part in field.split("."):
        if not isinstance(value, dict):
            return None
        value = value.get(part)
    return value


def build_connection(vms):
    """Build a VastConnection for the lookup plugins.

    Delegates to the shared :func:`auth.connection_from_vms` with alias support
    enabled (accepts ``vms_host``/``vms_token``/... keys). Raises ``VastError``
    (``VastAuthError`` is a subclass) on invalid input.
    """
    return connection_from_vms(vms, allow_aliases=True)


def name_to_id(name, vms, endpoint, name_field="name", id_field="id", query=None, strict=True):
    if name is None or str(name).strip() == "":
        raise VastError("name must be a non-empty value")
    if not endpoint or str(endpoint).strip() == "":
        raise VastError("endpoint must be a non-empty value")

    params = dict(query or {})
    params[name_field] = name

    try:
        client = VastClient(build_connection(vms))
        results = client.api[str(endpoint).strip()].get(**params)
    except VastAPIError as exc:
        raise VastError(f"failed to query {endpoint}: {exc}") from exc

    matches = [item for item in results if str(_get_nested(item, name_field)) == str(name)]
    if len(matches) != 1:
        if not strict:
            return None
        raise VastError(f"expected exactly one {endpoint} with {name_field}={name!r}, found {len(matches)}")

    value = _get_nested(matches[0], id_field)
    if value is None:
        value = _get_nested(matches[0], id_field[:1].upper() + id_field[1:])
    if value is None:
        raise VastError(f"{endpoint} with {name_field}={name!r} has no {id_field} field")
    return value


def _ip_in_ranges(ip_address, ip_ranges):
    try:
        ip_value = ipaddress.ip_address(str(ip_address))
    except ValueError:
        return False

    for ip_range in ip_ranges:
        if not isinstance(ip_range, (list, tuple)) or len(ip_range) != 2:
            continue
        try:
            start = ipaddress.ip_address(str(ip_range[0]))
            end = ipaddress.ip_address(str(ip_range[1]))
        except ValueError:
            continue
        if start <= ip_value <= end:
            return True
    return False


def _target_cluster_ip_ranges(target_cluster, vippools):
    ranges = []
    for pool in vippools or []:
        if pool.get("cluster") != target_cluster:
            continue
        if pool.get("role") not in (None, "REPLICATION"):
            continue
        ranges.extend(pool.get("ip_ranges") or [])
    return ranges


def _matching_replication_peers(source_cluster, target_cluster, replicationpeers, vippools):
    target_ranges = _target_cluster_ip_ranges(target_cluster, vippools)
    matches = []
    for peer in replicationpeers or []:
        if peer.get("cluster") != source_cluster:
            continue
        if peer.get("target_cluster") == target_cluster:
            matches.append(peer)
            continue
        if _ip_in_ranges(peer.get("leading_vip"), target_ranges):
            matches.append(peer)
    return matches


def target_cluster_to_remote_target_id(source_cluster, target_cluster, vms, replicationpeers, vippools):
    if not source_cluster or not target_cluster:
        raise VastError("source_cluster and target_cluster are required")

    matches = _matching_replication_peers(source_cluster, target_cluster, replicationpeers, vippools)
    if len(matches) != 1:
        names = ", ".join([str(peer.get("name")) for peer in matches if peer.get("name")])
        raise VastError(
            f"expected exactly one replication peer from {source_cluster} to target_cluster {target_cluster}, "
            f"found {len(matches)}{f': {names}' if names else ''}"
        )

    peer_name = matches[0].get("name")
    if not peer_name:
        raise VastError("matched replication peer has no name")
    return name_to_id(peer_name, vms, "nativereplicationremotetargets")
