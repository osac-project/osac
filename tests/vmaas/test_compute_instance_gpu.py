from __future__ import annotations

import json
import subprocess
from typing import Any

from tests.catalog.conftest import unique_name
from tests.core.grpc_client import GRPCClient
from tests.core.helpers import wait_for_cr, wait_for_deletion, wait_for_grpc_removal
from tests.core.k8s_client import K8sClient
from tests.core.osac_cli import OsacCLI
from tests.core.runner import poll_until

GPU_IT_CORES: int = 2
GPU_IT_MEMORY_GIB: int = 4
GPU_SPEC: dict[str, Any] = {"pci_device_selector": "10DE:25B6", "resource_name": "nvidia.com/gpu", "count": 1}


def _create_gpu_instance_type(private_grpc: GRPCClient, name: str) -> None:
    private_grpc.create_instance_type(
        name=name, cores=GPU_IT_CORES, memory_gib=GPU_IT_MEMORY_GIB, description="E2E GPU test type", gpu=GPU_SPEC
    )


def _delete_instance_type_safe(private_grpc: GRPCClient, name: str) -> None:
    try:
        private_grpc.delete_instance_type(name=name)
    except subprocess.CalledProcessError as e:
        output = ((e.stdout or "") + (e.stderr or "")).lower()
        if "not found" not in output:
            raise


def _get_vm_host_devices(k8s_virt: K8sClient, *, name: str, vm_namespace: str) -> list[dict[str, Any]]:
    output, rc = k8s_virt._get(
        "get",
        "virtualmachine",
        name,
        "-n",
        vm_namespace,
        "-o",
        "jsonpath={.spec.template.spec.domain.devices.hostDevices}",
        checked=False,
    )
    if rc != 0 or not output.strip():
        return []
    return json.loads(output)


def test_gpu_compute_instance(
    cli: OsacCLI,
    grpc: GRPCClient,
    private_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    k8s_virt_client: K8sClient,
    default_subnet: str,
    vm_template: str,
) -> None:
    # Does not require GPU hardware. Verifies the control plane path: GPU InstanceType
    # → reconciler stamps spec.gpu on the CR → AAP creates the VM with hostDevices.
    # The VM won't reach Running without a GPU node, so we skip wait_for_provision
    # and instead verify the VM spec directly while AAP is still waiting for Ready.
    it_name = unique_name("e2e-gpu-it")
    ci_uuid: str | None = None
    ci_name: str | None = None

    try:
        _create_gpu_instance_type(private_grpc, it_name)

        name = unique_name("e2e-gpu")
        ci_uuid = cli.create_compute_instance(
            name=name, template=vm_template, network_attachments=[{"subnet": default_subnet}], instance_type=it_name
        )
        assert ci_uuid in grpc.list_compute_instance_ids()

        ci_name = wait_for_cr(k8s=k8s_hub_client, uuid=ci_uuid)
        ci_obj: dict[str, Any] = k8s_hub_client.get_json(resource="computeinstance", name=ci_name)
        spec: dict[str, Any] = ci_obj["spec"]

        # Reconciler expands cores and memory from the GPU InstanceType
        assert spec["cores"] == GPU_IT_CORES, (
            f"reconciler should expand cores from GPU instance type: {spec['cores']} != {GPU_IT_CORES}"
        )
        assert spec["memoryGiB"] == GPU_IT_MEMORY_GIB, (
            f"reconciler should expand memory from GPU instance type: {spec['memoryGiB']} != {GPU_IT_MEMORY_GIB}"
        )

        # Reconciler stamps GPU fields from InstanceType onto the CR
        assert "gpu" in spec, f"CR spec should contain gpu field after reconciliation: {spec}"
        gpu: dict[str, Any] = spec["gpu"]
        assert gpu["pciDeviceSelector"] == GPU_SPEC["pci_device_selector"], (
            f"gpu.pciDeviceSelector mismatch: {gpu['pciDeviceSelector']} != {GPU_SPEC['pci_device_selector']}"
        )
        assert gpu["resourceName"] == GPU_SPEC["resource_name"], (
            f"gpu.resourceName mismatch: {gpu['resourceName']} != {GPU_SPEC['resource_name']}"
        )
        assert gpu["count"] == GPU_SPEC["count"], f"gpu.count mismatch: {gpu['count']} != {GPU_SPEC['count']}"

        # Instance type label
        labels: dict[str, str] = ci_obj["metadata"].get("labels", {})
        assert labels.get("osac.openshift.io/instance-type-name") == it_name, (
            f"instance-type-name label mismatch: {labels.get('osac.openshift.io/instance-type-name')!r} != {it_name!r}"
        )

        # Tenant isolation metadata
        annotations: dict[str, str] = ci_obj["metadata"].get("annotations", {})
        assert "osac.openshift.io/tenant" in annotations, (
            f"GPU ComputeInstance CR missing tenant annotation: {annotations}"
        )
        assert annotations["osac.openshift.io/tenant"] != "", "tenant annotation must not be empty"

        # Wait for AAP to trigger provisioning
        poll_until(
            fn=lambda: k8s_hub_client.get_compute_instance_phase(name=ci_name, checked=False),
            until=lambda v: v == "Starting",
            retries=30,
            delay=2,
            description=f"{ci_name} Starting",
        )

        # Wait for VM namespace to be assigned
        vm_ns: str = poll_until(
            fn=lambda: k8s_hub_client.get_compute_instance_vm_namespace(name=ci_name),
            until=lambda v: v != "",
            retries=30,
            delay=2,
            description=f"{ci_name} VM namespace",
        )

        # Wait for AAP to create the VM with GPU hostDevices on the virt cluster
        host_devices: list[dict[str, Any]] = poll_until(
            fn=lambda: _get_vm_host_devices(k8s_virt_client, name=ci_name, vm_namespace=vm_ns),
            until=lambda v: len(v) > 0,
            retries=60,
            delay=5,
            description=f"{ci_name} VM hostDevices",
        )
        assert any(d.get("deviceName") == GPU_SPEC["resource_name"] for d in host_devices), (
            f"VM hostDevices should contain {GPU_SPEC['resource_name']}: {host_devices}"
        )

        # Delete and verify removal
        cli.delete_compute_instance(uuid=ci_uuid)
        wait_for_deletion(k8s=k8s_hub_client, name=ci_name)
        wait_for_grpc_removal(grpc=grpc, uuid=ci_uuid)
        ci_uuid = None
        ci_name = None

    finally:
        if ci_uuid is not None:
            cli.delete_compute_instance(uuid=ci_uuid)
            if ci_name is not None:
                wait_for_deletion(k8s=k8s_hub_client, name=ci_name)
            wait_for_grpc_removal(grpc=grpc, uuid=ci_uuid)
        _delete_instance_type_safe(private_grpc, it_name)
