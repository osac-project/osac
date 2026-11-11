"""Check the execution-environment build's container-runtime CLI contract."""

import os
from pathlib import Path
import subprocess

import pytest


AAP = Path(__file__).resolve().parents[2]


def write_command(path, body):
    path.write_text("#!/bin/sh\nset -eu\n" + body + "\n")
    path.chmod(0o755)


@pytest.mark.parametrize("runtime", ["podman", "docker"])
@pytest.mark.parametrize("absolute_path", [False, True])
@pytest.mark.parametrize("platform", ["", "linux/arm64"])
def test_execution_environment_runtime(tmp_path, runtime, absolute_path, platform):
    bin_dir = tmp_path / "bin"
    bin_dir.mkdir()
    runtime_dir = tmp_path / "selected-runtime" if absolute_path else bin_dir
    runtime_dir.mkdir(exist_ok=True)
    log = tmp_path / "runtime.log"
    selected_runtime = runtime_dir / runtime
    write_command(selected_runtime, 'printf "%s\\n" "$*" >> "$RUNTIME_LOG"')
    if absolute_path:
        # A different binary on PATH must not replace the requested executable.
        write_command(bin_dir / runtime, "exit 99")

    write_command(
        bin_dir / "uv",
        """
if [ "$1" = sync ]; then exit 0; fi
[ "$1 $2 $3 $4" = 'tool run ansible-builder build' ]
shift 4
runtime=
platform=
while [ "$#" -gt 0 ]; do
    case "$1" in
        --container-runtime) runtime="$2"; shift ;;
        --extra-build-cli-args=*) platform="${1#*=}" ;;
    esac
    shift
done
case "$runtime" in
    podman|docker) ;;
    *) echo "invalid container runtime: $runtime" >&2; exit 2 ;;
esac
"$runtime" build $platform
""",
    )
    result = subprocess.run(
        [
            "make", "--no-print-directory", "-C", str(AAP),
            "execution-environment-build",
            f"CONTAINER_TOOL={selected_runtime if absolute_path else runtime}",
            f"EE_CONTAINER_PLATFORM={platform}",
            "EE_LOCAL_TAG=test-ee", "IMG=test/ee:latest",
        ],
        env={
            **os.environ,
            "PATH": f"{bin_dir}:/usr/bin:/bin",
            "RUNTIME_LOG": str(log),
        },
        text=True,
        capture_output=True,
        timeout=15,
    )
    assert result.returncode == 0, result.stdout + result.stderr
    expected_build = f"build --platform={platform}" if platform else "build"
    assert log.read_text().splitlines() == [
        expected_build, "tag test-ee test/ee:latest",
    ]
