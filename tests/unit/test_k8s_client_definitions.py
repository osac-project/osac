from __future__ import annotations

import ast
import inspect
import json
import textwrap

import pytest

from tests.e2e.core.k8s_client import K8sClient


def test_cluster_order_status_has_single_implementation() -> None:
    class_definition = ast.parse(textwrap.dedent(inspect.getsource(K8sClient))).body[0]
    implementations = [
        node
        for node in class_definition.body
        if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)) and node.name == "get_cluster_order_status"
    ]
    assert len(implementations) == 1, "Duplicate methods silently override the earlier implementation"


def test_get_tenant_condition_returns_full_matching_condition(monkeypatch: pytest.MonkeyPatch) -> None:
    k8s = K8sClient(namespace="osac")
    condition = {
        "type": "DefaultNetworkingReady",
        "status": "True",
        "reason": "AllResourcesReady",
        "message": "All default networking resources are ready",
    }
    monkeypatch.setattr(
        k8s, "_get", lambda *_args, checked=True: (json.dumps({"status": {"conditions": [condition]}}), 0)
    )

    assert k8s.get_tenant_condition(name="tenant1", condition_type="DefaultNetworkingReady") == condition
    assert k8s.get_tenant_condition_status(name="tenant1", condition_type="DefaultNetworkingReady") == "True"


def test_get_tenant_condition_returns_empty_when_missing_or_unavailable(monkeypatch: pytest.MonkeyPatch) -> None:
    k8s = K8sClient(namespace="osac")
    monkeypatch.setattr(k8s, "_get", lambda *_args, checked=True: ("{}", 0))
    assert k8s.get_tenant_condition(name="tenant1", condition_type="DefaultNetworkingReady") == {}

    monkeypatch.setattr(k8s, "_get", lambda *_args, checked=True: ("", 1))
    assert k8s.get_tenant_condition(name="tenant1", condition_type="DefaultNetworkingReady", checked=False) == {}
