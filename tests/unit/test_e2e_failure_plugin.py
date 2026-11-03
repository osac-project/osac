"""The failure-report plugin, driven by real `python -m pytest` subprocesses on throwaway test files."""

from __future__ import annotations

import json
import os
import subprocess
import sys
import textwrap
from dataclasses import dataclass
from pathlib import Path

import pytest

REPO_ROOT = Path(__file__).resolve().parents[2]

CONFTEST = """
import os
import time

from tests.e2e.core import failure_report
from tests.e2e.core.failure_snapshot import SOURCES


def _raising(directory, window):
    raise RuntimeError("source blew up Bearer " + ".".join(["eyJhbGciOiJSUzI1NiJ9", "eyJzdWIiOiJ0In0", "c2ln"]))


def _sleeping(directory, window):
    time.sleep(float(os.environ["SLEEP_S"]))
    (directory / "late.txt").write_text("late")


def _good(directory, window):
    (directory / "good.txt").write_text("good")


def pytest_configure(config):
    reporter = failure_report.register(config)
    reporter.snapshotter.budget_s = float(os.environ.get("BUDGET_S", "30"))
    for name in filter(None, os.environ.get("SOURCES", "").split(",")):
        SOURCES.register(name, {"raise": _raising, "sleep": _sleeping, "good": _good}[name])
    if os.environ.get("BREAK_CLASSIFY"):
        def broken(exc):
            raise TypeError("classifier bug")

        failure_report.classify = broken
    if os.environ.get("BREAK_APPEND"):
        original = reporter._append

        def append(entry):
            if entry.get("outcome") == "failed":
                raise OSError("disk full")
            original(entry)

        reporter._append = append
"""


@dataclass
class Result:
    returncode: int
    stdout: str
    out: Path

    def records(self, worker: str = "main") -> list[dict]:
        path = self.out / f"results-{worker}.jsonl"
        return [json.loads(line) for line in path.read_text().splitlines()]

    def tests(self, worker: str = "main") -> dict[str, dict]:
        return {r["test"]: r for r in self.records(worker) if "test" in r}


def run_pytest(
    tmp_path: Path,
    tests: str,
    *,
    env: dict[str, str] | None = None,
    args: tuple[str, ...] = (),
    conftest: str = CONFTEST,
) -> Result:
    project = tmp_path / "project"
    project.mkdir(exist_ok=True)
    (project / "conftest.py").write_text(conftest)
    (project / "test_sample.py").write_text(textwrap.dedent(tests))
    out = tmp_path / "out"
    merged = {
        **os.environ,
        "PYTHONPATH": str(REPO_ROOT),
        "OSAC_E2E_OUTPUT_DIR": str(out),
        "PYTHONDONTWRITEBYTECODE": "1",
        **(env or {}),
    }
    merged.pop("PYTEST_XDIST_WORKER", None)
    command = [
        sys.executable,
        "-m",
        "pytest",
        "-p",
        "no:cacheprovider",
        "-n",
        "0",
        "-o",
        "log_cli=true",
        "-o",
        "log_cli_level=INFO",
        "-o",
        f"log_file={tmp_path / 'e2e.log'}",
        "-o",
        "log_file_level=INFO",
        *args,
    ]
    completed = subprocess.run(command, cwd=project, env=merged, capture_output=True, text=True, timeout=120)
    return Result(completed.returncode, completed.stdout + completed.stderr, out)


def test_failure_is_logged_and_recorded_before_later_tests_run(tmp_path: Path) -> None:
    result = run_pytest(
        tmp_path,
        """
        import logging

        def test_a_fails():
            assert 1 == 2, "left and right differ"

        def test_b_runs_later():
            logging.getLogger("later").warning("LATER-TEST-RAN")
        """,
    )

    assert result.returncode == 1
    assert "E2E FAILURE test_a_fails [assertion] phase=call" in result.stdout
    assert result.stdout.index("E2E FAILURE test_a_fails") < result.stdout.index("LATER-TEST-RAN")
    assert "E2E FAILURE test_a_fails" in (tmp_path / "e2e.log").read_text()
    records = result.records()
    assert records[0]["event"] == "session-start"
    assert records[0]["collected"] == 2
    failed = result.tests()["test_a_fails"]
    assert (failed["outcome"], failed["kind"], failed["phase"]) == ("failed", "assertion", "call")
    assert "left and right differ" in failed["summary"]
    assert failed["nodeid"].endswith("test_sample.py::test_a_fails")
    assert failed["snapshot"] == "failures/main/01-test_a_fails"
    assert (result.out / failed["snapshot"] / "failure.json").is_file()
    assert result.tests()["test_b_runs_later"]["outcome"] == "passed"
    assert records[-1]["event"] == "session-finish"
    assert records[-1]["exitstatus"] == 1
    assert records[-1]["counts"] == {"failed": 1, "passed": 1}


def test_output_directory_is_private(tmp_path: Path) -> None:
    result = run_pytest(tmp_path, "def test_a():\n    assert False\n")
    assert (result.out.stat().st_mode & 0o777) == 0o700
    snapshot = result.out / result.tests()["test_a"]["snapshot"]
    assert (snapshot.stat().st_mode & 0o777) == 0o700


def test_a_killed_run_keeps_earlier_records_and_has_no_session_finish(tmp_path: Path) -> None:
    result = run_pytest(
        tmp_path,
        """
        import os, signal

        def test_a_fails():
            assert False, "first"

        def test_b_killed():
            os.kill(os.getpid(), signal.SIGKILL)
        """,
    )

    assert result.returncode != 0
    records = result.records()
    assert [r.get("event") for r in records if "event" in r] == ["session-start"]
    assert result.tests()["test_a_fails"]["outcome"] == "failed"


def test_the_failure_that_stops_a_x_run_is_logged_and_snapshotted(tmp_path: Path) -> None:
    result = run_pytest(
        tmp_path,
        """
        import logging

        def test_a_ok():
            pass

        def test_b_fails():
            assert False, "stop here"

        def test_c_never_runs():
            logging.getLogger("later").warning("LATER-TEST-RAN")
        """,
        args=("-x",),
    )

    assert result.returncode == 1
    assert "LATER-TEST-RAN" not in result.stdout
    assert "E2E FAILURE test_b_fails [assertion]" in result.stdout
    records = result.records()
    failed = result.tests()["test_b_fails"]
    assert (result.out / failed["snapshot"] / "failure.json").is_file()
    assert records[-1]["event"] == "session-finish"
    assert records.index(failed) < len(records) - 1


def test_a_shared_fixture_failure_is_reported_once(tmp_path: Path) -> None:
    result = run_pytest(
        tmp_path,
        """
        import pytest

        @pytest.fixture(scope="module")
        def stack():
            raise RuntimeError("stack never came up")

        def test_a(stack):
            pass

        def test_b(stack):
            pass

        def test_c(stack):
            pass
        """,
    )

    tests = result.tests()
    assert tests["test_a"]["repeat_of"] is None
    assert tests["test_a"]["phase"] == "setup"
    assert tests["test_a"]["kind"] == "unclassified"
    assert tests["test_a"]["snapshot"] is not None
    for later in ("test_b", "test_c"):
        assert tests[later]["repeat_of"] == "test_a"
        assert tests[later]["snapshot"] is None
    assert result.stdout.count("repeats the failure of test_a") == 2
    assert result.stdout.count("E2E FAILURE test_a [unclassified]") == 1
    assert len(list((result.out / "failures" / "main").iterdir())) == 1


def test_a_fixture_failure_is_snapshotted_before_its_finally_cleans_up(tmp_path: Path) -> None:
    conftest = (
        CONFTEST
        + """

LIVE = {"state": "unset"}


def _live(directory, window):
    (directory / "live.txt").write_text(LIVE["state"])


SOURCES.register("live", _live)
"""
    )
    result = run_pytest(
        tmp_path,
        """
        import time
        import pytest
        from conftest import LIVE
        from tests.e2e.core import failure_report

        @pytest.fixture(scope="module")
        def stack(request):
            LIVE["state"] = "live"
            started = time.time()
            try:
                raise RuntimeError("stack never came up")
            except BaseException as exc:
                failure_report.capture_fixture_failure(request.config, exc, "fixture stack", started)
                raise
            finally:
                LIVE["state"] = "cleaned"

        def test_a(stack):
            pass

        def test_b(stack):
            pass
        """,
        conftest=conftest,
    )

    assert result.returncode == 1
    tests = result.tests()
    assert tests["fixture stack"]["snapshot"] == "failures/main/01-fixture_stack"
    assert (result.out / "failures/main/01-fixture_stack/live.txt").read_text() == "live"
    assert tests["test_a"]["repeat_of"] == "fixture stack" and tests["test_a"]["snapshot"] is None
    assert tests["test_b"]["repeat_of"] == "fixture stack"
    assert (tmp_path / "e2e.log").read_text().count("E2E FAILURE fixture stack [unclassified]") == 1
    assert len(list((result.out / "failures" / "main").iterdir())) == 1


def test_capture_fixture_failure_without_the_plugin_does_nothing() -> None:
    from types import SimpleNamespace

    from tests.e2e.core import failure_report

    config = SimpleNamespace(pluginmanager=SimpleNamespace(get_plugin=lambda _name: None))

    failure_report.capture_fixture_failure(config, RuntimeError("x"), "fixture")  # type: ignore[arg-type]


def test_sources_run_and_a_failing_source_does_not_change_the_outcome(tmp_path: Path) -> None:
    result = run_pytest(tmp_path, "def test_a():\n    assert False\n", env={"SOURCES": "good,raise"})

    assert result.returncode == 1
    assert " 1 failed" in result.stdout
    directory = result.out / result.tests()["test_a"]["snapshot"]
    assert (directory / "good.txt").read_text() == "good"
    errors = (directory / "snapshot-errors.txt").read_text()
    assert "raise: RuntimeError: source blew up" in errors
    assert "eyJhbGciOiJSUzI1NiJ9" not in errors


def test_a_source_past_the_budget_is_abandoned_and_named(tmp_path: Path) -> None:
    result = run_pytest(
        tmp_path,
        "def test_a():\n    assert False\n\ndef test_b():\n    pass\n",
        env={"SOURCES": "good,sleep", "SLEEP_S": "3", "BUDGET_S": "0.5"},
    )

    assert result.returncode == 1
    assert " 1 failed, 1 passed" in result.stdout
    directory = result.out / result.tests()["test_a"]["snapshot"]
    assert (directory / "good.txt").exists()
    assert "unfinished sources: sleep" in (directory / "snapshot-errors.txt").read_text()


def test_an_internal_reporting_error_leaves_the_test_result_alone(tmp_path: Path) -> None:
    result = run_pytest(tmp_path, "def test_a():\n    assert False, 'real failure'\n", env={"BREAK_APPEND": "1"})

    assert result.returncode == 1
    assert " 1 failed" in result.stdout
    assert "real failure" in result.stdout
    assert "error" not in result.stdout.splitlines()[-1]


def test_each_xdist_worker_writes_its_own_file(tmp_path: Path) -> None:
    project = tmp_path / "project"
    project.mkdir()
    (project / "test_one.py").write_text("def test_one():\n    assert False\n")
    (project / "test_two.py").write_text("def test_two():\n    pass\n")
    result = run_pytest(
        tmp_path, "def test_sample():\n    pass\n", args=("-n", "2", "--dist", "loadfile", "test_one.py", "test_two.py")
    )

    files = sorted(path.name for path in result.out.glob("results-*.jsonl"))
    assert files == ["results-gw0.jsonl", "results-gw1.jsonl"], result.stdout
    found = {t for worker in ("gw0", "gw1") for t in result.tests(worker)}
    assert found == {"test_one", "test_two"}
    assert result.returncode == 1


FAKE_JWT = ".".join(["eyJ" + "a" * 300, "eyJ" + "b" * 600, "c" * 300])


@pytest.mark.parametrize("tb", ["long", "short"])
def test_a_failing_grpcurl_shaped_command_never_leaks_its_token(tmp_path: Path, tb: str) -> None:
    result = run_pytest(
        tmp_path,
        f"""
        from tests.e2e.core.runner import run

        TOKEN = {FAKE_JWT!r}
        PAYLOAD = '{{"payload-marker": 1}}'

        def test_call():
            run("sh", "-c", "echo Code: Unavailable >&2; exit 64", "-H", "Authorization: Bearer " + TOKEN,
                "-d", PAYLOAD)
        """,
        args=(f"--tb={tb}",),
    )

    assert result.returncode == 1
    everything = result.stdout + "".join(p.read_text() for p in result.out.rglob("*") if p.is_file())
    everything += (tmp_path / "e2e.log").read_text()
    assert "E2E FAILURE test_call" in result.stdout
    for secret in (FAKE_JWT, "Bearer eyJ", "a" * 30, "b" * 30, "c" * 30, "payload-marker"):
        assert secret not in everything


def test_a_timed_out_command_with_stderr_is_still_recorded(tmp_path: Path) -> None:
    result = run_pytest(
        tmp_path,
        """
        from tests.e2e.core.runner import run

        def test_slow():
            run("sh", "-c", "echo partial >&2; sleep 5", timeout=1)
        """,
    )
    failed = result.tests()["test_slow"]
    assert failed["kind"] == "unclassified"
    assert failed["snapshot"] is not None
    assert "E2E FAILURE test_slow" in result.stdout


def test_a_classification_bug_still_leaves_record_log_line_and_snapshot(tmp_path: Path) -> None:
    result = run_pytest(tmp_path, "def test_a():\n    assert False\n", env={"BREAK_CLASSIFY": "1"})
    failed = result.tests()["test_a"]
    assert failed["kind"] == "unclassified"
    assert failed["snapshot"] is not None
    assert "E2E FAILURE test_a [unclassified]" in result.stdout
    assert result.returncode == 1


REAL_CONFTEST_HOOK = "from tests.e2e.conftest import pytest_configure  # noqa: F401\n"
FRAME_TEST = f"""
from tests.e2e.core.runner import run

TOKEN = {FAKE_JWT!r}
SECRET = "hunter2-" + TOKEN

def login(password):
    raise RuntimeError("login refused")

def test_call():
    login(password=SECRET)
"""


def test_without_a_tb_option_the_real_conftest_uses_short_tracebacks(tmp_path: Path) -> None:
    result = run_pytest(tmp_path, FRAME_TEST, conftest=REAL_CONFTEST_HOOK)

    assert result.returncode == 1
    everything = result.stdout + (tmp_path / "e2e.log").read_text()
    everything += "".join(p.read_text() for p in result.out.rglob("*") if p.is_file())
    for secret in ("hunter2-", "a" * 30, "b" * 30, "c" * 30):
        assert secret not in everything
    # The failure text still reaches the console, the log file and the results.
    assert "RuntimeError: login refused" in result.stdout
    assert "E2E FAILURE test_call [unclassified]" in result.stdout
    assert "E2E FAILURE test_call" in (tmp_path / "e2e.log").read_text()
    failed = result.tests()["test_call"]
    assert "login refused" in failed["summary"]


def test_an_explicit_tb_long_is_left_alone(tmp_path: Path) -> None:
    result = run_pytest(tmp_path, FRAME_TEST, conftest=REAL_CONFTEST_HOOK, args=("--tb=long",))
    assert "hunter2-" in result.stdout
