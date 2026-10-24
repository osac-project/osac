"""Shared fixtures for the coverage reporter tests.

Each test builds a tiny throwaway git repository, so the reporter is exercised
against a real ``git rev-parse HEAD`` without touching this checkout. The YAML
inputs live in ``tests/fixtures/``.
"""

import json
import os
import subprocess
from pathlib import Path
from typing import Optional

FIXTURES = Path(__file__).resolve().parent / "fixtures"


def fixture(name: str) -> str:
    """Return the text of a fixture file."""
    return (FIXTURES / name).read_text(encoding="utf-8")


FEATURES_YAML = fixture("features.yaml")
CONFIG_YAML = fixture("coverage-skill.yaml")
AMBIGUOUS_FEATURES_YAML = fixture("ambiguous-features.yaml")


def _git(root: Path, *args: str, env: Optional[dict] = None) -> str:
    completed = subprocess.run(
        ["git", *args],
        cwd=str(root),
        check=True,
        capture_output=True,
        text=True,
        env=env,
    )
    return completed.stdout.strip()


def make_repo(
    root: Path,
    *,
    features: str = FEATURES_YAML,
    config: Optional[str] = CONFIG_YAML,
    commit: bool = True,
) -> Path:
    """Create a repository fixture and return its root."""
    root = Path(root)
    (root / "config").mkdir(parents=True, exist_ok=True)
    (root / "config" / "features.yaml").write_text(features, encoding="utf-8")
    if config is not None:
        (root / ".coverage-skill.yaml").write_text(config, encoding="utf-8")
    (root / "README.md").write_text("fixture\n", encoding="utf-8")

    if commit:
        _git(root, "init", "-q")
        _git(root, "config", "user.email", "fixture@example.com")
        _git(root, "config", "user.name", "Coverage Fixture")
        _git(root, "add", ".")
        env = os.environ.copy()
        env["GIT_AUTHOR_DATE"] = "2026-01-01T00:00:00Z"
        env["GIT_COMMITTER_DATE"] = "2026-01-01T00:00:00Z"
        _git(root, "commit", "-q", "-m", "fixture", env=env)
    return root


def head(root: Path) -> str:
    """Return the fixture repository's HEAD sha."""
    return _git(Path(root), "rev-parse", "HEAD")


def read_json(path: Path) -> object:
    with open(str(path), "r", encoding="utf-8") as handle:
        return json.load(handle)
