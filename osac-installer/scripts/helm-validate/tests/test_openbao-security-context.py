#!/usr/bin/env python3
"""Check the OpenBao pod securityContext renders correctly per platform.

Kind profiles must include a numeric runAsUser (the OpenBao image declares
USER openbao, a non-numeric name; Kubernetes cannot verify runAsNonRoot
without a numeric UID). OpenShift CI profiles must omit runAsUser so the
restricted-v2 SCC can allocate from the namespace UID range. Both must
include seccompProfile.type RuntimeDefault.
"""

from __future__ import annotations

import pathlib
import subprocess

import yaml

INSTALLER = pathlib.Path(__file__).resolve().parents[3]
INFRA_CHART = INSTALLER / "charts/osac-infra"


def openbao_pod_security_context(values_file: str) -> dict:
    manifest = subprocess.run(
        [
            "helm",
            "template",
            "osac-infra",
            str(INFRA_CHART),
            "--values",
            values_file,
        ],
        check=True,
        capture_output=True,
        text=True,
        cwd=INSTALLER,
    ).stdout
    for doc in yaml.safe_load_all(manifest):
        if not isinstance(doc, dict) or doc.get("kind") != "Deployment":
            continue
        if (doc.get("metadata", {}).get("labels") or {}).get("app") != "openbao":
            continue
        return doc["spec"]["template"]["spec"]["securityContext"]
    raise AssertionError(f"OpenBao Deployment pod securityContext not found for {values_file}")


def test_main() -> None:
    kind_sc = openbao_pod_security_context("values/dev/kind-infra.yaml")
    assert "runAsUser" in kind_sc, "Kind profile must include a numeric runAsUser for OpenBao"
    assert kind_sc.get("runAsNonRoot") is True, "Kind profile must include runAsNonRoot: true"
    assert kind_sc.get("seccompProfile", {}).get("type") == "RuntimeDefault", (
        "Kind profile must include seccompProfile type RuntimeDefault"
    )

    ocp_sc = openbao_pod_security_context("values/vmaas-ci/infra.yaml")
    assert "runAsUser" not in ocp_sc, (
        "OpenShift CI profile must NOT include runAsUser (SCC allocates UIDs)"
    )
    assert ocp_sc.get("runAsNonRoot") is True, "OpenShift CI profile must include runAsNonRoot: true"
    assert ocp_sc.get("seccompProfile", {}).get("type") == "RuntimeDefault", (
        "OpenShift CI profile must include seccompProfile type RuntimeDefault"
    )
