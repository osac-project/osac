from __future__ import annotations

import base64
import contextlib
import json
from pathlib import Path
from typing import Any

import pytest

from tests.e2e.core import failure_snapshot
from tests.e2e.core.failures import Kind
from tests.e2e.core.grpc_client import PUBLIC_API, GRPCClient
from tests.e2e.core.helpers import (
    WaitResourceKind,
    assert_no_network_attachment,
    unique_name,
    wait_for_cr,
    wait_for_deletion,
    wait_for_provision,
    wait_for_running,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import run, run_unchecked
from tests.e2e.networking.vmaas.stack import poll_or_fail
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


def test_compute_instance_reads_osac_user_data_secret(
    request: pytest.FixtureRequest,
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    k8s_virt_client: K8sClient,
    default_network_attachment: dict[str, object],
    vm_template: str,
) -> None:
    hostname = unique_name("e2e-userdata")
    secret_name = unique_name("e2e-ci-user-data")
    # Apply the marker in cloud-init's final stage, after early hostname setup.
    cloud_config = f"#cloud-config\nruncmd:\n  - [hostnamectl, set-hostname, {hostname}]\n"
    encoded = base64.b64encode(cloud_config.encode()).decode()
    secret_id = grpc.call(
        service=f"{PUBLIC_API}.Secrets/Create",
        data={
            "object": {
                "metadata": {"name": secret_name},
                "type": "SECRET_TYPE_USER_DATA",
                "data": {"userdata": encoded},
            }
        },
    )["object"]["id"]
    request.addfinalizer(lambda: cli.delete_secret(name=secret_name))

    instance_id: str = cli.create_compute_instance(
        template=vm_template,
        name=unique_name("e2e-ci"),
        network_attachments=[default_network_attachment],
        user_data_secret=secret_name,
    )
    ci_name: str | None = None

    def cleanup_instance() -> None:
        cli.delete_compute_instance(uuid=instance_id)
        if ci_name is not None:
            wait_for_deletion(k8s=k8s_hub_client, name=ci_name)

    request.addfinalizer(cleanup_instance)
    ci_name = wait_for_cr(k8s=k8s_hub_client, uuid=instance_id)
    cr = k8s_hub_client.get_json(resource="computeinstance", name=ci_name)
    assert cr["spec"]["userDataFulfillmentSecretRef"] == {"id": secret_id}
    assert "userDataSecretRef" not in cr["spec"]

    wait_for_provision(k8s=k8s_hub_client, name=ci_name)
    wait_for_running(k8s=k8s_hub_client, name=ci_name)
    staging = run(
        *k8s_hub_client.base_args(),
        "-n",
        cr["metadata"]["namespace"],
        "get",
        "secret",
        f"{instance_id}-user-data",
        "--ignore-not-found",
        "-o",
        "name",
    )
    assert not staging.strip(), "Fulfillment created a management-cluster staging Secret"

    vm_namespace = k8s_hub_client.get_compute_instance_vm_namespace(name=ci_name)
    vm_secret_name = f"{ci_name}-user-data"
    vm_secret = k8s_virt_client.get_json(resource="secret", name=vm_secret_name, namespace=vm_namespace)
    assert vm_secret["data"]["userdata"] == encoded
    vm = k8s_virt_client.get_json(resource="virtualmachine", name=ci_name, namespace=vm_namespace)
    assert any(
        volume.get("cloudInitNoCloud", {}).get("secretRef", {}).get("name") == vm_secret_name
        for volume in vm["spec"]["template"]["spec"]["volumes"]
    )

    guest_path = (
        f"/apis/subresources.kubevirt.io/v1/namespaces/{vm_namespace}"
        f"/virtualmachineinstances/{ci_name}/guestosinfo"
    )
    guest_command = [*k8s_virt_client.base_args(), "get", "--raw", guest_path]

    def snapshot_guest(directory: Path, window: failure_snapshot.Window) -> None:
        output, status = run_unchecked(*guest_command, timeout=10)
        failure_snapshot.write_text(directory, "guestosinfo.txt", f"exit status {status}\n{output}\n")

    snapshot_name = f"guest:{ci_name}"
    failure_snapshot.SOURCES.register(snapshot_name, snapshot_guest)
    request.addfinalizer(lambda: failure_snapshot.SOURCES.unregister(snapshot_name))
    poll_or_fail(
        hint="cloud-init hostname",
        fn=lambda: json.loads(run(*guest_command, timeout=10)).get("hostname", ""),
        until=lambda observed: observed == hostname,
        description=f"guest agent reports the cloud-init hostname for {ci_name}",
        resource_kind=WaitResourceKind.COMPUTE_INSTANCE,
        resource_id=instance_id,
        kind=Kind.STUCK_STATE,
        retries=36,
    )
