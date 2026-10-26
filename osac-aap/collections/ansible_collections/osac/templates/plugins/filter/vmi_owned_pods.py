"""Filter pods owned by a specific VMI UID.

Selection logic:
  1. If a pod has a controlling ownerReference (controller=true)
     whose UID matches the target VMI UID, include it.
  2. If a pod has a controlling ownerReference whose UID does
     NOT match, exclude it — even if a created-by label claims
     the target UID.
  3. Only when NO controlling ownerReference exists, fall back
     to the legacy kubevirt.io/created-by label.  Include the
     pod if that label equals the target UID.
  4. Pods with neither a controlling owner nor the legacy label
     are excluded.
"""

from __future__ import annotations


def _controlling_owner_uid(pod: dict) -> str | None:
    """Return the UID of the controlling ownerReference, or None.

    Returns None only when no controller=true ref exists at all.
    A controller=true ref with a missing, null, or blank UID
    returns '' — the controller EXISTS but its identity is
    unknown, so the pod must NOT fall through to legacy label
    matching.
    """
    owner_refs = pod.get("metadata", {}).get("ownerReferences")
    if owner_refs is None:
        return None
    for ref in owner_refs:
        if ref.get("controller") is True:
            return ref.get("uid") or ""
    return None


def vmi_owned_pods(pods: list, vmi_uid: str) -> list:
    """Return pods directly controlled by *vmi_uid*.

    See module docstring for the full selection logic.
    """
    result = []
    for pod in pods:
        ctrl_uid = _controlling_owner_uid(pod)
        if ctrl_uid is not None:
            # Controlling owner exists — match only by UID.
            if ctrl_uid == vmi_uid:
                result.append(pod)
            # else: different controller — skip even if
            # created-by label matches.
        else:
            # No controlling owner — legacy fallback.
            created_by = (
                pod.get("metadata", {})
                .get("labels", {})
                .get("kubevirt.io/created-by", "")
            )
            if created_by == vmi_uid:
                result.append(pod)
    return result


class FilterModule:
    """Ansible filter plugin for VMI pod ownership."""

    def filters(self):
        return {
            "vmi_owned_pods": vmi_owned_pods,
        }
