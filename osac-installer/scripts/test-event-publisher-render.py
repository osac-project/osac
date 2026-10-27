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
    # The retention reaches the flag as a Go duration. Quote characters in a
    # container argument become part of the flag value and the duration parser
    # rejects them, so the argument must render bare.
    assert "--kafka-topic-retention=-1s" in publisher, profile
    assert '--kafka-topic-retention="' not in publisher, profile

    metering = select_one(
        docs,
        lambda doc: "# Source: osac/charts/metering/templates/deployment.yaml\n" in doc,
        f"metering Deployment for {profile}",
    )
    # Metering creates each tenant's heartbeat and dead-letter topics when it first
    # serves the tenant, so the retention knob must reach it.
    assert "METERING_TOPIC_RETENTION_MS" in metering, profile


# The publisher parses its retention as a Go duration and refuses a negative
# one at startup, and metering parses its retention as an integer and refuses
# zero and anything below -1, so the schema must reject such values before an
# installation exists that cannot start. The schema is identical in every
# profile; one is enough to exercise the validation.
SCHEMA_PROFILE = PROFILES[0]
RETENTION_KEYS = (
    "service.eventPublisher.topicRetention",
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
    # The publisher takes a Go duration, negative meaning indefinite; metering
# takes milliseconds.
    invalid = {
        "service.eventPublisher.topicRetention": ("not-a-duration", "168"),
        "metering.topics.retentionMs": ("0", "-2", "not-a-number"),
    }
    valid = {
        "service.eventPublisher.topicRetention": ("-1s", "0", "168h", "45m30s"),
        "metering.topics.retentionMs": ("-1", "604800000"),
    }
    for key, values in invalid.items():
        for value in values:
            result = render_status(["--set-string", f"{key}={value}"])
            assert result.returncode != 0, f"{key}={value} passed schema validation"
            assert "does not match pattern" in result.stderr, (
                f"{key}={value} failed for another reason: {result.stderr}"
            )
    for key, values in valid.items():
        for value in values:
            result = render_status(["--set-string", f"{key}={value}"])
            assert result.returncode == 0, (
                f"{key}={value} failed schema validation: {result.stderr}"
            )


if __name__ == "__main__":
    for profile in PROFILES:
        check_profile(profile)
    check_retention_values()
    print("Event publisher and metering topic render checks passed.")
