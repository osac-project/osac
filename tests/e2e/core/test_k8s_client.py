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


def test_get_storage_class_for_tier_and_datavolume_support_tenant_and_vm_namespace(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    calls: list[tuple[str, ...]] = []

    def fake_run(*args: str, **_kwargs: object) -> str:
        calls.append(args)
        if "tenant" in args:
            return json.dumps({"status": {"storageClasses": [{"name": "netapp-gold-sc", "tier": "gold"}]}})
        return json.dumps({"spec": {"pvc": {"storageClassName": "netapp-gold-sc"}}})

    monkeypatch.setattr(k8s_client, "run", fake_run)

    client = K8sClient(namespace="osac-devel")
    assert client.get_storage_class_for_tier(tier_name=" GOLD ", tenant_name=" tenant1 ") == "netapp-gold-sc"
    assert client.get_datavolume_storage_class(name="vm-1-root-disk", namespace="subnet-ns") == "netapp-gold-sc"
    assert ("kubectl", "--as", "system:admin", "get", "tenant", "tenant1", "-n", "osac-devel", "-o", "json") in calls
    assert (
        "kubectl",
        "--as",
        "system:admin",
        "get",
        "datavolume",
        "vm-1-root-disk",
        "-n",
        "subnet-ns",
        "-o",
        "json",
    ) in calls


def test_get_storage_class_for_tier_fallback_prefers_csp_and_tenant_over_shared(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    calls: list[tuple[str, ...]] = []

    def fake_run(*args: str, **_kwargs: object) -> str:
        calls.append(args)
        if "tenant" in args:
            return json.dumps({"status": {"storageClasses": []}})
        return json.dumps(
            {
                "items": [
                    {
                        "metadata": {
                            "name": "shared-gold-sc",
                            "labels": {
                                "osac.openshift.io/storage-tier": "gold",
                                "osac.openshift.io/tenant": "shared",
                            },
                        }
                    },
                    {
                        "metadata": {
                            "name": "csp-gold-sc",
                            "labels": {
                                "osac.openshift.io/storage-tier": " GOLD ",
                                "osac.openshift.io/tenant": "csp-managed",
                            },
                        }
                    },
                ]
            }
        )

    monkeypatch.setattr(k8s_client, "run", fake_run)

    hub_client = K8sClient(namespace="osac-devel", kubeconfig="/tmp/hub-kubeconfig")
    virt_client = K8sClient(namespace="osac-devel", kubeconfig="/tmp/virt-kubeconfig")
    assert (
        hub_client.get_storage_class_for_tier(
            tier_name="gold", tenant_name="tenant1", storage_class_client=virt_client
        )
        == "csp-gold-sc"
    )
    assert (
        "kubectl",
        "--kubeconfig",
        "/tmp/hub-kubeconfig",
        "--as",
        "system:admin",
        "get",
        "tenant",
        "tenant1",
        "-n",
        "osac-devel",
        "-o",
        "json",
    ) in calls
    assert (
        "kubectl",
        "--kubeconfig",
        "/tmp/virt-kubeconfig",
        "--as",
        "system:admin",
        "get",
        "storageclass",
        "-o",
        "json",
    ) in calls
