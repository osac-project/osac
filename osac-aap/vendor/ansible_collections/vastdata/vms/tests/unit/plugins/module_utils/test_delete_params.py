#!/usr/bin/env python
# -*- coding: utf-8 -*-
"""Unit tests for DELETE parameter routing through client and BaseResource."""

import sys
from pathlib import Path
from unittest.mock import MagicMock

# Add collection root to path for importing module_utils
collection_root = Path(__file__).parent.parent.parent.parent.parent
sys.path.insert(0, str(collection_root))

from plugins.module_utils.vast.client import (
    VastClient,
    VastConnection,
    _APIPath,
)
from plugins.module_utils.vast.resource import BaseResource


class _FakeClient:
    """Stand-in for VastClient that records ``_request`` calls."""

    def __init__(self):
        self.calls = []

    def _request(self, method, segments, *, params=None, data=None):
        self.calls.append({"method": method, "segments": segments, "params": params, "data": data})
        return {"ok": True}


def _make_resource(delete_query=None, delete_body=None, params=None):
    """Build a BaseResource instance bypassing __init__ (no live VMS)."""

    class _R(BaseResource):
        resource_name = "things"
        singular = "thing"
        delete_query_params = set(delete_query or [])
        delete_body_params = set(delete_body or [])

    res = _R.__new__(_R)
    res.module = MagicMock()
    res.params = params or {}
    res.check_mode = False
    res.client = MagicMock()
    res.overrides = {}
    return res


def _make_client():
    """Build a VastClient with a stubbed requests.Session."""
    client = VastClient.__new__(VastClient)
    client._debug_traces = []
    client.debug = False
    client._connection = VastConnection(host="example.com")
    client._base_url = "https://example.com/api"
    client._version = "latest"
    client._timeout = None

    resp = MagicMock()
    resp.status_code = 204
    resp.content = b""
    resp.headers = {}
    resp.text = ""
    resp.raise_for_status = MagicMock()

    session = MagicMock()
    session.request = MagicMock(return_value=resp)
    client._session = session
    return client, session


class TestAPIPathDelete:
    """Test _APIPath.delete keyword routing."""

    def test_routes_query_and_body(self):
        """Verify _query_params goes to params and other kwargs go to data."""
        client = _FakeClient()
        _APIPath(client, ("views", "42")).delete(_query_params={"force": True}, remove_dir=True)
        call = client.calls[0]
        assert call["method"] == "DELETE"
        assert call["segments"] == ("views", "42")
        assert call["params"] == {"force": True}
        assert call["data"] == {"remove_dir": True}

    def test_no_args_sends_none(self):
        """Verify delete with no args sends None for both params and data."""
        client = _FakeClient()
        _APIPath(client, ("v",)).delete()
        assert client.calls[0]["params"] is None
        assert client.calls[0]["data"] is None

    def test_only_body(self):
        """Verify body-only delete leaves params empty."""
        client = _FakeClient()
        _APIPath(client, ("v",)).delete(reason="x")
        assert client.calls[0]["params"] is None
        assert client.calls[0]["data"] == {"reason": "x"}

    def test_only_query(self):
        """Verify query-only delete leaves data empty."""
        client = _FakeClient()
        _APIPath(client, ("v",)).delete(_query_params={"force": True})
        assert client.calls[0]["params"] == {"force": True}
        assert client.calls[0]["data"] is None


class TestRequestQueryOnNonGet:
    """Test VastClient._request query-string branch on non-GET verbs."""

    def test_delete_sends_query_and_body(self):
        """Verify DELETE sends both query params and JSON body."""
        client, session = _make_client()
        client._request(
            "DELETE",
            ("views", "42"),
            params={"force": True, "tags": ["a", "b"]},
            data={"reason": "x"},
        )
        args, kwargs = session.request.call_args
        assert args == ("DELETE", "https://example.com/api/latest/views/42/")
        assert kwargs["params"] == [("force", True), ("tags", "a"), ("tags", "b")]
        assert kwargs["data"] == '{"reason": "x"}'

    def test_delete_with_only_query(self):
        """Verify DELETE without body omits the data kwarg."""
        client, session = _make_client()
        client._request("DELETE", ("v", "1"), params={"force": True}, data=None)
        kwargs = session.request.call_args.kwargs
        assert kwargs["params"] == [("force", True)]
        assert "data" not in kwargs

    def test_get_uses_query_branch_only(self):
        """Verify GET only sends params and ignores data."""
        client, session = _make_client()
        client._request("GET", ("v",), params={"q": "x"}, data={"ignored": True})
        kwargs = session.request.call_args.kwargs
        assert kwargs["params"] == [("q", "x")]
        assert "data" not in kwargs


class TestBaseResourceDelete:
    """Test BaseResource.delete query/body routing to _APIPath."""

    def test_passes_query_and_body(self):
        """Verify query_params and body_params reach api[id].delete."""
        res = _make_resource()
        delete_mock = MagicMock(return_value={})
        res.client.api.__getitem__.return_value.__getitem__.return_value.delete = delete_mock

        res.delete(42, query_params={"force": True}, body_params={"reason": "x"})

        delete_mock.assert_called_once_with(_query_params={"force": True}, reason="x")

    def test_no_extras(self):
        """Verify delete without extras calls api[id].delete()."""
        res = _make_resource()
        delete_mock = MagicMock(return_value=None)
        res.client.api.__getitem__.return_value.__getitem__.return_value.delete = delete_mock

        result = res.delete(42)
        delete_mock.assert_called_once_with()
        assert result == {}

    def test_only_body(self):
        """Verify body-only delete spreads body into kwargs."""
        res = _make_resource()
        delete_mock = MagicMock(return_value={})
        res.client.api.__getitem__.return_value.__getitem__.return_value.delete = delete_mock

        res.delete(7, body_params={"reason": "x"})
        delete_mock.assert_called_once_with(reason="x")


class TestCollectDeleteParams:
    """Test BaseResource._collect_delete_params."""

    def test_splits_query_and_body(self):
        """Verify params split per delete_query_params and delete_body_params."""
        res = _make_resource(
            delete_query=["force"],
            delete_body=["reason"],
            params={"force": True, "reason": "x", "name": "foo"},
        )
        assert res._collect_delete_params() == {"query": {"force": True}, "body": {"reason": "x"}}

    def test_skips_none_values(self):
        """Verify None values are excluded."""
        res = _make_resource(
            delete_query=["force"],
            delete_body=["reason"],
            params={"force": None, "reason": None},
        )
        assert res._collect_delete_params() == {"query": {}, "body": {}}

    def test_missing_keys(self):
        """Verify missing keys produce empty dicts."""
        res = _make_resource(delete_query=["force"], delete_body=["reason"], params={})
        assert res._collect_delete_params() == {"query": {}, "body": {}}

    def test_falsy_but_set_values_included(self):
        """Verify False and 0 are kept (only None is filtered)."""
        res = _make_resource(
            delete_query=["force"],
            delete_body=["count"],
            params={"force": False, "count": 0},
        )
        assert res._collect_delete_params() == {"query": {"force": False}, "body": {"count": 0}}


class TestBuildDesiredStateExclusion:
    """Test BaseResource.build_desired_state excludes DELETE-only and framework fields."""

    def test_excludes_delete_query_and_body_params(self):
        """Verify DELETE-only fields never appear in create/update payload."""
        res = _make_resource(
            delete_query=["force"],
            delete_body=["reason"],
            params={
                "name": "foo",
                "path": "/x",
                "force": True,
                "reason": "because",
                "state": "present",
                "vms": {"host": "h"},
                "wait": True,
            },
        )
        assert res.build_desired_state(operation="update") == {"name": "foo", "path": "/x"}

    def test_excludes_framework_fields(self):
        """Verify state/wait/id/query/wait_timeout are excluded."""
        res = _make_resource(params={"name": "foo", "id": 1, "state": "absent", "wait_timeout": 5, "query": "x"})
        assert res.build_desired_state() == {"name": "foo"}

    def test_skips_none_values(self):
        """Verify None values are excluded from desired state."""
        res = _make_resource(params={"name": "foo", "path": None})
        assert res.build_desired_state() == {"name": "foo"}
