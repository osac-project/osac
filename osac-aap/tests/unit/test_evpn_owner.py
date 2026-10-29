import importlib.util
from copy import deepcopy
from pathlib import Path

import pytest
from ansible.errors import AnsibleFilterError


PLUGIN = Path(__file__).resolve().parents[2] / "collections/ansible_collections/osac/templates/plugins/filter/evpn_owner.py"
SPEC = importlib.util.spec_from_file_location("evpn_owner", PLUGIN)
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


def subnet(name, **annotations):
    return {"metadata": {"name": name, "annotations": annotations},
            "spec": {"virtualNetwork": "vn", "ipv4Cidr": "192.0.2.0/24"}}


def lease(owner="", cleaned=False):
    return {"metadata": {"annotations": {MODULE.OWNER: owner,
            MODULE.CLEANED: "true" if cleaned else "false"}}}


def test_three_subnets_have_exactly_one_owner_and_no_automatic_promotion():
    siblings = [subnet(name) for name in ("first", "second", "third")]
    persisted = lease()
    for candidate in siblings:
        decision = MODULE.evpn_owner_decision(candidate, siblings, persisted, {})
        persisted = lease(decision["owner"])
        candidate["metadata"]["annotations"][MODULE.SUPPORTED] = str(decision["supported"]).lower()
    assert persisted["metadata"]["annotations"][MODULE.OWNER] == "first"
    assert [item["metadata"]["annotations"][MODULE.SUPPORTED] for item in siblings] == ["true", "false", "false"]
    for candidate in siblings[1:]:
        decision = MODULE.evpn_owner_decision(candidate, siblings[1:], lease("first", cleaned=True), {})
        assert decision["supported"] is False
        assert not decision["owner"]
    new = subnet("new")
    assert MODULE.evpn_owner_decision(new, siblings[1:] + [new], lease("first", cleaned=True), {})["supported"] is True


def test_new_candidate_waits_for_deleting_owner_then_claims_after_cleanup_and_cr_removal():
    owner = subnet("owner")
    owner["metadata"]["deletionTimestamp"] = "2026-01-01T00:00:00Z"
    candidate = subnet("new")
    for completed in (False, True):
        decision = MODULE.evpn_owner_decision(candidate, [owner, candidate], lease("owner", completed), {})
        assert decision["retry"] is True
        assert decision["supported"] is None
        assert MODULE.SUPPORTED not in candidate["metadata"]["annotations"]
    assert MODULE.evpn_owner_decision(candidate, [candidate], lease("owner"), {})["retry"] is True
    decision = MODULE.evpn_owner_decision(candidate, [candidate], lease("owner", True), {})
    assert decision["supported"] is True


def test_owner_remains_without_any_vm_inventory():
    owner = subnet("owner")
    original = deepcopy(owner)
    decision = MODULE.evpn_owner_decision(owner, [owner], lease("owner"), {})
    assert decision["supported"] is True
    assert decision["owner"] == "owner"
    assert owner == original


def test_explicit_skip_never_claims_and_never_deletes_existing_owner():
    candidate = subnet("skip", **{MODULE.PREFIX + "skip-k8s-manager": "true"})
    decision = MODULE.evpn_owner_decision(candidate, [candidate], lease(), {})
    assert decision["supported"] is False
    assert not decision["owner"]
    decision = MODULE.evpn_owner_decision(candidate, [candidate], lease("skip"), {})
    assert decision["supported"] is False
    assert decision["owner"] == "skip"


def test_historical_owner_is_preserved_and_conflicting_owners_fail_closed():
    owner, candidate = subnet("historical"), subnet("new")
    existing = {"metadata": {"annotations": {MODULE.OWNER: "historical"}}}
    decision = MODULE.evpn_owner_decision(candidate, [owner, candidate], lease(), existing)
    assert decision["owner"] == "historical"
    assert decision["supported"] is False
    with pytest.raises(AnsibleFilterError, match="conflict"):
        MODULE.evpn_owner_decision(candidate, [owner, candidate], lease("new"), existing)


def test_historical_unannotated_cudn_requires_unambiguous_matching_subnet():
    owner = subnet("old", **{MODULE.PREFIX + "k8s-implementation-strategy": "cudn_evpn"})
    existing = {"spec": {"network": {"layer2": {"subnets": ["192.0.2.0/24"]}}}}
    assert MODULE.evpn_owner_decision(owner, [owner], lease(), existing)["supported"] is True
    with pytest.raises(AnsibleFilterError, match="unambiguous"):
        MODULE.evpn_owner_decision(owner, [owner, subnet("other", **owner["metadata"]["annotations"])], lease(), existing)
