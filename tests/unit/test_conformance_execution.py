"""Contract tests for the pytest-backed conformance execution adapter."""

from __future__ import annotations

import sys
from pathlib import Path

from tests.conformance.model import Classification, Layer, Result, RunContext
from tests.conformance.selection import compute_plan
from tests.conformance.testtree import PytestTestTree


def _tree(tmp_path: Path) -> Path:
    tree = tmp_path / "tree"
    tree.mkdir()
    (tree / "test_sample.py").write_text(
        "import pytest\n"
        "@pytest.mark.conformance_generic\n"
        "@pytest.mark.sig_networking\n"
        "@pytest.mark.conformance_blocking\n"
        "def test_passes():\n"
        "    print('pass output')\n"
        "@pytest.mark.conformance_generic\n"
        "@pytest.mark.sig_networking\n"
        "@pytest.mark.conformance_blocking\n"
        "def test_fails():\n"
        "    assert False, 'expected failure'\n"
    )
    return tree


def test_collect_reads_markers_into_identities(tmp_path: Path) -> None:
    tree = PytestTestTree(root=_tree(tmp_path), python=sys.executable)

    tests = tree.collect()

    assert len(tests) == 2
    assert all(test.area == "networking" for test in tests)
    assert all(test.classification is Classification.BLOCKING for test in tests)
    assert all(test.layer is Layer.COMPONENT for test in tests)


def test_run_maps_pytest_outcomes_to_conformance_results(tmp_path: Path) -> None:
    tree = PytestTestTree(root=_tree(tmp_path), python=sys.executable)
    plan = compute_plan(tree.collect(), ["generic"])
    context = RunContext(run_id="abcdefgh", results_dir=tmp_path / "results", config=None)  # type: ignore[arg-type]

    outcomes = tree.run(plan, context, keep=False)

    by_id = {outcome.identity.id: outcome for outcome in outcomes}
    assert by_id["test_passes"].result is Result.PASS
    assert by_id["test_fails"].result is Result.FAIL
    assert "expected failure" in by_id["test_fails"].message
