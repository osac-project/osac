#!/usr/bin/env python3
"""Check CaaS profile ignition trust and named ingress-CA read access offline."""

from __future__ import annotations

import subprocess

import yaml
from _render import select_one, template_command

TRUST_ENV = "OSAC_IGNITION_TRUST_INGRESS_CA"


def render(profile: str, *extra: str) -> list[str]:
    result = subprocess.run([*template_command(profile), *extra], capture_output=True, text=True, timeout=300)
    assert result.returncode == 0, result.stderr
    return [doc for doc in result.stdout.split("\n---\n") if doc.strip()]


def operator_resource(docs: list[str], template: str) -> dict:
    source = f"# Source: osac/charts/operator/templates/{template}.yaml\n"
    return yaml.safe_load(select_one(docs, lambda doc: source in doc, source.strip()))


def manager(deployment: dict) -> dict:
    containers = deployment["spec"]["template"]["spec"]["containers"]
    (container,) = [container for container in containers if container["name"] == "manager"]
    return container


def check_profile(profile: str) -> None:
    docs = render(profile)
    deployment = operator_resource(docs, "deployment")
    container = manager(deployment)
    trust = [entry for entry in container["env"] if entry["name"] == TRUST_ENV]
    assert trust == [{"name": TRUST_ENV, "value": "true"}], f"{profile}: ingress CA trust must be enabled"

    role = operator_resource(docs, "clusterrole")
    assert any(
        rule.get("apiGroups") == [""]
        and rule.get("resources") == ["configmaps"]
        and rule.get("resourceNames") == ["default-ingress-cert"]
        and rule.get("verbs") == ["get"]
        for rule in role["rules"]
    ), f"{profile}: operator must be able to read the named ingress CA ConfigMap"
    binding = operator_resource(docs, "clusterrolebinding")
    assert binding["roleRef"] == {
        "apiGroup": "rbac.authorization.k8s.io",
        "kind": "ClusterRole",
        "name": role["metadata"]["name"],
    }, profile
    assert {
        "kind": "ServiceAccount",
        "name": deployment["spec"]["template"]["spec"]["serviceAccountName"],
        "namespace": deployment["metadata"]["namespace"],
    } in binding["subjects"], profile

    disabled = render(profile, "--set", "operator.ignition.trustIngressCA=false")
    disabled_container = manager(operator_resource(disabled, "deployment"))
    assert all(entry["name"] != TRUST_ENV for entry in disabled_container["env"]), profile
    assert disabled_container == {
        **container,
        "env": [entry for entry in container["env"] if entry["name"] != TRUST_ENV],
    }, f"{profile}: disabling ingress CA trust must not change other transport settings"


if __name__ == "__main__":
    for profile in ("caas-ci", "full-ci"):
        check_profile(profile)
        print(f"{profile}: ingress CA trust, RBAC, and explicit opt-out render checks passed.")
