#!/usr/bin/env python3
"""Refresh a Helm-deployed OSAC cluster after booting from a cold snapshot.

All OSAC pods are at zero replicas. This script:
  1. Starts slow operators (AAP) early + fixes cluster identity
  2. Prepares the environment (Keycloak, secrets, CA bundle, DB)
  3. Deploys via helm upgrade + waits with health monitoring

Hub registration, template publishing, and AAP token creation are handled by
Helm hooks that fire during the Phase 3 upgrade -- no separate post-flight
step needed.

Fail fast: any error aborts immediately. CrashLoopBackOff/ImagePullBackOff
detected during rollout waits.
"""

from __future__ import annotations

import base64
import json
import os
import subprocess
import sys
import time
from concurrent.futures import ThreadPoolExecutor, as_completed
from dataclasses import dataclass, field
from pathlib import Path
from collections.abc import Callable
from typing import Any


SCRIPT_DIR = Path(__file__).resolve().parent
REPO_ROOT = SCRIPT_DIR.parent

TERMINAL_POD_REASONS = frozenset({
    "CrashLoopBackOff",
    "ImagePullBackOff",
    "ErrImagePull",
    "CreateContainerConfigError",
    "InvalidImageName",
})


# ─── Config ──────────────────────────────────────────────────────────────────


@dataclass
class RefreshConfig:
    values_file: str
    namespace: str
    cluster_domain: str
    keycloak_ns: str = "keycloak"
    realm_json: str = "prerequisites/keycloak/service/files/realm.json"
    aap_stale_ts: str = field(default="", init=False)

    @property
    def values_dir(self) -> str:
        """Directory containing the Helm values file."""
        return str(Path(self.values_file).parent)

    @property
    def external_host(self) -> str:
        """Public fulfillment API route hostname."""
        return f"fulfillment-api-{self.namespace}.{self.cluster_domain}"

    @property
    def internal_host(self) -> str:
        """Internal fulfillment API route hostname."""
        return f"fulfillment-internal-api-{self.namespace}.{self.cluster_domain}"


# ─── Shell helpers ───────────────────────────────────────────────────────────


def run(args: list[str], *, check: bool = True, capture: bool = False) -> subprocess.CompletedProcess[str]:
    """Run a command.

    When capture=False (default), stdout+stderr are streamed to stderr in real
    time and also collected in the returned CompletedProcess.
    When capture=True, output is only stored (for parsing).
    """
    if capture:
        result = subprocess.run(
            args, text=True, capture_output=True, cwd=str(REPO_ROOT),
        )
    else:
        proc = subprocess.Popen(
            args, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
            cwd=str(REPO_ROOT),
        )
        assert proc.stdout is not None
        lines: list[str] = []
        for line in proc.stdout:
            sys.stderr.write(line)
            sys.stderr.flush()
            lines.append(line)
        proc.wait()
        combined = "".join(lines)
        result = subprocess.CompletedProcess(args, proc.returncode, stdout=combined, stderr="")
    if check and result.returncode != 0:
        print(f"ERROR: command failed (exit {result.returncode}): {' '.join(args)}", file=sys.stderr)
        if result.stdout:
            print(f"  stdout: {result.stdout.rstrip()}", file=sys.stderr)
        if result.stderr:
            print(f"  stderr: {result.stderr.rstrip()}", file=sys.stderr)
        raise subprocess.CalledProcessError(
            result.returncode, args, output=result.stdout, stderr=result.stderr)
    return result


def oc(*args: str, check: bool = True, capture: bool = False) -> subprocess.CompletedProcess[str]:
    """Run oc with the given arguments."""
    return run(["oc", *args], check=check, capture=capture)


def oc_json(*args: str) -> dict:
    """Run oc with -o json and return the parsed object."""
    result = oc(*args, "-o", "json", capture=True)
    return json.loads(result.stdout)


def retry_until(*, description: str, timeout: int, interval: int, condition: Callable[[], bool]) -> None:
    """Poll condition until it returns True or timeout expires."""
    deadline = time.time() + timeout
    while not condition():
        if time.time() >= deadline:
            raise TimeoutError(f"{description}: timed out after {timeout}s")
        time.sleep(interval)


def oc_exists(resource: str, namespace: str | None = None) -> bool:
    """Return True when oc get succeeds for the given resource."""
    ns_args = ["-n", namespace] if namespace else []
    result = oc("get", resource, *ns_args, "--no-headers", check=False, capture=True)
    return result.returncode == 0


def oc_apply_secret(name: str, namespace: str, *literal_or_file_args: str) -> None:
    """Create a secret with --dry-run=client and pipe to oc apply."""
    result = run(
        ["oc", "create", "secret", "generic", name,
         *literal_or_file_args,
         "-n", namespace, "--dry-run=client", "-o", "yaml"],
        capture=True,
    )
    apply_result = subprocess.run(
        ["oc", "apply", "-f", "-"],
        input=result.stdout, text=True, capture_output=True, cwd=str(REPO_ROOT),
    )
    if apply_result.returncode != 0:
        print(f"ERROR: oc apply secret/{name} failed", file=sys.stderr)
        if apply_result.stderr:
            print(f"  stderr: {apply_result.stderr.rstrip()}", file=sys.stderr)
        raise subprocess.CalledProcessError(
            apply_result.returncode, ["oc", "apply", "-f", "-"],
            output=apply_result.stdout, stderr=apply_result.stderr)


# ─── Parallel execution ─────────────────────────────────────────────────────


def run_parallel(tasks: list[tuple[str, Callable[[], Any]]]) -> None:
    """Run named tasks in parallel. On any failure, print the task name + error and abort."""
    with ThreadPoolExecutor(max_workers=len(tasks)) as pool:
        futures = {pool.submit(fn): name for name, fn in tasks}
        errors: list[str] = []
        for future in as_completed(futures):
            name = futures[future]
            try:
                future.result()
            except subprocess.CalledProcessError as e:
                msg = f"{name}: command failed: {e.cmd}\n"
                if e.stdout:
                    msg += f"  stdout: {e.stdout.strip()}\n"
                if e.stderr:
                    msg += f"  stderr: {e.stderr.strip()}\n"
                errors.append(msg)
            except Exception as e:
                errors.append(f"{name}: {e}")
        if errors:
            print("ERROR: parallel tasks failed:", file=sys.stderr)
            for err in errors:
                print(f"  {err}", file=sys.stderr)
            sys.exit(1)


# ─── Health monitoring ───────────────────────────────────────────────────────


def _get_pod_failure(pod: dict) -> str | None:
    """Return a failure description if the pod has a terminal container issue."""
    pod_name: str = pod["metadata"]["name"]
    # containerStatuses is omitted by Kubernetes before kubelet processes the pod
    for cs in pod["status"].get("containerStatuses", []):
        reason = cs.get("state", {}).get("waiting", {}).get("reason", "")
        if reason in TERMINAL_POD_REASONS:
            msg = cs["state"]["waiting"].get("message", "")
            return f"{pod_name}: {reason}: {msg}"
    for cs in pod["status"].get("initContainerStatuses", []):
        reason = cs.get("state", {}).get("waiting", {}).get("reason", "")
        if reason in TERMINAL_POD_REASONS:
            msg = cs["state"]["waiting"].get("message", "")
            return f"{pod_name} (init): {reason}: {msg}"
    return None


def _get_pod_logs(pod_name: str, namespace: str) -> str:
    """Return the last 20 log lines for a pod."""
    result = oc("logs", f"pod/{pod_name}", "-n", namespace,
                "--tail=20", "--all-containers", check=False, capture=True)
    return result.stdout.strip() if result.stdout else "(no logs)"


def _get_pod_events(pod_name: str, namespace: str) -> str:
    """Return recent Kubernetes events involving the pod."""
    result = oc(
        "get", "events", "-n", namespace,
        "--field-selector", f"involvedObject.name={pod_name}",
        "--sort-by=.lastTimestamp",
        check=False, capture=True,
    )
    lines = result.stdout.strip().splitlines() if result.stdout else []
    return "\n".join(lines[-10:]) if lines else "(no events)"


def wait_rollout_healthy(deploy: str, namespace: str, timeout: int = 300) -> None:
    """Wait for deployment rollout. Fail fast on CrashLoopBackOff."""
    deploy_data = oc_json("get", f"deploy/{deploy}", "-n", namespace)
    # replicas is omitted by Kubernetes when defaulting to 1
    desired = deploy_data["spec"].get("replicas", 1)
    if desired == 0:
        return

    deadline = time.time() + timeout
    container_creating_since: dict[str, float] = {}

    while True:
        if time.time() >= deadline:
            raise TimeoutError(f"{deploy}: rollout not complete after {timeout}s")

        deploy_data = oc_json("get", f"deploy/{deploy}", "-n", namespace)
        # readyReplicas and updatedReplicas are omitted by Kubernetes when 0
        ready = deploy_data["status"].get("readyReplicas", 0)
        updated = deploy_data["status"].get("updatedReplicas", 0)
        if ready >= desired and updated >= desired:
            return

        selector = deploy_data["spec"]["selector"]["matchLabels"]
        label_selector = ",".join(f"{k}={v}" for k, v in selector.items())
        pods_data = oc_json("get", "pods", "-n", namespace, "-l", label_selector)

        now = time.time()
        for pod in pods_data["items"]:
            pod_name: str = pod["metadata"]["name"]

            failure = _get_pod_failure(pod)
            if failure:
                logs = _get_pod_logs(pod_name, namespace)
                raise RuntimeError(
                    f"{deploy}: {failure}\n"
                    f"--- Last 20 log lines ---\n{logs}")

            for cs in pod["status"].get("containerStatuses", []):
                if cs.get("state", {}).get("waiting", {}).get("reason") == "ContainerCreating":
                    first_seen = container_creating_since.setdefault(pod_name, now)
                    if now - first_seen > 120:
                        events = _get_pod_events(pod_name, namespace)
                        raise RuntimeError(
                            f"{deploy}: {pod_name} stuck in ContainerCreating for >2min\n"
                            f"--- Events ---\n{events}")

        time.sleep(5)


def check_all_pods(namespace: str) -> None:
    """Final sweep: fail if any pod in the namespace is unhealthy."""
    pods_data = oc_json("get", "pods", "-n", namespace)
    failures: list[str] = []
    for pod in pods_data["items"]:
        failure = _get_pod_failure(pod)
        if failure:
            failures.append(failure)
    if failures:
        print("ERROR: unhealthy pods detected:", file=sys.stderr)
        for f in failures:
            pname = f.split(":")[0]
            logs = _get_pod_logs(pname, namespace)
            print(f"  {f}", file=sys.stderr)
            print(f"  logs: {logs[:500]}", file=sys.stderr)
        sys.exit(1)


# ─── Phase 1: Start slow operators + fix cluster identity ───────────────────


def patch_stale_routes(config: RefreshConfig) -> None:
    """Rewrite route hosts that still reference the snapshot cluster domain."""
    for ns in [config.namespace, config.keycloak_ns, "multicluster-engine"]:
        if not oc_exists(f"namespace/{ns}"):
            continue
        routes_data = oc_json("get", "routes", "-n", ns)
        for route in routes_data["items"]:
            name: str = route["metadata"]["name"]
            old_host: str = route["spec"]["host"]
            route_domain = old_host.split(".", 1)[1] if "." in old_host else ""
            if route_domain != config.cluster_domain:
                route_name = old_host.split(".", 1)[0]
                new_host = f"{route_name}.{config.cluster_domain}"
                print(f"  {ns}/{name}: {old_host} -> {new_host}")
                oc("patch", "route", name, "-n", ns, "--type=merge",
                   "-p", json.dumps({"spec": {"host": new_host}}))


def refresh_cdi_certificates() -> None:
    """Regenerate CDI TLS certificates after a cold snapshot boot."""
    if not oc_exists("namespace/openshift-cnv"):
        return
    print("  Refreshing CDI certificates...")
    for secret in [
        "cdi-apiserver-signer", "cdi-uploadproxy-signer",
        "cdi-uploadserver-client-signer", "cdi-uploadserver-signer",
        "cdi-apiserver-server-cert", "cdi-uploadproxy-server-cert",
        "cdi-uploadserver-client-cert",
    ]:
        if oc_exists(f"secret/{secret}", "openshift-cnv"):
            oc("delete", "secret", secret, "-n", "openshift-cnv")
    if oc_exists("pod", "openshift-cnv"):
        oc("delete", "pod", "-n", "openshift-cnv", "-l", "app=cdi-operator")
    oc("rollout", "status", "deploy/cdi-operator", "-n", "openshift-cnv", "--timeout=300s")
    for deploy in ["cdi-deployment", "cdi-apiserver", "cdi-uploadproxy"]:
        if oc_exists(f"deploy/{deploy}", "openshift-cnv"):
            oc("rollout", "restart", f"deploy/{deploy}", "-n", "openshift-cnv")
    oc("rollout", "status", "deploy/cdi-deployment", "-n", "openshift-cnv", "--timeout=300s")
    print("  CDI certificates refreshed")


def refresh_metallb_and_subnet() -> None:
    """Refresh MetalLB webhook certs and apply the node subnet address pool."""
    if not oc_exists("crd/ipaddresspools.metallb.io"):
        return
    print("  Refreshing MetalLB webhook certificates...")
    if oc_exists("secret/metallb-operator-webhook-server-cert", "metallb-system"):
        oc("delete", "secret", "metallb-operator-webhook-server-cert", "-n", "metallb-system")
    for label in ["control-plane=controller-manager", "component=webhook-server"]:
        oc("delete", "pod", "-n", "metallb-system", "-l", label,
           "--ignore-not-found")

    retry_until(
        description="MetalLB webhook endpoints",
        timeout=300, interval=5,
        condition=lambda: bool(
            oc("get", "endpoints", "metallb-operator-webhook-server-service",
               "-n", "metallb-system",
               "-o", "jsonpath={.subsets[*].addresses[*].ip}",
               capture=True, check=False).stdout.strip()
        ),
    )

    node_ip = oc("get", "nodes", "-o",
                 "jsonpath={.items[0].status.addresses[?(@.type==\"InternalIP\")].address}",
                 capture=True).stdout.strip()
    subnet_prefix = ".".join(node_ip.split(".")[:3])
    print(f"  MetalLB: {subnet_prefix}.240-{subnet_prefix}.250")

    pool_yaml = json.dumps({
        "apiVersion": "metallb.io/v1beta1",
        "kind": "IPAddressPool",
        "metadata": {"name": "caas-address-pool", "namespace": "metallb-system"},
        "spec": {"addresses": [f"{subnet_prefix}.240-{subnet_prefix}.250"], "autoAssign": True},
    })
    def _try_apply_pool() -> bool:
        """Apply the MetalLB IPAddressPool manifest."""
        r = subprocess.run(
            ["oc", "apply", "-f", "-"],
            input=pool_yaml, text=True, capture_output=True, cwd=str(REPO_ROOT),
        )
        if r.returncode != 0:
            print(f"  MetalLB apply failed (retrying): {r.stderr.strip()}", file=sys.stderr)
        return r.returncode == 0

    retry_until(
        description="MetalLB IPAddressPool apply",
        timeout=120, interval=10,
        condition=_try_apply_pool,
    )
    print("  MetalLB configured")


def wait_keycloak_cert(config: RefreshConfig) -> None:
    """Wait for the Keycloak TLS certificate to become Ready."""
    print("  Waiting for Keycloak TLS certificate...")
    oc("wait", "--for=condition=Ready", "certificate/keycloak-tls",
       "-n", config.keycloak_ns, "--timeout=300s")
    print("  Keycloak TLS ready")


def pre_fix_cert_sans(config: RefreshConfig) -> None:
    """Patch fulfillment-api Certificate dnsNames before pods start.

    Prevents the race where helm upgrade starts pods before cert-manager
    reissues the cert with new SANs. Without this, console-proxy crashes
    on TLS verification (cert has old snapshot domain).
    """
    if not oc_exists("certificate.cert-manager.io/fulfillment-api", config.namespace):
        return

    cert = oc_json("get", "certificate.cert-manager.io/fulfillment-api", "-n", config.namespace)
    dns_names: list[str] = cert["spec"]["dnsNames"]

    if config.external_host in dns_names and config.internal_host in dns_names:
        print("  Cert SANs already correct")
        return

    new_dns_names = [n for n in dns_names if ".apps." not in n]
    new_dns_names.extend([config.external_host, config.internal_host])
    print(f"  Patching fulfillment-api cert SANs: +{config.external_host}, +{config.internal_host}")

    oc("patch", "certificate.cert-manager.io/fulfillment-api", "-n", config.namespace,
       "--type=json", "-p", json.dumps([
           {"op": "replace", "path": "/spec/dnsNames", "value": new_dns_names}
       ]))
    oc("wait", "--for=condition=Ready", "certificate.cert-manager.io/fulfillment-api",
       "-n", config.namespace, "--timeout=120s")
    print("  Cert reissued with new SANs")


# ─── Phase 2: Prepare environment ───────────────────────────────────────────


def keycloak_sync(config: RefreshConfig) -> None:
    """Update Keycloak realm configmap and wait for the realm endpoint."""
    print("  Syncing Keycloak realm configmap...")
    result = run(
        ["oc", "create", "configmap", "keycloak-realm",
         f"--from-file=realm.json={config.realm_json}",
         "-n", config.keycloak_ns, "--dry-run=client", "-o", "yaml"],
        capture=True,
    )
    apply_result = subprocess.run(
        ["oc", "apply", "-f", "-"],
        input=result.stdout, text=True, capture_output=True, cwd=str(REPO_ROOT),
    )
    if apply_result.returncode != 0:
        raise subprocess.CalledProcessError(
            apply_result.returncode, ["oc", "apply", "-f", "-"],
            output=apply_result.stdout, stderr=apply_result.stderr)
    oc("set", "env", "deploy/keycloak-service",
       "KC_SPI_IMPORT_REALM_FILE_STRATEGY=OVERWRITE",
       "-n", config.keycloak_ns)
    oc("rollout", "restart", "deploy/keycloak-service", "-n", config.keycloak_ns)
    oc("rollout", "status", "deploy/keycloak-service", "-n", config.keycloak_ns,
       "--timeout=300s")

    kc_host = oc("get", "route", "keycloak", "-n", config.keycloak_ns,
                 "-o", "jsonpath={.spec.host}", capture=True).stdout.strip()
    retry_until(
        description="Keycloak realm responding",
        timeout=300, interval=5,
        condition=lambda: subprocess.run(
            ["curl", "-sk", "-o", "/dev/null", "-w", "%{http_code}",
             f"https://{kc_host}/realms/osac"],
            capture_output=True, text=True, check=False,
        ).stdout.strip() == "200",
    )
    print("  Keycloak ready")


def create_secrets(config: RefreshConfig) -> None:
    """Create fulfillment, AAP license, and pull secrets for the namespace."""
    print("  Creating secrets...")
    realm = json.loads((REPO_ROOT / config.realm_json).read_text())

    fc_client = next((c for c in realm["clients"] if c.get("serviceAccountsEnabled")), None)
    if not fc_client:
        raise RuntimeError("No client with serviceAccountsEnabled in realm.json")
    fc_id: str = fc_client["clientId"]

    # realm.json ships with an __OSAC_CONTROLLER_CLIENT_SECRET__-style placeholder;
    # the real value lives in keycloak-client-secrets (see resolve-realm-secrets.sh,
    # which substitutes this same secret into the realm Keycloak actually imports).
    secret_result = oc("get", "secret", "keycloak-client-secrets", "-n", config.keycloak_ns,
                       "-o", f"jsonpath={{.data.{fc_id}}}", capture=True)
    fc_secret = base64.b64decode(secret_result.stdout.strip()).decode()
    if not fc_secret:
        raise RuntimeError(f"No key '{fc_id}' in keycloak-client-secrets -n {config.keycloak_ns}")

    oc_apply_secret("fulfillment-controller-credentials", config.namespace,
                    f"--from-literal=client-id={fc_id}",
                    f"--from-literal=client-secret={fc_secret}")

    license_path = Path(config.values_dir) / "license.zip"
    if not license_path.exists():
        raise FileNotFoundError(f"AAP license not found: {license_path}")
    oc_apply_secret("config-as-code-manifest-ig", config.namespace,
                    f"--from-file=license.zip={license_path}")

    pull_secret_path = Path(config.values_dir) / "pull-secret.json"
    if pull_secret_path.exists():
        oc_apply_secret("quay-pull-secret", config.namespace,
                        f"--from-file=.dockerconfigjson={pull_secret_path}",
                        "--type=kubernetes.io/dockerconfigjson")
        oc("secrets", "link", "osac-sa", "quay-pull-secret", "--for=pull",
           "-n", config.namespace)

    print("  Secrets created")


def ensure_ca_bundle(config: RefreshConfig) -> None:
    """Ensure the cluster CA bundle Bundle exists and covers the install namespace.

    The Bundle is cluster-scoped and shared across deployments, but
    osac-prereqs' own ca-issuer.yaml template only ever sets a single static
    namespace (.Values.osacNamespace) at chart-install time -- it doesn't
    additively cover a snapshot refresh into a differently-named namespace.
    Reimplements the old scripts/ensure-ca-bundle.sh (deleted by PR #404,
    which left this function's only caller pointed at a nonexistent file).
    """
    namespace = config.namespace
    if oc_exists("bundle/ca-bundle"):
        bundle = oc_json("get", "bundle", "ca-bundle")
        values = bundle["spec"]["target"]["namespaceSelector"]["matchExpressions"][0]["values"]
        if namespace not in values:
            print(f"  Adding {namespace} to ca-bundle namespace selector...")
            patch = [{
                "op": "add",
                "path": "/spec/target/namespaceSelector/matchExpressions/0/values/-",
                "value": namespace,
            }]
            oc("patch", "bundle", "ca-bundle", "--type=json", "-p", json.dumps(patch))
    else:
        print(f"  Creating ca-bundle Bundle targeting {namespace}...")
        manifest = f"""apiVersion: trust.cert-manager.io/v1alpha1
kind: Bundle
metadata:
  name: ca-bundle
spec:
  sources:
  - secret:
      name: "default-ca"
      key: "ca.crt"
  target:
    configMap:
      key: bundle.pem
    namespaceSelector:
      matchExpressions:
      - key: kubernetes.io/metadata.name
        operator: In
        values:
        - {namespace}
"""
        result = subprocess.run(
            ["oc", "apply", "-f", "-"],
            input=manifest, text=True, capture_output=True, cwd=str(REPO_ROOT),
        )
        if result.returncode != 0:
            print("ERROR: oc apply bundle/ca-bundle failed", file=sys.stderr)
            if result.stderr:
                print(f"  stderr: {result.stderr.rstrip()}", file=sys.stderr)
            raise subprocess.CalledProcessError(
                result.returncode, ["oc", "apply", "-f", "-"],
                output=result.stdout, stderr=result.stderr)


def wait_tls_certs(config: RefreshConfig) -> None:
    """Wait for all cert-manager Certificates in the namespace to become Ready."""
    print("  Waiting for TLS certificates...")
    certs_data = oc_json("get", "certificates.cert-manager.io", "-n", config.namespace)
    for cert in certs_data["items"]:
        name: str = cert["metadata"]["name"]
        oc("wait", "--for=condition=Ready", f"certificate.cert-manager.io/{name}",
           "-n", config.namespace, "--timeout=300s")
    print("  All TLS certificates ready")


# ─── Operator scaling + Phase 3: Deploy and wait ─────────────────────────────


def scale_csv_to(*, csv_name: str, namespace: str, replicas: int) -> None:
    """Patch a ClusterServiceVersion to scale all owned Deployments."""
    csv_data = oc_json("get", "csv", csv_name, "-n", namespace)
    deploys: list[dict] = csv_data["spec"]["install"]["spec"]["deployments"]
    patch = [
        {"op": "replace",
         "path": f"/spec/install/spec/deployments/{i}/spec/replicas",
         "value": replicas}
        for i in range(len(deploys))
    ]
    oc("patch", "csv", csv_name, "-n", namespace, "--type=json", "-p", json.dumps(patch))
    for d in deploys:
        target = f"{replicas}"
        oc("wait", f"deploy/{d['name']}", "-n", namespace,
           f"--for=jsonpath={{.spec.replicas}}={target}", "--timeout=120s")


def find_csv(*, namespace: str, deploy_name: str) -> str:
    """Return the CSV name that owns the given Deployment."""
    data = oc_json("get", "csv", "-n", namespace)
    for item in data["items"]:
        deploys = item.get("spec", {}).get("install", {}).get("spec", {}).get("deployments", [])
        if any(d["name"] == deploy_name for d in deploys):
            return item["metadata"]["name"]
    raise RuntimeError(f"CSV not found for deployment {deploy_name} in {namespace}")



def adopt_resources_for_helm(config: RefreshConfig) -> None:
    """Adopt vast-tenant-config-* secrets for Helm.

    The osac-operator chart creates these as empty shells (tenants.yaml
    template), then AAP's storage_provider role deletes and recreates them
    with real credentials -- wiping Helm's ownership metadata in the
    process. Without re-adopting them, the next helm upgrade refuses to
    import them back into the release.
    """
    result = oc("get", "secret", "-n", config.namespace, "-o", "name", capture=True)
    resources = [r for r in result.stdout.strip().splitlines()
                 if r.startswith("secret/vast-tenant-config-")]
    if not resources:
        return
    print(f"  Adopting {len(resources)} tenant secret(s) for Helm...")
    for resource in resources:
        oc("label", resource, "-n", config.namespace,
           "app.kubernetes.io/managed-by=Helm", "--overwrite")
        oc("annotate", resource, "-n", config.namespace,
           "meta.helm.sh/release-name=osac",
           f"meta.helm.sh/release-namespace={config.namespace}",
           "--overwrite")


def upgrade_osac(config: RefreshConfig) -> None:
    """Upgrade the osac Helm release and adopt pre-existing namespace resources."""
    print("  Upgrading osac chart...")
    (REPO_ROOT / "charts/osac/Chart.lock").unlink(missing_ok=True)
    run(["helm", "dependency", "build", "charts/osac/"])
    adopt_resources_for_helm(config)
    # Delete stale config-as-code-ig so helm recreates it from chart values.
    # The AAP subchart manages this secret; deleting forces a fresh render.
    oc("delete", "secret", "config-as-code-ig", "-n", config.namespace,
       "--ignore-not-found")
    # Remove OSAC_AAP_URL and OSAC_AAP_TOKEN env vars that prepare-aap.sh
    # injected via "oc set env" on the snapshot.  The chart now manages
    # OSAC_AAP_TOKEN via valueFrom/secretKeyRef; leaving the old plain
    # "value:" field causes a strategic-merge-patch conflict during upgrade.
    for pattern in ("osac-operator", "bmf-operator"):
        deploys = oc("get", "deploy", "-n", config.namespace, "-o", "name",
                     capture=True, check=False).stdout.strip().splitlines()
        for d in deploys:
            if pattern in d:
                oc("set", "env", d, "-n", config.namespace,
                   "OSAC_AAP_URL-", "OSAC_AAP_TOKEN-")
                break
    base_domain = "hosted." + config.cluster_domain.removeprefix("apps.")
    run(["helm", "upgrade", "osac", "charts/osac/",
         "--namespace", config.namespace,
         "--values", config.values_file,
         "--set", f"service.externalHostname={config.external_host}",
         "--set", f"service.internalHostname={config.internal_host}",
         "--set", "aap.bootstrap.enabled=false",
         "--set", f"clusterFulfillment.config.HOSTED_CLUSTER_BASE_DOMAIN={base_domain}",
         "--timeout", "15m"])


def wait_fulfillment(config: RefreshConfig) -> None:
    """Wait for fulfillment-service and osac-operator Deployments to become healthy."""
    print("  Waiting for fulfillment deployments...")
    deploys = oc_json("get", "deploy", "-n", config.namespace,
                      "-l", "app=fulfillment-service")
    for d in deploys["items"]:
        name: str = d["metadata"]["name"]
        wait_rollout_healthy(name, config.namespace, timeout=300)
    wait_rollout_healthy("osac-operator", config.namespace, timeout=300)
    print("  Fulfillment + operator running")


def scale_aap_operator(config: RefreshConfig) -> None:
    """Scale AAP operator CSV to 1. Capture stale timestamp for later wait."""
    print("  Scaling AAP operator to 1...")
    config.aap_stale_ts = oc(
        "get", "automationcontroller", "osac-aap-controller",
        "-n", config.namespace,
        "-o", 'jsonpath={.status.conditions[?(@.type=="Successful")].lastTransitionTime}',
        capture=True, check=False,
    ).stdout.strip()
    csv = find_csv(namespace="ansible-aap",
                   deploy_name="automation-controller-operator-controller-manager")
    scale_csv_to(csv_name=csv, namespace="ansible-aap", replicas=1)
    print(f"  AAP operator scaled via CSV {csv}")


def _aap_controller_reconciled(config: RefreshConfig) -> bool:
    """Return True when the AAP controller has reconciled since the stale timestamp."""
    result = oc(
        "get", "automationcontroller", "osac-aap-controller",
        "-n", config.namespace,
        "-o", "jsonpath="
              "{.status.conditions[?(@.type==\"Running\")].status}"
              " "
              "{.status.conditions[?(@.type==\"Successful\")].lastTransitionTime}",
        capture=True, check=False,
    ).stdout.strip().split()
    if len(result) != 2:
        return False
    running, current_ts = result
    return running == "True" and current_ts != config.aap_stale_ts


def wait_aap_ready(config: RefreshConfig) -> None:
    """Wait for AAP controller reconciliation + gateway. Operator was scaled in Phase 1."""
    print("  Waiting for AAP controller reconciliation...")
    retry_until(
        description="AAP controller reconciliation",
        timeout=600, interval=10,
        condition=lambda: _aap_controller_reconciled(config),
    )

    aap_host = oc("get", "route", "osac-aap", "-n", config.namespace,
                  "-o", "jsonpath={.spec.host}", capture=True).stdout.strip()
    run_parallel([
        ("AAP gateway responding", lambda: retry_until(
            description="AAP gateway responding",
            timeout=600, interval=10,
            condition=lambda: subprocess.run(
                ["curl", "-sk", "-o", "/dev/null", "-w", "%{http_code}",
                 f"https://{aap_host}/api/gateway/v1/"],
                capture_output=True, text=True, check=False,
            ).stdout.strip() == "200",
        )),
        ("AAP controller-task rollout", lambda: oc(
            "rollout", "status", "deploy/osac-aap-controller-task",
            "-n", config.namespace, "--timeout=600s",
        )),
    ])
    print("  AAP running")


def fix_assisted_service() -> None:
    """Reset assisted-service credentials after snapshot identity drift."""
    if oc_exists("secret/assisted-servicelocal-auth", "multicluster-engine"):
        print("  Deleting stale assisted-service auth keypair...")
        oc("delete", "secret", "assisted-servicelocal-auth", "-n", "multicluster-engine")
    if oc_exists("deploy/assisted-service", "multicluster-engine"):
        oc("rollout", "restart", "deploy/assisted-service", "-n", "multicluster-engine")
        oc("rollout", "restart", "statefulset/assisted-image-service", "-n", "multicluster-engine")



# ─── Main ────────────────────────────────────────────────────────────────────


def main() -> None:
    """Run the post-snapshot refresh workflow (prepare, deploy, post-flight)."""
    values_file = os.environ.get("VALUES_FILE")
    if not values_file:
        print("ERROR: VALUES_FILE must be set (e.g. values/vmaas-ci/values.yaml)", file=sys.stderr)
        sys.exit(1)

    namespace = os.environ.get("INSTALLER_NAMESPACE", "osac-e2e-ci")
    cluster_domain = oc("get", "ingresses.config/cluster", "-o",
                        "jsonpath={.spec.domain}", capture=True).stdout.strip()

    config = RefreshConfig(
        values_file=values_file,
        namespace=namespace,
        cluster_domain=cluster_domain,
    )

    start_time = time.time()
    print("=== Refreshing OSAC after snapshot boot (Helm) ===")
    print(f"Namespace: {config.namespace}")
    print(f"Values: {config.values_file}")
    print(f"Cluster domain: {config.cluster_domain}")
    print()

    # Phase 1: Start slow operators early + fix cluster identity (all parallel)
    phase_start = time.time()
    print("[Phase 1] Starting operators + fixing cluster identity...")
    run_parallel([
        ("scale AAP operator", lambda: scale_aap_operator(config)),
        ("patch routes", lambda: patch_stale_routes(config)),
        ("pre-fix cert SANs", lambda: pre_fix_cert_sans(config)),
        ("refresh CDI certs", refresh_cdi_certificates),
        ("refresh MetalLB", refresh_metallb_and_subnet),
        ("wait Keycloak cert", lambda: wait_keycloak_cert(config)),
    ])
    print(f"[Phase 1] Done ({time.time() - phase_start:.0f}s)\n")

    # Phase 2: Prepare environment (all parallel)
    phase_start = time.time()
    print("[Phase 2] Preparing environment...")
    run_parallel([
        ("Keycloak sync", lambda: keycloak_sync(config)),
        ("create secrets", lambda: create_secrets(config)),
        ("ensure CA bundle", lambda: ensure_ca_bundle(config)),
        ("wait TLS certs", lambda: wait_tls_certs(config)),
    ])
    print(f"[Phase 2] Done ({time.time() - phase_start:.0f}s)\n")

    # Phase 3: Deploy + health check
    phase_start = time.time()
    print("[Phase 3] Deploying + waiting...")
    # wait_aap_ready() must run BEFORE upgrade_osac(): the osac chart's
    # publish-templates post-upgrade hook (charts/osac/templates/hooks/
    # publish-templates.yaml) launches an AAP job template as part of
    # `helm upgrade` itself (Helm blocks on hook completion) -- calling
    # wait_aap_ready() only *after* upgrade_osac() returns is too late to
    # help that hook, since by then it has already run (and can already
    # have failed). Confirmed via a real dispatch on a fresh EC2 instance:
    # AAP's operators (scaled up in Phase 1, but still needing a full,
    # from-scratch image pull + CSV install on a box that's never run them
    # before -- unlike a long-lived, pre-warmed debug box) were still mid-
    # install (automation-controller-operator not yet available) when the
    # hook tried to launch a job against it, so the job errored instantly
    # (empty stdout) on every one of its 3 attempts until the hook's own
    # backoffLimit was exhausted, failing `helm upgrade` outright.
    wait_aap_ready(config)
    upgrade_osac(config)
    wait_fulfillment(config)
    # Console-proxy hard-exits if its initial JWKS fetch returns non-200.
    # It CrashLoopBackOff's until grpc-server is serving. Normally recovers
    # within ~70s (before the fulfillment wait above completes), so this
    # adds ~0s. The 360s timeout covers worst-case backoff (300s cap) if
    # timing is bad.
    if oc_exists("deploy/fulfillment-console-proxy", config.namespace):
        print("  Waiting for console-proxy (depends on grpc-server)...")
        oc("rollout", "status", "deploy/fulfillment-console-proxy",
           "-n", config.namespace, "--timeout=360s")
    fix_assisted_service()
    print("  Running final pod health check...")
    check_all_pods(config.namespace)
    print(f"[Phase 3] Done ({time.time() - phase_start:.0f}s)\n")

    total = time.time() - start_time
    print()
    print(f"=== Refresh complete ({total:.0f}s) ===")
    print(f"Cluster domain: {config.cluster_domain}")
    print(f"Namespace: {config.namespace}")


if __name__ == "__main__":
    main()
