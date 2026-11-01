"""Deployed Netris CA rotation, rollback, and admission-control coverage.

Run against a full Netris-enabled OSAC release that runs CaaS on Netris with:

    OSAC_NETRIS_CA_E2E=true
    OSAC_NETRIS_CA_NEW_PEM_PATH=/path/to/new-root.pem
    OSAC_PULL_SECRET_PATH=/path/to/pull-secret

The CA consumption probe needs the CaaS lab inputs (OSAC_CLUSTER_TEMPLATE,
OSAC_PULL_SECRET_PATH, OSAC_SSH_PUBLIC_KEY_PATH) because the ConfigMap mounts
only in the cluster-fulfillment job pod; the test skips when the deployment has
no cluster-fulfillment instance group.

The test mutates the release: it creates temporary ConfigMaps, probes admission
control with an impersonated ServiceAccount, performs two Helm upgrades, and
creates then deletes one ClusterOrder per probe. It rolls the release back to
the original CA ConfigMap and deletes only the temporary version it created. Use
a lab installation, not a shared environment.
"""

from __future__ import annotations

import hashlib
import json
import os
import subprocess
from pathlib import Path

import pytest

from tests.e2e.core.aap_job_pods import job_pod_mounting as _job_pod_mounting
from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import (
    unique_name,
    wait_for_cluster_deletion,
    wait_for_cluster_grpc_removal,
    wait_for_cluster_order_cr,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import env, poll_until, run, run_unchecked

pytestmark = [
    pytest.mark.regression,
    pytest.mark.skipif(
        os.environ.get("OSAC_NETRIS_CA_E2E") != "true",
        reason="Enable the deployed Netris CA lifecycle test with OSAC_NETRIS_CA_E2E=true",
    ),
]

POLICY_MESSAGE = "Only authorized platform administrators may modify or delete the active Netris CA ConfigMap."
CA_VALUE = "aap.instanceGroups.networkFulfillment.config.NETRIS_CA_CONFIGMAP"
NETRIS_CA_PATH = "/etc/netris/ca/bundle.pem"


def _helm_release() -> str:
    return env("OSAC_HELM_RELEASE", "osac")


def _helm_chart() -> str:
    default = str(Path("osac-installer/charts/osac").resolve())
    return env("OSAC_NETRIS_CA_HELM_CHART", default)


def _current_ca_configmap(namespace: str) -> str:
    release = _helm_release()
    values = json.loads(run("helm", "get", "values", release, "-n", namespace, "-o", "json"))
    configured = (
        values.get("aap", {})
        .get("instanceGroups", {})
        .get("networkFulfillment", {})
        .get("config", {})
        .get("NETRIS_CA_CONFIGMAP", "")
    )
    active = configured or os.environ.get("OSAC_NETRIS_CA_ACTIVE_CONFIGMAP", "")
    assert active, f"{CA_VALUE} is empty and OSAC_NETRIS_CA_ACTIVE_CONFIGMAP is not set"
    return active


def _expected_osac_service_account(namespace: str) -> str:
    values = json.loads(run("helm", "get", "values", _helm_release(), "-n", namespace, "-o", "json"))
    return (
        values.get("aap", {})
        .get("serviceAccounts", {})
        .get("osacSa", "osac-sa")
    )


def _cluster_fulfillment_enabled(namespace: str) -> bool:
    """Report whether the release runs CaaS jobs that mount the Netris CA.

    Only cluster-fulfillment instance groups mount the CA ConfigMap: networking
    jobs run the network manager contract without a Netris facade.
    """
    values = json.loads(run("helm", "get", "values", _helm_release(), "-n", namespace, "-o", "json"))
    cluster_fulfillment = values.get("aap", {}).get("instanceGroups", {}).get("clusterFulfillment", {})
    return bool(cluster_fulfillment.get("enabled", False))


def _caas_probe_inputs() -> dict[str, str]:
    """Return the CaaS inputs the probe needs to create a ClusterOrder."""
    inputs = {
        "template": env("OSAC_CLUSTER_TEMPLATE", "ocp-ci-small"),
        "pull_secret_path": env("OSAC_PULL_SECRET_PATH", ""),
        "ssh_public_key": env("OSAC_SSH_PUBLIC_KEY_PATH", os.path.expanduser("~/.ssh/id_rsa.pub")),
    }
    if not Path(inputs["pull_secret_path"]).is_file():
        pytest.skip("The Netris CA probe creates a ClusterOrder; set OSAC_PULL_SECRET_PATH to run it")
    if not Path(inputs["ssh_public_key"]).is_file():
        pytest.skip("The Netris CA probe creates a ClusterOrder; set OSAC_SSH_PUBLIC_KEY_PATH to a readable key")
    return inputs


def _configmap_bundle(k8s: K8sClient, configmap: str) -> str:
    data = k8s.get_json(resource="configmap", name=configmap).get("data", {})
    bundle = data.get("bundle.pem")
    assert bundle, f"ConfigMap {configmap} has no bundle.pem"
    return bundle


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
        timeout=300,
    )


def _latest_cluster_order_job_id(k8s: K8sClient, name: str) -> str:
    status = k8s.get_json(resource="clusterorder", name=name).get("status", {})
    jobs = [
        job for job in status.get("provisioningJobs", [])
        if job.get("type") == "provision"
    ]
    if not jobs:
        return ""
    return sorted(jobs, key=lambda job: job.get("timestamp", ""), reverse=True)[0].get("jobID", "")


def _wait_for_cluster_order_job_id(k8s: K8sClient, name: str) -> str:
    return poll_until(
        fn=lambda: _latest_cluster_order_job_id(k8s, name),
        until=bool,
        retries=120,
        delay=2,
        description=f"AAP provisioning job for ClusterOrder {name}",
    )


def _wait_for_job_pod(k8s: K8sClient, configmap: str, job_id: str) -> dict:
    # A cluster-fulfillment job can queue behind other AAP work before its pod
    # exists, so this waits longer than a networking job needs.
    pod = poll_until(
        fn=lambda: _job_pod_mounting(k8s, configmap, job_id),
        until=lambda value: value is not None,
        retries=300,
        delay=2,
        description=f"AAP job {job_id} pod mounting CA ConfigMap {configmap}",
    )
    assert pod is not None
    return pod


def _pod_environment(pod: dict) -> dict[str, str]:
    worker = next(container for container in pod["spec"]["containers"] if container["name"] == "worker")
    return {item["name"]: item.get("value", "") for item in worker.get("env", [])}


def _assert_job_pod_contract(pod: dict, configmap: str, service_account: str) -> None:
    name = pod["metadata"]["name"]
    spec = pod["spec"]
    environment = _pod_environment(pod)

    assert spec["serviceAccountName"] == service_account, (
        f"{name} uses ServiceAccount {spec['serviceAccountName']}, expected {service_account}"
    )
    assert environment["NETRIS_CONTROLLER_URL"].startswith("https://"), (
        f"{name} must target Netris over HTTPS"
    )
    assert environment["NETRIS_VALIDATE_CERTS"] == "true", (
        f"{name} must validate the Netris TLS certificate"
    )
    assert environment["NETRIS_CA_PATH"] == NETRIS_CA_PATH

    mounts = [
        mount for mount in spec["containers"][0].get("volumeMounts", [])
        if mount.get("name") == "netris-ca-bundle"
    ]
    assert len(mounts) == 1, f"{name} must mount one Netris CA bundle"
    assert mounts[0]["mountPath"] == NETRIS_CA_PATH
    assert mounts[0]["subPath"] == "bundle.pem"
    assert mounts[0]["readOnly"] is True

    volumes = [
        volume for volume in spec.get("volumes", [])
        if volume.get("name") == "netris-ca-bundle"
    ]
    assert len(volumes) == 1, f"{name} must reference one Netris CA ConfigMap volume"
    assert volumes[0]["configMap"]["name"] == configmap


def _read_job_bundle(k8s: K8sClient, pod_name: str) -> tuple[str, str]:
    script = 'printf "%s\\n" "$NETRIS_VALIDATE_CERTS"; sha256sum "$NETRIS_CA_PATH" | cut -d " " -f 1'
    output, returncode = run_unchecked(
        *k8s._base(), "exec", pod_name, "-n", k8s.namespace, "--", "sh", "-c", script
    )
    if returncode != 0:
        return "", ""
    lines = output.splitlines()
    if len(lines) < 2:
        return "", ""
    return lines[-2], lines[-1]


def _bundle_sha256(bundle: str) -> str:
    return hashlib.sha256(bundle.encode("utf-8")).hexdigest()


def _wait_for_bundle_in_job_pod(k8s: K8sClient, pod_name: str, expected_bundle: str) -> None:
    expected_hash = _bundle_sha256(expected_bundle)
    validation, actual_hash = poll_until(
        fn=lambda: _read_job_bundle(k8s, pod_name),
        until=lambda value: value == ("true", expected_hash),
        retries=60,
        delay=2,
        retry_on_error=True,
        description=f"AAP job pod {pod_name} reading the expected Netris CA bundle",
    )
    assert validation == "true", f"{pod_name} did not enable Netris certificate validation"
    assert actual_hash == expected_hash, f"{pod_name} mounted the wrong Netris CA bundle"


def _cleanup_cluster(cli: OsacCLI, grpc: GRPCClient, k8s: K8sClient, uuid: str, name: str) -> None:
    cli.delete_cluster(uuid=uuid)
    if not name:
        name = k8s.get_cluster_order_name(uuid=uuid, checked=False)
    if name:
        wait_for_cluster_deletion(k8s=k8s, name=name)
    wait_for_cluster_grpc_removal(grpc=grpc, uuid=uuid)


def _exercise_cluster_fulfillment_ca(
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s: K8sClient,
    configmap: str,
    expected_bundle: str,
    expected_service_account: str,
    caas: dict[str, str],
    version: str,
) -> None:
    """Prove a cluster-fulfillment job pod consumes the CA, then delete the cluster.

    The probe stops as soon as it has read the mounted bundle: it never waits
    for the cluster to become ready, only for the provisioning job that mounts
    the CA in the cluster-fulfillment instance group.
    """
    name = unique_name("netris-ca-probe")
    pull_secret = unique_name("netris-ca-probe-pull")
    cli.create_secret(
        name=pull_secret, from_files={".dockerconfigjson": caas["pull_secret_path"]}, secret_type="pull-secret"
    )
    uuid = ""
    cluster_order = ""
    try:
        uuid = cli.create_cluster(
            template=caas["template"],
            name=name,
            version=version,
            pull_secret=pull_secret,
            ssh_public_key_file=caas["ssh_public_key"],
        )
        cluster_order = wait_for_cluster_order_cr(k8s=k8s, uuid=uuid)
        job_id = _wait_for_cluster_order_job_id(k8s, cluster_order)
        pod = _wait_for_job_pod(k8s, configmap, job_id)
        _assert_job_pod_contract(pod, configmap, expected_service_account)
        _wait_for_bundle_in_job_pod(k8s, pod["metadata"]["name"], expected_bundle)
    finally:
        try:
            if uuid:
                _cleanup_cluster(cli, grpc, k8s, uuid, cluster_order)
        finally:
            cli.delete_secret(name=pull_secret)


def test_netris_ca_lifecycle(
    request: pytest.FixtureRequest,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    namespace: str,
) -> None:
    chart = _helm_chart()
    if not Path(chart).exists():
        pytest.fail(f"Helm chart not found at {chart}; set OSAC_NETRIS_CA_HELM_CHART")

    if not _cluster_fulfillment_enabled(namespace):
        pytest.skip(
            "The Netris CA mounts only in the cluster-fulfillment job pod; "
            "enable aap.instanceGroups.clusterFulfillment to run this test"
        )
    caas = _caas_probe_inputs()
    # Resolved by name so the CaaS fixtures are never built on a deployment that
    # skipped above.
    cli: OsacCLI = request.getfixturevalue("cli")
    version: str = request.getfixturevalue("caas_disk_image_version")

    pem_path_value = os.environ.get("OSAC_NETRIS_CA_NEW_PEM_PATH")
    if not pem_path_value:
        pytest.fail("Set OSAC_NETRIS_CA_NEW_PEM_PATH to a trusted replacement CA certificate")
    new_pem_path = Path(pem_path_value)
    new_pem = new_pem_path.read_text(encoding="utf-8")
    assert "BEGIN CERTIFICATE" in new_pem, f"{new_pem_path} does not contain a PEM certificate"

    active = _current_ca_configmap(namespace)
    active_bundle = _configmap_bundle(k8s_hub_client, active)
    service_account = _expected_osac_service_account(namespace)
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
        try:
            _apply(k8s_hub_client, _configmap_manifest(allowed, namespace, new_pem))
            output, returncode = run_unchecked(
                *_probe_base(k8s_hub_client, probe_sa),
                "label",
                "configmap",
                allowed,
                f"probe={probe_sa}",
                "-n",
                namespace,
            )
            assert returncode == 0, (
                f"Non-active ConfigMap must stay writable, got: {output}"
            )
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
            run(
                *k8s_hub_client._base(),
                "delete",
                "configmap",
                allowed,
                "-n",
                namespace,
                "--ignore-not-found",
            )

        # Register the name first: an apply that times out can still persist the object.
        created_next = True
        _apply(k8s_hub_client, _configmap_manifest(next_name, namespace, new_pem))
        next_bundle = _configmap_bundle(k8s_hub_client, next_name)
        rotated = True
        _upgrade_ca(namespace, next_name)

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
        _exercise_cluster_fulfillment_ca(
            cli, grpc, k8s_hub_client, next_name, next_bundle, service_account, caas, version
        )

        _upgrade_ca(namespace, active)
        rotated = False
        _exercise_cluster_fulfillment_ca(
            cli, grpc, k8s_hub_client, active, active_bundle, service_account, caas, version
        )
    finally:
        try:
            if rotated:
                _upgrade_ca(namespace, active)
            if created_next:
                run(
                    *k8s_hub_client._base(),
                    "delete",
                    "configmap",
                    next_name,
                    "-n",
                    namespace,
                    "--ignore-not-found",
                )
        finally:
            run(
                *k8s_hub_client._base(),
                "delete",
                "rolebinding",
                probe_sa,
                "-n",
                namespace,
                "--ignore-not-found",
            )
            run(
                *k8s_hub_client._base(),
                "delete",
                "role",
                probe_sa,
                "-n",
                namespace,
                "--ignore-not-found",
            )
            run(
                *k8s_hub_client._base(),
                "delete",
                "serviceaccount",
                probe_sa,
                "-n",
                namespace,
                "--ignore-not-found",
            )
