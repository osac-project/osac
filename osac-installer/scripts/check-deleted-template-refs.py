#!/usr/bin/env python3
"""Fail when a chart template deleted in this change is still named by a test.

Usage:
    check-deleted-template-refs.py BASE_REF

Lists the templates removed on this branch under osac-installer/charts and looks
for the identifiers that template contributed to the rendered output:

* its chart-relative path (``operator/templates/deployment.yaml``), which is how
  a render test selects a document from a ``# Source:`` comment or passes
  ``--show-only``;
* the literal parts of the resource names it declared (``pre-install-validate``
  from ``name: {{ include "osac.fullname" . }}-pre-install-validate``), which is
  how a render test asserts a document it produced.

A deleted template whose identifiers still appear in a test usually means the
test still asserts the resource that template rendered: PR #1845 removed
hooks/pre-install-validate.yaml while scripts/test-bmf-netbox-render.py still
asserted Job/osac-pre-install-validate, and that test failed on main until the
reference was removed.

Matching whole names keeps a generic word from failing the build: deleting
``templates/deployment.yaml`` no longer trips over tests that mention a
``metering/templates/deployment.yaml`` that still exists.

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
TEMPLATE_SUFFIXES = {".yaml", ".yml"}
# Resource names live next to metadata (indented two to four spaces); deeper
# ``name:`` keys are container, port and env-entry names, which are not what a
# render test selects on.
NAME_LINE = re.compile(r"^[ \t]{0,4}name:[ \t]*(.*?)[ \t]*$", re.MULTILINE)
TEMPLATED = re.compile(r"\{\{.*?\}\}")


def merge_base(base_ref: str) -> str:
    """The commit where this branch diverged from *base_ref*."""
    result = subprocess.run(
        ["git", "merge-base", base_ref, "HEAD"],
        cwd=REPO_ROOT,
        check=True,
        capture_output=True,
        text=True,
    )
    return result.stdout.strip()


def deleted_templates(base_ref: str) -> list[str]:
    """Chart templates removed between *base_ref* and HEAD."""
    command = [
        "git",
        "diff",
        "--no-renames",
        "--diff-filter=D",
        "--name-only",
        f"{base_ref}...HEAD",
        "--",
        "osac-installer/charts",
    ]
    result = subprocess.run(command, cwd=REPO_ROOT, check=True, capture_output=True, text=True)
    return [
        line
        for line in result.stdout.splitlines()
        if "/templates/" in line and Path(line).suffix in TEMPLATE_SUFFIXES
    ]


def path_fragment(template: str) -> str:
    """The template path as rendered ``# Source:`` lines spell it."""
    parts = Path(template).parts
    index = parts.index("templates")
    return "/".join(parts[index - 1 :])


def name_fragments(base_ref: str, template: str) -> set[str]:
    """Literal parts of the resource names the deleted template declared."""
    result = subprocess.run(
        ["git", "show", f"{base_ref}:{template}"],
        cwd=REPO_ROOT,
        check=True,
        capture_output=True,
        text=True,
    )
    fragments: set[str] = set()
    for match in NAME_LINE.finditer(result.stdout):
        for piece in TEMPLATED.split(match.group(1)):
            piece = piece.strip().strip("'\"-")
            # A single word ("ui", "report") is too generic to attribute to one
            # chart; the path fragment still covers the file itself.
            if "-" in piece and len(piece) >= 6:
                fragments.add(piece)
    return fragments


def references(identifiers: set[str]) -> list[str]:
    """Lines in the searched trees that name any of *identifiers*."""
    # A name is often prefixed (osac-pre-install-validate), so a hyphen may
    # precede it; only a word character may not.
    patterns = [
        re.compile(rf"(?<!\w){re.escape(identifier)}(?!\w)") for identifier in sorted(identifiers)
    ]
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
                if any(pattern.search(line) for pattern in patterns):
                    hits.append(f"{path.relative_to(REPO_ROOT)}:{number}: {line.strip()}")
    return hits


def main(arguments: list[str]) -> int:
    """Check every deleted template for references; non-zero when any remain."""
    if len(arguments) != 1:
        print(__doc__)
        return 2
    base = merge_base(arguments[0])
    templates = deleted_templates(base)
    failing = 0
    for template in templates:
        identifiers = {path_fragment(template)} | name_fragments(base, template)
        hits = references(identifiers)
        if not hits:
            continue
        failing += 1
        print(f"{template} was deleted but its identifiers are still referenced:")
        for identifier in sorted(identifiers):
            print(f"  identifier: {identifier}")
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
