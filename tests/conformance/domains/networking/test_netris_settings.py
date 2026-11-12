"""Informing Netris settings check for the networking domain."""

from __future__ import annotations

import json
import os
import subprocess
from pathlib import Path

import pytest
import yaml

_CONFIG_PATH = Path(os.environ.get("OSAC_CONFORMANCE_CONFIG", "/config/conformance.yaml"))
_CONFIGMAP = "network-fulfillment-ig"
_SETTING = "NETRIS_CONTROLLER_URL"

pytestmark = pytest.mark.skipif(
    not _CONFIG_PATH.exists(), reason="requires a conformance configuration with a target installation"
)


def _target() -> tuple[Path, str]:
    document = yaml.safe_load(_CONFIG_PATH.read_text())
    target = document.get("target", {})
    return Path(target["kubeconfig"]), target.get("namespace", "osac")


@pytest.mark.conformance_netris
@pytest.mark.conformance_informing
@pytest.mark.sig_networking
def test_netris_settings_are_present() -> None:
    kubeconfig, namespace = _target()
    result = subprocess.run(
        ["kubectl", "--kubeconfig", str(kubeconfig), "get", "configmap", _CONFIGMAP, "-n", namespace, "-o", "json"],
        capture_output=True,
        text=True,
        check=False,
    )
    assert result.returncode == 0, f"cannot read {_CONFIGMAP} in {namespace}: {result.stderr.strip()}"
    data = json.loads(result.stdout).get("data", {})
    assert data.get(_SETTING), f"{_SETTING} is not set in {_CONFIGMAP}"
