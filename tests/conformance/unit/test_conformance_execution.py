"""Contract tests for the pytest-backed conformance execution adapter."""

from __future__ import annotations

import json
import signal
import sys
from pathlib import Path

import pytest

from tests.conformance.errors import ConfigError
from tests.conformance.interrupt import Interrupted, InterruptGate
from tests.conformance.model import Classification, Result, RunContext, Selector
from tests.conformance.output import OutputSink
from tests.conformance.redact import Redactor
from tests.conformance.selection import compute_plan
from tests.conformance.testing import make_config
from tests.conformance.testtree import PytestTestTree

_REDACTION_MARKER_C = "redaction-marker-c"


class _FakeProcess:
    """Stand-in for ``subprocess.Popen`` that records what pytest would write."""

    def __init__(self, returncode: int, *, entries: list[dict[str, object]] | None = None, stderr: str = "") -> None:
        self.returncode = returncode
        self.entries = entries or []
        self.stderr = stderr
        self.command: list[str] = []
        self.signals: list[int] = []
        self.killed = False
        self.waited = False
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

    def send_signal(self, signum: int) -> None:
        self.signals.append(signum)

    def kill(self) -> None:
        self.killed = True

    def wait(self) -> int:
        self.waited = True
        return self.returncode

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
    context = RunContext(run_id="abcdefgh", results_dir=tmp_path / "results", config=make_config(tmp_path))

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
        config=make_config(tmp_path),
        redactor=redactor,
        output=OutputSink(results, redactor),
    )

    outcomes = tree.run(plan, context, keep=False)

    evidence = next(outcome.evidence_dir for outcome in outcomes if outcome.identity.id.endswith("::test_passes"))
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
    context = RunContext(run_id="abcdefgh", results_dir=tmp_path / "results", config=make_config(tmp_path))

    outcomes = tree.run(plan, context, keep=False)

    assert outcomes[0].result is Result.FAIL
    assert "teardown boom" in outcomes[0].message


def test_teardown_skip_is_reported(tmp_path: Path) -> None:
    tree_dir = tmp_path / "tree"
    tree_dir.mkdir()
    (tree_dir / "test_teardown.py").write_text(
        "import pytest\n"
        "@pytest.fixture\n"
        "def skipping():\n"
        "    yield\n"
        "    pytest.skip('teardown skipped')\n"
        "@pytest.mark.conformance_generic\n"
        "def test_body_passes(skipping):\n"
        "    assert True\n"
    )
    tree = PytestTestTree(root=tree_dir, python=sys.executable)
    plan = compute_plan(tree.collect(), [Selector("generic", "marker", ("conformance_generic",))])
    context = RunContext(run_id="abcdefgh", results_dir=tmp_path / "results", config=make_config(tmp_path))

    outcomes = tree.run(plan, context, keep=False)

    assert outcomes[0].result is Result.SKIP
    assert "teardown skipped" in outcomes[0].message


def test_relative_root_resolves_against_the_runner_cwd(tmp_path: Path) -> None:
    (tmp_path / "tests").mkdir()

    tree = PytestTestTree(root=Path("tests"), cwd=tmp_path)

    assert tree.root == (tmp_path / "tests").resolve()


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
    context = RunContext(run_id="abcdefgh", results_dir=tmp_path / "results", config=make_config(tmp_path))
    gate = InterruptGate()
    # What the gate does when the operator's first signal reaches the child.
    gate.first = True
    tree.popen = _FakeProcess(  # type: ignore[assignment]
        2,
        entries=[{"nodeid": "test_ok.py::test_ok", "outcome": "passed", "duration": 0.1, "stdout": "", "stderr": ""}],
        stderr="interrupted",
    )

    with pytest.raises(Interrupted) as raised:
        tree.run(plan, context, keep=False, interrupt=gate)

    assert [outcome.result for outcome in raised.value.outcomes] == [Result.PASS]


class _SignalingFakeProcess(_FakeProcess):
    """Delivers signals in the window between Popen returning and registration."""

    def __init__(self, gate: InterruptGate, returncode: int, *, signal_count: int = 1, **kwargs: object) -> None:
        super().__init__(returncode, **kwargs)
        self._gate = gate
        self._signal_count = signal_count
        self._finished = False

    def __call__(self, command: list[str], **kwargs: object) -> _SignalingFakeProcess:
        super().__call__(command, **kwargs)
        for _ in range(self._signal_count):
            self._gate.trigger()
        return self

    def poll(self) -> int | None:
        return None if not self._finished else self.returncode

    def communicate(self) -> tuple[str, str]:
        output = super().communicate()
        self._finished = True
        return output


def test_signal_during_child_registration_reaches_the_child(tmp_path: Path) -> None:
    tree = _one_test_tree(tmp_path)
    plan = compute_plan(tree.collect(), [Selector("generic", "marker", ("conformance_generic",))])
    context = RunContext(run_id="abcdefgh", results_dir=tmp_path / "results", config=make_config(tmp_path))
    gate = InterruptGate()
    entry = {"nodeid": "test_ok.py::test_ok", "outcome": "passed", "duration": 0.1, "stdout": "", "stderr": ""}
    process = _SignalingFakeProcess(gate, 0, entries=[entry])
    tree.popen = process  # type: ignore[assignment]

    with pytest.raises(Interrupted) as raised:
        tree.run(plan, context, keep=False, interrupt=gate)

    assert process.signals == [signal.SIGINT]
    assert [outcome.result for outcome in raised.value.outcomes] == [Result.PASS]


def test_two_signals_during_child_registration_kill_and_reap_the_child(tmp_path: Path) -> None:
    tree = _one_test_tree(tmp_path)
    plan = compute_plan(tree.collect(), [Selector("generic", "marker", ("conformance_generic",))])
    context = RunContext(run_id="abcdefgh", results_dir=tmp_path / "results", config=make_config(tmp_path))
    gate = InterruptGate()
    process = _SignalingFakeProcess(gate, 0, signal_count=2)
    tree.popen = process  # type: ignore[assignment]

    with pytest.raises(KeyboardInterrupt):
        tree.run(plan, context, keep=False, interrupt=gate)

    assert gate.first is True
    assert gate.second is True
    assert process.killed is True
    assert process.waited is True
    assert gate.child is None


def test_signal_after_pytest_exits_keeps_completed_outcomes(tmp_path: Path) -> None:
    tree = _one_test_tree(tmp_path)
    plan = compute_plan(tree.collect(), [Selector("generic", "marker", ("conformance_generic",))])
    context = RunContext(run_id="abcdefgh", results_dir=tmp_path / "results", config=make_config(tmp_path))
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
    context = RunContext(run_id="abcdefgh", results_dir=tmp_path / "results", config=make_config(tmp_path))
    tree.popen = _FakeProcess(returncode, stderr="boom")  # type: ignore[assignment]

    with pytest.raises(ConfigError, match=f"pytest exited {returncode}"):
        tree.run(plan, context, keep=False)


def test_execution_side_collection_error_is_a_config_error(tmp_path: Path) -> None:
    tree = _one_test_tree(tmp_path)
    plan = compute_plan(tree.collect(), [Selector("generic", "marker", ("conformance_generic",))])
    context = RunContext(run_id="abcdefgh", results_dir=tmp_path / "results", config=make_config(tmp_path))
    # pytest exits 2 for a module that fails to import during execution, which is
    # a configuration error, not user cancellation.
    tree.popen = _FakeProcess(2, stderr="ImportError while importing test module")  # type: ignore[assignment]

    with pytest.raises(ConfigError) as raised:
        tree.run(plan, context, keep=False)

    assert "pytest collection failed" in raised.value.checks[0]
    assert "ImportError" in raised.value.checks[0]


def test_a_child_killed_by_the_forwarded_signal_is_an_interrupt(tmp_path: Path) -> None:
    tree = _one_test_tree(tmp_path)
    plan = compute_plan(tree.collect(), [Selector("generic", "marker", ("conformance_generic",))])
    context = RunContext(run_id="abcdefgh", results_dir=tmp_path / "results", config=make_config(tmp_path))
    gate = InterruptGate()
    gate.first = True
    # pytest died from SIGINT before it could install its own handler.
    tree.popen = _FakeProcess(-signal.SIGINT)  # type: ignore[assignment]

    with pytest.raises(Interrupted) as raised:
        tree.run(plan, context, keep=False, interrupt=gate)

    assert [outcome.result for outcome in raised.value.outcomes] == [Result.SKIP]


def test_captured_logging_reaches_the_redacted_evidence_log(tmp_path: Path) -> None:
    tree_dir = tmp_path / "tree"
    tree_dir.mkdir()
    (tree_dir / "test_logs.py").write_text(
        "import logging\n"
        "import pytest\n"
        "@pytest.mark.conformance_generic\n"
        "def test_logs():\n"
        f"    logging.getLogger('conformance').warning('api_key={_REDACTION_MARKER_C}')\n"
    )
    tree = PytestTestTree(root=tree_dir, python=sys.executable)
    plan = compute_plan(tree.collect(), [Selector("generic", "marker", ("conformance_generic",))])
    results = tmp_path / "results"
    redactor = Redactor()
    context = RunContext(
        run_id="abcdefgh",
        results_dir=results,
        config=make_config(tmp_path),
        redactor=redactor,
        output=OutputSink(results, redactor),
    )

    outcomes = tree.run(plan, context, keep=False)

    log = (outcomes[0].evidence_dir / "test.log").read_text()
    assert "WARNING" in log
    assert _REDACTION_MARKER_C not in log
    assert "<redacted>" in log
