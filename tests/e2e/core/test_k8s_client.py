from __future__ import annotations

import json

import pytest

from tests.e2e.core import k8s_client
from tests.e2e.core.k8s_client import K8sClient


def test_get_json_supports_an_explicit_namespace(monkeypatch: pytest.MonkeyPatch) -> None:
    calls: list[tuple[str, ...]] = []

    def fake_run(*args: str, **_kwargs: object) -> str:
        calls.append(args)
        return json.dumps({"metadata": {"name": "order"}})

    monkeypatch.setattr(k8s_client, "run", fake_run)

    result = K8sClient(namespace="osac", kubeconfig="/tmp/hub-kubeconfig").get_json(
        resource="hostedcluster", name="order", namespace="osac-order"
    )

    assert result["metadata"]["name"] == "order"
    assert calls == [
        (
            "kubectl",
            "--kubeconfig",
            "/tmp/hub-kubeconfig",
            "--as",
            "system:admin",
            "get",
            "hostedcluster",
            "order",
            "-n",
            "osac-order",
            "-o",
            "json",
        )
    ]


def test_list_json_omits_namespace_for_cluster_scoped_resources(monkeypatch: pytest.MonkeyPatch) -> None:
    calls: list[tuple[str, ...]] = []

    def fake_run(*args: str, **_kwargs: object) -> str:
        calls.append(args)
        return json.dumps({"items": []})

    monkeypatch.setattr(k8s_client, "run", fake_run)

    result = K8sClient(namespace="osac").list_json(resource="nodes")

    assert result == {"items": []}
    assert calls == [("kubectl", "--as", "system:admin", "get", "nodes", "-o", "json")]


def test_list_json_can_use_the_credentials_from_the_kubeconfig(monkeypatch: pytest.MonkeyPatch) -> None:
    calls: list[tuple[str, ...]] = []

    def fake_run(*args: str, **_kwargs: object) -> str:
        calls.append(args)
        return json.dumps({"items": []})

    monkeypatch.setattr(k8s_client, "run", fake_run)

    K8sClient(namespace="osac", kubeconfig="/tmp/workload-kubeconfig", as_system_admin=False).list_json(
        resource="nodes"
    )

    assert calls == [("kubectl", "--kubeconfig", "/tmp/workload-kubeconfig", "get", "nodes", "-o", "json")]
