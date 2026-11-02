#!/usr/bin/env python3
"""Fail when a configuration file declares the same key twice.

Every JSON parser keeps the last duplicate key and silently drops the rest, and
the YAML parser GitHub uses for workflows behaves the same way. A second copy of
a key can therefore sit in the tree for months without taking effect: PR #1418
added a second ``operator.stall`` above the one PR #1311 had added, so every
parse silently used #1311's block.

Usage:
    check-duplicate-keys.py [PATH ...]

Without arguments it scans the chart values, schemas and preflight JSON files,
the installer profile values, and the GitHub workflow and filter definitions.
"""

from __future__ import annotations

import json
import sys
from collections.abc import Callable
from pathlib import Path

import yaml

REPO_ROOT = Path(__file__).resolve().parents[2]

DEFAULT_GLOBS = (
    "*/charts/**/values*.yaml",
    "*/charts/**/values*.json",
    "*/charts/**/files/**/*.json",
    "*/values/**/*.yaml",
    ".github/**/*.yml",
    ".github/**/*.yaml",
)


class _JsonObject(list):
    """A decoded JSON object, kept as its ordered (key, value) pairs."""


def _json_object(pairs: list[tuple[str, object]]) -> _JsonObject:
    """object_pairs_hook that keeps duplicate keys instead of dropping them."""
    return _JsonObject(pairs)


def _walk_json(node: object, path: str, problems: list[str]) -> None:
    """Collect duplicate keys in a decoded JSON node, recursing into values."""
    if isinstance(node, _JsonObject):
        seen = set()
        for key, value in node:
            if key in seen:
                problems.append(f"{path or '<root>'}: duplicate key {key!r}")
            seen.add(key)
            _walk_json(value, f"{path}.{key}", problems)
    elif isinstance(node, list):
        for index, value in enumerate(node):
            _walk_json(value, f"{path}[{index}]", problems)


def _yaml_key(key_node: yaml.Node, resolve: Callable[[yaml.Node], object]) -> tuple[object, str]:
    """Return the comparison value and the source text of a mapping key.

    Scalars are compared by the value a YAML 1.1 loader constructs, so ``true``
    and ``True`` collide exactly as they do for Helm and GitHub Actions. The
    source text is kept for the diagnostic.
    """
    if isinstance(key_node, yaml.ScalarNode):
        try:
            return resolve(key_node), key_node.value
        except yaml.YAMLError:
            # A tag with no constructor (a ``<<`` merge key, for one) is
            # compared by its tag and source text.
            return (key_node.tag, key_node.value), key_node.value
    # A collection used as a key compares by identity; its text is opaque.
    return key_node, key_node.value


def _walk_yaml(
    node: yaml.Node, path: str, problems: list[str], resolve: Callable[[yaml.Node], object]
) -> None:
    """Collect duplicate keys in a composed YAML node, recursing into values."""
    if isinstance(node, yaml.MappingNode):
        seen = set()
        for key_node, value_node in node.value:
            key, text = _yaml_key(key_node, resolve)
            if key in seen:
                problems.append(
                    f"{path or '<root>'}: duplicate key {text!r} at line {key_node.start_mark.line + 1}"
                )
            seen.add(key)
            _walk_yaml(value_node, f"{path}.{text}", problems, resolve)
    elif isinstance(node, yaml.SequenceNode):
        for index, value in enumerate(node.value):
            _walk_yaml(value, f"{path}[{index}]", problems, resolve)


def problems_in(path: Path) -> list[str]:
    """Every duplicate-key problem declared by one configuration file."""
    text = path.read_text(encoding="utf-8")
    problems: list[str] = []
    if path.suffix == ".json":
        _walk_json(json.loads(text, object_pairs_hook=_json_object), "", problems)
    else:
        # Resolve keys through a loader so YAML-equivalent spellings compare
        # equal, while composing again with SafeLoader keeps line numbers.
        resolver = yaml.SafeLoader("")
        try:
            for document in yaml.compose_all(text, Loader=yaml.SafeLoader):
                if document is not None:
                    _walk_yaml(document, "", problems, resolver.construct_object)
        finally:
            resolver.dispose()
    return problems


def display(path: Path) -> str:
    """Path relative to the repository, or absolute when it lives outside."""
    try:
        return str(path.relative_to(REPO_ROOT))
    except ValueError:
        return str(path)


def selected_paths(arguments: list[str]) -> list[Path]:
    """Paths named on the command line, or the default glob set when omitted."""
    if arguments:
        return [Path(argument) for argument in arguments]
    found: set[Path] = set()
    for pattern in DEFAULT_GLOBS:
        found.update(REPO_ROOT.glob(pattern))
    return sorted(path for path in found if path.is_file())


def main(arguments: list[str]) -> int:
    """Report duplicate keys in every selected file; non-zero if any exist."""
    paths = selected_paths(arguments)
    failing = 0
    for path in paths:
        problems = problems_in(path)
        for problem in problems:
            print(f"{display(path)}: {problem}")
        failing += bool(problems)
    if failing:
        print(
            f"\n{failing} file(s) declare a duplicate key. Keep one entry: a duplicate is "
            "silently ignored by every parser, so the earlier copy never takes effect."
        )
        return 1
    print(f"No duplicate keys in {len(paths)} files.")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
