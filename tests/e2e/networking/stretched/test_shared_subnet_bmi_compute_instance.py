from __future__ import annotations

import ipaddress
import os
import subprocess
from collections.abc import Callable
from pathlib import Path

import pytest

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import (
    wait_for_bmi_cr,
    wait_for_bmi_deletion,
    wait_for_bmi_grpc_removal,
    wait_for_bmi_running,
    wait_for_cr,
    wait_for_deletion,
    wait_for_grpc_subnet_ready,
    wait_for_provision,
    wait_for_running,
    wait_for_security_group_cr,
    wait_for_security_group_deletion,
    wait_for_security_group_ready,
    wait_for_subnet_cr,
    wait_for_subnet_deletion,
    wait_for_subnet_ready,
    wait_for_virtual_network_cr,
    wait_for_virtual_network_deletion,
    wait_for_virtual_network_ready,
    wait_for_vmi_ip,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import env, poll_until
from tests.e2e.networking.bmaas import bmi_ssh
from tests.e2e.networking.vmaas import guest_ssh

pytestmark = [pytest.mark.regression, pytest.mark.requires_stretched_networking]


@pytest.fixture(scope="module")
def full_vm_template() -> str:
    return env("OSAC_VM_TEMPLATE", "ocp-virt-vm")


def test_bmi_and_compute_instance_reach_each_other_on_shared_subnet(
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    namespace: str,
    full_vm_template: str,
    catalog_item_name: str,
    bmi_instance_type: str,
    bmi_network_interface: str,
    bmi_disk_image: str,
    net_ssh_public_key: str,
    bmh_ssh_hosts: dict[str, str],
    net_test_run_id: str,
    tmp_path: Path,
) -> None:
    vm_kubeconfig = os.environ["OSAC_VM_KUBECONFIG"]
    k8s_virt_client = K8sClient(namespace=namespace, kubeconfig=vm_kubeconfig)
    suffix = net_test_run_id
    octet = 160 + int(suffix[:2], 16) % 60
    virtual_network_name = f"stretched-shared-vn-{suffix}"
    virtual_network_cidr = f"10.{octet}.0.0/16"
    subnet_name = f"stretched-shared-subnet-{suffix}"
    subnet_cidr = f"10.{octet}.1.0/24"
    security_group_name = f"stretched-shared-sg-{suffix}"
    bmi_name = f"stretched-shared-bmi-{suffix}"
    compute_instance_name = f"stretched-shared-vm-{suffix}"
    ssh_secret_name = f"stretched-shared-vm-key-{suffix}"

    virtual_network_id: str | None = None
    virtual_network_cr: str | None = None
    subnet_id: str | None = None
    subnet_cr: str | None = None
    security_group_id: str | None = None
    security_group_cr: str | None = None
    bmi_id: str | None = None
    bmi_cr: str | None = None
    compute_instance_id: str | None = None
    compute_instance_cr: str | None = None
    ssh_secret_created = False
    private_key = tmp_path / "id_ed25519"
    public_key_file = private_key.with_suffix(".pub")

    try:
        virtual_network_id = grpc.create_virtual_network(name=virtual_network_name, ipv4_cidr=virtual_network_cidr)
        virtual_network_cr = wait_for_virtual_network_cr(k8s=k8s_hub_client, uuid=virtual_network_id)
        wait_for_virtual_network_ready(k8s=k8s_hub_client, name=virtual_network_cr)

        subnet_id = grpc.create_subnet(name=subnet_name, virtual_network=virtual_network_id, ipv4_cidr=subnet_cidr)
        subnet_cr = wait_for_subnet_cr(k8s=k8s_hub_client, uuid=subnet_id)
        wait_for_subnet_ready(k8s=k8s_hub_client, name=subnet_cr)
        wait_for_grpc_subnet_ready(grpc=grpc, subnet_id=subnet_id)

        security_group_id = grpc.create_security_group_with_rules(
            name=security_group_name,
            virtual_network=virtual_network_id,
            ingress=[
                {"protocol": "PROTOCOL_TCP", "port_from": 22, "port_to": 22, "ipv4_cidr": "0.0.0.0/0"},
                {"protocol": "PROTOCOL_ICMP", "ipv4_cidr": subnet_cidr},
            ],
            egress=[{"protocol": "PROTOCOL_ALL", "ipv4_cidr": "0.0.0.0/0"}],
        )
        security_group_cr = wait_for_security_group_cr(k8s=k8s_hub_client, uuid=security_group_id)
        wait_for_security_group_ready(k8s=k8s_hub_client, name=security_group_cr)

        subprocess.run(
            ["ssh-keygen", "-t", "ed25519", "-f", str(private_key), "-N", "", "-C", "osac-stretched-networking-e2e"],
            check=True,
            capture_output=True,
            text=True,
        )
        cli.create_secret(
            name=ssh_secret_name, secret_type="ssh-public-key", from_files={"public_key": str(public_key_file)}
        )
        ssh_secret_created = True

        bmi_id, _warnings = cli.create_baremetal_instance(
            name=bmi_name,
            catalog_item=catalog_item_name,
            instance_type=bmi_instance_type or None,
            ssh_key=net_ssh_public_key,
            disk_image=bmi_disk_image,
            network_attachments=[
                f"subnet={subnet_id},interface={bmi_network_interface},primary,security-groups={security_group_id}"
            ],
        )
        bmi_cr = wait_for_bmi_cr(k8s=k8s_hub_client, uuid=bmi_id)
        wait_for_bmi_running(grpc=grpc, bmi_id=bmi_id)
        bmi_ip = poll_until(
            fn=lambda: k8s_hub_client.get_baremetal_instance_tenant_ip(name=bmi_cr),
            until=bool,
            retries=60,
            delay=10,
            description=f"BMI {bmi_name} tenant IP assignment",
        )
        external_host_id = k8s_hub_client.get_baremetal_instance_external_host_id(name=bmi_cr)
        bmh_name = external_host_id.split("/", 1)[1]
        bmi_ssh_host = bmi_ssh.get_ssh_host(bmh_name, bmh_ssh_hosts)

        compute_instance_id = cli.create_compute_instance(
            template=full_vm_template,
            name=compute_instance_name,
            network_attachments=[{"subnet": subnet_id, "security_groups": [security_group_id]}],
            ssh_key=ssh_secret_name,
        )
        compute_instance_cr = wait_for_cr(k8s=k8s_hub_client, uuid=compute_instance_id)
        wait_for_provision(k8s=k8s_hub_client, name=compute_instance_cr)
        wait_for_running(k8s=k8s_hub_client, name=compute_instance_cr)
        vm_namespace = poll_until(
            fn=lambda: k8s_hub_client.get_compute_instance_vm_namespace(name=compute_instance_cr),
            until=bool,
            retries=60,
            delay=5,
            description=f"VMI namespace for {compute_instance_name}",
        )
        vm_ip = wait_for_vmi_ip(
            k8s=k8s_virt_client, vmi_namespace=vm_namespace, compute_instance_name=compute_instance_cr
        )

        subnet = ipaddress.IPv4Network(subnet_cidr)
        assert ipaddress.IPv4Address(bmi_ip) in subnet, f"BMI address {bmi_ip} is outside {subnet}"
        assert ipaddress.IPv4Address(vm_ip) in subnet, f"ComputeInstance address {vm_ip} is outside {subnet}"
        assert bmi_ip != vm_ip, f"BMI and ComputeInstance received the same address {bmi_ip}"

        poll_until(
            fn=lambda: bmi_ssh.arping(bmi_ssh_host, vm_ip),
            until=bool,
            retries=5,
            delay=10,
            description=f"BMI ARP resolution for ComputeInstance {vm_ip}",
        )
        poll_until(
            fn=lambda: guest_ssh.arping(vm_namespace, compute_instance_cr, str(private_key), bmi_ip),
            until=bool,
            retries=5,
            delay=10,
            description=f"ComputeInstance ARP resolution for BMI {bmi_ip}",
        )
        poll_until(
            fn=lambda: bmi_ssh.ping(bmi_ssh_host, vm_ip),
            until=bool,
            retries=5,
            delay=10,
            description=f"BMI ping to ComputeInstance {vm_ip}",
        )
        poll_until(
            fn=lambda: guest_ssh.ping(vm_namespace, compute_instance_cr, str(private_key), bmi_ip),
            until=bool,
            retries=5,
            delay=10,
            description=f"ComputeInstance ping to BMI {bmi_ip}",
        )
    finally:
        cleanup_errors: list[str] = []

        def cleanup(label: str, operation: Callable[[], None]) -> None:
            try:
                operation()
            except Exception as exc:
                cleanup_errors.append(f"{label}: {exc}")

        if compute_instance_id:
            ci_id = compute_instance_id

            def delete_compute_instance() -> None:
                cli.delete_compute_instance(uuid=ci_id)
                if compute_instance_cr:
                    wait_for_deletion(k8s=k8s_hub_client, name=compute_instance_cr)
                poll_until(
                    fn=lambda: ci_id not in grpc.list_compute_instance_ids(),
                    until=bool,
                    retries=30,
                    delay=5,
                    description=f"ComputeInstance {ci_id} API deletion",
                )

            cleanup(f"ComputeInstance {ci_id}", delete_compute_instance)
        if bmi_id:
            current_bmi_id = bmi_id

            def delete_bmi() -> None:
                cli.delete_baremetal_instance(uuid=current_bmi_id)
                if bmi_cr:
                    wait_for_bmi_deletion(k8s=k8s_hub_client, name=bmi_cr)
                wait_for_bmi_grpc_removal(grpc=grpc, uuid=current_bmi_id)

            cleanup(f"BMI {current_bmi_id}", delete_bmi)
        if ssh_secret_created:
            cleanup(f"SSH key Secret {ssh_secret_name}", lambda: cli.delete_secret(name=ssh_secret_name))
        if security_group_id:
            current_security_group_id = security_group_id

            def delete_security_group() -> None:
                grpc.delete_security_group(sg_id=current_security_group_id)
                if security_group_cr:
                    wait_for_security_group_deletion(k8s=k8s_hub_client, name=security_group_cr)

            cleanup(f"SecurityGroup {current_security_group_id}", delete_security_group)
        if subnet_id:
            current_subnet_id = subnet_id

            def delete_subnet() -> None:
                grpc.delete_subnet(subnet_id=current_subnet_id)
                if subnet_cr:
                    wait_for_subnet_deletion(k8s=k8s_hub_client, name=subnet_cr)

            cleanup(f"Subnet {current_subnet_id}", delete_subnet)
        if virtual_network_id:
            current_virtual_network_id = virtual_network_id

            def delete_virtual_network() -> None:
                grpc.delete_virtual_network(vn_id=current_virtual_network_id)
                if virtual_network_cr:
                    wait_for_virtual_network_deletion(k8s=k8s_hub_client, name=virtual_network_cr)

            cleanup(f"VirtualNetwork {current_virtual_network_id}", delete_virtual_network)
        if cleanup_errors:
            raise RuntimeError("E2E resource cleanup failed: " + "; ".join(cleanup_errors))
