from __future__ import annotations

import json
from collections.abc import Iterator
from dataclasses import dataclass
from unittest.mock import Mock

import pytest

from tests.e2e.core import runner
from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.vmaas.regression import test_compute_instance_gpu as gpu_test


@dataclass
class GPUScenario:
    cli: Mock
    grpc: Mock
    private_grpc: Mock
    hub: Mock
    virt: Mock
    elapsed: float = 0

    def sleep(self, seconds: float) -> None:
        self.elapsed += seconds

    def run(self) -> None:
        gpu_test.test_gpu_compute_instance(
            cli=self.cli,
            grpc=self.grpc,
            private_grpc=self.private_grpc,
            k8s_hub_client=self.hub,
            k8s_virt_client=self.virt,
            default_network_attachment={"subnet": "test-subnet"},
            vm_template="test-template",
        )


@pytest.fixture
def gpu_scenario(monkeypatch: pytest.MonkeyPatch) -> Iterator[GPUScenario]:
    scenario = GPUScenario(
        cli=Mock(spec=OsacCLI),
        grpc=Mock(spec=GRPCClient),
        private_grpc=Mock(spec=GRPCClient),
        hub=Mock(spec=K8sClient),
        virt=Mock(spec=K8sClient),
    )
    monkeypatch.setattr(gpu_test, "unique_name", lambda prefix: prefix)
    monkeypatch.setattr(runner.time, "monotonic", lambda: scenario.elapsed)
    monkeypatch.setattr(runner.time, "sleep", scenario.sleep)
    scenario.cli.create_compute_instance.return_value = "ci-id"
    scenario.grpc.list_compute_instance_ids.side_effect = [["ci-id"], []]
    scenario.hub.get_compute_instance_name.return_value = "vm-test"
    scenario.hub.get_compute_instance_phase.return_value = "Starting"
    scenario.hub.get_compute_instance_vm_namespace.return_value = "vm-namespace"
    scenario.hub.get_json.return_value = {
        "spec": {
            "vcpus": gpu_test.GPU_IT_VCPUS,
            "memoryGiB": gpu_test.GPU_IT_MEMORY_GIB,
            "gpu": {
                "pciDeviceSelector": gpu_test.GPU_SPEC["pci_device_selector"],
                "resourceName": gpu_test.GPU_SPEC["resource_name"],
                "count": gpu_test.GPU_SPEC["count"],
            },
        },
        "metadata": {
            "labels": {"osac.openshift.io/instance-type-name": "e2e-gpu-it"},
            "annotations": {"osac.openshift.io/tenant": "test-tenant"},
        },
    }
    scenario.hub.is_present.return_value = False
    scenario.virt.get_vm_host_devices.return_value = json.dumps(
        [{"name": "gpu-0", "deviceName": gpu_test.GPU_SPEC["resource_name"]}]
    )

    yield scenario

    scenario.cli.delete_compute_instance.assert_called_once_with(uuid="ci-id")
    scenario.hub.is_present.assert_called_once_with(resource="computeinstance", name="vm-test")
    assert scenario.grpc.list_compute_instance_ids.call_count == 2
    scenario.private_grpc.delete_instance_type.assert_called_once_with(name="e2e-gpu-it")
    scenario.hub.get_compute_instance_phase.assert_called_once_with(name="vm-test", checked=False)
    scenario.hub.get_compute_instance_condition_status.assert_not_called()


@pytest.mark.parametrize("namespace_delay,device_delay", [(65, 0), (0, 20)])
def test_gpu_spec_waits_for_delayed_resources(
    gpu_scenario: GPUScenario, namespace_delay: int, device_delay: int
) -> None:
    gpu_scenario.hub.get_compute_instance_vm_namespace.side_effect = lambda **_: (
        "vm-namespace" if gpu_scenario.elapsed >= namespace_delay else ""
    )
    devices = gpu_scenario.virt.get_vm_host_devices.return_value
    gpu_scenario.virt.get_vm_host_devices.side_effect = lambda **_: (
        devices if gpu_scenario.elapsed >= namespace_delay + device_delay else ""
    )

    gpu_scenario.run()

    assert gpu_scenario.elapsed == namespace_delay + device_delay
    gpu_scenario.virt.get_vm_host_devices.assert_called_with(name="vm-test", vm_namespace="vm-namespace")


def test_gpu_namespace_timeout_is_bounded(gpu_scenario: GPUScenario) -> None:
    gpu_scenario.hub.get_compute_instance_vm_namespace.return_value = ""

    with pytest.raises(TimeoutError, match="timed out after 595s"):
        gpu_scenario.run()

    assert gpu_scenario.elapsed == 595
    assert gpu_scenario.hub.get_compute_instance_vm_namespace.call_count == 120
    gpu_scenario.virt.get_vm_host_devices.assert_not_called()


def test_gpu_host_devices_timeout_is_bounded(gpu_scenario: GPUScenario) -> None:
    gpu_scenario.virt.get_vm_host_devices.return_value = "[]"

    with pytest.raises(TimeoutError, match="timed out after 295s"):
        gpu_scenario.run()

    assert gpu_scenario.elapsed == 295
    assert gpu_scenario.virt.get_vm_host_devices.call_count == 60


def test_gpu_spec_rejects_wrong_device(gpu_scenario: GPUScenario) -> None:
    gpu_scenario.virt.get_vm_host_devices.return_value = json.dumps([{"deviceName": "wrong-device"}])

    with pytest.raises(AssertionError, match="VM hostDevices should contain"):
        gpu_scenario.run()

    assert gpu_scenario.elapsed == 0
    gpu_scenario.virt.get_vm_host_devices.assert_called_once_with(name="vm-test", vm_namespace="vm-namespace")
