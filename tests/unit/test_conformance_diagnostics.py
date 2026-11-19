"""Contract tests for the conformance diagnostics bundle."""

from __future__ import annotations

from pathlib import Path

from tests.conformance.diagnostics import DiagnosticsCollector
from tests.conformance.model import RunContext
from tests.conformance.redact import Redactor
from tests.conformance.testing import make_config

_REDACTION_MARKER = "redaction-marker"
_REDACTION_MARKER_B = "redaction-marker-b"
_REDACTION_MARKER_C = "redaction-marker-c"
_REDACTION_MARKER_F = "redaction-marker-f"
_REDACTION_MARKER_API = "redaction-marker-api"
_REDACTION_MARKER_ENV = "redaction-marker-env"


def test_diagnostics_writes_redacted_control_plane_state(tmp_path: Path) -> None:
    calls: list[tuple[str, ...]] = []

    def fake_run(*args: str, timeout: int = 300) -> str:
        calls.append(args)
        if "logs" in args:
            return f"api_key={_REDACTION_MARKER_C} session_id={_REDACTION_MARKER_F}\n"
        return (
            "apiVersion: v1\n"
            "kind: Pod\n"
            "metadata:\n"
            "  name: demo\n"
            "  annotations:\n"
            f"    secret-value: {_REDACTION_MARKER}\n"
            f"    password: {_REDACTION_MARKER_B}\n"
            f"    api_key: {_REDACTION_MARKER_API}\n"
            "spec:\n"
            "  containers:\n"
            "    - name: fulfillment\n"
            "      env:\n"
            "        - name: API_KEY\n"
            f"          value: {_REDACTION_MARKER_ENV}\n"
            "        - name: LOG_LEVEL\n"
            "          value: debug\n"
        )

    context = RunContext(
        run_id="abcdefgh",
        results_dir=tmp_path,
        config=make_config(tmp_path),
        tenant="osac-conformance-abcdefgh",
        redactor=Redactor(secrets=(_REDACTION_MARKER,)),
    )

    path = DiagnosticsCollector(run_command=fake_run).collect(context, context.tenant)

    content = (path / "osac-state.yaml").read_text()
    assert _REDACTION_MARKER not in content
    assert "<redacted>" in content
    assert "password:" not in content
    assert _REDACTION_MARKER_API not in content
    assert _REDACTION_MARKER_ENV not in content
    assert "LOG_LEVEL" in content
    assert (path / "tenant-resources.yaml").exists()
    logs = (path / "pod-logs.txt").read_text()
    assert _REDACTION_MARKER_C not in logs
    assert _REDACTION_MARKER_F not in logs
    assert all("helm" not in " ".join(call).lower() for call in calls)


def test_a_failed_diagnostics_command_is_redacted(tmp_path: Path) -> None:
    def fake_run(*_args: str, timeout: int = 300) -> str:
        raise RuntimeError(f"token={_REDACTION_MARKER}")

    context = RunContext(
        run_id="abcdefgh",
        results_dir=tmp_path,
        config=make_config(tmp_path),
        redactor=Redactor(secrets=(_REDACTION_MARKER,)),
    )

    path = DiagnosticsCollector(run_command=fake_run).collect(context, None)

    for name in ("osac-state.yaml", "pod-logs.txt"):
        content = (path / name).read_text()
        assert _REDACTION_MARKER not in content
        assert "diagnostics command failed" in content


def test_diagnostics_use_the_configured_namespace(tmp_path: Path) -> None:
    calls: list[tuple[str, ...]] = []

    def fake_run(*args: str, timeout: int = 300) -> str:
        calls.append(args)
        return ""

    context = RunContext(
        run_id="abcdefgh",
        results_dir=tmp_path,
        config=make_config(tmp_path, namespace="osac-e2e-ci"),
        redactor=Redactor(),
    )

    DiagnosticsCollector(run_command=fake_run).collect(context, None)

    namespaces = [call[call.index("-n") + 1] for call in calls if "-n" in call]
    assert namespaces == ["osac-e2e-ci", "osac-e2e-ci"]
