"""Storage over tenant SNAT: VM guest, default network, and per-tenant identity."""

from __future__ import annotations

import ipaddress

import pytest

from tests.e2e.core.grpc_client import PUBLIC_API, GRPCClient
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.networking import cidrs
from tests.e2e.networking.storage.endpoint import (
    StorageEndpoint,
    assert_tenants_are_distinct,
    ensure_nfs_client_command,
    log_line_count,
    mount_command,
    new_log_lines,
    wait_for_client,
    write_probe_file_command,
)
from tests.e2e.networking.vmaas import guest_ssh, stack

pytestmark = pytest.mark.requires_vmaas

MOUNTPOINT = "/mnt/osac-storage"
_ALL = {"protocol": "PROTOCOL_ALL", "ipv4_cidr": "0.0.0.0/0"}


def _default_nat(client: GRPCClient) -> str:
    items = client.call(service=f"{PUBLIC_API}.ExternalIPs/List").get("items", [])
    external_ip = next((item for item in items if item.get("metadata", {}).get("name") == "default-nat"), None)
    address = (external_ip or {}).get("status", {}).get("address", "")
    if not address:
        raise AssertionError("the tenant has no allocated default-nat ExternalIP; default networking did not finish")
    return str(address)


def _run(vm: dict[str, str], command: str, identity: str, timeout: int = 180) -> tuple[str, int]:
    return guest_ssh.ssh_guest_unchecked(
        vm_namespace=vm["vm_namespace"], vm_name=vm["cr"], identity_file=identity, command=command, timeout=timeout
    )


def _mount_and_write(vm: dict[str, str], endpoint: StorageEndpoint, identity: str, marker: str) -> str:
    output, status = _run(vm, ensure_nfs_client_command(), identity, timeout=300)
    assert status == 0, f"NFS client setup failed on {vm['name']}: {output!r}"
    command = (
        f"{mount_command(endpoint, MOUNTPOINT)} || mountpoint -q {MOUNTPOINT}"
        f" && {write_probe_file_command(MOUNTPOINT, marker)}"
    )
    output, status = _run(vm, command, identity, timeout=120)
    assert status == 0, f"NFS mount failed on {vm['name']}: {output!r}"
    assert marker in output, f"probe file missing on {vm['name']}: {output!r}"
    return output


def test_fr5_01_vm_guest_mounts_nfs(
    storage_vms: dict[str, dict[str, str]],
    storage_endpoint: StorageEndpoint,
    storage_identity_file: str,
    grpc: GRPCClient,
) -> None:
    """TC-FR5-01: a VM guest mounts through the NATGateway and the server logs its NAT address."""
    vm = storage_vms["tenant1"]
    nat = _default_nat(grpc)
    since = log_line_count(storage_endpoint)
    marker = f"storage-nat-{vm['id']}"

    _mount_and_write(vm, storage_endpoint, storage_identity_file, marker)

    address = wait_for_client(storage_endpoint, nat, since=since)
    assert address == ipaddress.IPv4Address(nat), f"server saw {address}, expected the tenant NAT address {nat}"


def test_fr6_01_new_tenant_default_network(
    storage_vms: dict[str, dict[str, str]],
    tenant2_default_network: dict[str, str],
    storage_endpoint: StorageEndpoint,
    storage_identity_file: str,
    jwt_grpc_tenant2_admin: GRPCClient,
) -> None:
    """TC-FR6-01: a newly onboarded tenant reaches storage with no networking resource created by the test."""
    vm = storage_vms["tenant2"]
    nat = _default_nat(jwt_grpc_tenant2_admin)
    since = log_line_count(storage_endpoint)
    marker = f"storage-nat-{vm['id']}"

    assert tenant2_default_network["label"] == "default"
    _mount_and_write(vm, storage_endpoint, storage_identity_file, marker)

    address = wait_for_client(storage_endpoint, nat, since=since)
    assert address == ipaddress.IPv4Address(nat)


def test_nfr3_01_same_cidr_two_nat_addresses(
    storage_vms: dict[str, dict[str, str]],
    tenant1_default_network: dict[str, str],
    tenant2_default_network: dict[str, str],
    storage_endpoint: StorageEndpoint,
    storage_identity_file: str,
    grpc: GRPCClient,
    jwt_grpc_tenant2_admin: GRPCClient,
) -> None:
    """TC-NFR3-01: two tenants with the same CIDR reach one storage system with different source addresses."""
    assert tenant1_default_network["subnet_cidr"] == tenant2_default_network["subnet_cidr"]
    nat1 = _default_nat(grpc)
    nat2 = _default_nat(jwt_grpc_tenant2_admin)
    assert nat1 != nat2, "two tenants must not share a NAT address"

    seen: list[ipaddress.IPv4Address] = []
    for vm, nat in ((storage_vms["tenant1"], nat1), (storage_vms["tenant2"], nat2)):
        since = log_line_count(storage_endpoint)
        _mount_and_write(vm, storage_endpoint, storage_identity_file, f"storage-nat-{vm['id']}")
        seen.append(wait_for_client(storage_endpoint, nat, since=since))

    assert_tenants_are_distinct(seen)
    assert {str(address) for address in seen} == {nat1, nat2}


def test_fr5_02_no_natgateway_no_storage(
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    k8s_virt_client: K8sClient,
    vm_template: str,
    default_instance_type: str,
    default_disk_image: str,
    default_storage_tier: str,
    storage_key_secrets: dict[str, str],
    storage_endpoint: StorageEndpoint,
    storage_identity_file: str,
    net_test_run_id: str,
) -> None:
    """TC-FR5-02: a VirtualNetwork without a NATGateway has no storage path."""
    networks: list[dict[str, str]] = []
    instances: list[dict[str, str]] = []
    vn_cidr = cidrs.tenant_vn_cidr(13, offset=cidrs.run_offset(net_test_run_id))
    subnet_cidr = cidrs.subnet_cidr(vn_cidr, 1)
    try:
        network = stack.create_network(
            client=grpc,
            k8s_hub_client=k8s_hub_client,
            networks=networks,
            label="storage-nonat",
            vn_cidr=vn_cidr,
            subnet_cidr=subnet_cidr,
            run_id=net_test_run_id,
            ingress=[_ALL],
            egress=[_ALL],
        )
        vm = stack.create_vm(
            cli=cli,
            k8s_hub_client=k8s_hub_client,
            instances=instances,
            name=f"storage-nonat-vm-{net_test_run_id}",
            network=network,
            sg_id=network["sg_id"],
            key_secret=storage_key_secrets["tenant1"],
            vm_template=vm_template,
            instance_type=default_instance_type,
            disk_image=default_disk_image,
            storage_tier=default_storage_tier,
        )
        stack.wait_for_vm(
            k8s_hub_client=k8s_hub_client,
            k8s_virt_client=k8s_virt_client,
            instance=vm,
            identity_file=storage_identity_file,
        )

        since = log_line_count(storage_endpoint)
        setup, setup_status = _run(vm, ensure_nfs_client_command(), storage_identity_file, timeout=300)
        assert setup_status == 0, f"NFS client setup failed on {vm['name']}: {setup!r}"
        command = f"timeout 30 {mount_command(storage_endpoint, MOUNTPOINT)}"
        first = _run(vm, command, storage_identity_file, timeout=120)
        second = _run(vm, command, storage_identity_file, timeout=60)
        assert first[1] != 0, f"mount unexpectedly succeeded without a NATGateway: {first[0]!r}"
        assert second[1] != 0, f"mount unexpectedly succeeded on retry without a NATGateway: {second[0]!r}"

        new_lines = new_log_lines(storage_endpoint, since)
        assert not any(str(vm["ip"]) in line for line in new_lines), (
            f"the storage server logged the tenant address {vm['ip']} even though the VN has no NATGateway"
        )
    finally:
        for vm in list(instances):
            stack.delete_vm(client=grpc, k8s_hub_client=k8s_hub_client, instance=vm)
            instances.remove(vm)
        for network in list(networks):
            stack.delete_network(client=grpc, k8s_hub_client=k8s_hub_client, network=network)
            networks.remove(network)
