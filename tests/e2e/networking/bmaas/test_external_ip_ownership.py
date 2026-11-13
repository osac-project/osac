from __future__ import annotations

import ipaddress
import json
import subprocess
from collections.abc import Callable
from pathlib import Path
from typing import Any
from uuid import uuid4

import yaml

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import (
    poll_until,
    wait_for_external_ip_allocated,
    wait_for_external_ip_cr,
    wait_for_external_ip_deletion,
)
from tests.e2e.core.k8s_client import K8sClient

_EXTERNAL_IP_CREATE_TASK = "create_external_ip"
_FABRIC_MANAGER_TYPE = "fabric"


class TestExternalIPOwnership:
    """Verify Netris BMaaS ExternalIP allocation, retry, and release behavior."""

    def test_bmaas_netris_allocation_retry_and_release(
        self,
        grpc: GRPCClient,
        private_grpc: GRPCClient,
        k8s_hub_client: K8sClient,
        external_ip_ownership_pool: dict[str, str],
        record_property: Callable[[str, str], None],
    ) -> None:
        _exercise_external_ip_ownership_lifecycle(
            profile="bmaas-netris",
            grpc=grpc,
            private_grpc=private_grpc,
            k8s=k8s_hub_client,
            pool=external_ip_ownership_pool,
            record_property=record_property,
        )


def _exercise_external_ip_ownership_lifecycle(
    *,
    profile: str,
    grpc: GRPCClient,
    private_grpc: GRPCClient,
    k8s: K8sClient,
    pool: dict[str, str],
    record_property: Callable[[str, str], None],
) -> None:
    network_class = _active_network_class(private_grpc)
    fabric_manager = _network_class_value(network_class, "fabric_manager", "fabricManager")
    k8s_manager = _network_class_value(network_class, "k8s_manager", "k8sManager")
    assert fabric_manager == "netris", f"{profile} expected fabric_manager='netris', got {fabric_manager!r}"
    assert k8s_manager == "", f"{profile} expected no Kubernetes Manager, got {k8s_manager!r}"

    manager = fabric_manager
    collection_versions = _declared_aap_collection_versions(manager)
    network_metadata = network_class.get("metadata", {})
    record_property("osac_profile", profile)
    record_property("osac_manager_implementation", manager)
    record_property("osac_network_class", str(network_metadata.get("name", "")))
    record_property("osac_network_class_id", str(network_class.get("id", "")))
    record_property("osac_fabric_manager", fabric_manager)
    record_property("osac_k8s_manager", k8s_manager)
    record_property("aap_collection_versions_source", "osac-aap source manifests; not deployed runtime")
    record_property("aap_collection_versions_declared", json.dumps(collection_versions, sort_keys=True))

    initial_capacity = _pool_capacity(private_grpc, pool["id"])
    assert initial_capacity["allocated"] == 0, f"New test pool should start empty: {initial_capacity}"
    assert initial_capacity["available"] > 0, f"New test pool has no free addresses: {initial_capacity}"

    external_ip_id = ""
    external_ip_cr_name = ""
    external_ip_uid = ""
    try:
        external_ip_id = grpc.create_external_ip(name=f"ref-eip-owner-{uuid4().hex[:8]}", pool=pool["id"])
        external_ip_cr_name = wait_for_external_ip_cr(k8s=k8s, uuid=external_ip_id)
        wait_for_external_ip_allocated(k8s=k8s, name=external_ip_cr_name)

        first_job = _wait_for_successful_create_job(k8s=k8s, external_ip_name=external_ip_cr_name)
        allocated_cr = k8s.get_json(resource="externalip", name=external_ip_cr_name)
        metadata = allocated_cr.get("metadata", {})
        uid = metadata.get("uid", "")
        generation = metadata.get("generation")
        assert uid, "Allocated ExternalIP CR has no UID"
        assert generation is not None, "Allocated ExternalIP CR has no generation"
        tenant = (metadata.get("annotations") or {}).get("osac.openshift.io/tenant", "")
        assert tenant, "Allocated ExternalIP CR has no tenant annotation"
        assert tenant == pool["expected_external_ip_tenant"], (
            "Allocated ExternalIP tenant differs from the JWT user's independently resolved tenant"
        )
        external_ip_uid = uid

        cr_status = allocated_cr.get("status", {})
        address = _validated_allocated_address(external_ip=allocated_cr, selected_pool_cidr=pool["cidr"])
        managers = cr_status.get("networkManagers", {}) or {}
        fabric_ref = managers.get("fabric", {}) or {}
        assert fabric_ref.get("name") == manager, (
            f"ExternalIP records Fabric Manager {fabric_ref.get('name')!r}, expected {manager!r}"
        )
        assert fabric_ref.get("ansibleRole"), "ExternalIP has no recorded Fabric Manager role"
        _assert_task_input_identity(
            external_ip=allocated_cr, external_ip_id=external_ip_id, uid=uid, generation=generation
        )
        assert first_job["manager"] == _FABRIC_MANAGER_TYPE
        api_external_ip = _wait_for_api_external_ip_address(grpc=grpc, external_ip_id=external_ip_id, address=address)
        assert api_external_ip.get("id") == external_ip_id, "Fulfillment returned a different ExternalIP ID"
        after_allocation = _pool_capacity(private_grpc, pool["id"])
        assert after_allocation["allocated"] == 1, f"Expected one reserved address after allocation: {after_allocation}"
        assert after_allocation["available"] == initial_capacity["available"] - 1

        record_property("external_ip_id", external_ip_id)
        record_property("external_ip_uid", uid)
        record_property("external_ip_generation", str(generation))
        record_property("external_ip_address", address)
        record_property("external_ip_first_create_job_id", str(first_job["jobID"]))

        _request_task_retry(k8s=k8s, external_ip_name=external_ip_cr_name, completed_job=first_job)
        retry_job = _wait_for_successful_create_job(
            k8s=k8s, external_ip_name=external_ip_cr_name, previous_attempt=int(first_job["attempt"])
        )
        retried_cr = k8s.get_json(resource="externalip", name=external_ip_cr_name)
        retried_metadata = retried_cr.get("metadata", {})
        assert retried_metadata.get("uid") == uid, "Retry changed the ExternalIP UID"
        assert retried_metadata.get("generation") == generation, "Retry changed the ExternalIP generation"
        assert _validated_allocated_address(external_ip=retried_cr, selected_pool_cidr=pool["cidr"]) == address
        _assert_task_input_identity(
            external_ip=retried_cr, external_ip_id=external_ip_id, uid=uid, generation=generation
        )
        assert retry_job["manager"] == _FABRIC_MANAGER_TYPE
        assert int(retry_job["attempt"]) == int(first_job["attempt"]) + 1
        assert retry_job["jobID"] != first_job["jobID"], "Retry did not create a new AAP job"
        retried_api_external_ip = _wait_for_api_external_ip_address(
            grpc=grpc, external_ip_id=external_ip_id, address=address
        )
        assert retried_api_external_ip.get("id") == external_ip_id
        assert _pool_capacity(private_grpc, pool["id"]) == after_allocation, (
            "Retry of the same ExternalIP resource changed API pool capacity"
        )

        record_property("external_ip_retry_create_job_id", str(retry_job["jobID"]))
        grpc.delete_external_ip(external_ip_id=external_ip_id)

        wait_for_external_ip_deletion(k8s=k8s, name=external_ip_cr_name)
        poll_until(
            fn=lambda: _pool_capacity(private_grpc, pool["id"]),
            until=lambda status: status["allocated"] == 0 and status["available"] == initial_capacity["available"],
            retries=60,
            delay=2,
            description=f"{profile} ExternalIP release restores pool capacity",
        )
        record_property("external_ip_release", "ExternalIP CR removed; API pool capacity returned")
    finally:
        if external_ip_id:
            _cleanup_owned_external_ip(
                grpc=grpc,
                private_grpc=private_grpc,
                k8s=k8s,
                external_ip_id=external_ip_id,
                pool_id=pool["id"],
                tenant=pool["expected_external_ip_tenant"],
                expected_uid=external_ip_uid,
                external_ip_cr_name=external_ip_cr_name,
            )


def _active_network_class(private_grpc: GRPCClient) -> dict[str, Any]:
    network_classes = private_grpc.list_network_classes()
    assert len(network_classes) == 1, f"Expected one active NetworkClass, found {len(network_classes)}"
    return network_classes[0].get("object", network_classes[0])


def _network_class_value(network_class: dict[str, Any], snake_case: str, camel_case: str) -> str:
    return str(network_class.get(snake_case, network_class.get(camel_case, "")) or "")


def _declared_aap_collection_versions(manager: str) -> dict[str, str]:
    repository_root = Path(__file__).resolve().parents[4]

    def _yaml(path: str) -> dict[str, Any]:
        value = yaml.safe_load((repository_root / path).read_text(encoding="utf-8"))
        assert isinstance(value, dict), f"Expected a mapping in {path}"
        return value

    templates = _yaml("osac-aap/collections/ansible_collections/osac/templates/galaxy.yml")
    requirements = _yaml("osac-aap/collections/requirements.yml")
    required_versions = {
        item["name"]: str(item["version"])
        for item in requirements.get("collections", [])
        if item.get("name") and item.get("version")
    }
    versions = {
        "osac.templates": str(templates["version"]),
        "kubernetes.core": required_versions["kubernetes.core"],
        "ansible.utils": required_versions["ansible.utils"],
    }
    if manager == "netris":
        netris = _yaml("osac-aap/collections/ansible_collections/netris/controller/galaxy.yml")
        versions["netris.controller"] = str(netris["version"])
    return versions


def _pool_capacity(private_grpc: GRPCClient, pool_id: str) -> dict[str, int]:
    status = private_grpc.get_external_ip_pool(pool_id=pool_id).get("object", {}).get("status", {})
    return {
        "total": int(status.get("total", 0)),
        "allocated": int(status.get("allocated", 0)),
        "available": int(status.get("available", 0)),
    }


def _assert_task_input_identity(*, external_ip: dict[str, Any], external_ip_id: str, uid: str, generation: int) -> None:
    task_input = external_ip.get("status", {}).get("taskInput", {}) or {}
    resource = task_input.get("resource", {}) or {}
    metadata = resource.get("metadata", {}) or {}
    assert resource.get("id") == external_ip_id, "Stored Fabric task input does not use the Fulfillment API ID"
    assert metadata.get("uid") == uid, "Stored Fabric task input does not match the current CR UID"
    assert metadata.get("generation") == generation, "Stored Fabric task input does not match the current generation"


def _validated_allocated_address(*, external_ip: dict[str, Any], selected_pool_cidr: str) -> str:
    address = external_ip.get("status", {}).get("address", "")
    assert address, "ExternalIP has no allocated status.address"
    parsed_address = ipaddress.ip_address(address)
    assert isinstance(parsed_address, ipaddress.IPv4Address), f"Expected IPv4 allocation, got {address!r}"
    assert str(parsed_address) == address, f"Allocated address is not canonical IPv4 text: {address!r}"
    assert parsed_address in ipaddress.ip_network(selected_pool_cidr, strict=False), (
        f"Allocated address {address} is outside selected pool {selected_pool_cidr}"
    )
    return address


def _wait_for_api_external_ip_address(*, grpc: GRPCClient, external_ip_id: str, address: str) -> dict[str, Any]:
    def _get_external_ip() -> dict[str, Any]:
        return grpc.get_external_ip(external_ip_id=external_ip_id).get("object", {})

    return poll_until(
        fn=_get_external_ip,
        until=lambda external_ip: external_ip.get("status", {}).get("address") == address,
        retries=60,
        delay=2,
        description=f"Fulfillment ExternalIP {external_ip_id} status.address feedback",
    )


def _wait_for_successful_create_job(
    *, k8s: K8sClient, external_ip_name: str, previous_attempt: int = 0
) -> dict[str, Any]:
    def _latest() -> dict[str, Any] | None:
        external_ip = k8s.get_json(resource="externalip", name=external_ip_name)
        jobs = external_ip.get("status", {}).get("jobs", [])
        return next(
            (
                job
                for job in jobs
                if job.get("task") == _EXTERNAL_IP_CREATE_TASK and job.get("manager") == _FABRIC_MANAGER_TYPE
            ),
            None,
        )

    return poll_until(
        fn=_latest,
        until=lambda job: bool(
            job
            and int(job.get("attempt", 0)) > previous_attempt
            and job.get("jobID")
            and job.get("state") == "Succeeded"
        ),
        retries=120,
        delay=5,
        description=f"{external_ip_name} successful {_EXTERNAL_IP_CREATE_TASK} job after attempt {previous_attempt}",
    )


def _request_task_retry(*, k8s: K8sClient, external_ip_name: str, completed_job: dict[str, Any]) -> None:
    external_ip = k8s.get_json(resource="externalip", name=external_ip_name)
    jobs = external_ip.get("status", {}).get("jobs", [])
    matching = [
        (index, job)
        for index, job in enumerate(jobs)
        if job.get("task") == _EXTERNAL_IP_CREATE_TASK and job.get("manager") == _FABRIC_MANAGER_TYPE
    ]
    assert len(matching) == 1, f"Expected one {_EXTERNAL_IP_CREATE_TASK}/fabric status entry, got {matching!r}"
    index, job = matching[0]
    assert job.get("attempt") == completed_job.get("attempt")
    assert job.get("jobID") == completed_job.get("jobID")
    assert job.get("state") == "Succeeded", f"Cannot retry a job in state {job.get('state')!r}"
    patch = [
        {"op": "test", "path": f"/status/jobs/{index}/task", "value": _EXTERNAL_IP_CREATE_TASK},
        {"op": "test", "path": f"/status/jobs/{index}/manager", "value": _FABRIC_MANAGER_TYPE},
        {"op": "test", "path": f"/status/jobs/{index}/attempt", "value": completed_job["attempt"]},
        {"op": "test", "path": f"/status/jobs/{index}/state", "value": "Succeeded"},
        {"op": "replace", "path": f"/status/jobs/{index}/state", "value": "Cancelled"},
    ]
    command = ["kubectl"]
    if k8s.kubeconfig:
        command.extend(["--kubeconfig", k8s.kubeconfig])
    if k8s.as_system_admin:
        command.extend(["--as", "system:admin"])
    command.extend(
        [
            "patch",
            "externalip",
            external_ip_name,
            "-n",
            k8s.namespace,
            "--subresource=status",
            "--type=json",
            "-p",
            json.dumps(patch),
        ]
    )
    subprocess.run(command, capture_output=True, text=True, check=True)


def _is_not_found(exc: subprocess.CalledProcessError) -> bool:
    output = f"{exc.stdout or ''}\n{exc.stderr or ''}".lower()
    return "not found" in output or "notfound" in output


def _cleanup_owned_external_ip(
    *,
    grpc: GRPCClient,
    private_grpc: GRPCClient,
    k8s: K8sClient,
    external_ip_id: str,
    pool_id: str,
    tenant: str,
    expected_uid: str,
    external_ip_cr_name: str,
) -> None:
    api_external_ip: dict[str, Any] | None
    try:
        api_external_ip = private_grpc.get_private_external_ip(external_ip_id=external_ip_id).get("object", {})
    except subprocess.CalledProcessError as exc:
        if not _is_not_found(exc):
            raise
        api_external_ip = None

    if api_external_ip:
        assert api_external_ip.get("id") == external_ip_id, "Cleanup lookup returned a different ExternalIP"
        metadata = api_external_ip.get("metadata", {})
        assert metadata.get("tenant") == tenant, "Refusing cleanup of an ExternalIP from another tenant"
        pool_ref = api_external_ip.get("spec", {}).get("pool", {})
        assert pool_ref.get("id") == pool_id, "Refusing cleanup of an ExternalIP from another pool"

    if not external_ip_cr_name:
        external_ip_cr_name = k8s.get_external_ip_name(uuid=external_ip_id, checked=False)

    cr_exists = False
    if external_ip_cr_name:
        try:
            external_ip_cr = k8s.get_json(resource="externalip", name=external_ip_cr_name)
        except subprocess.CalledProcessError as exc:
            if not _is_not_found(exc):
                raise
        else:
            cr_exists = True
            cr_metadata = external_ip_cr.get("metadata", {})
            actual_uid = cr_metadata.get("uid", "")
            assert actual_uid, "Refusing cleanup of an ExternalIP CR without a UID"
            assert not expected_uid or actual_uid == expected_uid, (
                "Refusing cleanup of an ExternalIP with a different UID"
            )
            annotations = cr_metadata.get("annotations", {}) or {}
            assert annotations.get("osac.openshift.io/tenant") == tenant, (
                "Refusing cleanup of an ExternalIP CR from another tenant"
            )

    if api_external_ip:
        try:
            grpc.delete_external_ip(external_ip_id=external_ip_id)
        except subprocess.CalledProcessError as exc:
            if not _is_not_found(exc):
                raise

    if cr_exists and external_ip_cr_name:
        wait_for_external_ip_deletion(k8s=k8s, name=external_ip_cr_name)
