#!/usr/bin/env python3
"""Fail when a chart template deleted in this change is still named by a test.

Usage:
    check-deleted-template-refs.py BASE_REF

Lists the templates removed between BASE_REF and HEAD under osac-installer/charts
and looks for each file's stem in the installer scripts and the repository tests.
A deleted template whose name still appears in a test usually means the test
still asserts the resource that template rendered: PR #1845 removed
hooks/pre-install-validate.yaml while scripts/test-bmf-netbox-render.py still
asserted Job/osac-pre-install-validate, and that test failed on main until the
reference was removed.

Documentation is not searched: install guides deliberately mention retired
objects so operators know they are gone.
"""

from __future__ import annotations

import re
import subprocess
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
# A line that names a retired template on purpose (the retired-hook registry,
# for instance) carries this marker and is skipped.
ALLOW_MARKER = "allow-retired-reference"
SEARCHED_ROOTS = (
    REPO_ROOT / "osac-installer/scripts",
    REPO_ROOT / "tests",
)


def deleted_templates(base_ref: str) -> list[str]:
    command = [
        "git",
        "diff",
        "--diff-filter=D",
        "--name-only",
        f"{base_ref}...HEAD",
        "--",
        "osac-installer/charts",
    ]
    result = subprocess.run(command, cwd=REPO_ROOT, check=True, capture_output=True, text=True)
    return [line for line in result.stdout.splitlines() if line.strip()]


def references(stem: str) -> list[str]:
    # Resource names are often prefixed (osac-pre-install-validate), so a
    # hyphen may precede the stem; only a word character may not.
    pattern = re.compile(rf"(?<!\w){re.escape(stem)}(?!\w)")
    hits: list[str] = []
    me = Path(__file__).resolve()
    for root in SEARCHED_ROOTS:
        if not root.is_dir():
            continue
        for path in sorted(root.rglob("*")):
            if not path.is_file() or path.suffix not in {".py", ".sh", ".j2", ".yaml", ".yml"}:
                continue
            if path.resolve() == me:
                # This file documents the failure mode with a real example.
                continue
            try:
                text = path.read_text(encoding="utf-8")
            except UnicodeDecodeError:
                continue
            for number, line in enumerate(text.splitlines(), start=1):
                if ALLOW_MARKER in line:
                    continue
                if pattern.search(line):
                    hits.append(f"{path.relative_to(REPO_ROOT)}:{number}: {line.strip()}")
    return hits


def main(arguments: list[str]) -> int:
    if len(arguments) != 1:
        print(__doc__)
        return 2
    base_ref = arguments[0]
    templates = deleted_templates(base_ref)
    failing = 0
    for template in templates:
        stem = Path(template).stem
        hits = references(stem)
        if not hits:
            continue
        failing += 1
        print(f"{template} was deleted but {stem!r} is still referenced:")
        for hit in hits:
            print(f"  {hit}")
    if failing:
        print(
            "\nUpdate or remove those references, move them to the replacement "
            f"resource, or mark an intentional mention with {ALLOW_MARKER!r}."
        )
        return 1
    print(f"No deleted chart template is still referenced ({len(templates)} deleted).")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
