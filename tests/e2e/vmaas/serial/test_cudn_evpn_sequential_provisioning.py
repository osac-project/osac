"""End-to-end coverage for Phase 1 Netris + cudn_evpn sequential provisioning."""

from __future__ import annotations

import json
from uuid import uuid4

import pytest

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import wait_for_grpc_subnet_ready, wait_for_subnet_cr, wait_for_subnet_ready
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.runner import poll_until
from tests.e2e.vmaas.networking_lifecycle_helpers import (
    create_and_wait_for_subnet,
    create_and_wait_for_virtual_network,
    delete_and_wait_for_subnet,
    delete_and_wait_for_virtual_network,
)

pytestmark = pytest.mark.serial

_EXCLUDED_CIDR_PREFIXES = {199, 200, 209, 210}


def _assert_evpn_netris_profile(private_grpc: GRPCClient) -> None:
    network_classes = private_grpc.list_network_classes()
    if not any(
        network_class.get("fabricManager") == "netris" and network_class.get("k8sManager") == "cudn_evpn"
        for network_class in network_classes
    ):
        pytest.skip("requires the opt-in cudn-evpn-netris-test profile with Netris and cudn_evpn")


def _subnet_jobs(k8s: K8sClient, name: str) -> list[dict]:
    return k8s.get_json(resource="subnet", name=name).get("status", {}).get("provisioningJobs", [])


def _latest_successful_target_job(jobs: list[dict], target: str) -> dict:
    matching = [
        job
        for job in jobs
        if job.get("type") == "provision" and job.get("target") == target and job.get("state") == "Succeeded"
    ]
    assert matching, f"no successful {target} provisioning job found in {jobs!r}"
    return max(matching, key=lambda job: job["timestamp"])


def _wait_for_fabric_before_k8s(k8s: K8sClient, subnet_name: str) -> None:
    """Observe fabric running alone, then require success before K8s dispatch."""
    saw_fabric_active_without_k8s = False

    def observe() -> tuple[list[dict], bool]:
        nonlocal saw_fabric_active_without_k8s
        jobs = _subnet_jobs(k8s, subnet_name)
        fabric_jobs = [job for job in jobs if job.get("type") == "provision" and job.get("target") == "fabric"]
        k8s_jobs = [job for job in jobs if job.get("type") == "provision" and job.get("target") == "k8s"]
        latest_fabric = max(fabric_jobs, key=lambda job: job["timestamp"], default=None)
        fabric_state = latest_fabric.get("state") if latest_fabric else None

        if fabric_state in ("Pending", "Waiting", "Running") and not k8s_jobs:
            saw_fabric_active_without_k8s = True
        if fabric_state in ("Failed", "Canceled"):
            raise AssertionError(f"fabric provisioning failed before K8s dispatch: {latest_fabric!r}")
        if k8s_jobs:
            assert saw_fabric_active_without_k8s, f"did not observe fabric provisioning before K8s dispatch: {jobs!r}"
            assert fabric_state == "Succeeded", (
                f"K8s target appeared before fabric succeeded: fabric={latest_fabric!r}, jobs={jobs!r}"
            )
            return jobs, True
        return jobs, False

    poll_until(
        fn=observe,
        until=lambda snapshot: snapshot[1],
        retries=300,
        delay=1,
        description=f"fabric completion before K8s dispatch for {subnet_name}",
    )


def _cudns_for_virtual_network(k8s: K8sClient, vnet_name: str, subnet_names: set[str]) -> list[dict]:
    matching: list[dict] = []
    cudns = k8s.list_json(resource="clusteruserdefinednetwork", namespace="").get("items", [])
    for cudn in cudns:
        metadata = cudn.get("metadata", {})
        owner_reference = metadata.get("annotations", {}).get("osac.openshift.io/owner-reference", "")
        namespace_labels = cudn.get("spec", {}).get("namespaceSelector", {}).get("matchLabels", {})
        if (
            metadata.get("name") == vnet_name
            or metadata.get("name") in subnet_names
            or owner_reference == f"VirtualNetwork/{vnet_name}"
            or namespace_labels.get("virtual-network") == vnet_name
        ):
            matching.append(cudn)
    return matching


def _create_vm_manifest(name: str, namespace: str, tenant_id: str, subnet_name: str) -> str:
    return f"""\
apiVersion: kubevirt.io/v1
kind: VirtualMachine
metadata:
  name: {name}
  namespace: {namespace}
  annotations:
    osac.openshift.io/tenant: "{tenant_id}"
    osac.openshift.io/owner-reference: "Subnet/{subnet_name}"
spec:
  runStrategy: Halted
  template:
    metadata:
      labels:
        kubevirt.io/vm: {name}
    spec:
      domain:
        devices:
          disks:
            - name: rootdisk
              disk:
                bus: virtio
        resources:
          requests:
            memory: 64Mi
      volumes:
        - name: rootdisk
          containerDisk:
            image: quay.io/containerdisks/fedora:41
"""


def _create_subnet_with_k8s_skip(grpc: GRPCClient, *, name: str, virtual_network_id: str, ipv4_cidr: str) -> str:
    response = grpc.call(
        service="osac.public.v1.Subnets/Create",
        data={
            "object": {
                "metadata": {"name": name, "annotations": {"osac.openshift.io/skip-k8s-manager": "true"}},
                "spec": {"virtual_network": {"id": virtual_network_id}, "ipv4_cidr": ipv4_cidr},
            }
        },
    )
    return response["object"]["id"]


def test_cudn_evpn_provisions_first_subnet_only_and_rejects_second_with_vms(
    grpc: GRPCClient, private_grpc: GRPCClient, k8s_hub_client: K8sClient
) -> None:
    """Verify the fabric-to-K8s sequence, later fabric-only Subnets, and the API VM guard."""
    _assert_evpn_netris_profile(private_grpc)

    run_id = uuid4().hex[:8]
    cidr_prefix = int(run_id[:2], 16) % 254
    while any(cidr_prefix + offset in _EXCLUDED_CIDR_PREFIXES for offset in range(3)):
        cidr_prefix = (cidr_prefix + 1) % 254
    cidr_a = f"10.{cidr_prefix}.0.0/16"
    cidr_b = f"10.{(cidr_prefix + 1) % 256}.0.0/16"
    vnets: list[tuple[str, str]] = []
    subnets: list[tuple[str, str | None]] = []
    vm_name: str | None = None
    vm_namespace: str | None = None

    try:
        # Network A demonstrates the later no-VM Subnet remains fabric-only and
        # does not disturb the first Subnet's CUDN.
        vn_a_id, vn_a_cr = create_and_wait_for_virtual_network(grpc, k8s_hub_client, f"seq-vn-{run_id}-a", cidr_a)
        vnets.append((vn_a_id, vn_a_cr))
        first_a_id = grpc.create_subnet(
            name=f"seq-subnet-{run_id}-a1", virtual_network=vn_a_id, ipv4_cidr=f"10.{cidr_prefix}.1.0/24"
        )
        subnets.append((first_a_id, None))
        first_a_cr = wait_for_subnet_cr(k8s=k8s_hub_client, uuid=first_a_id)
        subnets[-1] = (first_a_id, first_a_cr)
        _wait_for_fabric_before_k8s(k8s_hub_client, first_a_cr)
        wait_for_grpc_subnet_ready(grpc=grpc, subnet_id=first_a_id)

        first_a_jobs = _subnet_jobs(k8s_hub_client, first_a_cr)
        fabric_job = _latest_successful_target_job(first_a_jobs, "fabric")
        k8s_job = _latest_successful_target_job(first_a_jobs, "k8s")
        assert fabric_job["state"] == "Succeeded"
        assert k8s_job["state"] == "Succeeded"

        fabric_output = k8s_hub_client.get_json(
            resource="configmap", name=f"subnet-{first_a_cr}-fabric-output", namespace=k8s_hub_client.namespace
        ).get("data", {})
        for key in ("l2_vni", "l3_vni", "fabric_reserved_range"):
            assert fabric_output.get(key), f"fabric output is missing {key}: {fabric_output!r}"

        first_cudns_before = _cudns_for_virtual_network(k8s_hub_client, vn_a_cr, {first_a_cr})
        assert len(first_cudns_before) == 1, f"expected one first-Subnet CUDN, found {first_cudns_before!r}"
        first_cudn_before = first_cudns_before[0]
        evpn_spec = first_cudn_before.get("spec", {}).get("network", {}).get("evpn", {})
        assert evpn_spec.get("macVRF", {}).get("vni") == int(fabric_output["l2_vni"]), (
            f"CUDN did not receive l2_vni from fabric output: {first_cudn_before!r}"
        )
        assert evpn_spec.get("ipVRF", {}).get("vni") == int(fabric_output["l3_vni"]), (
            f"CUDN did not receive l3_vni from fabric output: {first_cudn_before!r}"
        )
        assert first_cudn_before.get("spec", {}).get("network", {}).get("layer2", {}).get("reservedSubnets", []) == [
            fabric_output["fabric_reserved_range"]
        ]

        second_a_id, second_a_cr = create_and_wait_for_subnet(
            grpc, k8s_hub_client, vn_a_id, f"10.{cidr_prefix}.2.0/24", name_prefix=f"seq-subnet-{run_id}-a2"
        )
        subnets.append((second_a_id, second_a_cr))
        wait_for_grpc_subnet_ready(grpc=grpc, subnet_id=second_a_id)

        second_a_jobs = _subnet_jobs(k8s_hub_client, second_a_cr)
        _latest_successful_target_job(second_a_jobs, "fabric")
        assert not any(job.get("target") == "k8s" for job in second_a_jobs), second_a_jobs
        assert not k8s_hub_client.is_present(resource="namespace", name=second_a_cr, namespace="")
        second_a = k8s_hub_client.get_json(resource="subnet", name=second_a_cr)
        assert second_a.get("metadata", {}).get("annotations", {}).get(
            "osac.openshift.io/k8s-implementation-strategy"
        ) in (None, "")

        explicit_skip_name = f"seq-subnet-{run_id}-a3"
        explicit_skip_id = _create_subnet_with_k8s_skip(
            grpc, name=explicit_skip_name, virtual_network_id=vn_a_id, ipv4_cidr=f"10.{cidr_prefix}.3.0/24"
        )
        subnets.append((explicit_skip_id, None))
        explicit_skip_cr = wait_for_subnet_cr(k8s=k8s_hub_client, uuid=explicit_skip_id)
        subnets[-1] = (explicit_skip_id, explicit_skip_cr)
        wait_for_subnet_ready(k8s=k8s_hub_client, name=explicit_skip_cr)
        wait_for_grpc_subnet_ready(grpc=grpc, subnet_id=explicit_skip_id)
        explicit_skip_jobs = _subnet_jobs(k8s_hub_client, explicit_skip_cr)
        _latest_successful_target_job(explicit_skip_jobs, "fabric")
        assert not any(job.get("target") == "k8s" for job in explicit_skip_jobs), explicit_skip_jobs
        assert not k8s_hub_client.is_present(resource="namespace", name=explicit_skip_cr, namespace="")
        explicit_skip = k8s_hub_client.get_json(resource="subnet", name=explicit_skip_cr)
        assert (
            explicit_skip.get("metadata", {}).get("annotations", {}).get("osac.openshift.io/skip-k8s-manager") == "true"
        )

        first_cudns_after = _cudns_for_virtual_network(
            k8s_hub_client, vn_a_cr, {first_a_cr, second_a_cr, explicit_skip_cr}
        )
        assert [cudn["metadata"]["name"] for cudn in first_cudns_after] == [first_cudn_before["metadata"]["name"]]
        first_cudn_after = first_cudns_after[0]
        assert first_cudn_after["metadata"]["uid"] == first_cudn_before["metadata"]["uid"]
        assert first_cudn_after["spec"] == first_cudn_before["spec"]

        # Isolate the explicit annotation on the first Subnet in another
        # VirtualNetwork so oldest-Subnet auto-detection cannot mask the skip.
        cidr_c_prefix = (cidr_prefix + 2) % 256
        vn_c_id, vn_c_cr = create_and_wait_for_virtual_network(
            grpc, k8s_hub_client, f"seq-vn-{run_id}-c", f"10.{cidr_c_prefix}.0.0/16"
        )
        vnets.append((vn_c_id, vn_c_cr))
        explicit_first_id = _create_subnet_with_k8s_skip(
            grpc, name=f"seq-subnet-{run_id}-c1", virtual_network_id=vn_c_id, ipv4_cidr=f"10.{cidr_c_prefix}.1.0/24"
        )
        subnets.append((explicit_first_id, None))
        explicit_first_cr = wait_for_subnet_cr(k8s=k8s_hub_client, uuid=explicit_first_id)
        subnets[-1] = (explicit_first_id, explicit_first_cr)
        wait_for_subnet_ready(k8s=k8s_hub_client, name=explicit_first_cr)
        wait_for_grpc_subnet_ready(grpc=grpc, subnet_id=explicit_first_id)
        explicit_first_jobs = _subnet_jobs(k8s_hub_client, explicit_first_cr)
        _latest_successful_target_job(explicit_first_jobs, "fabric")
        assert not any(job.get("target") == "k8s" for job in explicit_first_jobs), explicit_first_jobs
        assert not k8s_hub_client.is_present(resource="namespace", name=explicit_first_cr, namespace="")
        assert not _cudns_for_virtual_network(k8s_hub_client, vn_c_cr, {explicit_first_cr})

        # Network B exercises the API rejection on a true second Subnet, with a
        # VM object in the oldest Subnet namespace on the VirtualNetwork hub.
        vn_b_id, vn_b_cr = create_and_wait_for_virtual_network(grpc, k8s_hub_client, f"seq-vn-{run_id}-b", cidr_b)
        vnets.append((vn_b_id, vn_b_cr))
        first_b_id, first_b_cr = create_and_wait_for_subnet(
            grpc, k8s_hub_client, vn_b_id, f"10.{(cidr_prefix + 1) % 256}.1.0/24", name_prefix=f"seq-subnet-{run_id}-b1"
        )
        subnets.append((first_b_id, first_b_cr))
        wait_for_grpc_subnet_ready(grpc=grpc, subnet_id=first_b_id)

        vm_name = f"seq-vm-{run_id}"
        vm_namespace = first_b_cr
        first_b_annotations = (
            k8s_hub_client.get_json(resource="subnet", name=first_b_cr).get("metadata", {}).get("annotations", {})
        )
        tenant_id = first_b_annotations.get("osac.openshift.io/tenant", "")
        assert tenant_id, f"Subnet {first_b_cr} has no tenant isolation annotation"
        k8s_hub_client.apply(manifest=_create_vm_manifest(vm_name, vm_namespace, tenant_id, first_b_cr))

        subnet_ids_before_rejection = set(grpc.list_subnet_ids())
        rejected_subnet_name = f"seq-subnet-{run_id}-b2"
        error_output, return_code = grpc.call_unchecked(
            service="osac.public.v1.Subnets/Create",
            data={
                "object": {
                    "metadata": {"name": rejected_subnet_name},
                    "spec": {"virtual_network": {"id": vn_b_id}, "ipv4_cidr": f"10.{(cidr_prefix + 1) % 256}.2.0/24"},
                }
            },
        )
        if return_code == 0:
            # Preserve cleanup coverage if a regression admits the request.
            created = json.loads(error_output).get("object", {})
            created_id = created.get("id")
            created_name = created.get("metadata", {}).get("name")
            if created_id and created_name:
                subnets.append((created_id, created_name))
        assert return_code != 0, "API unexpectedly admitted a second cudn_evpn Subnet when the first contains a VM"
        assert "FailedPrecondition" in error_output, error_output
        assert first_b_cr in error_output, error_output
        assert "cudn_evpn" in error_output, error_output
        new_subnet_ids = set(grpc.list_subnet_ids()) - subnet_ids_before_rejection
        new_subnet_names = [
            grpc.get_subnet(subnet_id=subnet_id).get("object", {}).get("metadata", {}).get("name", "")
            for subnet_id in new_subnet_ids
        ]
        assert rejected_subnet_name not in new_subnet_names, f"rejected Subnet was persisted: {new_subnet_names!r}"
    finally:
        if (
            vm_name is not None
            and vm_namespace is not None
            and k8s_hub_client.is_present(resource="virtualmachine", name=vm_name, namespace=vm_namespace)
        ):
            k8s_hub_client.delete(resource="virtualmachine", name=vm_name, namespace=vm_namespace)
        for subnet_id, subnet_cr in reversed(subnets):
            delete_and_wait_for_subnet(grpc, k8s_hub_client, subnet_id, subnet_cr)
        for vn_id, vn_cr in reversed(vnets):
            delete_and_wait_for_virtual_network(grpc, k8s_hub_client, vn_id, vn_cr)
