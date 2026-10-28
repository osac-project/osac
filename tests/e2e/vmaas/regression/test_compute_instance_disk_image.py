from __future__ import annotations

import re
import subprocess
from datetime import datetime, timedelta

import pytest

from tests.e2e.core.grpc_client import PRIVATE_API, GRPCClient
from tests.e2e.core.helpers import (
    assert_grpc_rejected,
    unique_name,
    wait_for_cr,
    wait_for_deletion,
    wait_for_grpc_removal,
    wait_for_provision,
    wait_for_running,
    wait_for_security_group_cr,
    wait_for_security_group_ready,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.runner import poll_until
from tests.e2e.vmaas.networking_lifecycle_helpers import (
    create_and_wait_for_subnet,
    create_and_wait_for_virtual_network,
    delete_and_wait_for_security_group,
    delete_and_wait_for_subnet,
    delete_and_wait_for_virtual_network,
)

pytestmark = pytest.mark.regression

SOURCE_REF = "quay.io/containerdisks/fedora:41"
DELETION_TEST_FINALIZER = "osac-6513-e2e"


def _set_deletion_test_finalizer(private_grpc: GRPCClient, ci_id: str, *, present: bool) -> None:
    """Hold or release the instance, rereading after concurrent controller updates."""

    def update_finalizers() -> bool:
        object_ = private_grpc.call(service=f"{PRIVATE_API}.ComputeInstances/Get", data={"id": ci_id})["object"]
        metadata = object_["metadata"]
        finalizers = metadata.get("finalizers", [])
        if (DELETION_TEST_FINALIZER in finalizers) == present:
            return True
        if present:
            finalizers = [*finalizers, DELETION_TEST_FINALIZER]
        else:
            finalizers = [value for value in finalizers if value != DELETION_TEST_FINALIZER]
        try:
            private_grpc.call(
                service=f"{PRIVATE_API}.ComputeInstances/Update",
                data={
                    "object": {
                        "id": ci_id,
                        "metadata": {"finalizers": finalizers, "version": metadata.get("version", 0)},
                    },
                    "updateMask": {"paths": ["metadata.finalizers"]},
                    "lock": True,
                },
            )
        except subprocess.CalledProcessError as exc:
            if re.search(r"(?m)^\s*Code:\s*Aborted\s*$", (exc.stderr or "") + "\n" + (exc.stdout or "")):
                return False
            raise
        return True

    poll_until(fn=update_finalizers, until=bool, retries=10, delay=1, description="ComputeInstance test finalizer")


def test_compute_instance_with_disk_image(
    grpc: GRPCClient,
    vm_template: str,
    default_subnet: str,
    default_security_group: str,
    default_instance_type: str,
    k8s_hub_client: K8sClient,
    default_storage_tier: str,
) -> None:
    """AC-1 / TC-FR7-01: Create CI with DiskImage reference, verify VM runs with correct image."""
    di_name = unique_name("e2e-di")
    di_id: str | None = None
    ci_id: str | None = None
    ci_name: str | None = None

    try:
        di_id = grpc.create_disk_image(
            name=di_name,
            source_ref=SOURCE_REF,
            guest_os_family="GUEST_OS_FAMILY_LINUX",
            architecture=["ARCHITECTURE_AMD64"],
        )
        assert di_id, "create_disk_image should return a non-empty ID"

        response = grpc.create_compute_instance_with_disk_image(
            template=vm_template,
            disk_image_name=di_name,
            subnet_ids=[default_subnet],
            security_group_ids=[default_security_group],
            instance_type=default_instance_type,
            name=unique_name("e2e-ci"),
            boot_disk_storage_tier=default_storage_tier,
        )
        ci_id = response["object"]["id"]
        assert ci_id, "create_compute_instance should return a non-empty ID"

        ci_name = wait_for_cr(k8s=k8s_hub_client, uuid=ci_id)
        wait_for_provision(k8s=k8s_hub_client, name=ci_name)
        wait_for_running(k8s=k8s_hub_client, name=ci_name)

        ci_obj = grpc.get_compute_instance(ci_id=ci_id)
        assert ci_obj["object"]["spec"]["diskImage"]["id"] == di_id

        source_ref = k8s_hub_client.get_jsonpath(
            resource="computeinstance", name=ci_name, jsonpath="{.spec.image.sourceRef}"
        )
        assert source_ref == SOURCE_REF, f"CRD sourceRef should be {SOURCE_REF}, got {source_ref}"

        guest_os = k8s_hub_client.get_jsonpath(
            resource="computeinstance", name=ci_name, jsonpath="{.spec.guestOSFamily}"
        )
        assert guest_os == "linux", f"CRD guestOSFamily should be 'linux', got {guest_os}"
    finally:
        if ci_id is not None:
            grpc.delete_compute_instance(ci_id=ci_id)
            if ci_name is not None:
                wait_for_deletion(k8s=k8s_hub_client, name=ci_name)
            wait_for_grpc_removal(grpc=grpc, uuid=ci_id)
        if di_id is not None:
            grpc.delete_disk_image(disk_image_id=di_id)


def test_deletion_feedback_with_removed_disk_image_releases_network(
    grpc: GRPCClient,
    private_grpc: GRPCClient,
    vm_template: str,
    default_instance_type: str,
    default_storage_tier: str,
    k8s_hub_client: K8sClient,
) -> None:
    """A stale image reference cannot block VM feedback or dependent network deletion."""
    vn_id: str | None = None
    vn_cr: str | None = None
    subnet_id: str | None = None
    subnet_cr: str | None = None
    sg_id: str | None = None
    sg_cr: str | None = None
    image_id: str | None = None
    ci_id: str | None = None
    ci_cr: str | None = None
    finalizer_present = False

    try:
        vn_id, vn_cr = create_and_wait_for_virtual_network(
            grpc, k8s_hub_client, unique_name("e2e-deleting-vn"), "10.214.0.0/16"
        )
        subnet_id, subnet_cr = create_and_wait_for_subnet(
            grpc, k8s_hub_client, vn_id, "10.214.1.0/24", name_prefix="e2e-deleting-subnet"
        )
        sg_id = grpc.create_security_group(name=unique_name("e2e-deleting-sg"), virtual_network=vn_id)
        sg_cr = wait_for_security_group_cr(k8s=k8s_hub_client, uuid=sg_id)
        wait_for_security_group_ready(k8s=k8s_hub_client, name=sg_cr)

        image_name = unique_name("e2e-deleting-image")
        image_id = grpc.create_disk_image(name=image_name, source_ref=SOURCE_REF)
        response = grpc.create_compute_instance_with_disk_image(
            template=vm_template,
            disk_image_name=image_name,
            subnet_ids=[subnet_id],
            security_group_ids=[sg_id],
            instance_type=default_instance_type,
            name=unique_name("e2e-deleting-ci"),
            boot_disk_storage_tier=default_storage_tier,
        )
        ci_id = response["object"]["id"]
        ci_cr = wait_for_cr(k8s=k8s_hub_client, uuid=ci_id)
        wait_for_provision(k8s=k8s_hub_client, name=ci_cr)
        wait_for_running(k8s=k8s_hub_client, name=ci_cr)

        _set_deletion_test_finalizer(private_grpc, ci_id, present=True)
        finalizer_present = True
        grpc.delete_compute_instance(ci_id=ci_id)
        grpc.delete_disk_image(disk_image_id=image_id)
        deleted_image_id = image_id
        image_id = None
        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            grpc.get_disk_image(disk_image_id=deleted_image_id)
        assert_grpc_rejected(exc_info, "NotFound")

        # Send deletion feedback with the full object, including its stale image reference.
        feedback = private_grpc.call(service=f"{PRIVATE_API}.ComputeInstances/Get", data={"id": ci_id})["object"]
        assert feedback["metadata"].get("deletionTimestamp"), "ComputeInstance should be deleting"
        assert feedback["spec"]["diskImage"]["id"] == deleted_image_id
        deleting_state = "COMPUTE_INSTANCE_STATE_DELETING"
        status = feedback.setdefault("status", {})
        if status.get("state") != deleting_state:
            transition = datetime.fromisoformat(feedback["metadata"]["deletionTimestamp"].replace("Z", "+00:00"))
            if previous := status.get("stateTransitionTime"):
                previous_time = datetime.fromisoformat(previous.replace("Z", "+00:00"))
                transition = max(transition, previous_time + timedelta(microseconds=1))
            status["state"] = deleting_state
            status["stateTransitionTime"] = transition.isoformat().replace("+00:00", "Z")
        private_grpc.call(
            service=f"{PRIVATE_API}.ComputeInstances/Update",
            data={"object": feedback, "updateMask": {"paths": ["status.state", "status.state_transition_time"]}},
        )
        persisted = private_grpc.call(service=f"{PRIVATE_API}.ComputeInstances/Get", data={"id": ci_id})["object"]
        assert persisted["status"]["state"] == deleting_state
        assert persisted["spec"]["diskImage"]["id"] == deleted_image_id

        _set_deletion_test_finalizer(private_grpc, ci_id, present=False)
        finalizer_present = False
        wait_for_deletion(k8s=k8s_hub_client, name=ci_cr)
        wait_for_grpc_removal(grpc=grpc, uuid=ci_id)
        ci_id = None

        delete_and_wait_for_security_group(grpc, k8s_hub_client, sg_id, sg_cr)
        sg_id = None
        delete_and_wait_for_subnet(grpc, k8s_hub_client, subnet_id, subnet_cr)
        subnet_id = None
        delete_and_wait_for_virtual_network(grpc, k8s_hub_client, vn_id, vn_cr)
        vn_id = None
    finally:
        if finalizer_present and ci_id is not None:
            _set_deletion_test_finalizer(private_grpc, ci_id, present=False)
        if ci_id is not None:
            grpc.delete_compute_instance(ci_id=ci_id)
            if ci_cr is not None:
                wait_for_deletion(k8s=k8s_hub_client, name=ci_cr)
            wait_for_grpc_removal(grpc=grpc, uuid=ci_id)
        if image_id is not None:
            grpc.delete_disk_image(disk_image_id=image_id)
        if sg_id is not None:
            delete_and_wait_for_security_group(grpc, k8s_hub_client, sg_id, sg_cr)
        if subnet_id is not None:
            delete_and_wait_for_subnet(grpc, k8s_hub_client, subnet_id, subnet_cr)
        if vn_id is not None:
            delete_and_wait_for_virtual_network(grpc, k8s_hub_client, vn_id, vn_cr)


def test_disk_image_deletion_protection(
    grpc: GRPCClient,
    vm_template: str,
    default_subnet: str,
    default_security_group: str,
    default_instance_type: str,
    k8s_hub_client: K8sClient,
    default_storage_tier: str,
) -> None:
    """AC-5 / TC-FR12-01 + TC-FR12-04: Deletion protection lifecycle."""
    di_name = unique_name("e2e-di")
    di_id: str | None = None
    ci_id: str | None = None
    ci_name: str | None = None

    try:
        di_id = grpc.create_disk_image(name=di_name, source_ref=SOURCE_REF)

        response = grpc.create_compute_instance_with_disk_image(
            template=vm_template,
            disk_image_name=di_name,
            subnet_ids=[default_subnet],
            security_group_ids=[default_security_group],
            instance_type=default_instance_type,
            name=unique_name("e2e-ci"),
            boot_disk_storage_tier=default_storage_tier,
        )
        ci_id = response["object"]["id"]
        ci_name = wait_for_cr(k8s=k8s_hub_client, uuid=ci_id)

        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            grpc.delete_disk_image(disk_image_id=di_id)
        assert_grpc_rejected(exc_info, "FailedPrecondition")

        grpc.delete_compute_instance(ci_id=ci_id)
        wait_for_deletion(k8s=k8s_hub_client, name=ci_name)
        wait_for_grpc_removal(grpc=grpc, uuid=ci_id)
        ci_id = None

        deleted_di_id = di_id
        grpc.delete_disk_image(disk_image_id=di_id)
        di_id = None

        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            grpc.get_disk_image(disk_image_id=deleted_di_id)
        assert_grpc_rejected(exc_info, "NotFound")
    finally:
        if ci_id is not None:
            grpc.delete_compute_instance(ci_id=ci_id)
            if ci_name is not None:
                wait_for_deletion(k8s=k8s_hub_client, name=ci_name)
            wait_for_grpc_removal(grpc=grpc, uuid=ci_id)
        if di_id is not None:
            grpc.delete_disk_image(disk_image_id=di_id)
