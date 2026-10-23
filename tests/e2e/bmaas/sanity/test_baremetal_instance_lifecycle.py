from __future__ import annotations

import logging
import os
import re
from typing import Any

import pytest

from tests.e2e.bmaas.conftest import BMI_DISK_IMAGE_SOURCE_REF
from tests.e2e.core.fulfillment_trust import assert_management_tls
from tests.e2e.core.grpc_client import PRIVATE_API, PUBLIC_API, GRPCClient
from tests.e2e.core.helpers import (
    HostRef,
    host_consumer_ref,
    host_hardware_macs,
    wait_for_bmi_cr,
    wait_for_bmi_deletion,
    wait_for_bmi_grpc_removal,
    wait_for_bmi_running,
    wait_for_host_allocated,
    wait_for_host_powered,
)
from tests.e2e.core.inventory import InventoryBackend
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import poll_until

pytestmark = pytest.mark.sanity

logger = logging.getLogger(__name__)

_RESTART_IN_PROGRESS: str = "BARE_METAL_INSTANCE_CONDITION_TYPE_RESTART_IN_PROGRESS"
_RESTART_FAILED: str = "BARE_METAL_INSTANCE_CONDITION_TYPE_RESTART_FAILED"
_READY: str = "BARE_METAL_INSTANCE_CONDITION_TYPE_READY"
_PROVISIONED: str = "BARE_METAL_INSTANCE_CONDITION_TYPE_PROVISIONED"
_CONDITION_STATUS_TRUE: str = "CONDITION_STATUS_TRUE"
# Terminal condition reasons stamped by the fulfillment reconciler at the Ready state:
# the READY axis carries the "Ready" state, the PROVISIONED axis the "Provisioned" state.
_READY_REASON: str = "Ready"
_PROVISIONED_REASON: str = "Provisioned"
_MAC_PATTERN: re.Pattern[str] = re.compile(r"^([0-9a-f]{2}:){5}[0-9a-f]{2}$")


def _assert_nic_metadata(
    *, grpc: GRPCClient, bmi_id: str, cli: OsacCLI, bmi_name: str, bmi_cr_name: str, k8s: K8sClient, host: HostRef
) -> None:
    """Verify NIC MAC addresses propagate correctly from BMH → BMI CR → gRPC API → CLI."""
    # 1. The BMH is the source of truth — hardware inspection provides the
    #    authoritative MAC list (the inventory hands over to a BMH in every mode)
    source_macs: set[str] = host_hardware_macs(k8s=k8s, host=host)
    assert source_macs, f"BareMetalHost {host.name} has no hardware.nics — inspection may not have completed"

    # 2. BMI CR status.hardware.nics must match the BMH
    cr_macs: set[str] = set(k8s.get_bmi_hardware_nics(name=bmi_cr_name))
    assert cr_macs, f"BareMetalInstance CR {bmi_cr_name} has no status.hardware.nics"
    assert cr_macs == source_macs, (
        f"BMI CR status.hardware.nics {sorted(cr_macs)} does not match "
        f"BareMetalHost hardware.nics {sorted(source_macs)}"
    )

    # 3. gRPC API response must match the BMI CR
    response: dict[str, Any] = grpc.get_baremetal_instance(bmi_id=bmi_id)
    nics: list[dict[str, Any]] = response.get("object", {}).get("status", {}).get("hardware", {}).get("nics", [])
    assert nics, f"gRPC API returned no status.hardware.nics for BMI {bmi_id}"
    api_macs: set[str] = {nic.get("mac", "").lower() for nic in nics}
    assert api_macs == source_macs, (
        f"gRPC API status.hardware.nics {sorted(api_macs)} does not match "
        f"BareMetalHost hardware.nics {sorted(source_macs)}"
    )

    # 4. CLI describe output must list all inventory MACs under the Network Interfaces section
    describe_output: str = cli.describe_baremetal_instance(name=bmi_name)
    assert "Network Interfaces:" in describe_output, (
        "osac describe baremetalinstance output missing 'Network Interfaces:' section"
    )
    ni_section = describe_output[describe_output.index("Network Interfaces:") :]
    for mac in source_macs:
        assert mac in ni_section, f"osac describe baremetalinstance 'Network Interfaces:' section missing MAC '{mac}'"


def _find_condition(response: dict[str, Any], condition_type: str) -> dict[str, Any]:
    """Return the status condition of the given type from a BMI API response, or {} if absent."""
    for condition in response.get("object", {}).get("status", {}).get("conditions", []):
        if condition.get("type") == condition_type:
            return condition
    return {}


def _get_condition(grpc: GRPCClient, bmi_id: str, condition_type: str) -> dict[str, Any]:
    """Fetch the BMI via the API and return its condition of the given type, or {} if absent."""
    return _find_condition(grpc.get_baremetal_instance(bmi_id=bmi_id), condition_type)


def _get_condition_status(grpc: GRPCClient, bmi_id: str, condition_type: str) -> str:
    """Return the status string of the BMI's condition of the given type, or "" if absent."""
    return _get_condition(grpc, bmi_id, condition_type).get("status", "")


def _get_status_restart_trigger(grpc: GRPCClient, bmi_id: str) -> int:
    response: dict[str, Any] = grpc.get_baremetal_instance(bmi_id=bmi_id)
    return int(response.get("object", {}).get("status", {}).get("restartTrigger", "0"))


def test_baremetal_instance_lifecycle(
    jwt_cli_user: OsacCLI,
    jwt_grpc_tenant1: GRPCClient,
    k8s_hub_client: K8sClient,
    catalog_item: str,
    bmi_instance_type: str,
    bmi_disk_image: str,
    inventory_backend: InventoryBackend,
    inventory_hosts: None,
    test_run_id: str,
    ssh_public_key: str,
) -> None:
    """Provision a BMI end-to-end and assert its terminal stage, NIC metadata, power cycle, and deprovision."""
    name = f"e2e-bmi-{test_run_id}"
    disk_images: dict[str, Any] = jwt_grpc_tenant1.call(service=f"{PUBLIC_API}.DiskImages/List")
    assert bmi_disk_image in {item["metadata"]["name"] for item in disk_images.get("items", [])}

    bmi_id, _ = jwt_cli_user.create_baremetal_instance(
        name=name, catalog_item=catalog_item, ssh_key=ssh_public_key, disk_image=bmi_disk_image
    )
    host: HostRef | None = None

    try:
        assert bmi_id in jwt_grpc_tenant1.list_baremetal_instance_ids()
        bmi_cr_name: str = wait_for_bmi_cr(k8s=k8s_hub_client, uuid=bmi_id)
        spec = jwt_grpc_tenant1.get_baremetal_instance(bmi_id=bmi_id).get("object", {}).get("spec", {})
        instance_type = spec.get("instance_type", spec.get("instanceType", {}))
        assert instance_type.get("name") == bmi_instance_type, (
            f"BareMetalInstance instance_type {instance_type.get('name')!r} does not match catalog default "
            f"{bmi_instance_type!r}"
        )

        wait_for_bmi_running(grpc=jwt_grpc_tenant1, bmi_id=bmi_id)
        if os.environ.get("OSAC_FULFILLMENT_TRUST_E2E") == "true":
            assert_management_tls(k8s_hub_client)

        # OSAC-5349: once the instance is RUNNING, the terminal provisioning stage must be
        # observable through the public API: the READY condition True (reason "Ready") with
        # the PROVISIONED axis coherent (True, reason "Provisioned"). Poll on the lightweight
        # READY status string (not the whole response) so the runner's progress/timeout
        # logging never dumps the full API payload, which can carry tenant data. Once READY
        # is True, read both axes from one fresh snapshot so they are asserted consistently.
        poll_until(
            fn=lambda: _get_condition_status(jwt_grpc_tenant1, bmi_id, _READY),
            until=lambda s: s == _CONDITION_STATUS_TRUE,
            retries=30,
            delay=2,
            description=f"{bmi_id} READY condition True",
        )
        instance: dict[str, Any] = jwt_grpc_tenant1.get_baremetal_instance(bmi_id=bmi_id)
        ready_condition: dict[str, Any] = _find_condition(instance, _READY)
        assert ready_condition.get("status") == _CONDITION_STATUS_TRUE, (
            f"READY condition should be True at the terminal stage, got {ready_condition.get('status')!r}"
        )
        assert ready_condition.get("reason") == _READY_REASON, (
            f"READY condition reason {ready_condition.get('reason')!r}, expected {_READY_REASON!r}"
        )
        provisioned_condition: dict[str, Any] = _find_condition(instance, _PROVISIONED)
        assert provisioned_condition.get("status") == _CONDITION_STATUS_TRUE, (
            f"PROVISIONED condition should be True at the terminal stage, got {provisioned_condition.get('status')!r}"
        )
        assert provisioned_condition.get("reason") == _PROVISIONED_REASON, (
            f"PROVISIONED condition reason {provisioned_condition.get('reason')!r}, expected {_PROVISIONED_REASON!r}"
        )

        host = inventory_backend.host_for_bmi(bmi_cr_name=bmi_cr_name)

        # Verify NIC metadata matches the BMH hardware inventory (OSAC-3254)
        _assert_nic_metadata(
            grpc=jwt_grpc_tenant1,
            bmi_id=bmi_id,
            cli=jwt_cli_user,
            bmi_name=name,
            bmi_cr_name=bmi_cr_name,
            k8s=k8s_hub_client,
            host=host,
        )

        # Verify provisioning on the BMH the inventory handed over to
        # (identical on every backend: image, consumerRef, online)
        wait_for_host_allocated(k8s=k8s_hub_client, host=host, expected_image_url=BMI_DISK_IMAGE_SOURCE_REF)

        consumer_ref: str = host_consumer_ref(k8s=k8s_hub_client, host=host)
        assert consumer_ref != "", f"BMH {host.name} has no consumerRef after allocation"

        # Power off
        halted = "BARE_METAL_INSTANCE_RUN_STRATEGY_HALTED"
        jwt_grpc_tenant1.update_baremetal_instance_run_strategy(bmi_id=bmi_id, run_strategy=halted)
        wait_for_host_powered(k8s=k8s_hub_client, host=host, on=False)

        # Power on
        jwt_grpc_tenant1.update_baremetal_instance_run_strategy(
            bmi_id=bmi_id, run_strategy="BARE_METAL_INSTANCE_RUN_STRATEGY_ALWAYS"
        )
        wait_for_host_powered(k8s=k8s_hub_client, host=host, on=True)

        # Deprovision
        jwt_cli_user.delete_baremetal_instance(uuid=bmi_id)
        wait_for_bmi_deletion(k8s=k8s_hub_client, name=bmi_cr_name)
        wait_for_bmi_grpc_removal(grpc=jwt_grpc_tenant1, uuid=bmi_id)

        inventory_backend.wait_for_released(host=host)
    except BaseException:
        bmi_cr: str = k8s_hub_client.get_baremetal_instance_name(uuid=bmi_id, checked=False)
        if bmi_cr:
            try:
                jwt_cli_user.delete_baremetal_instance(uuid=bmi_id)
                wait_for_bmi_deletion(k8s=k8s_hub_client, name=bmi_cr)
                wait_for_bmi_grpc_removal(grpc=jwt_grpc_tenant1, uuid=bmi_id)
                if host is not None:
                    inventory_backend.wait_for_released(host=host)
            except Exception:
                logger.exception("Failed to clean up BMI %s during error handling", bmi_id)
        raise


def test_baremetal_instance_restart(
    jwt_cli_user: OsacCLI,
    jwt_grpc_tenant1: GRPCClient,
    k8s_hub_client: K8sClient,
    catalog_item: str,
    inventory_backend: InventoryBackend,
    inventory_hosts: None,
    private_grpc: GRPCClient,
    test_run_id: str,
    ssh_public_key: str,
) -> None:
    name: str = f"e2e-bmi-restart-{test_run_id}"
    deprecated_disk_image_name = f"e2e-bmi-deprecated-di-{test_run_id}"
    deprecated_disk_image_id = private_grpc.create_disk_image(
        name=deprecated_disk_image_name, source_ref=BMI_DISK_IMAGE_SOURCE_REF, api=PRIVATE_API
    )
    bmi_id: str | None = None
    host: HostRef | None = None

    try:
        private_grpc.update_disk_image_lifecycle(
            disk_image_id=deprecated_disk_image_id, lifecycle="DISK_IMAGE_LIFECYCLE_DEPRECATED", api=PRIVATE_API
        )
        bmi_id, warnings = jwt_cli_user.create_baremetal_instance(
            name=name, catalog_item=catalog_item, disk_image=deprecated_disk_image_name, ssh_key=ssh_public_key
        )
        assert any("deprecated" in warning.lower() for warning in warnings), (
            f"Expected a deprecated DiskImage warning, got: {warnings}"
        )
        assert bmi_id in jwt_grpc_tenant1.list_baremetal_instance_ids()

        bmi_cr_name: str = wait_for_bmi_cr(k8s=k8s_hub_client, uuid=bmi_id)
        wait_for_bmi_running(grpc=jwt_grpc_tenant1, bmi_id=bmi_id)

        host = inventory_backend.host_for_bmi(bmi_cr_name=bmi_cr_name)

        initial_trigger: int = _get_status_restart_trigger(jwt_grpc_tenant1, bmi_id)
        new_trigger: int = initial_trigger + 1
        logger.info("Incrementing restart_trigger from %d to %d", initial_trigger, new_trigger)

        jwt_grpc_tenant1.update_baremetal_instance_restart_trigger(bmi_id=bmi_id, restart_trigger=new_trigger)

        poll_until(
            fn=lambda: _get_condition_status(jwt_grpc_tenant1, bmi_id, _RESTART_IN_PROGRESS),
            until=lambda v: v == "CONDITION_STATUS_TRUE",
            retries=60,
            delay=2,
            description=f"{bmi_id} RESTART_IN_PROGRESS condition appears",
        )

        poll_until(
            fn=lambda: _get_status_restart_trigger(jwt_grpc_tenant1, bmi_id),
            until=lambda v: v == new_trigger,
            retries=120,
            delay=10,
            description=f"{bmi_id} status.restart_trigger echoes {new_trigger}",
        )

        wait_for_host_powered(k8s=k8s_hub_client, host=host, on=True)

        wait_for_bmi_running(grpc=jwt_grpc_tenant1, bmi_id=bmi_id)

        restart_in_progress: str = _get_condition_status(jwt_grpc_tenant1, bmi_id, _RESTART_IN_PROGRESS)
        assert restart_in_progress in ("", "CONDITION_STATUS_FALSE"), (
            f"RESTART_IN_PROGRESS should have cleared after restart, got: {restart_in_progress}"
        )

        restart_failed: str = _get_condition_status(jwt_grpc_tenant1, bmi_id, _RESTART_FAILED)
        assert restart_failed in ("", "CONDITION_STATUS_FALSE"), (
            f"Unexpected RESTART_FAILED condition: {restart_failed}"
        )

        # Deprovision
        jwt_cli_user.delete_baremetal_instance(uuid=bmi_id)
        wait_for_bmi_deletion(k8s=k8s_hub_client, name=bmi_cr_name)
        wait_for_bmi_grpc_removal(grpc=jwt_grpc_tenant1, uuid=bmi_id)
        inventory_backend.wait_for_released(host=host)
        bmi_id = None
    except BaseException:
        bmi_cr: str = k8s_hub_client.get_baremetal_instance_name(uuid=bmi_id, checked=False) if bmi_id else ""
        if bmi_cr and bmi_id:
            try:
                jwt_cli_user.delete_baremetal_instance(uuid=bmi_id)
                wait_for_bmi_deletion(k8s=k8s_hub_client, name=bmi_cr)
                wait_for_bmi_grpc_removal(grpc=jwt_grpc_tenant1, uuid=bmi_id)
                if host is not None:
                    inventory_backend.wait_for_released(host=host)
            except Exception:
                logger.exception("Failed to delete BMI %s during cleanup", bmi_id)
        raise
    finally:
        try:
            private_grpc.delete_disk_image(disk_image_id=deprecated_disk_image_id, api=PRIVATE_API)
        except Exception:
            logger.exception("Failed to delete deprecated DiskImage %s during cleanup", deprecated_disk_image_id)
