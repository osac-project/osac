#!/usr/bin/env python3
"""Check the hub-access ClusterRole renders the Secret management verbs."""

from __future__ import annotations

import pathlib
import re
import subprocess

INSTALLER = pathlib.Path(__file__).resolve().parents[4]
CHART = INSTALLER / "charts/osac"


def test_main() -> None:
    rendered = subprocess.run(
        [
            "helm",
            "template",
            "osac",
            str(CHART),
            "--namespace",
            "osac",
            "--values",
            str(CHART / "ci/default-values.yaml"),
            "--set",
            "hubAccess.enabled=true",
        ],
        check=True,
        capture_output=True,
        text=True,
    ).stdout
    cluster_role = next(
        (
            document
            for document in rendered.split("\n---\n")
            if "kind: ClusterRole\n" in document and "name: osac-hub-access\n" in document
        ),
        None,
    )
    assert cluster_role is not None, "hub-access ClusterRole was not rendered"

    secret_rule = re.search(
        r'(?ms)^  - apiGroups:\n      - ""\n    resources:\n      - secrets\n    verbs:\n((?:      - .+(?:\n|$))+)',
        cluster_role,
    )
    assert secret_rule is not None, "hub-access Secret RBAC rule was not rendered"

    verbs = set(re.findall(r"^      - (.+)$", secret_rule.group(1), re.MULTILINE))
    missing = {"create", "patch"} - verbs
    assert not missing, f"hub-access Secret RBAC rule is missing verbs: {sorted(missing)}"
