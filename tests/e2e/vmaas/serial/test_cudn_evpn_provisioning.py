"""End-to-end EVPN provisioning, guest IPAM, and cleanup coverage."""

from __future__ import annotations

import http.cookiejar
import ipaddress
import json
import os
import re
import urllib.error
import urllib.request
from datetime import datetime
from typing import Any
from urllib.parse import urljoin

import pytest

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import (
    poll_until,
    unique_name,
    wait_for_cr,
    wait_for_deletion,
    wait_for_grpc_removal,
    wait_for_grpc_subnet_ready,
    wait_for_provision,
    wait_for_running,
    wait_for_subnet_cr,
    wait_for_virtual_network_cr,
    wait_for_virtual_network_ready,
    wait_for_vmi_ip,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.vmaas.networking_lifecycle_helpers import delete_and_wait_for_subnet, delete_and_wait_for_virtual_network

pytestmark = pytest.mark.serial

_EXCLUDED_CIDR_PREFIXES = {199, 200, 209, 210}
_NETRIS_TIMEOUT_SECONDS = 30


def _require_evpn_netris_profile(private_grpc: GRPCClient) -> None:
    network_classes = private_grpc.list_network_classes()
    if not any(
        network_class.get("fabricManager") == "netris" and network_class.get("k8sManager") == "cudn_evpn"
        for network_class in network_classes
    ):
        pytest.skip("requires the opt-in cudn-evpn-netris-test profile")


def _subnet_jobs(k8s: K8sClient, subnet_name: str) -> list[dict[str, Any]]:
    return k8s.get_json(resource="subnet", name=subnet_name).get("status", {}).get("provisioningJobs", [])


def _wait_for_fabric_before_k8s(k8s: K8sClient, subnet_name: str) -> list[dict[str, Any]]:
    def observe() -> tuple[list[dict[str, Any]], bool]:
        jobs = _subnet_jobs(k8s, subnet_name)
        fabric_jobs = [job for job in jobs if job.get("type") == "provision" and job.get("target") == "fabric"]
        k8s_jobs = [job for job in jobs if job.get("type") == "provision" and job.get("target") == "k8s"]
        latest_fabric = max(fabric_jobs, key=lambda job: job["timestamp"], default=None)
        fabric_state = latest_fabric.get("state") if latest_fabric else None

        if fabric_state in ("Failed", "Canceled"):
            raise AssertionError(f"fabric provisioning failed before K8s dispatch: {latest_fabric!r}")
        if k8s_jobs:
            assert fabric_state == "Succeeded", f"K8s target appeared before fabric succeeded: {jobs!r}"
            latest_k8s = max(k8s_jobs, key=lambda job: job["timestamp"])
            output_maps = k8s.list_json(resource="configmaps", namespace=k8s.namespace).get("items", [])
            fabric_output = next(
                (
                    item
                    for item in output_maps
                    if item.get("metadata", {}).get("name") == f"subnet-{subnet_name}-fabric-output"
                ),
                None,
            )
            assert fabric_output is not None, "K8s target appeared without the fabric output ConfigMap"
            output_created = datetime.fromisoformat(
                fabric_output["metadata"]["creationTimestamp"].replace("Z", "+00:00")
            )
            k8s_started = datetime.fromisoformat(latest_k8s["timestamp"].replace("Z", "+00:00"))
            assert output_created <= k8s_started, (
                "K8s target started before the fabric output ConfigMap existed: "
                f"output={output_created.isoformat()}, k8s={k8s_started.isoformat()}"
            )
            return jobs, True
        return jobs, False

    return poll_until(
        fn=observe,
        until=lambda snapshot: snapshot[1],
        retries=300,
        delay=1,
        description=f"fabric completion before K8s dispatch for {subnet_name}",
    )[0]


class _NetrisAPI:
    def __init__(self) -> None:
        self._base_url = os.environ.get("NETRIS_CONTROLLER_URL", "").strip()
        username = os.environ.get("NETRIS_USERNAME", "").strip()
        password = os.environ.get("NETRIS_PASSWORD", "")
        if not self._base_url or not username or not password:
            pytest.fail(
                "the opt-in EVPN profile requires NETRIS_CONTROLLER_URL, NETRIS_USERNAME, and NETRIS_PASSWORD "
                "in the E2E runner to verify fabric resources and DHCP leases"
            )

        cookies = http.cookiejar.CookieJar()
        self._client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(cookies))
        self._request_json("api/auth", data={"user": username, "password": password, "auth_scheme_id": 1})

    def get(self, path: str) -> dict[str, Any]:
        return self._request_json(path.lstrip("/"))

    def _request_json(self, path: str, *, data: dict[str, Any] | None = None) -> dict[str, Any]:
        request = urllib.request.Request(
            urljoin(f"{self._base_url.rstrip('/')}/", path),
            data=json.dumps(data).encode("utf-8") if data is not None else None,
            headers={"Content-Type": "application/json", "Cache-Control": "no-cache", "Pragma": "no-cache"},
            method="POST" if data is not None else "GET",
        )
        try:
            with self._client.open(request, timeout=_NETRIS_TIMEOUT_SECONDS) as response:
                payload = json.load(response)
        except urllib.error.HTTPError as exc:
            pytest.fail(f"Netris API request returned HTTP {exc.code}")
        except (urllib.error.URLError, TimeoutError, json.JSONDecodeError):
            pytest.fail("Netris API request failed; check the opt-in profile endpoint and runner connectivity")

        if data is not None:
            return payload
        if not isinstance(payload.get("data"), (dict, list)):
            pytest.fail(f"Netris API response for {path} did not contain a data object")
        return payload


def _reserved_range_contains(address: ipaddress.IPv4Address, value: str) -> bool:
    return address in ipaddress.ip_network(value.strip(), strict=True)


def _normalize_mac(value: str) -> str:
    return re.sub(r"[^0-9a-f]", "", value.lower())


def _vmi_macs(vmi: dict[str, Any]) -> set[str]:
    candidates = [
        interface.get("mac", "")
        for interface in vmi.get("status", {}).get("interfaces", [])
        if isinstance(interface, dict)
    ]
    candidates.extend(
        interface.get("macAddress", "")
        for interface in vmi.get("spec", {}).get("domain", {}).get("devices", {}).get("interfaces", [])
        if isinstance(interface, dict)
    )
    return {_normalize_mac(mac) for mac in candidates if mac}


def _assert_no_netris_dhcp_lease(netris: _NetrisAPI, subnet_name: str, vm_macs: set[str]) -> None:
    subnets = netris.get("/api/v2/ipam/subnets").get("data", [])
    ipam_subnet = next((item for item in subnets if item.get("name") == subnet_name), None)
    assert ipam_subnet is not None, f"Netris IPAM subnet {subnet_name!r} was not found"
    assert ipam_subnet.get("id") is not None, f"Netris IPAM subnet {subnet_name!r} has no ID"

    hosts = netris.get(f"/api/v2/ipam/hosts/{ipam_subnet['id']}").get("data", [])
    leased_macs = {
        _normalize_mac(mac.get("address", ""))
        for host in hosts
        for mac in host.get("mac", [])
        if isinstance(mac, dict) and mac.get("address")
    }
    assert vm_macs.isdisjoint(leased_macs), "Netris DHCP/IPAM contains a lease for the VM interface MAC"


def _assert_netris_resources(
    netris: _NetrisAPI, *, subnet_name: str, virtual_network_name: str, fabric_output: dict[str, str]
) -> None:
    vpcs = netris.get("/api/v2/vpc").get("data", [])
    vnets = netris.get("/api/v2/vnet").get("data", [])
    matching_vpcs = [item for item in vpcs if item.get("name") == virtual_network_name]
    matching_vnets = [item for item in vnets if item.get("name") == subnet_name]
    assert len(matching_vpcs) == 1, f"Expected one Netris VPC named {virtual_network_name!r}"
    assert len(matching_vnets) == 1, f"Expected one Netris V-Net named {subnet_name!r}"
    vpc = matching_vpcs[0]
    vnet = matching_vnets[0]
    assert vpc is not None and vpc.get("id") is not None, f"Netris VPC {virtual_network_name!r} is missing"
    assert vnet is not None and vnet.get("id") is not None, f"Netris V-Net {subnet_name!r} is missing"
    assert vnet.get("vxlanID") is not None, f"Netris V-Net {subnet_name!r} has no VXLAN ID"
    assert int(vnet["vxlanID"]) == int(fabric_output["l2_vni"]), "V-Net VXLAN ID differs from fabric output"
    assert vpc.get("vni") is not None, f"Netris VPC {virtual_network_name!r} has no VNI"
    assert int(vpc["vni"]) == int(fabric_output["l3_vni"]), "VPC VNI differs from fabric output"

    detail = netris.get(f"/api/v2/vnet/{vnet['id']}").get("data")
    assert isinstance(detail, dict), f"Netris V-Net {subnet_name!r} has no detail object"
    gateways = detail.get("gateways", [])
    assert len(gateways) == 1, f"Netris V-Net {subnet_name!r} must have exactly one gateway"
    gateway = gateways[0]
    reserved_range = ipaddress.ip_network(fabric_output["fabric_reserved_range"], strict=True)
    expected_gateway = reserved_range.network_address + 1
    expected_dhcp_start = reserved_range.network_address + 2
    expected_dhcp_end = reserved_range.broadcast_address - 1
    assert gateway.get("prefix") == f"{expected_gateway}/{reserved_range.prefixlen}", (
        "Netris gateway prefix differs from the reserved fabric range"
    )
    assert gateway.get("dhcpEnabled") is True, "Netris DHCP is not enabled for the reserved fabric range"
    assert gateway.get("dhcpStartIp") == str(expected_dhcp_start), "Netris DHCP start differs from reserved range"
    assert gateway.get("dhcpEndIp") == str(expected_dhcp_end), "Netris DHCP end differs from reserved range"


def test_cudn_evpn_provisions_fabric_and_reserved_vm_ip(
    grpc: GRPCClient,
    private_grpc: GRPCClient,
    cli: OsacCLI,
    k8s_hub_client: K8sClient,
    k8s_virt_client: K8sClient,
    vm_template: str,
) -> None:
    """Verify fabric VNIs feed a Ready CUDN and VM addresses avoid Netris reservations."""
    _require_evpn_netris_profile(private_grpc)
    netris = _NetrisAPI()

    run_id = unique_name("evpn")
    cidr_prefix = int(run_id.rsplit("-", maxsplit=1)[-1][:2], 16) % 254
    while cidr_prefix in _EXCLUDED_CIDR_PREFIXES:
        cidr_prefix = (cidr_prefix + 1) % 254
    vnet_cidr = f"10.{cidr_prefix}.0.0/16"
    subnet_cidr = f"10.{cidr_prefix}.1.0/24"
    network = ipaddress.ip_network(subnet_cidr)
    vnet_id: str | None = None
    vnet_name: str | None = None
    subnet_id: str | None = None
    subnet_name: str | None = None
    compute_instance_id: str | None = None
    compute_instance_name: str | None = None

    try:
        vnet_name = f"{run_id}-vpc"
        vnet_id = grpc.create_virtual_network(name=vnet_name, ipv4_cidr=vnet_cidr)
        vnet_name = wait_for_virtual_network_cr(k8s=k8s_hub_client, uuid=vnet_id)
        wait_for_virtual_network_ready(k8s=k8s_hub_client, name=vnet_name)
        subnet_name = f"{run_id}-subnet"
        subnet_id = grpc.create_subnet(name=subnet_name, virtual_network=vnet_id, ipv4_cidr=subnet_cidr)
        subnet_name = wait_for_subnet_cr(k8s=k8s_hub_client, uuid=subnet_id)
        _wait_for_fabric_before_k8s(k8s_hub_client, subnet_name)
        wait_for_grpc_subnet_ready(grpc=grpc, subnet_id=subnet_id)
        jobs = _subnet_jobs(k8s_hub_client, subnet_name)
        fabric_job = max(
            (job for job in jobs if job.get("type") == "provision" and job.get("target") == "fabric"),
            key=lambda job: job["timestamp"],
        )
        k8s_job = max(
            (job for job in jobs if job.get("type") == "provision" and job.get("target") == "k8s"),
            key=lambda job: job["timestamp"],
        )
        assert fabric_job["state"] == k8s_job["state"] == "Succeeded"

        fabric_output = k8s_hub_client.get_json(
            resource="configmap", name=f"subnet-{subnet_name}-fabric-output", namespace=k8s_hub_client.namespace
        ).get("data", {})
        for key in ("l2_vni", "l3_vni", "fabric_reserved_range"):
            assert fabric_output.get(key), f"fabric output is missing {key}"
        reserved_network = ipaddress.ip_network(fabric_output["fabric_reserved_range"], strict=True)
        assert reserved_network.subnet_of(network) and reserved_network != network, (
            f"fabric reserved range {reserved_network} must be a proper CIDR subnetwork of {network}"
        )
        _assert_netris_resources(
            netris, subnet_name=subnet_name, virtual_network_name=vnet_name, fabric_output=fabric_output
        )

        namespaces = k8s_hub_client.list_json(resource="namespaces").get("items", [])
        namespace = next((item for item in namespaces if item.get("metadata", {}).get("name") == subnet_name), None)
        assert namespace is not None, f"Subnet workload namespace {subnet_name!r} is missing"
        tenant_id = namespace.get("metadata", {}).get("annotations", {}).get("osac.openshift.io/tenant", "")
        assert tenant_id, "Subnet workload namespace is missing tenant isolation metadata"
        assert namespace.get("metadata", {}).get("annotations", {}).get("osac.openshift.io/owner-reference") == (
            f"Subnet/{subnet_name}"
        )
        assert namespace.get("metadata", {}).get("labels", {}).get("virtual-network") == vnet_name

        cudn = k8s_hub_client.get_json(resource="clusteruserdefinednetwork", name=vnet_name, namespace="")
        network_spec = cudn.get("spec", {}).get("network", {})
        assert network_spec.get("transport") == "EVPN"
        assert network_spec.get("evpn", {}).get("macVRF", {}).get("vni") == int(fabric_output["l2_vni"])
        assert network_spec.get("evpn", {}).get("ipVRF", {}).get("vni") == int(fabric_output["l3_vni"])
        assert network_spec.get("layer2", {}).get("reservedSubnets") == [fabric_output["fabric_reserved_range"]]
        assert any(
            condition.get("type") == "Ready" and condition.get("status") == "True"
            for condition in cudn.get("status", {}).get("conditions", [])
        ), "CUDN is not Ready"
        assert cudn.get("status", {}).get("vrfName"), "Ready CUDN has no status.vrfName"

        compute_instance_name = unique_name("evpn-vm")
        compute_instance_id = cli.create_compute_instance(
            name=compute_instance_name, template=vm_template, network_attachments=[{"subnet": subnet_id}]
        )
        compute_instance_name = wait_for_cr(k8s=k8s_hub_client, uuid=compute_instance_id)
        wait_for_provision(k8s=k8s_hub_client, name=compute_instance_name)
        wait_for_running(k8s=k8s_hub_client, name=compute_instance_name)
        vm_namespace = k8s_hub_client.get_compute_instance_vm_namespace(name=compute_instance_name)
        vm_ip = ipaddress.ip_address(
            wait_for_vmi_ip(
                k8s=k8s_virt_client, vmi_namespace=vm_namespace, compute_instance_name=compute_instance_name
            )
        )
        assert vm_ip in network, f"VM IP {vm_ip} is outside Subnet CIDR {network}"
        assert not _reserved_range_contains(vm_ip, fabric_output["fabric_reserved_range"]), (
            f"VM IP {vm_ip} falls within fabric-reserved range {fabric_output['fabric_reserved_range']!r}"
        )

        vmi = k8s_virt_client.get_vmi_json(vmi_namespace=vm_namespace, compute_instance_name=compute_instance_name)
        vm_macs = _vmi_macs(vmi)
        assert vm_macs, f"VMI for {compute_instance_name} did not report a network MAC"
        _assert_no_netris_dhcp_lease(netris, subnet_name, vm_macs)
    finally:
        try:
            if compute_instance_id is not None:
                cli.delete_compute_instance(uuid=compute_instance_id)
                if compute_instance_name is not None:
                    wait_for_deletion(k8s=k8s_hub_client, name=compute_instance_name)
                wait_for_grpc_removal(grpc=grpc, uuid=compute_instance_id)
        finally:
            try:
                if subnet_id is not None:
                    delete_and_wait_for_subnet(grpc, k8s_hub_client, subnet_id, subnet_name)
                    cudns = k8s_hub_client.list_json(resource="clusteruserdefinednetwork").get("items", [])
                    namespaces = k8s_hub_client.list_json(resource="namespaces").get("items", [])
                    assert not any(item.get("metadata", {}).get("name") == vnet_name for item in cudns)
                    assert not any(item.get("metadata", {}).get("name") == subnet_name for item in namespaces)
            finally:
                if vnet_id is not None:
                    delete_and_wait_for_virtual_network(grpc, k8s_hub_client, vnet_id, vnet_name)
