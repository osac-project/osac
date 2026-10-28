"""Helm registration contract tests for the Netris ExternalIP contract version.

These tests invoke real `helm template` in a subprocess against the operator
chart and parse the rendered YAML. No network, cluster, or provider access.

They guard the AAP capability/registration agreement for
`networkManagers.fabricManagers.netris.externalIPContractVersion` before the
default chart enables it (plan OSAC-6376, tests H01-H03, AC 1 and 5):

- H01: enabling Netris with an explicit v1 override renders exactly one Netris
  fabric-manager ConfigMap whose contract version is v1.
- H02: an explicit empty (or absent) contract version omits the key, keeping
  the legacy/unversioned dispatch.
- H03: disabling Netris registration emits no Netris manager ConfigMap.
"""

from pathlib import Path

import pytest
import yaml

REPO_ROOT = Path(__file__).resolve().parents[3]
OPERATOR_CHART = REPO_ROOT / "osac-operator" / "charts" / "operator"

FABRIC_MANAGER_LABEL = "osac.openshift.io/network-fabric-manager"
CONTRACT_VERSION_KEY = "externalIPContractVersion"

HELM_TIMEOUT_SECONDS = 300


@pytest.fixture(autouse=True)
def _require_helm() -> None:
    """Fail with an actionable message when the helm executable is missing.

    This is mandatory coverage, so a missing prerequisite must fail the tests
    rather than silently skipping them.
    """
    import shutil

    if shutil.which("helm") is None:
        pytest.fail(
            "helm executable not found on PATH; the Netris ExternalIP "
            "registration contract tests render the operator chart with real "
            "'helm template' (helm >= 3.x required). Install helm "
            "(https://helm.sh/docs/intro/install/) and re-run these tests."
        )


def _render(
    set_overrides: list[str] | None = None,
    set_string_overrides: list[str] | None = None,
) -> list[dict]:
    """Render the operator chart with real helm and return parsed documents."""
    cmd = [
        "helm",
        "template",
        "test-render",
        str(OPERATOR_CHART),
        "--namespace",
        "osac-system",
    ]
    for override in set_overrides or []:
        cmd.extend(["--set", override])
    for override in set_string_overrides or []:
        cmd.extend(["--set-string", override])
    import subprocess

    manifest = subprocess.run(
        cmd, check=True, capture_output=True, text=True, timeout=HELM_TIMEOUT_SECONDS
    ).stdout
    return [doc for doc in yaml.safe_load_all(manifest) if doc]


def _netris_fabric_manager_configmaps(docs: list[dict]) -> list[dict]:
    """Select rendered fabric-manager ConfigMaps registered for the netris key."""
    return [
        doc
        for doc in docs
        if doc.get("kind") == "ConfigMap"
        and doc.get("metadata", {}).get("labels", {}).get(FABRIC_MANAGER_LABEL)
        == "true"
        and doc.get("data", {}).get("name") == "netris"
    ]


@pytest.mark.parametrize(
    "set_overrides",
    [
        pytest.param(
            [
                "networkManagers.fabricManagers.netris.enabled=true",
            ],
            id="direct-enabled",
        ),
        pytest.param(
            ["global.networking.fabricManager=netris"],
            id="global-fabric-manager-selection",
        ),
    ],
)
def test_h01_explicit_v1_override_renders_exactly_one_netris_manager_with_v1(
    set_overrides: list[str],
) -> None:
    docs = _render(
        set_overrides=set_overrides,
        set_string_overrides=[
            f"networkManagers.fabricManagers.netris.{CONTRACT_VERSION_KEY}=v1",
        ],
    )
    configmaps = _netris_fabric_manager_configmaps(docs)
    assert len(configmaps) == 1, (
        f"expected exactly one Netris fabric-manager ConfigMap, found {len(configmaps)}"
    )
    data = configmaps[0]["data"]
    assert data[CONTRACT_VERSION_KEY] == "v1"


@pytest.mark.parametrize(
    "set_overrides,set_string_overrides",
    [
        pytest.param(
            ["networkManagers.fabricManagers.netris.enabled=true"],
            [f"networkManagers.fabricManagers.netris.{CONTRACT_VERSION_KEY}="],
            id="explicit-empty-contract-version",
        ),
    ],
)
def test_h02_empty_contract_version_keeps_legacy_render(
    set_overrides: list[str],
    set_string_overrides: list[str],
) -> None:
    docs = _render(
        set_overrides=set_overrides,
        set_string_overrides=set_string_overrides,
    )
    configmaps = _netris_fabric_manager_configmaps(docs)
    assert len(configmaps) == 1, (
        f"expected exactly one Netris fabric-manager ConfigMap, found {len(configmaps)}"
    )
    assert CONTRACT_VERSION_KEY not in configmaps[0]["data"], (
        "an empty contract version must omit "
        f"{CONTRACT_VERSION_KEY} (legacy/unversioned dispatch)"
    )


@pytest.mark.parametrize(
    "set_overrides",
    [
        pytest.param(
            ["networkManagers.fabricManagers.netris.enabled=true"],
            id="direct-enabled-default-v1",
        ),
        pytest.param(
            ["global.networking.fabricManager=netris"],
            id="global-selection-default-v1",
        ),
    ],
)
def test_h02_default_contract_version_renders_v1(set_overrides: list[str]) -> None:
    docs = _render(set_overrides=set_overrides, set_string_overrides=[])
    configmaps = _netris_fabric_manager_configmaps(docs)
    assert len(configmaps) == 1, (
        f"expected exactly one Netris fabric-manager ConfigMap, found {len(configmaps)}"
    )
    assert configmaps[0]["data"][CONTRACT_VERSION_KEY] == "v1", (
        "the registration default advertises the v1 contract once the "
        "AAP role supports the v1 input and result contract"
    )


@pytest.mark.parametrize(
    "set_overrides",
    [
        pytest.param([], id="chart-default-disabled"),
        pytest.param(
            ["networkManagers.fabricManagers.netris.enabled=false"],
            id="explicitly-disabled",
        ),
    ],
)
def test_h03_disabled_netris_registration_emits_no_netris_manager_configmap(
    set_overrides: list[str],
) -> None:
    docs = _render(set_overrides=set_overrides)
    configmaps = _netris_fabric_manager_configmaps(docs)
    assert not configmaps, (
        "disabled Netris registration must emit no Netris fabric-manager "
        f"ConfigMap, found {len(configmaps)}"
    )
