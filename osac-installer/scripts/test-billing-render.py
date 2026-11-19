#!/usr/bin/env python3
"""Check the billing provider configuration renders and validates."""

from __future__ import annotations

import pathlib
import secrets
import subprocess
import tempfile
from urllib.parse import urlsplit, urlunsplit

import yaml

from _render import INSTALLER, PROFILES, render, select_one, template_command

SERVICE_CHART = INSTALLER.parent / "fulfillment-service/charts/service"
GRPC_SOURCE = "# Source: osac/charts/service/templates/grpc-server/deployment.yaml\n"
SCHEMA_PROFILE = PROFILES[0]
PROVIDER = {
    "key": "m360",
    "baseUrl": "https://billing.example.com/api/v1/external",
    "credentialSecret.name": "billing-token",
    "credentialSecret.key": "api-token",
}
CA_SECRET = {"caSecret.name": "billing-ca-bundle", "caSecret.key": "ca-bundle"}
CA_MOUNT_PATH = "/etc/fulfillment-grpc-server/billing-ca"
# --set-string consumes one escape level, so this literal's two runtime
# backslashes reach the schema as the single backslash the rejection cases need.
BACKSLASH_IN_HOST_URL = r"https://billing\\example.com"


def base_url_with_credentials() -> str:
    """Build a credential-bearing URL at runtime; no such literal is stored."""
    base = urlsplit(PROVIDER["baseUrl"])
    userinfo = f"{secrets.token_hex(4)}:{secrets.token_hex(8)}"
    return urlunsplit((base.scheme, f"{userinfo}@{base.netloc}", base.path, "", ""))


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
        'OSAC_BILLING_BASE_URL\n          value: "https://billing.example.com/api/v1/external"',
        "OSAC_BILLING_CREDENTIAL_FILE",
        'OSAC_BILLING_RETRY_COUNT\n          value: "3"',
        'OSAC_BILLING_PRICING_BREAKER_FAILURE_THRESHOLD\n          value: "5"',
    ):
        assert want in doc, want
    check_grpc_env(doc)


def grpc_pod(doc: str) -> tuple[dict, dict]:
    """Return the pod spec and the grpc-server container of a rendered Deployment."""
    spec = yaml.safe_load(doc.split("\n", 1)[1])["spec"]["template"]["spec"]
    (container,) = [c for c in spec["containers"] if c["name"] == "grpc-server"]
    return spec, container


def check_grpc_env(doc: str) -> None:
    """The gRPC server container has exactly one env key holding a valid list."""
    _, container = grpc_pod(doc)
    assert sum(1 for line in doc.splitlines() if line == "        env:") == 1, "duplicate env key"
    env = container["env"]
    assert isinstance(env, list) and env, env
    for item in env:
        assert set(item) == {"name", "value"} and item["value"] != "", item


def check_ca_secret() -> None:
    """caSecret adds a CA volume, a read-only mount and the env var; unset renders none."""
    doc = grpc_server(provider_args(CA_SECRET))
    check_grpc_env(doc)
    spec, container = grpc_pod(doc)
    (volume,) = [v for v in spec["volumes"] if v["name"] == "billing-ca"]
    assert volume["secret"] == {
        "secretName": "billing-ca-bundle",
        "items": [{"key": "ca-bundle", "path": "ca.crt"}],
    }, volume
    (mount,) = [m for m in container["volumeMounts"] if m["name"] == "billing-ca"]
    assert mount == {"name": "billing-ca", "mountPath": CA_MOUNT_PATH, "readOnly": True}, mount
    env = {item["name"]: item["value"] for item in container["env"]}
    assert env["OSAC_BILLING_CA_FILE"] == f"{CA_MOUNT_PATH}/ca.crt", env
    assert "secretName: billing-token" in doc, "credential mount lost"

    doc = grpc_server(provider_args())
    assert "billing-ca" not in doc and "OSAC_BILLING_CA_FILE" not in doc, "CA rendered unset"


def standalone_result(extra: list[str]) -> subprocess.CompletedProcess[str]:
    """Render the service subchart alone with the provider set, without check."""
    return subprocess.run(
        ["helm", "template", "t", str(SERVICE_CHART)]
        + ["-f", str(SERVICE_CHART / "ci-values.yaml")]
        + provider_args()
        + extra,
        capture_output=True,
        text=True,
        timeout=300,
    )


def standalone(extra: list[str]) -> str:
    """Render the service subchart alone with the provider set and return the manifest."""
    result = standalone_result(extra)
    assert result.returncode == 0, result.stderr
    return result.stdout


def check_standalone_subchart() -> None:
    """Provider set without any tuning: the subchart falls back to its own defaults."""
    docs = [d for d in standalone([]).split("\n---\n") if "OSAC_BILLING_PROVIDER" in d]
    assert len(docs) == 1, len(docs)
    check_grpc_env(docs[0])
    assert 'OSAC_BILLING_RETRY_COUNT\n          value: "3"' in docs[0]
    breaker = "global.billing.tuning.pricingBreaker"
    leaves = {
        "failureThreshold": ("FAILURE_THRESHOLD", 5),
        "failureWindowSeconds": ("FAILURE_WINDOW_SECONDS", 30),
        "openSeconds": ("OPEN_SECONDS", 30),
    }
    for env, default in leaves.values():
        assert f'OSAC_BILLING_PRICING_BREAKER_{env}\n          value: "{default}"' in docs[0], env
    # A zero is valid for retryCount, but not for any breaker field.
    manifest = standalone(["--set", "global.billing.tuning.retryCount=0"])
    assert 'OSAC_BILLING_RETRY_COUNT\n          value: "0"' in manifest
    for key, (env, _) in leaves.items():
        result = standalone_result(["--set", f"{breaker}.{key}=0"])
        assert result.returncode != 0, f"standalone {key}=0 passed validation"
        assert key in result.stderr, result.stderr

    # The standalone schema enforces the loader's duration limits.
    manifest = standalone([
        "--set", f"{breaker}.failureWindowSeconds=9223372036",
        "--set", f"{breaker}.openSeconds=9223372036",
    ])
    for env, maximum in (
        ("PRICING_BREAKER_FAILURE_WINDOW_SECONDS", 9223372036),
        ("PRICING_BREAKER_OPEN_SECONDS", 9223372036),
    ):
        assert f'OSAC_BILLING_{env}\n          value: "{maximum}"' in manifest, env

    for key, maximum in (
        ("pricingBreaker.failureWindowSeconds", 9223372037),
        ("pricingBreaker.openSeconds", 9223372037),
    ):
        result = standalone_result(["--set", f"global.billing.tuning.{key}={maximum}"])
        assert result.returncode != 0, f"standalone {key}={maximum} passed validation"
        assert key.split(".")[-1] in result.stderr, result.stderr
    # A values-file integer arrives as a float64, which renders as 1e+06 and fails Atoi.
    tuning = {"retryCount": 1000000, "pricingBreaker": dict.fromkeys(leaves, 1000000)}
    with tempfile.TemporaryDirectory(prefix="billing-values-") as temp_dir:
        values = pathlib.Path(temp_dir) / "tuning.yaml"
        values.write_text(yaml.safe_dump({"global": {"billing": {"tuning": tuning}}}), encoding="utf-8")
        manifest = standalone(["--values", str(values)])
    for env in ["RETRY_COUNT"] + [f"PRICING_BREAKER_{env}" for env, _ in leaves.values()]:
        assert f'OSAC_BILLING_{env}\n          value: "1000000"' in manifest, env


def check_rejected(extra: list[str], why: str, reason: str) -> None:
    """Assert helm fails with reason in stderr for these extra args."""
    result = helm(extra)
    assert result.returncode != 0, f"{why} passed validation"
    assert reason in result.stderr, f"{why}: expected {reason!r} in stderr:\n{result.stderr}"


def check_base_url_forms() -> None:
    """The schema accepts loopback http, numeric https ports, IPv6 hosts and well-formed escapes."""
    for ok in (
        "http://localhost:8080",
        "http://127.0.0.1:8080",
        "http://[::1]:8080",
        "https://billing.example.com:8443",
        "https://[2001:db8::1]:8443",
        "https://billing.example.com/api/v1/external",
        "https://billing.example.com/api/v1/external/",
        "https://billing.example.com/api%20v1",
        "http://localhost:8080/api/v1/external",
    ):
        result = helm(provider_args({"baseUrl": ok}))
        assert result.returncode == 0, f"{ok} rejected by schema:\n{result.stderr}"


def check_invalid() -> None:
    """Invalid provider and tuning values are rejected by the schema."""
    for drop in PROVIDER:
        leaf = drop.split(".")[-1]
        check_rejected(
            provider_args({drop: None}), f"provider without {drop}", f"missing property '{leaf}'"
        )
    check_rejected(provider_args({"key": "other"}), "unknown provider key", "m360")
    check_rejected(provider_args({"baseUrl": "ftp://x"}), "non-http scheme", "baseUrl")
    check_rejected(
        provider_args({"baseUrl": "http://billing.example.com"}),
        "http on a non-loopback host",
        "baseUrl",
    )
    check_rejected(
        provider_args({"baseUrl": base_url_with_credentials()}),
        "URL credentials",
        "baseUrl",
    )
    check_rejected(
        provider_args({"baseUrl": "https://billing.example.com:notaport"}),
        "nonnumeric https port",
        "baseUrl",
    )
    check_rejected(
        provider_args({"baseUrl": "https://billing.example.com?x=1"}),
        "query in the base URL",
        "baseUrl",
    )
    check_rejected(
        provider_args({"baseUrl": "https://billing.example.com/api%zz"}),
        "malformed percent escape in the base URL",
        "baseUrl",
    )
    for bad_url, reason in (
        ("https://%62illing.example.com", "percent-escaped host"),
        ("https://%25billing.example.com", "escaped percent in host"),
        ("https://billing[example.com", "unclosed bracket in host"),
        (BACKSLASH_IN_HOST_URL, "backslash in host"),
        ("https://billing|example.com", "pipe in host"),
        ("https://[::::]", "invalid IPv6 host"),
        ("https://billing.example.com/api\x01", "control character in path"),
    ):
        check_rejected(provider_args({"baseUrl": bad_url}), reason, "baseUrl")
    extra = ["--set-string", f"global.billing.provider.token={secrets.token_hex(8)}"]
    check_rejected(provider_args() + extra, "inline credential", "token")
    extra = ["--set-string", "global.billing.providers.second.key=m360"]
    check_rejected(provider_args() + extra, "second provider", "providers")
    t = "global.billing.tuning"
    for bad in (
        "retryCount=-1",
        "pricingBreaker.failureThreshold=0",
        "pricingBreaker.failureWindowSeconds=0",
        "pricingBreaker.openSeconds=0",
    ):
        check_rejected(["--set", f"{t}.{bad}"], bad, bad.split("=")[0].split(".")[-1])
    for missing, present in (("name", "key"), ("key", "name")):
        only = f"caSecret.{present}"
        check_rejected(
            provider_args({only: CA_SECRET[only]}),
            f"caSecret without {missing}",
            f"missing property '{missing}'",
        )
    for ok in (0, 3, 10):
        result = helm(["--set", f"{t}.retryCount={ok}"])
        assert result.returncode == 0, result.stderr


def check_standalone_base_url_schema() -> None:
    """The standalone chart's URL schema agrees with runtime validation."""
    for ok in (
        "https://billing.example.com/api%20v1",
        "http://localhost:8080/api/v1/external",
        "https://[2001:db8::1]:8443",
    ):
        result = standalone_result(provider_args({"baseUrl": ok}))
        assert result.returncode == 0, f"standalone {ok} rejected:\n{result.stderr}"
    for bad in (
        "https://%62illing.example.com",
        "https://%25billing.example.com",
        "https://billing[example.com",
        BACKSLASH_IN_HOST_URL,
        "https://billing|example.com",
        "https://[::::]",
        "https://billing.example.com/api\x01",
    ):
        result = standalone_result(provider_args({"baseUrl": bad}))
        assert result.returncode != 0, f"standalone {bad!r} passed validation"
        assert "baseUrl" in result.stderr, result.stderr


if __name__ == "__main__":
    check_disabled()
    check_configured()
    check_ca_secret()
    check_standalone_subchart()
    check_base_url_forms()
    check_invalid()
    check_standalone_base_url_schema()
    print("Billing provider render checks passed.")
