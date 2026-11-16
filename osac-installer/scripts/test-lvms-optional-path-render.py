#!/usr/bin/env python3
"""Verify the OSAC-managed LVMCluster uses only its optional worker allowlist."""

from __future__ import annotations

import pathlib
import shutil
import subprocess
import tempfile
from typing import Any

import yaml

INSTALLER = pathlib.Path(__file__).resolve().parents[1]
INFRA_CHART = INSTALLER / "charts/osac-infra"
LVMS_CONFIG = ("ConfigMap", "osac-infra-lvms-config")
DEFAULT_DEVICE_PATH = "/dev/sample-osac-disk"
PROFILE_VALUES = {
    "vmaas-ci": (
        INSTALLER / "values/vmaas-ci/infra.yaml",
        ["/dev/sdb", "/dev/vdb"],
    ),
    "bmaas-ci": (
        INSTALLER / "values/bmaas-ci/infra.yaml",
        ["/dev/sdb", "/dev/vdb"],
    ),
    "caas-ci": (INSTALLER / "values/caas-ci/infra.yaml", ["/dev/sdb"]),
    "full-ci": (INSTALLER / "values/full-ci/infra.yaml", ["/dev/vdb"]),
}
WORKER_LABEL = "node-role.kubernetes.io/worker"


def render(chart: pathlib.Path, *args: str) -> list[dict[str, Any]]:
    command = ["helm", "template", "osac-infra", str(chart), *args]
    result = subprocess.run(command, check=False, capture_output=True, text=True)
    if result.returncode:
        raise RuntimeError(f"Helm render failed: {' '.join(command)}\n{result.stderr}")
    return [doc for doc in yaml.safe_load_all(result.stdout) if isinstance(doc, dict)]


def find_resource(docs: list[dict[str, Any]], kind: str, name: str) -> dict[str, Any]:
    matches = [
        doc
        for doc in docs
        if doc.get("kind") == kind and doc.get("metadata", {}).get("name") == name
    ]
    assert len(matches) == 1, f"expected one {kind}/{name}, found {len(matches)}"
    return matches[0]


def main() -> None:
    with tempfile.TemporaryDirectory(prefix="lvms-optional-path-chart-") as temp_dir:
        chart = pathlib.Path(temp_dir) / "osac-infra"
        templates = chart / "templates"
        templates.mkdir(parents=True)
        (chart / "Chart.yaml").write_text(
            "apiVersion: v2\nname: osac-infra\ntype: application\nversion: 0.0.1\n",
            encoding="utf-8",
        )
        shutil.copy2(INFRA_CHART / "values.yaml", chart / "values.yaml")
        shutil.copy2(INFRA_CHART / "templates/_helpers.tpl", templates / "_helpers.tpl")
        shutil.copy2(INFRA_CHART / "templates/lvms.yaml", templates / "lvms.yaml")

        disabled = render(chart)
        assert not any(
            doc.get("kind") == LVMS_CONFIG[0]
            and doc.get("metadata", {}).get("name") == LVMS_CONFIG[1]
            for doc in disabled
        ), "LVMS configuration must remain disabled by default"

        render_cases = [
            ("chart default", ("--set", "lvms.enabled=true"), [DEFAULT_DEVICE_PATH])
        ]
        render_cases.extend(
            (
                profile,
                ("--values", str(values_file)),
                expected_path,
            )
            for profile, (values_file, expected_path) in PROFILE_VALUES.items()
        )
        for profile, values_args, expected_paths in render_cases:
            enabled = render(chart, *values_args)
            config_map = find_resource(enabled, *LVMS_CONFIG)
            lvm_cluster = yaml.safe_load(config_map["data"]["config.yaml"])
            device_classes = lvm_cluster["spec"]["storage"]["deviceClasses"]
            assert len(device_classes) == 1 and device_classes[0]["name"] == "vg1"

            device_class = device_classes[0]
            assert device_class["nodeSelector"] == {
                "nodeSelectorTerms": [
                    {"matchExpressions": [{"key": WORKER_LABEL, "operator": "Exists"}]}
                ]
            }
            assert device_class["deviceSelector"] == {
                "optionalPaths": expected_paths
            }, f"{profile} must use only its explicit optional paths {expected_paths}"
    print("LVMS optional-path render checks passed.")


if __name__ == "__main__":
    main()
