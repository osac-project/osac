"""Idempotency helpers for Netris controller create/delete/move tasks (OSAC-4923)."""

from __future__ import annotations

from typing import Any


def _dig(obj: Any, path: str) -> Any:
    """Return nested value for dotted path; supports dict key 'id' under vpc."""
    cur = obj
    for part in path.split("."):
        if cur is None:
            return None
        if isinstance(cur, dict):
            cur = cur.get(part)
        else:
            return None
    return cur


def _norm(value: Any) -> str:
    if value is None:
        return ""
    return str(value).strip()


def netris_immutable_mismatch(
    existing: Any,
    expected_fields: dict[str, Any] | None,
) -> list[dict[str, str]]:
    """Compare expected immutable fields against an existing Netris object.

    ``expected_fields`` maps dotted paths on ``existing`` to desired values.
    Returns a list of mismatch dicts ``{field, expected, actual}``. Empty list
    means no mismatch (safe no-op).
    """
    if not existing or not isinstance(existing, dict):
        return []
    if not expected_fields:
        return []

    mismatches: list[dict[str, str]] = []
    for field, expected in expected_fields.items():
        actual = _dig(existing, field)
        # Netris may nest vpc as {id: N} or expose vpcId
        if actual is None and field == "vpc.id":
            actual = existing.get("vpcId", existing.get("vpc_id"))
        exp_s = _norm(expected)
        act_s = _norm(actual)
        if isinstance(actual, dict) and "id" in actual:
            act_s = _norm(actual.get("id"))
        # Netris may return tenant/vpc ids as ints; normalize for compare
        if field == "action":
            exp_s = exp_s.upper()
            act_s = act_s.upper()
        if exp_s != act_s:
            mismatches.append(
                {
                    "field": field,
                    "expected": exp_s,
                    "actual": act_s if act_s else "<missing>",
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


def format_immutable_mismatch_message(
    object_type: str,
    object_name: str,
    mismatches: list[dict[str, str]],
) -> str:
    """Build an actionable fail message for immutable field drift."""
    lines = [
        f"Netris {object_type} '{object_name}' already exists with immutable field mismatch:"
    ]
    for m in mismatches:
        lines.append(
            f"  field={m['field']} expected={m['expected']} actual={m['actual']}."
        )
    lines.append(
        "Refusing delete-and-recreate. Correct Netris state or recreate the OSAC "
        "resource with a new name after cleanup."
    )
    return "\n".join(lines)


class FilterModule:
    def filters(self):
        return {
            "netris_immutable_mismatch": netris_immutable_mismatch,
            "osac_is_default_networking_resource": osac_is_default_networking_resource,
            "netris_port_on_vnet": netris_port_on_vnet,
            "format_immutable_mismatch_message": format_immutable_mismatch_message,
        }
