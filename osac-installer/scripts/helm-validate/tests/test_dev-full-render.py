#!/usr/bin/env python3
"""Check dev-full TLS, ARM64 admission, cluster version, and Helm post-rendering."""

from __future__ import annotations

import os
import pathlib
import re
import subprocess
import tempfile

INSTALLER = pathlib.Path(__file__).resolve().parents[3]
COMPATIBILITY_CHECK = INSTALLER / "scripts/dev-full/check-cluster-compatibility.sh"
POST_RENDERER = INSTALLER / "scripts/dev-full/helm-post-renderer/helm-post-render.sh"

PROFILE_VALUES = (
    "--values",
    "values/dev/kind-instance.yaml",
    "--values",
    "values/dev/kind-instance-devfull.yaml",
)


def helm_template(*args: str) -> str:
    result = subprocess.run(
        ["helm", "template", *args], check=True, capture_output=True, text=True, cwd=INSTALLER
    )
    return result.stdout


def make_dry_run(target: str, *assignments: str) -> str:
    result = subprocess.run(
        ["make", "--no-print-directory", "--dry-run", target, *assignments],
        check=True,
        capture_output=True,
        text=True,
        cwd=INSTALLER,
    )
    return result.stdout


def docs(rendered: str) -> list[str]:
    return [doc for doc in rendered.split("\n---\n") if doc.strip()]


def embedded_script(rendered: str, name: str) -> str:
    """Extract a ConfigMap-embedded script the way the chart renders it."""
    lines = rendered.splitlines()
    for index, line in enumerate(lines):
        if line == f"  {name}: |":
            script: list[str] = []
            for script_line in lines[index + 1 :]:
                # Two-space indentation starts the next key at the same level.
                if script_line.startswith("  ") and not script_line.startswith("   "):
                    break
                script.append(script_line[4:] if script_line.startswith("    ") else script_line)
            return "\n".join(script)
    raise AssertionError(f"embedded script {name} not found in the rendered devstack chart")


def check_infra_renders() -> None:
    openbao = helm_template(
        "test",
        "charts/osac-infra",
        "--namespace",
        "osac-infra",
        "--values",
        "values/dev/kind-infra.yaml",
        "--values",
        "values/dev/kind-infra-devfull.yaml",
        "--show-only",
        "templates/bundled-openbao.yaml",
    )
    assert 'value: "https://keycloak.osac.localhost:8443/realms/osac"' in openbao, (
        "dev-full OpenBao must use the advertised Keycloak issuer"
    )

    cudn_crd = helm_template(
        "test",
        "charts/osac-infra",
        "--namespace",
        "osac-infra",
        "--values",
        "values/dev/kind-infra.yaml",
        "--values",
        "values/dev/kind-infra-devfull.yaml",
        "--show-only",
        "templates/fake-crds.yaml",
    )
    for expected in (
        "name: clusteruserdefinednetworks.k8s.ovn.org",
        "group: k8s.ovn.org",
        "kind: ClusterUserDefinedNetwork",
        "name: v1",
        "x-kubernetes-preserve-unknown-fields: true",
        "scope: Cluster",
    ):
        assert expected in cudn_crd, (
            "dev-full must render the cluster-scoped, non-finalized Kind CUDN API placeholder in osac-infra"
        )
    assert "finalizers:" not in cudn_crd, (
        "dev-full must render the cluster-scoped, non-finalized Kind CUDN API placeholder in osac-infra"
    )


def check_osac_render() -> None:
    rendered = helm_template(
        "osac",
        "charts/osac",
        "--namespace",
        "osac",
        "--values",
        "values/dev/kind-instance.yaml",
        "--values",
        "values/dev/kind-instance-devfull.yaml",
        "--set-string",
        "global.osacDeploymentId=osac/osac",
    )
    assert any(
        "metering-service:latest" in doc and "imagePullPolicy: IfNotPresent" in doc
        for doc in docs(rendered)
    ), "Kind dev-full must use the locally loaded metering image without pulling it"
    assert re.search(r'name: OSAC_AAP_ORGANIZATION\n *value: "Default"', rendered), (
        "dev-full must use the AWX Default organization that configure-awx.sh assigns OSAC resources to"
    )
    assert re.search(r'name: OSAC_ENABLE_NETWORKING_PROVISIONING\n *value: "false"', rendered), (
        "Kind dev-full must disable provider networking so it does not require Netris"
    )
    assert not any(
        "kind: Deployment" in doc and "name: osac-ui" in doc for doc in docs(rendered)
    ), "dev-full must keep the umbrella chart's UI disabled to avoid a second release owner"


def check_arm64_admission(chart_render: str) -> None:
    assert not re.search(r"^kind: MutatingAdmissionPolicy(Binding)?$", chart_render, re.MULTILINE), (
        "the ARM64 VM compatibility policy must be disabled by default and for Linux dev-full"
    )

    arm64_compat = helm_template(
        "test",
        "charts/osac-devstack",
        "--namespace",
        "osac",
        "--set",
        "arm64VmCompatibility.enabled=true",
    )
    assert len(re.findall(r"^kind: MutatingAdmissionPolicy$", arm64_compat, re.MULTILINE)) == 1, (
        "Apple Silicon dev-full must render exactly one VM compatibility policy and binding"
    )
    assert (
        len(re.findall(r"^kind: MutatingAdmissionPolicyBinding$", arm64_compat, re.MULTILINE)) == 1
    ), "Apple Silicon dev-full must render exactly one VM compatibility policy and binding"
    for expected in (
        "apiVersion: admissionregistration.k8s.io/v1",
        'operations: ["CREATE", "UPDATE"]',
        'resources: ["virtualmachines"]',
        "failurePolicy: Fail",
        "operator: Exists",
        "key: osac.openshift.io/tenant",
        "/spec/template/spec/domain/features/smm",
        "/spec/template/spec/domain/features/hyperv",
        "/spec/template/spec/domain/clock/timer/hyperv",
    ):
        assert expected in arm64_compat, (
            "the ARM64 VM compatibility resources must scope and remove only the planned KubeVirt fields"
        )


def check_make_wiring() -> None:
    mac_arm = make_dry_run(
        "install-devstack", "PLATFORM=kind", "PROFILE=dev-full", "NS=osac", "HOST_IS_MAC_ARM=true"
    )
    assert "--set arm64VmCompatibility.enabled=true" in mac_arm, (
        "Apple Silicon dev-full must check node architecture and enable the compatibility policy"
    )
    assert re.search(
        r"^\s*scripts/dev-full/check-cluster-compatibility\.sh\s*$", mac_arm, re.MULTILINE
    ), "Apple Silicon dev-full must check node architecture and enable the compatibility policy"

    linux = make_dry_run(
        "install-devstack", "PLATFORM=kind", "PROFILE=dev-full", "NS=osac", "HOST_IS_MAC_ARM=false"
    )
    assert "arm64VmCompatibility.enabled=true" not in linux and "check-cluster-compatibility.sh" not in linux, (
        "Linux dev-full must not enable the ARM64 VM compatibility policy or run its compatibility check"
    )

    dev = make_dry_run("install", "PLATFORM=kind", "PROFILE=dev", "NS=osac", "HOST_IS_MAC_ARM=true")
    assert "osac-devstack" not in dev and "arm64VmCompatibility.enabled=true" not in dev, (
        "PROFILE=dev must not install the dev-full ARM64 VM compatibility policy"
    )


def check_cluster_compatibility() -> None:
    stub = """\
#!/usr/bin/env bash
set -euo pipefail
case "${1:-}" in
  version)
    if [[ "${2:-}" != "-o" || "${3:-}" != "json" ]]; then
      echo "kubectl version must request JSON: $*" >&2
      exit 2
    fi
    printf '{"serverVersion":{"gitVersion":"%s"}}\\n' "${MOCK_SERVER_VERSION:-}"
    ;;
  get)
    printf '%s\\n' "${MOCK_NODE_ARCHITECTURES:-}" | tr ' ' '\\n'
    ;;
  *)
    echo "unexpected kubectl invocation: $*" >&2
    exit 2
    ;;
esac
"""
    with tempfile.TemporaryDirectory(prefix="dev-full-render-") as stub_dir:
        kubectl = pathlib.Path(stub_dir) / "kubectl"
        kubectl.write_text(stub, encoding="utf-8")
        kubectl.chmod(0o755)

        def run_check(server_version: str, architectures: str) -> subprocess.CompletedProcess[str]:
            env = {
                **os.environ,
                "PATH": f"{stub_dir}:{os.environ['PATH']}",
                "MOCK_SERVER_VERSION": server_version,
                "MOCK_NODE_ARCHITECTURES": architectures,
            }
            return subprocess.run(
                [str(COMPATIBILITY_CHECK)],
                env=env,
                capture_output=True,
                text=True,
                cwd=INSTALLER,
            )

        accepted = run_check("v1.36.0", "arm64 arm64")
        assert accepted.returncode == 0, (
            f"the installer must accept a Kubernetes 1.36 API server with arm64 nodes: {accepted.stderr}"
        )
        accepted = run_check("v1.37.1", "arm64")
        assert accepted.returncode == 0, (
            f"the installer must accept Kubernetes API servers newer than 1.36: {accepted.stderr}"
        )

        rejected = run_check("v1.35.9", "arm64")
        assert rejected.returncode != 0, (
            "the installer must reject Kubernetes API servers older than 1.36"
        )
        assert "requires a Kubernetes API server v1.36 or later" in rejected.stderr + rejected.stdout, (
            "an older Kubernetes API server must produce actionable remediation"
        )
        assert "Upgrade this Kind cluster or recreate it" in rejected.stderr + rejected.stdout, (
            "an older Kubernetes API server must produce actionable remediation"
        )

        rejected = run_check("v1.36.0", "arm64 amd64")
        assert rejected.returncode != 0, "Apple Silicon dev-full must reject non-arm64 Kind nodes"
        assert "requires arm64 Kind nodes" in rejected.stderr + rejected.stdout, (
            "a non-arm64 Kind cluster must produce an architecture error"
        )


def check_ui_tls(chart_render: str) -> None:
    for expected in (
        'FULFILLMENT_TLS_CA_FILE: "/etc/osac-ui/tls/api/bundle.pem"',
        'OIDC_TLS_CA_FILE: "/etc/osac-ui/tls/auth/bundle.pem"',
        'FULFILLMENT_API_URL: "https://fulfillment-api.osac.localhost:8000"',
        'OIDC_ISSUER_URL: "https://keycloak.osac.localhost:8443/realms/osac"',
        "imagePullPolicy: IfNotPresent",
        "mountPath: /etc/osac-ui/tls/api",
        "mountPath: /etc/osac-ui/tls/auth",
        "automountServiceAccountToken: false",
    ):
        assert expected in chart_render, (
            "dev-full must deploy the shared osac-ui chart with verified TLS and Kind image settings"
        )

    ui_deployments = [
        doc for doc in docs(chart_render) if "kind: Deployment" in doc and "name: osac-ui" in doc
    ]
    assert len(ui_deployments) == 1, (
        "osac-devstack must deploy exactly one UI Deployment through its chart dependency"
    )
    assert not any(
        "kind: Route" in doc and "name: osac-ui" in doc for doc in docs(chart_render)
    ), "dev-full on Kind must disable the OpenShift UI Route"

    route_enabled = helm_template(
        "test", "charts/osac-devstack", "--namespace", "osac", "--set", "ui.route.enabled=true"
    )
    assert any(
        "kind: Route" in doc and "name: osac-ui" in doc for doc in docs(route_enabled)
    ), "the shared UI chart must retain its configurable OpenShift Route"

    ui_route = helm_template(
        "test", "charts/osac-devstack", "--namespace", "osac", "--show-only", "templates/ui.yaml"
    )
    for expected in ("kind: HTTPRoute", "ui.osac.localhost", "name: osac-ui"):
        assert expected in ui_route, (
            "dev-full must route the shared osac-ui Service through Gateway API"
        )


def check_hook_ca_bundle() -> None:
    for template in ("templates/hooks/provision-tenant.yaml", "templates/hooks/seed-catalog.yaml"):
        rendered = helm_template(
            "test",
            "charts/osac-devstack",
            "--namespace",
            "osac",
            *PROFILE_VALUES,
            "--show-only",
            template,
        )
        for expected in ("name: ca-bundle", "key: bundle.pem", "mountPath: /etc/ca-bundle"):
            assert expected in rendered, f"{template} must mount the trusted CA bundle"


def check_hook_credentials() -> None:
    for template in ("templates/hooks/seed-catalog.yaml", "templates/hooks/provision-tenant.yaml"):
        rendered = helm_template(
            "test",
            "charts/osac-devstack",
            "--namespace",
            "osac",
            *PROFILE_VALUES,
            "--show-only",
            template,
        )
        for expected in (
            "name: FULFILLMENT_ISSUER_URL",
            "name: FULFILLMENT_CLIENT_ID",
            'secretName: "fulfillment-controller-credentials"',
            'key: "client-id"',
            'key: "client-secret"',
            "mountPath: /var/run/secrets/fulfillment-client-secret",
            'value: "https://keycloak.osac.localhost:8443/realms/osac"',
        ):
            assert expected in rendered, (
                f"{template} must provide service-account credentials by Secret reference"
            )

    custom_auth = (
        "--set",
        "service.auth.controllerCredentials[0].secret.name=custom-fulfillment-credentials",
        "--set",
        "service.auth.controllerCredentials[0].secret.items[0].key=custom-client-id",
        "--set",
        "service.auth.controllerCredentials[0].secret.items[1].key=custom-client-secret",
    )
    for template in ("templates/hooks/seed-catalog.yaml", "templates/hooks/provision-tenant.yaml"):
        rendered = helm_template(
            "test",
            "charts/osac-devstack",
            "--namespace",
            "osac",
            *PROFILE_VALUES,
            *custom_auth,
            "--show-only",
            template,
        )
        for expected in (
            'name: "custom-fulfillment-credentials"',
            'key: "custom-client-id"',
            'secretName: "custom-fulfillment-credentials"',
            'key: "custom-client-secret"',
        ):
            assert expected in rendered, (
                f"{template} must use custom Fulfillment credential Secret references"
            )

    for template in ("templates/hooks/seed-catalog.yaml", "templates/hooks/provision-tenant.yaml"):
        rendered = helm_template(
            "test",
            "charts/osac-devstack",
            "--namespace",
            "osac",
            *PROFILE_VALUES,
            "--show-only",
            template,
        )
        assert "fsGroup: 1001" in rendered, (
            f"{template} must make the mounted client-secret file readable by the CLI image"
        )


def check_hook_authentication(chart_render: str) -> None:
    assert "fulfillment-auth.sh: |" in chart_render and "get_fulfillment_access_token()" in chart_render, (
        "dev-full Fulfillment callers must exchange service-account credentials for Keycloak tokens"
    )
    assert 'kubectl -n "${NS}" create token admin' not in chart_render, (
        "dev-full Fulfillment callers must exchange service-account credentials for Keycloak tokens"
    )

    seed_catalog = embedded_script(chart_render, "seed-catalog-simple.sh")
    assert 'AUTH_TOKEN="$(get_fulfillment_access_token)"' in seed_catalog, (
        "dev-full catalog seeding must authenticate Fulfillment requests with a Keycloak service-account token"
    )
    assert "Authorization: Bearer ${AUTH_TOKEN}" in seed_catalog, (
        "dev-full catalog seeding must authenticate Fulfillment requests with a Keycloak service-account token"
    )

    provision_tenant = embedded_script(chart_render, "provision-tenant.sh")
    assert 'fulfillment_token="$(get_fulfillment_access_token)"' in provision_tenant, (
        "dev-full tenant provisioning must pass its Keycloak token to grpcurl through the temporary Secret"
    )
    assert '"authorization: Bearer ${FULFILLMENT_TOKEN}"' in provision_tenant, (
        "dev-full tenant provisioning must pass its Keycloak token to grpcurl through the temporary Secret"
    )
    assert "--from-file=token=/dev/stdin" in provision_tenant, (
        "dev-full tenant provisioning must pass its Keycloak token to grpcurl through the temporary Secret"
    )

    assert "kubectl get clusteruserdefinednetwork" not in provision_tenant, (
        "dev-full tenant hook must not wait for provider networking resources on Kind"
    )
    assert "get subnets" not in provision_tenant, (
        "dev-full tenant hook must not wait for provider networking resources on Kind"
    )
    assert "kubectl create namespace" not in provision_tenant, (
        "dev-full tenant hook must not wait for provider networking resources on Kind"
    )

    assert '"-expand-headers"' in chart_render, (
        "dev-full tenant gRPC call must read and clean up its temporary Fulfillment-token Secret"
    )
    assert "authorization: Bearer ${FULFILLMENT_TOKEN}" in chart_render, (
        "dev-full tenant gRPC call must read and clean up its temporary Fulfillment-token Secret"
    )
    assert 'kubectl -n "${NS}" delete secret "${TOKEN_SECRET_NAME}"' in chart_render, (
        "dev-full tenant gRPC call must read and clean up its temporary Fulfillment-token Secret"
    )

    assert "-cacert" in chart_render and "--cacert" in chart_render, (
        "dev-full setup hooks must verify TLS without insecure flags"
    )
    assert not re.search(
        r"curl.*\s(-k|--insecure)(\s|$)|grpcurl.*\s-insecure", chart_render
    ), "dev-full setup hooks must verify TLS without insecure flags"


def check_awx_labels_and_post_renderer() -> None:
    awx = helm_template(
        "test",
        "charts/osac-devstack",
        "--namespace",
        "osac",
        *PROFILE_VALUES,
        "--show-only",
        "templates/awx-instance.yaml",
    )
    awx_docs = [doc for doc in docs(awx) if "kind: AWX" in doc]
    assert len(awx_docs) == 1, "AWX resource must use chart selector labels without the operator-owned managed-by label"
    assert "app.kubernetes.io/name: osac-devstack" in awx_docs[0], (
        "AWX resource must use chart selector labels without the operator-owned managed-by label"
    )
    assert "app.kubernetes.io/managed-by" not in awx_docs[0], (
        "AWX resource must use chart selector labels without the operator-owned managed-by label"
    )

    controller = helm_template(
        "test",
        "charts/osac-devstack",
        "--namespace",
        "osac",
        *PROFILE_VALUES,
        "--show-only",
        "charts/awx-operator/templates/deployment-awx-operator-controller-manager.yaml",
    )
    proxied = subprocess.run(
        [str(POST_RENDERER)],
        input=controller,
        capture_output=True,
        text=True,
        check=True,
    ).stdout
    assert "image: ghcr.io/kube-rbac-proxy/kube-rbac-proxy:v0.22.1" in proxied, (
        "AWX operator post-renderer must replace the unavailable proxy image"
    )
    assert "image: gcr.io/kubebuilder/kube-rbac-proxy:v0.15.0" not in proxied, (
        "AWX operator post-renderer must replace the unavailable proxy image"
    )


def test_main() -> None:
    check_infra_renders()
    check_osac_render()

    chart_render = helm_template(
        "test",
        "charts/osac-devstack",
        "--namespace",
        "osac",
        *PROFILE_VALUES,
        "--set",
        "ui.enabled=true",
    )
    check_arm64_admission(chart_render)
    check_make_wiring()
    check_cluster_compatibility()
    check_ui_tls(chart_render)
    check_hook_ca_bundle()
    check_hook_credentials()
    check_hook_authentication(chart_render)
    check_awx_labels_and_post_renderer()
    print("Dev-full render, ARM64 admission, cluster-version, TLS, and Helm post-render checks passed.")
