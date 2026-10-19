"""Keep E2E echo-adapter image builds aligned with their Containerfile."""

from __future__ import annotations

import json
from pathlib import Path

import pytest
import yaml

ROOT = Path(__file__).resolve().parents[2]
CONTAINERFILE = "osac-metering/adapters/Containerfile.echo-adapter"


@pytest.mark.parametrize("suite", ["vmaas", "caas"])
def test_echo_adapter_build_uses_repository_root(suite: str) -> None:
    workflow = yaml.safe_load((ROOT / f".github/workflows/e2e-{suite}-full-install.yml").read_text())
    components = json.loads(workflow["jobs"][f"e2e-{suite}-full-install"]["with"]["components"])
    adapters = [component for component in components if component["containerfile"] == CONTAINERFILE]

    assert len(adapters) == 1
    assert adapters[0]["context"] == "."
    assert (ROOT / adapters[0]["context"] / "go.work").is_file()
    assert "COPY --chown=1001:0 go.work ./" in (ROOT / CONTAINERFILE).read_text()
