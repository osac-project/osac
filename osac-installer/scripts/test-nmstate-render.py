#!/usr/bin/env python3
"""Verify the NMState prerequisite contract and VMaaS profile wiring."""

from __future__ import annotations

import os
import pathlib
import subprocess
from typing import Any

import yaml

INSTALLER = pathlib.Path(__file__).resolve().parents[1]
DEPS_CHART = INSTALLER / "charts/osac-deps"
DEPENDENCY_RESOURCES = (
    ("Namespace", "openshift-nmstate"),
    ("OperatorGroup", "openshift-nmstate"),
    ("Subscription", "kubernetes-nmstate-operator"),
)
HOOK_RESOURCES = (
    ("ServiceAccount", "osac-deps-wait-nmstate"),
    ("Role", "osac-deps-wait-nmstate"),
    ("RoleBinding", "osac-deps-wait-nmstate"),
    ("Job", "osac-deps-wait-nmstate"),
)
VMAAS_PROFILES = ("dev", "vmaas-ci", "caas-ci", "full-ci", "cudn-evpn-netris-test")
NON_VMAAS_PROFILES = ("bmaas-ci", "bmaas-netbox-ci")


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


def render(*args: str) -> list[dict[str, Any]]:
    command = [
        os.environ.get("HELM_BIN", "helm"),
        "template",
        "osac-deps",
        str(DEPS_CHART),
        "--namespace",
        "osac",
        *args,
    ]
    result = subprocess.run(command, check=False, capture_output=True, text=True)
    if result.returncode:
        raise RuntimeError(f"Helm render failed: {' '.join(command)}\n{result.stderr}")
    return [doc for doc in yaml.safe_load_all(result.stdout) if isinstance(doc, dict)]


def find(docs: list[dict[str, Any]], kind: str, name: str) -> dict[str, Any]:
    matches = [doc for doc in docs if doc.get("kind") == kind and doc.get("metadata", {}).get("name") == name]
    assert len(matches) == 1, f"expected one {kind}/{name}, found {len(matches)}"
    return matches[0]


def assert_absent(docs: list[dict[str, Any]]) -> None:
    for kind, name in (*DEPENDENCY_RESOURCES, *HOOK_RESOURCES):
        matches = [doc for doc in docs if doc.get("kind") == kind and doc.get("metadata", {}).get("name") == name]
        assert not matches, f"expected {kind}/{name} to be omitted"


def assert_enabled_contract(docs: list[dict[str, Any]]) -> None:
    namespace = find(docs, "Namespace", "openshift-nmstate")
    assert namespace["metadata"]["name"] == "openshift-nmstate"

    operator_group = find(docs, "OperatorGroup", "openshift-nmstate")
    assert operator_group["metadata"]["namespace"] == "openshift-nmstate"
    assert operator_group["spec"]["targetNamespaces"] == ["openshift-nmstate"]

    subscription = find(docs, "Subscription", "kubernetes-nmstate-operator")
    assert subscription["metadata"]["namespace"] == "openshift-nmstate"
    assert subscription["spec"] == {
        "channel": "stable",
        "name": "kubernetes-nmstate-operator",
        "source": "redhat-operators",
        "sourceNamespace": "openshift-marketplace",
        "installPlanApproval": "Automatic",
    }

    role = find(docs, "Role", "osac-deps-wait-nmstate")
    assert role["metadata"]["namespace"] == "openshift-nmstate"
    assert role["rules"] == [
        {
            "apiGroups": ["operators.coreos.com"],
            "resources": ["subscriptions", "clusterserviceversions"],
            "verbs": ["get", "list"],
        },
        {"apiGroups": ["apps"], "resources": ["deployments"], "verbs": ["get", "list", "watch"]},
    ]

    job = find(docs, "Job", "osac-deps-wait-nmstate")
    annotations = job["metadata"]["annotations"]
    assert annotations["helm.sh/hook"] == "post-install,post-upgrade"
    assert annotations["helm.sh/hook-weight"] == "10"
    pod_spec = job["spec"]["template"]["spec"]
    assert pod_spec["serviceAccountName"] == "osac-deps-wait-nmstate"
    role_binding = find(docs, "RoleBinding", "osac-deps-wait-nmstate")
    assert role_binding["metadata"]["namespace"] == "openshift-nmstate"
    assert role_binding["roleRef"]["kind"] == "Role"
    assert role_binding["subjects"] == [
        {"kind": "ServiceAccount", "name": "osac-deps-wait-nmstate", "namespace": "osac"}
    ]
    container = pod_spec["containers"][0]
    assert container["command"] == ["/bin/bash", "/scripts/wait-nmstate.sh"]

    hook_scripts = find(docs, "ConfigMap", "osac-deps-hook-scripts")["data"]
    script = hook_scripts["wait-nmstate.sh"]
    assert "status.installedCSV" in script
    assert "status.phase" in script
    assert 'oc wait --for=condition=Available' in script


def profile_values(profile: str) -> tuple[dict[str, Any], dict[str, Any]]:
    if profile == "bmaas-netbox-ci":
        infra = merged_values(
            read_values(INSTALLER / "values/bmaas-ci/infra.yaml"),
            read_values(INSTALLER / "values/bmaas-netbox-ci/infra.yaml"),
        )
        instance = merged_values(
            read_values(INSTALLER / "values/bmaas-ci/instance.yaml"),
            read_values(INSTALLER / "values/bmaas-netbox-ci/instance.yaml"),
        )
        return infra, instance
    return (
        read_values(INSTALLER / f"values/{profile}/infra.yaml"),
        read_values(INSTALLER / f"values/{profile}/instance.yaml"),
    )


def check_profile(profile: str, expected_enabled: bool) -> None:
    infra, instance = profile_values(profile)
    vmaas_enabled = bool(instance.get("global", {}).get("services", {}).get("vmaas", {}).get("enabled", False))
    nmstate_enabled = bool(infra.get("nmstate", {}).get("enabled", False))
    assert vmaas_enabled is expected_enabled, f"unexpected VMaaS configuration in {profile} profile"
    assert nmstate_enabled is expected_enabled, f"NMState must track VMaaS enablement in {profile} profile"

    fixture = INSTALLER / f"values/{profile}/infra.yaml"
    args = ["--values", str(fixture)]
    if profile == "bmaas-netbox-ci":
        args = ["--values", str(INSTALLER / "values/bmaas-ci/infra.yaml"), *args]
    docs = render(*args)
    if expected_enabled:
        assert_enabled_contract(docs)
    else:
        assert_absent(docs)


def main() -> None:
    default_values = read_values(DEPS_CHART / "values.yaml")
    assert default_values["nmstate"] == {"enabled": False, "channel": "stable"}
    assert_absent(render())
    assert_enabled_contract(render("--set", "nmstate.enabled=true"))

    for profile in VMAAS_PROFILES:
        check_profile(profile, expected_enabled=True)
    for profile in NON_VMAAS_PROFILES:
        check_profile(profile, expected_enabled=False)

    print("NMState subscription, readiness hook, and VMaaS profile render checks passed.")


if __name__ == "__main__":
    main()
