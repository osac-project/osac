from __future__ import annotations

from tests.e2e.vmaas.regression.test_tenant_isolation import _copycat_vm_entries


def test_copycat_vm_entries_merges_sources_and_dedupes_by_id() -> None:
    """Cleanup considers copycat_vms and in-flight copycat_vm once per id."""
    vm_a = {"id": "a", "cr": "ca"}
    vm_b = {"id": "b"}
    in_flight = {"id": "c"}
    state = {"copycat_vms": [vm_a, "skip", {}, vm_b], "copycat_vm": vm_a}
    assert _copycat_vm_entries(state) == [vm_a, vm_b]
    state["copycat_vm"] = in_flight
    assert _copycat_vm_entries(state) == [vm_a, vm_b, in_flight]


def test_copycat_vm_entries_skips_missing_and_invalid() -> None:
    """Missing lists, non-dicts, and entries without id are ignored."""
    assert _copycat_vm_entries({}) == []
    assert _copycat_vm_entries({"copycat_vms": None, "copycat_vm": {"cr": "only"}}) == []
