from __future__ import annotations

import json
import subprocess
from collections.abc import Callable
from typing import Any, ClassVar

import pytest

from tests.e2e.bmaas.regression.networking import bmi_ssh
from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import (
    assert_grpc_rejected,
    wait_for_bmh_available,
    wait_for_bmi_cr,
    wait_for_bmi_deletion,
    wait_for_bmi_grpc_removal,
    wait_for_bmi_running,
    wait_for_external_ip_allocated,
    wait_for_external_ip_attachment_cr,
    wait_for_external_ip_attachment_deletion,
    wait_for_external_ip_attachment_ready,
    wait_for_external_ip_cr,
    wait_for_external_ip_deletion,
    wait_for_external_ip_pool_cr,
    wait_for_external_ip_pool_deletion,
    wait_for_external_ip_pool_grpc_ready,
    wait_for_external_ip_pool_ready,
    wait_for_grpc_subnet_ready,
    wait_for_nat_gateway_deletion,
    wait_for_security_group_cr,
    wait_for_security_group_deletion,
    wait_for_security_group_ready,
    wait_for_subnet_cr,
    wait_for_subnet_deletion,
    wait_for_subnet_ready,
    wait_for_virtual_network_cr,
    wait_for_virtual_network_deletion,
    wait_for_virtual_network_ready,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import poll_until

pytestmark = pytest.mark.regression

_NETRIS_BMI_RUNNING_RETRIES = 240
_PROVISIONING_REPLAY_RETRIES = 180
_PROVISIONING_REPLAY_DELAY = 2
_DELETE_REPLAY_RETRIES = 180
_DELETE_REPLAY_DELAY = 5
_DELETE_OBSERVER_FINALIZER = "e2e.osac.openshift.io/delete-observer"
_UUID_LABELS = {
    "externalip": "osac.openshift.io/externalip-uuid",
    "externalipattachment": "osac.openshift.io/externalipattachment-uuid",
    "externalippool": "osac.openshift.io/externalippool-uuid",
    "natgateway": "osac.openshift.io/natgateway-uuid",
    "securitygroup": "osac.openshift.io/securitygroup-uuid",
    "subnet": "osac.openshift.io/subnet-uuid",
    "virtualnetwork": "osac.openshift.io/virtualnetwork-uuid",
}
_DELETE_FINALIZERS = {
    "externalip": "osac.openshift.io/externalip-finalizer",
    "externalipattachment": "osac.openshift.io/externalipattachment-finalizer",
    "externalippool": "osac.openshift.io/externalippool-finalizer",
    "natgateway": "osac.openshift.io/natgateway-finalizer",
    "securitygroup": "osac.openshift.io/securitygroup-finalizer",
    "subnet": "osac.openshift.io/subnet-finalizer",
    "virtualnetwork": "osac.openshift.io/virtualnetwork-finalizer",
    "baremetalinstance": "osac.openshift.io/baremetalinstance-networking",
}
_REPLAY_STABLE_STATUS_FIELDS = {
    "externalippool": ("total", "allocated", "available"),
    "externalip": ("address", "state", "attached", "stateTransitionTime", "attachmentTransitionTime"),
}


def _require(state: dict[str, Any], *keys: str) -> None:
    missing = [k for k in keys if k not in state]
    if missing:
        pytest.skip(f"Prerequisite state missing: {', '.join(missing)}")


def _latest_jobs(cr: dict[str, Any], *, status_field: str, job_type: str) -> dict[str, dict[str, Any]]:
    latest: dict[str, dict[str, Any]] = {}
    for job in cr.get("status", {}).get(status_field, []):
        if job.get("type") != job_type:
            continue
        target = str(job.get("target", ""))
        if target not in latest or str(job.get("timestamp", "")) > str(latest[target].get("timestamp", "")):
            latest[target] = job
    return latest


def _latest_provision_jobs(cr: dict[str, Any]) -> dict[str, dict[str, Any]]:
    return _latest_jobs(cr, status_field="provisioningJobs", job_type="provision")


def _set_finalizers(k8s: K8sClient, *, resource: str, name: str, finalizers: list[str]) -> None:
    output, return_code = k8s.patch(
        resource=resource, name=name, patch=json.dumps({"metadata": {"finalizers": finalizers}})
    )
    assert return_code == 0, f"Failed to patch {resource}/{name} finalizers: {output}"


def _release_delete_observer(k8s: K8sClient, *, resource: str, name: str) -> None:
    if not k8s.is_present(resource=resource, name=name):
        return
    cr = k8s.get_json(resource=resource, name=name)
    finalizers = cr.get("metadata", {}).get("finalizers", [])
    if _DELETE_OBSERVER_FINALIZER in finalizers:
        _set_finalizers(
            k8s,
            resource=resource,
            name=name,
            finalizers=[item for item in finalizers if item != _DELETE_OBSERVER_FINALIZER],
        )


def _wait_for_deprovision_jobs(
    *, k8s: K8sClient, resource: str, name: str, status_field: str, owner_finalizer: str
) -> dict[str, dict[str, Any]]:
    failed_states = {"Failed", "Canceled"}

    def _deprovision_finished(snapshot: dict[str, Any]) -> bool:
        latest = _latest_jobs(snapshot, status_field=status_field, job_type="deprovision")
        if not latest:
            return False
        if any(job.get("state") in failed_states for job in latest.values()):
            return True

        # The controller removes this finalizer after successful AAP
        # deprovisioning. Its final status update may not be persisted before
        # the finalizer removal, so the job can still appear Pending or Running.
        finalizers = snapshot.get("metadata", {}).get("finalizers", [])
        return owner_finalizer not in finalizers

    snapshot = poll_until(
        fn=lambda: k8s.get_json(resource=resource, name=name),
        until=_deprovision_finished,
        retries=_DELETE_REPLAY_RETRIES,
        delay=_DELETE_REPLAY_DELAY,
        description=f"{resource}/{name} Netris deprovision job completion",
    )
    jobs = _latest_jobs(snapshot, status_field=status_field, job_type="deprovision")
    assert jobs, f"{resource}/{name} deletion completed without a recorded deprovision job"
    assert all(job.get("jobID") for job in jobs.values()), f"{resource}/{name} has a deprovision job without an ID"
    assert all(job.get("blockDeletionOnFailure") is True for job in jobs.values()), (
        f"{resource}/{name} deprovision jobs do not block deletion on failure: {jobs}"
    )
    failed_jobs = {target: job for target, job in jobs.items() if job.get("state") in failed_states}
    assert not failed_jobs, f"{resource}/{name} Netris deprovision failed: {failed_jobs}"
    assert owner_finalizer not in snapshot.get("metadata", {}).get("finalizers", []), (
        f"{resource}/{name} deprovision succeeded but its controller finalizer remains"
    )
    return jobs


def _delete_netris_resource_and_verify(
    *,
    k8s: K8sClient,
    resource: str,
    name: str,
    delete: Callable[[], None],
    status_field: str = "provisioningJobs",
    require_netris_annotation: bool = True,
) -> None:
    """Delete a Netris-backed CR and assert its AAP deprovision job succeeds.

    A test-only finalizer holds the terminating CR long enough to observe the
    successful AAP job and verify the operator removes its own finalizer only
    after deprovisioning. The observer finalizer is removed even if an assertion
    fails. Some provider-already-absent handling has focused component coverage;
    this E2E does not force a second deletion reconcile after deletion begins.
    """
    owner_finalizer = _DELETE_FINALIZERS[resource]
    before = k8s.get_json(resource=resource, name=name)
    before_metadata = before.get("metadata", {})
    original_finalizers = list(before_metadata.get("finalizers", []))
    assert owner_finalizer in original_finalizers, f"{resource}/{name} lacks {owner_finalizer} before deletion"
    if require_netris_annotation:
        annotations = before_metadata.get("annotations", {})
        assert annotations.get("osac.openshift.io/implementation-strategy") == "netris", (
            f"{resource}/{name} is not provisioned by Netris"
        )
    create_jobs = _latest_jobs(before, status_field=status_field, job_type="provision")
    assert create_jobs and all(job.get("state") == "Succeeded" for job in create_jobs.values()), (
        f"{resource}/{name} has no successful create job history: {create_jobs}"
    )
    create_job_ids = {str(job.get("jobID", "")) for job in create_jobs.values()}
    assert create_job_ids and "" not in create_job_ids, f"{resource}/{name} has no successful create job history"
    assert _DELETE_OBSERVER_FINALIZER not in original_finalizers
    _set_finalizers(k8s, resource=resource, name=name, finalizers=[*original_finalizers, _DELETE_OBSERVER_FINALIZER])

    try:
        delete()
        deprovision_jobs = _wait_for_deprovision_jobs(
            k8s=k8s, resource=resource, name=name, status_field=status_field, owner_finalizer=owner_finalizer
        )
        assert all(str(job["jobID"]) not in create_job_ids for job in deprovision_jobs.values()), (
            f"{resource}/{name} reused a create job ID for deprovisioning: {deprovision_jobs}"
        )
    finally:
        _release_delete_observer(k8s, resource=resource, name=name)


def _replay_netris_create(*, k8s: K8sClient, resource: str, name: str, uuid: str) -> None:
    """Re-run the deployed create path against an already-created Netris object.

    Clearing only this test-owned CR's completed job history makes its normal
    operator reconcile dispatch the same AAP create operation again. The CR's
    spec and Kubernetes identity remain intact; the provider replay exercises
    the Netris create operation's idempotency contract.
    """
    before = k8s.get_json(resource=resource, name=name)
    before_status = before.get("status", {})
    before_jobs = _latest_provision_jobs(before)
    assert before_status.get("phase") == "Ready", f"{resource}/{name} is not Ready before replay"
    annotations = before.get("metadata", {}).get("annotations") or {}
    assert annotations.get("osac.openshift.io/implementation-strategy") == "netris", (
        f"{resource}/{name} is not provisioned by Netris"
    )
    assert before_jobs, f"{resource}/{name} has no completed provision jobs to replay"
    assert all(job.get("state") == "Succeeded" for job in before_jobs.values()), (
        f"{resource}/{name} has an unsuccessful provision job before replay: {before_jobs}"
    )
    desired_config_version = before_status.get("desiredConfigVersion")
    assert desired_config_version, f"{resource}/{name} has no desired config version"
    assert all(job.get("configVersion") == desired_config_version for job in before_jobs.values()), (
        f"{resource}/{name} has a provision job for a different config version: {before_jobs}"
    )
    old_job_ids = {target: str(job.get("jobID", "")) for target, job in before_jobs.items()}
    assert all(old_job_ids.values()), f"{resource}/{name} has a provision job without an ID"
    before_metadata = before.get("metadata", {})
    uid = before_metadata.get("uid")
    assert uid, f"{resource}/{name} has no Kubernetes UID"
    assert before_metadata.get("labels", {}).get(_UUID_LABELS[resource]) == uuid, (
        f"{resource}/{name} does not carry fulfillment UUID {uuid}"
    )
    stable_status = {
        field: before_status[field]
        for field in _REPLAY_STABLE_STATUS_FIELDS.get(resource, ())
        if field in before_status
    }
    if resource == "externalip":
        assert before_status.get("state") == "Allocated", f"{resource}/{name} is not Allocated before replay"
        assert before_status.get("address"), f"{resource}/{name} has no allocated address before replay"

    output, return_code = k8s.patch_status(
        resource=resource, name=name, patch=json.dumps({"status": {"provisioningJobs": []}})
    )
    assert return_code == 0, f"Failed to reset test-owned {resource}/{name} job history: {output}"

    def _resource_snapshot() -> dict[str, Any]:
        return k8s.get_json(resource=resource, name=name)

    def _replay_finished(snapshot: dict[str, Any]) -> bool:
        latest = _latest_provision_jobs(snapshot)
        terminal_states = {"Succeeded", "Failed", "Canceled"}
        all_targets_terminal = True
        for target, old_job_id in old_job_ids.items():
            job = latest.get(target, {})
            job_id = str(job.get("jobID", ""))
            if not job_id or job_id == old_job_id or job.get("state") not in terminal_states:
                all_targets_terminal = False
                break
        if not all_targets_terminal:
            return False
        all_targets_succeeded = all(latest[target].get("state") == "Succeeded" for target in old_job_ids)
        phase = snapshot.get("status", {}).get("phase")
        return phase in {"Ready", "Failed"} or not all_targets_succeeded

    after = poll_until(
        fn=_resource_snapshot,
        until=_replay_finished,
        retries=_PROVISIONING_REPLAY_RETRIES,
        delay=_PROVISIONING_REPLAY_DELAY,
        description=f"{resource}/{name} Netris create replay to finish",
    )
    after_status = after.get("status", {})
    after_jobs = _latest_provision_jobs(after)
    assert set(after_jobs) == set(old_job_ids), (
        f"{resource}/{name} replay changed its provision targets: "
        f"before={sorted(old_job_ids)}, after={sorted(after_jobs)}"
    )
    assert all(
        after_jobs[target].get("jobID") and str(after_jobs[target].get("jobID")) != old_job_ids[target]
        for target in old_job_ids
    ), f"{resource}/{name} replay did not create a fresh AAP job for every target: {after_jobs}"
    assert all(job.get("configVersion") == desired_config_version for job in after_jobs.values()), (
        f"{resource}/{name} replay used a different config version: {after_jobs}"
    )
    failed_jobs = {
        target: job for target, job in after_jobs.items() if target in old_job_ids and job.get("state") != "Succeeded"
    }
    assert not failed_jobs, f"{resource}/{name} Netris create replay failed: {failed_jobs}"
    assert after_status.get("phase") == "Ready", f"{resource}/{name} did not return to Ready after replay"
    assert after_status.get("desiredConfigVersion") == desired_config_version, (
        f"{resource}/{name} desired config changed during a same-spec replay"
    )
    assert after.get("spec") == before.get("spec"), f"{resource}/{name} spec changed during replay"
    after_metadata = after.get("metadata", {})
    assert after_metadata.get("uid") == uid, f"{resource}/{name} Kubernetes identity changed during replay"
    assert after_metadata.get("generation") == before_metadata.get("generation"), (
        f"{resource}/{name} generation changed during a status-only replay"
    )
    assert after_metadata.get("labels", {}) == before_metadata.get("labels", {}), (
        f"{resource}/{name} labels changed during replay"
    )
    assert after_metadata.get("annotations", {}) == before_metadata.get("annotations", {}), (
        f"{resource}/{name} annotations changed during replay"
    )
    for field, value in stable_status.items():
        assert after_status.get(field) == value, (
            f"{resource}/{name} status.{field} changed during replay: "
            f"before={value!r}, after={after_status.get(field)!r}"
        )
    matching_crs = k8s.count_by_label_all_namespaces(resource=resource, label=f"{_UUID_LABELS[resource]}={uuid}")
    assert matching_crs == 1, (
        f"Replay created duplicate Kubernetes {resource} objects for UUID {uuid}: found {matching_crs}"
    )


def _replay_netris_network_attachment_move(*, k8s: K8sClient, name: str) -> None:
    """Re-run an already-completed BMI move onto its tenant V-Net.

    This uses the normal bare-metal operator reconcile path and checks that the
    repeated Netris move gets a fresh successful AAP job without changing the
    BMI, its network handoff conditions, or the discovered attachment state.
    """
    before = k8s.get_json(resource="baremetalinstance", name=name)
    before_status = before.get("status", {})
    before_jobs = _latest_jobs(before, status_field="networkingJobs", job_type="provision")
    assert before_status.get("phase") == "Ready", f"BareMetalInstance/{name} is not Ready before move replay"
    assert before_jobs, f"BareMetalInstance/{name} has no network-move jobs to replay"
    assert all(job.get("state") == "Succeeded" for job in before_jobs.values()), (
        f"BareMetalInstance/{name} has an unsuccessful network-move job: {before_jobs}"
    )
    old_job_ids = {target: str(job.get("jobID", "")) for target, job in before_jobs.items()}
    assert all(old_job_ids.values()), f"BareMetalInstance/{name} has a network-move job without an ID"
    config_versions = {target: job.get("configVersion") for target, job in before_jobs.items()}
    assert all(config_versions.values()), f"BareMetalInstance/{name} has a network-move job without a config version"

    before_metadata = before.get("metadata", {})
    uid = before_metadata.get("uid")
    assert uid, f"BareMetalInstance/{name} has no Kubernetes UID"
    attachment_statuses = before_status.get("networkAttachmentStatuses", [])
    assert attachment_statuses and all(item.get("ipAddress") for item in attachment_statuses), (
        f"BareMetalInstance/{name} has no settled network attachment state: {attachment_statuses}"
    )
    required_conditions = {"NetworkAttachmentsReady", "NetworkHandoffComplete", "IPDiscoveryComplete"}
    conditions = {condition.get("type"): condition.get("status") for condition in before_status.get("conditions", [])}
    assert all(conditions.get(condition) == "True" for condition in required_conditions), (
        f"BareMetalInstance/{name} network handoff is incomplete: {conditions}"
    )

    output, return_code = k8s.patch_status(
        resource="baremetalinstance", name=name, patch=json.dumps({"status": {"networkingJobs": []}})
    )
    assert return_code == 0, f"Failed to reset test-owned BareMetalInstance/{name} network job history: {output}"

    def _resource_snapshot() -> dict[str, Any]:
        return k8s.get_json(resource="baremetalinstance", name=name)

    def _replay_finished(snapshot: dict[str, Any]) -> bool:
        latest = _latest_jobs(snapshot, status_field="networkingJobs", job_type="provision")
        if set(latest) != set(old_job_ids):
            return False
        if any(
            not latest[target].get("jobID")
            or str(latest[target].get("jobID")) == old_job_ids[target]
            or latest[target].get("state") not in {"Succeeded", "Failed", "Canceled"}
            for target in old_job_ids
        ):
            return False
        if any(job.get("state") != "Succeeded" for job in latest.values()):
            return True
        return snapshot.get("status", {}).get("phase") in {"Ready", "Failed"}

    after = poll_until(
        fn=_resource_snapshot,
        until=_replay_finished,
        retries=_PROVISIONING_REPLAY_RETRIES,
        delay=_PROVISIONING_REPLAY_DELAY,
        description=f"BareMetalInstance/{name} Netris tenant-network move replay",
    )
    after_status = after.get("status", {})
    after_jobs = _latest_jobs(after, status_field="networkingJobs", job_type="provision")
    assert set(after_jobs) == set(old_job_ids), (
        f"BareMetalInstance/{name} move replay changed targets: "
        f"before={sorted(old_job_ids)}, after={sorted(after_jobs)}"
    )
    assert all(
        after_jobs[target].get("state") == "Succeeded"
        and str(after_jobs[target].get("jobID", "")) != old_job_ids[target]
        and after_jobs[target].get("configVersion") == config_versions[target]
        for target in old_job_ids
    ), f"BareMetalInstance/{name} Netris tenant-network move replay failed or changed config: {after_jobs}"
    assert after_status.get("phase") == "Ready", f"BareMetalInstance/{name} did not return to Ready after move replay"
    assert after.get("spec") == before.get("spec"), f"BareMetalInstance/{name} spec changed during move replay"
    after_metadata = after.get("metadata", {})
    assert after_metadata.get("uid") == uid, f"BareMetalInstance/{name} identity changed during move replay"
    assert after_metadata.get("generation") == before_metadata.get("generation"), (
        f"BareMetalInstance/{name} generation changed during a status-only move replay"
    )
    assert after_status.get("networkAttachmentStatuses") == attachment_statuses, (
        f"BareMetalInstance/{name} network attachment state changed during move replay: "
        f"before={attachment_statuses}, after={after_status.get('networkAttachmentStatuses')}"
    )
    after_conditions = {
        condition.get("type"): condition.get("status") for condition in after_status.get("conditions", [])
    }
    assert all(after_conditions.get(condition) == "True" for condition in required_conditions), (
        f"BareMetalInstance/{name} network handoff conditions changed during replay: {after_conditions}"
    )


class TestBmaasNetworking:
    state: ClassVar[dict[str, Any]] = {}

    # ── Phase 0: External IP Pool ─────────────────────────────────────

    def test_00_create_external_ip_pool_and_replay(
        self,
        private_grpc: GRPCClient,
        k8s_hub_client: K8sClient,
        external_ip_pool_name: str,
        external_ip_pool_cidr: str,
    ) -> None:
        pool_id = private_grpc.create_external_ip_pool(
            name=external_ip_pool_name, cidrs=[external_ip_pool_cidr], implementation_strategy="netris"
        )
        pool_cr = wait_for_external_ip_pool_cr(k8s=k8s_hub_client, uuid=pool_id)
        self.__class__.state.update(pool_id=pool_id, pool_cr=pool_cr)
        wait_for_external_ip_pool_ready(k8s=k8s_hub_client, name=pool_cr)
        wait_for_external_ip_pool_grpc_ready(private_grpc=private_grpc, pool_id=pool_id)
        _replay_netris_create(k8s=k8s_hub_client, resource="externalippool", name=pool_cr, uuid=pool_id)
        print("Created ExternalIPPool")

    # ── Phase 1: Build the Network ──────────────────────────────────────

    def test_01_create_virtual_network_and_replay(
        self, grpc: GRPCClient, k8s_hub_client: K8sClient, net_test_run_id: str
    ) -> None:
        name = f"net-{net_test_run_id}"
        vnet_id = grpc.create_virtual_network(name=name, ipv4_cidr="10.100.0.0/16")
        vnet_cr = wait_for_virtual_network_cr(k8s=k8s_hub_client, uuid=vnet_id)
        self.__class__.state["vnet_id"] = vnet_id
        self.__class__.state["vnet_cr"] = vnet_cr
        self.__class__.state["vnet_name"] = name
        wait_for_virtual_network_ready(k8s=k8s_hub_client, name=vnet_cr)

        _replay_netris_create(k8s=k8s_hub_client, resource="virtualnetwork", name=vnet_cr, uuid=vnet_id)

    def test_02_create_subnets_and_replay(
        self, grpc: GRPCClient, k8s_hub_client: K8sClient, net_test_run_id: str
    ) -> None:
        _require(self.state, "vnet_id")

        subnet_a_name = f"sub-a-{net_test_run_id}"
        subnet_a_id = grpc.create_subnet(
            name=subnet_a_name, virtual_network=self.state["vnet_id"], ipv4_cidr="10.100.1.0/24"
        )
        subnet_a_cr = wait_for_subnet_cr(k8s=k8s_hub_client, uuid=subnet_a_id)
        self.__class__.state.update(subnet_a_id=subnet_a_id, subnet_a_cr=subnet_a_cr)
        wait_for_subnet_ready(k8s=k8s_hub_client, name=subnet_a_cr)
        wait_for_grpc_subnet_ready(grpc=grpc, subnet_id=subnet_a_id)
        _replay_netris_create(k8s=k8s_hub_client, resource="subnet", name=subnet_a_cr, uuid=subnet_a_id)

        subnet_b_name = f"sub-b-{net_test_run_id}"
        subnet_b_id = grpc.create_subnet(
            name=subnet_b_name, virtual_network=self.state["vnet_id"], ipv4_cidr="10.100.2.0/24"
        )
        subnet_b_cr = wait_for_subnet_cr(k8s=k8s_hub_client, uuid=subnet_b_id)
        self.__class__.state.update(subnet_b_id=subnet_b_id, subnet_b_cr=subnet_b_cr)
        wait_for_subnet_ready(k8s=k8s_hub_client, name=subnet_b_cr)
        wait_for_grpc_subnet_ready(grpc=grpc, subnet_id=subnet_b_id)
        _replay_netris_create(k8s=k8s_hub_client, resource="subnet", name=subnet_b_cr, uuid=subnet_b_id)

        self.__class__.state.update(
            subnet_a_id=subnet_a_id, subnet_a_cr=subnet_a_cr, subnet_b_id=subnet_b_id, subnet_b_cr=subnet_b_cr
        )

    def test_03_create_security_group_and_replay(
        self, grpc: GRPCClient, k8s_hub_client: K8sClient, net_test_run_id: str
    ) -> None:
        _require(self.state, "vnet_id")

        sg_name = f"sg-{net_test_run_id}"
        sg_id = grpc.create_security_group_with_rules(
            name=sg_name,
            virtual_network=self.state["vnet_id"],
            ingress=[
                {"protocol": "PROTOCOL_TCP", "port_from": 22, "port_to": 22, "ipv4_cidr": "0.0.0.0/0"},
                {"protocol": "PROTOCOL_ICMP", "ipv4_cidr": "0.0.0.0/0"},
            ],
            egress=[{"protocol": "PROTOCOL_ALL", "ipv4_cidr": "0.0.0.0/0"}],
        )
        sg_cr = wait_for_security_group_cr(k8s=k8s_hub_client, uuid=sg_id)
        self.__class__.state.update(sg_id=sg_id, sg_cr=sg_cr, sg_name=sg_name)
        wait_for_security_group_ready(k8s=k8s_hub_client, name=sg_cr)
        _replay_netris_create(k8s=k8s_hub_client, resource="securitygroup", name=sg_cr, uuid=sg_id)

    def test_04_create_nat_gateway_and_replay(
        self, grpc: GRPCClient, k8s_hub_client: K8sClient, net_test_run_id: str
    ) -> None:
        _require(self.state, "vnet_name", "pool_id")

        nat_eip_name = f"nat-eip-{net_test_run_id}"
        nat_eip_id = grpc.create_external_ip(name=nat_eip_name, pool=self.state["pool_id"])
        nat_eip_cr = wait_for_external_ip_cr(k8s=k8s_hub_client, uuid=nat_eip_id)
        self.__class__.state.update(nat_eip_id=nat_eip_id, nat_eip_cr=nat_eip_cr, nat_eip_name=nat_eip_name)
        wait_for_external_ip_allocated(k8s=k8s_hub_client, name=nat_eip_cr)

        nat_name = f"nat-{net_test_run_id}"
        nat_id = grpc.create_nat_gateway(
            name=nat_name, virtual_network_name=self.state["vnet_name"], external_ip_name=nat_eip_name
        )
        self.__class__.state.update(nat_id=nat_id, nat_name=nat_name)
        poll_until(
            fn=lambda: (
                grpc.call(service="osac.public.v1.NATGateways/Get", data={"id": nat_id})
                .get("object", {})
                .get("status", {})
                .get("state", "")
            ),
            until=lambda s: s in ("NAT_GATEWAY_STATE_READY", "Ready"),
            retries=30,
            delay=5,
            description=f"NATGateway {nat_name} to become Ready",
        )
        nat_cr_name = poll_until(
            fn=lambda: k8s_hub_client.get_nat_gateway_name(uuid=nat_id, checked=False),
            until=lambda name: name != "",
            retries=30,
            delay=5,
            description=f"NATGateway CR for {nat_name}",
        )
        self.__class__.state["nat_cr"] = nat_cr_name
        assert k8s_hub_client.get_jsonpath(
            resource="natgateway", name=nat_cr_name, jsonpath="{.status.stateTransitionTime}"
        )
        _replay_netris_create(k8s=k8s_hub_client, resource="natgateway", name=nat_cr_name, uuid=nat_id)

    # ── Phase 2: Provision Servers ──────────────────────────────────────

    def test_05_create_three_bmis(
        self,
        cli: OsacCLI,
        grpc: GRPCClient,
        k8s_hub_client: K8sClient,
        catalog_item_name: str,
        auto_eip_catalog_item_name: str,
        bmi_instance_type: str,
        bmi_disk_image: str,
        net_ssh_public_key: str,
        bmh_namespace: str,
        net_test_run_id: str,
        bmh_ssh_hosts: dict[str, str],
    ) -> None:
        _require(self.state, "subnet_a_id", "subnet_b_id", "sg_id")

        subnet_a = self.state["subnet_a_id"]
        subnet_b = self.state["subnet_b_id"]
        sg = self.state["sg_id"]

        bmis: list[dict[str, str]] = []
        for i, (name_suffix, subnet_id) in enumerate([("bmi1", subnet_a), ("bmi2", subnet_a), ("bmi3", subnet_b)]):
            bmi_name = f"{name_suffix}-{net_test_run_id}"
            is_auto_eip = i == 2
            catalog = auto_eip_catalog_item_name if is_auto_eip else catalog_item_name
            bmi_id, _ = cli.create_baremetal_instance(
                name=bmi_name,
                catalog_item=catalog,
                instance_type=bmi_instance_type,
                ssh_key=net_ssh_public_key,
                disk_image=bmi_disk_image,
                network_attachments=[f"subnet={subnet_id},interface=eth9,primary,security-groups={sg}"],
                external_ip_attachment=is_auto_eip,
            )
            print("Created BMI")
            bmis.append({"name": bmi_name, "id": bmi_id, "subnet": "a" if i < 2 else "b"})

        for bmi in bmis:
            bmi["cr"] = wait_for_bmi_cr(k8s=k8s_hub_client, uuid=bmi["id"])
            print("BMI custom resource found")

        for bmi in bmis:
            wait_for_bmi_running(grpc=grpc, bmi_id=bmi["id"], retries=_NETRIS_BMI_RUNNING_RETRIES)
            print("BMI is RUNNING")

        for bmi in bmis:
            bmi["ip"] = poll_until(
                fn=lambda b=bmi: k8s_hub_client.get_baremetal_instance_tenant_ip(name=b["cr"]),
                until=lambda ip: ip != "",
                retries=60,
                delay=10,
                description=f"BMI {bmi['name']} tenant IP assignment",
            )

            ext_host = k8s_hub_client.get_baremetal_instance_external_host_id(name=bmi["cr"])
            bmi["bmh"] = ext_host.split("/", 1)[1]
            bmi["ssh_host"] = bmi_ssh.get_ssh_host(bmi["bmh"], bmh_ssh_hosts)
            print("BMI tenant IP and SSH host resolved")

        for bmi in bmis:
            if bmi["subnet"] == "a":
                assert bmi["ip"].startswith("10.100.1."), f"BMI {bmi['name']} IP {bmi['ip']} not in subnet A"
            else:
                assert bmi["ip"].startswith("10.100.2."), f"BMI {bmi['name']} IP {bmi['ip']} not in subnet B"

        self.__class__.state["bmi1"] = bmis[0]
        self.__class__.state["bmi2"] = bmis[1]
        self.__class__.state["bmi3"] = bmis[2]
        for bmi in bmis:
            spec = grpc.get_baremetal_instance(bmi_id=bmi["id"]).get("object", {}).get("spec", {})
            instance_type = spec.get("instance_type", spec.get("instanceType", {}))
            assert instance_type.get("name") == bmi_instance_type, (
                f"BareMetalInstance {bmi['name']} has instance_type {instance_type.get('name')!r}, "
                f"expected {bmi_instance_type!r}"
            )

        _replay_netris_network_attachment_move(k8s=k8s_hub_client, name=bmis[0]["cr"])

    def test_05b_verify_auto_eip_on_bmi3(
        self, grpc: GRPCClient, private_grpc: GRPCClient, k8s_hub_client: K8sClient
    ) -> None:
        _require(self.state, "bmi3")
        bmi3 = self.state["bmi3"]

        def find_auto_attachment() -> dict[str, Any] | None:
            attachments = grpc.call(service="osac.public.v1.ExternalIPAttachments/List")
            for item in attachments.get("items", []):
                bmi_ref = item.get("spec", {}).get("baremetalInstance", {}).get("id", "")
                if bmi_ref == bmi3["id"]:
                    return item
            return None

        attachment = poll_until(
            fn=find_auto_attachment,
            until=lambda a: a is not None,
            retries=60,
            delay=5,
            description="auto-created ExternalIPAttachment for BMI3",
        )

        auto_attach_id = attachment["id"]
        auto_attach_cr = wait_for_external_ip_attachment_cr(k8s=k8s_hub_client, uuid=auto_attach_id)
        wait_for_external_ip_attachment_ready(k8s=k8s_hub_client, name=auto_attach_cr)
        auto_eip_ref = attachment.get("spec", {}).get("externalIp", {}).get("id", "")
        assert auto_eip_ref, "Auto-created attachment has no ExternalIP reference"

        eip_data = grpc.get_external_ip(external_ip_id=auto_eip_ref)
        auto_ext_addr = eip_data.get("object", {}).get("status", {}).get("address", "")
        assert auto_ext_addr, "Auto-created ExternalIP has no allocated address"
        _private_eip = poll_until(
            fn=lambda: private_grpc.get_private_external_ip(external_ip_id=auto_eip_ref)["object"],
            until=lambda item: (
                item["status"].get("attribution", {}).get("baremetalInstance", {}).get("id") == bmi3["id"]
                and bool(item["status"].get("attachmentTransitionTime"))
            ),
            retries=30,
            delay=5,
            description="auto-created ExternalIP attribution settlement for BMI3",
        )

        self.__class__.state.update(
            auto_attach_id=auto_attach_id, auto_eip_id=auto_eip_ref, auto_ext_addr=auto_ext_addr
        )

        print("BMI3 auto EIP and attachment resolved")

    def test_05c_verify_attachment_immutability(self, grpc: GRPCClient, k8s_hub_client: K8sClient) -> None:
        """A running BMI's resolved attachment is immutable through the public API.

        Changed SecurityGroup, interface, and auto-external-access updates are
        rejected with InvalidArgument and the persisted attachment and CR spec are
        unchanged after every rejection. The primary check runs before the
        security-group comparison, so every candidate pins ``primary: true``.
        """
        _require(self.state, "bmi1", "subnet_a_id", "sg_id")
        bmi1 = self.state["bmi1"]
        subnet_a_id = self.state["subnet_a_id"]
        sg_id = self.state["sg_id"]

        instance_before = grpc.get_baremetal_instance(bmi_id=bmi1["id"])["object"]
        cr_before = k8s_hub_client.get_json(resource="baremetalinstance", name=bmi1["cr"]).get("spec", {})

        def stored_attachment(candidate: dict[str, Any]) -> dict[str, Any]:
            return {
                "object": {"id": bmi1["id"], "spec": {"network_attachments": [candidate]}},
                "updateMask": {"paths": ["spec.network_attachments"]},
            }

        def assert_unchanged() -> None:
            instance_after = grpc.get_baremetal_instance(bmi_id=bmi1["id"])["object"]
            spec_before = instance_before.get("spec", {})
            spec_after = instance_after.get("spec", {})
            before_attach = (spec_before.get("networkAttachments") or spec_before.get("network_attachments") or [{}])[0]
            after_attach = (spec_after.get("networkAttachments") or spec_after.get("network_attachments") or [{}])[0]
            assert after_attach == before_attach, "stored attachment changed"
            cr_after = k8s_hub_client.get_json(resource="baremetalinstance", name=bmi1["cr"]).get("spec", {})
            assert cr_after == cr_before, "BareMetalInstance CR spec changed"

        # SecurityGroup removal is rejected.
        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            grpc.call(
                service="osac.public.v1.BareMetalInstances/Update",
                data=stored_attachment(
                    {"subnet": {"id": subnet_a_id}, "interface": "eth9", "primary": True, "security_groups": []}
                ),
            )
        assert_grpc_rejected(exc_info, "InvalidArgument")
        assert_unchanged()

        # An interface change is rejected.
        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            grpc.call(
                service="osac.public.v1.BareMetalInstances/Update",
                data=stored_attachment(
                    {
                        "subnet": {"id": subnet_a_id},
                        "interface": "eth10",
                        "primary": True,
                        "security_groups": [{"id": sg_id}],
                    }
                ),
            )
        assert_grpc_rejected(exc_info, "InvalidArgument")
        assert_unchanged()

        # Flipping auto_external_ip_attachment after creation is rejected.
        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            grpc.call(
                service="osac.public.v1.BareMetalInstances/Update",
                data={
                    "object": {"id": bmi1["id"], "spec": {"auto_external_ip_attachment": True}},
                    "updateMask": {"paths": ["spec.auto_external_ip_attachment"]},
                },
            )
        assert_grpc_rejected(exc_info, "InvalidArgument")
        assert_unchanged()
        print("BMI attachment immutability verified")

    # ── Phase 3: Connectivity Tests ─────────────────────────────────────

    def test_06_l2_arping_same_subnet(self) -> None:
        _require(self.state, "bmi1", "bmi2")
        bmi1 = self.state["bmi1"]
        bmi2 = self.state["bmi2"]

        poll_until(
            fn=lambda: bmi_ssh.arping(bmi1["ssh_host"], bmi2["ip"]),
            until=lambda ok: ok,
            retries=5,
            delay=10,
            description=f"L2 arping BMI1 ({bmi1['ip']}) → BMI2 ({bmi2['ip']})",
        )

    def test_07_l3_ping_same_subnet(self) -> None:
        _require(self.state, "bmi1", "bmi2")
        bmi1 = self.state["bmi1"]
        bmi2 = self.state["bmi2"]

        poll_until(
            fn=lambda: bmi_ssh.ping(bmi1["ssh_host"], bmi2["ip"]),
            until=lambda ok: ok,
            retries=5,
            delay=10,
            description=f"L3 ping BMI1 ({bmi1['ip']}) → BMI2 ({bmi2['ip']})",
        )

    def test_08_l3_ping_cross_subnet(self) -> None:
        _require(self.state, "bmi1", "bmi3")
        bmi1 = self.state["bmi1"]
        bmi3 = self.state["bmi3"]

        poll_until(
            fn=lambda: bmi_ssh.ping(bmi1["ssh_host"], bmi3["ip"]),
            until=lambda ok: ok,
            retries=5,
            delay=10,
            description=f"L3 ping BMI1 ({bmi1['ip']}) → BMI3 ({bmi3['ip']}) cross-subnet",
        )

    def test_09_l2_arping_cross_subnet_fails(self) -> None:
        _require(self.state, "bmi1", "bmi3")
        bmi1 = self.state["bmi1"]
        bmi3 = self.state["bmi3"]

        # The probe helper requires a completion marker from the same SSH command.
        assert not bmi_ssh.arping(bmi1["ssh_host"], bmi3["ip"]), (
            f"arping from BMI1 ({bmi1['ip']}, subnet A) to BMI3 ({bmi3['ip']}, subnet B) "
            f"succeeded unexpectedly — different subnets should be different broadcast domains"
        )

    def test_10_tenant_isolation(self, mgmt_cluster_ip: str) -> None:
        _require(self.state, "bmi1")
        bmi1 = self.state["bmi1"]

        # The probe helper requires a completion marker from the same SSH command.
        assert not bmi_ssh.ping(bmi1["ssh_host"], mgmt_cluster_ip), (
            f"ping from BMI1 ({bmi1['ip']}) to management cluster ({mgmt_cluster_ip}) "
            f"succeeded unexpectedly — tenant isolation should prevent cross-VNet traffic"
        )

    def test_11_nat_gateway_egress(self) -> None:
        _require(self.state, "bmi1")
        bmi1 = self.state["bmi1"]

        poll_until(
            fn=lambda: bmi_ssh.curl_status(bmi1["ssh_host"], "https://quay.io"),
            until=lambda status: status == 200,
            retries=5,
            delay=15,
            description=f"NAT gateway egress curl quay.io (ssh_host={bmi1['ssh_host']}, tenant_ip={bmi1['ip']})",
        )

    def test_12_external_ip_ingress_and_replay(
        self, grpc: GRPCClient, private_grpc: GRPCClient, k8s_hub_client: K8sClient, net_test_run_id: str
    ) -> None:
        _require(self.state, "bmi1", "pool_id")
        bmi1 = self.state["bmi1"]

        eip_name = f"ingress-eip-{net_test_run_id}"
        eip_id = grpc.create_external_ip(name=eip_name, pool=self.state["pool_id"])
        eip_cr = wait_for_external_ip_cr(k8s=k8s_hub_client, uuid=eip_id)
        self.__class__.state.update(ingress_eip_id=eip_id, ingress_eip_cr=eip_cr)
        wait_for_external_ip_allocated(k8s=k8s_hub_client, name=eip_cr)
        _replay_netris_create(k8s=k8s_hub_client, resource="externalip", name=eip_cr, uuid=eip_id)
        wait_for_external_ip_allocated(k8s=k8s_hub_client, name=eip_cr)

        attach_name = f"ingress-attach-{net_test_run_id}"
        attach_id = grpc.create_external_ip_attachment_bmi(
            name=attach_name, external_ip=eip_id, baremetal_instance=bmi1["id"]
        )
        attach_cr = wait_for_external_ip_attachment_cr(k8s=k8s_hub_client, uuid=attach_id)
        self.__class__.state.update(ingress_attach_id=attach_id, ingress_attach_cr=attach_cr)
        wait_for_external_ip_attachment_ready(k8s=k8s_hub_client, name=attach_cr)
        _replay_netris_create(k8s=k8s_hub_client, resource="externalipattachment", name=attach_cr, uuid=attach_id)
        poll_until(
            fn=lambda: private_grpc.get_private_external_ip(external_ip_id=eip_id)["object"],
            until=lambda item: (
                item["status"].get("attribution", {}).get("baremetalInstance", {}).get("id") == bmi1["id"]
                and bool(item["status"].get("attachmentTransitionTime"))
            ),
            retries=30,
            delay=5,
            description="ingress ExternalIP attribution settlement",
        )

        eip_data = grpc.get_external_ip(external_ip_id=eip_id)
        ext_addr = eip_data.get("object", {}).get("status", {}).get("address", "")
        assert ext_addr, "ExternalIP has no allocated address"

        def _try_ssh_eip() -> str:
            try:
                return bmi_ssh.ssh_via_external_ip(ext_addr, timeout=10)
            except (subprocess.CalledProcessError, subprocess.TimeoutExpired):
                return ""

        poll_until(
            fn=_try_ssh_eip, until=lambda h: bool(h), retries=5, delay=15, description=f"SSH via external IP {ext_addr}"
        )

        self.__class__.state.update(
            ingress_eip_id=eip_id,
            ingress_eip_cr=eip_cr,
            ingress_attach_id=attach_id,
            ingress_attach_cr=attach_cr,
            ingress_ext_addr=ext_addr,
        )

    # ── Phase 4: Teardown ───────────────────────────────────────────────

    def test_13_delete_external_ip_attachment(self, grpc: GRPCClient, k8s_hub_client: K8sClient) -> None:
        if "ingress_attach_id" not in self.state:
            pytest.skip("No ExternalIPAttachment to delete")

        _delete_netris_resource_and_verify(
            k8s=k8s_hub_client,
            resource="externalipattachment",
            name=self.state["ingress_attach_cr"],
            delete=lambda: grpc.delete_external_ip_attachment(attachment_id=self.state["ingress_attach_id"]),
        )
        wait_for_external_ip_attachment_deletion(k8s=k8s_hub_client, name=self.state["ingress_attach_cr"])

        _delete_netris_resource_and_verify(
            k8s=k8s_hub_client,
            resource="externalip",
            name=self.state["ingress_eip_cr"],
            delete=lambda: grpc.delete_external_ip(external_ip_id=self.state["ingress_eip_id"]),
        )
        wait_for_external_ip_deletion(k8s=k8s_hub_client, name=self.state["ingress_eip_cr"])

    def test_14_delete_bmis(
        self, cli: OsacCLI, grpc: GRPCClient, k8s_hub_client: K8sClient, bmh_namespace: str
    ) -> None:
        for key in ("bmi1", "bmi2", "bmi3"):
            if key not in self.state:
                continue
            bmi = self.state[key]
            print("Deleting BMI...")
            _delete_netris_resource_and_verify(
                k8s=k8s_hub_client,
                resource="baremetalinstance",
                name=bmi["cr"],
                status_field="networkingJobs",
                require_netris_annotation=False,
                delete=lambda bmi=bmi: cli.delete_baremetal_instance(uuid=bmi["id"]),
            )

        for key in ("bmi1", "bmi2", "bmi3"):
            if key not in self.state:
                continue
            bmi = self.state[key]
            wait_for_bmi_deletion(k8s=k8s_hub_client, name=bmi["cr"])
            wait_for_bmi_grpc_removal(grpc=grpc, uuid=bmi["id"])
            wait_for_bmh_available(k8s=k8s_hub_client, name=bmi["bmh"], bmh_namespace=bmh_namespace)
            if key == "bmi1":
                self.__class__.state["bmi1_deleted"] = True
            print("BMI deprovisioned, BMH available")

    # Temporarily disabled: an E2E run timed out waiting for this
    # auto-created attachment to disappear from the Fulfillment list
    # after BMI3 deletion. Re-enable when this cleanup is reliable.
    # def test_14b_verify_auto_eip_garbage_collected(self, grpc: GRPCClient) -> None:
    #     if "auto_attach_id" not in self.state:
    #         pytest.skip("No auto EIP to verify")
    #
    #     poll_until(
    #         fn=lambda: self.state["auto_attach_id"] not in grpc.list_external_ip_attachment_ids(),
    #         until=lambda gone: gone is True,
    #         retries=30,
    #         delay=5,
    #         description="auto-created ExternalIPAttachment garbage collection",
    #     )
    #
    #     poll_until(
    #         fn=lambda: self.state["auto_eip_id"] not in grpc.list_external_ip_ids(),
    #         until=lambda gone: gone is True,
    #         retries=30,
    #         delay=5,
    #         description="auto-created ExternalIP garbage collection",
    #     )
    #     print("Auto EIP and attachment garbage collected after BMI3 deletion")
    #
    def test_14c_verify_idempotent_delete(self, grpc: GRPCClient) -> None:
        """A second public Delete of a removed BMI returns NotFound (the retry contract)."""
        _require(self.state, "bmi1", "bmi1_deleted")
        bmi1 = self.state["bmi1"]
        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            grpc.call(service="osac.public.v1.BareMetalInstances/Delete", data={"id": bmi1["id"]})
        assert_grpc_rejected(exc_info, "NotFound")
        print("Second BMI delete returned NotFound")

    def test_15_delete_nat_gateway(self, grpc: GRPCClient, k8s_hub_client: K8sClient) -> None:
        if "nat_id" in self.state:
            _delete_netris_resource_and_verify(
                k8s=k8s_hub_client,
                resource="natgateway",
                name=self.state["nat_cr"],
                delete=lambda: grpc.delete_nat_gateway(nat_gateway_id=self.state["nat_id"]),
            )
            wait_for_nat_gateway_deletion(k8s=k8s_hub_client, name=self.state["nat_cr"])
            poll_until(
                fn=lambda: (
                    self.state["nat_id"]
                    not in [
                        item["id"] for item in grpc.call(service="osac.public.v1.NATGateways/List").get("items", [])
                    ]
                ),
                until=lambda gone: gone is True,
                retries=60,
                delay=5,
                description=f"NATGateway {self.state['nat_name']} deletion",
            )

        if "nat_eip_id" not in self.state:
            pytest.skip("No NATGateway to delete")

        _delete_netris_resource_and_verify(
            k8s=k8s_hub_client,
            resource="externalip",
            name=self.state["nat_eip_cr"],
            delete=lambda: grpc.delete_external_ip(external_ip_id=self.state["nat_eip_id"]),
        )
        wait_for_external_ip_deletion(k8s=k8s_hub_client, name=self.state["nat_eip_cr"])

    def test_16_delete_security_group(self, grpc: GRPCClient, k8s_hub_client: K8sClient) -> None:
        if "sg_id" not in self.state:
            pytest.skip("No SecurityGroup to delete")

        _delete_netris_resource_and_verify(
            k8s=k8s_hub_client,
            resource="securitygroup",
            name=self.state["sg_cr"],
            delete=lambda: grpc.delete_security_group(sg_id=self.state["sg_id"]),
        )
        wait_for_security_group_deletion(k8s=k8s_hub_client, name=self.state["sg_cr"])

    def test_17_delete_subnets(self, grpc: GRPCClient, k8s_hub_client: K8sClient) -> None:
        for key, cr_key in [("subnet_a_id", "subnet_a_cr"), ("subnet_b_id", "subnet_b_cr")]:
            if key not in self.state:
                continue
            _delete_netris_resource_and_verify(
                k8s=k8s_hub_client,
                resource="subnet",
                name=self.state[cr_key],
                delete=lambda key=key: grpc.delete_subnet(subnet_id=self.state[key]),
            )
            wait_for_subnet_deletion(k8s=k8s_hub_client, name=self.state[cr_key])

    def test_18_delete_virtual_network(self, grpc: GRPCClient, k8s_hub_client: K8sClient) -> None:
        if "vnet_id" not in self.state:
            pytest.skip("No VirtualNetwork to delete")

        _delete_netris_resource_and_verify(
            k8s=k8s_hub_client,
            resource="virtualnetwork",
            name=self.state["vnet_cr"],
            delete=lambda: grpc.delete_virtual_network(vn_id=self.state["vnet_id"]),
        )
        wait_for_virtual_network_deletion(k8s=k8s_hub_client, name=self.state["vnet_cr"])

        remaining = grpc.list_virtual_network_ids()
        assert self.state["vnet_id"] not in remaining, "VirtualNetwork still in API after deletion"

    def test_19_delete_external_ip_pool(self, private_grpc: GRPCClient, k8s_hub_client: K8sClient) -> None:
        if "pool_id" not in self.state:
            pytest.skip("No ExternalIPPool to delete")

        _delete_netris_resource_and_verify(
            k8s=k8s_hub_client,
            resource="externalippool",
            name=self.state["pool_cr"],
            delete=lambda: private_grpc.delete_external_ip_pool(pool_id=self.state["pool_id"]),
        )
        wait_for_external_ip_pool_deletion(k8s=k8s_hub_client, name=self.state["pool_cr"])

        remaining = private_grpc.list_external_ip_pool_ids()
        assert self.state["pool_id"] not in remaining, "ExternalIPPool still in API after deletion"
