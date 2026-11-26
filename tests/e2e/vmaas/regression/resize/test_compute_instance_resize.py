"""Resize regressions migrated from the OSAC-4277 Ansible VMaaS suite.

Run with ``uv run pytest -n 0 tests/e2e/vmaas/regression/resize/`` against a
deployed VMaaS stack with Fulfillment, AAP, KubeVirt, storage, and the public
and private API credentials used by the VMaaS fixtures. Each test owns its
ComputeInstance and InstanceTypes and waits for natural VM cleanup.
"""

from __future__ import annotations

import subprocess
import sys
from collections.abc import Callable, Iterator
from pathlib import Path
from typing import Any

import pytest

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import (
    delete_instance_type_if_present,
    unique_name,
    wait_for_cr,
    wait_for_deletion,
    wait_for_grpc_removal,
    wait_for_provision,
    wait_for_running,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.vmaas.sanity.test_compute_instance_instance_type import (
    _condition_status,
    _configuration_snapshot,
    _instance_type_name,
    _next_restart_timestamp,
    _wait_for_configuration_applied,
    _wait_for_resize_staged,
    _wait_for_resized_restart,
    _wait_for_vmi_resources,
)
from tests.e2e.vmaas.sanity.test_compute_instance_instance_type import active_instance_type as active_instance_type
from tests.e2e.vmaas.sanity.test_compute_instance_instance_type import (
    compute_instance_type_editor as compute_instance_type_editor,
)
from tests.e2e.vmaas.sanity.test_compute_instance_instance_type import medium_instance_type as medium_instance_type
from tests.e2e.vmaas.sanity.test_compute_instance_instance_type import (
    running_compute_instance_factory as running_compute_instance_factory,
)

pytestmark = pytest.mark.regression


@pytest.fixture
def mixed_instance_types(private_grpc: GRPCClient) -> Iterator[tuple[str, str]]:
    """Own the 2 CPU/8 GiB and 4 CPU/4 GiB types for TC-FR2-02."""
    source, target = unique_name("resize-mixed-source"), unique_name("resize-mixed-target")
    private_grpc.create_instance_type(name=source, vcpus=2, memory_gib=8)
    try:
        private_grpc.create_instance_type(name=target, vcpus=4, memory_gib=4)
        try:
            yield source, target
        finally:
            delete_instance_type_if_present(grpc=private_grpc, name=target)
    finally:
        delete_instance_type_if_present(grpc=private_grpc, name=source)


@pytest.fixture
def mixed_resize_instance(
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    vm_template: str,
    default_network_attachment: dict[str, Any],
    mixed_instance_types: tuple[str, str],
) -> Iterator[tuple[str, str]]:
    ci_id = cli.create_compute_instance(
        name=unique_name("e2e-resize-mixed"),
        template=vm_template,
        network_attachments=[default_network_attachment],
        instance_type=mixed_instance_types[0],
        run_strategy="Always",
    )
    cr_name: str | None = None
    try:
        cr_name = wait_for_cr(k8s=k8s_hub_client, uuid=ci_id)
        wait_for_provision(k8s=k8s_hub_client, name=cr_name)
        wait_for_running(k8s=k8s_hub_client, name=cr_name)
        _wait_for_configuration_applied(k8s_hub_client, name=cr_name, vcpus=2, memory_gib=8)
        yield ci_id, cr_name
    finally:
        grpc.delete_compute_instance(ci_id=ci_id)
        if cr_name is not None:
            wait_for_deletion(k8s=k8s_hub_client, name=cr_name)
        wait_for_grpc_removal(grpc=grpc, uuid=ci_id)


@pytest.mark.usefixtures("compute_instance_type_editor")
def test_resize_rejects_missing_instance_type_via_cli(
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    active_instance_type: str,
    running_compute_instance_factory: Callable[[str], tuple[str, str]],
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """TC-FR3-03: invalid target is rejected without changing the VM."""
    ci_id, cr_name = running_compute_instance_factory("active")
    before = _configuration_snapshot(k8s_hub_client.get_json(resource="computeinstance", name=cr_name))
    monkeypatch.setenv("OSAC_E2E_TARGET_INSTANCE_TYPE", unique_name("resize-missing"))
    with pytest.raises(subprocess.CalledProcessError) as error:
        cli.edit_compute_instance(uuid=ci_id)
    message = (error.value.stderr or "").lower()
    assert any(term in message for term in ("notfound", "not found", "invalidargument", "invalid argument")), (
        "Expected a missing-instance-type rejection"
    )
    assert _instance_type_name(grpc.get_compute_instance(ci_id=ci_id)) == active_instance_type
    assert _configuration_snapshot(k8s_hub_client.get_json(resource="computeinstance", name=cr_name)) == before


@pytest.mark.usefixtures("compute_instance_type_editor")
def test_resize_to_current_obsolete_type_is_noop_via_cli(
    cli: OsacCLI,
    grpc: GRPCClient,
    private_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    active_instance_type: str,
    medium_instance_type: str,
    running_compute_instance_factory: Callable[[str], tuple[str, str]],
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """TC-FR4-02: an obsolete current type remains valid as a no-op."""
    ci_id, cr_name = running_compute_instance_factory("active")
    private_grpc.update_instance_type(name=active_instance_type, state="INSTANCE_TYPE_STATE_DEPRECATED")
    private_grpc.call(
        service="osac.private.v1.InstanceTypes/Update",
        data={
            "object": {
                "id": active_instance_type,
                "spec": {
                    "deprecation": {
                        "replacement": {"name": medium_instance_type},
                        "obsolescence_timestamp": "2030-01-01T00:00:00Z",
                    }
                },
            },
            "updateMask": {"paths": ["spec.deprecation.replacement", "spec.deprecation.obsolescence_timestamp"]},
        },
    )
    private_grpc.update_instance_type(name=active_instance_type, state="INSTANCE_TYPE_STATE_OBSOLETE")
    before = _configuration_snapshot(k8s_hub_client.get_json(resource="computeinstance", name=cr_name))
    monkeypatch.setenv("OSAC_E2E_TARGET_INSTANCE_TYPE", active_instance_type)
    cli.edit_compute_instance(uuid=ci_id)
    assert _instance_type_name(grpc.get_compute_instance(ci_id=ci_id)) == active_instance_type
    current = k8s_hub_client.get_json(resource="computeinstance", name=cr_name)
    assert _configuration_snapshot(current) == before
    assert _condition_status(current, "ConfigurationApplied") == "True"


@pytest.mark.usefixtures("compute_instance_type_editor")
def test_mixed_direction_resize_and_restart_via_cli(
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    k8s_virt_client: K8sClient,
    mixed_instance_types: tuple[str, str],
    mixed_resize_instance: tuple[str, str],
    monkeypatch: pytest.MonkeyPatch,
    tmp_path: Path,
) -> None:
    """TC-FR2-02: increase CPU while decreasing memory, then restart."""
    ci_id, cr_name = mixed_resize_instance
    vm_namespace = k8s_hub_client.get_compute_instance_vm_namespace(name=cr_name)
    _wait_for_vmi_resources(k8s_virt_client, vmi_namespace=vm_namespace, ci_name=cr_name, vcpus=2, memory_gib=8)
    before = k8s_hub_client.get_json(resource="computeinstance", name=cr_name)
    previous_version = before.get("status", {}).get("desiredConfigVersion", "")
    previous_vmi = k8s_virt_client.get_vmi_creation_timestamp(vmi_namespace=vm_namespace, compute_instance_name=cr_name)
    previous_restart = k8s_hub_client.get_compute_instance_last_restarted_at(name=cr_name)

    monkeypatch.setenv("OSAC_E2E_TARGET_INSTANCE_TYPE", mixed_instance_types[1])
    cli.edit_compute_instance(uuid=ci_id)
    assert _instance_type_name(grpc.get_compute_instance(ci_id=ci_id)) == mixed_instance_types[1]
    _wait_for_resize_staged(
        k8s_hub_client,
        k8s_virt_client,
        ci_name=cr_name,
        vmi_namespace=vm_namespace,
        current_vcpus=2,
        current_memory_gib=8,
        target_vcpus=4,
        target_memory_gib=4,
        previous_config_version=previous_version,
    )

    restart_timestamp = _next_restart_timestamp(previous_restart)
    editor = tmp_path / "restart-editor.py"
    editor.write_text(
        f"#!{sys.executable}\n"
        "import json, os, sys\n"
        "from pathlib import Path\n"
        "path = Path(sys.argv[1])\n"
        "instance = json.loads(path.read_text())\n"
        "instance['spec']['restart_requested_at'] = os.environ['OSAC_E2E_RESTART_AT']\n"
        "path.write_text(json.dumps(instance))\n",
        encoding="utf-8",
    )
    editor.chmod(0o700)
    monkeypatch.setenv("EDITOR", str(editor))
    monkeypatch.setenv("OSAC_E2E_RESTART_AT", restart_timestamp)
    cli.edit_compute_instance(uuid=ci_id)
    _wait_for_resized_restart(
        k8s_hub_client,
        k8s_virt_client,
        ci_name=cr_name,
        vmi_namespace=vm_namespace,
        previous_restart=previous_restart,
        restart_timestamp=restart_timestamp,
        initial_vmi_timestamp=previous_vmi,
        target_vcpus=4,
        target_memory_gib=4,
    )
