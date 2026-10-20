#!/usr/bin/env python3
"""Check metering event-log wiring in every metering profile."""

from __future__ import annotations

import pathlib
import subprocess

INSTALLER = pathlib.Path(__file__).resolve().parents[1]
CHART = INSTALLER / "charts/osac"
PROFILES = ("vmaas-ci", "caas-ci", "full-ci", "cudn-evpn-netris-test")


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
    rules = select_one(
        docs,
        lambda doc: "kind: ConfigMap\n" in doc
        and "  name: osac-metering-rules-0001\n" in doc,
        f"metering rules ConfigMap for {profile}",
    )
    policy = select_one(
        docs,
        lambda doc: "kind: ValidatingAdmissionPolicy\n" in doc,
        f"rules admission policy for {profile}",
    )
    binding = select_one(
        docs,
        lambda doc: "kind: ValidatingAdmissionPolicyBinding\n" in doc,
        f"rules admission policy binding for {profile}",
    )

    # Metering reads events from Kafka and folds them; it no longer talks to
    # fulfillment, so none of its credentials appear.
    assert "METERING_DEPLOYMENT_ID" in metering, profile
    assert "HEARTBEAT_INTERVAL" in metering, profile
    assert "KAFKA_BROKERS" in metering, profile
    assert "FULFILLMENT_" not in metering, profile
    assert "fulfillment-credentials" not in metering, profile

    # The default meters ship as revision 0001 and are protected.
    assert 'effective_at: "1970-01-01T00:00:00Z"' in rules, profile
    assert "meters.yaml:" in rules, profile
    assert "resource: ComputeInstance" in rules, profile
    assert "immutable: true" in rules, profile
    assert "osac-metering-rules-" in policy, profile
    assert "impossible" not in policy, profile
    assert "validationActions:" in binding, profile
    assert 'validationActions: ["Deny"]' in binding, profile


if __name__ == "__main__":
    for profile in PROFILES:
        check_profile(profile)
    print("Metering event-log profile render checks passed.")
