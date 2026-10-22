"""Deployed Netris CA rotation, rollback, and admission-control coverage.

Run against a full Netris-enabled OSAC release with:

    OSAC_NETRIS_CA_E2E=true
    OSAC_NETRIS_CA_NEW_PEM_PATH=/path/to/new-root.pem

The test mutates the release: it creates temporary ConfigMaps, probes admission
control with an impersonated ServiceAccount, performs two Helm upgrades, and
runs real VirtualNetwork operations. It rolls the release back to the original
CA ConfigMap and deletes only the temporary version it created. Use a lab
installation, not a shared environment.
"""

from __future__ import annotations

import json
import os
import random
import subprocess
import time
from datetime import datetime
from pathlib import Path

import pytest

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import (
    unique_name,
    wait_for_virtual_network_cr,
    wait_for_virtual_network_deletion,
    wait_for_virtual_network_ready,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.runner import env, poll_until, run, run_unchecked

pytestmark = [
    pytest.mark.regression,
    pytest.mark.skipif(
        not os.environ.get("OSAC_NETRIS_CA_E2E"),
        reason="Enable the deployed Netris CA lifecycle test with OSAC_NETRIS_CA_E2E=true",
    ),
]

POLICY_MESSAGE = "Only authorized platform administrators may modify or delete the active Netris CA ConfigMap."
CA_VALUE = "global.networking.netris.caBundle.configMap"


def _helm_release() -> str:
    return env("OSAC_HELM_RELEASE", "osac")


def _helm_chart() -> str:
    default = str(Path("osac-installer/charts/osac").resolve())
    return env("OSAC_NETRIS_CA_HELM_CHART", default)


def _current_ca_configmap(namespace: str) -> str:
    release = _helm_release()
    values = json.loads(run("helm", "get", "values", release, "-n", namespace, "-o", "json"))
    configured = (
        values.get("global", {}).get("networking", {}).get("netris", {}).get("caBundle", {}).get("configMap", "")
    )
    active = configured or os.environ.get("OSAC_NETRIS_CA_ACTIVE_CONFIGMAP", "")
    assert active, f"{CA_VALUE} is empty and OSAC_NETRIS_CA_ACTIVE_CONFIGMAP is not set"
    return active


def _upgrade_ca(namespace: str, configmap: str) -> None:
    run(
        "helm",
        "upgrade",
        _helm_release(),
        _helm_chart(),
        "-n",
        namespace,
        "--reuse-values",
        "--set-string",
        f"{CA_VALUE}={configmap}",
        "--timeout",
        "20m",
        timeout=1500,
    )


def _probe_base(k8s: K8sClient, service_account: str) -> list[str]:
    args = ["kubectl"]
    if k8s.kubeconfig is not None:
        args.extend(["--kubeconfig", k8s.kubeconfig])
    args.extend(["--as", f"system:serviceaccount:{k8s.namespace}:{service_account}"])
    return args


def _expect_denied(k8s: K8sClient, service_account: str, *args: str) -> None:
    output, returncode = run_unchecked(*_probe_base(k8s, service_account), *args)
    assert returncode != 0, f"Expected admission denial for: {' '.join(args)}"
    assert POLICY_MESSAGE in output, f"Expected the Netris CA policy message, got: {output}"


def _configmap_manifest(name: str, namespace: str, bundle: str) -> str:
    indented = "\n".join(f"    {line}" for line in bundle.rstrip().splitlines())
    return (
        "apiVersion: v1\n"
        "kind: ConfigMap\n"
        "metadata:\n"
        f"  name: {name}\n"
        f"  namespace: {namespace}\n"
        "immutable: true\n"
        "data:\n"
        "  bundle.pem: |\n"
        f"{indented}\n"
    )


def _apply(k8s: K8sClient, manifest: str) -> None:
    subprocess.run(
        [*k8s._base(), "apply", "-f", "-"],
        input=manifest,
        capture_output=True,
        text=True,
        check=True,
    )


def _pods_mounting(k8s: K8sClient, configmap: str, since: float) -> list[str]:
    names: list[str] = []
    for pod in k8s.list_json(resource="pods", namespace=k8s.namespace)["items"]:
        created = pod.get("metadata", {}).get("creationTimestamp", "")
        if not created:
            continue
        if datetime.fromisoformat(created.replace("Z", "+00:00")).timestamp() < since:
            continue
        for volume in pod.get("spec", {}).get("volumes", []):
            if volume.get("configMap", {}).get("name") == configmap:
                names.append(pod["metadata"]["name"])
    return names


def _wait_for_ca_mount(k8s: K8sClient, configmap: str, since: float) -> list[str]:
    return poll_until(
        fn=lambda: _pods_mounting(k8s, configmap, since),
        until=lambda pods: bool(pods),
        retries=120,
        delay=2,
        description=f"AAP job pod mounting CA ConfigMap {configmap}",
    )


def _exercise_netris(grpc: GRPCClient, k8s: K8sClient, configmap: str) -> None:
    name = unique_name("netris-ca-probe")
    cidr = f"10.{random.randint(100, 250)}.{random.randint(0, 255)}.0/24"
    started = time.time()
    vnet_id = grpc.create_virtual_network(name=name, ipv4_cidr=cidr)
    cr_name = name
    try:
        cr_name = wait_for_virtual_network_cr(k8s=k8s, uuid=vnet_id)
        wait_for_virtual_network_ready(k8s=k8s, name=cr_name)
        assert _wait_for_ca_mount(k8s, configmap, started), f"AAP job did not mount {configmap}"
    finally:
        grpc.delete_virtual_network(vn_id=vnet_id)
        wait_for_virtual_network_deletion(k8s=k8s, name=cr_name)


def test_netris_ca_lifecycle(grpc: GRPCClient, k8s_hub_client: K8sClient, namespace: str) -> None:
    chart = _helm_chart()
    if not Path(chart).exists():
        pytest.skip(f"Helm chart not found at {chart}; set OSAC_NETRIS_CA_HELM_CHART")

    pem_path_value = os.environ.get("OSAC_NETRIS_CA_NEW_PEM_PATH")
    if not pem_path_value:
        pytest.skip("Set OSAC_NETRIS_CA_NEW_PEM_PATH to a trusted replacement CA certificate")
    new_pem_path = Path(pem_path_value)
    new_pem = new_pem_path.read_text(encoding="utf-8")
    assert "BEGIN CERTIFICATE" in new_pem, f"{new_pem_path} does not contain a PEM certificate"

    active = _current_ca_configmap(namespace)
    next_name = f"{active[:40]}-next-{unique_name('ca')[-8:]}"
    probe_sa = unique_name("netris-ca-probe")

    probe_role = (
        "apiVersion: rbac.authorization.k8s.io/v1\n"
        "kind: Role\n"
        "metadata:\n"
        f"  name: {probe_sa}\n"
        f"  namespace: {namespace}\n"
        "rules:\n"
        '- apiGroups: [""]\n'
        '  resources: ["configmaps"]\n'
        '  verbs: ["get", "list", "create", "update", "patch", "delete"]\n'
        "---\n"
        "apiVersion: rbac.authorization.k8s.io/v1\n"
        "kind: RoleBinding\n"
        "metadata:\n"
        f"  name: {probe_sa}\n"
        f"  namespace: {namespace}\n"
        "roleRef:\n"
        "  apiGroup: rbac.authorization.k8s.io\n"
        "  kind: Role\n"
        f"  name: {probe_sa}\n"
        "subjects:\n"
        "- kind: ServiceAccount\n"
        f"  name: {probe_sa}\n"
        f"  namespace: {namespace}\n"
    )

    rotated = False
    created_next = False
    try:
        run(*k8s_hub_client._base(), "create", "serviceaccount", probe_sa, "-n", namespace)
        _apply(k8s_hub_client, probe_role)

        allowed = unique_name("netris-ca-probe-allowed")
        _apply(k8s_hub_client, _configmap_manifest(allowed, namespace, new_pem))
        try:
            _expect_denied(
                k8s_hub_client,
                probe_sa,
                "label",
                "configmap",
                active,
                f"probe={probe_sa}",
                "-n",
                namespace,
            )
            _expect_denied(
                k8s_hub_client,
                probe_sa,
                "delete",
                "configmap",
                active,
                "-n",
                namespace,
                "--wait=false",
            )
            _expect_denied(
                k8s_hub_client,
                probe_sa,
                "create",
                "configmap",
                active,
                "--from-literal=probe=denied",
                "--dry-run=server",
                "-n",
                namespace,
            )
        finally:
            run(*k8s_hub_client._base(), "delete", "configmap", allowed, "-n", namespace)

        _apply(k8s_hub_client, _configmap_manifest(next_name, namespace, new_pem))
        created_next = True
        _upgrade_ca(namespace, next_name)
        rotated = True

        _expect_denied(
            k8s_hub_client,
            probe_sa,
            "label",
            "configmap",
            next_name,
            f"probe={probe_sa}",
            "-n",
            namespace,
        )
        _expect_denied(
            k8s_hub_client,
            probe_sa,
            "create",
            "configmap",
            next_name,
            "--from-literal=probe=denied",
            "--dry-run=server",
            "-n",
            namespace,
        )
        _exercise_netris(grpc, k8s_hub_client, next_name)

        _upgrade_ca(namespace, active)
        rotated = False
        _exercise_netris(grpc, k8s_hub_client, active)
    finally:
        if rotated:
            _upgrade_ca(namespace, active)
        if created_next:
            run(*k8s_hub_client._base(), "delete", "configmap", next_name, "-n", namespace, "--ignore-not-found")
        run(
            *k8s_hub_client._base(),
            "delete",
            "rolebinding",
            probe_sa,
            "-n",
            namespace,
            "--ignore-not-found",
        )
        run(*k8s_hub_client._base(), "delete", "role", probe_sa, "-n", namespace, "--ignore-not-found")
        run(
            *k8s_hub_client._base(),
            "delete",
            "serviceaccount",
            probe_sa,
            "-n",
            namespace,
            "--ignore-not-found",
        )
