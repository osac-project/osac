"""Optional local image smoke test for the conformance Containerfile."""

from __future__ import annotations

import os
import subprocess
from pathlib import Path

import pytest

from tests.conformance.image import runtime_command

pytestmark = pytest.mark.skipif(
    os.environ.get("OSAC_CONFORMANCE_IMAGE_TEST") != "1",
    reason="set OSAC_CONFORMANCE_IMAGE_TEST=1 to build the local conformance image",
)


def test_containerfile_builds_and_starts(tmp_path: Path) -> None:
    runtime = runtime_command()
    if runtime is None:
        pytest.skip("neither podman nor docker is installed")
    repo = Path(__file__).resolve().parents[2]
    tag = f"osac-conformance:test-{tmp_path.name}"
    build = subprocess.run(
        [runtime, "build", "-f", "tests/conformance/Containerfile", "-t", tag, "."],
        cwd=repo,
        text=True,
        capture_output=True,
        check=False,
    )
    assert build.returncode == 0, f"image build failed: {build.stderr[-2000:]}"
    run = subprocess.run([runtime, "run", "--rm", tag, "--help"], text=True, capture_output=True, check=False)
    assert run.returncode == 0, run.stderr
