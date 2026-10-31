from __future__ import annotations

import ast
import json
import re
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[2]
NETWORKING_TESTS = "tests/e2e/networking/**"
NETWORKING_DIR = ROOT / "tests/e2e/networking"
GENERIC_SUITES = (
    ("e2e-vmaas-full-install.yml", "e2e-vmaas-full-install", "vmaas"),
    ("e2e-bmaas-full-install.yml", "e2e-bmaas-full-install", "bmaas"),
)


def _pytest_markers(path: Path) -> set[str]:
    module = ast.parse(path.read_text())
    for node in module.body:
        if not isinstance(node, ast.Assign):
            continue
        if not any(isinstance(target, ast.Name) and target.id == "pytestmark" for target in node.targets):
            continue
        marker_nodes = node.value.elts if isinstance(node.value, (ast.List, ast.Tuple)) else [node.value]
        markers = set()
        for marker in marker_nodes:
            if isinstance(marker, ast.Attribute):
                markers.add(marker.attr)
            if isinstance(marker, ast.Call) and isinstance(marker.func, ast.Attribute):
                markers.add(marker.func.attr)
        return markers
    return set()


def test_networking_tests_carry_no_backend_markers() -> None:
    files = sorted(NETWORKING_DIR.rglob("*.py"))
    assert files
    for path in files:
        markers = _pytest_markers(path)
        assert not any(m.startswith("requires_netris") or m == "requires_stretched_networking" for m in markers), path


def test_generic_workflows_cannot_select_networking_tests() -> None:
    for filename, job_name, service in GENERIC_SUITES:
        workflow = yaml.safe_load((ROOT / ".github/workflows" / filename).read_text())
        expression = workflow["jobs"][job_name]["with"]["test-suite"]
        # Every literal the expression can yield stays in the service directory.
        # Result literals follow `&&` or `||`; comparison operands (`== 'schedule'`) do not.
        literals = re.findall(r"(?:&&|\|\|)\s*'([^']+)'", expression)
        assert literals, filename
        for literal in literals:
            assert literal == service or literal.startswith(f"{service}/"), (filename, literal)
        # The only free-form value is the dispatch input, which is a closed choice.
        triggers = workflow.get("on", workflow.get(True))
        dispatch = triggers["workflow_dispatch"]["inputs"]["test-suite"]
        assert dispatch["type"] == "choice", filename
        assert dispatch["default"] in dispatch["options"], filename
        for option in dispatch["options"]:
            assert option == service or option.startswith(f"{service}/"), (filename, option)
        assert "networking" not in expression and not (ROOT / "tests/e2e" / service / "networking").exists()


def test_generic_vmaas_path_filter_skips_netris_only_tests() -> None:
    filters = yaml.safe_load((ROOT / ".github/filters/ci-filters.yml").read_text())
    assert f"!{NETWORKING_TESTS}" in filters["vmaas-outside-known-safe"]


def test_netris_suites_are_separate_manual_suites() -> None:
    policy = yaml.safe_load((ROOT / ".github/e2e-policy.yml").read_text())
    jobs = policy["jobs"]
    expected = {
        "e2e-bmaas-netris": {
            "suite": "bmaas-netris",
            "workflow": "e2e-pr-bmaas-netris.yml",
            "workflow_name": "E2E BMaaS Netris PR suite",
            "check": "E2E / BMaaS Netris Full Install",
            "runner": "e2e-bmaas-netris-full-install.yml",
        },
        "e2e-vmaas-netris": {
            "suite": "vmaas-netris",
            "workflow": "e2e-pr-vmaas-netris.yml",
            "workflow_name": "E2E VMaaS Netris PR suite",
            "check": "E2E / VMaaS Netris Full Install",
            "runner": "e2e-vmaas-netris-full-install.yml",
        },
        "e2e-stretched-netris": {
            "suite": "stretched-netris",
            "workflow": "e2e-pr-stretched-netris.yml",
            "workflow_name": "E2E Stretched Netris PR suite",
            "check": "E2E / Stretched Netris Full Install",
            "runner": "e2e-stretched-netris-full-install.yml",
        },
    }
    assert {name for name in jobs if "netris" in name} == set(expected)
    for policy_name, details in expected.items():
        policy_job = jobs[policy_name]
        assert policy_job["manual_only"] is True
        assert policy_job["suite"] == details["suite"]
        assert policy_job["workflow"] == details["workflow"]
        assert policy_job["check"] == details["check"]

    pr_workflow = yaml.safe_load((ROOT / ".github/workflows/e2e-pr.yml").read_text())
    pr_jobs = pr_workflow["jobs"]
    assert set(pr_jobs) == {"decide", "track", "finish"}
    triggers = pr_workflow.get("on", pr_workflow.get(True))
    workflow_names = set(triggers["workflow_run"]["workflows"])
    for details in expected.values():
        wrapper = yaml.safe_load((ROOT / ".github/workflows" / details["workflow"]).read_text())
        suite = details["suite"]
        assert wrapper["name"] == details["workflow_name"]
        assert f"E2E suite · {suite} ·" in wrapper["run-name"]
        assert wrapper["jobs"][suite]["needs"] == "verify"
        assert wrapper["jobs"][suite]["uses"] == f"./.github/workflows/{details['runner']}"
        assert wrapper["name"] in workflow_names


def test_stretched_networking_test_uses_the_networking_path() -> None:
    markers = _pytest_markers(ROOT / "tests/e2e/networking/stretched/test_shared_subnet_bmi_compute_instance.py")
    assert markers == {"regression"}


def _assert_deploy_inputs(job: dict, *, deploy_suite: str, test_suite: str | None = None) -> None:
    with_ = job["with"]
    assert with_["deploy-suite"] == deploy_suite
    assert with_.get("test-suite") == test_suite
    assert with_["test-infra-ref"] == "main"
    for removed in ("test-infra-sha", "suite", "osac-profile"):
        assert removed not in with_


def test_vmaas_netris_calls_networking_runner_with_vmaas_deploy_suite() -> None:
    workflow = yaml.safe_load((ROOT / ".github/workflows/e2e-vmaas-netris-full-install.yml").read_text())
    job = workflow["jobs"]["e2e-vmaas-netris"]
    workflow_path, workflow_ref = job["uses"].rsplit("@", 1)
    assert workflow_path == "osac-project/osac-test-infra/.github/workflows/e2e-vmaas-netris-full-install.yml"
    assert workflow_ref == "main"
    _assert_deploy_inputs(job, deploy_suite="vmaas")

    caller = yaml.safe_load((ROOT / ".github/workflows/e2e-vmaas-netris-full-install-caller.yml").read_text())
    assert caller["jobs"]["e2e-vmaas-netris"]["uses"] == "./.github/workflows/e2e-vmaas-netris-full-install.yml"


def test_bmaas_netris_calls_networking_runner_with_bmaas_deploy_suite() -> None:
    workflow = yaml.safe_load((ROOT / ".github/workflows/e2e-bmaas-netris-full-install.yml").read_text())
    job = workflow["jobs"]["e2e-bmaas-netris"]
    assert "/e2e-bmaas-netris-full-install.yml@" in job["uses"]
    assert job["uses"].endswith("@main")
    _assert_deploy_inputs(job, deploy_suite="bmaas")


def test_netris_workflows_share_the_test_infra_ref() -> None:
    bmaas = yaml.safe_load((ROOT / ".github/workflows/e2e-bmaas-netris-full-install.yml").read_text())
    vmaas = yaml.safe_load((ROOT / ".github/workflows/e2e-vmaas-netris-full-install.yml").read_text())
    stretched = yaml.safe_load((ROOT / ".github/workflows/e2e-stretched-netris-full-install.yml").read_text())
    refs = {
        bmaas["jobs"]["e2e-bmaas-netris"]["with"]["test-infra-ref"],
        vmaas["jobs"]["e2e-vmaas-netris"]["with"]["test-infra-ref"],
        stretched["jobs"]["e2e-stretched-netris"]["with"]["test-infra-ref"],
    }
    assert refs == {"main"}


def test_stretched_netris_calls_its_runner_with_full_deploy_suite() -> None:
    workflow = yaml.safe_load((ROOT / ".github/workflows/e2e-stretched-netris-full-install.yml").read_text())
    job = workflow["jobs"]["e2e-stretched-netris"]
    assert "/e2e-stretched-netris-full-install.yml@" in job["uses"]
    assert job["uses"].endswith("@main")
    _assert_deploy_inputs(job, deploy_suite="full", test_suite="stretched")


def test_netris_callers_use_a_supported_service_image_key() -> None:
    for filename, job_name in (
        ("e2e-bmaas-netris-full-install.yml", "e2e-bmaas-netris"),
        ("e2e-vmaas-netris-full-install.yml", "e2e-vmaas-netris"),
        ("e2e-stretched-netris-full-install.yml", "e2e-stretched-netris"),
    ):
        workflow = yaml.safe_load((ROOT / ".github/workflows" / filename).read_text())
        components = json.loads(workflow["jobs"][job_name]["with"]["components"])
        service_component = next(
            component for component in components if component["containerfile"] == "fulfillment-service/Containerfile"
        )
        assert service_component["imageKey"] == "service.images.service"


def test_netris_overlays_select_the_managers_of_each_service() -> None:
    expected = {"vmaas-ci": "cudn_evpn", "bmaas-ci": "", "full-ci": "cudn_evpn"}
    for profile, k8s_manager in expected.items():
        overlay = yaml.safe_load((ROOT / "osac-installer/values" / profile / "netris.yaml").read_text())
        assert overlay["global"]["networking"] == {
            "provisioningEnabled": True,
            "fabricManager": "netris",
            "k8sManager": k8s_manager,
        }, profile
        assert overlay["aap"]["instanceGroups"]["networkFulfillment"]["enabled"] is True, profile
    assert not (ROOT / "osac-installer/values/cudn-evpn-netris-test").exists()
