#!/usr/bin/env python3
"""Check the billing provider configuration renders and validates."""

from __future__ import annotations

import subprocess

import yaml

from _render import INSTALLER, PROFILES, render, select_one, template_command

SERVICE_CHART = INSTALLER.parent / "fulfillment-service/charts/service"
GRPC_SOURCE = "# Source: osac/charts/service/templates/grpc-server/deployment.yaml\n"
SCHEMA_PROFILE = PROFILES[0]
PROVIDER = {
    "key": "m360",
    "baseUrl": "https://billing.example.com/api",
    "apiVersion": "v1",
    "credentialSecret.name": "billing-token",
    "credentialSecret.key": "api-token",
}


def provider_args(override: dict[str, str | None] | None = None) -> list[str]:
    """--set-string args for the provider values; None overrides drop a key."""
    values = {**PROVIDER, **(override or {})}
    return [
        arg
        for k, v in values.items()
        if v is not None
        for arg in ("--set-string", f"global.billing.provider.{k}={v}")
    ]


def helm(extra: list[str]) -> subprocess.CompletedProcess[str]:
    """Run helm template with extra args, without check."""
    return subprocess.run(
        template_command(SCHEMA_PROFILE) + extra, capture_output=True, text=True, timeout=300
    )


def grpc_server(extra: list[str]) -> str:
    """Render and return the fulfillment gRPC server Deployment document."""
    result = helm(extra)
    assert result.returncode == 0, result.stderr
    docs = [d for d in result.stdout.split("\n---\n") if d.strip()]
    return select_one(docs, lambda d: GRPC_SOURCE in d, "fulfillment gRPC server Deployment")


def check_disabled() -> None:
    """Without provider values no billing environment renders in any profile."""
    for profile in PROFILES:
        doc = select_one(
            render(profile), lambda d: GRPC_SOURCE in d, f"gRPC server Deployment for {profile}"
        )
        assert "OSAC_BILLING" not in doc and "billing" not in doc, profile


def check_configured() -> None:
    """A fully configured provider renders its env vars and Secret mount."""
    doc = grpc_server(provider_args())
    assert "secretName: billing-token" in doc
    assert "path: credential" in doc and "key: api-token" in doc
    for want in (
        'OSAC_BILLING_PROVIDER\n          value: "m360"',
        'OSAC_BILLING_BASE_URL\n          value: "https://billing.example.com/api"',
        'OSAC_BILLING_API_VERSION\n          value: "v1"',
        "OSAC_BILLING_CREDENTIAL_FILE",
        'OSAC_BILLING_RETRY_COUNT\n          value: "3"',
        'OSAC_BILLING_CACHE_FRESH_TTL_SECONDS\n          value: "300"',
        'OSAC_BILLING_PRICING_BREAKER_FAILURE_THRESHOLD\n          value: "5"',
    ):
        assert want in doc, want
    check_grpc_env(doc)


def check_grpc_env(doc: str) -> None:
    """The gRPC server container has exactly one env key holding a valid list."""
    deployment = yaml.safe_load(doc.split("\n", 1)[1])
    containers = deployment["spec"]["template"]["spec"]["containers"]
    (container,) = [c for c in containers if c["name"] == "grpc-server"]
    assert sum(1 for line in doc.splitlines() if line == "        env:") == 1, "duplicate env key"
    env = container["env"]
    assert isinstance(env, list) and env, env
    for item in env:
        assert set(item) == {"name", "value"} and item["value"] != "", item


def check_standalone_subchart() -> None:
    """Provider set without any tuning: the subchart falls back to its own defaults."""
    result = subprocess.run(
        ["helm", "template", "t", str(SERVICE_CHART)]
        + ["-f", str(SERVICE_CHART / "ci-values.yaml")]
        + provider_args(),
        capture_output=True,
        text=True,
        timeout=300,
    )
    assert result.returncode == 0, result.stderr
    docs = [d for d in result.stdout.split("\n---\n") if "OSAC_BILLING_PROVIDER" in d]
    assert len(docs) == 1, len(docs)
    check_grpc_env(docs[0])
    assert 'OSAC_BILLING_RETRY_COUNT\n          value: "3"' in docs[0]
    assert 'OSAC_BILLING_ITEM_TIMEOUT_SECONDS\n          value: "2"' in docs[0]


def check_rejected(extra: list[str], why: str, reason: str) -> None:
    """Assert helm fails with reason in stderr for these extra args."""
    result = helm(extra)
    assert result.returncode != 0, f"{why} passed validation"
    assert reason in result.stderr, f"{why}: expected {reason!r} in stderr:\n{result.stderr}"


def check_invalid() -> None:
    """Invalid provider and tuning values are rejected by the schema."""
    for drop in PROVIDER:
        leaf = drop.split(".")[-1]
        check_rejected(
            provider_args({drop: None}), f"provider without {drop}", f"missing property '{leaf}'"
        )
    check_rejected(provider_args({"key": "other"}), "unknown provider key", "m360")
    check_rejected(provider_args({"baseUrl": "ftp://x"}), "non-http scheme", "baseUrl")
    extra = ["--set-string", "global.billing.provider.token=s"]
    check_rejected(provider_args() + extra, "inline credential", "token")
    extra = ["--set-string", "global.billing.providers.second.key=m360"]
    check_rejected(provider_args() + extra, "second provider", "providers")
    t = "global.billing.tuning"
    for bad in (
        "itemTimeoutSeconds=0",
        "listDeadlineSeconds=-1",
        "cacheFreshTtlSeconds=-5",
        "cacheMaxEntries=0",
        "retryCount=4",
        "retryCount=-1",
        "pricingBreaker.failureThreshold=0",
        "pricingBreaker.openSeconds=0",
    ):
        check_rejected(["--set", f"{t}.{bad}"], bad, bad.split("=")[0].split(".")[-1])
    check_rejected(
        ["--set", f"{t}.cacheFreshTtlSeconds=600", "--set", f"{t}.cacheStaleTtlSeconds=60"],
        "stale TTL shorter than fresh TTL",
        "cacheStaleTtlSeconds must not be shorter than cacheFreshTtlSeconds",
    )
    check_rejected(
        ["--set", f"{t}.itemTimeoutSeconds=30", "--set", f"{t}.listDeadlineSeconds=5"],
        "list deadline shorter than item timeout",
        "listDeadlineSeconds must not be shorter than itemTimeoutSeconds",
    )
    for ok in (0, 3):
        result = helm(["--set", f"{t}.retryCount={ok}"])
        assert result.returncode == 0, result.stderr


if __name__ == "__main__":
    check_disabled()
    check_configured()
    check_standalone_subchart()
    check_invalid()
    print("Billing provider render checks passed.")
