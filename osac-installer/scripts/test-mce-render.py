#!/usr/bin/env python3
"""Verify disabled MCE defaults and explicit CaaS enablement."""

from __future__ import annotations

import json
import os
import pathlib
import shutil
import subprocess
import tempfile
from typing import Any

import yaml

INSTALLER = pathlib.Path(__file__).resolve().parents[1]
DEPS_CHART = INSTALLER / "charts/osac-deps"
INFRA_CHART = INSTALLER / "charts/osac-infra"
CAAS_VALUES = INSTALLER / "values/caas-ci/infra.yaml"

DEPS_RESOURCES = (
    ("Namespace", "multicluster-engine"),
    ("OperatorGroup", "mce"),
    ("Subscription", "multicluster-engine"),
)
INFRA_RESOURCES = (
    ("ConfigMap", "osac-infra-mce-config"),
    ("ConfigMap", "osac-infra-mce-image-overrides"),
    ("ClusterRole", "osac-infra-mce-assisted-service-ocm5-compat"),
    ("ClusterRoleBinding", "osac-infra-mce-assisted-service-ocm5-compat"),
    ("ServiceAccount", "osac-infra-configure-mce"),
    ("ClusterRole", "osac-infra-configure-mce"),
    ("Role", "osac-infra-configure-mce"),
    ("RoleBinding", "osac-infra-configure-mce"),
    ("ClusterRoleBinding", "osac-infra-configure-mce"),
    ("Job", "osac-infra-configure-mce"),
)
CONFIGURE_HOOK_RESOURCES = INFRA_RESOURCES[4:]


def read_values(path: pathlib.Path) -> dict[str, Any]:
    values = yaml.safe_load(path.read_text(encoding="utf-8")) or {}
    assert isinstance(values, dict), f"expected {path} to contain a values mapping"
    return values


def merged_values(base: dict[str, Any], overlay: dict[str, Any]) -> dict[str, Any]:
    result = base.copy()
    for key, value in overlay.items():
        if isinstance(value, dict) and isinstance(result.get(key), dict):
            result[key] = merged_values(result[key], value)
        else:
            result[key] = value
    return result


def configured_mce(chart: pathlib.Path, overlay: pathlib.Path | None = None) -> dict[str, Any]:
    values = read_values(chart / "values.yaml")
    if overlay is not None:
        values = merged_values(values, read_values(overlay))
    mce = values.get("mce") or {}
    assert isinstance(mce, dict), f"expected effective mce values for {chart} to be a mapping"
    return mce


def render(release: str, chart: pathlib.Path, *args: str) -> list[dict[str, Any]]:
    command = [os.environ.get("HELM_BIN", "helm"), "template", release, str(chart), *args]
    result = subprocess.run(command, check=False, capture_output=True, text=True)
    if result.returncode:
        raise RuntimeError(f"Helm render failed: {' '.join(command)}\n{result.stderr}")
    return [doc for doc in yaml.safe_load_all(result.stdout) if isinstance(doc, dict)]


def resource(docs: list[dict[str, Any]], kind: str, name: str) -> dict[str, Any]:
    matches = [doc for doc in docs if doc.get("kind") == kind and doc.get("metadata", {}).get("name") == name]
    assert len(matches) == 1, f"expected one {kind}/{name}, found {len(matches)}"
    return matches[0]


def assert_absent(docs: list[dict[str, Any]], resources: tuple[tuple[str, str], ...]) -> None:
    for kind, name in resources:
        matches = [doc for doc in docs if doc.get("kind") == kind and doc.get("metadata", {}).get("name") == name]
        assert not matches, f"expected {kind}/{name} to be omitted"


def selected(docs: list[dict[str, Any]], resources: tuple[tuple[str, str], ...]) -> list[dict[str, Any]]:
    return [resource(docs, kind, name) for kind, name in resources]


def assert_configuration_contract(infra: list[dict[str, Any]]) -> None:
    config = resource(infra, "ConfigMap", "osac-infra-mce-config")
    agent_service_config = yaml.safe_load(config["data"]["config.yaml"])
    assert agent_service_config["kind"] == "AgentServiceConfig"
    assert agent_service_config["metadata"]["name"] == "agent"
    assert "agent-install.openshift.io/enable-image-service" not in agent_service_config["metadata"].get(
        "annotations", {}
    ), "MCE image service must remain enabled when MCE is enabled"

    selected(infra, CONFIGURE_HOOK_RESOURCES)
    job = resource(infra, "Job", "osac-infra-configure-mce")
    pod_spec = job["spec"]["template"]["spec"]
    config_volume = next(volume for volume in pod_spec["volumes"] if volume["name"] == "config")
    assert config_volume["configMap"]["name"] == "osac-infra-mce-config"
    container = next(container for container in pod_spec["containers"] if container["name"] == "configure-mce")
    assert {"name": "config", "mountPath": "/config"} in container["volumeMounts"]
    assert {"name": "scripts", "mountPath": "/scripts"} in container["volumeMounts"]
    assert container["command"] == ["/bin/bash", "/scripts/configure-mce.sh"]


def assert_dependencies_contract(deps: list[dict[str, Any]], expected_mce: dict[str, Any]) -> None:
    namespace, operator_group, subscription = selected(deps, DEPS_RESOURCES)
    assert namespace["metadata"]["name"] == "multicluster-engine"
    assert operator_group["metadata"]["namespace"] == "multicluster-engine"
    assert operator_group["spec"]["targetNamespaces"] == ["multicluster-engine"]
    assert subscription["metadata"]["namespace"] == "multicluster-engine"
    assert subscription["spec"]["name"] == "multicluster-engine"
    assert subscription["spec"]["channel"] == expected_mce["channel"], (
        "rendered MCE channel must match the effective configured channel"
    )


def assert_image_override_contract(infra: list[dict[str, Any]], expected_mce: dict[str, Any]) -> None:
    expected_overrides = expected_mce.get("imageOverrides") or []
    if not expected_overrides:
        assert_absent(infra, INFRA_RESOURCES[1:4])
        return

    overrides, role, binding = selected(infra, INFRA_RESOURCES[1:4])
    rendered_overrides = json.loads(overrides["data"]["manifest.json"])
    assert rendered_overrides == expected_overrides, (
        "rendered image overrides must exactly match the effective configured list"
    )
    assert role["rules"] == [
        {"apiGroups": ["config.openshift.io"], "resources": ["apiservers"], "verbs": ["get", "list", "watch"]},
        {
            "apiGroups": ["networking.k8s.io"],
            "resources": ["networkpolicies"],
            "verbs": ["create", "delete", "get", "list", "patch", "update", "watch"],
        },
    ]
    assert binding["roleRef"] == {
        "apiGroup": "rbac.authorization.k8s.io",
        "kind": "ClusterRole",
        "name": "osac-infra-mce-assisted-service-ocm5-compat",
    }
    assert binding["subjects"] == [
        {"kind": "ServiceAccount", "name": "assisted-service", "namespace": "multicluster-engine"}
    ]


def assert_infrastructure_contract(infra: list[dict[str, Any]], expected_mce: dict[str, Any]) -> None:
    assert_configuration_contract(infra)
    assert_image_override_contract(infra, expected_mce)


def check_disabled() -> None:
    deps_values = configured_mce(DEPS_CHART)
    infra_values = configured_mce(INFRA_CHART)
    assert deps_values.get("enabled") is False, "osac-deps must disable standalone MCE by default"
    assert infra_values.get("enabled") is False, "osac-infra must disable standalone MCE by default"

    default_deps = render("osac-deps", DEPS_CHART)
    default_infra = render("osac-infra", INFRA_CHART)
    assert_absent(default_deps, DEPS_RESOURCES)
    assert_absent(default_infra, INFRA_RESOURCES)

    deps = render("osac-deps", DEPS_CHART, "--set", "mce.enabled=false")
    infra = render("osac-infra", INFRA_CHART, "--set", "mce.enabled=false")
    assert_absent(deps, DEPS_RESOURCES)
    assert_absent(infra, INFRA_RESOURCES)


def check_configured_defaults() -> None:
    deps_values = merged_values(configured_mce(DEPS_CHART), {"enabled": True})
    infra_values = merged_values(configured_mce(INFRA_CHART), {"enabled": True})

    deps = render("osac-deps", DEPS_CHART, "--set", "mce.enabled=true")
    infra = render("osac-infra", INFRA_CHART, "--set", "mce.enabled=true")
    assert_dependencies_contract(deps, deps_values)
    assert_infrastructure_contract(infra, infra_values)


def write_mce_fixture(path: pathlib.Path, mce: dict[str, Any]) -> None:
    path.write_text(yaml.safe_dump({"mce": mce}, sort_keys=False), encoding="utf-8")


def check_arbitrary_configured_values() -> None:
    replacement_mce = {
        "enabled": True,
        "channel": "candidate-configured-channel",
        "imageOverrides": [
            {
                "image-name": "configured-service",
                "image-remote": "registry.example.test/team",
                "image-digest": f"sha256:{'1' * 64}",
                "image-key": "configured_service",
            },
            {
                "image-name": "configured-agent",
                "image-remote": "registry.example.test/other-team",
                "image-digest": f"sha256:{'2' * 64}",
                "image-key": "configured_agent",
            },
        ],
    }
    with tempfile.TemporaryDirectory(prefix="mce-values-") as temp_dir:
        fixture = pathlib.Path(temp_dir) / "replacement.yaml"
        write_mce_fixture(fixture, replacement_mce)
        deps_values = configured_mce(DEPS_CHART, fixture)
        infra_values = configured_mce(INFRA_CHART, fixture)
        args = ("--values", str(fixture))
        deps = render("osac-deps", DEPS_CHART, *args)
        infra = render("osac-infra", INFRA_CHART, *args)
        assert_dependencies_contract(deps, deps_values)
        assert_infrastructure_contract(infra, infra_values)


def check_empty_overrides() -> None:
    with tempfile.TemporaryDirectory(prefix="mce-values-") as temp_dir:
        fixture = pathlib.Path(temp_dir) / "empty-overrides.yaml"
        write_mce_fixture(fixture, {"enabled": True, "imageOverrides": []})
        infra_values = configured_mce(INFRA_CHART, fixture)
        infra = render("osac-infra", INFRA_CHART, "--values", str(fixture))
        assert_infrastructure_contract(infra, infra_values)


def check_absent_overrides() -> None:
    with tempfile.TemporaryDirectory(prefix="mce-chart-") as temp_dir:
        chart = pathlib.Path(temp_dir) / "osac-infra"
        shutil.copytree(INFRA_CHART, chart)
        values_path = chart / "values.yaml"
        values = read_values(values_path)
        values["mce"].pop("imageOverrides", None)
        values_path.write_text(yaml.safe_dump(values, sort_keys=False), encoding="utf-8")

        infra_values = merged_values(configured_mce(chart), {"enabled": True})
        infra = render("osac-infra", chart, "--set", "mce.enabled=true")
        assert_infrastructure_contract(infra, infra_values)


def check_caas_inherits_defaults() -> None:
    caas_values = yaml.safe_load(CAAS_VALUES.read_text(encoding="utf-8"))
    assert caas_values["mce"] == {"enabled": True}, "CaaS must explicitly enable MCE without duplicating defaults"

    caas_args = ("--values", str(CAAS_VALUES))
    caas_deps = render("osac-deps", DEPS_CHART, *caas_args)
    caas_infra = render("osac-infra", INFRA_CHART, *caas_args)
    assert_dependencies_contract(caas_deps, configured_mce(DEPS_CHART, CAAS_VALUES))
    assert_infrastructure_contract(caas_infra, configured_mce(INFRA_CHART, CAAS_VALUES))


def main() -> None:
    check_disabled()
    check_configured_defaults()
    check_arbitrary_configured_values()
    check_empty_overrides()
    check_absent_overrides()
    check_caas_inherits_defaults()
    print("MCE disabled-default, configured-value, no-override, and CaaS render checks passed.")


if __name__ == "__main__":
    main()
