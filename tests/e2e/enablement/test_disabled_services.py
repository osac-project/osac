from __future__ import annotations

import json
import subprocess

import pytest

from tests.e2e.core.grpc_client import PRIVATE_API, PUBLIC_API, GRPCClient
from tests.e2e.core.helpers import assert_grpc_rejected
from tests.e2e.core.runner import run, run_unchecked


def test_disabled_service_grpc_unavailable(grpc: GRPCClient) -> None:
    with pytest.raises(subprocess.CalledProcessError) as exc_info:
        grpc.call(service=f"{PUBLIC_API}.BareMetalInstances/List")
    assert_grpc_rejected(exc_info, "Unavailable")
    combined = (exc_info.value.stderr or "") + (exc_info.value.stdout or "")
    assert "bmaas" in combined.lower(), f"Error should mention bmaas service, got: {combined.strip()}"


def test_disabled_service_absent_from_reflection(fulfillment_address: str) -> None:
    output = run("grpcurl", "-insecure", fulfillment_address, "list")
    services = output.strip().splitlines()
    assert f"{PUBLIC_API}.BareMetalInstances" not in services, (
        "Disabled BareMetalInstances should not appear in reflection"
    )
    assert f"{PUBLIC_API}.Clusters" in services, "Enabled Clusters should appear in reflection"
    assert f"{PUBLIC_API}.ComputeInstances" in services, "Enabled ComputeInstances should appear"


def test_disabled_service_rest_not_registered(fulfillment_address: str, grpc: GRPCClient) -> None:
    host = fulfillment_address.rsplit(":", 1)[0]
    output, _rc = run_unchecked(
        "curl",
        "-sk",
        "-H",
        f"Authorization: Bearer {grpc.token}",
        "-o",
        "-",
        "-w",
        "\n%{http_code}",
        f"https://{host}/api/fulfillment/v1/baremetal_instances",
    )
    lines = output.strip().splitlines()
    status_code = lines[-1] if lines else ""
    body = "\n".join(lines[:-1])
    assert "bareMetalInstances" not in body and "bare_metal_instances" not in body, (
        f"Disabled service REST endpoint should not return valid BMaaS data, got: {body[:200]}"
    )
    assert status_code == "503", f"Disabled service REST endpoint should return 503, got {status_code}: {body[:200]}"


def test_shared_infrastructure_always_available(private_grpc: GRPCClient) -> None:
    for service_method in (
        f"{PRIVATE_API}.Tenants/List",
        f"{PRIVATE_API}.VirtualNetworks/List",
        f"{PRIVATE_API}.StorageTiers/List",
    ):
        output, rc = private_grpc.call_unchecked(service=service_method)
        assert rc == 0, f"{service_method} should succeed (shared infra), got rc={rc}: {output}"


def test_disabled_service_controllers_not_running(namespace: str) -> None:
    pods_json = run(
        "kubectl",
        "--as",
        "system:admin",
        "get",
        "pods",
        "-n",
        namespace,
        "-l",
        "app.kubernetes.io/name=operator",
        "-o",
        "json",
    )
    pods = json.loads(pods_json)
    assert pods.get("items"), "osac-operator pod(s) should exist"
    found_manager = False
    for pod in pods["items"]:
        for container in pod.get("spec", {}).get("containers", []):
            if "manager" in container.get("name", ""):
                found_manager = True
                env_map = {e["name"]: e.get("value", "") for e in container.get("env", [])}
                actual = env_map.get("OSAC_ENABLE_BAREMETAL_INSTANCE_CONTROLLER")
                assert actual == "false", f"Expected OSAC_ENABLE_BAREMETAL_INSTANCE_CONTROLLER=false, got: {actual}"
    assert found_manager, "No manager container found in osac-operator pod(s)"

    bmf_json = run(
        "kubectl",
        "--as",
        "system:admin",
        "get",
        "pods",
        "-n",
        namespace,
        "-l",
        "app.kubernetes.io/name=bare-metal-fulfillment-operator",
        "-o",
        "json",
    )
    bmf_pods = json.loads(bmf_json).get("items", [])
    assert not bmf_pods, f"BMF operator pods should not exist when BMaaS is disabled, found: {bmf_pods}"


def test_enabled_services_function_normally(grpc: GRPCClient) -> None:
    clusters_output, clusters_rc = grpc.call_unchecked(service=f"{PUBLIC_API}.Clusters/List")
    assert clusters_rc == 0, f"Clusters.List (CaaS) should succeed, got rc={clusters_rc}: {clusters_output}"

    ci_output, ci_rc = grpc.call_unchecked(service=f"{PUBLIC_API}.ComputeInstances/List")
    assert ci_rc == 0, f"ComputeInstances.List (VMaaS) should succeed, got rc={ci_rc}: {ci_output}"


def test_hosttypes_filters_disabled_service(grpc: GRPCClient) -> None:
    response = grpc.call(service=f"{PUBLIC_API}.HostTypes/List")
    items = response.get("items", [])
    for item in items:
        spec = item.get("object", item).get("spec", item.get("object", item))
        interfaces = spec.get("interfaces", [])
        assert not interfaces, (
            f"With BMaaS disabled, no host type should have interfaces (bare-metal), "
            f"but found: {item.get('object', item).get('metadata', {}).get('name', 'unknown')}"
        )
