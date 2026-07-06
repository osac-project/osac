from __future__ import annotations

import re
import subprocess
from typing import Any, Iterator
from uuid import uuid4

import pytest

from tests.core.grpc_client import GRPCClient
from tests.core.helpers import wait_for_cr, wait_for_deletion
from tests.core.k8s_client import K8sClient
from tests.core.osac_cli import OsacCLI
from tests.core.runner import run_unchecked

IT_CORES: int = 2
IT_MEMORY_GIB: int = 4


def _build_create_ci_args(
    cli: OsacCLI,
    vm_template: str,
    default_subnet: str,
    it_name: str,
    ci_name: str | None = None,
) -> list[str]:
    args = [cli.binary, "--config", cli.config_dir, "create", "computeinstance"]
    if ci_name is not None:
        args += ["--name", ci_name]
    args += [
        "--template", vm_template,
        "--network-attachment", f"subnet={default_subnet}",
        "--instance-type", it_name,
        "--boot-disk-size", "20",
        "--image", "quay.io/containerdisks/fedora:latest",
        "--image-source-type", "registry",
        "--run-strategy", "Always",
    ]
    return args


@pytest.fixture
def active_instance_type(private_grpc: GRPCClient) -> Iterator[str]:
    """Create an ACTIVE instance type for testing; clean up after."""
    it_name = f"e2e-ci-it-{uuid4().hex[:8]}"
    private_grpc.create_instance_type(
        name=it_name,
        cores=IT_CORES,
        memory_gib=IT_MEMORY_GIB,
        description="E2E compute instance test type",
    )
    yield it_name
    try:
        private_grpc.delete_instance_type(name=it_name)
    except subprocess.CalledProcessError as e:
        output = ((e.stdout or "") + (e.stderr or "")).lower()
        if "not found" not in output:
            raise


def test_compute_instance_happy_path(
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    default_subnet: str,
    vm_template: str,
    active_instance_type: str,
) -> None:
    ci_uuid: str | None = None
    ci_name: str | None = None

    try:
        ci_uuid = cli.create_compute_instance(
            template=vm_template,
            network_attachments=[{"subnet": default_subnet}],
            instance_type=active_instance_type,
        )
        assert ci_uuid in grpc.list_compute_instance_ids(), (
            f"ComputeInstance {ci_uuid} not found in list after create"
        )

        # Wait for CR and verify reconciler expansion
        ci_name = wait_for_cr(k8s=k8s_hub_client, uuid=ci_uuid)
        ci_obj: dict[str, Any] = k8s_hub_client.get_json(resource="computeinstance", name=ci_name)
        spec: dict[str, Any] = ci_obj["spec"]
        assert spec["cores"] == IT_CORES, (
            f"E2E-02: reconciler should expand cores from instance type: {spec['cores']} != {IT_CORES}"
        )
        assert spec["memoryGiB"] == IT_MEMORY_GIB, (
            f"E2E-02: reconciler should expand memory from instance type: {spec['memoryGiB']} != {IT_MEMORY_GIB}"
        )

        # Verify osac.openshift.io/instance-type-name label (E2E-03)
        labels: dict[str, str] = ci_obj["metadata"].get("labels", {})
        assert labels.get("osac.openshift.io/instance-type-name") == active_instance_type, (
            f"E2E-03: osac.openshift.io/instance-type-name label mismatch: "
            f"{labels.get('osac.openshift.io/instance-type-name')!r} != {active_instance_type!r}"
        )
    finally:
        if ci_uuid is not None:
            cli.delete_compute_instance(uuid=ci_uuid)
            if ci_name is not None:
                wait_for_deletion(k8s=k8s_hub_client, name=ci_name)


def test_compute_instance_deletion_protection(
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    default_subnet: str,
    vm_template: str,
    active_instance_type: str,
) -> None:
    ci_uuid: str | None = None
    ci_name: str | None = None

    try:
        ci_uuid = cli.create_compute_instance(
            template=vm_template,
            network_attachments=[{"subnet": default_subnet}],
            instance_type=active_instance_type,
        )
        assert ci_uuid in grpc.list_compute_instance_ids(), (
            f"ComputeInstance {ci_uuid} not found in list after create"
        )
        ci_name = wait_for_cr(k8s=k8s_hub_client, uuid=ci_uuid)

        output, rc = run_unchecked(
            cli.binary, "--config", cli.config_dir, "delete", "instancetype", active_instance_type,
        )
        assert rc != 0, "delete should be rejected when ComputeInstance references instance type"
        error_lower = output.lower()
        assert any(term in error_lower for term in [
            "409", "conflict", "referenced", "failedprecondition", "in use",
        ]), f"Expected conflict/reference error, got: {output}"
    finally:
        if ci_uuid is not None:
            cli.delete_compute_instance(uuid=ci_uuid)
            if ci_name is not None:
                wait_for_deletion(k8s=k8s_hub_client, name=ci_name)


def test_compute_instance_deprecated_warning(
    cli: OsacCLI,
    private_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    default_subnet: str,
    vm_template: str,
    active_instance_type: str,
) -> None:
    deprecated_ci_uuid: str | None = None
    deprecated_ci_name: str | None = None

    try:
        private_grpc.update_instance_type(
            name=active_instance_type, state="INSTANCE_TYPE_STATE_DEPRECATED",
        )

        dep_output, dep_rc = run_unchecked(
            *_build_create_ci_args(cli, vm_template, default_subnet, active_instance_type),
        )
        assert dep_rc == 0, f"create with DEPRECATED type should succeed, got: {dep_output}"
        assert "deprecat" in dep_output.lower() or "warning" in dep_output.lower(), (
            f"Expected deprecation warning in output, got: {dep_output}"
        )

        uuid_match: re.Match[str] | None = re.search(r"'([^']+)'", dep_output)
        assert uuid_match is not None, f"Failed to parse UUID from CLI output: {dep_output}"
        deprecated_ci_uuid = uuid_match.group(1)
        deprecated_ci_name = wait_for_cr(k8s=k8s_hub_client, uuid=deprecated_ci_uuid)
    finally:
        if deprecated_ci_uuid is not None:
            cli.delete_compute_instance(uuid=deprecated_ci_uuid)
            if deprecated_ci_name is not None:
                wait_for_deletion(k8s=k8s_hub_client, name=deprecated_ci_name)


def test_compute_instance_nonexistent_instance_type(
    cli: OsacCLI,
    k8s_hub_client: K8sClient,
    default_subnet: str,
    vm_template: str,
) -> None:
    missing_it_name = f"nonexistent-it-{uuid4().hex[:8]}"
    ci_name = f"e2e-neg-{uuid4().hex[:8]}"
    output, rc = run_unchecked(
        *_build_create_ci_args(
            cli, vm_template, default_subnet, missing_it_name, ci_name=ci_name,
        ),
    )
    if rc == 0:
        uuid_match = re.search(r"'([^']+)'", output)
        if uuid_match:
            ci_uuid = uuid_match.group(1)
            cr_name = wait_for_cr(k8s=k8s_hub_client, uuid=ci_uuid)
            cli.delete_compute_instance(uuid=ci_uuid)
            wait_for_deletion(k8s=k8s_hub_client, name=cr_name)
        elif k8s_hub_client.is_present(resource="computeinstance", name=ci_name):
            pytest.fail(
                f"ComputeInstance CR {ci_name} leaked but UUID could not be parsed from output: {output}"
            )
    assert rc != 0, f"create with nonexistent instance type should fail, got: {output}"
    error_lower = output.lower()
    assert any(term in error_lower for term in [
        "not found", "404", "notfound",
    ]), f"Expected not-found error, got: {output}"


def test_compute_instance_obsolete_instance_type(
    cli: OsacCLI,
    private_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    default_subnet: str,
    vm_template: str,
    active_instance_type: str,
) -> None:
    private_grpc.update_instance_type(
        name=active_instance_type, state="INSTANCE_TYPE_STATE_DEPRECATED",
    )
    private_grpc.update_instance_type(
        name=active_instance_type, state="INSTANCE_TYPE_STATE_OBSOLETE",
    )

    ci_name = f"e2e-obs-{uuid4().hex[:8]}"
    output, rc = run_unchecked(
        *_build_create_ci_args(
            cli, vm_template, default_subnet, active_instance_type, ci_name=ci_name,
        ),
    )
    if rc == 0:
        uuid_match = re.search(r"'([^']+)'", output)
        if uuid_match:
            ci_uuid = uuid_match.group(1)
            cr_name = wait_for_cr(k8s=k8s_hub_client, uuid=ci_uuid)
            cli.delete_compute_instance(uuid=ci_uuid)
            wait_for_deletion(k8s=k8s_hub_client, name=cr_name)
        elif k8s_hub_client.is_present(resource="computeinstance", name=ci_name):
            pytest.fail(
                f"ComputeInstance CR {ci_name} leaked but UUID could not be parsed from output: {output}"
            )
    assert rc != 0, f"create with OBSOLETE instance type should be rejected, got: {output}"
    error_lower = output.lower()
    assert any(term in error_lower for term in [
        "obsolete", "rejected", "failedprecondition", "409", "conflict",
    ]), f"Expected rejection error for obsolete instance type, got: {output}"
