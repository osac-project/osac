#!/usr/bin/env python
# -*- coding: utf-8 -*-
"""Unit tests for SubEndpointResource read behavior (array vs single object)."""

import sys
from pathlib import Path
from unittest.mock import MagicMock

# Add collection root to path for importing module_utils
collection_root = Path(__file__).parent.parent.parent.parent.parent
sys.path.insert(0, str(collection_root))

from plugins.module_utils.vast import sub_endpoint_resource as sub_endpoint_module  # noqa: E402
from plugins.module_utils.vast.errors import VastAPIError  # noqa: E402
from plugins.module_utils.vast.sub_endpoint_resource import (  # noqa: E402
    CrudCapability,
    SubEndpointResource,
)


class _FakeApiBase:
    """Stand-in for the client API segment, mirroring get()/first() semantics."""

    def __init__(self, results):
        self._results = list(results)
        self.get_calls = []
        self.first_calls = []

    def get(self, **params):
        self.get_calls.append(params)
        return list(self._results)

    def first(self, **params):
        self.first_calls.append(params)
        return self._results[0] if self._results else None


def _make_sub(
    returns_list,
    results,
    path_has_id=True,
    supported=frozenset({CrudCapability.READ}),
    parent_resource="vms",
    overrides=None,
):
    """Build a SubEndpointResource instance bypassing __init__ (no live VMS)."""
    fake = _FakeApiBase(results)

    class _S(SubEndpointResource):
        pass

    _S.parent_resource = parent_resource
    _S.sub_path = "configured_idps"
    _S.path_has_id = path_has_id
    _S.returns_list = returns_list
    _S.supported_operations = supported
    _S._api_base = property(lambda self: fake)  # type: ignore[assignment]

    sub = _S.__new__(_S)
    sub.module = MagicMock()
    sub.params = {}
    sub.check_mode = False
    sub._exclude = set(SubEndpointResource._EXCLUDE_KEYS)
    sub.overrides = overrides or {}
    sub._fake = fake
    return sub


def test_init_prefers_endpoint_override_and_falls_back_to_parent(monkeypatch):
    """Only explicitly configured endpoint keys replace historical parent overrides."""

    class VmsNetworkSettings(SubEndpointResource):
        parent_resource = "vms"
        sub_path = "network_settings"

    def fake_connection_init(self, module):
        self.module = module
        self.params = {}
        self.cluster_mm = (5, 5)

    monkeypatch.setattr(VmsNetworkSettings, "_init_vast_connection", fake_connection_init)
    monkeypatch.setattr(
        sub_endpoint_module,
        "get_overrides",
        lambda key, cluster_mm: {"selected": key},
    )

    monkeypatch.setattr(sub_endpoint_module, "has_overrides", lambda key: key == "vms_network_settings")
    endpoint = VmsNetworkSettings(MagicMock())
    assert endpoint.overrides == {"selected": "vms_network_settings"}

    monkeypatch.setattr(sub_endpoint_module, "has_overrides", lambda key: False)
    fallback = VmsNetworkSettings(MagicMock())
    assert fallback.overrides == {"selected": "vms"}


class TestGetReturnsList:
    """returns_list=True surfaces the whole array response."""

    def test_multiple_items_survive(self):
        """All elements survive instead of collapsing to the first."""
        sub = _make_sub(returns_list=True, results=["okta", "azure", "ping"])
        assert sub.get() == ["okta", "azure", "ping"]

    def test_empty_stays_empty_list(self):
        """An empty collection reads back as []."""
        sub = _make_sub(returns_list=True, results=[])
        assert sub.get() == []

    def test_routes_through_get_not_first(self):
        """The array path uses get(), never first()."""
        sub = _make_sub(returns_list=True, results=["okta"])
        sub.get()
        assert sub._fake.get_calls == [{}]
        assert sub._fake.first_calls == []


class TestGetSingleObject:
    """returns_list=False keeps the legacy single-object behavior."""

    def test_collapses_to_first(self):
        """Non-list endpoints still return only the first element."""
        sub = _make_sub(returns_list=False, results=["okta", "azure"])
        assert sub.get() == "okta"

    def test_empty_returns_empty_dict(self):
        """An empty result is normalized to ``{}`` inside ``get()`` itself."""
        sub = _make_sub(returns_list=False, results=[])
        assert sub.get() == {}

    def test_collection_level_passes_search_params(self):
        """Without a parent id, search params are forwarded to the API call."""
        sub = _make_sub(returns_list=False, results=[{"a": 1}], path_has_id=False)
        sub.params = {"name": "foo", "vms": {}, "state": "present"}
        sub.get()
        assert sub._fake.first_calls == [{"name": "foo"}]

    def test_applies_configured_response_normalizer(self):
        """Sub-endpoint manage reads honor the same normalizer as info reads."""
        sub = _make_sub(
            returns_list=False,
            results=[{"data": {"eth_mtu": 9000}}],
            overrides={"response_normalizer": lambda rows: [row["data"] for row in rows]},
        )
        assert sub.get() == {"eth_mtu": 9000}


class TestRunReadOnly:
    """_run_read_only emits a shape-correct result for both flavors."""

    def test_list_result_emitted_intact(self):
        sub = _make_sub(returns_list=True, results=["okta", "azure", "ping"])
        sub._run_read_only()
        kwargs = sub.module.exit_json.call_args.kwargs
        assert kwargs["changed"] is False
        assert kwargs[sub.module_result_key] == ["okta", "azure", "ping"]

    def test_empty_list_emitted_as_list(self):
        sub = _make_sub(returns_list=True, results=[])
        sub._run_read_only()
        kwargs = sub.module.exit_json.call_args.kwargs
        assert kwargs[sub.module_result_key] == []

    def test_empty_single_emitted_as_dict(self):
        sub = _make_sub(returns_list=False, results=[])
        sub._run_read_only()
        kwargs = sub.module.exit_json.call_args.kwargs
        assert kwargs[sub.module_result_key] == {}


class _FakeDeleteApi:
    """Stand-in API segment that records delete() calls (body + query)."""

    def __init__(self):
        self.delete_calls = []

    def delete(self, *, _query_params=None, **params):
        self.delete_calls.append({"body": params, "query": _query_params})
        return {}


def _make_delete_sub(
    delete_body_fields,
    params,
    path_has_id=False,
    parent_id_param="",
    check_mode=False,
):
    """Build a DELETE-capable SubEndpointResource with a fake delete api base."""
    fake = _FakeDeleteApi()

    class _S(SubEndpointResource):
        pass

    _S.parent_resource = "blobexpansions"
    _S.sub_path = "delete"
    _S.path_has_id = path_has_id
    _S.parent_id_param = parent_id_param
    _S.supported_operations = frozenset({CrudCapability.DELETE})
    _S.delete_body_fields = set(delete_body_fields)
    _S._api_base = property(lambda self: fake)  # type: ignore[assignment]

    sub = _S.__new__(_S)
    sub.module = MagicMock()
    sub.module.exit_json.side_effect = SystemExit
    sub.module.fail_json.side_effect = SystemExit
    sub.params = params
    sub.check_mode = check_mode
    exclude = set(SubEndpointResource._EXCLUDE_KEYS)
    if path_has_id and parent_id_param:
        exclude.add(parent_id_param)
    sub._exclude = exclude
    sub.overrides = {}
    sub.is_async = False
    sub._fake = fake
    return sub


class TestDeleteSendsBody:
    """DELETE sub-endpoints must send their declared body params (issue: blobexpansions)."""

    def test_body_params_are_sent(self):
        sub = _make_delete_sub(
            delete_body_fields={"database_name", "table_name", "source_column_name", "tenant_id"},
            params={
                "vms": {},
                "state": "absent",
                "database_name": "bucket1",
                "table_name": "topic1",
                "source_column_name": None,
                "tenant_id": None,
            },
        )
        try:
            sub._perform_delete()
        except SystemExit:
            pass
        assert len(sub._fake.delete_calls) == 1
        # Only non-None declared body params are sent; None values are dropped.
        assert sub._fake.delete_calls[0]["body"] == {"database_name": "bucket1", "table_name": "topic1"}

    def test_path_has_id_delete_sends_no_body(self):
        """A path-id delete with no body fields (e.g. tlscertificate_crl) sends an empty body."""
        sub = _make_delete_sub(
            delete_body_fields=set(),
            params={"vms": {}, "state": "absent", "tlscertificate_id": 7},
            path_has_id=True,
            parent_id_param="tlscertificate_id",
        )
        try:
            sub._perform_delete()
        except SystemExit:
            pass
        assert sub._fake.delete_calls == [{"body": {}, "query": None}]

    def test_check_mode_does_not_delete(self):
        sub = _make_delete_sub(
            delete_body_fields={"database_name", "table_name"},
            params={"vms": {}, "state": "absent", "database_name": "b", "table_name": "t"},
            check_mode=True,
        )
        try:
            sub._perform_delete()
        except SystemExit:
            pass
        assert sub._fake.delete_calls == []


class _FakeMultipartApi:
    """Stand-in API segment that records post_file() calls."""

    def __init__(self):
        self.post_file_calls = []

    def post_file(self, files, **fields):
        self.post_file_calls.append({"files": files, "fields": fields})
        return {"warnings": []}


def _make_action_sub(file_params, params, supported=frozenset({CrudCapability.CREATE}), check_mode=False):
    """Build a CREATE-action SubEndpointResource with a fake multipart api base."""
    fake = _FakeMultipartApi()

    class _S(SubEndpointResource):
        pass

    _S.parent_resource = "tlscertificates"
    _S.sub_path = "is_operation_healthy"
    _S.path_has_id = False
    _S.supported_operations = supported
    _S.file_params = set(file_params)
    _S._api_base = property(lambda self: fake)  # type: ignore[assignment]

    sub = _S.__new__(_S)
    sub.module = MagicMock()
    sub.module.exit_json.side_effect = SystemExit
    sub.module.fail_json.side_effect = SystemExit
    sub.params = params
    sub.check_mode = check_mode
    sub._exclude = set(SubEndpointResource._EXCLUDE_KEYS) | set(file_params)
    sub.overrides = {}
    sub._fake = fake
    return sub


class TestActionMultipartUpload:
    """POST multipart file-upload action sub-endpoints (e.g. is_operation_healthy)."""

    def test_posts_files_and_excludes_them_from_fields(self, tmp_path):
        ca = tmp_path / "ca.pem"
        ca.write_text("---CERT---")
        sub = _make_action_sub(
            file_params={"ca_certificate_file", "revocation_file"},
            params={
                "vms": {},
                "state": "present",
                "ca_certificate_file": str(ca),
                "revocation_file": None,
            },
        )
        try:
            sub._run_action()
        except SystemExit:
            pass
        assert len(sub._fake.post_file_calls) == 1
        call = sub._fake.post_file_calls[0]
        assert call["files"] == {"ca_certificate_file": str(ca)}
        # File params never leak into the form fields.
        assert "ca_certificate_file" not in call["fields"]
        assert "revocation_file" not in call["fields"]
        kwargs = sub.module.exit_json.call_args.kwargs
        assert kwargs["changed"] is True

    def test_check_mode_does_not_post(self, tmp_path):
        ca = tmp_path / "ca.pem"
        ca.write_text("---CERT---")
        sub = _make_action_sub(
            file_params={"ca_certificate_file", "revocation_file"},
            params={"vms": {}, "state": "present", "ca_certificate_file": str(ca), "revocation_file": None},
            check_mode=True,
        )
        try:
            sub._run_action()
        except SystemExit:
            pass
        assert sub._fake.post_file_calls == []
        kwargs = sub.module.exit_json.call_args.kwargs
        assert kwargs["changed"] is True


class _FakePostApi:
    """Stand-in API segment that records post() calls and returns a fixed body."""

    def __init__(self, response):
        self._response = response
        self.post_calls = []

    def post(self, **fields):
        self.post_calls.append(fields)
        return self._response


class TestActionResultBypassesResponseNormalizer:
    """Action POST responses are not GET list envelopes, so the response_normalizer
    (e.g. unwrap_list_envelope) must NOT touch them."""

    def _make_post_action_sub(self, response, overrides):
        fake = _FakePostApi(response)

        class _S(SubEndpointResource):
            pass

        _S.parent_resource = "quotagroups"
        _S.sub_path = "assign_quotas"
        _S.path_has_id = False
        _S.supported_operations = frozenset({CrudCapability.CREATE})
        _S.file_params = set()
        _S.is_async = False
        _S._api_base = property(lambda self: fake)  # type: ignore[assignment]

        sub = _S.__new__(_S)
        sub.module = MagicMock()
        sub.module.exit_json.side_effect = SystemExit
        sub.module.fail_json.side_effect = SystemExit
        sub.params = {"vms": {}, "state": "present", "quota_ids": [6]}
        sub.check_mode = False
        sub._exclude = set(SubEndpointResource._EXCLUDE_KEYS)
        sub.overrides = overrides
        sub._fake = fake
        return sub

    def test_async_task_envelope_returned_raw(self):
        """An ``{'async_task': {...}}`` 200 body survives even with a normalizer set."""
        from plugins.module_utils.vast.client import unwrap_list_envelope

        body = {"async_task": {"id": 19, "state": "RUNNING"}}
        sub = self._make_post_action_sub(
            response=body,
            overrides={"response_normalizer": unwrap_list_envelope},
        )
        try:
            sub._run_action()
        except SystemExit:
            pass
        kwargs = sub.module.exit_json.call_args.kwargs
        assert kwargs["result"] == body
        sub.module.fail_json.assert_not_called()

    def test_warnings_envelope_returned_raw(self):
        """A ``{'warnings': [...]}`` body (e.g. cert expiring) is not unwrapped."""
        from plugins.module_utils.vast.client import unwrap_list_envelope

        body = {"warnings": ["The certificate file will expire soon."]}
        sub = self._make_post_action_sub(
            response=body,
            overrides={"response_normalizer": unwrap_list_envelope},
        )
        try:
            sub._run_action()
        except SystemExit:
            pass
        kwargs = sub.module.exit_json.call_args.kwargs
        assert kwargs["result"] == body
        sub.module.fail_json.assert_not_called()

    def test_action_response_normalizer_unwraps_data_envelope(self):
        """A dedicated ``action_response_normalizer`` (e.g. network_settings) unwraps
        the ``{data: {...}}`` action body so ``result`` exposes top-level keys."""
        from plugins.module_utils.vast.network_settings import (
            network_settings_response_normalizer,
        )

        body = {"data": {"hosts": [{"id": 1}], "ntp": ["10.0.0.1"]}}
        sub = self._make_post_action_sub(
            response=body,
            overrides={
                "action_response_normalizer": network_settings_response_normalizer,
            },
        )
        try:
            sub._run_action()
        except SystemExit:
            pass
        kwargs = sub.module.exit_json.call_args.kwargs
        assert kwargs["result"]["hosts"] == [{"id": 1}]
        assert kwargs["result"]["ntp"] == ["10.0.0.1"]
        assert "data" not in kwargs["result"]
        sub.module.fail_json.assert_not_called()

    def test_list_envelope_normalizer_ignored_for_action_when_action_key_absent(self):
        """With only the read ``response_normalizer`` set, a ``{data: {...}}`` action
        body is returned raw — the read normalizer must not touch action results."""
        from plugins.module_utils.vast.network_settings import (
            network_settings_response_normalizer,
        )

        body = {"data": {"hosts": [{"id": 1}]}}
        sub = self._make_post_action_sub(
            response=body,
            overrides={"response_normalizer": network_settings_response_normalizer},
        )
        try:
            sub._run_action()
        except SystemExit:
            pass
        kwargs = sub.module.exit_json.call_args.kwargs
        assert kwargs["result"] == body
        sub.module.fail_json.assert_not_called()


class TestRunReadUpdateSetLikeIdempotency:
    """Order-insensitive list fields."""

    def _sub_with_current(self, current, supported_ops, set_like_lists):
        sub = _make_sub(
            returns_list=False,
            results=[current],
            path_has_id=False,
            supported=supported_ops,
            parent_resource="groups",
            overrides={"set_like_lists": set_like_lists},
        )
        # Real Ansible's exit_json raises SystemExit; mimic that so control
        # flow stops on the first exit_json call, matching production.
        sub.module.exit_json.side_effect = SystemExit
        return sub

    def test_s3_policies_ids_order_difference_is_not_a_change(self):
        """API returns [14, 15]; user sends [15, 14] — must be idempotent."""
        sub = self._sub_with_current(
            current={"gid": 1, "tenant_id": 53, "s3_policies_ids": [14, 15]},
            supported_ops=frozenset({CrudCapability.READ, CrudCapability.UPDATE}),
            set_like_lists={"s3_policies_ids"},
        )
        sub.params = {
            "gid": 1,
            "tenant_id": 53,
            "s3_policies_ids": [15, 14],
            "state": "present",
        }
        try:
            sub._run_read_update("present")
        except SystemExit:
            pass
        kwargs = sub.module.exit_json.call_args.kwargs
        assert kwargs["changed"] is False, "Set-like list reorder must be idempotent"

    def test_order_difference_is_a_change_when_not_set_like(self):
        """Sanity check: without set_like_lists, order still matters."""
        sub = self._sub_with_current(
            current={"gid": 1, "tenant_id": 53, "s3_policies_ids": [14, 15]},
            supported_ops=frozenset({CrudCapability.READ, CrudCapability.UPDATE}),
            set_like_lists=set(),
        )
        sub.params = {
            "gid": 1,
            "tenant_id": 53,
            "s3_policies_ids": [15, 14],
            "state": "present",
        }
        try:
            sub._run_read_update("present")
        except SystemExit:
            pass
        kwargs = sub.module.exit_json.call_args.kwargs
        assert kwargs["changed"] is True

    def test_real_change_still_detected_with_set_like(self):
        """Adding a new id must still register as a change."""
        sub = self._sub_with_current(
            current={"gid": 1, "tenant_id": 53, "s3_policies_ids": [14, 15]},
            supported_ops=frozenset({CrudCapability.READ, CrudCapability.UPDATE}),
            set_like_lists={"s3_policies_ids"},
        )
        sub.params = {
            "gid": 1,
            "tenant_id": 53,
            "s3_policies_ids": [14, 15, 16],
            "state": "present",
        }
        sub.check_mode = True  # avoid needing update() plumbing
        try:
            sub._run_read_update("present")
        except SystemExit:
            pass
        kwargs = sub.module.exit_json.call_args.kwargs
        assert kwargs["changed"] is True

    def test_write_body_builder_receives_existing_state_without_second_get(self):
        """Partial writes are shaped from the state already read for comparison."""
        builder = MagicMock(return_value={"required": "echoed", "eth_mtu": 9216})
        sub = _make_sub(
            returns_list=False,
            results=[{"eth_mtu": 9000}],
            supported=frozenset({CrudCapability.READ, CrudCapability.UPDATE}),
            overrides={"write_body_builder": builder},
        )
        sub.params = {"eth_mtu": 9216, "state": "present"}
        sub.module.exit_json.side_effect = SystemExit
        sub.update = MagicMock(return_value={"eth_mtu": 9216})

        try:
            sub._run_read_update("present")
        except SystemExit:
            pass

        builder.assert_called_once_with({"eth_mtu": 9000}, {"eth_mtu": 9216})
        sub.update.assert_called_once_with({"required": "echoed", "eth_mtu": 9216})
        assert sub._fake.first_calls == [{}]


class TestActionWriteShaping:
    """Action endpoints may preview a write body built from a sibling resource."""

    def test_check_mode_preview_is_non_mutating_and_reports_unchanged(self):
        builder = MagicMock(return_value={"required": "echoed", "ntp": ["2.2.2.2"]})
        sub = _make_action_sub(file_params=set(), params={"ntp": ["2.2.2.2"]}, check_mode=True)
        sub.overrides = {
            "write_body_builder": builder,
            "write_source_sub_path": "network_settings",
            "action_reports_changed": False,
        }
        sub._read_write_source = MagicMock(return_value={"ntp": ["1.1.1.1"]})

        try:
            sub._run_action()
        except SystemExit:
            pass

        builder.assert_called_once_with({"ntp": ["1.1.1.1"]}, {"ntp": ["2.2.2.2"]})
        kwargs = sub.module.exit_json.call_args.kwargs
        assert kwargs == {
            "changed": False,
            "result": {"required": "echoed", "ntp": ["2.2.2.2"]},
        }

    def test_write_source_read_failure_reports_via_fail_json(self):
        """A failing sibling GET surfaces as fail_json, not a traceback."""
        sub = _make_action_sub(file_params=set(), params={"ntp": ["2.2.2.2"]})
        sub.overrides = {
            "write_body_builder": MagicMock(),
            "write_source_sub_path": "network_settings",
        }
        sub._read_write_source = MagicMock(side_effect=VastAPIError("Failed to read network_settings: boom"))

        try:
            sub._run_action()
        except SystemExit:
            pass

        sub.module.fail_json.assert_called_once_with(msg="Failed to read network_settings: boom")
        sub.module.exit_json.assert_not_called()


class TestBuildPayloadNullableBodyFields:
    """nullable_body_fields keep None in write payloads; search still drops None."""

    def _payload_sub(self, nullable=None):
        class _S(SubEndpointResource):
            parent_resource = "vms"
            sub_path = "set_ssl_ciphers"
            path_has_id = True
            supported_operations = frozenset({CrudCapability.UPDATE})
            parent_id_param = "vms_id"
            nullable_body_fields = set(nullable or ())

        sub = _S.__new__(_S)
        sub.module = MagicMock()
        sub.params = {
            "vms": {"host": "h"},
            "vms_id": 1,
            "ssl_ciphers": None,
            "other": None,
        }
        sub._exclude = set(SubEndpointResource._EXCLUDE_KEYS) | {"vms_id"}
        return sub

    def test_nullable_none_kept_in_payload(self):
        sub = self._payload_sub(nullable={"ssl_ciphers"})
        assert sub._build_payload() == {"ssl_ciphers": None}

    def test_non_nullable_none_still_omitted(self):
        sub = self._payload_sub(nullable=set())
        assert sub._build_payload() == {}

    def test_search_params_drop_nullable_none(self):
        sub = self._payload_sub(nullable={"ssl_ciphers"})
        assert sub._build_search_params() == {}

    def test_non_null_value_still_included(self):
        sub = self._payload_sub(nullable={"ssl_ciphers"})
        sub.params["ssl_ciphers"] = "AES256+EECDH"
        assert sub._build_payload() == {"ssl_ciphers": "AES256+EECDH"}
