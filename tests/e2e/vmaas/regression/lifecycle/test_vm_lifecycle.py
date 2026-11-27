"""OSAC-6110 catalog-to-VM lifecycle, migrated from the Ansible VMaaS suite."""

from __future__ import annotations

import ipaddress
import json
from collections.abc import Callable, Iterator
from contextlib import ExitStack
from dataclasses import dataclass

import pytest

from tests.e2e.core.failures import Kind
from tests.e2e.core.grpc_client import PRIVATE_API, GRPCClient
from tests.e2e.core.helpers import (
    allocate_worker_subnet,
    unique_name,
    wait_for_cr,
    wait_for_deletion,
    wait_for_grpc_removal,
    wait_for_grpc_security_group_ready,
    wait_for_grpc_subnet_ready,
    wait_for_grpc_virtual_network_ready,
    wait_for_new_vmi,
    wait_for_provision,
    wait_for_running,
    wait_for_security_group_cr,
    wait_for_security_group_ready,
    wait_for_subnet_cr,
    wait_for_subnet_ready,
    wait_for_virtual_network_cr,
    wait_for_virtual_network_ready,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import run
from tests.e2e.core.wait_kinds import WaitResourceKind
from tests.e2e.networking.vmaas import stack
from tests.e2e.vmaas.compute_instance_helpers import next_restart_timestamp
from tests.e2e.vmaas.conftest import DEFAULT_DISK_IMAGE_SOURCE_REF
from tests.e2e.vmaas.networking_lifecycle_helpers import (
    delete_and_wait_for_security_group,
    delete_and_wait_for_subnet,
    delete_and_wait_for_virtual_network,
)

pytestmark = pytest.mark.regression


@dataclass(frozen=True)
class LifecycleNetwork:
    cidr: str
    subnet_id: str
    security_group_id: str


@dataclass(frozen=True)
class LifecycleVM:
    compute_instance_id: str
    compute_instance_cr: str
    network: LifecycleNetwork
    guest_snapshot: dict[str, str]


def _wait_private_removal(grpc: GRPCClient, service: str, resource_id: str, kind: WaitResourceKind) -> None:
    def absent() -> bool:
        page_token = ""
        while True:
            page = grpc.call(
                service=f"{PRIVATE_API}.{service}/List", data={"pageToken": page_token} if page_token else None
            )
            if any(item["id"] == resource_id for item in page.get("items", [])):
                return False
            page_token = page.get("nextPageToken", "")
            if not page_token:
                return True

    stack.poll_or_fail(
        fn=absent,
        until=bool,
        description=f"{kind} {resource_id} removal",
        hint="Check the resource deletion and its references.",
        resource_kind=kind,
        resource_id=resource_id,
        kind=Kind.STUCK_STATE,
    )


@pytest.fixture
def lifecycle_image(private_grpc: GRPCClient) -> Iterator[str]:
    """Create a shared image for the catalog and remove it after use."""
    with ExitStack() as cleanup:
        image_name = unique_name("lifecycle-image")
        image_id = private_grpc.create_disk_image(
            name=image_name, source_ref=DEFAULT_DISK_IMAGE_SOURCE_REF, api=PRIVATE_API
        )

        def delete_image() -> None:
            private_grpc.delete_disk_image(disk_image_id=image_id, api=PRIVATE_API)
            _wait_private_removal(private_grpc, "DiskImages", image_id, WaitResourceKind.DISK_IMAGE)

        cleanup.callback(delete_image)
        stack.poll_or_fail(
            fn=lambda: (
                private_grpc.call(service=f"{PRIVATE_API}.DiskImages/Get", data={"id": image_id})["object"]
                .get("spec", {})
                .get("lifecycle", "")
            ),
            until=lambda state: state == "DISK_IMAGE_LIFECYCLE_AVAILABLE",
            description=f"DiskImage {image_name} AVAILABLE",
            hint="Check the test DiskImage lifecycle.",
            resource_kind=WaitResourceKind.DISK_IMAGE,
            resource_id=image_id,
            kind=Kind.STUCK_STATE,
        )
        yield image_name


@pytest.fixture
def lifecycle_network(grpc: GRPCClient, k8s_hub_client: K8sClient) -> Iterator[LifecycleNetwork]:
    """Create the VM's network dependencies and remove them in reverse order."""
    network = allocate_worker_subnet()
    with ExitStack() as cleanup:
        vn_id = grpc.create_virtual_network(name=unique_name("lifecycle-vn"), ipv4_cidr=str(network))
        vn_cr: str | None = None
        cleanup.callback(lambda: delete_and_wait_for_virtual_network(grpc, k8s_hub_client, vn_id, vn_cr))
        vn_cr = wait_for_virtual_network_cr(k8s=k8s_hub_client, uuid=vn_id)
        wait_for_virtual_network_ready(k8s=k8s_hub_client, name=vn_cr)
        wait_for_grpc_virtual_network_ready(grpc=grpc, vn_id=vn_id)

        subnet_id = grpc.create_subnet(
            name=unique_name("lifecycle-subnet"), virtual_network=vn_id, ipv4_cidr=str(network)
        )
        subnet_cr: str | None = None
        cleanup.callback(lambda: delete_and_wait_for_subnet(grpc, k8s_hub_client, subnet_id, subnet_cr))
        subnet_cr = wait_for_subnet_cr(k8s=k8s_hub_client, uuid=subnet_id)
        wait_for_subnet_ready(k8s=k8s_hub_client, name=subnet_cr)
        wait_for_grpc_subnet_ready(grpc=grpc, subnet_id=subnet_id)

        sg_id = grpc.create_security_group_with_rules(
            name=unique_name("lifecycle-sg"),
            virtual_network=vn_id,
            ingress=[
                {"protocol": "PROTOCOL_TCP", "port_from": 22, "port_to": 22, "ipv4_cidr": "0.0.0.0/0"},
                {"protocol": "PROTOCOL_ICMP", "ipv4_cidr": "0.0.0.0/0"},
            ],
            egress=[{"protocol": "PROTOCOL_ALL", "ipv4_cidr": "0.0.0.0/0"}],
        )
        sg_cr: str | None = None
        cleanup.callback(lambda: delete_and_wait_for_security_group(grpc, k8s_hub_client, sg_id, sg_cr))
        sg_cr = wait_for_security_group_cr(k8s=k8s_hub_client, uuid=sg_id)
        wait_for_security_group_ready(k8s=k8s_hub_client, name=sg_cr)
        wait_for_grpc_security_group_ready(grpc=grpc, sg_id=sg_id)
        yield LifecycleNetwork(cidr=str(network), subnet_id=subnet_id, security_group_id=sg_id)


@pytest.fixture
def lifecycle_catalog(
    grpc: GRPCClient,
    private_grpc: GRPCClient,
    vm_template: str,
    default_instance_type: str,
    default_storage_tier: str,
    lifecycle_image: str,
) -> Iterator[str]:
    """Publish the catalog item and remove it before its image is deleted."""
    stack.poll_or_fail(
        fn=lambda: private_grpc.get_instance_type(name=default_instance_type)["object"]["spec"].get("state", ""),
        until=lambda state: state == "INSTANCE_TYPE_STATE_ACTIVE",
        description=f"InstanceType {default_instance_type} ACTIVE",
        hint="Check the shared InstanceType state.",
        resource_kind=WaitResourceKind.INSTANCE_TYPE,
        resource_id=default_instance_type,
        kind=Kind.STUCK_STATE,
    )
    fields = {
        "boot_disk": {
            "storage_tier": {"editable": {"default_value": {"name": default_storage_tier}}},
            "size_gib": {"editable": {"default_value": 10}},
        },
        "disk_image": {"editable": {"default_value": {"name": lifecycle_image, "shared": True}}},
        "instance_type": {"editable": {"default_value": {"name": default_instance_type, "shared": True}}},
        "network_attachments": {"editable": {}},
        "run_strategy": {"editable": {}},
    }
    catalog_id = private_grpc.create_compute_instance_catalog_item(
        name=unique_name("lifecycle-catalog"), template=vm_template, published=True, fields=fields, api=PRIVATE_API
    )
    try:
        catalog = grpc.get_compute_instance_catalog_item(catalog_item_id=catalog_id)["object"]
        assert catalog["published"] is True
        yield catalog_id
    finally:
        private_grpc.delete_compute_instance_catalog_item(catalog_item_id=catalog_id, api=PRIVATE_API)
        _wait_private_removal(
            private_grpc, "ComputeInstanceCatalogItems", catalog_id, WaitResourceKind.COMPUTE_INSTANCE_CATALOG_ITEM
        )


@pytest.fixture
def lifecycle_vm(
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    lifecycle_network: LifecycleNetwork,
    lifecycle_catalog: str,
) -> Iterator[LifecycleVM]:
    """Create the VM and register its snapshot and teardown."""
    guests: list[dict[str, str]] = []
    snapshot_name = unique_name("lifecycle-guest")
    stack.register_guest_snapshot(snapshot_name, guests, identity_file=None)

    with ExitStack() as cleanup:
        cleanup.callback(stack.unregister_guest_snapshot, snapshot_name)
        ci_id = cli.create_compute_instance_with_catalog_item(
            catalog_item=lifecycle_catalog,
            name=unique_name("lifecycle-vm"),
            subnet=lifecycle_network.subnet_id,
            security_groups=[lifecycle_network.security_group_id],
        )
        ci_cr: str | None = None

        def delete_vm() -> None:
            cli.delete_compute_instance(uuid=ci_id)
            if ci_cr is not None:
                wait_for_deletion(k8s=k8s_hub_client, name=ci_cr)
            wait_for_grpc_removal(grpc=grpc, uuid=ci_id)

        cleanup.callback(delete_vm)
        ci_cr = wait_for_cr(k8s=k8s_hub_client, uuid=ci_id)
        guest_snapshot = {"name": ci_id, "cr": ci_cr, "vm_namespace": ""}
        guests.append(guest_snapshot)
        yield LifecycleVM(
            compute_instance_id=ci_id,
            compute_instance_cr=ci_cr,
            network=lifecycle_network,
            guest_snapshot=guest_snapshot,
        )


def _wait_for_api_state(grpc: GRPCClient, ci_id: str, state: str) -> None:
    stack.poll_or_fail(
        fn=lambda: grpc.get_compute_instance(ci_id=ci_id)["object"].get("status", {}).get("state", ""),
        until=lambda actual: actual == state,
        description=f"ComputeInstance {ci_id} to reach {state}",
        hint="Check the ComputeInstance and its VM reconciliation.",
        resource_kind=WaitResourceKind.COMPUTE_INSTANCE,
        resource_id=ci_id,
        kind=Kind.STUCK_STATE,
    )


def test_catalog_vm_stop_start_restart_and_delete(
    lifecycle_vm: LifecycleVM,
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    k8s_virt_client: K8sClient,
    networking_provisioning_enabled: bool,
    compute_instance_spec_editor: Callable[[dict[str, object]], None],
) -> None:
    ci_id, cr = lifecycle_vm.compute_instance_id, lifecycle_vm.compute_instance_cr
    wait_for_provision(k8s=k8s_hub_client, name=cr)
    wait_for_running(k8s=k8s_hub_client, name=cr)
    _wait_for_api_state(grpc, ci_id, "COMPUTE_INSTANCE_STATE_RUNNING")
    vm_namespace = k8s_hub_client.get_compute_instance_vm_namespace(name=cr)
    assert vm_namespace
    lifecycle_vm.guest_snapshot["vm_namespace"] = vm_namespace

    obj = grpc.get_compute_instance(ci_id=ci_id)["object"]
    attachment = obj["spec"]["networkAttachments"][0]
    assert attachment["subnet"]["id"] == lifecycle_vm.network.subnet_id
    assert lifecycle_vm.network.security_group_id in [item["id"] for item in attachment["securityGroups"]]

    guest_ip = stack.poll_or_fail(
        fn=lambda: grpc.get_compute_instance(ci_id=ci_id)["object"].get("status", {}).get("internalIpAddress", ""),
        until=bool,
        description=f"ComputeInstance {ci_id} internal IP",
        hint="Check the VM guest agent and OSAC status feedback.",
        resource_kind=WaitResourceKind.COMPUTE_INSTANCE,
        resource_id=ci_id,
        kind=Kind.STUCK_STATE,
    )
    if networking_provisioning_enabled:
        networks = [ipaddress.ip_network(lifecycle_vm.network.cidr)]
    else:
        network_config = json.loads(run(*k8s_virt_client.base_args(), "get", "network.config/cluster", "-o", "json"))
        networks = [ipaddress.ip_network(item["cidr"]) for item in network_config["status"]["clusterNetwork"]]
    assert any(ipaddress.ip_address(guest_ip) in network for network in networks)

    for strategy, expected in (("Halted", "STOPPED"), ("Always", "RUNNING")):
        compute_instance_spec_editor({"run_strategy": strategy})
        cli.edit_compute_instance(uuid=ci_id)
        _wait_for_api_state(grpc, ci_id, f"COMPUTE_INSTANCE_STATE_{expected}")

    previous_vmi = k8s_virt_client.get_vmi_creation_timestamp(vmi_namespace=vm_namespace, compute_instance_name=cr)
    assert previous_vmi
    previous_restart = k8s_hub_client.get_compute_instance_last_restarted_at(name=cr)
    restart_at = next_restart_timestamp(previous_restart)
    compute_instance_spec_editor({"restart_requested_at": restart_at})
    cli.edit_compute_instance(uuid=ci_id)
    new_vmi = wait_for_new_vmi(
        k8s=k8s_virt_client, vmi_namespace=vm_namespace, compute_instance_name=cr, initial_timestamp=previous_vmi
    )
    assert new_vmi > previous_vmi
    _wait_for_api_state(grpc, ci_id, "COMPUTE_INSTANCE_STATE_RUNNING")
