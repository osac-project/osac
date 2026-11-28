#!/usr/bin/env python3
"""Check the supported MCP installer contract through Helm rendering."""

from __future__ import annotations

import subprocess

import yaml
from _render import INSTALLER, PROFILES, select_one, template_command

CHART = INSTALLER / "charts/osac"
INFRA_CHART = INSTALLER / "charts/osac-infra"
PROFILE = PROFILES[0]
DEPLOYMENT_SOURCE = "# Source: osac/charts/service/templates/mcp-server/deployment.yaml\n"
SERVICE_SOURCE = "# Source: osac/charts/service/templates/mcp-server/service.yaml\n"
ROUTE_SOURCE = "# Source: osac/charts/service/templates/mcp-server/route.yaml\n"
CERTIFICATE_SOURCE = "# Source: osac/charts/service/templates/mcp-server/certificate.yaml\n"
BUDGET_SOURCE = "# Source: osac/charts/service/templates/mcp-server/poddisruptionbudget.yaml\n"
RUNTIME_SOURCE = "# Source: osac/templates/mcp-runtime-config.yaml\n"


def render(extra: list[str] | None = None) -> list[str]:
    result = subprocess.run(
        template_command(PROFILE) + (extra or []),
        check=True,
        capture_output=True,
        text=True,
        timeout=300,
    )
    return [doc for doc in result.stdout.split("\n---\n") if doc.strip()]


def render_status(extra: list[str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        template_command(PROFILE) + extra,
        capture_output=True,
        text=True,
        timeout=300,
    )


def document(docs: list[str], source: str, description: str) -> str:
    return select_one(docs, lambda doc: source in doc, description)


def check_disabled() -> None:
    docs = render(
        [
            "--set",
            "global.mcp.enabled=false",
            "--set-string",
            "global.mcp.publicURL=",
            "--set-string",
            "global.mcp.issuerURL=",
            "--set-string",
            "global.mcp.exchangeTokenEndpoint=",
            "--set-string",
            "global.mcp.exchangeClientID=",
            "--set-string",
            "global.mcp.exchangeClientSecret.name=",
            "--set",
            "global.mcp.trust.mode=system",
            "--set-string",
            "global.mcp.trust.configMap=",
        ]
    )
    for source in (
        DEPLOYMENT_SOURCE,
        SERVICE_SOURCE,
        ROUTE_SOURCE,
        CERTIFICATE_SOURCE,
        BUDGET_SOURCE,
        RUNTIME_SOURCE,
    ):
        assert not any(source in doc for doc in docs), source
    assert not any("name: fulfillment-mcp-server\n" in doc for doc in docs)
    assert not any("name: osac-mcp-runtime\n" in doc for doc in docs)


def check_infra_disabled() -> None:
    result = render_infra([])
    assert "name: keycloak-mcp-client\n" not in result.stdout
    assert "name: osac-mcp-exchange-credentials\n" not in result.stdout
    assert "name: MCP_RESOURCE_URL\n" not in result.stdout
    assert "name: mcp-client\n" not in result.stdout


def render_infra(extra: list[str]) -> subprocess.CompletedProcess[str]:
    result = subprocess.run(
        [
            "helm",
            "template",
            "osac-infra",
            str(INFRA_CHART),
            "--namespace",
            "osac-infra",
            "--values",
            str(INSTALLER / "values/dev/kind-infra.yaml"),
            "--set",
            "osacNamespace=osac",
            *extra,
        ],
        check=True,
        capture_output=True,
        text=True,
        timeout=300,
    )
    return result


def check_infra_enabled() -> None:
    result = render_infra(["--values", str(INSTALLER / "values/dev/mcp.yaml")])
    assert "name: keycloak-mcp-client\n" in result.stdout
    assert "name: osac-mcp-exchange-credentials\n" in result.stdout
    assert 'name: MCP_RESOURCE_URL\n          value: "https://mcp.osac.localhost:8443/"\n' in result.stdout
    assert "name: mcp-client\n" in result.stdout


def check_enabled() -> None:
    docs = render()
    deployment_doc = document(docs, DEPLOYMENT_SOURCE, "MCP Deployment")
    deployment = yaml.safe_load(deployment_doc.split("\n", 1)[1])
    spec = deployment["spec"]
    assert spec["replicas"] == 2
    assert spec["strategy"]["rollingUpdate"] == {"maxUnavailable": 0, "maxSurge": 1}

    pod = spec["template"]["spec"]
    container = next(item for item in pod["containers"] if item["name"] == "mcp-server")
    command = container["command"]
    expected_command_parts = (
        "--http-cors-allowed-origins=https://mcp.example.com",
        "--grpc-authn-trusted-token-issuers=https://keycloak.example.com/realms/osac",
        "--oauth-authorization-server=https://keycloak.example.com/realms/osac",
        "--oauth-resource-url=https://mcp.example.com/",
        "--oauth-token-endpoint=https://keycloak.example.com/realms/osac/protocol/openid-connect/token",
        "--oauth-exchange-client-id=https://mcp.example.com/",
        "--oauth-api-audience=osac-api",
        "--oauth-exchange-client-secret-file=/etc/fulfillment-mcp-server/oauth/client-secret",
        "--ca-file=/etc/fulfillment-mcp-server/tls/cas",
        "--mcp-request-body-limit=1Mi",
        "--mcp-concurrency-limit=100",
        "--mcp-request-timeout=30s",
    )
    for part in expected_command_parts:
        assert part in command, part

    volumes = {volume["name"]: volume for volume in pod["volumes"]}
    assert volumes["cas"]["configMap"]["name"] == "ca-bundle"
    assert volumes["exchange-credentials"]["secret"] == {
        "secretName": "ci-mcp-exchange-credentials",
        "items": [{"key": "client-secret", "path": "client-secret"}],
    }
    mounts = {mount["name"]: mount for mount in container["volumeMounts"]}
    assert mounts["cas"]["mountPath"] == "/etc/fulfillment-mcp-server/tls/cas"
    assert mounts["exchange-credentials"]["readOnly"] is True
    assert container["resources"] == {
        "requests": {"cpu": "50m", "memory": "128Mi"},
        "limits": {"cpu": "500m", "memory": "512Mi"},
    }

    service_doc = document(docs, SERVICE_SOURCE, "MCP Service")
    service = yaml.safe_load(service_doc.split("\n", 1)[1])
    assert "clusterIP" not in service["spec"]

    route_doc = document(docs, ROUTE_SOURCE, "MCP Route")
    route = yaml.safe_load(route_doc.split("\n", 1)[1])
    assert route["spec"]["host"] == "mcp.example.com"

    document(docs, CERTIFICATE_SOURCE, "MCP Certificate")
    budget_doc = document(docs, BUDGET_SOURCE, "MCP PodDisruptionBudget")
    budget = yaml.safe_load(budget_doc.split("\n", 1)[1])
    assert budget["spec"]["minAvailable"] == 1

    runtime_doc = document(docs, RUNTIME_SOURCE, "MCP runtime ConfigMap")
    runtime = yaml.safe_load(runtime_doc.split("\n", 1)[1])
    assert runtime["data"] == {
        "OSAC_MCP_PUBLIC_URL": "https://mcp.example.com/",
        "OSAC_MCP_ISSUER_URL": "https://keycloak.example.com/realms/osac",
        "OSAC_MCP_AUDIENCE": "osac-api",
        "OSAC_MCP_TRUST_MODE": "configMap",
        "OSAC_MCP_CA_CONFIG_MAP": "ca-bundle",
        "OSAC_MCP_CHECK_CONNECTION_TOOL": "check_connection",
    }


def check_dev_overlay() -> None:
    command = [
        "helm",
        "template",
        "osac",
        str(CHART),
        "--values",
        str(CHART / "ci/default-values.yaml"),
        "--values",
        str(INSTALLER / "values/dev/kind-instance.yaml"),
        "--values",
        str(INSTALLER / "values/dev/mcp.yaml"),
        "--namespace",
        "osac",
        "--set-string",
        "global.clusterDomain=example.com",
    ]
    result = subprocess.run(command, check=True, capture_output=True, text=True, timeout=300)
    docs = [doc for doc in result.stdout.split("\n---\n") if doc.strip()]
    deployment_doc = document(docs, DEPLOYMENT_SOURCE, "Kind MCP Deployment")
    assert "--oauth-resource-url=https://mcp.osac.localhost:8443/" in deployment_doc
    route_doc = document(docs, ROUTE_SOURCE, "Kind MCP TLSRoute")
    assert "kind: TLSRoute" in route_doc
    assert "- mcp.osac.localhost" in route_doc


def check_rejected(values: list[str], reason: str) -> None:
    result = render_status(values)
    assert result.returncode != 0, values
    dotted_reason = reason.replace("/", ".")
    assert reason in result.stderr or dotted_reason in result.stderr, f"{values}: {result.stderr}"


def check_schema() -> None:
    check_rejected(
        ["--set-string", "global.mcp.publicURL=https://mcp.example.com"],
        "global/mcp/publicURL",
    )
    check_rejected(
        ["--set", "global.mcp.trust.mode=secret"],
        "global/mcp/trust/mode",
    )
    check_rejected(
        ["--set", "global.mcp.exchangeClientSecret.name="],
        "global/mcp/exchangeClientSecret/name",
    )
    check_rejected(
        ["--set", "service.mcp.enabled=true"],
        "additional properties 'mcp' not allowed",
    )
    check_rejected(
        ["--set", "global.mcp.limits.requestSize=not-a-quantity"],
        "global/mcp/limits/requestSize",
    )
    check_rejected(
        ["--set", "global.mcp.limits.concurrency=0"],
        "global/mcp/limits/concurrency",
    )
    check_rejected(
        ["--set-string", "global.mcp.limits.timeout=soon"],
        "global/mcp/limits/timeout",
    )
    check_rejected(
        ["--set-string", "global.mcp.limits.requestSize=64ki"],
        "global/mcp/limits/requestSize",
    )


def check_runtime_limit_override() -> None:
    docs = render(
        [
            "--set-string",
            "global.mcp.limits.requestSize=64Ki",
            "--set",
            "global.mcp.limits.concurrency=7",
            "--set-string",
            "global.mcp.limits.timeout=2s",
        ]
    )
    deployment_doc = document(docs, DEPLOYMENT_SOURCE, "MCP Deployment")
    for part in (
        "--mcp-request-body-limit=64Ki",
        "--mcp-concurrency-limit=7",
        "--mcp-request-timeout=2s",
    ):
        assert part in deployment_doc, part


if __name__ == "__main__":
    check_disabled()
    check_infra_disabled()
    check_enabled()
    check_infra_enabled()
    check_dev_overlay()
    check_runtime_limit_override()
    check_schema()
    print("MCP installer render checks passed.")
