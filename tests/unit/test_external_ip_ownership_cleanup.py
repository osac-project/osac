from __future__ import annotations

import subprocess
from unittest.mock import Mock

import pytest

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.networking.bmaas import test_external_ip_ownership as ownership


@pytest.mark.parametrize("api_record_exists", [False, True])
@pytest.mark.parametrize("cr_name", ["", "external-ip-cr"])
def test_cleanup_waits_for_api_removal_with_or_without_cr(
    monkeypatch: pytest.MonkeyPatch, api_record_exists: bool, cr_name: str
) -> None:
    grpc = Mock(spec=GRPCClient)
    private_grpc = Mock(spec=GRPCClient)
    k8s = Mock(spec=K8sClient)
    events: list[str] = []
    api_record = {
        "object": {"id": "external-ip-id", "metadata": {"tenant": "tenant-a"}, "spec": {"pool": {"id": "pool-id"}}}
    }
    if api_record_exists:
        private_grpc.get_private_external_ip.return_value = api_record
    else:
        private_grpc.get_private_external_ip.side_effect = subprocess.CalledProcessError(
            1, ["grpcurl"], stderr="ERROR:\n  Code: NotFound\n"
        )
    grpc.delete_external_ip.side_effect = lambda **_: events.append("api-delete")
    if not cr_name:
        k8s.get_external_ip_name.return_value = ""
    else:
        k8s.get_json.return_value = {
            "metadata": {"uid": "external-ip-uid", "annotations": {"osac.openshift.io/tenant": "tenant-a"}}
        }
    monkeypatch.setattr(ownership, "wait_for_external_ip_grpc_removal", lambda **_: events.append("api-removal-wait"))
    monkeypatch.setattr(ownership, "wait_for_external_ip_deletion", lambda **_: events.append("cr-removal-wait"))

    ownership._cleanup_owned_external_ip(
        grpc=grpc,
        private_grpc=private_grpc,
        k8s=k8s,
        external_ip_id="external-ip-id",
        pool_id="pool-id",
        tenant="tenant-a",
        expected_uid="external-ip-uid",
        external_ip_cr_name=cr_name,
    )

    expected_events = ["api-delete"] if api_record_exists else []
    expected_events.append("api-removal-wait")
    if cr_name:
        expected_events.append("cr-removal-wait")
    assert events == expected_events


def test_task_retry_patches_status_as_the_operator_service_account(monkeypatch: pytest.MonkeyPatch) -> None:
    k8s = K8sClient(namespace="osac-e2e-ci", kubeconfig="hub.kubeconfig")
    job = {"task": "create_external_ip", "manager": "fabric", "attempt": 1, "jobID": "42", "state": "Succeeded"}
    monkeypatch.setattr(k8s, "get_json", lambda **_: {"status": {"jobs": [job]}})
    commands: list[tuple[str, ...]] = []

    def fake_run(*args: str, **_: object) -> str:
        commands.append(args)
        return "osac-operator-sa\n" if "pods" in args else ""

    monkeypatch.setattr(ownership, "run", fake_run)

    ownership._request_task_retry(k8s=k8s, external_ip_name="externalip-a", completed_job=job)

    lookup, patch = commands
    assert lookup[:5] == ("kubectl", "--kubeconfig", "hub.kubeconfig", "--as", "system:admin")
    assert "jsonpath={.items[0].spec.serviceAccountName}" in lookup
    assert patch[:5] == (
        "kubectl",
        "--kubeconfig",
        "hub.kubeconfig",
        "--as",
        "system:serviceaccount:osac-e2e-ci:osac-operator-sa",
    )
    assert "system:admin" not in patch
    assert "--subresource=status" in patch
