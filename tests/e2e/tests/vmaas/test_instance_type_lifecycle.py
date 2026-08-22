from __future__ import annotations

import subprocess
from typing import Any
from uuid import uuid4

from tests.core.grpc_client import PRIVATE_API, GRPCClient
from tests.core.osac_cli import OsacCLI

TEST_CORES: int = 4
TEST_MEMORY_GIB: int = 8

TEST_GPU: dict[str, Any] = {"pci_device_selector": "10DE:20B0", "resource_name": "nvidia.com/A100", "count": 1}


def _assert_state_transition(private_grpc: GRPCClient, it_name: str, target_state: str) -> None:
    """Update an InstanceType to *target_state* and verify the transition."""
    private_grpc.update_instance_type(name=it_name, state=target_state)
    response = private_grpc.get_instance_type(name=it_name)
    actual = response["object"]["spec"]["state"]
    assert actual == target_state, f"state transition to {target_state}: {actual} != {target_state}"


def test_instance_type_lifecycle(cli: OsacCLI, private_grpc: GRPCClient) -> None:
    it_name: str = f"e2e-lifecycle-{uuid4().hex[:8]}"

    try:
        # 1. CREATE via private gRPC (admin operation)
        private_grpc.create_instance_type(
            name=it_name, cores=TEST_CORES, memory_gib=TEST_MEMORY_GIB, description="Lifecycle test type"
        )
        names: list[str] = private_grpc.list_instance_type_names()
        assert it_name in names, f"InstanceType {it_name} not found in list after create: {names}"

        # 2. GET via gRPC (verify API fields)
        response: dict = private_grpc.get_instance_type(name=it_name)
        obj: dict = response["object"]
        assert obj["spec"]["cores"] == TEST_CORES, f"spec.cores mismatch: {obj['spec']['cores']} != {TEST_CORES}"
        assert obj["spec"]["memoryGib"] == TEST_MEMORY_GIB, (
            f"spec.memoryGib mismatch: {obj['spec']['memoryGib']} != {TEST_MEMORY_GIB}"
        )
        assert obj["spec"]["state"] == "INSTANCE_TYPE_STATE_ACTIVE", (
            f"spec.state mismatch: {obj['spec']['state']} != INSTANCE_TYPE_STATE_ACTIVE"
        )
        assert obj["metadata"]["name"] == it_name, f"metadata.name mismatch: {obj['metadata']['name']} != {it_name}"

        # Verify CLI describe works
        cli_output = cli.describe_instance_type(name=it_name)
        assert it_name in cli_output, f"CLI describe should show {it_name}: {cli_output}"

        # 3. STATE TRANSITION: ACTIVE -> DEPRECATED
        _assert_state_transition(private_grpc, it_name, "INSTANCE_TYPE_STATE_DEPRECATED")

        # 4. STATE TRANSITION: DEPRECATED -> OBSOLETE
        _assert_state_transition(private_grpc, it_name, "INSTANCE_TYPE_STATE_OBSOLETE")

        # 5. STATE TRANSITION: OBSOLETE -> ACTIVE (reactivation)
        _assert_state_transition(private_grpc, it_name, "INSTANCE_TYPE_STATE_ACTIVE")

        # 6. DELETE via private gRPC (admin operation)
        private_grpc.delete_instance_type(name=it_name)
        names = private_grpc.list_instance_type_names()
        assert it_name not in names, f"InstanceType {it_name} still in list after delete: {names}"

        # 7. NEGATIVE: get after delete should fail
        output, rc = private_grpc.call_unchecked(service=f"{PRIVATE_API}.InstanceTypes/Get", data={"id": it_name})
        assert rc != 0, f"get after delete should fail, but rc={rc}, output: {output}"
        error_lower = output.lower()
        assert any(term in error_lower for term in ["not found", "404", "notfound"]), (
            f"Expected not-found error after delete, got: {output}"
        )

    finally:
        try:
            private_grpc.delete_instance_type(name=it_name)
        except subprocess.CalledProcessError as e:
            output = ((e.stdout or "") + (e.stderr or "")).lower()
            if "not found" not in output:
                raise


def test_create_instance_type_via_cli(private_cli: OsacCLI, private_grpc: GRPCClient) -> None:
    it_name: str = f"e2e-cli-it-{uuid4().hex[:8]}"

    try:
        private_cli.create_instance_type(
            name=it_name,
            cores=TEST_CORES,
            memory_gib=TEST_MEMORY_GIB,
            description="CLI create test type",
            gpu_pci_device_selector=TEST_GPU["pci_device_selector"],
            gpu_resource_name=TEST_GPU["resource_name"],
            gpu_count=TEST_GPU["count"],
        )

        response: dict = private_grpc.get_instance_type(name=it_name)
        spec: dict = response["object"]["spec"]
        assert spec["cores"] == TEST_CORES, f"spec.cores mismatch: {spec['cores']} != {TEST_CORES}"
        assert spec["memoryGib"] == TEST_MEMORY_GIB, (
            f"spec.memoryGib mismatch: {spec['memoryGib']} != {TEST_MEMORY_GIB}"
        )
        assert "gpu" in spec, f"spec.gpu missing from response: {spec}"
        gpu: dict = spec["gpu"]
        assert gpu["pciDeviceSelector"] == TEST_GPU["pci_device_selector"], (
            f"gpu.pciDeviceSelector mismatch: {gpu['pciDeviceSelector']} != {TEST_GPU['pci_device_selector']}"
        )
        assert gpu["resourceName"] == TEST_GPU["resource_name"], (
            f"gpu.resourceName mismatch: {gpu['resourceName']} != {TEST_GPU['resource_name']}"
        )
        assert gpu["count"] == TEST_GPU["count"], f"gpu.count mismatch: {gpu['count']} != {TEST_GPU['count']}"

    finally:
        try:
            private_grpc.delete_instance_type(name=it_name)
        except subprocess.CalledProcessError as e:
            output = ((e.stdout or "") + (e.stderr or "")).lower()
            if "not found" not in output:
                raise


def test_gpu_instance_type(private_grpc: GRPCClient) -> None:
    gpu_name: str = f"e2e-gpu-lifecycle-{uuid4().hex[:8]}"
    nogpu_name: str = f"e2e-nogpu-lifecycle-{uuid4().hex[:8]}"

    try:
        # 1. CREATE GPU-enabled InstanceType and verify GPU fields
        private_grpc.create_instance_type(
            name=gpu_name,
            cores=TEST_CORES,
            memory_gib=TEST_MEMORY_GIB,
            description="GPU lifecycle test type",
            gpu=TEST_GPU,
        )

        response: dict = private_grpc.get_instance_type(name=gpu_name)
        spec: dict = response["object"]["spec"]
        assert "gpu" in spec, f"spec.gpu missing from response: {spec}"
        gpu: dict = spec["gpu"]
        assert gpu["pciDeviceSelector"] == TEST_GPU["pci_device_selector"], (
            f"gpu.pciDeviceSelector mismatch: {gpu['pciDeviceSelector']} != {TEST_GPU['pci_device_selector']}"
        )
        assert gpu["resourceName"] == TEST_GPU["resource_name"], (
            f"gpu.resourceName mismatch: {gpu['resourceName']} != {TEST_GPU['resource_name']}"
        )
        assert gpu["count"] == TEST_GPU["count"], f"gpu.count mismatch: {gpu['count']} != {TEST_GPU['count']}"

        # 2. LIST: GPU types are distinguishable from non-GPU types
        private_grpc.create_instance_type(
            name=nogpu_name, cores=TEST_CORES, memory_gib=TEST_MEMORY_GIB, description="Non-GPU lifecycle test type"
        )

        list_response: dict[str, Any] = private_grpc.call(service=f"{PRIVATE_API}.InstanceTypes/List")
        items: list[dict[str, Any]] = list_response.get("items", [])
        gpu_items = [i for i in items if i["metadata"]["name"] == gpu_name]
        nogpu_items = [i for i in items if i["metadata"]["name"] == nogpu_name]

        assert len(gpu_items) == 1, f"GPU InstanceType {gpu_name} not found in list"
        assert len(nogpu_items) == 1, f"Non-GPU InstanceType {nogpu_name} not found in list"
        assert "gpu" in gpu_items[0]["spec"], f"GPU type should have gpu field: {gpu_items[0]['spec']}"
        assert "gpu" not in nogpu_items[0]["spec"], f"Non-GPU type should not have gpu field: {nogpu_items[0]['spec']}"

        # 3. IMMUTABILITY: update attempt with different GPU is rejected
        output, rc = private_grpc.call_unchecked(
            service=f"{PRIVATE_API}.InstanceTypes/Update",
            data={
                "object": {
                    "id": gpu_name,
                    "spec": {
                        "gpu": {"pci_device_selector": "10DE:2204", "resource_name": "nvidia.com/A10", "count": 2}
                    },
                }
            },
        )
        assert rc != 0, f"update should reject GPU field change, but rc={rc}, output: {output}"
        assert "immutable" in output.lower(), f"Expected immutability error, got: {output}"

        response = private_grpc.get_instance_type(name=gpu_name)
        assert response["object"]["spec"]["gpu"] == gpu, "GPU fields should be unchanged after rejected update"

    finally:
        for name in (gpu_name, nogpu_name):
            try:
                private_grpc.delete_instance_type(name=name)
            except subprocess.CalledProcessError as e:
                output = ((e.stdout or "") + (e.stderr or "")).lower()
                if "not found" not in output:
                    raise
