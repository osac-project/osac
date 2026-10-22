from __future__ import annotations

import ipaddress
import os
import re
import subprocess
from collections.abc import Iterator
from typing import Any

import pytest

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import (
    allocate_worker_subnet,
    wait_for_cr,
    wait_for_deletion,
    wait_for_external_ip_allocated,
    wait_for_external_ip_attachment_cr,
    wait_for_external_ip_attachment_deletion,
    wait_for_external_ip_attachment_ready,
    wait_for_external_ip_cr,
    wait_for_external_ip_deletion,
    wait_for_external_ip_pool_cr,
    wait_for_external_ip_pool_deletion,
    wait_for_external_ip_pool_grpc_ready,
    wait_for_external_ip_pool_ready,
    wait_for_grpc_subnet_ready,
    wait_for_nat_gateway_deletion,
    wait_for_nat_gateway_ready,
    wait_for_provision,
    wait_for_running,
    wait_for_security_group_cr,
    wait_for_security_group_ready,
    wait_for_subnet_cr,
    wait_for_subnet_ready,
    wait_for_virtual_network_cr,
    wait_for_virtual_network_ready,
    wait_for_vmi_ip,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import poll_until
from tests.e2e.networking.vmaas import guest_ssh
from tests.e2e.networking.vmaas.cleanup import _run_cleanup
from tests.e2e.vmaas.networking_lifecycle_helpers import (
    delete_and_wait_for_security_group,
    delete_and_wait_for_subnet,
    delete_and_wait_for_virtual_network,
)

pytestmark = [pytest.mark.regression, pytest.mark.requires_netris, pytest.mark.requires_netris_vmaas]

_EXTERNAL_PATH_FLAG = "OSAC_NETRIS_VM_EXTERNAL_PATH_ENABLED"
_NETWORKING_SUITE_PREREQUISITE = (
    "Set OSAC_NETRIS_VM_EXTERNAL_PATH_ENABLED=true after the Netris VMaaS ExternalIP/EgressIP "
    "route and return policy is configured"
)


def _network_octets() -> tuple[int, int]:
    worker_id = os.environ.get("PYTEST_XDIST_WORKER", "gw0")
    match = re.fullmatch(r"gw(\d+)", worker_id)
    worker_num = int(match.group(1)) if match else 0
    first_octet = 220 + worker_num * 2
    if first_octet > 252:
        raise RuntimeError(f"Worker {worker_id} has no reserved VMaaS Netris test CIDR")
    return first_octet, first_octet + 1


@pytest.fixture(scope="module")
def netris_vm_network(
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    k8s_virt_client: K8sClient,
    vm_template: str,
    net_test_run_id: str,
    namespace: str,
    tmp_path_factory: pytest.TempPathFactory,
) -> Iterator[dict[str, Any]]:
    """Create three keyed VMs on two isolated one-Subnet VirtualNetworks."""
    worker_octets = _network_octets()
    vn_cidrs = [f"10.{octet}.0.0/16" for octet in worker_octets]
    subnet_cidrs = [f"10.{octet}.1.0/24" for octet in worker_octets]
    temp_dir = tmp_path_factory.mktemp(f"vmaas-netris-{net_test_run_id}")
    private_key = temp_dir / "id_ed25519"
    public_key_file = private_key.with_suffix(".pub")
    subprocess.run(
        ["ssh-keygen", "-t", "ed25519", "-f", str(private_key), "-N", "", "-C", "osac-vmaas-netris-e2e"],
        check=True,
        capture_output=True,
        text=True,
    )

    secret_name = f"netris-vm-key-{net_test_run_id}"
    secret_created = False
    networks: list[dict[str, str]] = []
    instances: list[dict[str, str]] = []
    state: dict[str, Any] = {"identity_file": str(private_key), "instances": instances, "networks": networks}

    try:
        cli.create_secret(
            name=secret_name, secret_type="ssh-public-key", from_files={"public_key": str(public_key_file)}
        )
        secret_created = True

        for index, (vn_cidr, subnet_cidr) in enumerate(zip(vn_cidrs, subnet_cidrs, strict=True), start=1):
            vn_name = f"netris-vn-{index}-{net_test_run_id}"
            vn_id = grpc.create_virtual_network(name=vn_name, ipv4_cidr=vn_cidr)
            network: dict[str, str] = {"vn_id": vn_id, "vn_name": vn_name, "vn_cidr": vn_cidr}
            networks.append(network)
            vn_cr = wait_for_virtual_network_cr(k8s=k8s_hub_client, uuid=vn_id)
            network["vn_cr"] = vn_cr
            wait_for_virtual_network_ready(k8s=k8s_hub_client, name=vn_cr)

            subnet_name = f"netris-subnet-{index}-{net_test_run_id}"
            subnet_id = grpc.create_subnet(name=subnet_name, virtual_network=vn_id, ipv4_cidr=subnet_cidr)
            network.update(subnet_id=subnet_id, subnet_cidr=subnet_cidr)
            subnet_cr = wait_for_subnet_cr(k8s=k8s_hub_client, uuid=subnet_id)
            network["subnet_cr"] = subnet_cr
            wait_for_subnet_ready(k8s=k8s_hub_client, name=subnet_cr)
            wait_for_grpc_subnet_ready(grpc=grpc, subnet_id=subnet_id)

            sg_id = grpc.create_security_group_with_rules(
                name=f"netris-sg-{index}-{net_test_run_id}",
                virtual_network=vn_id,
                ingress=[
                    {"protocol": "PROTOCOL_TCP", "port_from": 22, "port_to": 22, "ipv4_cidr": "0.0.0.0/0"},
                    {"protocol": "PROTOCOL_ICMP", "ipv4_cidr": "0.0.0.0/0"},
                ],
                egress=[{"protocol": "PROTOCOL_ALL", "ipv4_cidr": "0.0.0.0/0"}],
            )
            network["sg_id"] = sg_id
            sg_cr = wait_for_security_group_cr(k8s=k8s_hub_client, uuid=sg_id)
            network["sg_cr"] = sg_cr
            wait_for_security_group_ready(k8s=k8s_hub_client, name=sg_cr)

        for name, network in [
            (f"netris-vm-a1-{net_test_run_id}", networks[0]),
            (f"netris-vm-a2-{net_test_run_id}", networks[0]),
            (f"netris-vm-b1-{net_test_run_id}", networks[1]),
        ]:
            ci_id = cli.create_compute_instance(
                template=vm_template,
                name=name,
                network_attachments=[{"subnet": network["subnet_id"], "security_groups": [network["sg_id"]]}],
                ssh_key=secret_name,
            )
            instance: dict[str, str] = {
                "id": ci_id,
                "name": name,
                "subnet_cidr": network["subnet_cidr"],
                "network": "a" if network is networks[0] else "b",
            }
            instances.append(instance)
            instance["cr"] = wait_for_cr(k8s=k8s_hub_client, uuid=ci_id)

        for instance in instances:
            wait_for_provision(k8s=k8s_hub_client, name=instance["cr"])
            wait_for_running(k8s=k8s_hub_client, name=instance["cr"])
            instance["vm_namespace"] = poll_until(
                fn=lambda vm=instance: k8s_hub_client.get_compute_instance_vm_namespace(name=vm["cr"]),
                until=bool,
                retries=60,
                delay=5,
                description=f"VMI namespace for {instance['name']}",
            )
            instance["ip"] = wait_for_vmi_ip(
                k8s=k8s_virt_client, vmi_namespace=instance["vm_namespace"], compute_instance_name=instance["cr"]
            )
            assert ipaddress.IPv4Address(instance["ip"]) in ipaddress.IPv4Network(instance["subnet_cidr"]), (
                f"ComputeInstance {instance['name']} IP {instance['ip']} is outside {instance['subnet_cidr']}"
            )

        state.update(
            {
                "identity_file": str(private_key),
                "key_secret": secret_name,
                "network_a": networks[0],
                "network_b": networks[1],
                "vm_a1": instances[0],
                "vm_a2": instances[1],
                "vm_b1": instances[2],
            }
        )
        yield state
    finally:
        cleanup_errors: list[str] = []
        for instance in reversed(instances):

            def delete_instance(instance: dict[str, str] = instance) -> None:
                cli.delete_compute_instance(uuid=instance["id"])
                if instance.get("cr"):
                    wait_for_deletion(k8s=k8s_hub_client, name=instance["cr"])
                poll_until(
                    fn=lambda vm=instance: vm["id"] not in grpc.list_compute_instance_ids(),
                    until=bool,
                    retries=30,
                    delay=5,
                    description=f"ComputeInstance {instance['id']} API deletion",
                )

            _run_cleanup(cleanup_errors, f"ComputeInstance {instance['id']}", delete_instance)
        if secret_created:
            _run_cleanup(cleanup_errors, f"SSH key Secret {secret_name}", lambda: cli.delete_secret(name=secret_name))
        for network in reversed(networks):
            if "sg_id" in network:
                _run_cleanup(
                    cleanup_errors,
                    f"SecurityGroup {network['sg_id']}",
                    lambda network=network: delete_and_wait_for_security_group(
                        grpc, k8s_hub_client, network["sg_id"], network.get("sg_cr")
                    ),
                )
            if "subnet_id" in network:
                _run_cleanup(
                    cleanup_errors,
                    f"Subnet {network['subnet_id']}",
                    lambda network=network: delete_and_wait_for_subnet(
                        grpc, k8s_hub_client, network["subnet_id"], network.get("subnet_cr")
                    ),
                )
            _run_cleanup(
                cleanup_errors,
                f"VirtualNetwork {network['vn_id']}",
                lambda network=network: delete_and_wait_for_virtual_network(
                    grpc, k8s_hub_client, network["vn_id"], network.get("vn_cr")
                ),
            )
        if cleanup_errors:
            raise RuntimeError("E2E resource cleanup failed: " + "; ".join(cleanup_errors))


@pytest.fixture
def require_vm_external_path() -> None:
    if os.environ.get(_EXTERNAL_PATH_FLAG, "").strip().lower() != "true":
        pytest.skip(_NETWORKING_SUITE_PREREQUISITE)


@pytest.fixture
def netris_external_ip_pool(
    require_vm_external_path: None, private_grpc: GRPCClient, k8s_hub_client: K8sClient, net_test_run_id: str
) -> Iterator[dict[str, str]]:
    pool_name = f"netris-vm-pool-{net_test_run_id}"
    pool_id = private_grpc.create_external_ip_pool(
        name=pool_name, cidrs=[str(allocate_worker_subnet())], implementation_strategy="netris"
    )
    pool_cr: str | None = None
    try:
        pool_cr = wait_for_external_ip_pool_cr(k8s=k8s_hub_client, uuid=pool_id)
        wait_for_external_ip_pool_ready(k8s=k8s_hub_client, name=pool_cr)
        wait_for_external_ip_pool_grpc_ready(private_grpc=private_grpc, pool_id=pool_id)
        yield {"id": pool_id, "cr": pool_cr}
    finally:
        cleanup_errors: list[str] = []
        _run_cleanup(
            cleanup_errors, f"ExternalIPPool {pool_id}", lambda: private_grpc.delete_external_ip_pool(pool_id=pool_id)
        )

        def wait_for_pool_deletion() -> None:
            pool_cr_name = pool_cr or k8s_hub_client.get_external_ip_pool_name(uuid=pool_id, checked=False)
            if pool_cr_name:
                wait_for_external_ip_pool_deletion(k8s=k8s_hub_client, name=pool_cr_name)

        _run_cleanup(cleanup_errors, f"ExternalIPPool {pool_id} deletion wait", wait_for_pool_deletion)
        if cleanup_errors:
            raise RuntimeError("E2E resource cleanup failed: " + "; ".join(cleanup_errors))


@pytest.fixture
def netris_nat_gateway(
    netris_external_ip_pool: dict[str, str],
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    net_test_run_id: str,
    netris_vm_network: dict[str, Any],
) -> Iterator[None]:
    network = netris_vm_network["network_a"]
    eip_id: str | None = None
    eip_cr: str | None = None
    nat_id: str | None = None
    nat_cr: str | None = None
    try:
        eip_name = f"netris-vm-nat-eip-{net_test_run_id}"
        eip_id = grpc.create_external_ip(name=eip_name, pool=netris_external_ip_pool["id"])
        eip_cr = wait_for_external_ip_cr(k8s=k8s_hub_client, uuid=eip_id)
        wait_for_external_ip_allocated(k8s=k8s_hub_client, name=eip_cr)
        nat_id = grpc.create_nat_gateway(
            name=f"netris-vm-nat-{net_test_run_id}", virtual_network_name=network["vn_name"], external_ip_name=eip_name
        )
        nat_cr = poll_until(
            fn=lambda: k8s_hub_client.get_nat_gateway_name(uuid=nat_id, checked=False),
            until=bool,
            retries=30,
            delay=5,
            description=f"NATGateway CR for {network['vn_name']}",
        )
        wait_for_nat_gateway_ready(k8s=k8s_hub_client, name=nat_cr)
        yield
    finally:
        cleanup_errors: list[str] = []
        if nat_id:

            def delete_nat_gateway() -> None:
                grpc.delete_nat_gateway(nat_gateway_id=nat_id)
                if nat_cr:
                    wait_for_nat_gateway_deletion(k8s=k8s_hub_client, name=nat_cr)

            _run_cleanup(cleanup_errors, f"NATGateway {nat_id}", delete_nat_gateway)
        if eip_id:

            def delete_nat_external_ip() -> None:
                grpc.delete_external_ip(external_ip_id=eip_id)
                if eip_cr:
                    wait_for_external_ip_deletion(k8s=k8s_hub_client, name=eip_cr)

            _run_cleanup(cleanup_errors, f"NAT ExternalIP {eip_id}", delete_nat_external_ip)
        if cleanup_errors:
            raise RuntimeError("E2E resource cleanup failed: " + "; ".join(cleanup_errors))


def _assert_external_ip_ssh(*, vm: dict[str, str], identity_file: str, external_ip: str, description: str) -> None:
    expected_host_key = guest_ssh.guest_host_key(vm["vm_namespace"], vm["cr"], identity_file)

    def try_ssh() -> str:
        try:
            return guest_ssh.ssh_via_external_ip(
                external_ip, identity_file, expected_host_key=expected_host_key, timeout=10
            )
        except (OSError, subprocess.CalledProcessError, subprocess.TimeoutExpired):
            return ""

    hostname = poll_until(fn=try_ssh, until=bool, retries=5, delay=15, description=description)
    assert hostname, f"SSH through ExternalIP {external_ip} did not reach ComputeInstance {vm['name']}"


class TestVmaasNetworking:
    """Ordered connectivity and ExternalIP flows for Netris-backed VMaaS."""

    def test_00_l2_arping_same_subnet(self, netris_vm_network: dict[str, Any]) -> None:
        first = netris_vm_network["vm_a1"]
        second = netris_vm_network["vm_a2"]
        identity_file = netris_vm_network["identity_file"]

        for source, target in ((first, second), (second, first)):
            assert poll_until(
                fn=lambda src=source, dst=target: guest_ssh.arping(
                    src["vm_namespace"], src["cr"], identity_file, dst["ip"]
                ),
                until=bool,
                retries=5,
                delay=10,
                description=f"same-Subnet VMaaS ARP {source['ip']} to {target['ip']}",
            )

    def test_01_l3_ping_same_subnet(self, netris_vm_network: dict[str, Any]) -> None:
        first = netris_vm_network["vm_a1"]
        second = netris_vm_network["vm_a2"]
        identity_file = netris_vm_network["identity_file"]

        for source, target in ((first, second), (second, first)):
            assert poll_until(
                fn=lambda src=source, dst=target: guest_ssh.ping(
                    src["vm_namespace"], src["cr"], identity_file, dst["ip"]
                ),
                until=bool,
                retries=5,
                delay=10,
                description=f"same-Subnet VMaaS ICMP {source['ip']} to {target['ip']}",
            )

    def test_02_l2_arping_cross_virtual_network_fails(self, netris_vm_network: dict[str, Any]) -> None:
        first = netris_vm_network["vm_a1"]
        other_network = netris_vm_network["vm_b1"]
        identity_file = netris_vm_network["identity_file"]

        for source, target in ((first, other_network), (other_network, first)):
            assert not guest_ssh.arping(source["vm_namespace"], source["cr"], identity_file, target["ip"]), (
                f"ARP from VM {source['ip']} to {target['ip']} in another VirtualNetwork succeeded unexpectedly"
            )

    def test_03_l3_ping_cross_virtual_network_fails(self, netris_vm_network: dict[str, Any]) -> None:
        first = netris_vm_network["vm_a1"]
        other_network = netris_vm_network["vm_b1"]
        identity_file = netris_vm_network["identity_file"]

        for source, target in ((first, other_network), (other_network, first)):
            assert not guest_ssh.ping(source["vm_namespace"], source["cr"], identity_file, target["ip"]), (
                f"ICMP from VM {source['ip']} to {target['ip']} in another VirtualNetwork succeeded unexpectedly"
            )

    def test_04_management_endpoint_is_not_reachable(
        self, netris_vm_network: dict[str, Any], mgmt_cluster_ip: str
    ) -> None:
        vm = netris_vm_network["vm_a1"]
        same_subnet_peer = netris_vm_network["vm_a2"]
        identity_file = netris_vm_network["identity_file"]

        assert poll_until(
            fn=lambda: guest_ssh.ping(vm["vm_namespace"], vm["cr"], identity_file, same_subnet_peer["ip"]),
            until=bool,
            retries=5,
            delay=10,
            description=(
                f"positive connectivity control from VM {vm['ip']} to same-Subnet peer {same_subnet_peer['ip']}"
            ),
        )

        assert not guest_ssh.ping(vm["vm_namespace"], vm["cr"], identity_file, mgmt_cluster_ip), (
            f"ICMP from tenant VM {vm['ip']} to management endpoint {mgmt_cluster_ip} succeeded unexpectedly"
        )

    def test_05_nat_gateway_egress(self, netris_vm_network: dict[str, Any], netris_nat_gateway: None) -> None:
        vm = netris_vm_network["vm_a1"]
        assert (
            poll_until(
                fn=lambda: guest_ssh.curl_status(vm["vm_namespace"], vm["cr"], netris_vm_network["identity_file"]),
                until=lambda status: status == 200,
                retries=5,
                delay=15,
                description=f"NAT egress from VM {vm['ip']} to quay.io",
            )
            == 200
        )

    def test_06_manual_external_ip_ingress_and_deletion(
        self,
        netris_vm_network: dict[str, Any],
        netris_external_ip_pool: dict[str, str],
        grpc: GRPCClient,
        k8s_hub_client: K8sClient,
        net_test_run_id: str,
    ) -> None:
        vm = netris_vm_network["vm_a1"]
        eip_id: str | None = None
        eip_cr: str | None = None
        attachment_id: str | None = None
        attachment_cr: str | None = None

        try:
            eip_id = grpc.create_external_ip(
                name=f"netris-vm-manual-eip-{net_test_run_id}", pool=netris_external_ip_pool["id"]
            )
            eip_cr = wait_for_external_ip_cr(k8s=k8s_hub_client, uuid=eip_id)
            wait_for_external_ip_allocated(k8s=k8s_hub_client, name=eip_cr)
            attachment_id = grpc.create_external_ip_attachment(
                name=f"netris-vm-manual-attachment-{net_test_run_id}", external_ip=eip_id, compute_instance=vm["id"]
            )
            attachment_cr = wait_for_external_ip_attachment_cr(k8s=k8s_hub_client, uuid=attachment_id)
            wait_for_external_ip_attachment_ready(k8s=k8s_hub_client, name=attachment_cr)
            address = poll_until(
                fn=lambda: (
                    grpc.get_external_ip(external_ip_id=eip_id).get("object", {}).get("status", {}).get("address", "")
                ),
                until=bool,
                retries=30,
                delay=5,
                description="manually attached ComputeInstance ExternalIP address",
            )
            _assert_external_ip_ssh(
                vm=vm,
                identity_file=netris_vm_network["identity_file"],
                external_ip=address,
                description=f"SSH ingress through manually attached VM ExternalIP {address}",
            )

            grpc.delete_external_ip_attachment(attachment_id=attachment_id)
            wait_for_external_ip_attachment_deletion(k8s=k8s_hub_client, name=attachment_cr)
            poll_until(
                fn=lambda: attachment_id not in grpc.list_external_ip_attachment_ids(),
                until=bool,
                retries=30,
                delay=5,
                description=f"manual ExternalIPAttachment {attachment_id} API deletion",
            )
            attachment_id = None

            grpc.delete_external_ip(external_ip_id=eip_id)
            wait_for_external_ip_deletion(k8s=k8s_hub_client, name=eip_cr)
            poll_until(
                fn=lambda: eip_id not in grpc.list_external_ip_ids(),
                until=bool,
                retries=30,
                delay=5,
                description=f"manual ExternalIP {eip_id} API deletion",
            )
            eip_id = None
        finally:
            cleanup_errors: list[str] = []
            if attachment_id:

                def delete_attachment() -> None:
                    grpc.delete_external_ip_attachment(attachment_id=attachment_id)
                    if attachment_cr:
                        wait_for_external_ip_attachment_deletion(k8s=k8s_hub_client, name=attachment_cr)

                _run_cleanup(cleanup_errors, f"ExternalIPAttachment {attachment_id}", delete_attachment)
            if eip_id:

                def delete_external_ip() -> None:
                    grpc.delete_external_ip(external_ip_id=eip_id)
                    if eip_cr:
                        wait_for_external_ip_deletion(k8s=k8s_hub_client, name=eip_cr)

                _run_cleanup(cleanup_errors, f"ExternalIP {eip_id}", delete_external_ip)
            if cleanup_errors:
                raise RuntimeError("E2E resource cleanup failed: " + "; ".join(cleanup_errors))

    def test_07_automatic_external_ip_ingress_and_compute_instance_deletion(
        self,
        cli: OsacCLI,
        grpc: GRPCClient,
        k8s_hub_client: K8sClient,
        k8s_virt_client: K8sClient,
        vm_template: str,
        net_test_run_id: str,
        netris_vm_network: dict[str, Any],
        netris_external_ip_pool: dict[str, str],
    ) -> None:
        network = netris_vm_network["network_a"]
        vm_name = f"netris-vm-auto-eip-{net_test_run_id}"
        ci_id: str | None = None
        ci_cr: str | None = None
        delete_issued = False
        deletion_verified = False
        attachment_id: str | None = None
        attachment_cr: str | None = None
        eip_id: str | None = None
        eip_cr: str | None = None

        try:
            ci_id = cli.create_compute_instance(
                template=vm_template,
                name=vm_name,
                network_attachments=[{"subnet": network["subnet_id"], "security_groups": [network["sg_id"]]}],
                ssh_key=netris_vm_network["key_secret"],
                external_ip_attachment=True,
            )
            ci_cr = wait_for_cr(k8s=k8s_hub_client, uuid=ci_id)
            wait_for_provision(k8s=k8s_hub_client, name=ci_cr)
            wait_for_running(k8s=k8s_hub_client, name=ci_cr)
            vm_namespace = poll_until(
                fn=lambda: k8s_hub_client.get_compute_instance_vm_namespace(name=ci_cr),
                until=bool,
                retries=60,
                delay=5,
                description=f"VMI namespace for auto-ExternalIP ComputeInstance {vm_name}",
            )
            vm_ip = wait_for_vmi_ip(k8s=k8s_virt_client, vmi_namespace=vm_namespace, compute_instance_name=ci_cr)

            def find_auto_attachment() -> dict[str, Any] | None:
                response = grpc.call(service="osac.public.v1.ExternalIPAttachments/List")
                return next(
                    (
                        item
                        for item in response.get("items", [])
                        if item.get("spec", {}).get("computeInstance", {}).get("id") == ci_id
                    ),
                    None,
                )

            attachment = poll_until(
                fn=find_auto_attachment,
                until=lambda value: value is not None,
                retries=60,
                delay=5,
                description=f"auto-created ExternalIPAttachment for ComputeInstance {vm_name}",
            )
            attachment_id = attachment["id"]
            attachment_cr = wait_for_external_ip_attachment_cr(k8s=k8s_hub_client, uuid=attachment_id)
            wait_for_external_ip_attachment_ready(k8s=k8s_hub_client, name=attachment_cr)
            eip_id = attachment.get("spec", {}).get("externalIp", {}).get("id", "")
            assert eip_id, "Auto-created ExternalIPAttachment has no ExternalIP reference"
            eip_cr = wait_for_external_ip_cr(k8s=k8s_hub_client, uuid=eip_id)
            wait_for_external_ip_allocated(k8s=k8s_hub_client, name=eip_cr)
            address = poll_until(
                fn=lambda: (
                    grpc.get_external_ip(external_ip_id=eip_id).get("object", {}).get("status", {}).get("address", "")
                ),
                until=bool,
                retries=30,
                delay=5,
                description=f"auto-created ExternalIP address for ComputeInstance {vm_name}",
            )
            vm = {"name": vm_name, "cr": ci_cr, "vm_namespace": vm_namespace, "ip": vm_ip}
            _assert_external_ip_ssh(
                vm=vm,
                identity_file=netris_vm_network["identity_file"],
                external_ip=address,
                description=f"SSH ingress through automatically attached VM ExternalIP {address}",
            )

            cli.delete_compute_instance(uuid=ci_id)
            delete_issued = True
            wait_for_deletion(k8s=k8s_hub_client, name=ci_cr)
            poll_until(
                fn=lambda: ci_id not in grpc.list_compute_instance_ids(),
                until=bool,
                retries=30,
                delay=5,
                description=f"auto-ExternalIP ComputeInstance {ci_id} API deletion",
            )
            wait_for_external_ip_attachment_deletion(k8s=k8s_hub_client, name=attachment_cr)
            poll_until(
                fn=lambda: attachment_id not in grpc.list_external_ip_attachment_ids(),
                until=bool,
                retries=30,
                delay=5,
                description=f"auto-created ExternalIPAttachment {attachment_id} garbage collection",
            )
            wait_for_external_ip_deletion(k8s=k8s_hub_client, name=eip_cr)
            poll_until(
                fn=lambda: eip_id not in grpc.list_external_ip_ids(),
                until=bool,
                retries=30,
                delay=5,
                description=f"auto-created ExternalIP {eip_id} garbage collection",
            )
            deletion_verified = True
        finally:
            if ci_id and not deletion_verified:
                cleanup_errors: list[str] = []

                def delete_compute_instance() -> None:
                    if not delete_issued:
                        cli.delete_compute_instance(uuid=ci_id)
                    if ci_cr:
                        wait_for_deletion(k8s=k8s_hub_client, name=ci_cr)
                    poll_until(
                        fn=lambda: ci_id not in grpc.list_compute_instance_ids(),
                        until=bool,
                        retries=30,
                        delay=5,
                        description=f"auto-ExternalIP ComputeInstance {ci_id} cleanup",
                    )

                _run_cleanup(cleanup_errors, f"ComputeInstance {ci_id}", delete_compute_instance)
                if attachment_id and attachment_cr:
                    _run_cleanup(
                        cleanup_errors,
                        f"ExternalIPAttachment {attachment_id}",
                        lambda: wait_for_external_ip_attachment_deletion(k8s=k8s_hub_client, name=attachment_cr),
                    )
                if eip_id and eip_cr:
                    _run_cleanup(
                        cleanup_errors,
                        f"ExternalIP {eip_id}",
                        lambda: wait_for_external_ip_deletion(k8s=k8s_hub_client, name=eip_cr),
                    )
                if cleanup_errors:
                    raise RuntimeError("E2E resource cleanup failed: " + "; ".join(cleanup_errors))
