#!/usr/bin/env python3
"""Sync config/crd/bases CRDs into charts/operator-crds/templates for Helm."""

from __future__ import annotations

import sys
from pathlib import Path

BASES_DIR = Path("config/crd/bases")
TEMPLATES_DIR = Path("charts/operator-crds/templates")


def inject_resource_policy_annotation(content: str) -> str:
    if "helm.sh/resource-policy" in content:
        return content

    annotations_anchor = "  annotations:\n"
    if annotations_anchor in content:
        updated = content.replace(
            annotations_anchor,
            annotations_anchor + '    "helm.sh/resource-policy": keep\n',
            1,
        )
        if updated != content:
            return updated

    metadata_anchor = "metadata:\n"
    if metadata_anchor in content:
        updated = content.replace(
            metadata_anchor,
            metadata_anchor
            + "  annotations:\n"
            + '    "helm.sh/resource-policy": keep\n',
            1,
        )
        if updated != content:
            return updated

    raise SystemExit(
        "Could not inject helm.sh/resource-policy: keep (metadata section not found)"
    )


def wrap_for_helm(content: str) -> str:
    return inject_resource_policy_annotation(content)


def sync_crds() -> None:
    if not BASES_DIR.is_dir():
        raise SystemExit(f"CRD bases directory not found: {BASES_DIR}")

    TEMPLATES_DIR.mkdir(parents=True, exist_ok=True)

    base_files = sorted(BASES_DIR.glob("*.yaml"))
    if not base_files:
        raise SystemExit(f"No CRD YAML files found in {BASES_DIR}")

    expected_names = {path.name for path in base_files}

    for src in base_files:
        dst = TEMPLATES_DIR / src.name
        dst.write_text(wrap_for_helm(src.read_text(encoding="utf-8")), encoding="utf-8")
        print(f"  {src.name}")

    for stale in sorted(TEMPLATES_DIR.glob("osac.openshift.io_*.yaml")):
        if stale.name not in expected_names:
            stale.unlink()
            print(f"  removed stale template {stale.name}")


def main() -> int:
    print(f"Syncing CRDs from {BASES_DIR} to {TEMPLATES_DIR}...")
    sync_crds()
    print("Done.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
