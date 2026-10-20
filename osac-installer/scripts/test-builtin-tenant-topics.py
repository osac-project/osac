#!/usr/bin/env python3
"""Check the built-in tenants' Kafka topics render.

The built-in tenants exist from the database migration that seeds them, which
runs before the local hub is registered, so neither the server's synchronous
creation at tenant creation nor the onboarding reconciler can create their
topics in time. The chart pre-creates them: system and shared each get their
event, heartbeat and dead-letter topic on the Kafka cluster the installer
deploys. With an external Kafka (service.kafkaTopics.hub empty) nothing is
rendered and the operator owns topic creation.
"""

from __future__ import annotations

import pathlib
import subprocess

INSTALLER = pathlib.Path(__file__).resolve().parents[1]
CHART = INSTALLER / "charts/osac"

BUILTIN_TENANTS = ("system", "shared")
TOPIC_PREFIXES = ("osac.events.", "osac.metering.heartbeat.", "osac.metering.dlq.")
EXPECTED_NAMES = {f"{prefix}{tenant}" for tenant in BUILTIN_TENANTS for prefix in TOPIC_PREFIXES}


def render(*extra: str) -> list[str]:
    cmd = [
        "helm",
        "template",
        "osac",
        str(CHART),
        "--values",
        str(CHART / "ci/default-values.yaml"),
        *extra,
    ]
    try:
        manifest = subprocess.run(cmd, check=True, capture_output=True, text=True, timeout=300).stdout
    except subprocess.TimeoutExpired as exc:
        raise SystemExit(f"helm template timed out after {exc.timeout} seconds") from exc
    return [doc for doc in manifest.split("\n---\n") if doc.strip()]


def builtin_topics(docs: list[str]) -> dict[str, str]:
    matches = [
        doc
        for doc in docs
        if "# Source: osac/templates/kafka/builtin-tenants-kafka-topics.yaml\n" in doc
        and "kind: KafkaTopic\n" in doc
    ]
    topics = {}
    for doc in matches:
        name = next(line.removeprefix("  name: ") for line in doc.splitlines() if line.startswith("  name: "))
        topics[name] = doc
    return topics


def main() -> None:
    topics = builtin_topics(render())
    assert set(topics) == EXPECTED_NAMES, (
        f"expected the built-in tenants' topics {sorted(EXPECTED_NAMES)}, found {sorted(topics)}"
    )
    for name, doc in topics.items():
        assert "namespace: osac-kafka" in doc, f"{name}: wrong namespace"
        assert "strimzi.io/cluster: osac-kafka" in doc, f"{name}: missing cluster label"
        assert "helm.sh/resource-policy: keep" in doc, (
            f"{name}: missing the keep annotation; an upgrade that stops rendering the "
            "topic would let Helm prune it and the Topic Operator delete its records"
        )
        assert 'retention.ms: "-1"' in doc, f"{name}: wrong retention"
        assert 'retention.bytes: "-1"' in doc, f"{name}: wrong size limit"
        assert "cleanup.policy: delete" in doc, f"{name}: wrong cleanup policy"
        assert "partitions: 1" in doc, f"{name}: wrong partition count"

    external = render("--set", "service.kafkaTopics.hub=")
    assert not builtin_topics(external), (
        "the built-in tenants' topics must not render when service.kafkaTopics.hub is empty: "
        "with an external Kafka the operator creates the topics"
    )

    # A retention below -1 is invalid: the controller rejects it at startup and crash-loops, so the
    # schema must reject it at install time instead. Zero is invalid the other way round: it is
    # not indefinite but evicts records as soon as they land.
    for bad_retention in ("-2", "-1000", "0"):
        cmd = [
            "helm",
            "template",
            "osac",
            str(CHART),
            "--values",
            str(CHART / "ci/default-values.yaml"),
            "--set",
            f"service.kafkaTopics.retentionMs={bad_retention}",
        ]
        result = subprocess.run(cmd, capture_output=True, text=True, timeout=300)
        assert result.returncode != 0, (
            f"service.kafkaTopics.retentionMs={bad_retention} must fail the render: "
            "the controller rejects any retention below -1 and zero, which evicts "
            "records as soon as they land"
        )

    # Tenant topics are created through the hub-access identity on the in-cluster Kafka, so enabling
    # them while either is disabled must fail the render instead of failing every tenant reconcile.
    for invalid in (
        ["--set", "hubAccess.enabled=false"],
        ["--set", "kafka.enabled=false"],
    ):
        result = subprocess.run(
            [
                "helm",
                "template",
                "osac",
                str(CHART),
                "--values",
                str(CHART / "ci/default-values.yaml"),
                *invalid,
            ],
            capture_output=True,
            text=True,
            timeout=300,
        )
        assert result.returncode != 0, (
            f"enabling tenant topics with {invalid[-1]} must fail the render: "
            "the reconciler could neither reach the Kafka nor use the hub-access identity"
        )

    print("Built-in tenants Kafka topics render checks passed.")


if __name__ == "__main__":
    main()
