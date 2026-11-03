#!/usr/bin/env python
# -*- coding: utf-8 -*-
# Copyright: (c) 2026, VAST Data
# Apache License 2.0 (see LICENSE or https://www.apache.org/licenses/LICENSE-2.0)
# SPDX-License-Identifier: Apache-2.0
"""Unit tests for KeyedShowCrudMixin show-first existence lookup."""

import json
import sys
from pathlib import Path

import pytest

requests_mock = pytest.importorskip("requests_mock")

collection_root = Path(__file__).parent.parent.parent.parent.parent
sys.path.insert(0, str(collection_root))
sys.path.insert(0, str(collection_root.parent.parent.parent))

from ansible_collections.vastdata.vms.plugins.module_utils.vast.keyed_show_lookup import (  # noqa: E402
    KeyedShowCrudMixin as CollectionKeyedShowCrudMixin,
)
from ansible_collections.vastdata.vms.plugins.module_utils.vast.resource import BaseResource as CollectionBaseResource  # noqa: E402
from ansible_collections.vastdata.vms.plugins.modules.blobexpansions import (  # noqa: E402
    BlobexpansionResource,
)
from plugins.module_utils.vast.errors import VastAPIError
from plugins.module_utils.vast.keyed_show_lookup import KeyedShowCrudMixin
from plugins.module_utils.vast.resource import BaseResource

BASE = "https://vms.test/api/latest"
JSON_CT = {"Content-Type": "application/json"}


class _Exit(Exception):
    def __init__(self, kwargs):
        self.kwargs = kwargs


class _Fail(Exception):
    def __init__(self, kwargs):
        self.kwargs = kwargs


class FakeModule:
    """Minimal AnsibleModule stand-in. exit_json/fail_json raise to unwind."""

    def __init__(self, params, check_mode=False):
        self.params = params
        self.check_mode = check_mode
        self.warnings = []

    def warn(self, msg):
        self.warnings.append(msg)

    def exit_json(self, **kwargs):
        raise _Exit(kwargs)

    def fail_json(self, **kwargs):
        raise _Fail(kwargs)


class ShowLookupResource(KeyedShowCrudMixin, BaseResource):
    resource_name = "topics"
    singular = "topic"
    lookup_field = "name"
    show_query_fields = ("database_name", "name")
    list_fallback_scope_fields = ("database_name",)
    list_fallback_match_field = "name"


class NoFallbackShowLookupResource(KeyedShowCrudMixin, BaseResource):
    resource_name = "topics"
    singular = "topic"
    lookup_field = "name"
    show_query_fields = ("database_name", "name")
    list_fallback_scope_fields = None


class BlobShowLookupResource(KeyedShowCrudMixin, BaseResource):
    resource_name = "blobexpansions"
    singular = "blobexpansion"
    lookup_field = "database_name"
    can_create = True
    create_only_fields = {"database_name", "table_name", "target_table_name"}
    show_query_fields = ("database_name", "table_name", "source_column_name", "tenant_id")
    required_show_query_fields = ("database_name", "table_name")
    not_found_body_substrings = ("Invalid blob expansion configuration",)

    def _get_raw(self, lookup_value=None, resource_id=None, unique_constraints=None):
        if resource_id is not None:
            return BaseResource._get_raw(self, lookup_value, resource_id, unique_constraints)
        return self.find_current()


def _params(state="present", check=False, **fields):
    params = {
        "vms": {"host": "vms.test", "token": "secret"},
        "state": state,
        "id": None,
        "clear_fields": None,
        "database_name": fields.pop("database_name", "db1"),
        "name": fields.pop("name", "topic1"),
    }
    params.update(fields)
    return params


def _blob_params(**fields):
    params = {
        "vms": {"host": "vms.test", "token": "secret"},
        "state": "present",
        "id": None,
        "clear_fields": None,
        "database_name": fields.pop("database_name", "bucket1"),
        "table_name": fields.pop("table_name", "topic1"),
        "source_column_name": None,
        "tenant_id": None,
    }
    params.update(fields)
    return params


def _mock_version(m, version="5.5.0"):
    m.get(f"{BASE}/clusters/", json=[{"id": 1, "sw_version": version}], headers=JSON_CT)


def _run(resource_cls, params, check_mode=False):
    module = FakeModule(params, check_mode=check_mode)
    try:
        resource = resource_cls(module)
        resource.run()
    except _Exit as e:
        return module, ("exit", e.kwargs)
    except _Fail as e:
        return module, ("fail", e.kwargs)
    raise AssertionError("run() did not call exit_json/fail_json")


def _make_resource(resource_cls, params):
    return resource_cls(FakeModule(params))


def test_find_current_returns_show_body():
    show_body = {"id": 1, "name": "topic1", "database_name": "db1"}
    with requests_mock.Mocker() as m:
        _mock_version(m)
        show = m.get(f"{BASE}/topics/show/", json=show_body, headers=JSON_CT)
        resource = _make_resource(ShowLookupResource, _params())
        result = resource.find_current()

    assert result == show_body
    assert show.called
    assert show.call_count == 1


def test_find_current_404_without_list_fallback_returns_none():
    with requests_mock.Mocker() as m:
        _mock_version(m)
        m.get(f"{BASE}/topics/show/", status_code=404, json={}, headers=JSON_CT)
        resource = _make_resource(NoFallbackShowLookupResource, _params())
        result = resource.find_current()

    assert result is None


def test_find_current_404_falls_back_to_list_hit():
    row = {"id": 2, "name": "topic1", "database_name": "db1"}
    with requests_mock.Mocker() as m:
        _mock_version(m)
        m.get(f"{BASE}/topics/show/", status_code=404, json={}, headers=JSON_CT)
        m.get(f"{BASE}/topics/", json={"results": [row]}, headers=JSON_CT)
        resource = _make_resource(ShowLookupResource, _params())
        result = resource.find_current()

    assert result == row


def test_find_current_404_falls_back_to_list_miss():
    with requests_mock.Mocker() as m:
        _mock_version(m)
        m.get(f"{BASE}/topics/show/", status_code=404, json={}, headers=JSON_CT)
        m.get(f"{BASE}/topics/", json={"results": [{"id": 3, "name": "other", "database_name": "db1"}]}, headers=JSON_CT)
        resource = _make_resource(ShowLookupResource, _params())
        result = resource.find_current()

    assert result is None


def test_find_current_400_falls_back_to_list():
    row = {"id": 4, "name": "topic1", "database_name": "db1"}
    with requests_mock.Mocker() as m:
        _mock_version(m)
        m.get(
            f"{BASE}/topics/show/",
            status_code=400,
            json={"detail": "topic not found"},
            headers=JSON_CT,
        )
        m.get(f"{BASE}/topics/", json={"results": [row]}, headers=JSON_CT)
        resource = _make_resource(ShowLookupResource, _params())
        result = resource.find_current()

    assert result == row


def test_find_current_list_accepts_bare_object():
    row = {"id": 5, "name": "topic1", "database_name": "db1"}
    with requests_mock.Mocker() as m:
        _mock_version(m)
        m.get(f"{BASE}/topics/show/", status_code=404, json={}, headers=JSON_CT)
        m.get(f"{BASE}/topics/", json=row, headers=JSON_CT)
        resource = _make_resource(ShowLookupResource, _params())
        result = resource.find_current()

    assert result == row


def test_blob_500_invalid_config_is_absent():
    with requests_mock.Mocker() as m:
        _mock_version(m)
        m.get(
            f"{BASE}/blobexpansions/show/",
            status_code=500,
            json={"detail": "Getting blob_expansion details failed. Invalid blob expansion configuration for table=x"},
            headers=JSON_CT,
        )
        posted = {}

        def _post(request, context):
            posted.update(json.loads(request.body))
            return {"id": 3, "database_name": "bucket1"}

        m.post(f"{BASE}/blobexpansions/", json=_post, headers=JSON_CT)

        _module, (kind, result) = _run(BlobShowLookupResource, _blob_params(target_table_name="tt"))

    assert kind == "exit"
    assert result["changed"] is True
    assert posted["database_name"] == "bucket1"


def test_blob_unexpected_500_fails():
    with requests_mock.Mocker() as m:
        _mock_version(m)
        m.get(
            f"{BASE}/blobexpansions/show/",
            status_code=500,
            json={"detail": "internal server error"},
            headers=JSON_CT,
        )

        _module, (kind, result) = _run(BlobShowLookupResource, _blob_params(target_table_name="tt"))

    assert kind == "fail"


def test_blob_show_hit_idempotent():
    with requests_mock.Mocker() as m:
        _mock_version(m)
        m.get(
            f"{BASE}/blobexpansions/show/",
            json={"database_name": "bucket1", "table_name": "topic1", "target_table_name": "tt"},
            headers=JSON_CT,
        )

        _module, (kind, result) = _run(BlobShowLookupResource, _blob_params(target_table_name="tt"))

    assert kind == "exit"
    assert result["changed"] is False


def test_missing_query_field_returns_none():
    params = _params()
    params.pop("name")
    with requests_mock.Mocker() as m:
        _mock_version(m)
        resource = _make_resource(ShowLookupResource, params)
        result = resource.find_current()

    assert result is None
    topic_requests = [r for r in m.request_history if "/topics/" in r.url]
    assert not any("/show/" in r.url for r in topic_requests)


def test_default_required_fields_treat_present_none_as_missing():
    with requests_mock.Mocker() as m:
        _mock_version(m)
        show = m.get(f"{BASE}/topics/show/", json={"id": 9, "name": "other"}, headers=JSON_CT)
        resource = _make_resource(ShowLookupResource, _params(name=None))
        result = resource.find_current()

    assert result is None
    assert not show.called


def test_blob_optional_none_fields_still_call_show():
    with requests_mock.Mocker() as m:
        _mock_version(m)
        show = m.get(
            f"{BASE}/blobexpansions/show/",
            json={"database_name": "bucket1", "table_name": "topic1", "target_table_name": "tt"},
            headers=JSON_CT,
        )
        resource = _make_resource(BlobShowLookupResource, _blob_params(target_table_name="tt"))
        result = resource.find_current()

    assert result["database_name"] == "bucket1"
    assert show.called


def test_find_current_empty_show_without_list_fallback_returns_none():
    with requests_mock.Mocker() as m:
        _mock_version(m)
        show = m.get(f"{BASE}/topics/show/", json=[], headers=JSON_CT)
        listed = m.get(f"{BASE}/topics/", json={"results": []}, headers=JSON_CT)
        resource = _make_resource(NoFallbackShowLookupResource, _params())
        result = resource.find_current()

    assert result is None
    assert show.called
    assert not listed.called


def test_find_current_empty_show_falls_back_to_list_hit():
    row = {"id": 6, "name": "topic1", "database_name": "db1"}
    with requests_mock.Mocker() as m:
        _mock_version(m)
        m.get(f"{BASE}/topics/show/", json=[], headers=JSON_CT)
        m.get(f"{BASE}/topics/", json={"results": [row]}, headers=JSON_CT)
        resource = _make_resource(ShowLookupResource, _params())
        result = resource.find_current()

    assert result == row


def test_show_success_non_dict_body_raises_api_error_without_list_fallback():
    with requests_mock.Mocker() as m:
        _mock_version(m)
        m.get(f"{BASE}/topics/show/", json=["not-a-dict"], headers=JSON_CT)
        listed = m.get(f"{BASE}/topics/", json={"results": []}, headers=JSON_CT)
        resource = _make_resource(ShowLookupResource, _params())

        with pytest.raises(VastAPIError, match="show lookup returned str"):
            resource.find_current()

    assert not listed.called


def test_real_blobexpansion_resource_uses_keyed_show_lookup_customization():
    assert CollectionKeyedShowCrudMixin in BlobexpansionResource.__mro__
    assert BlobexpansionResource._get_raw is not CollectionBaseResource._get_raw
    assert BlobexpansionResource.required_show_query_fields == ("database_name", "table_name")
