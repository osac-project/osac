"""Command line entry point for the feature coverage reporter."""

from __future__ import annotations

import argparse
import difflib
import json
import os
import subprocess
import sys
from typing import List, Optional

from .inputs import InputError, load_feature_paths, parse_coverprofiles, resolve_feature
from .report import build_feature_report, render_markdown

DEFAULT_OUT = os.path.join(".artifacts", "coverage-report")
FEATURES_FILE = "features.yaml"


def _git(repo_root: str, *args: str) -> str:
    try:
        result = subprocess.run(
            ["git", "-C", repo_root] + list(args),
            check=True,
            capture_output=True,
            text=True,
        )
        return result.stdout.strip()
    except (OSError, subprocess.CalledProcessError):
        return ""


def _repo_root(value: Optional[str]) -> str:
    if value:
        return os.path.abspath(value)
    return _git(os.getcwd(), "rev-parse", "--show-toplevel") or os.getcwd()


def _commit(repo_root: str) -> str:
    return _git(repo_root, "rev-parse", "HEAD")


def _features_path(repo_root: str) -> str:
    return os.path.join(repo_root, "tools", "coverage-report", FEATURES_FILE)


def _unknown_feature(query: str, choices: List[str]) -> int:
    print("Unknown feature: {0}".format(query), file=sys.stderr)
    matches = difflib.get_close_matches(query, choices, n=3, cutoff=0.3)
    if matches:
        print("Did you mean: {0}".format(", ".join(matches)), file=sys.stderr)
    else:
        print("Known features: {0}".format(", ".join(choices)), file=sys.stderr)
    return 2


def _previous_bytes(path: str) -> Optional[bytes]:
    """Return the bytes to restore, or ``None`` when the file is absent."""
    try:
        with open(path, "rb") as handle:
            return handle.read()
    except FileNotFoundError:
        return None


def _restore(path: str, previous: Optional[bytes]) -> None:
    try:
        if previous is None:
            os.remove(path)
        else:
            with open(path, "wb") as handle:
                handle.write(previous)
    except OSError:
        pass


def _write_report(directory: str, report: dict) -> None:
    """Publish ``report.json`` and ``report.md`` together, or neither."""
    os.makedirs(directory, exist_ok=True)
    json_path = os.path.join(directory, "report.json")
    markdown_path = os.path.join(directory, "report.md")
    staged = [
        (json_path, json.dumps(report, indent=2, sort_keys=True) + "\n"),
        (markdown_path, render_markdown(report)),
    ]
    published = []
    try:
        for path, text in staged:
            with open(path + ".tmp", "w", encoding="utf-8") as handle:
                handle.write(text)
        for path, _text in staged:
            previous = _previous_bytes(path)
            os.replace(path + ".tmp", path)
            published.append((path, previous))
    except OSError as exc:
        for path, previous in reversed(published):
            _restore(path, previous)
        raise OSError("cannot write report to {0}: {1}".format(directory, exc)) from exc
    finally:
        for path, _text in staged:
            temporary = path + ".tmp"
            if os.path.exists(temporary):
                try:
                    os.remove(temporary)
                except OSError:
                    pass
    print(json_path)
    print(markdown_path)


def main(argv: Optional[List[str]] = None) -> int:
    parser = argparse.ArgumentParser(
        prog="coverage-report",
        description="Generate a Feature x test-tier coverage report from the OSAC repository.",
    )
    parser.add_argument("feature", nargs="?", help="Jira key, name, or alias; omit for all features")
    parser.add_argument("--coverprofile", action="append", default=[], metavar="PATH",
                        help="Go coverprofile to measure; repeat to merge")
    parser.add_argument("--features", action="append", default=[], metavar="PATH",
                        help="config file or directory of *.yaml; repeat to merge")
    parser.add_argument("--group", default=None, metavar="WG", help="only areas in this working group")
    parser.add_argument("--allow-external-root", action="append", default=[], metavar="PATH", help="trust an external root for test plans; repeat to add")
    parser.add_argument("--out", default=DEFAULT_OUT, metavar="DIR", help="output directory")
    parser.add_argument("--repo", default=None, metavar="PATH", help="repository root")
    args = parser.parse_args(argv)

    repo_root = _repo_root(args.repo)
    try:
        features = load_feature_paths(args.features or [_features_path(repo_root)])
    except InputError as exc:
        print("coverage-report: {0}".format(exc), file=sys.stderr)
        return 2

    selected = features
    if args.group:
        wanted = args.group.casefold()
        selected = [feature for feature in features if feature.group.casefold() == wanted]
        if not selected:
            print("Unknown group: {0}".format(args.group), file=sys.stderr)
            return 2
    choices: List[str] = []
    for feature in selected:
        choices.extend([feature.key, feature.name] + list(feature.aliases))
    if args.feature:
        feature = resolve_feature(selected, args.feature)
        if feature is None:
            return _unknown_feature(args.feature, choices)
        selected = [feature]

    try:
        packages = parse_coverprofiles(args.coverprofile) if args.coverprofile else None
    except InputError as exc:
        print("coverage-report: {0}".format(exc), file=sys.stderr)
        return 2

    commit = _commit(repo_root)
    try:
        reports = [
            build_feature_report(feature, repo_root, commit, packages, args.allow_external_root) for feature in selected
        ]
    except InputError as exc:
        print("coverage-report: {0}".format(exc), file=sys.stderr)
        return 2
    groups = sorted({item["feature"]["group"] for item in reports if item["feature"]["group"]})
    group_list = [{"key": group} for group in groups]
    if args.feature:
        report = reports[0]
        report["groups"] = group_list
        directory = os.path.join(args.out, selected[0].key)
    else:
        report = {"commit": commit, "groups": group_list, "features": reports, "overview": [
            {"key": item["feature"]["key"],
             "area": item["feature"]["name"] or item["feature"]["key"],
             "group": item["feature"]["group"], "verdict": item["verdict"],
             "verdict_reason": item["verdict_reason"],
             **{tier: {"state": item["metrics"][tier]["state"],
                       "display": item["metrics"][tier]["display"]}
                for tier in ("unit", "integration", "e2e")},
             "gaps": {"critical": sum(gap["priority"] == "critical" for gap in item["gaps"]),
                      "high": sum(gap["priority"] == "high" for gap in item["gaps"]),
                      "total": len(item["gaps"])}}
            for item in reports]}
        directory = os.path.join(args.out, selected[0].group if args.group else "ALL")
    try:
        _write_report(directory, report)
    except OSError as exc:
        print("coverage-report: cannot write report to {0}: {1}".format(directory, exc), file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    sys.exit(main())
