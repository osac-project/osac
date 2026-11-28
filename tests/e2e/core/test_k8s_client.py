from __future__ import annotations

import json

import pytest

from tests.e2e.core import k8s_client
from tests.e2e.core.k8s_client import K8sClient


@pytest.fixture(autouse=True)
def _default_connection_env(monkeypatch: pytest.MonkeyPatch) -> None:
    """Keep assertions independent of the caller's OSAC connection overrides."""
    monkeypatch.delenv("OSAC_IMPERSONATE_USER", raising=False)
    monkeypatch.delenv("OSAC_HUB_KUBECONFIG", raising=False)


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


def test_get_storage_class_for_tier_fallback_uses_target_tenant(monkeypatch: pytest.MonkeyPatch) -> None:
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
                            "labels": {"osac.openshift.io/storage-tier": "gold", "osac.openshift.io/tenant": "shared"},
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
                    {
                        "metadata": {
                            "name": "tenant-gold-sc",
                            "labels": {
                                "osac.openshift.io/storage-tier": " GOLD ",
                                "osac.openshift.io/tenant": "tenant1",
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
        hub_client.get_storage_class_for_tier(tier_name="gold", tenant_name="tenant1", storage_class_client=virt_client)
        == "tenant-gold-sc"
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


def test_impersonation_user_is_configurable(monkeypatch: pytest.MonkeyPatch) -> None:
    calls: list[tuple[str, ...]] = []

    def fake_run(*args: str, **_kwargs: object) -> str:
        calls.append(args)
        return json.dumps({"items": []})

    monkeypatch.setattr(k8s_client, "run", fake_run)
    monkeypatch.setenv("OSAC_IMPERSONATE_USER", "kubernetes-admin")

    K8sClient(namespace="osac").list_json(resource="nodes")

    assert calls == [("kubectl", "--as", "kubernetes-admin", "get", "nodes", "-o", "json")]


def test_empty_impersonation_user_drops_the_as_flag(monkeypatch: pytest.MonkeyPatch) -> None:
    calls: list[tuple[str, ...]] = []

    def fake_run(*args: str, **_kwargs: object) -> str:
        calls.append(args)
        return json.dumps({"items": []})

    monkeypatch.setattr(k8s_client, "run", fake_run)
    monkeypatch.setenv("OSAC_IMPERSONATE_USER", "")

    K8sClient(namespace="osac").list_json(resource="nodes")

    assert calls == [("kubectl", "get", "nodes", "-o", "json")]


def test_hub_kubeconfig_prefers_the_env_override(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("OSAC_HUB_KUBECONFIG", "/tmp/vcluster-kubeconfig")
    assert k8s_client.hub_kubeconfig() == "/tmp/vcluster-kubeconfig"

    monkeypatch.delenv("OSAC_HUB_KUBECONFIG")
    assert k8s_client.hub_kubeconfig() is None

    monkeypatch.setenv("OSAC_HUB_KUBECONFIG", "  ")
    assert k8s_client.hub_kubeconfig() is None


def test_service_account_token_args_follow_the_impersonation_setting(monkeypatch: pytest.MonkeyPatch) -> None:
    args = k8s_client.service_account_token_args(namespace="osac", service_account="admin", duration="4h")
    assert args == ["admin", "-n", "osac", "--duration", "4h", "--as", "system:admin"]

    monkeypatch.setenv("OSAC_IMPERSONATE_USER", "")
    args = k8s_client.service_account_token_args(namespace="osac", service_account="admin")
    assert args == ["admin", "-n", "osac"]
