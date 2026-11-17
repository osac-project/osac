#!/usr/bin/env python
# -*- coding: utf-8 -*-
"""Behavioral tests for vms_saml_config (requests-mock)."""

import sys
from pathlib import Path

import pytest

requests_mock = pytest.importorskip("requests_mock")

collection_root = Path(__file__).parent.parent.parent.parent.parent
collections_root = collection_root.parent.parent.parent
sys.path.insert(0, str(collection_root))
sys.path.insert(0, str(collections_root))

from ansible_collections.vastdata.vms.plugins.modules import vms_saml_config  # noqa: E402

BASE = "https://vms.test/api/latest"
JSON_CT = {"Content-Type": "application/json"}


class _Exit(Exception):
    def __init__(self, kwargs):
        self.kwargs = kwargs


class FakeModule:
    def __init__(self, params, check_mode=False):
        self.params = params
        self.check_mode = check_mode
        self.warnings = []

    def warn(self, msg):
        self.warnings.append(msg)

    def exit_json(self, **kwargs):
        raise _Exit(kwargs)

    def fail_json(self, **kwargs):
        raise pytest.fail(kwargs.get("msg", str(kwargs)))


def _params(**fields):
    base = {
        "vms": {"host": "vms.test", "token": "secret", "validate_certs": False},
        "vms_id": 1,
        "idp_name": "test-idp",
        "state": "present",
        "remove_signed_certs": False,
        "saml_settings": None,
    }
    base.update(fields)
    return base


def _run(params, check_mode=False):
    module = FakeModule(params, check_mode=check_mode)
    resource = vms_saml_config.VmsSamlConfig(module)
    with pytest.raises(_Exit) as exc:
        resource.run()
    return exc.value.kwargs


def _mock_version(requests_mock, version="5.4.0.20"):
    requests_mock.get(
        f"{BASE}/clusters/",
        json=[{"id": 1, "sw_version": version}],
        headers=JSON_CT,
    )


class TestVmsSamlConfigBehavior:
    def test_get_unconfigured_returns_empty_and_no_change(self, requests_mock):
        _mock_version(requests_mock)
        requests_mock.get(
            f"{BASE}/vms/1/saml_config/?idp_name=test-idp",
            status_code=400,
            text="no saml config for test-idp",
            headers=JSON_CT,
        )
        result = _run(_params())
        assert result["changed"] is False
        assert result["vms_saml_config"] == {}

    def test_post_create_is_idempotent(self, requests_mock):
        _mock_version(requests_mock)
        settings = {"idp_metadata_url": "https://idp/descriptor", "force_authn": False}
        current = {
            "sp_settings": {"force_authn": False},
            "metadata": {"remote": [{"url": "https://idp/descriptor"}]},
        }
        requests_mock.get(
            f"{BASE}/vms/1/saml_config/?idp_name=test-idp",
            [{"json": current, "headers": JSON_CT}, {"json": current, "headers": JSON_CT}],
        )
        result = _run(_params(saml_settings=settings))
        assert result["changed"] is False
        saml_requests = [r for r in requests_mock.request_history if "/saml_config/" in r.url]
        assert len(saml_requests) == 1
        assert saml_requests[0].method == "GET"

    def test_post_create_is_idempotent_when_metadata_url_not_projected(self, requests_mock):
        _mock_version(requests_mock)
        settings = {"idp_metadata_url": "https://idp/descriptor", "force_authn": False}
        current = {"sp_settings": {"force_authn": False}}
        requests_mock.get(
            f"{BASE}/vms/1/saml_config/?idp_name=test-idp",
            [{"json": current, "headers": JSON_CT}, {"json": current, "headers": JSON_CT}],
        )
        result = _run(_params(saml_settings=settings))
        assert result["changed"] is False
        assert not any(r.method == "POST" for r in requests_mock.request_history)

    def test_post_modify_detects_force_authn_when_omitted_from_get(self, requests_mock):
        _mock_version(requests_mock)
        settings = {"idp_metadata_url": "https://idp/descriptor", "force_authn": True}
        current = {
            "sp_settings": {},
            "metadata": {"remote": [{"url": "https://idp/descriptor"}]},
        }
        after = {"sp_settings": {"force_authn": True}, "metadata": {"remote": [{"url": "https://idp/descriptor"}]}}
        requests_mock.get(
            f"{BASE}/vms/1/saml_config/?idp_name=test-idp",
            [
                {"json": current, "headers": JSON_CT},
                {"json": after, "headers": JSON_CT},
            ],
        )
        requests_mock.post(
            f"{BASE}/vms/1/saml_config/?idp_name=test-idp",
            json=after,
            headers=JSON_CT,
        )
        result = _run(_params(saml_settings=settings))
        assert result["changed"] is True
        assert any(r.method == "POST" for r in requests_mock.request_history)

    def test_remove_signed_certs_idempotent_when_already_removed(self, requests_mock):
        _mock_version(requests_mock)
        current = {"sp_settings": {"want_assertions_or_response_signed": False}}
        requests_mock.get(
            f"{BASE}/vms/1/saml_config/?idp_name=test-idp",
            json=current,
            headers=JSON_CT,
        )
        result = _run(_params(remove_signed_certs=True))
        assert result["changed"] is False
        assert not any(r.method == "PATCH" for r in requests_mock.request_history)

    def test_remove_signed_certs_idempotent_when_sp_settings_missing(self, requests_mock):
        _mock_version(requests_mock)
        current = {"idp": {"entityid": "https://idp.example.com"}}
        requests_mock.get(
            f"{BASE}/vms/1/saml_config/?idp_name=test-idp",
            json=current,
            headers=JSON_CT,
        )
        result = _run(_params(remove_signed_certs=True))
        assert result["changed"] is False
        assert not any(r.method == "PATCH" for r in requests_mock.request_history)

    def test_remove_signed_certs_issues_patch_when_needed(self, requests_mock):
        _mock_version(requests_mock)
        current = {"sp_settings": {"want_assertions_or_response_signed": True}}
        after = {"sp_settings": {"want_assertions_or_response_signed": False}}
        requests_mock.get(
            f"{BASE}/vms/1/saml_config/?idp_name=test-idp",
            [
                {"json": current, "headers": JSON_CT},
                {"json": after, "headers": JSON_CT},
            ],
        )
        requests_mock.patch(
            f"{BASE}/vms/1/saml_config/?idp_name=test-idp",
            json=after,
            headers=JSON_CT,
        )
        result = _run(_params(remove_signed_certs=True))
        assert result["changed"] is True
        patch_calls = [r for r in requests_mock.request_history if r.method == "PATCH"]
        assert len(patch_calls) == 1
