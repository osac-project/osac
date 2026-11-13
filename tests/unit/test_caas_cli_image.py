"""Opt-in companion-workflow contracts, not a dependency on unmerged infra code.

Set OSAC_TEST_INFRA_DIR to the test-infra revision under test. Without it these
checks skip, like test_caas_ci_suite_policy.py; standalone monorepo CI does not
require the companion change to have merged.
"""

from __future__ import annotations

import json
import os
import subprocess
from pathlib import Path

import pytest
import yaml


@pytest.fixture
def steps() -> list[dict]:
    directory = os.environ.get("OSAC_TEST_INFRA_DIR")
    if not directory:
        pytest.skip("Set OSAC_TEST_INFRA_DIR to validate the companion workflow revision")
    workflow = Path(directory) / ".github/workflows/e2e-caas-full-install.yml"
    return yaml.safe_load(workflow.read_text())["jobs"]["e2e"]["steps"]


def named(steps: list[dict], name: str) -> dict:
    matches = [step for step in steps if step.get("name") == name]
    assert len(matches) == 1, f"Expected one {name} step"
    return matches[0]


@pytest.fixture
def runner(tmp_path: Path) -> tuple[dict[str, str], Path]:
    output = tmp_path / "output"
    output.touch()
    (tmp_path / "oc").write_text(
        "#!/bin/bash\nset -euo pipefail\n"
        'if [[ "$1" == get ]]; then '
        '[[ "$*" == "get pods -n test -l app=fulfillment-grpc-server -o json" ]]; printf "%s" "$PODS"; '
        'elif [[ "$1" == exec ]]; then '
        '[[ "$*" == "exec fulfillment-ready -n test -c grpc-server -- cat /usr/local/bin/osac" ]]; '
        'printf "pod-cli"; exit "${COPY_STATUS:-0}"; '
        "else exit 91; fi\n"
    )
    (tmp_path / "podman").write_text(
        '#!/bin/bash\nset -euo pipefail\nprintf "%s\\n" "$*" >> "$COMMAND_LOG"\n'
        'case "$1" in\n'
        'pull) exit "${PULL_STATUS:-0}" ;;\n'
        "run) exit 0 ;;\n"
        "create) echo test-container ;;\n"
        'cp) printf "image-cli" > "$3" ;;\n'
        "rm) exit 0 ;;\n"
        'build) if [[ "$*" == *OSAC_CLI_BIN=* ]]; then cp osac-cli-bin image-cli; fi ;;\n'
        "*) exit 92 ;;\nesac\n"
    )
    (tmp_path / "sleep").write_text("#!/bin/bash\nexit 0\n")
    for command in ("oc", "podman", "sleep"):
        (tmp_path / command).chmod(0o755)
    pod = {
        "metadata": {"name": "fulfillment-ready"},
        "status": {
            "conditions": [{"type": "Ready", "status": "True"}],
            "containerStatuses": [{"name": "grpc-server", "ready": True, "imageID": ""}],
        },
    }
    env = {
        **os.environ,
        "PATH": f"{tmp_path}:{os.environ['PATH']}",
        "RUNNER_TEMP": str(tmp_path),
        "GITHUB_OUTPUT": str(output),
        "INSTALLER_NAMESPACE": "test",
        "COMMAND_LOG": str(tmp_path / "commands"),
        "PODS": json.dumps({"items": [pod]}),
        "OSAC_VERSION": "",
        "E2E_IMAGE": "test:run",
        "OSAC_CLI_BIN": "",
        "DEPLOYED_CLI_BIN": "",
        "POD_CLI_BIN": "",
    }
    return env, tmp_path


def execute(step: dict, env: dict[str, str], root: Path) -> subprocess.CompletedProcess[str]:
    return subprocess.run(["bash", "-euc", step["run"]], env=env, cwd=root, capture_output=True, text=True)


def test_implicit_cli_runs_after_install_and_uses_shared_action(steps: list[dict]) -> None:
    resolver = named(steps, "Resolve deployed fulfillment-service image")
    extract = named(steps, "Extract OSAC CLI from deployed fulfillment-service image")
    build = named(steps, "Build test image")
    install = next(step for step in steps if "make install-osac " in step.get("run", ""))
    test = named(steps, "Run E2E tests")
    assert steps.index(install) < steps.index(resolver) < steps.index(extract) < steps.index(build) < steps.index(test)
    for step in (resolver, extract):
        assert "env.OSAC_CLI_BIN == ''" in step["if"]
        assert "inputs.osac-cli-version == ''" in step["if"]
        assert "components" not in step["if"]
    assert extract["uses"] == "./.github/actions/extract-osac-cli"
    assert "deployed-cli-image.outputs.image" in extract["with"]["image"]


@pytest.mark.parametrize(
    "image_id", ["docker-pullable://registry.example:5000/service@sha256:abc", "registry.example/service@sha256:def"]
)
def test_digest_resolves_without_tag_parsing(steps: list[dict], runner: tuple[dict, Path], image_id: str) -> None:
    env, root = runner
    pods = json.loads(env["PODS"])
    pods["items"][0]["spec"] = {"containers": [{"name": "grpc-server", "image": "registry.example/service:main"}]}
    pods["items"][0]["status"]["containerStatuses"].insert(
        0, {"name": "envoy", "ready": True, "imageID": "registry.example/sidecar@sha256:wrong"}
    )
    pods["items"][0]["status"]["containerStatuses"][1]["imageID"] = image_id
    env["PODS"] = json.dumps(pods)
    result = execute(named(steps, "Resolve deployed fulfillment-service image"), env, root)
    assert result.returncode == 0, result.stderr
    assert (root / "output").read_text() == f"image={image_id.removeprefix('docker-pullable://')}\n"
    assert (root / "commands").read_text() == f"pull {image_id.removeprefix('docker-pullable://')}\n"


def test_shared_action_output_reaches_test_image(steps: list[dict], runner: tuple[dict, Path]) -> None:
    env, root = runner
    action_path = Path(os.environ["OSAC_TEST_INFRA_DIR"]) / ".github/actions/extract-osac-cli/action.yml"
    action = yaml.safe_load(action_path.read_text())["runs"]["steps"][0]
    binary = root / "extracted CLI"
    env.update(IMAGE="registry.example/service@sha256:abc", OUTPUT_PATH=str(binary))
    extracted = execute(action, env, root)
    assert extracted.returncode == 0, extracted.stderr
    output = (root / "output").read_text().strip().removeprefix("path=")
    assert output == str(binary)
    assert os.access(output, os.X_OK)
    env["DEPLOYED_CLI_BIN"] = output
    built = execute(named(steps, "Build test image"), env, root)
    assert built.returncode == 0, built.stderr
    assert (root / "image-cli").read_bytes() == binary.read_bytes()
    assert not (root / "osac-cli-bin").exists()


def test_node_local_image_copies_running_pod_cli(steps: list[dict], runner: tuple[dict, Path]) -> None:
    env, root = runner
    result = execute(named(steps, "Resolve deployed fulfillment-service image"), env, root)
    assert result.returncode == 0, result.stderr
    binary = root / "osac-cli-bin"
    assert binary.read_text() == "pod-cli"
    assert os.access(binary, os.X_OK)
    assert (root / "output").read_text() == f"pod-cli-path={binary}\n"
    assert not (root / "commands").exists()


def test_resolution_ignores_terminating_and_unready_pods(steps: list[dict], runner: tuple[dict, Path]) -> None:
    env, root = runner
    pod = json.loads(env["PODS"])["items"][0]
    terminating = {**pod, "metadata": {"name": "old", "deletionTimestamp": "2026-01-01T00:00:00Z"}}
    unready = {**pod, "status": {"conditions": [], "containerStatuses": []}}
    env["PODS"] = json.dumps({"items": [terminating, unready, pod]})
    result = execute(named(steps, "Resolve deployed fulfillment-service image"), env, root)
    assert result.returncode == 0, result.stderr
    assert (root / "osac-cli-bin").read_text() == "pod-cli"


@pytest.mark.parametrize("failure", ["absent-binary", "copy"])
def test_shared_extraction_failure_cannot_select_stable(
    steps: list[dict], runner: tuple[dict, Path], failure: str
) -> None:
    env, root = runner
    action_path = Path(os.environ["OSAC_TEST_INFRA_DIR"]) / ".github/actions/extract-osac-cli/action.yml"
    action = yaml.safe_load(action_path.read_text())["runs"]["steps"][0]
    (root / "podman").write_text(
        "#!/bin/bash\nset -euo pipefail\n"
        'case "$1" in\n'
        'run) [[ "$FAILURE" != absent-binary ]] ;;\n'
        "create) echo test-container ;;\n"
        "cp) exit 42 ;;\n"
        "rm) exit 0 ;;\n"
        "*) exit 93 ;;\nesac\n"
    )
    env.update(IMAGE="registry.example/service@sha256:abc", OUTPUT_PATH=str(root / "extracted"), FAILURE=failure)
    result = execute(action, env, root)
    if failure == "copy":
        assert result.returncode == 42
    else:
        assert result.returncode == 0, result.stderr
        assert (root / "output").read_text() == "path=\n"
        build = execute(named(steps, "Build test image"), env, root)
        assert build.returncode != 0
        assert "No CLI extracted" in build.stdout
    assert not (root / "extracted").exists()


@pytest.mark.parametrize("failure", ["no-ready-pod", "pod-copy", "pull"])
def test_resolution_failures_abort(steps: list[dict], runner: tuple[dict, Path], failure: str) -> None:
    env, root = runner
    if failure == "no-ready-pod":
        env["PODS"] = '{"items": []}'
    elif failure == "pod-copy":
        env["COPY_STATUS"] = "42"
    else:
        pods = json.loads(env["PODS"])
        pods["items"][0]["status"]["containerStatuses"][0]["imageID"] = "registry.example/service@sha256:abc"
        env["PODS"] = json.dumps(pods)
        env["PULL_STATUS"] = "42"
    result = execute(named(steps, "Resolve deployed fulfillment-service image"), env, root)
    assert result.returncode != 0
    assert "::error::" in result.stdout
    assert not (root / "osac-cli-bin").exists()


@pytest.mark.parametrize(
    "source", ["OSAC_CLI_BIN", "DEPLOYED_CLI_BIN", "POD_CLI_BIN", "explicit", "missing", "invalid"]
)
def test_build_cli_precedence_and_no_implicit_stable_fallback(
    steps: list[dict], runner: tuple[dict, Path], source: str
) -> None:
    env, root = runner
    if source == "explicit":
        env["OSAC_VERSION"] = "0.0.134"
    elif source == "invalid":
        env["DEPLOYED_CLI_BIN"] = str(root / "absent")
    elif source != "missing":
        binary = root / "selected-cli"
        binary.write_text(source)
        env[source] = str(binary)
        env["OSAC_VERSION"] = "0.0.134"
        if source == "OSAC_CLI_BIN":
            env["DEPLOYED_CLI_BIN"] = str(root / "absent")
    result = execute(named(steps, "Build test image"), env, root)
    if source in ("missing", "invalid"):
        assert result.returncode != 0
        assert "::error::" in result.stdout
        assert not (root / "commands").exists()
    else:
        assert result.returncode == 0, result.stderr
        if source == "explicit":
            assert "OSAC_VERSION=0.0.134" in (root / "commands").read_text()
        else:
            assert (root / "image-cli").read_text() == source
    assert not (root / "osac-cli-bin").exists()
