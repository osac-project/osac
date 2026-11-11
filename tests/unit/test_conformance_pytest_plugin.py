"""Contract tests for the conformance pytest collection and result plugin."""

from __future__ import annotations

import json
import os
import subprocess
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parents[2]


def _run_plugin(tmp_path: Path, *args: str) -> subprocess.CompletedProcess[str]:
    env = os.environ.copy()
    env["OSAC_CONFORMANCE_MANIFEST"] = str(tmp_path / "manifest.json")
    env["OSAC_CONFORMANCE_RESULTS"] = str(tmp_path / "results.jsonl")
    return subprocess.run(
        [sys.executable, "-m", "pytest", "-n", "0", "-p", "no:cacheprovider", "-p", "tests.conformance.pytest_plugin", *args],
        cwd=REPO,
        env=env,
        text=True,
        capture_output=True,
        check=False,
    )


def _tree(tmp_path: Path) -> Path:
    tree = tmp_path / "tree"
    tree.mkdir()
    (tree / "test_sample.py").write_text(
        "import pytest\n"
        "@pytest.mark.conformance_generic\n"
        "@pytest.mark.sig_networking\n"
        "@pytest.mark.conformance_blocking\n"
        "def test_passes():\n"
        "    assert True\n"
        "@pytest.mark.conformance_netris\n"
        "@pytest.mark.sig_networking\n"
        "@pytest.mark.conformance_informing\n"
        "def test_fails():\n"
        "    assert False\n"
        "@pytest.mark.conformance_generic\n"
        "@pytest.mark.sig_networking\n"
        "@pytest.mark.conformance_blocking\n"
        "def test_skips():\n"
        "    pytest.skip('optional')\n"
    )
    return tree


def test_collection_writes_a_marker_manifest(tmp_path: Path) -> None:
    tree = _tree(tmp_path)
    result = _run_plugin(tmp_path, "--collect-only", "-q", str(tree))
    assert result.returncode == 0, result.stderr

    manifest = json.loads((tmp_path / "manifest.json").read_text())
    assert {item["name"] for item in manifest} == {"test_passes", "test_fails", "test_skips"}
    assert "conformance_generic" in manifest[0]["markers"]
    assert "sig_networking" in manifest[0]["markers"]


def test_run_writes_pass_fail_and_skip_outcomes(tmp_path: Path) -> None:
    tree = _tree(tmp_path)
    result = _run_plugin(tmp_path, "-q", str(tree))
    assert result.returncode == 1, result.stdout + result.stderr

    lines = [json.loads(line) for line in (tmp_path / "results.jsonl").read_text().splitlines()]
    outcomes = {line["name"]: line["outcome"] for line in lines}
    assert outcomes == {"test_passes": "passed", "test_fails": "failed", "test_skips": "skipped"}
    failed = next(line for line in lines if line["name"] == "test_fails")
    assert "assert False" in failed["message"]
