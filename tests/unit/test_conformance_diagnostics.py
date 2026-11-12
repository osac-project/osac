"""Contract tests for the conformance diagnostics bundle."""

from __future__ import annotations

from pathlib import Path

from tests.conformance.diagnostics import DiagnosticsCollector
from tests.conformance.model import RunContext
from tests.conformance.redact import Redactor

_FAKE_SECRET = "not-a-real-secret"
_FAKE_PASSWORD = "not-a-real-password"
_FAKE_API_KEY = "not-a-real-api-key"
_FAKE_SESSION = "not-a-real-session"


def test_diagnostics_writes_redacted_control_plane_state(tmp_path: Path) -> None:
    calls: list[tuple[str, ...]] = []

    def fake_run(*args: str, timeout: int = 300) -> str:
        calls.append(args)
        if "logs" in args:
            return f"api_key={_FAKE_API_KEY} session_id={_FAKE_SESSION}\n"
        return (
            "apiVersion: v1\n"
            "kind: Pod\n"
            "metadata:\n"
            "  name: demo\n"
            "  annotations:\n"
            f"    secret-value: {_FAKE_SECRET}\n"
            f"    password: {_FAKE_PASSWORD}\n"
        )

    context = RunContext(
        run_id="abcdefgh",
        results_dir=tmp_path,
        config=None,  # type: ignore[arg-type]
        tenant="osac-conformance-abcdefgh",
        redactor=Redactor(secrets=(_FAKE_SECRET,)),
    )

    path = DiagnosticsCollector(run_command=fake_run).collect(context, context.tenant)

    content = (path / "osac-state.yaml").read_text()
    assert _FAKE_SECRET not in content
    assert "<redacted>" in content
    assert "password:" not in content
    assert (path / "tenant-resources.yaml").exists()
    logs = (path / "pod-logs.txt").read_text()
    assert _FAKE_API_KEY not in logs
    assert _FAKE_SESSION not in logs
    assert all("helm" not in " ".join(call).lower() for call in calls)
