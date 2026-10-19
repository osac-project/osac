#!/usr/bin/env python3
"""Check optional billing configuration in Fulfillment and metering Helm renders."""

from __future__ import annotations

import pathlib
import subprocess

CHART = pathlib.Path(__file__).resolve().parents[1] / "charts/osac"
DEFAULT_VALUES = CHART / "ci/default-values.yaml"
PROCESSES = {
    "grpc-server": "/etc/fulfillment-grpc-server/billing/api-key",
    "controller": "/etc/fulfillment-controller/billing/api-key",
}


def render(
    *,
    provider: str | None = None,
    configure_m360: bool = False,
    shared_m360: bool = False,
) -> str:
    cmd = ["helm", "template", "osac", str(CHART), "--values", str(DEFAULT_VALUES)]
    if provider is not None:
        cmd.extend(("--set", f"service.billing.provider={provider}"))
    if configure_m360:
        cmd.extend(
            (
                "--set-string",
                "service.billing.m360.apiUrl=https://m360.ci.example.com",
                "--set-string",
                "service.billing.m360.apiVersion=v1",
                "--set-string",
                "service.billing.m360.apiKeySecret.name=ci-m360-credentials",
                "--set-string",
                "service.billing.m360.apiKeySecret.key=legacy-token",
                "--set",
                "global.osacDeploymentId=ci/osac",
                "--set",
                "metering.enabled=true",
                "--set",
                "metering.m360Adapter.enabled=true",
                "--set-string",
                "metering.m360Adapter.m360.apiUrl=https://legacy-m360.ci.example.com",
                "--set-string",
                "metering.m360Adapter.m360.apiVersion=v1",
                "--set-string",
                "metering.m360Adapter.m360.apiKeySecret=ci-metering-m360-credentials",
            )
        )
    if shared_m360:
        cmd.extend(
            (
                "--set-string",
                "global.billing.provider=m360",
                "--set-string",
                "global.billing.m360.apiUrl=https://m360.ci.example.com",
                "--set-string",
                "global.billing.m360.apiVersion=v1",
                "--set-string",
                "global.billing.m360.apiKeySecret.name=ci-m360-credentials",
                "--set-string",
                "global.billing.m360.apiKeySecret.key=integration-token",
                "--set-string",
                "metering.m360Adapter.m360.apiUrl=https://legacy-m360.ci.example.com",
                "--set-string",
                "metering.m360Adapter.m360.apiVersion=legacy-v9",
                "--set-string",
                "metering.m360Adapter.m360.apiKeySecret=legacy-metering-credentials",
                "--set",
                "global.osacDeploymentId=ci/osac",
                "--set",
                "metering.enabled=true",
                "--set",
                "metering.m360Adapter.enabled=true",
            )
        )
    result = subprocess.run(cmd, check=False, capture_output=True, text=True)
    if result.returncode != 0:
        raise RuntimeError(f"helm template failed:\n{result.stderr}")
    return result.stdout


def select_deployment(manifest: str, source_file: str) -> str:
    source = f"# Source: osac/charts/{source_file}\n"
    documents = [doc for doc in manifest.split("\n---\n") if source in doc]
    if len(documents) != 1:
        raise AssertionError(f"expected one rendered {source_file} Deployment, found {len(documents)}")
    return documents[0]


def check_disabled() -> None:
    manifest = render()
    for process, key_file in PROCESSES.items():
        deployment = select_deployment(manifest, f"service/templates/{process}/deployment.yaml")
        assert "billing-m360-api-key" not in deployment, process
        assert "--billing-provider=" not in deployment, process
        assert key_file not in deployment, process


def check_m360() -> None:
    manifest = render(provider="m360", configure_m360=True)
    for process, key_file in PROCESSES.items():
        deployment = select_deployment(manifest, f"service/templates/{process}/deployment.yaml")
        assert "secretName: ci-m360-credentials" in deployment, process
        assert "key: legacy-token" in deployment, process
        assert f"mountPath: {key_file.removesuffix('/api-key')}" in deployment, process
        assert f"--billing-m360-api-key-file={key_file}" in deployment, process
        assert "--billing-provider=m360" in deployment, process
        assert "--billing-m360-api-url=https://m360.ci.example.com" in deployment, process
        assert "--billing-m360-api-version=v1" in deployment, process

    assert not contains_deployment(manifest, "metering/templates/m360-adapter-deployment.yaml")


def check_shared_m360() -> None:
    manifest = render(shared_m360=True)
    for process, key_file in PROCESSES.items():
        deployment = select_deployment(manifest, f"service/templates/{process}/deployment.yaml")
        assert "secretName: ci-m360-credentials" in deployment, process
        assert "key: integration-token" in deployment, process
        assert f"mountPath: {key_file.removesuffix('/api-key')}" in deployment, process
        assert "--billing-provider=m360" in deployment, process
        assert "--billing-m360-api-url=https://m360.ci.example.com" in deployment, process
        assert "--billing-m360-api-version=v1" in deployment, process

    adapter = select_deployment(manifest, "metering/templates/m360-adapter-deployment.yaml")
    assert "secretName: ci-m360-credentials" in adapter
    assert "key: integration-token" in adapter
    assert 'value: "https://m360.ci.example.com"' in adapter
    assert 'value: "v1"' in adapter


def contains_deployment(manifest: str, source_file: str) -> bool:
    return f"# Source: osac/charts/{source_file}\n" in manifest


def check_invalid_provider() -> None:
    cmd = [
        "helm",
        "template",
        "osac",
        str(CHART),
        "--values",
        str(DEFAULT_VALUES),
        "--set",
        "service.billing.provider=unsupported",
    ]
    result = subprocess.run(cmd, check=False, capture_output=True, text=True)
    if result.returncode == 0:
        raise AssertionError("unsupported billing provider unexpectedly rendered")


if __name__ == "__main__":
    check_disabled()
    check_m360()
    check_shared_m360()
    check_invalid_provider()
    print("Billing render checks passed.")
