"""Verify that ``run()`` retries only transient gRPC/Envoy errors."""

from __future__ import annotations

import subprocess
from unittest.mock import patch

import pytest

from tests.e2e.core import runner
from tests.e2e.core.runner import _is_transient_grpc_error, run

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

        expected_calls = runner._TRANSIENT_GRPC_MAX_RETRIES + 1
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
