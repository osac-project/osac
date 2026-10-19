"""run_unchecked must not let stderr contaminate stdout on success.

Regression tests for the fix to ``runner.run_unchecked``: when a command
succeeds (rc 0) the return value must contain only stdout, so callers
that ``json.loads()`` the output are not broken by warnings or transient
errors written to stderr -- such as DNS resolver blips that append
non-JSON text after valid JSON.

Callers that need stderr on success (e.g. to assert a deprecation
warning) pass ``include_stderr=True``.
"""

from __future__ import annotations

import json
import subprocess
from unittest.mock import patch

from tests.e2e.core.runner import run_unchecked

_VALID_JSON = '{"apiVersion": "v1", "kind": "Pod"}'
_DNS_WARNING = (
    "E1019 dial tcp: lookup api.cluster.example.com "
    "on 127.0.0.1:53: read udp 127.0.0.1:36626->127.0.0.1:53: "
    "read: connection refused"
)


def _fake_run(
    *args: object, stdout: str = "", stderr: str = "", returncode: int = 0, **kwargs: object
) -> subprocess.CompletedProcess[str]:
    return subprocess.CompletedProcess(args=args, returncode=returncode, stdout=stdout, stderr=stderr)


def test_success_returns_only_stdout() -> None:
    """On rc 0 the caller must receive stdout without stderr appended."""

    def side_effect(*a: object, **kw: object) -> subprocess.CompletedProcess[str]:
        return _fake_run(stdout=_VALID_JSON + "\n", stderr=_DNS_WARNING + "\n", returncode=0)

    with patch("subprocess.run", side_effect=side_effect):
        output, rc = run_unchecked("kubectl", "get", "pod", "-o", "json")

    assert rc == 0
    assert output == _VALID_JSON
    assert _DNS_WARNING not in output


def test_success_stdout_is_valid_json_despite_stderr() -> None:
    """json.loads on the returned output must not raise when stderr has noise."""

    def side_effect(*a: object, **kw: object) -> subprocess.CompletedProcess[str]:
        return _fake_run(stdout=_VALID_JSON + "\n", stderr=_DNS_WARNING + "\n", returncode=0)

    with patch("subprocess.run", side_effect=side_effect):
        output, rc = run_unchecked("kubectl", "get", "pod", "-o", "json")

    assert rc == 0
    parsed = json.loads(output)
    assert parsed["kind"] == "Pod"


def test_failure_combines_stdout_and_stderr() -> None:
    """On rc != 0 the combined output must still include stderr for pattern matching."""
    error_msg = 'Error from server (NotFound): pods "missing" not found'

    def side_effect(*a: object, **kw: object) -> subprocess.CompletedProcess[str]:
        return _fake_run(stdout="", stderr=error_msg + "\n", returncode=1)

    with patch("subprocess.run", side_effect=side_effect):
        output, rc = run_unchecked("kubectl", "get", "pod", "missing")

    assert rc == 1
    assert error_msg in output


def test_failure_includes_both_streams() -> None:
    """On rc != 0, stdout and stderr are both present in the combined output."""

    def side_effect(*a: object, **kw: object) -> subprocess.CompletedProcess[str]:
        return _fake_run(stdout="partial output", stderr="fatal error", returncode=2)

    with patch("subprocess.run", side_effect=side_effect):
        output, rc = run_unchecked("cmd")

    assert rc == 2
    assert "partial output" in output
    assert "fatal error" in output


def test_success_with_empty_stderr() -> None:
    """Clean success: no stderr, stdout returned as-is."""

    def side_effect(*a: object, **kw: object) -> subprocess.CompletedProcess[str]:
        return _fake_run(stdout=_VALID_JSON + "\n", stderr="", returncode=0)

    with patch("subprocess.run", side_effect=side_effect):
        output, rc = run_unchecked("kubectl", "get", "pod", "-o", "json")

    assert rc == 0
    assert output == _VALID_JSON


def test_success_strips_whitespace() -> None:
    """Trailing whitespace on stdout is stripped on success, matching the old behavior."""

    def side_effect(*a: object, **kw: object) -> subprocess.CompletedProcess[str]:
        return _fake_run(stdout="  value  \n", stderr="", returncode=0)

    with patch("subprocess.run", side_effect=side_effect):
        output, rc = run_unchecked("echo", "value")

    assert rc == 0
    assert output == "value"


# --- include_stderr=True tests ---


_DEPRECATION_WARNING = "Warning: instance type is deprecated"


def test_include_stderr_success_combines_streams() -> None:
    """``include_stderr=True`` includes stderr in successful output."""

    def side_effect(*a: object, **kw: object) -> subprocess.CompletedProcess[str]:
        return _fake_run(stdout="Created 'abc-123'\n", stderr=_DEPRECATION_WARNING + "\n", returncode=0)

    with patch("subprocess.run", side_effect=side_effect):
        output, rc = run_unchecked("osac", "create", include_stderr=True)

    assert rc == 0
    assert "abc-123" in output
    assert _DEPRECATION_WARNING in output


def test_include_stderr_false_excludes_stderr_on_success() -> None:
    """Default (``include_stderr=False``) excludes stderr on success."""

    def side_effect(*a: object, **kw: object) -> subprocess.CompletedProcess[str]:
        return _fake_run(stdout="Created 'abc-123'\n", stderr=_DEPRECATION_WARNING + "\n", returncode=0)

    with patch("subprocess.run", side_effect=side_effect):
        output, rc = run_unchecked("osac", "create", include_stderr=False)

    assert rc == 0
    assert "abc-123" in output
    assert _DEPRECATION_WARNING not in output


def test_include_stderr_failure_always_combines() -> None:
    """On failure, stderr is combined regardless of include_stderr."""

    def side_effect(*a: object, **kw: object) -> subprocess.CompletedProcess[str]:
        return _fake_run(stdout="", stderr="connection refused\n", returncode=1)

    with patch("subprocess.run", side_effect=side_effect):
        output, rc = run_unchecked("cmd", include_stderr=False)

    assert rc == 1
    assert "connection refused" in output


def test_include_stderr_json_with_warning() -> None:
    """``include_stderr=True`` lets callers see both JSON and warning."""

    def side_effect(*a: object, **kw: object) -> subprocess.CompletedProcess[str]:
        return _fake_run(stdout=_VALID_JSON + "\n", stderr=_DEPRECATION_WARNING + "\n", returncode=0)

    with patch("subprocess.run", side_effect=side_effect):
        output, rc = run_unchecked("cmd", include_stderr=True)

    assert rc == 0
    assert _DEPRECATION_WARNING in output
    # JSON is present but may not be directly parseable because
    # stderr is appended; that is the caller's choice.
    assert _VALID_JSON in output
