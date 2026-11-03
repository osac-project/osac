#!/usr/bin/env python
# -*- coding: utf-8 -*-
"""Unit tests for compute_patch, with focus on the renamed_on_response override.

These cover the asymmetric input/output naming convention used by some VAST
endpoints (e.g. roles), where the user-facing parameter name differs from the
field name returned on read.
"""

import sys
from pathlib import Path

collection_root = Path(__file__).parent.parent.parent.parent.parent
sys.path.insert(0, str(collection_root))

from plugins.module_utils.vast.diff import (
    compute_patch,
    flatten_subresources,
    has_changes,
    values_equal,
)


def test_values_equal_nested_dict_subset_ignores_extra_server_keys():
    current = {"max_reads_bw_mbps": 1000, "max_writes_bw_mbps": 1000, "max_reads_iops": 10000}
    desired = {"max_reads_bw_mbps": 1000, "max_writes_bw_mbps": 1000}
    assert values_equal(current, desired) is True


def test_values_equal_nested_dict_subset_detects_value_mismatch():
    current = {"max_reads_bw_mbps": 1000, "max_writes_bw_mbps": 1000}
    desired = {"max_reads_bw_mbps": 2000}
    assert values_equal(current, desired) is False


def test_values_equal_nested_dict_subset_detects_missing_key_in_current():
    current = {"max_reads_bw_mbps": 1000}
    desired = {"max_writes_bw_mbps": 1000}
    assert values_equal(current, desired) is False


def test_values_equal_empty_desired_dict_uses_strict_equality():
    assert values_equal({}, {}) is True
    assert values_equal({"a": 1}, {}) is False


def test_values_equal_nested_dict_recurses():
    current = {"limits": {"bw": 1000, "iops": 10000, "extra": "ignored"}}
    desired = {"limits": {"bw": 1000, "iops": 10000}}
    assert values_equal(current, desired) is True


def test_compute_patch_subset_match_static_limits_idempotent():
    """qospolicies-style: server echoes extras; user-provided subset stays clean."""
    current = {
        "static_limits": {
            "max_reads_bw_mbps": 1000,
            "max_writes_bw_mbps": 1000,
            "max_reads_iops": 10000,
            "max_writes_iops": 10000,
        },
    }
    desired = {
        "static_limits": {
            "max_reads_bw_mbps": 1000,
            "max_writes_bw_mbps": 1000,
        },
    }
    assert compute_patch(current, desired) == {}


def test_compute_patch_subset_match_emits_patch_on_change():
    current = {"static_limits": {"max_reads_bw_mbps": 1000, "max_writes_bw_mbps": 1000}}
    desired = {"static_limits": {"max_reads_bw_mbps": 2000}}
    assert compute_patch(current, desired) == {"static_limits": {"max_reads_bw_mbps": 2000}}


def test_values_equal_exact_dict_detects_extra_current_keys():
    current = {"X-A": "9", "X-B": "2"}
    desired = {"X-A": "9"}
    assert values_equal(current, desired, exact_dict=True) is False
    assert values_equal(current, desired, exact_dict=False) is True


def test_values_equal_exact_dict_idempotent_when_identical():
    current = {"X-A": "9", "X-B": "2"}
    desired = {"X-A": "9", "X-B": "2"}
    assert values_equal(current, desired, exact_dict=True) is True


def test_compute_patch_exact_dict_fields_emits_on_key_removal():
    current = {"headers": {"X-A": "9", "X-B": "2"}}
    desired = {"headers": {"X-A": "9"}}
    overrides = {"exact_dict_fields": {"headers"}}
    assert compute_patch(current, desired, overrides) == {"headers": {"X-A": "9"}}


def test_compute_patch_exact_dict_fields_idempotent_when_identical():
    current = {"headers": {"X-A": "9", "X-B": "2"}}
    desired = {"headers": {"X-A": "9", "X-B": "2"}}
    overrides = {"exact_dict_fields": {"headers"}}
    assert compute_patch(current, desired, overrides) == {}


def test_compute_patch_without_exact_dict_still_subset_for_headers():
    """Default (no override) keeps subset semantics for nested dicts."""
    current = {"headers": {"X-A": "9", "X-B": "2"}}
    desired = {"headers": {"X-A": "9"}}
    assert compute_patch(current, desired) == {}


def test_compute_patch_no_changes_simple():
    current = {"name": "r1", "ldap_groups": ["a", "b"]}
    desired = {"name": "r1", "ldap_groups": ["a", "b"]}
    assert compute_patch(current, desired) == {}


def test_compute_patch_skips_none_values():
    current = {"name": "r1"}
    desired = {"name": "r1", "ldap_groups": None}
    assert compute_patch(current, desired) == {}


def test_compute_patch_set_like_lists_order_insensitive():
    current = {"permissions": ["a", "b"]}
    desired = {"permissions": ["b", "a"]}
    overrides = {"set_like_lists": {"permissions"}}
    assert compute_patch(current, desired, overrides) == {}


def test_compute_patch_renamed_on_response_idempotent():
    """Param `permissions_list` matches API field `permissions` despite rename."""
    current = {"permissions": ["a", "b"]}
    desired = {"permissions_list": ["b", "a"]}
    overrides = {
        "set_like_lists": {"permissions"},
        "renamed_on_response": {"permissions_list": "permissions"},
    }
    assert compute_patch(current, desired, overrides) == {}


def test_compute_patch_renamed_on_response_emits_param_name_on_change():
    """When a change is detected, patch is keyed by the param (input) name."""
    current = {"permissions": ["a"]}
    desired = {"permissions_list": ["a", "b"]}
    overrides = {
        "set_like_lists": {"permissions"},
        "renamed_on_response": {"permissions_list": "permissions"},
    }
    assert compute_patch(current, desired, overrides) == {"permissions_list": ["a", "b"]}


def test_compute_patch_renamed_on_response_param_in_set_like_lists_only():
    """set_like comparison still works when only the param name is registered."""
    current = {"permissions": ["a", "b"]}
    desired = {"permissions_list": ["b", "a"]}
    overrides = {
        "set_like_lists": {"permissions_list"},
        "renamed_on_response": {"permissions_list": "permissions"},
    }
    assert compute_patch(current, desired, overrides) == {}


def test_compute_patch_param_not_in_renamed_on_response_falls_back():
    """Params absent from renamed_on_response are looked up by their own name in current."""
    current = {"name": "r1", "permissions": ["a"]}
    desired = {"name": "r2", "permissions_list": ["a"]}
    overrides = {
        "set_like_lists": {"permissions"},
        "renamed_on_response": {"permissions_list": "permissions"},
    }
    assert compute_patch(current, desired, overrides) == {"name": "r2"}


def test_compute_patch_multiple_renamed_fields():
    current = {"permissions": ["a"], "tenants": [1, 2]}
    desired = {"permissions_list": ["a"], "tenant_ids": [2, 1]}
    overrides = {
        "set_like_lists": {"permissions", "tenants"},
        "renamed_on_response": {
            "permissions_list": "permissions",
            "tenant_ids": "tenants",
        },
    }
    assert compute_patch(current, desired, overrides) == {}


def test_compute_patch_change_in_one_renamed_field_only():
    current = {"permissions": ["a"], "tenants": [1, 2]}
    desired = {"permissions_list": ["a", "b"], "tenant_ids": [2, 1]}
    overrides = {
        "set_like_lists": {"permissions", "tenants"},
        "renamed_on_response": {
            "permissions_list": "permissions",
            "tenant_ids": "tenants",
        },
    }
    assert compute_patch(current, desired, overrides) == {"permissions_list": ["a", "b"]}


def test_has_changes_respects_renamed_on_response():
    current = {"permissions": ["a", "b"]}
    desired = {"permissions_list": ["b", "a"]}
    overrides = {
        "set_like_lists": {"permissions"},
        "renamed_on_response": {"permissions_list": "permissions"},
    }
    assert has_changes(current, desired, overrides) is False


def test_flatten_subresources_lifts_nested_to_top_level():
    resource = {"ldap": {"uid": "sAMAccountName", "gid_number": "gidNumber"}}
    assert flatten_subresources(resource, {"ldap"}) == {
        "uid": "sAMAccountName",
        "gid_number": "gidNumber",
    }


def test_flatten_subresources_does_not_overwrite_existing_top_level():
    resource = {"uid": "keep", "ldap": {"uid": "drop"}}
    assert flatten_subresources(resource, {"ldap"}) == {"uid": "keep"}


def test_flatten_subresources_overwrites_explicit_none_top_level():
    resource = {"uid": None, "ldap": {"uid": "sAMAccountName"}}
    assert flatten_subresources(resource, {"ldap"}) == {"uid": "sAMAccountName"}


def test_flatten_subresources_preserves_falsy_top_level():
    resource = {"count": 0, "ldap": {"count": 5}}
    assert flatten_subresources(resource, {"ldap"}) == {"count": 0}


def test_flatten_subresources_skips_non_dict_nested():
    resource = {"ldap": "not-a-dict", "name": "x"}
    assert flatten_subresources(resource, {"ldap"}) == {"ldap": "not-a-dict", "name": "x"}


def test_flatten_subresources_drops_nested_key_after_flatten():
    resource = {"ldap": {"uid": "u"}}
    result = flatten_subresources(resource, {"ldap"})
    assert "ldap" not in result


def test_flatten_subresources_empty_keys_is_noop():
    resource = {"ldap": {"uid": "u"}}
    assert flatten_subresources(resource, set()) == {"ldap": {"uid": "u"}}


def test_flatten_subresources_empty_resource_is_noop():
    assert flatten_subresources({}, {"ldap"}) == {}


def test_flatten_subresources_multiple_keys():
    resource = {"ldap": {"uid": "u"}, "posix": {"gid": "g"}}
    assert flatten_subresources(resource, {"ldap", "posix"}) == {"uid": "u", "gid": "g"}


def test_compute_patch_clear_field_emits_null_when_currently_set():
    current = {"customer_id": "ACME"}
    desired = {"customer_id": None}
    assert compute_patch(current, desired, clear_fields={"customer_id"}) == {"customer_id": None}


def test_compute_patch_clear_field_idempotent_when_already_null():
    current = {"customer_id": None}
    desired = {"customer_id": None}
    assert compute_patch(current, desired, clear_fields={"customer_id"}) == {}


def test_compute_patch_none_without_clear_is_omitted():
    current = {"customer_id": "ACME"}
    desired = {"customer_id": None}
    assert compute_patch(current, desired) == {}
