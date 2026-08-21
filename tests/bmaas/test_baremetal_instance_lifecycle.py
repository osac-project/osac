from __future__ import annotations

import pytest
import logging
import re
from typing import Any

from tests.core.grpc_client import GRPCClient
from tests.core.helpers import (
    wait_for_bmh_available,
    wait_for_bmh_provisioned,
    wait_for_bmi_cr,
    wait_for_bmi_deletion,
    wait_for_bmi_grpc_removal,
    wait_for_bmi_running,
)
from tests.core.k8s_client import K8sClient
from tests.core.osac_cli import OsacCLI
from tests.core.runner import poll_until

logger = logging.getLogger(__name__)

_RESTART_IN_PROGRESS: str = "BARE_METAL_INSTANCE_CONDITION_TYPE_RESTART_IN_PROGRESS"
_RESTART_FAILED: str = "BARE_METAL_INSTANCE_CONDITION_TYPE_RESTART_FAILED"
_MAC_PATTERN: re.Pattern[str] = re.compile(r"^([0-9a-f]{2}:){5}[0-9a-f]{2}$")


def _assert_nic_metadata(
    *,
    grpc: GRPCClient,
    bmi_id: str,
    cli: OsacCLI,
    bmi_name: str,
    bmi_cr_name: str,
    k8s: K8sClient,
    bmh_name: str,
    bmh_namespace: str,
) -> None:
    """Verify NIC MAC addresses propagate correctly from BMH → BMI CR → gRPC API → CLI."""
    # 1. BMH is the source of truth — hardware inspection provides the authoritative MAC list
    bmh_macs: set[str] = set(k8s.get_bmh_hardware_nics(name=bmh_name, bmh_namespace=bmh_namespace))
    assert bmh_macs, f"BareMetalHost {bmh_name} has no hardware.nics — inspection may not have completed"

    # 2. BMI CR status.hardware.nics must match the BMH
    cr_macs: set[str] = set(k8s.get_bmi_hardware_nics(name=bmi_cr_name))
    assert cr_macs, f"BareMetalInstance CR {bmi_cr_name} has no status.hardware.nics"
    assert cr_macs == bmh_macs, (
        f"BMI CR status.hardware.nics {sorted(cr_macs)} does not match "
        f"BareMetalHost hardware.nics {sorted(bmh_macs)}"
    )

    # 3. gRPC API response must match the BMI CR
    response: dict[str, Any] = grpc.get_baremetal_instance(bmi_id=bmi_id)
    nics: list[dict[str, Any]] = response.get("object", {}).get("status", {}).get("hardware", {}).get("nics", [])
    assert nics, f"gRPC API returned no status.hardware.nics for BMI {bmi_id}"
    api_macs: set[str] = {nic.get("mac", "").lower() for nic in nics}
    assert api_macs == bmh_macs, (
        f"gRPC API status.hardware.nics {sorted(api_macs)} does not match "
        f"BareMetalHost hardware.nics {sorted(bmh_macs)}"
    )

    # 4. CLI describe output must list all BMH MACs under the Network Interfaces section
    describe_output: str = cli.describe_baremetal_instance(name=bmi_name)
    assert "Network Interfaces:" in describe_output, (
        "osac describe baremetalinstance output missing 'Network Interfaces:' section"
    )
    ni_section = describe_output[describe_output.index("Network Interfaces:"):]
    for mac in bmh_macs:
        assert mac in ni_section, (
            f"osac describe baremetalinstance 'Network Interfaces:' section missing MAC '{mac}'"
        )


def _get_condition_status(grpc: GRPCClient, bmi_id: str, condition_type: str) -> str:
    response: dict[str, Any] = grpc.get_baremetal_instance(bmi_id=bmi_id)
    for condition in response.get("object", {}).get("status", {}).get("conditions", []):
        if condition.get("type") == condition_type:
            return condition.get("status", "")
    return ""


def _get_status_restart_trigger(grpc: GRPCClient, bmi_id: str) -> int:
    response: dict[str, Any] = grpc.get_baremetal_instance(bmi_id=bmi_id)
    return int(response.get("object", {}).get("status", {}).get("restartTrigger", "0"))


@pytest.mark.sanity
def test_baremetal_instance_lifecycle(
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    catalog_item: str,
    bmh_namespace: str,
    test_run_id: str,
    ssh_public_key: str,
) -> None:
    name = f"e2e-bmi-{test_run_id}"
    bmi_id: str = cli.create_baremetal_instance(name=name, catalog_item=catalog_item, ssh_key=ssh_public_key)
    bmh_ns = ""
    bmh_name = ""

    try:
        assert bmi_id in grpc.list_baremetal_instance_ids()

        bmi_cr_name: str = wait_for_bmi_cr(k8s=k8s_hub_client, uuid=bmi_id)
        wait_for_bmi_running(grpc=grpc, bmi_id=bmi_id)

        external_host_id: str = k8s_hub_client.get_baremetal_instance_external_host_id(name=bmi_cr_name)
        assert "/" in external_host_id, f"Expected namespace/name format, got: {external_host_id}"
        bmh_ns, bmh_name = external_host_id.split("/", 1)
        assert bmh_ns == bmh_namespace, f"BMH landed in {bmh_ns}, expected {bmh_namespace}"

        # Verify NIC metadata matches the BMH hardware inventory (OSAC-3254)
        _assert_nic_metadata(
            grpc=grpc,
            bmi_id=bmi_id,
            cli=cli,
            bmi_name=name,
            bmi_cr_name=bmi_cr_name,
            k8s=k8s_hub_client,
            bmh_name=bmh_name,
            bmh_namespace=bmh_ns,
        )

        # Verify provisioning
        wait_for_bmh_provisioned(k8s=k8s_hub_client, name=bmh_name, bmh_namespace=bmh_ns)

        image_url: str = k8s_hub_client.get_bmh_image_url(name=bmh_name, bmh_namespace=bmh_ns)
        assert image_url != "", f"BMH {bmh_name} has no image URL after provisioning"

        consumer_ref: str = k8s_hub_client.get_bmh_consumer_ref(name=bmh_name, bmh_namespace=bmh_ns)
        assert consumer_ref != "", f"BMH {bmh_name} has no consumerRef after allocation"

        online: str = k8s_hub_client.get_bmh_online(name=bmh_name, bmh_namespace=bmh_ns)
        assert online == "true", f"BMH {bmh_name} should be online after provisioning, got: {online}"

        # Power off
        halted = "BARE_METAL_INSTANCE_RUN_STRATEGY_HALTED"
        grpc.update_baremetal_instance_run_strategy(bmi_id=bmi_id, run_strategy=halted)

        poll_until(
            fn=lambda: k8s_hub_client.get_bmh_powered_on(name=bmh_name, bmh_namespace=bmh_ns),
            until=lambda v: v == "false",
            retries=60,
            delay=5,
            description=f"{bmh_name} powered off",
        )

        # Power on
        grpc.update_baremetal_instance_run_strategy(
            bmi_id=bmi_id, run_strategy="BARE_METAL_INSTANCE_RUN_STRATEGY_ALWAYS"
        )

        poll_until(
            fn=lambda: k8s_hub_client.get_bmh_powered_on(name=bmh_name, bmh_namespace=bmh_ns),
            until=lambda v: v == "true",
            retries=60,
            delay=5,
            description=f"{bmh_name} powered on",
        )

        # Deprovision
        cli.delete_baremetal_instance(uuid=bmi_id)
        wait_for_bmi_deletion(k8s=k8s_hub_client, name=bmi_cr_name)
        wait_for_bmi_grpc_removal(grpc=grpc, uuid=bmi_id)

        wait_for_bmh_available(k8s=k8s_hub_client, name=bmh_name, bmh_namespace=bmh_ns)

        image_url_after: str = k8s_hub_client.get_bmh_image_url(name=bmh_name, bmh_namespace=bmh_ns)
        assert image_url_after == "", f"BMH {bmh_name} image not cleared after deprovision: {image_url_after}"

        consumer_ref_after: str = k8s_hub_client.get_bmh_consumer_ref(name=bmh_name, bmh_namespace=bmh_ns)
        assert consumer_ref_after == "", f"BMH {bmh_name} consumerRef not cleared: {consumer_ref_after}"
    except BaseException:
        bmi_cr: str = k8s_hub_client.get_baremetal_instance_name(uuid=bmi_id, checked=False)
        if bmi_cr:
            try:
                cli.delete_baremetal_instance(uuid=bmi_id)
                wait_for_bmi_deletion(k8s=k8s_hub_client, name=bmi_cr)
                wait_for_bmi_grpc_removal(grpc=grpc, uuid=bmi_id)
                if bmh_name:
                    wait_for_bmh_available(k8s=k8s_hub_client, name=bmh_name, bmh_namespace=bmh_ns)
            except Exception:
                pass
        raise


@pytest.mark.sanity
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

        # Deprovision
        cli.delete_baremetal_instance(uuid=bmi_id)
        wait_for_bmi_deletion(k8s=k8s_hub_client, name=bmi_cr_name)
        wait_for_bmi_grpc_removal(grpc=grpc, uuid=bmi_id)
        wait_for_bmh_available(k8s=k8s_hub_client, name=bmh_name, bmh_namespace=bmh_ns)
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
