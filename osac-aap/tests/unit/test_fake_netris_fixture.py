"""Contracts for the Netris test provider's listener configuration."""

from __future__ import annotations

import importlib.util
import json
import subprocess
import sys
import time
from pathlib import Path
from urllib.request import ProxyHandler, Request, build_opener

import pytest

FAKE_NETRIS_PATH = (
    Path(__file__).resolve().parents[1]
    / "integration"
    / "fixtures"
    / "netris"
    / "fake_netris.py"
)
SPEC = importlib.util.spec_from_file_location("fake_netris_listener_test", FAKE_NETRIS_PATH)
assert SPEC and SPEC.loader
fake_netris = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(fake_netris)


@pytest.mark.parametrize(
    ("host", "expected"),
    [(None, "127.0.0.1"), ("0.0.0.0", "0.0.0.0")],
    ids=["default-loopback", "configured-container-listener"],
)
def test_listener_uses_configured_host_and_keeps_loopback_default(host, expected):
    options = {} if host is None else {"host": host}
    server = fake_netris.Netris(port=0, **options)
    try:
        assert server.server_address[0] == expected
    finally:
        server.server_close()


def test_command_line_starts_a_fake_with_an_explicit_host(tmp_path):
    port_file = tmp_path / "port"
    process = subprocess.Popen(
        [
            sys.executable,
            str(FAKE_NETRIS_PATH),
            "--port-file",
            str(port_file),
            "--host",
            "0.0.0.0",
        ],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.PIPE,
        text=True,
    )
    try:
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline:
            if process.poll() is not None:
                _, stderr = process.communicate(timeout=1)
                pytest.fail(f"fake Netris exited before writing its port: {stderr}")
            if port_file.exists() and port_file.read_text(encoding="utf-8").strip():
                break
            time.sleep(0.05)
        else:
            pytest.fail("fake Netris did not write its listening port")

        port = port_file.read_text(encoding="utf-8").strip()
        opener = build_opener(ProxyHandler({}))
        with opener.open(Request(f"http://127.0.0.1:{port}/__admin/state"), timeout=2) as response:
            state = json.load(response)
        assert state["vpcs"] == []
    finally:
        if process.poll() is None:
            process.terminate()
            process.wait(timeout=5)
