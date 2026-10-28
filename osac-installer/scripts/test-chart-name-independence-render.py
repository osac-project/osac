#!/usr/bin/env python3
"""Check that cross-chart secret/cert names survive repackaging under a different Chart.yaml name.

The real Konflux-built OCI artifact is packaged under name "osac-chart" (to
satisfy Pyxis's -chart naming rule), not "osac" as in this source tree. Any
template keyed off osac.fullname (which depends on .Chart.Name) renders a
different name once the release name ("osac") no longer matches the chart
name -- breaking any sibling chart or values file that references that name
literally. This bit workload-db-secrets.yaml for real; this test renders
under both names and checks the producing resources (not just the plain
values-driven references to them, which stay the same literal string
either way and would false-pass this check) keep the expected names,
without needing a live cluster or a real Konflux build to catch it.
"""

from __future__ import annotations

import pathlib
import shutil
import subprocess
import tempfile
from typing import Any

import yaml

CHART = pathlib.Path(__file__).resolve().parents[1] / "charts/osac"
# "**/" (not "*/", which Helm's --show-only glob does not match across the
# chart-name/templates boundary) so this matches regardless of the chart's
# own declared name -- exactly the thing this test varies.
SHOW_ONLY_GLOB = "**/workload-db-secrets.yaml"

# (kind, field, name): the resource kind and the metadata field whose value
# a sibling chart or values file (service.database.connection,
# metering.database.connection) references by this literal name. "field" is
# "name" for the Secret's own metadata.name, "secretName" for a
# Certificate's spec.secretName (the Secret cert-manager creates from it).
EXPECTED_RESOURCES = (
    ("Secret", "name", "osac-db-config"),
    ("Certificate", "secretName", "osac-db-client-cert"),
    ("Secret", "name", "osac-db-metering-config"),
    ("Certificate", "secretName", "osac-db-metering-client-cert"),
)


def render(chart_dir: pathlib.Path) -> list[dict[str, Any]]:
    cmd = [
        "helm",
        "template",
        "osac",
        str(chart_dir),
        "--values",
        str(CHART / "ci/default-values.yaml"),
        "--set",
        "metering.enabled=true",
        "--set",
        "global.osacDeploymentId=test/osac",
        "--show-only",
        SHOW_ONLY_GLOB,
    ]
    manifest = subprocess.run(cmd, check=True, capture_output=True, text=True).stdout
    return [doc for doc in yaml.safe_load_all(manifest) if doc]


def check_producing_resources(docs: list[dict[str, Any]], context: str) -> None:
    for kind, field, name in EXPECTED_RESOURCES:
        if field == "name":
            matches = [doc for doc in docs if doc.get("kind") == kind and doc.get("metadata", {}).get("name") == name]
        else:
            matches = [doc for doc in docs if doc.get("kind") == kind and doc.get("spec", {}).get(field) == name]
        assert matches, (
            f"{context}: expected exactly one {kind} with {field}={name!r} rendered from "
            f"{SHOW_ONLY_GLOB}, found none -- the producing resource's name changed, even "
            "though sibling charts/values still reference it by this fixed literal"
        )


def main() -> None:
    docs = render(CHART)
    check_producing_resources(docs, "real chart name (osac)")

    with tempfile.TemporaryDirectory() as tmp:
        renamed_chart = pathlib.Path(tmp) / "osac"
        shutil.copytree(CHART, renamed_chart, symlinks=True)
        chart_yaml = renamed_chart / "Chart.yaml"
        text = chart_yaml.read_text(encoding="utf-8")
        patched = text.replace("name: osac\n", "name: osac-chart\n", 1)
        assert patched != text, "Chart.yaml's name: osac line was not found to patch"
        chart_yaml.write_text(patched, encoding="utf-8")

        renamed_docs = render(renamed_chart)
        check_producing_resources(renamed_docs, "simulated published chart name (osac-chart)")

    print("Chart name independence render checks passed.")


if __name__ == "__main__":
    main()
