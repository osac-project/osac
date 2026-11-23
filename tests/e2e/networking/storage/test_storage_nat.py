"""Storage over tenant SNAT: VM guest, default network, and per-tenant identity."""

from __future__ import annotations

import ipaddress
import shlex
import tempfile
from functools import partial
from typing import Any

import pytest

from tests.e2e.core.failures import E2EFailure, Kind
from tests.e2e.core.grpc_client import PUBLIC_API, GRPCClient
from tests.e2e.core.helpers import (
    unique_name,
    wait_for_bmh_available,
    wait_for_bmi_cr,
    wait_for_bmi_deletion,
    wait_for_bmi_grpc_removal,
    wait_for_bmi_running,
    wait_for_cluster_deletion,
    wait_for_cluster_order_cr,
    wait_for_cluster_ready,
    wait_for_external_ip_allocated,
    wait_for_external_ip_attachment_cr,
    wait_for_external_ip_attachment_ready,
    wait_for_external_ip_cr,
    wait_for_hosted_cluster_kubeconfig,
    wait_for_hosted_cluster_reference,
    wait_for_nat_gateway_ready,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import poll_until, run
from tests.e2e.core.ssh_probe import ProbeTransportError, probe_connectivity
from tests.e2e.core.wait_kinds import WaitResourceKind
from tests.e2e.networking import cidrs
from tests.e2e.networking.bmaas import bmi_ssh
from tests.e2e.networking.bmaas import guest_ssh as bmaas_guest_ssh
from tests.e2e.networking.storage.endpoint import (
    StorageEndpoint,
    default_ssh_runner,
    ensure_nfs_client_command,
    log_line_count,
    marker_on_server,
    mount_command,
    new_log_lines,
    timeout_mount_command,
    wait_for_client,
    write_probe_file_command,
)
from tests.e2e.networking.teardown import (
    teardown_external_ip,
    teardown_external_ip_attachment,
    teardown_nat,
    teardown_sg,
)
from tests.e2e.networking.vmaas import guest_ssh, stack

pytestmark = [pytest.mark.serial]

MOUNTPOINT = "/mnt/osac-storage"
_ALL = {"protocol": "PROTOCOL_ALL", "ipv4_cidr": "0.0.0.0/0"}
_TCP_22 = {"protocol": "PROTOCOL_TCP", "port_from": 22, "port_to": 22, "ipv4_cidr": "0.0.0.0/0"}
_TCP_2222 = {"protocol": "PROTOCOL_TCP", "port_from": 2222, "port_to": 2222, "ipv4_cidr": "0.0.0.0/0"}
_PROBE_PORT = 2222
_SSH_TRANSPORT_FAILURE = 255


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
    # A second mount would reuse the existing NFS connection and produce no
    # new SYN, so the server log would have no new source address. Unmount
    # first; the test's evidence is the new connection.
    command = (
        f"sudo umount {MOUNTPOINT} 2>/dev/null || true; "
        f"{mount_command(endpoint, MOUNTPOINT)}"
        f" && {write_probe_file_command(MOUNTPOINT, marker)}"
    )
    output, status = _run(vm, command, identity, timeout=120)
    assert status == 0, f"NFS mount failed on {vm['name']}: {output!r}"
    assert marker in output, f"probe file missing on {vm['name']}: {output!r}"
    assert marker in marker_on_server(endpoint, marker), f"probe file {marker} is not on the export"
    return output


def _assert_no_storage_path(vm: dict[str, str], endpoint: StorageEndpoint, identity: str) -> None:
    """Prove the mount path times out; an SSH or setup failure is not a network result."""
    output, status = _run(vm, "command -v mount.nfs", identity, timeout=30)
    assert status == 0 and output.strip(), f"mount.nfs is missing on {vm['name']}: {output!r}"
    expected = ("timed out", "no route to host", "network is unreachable", "connection timed out", "host unreachable")
    for attempt in (1, 2):
        output, status = _run(vm, timeout_mount_command(endpoint, MOUNTPOINT), identity, timeout=120)
        assert status != _SSH_TRANSPORT_FAILURE, (
            f"SSH to {vm['name']} failed during mount attempt {attempt}; this is not a no-storage-path result: "
            f"{output!r}"
        )
        if status == 124:
            continue
        lowered = output.lower()
        if status != 0 and any(marker in lowered for marker in expected):
            continue
        pytest.fail(f"mount attempt {attempt} without a NATGateway did not time out (rc={status}): {output!r}")


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
    storage_endpoint: StorageEndpoint,
    storage_identity_file: str,
    jwt_grpc_tenant2_admin: GRPCClient,
) -> None:
    """TC-FR6-01: a newly onboarded tenant reaches storage with no networking resource created by the test."""
    vm = storage_vms["tenant2"]
    nat = _default_nat(jwt_grpc_tenant2_admin)
    since = log_line_count(storage_endpoint)
    marker = f"storage-nat-{vm['id']}"

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

    assert seen == [ipaddress.IPv4Address(nat1), ipaddress.IPv4Address(nat2)]


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
        _assert_no_storage_path(vm, storage_endpoint, storage_identity_file)

        new_lines = new_log_lines(storage_endpoint, since)
        assert not any(str(vm["ip"]) in line for line in new_lines), (
            f"the storage server logged the tenant address {vm['ip']} even though the VN has no NATGateway"
        )
    finally:
        # Each step is independent: one failed delete must neither mask the test
        # failure nor leave the network behind for the next case.
        errors: list[str] = []
        stack.run_stage(
            [
                (
                    f"ComputeInstance {vm['id']}",
                    partial(stack.delete_vm, client=grpc, k8s_hub_client=k8s_hub_client, instance=vm),
                )
                for vm in list(instances)
            ],
            errors=errors,
        )
        stack.run_stage(
            [
                (
                    f"VirtualNetwork {network.get('vnet_name', network['vnet_id'])}",
                    partial(stack.delete_network, client=grpc, k8s_hub_client=k8s_hub_client, network=network),
                )
                for network in list(networks)
            ],
            errors=errors,
        )
        for error in errors:
            print(f"WARNING: no-NAT storage cleanup failed: {error}")


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
        # The tenant comes from the Cluster API, not a hardcoded annotation: the
        # CLI identity owns the ClusterOrder and its tenant must match it.
        cluster_tenant = grpc.get_cluster(cluster_id=cluster_uuid)["object"]["metadata"]["tenant"]
        cluster_order = k8s_hub_client.get_json(resource="clusterorder", name=co_name)
        assert cluster_order["metadata"]["annotations"].get("osac.openshift.io/tenant") == cluster_tenant, (
            f"ClusterOrder {co_name} tenant annotation does not match Cluster tenant {cluster_tenant}"
        )
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
  storageClassName: ""
  volumeMode: Filesystem
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
  storageClassName: ""
  volumeMode: Filesystem
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
            # Record the log position before the workload exists: kubelet mounts
            # the NFS volume during PVC binding, before the pod's exec step.
            since = log_line_count(storage_endpoint)
            workload.apply(manifest=manifest)
            poll_until(
                fn=lambda: workload.get_jsonpath(
                    resource="persistentvolumeclaim", name=pvc_name, jsonpath="{.status.phase}"
                ),
                until=lambda phase: phase == "Bound",
                retries=60,
                delay=5,
                description=f"PVC {pvc_name} to bind",
                resource_kind=WaitResourceKind.PERSISTENT_VOLUME_CLAIM,
                resource_id=pvc_name,
            )
            poll_until(
                fn=lambda: workload.get_jsonpath(resource="pod", name=pod_name, jsonpath="{.status.phase}"),
                until=lambda phase: phase == "Running",
                retries=60,
                delay=5,
                description=f"pod {pod_name} to run",
                resource_kind=WaitResourceKind.POD,
                resource_id=pod_name,
            )
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
    bmh_namespace: str,
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
    pool: dict[str, Any] = {}
    network: dict[str, str] = {}
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
        stack.assert_pool_routed(external_ip_pool_cidr)
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
            resource_kind=WaitResourceKind.NAT_GATEWAY,
            resource_id=nat_id,
        )
        wait_for_nat_gateway_ready(k8s=k8s_hub_client, name=nat_cr)

        bmi_id, _ = cli.create_baremetal_instance(
            name=f"storage-nat-bmi-{net_test_run_id}",
            catalog_item=catalog_item_name,
            instance_type=bmi_instance_type or None,
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
            resource_kind=WaitResourceKind.BARE_METAL_INSTANCE,
            resource_id=bmi["cr"],
        )
        bmh = k8s_hub_client.get_baremetal_instance_external_host_id(name=bmi["cr"]).split("/", 1)[1]
        bmi["bmh"] = bmh
        bmi["ssh_host"] = bmi_ssh.get_ssh_host(bmh, bmh_ssh_hosts)
        bmi_ssh.wait_for_ssh_ready([bmi])

        since = log_line_count(storage_endpoint)
        marker = f"storage-nat-{bmi_id}"
        # A second mount would reuse the existing connection and log no new
        # source. Unmount first, and fail on the mount itself instead of hiding
        # it behind `mountpoint -q`.
        command = (
            f"{ensure_nfs_client_command()}; "
            f"sudo umount {MOUNTPOINT} 2>/dev/null || true; "
            f"{mount_command(storage_endpoint, MOUNTPOINT)}"
            f" && {write_probe_file_command(MOUNTPOINT, marker)}"
        )
        output, status = bmaas_guest_ssh.ssh_unchecked(bmi["ssh_host"], command, timeout=300)
        assert status == 0, f"NFS mount failed on BMaaS host {bmi['name']}: {output!r}"
        assert marker in output, f"probe file missing on BMaaS host {bmi['name']}: {output!r}"
        assert marker in marker_on_server(storage_endpoint, marker), f"probe file {marker} is not on the export"
        seen = wait_for_client(storage_endpoint, address, since=since)
        assert seen == ipaddress.IPv4Address(address)
    finally:
        errors: list[str] = []
        for bmi in list(instances):
            try:
                cli.delete_baremetal_instance(uuid=bmi["id"])
                if bmi.get("cr"):
                    wait_for_bmi_deletion(k8s=k8s_hub_client, name=bmi["cr"])
                wait_for_bmi_grpc_removal(grpc=grpc, uuid=bmi["id"])
                if bmi.get("bmh"):
                    wait_for_bmh_available(k8s=k8s_hub_client, name=bmi["bmh"], bmh_namespace=bmh_namespace)
            except Exception as exc:
                errors.append(f"BareMetalInstance {bmi['id']}: {type(exc).__name__}: {exc}")
        if nat_id:
            try:
                teardown_nat(grpc, k8s_hub_client, {"nat_id": nat_id, "vnet_name": network.get("vnet_name", "")})
            except Exception as exc:
                errors.append(f"NATGateway {nat_id}: {type(exc).__name__}: {exc}")
        if eip_id:
            try:
                teardown_external_ip(grpc, k8s_hub_client, eip_id, "storage BMaaS NAT")
            except Exception as exc:
                errors.append(f"ExternalIP {eip_id}: {type(exc).__name__}: {exc}")
        if network:
            try:
                stack.delete_network(client=grpc, k8s_hub_client=k8s_hub_client, network=network)
            except Exception as exc:
                errors.append(f"VirtualNetwork {network.get('vnet_id', '')}: {type(exc).__name__}: {exc}")
        if pool.get("owned"):
            try:
                private_grpc.delete_external_ip_pool(pool_id=pool["id"])
                poll_until(
                    fn=lambda: pool["id"] not in private_grpc.list_external_ip_pool_ids(),
                    until=bool,
                    retries=60,
                    delay=5,
                    description=f"ExternalIPPool {pool['id']} gone",
                    resource_kind=WaitResourceKind.EXTERNAL_IP_POOL,
                    resource_id=pool["id"],
                )
            except Exception as exc:
                errors.append(f"ExternalIPPool {pool['id']}: {type(exc).__name__}: {exc}")
        for error in errors:
            print(f"WARNING: BMaaS storage cleanup failed: {error}")


def _storage_tcp_open(endpoint: StorageEndpoint, host: str, timeout: int = 8) -> bool:
    """Connect from the storage server; a dead SSH channel raises instead of looking blocked."""
    command = f"nc -z -w {int(timeout)} {shlex.quote(host)} {_PROBE_PORT}"
    try:
        return probe_connectivity(
            endpoint.ssh_host,
            command,
            lambda wrapped, call_timeout: default_ssh_runner(endpoint, wrapped, call_timeout),
            transport="SSH",
            timeout=timeout + 20,
        )
    except ProbeTransportError as exc:
        raise E2EFailure(
            Kind.TEST_SSH_FAILED, f"SSH to the storage server {endpoint.ssh_host} failed during the TCP probe: {exc}"
        ) from exc


def _require_storage_nc(endpoint: StorageEndpoint) -> None:
    output, status = default_ssh_runner(endpoint, "command -v nc", 30)
    assert status == 0 and output.strip(), f"the storage server has no nc to probe with: {output!r}"


@pytest.mark.requires_vmaas
def test_nfr2_01_storage_reaches_only_exposed_workload(
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    k8s_virt_client: K8sClient,
    tenant1_default_network: dict[str, str],
    storage_vms: dict[str, dict[str, str]],
    storage_key_secrets: dict[str, str],
    storage_endpoint: StorageEndpoint,
    storage_identity_file: str,
    vm_template: str,
    default_instance_type: str,
    default_disk_image: str,
    default_storage_tier: str,
    net_test_run_id: str,
) -> None:
    """TC-NFR2-01: only a workload exposed on purpose is reachable from storage."""
    first = storage_vms["tenant1"]
    instances: list[dict[str, str]] = []
    sg_network = dict(tenant1_default_network)
    attachment_id = ""
    try:
        assert stack.find_auto_attachment(grpc, first["id"]) is None, (
            f"the first VM {first['name']} must have no ExternalIPAttachment"
        )
        stack.create_security_group(
            client=grpc,
            k8s_hub_client=k8s_hub_client,
            network=sg_network,
            key="nfr2",
            name=f"storage-nfr2-sg-{net_test_run_id}",
            ingress=[_TCP_22, _TCP_2222],
            egress=[_ALL],
        )
        second = stack.create_vm(
            cli=cli,
            k8s_hub_client=k8s_hub_client,
            instances=instances,
            name=f"storage-nfr2-vm-{net_test_run_id}",
            network=tenant1_default_network,
            sg_id=sg_network["nfr2_sg_id"],
            key_secret=storage_key_secrets["tenant1"],
            vm_template=vm_template,
            instance_type=default_instance_type,
            disk_image=default_disk_image,
            storage_tier=default_storage_tier,
            external_ip_attachment=True,
        )
        stack.wait_for_vm(
            k8s_hub_client=k8s_hub_client,
            k8s_virt_client=k8s_virt_client,
            instance=second,
            identity_file=storage_identity_file,
        )
        for vm in (first, second):
            problem = guest_ssh.login_problem(vm["vm_namespace"], vm["cr"], storage_identity_file)
            assert problem is None, f"cannot log in to {vm['name']}; the probe result would be meaningless: {problem}"

        attachment = poll_until(
            fn=lambda: stack.find_auto_attachment(grpc, second["id"]),
            until=lambda item: item is not None,
            retries=60,
            delay=5,
            description=f"ExternalIPAttachment for {second['name']}",
            resource_kind=WaitResourceKind.EXTERNAL_IP_ATTACHMENT,
            resource_id=second["id"],
        )
        assert attachment is not None
        attachment_id = attachment["id"]
        attach_cr = wait_for_external_ip_attachment_cr(k8s=k8s_hub_client, uuid=attachment_id)
        wait_for_external_ip_attachment_ready(k8s=k8s_hub_client, name=attach_cr)
        eip_id = attachment.get("spec", {}).get("externalIp", {}).get("id", "")
        assert eip_id, f"ExternalIPAttachment {attachment_id} has no ExternalIP reference"
        external_address = stack.external_ip_address(grpc, eip_id)
        nat = _default_nat(grpc)

        guest_ssh.start_tcp_listener(first["vm_namespace"], first["cr"], storage_identity_file, _PROBE_PORT)
        guest_ssh.start_tcp_listener(second["vm_namespace"], second["cr"], storage_identity_file, _PROBE_PORT)
        _require_storage_nc(storage_endpoint)

        assert not _storage_tcp_open(storage_endpoint, nat), (
            f"the storage server reached {nat}:{_PROBE_PORT}, the NAT address, so the NATGateway allows inbound"
        )
        assert not _storage_tcp_open(storage_endpoint, first["ip"]), (
            f"the storage server reached tenant address {first['ip']}:{_PROBE_PORT} without an ExternalIPAttachment"
        )
        poll_until(
            fn=lambda: _storage_tcp_open(storage_endpoint, external_address),
            until=bool,
            retries=12,
            delay=5,
            description=f"storage server reach to {external_address}:{_PROBE_PORT}",
            resource_kind=WaitResourceKind.EXTERNAL_IP,
            resource_id=eip_id,
        )
    finally:
        errors: list[str] = []
        if attachment_id:
            try:
                teardown_external_ip_attachment(grpc, k8s_hub_client, attachment_id, "NFR-2 exposed VM")
            except Exception as exc:
                errors.append(f"ExternalIPAttachment {attachment_id}: {type(exc).__name__}: {exc}")
        for vm in list(instances):
            try:
                stack.delete_vm(client=grpc, k8s_hub_client=k8s_hub_client, instance=vm)
            except Exception as exc:
                errors.append(f"ComputeInstance {vm['id']}: {type(exc).__name__}: {exc}")
        if sg_network.get("nfr2_sg_id"):
            try:
                teardown_sg(
                    grpc,
                    k8s_hub_client,
                    {"sg_id": sg_network["nfr2_sg_id"], "vnet_name": sg_network.get("vnet_name", "")},
                )
            except Exception as exc:
                errors.append(f"SecurityGroup {sg_network['nfr2_sg_id']}: {type(exc).__name__}: {exc}")
        for error in errors:
            print(f"WARNING: NFR-2 storage cleanup failed: {error}")
