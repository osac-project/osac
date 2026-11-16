"""Contract tests for the pytest-backed conformance execution adapter."""

from __future__ import annotations

import json
import sys
from pathlib import Path

import pytest

from tests.conformance.errors import ConfigError
from tests.conformance.interrupt import Interrupted, InterruptGate
from tests.conformance.model import Classification, Result, RunContext, Selector
from tests.conformance.output import OutputSink
from tests.conformance.redact import Redactor
from tests.conformance.selection import compute_plan
from tests.conformance.testtree import PytestTestTree

_REDACTION_MARKER_C = "redaction-marker-c"


class _FakeProcess:
    """Stand-in for ``subprocess.Popen`` that records what pytest would write."""

    def __init__(self, returncode: int, *, entries: list[dict[str, object]] | None = None, stderr: str = "") -> None:
        self.returncode = returncode
        self.entries = entries or []
        self.stderr = stderr
        self.command: list[str] = []
        self._results = Path("/dev/null")

    def __call__(self, command: list[str], **kwargs: object) -> _FakeProcess:
        self.command = command
        env = kwargs["env"]
        assert isinstance(env, dict)
        self._results = Path(env["OSAC_CONFORMANCE_RESULTS"])
        return self

    def communicate(self) -> tuple[str, str]:
        self._results.write_text("".join(json.dumps(entry) + "\n" for entry in self.entries))
        return "", self.stderr

    def send_signal(self, _signum: int) -> None:
        return None

    def kill(self) -> None:
        return None

    def poll(self) -> int:
        return self.returncode


def _tree(tmp_path: Path) -> Path:
    tree = tmp_path / "tree"
    tree.mkdir()
    (tree / "test_sample.py").write_text(
        "import pytest\n"
        "@pytest.mark.conformance_generic\n"
        "@pytest.mark.conformance_blocking\n"
        "def test_passes():\n"
        "    print('pass output')\n"
        f"    print('api_key={_REDACTION_MARKER_C}')\n"
        "@pytest.mark.conformance_generic\n"
        "@pytest.mark.conformance_blocking\n"
        "def test_fails():\n"
        "    assert False, 'expected failure'\n"
    )
    return tree


def test_collect_reads_markers_into_identities(tmp_path: Path) -> None:
    tree = PytestTestTree(root=_tree(tmp_path), python=sys.executable)

    tests = tree.collect()

    assert len(tests) == 2
    assert all(test.classification is Classification.BLOCKING for test in tests)
    assert all(test.profiles == () for test in tests)


def test_run_maps_pytest_outcomes_to_conformance_results(tmp_path: Path) -> None:
    tree = PytestTestTree(root=_tree(tmp_path), python=sys.executable)
    plan = compute_plan(tree.collect(), [Selector("generic", "marker", ("conformance_generic",))])
    context = RunContext(run_id="abcdefgh", results_dir=tmp_path / "results", config=None)  # type: ignore[arg-type]

    outcomes = tree.run(plan, context, keep=False)

    by_id = {outcome.identity.id: outcome for outcome in outcomes}
    assert by_id["test_sample.py::test_passes"].result is Result.PASS
    assert by_id["test_sample.py::test_fails"].result is Result.FAIL
    assert "expected failure" in by_id["test_sample.py::test_fails"].message


def test_captured_test_output_is_redacted_in_evidence(tmp_path: Path) -> None:
    tree = PytestTestTree(root=_tree(tmp_path), python=sys.executable)
    plan = compute_plan(tree.collect(), [Selector("generic", "marker", ("conformance_generic",))])
    results = tmp_path / "results"
    redactor = Redactor()
    context = RunContext(
        run_id="abcdefgh",
        results_dir=results,
        config=None,  # type: ignore[arg-type]
        redactor=redactor,
        output=OutputSink(results, redactor),
    )

    tree.run(plan, context, keep=False)

    evidence = next((results / "tests").glob("test_sample.py_test_passes-*"))
    log = (evidence / "test.log").read_text()
    assert _REDACTION_MARKER_C not in log
    assert "<redacted>" in log


def test_duplicate_function_names_get_distinct_ids(tmp_path: Path) -> None:
    tree_dir = tmp_path / "tree"
    (tree_dir / "a").mkdir(parents=True)
    (tree_dir / "b").mkdir()
    body = "import pytest\n@pytest.mark.conformance_generic\ndef test_same():\n    assert True\n"
    (tree_dir / "a" / "test_a.py").write_text(body)
    (tree_dir / "b" / "test_b.py").write_text(body)
    tree = PytestTestTree(root=tree_dir, python=sys.executable)

    tests = tree.collect()

    assert len(tests) == 2
    assert {test.id for test in tests} == {"a/test_a.py::test_same", "b/test_b.py::test_same"}


def test_teardown_failure_is_reported(tmp_path: Path) -> None:
    tree_dir = tmp_path / "tree"
    tree_dir.mkdir()
    (tree_dir / "test_teardown.py").write_text(
        "import pytest\n"
        "@pytest.fixture\n"
        "def broken():\n"
        "    yield\n"
        "    raise RuntimeError('teardown boom')\n"
        "@pytest.mark.conformance_generic\n"
        "def test_teardown_fails(broken):\n"
        "    assert True\n"
    )
    tree = PytestTestTree(root=tree_dir, python=sys.executable)
    plan = compute_plan(tree.collect(), [Selector("generic", "marker", ("conformance_generic",))])
    context = RunContext(run_id="abcdefgh", results_dir=tmp_path / "results", config=None)  # type: ignore[arg-type]

    outcomes = tree.run(plan, context, keep=False)

    assert outcomes[0].result is Result.FAIL
    assert "teardown boom" in outcomes[0].message


def test_collection_failure_is_a_config_error(tmp_path: Path) -> None:
    tree_dir = tmp_path / "tree"
    tree_dir.mkdir()
    (tree_dir / "test_bad.py").write_text("def broken(:\n")
    tree = PytestTestTree(root=tree_dir, python=sys.executable)

    with pytest.raises(ConfigError):
        tree.collect()


def _one_test_tree(tmp_path: Path) -> PytestTestTree:
    tree_dir = tmp_path / "tree"
    tree_dir.mkdir()
    (tree_dir / "test_ok.py").write_text(
        "import pytest\n@pytest.mark.conformance_generic\ndef test_ok():\n    assert True\n"
    )
    return PytestTestTree(root=tree_dir, python=sys.executable)


def test_pytest_interrupt_return_code_keeps_completed_outcomes(tmp_path: Path) -> None:
    tree = _one_test_tree(tmp_path)
    plan = compute_plan(tree.collect(), [Selector("generic", "marker", ("conformance_generic",))])
    context = RunContext(run_id="abcdefgh", results_dir=tmp_path / "results", config=None)  # type: ignore[arg-type]
    tree.popen = _FakeProcess(  # type: ignore[assignment]
        2,
        entries=[{"nodeid": "test_ok.py::test_ok", "outcome": "passed", "duration": 0.1, "stdout": "", "stderr": ""}],
        stderr="interrupted",
    )

    with pytest.raises(Interrupted) as raised:
        tree.run(plan, context, keep=False)

    assert [outcome.result for outcome in raised.value.outcomes] == [Result.PASS]


def test_signal_after_pytest_exits_keeps_completed_outcomes(tmp_path: Path) -> None:
    tree = _one_test_tree(tmp_path)
    plan = compute_plan(tree.collect(), [Selector("generic", "marker", ("conformance_generic",))])
    context = RunContext(run_id="abcdefgh", results_dir=tmp_path / "results", config=None)  # type: ignore[arg-type]
    gate = InterruptGate()
    entry = {"nodeid": "test_ok.py::test_ok", "outcome": "passed", "duration": 0.1, "stdout": "", "stderr": ""}
    process = _FakeProcess(0, entries=[entry])
    communicate = process.communicate

    def communicate_then_signal() -> tuple[str, str]:
        output = communicate()
        gate.trigger()
        return output

    process.communicate = communicate_then_signal  # type: ignore[method-assign]
    tree.popen = process  # type: ignore[assignment]

    with pytest.raises(Interrupted) as raised:
        tree.run(plan, context, keep=False, interrupt=gate)

    assert [outcome.result for outcome in raised.value.outcomes] == [Result.PASS]


@pytest.mark.parametrize("returncode", [3, 4, 5])
def test_pytest_process_errors_are_config_errors(tmp_path: Path, returncode: int) -> None:
    tree = _one_test_tree(tmp_path)
    plan = compute_plan(tree.collect(), [Selector("generic", "marker", ("conformance_generic",))])
    context = RunContext(run_id="abcdefgh", results_dir=tmp_path / "results", config=None)  # type: ignore[arg-type]
    tree.popen = _FakeProcess(returncode, stderr="boom")  # type: ignore[assignment]

    with pytest.raises(ConfigError, match=f"pytest exited {returncode}"):
        tree.run(plan, context, keep=False)
