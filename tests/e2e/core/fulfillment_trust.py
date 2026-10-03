"""Assertions for a deployed fulfillment trust rollout."""

from __future__ import annotations

import base64
import hashlib
import json
import subprocess
from collections.abc import Callable
from typing import Any

from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.runner import env, poll_until, run, run_unchecked


def _condition(order: dict[str, Any]) -> dict[str, Any]:
    return next(
        (item for item in order.get("status", {}).get("conditions", []) if item.get("type") == "FulfillmentTrustReady"),
        {},
    )


def _tenant_readonly_kubectl(k8s: K8sClient, order: dict[str, Any]) -> Callable[..., str]:
    """Use management access for read-only target inspection without writing kubeconfig to disk."""
    reference = order.get("status", {}).get("clusterReference", {})
    hosted_cluster_name = reference.get("hostedClusterName")
    hosted_cluster_namespace = reference.get("namespace")
    assert hosted_cluster_name and hosted_cluster_namespace, "ClusterOrder has no HostedCluster reference"
    hcp_namespace = f"{hosted_cluster_namespace}-{hosted_cluster_name}"
    hcp = json.loads(
        run(*k8s._base(), "get", "hostedcontrolplane", hosted_cluster_name, "-n", hcp_namespace, "-o", "json")
    )
    kubeconfig_ref = hcp.get("status", {}).get("kubeConfig", {})
    secret_name = kubeconfig_ref.get("name")
    secret_key = kubeconfig_ref.get("key")
    assert secret_name and secret_key, "HostedControlPlane has no kubeconfig Secret reference"
    secret = json.loads(run(*k8s._base(), "get", "secret", secret_name, "-n", hcp_namespace, "-o", "json"))
    kubeconfig = base64.b64decode(secret["data"][secret_key], validate=True)
    namespace = env("OSAC_FULFILLMENT_TRUST_TENANT_NAMESPACE", "osac-csi")

    def kubectl(*args: str) -> str:
        result = subprocess.run(
            ["kubectl", "--kubeconfig=/dev/stdin", "-n", namespace, *args],
            input=kubeconfig,
            capture_output=True,
            timeout=300,
            check=True,
        )
        return result.stdout.decode().strip()

    return kubectl


def assert_cluster_trust(
    k8s: K8sClient, order_name: str, *, require_csi: bool = False, expected_bundle_hash: str | None = None
) -> str:
    """Prove the order identity, target ConfigMap, and CSI rollout converge."""
    order = poll_until(
        fn=lambda: k8s.get_json(resource="clusterorder", name=order_name),
        until=lambda item: (
            _condition(item).get("status") == "True"
            and (
                expected_bundle_hash is None
                or item.get("status", {}).get("fulfillmentTrustBundleHash") == expected_bundle_hash
            )
        ),
        retries=120,
        delay=10,
        description=f"ClusterOrder {order_name} fulfillment trust ready",
    )
    assert _condition(order).get("reason") == "TrustBundleSynchronized"
    bundle = k8s.get_json(resource="configmap", name="ca-bundle")["data"]["bundle.pem"]
    digest = hashlib.sha256(bundle.encode()).hexdigest()
    if expected_bundle_hash is not None:
        assert digest == expected_bundle_hash, f"management bundle hash is {digest}, expected {expected_bundle_hash}"
    assert order["status"]["fulfillmentTrustBundleHash"] == digest

    uid = order["metadata"]["uid"]
    tenant = order["metadata"]["annotations"]["osac.openshift.io/tenant"]
    _, rc = run_unchecked(*k8s._base(), "get", "secret", f"fulfillment-trust-{uid}", "-n", k8s.namespace)
    assert rc != 0, "Automatic trust must not depend on a per-ClusterOrder credential Secret"
    remote = _tenant_readonly_kubectl(k8s, order)
    config_map = json.loads(remote("get", "configmap", "osac-fulfillment-ca", "-o", "json"))
    assert config_map["data"] == {"bundle.pem": bundle}
    assert config_map["metadata"]["annotations"] == {
        "osac.openshift.io/tenant": tenant,
        "osac.openshift.io/owner-reference": uid,
        "osac.openshift.io/fulfillment-ca-sha256": digest,
    }

    def list_deployments() -> list[dict[str, Any]]:
        return json.loads(
            remote(
                "get",
                "deployment",
                "-l",
                "app.kubernetes.io/name in (csi-driver,csiDriver),app.kubernetes.io/component=controller",
                "-o",
                "json",
            )
        )["items"]

    def rollout_complete(deployments: list[dict[str, Any]]) -> bool:
        if require_csi and not deployments:
            return False
        for deployment in deployments:
            metadata = deployment.get("metadata", {})
            if metadata.get("labels", {}).get("osac.openshift.io/fulfillment-trust-client") != "true":
                return False
            template_metadata = deployment.get("spec", {}).get("template", {}).get("metadata", {})
            if template_metadata.get("annotations", {}).get("osac.openshift.io/fulfillment-ca-sha256") != digest:
                return False
            status = deployment.get("status", {})
            desired = deployment["spec"].get("replicas", 1)
            if status.get("observedGeneration") != deployment["metadata"]["generation"]:
                return False
            if any(status.get(key, 0) != desired for key in ("updatedReplicas", "readyReplicas", "availableReplicas")):
                return False
            if status.get("unavailableReplicas", 0) != 0:
                return False
        return True

    deployments = list_deployments()
    if deployments or require_csi:
        deployments = poll_until(
            fn=list_deployments,
            until=rollout_complete,
            retries=60,
            delay=5,
            description=f"tenant CSI rollout for ClusterOrder {order_name}",
        )
    return digest


def assert_management_tls(k8s: K8sClient, *, require_metering: bool = False) -> None:
    """Assert deployed fulfillment clients select the mounted verified CA path."""
    operator = k8s.get_json(resource="deployment", name="osac-operator")
    manager = next(
        (item for item in operator["spec"]["template"]["spec"]["containers"] if item.get("name") == "manager"), None
    )
    assert manager is not None, "osac-operator Deployment has no manager container"
    args = [*manager.get("command", []), *manager.get("args", [])]
    assert "--fulfillment-ca-file=/etc/ca-bundle/bundle.pem" in args
    assert "--grpc-insecure" not in args
    assert any(
        volume.get("configMap", {}).get("name") == "ca-bundle"
        for volume in operator["spec"]["template"]["spec"]["volumes"]
    )
    if require_metering:
        metering = k8s.get_json(resource="deployment", name="osac-metering")
        assert any(
            mount.get("mountPath") == "/etc/ca-bundle" and mount.get("readOnly") is True
            for container in metering["spec"]["template"]["spec"]["containers"]
            for mount in container.get("volumeMounts", [])
        )
