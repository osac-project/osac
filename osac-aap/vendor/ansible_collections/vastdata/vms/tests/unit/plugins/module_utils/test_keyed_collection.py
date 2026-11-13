#!/usr/bin/env python
# -*- coding: utf-8 -*-
# Copyright: (c) 2026, VAST Data
# Apache License 2.0 (see LICENSE or https://www.apache.org/licenses/LICENSE-2.0)
# SPDX-License-Identifier: Apache-2.0
"""Unit tests for keyed_collection helpers (identity match, find, present/absent)."""

import sys
from pathlib import Path
from unittest.mock import MagicMock

import pytest

collection_root = Path(__file__).parent.parent.parent.parent.parent
sys.path.insert(0, str(collection_root))

from plugins.module_utils.vast.keyed_collection import (  # noqa: E402
    _keyed_collection_find_existing,
    _keyed_collection_identity_matches,
    _keyed_collection_list_rows,
    _keyed_collection_run,
)
from plugins.module_utils.vast.sub_endpoint_resource import (  # noqa: E402
    CrudCapability,
    SubEndpointResource,
)


class _RowApi:
    """Minimal API stub: list via get() (envelope shape), instance via [id]."""

    def __init__(self, rows):
        self.rows = list(rows)
        self.delete_ids = []
        self.patch_calls = []

    def get(self, **_params):
        # Mirror client wrap of a singleton list envelope as ``[envelope]``.
        return [
            {
                "count": len(self.rows),
                "next": None,
                "previous": None,
                "results": list(self.rows),
            }
        ]

    def __getitem__(self, row_id):
        parent = self

        class _Instance:
            def delete(self):
                parent.delete_ids.append(row_id)

            def patch(self, **fields):
                parent.patch_calls.append((row_id, fields))
                for row in parent.rows:
                    if row.get("id") == row_id:
                        row.update(fields)
                        return dict(row)
                return dict(fields)

        return _Instance()


def _make_keyed(
    rows,
    params,
    *,
    identity=None,
    identity_field_map=None,
    supported=None,
    check_mode=False,
    overrides=None,
):
    """Build a keyed-collection SubEndpointResource without touching a live VMS."""
    fake = _RowApi(rows)
    field_map = identity_field_map if identity_field_map is not None else {"label_id": ["label", "id"]}
    identity_params = identity or ["label_id"]
    ops = supported or frozenset({CrudCapability.CREATE, CrudCapability.READ, CrudCapability.UPDATE, CrudCapability.DELETE})

    class _S(SubEndpointResource):
        pass

    _S.parent_resource = "tenants"
    _S.sub_path = "metric_label_values"
    _S.path_has_id = True
    _S.parent_id_param = "tenant_id"
    _S.collection_identity = identity_params
    _S.identity_field_map = field_map
    _S.supported_operations = ops
    _S._api_base = property(lambda self: fake)  # type: ignore[assignment]

    sub = _S.__new__(_S)
    sub.module = MagicMock()
    sub.module.exit_json.side_effect = SystemExit
    sub.module.fail_json.side_effect = SystemExit
    sub.params = params
    sub.check_mode = check_mode
    sub._exclude = set(SubEndpointResource._EXCLUDE_KEYS) | {"tenant_id"}
    sub.overrides = overrides or {}
    sub._fake = fake
    # Bind helpers the same way generated modules do.
    sub._list_rows = _keyed_collection_list_rows.__get__(sub, _S)
    sub._identity_matches = _keyed_collection_identity_matches.__get__(sub, _S)
    sub._find_existing = _keyed_collection_find_existing.__get__(sub, _S)
    sub.run = _keyed_collection_run.__get__(sub, _S)
    sub.create = MagicMock(side_effect=lambda payload: {"id": 99, **payload})
    return sub


class TestIdentityMatches:
    """_identity_matches: flat key, nested map, type coerce."""

    def test_flat_key_match(self):
        sub = _make_keyed([], {"key": "foo"}, identity=["key"], identity_field_map={})
        assert sub._identity_matches({"key": "foo", "id": 1}, "key") is True
        assert sub._identity_matches({"key": "bar", "id": 1}, "key") is False

    def test_nested_label_id_match(self):
        sub = _make_keyed([], {"label_id": 5})
        assert sub._identity_matches({"label": {"id": 5}, "value": "v"}, "label_id") is True
        assert sub._identity_matches({"label": {"id": 9}, "value": "v"}, "label_id") is False

    def test_nested_path_broken_is_miss(self):
        sub = _make_keyed([], {"label_id": 5})
        assert sub._identity_matches({"label": "not-a-dict"}, "label_id") is False
        assert sub._identity_matches({}, "label_id") is False

    def test_coerce_int_actual_to_string_desired(self):
        """API int still matches Ansible string desired (Dor review)."""
        sub = _make_keyed([], {"label_id": "5"})
        assert sub._identity_matches({"label": {"id": 5}}, "label_id") is True
        assert sub._identity_matches({"label": {"id": 6}}, "label_id") is False


class TestFindExisting:
    """_find_existing: hit / miss across the listed rows."""

    def test_find_hit(self):
        rows = [
            {"id": 1, "label": {"id": 1}, "value": "a"},
            {"id": 2, "label": {"id": 5}, "value": "b"},
        ]
        sub = _make_keyed(rows, {"label_id": 5, "value": "b"})
        assert sub._find_existing() == rows[1]

    def test_find_miss(self):
        rows = [{"id": 1, "label": {"id": 1}, "value": "a"}]
        sub = _make_keyed(rows, {"label_id": 99, "value": "x"})
        assert sub._find_existing() == {}

    def test_skips_non_dict_rows(self):
        rows = ["garbage", {"id": 1, "label": {"id": 5}, "value": "ok"}]
        sub = _make_keyed(rows, {"label_id": 5})
        assert sub._find_existing()["id"] == 1


class TestRunMissingIdentity:
    def test_fail_json_when_identity_param_none(self):
        sub = _make_keyed([], {"label_id": None, "value": "v", "state": "present"})
        with pytest.raises(SystemExit):
            sub.run()
        msg = sub.module.fail_json.call_args.kwargs["msg"]
        assert "label_id" in msg
        assert "missing required argument" in msg


class TestRunPresent:
    def test_create_when_absent(self):
        sub = _make_keyed([], {"label_id": 5, "value": "v1", "state": "present", "tenant_id": 1})
        with pytest.raises(SystemExit):
            sub.run()
        sub.create.assert_called_once_with({"label_id": 5, "value": "v1"})
        kwargs = sub.module.exit_json.call_args.kwargs
        assert kwargs["changed"] is True
        assert kwargs[sub.module_result_key]["value"] == "v1"

    def test_idempotent_when_exists_same_value(self):
        rows = [{"id": 2, "label": {"id": 5}, "value": "v1"}]
        sub = _make_keyed(rows, {"label_id": 5, "value": "v1", "state": "present", "tenant_id": 1})
        with pytest.raises(SystemExit):
            sub.run()
        sub.create.assert_not_called()
        assert sub._fake.patch_calls == []
        kwargs = sub.module.exit_json.call_args.kwargs
        assert kwargs["changed"] is False

    def test_patch_when_value_differs(self):
        rows = [{"id": 2, "label": {"id": 5}, "value": "old"}]
        sub = _make_keyed(rows, {"label_id": 5, "value": "new", "state": "present", "tenant_id": 1})
        with pytest.raises(SystemExit):
            sub.run()
        assert sub._fake.patch_calls == [(2, {"value": "new"})]
        kwargs = sub.module.exit_json.call_args.kwargs
        assert kwargs["changed"] is True


class TestRunAbsent:
    def test_delete_when_present(self):
        rows = [{"id": 2, "label": {"id": 5}, "value": "v1"}]
        sub = _make_keyed(rows, {"label_id": 5, "state": "absent", "tenant_id": 1})
        with pytest.raises(SystemExit):
            sub.run()
        assert sub._fake.delete_ids == [2]
        kwargs = sub.module.exit_json.call_args.kwargs
        assert kwargs["changed"] is True
        assert kwargs[sub.module_result_key] == {}

    def test_noop_when_already_gone(self):
        sub = _make_keyed([], {"label_id": 5, "state": "absent", "tenant_id": 1})
        with pytest.raises(SystemExit):
            sub.run()
        assert sub._fake.delete_ids == []
        kwargs = sub.module.exit_json.call_args.kwargs
        assert kwargs["changed"] is False
