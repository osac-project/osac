from __future__ import annotations

from contextlib import nullcontext
from unittest.mock import Mock

import pytest

from tests.e2e.core import runner
from tests.e2e.core.wait_kinds import WaitResourceKind
from tests.e2e.enablement import test_netris_ca_lifecycle as lifecycle
from tests.e2e.regression import test_tenant_service_accounts as accounts


@pytest.mark.parametrize(
    ("helper", "probe", "kind"),
    [
        ("_wait_for_project_absent", "_project_is_absent", "PROJECT"),
        ("_wait_for_role_binding_absent", "_role_binding_is_absent", "ROLE_BINDING"),
        ("_wait_for_secret_absent", "_secret_is_absent", "SECRET"),
        ("_wait_for_account_absent", "_service_account_is_absent", "SERVICE_ACCOUNT"),
        ("_wait_for_account_deletion_started", "_account_deletion_started", "SERVICE_ACCOUNT"),
    ],
)
def test_account_waits_execute_with_resource_identity(
    monkeypatch: pytest.MonkeyPatch, helper: str, probe: str, kind: str
) -> None:
    span = Mock(return_value=nullcontext(Mock()))
    monkeypatch.setattr(runner, "_wait_span", span)
    monkeypatch.setattr(accounts, probe, Mock(return_value=True))

    getattr(accounts, helper)(Mock(), "resource-id")

    assert span.call_args.kwargs["resource_kind"] is getattr(WaitResourceKind, kind)
    assert span.call_args.kwargs["resource_id"] == "resource-id"


def test_keycloak_cleanup_wait_executes_with_client_identity(monkeypatch: pytest.MonkeyPatch) -> None:
    span = Mock(return_value=nullcontext(Mock()))
    monkeypatch.setattr(runner, "_wait_span", span)
    monkeypatch.setattr(accounts, "_keycloak_admin_token", Mock(return_value="unused"))
    monkeypatch.setattr(accounts, "_keycloak_client_exists", Mock(return_value=False))

    accounts._wait_for_keycloak_client_absent(
        keycloak_url="https://keycloak.invalid", keycloak_admin_password="unused", client_id="client-id"
    )

    assert span.call_args.kwargs["resource_kind"] is WaitResourceKind.KEYCLOAK_CLIENT
    assert span.call_args.kwargs["resource_id"] == "client-id"


def test_ca_lifecycle_waits_execute_with_resource_identity(monkeypatch: pytest.MonkeyPatch) -> None:
    span = Mock(side_effect=lambda *_args, **_kwargs: nullcontext(Mock()))
    monkeypatch.setattr(runner, "_wait_span", span)
    monkeypatch.setattr(lifecycle, "_latest_cluster_order_job_id", Mock(return_value="job-id"))
    pod = {"metadata": {"name": "pod-name"}}
    monkeypatch.setattr(lifecycle, "_job_pod_mounting", Mock(return_value=pod))
    bundle = "test-bundle"
    monkeypatch.setattr(lifecycle, "_read_job_bundle", Mock(return_value=("true", lifecycle._bundle_sha256(bundle))))
    k8s = Mock()

    assert lifecycle._wait_for_cluster_order_job_id(k8s, "order-name") == "job-id"
    assert lifecycle._wait_for_job_pod(k8s, "configmap-name", "job-id") == pod
    lifecycle._wait_for_bundle_in_job_pod(k8s, "pod-name", bundle)

    assert [(call.kwargs["resource_kind"], call.kwargs["resource_id"]) for call in span.call_args_list] == [
        (WaitResourceKind.CLUSTER_ORDER, "order-name"),
        (WaitResourceKind.AAP_JOB, "job-id"),
        (WaitResourceKind.POD, "pod-name"),
    ]
