#!/usr/bin/env python3
"""Check the event publisher's and metering's topic wiring in every profile."""

from __future__ import annotations

import subprocess

from _render import PROFILES, render, select_one, template_command


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


# Both processes parse their retention value as an integer and refuse zero and
# anything below -1 at startup, so the schema must reject such values before an
# installation exists that cannot start. The schema is identical in every
# profile; one is enough to exercise the validation.
SCHEMA_PROFILE = PROFILES[0]
RETENTION_KEYS = (
    "service.eventPublisher.topicRetentionMs",
    "metering.topics.retentionMs",
)


def render_status(extra: list[str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        template_command(SCHEMA_PROFILE) + extra,
        capture_output=True,
        text=True,
        timeout=300,
    )


def check_retention_values() -> None:
    for key in RETENTION_KEYS:
        for value in ("0", "-2", "not-a-number"):
            result = render_status(["--set-string", f"{key}={value}"])
            assert result.returncode != 0, f"{key}={value} passed schema validation"
            assert "does not match pattern" in result.stderr, (
                f"{key}={value} failed for another reason: {result.stderr}"
            )
        for value in ("-1", "604800000"):
            result = render_status(["--set-string", f"{key}={value}"])
            assert result.returncode == 0, (
                f"{key}={value} failed schema validation: {result.stderr}"
            )


if __name__ == "__main__":
    for profile in PROFILES:
        check_profile(profile)
    check_retention_values()
    print("Event publisher and metering topic render checks passed.")
