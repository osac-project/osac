#!/usr/bin/env python3
"""Check CSI and storage-provider chart rendering.

Port of the removed `csi-render-test` Makefile target: the CSI render
contract (vendor-controller pinning, fulfillment-endpoint cardinality,
custom fulfillment ConfigMap wiring, the csi-driver node DaemonSet
arguments and credentials, and the schema rejections for invalid values)
stays part of the aggregate `make helm-validate` run.
"""

from __future__ import annotations

import pathlib
import re
import subprocess

TESTS = pathlib.Path(__file__).resolve().parent
INSTALLER = TESTS.parents[2]
CHART = INSTALLER / "charts/osac"
INFRA_CHART = INSTALLER / "charts/osac-infra"
CSI_DRIVER_CHART = INSTALLER.parent / "osac-csi-driver/charts/csi-driver"

# Profile renders need the same overrides the removed target passed so the
# umbrella chart renders offline (hostnames, domain, deployment id, issuer).
PROFILE_OVERRIDES = (
    "--set",
    "service.externalHostname=fulfillment-api.example.com",
    "--set",
    "service.internalHostname=fulfillment-internal-api.example.com",
    "--set",
    "global.clusterDomain=example.com",
    "--set-string",
    "global.osacDeploymentId=ci/osac",
    "--set",
    "ui.auth.oidcIssuerURL=https://keycloak-keycloak.example.com/realms/osac",
)


def run_helm(*args: str) -> subprocess.CompletedProcess[str]:
    command = ["helm", "template", *args]
    return subprocess.run(command, check=False, capture_output=True, text=True)


def render(*args: str) -> str:
    result = run_helm(*args)
    assert result.returncode == 0, f"helm render failed:\n{result.stderr}"
    return result.stdout


def assert_helm_failure(expected: str, *args: str) -> None:
    result = run_helm(*args)
    output = result.stdout + result.stderr
    assert result.returncode != 0, (
        f"Helm unexpectedly accepted invalid values; expected error pattern: {expected}"
    )
    assert re.search(expected, output), (
        f"Helm failed without the expected error pattern: {expected}\n{output}"
    )


def fulfillment_endpoint_count(manifest: str) -> int:
    return len(re.findall(r"^  OSAC_FULFILLMENT_ENDPOINT:", manifest, re.MULTILINE))


def test_caas_ci_renders_one_fulfillment_endpoint() -> None:
    manifest = render(
        "osac",
        str(CHART),
        "--values",
        str(INSTALLER / "values/caas-ci/instance.yaml"),
        *PROFILE_OVERRIDES,
    )
    assert fulfillment_endpoint_count(manifest) == 1


def test_vmaas_ci_pins_vendor_controllers_lvms_none() -> None:
    manifest = render(
        "osac",
        str(CHART),
        "--values",
        str(INSTALLER / "values/vmaas-ci/instance.yaml"),
        *PROFILE_OVERRIDES,
    )
    assert re.search(
        r'name: OSAC_VENDOR_CONTROLLERS\n[ \t]+value: "lvms=none"', manifest
    ), "vmaas-ci must pin OSAC_VENDOR_CONTROLLERS to lvms=none"
    assert fulfillment_endpoint_count(manifest) == 1


def test_default_values_omit_vendor_controllers() -> None:
    manifest = render("osac", str(CHART), "--values", str(CHART / "ci/default-values.yaml"))
    assert "name: OSAC_VENDOR_CONTROLLERS" not in manifest
    assert fulfillment_endpoint_count(manifest) == 1


def test_vendor_controllers_rejects_non_string_values() -> None:
    assert_helm_failure(
        r"operator[./]vendorControllers[./]lvms",
        "osac",
        str(CHART),
        "--values",
        str(CHART / "ci/default-values.yaml"),
        "--set-string",
        "operator.vendorControllers.lvms=",
    )
    assert_helm_failure(
        r"operator[./]vendorControllers[./]lvms",
        "osac",
        str(CHART),
        "--values",
        str(CHART / "ci/default-values.yaml"),
        "--set",
        "operator.vendorControllers.lvms=42",
    )


def test_full_values_omit_csi_subcharts() -> None:
    manifest = render("osac", str(CHART), "--values", str(CHART / "ci/full-values.yaml"))
    assert fulfillment_endpoint_count(manifest) == 1
    assert not re.search(r"# Source: osac/charts/(csiDriver|csiBackends)/", manifest), (
        "full-values must not render the vendored csiDriver/csiBackends subcharts"
    )


def test_custom_fulfillment_configmap_name_rendered() -> None:
    manifest = render(
        "osac",
        str(CHART),
        "--values",
        str(CHART / "ci/full-values.yaml"),
        "--set",
        "operator.fulfillment.configMap.name=osac-custom-fulfillment-config",
    )
    assert re.search(
        r'^  name: "?osac-custom-fulfillment-config"?$', manifest, re.MULTILINE
    ), "the custom fulfillment ConfigMap name must reach the rendered hook"


def test_csi_driver_node_daemonset_contract() -> None:
    manifest = render(
        "csi-driver",
        str(CSI_DRIVER_CHART),
        "--show-only",
        "templates/node-daemonset.yaml",
        "--set",
        "namespace=osac-csi",
        "--set",
        "controller.fulfillment.configMap.name=osac-csi-fulfillment-config",
        "--set",
        "controller.fulfillment.credentials.secretName=osac-csi-driver-credentials",
        "--set",
        "controller.fulfillment.allowStub=false",
    )
    for literal in (
        "--fulfillment-endpoint=$(OSAC_FULFILLMENT_ENDPOINT)",
        "--fulfillment-issuer-url=$(OSAC_FULFILLMENT_ISSUER_URL)",
        "--fulfillment-client-id=$(FULFILLMENT_CLIENT_ID)",
        "name: osac-csi-fulfillment-config",
        "secretName: osac-csi-driver-credentials",
    ):
        assert literal in manifest, f"node DaemonSet must render {literal!r}"


def test_infra_caas_ci_omits_csi_credentials() -> None:
    manifest = render(
        "osac-infra",
        str(INFRA_CHART),
        "--values",
        str(INSTALLER / "values/caas-ci/infra.yaml"),
    )
    assert not re.search(
        r"osac-infra-create-csi-creds|osac-csi-driver-credentials", manifest
    ), "caas-ci infra must not create CSI credentials"


def test_csi_driver_rejects_foreign_namespace() -> None:
    assert_helm_failure(
        r"namespace.*must be one of",
        "csi-driver",
        str(CSI_DRIVER_CHART),
        "--set",
        "namespace=not-osac-csi",
    )
