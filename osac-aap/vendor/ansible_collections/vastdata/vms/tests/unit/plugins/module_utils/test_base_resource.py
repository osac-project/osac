#!/usr/bin/env python
# -*- coding: utf-8 -*-
# Copyright: (c) 2026, VAST Data
# Apache License 2.0 (see LICENSE or https://www.apache.org/licenses/LICENSE-2.0)
# SPDX-License-Identifier: Apache-2.0
"""Framework-level tests for BaseResource / BaseInfoResource.

These exercise the shared CRUD lifecycle once against a mocked VMS REST API
(via requests-mock). Because every generated module delegates to these base
classes, this protects the create/update/delete/idempotency/404/version-gating
behavior for all ~329 modules in one place.
"""

import json
import sys
from pathlib import Path

import pytest

requests_mock = pytest.importorskip("requests_mock")

collection_root = Path(__file__).parent.parent.parent.parent.parent
sys.path.insert(0, str(collection_root))

from plugins.module_utils.vast.client import unwrap_list_envelope
from plugins.module_utils.vast.info_resource import BaseInfoResource
from plugins.module_utils.vast.resource import BaseResource
from plugins.module_utils.vast.sub_endpoint_resource import CrudCapability, SubEndpointResource

BASE = "https://vms.test/api/latest"
# The client only parses bodies advertised as JSON, so every mock must say so.
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


class WidgetResource(BaseResource):
    resource_name = "widgets"
    singular = "widget"
    lookup_field = "name"


class WidgetInfoResource(BaseInfoResource):
    resource_name = "widgets"
    return_key = "widgets"


def _params(state="present", check=False, **fields):
    params = {
        "vms": {"host": "vms.test", "token": "secret"},
        "state": state,
        "id": None,
        "clear_fields": None,
        "name": fields.pop("name", "w1"),
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


def test_create_when_absent():
    with requests_mock.Mocker() as m:
        _mock_version(m)
        m.get(f"{BASE}/widgets/", json=[], headers=JSON_CT)  # lookup -> not found
        posted = {}

        def _post(request, context):
            posted.update(json.loads(request.body))
            return {"id": 7, "name": "w1", "size": 10}

        m.post(f"{BASE}/widgets/", json=_post, headers=JSON_CT)

        _module, (kind, result) = _run(WidgetResource, _params(size=10))

    assert kind == "exit"
    assert result["changed"] is True
    assert result["widgets"]["id"] == 7
    assert posted["size"] == 10


def test_idempotent_no_change():
    with requests_mock.Mocker() as m:
        _mock_version(m)
        m.get(f"{BASE}/widgets/", json=[{"id": 7, "name": "w1", "size": 10}], headers=JSON_CT)

        _module, (kind, result) = _run(WidgetResource, _params(size=10))

    assert kind == "exit"
    assert result["changed"] is False


def test_update_when_diff():
    with requests_mock.Mocker() as m:
        _mock_version(m)
        m.get(f"{BASE}/widgets/", json=[{"id": 7, "name": "w1", "size": 10}], headers=JSON_CT)
        patched = {}

        def _patch(request, context):
            patched.update(json.loads(request.body))
            return {"id": 7, "name": "w1", "size": 20}

        m.patch(f"{BASE}/widgets/7/", json=_patch, headers=JSON_CT)

        _module, (kind, result) = _run(WidgetResource, _params(size=20))

    assert kind == "exit"
    assert result["changed"] is True
    assert patched["size"] == 20


def test_delete_when_present():
    with requests_mock.Mocker() as m:
        _mock_version(m)
        m.get(f"{BASE}/widgets/", json=[{"id": 7, "name": "w1"}], headers=JSON_CT)
        m.delete(f"{BASE}/widgets/7/", status_code=200, json={}, headers=JSON_CT)

        _module, (kind, result) = _run(WidgetResource, _params(state="absent"))

    assert kind == "exit"
    assert result["changed"] is True


def test_absent_noop_when_missing():
    with requests_mock.Mocker() as m:
        _mock_version(m)
        m.get(f"{BASE}/widgets/", json=[], headers=JSON_CT)

        _module, (kind, result) = _run(WidgetResource, _params(state="absent"))

    assert kind == "exit"
    assert result["changed"] is False


def test_create_in_check_mode_does_not_post():
    with requests_mock.Mocker() as m:
        _mock_version(m)
        m.get(f"{BASE}/widgets/", json=[], headers=JSON_CT)
        # No POST registered: if create() were called it would raise.

        _module, (kind, result) = _run(WidgetResource, _params(size=10), check_mode=True)

    assert kind == "exit"
    assert result["changed"] is True


def test_404_on_id_lookup_treated_as_absent():
    with requests_mock.Mocker() as m:
        _mock_version(m)
        m.get(f"{BASE}/widgets/9/", status_code=404, text="not found")

        params = _params(state="absent")
        params["id"] = 9
        _module, (kind, result) = _run(WidgetResource, params)

    assert kind == "exit"
    assert result["changed"] is False


def test_unsupported_version_fails():
    with requests_mock.Mocker() as m:
        _mock_version(m, version="6.0.0")

        _module, (kind, result) = _run(WidgetResource, _params(size=10))

    assert kind == "fail"
    assert "not supported" in result["msg"].lower()


class BgpConfigResource(SubEndpointResource):
    parent_resource = "cnodes"
    sub_path = "bgp_config"
    path_has_id = True
    parent_id_param = "cnode_id"
    supported_operations = frozenset({CrudCapability.READ, CrudCapability.UPDATE})


def _sub_params(**fields):
    params = {
        "vms": {"host": "vms.test", "token": "secret"},
        "state": "present",
        "wait": True,
        "wait_timeout": 300,
        "cnode_id": 5,
    }
    params.update(fields)
    return params


def test_sub_endpoint_idempotent_no_change():
    with requests_mock.Mocker() as m:
        _mock_version(m)
        m.get(f"{BASE}/cnodes/5/bgp_config/", json=[{"enabled": True}], headers=JSON_CT)

        _module, (kind, result) = _run(BgpConfigResource, _sub_params(enabled=True))

    assert kind == "exit"
    assert result["changed"] is False


def test_sub_endpoint_update_when_diff():
    with requests_mock.Mocker() as m:
        _mock_version(m)
        m.get(f"{BASE}/cnodes/5/bgp_config/", json=[{"enabled": False}], headers=JSON_CT)
        m.patch(f"{BASE}/cnodes/5/bgp_config/", json={"enabled": True}, headers=JSON_CT)

        _module, (kind, result) = _run(BgpConfigResource, _sub_params(enabled=True))

    assert kind == "exit"
    assert result["changed"] is True


def test_info_resource_lists_without_change():
    with requests_mock.Mocker() as m:
        _mock_version(m)
        m.get(f"{BASE}/widgets/", json=[{"id": 1, "name": "a"}, {"id": 2, "name": "b"}], headers=JSON_CT)

        params = {"vms": {"host": "vms.test", "token": "secret"}}
        _module, (kind, result) = _run(WidgetInfoResource, params)

    assert kind == "exit"
    assert result["changed"] is False
    assert len(result["widgets"]) == 2


# ---------------------------------------------------------------------------
# Composite-key lookup: response_filters, renamed_on_response, unique_constraints
# (userquotas-style resources whose list rows must be filtered/renamed before
# matching). Covers the _get_by_* / _apply_response_filters / _get_field_value
# helpers.
# ---------------------------------------------------------------------------


class FilteredWidgetResource(BaseResource):
    resource_name = "widgets"
    singular = "widget"
    lookup_field = "name"

    _extra_overrides = {
        "unique_constraints": {"name", "quota_id"},
        "renamed_on_response": {"quota_id": "quota_system_id", "name": "entity_identifier"},
        "response_filters": {"is_accountable": True},
    }

    def __init__(self, module):
        super().__init__(module)
        self.overrides = {**self.overrides, **self._extra_overrides}


class _FakeDetail:
    """Result of api[resource_id]; first() returns the matching row (or None)."""

    def __init__(self, row):
        self._row = row

    def first(self):
        return dict(self._row) if self._row else None


class _FakeApi:
    """Stands in for client.api[resource_name]; get() returns preset rows."""

    def __init__(self, rows):
        self._rows = rows

    def get(self, **kwargs):
        return [dict(r) for r in self._rows]

    def __getitem__(self, resource_id):
        return _FakeDetail(next((r for r in self._rows if r.get("id") == resource_id), None))


class _FakeApiMap:
    """Stands in for client.api: subscript by resource name -> the endpoint api."""

    def __init__(self, api):
        self._api = api

    def __getitem__(self, _resource_name):
        return self._api


class _FakeClient:
    """Stands in for VastClient so tests can inject a fake api map."""

    def __init__(self, api_map):
        self.api = api_map


def _make(resource_cls, **fields):
    """Instantiate a resource (version handshake mocked), ready for helper calls."""
    with requests_mock.Mocker() as m:
        _mock_version(m)
        return resource_cls(FakeModule(_params(**fields)))


def test_apply_response_filters_excludes_non_matching():
    res = _make(FilteredWidgetResource)
    rows = [
        {"id": 1, "is_accountable": False},
        {"id": 2, "is_accountable": True},
    ]
    assert [r["id"] for r in res._apply_response_filters(rows)] == [2]


def test_apply_response_filters_noop_without_override():
    res = _make(WidgetResource)
    rows = [{"id": 1}, {"id": 2}]
    assert res._apply_response_filters(rows) == rows


def test_get_field_value_uses_renamed_on_response():
    res = _make(FilteredWidgetResource)
    # Response carries quota_system_id; the module param is quota_id.
    assert res._get_field_value({"quota_system_id": 42}, "quota_id") == 42


def test_get_field_value_renames_lookup_field():
    res = _make(FilteredWidgetResource)
    # Rows expose the name as entity_identifier, not a top-level "name".
    assert res._get_field_value({"entity_identifier": "alice"}, "name") == "alice"


def test_get_by_unique_constraints_applies_response_filter():
    res = _make(FilteredWidgetResource)
    api = _FakeApi(
        [
            {"id": 1, "quota_system_id": 5, "is_accountable": False},
            {"id": 2, "quota_system_id": 5, "is_accountable": True},
        ]
    )
    found = res._get_by_unique_constraints(api, {"quota_id": 5})
    assert found["id"] == 2


def test_get_by_unique_constraints_multiple_matches_fails():
    res = _make(FilteredWidgetResource)
    api = _FakeApi(
        [
            {"id": 1, "quota_system_id": 5, "is_accountable": True},
            {"id": 2, "quota_system_id": 5, "is_accountable": True},
        ]
    )
    with pytest.raises(_Fail) as exc:
        res._get_by_unique_constraints(api, {"quota_id": 5})
    assert "Multiple" in exc.value.kwargs["msg"]


def test_get_by_lookup_field_refines_by_renamed_constraint():
    res = _make(FilteredWidgetResource)
    api = _FakeApi(
        [
            {"id": 1, "name": "w1", "quota_system_id": 5, "is_accountable": True},
            {"id": 2, "name": "w1", "quota_system_id": 9, "is_accountable": True},
        ]
    )
    found = res._get_by_lookup_field(api, "w1", {"quota_id": 9})
    assert found["id"] == 2


def test_get_by_lookup_field_ambiguous_constraint_fails():
    res = _make(FilteredWidgetResource)
    api = _FakeApi(
        [
            {"id": 1, "name": "w1", "quota_system_id": 9, "is_accountable": True},
            {"id": 2, "name": "w1", "quota_system_id": 9, "is_accountable": True},
        ]
    )
    with pytest.raises(_Fail) as exc:
        res._get_by_lookup_field(api, "w1", {"quota_id": 9})
    assert "Multiple" in exc.value.kwargs["msg"]


def test_get_raw_disambiguates_same_parent_by_lookup_field():
    """Two distinct users share one parent quota_id. Because the lookup_field is
    part of unique_constraints, _get_raw must route through the name+quota_id
    match (resolving name via entity_identifier) and return the right row --
    not raise a spurious 'Multiple found' nor miss the match (idempotency)."""
    res = _make(FilteredWidgetResource, name="alice", quota_id=5)
    res.client = _FakeClient(
        _FakeApiMap(
            _FakeApi(
                [
                    {"id": 1, "entity_identifier": "alice", "quota_system_id": 5, "is_accountable": True},
                    {"id": 2, "entity_identifier": "bob", "quota_system_id": 5, "is_accountable": True},
                ]
            )
        )
    )
    found = res._get_raw(lookup_value="alice", unique_constraints={"name": "alice", "quota_id": 5})
    assert found is not None
    assert found["id"] == 1


# ---------------------------------------------------------------------------
# Multipart file-upload create/update (Swagger ``in: formData`` ``type: file``).
# Models the tlscertificates resource: create POSTs multipart/form-data with a
# file part + form fields, idempotency is keyed on ca_certificate_name, and the
# file is write-only (never diffed, never re-uploaded on update).
# ---------------------------------------------------------------------------


class CertResource(BaseResource):
    resource_name = "tlscertificates"
    singular = "tlscertificate"
    lookup_field = "ca_certificate_name"
    file_params = {"ca_certificate_file", "revocation_file"}
    resource_min_version = (5, 5)

    _extra_overrides = {
        "unique_constraints": {"ca_certificate_name"},
        "response_normalizer": unwrap_list_envelope,
    }

    def __init__(self, module):
        super().__init__(module)
        self.overrides = {**self.overrides, **self._extra_overrides}


def _cert_params(state="present", **fields):
    params = {
        "vms": {"host": "vms.test", "token": "secret"},
        "state": state,
        "id": None,
        "clear_fields": None,
        "ca_certificate_name": fields.pop("ca_certificate_name", "ansible-ca"),
        "ca_certificate_file": None,
        "revocation_file": None,
        "protocols": None,
        "revocations_name": None,
        "tenant_associate_param": None,
        "tenant_id": None,
        "wait": True,
        "wait_timeout": 300,
    }
    params.update(fields)
    return params


def _envelope(rows):
    return {"count": len(rows), "next": None, "previous": None, "results": rows}


def test_multipart_create_posts_file_and_fields(tmp_path):
    ca = tmp_path / "ca.pem"
    ca.write_text("---CERT---")

    with requests_mock.Mocker() as m:
        _mock_version(m)
        m.get(f"{BASE}/tlscertificates/", json=_envelope([]), headers=JSON_CT)
        seen = {}

        def _post(request, context):
            seen["content_type"] = request.headers.get("Content-Type", "")
            seen["body"] = request.text
            return {"id": 5, "ca_certificate_name": "ansible-ca", "protocols": ["NFS", "KAFKA"]}

        m.post(f"{BASE}/tlscertificates/", json=_post, headers=JSON_CT)

        _module, (kind, result) = _run(
            CertResource,
            _cert_params(ca_certificate_file=str(ca), protocols=["NFS", "KAFKA"]),
        )

    assert kind == "exit"
    assert result["changed"] is True
    assert result["tlscertificates"]["id"] == 5
    # Sent as multipart, with the file part and the form fields.
    assert seen["content_type"].startswith("multipart/form-data")
    assert "ca.pem" in seen["body"]
    assert "ca_certificate_name" in seen["body"]
    assert "NFS" in seen["body"] and "KAFKA" in seen["body"]


def test_multipart_create_without_file_fails():
    with requests_mock.Mocker() as m:
        _mock_version(m)
        m.get(f"{BASE}/tlscertificates/", json=_envelope([]), headers=JSON_CT)
        # No POST registered -> must fail before any request.

        _module, (kind, result) = _run(CertResource, _cert_params(protocols=["NFS"]))

    assert kind == "fail"
    assert "file upload" in result["msg"].lower()


def test_multipart_idempotent_no_reupload(tmp_path):
    ca = tmp_path / "ca.pem"
    ca.write_text("---CERT---")

    with requests_mock.Mocker() as m:
        _mock_version(m)
        # Cert already exists with the same metadata; passing the file again must
        # NOT trigger a POST or PATCH (no such mocks registered -> would 404/raise).
        m.get(
            f"{BASE}/tlscertificates/",
            json=_envelope([{"id": 5, "ca_certificate_name": "ansible-ca", "protocols": ["NFS"]}]),
            headers=JSON_CT,
        )

        _module, (kind, result) = _run(
            CertResource,
            _cert_params(ca_certificate_file=str(ca), protocols=["NFS"]),
        )

    assert kind == "exit"
    assert result["changed"] is False


def test_multipart_update_metadata_patches_json_not_file(tmp_path):
    ca = tmp_path / "ca.pem"
    ca.write_text("---CERT---")

    with requests_mock.Mocker() as m:
        _mock_version(m)
        m.get(
            f"{BASE}/tlscertificates/",
            json=_envelope([{"id": 5, "ca_certificate_name": "ansible-ca", "protocols": ["NFS"]}]),
            headers=JSON_CT,
        )
        patched = {}

        def _patch(request, context):
            patched["content_type"] = request.headers.get("Content-Type", "")
            patched["body"] = json.loads(request.body)
            return {"id": 5, "ca_certificate_name": "ansible-ca", "protocols": ["NFS", "KAFKA"]}

        m.patch(f"{BASE}/tlscertificates/5/", json=_patch, headers=JSON_CT)

        _module, (kind, result) = _run(
            CertResource,
            _cert_params(ca_certificate_file=str(ca), protocols=["NFS", "KAFKA"]),
        )

    assert kind == "exit"
    assert result["changed"] is True
    # PATCH is JSON, carries the changed metadata, and never the file.
    assert "application/json" in patched["content_type"]
    assert patched["body"]["protocols"] == ["NFS", "KAFKA"]
    assert "ca_certificate_file" not in patched["body"]


def test_multipart_absent_deletes(tmp_path):
    with requests_mock.Mocker() as m:
        _mock_version(m)
        m.get(
            f"{BASE}/tlscertificates/",
            json=_envelope([{"id": 5, "ca_certificate_name": "ansible-ca"}]),
            headers=JSON_CT,
        )
        m.delete(f"{BASE}/tlscertificates/5/", status_code=204, headers=JSON_CT)

        _module, (kind, result) = _run(CertResource, _cert_params(state="absent"))

    assert kind == "exit"
    assert result["changed"] is True


# ---------------------------------------------------------------------------
# read_query_params: endpoints that omit sub-resources from GET unless a query
# flag is passed. Models the quotas resource: the quota GET echoes
# user_quotas/group_quotas only when the list request carries
# ``show_user_rules=true``, so the read must ask for them to compare them.
# ---------------------------------------------------------------------------


class QuotaResource(BaseResource):
    resource_name = "quotas"
    singular = "quota"
    lookup_field = "path"


def _quota_params(state="present", **fields):
    params = {
        "vms": {"host": "vms.test", "token": "secret"},
        "state": state,
        "id": None,
        "clear_fields": None,
        "wait": True,
        "wait_timeout": 300,
        "path": fields.pop("path", "/ansible-test-quota-nested"),
    }
    params.update(fields)
    return params


def _desired_nested_quota_fields():
    """User-supplied fields for a quota with nested user/group rules."""
    return dict(
        name="test-quota-nested",
        tenant_id=1,
        soft_limit=100000,
        hard_limit=100000,
        is_user_quota=True,
        enable_alarms=True,
        default_email="user@example.com",
        user_quotas=[
            {
                "identifier": "test-user",
                "identifier_type": "username",
                "hard_limit": 15000,
                "soft_limit": 15000,
                "grace_period": "02:00:00",
                "email": "user1@example.com",
            }
        ],
        group_quotas=[
            {
                "identifier": "test-group",
                "identifier_type": "groupname",
                "hard_limit": 15000,
                "soft_limit": 15000,
                "grace_period": "4 03:00:00",
            }
        ],
        default_user_quota={"soft_limit": 50000, "hard_limit": 100000, "hard_limit_inodes": 10000},
        default_group_quota={"soft_limit": 75000, "hard_limit": 150000, "hard_limit_inodes": 15000},
    )


def _ruleless_quota_row():
    """What GET /quotas/ returns WITHOUT show_user_rules: no user/group rules."""
    return {
        "id": 3,
        "name": "test-quota-nested",
        "path": "/ansible-test-quota-nested",
        "tenant_id": 1,
        "soft_limit": 100000,
        "hard_limit": 100000,
        "is_user_quota": True,
        "enable_alarms": True,
        "enable_email_providers": True,
        "default_email": "user@example.com",
        "grace_period": None,
        "default_user_quota": {
            "grace_period": None,
            "hard_limit": 100000,
            "hard_limit_inodes": 10000,
            "quota_system_id": 3,
            "soft_limit": 50000,
            "soft_limit_inodes": None,
        },
        "default_group_quota": {
            "grace_period": None,
            "hard_limit": 150000,
            "hard_limit_inodes": 15000,
            "quota_system_id": 3,
            "soft_limit": 75000,
            "soft_limit_inodes": None,
        },
    }


def _enriched_quota_row():
    """What GET /quotas/?show_user_rules=true returns: nested rules included."""
    row = _ruleless_quota_row()
    row["user_quotas"] = [
        {
            "entity": {
                "email": "",
                "identifier": "test-user",
                "identifier_type": "username",
                "is_group": False,
                "name": "test-user",
                "vast_id": 114,
            },
            "grace_period": "02:00:00",
            "hard_limit": 15000,
            "is_accountable": True,
            "quota_system_id": 3,
            "soft_limit": 15000,
            "state": "OK",
        }
    ]
    row["group_quotas"] = [
        {
            "entity": {
                "email": "",
                "identifier": "test-group",
                "identifier_type": "groupname",
                "is_group": True,
                "name": "test-group",
                "vast_id": 100108,
            },
            "grace_period": "4 03:00:00",
            "hard_limit": 15000,
            "is_accountable": True,
            "quota_system_id": 3,
            "soft_limit": 15000,
            "state": "OK",
        }
    ]
    return row


def test_read_query_params_from_override():
    """quotas carries show_user_rules; a resource without the override gets none."""
    assert _make(QuotaResource)._read_query_params() == {"show_user_rules": True}
    assert _make(WidgetResource)._read_query_params() == {}


def test_quotas_read_requests_show_user_rules_and_is_idempotent():
    """The list read carries show_user_rules so the API echoes the nested rules;
    a re-run with the same values then matches and reports no change.

    No /quotas/3/ GET is registered: the by-id detail refetch must be skipped
    because it cannot honor show_user_rules and would omit the rules."""
    with requests_mock.Mocker() as m:
        _mock_version(m)
        m.get(f"{BASE}/quotas/", json=[_enriched_quota_row()], headers=JSON_CT)

        _module, (kind, result) = _run(QuotaResource, _quota_params(**_desired_nested_quota_fields()))

    assert kind == "exit"
    assert result["changed"] is False
    assert m.last_request.qs.get("show_user_rules") == ["true"]


def test_quotas_read_without_flag_omits_rules_and_patches():
    """Without the override the read omits the nested rules, so the module has
    nothing to compare against and re-sends user_quotas/group_quotas."""

    class QuotaNoFlagResource(QuotaResource):
        def __init__(self, module):
            super().__init__(module)
            self.overrides = {**self.overrides, "read_query_params": {}}

    with requests_mock.Mocker() as m:
        _mock_version(m)
        m.get(f"{BASE}/quotas/", json=[_ruleless_quota_row()], headers=JSON_CT)
        # Missing nested fields trigger the by-id detail refetch (also ruleless).
        m.get(f"{BASE}/quotas/3/", json=_ruleless_quota_row(), headers=JSON_CT)
        patched = {}

        def _patch(request, context):
            patched.update(json.loads(request.body))
            return _enriched_quota_row()

        m.patch(f"{BASE}/quotas/3/", json=_patch, headers=JSON_CT)

        _module, (kind, result) = _run(QuotaNoFlagResource, _quota_params(**_desired_nested_quota_fields()))

    assert kind == "exit"
    assert result["changed"] is True
    assert "user_quotas" in patched
    assert "group_quotas" in patched


def test_quotas_id_lookup_uses_list_with_show_user_rules():
    """id= must not hit /quotas/{id}/ (no show_user_rules there); list+filter instead."""
    with requests_mock.Mocker() as m:
        _mock_version(m)
        m.get(f"{BASE}/quotas/", json=[_enriched_quota_row()], headers=JSON_CT)

        params = _quota_params(**_desired_nested_quota_fields())
        params["id"] = 3
        params["path"] = None
        _module, (kind, result) = _run(QuotaResource, params)

    assert kind == "exit"
    assert result["changed"] is False
    assert m.last_request.qs.get("show_user_rules") == ["true"]
    assert "/quotas/3/" not in m.last_request.url


class QuotagroupResource(BaseResource):
    resource_name = "quotagroups"
    singular = "quotagroup"
    lookup_field = "name"


def test_quotagroups_read_requests_show_user_rules_and_is_idempotent():
    """quotagroups mirrors quotas: the (enveloped) list read carries
    show_user_rules so the API echoes the nested rules and a re-run reports no
    change. No by-id detail GET is registered; it must be skipped."""
    with requests_mock.Mocker() as m:
        _mock_version(m)
        m.get(f"{BASE}/quotagroups/", json=_envelope([_enriched_quota_row()]), headers=JSON_CT)

        params = {
            "vms": {"host": "vms.test", "token": "secret"},
            "state": "present",
            "id": None,
            "clear_fields": None,
            "wait": True,
            "wait_timeout": 300,
            **_desired_nested_quota_fields(),
        }
        _module, (kind, result) = _run(QuotagroupResource, params)

    assert kind == "exit"
    assert result["changed"] is False
    assert m.last_request.qs.get("show_user_rules") == ["true"]
