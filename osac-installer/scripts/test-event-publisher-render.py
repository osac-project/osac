#!/usr/bin/env python3
"""Check the event publisher's and metering's topic wiring in every profile."""

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
    publisher = select_one(
        docs,
        lambda doc: "# Source: osac/charts/service/templates/event-publisher/deployment.yaml\n" in doc,
        f"event publisher Deployment for {profile}",
    )
    # The retention reaches the broker as a Kafka config value. Quote characters in a
    # container argument become part of the flag value, and the broker rejects a value
    # it cannot parse as a number, so the argument must render bare.
    assert "--kafka-topic-retention-ms=-1" in publisher, profile
    assert '--kafka-topic-retention-ms="' not in publisher, profile

    metering = select_one(
        docs,
        lambda doc: "# Source: osac/charts/metering/templates/deployment.yaml\n" in doc,
        f"metering Deployment for {profile}",
    )
    # Metering creates each tenant's heartbeat and dead-letter topics when it first
    # serves the tenant, so the retention knob must reach it.
    assert "METERING_TOPIC_RETENTION_MS" in metering, profile


if __name__ == "__main__":
    for profile in PROFILES:
        check_profile(profile)
    print("Event publisher and metering topic render checks passed.")
