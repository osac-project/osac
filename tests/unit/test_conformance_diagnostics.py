"""Contract tests for the conformance diagnostics bundle."""

from __future__ import annotations

from pathlib import Path

from tests.conformance.diagnostics import DiagnosticsCollector
from tests.conformance.model import RunContext
from tests.conformance.redact import Redactor


def test_diagnostics_writes_redacted_control_plane_state(tmp_path: Path) -> None:
    calls: list[tuple[str, ...]] = []

    def fake_run(*args: str, timeout: int = 300) -> str:
        calls.append(args)
        return "apiVersion: v1\nsecret-value: s3\n"

    context = RunContext(
        run_id="abcdefgh",
        results_dir=tmp_path,
        config=None,  # type: ignore[arg-type]
        tenant="osac-conformance-abcdefgh",
        redactor=Redactor(secrets=("s3",)),
    )

    path = DiagnosticsCollector(run_command=fake_run).collect(context, context.tenant)

    content = (path / "osac-state.yaml").read_text()
    assert "s3" not in content
    assert "<redacted>" in content
    assert (path / "tenant-resources.yaml").exists()
    assert all("helm" not in " ".join(call).lower() for call in calls)
