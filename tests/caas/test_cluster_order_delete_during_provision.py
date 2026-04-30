from __future__ import annotations

import time
from pathlib import Path

import pytest

from tests.fulfillment_cli import FulfillmentCLI
from tests.grpc_client import GRPCClient
from tests.helpers import wait_for_cluster_deletion, wait_for_cluster_order_cr
from tests.k8s_client import K8sClient
from tests.runner import poll_until

TERMINAL_JOB_STATES: tuple[str, ...] = ("Canceled", "Failed", "Succeeded")


def _wait_for_provision_job(k8s: K8sClient, *, name: str) -> None:
    poll_until(
        fn=lambda: k8s.get_cluster_order_latest_job_id(name=name, job_type="provision", checked=False),
        until=lambda v: v != "",
        retries=30,
        delay=2,
        description=f"provision job for ClusterOrder {name}",
    )


def _wait_for_provision_termination(k8s: K8sClient, *, name: str) -> None:
    poll_until(
        fn=lambda: k8s.get_cluster_order_latest_job_state(name=name, job_type="provision", checked=False),
        until=lambda v: v in (*TERMINAL_JOB_STATES, ""),
        retries=60,
        delay=1,
        description=f"provision termination for ClusterOrder {name}",
    )


def _get_deprovision_status(k8s: K8sClient, *, name: str) -> str:
    if not k8s.is_present(resource="clusterorder", name=name):
        return "cr_deleted"
    return k8s.get_cluster_order_latest_job_state(name=name, job_type="deprovision", checked=False)


def _wait_for_deprovision_completion(k8s: K8sClient, *, name: str) -> str:
    result: str = poll_until(
        fn=lambda: _get_deprovision_status(k8s, name=name),
        until=lambda v: v in (*TERMINAL_JOB_STATES, "cr_deleted"),
        retries=120,
        delay=1,
        description=f"deprovision completion for ClusterOrder {name}",
    )
    assert result != "Failed", f"Deprovision job failed for ClusterOrder {name}"
    return result


def _verify_no_duplicate_deprovision(k8s: K8sClient, *, name: str) -> None:
    if not k8s.is_present(resource="clusterorder", name=name):
        return
    first_id: str = k8s.get_cluster_order_latest_job_id(name=name, job_type="deprovision", checked=False)
    assert first_id != "", f"Expected deprovision job ID for ClusterOrder {name}"
    time.sleep(10)
    if not k8s.is_present(resource="clusterorder", name=name):
        return
    second_id: str = k8s.get_cluster_order_latest_job_id(name=name, job_type="deprovision", checked=False)
    assert first_id == second_id, f"Deprovision job ID changed: {first_id} -> {second_id}"


@pytest.fixture
def cluster_order(
    cli: FulfillmentCLI, k8s_hub_client: K8sClient, cluster_template: str, pull_secret_path: str, ssh_public_key_path: str
):
    uuid: str = cli.create_cluster(
        template=cluster_template,
        template_parameter_files={"pull_secret": pull_secret_path},
        template_parameters={"ssh_public_key": Path(ssh_public_key_path).read_text().strip()},
    )
    co_name: str = wait_for_cluster_order_cr(k8s=k8s_hub_client, uuid=uuid)
    yield uuid, co_name
    if k8s_hub_client.is_present(resource="clusterorder", name=co_name):
        cli.delete_cluster(uuid=uuid)
        wait_for_cluster_deletion(k8s=k8s_hub_client, name=co_name)


def test_cluster_order_delete_during_provision(
    cluster_order: tuple[str, str], grpc: GRPCClient, k8s_hub_client: K8sClient, cli: FulfillmentCLI
) -> None:
    uuid, co_name = cluster_order

    _wait_for_provision_job(k8s_hub_client, name=co_name)

    prov_state: str = k8s_hub_client.get_cluster_order_latest_job_state(
        name=co_name, job_type="provision", checked=False
    )
    deprov_job_id: str = k8s_hub_client.get_cluster_order_latest_job_id(
        name=co_name, job_type="deprovision", checked=False
    )
    assert prov_state in ("Running", "Pending", "Unknown"), f"Expected provision in progress, got {prov_state}"
    assert deprov_job_id == "", "No deprovision job should exist before deletion"

    cli.delete_cluster(uuid=uuid)

    _wait_for_provision_termination(k8s_hub_client, name=co_name)
    _wait_for_deprovision_completion(k8s_hub_client, name=co_name)
    _verify_no_duplicate_deprovision(k8s_hub_client, name=co_name)

    wait_for_cluster_deletion(k8s=k8s_hub_client, name=co_name)
    assert uuid not in grpc.list_cluster_ids()

    orphan_ns: int = k8s_hub_client.count_by_label_all_namespaces(
        resource="namespace", label=f"osac.openshift.io/clusterorder={co_name}"
    )
    assert orphan_ns == 0, f"Found {orphan_ns} orphaned namespaces for {co_name}"
