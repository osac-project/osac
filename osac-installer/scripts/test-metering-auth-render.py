#!/usr/bin/env python3
"""Check Keycloak service-account auth wiring in every metering profile."""

from __future__ import annotations

import pathlib
import subprocess

INSTALLER = pathlib.Path(__file__).resolve().parents[1]
CHART = INSTALLER / "charts/osac"
PROFILES = ("vmaas-ci", "caas-ci", "full-ci", "cudn-evpn-netris-test")
EXPECTED_ISSUER = "https://keycloak-keycloak.example.com/realms/osac"


def render(profile: str) -> list[str]:
    cmd = [
        "helm",
        "template",
        "osac",
        str(CHART),
        "--values",
        str(CHART / "ci/default-values.yaml"),
        "--values",
        str(INSTALLER / "values" / profile / "instance.yaml"),
        "--set-string",
        "global.clusterDomain=example.com",
        "--set",
        "service.externalHostname=fulfillment.example.com",
        "--set",
        "service.internalHostname=fulfillment-internal.example.com",
    ]
    try:
        manifest = subprocess.run(
            cmd, check=True, capture_output=True, text=True, timeout=300
        ).stdout
    except subprocess.TimeoutExpired as exc:
        raise SystemExit(
            f"helm template timed out after {exc.timeout} seconds for profile {profile}"
        ) from exc
    return [doc for doc in manifest.split("\n---\n") if doc.strip()]


def select_one(docs: list[str], predicate, description: str) -> str:
    matches = [doc for doc in docs if predicate(doc)]
    if len(matches) != 1:
        raise AssertionError(f"expected one {description}, found {len(matches)}")
    return matches[0]


def check_profile(profile: str) -> None:
    docs = render(profile)
    metering = select_one(
        docs,
        lambda doc: "# Source: osac/charts/metering/templates/deployment.yaml\n" in doc,
        f"metering Deployment for {profile}",
    )
    issuer_config = select_one(
        docs,
        lambda doc: "kind: ConfigMap\n" in doc
        and "  name: osac-metering-fulfillment-config\n" in doc,
        f"metering issuer ConfigMap for {profile}",
    )

    assert "FULFILLMENT_ISSUER_URL" in metering, profile
    assert "name: osac-metering-fulfillment-config" in metering, profile
    assert "key: FULFILLMENT_ISSUER_URL" in metering, profile
    assert f'FULFILLMENT_ISSUER_URL: "{EXPECTED_ISSUER}"' in issuer_config, profile

    assert "secretName: fulfillment-controller-credentials" in metering, profile
    assert "key: client-id" in metering, profile
    assert "key: client-secret" in metering, profile
    assert "mountPath: /etc/fulfillment-credentials" in metering, profile
    assert "FULFILLMENT_CLIENT_ID_FILE" in metering, profile
    assert "FULFILLMENT_CLIENT_SECRET_FILE" in metering, profile

    assert "FULFILLMENT_TOKEN_FILE" not in metering, profile
    assert "/var/run/secrets/kubernetes.io/serviceaccount/token" not in metering, profile


if __name__ == "__main__":
    for profile in PROFILES:
        check_profile(profile)
    print("Metering Keycloak authentication profile render checks passed.")
