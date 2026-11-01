"""The E2E polling helper must not expose API data in CI logs."""

import ast
import logging
import subprocess
from pathlib import Path
from unittest.mock import Mock

import pytest

from tests.e2e.core import runner
from tests.e2e.core.wait_kinds import WaitResourceKind


@pytest.mark.parametrize(
    "relative_path",
    ["bmaas/regression/networking/test_bmaas_networking.py", "vmaas/conftest.py", "vmaas/regression/test_console.py"],
)
def test_e2e_progress_prints_do_not_include_runtime_values(relative_path: str) -> None:
    path = Path(__file__).resolve().parents[1] / "e2e" / relative_path
    tree = ast.parse(path.read_text())
    for node in ast.walk(tree):
        if isinstance(node, ast.Call) and isinstance(node.func, ast.Name) and node.func.id == "print":
            assert all(isinstance(arg, ast.Constant) and isinstance(arg.value, str) for arg in node.args), (
                f"{relative_path}:{node.lineno} prints runtime data"
            )


def test_storage_teardown_warnings_do_not_log_runtime_values_or_tracebacks() -> None:
    path = Path(__file__).resolve().parents[1] / "e2e/storage/test_caas_cluster_storage.py"
    tree = ast.parse(path.read_text())
    warnings = [
        node
        for node in ast.walk(tree)
        if isinstance(node, ast.Call)
        and isinstance(node.func, ast.Attribute)
        and isinstance(node.func.value, ast.Name)
        and node.func.value.id == "logger"
        and node.func.attr == "warning"
    ]
    assert warnings
    for node in warnings:
        assert len(node.args) == 1 and isinstance(node.args[0], ast.Constant)
        assert not any(keyword.arg == "exc_info" for keyword in node.keywords)


def test_poll_until_logs_description_but_not_values(
    caplog: pytest.LogCaptureFixture, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(runner, "_PROGRESS_LOG_INTERVAL_S", 0)
    values = iter(["secret-response", "ready"])

    with caplog.at_level(logging.INFO, logger=runner.logger.name):
        result = runner.poll_until(
            fn=lambda: next(values),
            until=lambda value: value == "ready",
            retries=2,
            delay=0,
            description="widget ready",
            resource_kind=WaitResourceKind.COMPUTE_INSTANCE,
            resource_id="widget-1",
        )

    messages = [record.getMessage() for record in caplog.records]
    assert result == "ready"
    assert any(m.startswith("Waiting for widget ready") for m in messages)
    assert any(m.startswith("Still waiting for widget ready (attempt 1/2") for m in messages)
    assert any(m.startswith("Done waiting for widget ready after") for m in messages)
    assert "secret-response" not in caplog.text


def test_poll_until_redacts_credentials_in_the_description(
    caplog: pytest.LogCaptureFixture, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(runner, "_PROGRESS_LOG_INTERVAL_S", 0)
    values = iter(["pending", "ready"])

    with caplog.at_level(logging.INFO, logger=runner.logger.name):
        runner.poll_until(
            fn=lambda: next(values),
            until=lambda value: value == "ready",
            retries=2,
            delay=0,
            description="call with Bearer abcdefgh12345678",
            resource_kind=WaitResourceKind.COMPUTE_INSTANCE,
            resource_id="widget-1",
        )

    assert "<redacted>" in caplog.text
    assert "abcdefgh12345678" not in caplog.text


def test_poll_until_timeout_reports_the_description_and_scalar_but_logs_neither(
    caplog: pytest.LogCaptureFixture, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(runner, "_PROGRESS_LOG_INTERVAL_S", 0)

    with caplog.at_level(logging.INFO, logger=runner.logger.name), pytest.raises(TimeoutError) as exc:
        runner.poll_until(
            fn=lambda: "secret-response",
            until=lambda _: False,
            retries=1,
            delay=0,
            description="secret-description",
            resource_kind=WaitResourceKind.COMPUTE_INSTANCE,
            resource_id="widget-1",
        )

    # The exception reaches the failure report; the shared progress log stays free of runtime values.
    assert "secret-response" not in caplog.text
    assert "secret-description not reached" in str(exc.value)
    assert "'secret-response'" in str(exc.value)


def test_poll_until_timeout_does_not_expose_the_content_of_a_container() -> None:
    with pytest.raises(TimeoutError) as exc:
        runner.poll_until(
            fn=lambda: {"key": "secret-response"},
            until=lambda _: False,
            retries=1,
            delay=0,
            description="d",
            resource_kind=WaitResourceKind.COMPUTE_INSTANCE,
            resource_id="widget-1",
        )

    assert "secret-response" not in str(exc.value)


def test_poll_until_recovers_after_a_command_error(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(runner.time, "sleep", lambda _delay: None)
    observe = Mock(side_effect=[subprocess.CalledProcessError(1, ["kubectl"]), "ready"])

    assert (
        runner.poll_until(
            fn=observe,
            until=lambda value: value == "ready",
            retries=2,
            delay=0,
            description="test recovery",
            retry_on_error=True,
            resource_kind=WaitResourceKind.COMPUTE_INSTANCE,
            resource_id="widget-1",
        )
        == "ready"
    )


def test_poll_until_retry_error_does_not_log_command_output(
    caplog: pytest.LogCaptureFixture, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(runner, "_PROGRESS_LOG_INTERVAL_S", 0)

    monkeypatch.setattr(runner.time, "sleep", lambda _delay: None)

    def fail() -> str:
        raise subprocess.CalledProcessError(1, "secret-command", output="secret-response")

    with caplog.at_level(logging.INFO, logger=runner.logger.name), pytest.raises(TimeoutError) as exc:
        runner.poll_until(
            fn=fail,
            until=lambda _: False,
            retries=2,
            delay=0,
            description="secret-description",
            retry_on_error=True,
            resource_kind=WaitResourceKind.COMPUTE_INSTANCE,
            resource_id="widget-1",
        )

    for secret in ("secret-command", "secret-response"):
        assert secret not in caplog.text + str(exc.value)
