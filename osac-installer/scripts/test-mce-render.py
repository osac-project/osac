#!/usr/bin/env python3
"""Verify the Helm contract for the standalone MCE defaults and opt-outs."""

from __future__ import annotations

import json
import os
import pathlib
import subprocess
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
EXPECTED_OVERRIDES = [
    {
        "image-name": "assisted-service",
        "image-remote": "quay.io/edge-infrastructure",
        "image-digest": "sha256:c007ecc530f4bb0a43814f4373bdec6e27d1fa3698554b0935a07c086802e479",
        "image-key": "assisted_service_9",
    },
    {
        "image-name": "assisted-installer-agent",
        "image-remote": "quay.io/edge-infrastructure",
        "image-digest": "sha256:0f4f6041821d1a03da280090cf8a857e6b081487301de7e37553b0398925b391",
        "image-key": "assisted_installer_agent",
    },
    {
        "image-name": "assisted-installer",
        "image-remote": "quay.io/edge-infrastructure",
        "image-digest": "sha256:04fe09dddf10be4fa7dfd91bde7b4c6addb60b3d1291596a9399fe057f5f1f3b",
        "image-key": "assisted_installer",
    },
    {
        "image-name": "assisted-installer-controller",
        "image-remote": "quay.io/edge-infrastructure",
        "image-digest": "sha256:54c095c08defc738558948ef1be52dd980642bf0020570435a68384d1d5177a3",
        "image-key": "assisted_installer_controller",
    },
]


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
    ), "MCE image service must remain enabled by default"

    selected(infra, CONFIGURE_HOOK_RESOURCES)
    job = resource(infra, "Job", "osac-infra-configure-mce")
    pod_spec = job["spec"]["template"]["spec"]
    config_volume = next(volume for volume in pod_spec["volumes"] if volume["name"] == "config")
    assert config_volume["configMap"]["name"] == "osac-infra-mce-config"
    container = next(container for container in pod_spec["containers"] if container["name"] == "configure-mce")
    assert {"name": "config", "mountPath": "/config"} in container["volumeMounts"]
    assert {"name": "scripts", "mountPath": "/scripts"} in container["volumeMounts"]
    assert container["command"] == ["/bin/bash", "/scripts/configure-mce.sh"]


def check_defaults(deps: list[dict[str, Any]], infra: list[dict[str, Any]]) -> None:
    namespace, operator_group, subscription = selected(deps, DEPS_RESOURCES)
    assert namespace["metadata"]["name"] == "multicluster-engine"
    assert operator_group["metadata"]["namespace"] == "multicluster-engine"
    assert operator_group["spec"]["targetNamespaces"] == ["multicluster-engine"]
    assert subscription["metadata"]["namespace"] == "multicluster-engine"
    assert subscription["spec"]["name"] == "multicluster-engine"
    assert subscription["spec"]["channel"] == "stable-2.17"

    assert_configuration_contract(infra)
    overrides, role, binding = selected(infra, INFRA_RESOURCES[1:4])

    rendered_overrides = json.loads(overrides["data"]["manifest.json"])
    assert rendered_overrides == EXPECTED_OVERRIDES, (
        "default MCE render must contain the four pinned Assisted image overrides"
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


def check_disabled() -> None:
    deps = render("osac-deps", DEPS_CHART, "--set", "mce.enabled=false")
    infra = render("osac-infra", INFRA_CHART, "--set", "mce.enabled=false")
    assert_absent(deps, DEPS_RESOURCES)
    assert_absent(infra, INFRA_RESOURCES)


def check_empty_overrides() -> None:
    infra = render("osac-infra", INFRA_CHART, "--set-json", "mce.imageOverrides=[]")
    assert_configuration_contract(infra)
    assert_absent(infra, INFRA_RESOURCES[1:4])


def check_caas_inherits_defaults(default_deps: list[dict[str, Any]], default_infra: list[dict[str, Any]]) -> None:
    caas_values = yaml.safe_load(CAAS_VALUES.read_text(encoding="utf-8"))
    assert "mce" not in caas_values, "CaaS must inherit MCE defaults without an overlay block"

    caas_args = ("--values", str(CAAS_VALUES))
    caas_deps = render("osac-deps", DEPS_CHART, *caas_args)
    caas_infra = render("osac-infra", INFRA_CHART, *caas_args)
    assert selected(caas_deps, DEPS_RESOURCES) == selected(default_deps, DEPS_RESOURCES)
    assert selected(caas_infra, INFRA_RESOURCES) == selected(default_infra, INFRA_RESOURCES)
    assert_configuration_contract(caas_infra)


def main() -> None:
    default_deps = render("osac-deps", DEPS_CHART)
    default_infra = render("osac-infra", INFRA_CHART)
    check_defaults(default_deps, default_infra)
    check_disabled()
    check_empty_overrides()
    check_caas_inherits_defaults(default_deps, default_infra)
    print("MCE default, opt-out, and CaaS inheritance render checks passed.")


if __name__ == "__main__":
    main()
