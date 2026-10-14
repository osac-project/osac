from __future__ import annotations

import ipaddress
import json
import logging
import subprocess
from collections.abc import Callable
from pathlib import Path
from typing import Any
from uuid import uuid4

import pytest
import yaml

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import (
    poll_until,
    wait_for_external_ip_allocated,
    wait_for_external_ip_cr,
    wait_for_external_ip_deletion,
)
from tests.e2e.core.k8s_client import K8sClient

logger = logging.getLogger(__name__)

_ALLOCATED_ADDRESS_ANNOTATION = "osac.openshift.io/allocated-address"
_IMPLEMENTATION_STRATEGY_ANNOTATION = "osac.openshift.io/implementation-strategy"
_EXTERNAL_IP_UID_LABEL = "osac.openshift.io/externalip-uid"
_EXTERNAL_IP_FINALIZER = "osac.openshift.io/externalip-finalizer"
_METALLB_NAMESPACE = "metallb-system"


class TestExternalIPOwnership:
    """Verify UID-owned ExternalIP allocation, retry, and release in each profile."""

    @pytest.mark.requires_vmaas
    def test_vmaas_k8s_only_allocation_retry_and_release(
        self,
        grpc: GRPCClient,
        private_grpc: GRPCClient,
        k8s_hub_client: K8sClient,
        external_ip_ownership_pool: dict[str, str],
        record_property: Callable[[str, str], None],
    ) -> None:
        _exercise_external_ip_ownership_lifecycle(
            profile="vmaas-k8s-only",
            expected_fabric_manager="",
            expected_k8s_manager="k8s_only",
            grpc=grpc,
            private_grpc=private_grpc,
            k8s=k8s_hub_client,
            pool=external_ip_ownership_pool,
            record_property=record_property,
        )

    @pytest.mark.requires_bmaas
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
            expected_fabric_manager="netris",
            expected_k8s_manager="",
            grpc=grpc,
            private_grpc=private_grpc,
            k8s=k8s_hub_client,
            pool=external_ip_ownership_pool,
            record_property=record_property,
        )


def _exercise_external_ip_ownership_lifecycle(
    *,
    profile: str,
    expected_fabric_manager: str,
    expected_k8s_manager: str,
    grpc: GRPCClient,
    private_grpc: GRPCClient,
    k8s: K8sClient,
    pool: dict[str, str],
    record_property: Callable[[str, str], None],
) -> None:
    network_class = _active_network_class(private_grpc)
    fabric_manager = _network_class_value(network_class, "fabric_manager", "fabricManager")
    k8s_manager = _network_class_value(network_class, "k8s_manager", "k8sManager")
    assert fabric_manager == expected_fabric_manager, (
        f"{profile} expected fabric_manager={expected_fabric_manager!r}, got {fabric_manager!r}"
    )
    assert k8s_manager == expected_k8s_manager, (
        f"{profile} expected k8s_manager={expected_k8s_manager!r}, got {k8s_manager!r}"
    )

    manager = expected_fabric_manager or expected_k8s_manager
    collection_versions = _declared_aap_collection_versions(manager)
    network_metadata = network_class.get("metadata", {})
    record_property("osac_profile", profile)
    record_property("osac_manager_implementation", manager)
    record_property("osac_network_class", str(network_metadata.get("name", "")))
    record_property("osac_network_class_id", str(network_class.get("id", "")))
    record_property("osac_fabric_manager", fabric_manager)
    record_property("osac_k8s_manager", k8s_manager)
    record_property("aap_collection_versions_source", "osac-aap source manifests")
    record_property("aap_collection_versions", json.dumps(collection_versions, sort_keys=True))

    initial_capacity = _pool_capacity(private_grpc, pool["id"])
    assert initial_capacity["allocated"] == 0, f"New test pool should start empty: {initial_capacity}"
    assert initial_capacity["available"] > 0, f"New test pool has no free addresses: {initial_capacity}"

    external_ip_id = ""
    external_ip_cr_name = ""
    service_name = ""
    try:
        external_ip_id = grpc.create_external_ip(name=f"ref-eip-owner-{uuid4().hex[:8]}", pool=pool["id"])
        external_ip_cr_name = wait_for_external_ip_cr(k8s=k8s, uuid=external_ip_id)
        wait_for_external_ip_allocated(k8s=k8s, name=external_ip_cr_name)

        first_job = _wait_for_successful_provision_job(k8s=k8s, external_ip_name=external_ip_cr_name)
        allocated_cr = k8s.get_json(resource="externalip", name=external_ip_cr_name)
        metadata = allocated_cr.get("metadata", {})
        uid = metadata.get("uid", "")
        generation = metadata.get("generation")
        assert uid, "Allocated ExternalIP CR has no UID"
        assert generation is not None, "Allocated ExternalIP CR has no generation"
        tenant = (metadata.get("annotations") or {}).get("osac.openshift.io/tenant", "")
        assert tenant, "Allocated ExternalIP CR has no tenant annotation"
        assert (metadata.get("annotations") or {}).get(_IMPLEMENTATION_STRATEGY_ANNOTATION) == manager

        address = _validated_allocated_address(external_ip=allocated_cr, selected_pool_cidr=pool["cidr"])
        cr_status = allocated_cr.get("status", {})
        assert cr_status.get("state") == "Allocated", f"Expected Allocated CR state, got {cr_status.get('state')!r}"
        assert cr_status.get("address") == address, "CR status address differs from the manager annotation"
        api_external_ip = grpc.get_external_ip(external_ip_id=external_ip_id).get("object", {})
        assert api_external_ip.get("status", {}).get("address") == address, (
            "Fulfillment API status address differs from the manager annotation"
        )
        after_allocation = _pool_capacity(private_grpc, pool["id"])
        assert after_allocation["allocated"] == 1, f"Expected one reserved address after allocation: {after_allocation}"
        assert after_allocation["available"] == initial_capacity["available"] - 1

        if manager == "k8s_only":
            service_name = f"osac-eip-{uid}"
            reservation = _get_metallb_reservation(k8s=k8s, service_name=service_name)
            assert reservation is not None, f"UID-owned MetalLB Service {service_name} is missing"
            _assert_metallb_reservation(reservation, uid=uid, tenant=tenant, external_ip_name=external_ip_cr_name)
            assert _reservation_address(reservation) == address

        record_property("external_ip_id", external_ip_id)
        record_property("external_ip_uid", uid)
        record_property("external_ip_generation", str(generation))
        record_property("external_ip_address", address)
        record_property("external_ip_first_provision_job_id", str(first_job["jobID"]))

        _clear_provisioning_history(k8s=k8s, external_ip_name=external_ip_cr_name)
        retry_job = _wait_for_successful_provision_job(
            k8s=k8s, external_ip_name=external_ip_cr_name, previous_job_id=str(first_job["jobID"])
        )
        retried_cr = k8s.get_json(resource="externalip", name=external_ip_cr_name)
        retried_metadata = retried_cr.get("metadata", {})
        assert retried_metadata.get("uid") == uid, "Retry changed the ExternalIP UID"
        assert retried_metadata.get("generation") == generation, "Retry changed the ExternalIP generation"
        assert _validated_allocated_address(external_ip=retried_cr, selected_pool_cidr=pool["cidr"]) == address
        assert retried_cr.get("status", {}).get("address") == address
        assert (
            grpc.get_external_ip(external_ip_id=external_ip_id).get("object", {}).get("status", {}).get("address")
            == address
        )
        assert _pool_capacity(private_grpc, pool["id"]) == after_allocation, "Same-UID retry changed API pool capacity"
        assert retry_job["jobID"] != first_job["jobID"], "Retry did not create a new provision job"

        if manager == "k8s_only":
            retried_reservation = _get_metallb_reservation(k8s=k8s, service_name=service_name)
            assert retried_reservation is not None, f"Retry removed UID-owned MetalLB Service {service_name}"
            _assert_metallb_reservation(
                retried_reservation, uid=uid, tenant=tenant, external_ip_name=external_ip_cr_name
            )
            assert _reservation_address(retried_reservation) == address

        record_property("external_ip_retry_provision_job_id", str(retry_job["jobID"]))
        grpc.delete_external_ip(external_ip_id=external_ip_id)

        pending_release = poll_until(
            fn=lambda: _release_pending_observation(
                k8s=k8s,
                private_grpc=private_grpc,
                external_ip_name=external_ip_cr_name,
                pool_id=pool["id"],
                manager=manager,
                service_name=service_name,
            ),
            until=lambda observation: observation is not None,
            retries=300,
            delay=1,
            description=f"{profile} release holds the ExternalIP finalizer and pool capacity",
        )
        assert pending_release["capacity"]["allocated"] == 1, (
            f"Pool capacity returned while the ExternalIP finalizer was still present: {pending_release['capacity']}"
        )
        assert pending_release["capacity"]["available"] == after_allocation["available"]

        wait_for_external_ip_deletion(k8s=k8s, name=external_ip_cr_name)
        released_capacity = poll_until(
            fn=lambda: _pool_capacity(private_grpc, pool["id"]),
            until=lambda status: status["allocated"] == 0 and status["available"] == initial_capacity["available"],
            retries=60,
            delay=2,
            description=f"{profile} ExternalIP release restores pool capacity",
        )
        assert released_capacity["allocated"] == 0
        assert released_capacity["available"] == initial_capacity["available"]
        if manager == "k8s_only":
            assert _get_metallb_reservation(k8s=k8s, service_name=service_name) is None, (
                f"UID-owned MetalLB Service {service_name} remains after ExternalIP release"
            )
        record_property("external_ip_release", "finalizer completed; pool capacity returned once")
    finally:
        if external_ip_id:
            try:
                grpc.delete_external_ip(external_ip_id=external_ip_id)
            except subprocess.CalledProcessError as exc:
                if not _is_not_found(exc):
                    raise
            if not external_ip_cr_name:
                external_ip_cr_name = k8s.get_external_ip_name(uuid=external_ip_id, checked=False)
            if external_ip_cr_name:
                wait_for_external_ip_deletion(k8s=k8s, name=external_ip_cr_name)


def _active_network_class(private_grpc: GRPCClient) -> dict[str, Any]:
    network_classes = private_grpc.list_network_classes()
    assert len(network_classes) == 1, f"Expected one active NetworkClass, found {len(network_classes)}"
    return network_classes[0].get("object", network_classes[0])


def _network_class_value(network_class: dict[str, Any], snake_case: str, camel_case: str) -> str:
    return str(network_class.get(snake_case, network_class.get(camel_case, "")) or "")


def _declared_aap_collection_versions(manager: str) -> dict[str, str]:
    repository_root = Path(__file__).resolve().parents[3]

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


def _validated_allocated_address(*, external_ip: dict[str, Any], selected_pool_cidr: str) -> str:
    annotations = external_ip.get("metadata", {}).get("annotations", {}) or {}
    address = annotations.get(_ALLOCATED_ADDRESS_ANNOTATION, "")
    assert address, "ExternalIP has no manager-written allocated-address annotation"
    parsed_address = ipaddress.ip_address(address)
    assert isinstance(parsed_address, ipaddress.IPv4Address), f"Expected IPv4 allocation, got {address!r}"
    assert str(parsed_address) == address, f"Allocated address is not canonical IPv4 text: {address!r}"
    assert parsed_address in ipaddress.ip_network(selected_pool_cidr, strict=False), (
        f"Allocated address {address} is outside selected pool {selected_pool_cidr}"
    )
    return address


def _wait_for_successful_provision_job(
    *, k8s: K8sClient, external_ip_name: str, previous_job_id: str = ""
) -> dict[str, Any]:
    def _latest() -> dict[str, Any] | None:
        external_ip = k8s.get_json(resource="externalip", name=external_ip_name)
        jobs = external_ip.get("status", {}).get("provisioningJobs", [])
        provision_jobs = [job for job in jobs if job.get("type") == "provision"]
        return provision_jobs[-1] if provision_jobs else None

    return poll_until(
        fn=_latest,
        until=lambda job: bool(
            job and job.get("jobID") and job.get("jobID") != previous_job_id and job.get("state") == "Succeeded"
        ),
        retries=120,
        delay=5,
        description=f"{external_ip_name} successful provision job after {previous_job_id or 'initial allocation'}",
    )


def _clear_provisioning_history(*, k8s: K8sClient, external_ip_name: str) -> None:
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
            "--type=merge",
            "-p",
            json.dumps({"status": {"provisioningJobs": []}}),
        ]
    )
    subprocess.run(command, capture_output=True, text=True, check=True)


def _get_metallb_reservation(*, k8s: K8sClient, service_name: str) -> dict[str, Any] | None:
    try:
        return k8s.get_json(resource="service", name=service_name, namespace=_METALLB_NAMESPACE)
    except subprocess.CalledProcessError as exc:
        if _is_not_found(exc):
            return None
        raise


def _assert_metallb_reservation(service: dict[str, Any], *, uid: str, tenant: str, external_ip_name: str) -> None:
    metadata = service.get("metadata", {})
    labels = metadata.get("labels", {}) or {}
    annotations = metadata.get("annotations", {}) or {}
    assert labels.get(_EXTERNAL_IP_UID_LABEL) == uid, "MetalLB Service is not owned by the current ExternalIP UID"
    assert annotations.get("osac.openshift.io/tenant") == tenant, (
        "MetalLB Service tenant differs from ExternalIP tenant"
    )
    assert annotations.get("osac.openshift.io/owner-reference") == f"ExternalIP/{external_ip_name}"


def _reservation_address(service: dict[str, Any]) -> str:
    ingress = service.get("status", {}).get("loadBalancer", {}).get("ingress", [])
    assert ingress and ingress[0].get("ip"), "MetalLB Service has no assigned IPv4 address"
    return ingress[0]["ip"]


def _release_pending_observation(
    *, k8s: K8sClient, private_grpc: GRPCClient, external_ip_name: str, pool_id: str, manager: str, service_name: str
) -> dict[str, Any] | None:
    try:
        external_ip = k8s.get_json(resource="externalip", name=external_ip_name)
    except subprocess.CalledProcessError as exc:
        if _is_not_found(exc):
            return None
        raise

    metadata = external_ip.get("metadata", {})
    if not metadata.get("deletionTimestamp") or _EXTERNAL_IP_FINALIZER not in metadata.get("finalizers", []):
        return None

    if manager == "k8s_only" and _get_metallb_reservation(k8s=k8s, service_name=service_name) is not None:
        return None

    return {"capacity": _pool_capacity(private_grpc, pool_id)}


def _is_not_found(exc: subprocess.CalledProcessError) -> bool:
    output = f"{exc.stdout or ''}\n{exc.stderr or ''}".lower()
    return "not found" in output or "notfound" in output
