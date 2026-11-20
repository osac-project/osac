"""Validate that E2E workflow component build contexts match Containerfiles.

Every Go Containerfile that copies go.work requires the mono-repo root as
its build context ("." in the caller workflow's component JSON).  A mismatch
causes ``COPY go.work ./`` to fail with "no such file or directory" at E2E
build time, as happened when the echo-adapter context was set to
"osac-metering" instead of ".".
"""

from __future__ import annotations

import json
import re
from pathlib import Path

import pytest
import yaml

ROOT = Path(__file__).resolve().parents[2]

# E2E caller workflows that pass a components list.
_CALLER_WORKFLOWS = [
    "e2e-caas-full-install.yml",
    "e2e-vmaas-full-install.yml",
]


def _workflow(name: str) -> dict:
    return yaml.safe_load(
        (ROOT / ".github/workflows" / name).read_text()
    )


def _extract_components(workflow: dict) -> list[dict]:
    """Extract the components JSON array from a caller workflow."""
    for job in workflow.get("jobs", {}).values():
        raw = job.get("with", {}).get("components", "")
        if not raw:
            continue
        # Strip GitHub Actions expressions -- they resolve at run
        # time so cannot be parsed here.  Replace each
        # ``"${{ ... }}"`` (quoted expression) with a placeholder
        # string that keeps the JSON valid.
        sanitised = re.sub(
            r'"\$\{\{[^}]*\}\}"', '"__gha_expr__"', raw
        )
        try:
            return json.loads(sanitised)
        except json.JSONDecodeError:
            continue
    return []


def _containerfile_needs_root(containerfile: Path) -> bool:
    """Return True if the Containerfile copies go.work (needs root)."""
    if not containerfile.is_file():
        return False
    text = containerfile.read_text()
    return bool(re.search(r"^COPY\b.*\bgo\.work\b", text, re.MULTILINE))


@pytest.mark.parametrize("workflow_name", _CALLER_WORKFLOWS)
def test_component_context_matches_containerfile(
    workflow_name: str,
) -> None:
    """Components whose Containerfile copies go.work must use root."""
    workflow = _workflow(workflow_name)
    components = _extract_components(workflow)
    assert components, (
        f"{workflow_name}: could not extract components list"
    )

    errors: list[str] = []
    for i, comp in enumerate(components):
        build_type = comp.get("buildType", "containerfile")
        if build_type != "containerfile":
            continue

        cfile_rel = comp.get("containerfile", "Containerfile")
        cfile = ROOT / cfile_rel
        context = comp.get("context", "")

        if _containerfile_needs_root(cfile) and context != ".":
            errors.append(
                f"component[{i}] ({cfile_rel}): context is"
                f" {context!r} but Containerfile copies go.work"
                f" — must be \".\""
            )

    assert not errors, (
        f"{workflow_name} has component build-context mismatches:"
        f"\n  " + "\n  ".join(errors)
    )
