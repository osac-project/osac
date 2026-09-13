from __future__ import annotations

import json

from tests.e2e.core.grpc_client import PUBLIC_API, GRPCClient
from tests.e2e.core.runner import poll_until, run, run_unchecked


def test_enabled_services_after_upgrade(grpc: GRPCClient, namespace: str) -> None:
    _wait_for_rollout(namespace=namespace, deployment="fulfillment-service")
    _wait_for_rollout(namespace=namespace, deployment="osac-operator")

    poll_until(
        fn=lambda: grpc.call_unchecked(service=f"{PUBLIC_API}.BareMetalInstances/List"),
        until=lambda result: result[1] == 0,
        retries=30,
        delay=5,
        description="BareMetalInstances.List becomes available after upgrade",
        retry_on_error=True,
    )

    _verify_operator_controller_enabled(namespace=namespace)
    _verify_bmf_pods_running(namespace=namespace)

    for svc in (
        f"{PUBLIC_API}.Clusters/List",
        f"{PUBLIC_API}.ComputeInstances/List",
        f"{PUBLIC_API}.BareMetalInstances/List",
    ):
        output, rc = grpc.call_unchecked(service=svc)
        assert rc == 0, f"{svc} should succeed after enabling all services, got rc={rc}: {output}"



def _wait_for_rollout(*, namespace: str, deployment: str) -> None:
    run("kubectl", "rollout", "status", f"deployment/{deployment}", "-n", namespace, "--timeout=120s", timeout=150)


def _verify_operator_controller_enabled(*, namespace: str) -> None:
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
    assert pods.get("items"), "osac-operator pod(s) should exist after upgrade"
    found_manager = False
    for pod in pods["items"]:
        for container in pod.get("spec", {}).get("containers", []):
            if "manager" in container.get("name", ""):
                found_manager = True
                env_map = {e["name"]: e.get("value", "") for e in container.get("env", [])}
                actual = env_map.get("OSAC_ENABLE_BAREMETAL_INSTANCE_CONTROLLER")
                assert actual == "true", (
                    f"Expected OSAC_ENABLE_BAREMETAL_INSTANCE_CONTROLLER=true after upgrade, got: {actual}"
                )
    assert found_manager, "No manager container found in osac-operator pod(s) after upgrade"


def _verify_bmf_pods_running(*, namespace: str) -> None:
    poll_until(
        fn=lambda: run_unchecked(
            "kubectl",
            "--as",
            "system:admin",
            "get",
            "pods",
            "-n",
            namespace,
            "-l",
            "app.kubernetes.io/name=bare-metal-fulfillment-operator",
            "--no-headers",
        ),
        until=lambda result: result[1] == 0 and any(line.strip() for line in result[0].strip().splitlines()),
        retries=30,
        delay=5,
        description="BMF operator pods running after upgrade",
    )
