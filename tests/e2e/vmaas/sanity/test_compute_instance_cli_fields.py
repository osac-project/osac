from __future__ import annotations

import base64
import contextlib
from typing import Any

import pytest

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import (
    assert_no_network_attachment,
    unique_name,
    wait_for_cr,
    wait_for_deletion,
    wait_for_provision,
    wait_for_running,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.vmaas.conftest import DEFAULT_IT_MEMORY_GIB, DEFAULT_IT_VCPUS

pytestmark = pytest.mark.sanity

TEST_BOOT_DISK_SIZE: int = 20
TEST_RUN_STRATEGY: str = "Always"
TEST_USER_DATA: str = "#cloud-config\npackages:\n  - vim\n"


def test_compute_instance_cli_explicit_fields(
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    default_network_attachment: dict[str, object],
    vm_template: str,
    networking_provisioning_enabled: bool,
) -> None:
    name = unique_name("e2e-ci")
    uuid: str = cli.create_compute_instance(
        template=vm_template,
        name=name,
        network_attachments=[default_network_attachment],
        boot_disk_size=TEST_BOOT_DISK_SIZE,
        run_strategy=TEST_RUN_STRATEGY,
        user_data_secret_ref=TEST_USER_DATA,
    )

    ci_name: str | None = None

    def cleanup() -> None:
        cli.delete_compute_instance(uuid=uuid)
        if ci_name is not None:
            wait_for_deletion(k8s=k8s_hub_client, name=ci_name)

    try:
        assert uuid in grpc.list_compute_instance_ids()

        ci_name = wait_for_cr(k8s=k8s_hub_client, uuid=uuid)

        ci_spec: dict[str, Any] = k8s_hub_client.get_json(resource="computeinstance", name=ci_name)
        spec: dict[str, Any] = ci_spec["spec"]
        assert spec["vcpus"] == DEFAULT_IT_VCPUS, f"vCPUs mismatch: {spec['vcpus']} != {DEFAULT_IT_VCPUS}"
        assert spec["memoryGiB"] == DEFAULT_IT_MEMORY_GIB, (
            f"memoryGiB mismatch: {spec['memoryGiB']} != {DEFAULT_IT_MEMORY_GIB}"
        )

        # Verify osac.openshift.io/instance-type-name label is set by reconciler
        labels: dict[str, str] = ci_spec["metadata"].get("labels", {})
        it_label: str | None = labels.get("osac.openshift.io/instance-type-name")
        assert it_label is not None, f"osac.openshift.io/instance-type-name label should be set, got: {it_label!r}"
        assert len(it_label) > 0, f"osac.openshift.io/instance-type-name label should be non-empty, got: {it_label!r}"

        assert spec["bootDisk"]["sizeGiB"] == TEST_BOOT_DISK_SIZE, (
            f"bootDisk.sizeGiB mismatch: {spec['bootDisk']['sizeGiB']} != {TEST_BOOT_DISK_SIZE}"
        )
        assert spec["runStrategy"] == TEST_RUN_STRATEGY, f"runStrategy mismatch: {spec['runStrategy']}"

        expected_secret_name: str = f"{uuid}-user-data"
        assert spec["userDataSecretRef"]["name"] == expected_secret_name, (
            f"userDataSecretRef.name mismatch: {spec['userDataSecretRef']['name']} != {expected_secret_name}"
        )

        secret: dict[str, Any] = k8s_hub_client.get_json(resource="secret", name=expected_secret_name)
        expected_b64: str = base64.b64encode(TEST_USER_DATA.encode()).decode()
        assert secret["data"]["userdata"] == expected_b64, "User data Secret content does not match"

        owner_refs: list[dict[str, Any]] = secret["metadata"]["ownerReferences"]
        assert len(owner_refs) > 0, "Secret missing ownerReferences"
        assert owner_refs[0]["kind"] == "ComputeInstance"
        assert owner_refs[0]["name"] == ci_name

        wait_for_provision(k8s=k8s_hub_client, name=ci_name)
        wait_for_running(k8s=k8s_hub_client, name=ci_name)

        if not networking_provisioning_enabled:
            # The workload runs on the disabled fallback and claims no provider work:
            # no attachment, no attachment status, and no attachment jobs exist.
            network_ready = k8s_hub_client.get_condition(
                resource="computeinstance", name=ci_name, condition_type="NetworkReady"
            )
            assert network_ready.get("status") == "True", (
                f"NetworkReady must be True on the disabled fallback, got {network_ready}"
            )
            assert network_ready.get("reason") == "ProvisioningDisabled", (
                f"NetworkReady reason {network_ready.get('reason')!r}, expected 'ProvisioningDisabled': {network_ready}"
            )
            ci_status: dict[str, Any] = k8s_hub_client.get_json(resource="computeinstance", name=ci_name)["status"]
            assert not ci_status.get("computeNetworkAttachmentStatuses"), (
                f"provider networking is disabled, but {ci_name} reports attachment statuses "
                f"{ci_status.get('computeNetworkAttachmentStatuses')}"
            )
            assert_no_network_attachment(k8s=k8s_hub_client, target_kind="ComputeInstance", target_id=uuid)
    except BaseException:
        with contextlib.suppress(Exception):
            cleanup()
        raise
    else:
        cleanup()
