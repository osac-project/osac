from __future__ import annotations

import logging
from typing import Any

import pytest

from tests.core.grpc_client import GRPCClient
from tests.core.helpers import wait_for_bmi_cr, wait_for_bmi_deletion, wait_for_bmi_grpc_removal, wait_for_bmi_running
from tests.core.k8s_client import K8sClient
from tests.core.osac_cli import OsacCLI
from tests.core.runner import poll_until

logger = logging.getLogger(__name__)

_RESTART_IN_PROGRESS: str = "BARE_METAL_INSTANCE_CONDITION_TYPE_RESTART_IN_PROGRESS"
_RESTART_FAILED: str = "BARE_METAL_INSTANCE_CONDITION_TYPE_RESTART_FAILED"


def _get_condition_status(grpc: GRPCClient, bmi_id: str, condition_type: str) -> str:
    response: dict[str, Any] = grpc.get_baremetal_instance(bmi_id=bmi_id)
    for condition in response.get("object", {}).get("status", {}).get("conditions", []):
        if condition.get("type") == condition_type:
            return condition.get("status", "")
    return ""


def _get_status_restart_trigger(grpc: GRPCClient, bmi_id: str) -> int:
    response: dict[str, Any] = grpc.get_baremetal_instance(bmi_id=bmi_id)
    return int(response.get("object", {}).get("status", {}).get("restartTrigger", "0"))


@pytest.mark.xdist_group("bmaas")
def test_baremetal_instance_restart(
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    catalog_item: str,
    bmh_namespace: str,
    test_run_id: str,
    ssh_public_key: str,
) -> None:
    name: str = f"e2e-bmi-restart-{test_run_id}"
    bmi_id: str = cli.create_baremetal_instance(name=name, catalog_item=catalog_item, ssh_key=ssh_public_key)

    try:
        assert bmi_id in grpc.list_baremetal_instance_ids()

        bmi_cr_name: str = wait_for_bmi_cr(k8s=k8s_hub_client, uuid=bmi_id)
        wait_for_bmi_running(grpc=grpc, bmi_id=bmi_id)

        external_host_id: str = k8s_hub_client.get_baremetal_instance_external_host_id(name=bmi_cr_name)
        assert "/" in external_host_id, f"Expected namespace/name format, got: {external_host_id}"
        bmh_ns, bmh_name = external_host_id.split("/", 1)
        assert bmh_ns == bmh_namespace, f"BMH landed in {bmh_ns}, expected {bmh_namespace}"

        initial_trigger: int = _get_status_restart_trigger(grpc, bmi_id)
        new_trigger: int = initial_trigger + 1
        logger.info("Incrementing restart_trigger from %d to %d", initial_trigger, new_trigger)

        grpc.update_baremetal_instance_restart_trigger(bmi_id=bmi_id, restart_trigger=new_trigger)

        poll_until(
            fn=lambda: _get_condition_status(grpc, bmi_id, _RESTART_IN_PROGRESS),
            until=lambda v: v == "CONDITION_STATUS_TRUE",
            retries=60,
            delay=2,
            description=f"{bmi_id} RESTART_IN_PROGRESS condition appears",
        )

        poll_until(
            fn=lambda: _get_status_restart_trigger(grpc, bmi_id),
            until=lambda v: v == new_trigger,
            retries=120,
            delay=10,
            description=f"{bmi_id} status.restart_trigger echoes {new_trigger}",
        )

        poll_until(
            fn=lambda: k8s_hub_client.get_bmh_powered_on(name=bmh_name, bmh_namespace=bmh_ns),
            until=lambda v: v == "true",
            retries=60,
            delay=5,
            description=f"{bmh_name} powered on after restart",
        )

        wait_for_bmi_running(grpc=grpc, bmi_id=bmi_id)

        restart_in_progress: str = _get_condition_status(grpc, bmi_id, _RESTART_IN_PROGRESS)
        assert restart_in_progress in ("", "CONDITION_STATUS_FALSE"), (
            f"RESTART_IN_PROGRESS should have cleared after restart, got: {restart_in_progress}"
        )

        restart_failed: str = _get_condition_status(grpc, bmi_id, _RESTART_FAILED)
        assert restart_failed in ("", "CONDITION_STATUS_FALSE"), (
            f"Unexpected RESTART_FAILED condition: {restart_failed}"
        )

        cli.delete_baremetal_instance(uuid=bmi_id)
        wait_for_bmi_deletion(k8s=k8s_hub_client, name=bmi_cr_name)
        wait_for_bmi_grpc_removal(grpc=grpc, uuid=bmi_id)
    except BaseException:
        bmi_cr: str = k8s_hub_client.get_baremetal_instance_name(uuid=bmi_id, checked=False)
        if bmi_cr:
            try:
                cli.delete_baremetal_instance(uuid=bmi_id)
                wait_for_bmi_deletion(k8s=k8s_hub_client, name=bmi_cr)
                wait_for_bmi_grpc_removal(grpc=grpc, uuid=bmi_id)
            except Exception:
                logger.exception("Failed to delete BMI %s during cleanup", bmi_id)
        raise
