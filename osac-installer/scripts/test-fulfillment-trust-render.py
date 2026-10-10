#!/usr/bin/env python3
"""Check the staged production Helm transport contract in both gate states."""

from __future__ import annotations

import pathlib
import re
import subprocess

CHART = pathlib.Path(__file__).resolve().parents[1] / "charts/osac"
HOOKS = {
    "fulfillment-create-hub",
    "create-network-class",
    "seed-cluster-versions",
    "register-local-storage",
    "osac-publish-templates",
}


def render(enabled: bool | None = None) -> list[str]:
    cmd = [
        "helm",
        "template",
        "osac",
        str(CHART),
        "--values",
        str(CHART / "ci/default-values.yaml"),
        "--set",
        "global.osacDeploymentId=ci/osac",
        "--set",
        "metering.enabled=true",
        "--set",
        "lvms.enabled=true",
        "--set",
        "hubAccess.enabled=true",
        "--set",
        "clusterVersions.enabled=true",
        "--set",
        "aap.instanceGroups.publishTemplates.enabled=true",
    ]
    if enabled is not None:
        cmd.extend(("--set", f"global.fulfillmentTrust.enabled={str(enabled).lower()}"))
    manifest = subprocess.run(cmd, check=True, capture_output=True, text=True).stdout
    return [doc for doc in manifest.split("\n---\n") if doc.strip()]


def select(docs: list[str], source: str, name: str | None = None) -> str:
    matches = [doc for doc in docs if f"# Source: {source}\n" in doc]
    if name:
        matches = [doc for doc in matches if "kind: Job\n" in doc and f"  name: {name}\n" in doc]
    if len(matches) != 1:
        raise AssertionError(f"expected one rendered {source} {name or ''}, found {len(matches)}")
    return matches[0]


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


def check(enabled: bool) -> None:
    docs = render(enabled)
    operator = select(docs, "osac/charts/operator/templates/deployment.yaml")
    metering = select(docs, "osac/charts/metering/templates/deployment.yaml")
    assert "mountPath: /etc/ca-bundle" in metering
    assert "readOnly: true" in metering
    assert "key: bundle.pem" in metering
    assert "name: ca-bundle" in metering
    for marker in ("mountPath: /etc/ca-bundle", "name: fulfillment-ca-bundle", "key: bundle.pem"):
        assert marker in operator
    assert "value: /etc/ca-bundle/bundle.pem" in metering
    assert re.search(
        rf'- name: FULFILLMENT_TRUST_ENABLED\n\s+value: "{str(enabled).lower()}"', metering
    ), "metering fulfillment trust flag does not match global.fulfillmentTrust.enabled"
    assert "--fulfillment-ca-file=/etc/ca-bundle/bundle.pem" in operator
    assert "--grpc-insecure" not in operator

    for name in HOOKS:
        source = f"osac/templates/hooks/{name}.yaml"
        if name == "fulfillment-create-hub":
            source = "osac/templates/hooks/create-hub.yaml"
        elif name == "osac-publish-templates":
            source = "osac/templates/hooks/publish-templates.yaml"
        job = select(docs, source, name)
        for marker in ("mountPath: /etc/ca-bundle", "key: bundle.pem", "name: ca-bundle"):
            assert marker in job, name
        assert "readOnly: true" in job, name
        assert re.search(r"\bcurl\b[^\n]*--cacert\s+/etc/ca-bundle/bundle\.pem\b", job), name
        assert not re.search(r"\bcurl\b[^\n]*(?:-[A-Za-z]*k[A-Za-z]*|--insecure)(?:\s|$)", job), name
        if name in {"create-network-class", "register-local-storage", "seed-cluster-versions"}:
            check_main_container_tls(job, name)


def check_production_default() -> None:
    docs = render(True)
    operator = select(docs, "osac/charts/operator/templates/deployment.yaml")
    controller = select(docs, "osac/charts/service/templates/controller/deployment.yaml")
    fulfillment_config = select(docs, "osac/templates/fulfillment-runtime-config.yaml")
    assert "--grpc-insecure" not in operator
    assert "--fulfillment-ca-file=/etc/ca-bundle/bundle.pem" in operator
    assert "--fulfillment-trust-enabled" not in controller
    assert "kind: ConfigMap" in fulfillment_config
    assert "name: osac-fulfillment-config" in fulfillment_config
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


def check_trust_gate(enabled: bool) -> None:
    docs = render(enabled)
    controller = select(docs, "osac/charts/service/templates/controller/deployment.yaml")
    operator = select(docs, "osac/charts/operator/templates/deployment.yaml")
    select(docs, "osac/templates/fulfillment-runtime-config.yaml")
    assert "--fulfillment-trust-enabled" not in controller
    assert "--metrics-bind-address=:8443" in operator
    metrics_reader_binding = "kind: ClusterRoleBinding\nmetadata:\n  name: osac-operator-metrics-reader\n"
    assert (metrics_reader_binding in "\n".join(docs)) is enabled
    assert re.search(
        rf'- name: OSAC_ENABLE_FULFILLMENT_TRUST_RECONCILER\n\s+value: "{str(enabled).lower()}"', operator
    ), "operator trust reconciler flag does not match global.fulfillmentTrust.enabled"
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
    check_trust_gate(False)
    check_trust_gate(True)
    check_infra_ca_bundle_targets()
    print("Fulfillment trust production render checks passed.")
