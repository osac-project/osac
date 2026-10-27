"""Unit tests for the vmi_owned_pods filter plugin."""

import sys
from pathlib import Path

# Add the templates filter plugin directory to the import path,
# matching the pattern in pyproject.toml for service plugins.
_filter_dir = (
    Path(__file__).resolve().parents[2]
    / "collections"
    / "ansible_collections"
    / "osac"
    / "templates"
    / "plugins"
    / "filter"
)
sys.path.insert(0, str(_filter_dir))

from vmi_owned_pods import vmi_owned_pods  # noqa: E402

TARGET_UID = "target-vmi-uid-1234"
FOREIGN_UID = "foreign-vmi-uid-5678"


def _pod(name, controller_uid=None, created_by=None,
         extra_owners=None):
    """Build a minimal pod dict with optional ownership."""
    pod = {"metadata": {"name": name, "labels": {}}}
    owners = []
    if extra_owners:
        owners.extend(extra_owners)
    if controller_uid is not None:
        owners.append(
            {
                "apiVersion": "kubevirt.io/v1",
                "kind": "VirtualMachineInstance",
                "name": "test-vmi",
                "uid": controller_uid,
                "controller": True,
                "blockOwnerDeletion": True,
            }
        )
    if owners:
        pod["metadata"]["ownerReferences"] = owners
    if created_by is not None:
        pod["metadata"]["labels"][
            "kubevirt.io/created-by"
        ] = created_by
    return pod


class TestControllingOwner:
    """Pods with controller=true ownerReference."""

    def test_matching_controller_uid_included(self):
        pod = _pod("launcher", controller_uid=TARGET_UID)
        assert vmi_owned_pods([pod], TARGET_UID) == [pod]

    def test_foreign_controller_uid_excluded(self):
        pod = _pod("launcher", controller_uid=FOREIGN_UID)
        assert vmi_owned_pods([pod], TARGET_UID) == []

    def test_controller_overrides_created_by_label(self):
        """Foreign controller excludes even with target
        created-by."""
        pod = _pod(
            "launcher",
            controller_uid=FOREIGN_UID,
            created_by=TARGET_UID,
        )
        assert vmi_owned_pods([pod], TARGET_UID) == []

    def test_matching_controller_without_label(self):
        """Unlabelled pod included if controller matches."""
        pod = _pod("sidecar", controller_uid=TARGET_UID)
        assert vmi_owned_pods([pod], TARGET_UID) == [pod]


class TestNonControllingOwners:
    """Pods with ownerReferences but none controller=true."""

    def test_non_controlling_uses_label_fallback(self):
        non_ctrl = {
            "apiVersion": "kubevirt.io/v1",
            "kind": "VirtualMachineInstance",
            "name": "test-vmi",
            "uid": TARGET_UID,
            "controller": False,
        }
        pod = _pod(
            "launcher",
            extra_owners=[non_ctrl],
            created_by=TARGET_UID,
        )
        assert vmi_owned_pods([pod], TARGET_UID) == [pod]

    def test_non_controlling_no_label_excluded(self):
        non_ctrl = {
            "apiVersion": "kubevirt.io/v1",
            "kind": "VirtualMachineInstance",
            "name": "test-vmi",
            "uid": TARGET_UID,
            "controller": False,
        }
        pod = _pod("launcher", extra_owners=[non_ctrl])
        assert vmi_owned_pods([pod], TARGET_UID) == []

    def test_ordering_first_controller_wins(self):
        """Only controller=true ref matters, not position."""
        non_ctrl = {
            "apiVersion": "v1",
            "kind": "Node",
            "name": "node",
            "uid": "node-uid",
        }
        pod = _pod(
            "launcher",
            controller_uid=TARGET_UID,
            extra_owners=[non_ctrl],
        )
        assert vmi_owned_pods([pod], TARGET_UID) == [pod]


class TestLegacyFallback:
    """Pods with no ownerReferences — legacy label."""

    def test_label_match_included(self):
        pod = _pod("launcher", created_by=TARGET_UID)
        assert vmi_owned_pods([pod], TARGET_UID) == [pod]

    def test_label_mismatch_excluded(self):
        pod = _pod("launcher", created_by=FOREIGN_UID)
        assert vmi_owned_pods([pod], TARGET_UID) == []

    def test_no_label_no_owner_excluded(self):
        pod = _pod("unrelated")
        assert vmi_owned_pods([pod], TARGET_UID) == []

    def test_empty_owner_refs_uses_label(self):
        pod = _pod("launcher", created_by=TARGET_UID)
        pod["metadata"]["ownerReferences"] = []
        assert vmi_owned_pods([pod], TARGET_UID) == [pod]


class TestNullOwnerEdges:
    """Null/None controller UID and ownerReferences."""

    def test_controller_uid_none_excludes(self):
        """controller=true with uid=None: controller EXISTS
        but UID unknown — must NOT fall to legacy."""
        pod = {
            "metadata": {
                "name": "null-uid",
                "ownerReferences": [
                    {"controller": True, "uid": None}
                ],
                "labels": {
                    "kubevirt.io/created-by": TARGET_UID,
                },
            }
        }
        assert vmi_owned_pods([pod], TARGET_UID) == []

    def test_owner_references_key_none_uses_legacy(self):
        """ownerReferences=None (key present, value None):
        no controlling owner — legacy label fallback."""
        pod = {
            "metadata": {
                "name": "none-refs",
                "ownerReferences": None,
                "labels": {
                    "kubevirt.io/created-by": TARGET_UID,
                },
            }
        }
        assert vmi_owned_pods([pod], TARGET_UID) == [pod]

    def test_controller_uid_missing_key_excludes(self):
        """controller=true with no uid key at all:
        controller EXISTS — must NOT fall to legacy."""
        pod = {
            "metadata": {
                "name": "no-uid-key",
                "ownerReferences": [{"controller": True}],
                "labels": {
                    "kubevirt.io/created-by": TARGET_UID,
                },
            }
        }
        assert vmi_owned_pods([pod], TARGET_UID) == []


class TestEdgeCases:
    def test_empty_pod_list(self):
        assert vmi_owned_pods([], TARGET_UID) == []

    def test_mixed_pods(self):
        owned = _pod("owned", controller_uid=TARGET_UID)
        foreign = _pod("foreign", controller_uid=FOREIGN_UID)
        legacy = _pod("legacy", created_by=TARGET_UID)
        unrelated = _pod("unrelated")
        result = vmi_owned_pods(
            [owned, foreign, legacy, unrelated], TARGET_UID
        )
        assert result == [owned, legacy]
