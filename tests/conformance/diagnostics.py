"""Collect the conformance diagnostics bundle through the lab credentials."""

from __future__ import annotations

from collections.abc import Callable
from pathlib import Path

import yaml

from tests.conformance.model import RunContext
from tests.conformance.redact import Redactor
from tests.e2e.core.runner import run


class DiagnosticsCollector:
    def __init__(self, *, run_command: Callable[..., str] | None = None) -> None:
        self.run_command = run_command or run

    def _safe(self, *args: str) -> str:
        try:
            return self.run_command(*args, timeout=300)
        except Exception as exc:
            return f"diagnostics command failed: {exc}"

    def collect(self, context: RunContext, tenant: str | None) -> Path:
        output = context.results_dir / "diagnostics"
        output.mkdir(parents=True, exist_ok=True)
        redactor = context.redactor or Redactor()
        kubeconfig = context.config.lab_kubeconfig if context.config is not None else Path("/config/lab-hub.kubeconfig")
        base = ("kubectl", "--kubeconfig", str(kubeconfig))
        files = {
            "osac-state.yaml": self._safe(*base, "get", "pods,deployments,events", "-n", "osac", "-o", "yaml"),
            "pod-logs.txt": self._safe(
                *base, "logs", "-n", "osac", "-l", "app.kubernetes.io/part-of=osac", "--tail=2000"
            ),
        }
        if tenant:
            files["tenant-resources.yaml"] = self._safe(
                *base, "get", "all", "-A", "-l", f"osac.openshift.io/tenant={tenant}", "-o", "yaml"
            )
        for name, content in files.items():
            (output / name).write_text(_render(name, content, redactor), encoding="utf-8")
        return output


def _render(name: str, content: str, redactor: Redactor) -> str:
    """Redact YAML by key as well as by value, and other output as text."""
    if name.endswith((".yaml", ".yml")):
        try:
            document = yaml.safe_load(content)
        except yaml.YAMLError:
            return redactor.text(content)
        if isinstance(document, (dict, list)):
            return yaml.safe_dump(redactor.obj(document), sort_keys=False)
    return redactor.text(content)
