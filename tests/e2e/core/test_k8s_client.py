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


def _invoke_generic_operation(client: K8sClient, operation: str, namespace_override: str | None) -> None:
    namespace_args = {} if namespace_override is None else {"namespace": namespace_override}
    if operation == "get_json":
        client.get_json(resource="subnet", name="subnet-one", **namespace_args)
    elif operation == "list_json":
        client.list_json(resource="subnets", **namespace_args)
    elif operation == "delete":
        client.delete(resource="subnet", name="subnet-one", **namespace_args)
    elif operation == "is_present":
        client.is_present(resource="subnet", name="subnet-one", **namespace_args)
    else:
        raise AssertionError(f"unsupported operation: {operation}")


@pytest.mark.parametrize(
    ("operation", "namespace_override", "expected_namespace"),
    [
        ("get_json", None, "tenant-a"),
        ("get_json", "", None),
        ("get_json", "subnet-one", "subnet-one"),
        ("list_json", None, None),
        ("list_json", "", None),
        ("list_json", "subnet-one", "subnet-one"),
        ("delete", None, "tenant-a"),
        ("delete", "", None),
        ("delete", "subnet-one", "subnet-one"),
        ("is_present", None, "tenant-a"),
        ("is_present", "", None),
        ("is_present", "subnet-one", "subnet-one"),
    ],
    ids=[
        "get-json-default-namespace",
        "get-json-cluster-scoped",
        "get-json-namespace-override",
        "list-json-cluster-scoped-default",
        "list-json-cluster-scoped-empty",
        "list-json-namespace-override",
        "delete-default-namespace",
        "delete-cluster-scoped",
        "delete-namespace-override",
        "is-present-default-namespace",
        "is-present-cluster-scoped",
        "is-present-namespace-override",
    ],
)
def test_generic_operations_select_expected_namespace(
    monkeypatch: pytest.MonkeyPatch, operation: str, namespace_override: str | None, expected_namespace: str | None
) -> None:
    calls: list[tuple[str, ...]] = []

    def fake_run(*args: str, timeout: int = 300, **_kwargs: object) -> str:
        calls.append(args)
        return '{"items": []}'

    def fake_run_unchecked(*args: str, timeout: int = 300, **_kwargs: object) -> tuple[str, int]:
        calls.append(args)
        return "", 0

    monkeypatch.setattr(k8s_client, "run", fake_run)
    monkeypatch.setattr(k8s_client, "run_unchecked", fake_run_unchecked)

    client = K8sClient(namespace="tenant-a")
    _invoke_generic_operation(client, operation, namespace_override)

    assert len(calls) == 1
    command = calls[0]
    if expected_namespace is None:
        assert "-n" not in command
    else:
        namespace_index = command.index("-n")
        assert command[namespace_index + 1] == expected_namespace
