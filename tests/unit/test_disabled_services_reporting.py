"""Exercise reflection failure reporting through real, isolated pytest processes."""

from __future__ import annotations

import os
import subprocess
import sys
from pathlib import Path
from unittest.mock import Mock

import pytest

from tests.e2e.core.grpc_client import PUBLIC_API, GRPCClient
from tests.e2e.core.runner import CommandFailed, CommandTimedOut
from tests.e2e.vmaas.sanity import test_disabled_services as target

ROOT = Path(__file__).resolve().parents[2]
FAKE_TOKEN = "synthetic-reflection-token-not-a-credential"
FAKE_ENV = "synthetic-environment-not-a-credential"


@pytest.mark.parametrize("showlocals", [False, True])
@pytest.mark.parametrize("mode", ["nonzero", "timeout", "missing", "assertion"])
def test_reflection_traceback_keeps_credentials_private(tmp_path: Path, showlocals: bool, mode: str) -> None:
    executable = tmp_path / "grpcurl"
    executable.write_text(
        f"#!{sys.executable}\n"
        "import sys, time\n"
        + ("time.sleep(60)\n" if mode == "timeout" else "")
        + ("print('synthetic grpc diagnostic', file=sys.stderr)\nsys.exit(7)\n" if mode == "nonzero" else "")
    )
    executable.chmod(0o755)
    if mode == "missing":
        executable.unlink()
    (tmp_path / "conftest.py").write_text(
        "import pytest\n"
        "from tests.e2e.core.grpc_client import GRPCClient\n"
        "from tests.e2e.vmaas.sanity import test_disabled_services as target\n"
        "@pytest.fixture(autouse=True)\n"
        "def synthetic_environment(monkeypatch):\n"
        f"    monkeypatch.setattr(target.os, 'environ', {{'PATH': {str(tmp_path)!r}, 'SYNTHETIC': {FAKE_ENV!r}}})\n"
        + (
            "    real_run = target.subprocess.run\n"
            "    def short_run(*args, **kwargs):\n"
            "        kwargs['timeout'] = 0.05\n"
            "        return real_run(*args, **kwargs)\n"
            "    monkeypatch.setattr(target.subprocess, 'run', short_run)\n"
            if mode == "timeout"
            else ""
        )
        + "@pytest.fixture\ndef grpc():\n"
        f"    return GRPCClient(address='synthetic.invalid:443', token={FAKE_TOKEN!r})\n"
    )
    test_file = tmp_path / "test_reflection.py"
    test_file.write_text(
        "from tests.e2e.vmaas.sanity import test_disabled_services as target\n"
        "def test_reflection(grpc):\n"
        "    target.test_disabled_service_absent_from_reflection('synthetic.invalid:443', grpc)\n"
    )
    command = [sys.executable, "-m", "pytest", "-c", "/dev/null", "-p", "no:cacheprovider", "-q", str(test_file)]
    if showlocals:
        command.append("--showlocals")
    report = subprocess.run(
        command,
        capture_output=True,
        text=True,
        timeout=30,
        cwd=tmp_path,
        env={"PATH": os.defpath, "PYTHONPATH": str(ROOT), "PYTEST_DISABLE_PLUGIN_AUTOLOAD": "1"},
        check=False,
    )
    output = report.stdout + report.stderr
    (tmp_path / "report.txt").write_text(output)
    assert report.returncode == 1, output
    assert "1 failed" in output, output
    expected = {
        "nonzero": "exited 7: synthetic grpc diagnostic",
        "timeout": "timed out after 0.05s",
        "missing": "FileNotFoundError",
        "assertion": "Enabled ComputeInstances should appear",
    }
    assert expected[mode] in output, output
    assert FAKE_TOKEN not in output, output
    assert FAKE_ENV not in output, output
    assert "During handling of the above exception" not in output, output
    assert "direct cause" not in output, output


def test_reflection_preserves_command_environment_and_service_assertions(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(target.os, "environ", {"SYNTHETIC": FAKE_ENV})
    grpc = GRPCClient(address="synthetic.invalid:443", token=FAKE_TOKEN)
    execute = Mock(return_value=subprocess.CompletedProcess([], 0, f"  {PUBLIC_API}.ComputeInstances\n", ""))
    monkeypatch.setattr(target.subprocess, "run", execute)
    target.test_disabled_service_absent_from_reflection(grpc.address, grpc)
    execute.assert_called_once_with(
        [
            "grpcurl",
            "-insecure",
            "-expand-headers",
            "-H",
            "Authorization: Bearer ${REFLECTION_JWT}",
            grpc.address,
            "list",
        ],
        capture_output=True,
        text=True,
        timeout=30,
        check=True,
        env={"SYNTHETIC": FAKE_ENV, "REFLECTION_JWT": FAKE_TOKEN},
    )
    assert target.os.environ == {"SYNTHETIC": FAKE_ENV}
    execute.return_value.stdout = f"{PUBLIC_API}.ComputeInstances\n{PUBLIC_API}.BareMetalInstances\n"
    with pytest.raises(AssertionError, match="Disabled BareMetalInstances should not appear"):
        target.test_disabled_service_absent_from_reflection(grpc.address, grpc)


@pytest.mark.parametrize("timeout", [False, True])
def test_reflection_errors_preserve_subprocess_contract(monkeypatch: pytest.MonkeyPatch, timeout: bool) -> None:
    monkeypatch.setattr(target.os, "environ", {"SYNTHETIC": FAKE_ENV})
    grpc = GRPCClient(address="synthetic.invalid:443", token=FAKE_TOKEN)
    stdout = "synthetic partial output"
    stderr = f"synthetic diagnostic Bearer {FAKE_TOKEN}"
    original = (
        subprocess.TimeoutExpired(["grpcurl"], 30, stdout, stderr)
        if timeout
        else subprocess.CalledProcessError(7, ["grpcurl"], stdout, stderr)
    )
    monkeypatch.setattr(target.subprocess, "run", Mock(side_effect=original))
    with pytest.raises(subprocess.TimeoutExpired if timeout else subprocess.CalledProcessError) as raised:
        target._reflection_services(grpc.address, grpc)
    error = raised.value
    assert isinstance(error, CommandTimedOut if timeout else CommandFailed)
    assert error.stdout == stdout
    assert error.stderr == stderr
    assert (error.timeout if timeout else error.returncode) == (30 if timeout else 7)
    assert error.__suppress_context__ and error.__cause__ is None
    assert error.cmd[-2:] == ["<redacted>", "list"]
    assert FAKE_TOKEN not in str(error)
