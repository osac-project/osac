"""CI selection contracts; set OSAC_TEST_INFRA_DIR to check the companion repo."""

from __future__ import annotations

import os
import re
import subprocess
from pathlib import Path

import pytest
import yaml

ROOT = Path(__file__).resolve().parents[2]
FULL_MARKER = "sanity or regression or requires_caas or reference_common"


def _workflow(root: Path, filename: str) -> dict:
    return yaml.safe_load((root / ".github/workflows" / filename).read_text())


def _selection(expression: str, event: str, suite: str = "", marker: str = "", pr: str = "") -> str:
    # These selectors use only string comparisons and &&/|| short-circuiting.
    # Substitute contexts from the real YAML rather than reimplementing policy.
    values = {
        "github.event_name": event,
        "inputs.test-suite": suite,
        "inputs.test-marker": marker,
        "inputs.pr-number": pr,
        "github.event.pull_request.labels.*.name": ["e2e-serial", "e2e-regression"],
    }
    code = expression.removeprefix("${{").removesuffix("}}").strip()
    for name, value in values.items():
        code = code.replace(name, repr(value))
    code = code.replace("&&", " and ").replace("||", " or ")
    code = " ".join(code.split())
    return eval(code, {"__builtins__": {}, "contains": lambda items, item: item in items})


@pytest.fixture(params=["osac", "test-infra"])
def caller(request: pytest.FixtureRequest) -> dict:
    if request.param == "osac":
        return _workflow(ROOT, "e2e-caas-full-install.yml")
    return _workflow(_infra_root(), "e2e-caas-full-install-caller.yml")


def _infra_root() -> Path:
    path = os.environ.get("OSAC_TEST_INFRA_DIR")
    if not path:
        pytest.skip("Set OSAC_TEST_INFRA_DIR to validate companion test-infra checkout")
    return Path(path)


@pytest.mark.parametrize(
    ("event", "suite", "marker"),
    [
        ("pull_request", "caas/sanity", "sanity"),
        ("merge_group", "caas/sanity", "sanity"),
        ("schedule", "caas", FULL_MARKER),
        ("workflow_dispatch", "caas/sanity", FULL_MARKER),
    ],
)
def test_caller_trigger_policy(caller: dict, event: str, suite: str, marker: str) -> None:
    inputs = caller["jobs"]["e2e-caas-full-install"]["with"]
    assert _selection(inputs["test-suite"], event) == suite
    assert _selection(inputs.get("test-marker", "''"), event) == marker


@pytest.mark.parametrize("event", ["pull_request", "merge_group"])
def test_pr_events_ignore_overrides(caller: dict, event: str) -> None:
    inputs = caller["jobs"]["e2e-caas-full-install"]["with"]
    assert _selection(inputs["test-suite"], event, "caas/regression") == "caas/sanity"
    assert _selection(inputs["test-marker"], event, marker="regression") == "sanity"


def test_dispatch_defaults(caller: dict) -> None:
    # PyYAML's YAML 1.1 loader treats the unquoted Actions key `on` as True.
    triggers = caller[True]
    assert triggers["workflow_dispatch"]["inputs"]["test-suite"]["default"] == "caas/sanity"


def test_manual_overrides(caller: dict) -> None:
    inputs = caller["jobs"]["e2e-caas-full-install"]["with"]
    assert _selection(inputs["test-suite"], "workflow_dispatch", "caas/regression") == "caas/regression"
    assert _selection(inputs.get("test-marker", "''"), "workflow_dispatch", marker="regression") == "regression"


def test_pr_associated_dispatch_ignores_suite_overrides() -> None:
    workflow = _workflow(_infra_root(), "e2e-caas-full-install-caller.yml")
    inputs = workflow["jobs"]["e2e-caas-full-install"]["with"]
    assert _selection(inputs["test-suite"], "workflow_dispatch", "caas/regression", pr="123") == "caas/sanity"
    assert _selection(inputs.get("test-marker", "''"), "workflow_dispatch", marker="regression", pr="123") == "sanity"


@pytest.mark.parametrize("suite", ["caas/sanity", "caas/regression", "caas"])
@pytest.mark.parametrize("marker", ["", FULL_MARKER])
def test_runner_reference_selection(tmp_path: Path, suite: str, marker: str) -> None:
    workflow = _workflow(_infra_root(), "e2e-caas-full-install.yml")
    step = next(step for step in workflow["jobs"]["e2e"]["steps"] if step.get("id") == "test")
    # Execute just the real path-selection block, before filters/container setup.
    script = step["run"].split('TEST_ARGS="tests/e2e/"', 1)[1].split("# Keep compound", 1)[0]
    result = subprocess.run(
        ["bash", "-euc", 'TEST_ARGS="tests/e2e/"' + script + '\nprintf "%s" "$TEST_ARGS"'],
        env={**os.environ, "EFFECTIVE_TEST_SUITE": suite, "TEST_MARKER": marker},
        cwd=tmp_path,
        capture_output=True,
        text=True,
        check=True,
    )
    expected = f"tests/e2e/{suite}/"
    if suite != "caas/sanity" and marker:
        expected += " tests/e2e/references/"
    assert result.stdout == expected


@pytest.mark.parametrize("populated", [False, True])
def test_runner_tier_does_not_fall_back(tmp_path: Path, populated: bool) -> None:
    workflow = _workflow(_infra_root(), "e2e-caas-full-install.yml")
    step = next(
        step
        for step in workflow["jobs"]["e2e"]["steps"]
        if step.get("name", "").startswith("Resolve effective test suite")
    )
    suite_dir = tmp_path / "tests/e2e/caas/sanity"
    suite_dir.mkdir(parents=True)
    if populated:
        (suite_dir / "test_lifecycle.py").touch()
    output = tmp_path / "env"
    result = subprocess.run(
        ["bash", "-euo", "pipefail", "-c", step["run"]],
        env={**os.environ, "TEST_SUITE": "caas/sanity", "GITHUB_ENV": str(output)},
        cwd=tmp_path,
        capture_output=True,
        text=True,
        check=False,
    )
    if populated:
        assert result.returncode == 0, result.stderr
        assert output.read_text().strip() == "EFFECTIVE_TEST_SUITE=caas/sanity"
    else:
        assert result.returncode != 0, "Empty sanity tier must fail, not silently run regression"


def test_pr_selector_has_no_label_override() -> None:
    workflow = _workflow(_infra_root(), "e2e-caas-full-install-caller.yml")
    expression = workflow["jobs"]["e2e-caas-full-install"]["with"]["test-suite"]
    assert not re.search(r"labels.*(e2e-serial|e2e-regression)", expression)


def test_full_regression_bmaas_backed_caas_prerequisites() -> None:
    workflow = _workflow(_infra_root(), "e2e-full-regression.yml")
    steps = workflow["jobs"]["e2e"]["steps"]
    names = [step.get("name") for step in steps]
    ordered = [
        "Boot cluster clone",
        "Setup virtual BareMetalHosts",
        "Install infrastructure operators",
        "Create BMF secrets",
        "Install OSAC",
        "Run E2E tests",
        "Teardown",
    ]
    positions = [names.index(name) for name in ordered]
    assert positions == sorted(positions)
    assert "Setup CaaS agent" not in names
    assert all("setup-caas-agents.sh" not in step.get("run", "") for step in steps)

    by_name = {step.get("name"): step for step in steps}
    bmh = by_name["Setup virtual BareMetalHosts"]
    inventory = by_name["Create BMF secrets"]
    runner = by_name["Run E2E tests"]
    assert "if" not in bmh
    assert bmh["run"].strip() == "bash .github/scripts/setup-virtual-bmh.sh"
    assert bmh["env"]["KUBECONFIG"] == inventory["env"]["KUBECONFIG"] == "${{ runner.temp }}/kubeconfig"
    assert bmh["env"]["BMH_NAMESPACE"] == inventory["env"]["BMH_NAMESPACE"] == "${{ inputs.bmh-namespace }}"
    assert runner["env"]["OSAC_BMH_NAMESPACE"] == bmh["env"]["BMH_NAMESPACE"]
    assert inventory["env"]["INSTALLER_NAMESPACE"] == runner["env"]["OSAC_NAMESPACE"] == "${{ inputs.namespace }}"
    for fragment in (
        "type: metal3",
        "namespace: ${BMH_NAMESPACE}",
        "--from-file=inventory.yaml=",
        "--from-file=management.yaml=",
        "oc create secret generic osac-inventory-config",
        "oc create secret generic osac-management-config",
    ):
        assert fragment in inventory["run"]
    infra = by_name["Install infrastructure operators"]["run"]
    assert "${OVERLAY_DIR}/full-ci-infra.yaml" in infra
    assert "${OVERLAY_DIR}/full-ci-instance.yaml" in infra
    assert "make install-infra PLATFORM=openshift PROFILE=full-ci" in infra
    assert "PLATFORM=openshift PROFILE=full-ci" in by_name["Install OSAC"]["run"]


@pytest.mark.parametrize("test_filter", ["", "test_cluster_create"])
def test_full_regression_runner_arguments(tmp_path: Path, test_filter: str) -> None:
    workflow = _workflow(_infra_root(), "e2e-full-regression.yml")
    step = next(step for step in workflow["jobs"]["e2e"]["steps"] if step.get("id") == "test")
    recording = tmp_path / "podman-args"
    script = 'podman() { printf "%s\\0" "$@" > "$RECORDED_ARGS"; }; ssh-keygen() { :; };\n' + step["run"]
    subprocess.run(
        ["bash", "-euo", "pipefail", "-c", script],
        env={
            **os.environ,
            **step["env"],
            "RUNNER_TEMP": str(tmp_path),
            "RECORDED_ARGS": str(recording),
            "TEST_FILTER": test_filter,
        },
        cwd=tmp_path,
        capture_output=True,
        text=True,
        check=True,
    )
    arguments = recording.read_text().removesuffix("\0").split("\0")
    default_filter = (
        "not test_cluster_provisioning_chain_by_name and not test_cross_tenant_cluster_template_reference "
        "and not test_create_subnet_with_virtual_network_by_name"
    )
    expected_filter = f"({test_filter}) and {default_filter}" if test_filter else default_filter
    assert arguments[:3] == ["run", "--rm", "--network=host"]
    assert arguments[arguments.index("pytest") :] == [
        "pytest",
        "tests/e2e/vmaas/",
        "tests/e2e/references/",
        "tests/e2e/caas/",
        "tests/e2e/bmaas/",
        "--ignore=tests/e2e/vmaas/regression/external_ip",
        "--ignore=tests/e2e/vmaas/test_disabled_services.py",
        "-m",
        "not requires_netris and not requires_stretched_networking",
        "-k",
        expected_filter,
        "-n",
        "0",
        "--junitxml=/tmp/test-output/junit.xml",
        "-v",
    ]
    for mount in (
        "kubeconfig:/root/.kube/config:ro,Z",
        "pull-secret.json:/root/pull-secret:ro,Z",
        "aap-license.zip:/root/aap-license.zip:ro,Z",
        "caas_ssh_key.pub:/root/.ssh/id_rsa.pub:ro,Z",
        "test-output:/tmp/test-output:Z",
    ):
        assert f"{tmp_path}/{mount}" in arguments
    assert f"OSAC_BMH_NAMESPACE={step['env']['OSAC_BMH_NAMESPACE']}" in arguments
    assert f"OSAC_NAMESPACE={step['env']['OSAC_NAMESPACE']}" in arguments
