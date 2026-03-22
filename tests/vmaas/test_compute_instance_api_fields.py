from __future__ import annotations

import time

import yaml

from tests.helpers import wait_for_deletion, wait_for_provision, wait_for_running
from tests.k8s_client import K8sClient
from tests.runner import poll_until

TEST_IMAGE_REF: str = "quay.io/containerdisks/fedora:latest"
TEST_CORES: int = 2
TEST_MEMORY_GIB: int = 4
TEST_BOOT_DISK_SIZE: int = 20


def test_compute_instance_api_fields(k8s_hub_client: K8sClient, k8s_virt_client: K8sClient, namespace: str) -> None:
    instance_name: str = f"e2e-test-api-fields-{int(time.time())}"

    manifest: str = yaml.dump(
        {
            "apiVersion": "osac.openshift.io/v1alpha1",
            "kind": "ComputeInstance",
            "metadata": {
                "name": instance_name,
                "namespace": namespace,
                "annotations": {"osac.openshift.io/tenant": namespace},
            },
            "spec": {
                "templateID": "osac.templates.ocp_virt_vm",
                "image": {"sourceType": "registry", "sourceRef": TEST_IMAGE_REF},
                "cores": TEST_CORES,
                "memoryGiB": TEST_MEMORY_GIB,
                "bootDisk": {"sizeGiB": TEST_BOOT_DISK_SIZE},
                "runStrategy": "Always",
            },
        }
    )

    k8s_hub_client.apply(manifest=manifest)

    wait_for_provision(k8s=k8s_hub_client, name=instance_name)
    wait_for_running(k8s=k8s_hub_client, name=instance_name)

    vm_ns: str = k8s_hub_client.get_compute_instance_vm_namespace(name=instance_name)

    # runStrategy mutability: Always -> Halted
    _, rc = k8s_hub_client.patch(
        resource="computeinstance", name=instance_name, patch='{"spec":{"runStrategy":"Halted"}}'
    )
    assert rc == 0, "runStrategy patch to Halted should succeed"

    poll_until(
        fn=lambda: k8s_virt_client.get_vm_printable_status(name=instance_name, vm_namespace=vm_ns, checked=False),
        until=lambda v: v == "Stopped",
        retries=12,
        delay=10,
        description=f"{instance_name} VM stopped",
    )

    vm_strategy: str = k8s_virt_client.get_vm_run_strategy(name=instance_name, vm_namespace=vm_ns)
    vm_status: str = k8s_virt_client.get_vm_printable_status(name=instance_name, vm_namespace=vm_ns, checked=False)
    assert vm_strategy == "Halted", f"VM runStrategy should be Halted, got {vm_strategy}"
    assert vm_status == "Stopped", f"VM should be Stopped, got {vm_status}"

    # runStrategy mutability: Halted -> Always
    _, rc = k8s_hub_client.patch(
        resource="computeinstance", name=instance_name, patch='{"spec":{"runStrategy":"Always"}}'
    )
    assert rc == 0, "runStrategy patch to Always should succeed"

    poll_until(
        fn=lambda: k8s_virt_client.get_vm_printable_status(name=instance_name, vm_namespace=vm_ns, checked=False),
        until=lambda v: v == "Running",
        retries=12,
        delay=10,
        description=f"{instance_name} VM running",
    )

    vm_strategy = k8s_virt_client.get_vm_run_strategy(name=instance_name, vm_namespace=vm_ns)
    vm_status = k8s_virt_client.get_vm_printable_status(name=instance_name, vm_namespace=vm_ns, checked=False)
    assert vm_strategy == "Always", f"VM runStrategy should be Always, got {vm_strategy}"
    assert vm_status == "Running", f"VM should be Running, got {vm_status}"

    # Immutability: cores
    output, rc = k8s_hub_client.patch(
        resource="computeinstance", name=instance_name, patch='{"spec":{"cores":8}}'
    )
    assert rc != 0, "cores field should be immutable"
    assert "cores is immutable" in output, f"Expected immutability error, got: {output}"

    # Immutability: memoryGiB
    output, rc = k8s_hub_client.patch(
        resource="computeinstance", name=instance_name, patch='{"spec":{"memoryGiB":16}}'
    )
    assert rc != 0, "memoryGiB field should be immutable"
    assert "memoryGiB is immutable" in output, f"Expected immutability error, got: {output}"

    # Immutability: image
    output, rc = k8s_hub_client.patch(
        resource="computeinstance",
        name=instance_name,
        patch='{"spec":{"image":{"sourceRef":"quay.io/fedora/fedora:latest"}}}',
    )
    assert rc != 0, "image field should be immutable"
    assert "image is immutable" in output, f"Expected immutability error, got: {output}"

    k8s_hub_client.delete(resource="computeinstance", name=instance_name)
    wait_for_deletion(k8s=k8s_hub_client, name=instance_name)
