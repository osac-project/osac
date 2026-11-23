"""Storage over tenant SNAT: VM guest, default network, and per-tenant identity."""

from __future__ import annotations

import ipaddress
import json
import tempfile

import pytest

from tests.e2e.core.grpc_client import PUBLIC_API, GRPCClient
from tests.e2e.core.helpers import (
    unique_name,
    wait_for_bmi_cr,
    wait_for_bmi_running,
    wait_for_cluster_deletion,
    wait_for_cluster_order_cr,
    wait_for_cluster_ready,
    wait_for_external_ip_allocated,
    wait_for_external_ip_cr,
    wait_for_hosted_cluster_kubeconfig,
    wait_for_hosted_cluster_reference,
    wait_for_nat_gateway_ready,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import poll_until, run
from tests.e2e.networking import cidrs
from tests.e2e.networking.bmaas import bmi_ssh
from tests.e2e.networking.bmaas import guest_ssh as bmaas_guest_ssh
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


@pytest.mark.requires_vmaas
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


@pytest.mark.requires_vmaas
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


@pytest.mark.requires_vmaas
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


@pytest.mark.requires_vmaas
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


@pytest.mark.requires_caas
def test_fr5_03_caas_pvc_mounts_through_nat(
    cli: OsacCLI,
    grpc: GRPCClient,
    private_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    cluster_template: str,
    pull_secret_name: str,
    ssh_key_secret: str,
    storage_endpoint: StorageEndpoint,
    net_test_run_id: str,
) -> None:
    """TC-FR5-03: a CaaS PVC mounts through the Cluster's NATGateway.

    OSAC has no generic NFS StorageBackend, so this uses a static NFS PV on the
    lab endpoint. It proves the worker-node NAT path and the source address; it
    does not claim OSAC-managed dynamic NFS (OSAC-7835).
    """
    cluster_uuid = ""
    co_name = ""
    nat = _default_nat(grpc)
    try:
        private_grpc.ensure_bare_metal_instance_type(name="ci-worker-bm")
        cluster_uuid = cli.create_cluster(
            name=unique_name("storage-nat-caas"),
            template=cluster_template,
            node_sets={"workers": {"size": 1, "baremetal_instance_type": {"name": "ci-worker-bm"}}},
            pull_secret=pull_secret_name,
            ssh_key=ssh_key_secret,
        )
        co_name = wait_for_cluster_order_cr(k8s=k8s_hub_client, uuid=cluster_uuid)
        _, rc = k8s_hub_client.patch(
            resource="clusterorder",
            name=co_name,
            patch=json.dumps({"metadata": {"annotations": {"osac.openshift.io/tenant": "tenant1"}}}),
        )
        assert rc == 0, f"could not associate ClusterOrder {co_name} with tenant1"
        wait_for_cluster_ready(k8s=k8s_hub_client, name=co_name)
        hosted_name, hosted_ns = wait_for_hosted_cluster_reference(k8s=k8s_hub_client, co_name=co_name)
        kubeconfig = wait_for_hosted_cluster_kubeconfig(
            k8s=k8s_hub_client, hosted_cluster_namespace=hosted_ns, hosted_cluster_name=hosted_name
        )

        namespace = f"storage-nat-{net_test_run_id}"
        pv_name = f"storage-nat-pv-{net_test_run_id}"
        pvc_name = "storage-nat-pvc"
        pod_name = "storage-nat-pod"
        marker = f"storage-nat-{net_test_run_id}"
        manifest = f"""apiVersion: v1
kind: PersistentVolume
metadata:
  name: {pv_name}
spec:
  capacity:
    storage: 1Gi
  accessModes: [ReadWriteMany]
  nfs:
    server: {storage_endpoint.server}
    path: {storage_endpoint.export}
  mountOptions: [vers=4.1, noresvport]
  persistentVolumeReclaimPolicy: Retain
---
apiVersion: v1
kind: Namespace
metadata:
  name: {namespace}
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: {pvc_name}
  namespace: {namespace}
spec:
  accessModes: [ReadWriteMany]
  resources:
    requests:
      storage: 1Gi
  volumeName: {pv_name}
---
apiVersion: v1
kind: Pod
metadata:
  name: {pod_name}
  namespace: {namespace}
spec:
  containers:
  - name: writer
    image: quay.io/centos/centos:stream9
    command: ["sh", "-c", "sleep 3600"]
    volumeMounts:
    - name: data
      mountPath: /mnt/osac-storage
  volumes:
  - name: data
    persistentVolumeClaim:
      claimName: {pvc_name}
"""
        with tempfile.NamedTemporaryFile(prefix="storage-nat-", suffix=".kubeconfig") as kubeconfig_file:
            kubeconfig_file.write(kubeconfig)
            kubeconfig_file.flush()
            workload = K8sClient(namespace=namespace, kubeconfig=kubeconfig_file.name, as_system_admin=False)
            workload.apply(manifest=manifest)
            poll_until(
                fn=lambda: workload.get_jsonpath(
                    resource="persistentvolumeclaim", name=pvc_name, jsonpath="{.status.phase}"
                ),
                until=lambda phase: phase == "Bound",
                retries=60,
                delay=5,
                description=f"PVC {pvc_name} to bind",
            )
            poll_until(
                fn=lambda: workload.get_jsonpath(resource="pod", name=pod_name, jsonpath="{.status.phase}"),
                until=lambda phase: phase == "Running",
                retries=60,
                delay=5,
                description=f"pod {pod_name} to run",
            )
            since = log_line_count(storage_endpoint)
            run(
                "kubectl",
                "--kubeconfig",
                kubeconfig_file.name,
                "-n",
                namespace,
                "exec",
                pod_name,
                "--",
                "sh",
                "-c",
                f"echo {marker} > /mnt/osac-storage/{marker}",
                timeout=120,
            )
            address = wait_for_client(storage_endpoint, nat, since=since)
            assert address == ipaddress.IPv4Address(nat), f"CaaS worker source {address}, expected {nat}"
    finally:
        if cluster_uuid:
            try:
                cli.delete_cluster(uuid=cluster_uuid)
                wait_for_cluster_deletion(k8s=k8s_hub_client, name=co_name)
            except Exception as exc:  # keep the original failure visible
                print(f"WARNING: CaaS cleanup failed: {exc}")


@pytest.mark.requires_bmaas
def test_fr5_04_bmaas_host_mounts_nfs(
    cli: OsacCLI,
    grpc: GRPCClient,
    private_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    external_ip_pool_name: str,
    external_ip_pool_cidr: str,
    bmh_ssh_hosts: dict[str, str],
    bmi_network_interface: str,
    bmi_disk_image: str,
    catalog_item_name: str,
    bmi_instance_type: str,
    net_ssh_key_secret: str,
    bmi_user_data: str,
    storage_endpoint: StorageEndpoint,
    net_test_run_id: str,
) -> None:
    """TC-FR5-04: a BMaaS host mounts NFS through its VirtualNetwork NATGateway."""
    networks: list[dict[str, str]] = []
    instances: list[dict[str, str]] = []
    nat_id = ""
    eip_id = ""
    vn_cidr = cidrs.tenant_vn_cidr(12, offset=cidrs.run_offset(net_test_run_id))
    try:
        pool = stack.resolve_external_ip_pool(
            private_grpc=private_grpc,
            k8s_hub_client=k8s_hub_client,
            name=external_ip_pool_name,
            cidr=external_ip_pool_cidr,
        )
        network = stack.create_network(
            client=grpc,
            k8s_hub_client=k8s_hub_client,
            networks=networks,
            label="storage-bmaas",
            vn_cidr=vn_cidr,
            subnet_cidr=cidrs.subnet_cidr(vn_cidr, 1),
            run_id=net_test_run_id,
            ingress=[_ALL],
            egress=[_ALL],
        )
        eip_id = grpc.create_external_ip(name=f"storage-nat-eip-{net_test_run_id}", pool=pool["id"])
        eip_cr = wait_for_external_ip_cr(k8s=k8s_hub_client, uuid=eip_id)
        wait_for_external_ip_allocated(k8s=k8s_hub_client, name=eip_cr)
        address = stack.wait_for_grpc_external_ip_allocated(grpc, eip_id)
        nat_id = grpc.create_nat_gateway(
            name=f"storage-nat-{net_test_run_id}", virtual_network_id=network["vnet_id"], external_ip_id=eip_id
        )
        nat_cr = poll_until(
            fn=lambda: k8s_hub_client.get_nat_gateway_name(uuid=nat_id, checked=False),
            until=bool,
            retries=30,
            delay=5,
            description=f"NATGateway CR for {nat_id}",
        )
        wait_for_nat_gateway_ready(k8s=k8s_hub_client, name=nat_cr)

        bmi_id, _ = cli.create_baremetal_instance(
            name=f"storage-nat-bmi-{net_test_run_id}",
            catalog_item=catalog_item_name,
            instance_type=bmi_instance_type,
            ssh_key=net_ssh_key_secret,
            disk_image=bmi_disk_image,
            user_data=bmi_user_data,
            network_attachments=[
                f"subnet={network['sub_a_id']},interface={bmi_network_interface},primary,"
                f"security-groups={network['sg_id']}"
            ],
        )
        bmi = {"id": bmi_id, "name": f"storage-nat-bmi-{net_test_run_id}"}
        instances.append(bmi)
        bmi["cr"] = wait_for_bmi_cr(k8s=k8s_hub_client, uuid=bmi_id)
        wait_for_bmi_running(grpc=grpc, bmi_id=bmi_id, retries=240)
        bmi["ip"] = poll_until(
            fn=lambda: k8s_hub_client.get_baremetal_instance_tenant_ip(name=bmi["cr"]),
            until=bool,
            retries=60,
            delay=10,
            description=f"BMI {bmi['name']} tenant address",
        )
        bmh = k8s_hub_client.get_baremetal_instance_external_host_id(name=bmi["cr"]).split("/", 1)[1]
        bmi["ssh_host"] = bmi_ssh.get_ssh_host(bmh, bmh_ssh_hosts)
        bmi_ssh.wait_for_ssh_ready([bmi])

        since = log_line_count(storage_endpoint)
        output, status = bmaas_guest_ssh.ssh_unchecked(
            bmi["ssh_host"],
            f"{ensure_nfs_client_command()} && {mount_command(storage_endpoint, MOUNTPOINT)}"
            f" || mountpoint -q {MOUNTPOINT} && {write_probe_file_command(MOUNTPOINT, f'storage-nat-{bmi_id}')}",
            timeout=300,
        )
        assert status == 0, f"NFS mount failed on BMaaS host {bmi['name']}: {output!r}"
        seen = wait_for_client(storage_endpoint, address, since=since)
        assert seen == ipaddress.IPv4Address(address)
    finally:
        for bmi in list(instances):
            try:
                cli.delete_baremetal_instance(uuid=bmi["id"])
            except Exception as exc:
                print(f"WARNING: BMI cleanup failed: {exc}")
        if nat_id:
            try:
                grpc.delete_nat_gateway(nat_gateway_id=nat_id)
            except Exception as exc:
                print(f"WARNING: NAT cleanup failed: {exc}")
        if eip_id:
            try:
                grpc.delete_external_ip(external_ip_id=eip_id)
            except Exception as exc:
                print(f"WARNING: ExternalIP cleanup failed: {exc}")
        for network in list(networks):
            try:
                stack.delete_network(client=grpc, k8s_hub_client=k8s_hub_client, network=network)
            except Exception as exc:
                print(f"WARNING: network cleanup failed: {exc}")
