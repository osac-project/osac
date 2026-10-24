#!/usr/bin/env python3
"""Check metering event-log wiring in every metering profile."""

from __future__ import annotations

from _render import PROFILES, render, select_one


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

    # Billing history survives a release uninstall: the ConfigMap, the policy,
    # and the binding are all kept. Removing them is always manual.
    keep = '"helm.sh/resource-policy": keep'
    assert keep in rules, f"{profile}: rules ConfigMap must be kept on uninstall"
    assert keep in policy, f"{profile}: rules policy must be kept on uninstall"
    assert keep in binding, f"{profile}: rules binding must be kept on uninstall"


if __name__ == "__main__":
    for profile in PROFILES:
        check_profile(profile)
    print("Metering event-log profile render checks passed.")
