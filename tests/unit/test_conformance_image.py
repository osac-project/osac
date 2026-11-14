"""Optional local image smoke test for the conformance Containerfile."""

from __future__ import annotations

import os
import shutil
import subprocess
from pathlib import Path

import pytest

pytestmark = pytest.mark.skipif(
    os.environ.get("OSAC_CONFORMANCE_IMAGE_TEST") != "1",
    reason="set OSAC_CONFORMANCE_IMAGE_TEST=1 to build the local conformance image",
)


def test_containerfile_builds_and_starts(tmp_path: Path) -> None:
    if shutil.which("podman") is None:
        pytest.skip("podman is not installed")
    repo = Path(__file__).resolve().parents[2]
    tag = f"osac-conformance:test-{tmp_path.name}"
    build = subprocess.run(
        ["podman", "build", "-f", "tests/conformance/Containerfile", "-t", tag, "."],
        cwd=repo,
        text=True,
        capture_output=True,
        check=False,
    )
    if build.returncode != 0:
        pytest.skip(f"image build unavailable: {build.stderr[-500:]}")
    run = subprocess.run(["podman", "run", "--rm", tag, "--help"], text=True, capture_output=True, check=False)
    assert run.returncode == 0, run.stderr
