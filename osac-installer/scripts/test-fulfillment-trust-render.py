#!/usr/bin/env python3
"""Check the staged production Helm transport contract in both gate states."""

from __future__ import annotations

import pathlib
import re
import subprocess

CHART = pathlib.Path(__file__).resolve().parents[1] / "charts/osac"
VALUES = CHART.parents[1] / "values"
HOOKS = {
    "fulfillment-create-hub",
    "create-network-class",
    "seed-cluster-versions",
    "register-local-storage",
    "osac-publish-templates",
}
FULFILLMENT_AUTH_HOOKS = {
    "fulfillment-create-hub": "osac/templates/hooks/create-hub.yaml",
    "create-network-class": "osac/templates/hooks/create-network-class.yaml",
    "seed-cluster-versions": "osac/templates/hooks/seed-cluster-versions.yaml",
    "register-local-storage": "osac/templates/hooks/register-local-storage.yaml",
}
PLATFORM_IDENTITY_HOOKS = {
    "create-network-class",
    "seed-cluster-versions",
    "register-local-storage",
}


def render(
    enabled: bool | None = None,
    *,
    custom_auth_credentials: bool = False,
    openshift: bool = False,
    server_address: str | None = None,
) -> list[str]:
    cmd = [
        "helm",
        "template",
        "osac",
        str(CHART),
        "--values",
        str(CHART / "ci/default-values.yaml"),
        "--set",
        "global.osacDeploymentId=ci/osac",
        "--set-string",
        "global.clusterDomain=example.com",
        "--set",
        "lvms.enabled=true",
        "--set",
        "hubAccess.enabled=true",
        "--set",
        "clusterVersions.enabled=true",
        "--set",
        "aap.instanceGroups.publishTemplates.enabled=true",
    ]
    if custom_auth_credentials:
        cmd.extend(
            (
                "--set",
                "service.auth.controllerCredentials[0].secret.name=custom-fulfillment-credentials",
                "--set",
                "service.auth.controllerCredentials[0].secret.items[0].key=custom-client-id",
                "--set",
                "service.auth.controllerCredentials[0].secret.items[1].key=custom-client-secret",
                "--set",
                "operator.fulfillment.configMap.name=custom-fulfillment-config",
            )
        )
    if enabled is not None:
        cmd.extend(("--set", f"global.fulfillmentTrust.enabled={str(enabled).lower()}"))
    if openshift:
        cmd.extend(("--api-versions", "security.openshift.io/v1"))
    if server_address is not None:
        cmd.extend(("--set", f"operator.fulfillment.serverAddress={server_address}"))
    manifest = subprocess.run(cmd, check=True, capture_output=True, text=True).stdout
    return [doc for doc in manifest.split("\n---\n") if doc.strip()]


def select(docs: list[str], source: str, name: str | None = None) -> str:
    matches = [doc for doc in docs if f"# Source: {source}\n" in doc]
    if name:
        matches = [doc for doc in matches if "kind: Job\n" in doc and f"  name: {name}\n" in doc]
    if len(matches) != 1:
        raise AssertionError(f"expected one rendered {source} {name or ''}, found {len(matches)}")
    return matches[0]


def trust_reconciler_enabled(operator: str) -> bool:
    match = re.search(
        r'- name: OSAC_ENABLE_FULFILLMENT_TRUST_RECONCILER\n\s+value: "(true|false)"', operator
    )
    if match is None:
        raise AssertionError("rendered operator deployment is missing the trust reconciler setting")
    return match.group(1) == "true"


def main_container_commands(job: str) -> str:
    marker = "      containers:\n"
    if marker not in job:
        raise AssertionError("rendered Job has no main containers section")
    return job.split(marker, 1)[1]


def check_main_container_tls(job: str, name: str) -> None:
    commands = main_container_commands(job)
    curl_count = len(re.findall(r"\bcurl\b", commands))
    verified_count = len(re.findall(r"\bcurl\b[^\n]*--cacert\s+/etc/ca-bundle/bundle\.pem\b", commands))
    assert curl_count > 0, name
    assert verified_count == curl_count, name
    assert not re.search(r"\bcurl\b[^\n]*(?:-[A-Za-z]*k[A-Za-z]*|--insecure)(?:\s|$)", commands), name


def check_openshift_restricted_scc(job: str, name: str) -> None:
    template = job.split("  template:\n", 1)[1]
    pod_metadata, pod_spec = template.split("    spec:\n", 1)
    assert re.search(
        r"(?m)^      annotations:\n        openshift.io/required-scc: restricted-v2$", pod_metadata
    ), name
    assert not re.search(r"(?m)^      hostUsers: false$", pod_spec), name
    assert re.search(r"(?m)^        runAsNonRoot: true$", pod_spec), name
    assert not re.search(r"(?m)^        (?:runAsUser|runAsGroup|fsGroup):", pod_spec), name


def check_kind_non_root_identity(job: str, name: str) -> None:
    template = job.split("  template:\n", 1)[1]
    pod_spec = template.split("    spec:\n", 1)[1]
    for field, value in (
        ("runAsNonRoot", "true"),
        ("runAsUser", "1000"),
        ("fsGroup", "1000"),
    ):
        assert re.search(rf"(?m)^        {field}: {value}$", pod_spec), name


def check_fulfillment_auth(
    job: str,
    name: str,
    *,
    secret_name: str = "fulfillment-controller-credentials",
    client_id_key: str = "client-id",
    client_secret_key: str = "client-secret",
) -> None:
    commands = main_container_commands(job)
    assert "get_fulfillment_access_token()" in commands, name
    assert "AUTH_TOKEN=$(get_fulfillment_access_token)" in commands, name
    assert "grant_type=client_credentials" in commands, name
    assert "client_secret@${client_secret_file}" in commands, name
    assert "printf '%s\\n' \"${access_token}\"" in commands, name
    assert "Authorization: Bearer ${AUTH_TOKEN}" in commands, name
    assert "/var/run/secrets/kubernetes.io/serviceaccount/token" not in commands, name
    assert "name: FULFILLMENT_ISSUER_URL" in job, name
    assert "valueFrom:\n            secretKeyRef:" in job, name
    assert f'name: "{secret_name}"' in job, name
    assert f'key: "{client_id_key}"' in job, name
    assert f'secretName: "{secret_name}"' in job, name
    assert f'key: "{client_secret_key}"' in job, name
    assert "name: FULFILLMENT_CLIENT_SECRET_FILE" in job, name
    assert "mountPath: /var/run/secrets/fulfillment-client-secret" in job, name
    check_main_container_tls(job, name)

    if name == "fulfillment-create-hub":
        assert "serviceAccountName: admin" in job, name
    else:
        assert "automountServiceAccountToken: false" in job, name
        assert "serviceAccountName: admin" not in job, name


def check(enabled: bool) -> None:
    docs = render(enabled)
    openshift_docs = render(enabled, openshift=True)
    operator = select(docs, "osac/charts/operator/templates/deployment.yaml")
    runtime_config = select(docs, "osac/templates/fulfillment-runtime-config.yaml")
    metering = select(docs, "osac/charts/metering/templates/deployment.yaml")
    assert 'name: "osac-fulfillment-config"' in runtime_config
    assert "OSAC_FULFILLMENT_ISSUER_URL:" in runtime_config
    assert "OSAC_FULFILLMENT_TOKEN_FILE" not in operator
    assert "OSAC_FULFILLMENT_CLIENT_ID" in operator
    assert "OSAC_FULFILLMENT_CLIENT_SECRET" in operator
    assert "name: fulfillment-controller-credentials" in operator
    assert "key: client-id" in operator
    assert "key: client-secret" in operator
    assert "mountPath: /etc/ca-bundle" in metering
    assert "readOnly: true" in metering
    assert "key: bundle.pem" in metering
    assert "name: ca-bundle" in metering
    for marker in ("mountPath: /etc/ca-bundle", "name: fulfillment-ca-bundle", "key: bundle.pem"):
        assert marker in operator
    assert "value: /etc/ca-bundle/bundle.pem" in metering
    assert "--fulfillment-ca-file=/etc/ca-bundle/bundle.pem" in operator
    assert "--grpc-insecure" not in operator

    for name in HOOKS:
        source = f"osac/templates/hooks/{name}.yaml"
        if name == "fulfillment-create-hub":
            source = "osac/templates/hooks/create-hub.yaml"
        elif name == "osac-publish-templates":
            source = "osac/templates/hooks/publish-templates.yaml"
        job = select(docs, source, name)
        if name in PLATFORM_IDENTITY_HOOKS:
            check_kind_non_root_identity(job, name)
            check_openshift_restricted_scc(select(openshift_docs, source, name), name)
        for marker in ("mountPath: /etc/ca-bundle", "key: bundle.pem", "name: ca-bundle"):
            assert marker in job, name
        assert "readOnly: true" in job, name
        assert re.search(r"\bcurl\b[^\n]*--cacert\s+/etc/ca-bundle/bundle\.pem\b", job), name
        assert not re.search(r"\bcurl\b[^\n]*(?:-[A-Za-z]*k[A-Za-z]*|--insecure)(?:\s|$)", job), name
        if name in {"create-network-class", "register-local-storage", "seed-cluster-versions"}:
            check_main_container_tls(job, name)
    for name, source in FULFILLMENT_AUTH_HOOKS.items():
        check_fulfillment_auth(select(docs, source, name), name)


def check_production_default() -> None:
    docs = render(True)
    operator = select(docs, "osac/charts/operator/templates/deployment.yaml")
    controller = select(docs, "osac/charts/service/templates/controller/deployment.yaml")
    fulfillment_config = select(docs, "osac/templates/fulfillment-runtime-config.yaml")
    assert "--grpc-insecure" not in operator
    assert "--fulfillment-ca-file=/etc/ca-bundle/bundle.pem" in operator
    assert "--fulfillment-trust-enabled" not in controller
    assert "kind: ConfigMap" in fulfillment_config
    assert 'name: "osac-fulfillment-config"' in fulfillment_config
    assert "OSAC_FULFILLMENT_ENDPOINT:" in fulfillment_config
    assert "OSAC_FULFILLMENT_ISSUER_URL:" in fulfillment_config
    for name in HOOKS:
        source = f"osac/templates/hooks/{name}.yaml"
        if name == "fulfillment-create-hub":
            source = "osac/templates/hooks/create-hub.yaml"
        elif name == "osac-publish-templates":
            source = "osac/templates/hooks/publish-templates.yaml"
        job = select(docs, source, name)
        if name in {"create-network-class", "register-local-storage", "seed-cluster-versions"}:
            check_main_container_tls(job, name)


def check_custom_auth_credentials() -> None:
    docs = render(custom_auth_credentials=True)
    for name, source in FULFILLMENT_AUTH_HOOKS.items():
        check_fulfillment_auth(
            select(docs, source, name),
            name,
            secret_name="custom-fulfillment-credentials",
            client_id_key="custom-client-id",
            client_secret_key="custom-client-secret",
        )
    runtime_config = next(doc for doc in docs if "OSAC_FULFILLMENT_CLIENT_ID_SECRET_NAME" in doc)
    assert 'name: "custom-fulfillment-config"' in runtime_config
    assert 'osac.openshift.io/fulfillment-auth-config: "true"' in runtime_config
    assert 'OSAC_FULFILLMENT_CLIENT_ID_SECRET_NAME: "custom-fulfillment-credentials"' in runtime_config
    assert 'OSAC_FULFILLMENT_CLIENT_ID_SECRET_KEY: "custom-client-id"' in runtime_config
    assert 'OSAC_FULFILLMENT_CLIENT_SECRET_SECRET_NAME: "custom-fulfillment-credentials"' in runtime_config
    assert 'OSAC_FULFILLMENT_CLIENT_SECRET_SECRET_KEY: "custom-client-secret"' in runtime_config


def check_profile_emergency_service_accounts() -> None:
    expected_profiles = {
        "caas-ci/instance.yaml": {
            "admin",
            "template-publisher",
            "osac-metering",
        },
        "vmaas-ci/instance.yaml": {
            "admin",
            "template-publisher",
            "osac-metering",
        },
        "dev/instance.yaml": {
            "admin",
            "template-publisher",
            "osac-metering",
        },
        "bmaas-ci/instance.yaml": {
            "admin",
            "template-publisher",
            "osac-metering",
        },
        "cudn-evpn-netris-test/instance.yaml": {
            "admin",
            "template-publisher",
            "osac-metering",
        },
        "dev/kind-instance.yaml": {
            "admin",
        },
        "full-ci/instance.yaml": {
            "admin",
            "template-publisher",
            "osac-metering",
        },
    }
    for profile, expected_accounts in expected_profiles.items():
        cmd = [
            "helm",
            "template",
            "osac",
            str(CHART),
            "--values",
            str(CHART / "ci/default-values.yaml"),
            "--values",
            str(VALUES / profile),
            "--namespace",
            "osac",
            "--set",
            "global.osacDeploymentId=ci/osac",
            "--set",
            "service.internalHostname=fulfillment-internal-api.test",
            "--set",
            "service.externalHostname=fulfillment.test",
            "--set",
            "service.mcp.externalHostname=mcp.osac.localhost",
        ]
        manifest = subprocess.run(cmd, check=True, capture_output=True, text=True).stdout
        docs = [doc for doc in manifest.split("\n---\n") if doc.strip()]
        deployment = select(
            docs,
            "osac/charts/service/templates/grpc-server/deployment.yaml",
        )
        match = re.search(r"--emergency-service-accounts=([^\s]+)", deployment)
        assert match, profile
        accounts = set(match.group(1).split(","))
        assert "admin" in accounts, profile
        assert accounts == expected_accounts, profile


def check_operator_fulfillment_can_be_disabled() -> None:
    docs = render(False, server_address="")
    operator = select(docs, "osac/charts/operator/templates/deployment.yaml")
    assert "OSAC_FULFILLMENT_SERVER_ADDRESS" not in operator
    assert "OSAC_FULFILLMENT_CLIENT_ID" not in operator
    assert "OSAC_FULFILLMENT_CLIENT_SECRET" not in operator


def check_trust_gate(enabled: bool) -> None:
    docs = render(enabled)
    controller = select(docs, "osac/charts/service/templates/controller/deployment.yaml")
    operator = select(docs, "osac/charts/operator/templates/deployment.yaml")
    select(docs, "osac/templates/fulfillment-runtime-config.yaml")
    assert "--fulfillment-trust-enabled" not in controller
    assert "--metrics-bind-address=:8443" in operator
    metrics_reader_binding = "kind: ClusterRoleBinding\nmetadata:\n  name: osac-operator-metrics-reader\n"
    assert (metrics_reader_binding in "\n".join(docs)) is enabled
    assert trust_reconciler_enabled(operator) is enabled, (
        "operator trust reconciler flag does not match global.fulfillmentTrust.enabled"
    )
    assert "--fulfillment-ca-file=/etc/ca-bundle/bundle.pem" in operator
    assert "--grpc-insecure" not in operator


def check_infra_ca_bundle_targets() -> None:
    script = (CHART.parents[1] / "charts/osac-infra/files/hooks/apply-ca-bundle.sh").read_text(encoding="utf-8")
    template = (CHART.parents[1] / "charts/osac-infra/templates/hooks/apply-ca-bundle.yaml").read_text(
        encoding="utf-8"
    )
    assert '- "${RELEASE_NAMESPACE}"' in script
    assert '- "${OSAC_NAMESPACE}"' in script
    assert "CSI_NAMESPACE" not in script
    assert "CSI_NAMESPACE" not in template


if __name__ == "__main__":
    check(False)
    check(True)
    check_production_default()
    check_custom_auth_credentials()
    check_profile_emergency_service_accounts()
    check_operator_fulfillment_can_be_disabled()
    check_trust_gate(False)
    check_trust_gate(True)
    check_infra_ca_bundle_targets()
    print("Fulfillment trust production render checks passed.")
