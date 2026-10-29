"""Idempotency helpers for Netris controller create/delete/move tasks (OSAC-4923)."""

from __future__ import annotations

import ipaddress
import re
from typing import Any
from urllib.parse import urlsplit


def _dig(obj: Any, path: str) -> Any:
    """Return nested value for dotted path; supports dict key 'id' under vpc."""
    cur = obj
    for part in path.split("."):
        if cur is None:
            return None
        if isinstance(cur, dict):
            cur = cur.get(part)
        elif isinstance(cur, list):
            try:
                cur = cur[int(part)]
            except (ValueError, IndexError):
                return None
        else:
            return None
    return cur


def _norm(value: Any) -> str:
    """Normalize a Netris field value for type-insensitive comparison."""
    if value is None:
        return ""
    if isinstance(value, (list, tuple)):
        return ",".join(sorted(_norm(item) for item in value))
    return str(value).strip()


def _norm_ipv4_host_address(value: Any) -> str | None:
    """Normalize a bare IPv4 address or /32 host prefix to its address."""
    text = _norm(value)
    if not text:
        return None
    candidate = text if "/" in text else f"{text}/32"
    try:
        interface = ipaddress.ip_interface(candidate)
    except ValueError:
        return None
    if interface.version != 4 or interface.network.prefixlen != 32:
        return None
    return str(interface.ip)


def _actual_value(existing: dict[str, Any], field: str) -> Any:
    """Read a comparable field, accepting Netris' known ID representations."""
    if field == "sites.ids":
        sites = existing.get("sites")
        if isinstance(sites, list):
            return [
                site.get("id") if isinstance(site, dict) else site
                for site in sites
            ]
        return None
    actual = _dig(existing, field)
    if actual is None and field == "tenant.id":
        actual = existing.get("tenantID", existing.get("tenant_id"))
        if actual is None and existing.get("tenant") is not None:
            tenant = existing["tenant"]
            actual = tenant if not isinstance(tenant, dict) else None
    if actual is None and field == "adminTenant.id":
        tenant = existing.get("adminTenant")
        actual = tenant if tenant is not None and not isinstance(tenant, dict) else None
    if actual is None and field == "site.id":
        site = existing.get("site")
        actual = site if site is not None and not isinstance(site, dict) else None
    if actual is None and field == "vpc.id":
        vpc_value = existing.get("vpc")
        if vpc_value is not None and not isinstance(vpc_value, dict):
            return vpc_value
        return existing.get("vpcId", existing.get("vpc_id"))
    if isinstance(actual, dict) and "id" in actual:
        return actual.get("id")
    if isinstance(actual, dict) and field in {"action", "protocol", "state"}:
        return actual.get("value", actual.get("label"))
    return actual


def netris_immutable_missing_fields(
    existing: Any,
    expected_fields: dict[str, Any] | None,
) -> list[str]:
    """Return expected field paths absent from a Netris response."""
    if not isinstance(existing, dict) or not expected_fields:
        return list(expected_fields or {}) if isinstance(existing, dict) else []
    return [
        field
        for field in expected_fields
        if _actual_value(existing, field) is None
    ]


def netris_immutable_mismatch(
    existing: Any,
    expected_fields: dict[str, Any] | None,
) -> list[dict[str, str]]:
    """Compare expected immutable fields against an existing Netris object.

    ``expected_fields`` maps dotted paths on ``existing`` to desired values.
    Returns differing or missing field dicts. A missing value has
    ``unverifiable=True`` and is not considered a safe no-op.
    """
    if not isinstance(existing, dict):
        return []
    if not expected_fields:
        return []

    mismatches: list[dict[str, str]] = []
    for field, expected in expected_fields.items():
        actual = _actual_value(existing, field)
        exp_s = _norm(expected)
        act_s = _norm(actual)
        # Netris may return tenant/vpc ids as ints; normalize for compare
        if field == "action":
            exp_s = exp_s.upper()
            act_s = act_s.upper()
        same = exp_s == act_s
        if field == "snatToIP":
            expected_host = _norm_ipv4_host_address(expected)
            actual_host = _norm_ipv4_host_address(actual)
            same = (
                expected_host is not None
                and actual_host is not None
                and expected_host == actual_host
            )
        elif field == "destinationAddress":
            expected_host = _norm_ipv4_host_address(expected)
            actual_host = _norm_ipv4_host_address(actual)
            if expected_host is not None and actual_host is not None:
                same = expected_host == actual_host
        elif field == "dnatToPort":
            # Attachment replay sees Netris return 0 for an unset DNAT port.
            same = same or {exp_s, act_s} == {"", "0"}
        if actual is None or not same:
            mismatches.append(
                {
                    "field": field,
                    "expected": exp_s,
                    "actual": act_s if actual is not None else "<missing>",
                    "unverifiable": actual is None,
                }
            )
    return mismatches


def osac_is_default_networking_resource(resource: Any) -> bool:
    """True when the OSAC CR has label osac.openshift.io/default=true."""
    if not resource or not isinstance(resource, dict):
        return False
    metadata = resource.get("metadata") or {}
    labels = metadata.get("labels") or {}
    return _norm(labels.get("osac.openshift.io/default")).lower() == "true"


def netris_port_on_vnet(vnet_detail: Any, port_id: Any) -> bool:
    """True when ``port_id`` appears in ``vnet_detail.ports``."""
    if not vnet_detail or not isinstance(vnet_detail, dict):
        return False
    if port_id is None:
        return False
    want = _norm(port_id)
    for port in vnet_detail.get("ports") or []:
        if isinstance(port, dict) and _norm(port.get("id")) == want:
            return True
    return False


def netris_ports_excluding(ports: Any, port_id: Any) -> list[Any]:
    """Return ``ports`` without the entry whose id matches ``port_id``.

    Matching is type-insensitive (int ``20`` equals string ``\"20\"``), matching
    ``netris_port_on_vnet``. Use when building a V-Net update body for detach.
    """
    if not isinstance(ports, list):
        return []
    if port_id is None:
        return list(ports)
    want = _norm(port_id)
    return [
        port
        for port in ports
        if not (isinstance(port, dict) and _norm(port.get("id")) == want)
    ]


def format_immutable_mismatch_message(
    object_type: str,
    object_name: str,
    mismatches: list[dict[str, str]],
) -> str:
    """Build an actionable fail message for drift or unverifiable fields."""
    unverifiable = [m for m in mismatches if m.get("unverifiable")]
    drift = [m for m in mismatches if not m.get("unverifiable")]
    if unverifiable and drift:
        heading = "has immutable drift and fields that could not be verified"
    elif unverifiable:
        heading = "has immutable fields that could not be verified from the response"
    else:
        heading = "already exists with immutable field mismatch"
    lines = [f"Netris {object_type} '{object_name}' {heading}:"]
    for m in mismatches:
        state = " (unverifiable)" if m.get("unverifiable") else ""
        lines.append(f"  field={m['field']} expected={m['expected']} actual={m['actual']}{state}.")
    if unverifiable:
        lines.append(
            "Read the Netris resource details or verify the provider response contract; "
            "the playbook did not treat the resource as a match."
        )
    if drift:
        lines.append(
            "Refusing delete-and-recreate. Correct Netris state or recreate the OSAC "
            "resource with a new name after cleanup."
        )
    return "\n".join(lines)


def _http_status(result: dict[str, Any]) -> int | None:
    """Return a real HTTP status, ignoring the URI no-response sentinel (-1).

    ``ansible.builtin.uri`` reports a connection that closed before any
    response as ``status: -1``. That is a transport failure, not an HTTP
    status, so retry classification must inspect the error text instead.
    """
    status = result.get("status")
    if isinstance(status, int) and not isinstance(status, bool) and status >= 100:
        return status
    return None


def netris_safe_error_detail(result: Any) -> str:
    """Return a bounded provider/task error summary without request secrets."""
    if not isinstance(result, dict):
        return "provider response details are unavailable"

    status = _http_status(result)
    body = result.get("json")
    detail = body.get("message") if isinstance(body, dict) else None
    if not isinstance(detail, str) or not detail.strip():
        detail = result.get("msg")
    if not isinstance(detail, str) or not detail.strip():
        detail = "provider returned no diagnostic message"

    detail = re.sub(r"[\r\n\t]+", " ", detail).strip()
    detail = re.sub(
        r"""(?ix)\b(connect\.sid|cookie|authorization|token|password|secret)\b
        ["']?\s*[:=]\s*
        (?:"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|(?:Bearer\s+)?[^\s,;}\]]+)""",
        r"\1=<redacted>",
        detail,
    )
    detail = re.sub(r"(?i)\bBearer\s+[^\s,;]+", "Bearer <redacted>", detail)
    detail = detail[:240]

    if status is None:
        return f"no HTTP status was received; {detail}"
    return f"HTTP {status}; {detail}"


RETRYABLE_HTTP_STATUSES = frozenset({408, 425, 429})

TRANSIENT_ERROR_MARKERS = (
    "connection failure",
    "connection refused",
    "connection reset",
    "temporary failure",
    "timed out",
    "timeout",
    "bad gateway",
    "gateway timeout",
    "service unavailable",
    "eof occurred",
    "remote end closed connection",
    "broken pipe",
)

# Contract 4.3/4.6: a missing or malformed setting is retryable, because an
# administrator can repair it and job pods read settings at start. Role tasks
# prefix those failures with this marker so classification does not have to
# guess from unrelated assertion text.
MISSING_SETTING_MARKER = "netris setting error"

# Contract 4.10: a MAC match alone may be a historical or reservation-only
# record, so only a DHCP-sourced active binding is a lease.
ACTIVE_DHCP_SOURCE = "dhcp"
ACTIVE_DHCP_STATE = "active"


def netris_attachment_port_plan(
    vnet: dict,
    server_ports: list,
    target_id: str,
    selected_id: Any,
    deleting=False,
    legacy_uid: str | None = None,
) -> dict:
    """Remember only this host's provisioning ports across jobs and retries.

    Ports outside provisioning are not candidates, notably FabricDomain ports.
    A resource-scoped provider tag is saved before disconnecting any port, so
    delete can restore the exact set even in a fresh AAP job after interruption.
    """
    if not isinstance(target_id, str) or not re.fullmatch(r"[A-Za-z0-9-]+", target_id):
        raise ValueError("NetworkAttachment requires a stable target API ID")
    host_ids = {_norm(port.get("id")) for port in server_ports}
    if any(not re.fullmatch(r"[1-9][0-9]*", value) for value in host_ids):
        raise ValueError("Host inventory contains an invalid port ID")
    prefix = f"osac-na-target:{target_id}:"
    tags = vnet.get("tags") or []
    if not isinstance(tags, list) or any(not isinstance(tag, str) for tag in tags):
        raise ValueError("Provider returned malformed provisioning V-Net tags")
    saved = [tag for tag in tags if tag.startswith(prefix)]
    # OSAC-6754: only the supplied UID can prove ownership of a legacy tag.
    # Never guess from port overlap after a hub CR has been replaced: refusing
    # the operation preserves the only record of already-disconnected ports.
    legacy_prefix = f"osac-na:{legacy_uid}:" if legacy_uid else None
    for tag in tags:
        if not tag.startswith("osac-na:"):
            continue
        if legacy_prefix and tag.startswith(legacy_prefix):
            saved.append(tag)
        elif host_ids.intersection(tag.rsplit(":", 1)[-1].split(",")):
            raise ValueError(
                "Unverified legacy NetworkAttachment snapshot overlaps this host; "
                "verify its owner and migrate it to the target API ID before retrying"
            )
    if len(saved) > 1:
        raise ValueError("Ambiguous saved NetworkAttachment port identity")
    ids = set(saved[0].rsplit(":", 1)[-1].split(",")) if saved else set()
    if not ids <= host_ids:
        raise ValueError("Saved NetworkAttachment ports do not belong to this host")
    selected = _norm(selected_id)
    if selected not in host_ids:
        raise ValueError("Selected port does not belong to this host")
    if saved and selected not in ids:
        raise ValueError("Selected port differs from the saved fabric port identity")
    ids.update(port_id for port_id in host_ids if netris_port_on_vnet(vnet, port_id))
    if not deleting and selected not in ids:
        raise ValueError("Selected port is not a provisioning fabric port")
    # Also supports deleting an attachment applied by the old single-port path.
    ids.add(selected)
    port_ids = sorted(int(port_id) for port_id in ids)
    tag = prefix + ",".join(str(port_id) for port_id in port_ids)
    return {
        "port_ids": port_ids,
        "tag": tag,
        "tags": [value for value in tags if value not in saved] + [tag],
        "had_snapshot": bool(saved),
        "saved_tag": saved[0] if saved else "",
    }


def netris_controller_url_valid(value: Any) -> bool:
    """Validate the installation's HTTP(S) base URL before attempting auth."""
    if not isinstance(value, str) or re.search(r"[\s\\]", value):
        return False
    try:
        url = urlsplit(value)
        return bool(
            url.scheme in ("http", "https")
            and url.hostname
            and not url.username
            and not url.password
            and "@" not in url.netloc
            and not url.netloc.endswith(":")
            and (url.port is None or 0 < url.port <= 65535)
            and not url.query
            and not url.fragment
        )
    except ValueError:
        return False


def netris_active_dhcp_addresses(host: Any, mac_address: Any) -> list[str]:
    """Return the addresses active DHCP leases hold for ``mac_address``.

    The IP is the enclosing IPAM host's address, not ``mac[].address``.
    MAC history only establishes whether that host has an active DHCP binding.
    """
    if not isinstance(host, dict):
        return []
    if not isinstance(mac_address, str) or not mac_address.strip():
        return []
    if not isinstance(host.get("address"), str):
        return []
    want = mac_address.strip().lower()
    try:
        address = str(ipaddress.IPv4Address(host.get("address")))
    except (ValueError, TypeError):
        return []
    for entry in host.get("mac") or []:
        if not isinstance(entry, dict):
            continue
        mac = entry.get("address")
        if not isinstance(mac, str) or mac.strip().lower() != want:
            continue
        source = str(entry.get("source", "")).strip().lower()
        state = str(entry.get("state", "")).strip().lower()
        if source != ACTIVE_DHCP_SOURCE or state != ACTIVE_DHCP_STATE:
            continue
        return [address]
    return []


def netris_retryable_error(result: Any) -> bool:
    """Return whether a failed task is transient enough for OSAC to retry.

    An explicit ``retryable`` field wins. Otherwise a real HTTP status decides:
    5xx and 408/425/429 are transient, every other status is final. A missing
    HTTP status (including the URI ``-1`` no-response sentinel) falls back to
    transport markers and then to the missing-setting marker; an assert or an
    explicit ``fail`` is a permanent input or contract problem.
    """
    if not isinstance(result, dict):
        return False
    explicit = result.get("retryable")
    if isinstance(explicit, bool):
        return explicit
    status = _http_status(result)
    if status is not None:
        return status >= 500 or status in RETRYABLE_HTTP_STATUSES
    message = result.get("msg")
    if not isinstance(message, str):
        return False
    lowered = message.lower()
    if MISSING_SETTING_MARKER in lowered:
        return True
    return any(marker in lowered for marker in TRANSIENT_ERROR_MARKERS)


def netris_ip_in_cidrs(address: Any, cidrs: Any) -> bool:
    """Return whether an address or host prefix belongs to any allocation CIDR."""
    if not isinstance(address, str) or not isinstance(cidrs, (list, tuple)):
        return False
    try:
        try:
            ip = ipaddress.ip_interface(address).ip
        except ValueError:
            ip = ipaddress.ip_address(address)
    except ValueError:
        return False
    for cidr in cidrs:
        if not isinstance(cidr, str):
            continue
        try:
            network = ipaddress.ip_network(cidr, strict=False)
        except ValueError:
            continue
        if ip.version == network.version and ip in network:
            return True
    return False


class FilterModule:
    """Ansible filter plugin registration for Netris idempotency helpers."""

    def filters(self):
        """Return the filter name → callable map for Ansible."""
        return {
            "netris_immutable_mismatch": netris_immutable_mismatch,
            "netris_immutable_missing_fields": netris_immutable_missing_fields,
            "osac_is_default_networking_resource": osac_is_default_networking_resource,
            "netris_port_on_vnet": netris_port_on_vnet,
            "netris_ports_excluding": netris_ports_excluding,
            "format_immutable_mismatch_message": format_immutable_mismatch_message,
            "netris_safe_error_detail": netris_safe_error_detail,
            "netris_retryable_error": netris_retryable_error,
            "netris_active_dhcp_addresses": netris_active_dhcp_addresses,
            "netris_attachment_port_plan": netris_attachment_port_plan,
            "netris_controller_url_valid": netris_controller_url_valid,
            "netris_ip_in_cidrs": netris_ip_in_cidrs,
        }
