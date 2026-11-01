"""Verify that ``run()`` retries only transient gRPC/Envoy errors."""

from __future__ import annotations

import subprocess
from unittest.mock import patch

import pytest

from tests.e2e.core import runner
from tests.e2e.core.runner import CommandFailed, CommandTimedOut, _is_transient_grpc_error, run

# ---------------------------------------------------------------------------
# _is_transient_grpc_error classification
# ---------------------------------------------------------------------------


def _make_cpe(stderr: str) -> subprocess.CalledProcessError:
    exc = subprocess.CalledProcessError(1, "grpcurl")
    exc.stderr = stderr
    return exc


class TestIsTransientGrpcError:
    def test_matches_envoy_no_healthy_upstream(self) -> None:
        exc = _make_cpe("ERROR:\n  Code: Unavailable\n  Message: no healthy upstream")
        assert _is_transient_grpc_error(exc) is True

    def test_matches_osac_cli_go_status_format(self) -> None:
        """The osac CLI (a Go gRPC binary) emits the Go status format."""
        exc = _make_cpe("Error: rpc error: code = Unavailable desc = no healthy upstream")
        assert _is_transient_grpc_error(exc) is True

    def test_matches_case_insensitive(self) -> None:
        exc = _make_cpe("code: UNAVAILABLE\nmessage: No Healthy Upstream")
        assert _is_transient_grpc_error(exc) is True

    def test_rejects_permission_denied(self) -> None:
        exc = _make_cpe("Code: PermissionDenied\nMessage: access denied")
        assert _is_transient_grpc_error(exc) is False

    def test_rejects_not_found(self) -> None:
        exc = _make_cpe("Code: NotFound\nMessage: resource not found")
        assert _is_transient_grpc_error(exc) is False

    def test_rejects_unavailable_without_upstream_message(self) -> None:
        exc = _make_cpe("Code: Unavailable\nMessage: connection refused")
        assert _is_transient_grpc_error(exc) is False

    def test_rejects_no_healthy_upstream_without_unavailable(self) -> None:
        exc = _make_cpe("Code: Internal\nMessage: no healthy upstream")
        assert _is_transient_grpc_error(exc) is False

    def test_rejects_none_stderr(self) -> None:
        exc = subprocess.CalledProcessError(1, "grpcurl")
        exc.stderr = None  # type: ignore[assignment]
        assert _is_transient_grpc_error(exc) is False


# ---------------------------------------------------------------------------
# run() retry behavior
# ---------------------------------------------------------------------------


def _subprocess_ok() -> subprocess.CompletedProcess[str]:
    return subprocess.CompletedProcess(args=["grpcurl"], returncode=0, stdout="ok\n", stderr="")


def _subprocess_transient() -> subprocess.CalledProcessError:
    exc = subprocess.CalledProcessError(1, "grpcurl", output="", stderr="")
    exc.stderr = "ERROR:\n  Code: Unavailable\n  Message: no healthy upstream"
    return exc


def _subprocess_permanent() -> subprocess.CalledProcessError:
    exc = subprocess.CalledProcessError(1, "grpcurl", output="", stderr="")
    exc.stderr = "ERROR:\n  Code: NotFound\n  Message: not found"
    return exc


class TestRunTransientRetry:
    """Verify retry-on-transient and fail-fast-on-permanent behavior."""

    def test_succeeds_after_transient_failure(self, monkeypatch: pytest.MonkeyPatch) -> None:
        monkeypatch.setattr(runner, "_TRANSIENT_GRPC_RETRY_DELAY_S", 0.0)
        side_effects = [_subprocess_transient(), _subprocess_ok()]

        def mock_run(*_a: object, **_kw: object) -> subprocess.CompletedProcess[str]:
            effect = side_effects.pop(0)
            if isinstance(effect, Exception):
                raise effect
            return effect

        with patch("subprocess.run", side_effect=mock_run):
            assert run("grpcurl", "test") == "ok"
        assert side_effects == []

    def test_raises_after_retry_exhaustion(self, monkeypatch: pytest.MonkeyPatch) -> None:
        monkeypatch.setattr(runner, "_TRANSIENT_GRPC_RETRY_DELAY_S", 0.0)
        call_count = 0

        def mock_run(*_a: object, **_kw: object) -> subprocess.CompletedProcess[str]:
            nonlocal call_count
            call_count += 1
            raise _subprocess_transient()

        with patch("subprocess.run", side_effect=mock_run), pytest.raises(subprocess.CalledProcessError):
            run("grpcurl", "test")

        expected_calls = runner._TRANSIENT_GRPC_MAX_ATTEMPTS
        assert call_count == expected_calls

    def test_permanent_error_not_retried(self, monkeypatch: pytest.MonkeyPatch) -> None:
        monkeypatch.setattr(runner, "_TRANSIENT_GRPC_RETRY_DELAY_S", 0.0)
        call_count = 0

        def mock_run(*_a: object, **_kw: object) -> subprocess.CompletedProcess[str]:
            nonlocal call_count
            call_count += 1
            raise _subprocess_permanent()

        with patch("subprocess.run", side_effect=mock_run), pytest.raises(subprocess.CalledProcessError):
            run("grpcurl", "test")

        assert call_count == 1

    def test_no_retry_on_success(self) -> None:
        call_count = 0

        def mock_run(*_a: object, **_kw: object) -> subprocess.CompletedProcess[str]:
            nonlocal call_count
            call_count += 1
            return _subprocess_ok()

        with patch("subprocess.run", side_effect=mock_run):
            assert run("grpcurl", "test") == "ok"

        assert call_count == 1

    def test_retry_does_not_leak_secrets_in_logs(
        self, caplog: pytest.LogCaptureFixture, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        monkeypatch.setattr(runner, "_TRANSIENT_GRPC_RETRY_DELAY_S", 0.0)
        side_effects = [_subprocess_transient(), _subprocess_ok()]

        def mock_run(*_a: object, **_kw: object) -> subprocess.CompletedProcess[str]:
            effect = side_effects.pop(0)
            if isinstance(effect, Exception):
                raise effect
            return effect

        import logging

        with caplog.at_level(logging.WARNING, logger=runner.logger.name), patch("subprocess.run", side_effect=mock_run):
            run("grpcurl", "-H", "Authorization: Bearer secret-token", "addr")

        assert "secret-token" not in caplog.text
        assert "Transient gRPC" in caplog.text


# ---------------------------------------------------------------------------
# Conflict-sensitive: run() must preserve upstream exception wrapping
# ---------------------------------------------------------------------------


class TestRunExceptionWrapping:
    """Verify that run() wraps errors as CommandFailed/CommandTimedOut.

    These tests guard the rebase integration between the transient-retry
    loop and the upstream exception-sanitisation layer.
    """

    def test_permanent_error_raises_command_failed(self) -> None:
        """Non-transient CalledProcessError becomes CommandFailed."""
        with patch("subprocess.run", side_effect=_subprocess_permanent()), pytest.raises(CommandFailed) as exc_info:
            run("grpcurl", "test")
        assert exc_info.value.returncode == 1
        # stderr is preserved for diagnostics
        assert "NotFound" in (exc_info.value.stderr or "")

    def test_exhausted_transient_raises_command_failed(self, monkeypatch: pytest.MonkeyPatch) -> None:
        """Transient error after all retries still becomes CommandFailed."""
        monkeypatch.setattr(runner, "_TRANSIENT_GRPC_RETRY_DELAY_S", 0.0)
        with patch("subprocess.run", side_effect=_subprocess_transient()), pytest.raises(CommandFailed) as exc_info:
            run("grpcurl", "test")
        assert "Unavailable" in (exc_info.value.stderr or "")

    def test_command_failed_sanitizes_command(self) -> None:
        """CommandFailed.cmd must not contain raw secret arguments."""
        with patch("subprocess.run", side_effect=_subprocess_permanent()), pytest.raises(CommandFailed) as exc_info:
            run("grpcurl", "-H", "Authorization: Bearer secret-token", "addr")
        assert "secret-token" not in str(exc_info.value)
        assert "secret-token" not in " ".join(map(str, exc_info.value.cmd))

    def test_timeout_raises_command_timed_out(self) -> None:
        """TimeoutExpired becomes CommandTimedOut, never retried."""
        call_count = 0

        def mock_run(*_a: object, **_kw: object) -> subprocess.CompletedProcess[str]:
            nonlocal call_count
            call_count += 1
            raise subprocess.TimeoutExpired("grpcurl", 300)

        with patch("subprocess.run", side_effect=mock_run), pytest.raises(CommandTimedOut):
            run("grpcurl", "test")
        assert call_count == 1  # not retried
