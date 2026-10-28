from __future__ import annotations

import ast
import json
import re
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[2]
NETWORKING_TESTS = "tests/e2e/networking/**"
VMAAS_TEST_FILE = "tests/e2e/networking/vmaas/test_vmaas_networking.py"
NETRIS_MARKER = "requires_netris_vmaas"


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


def test_netris_vmaas_tests_have_a_dedicated_marker() -> None:
    test_file = ROOT / VMAAS_TEST_FILE
    assert {"requires_netris", NETRIS_MARKER} <= _pytest_markers(test_file)


def test_generic_vmaas_workflow_excludes_netris_tests() -> None:
    workflow = yaml.safe_load((ROOT / ".github/workflows/e2e-vmaas-full-install.yml").read_text())
    test_marker = workflow["jobs"]["e2e-vmaas-full-install"]["with"]["test-marker"]
    assert "and not requires_netris" in test_marker


def test_generic_bmaas_workflow_excludes_netris_tests() -> None:
    workflow = yaml.safe_load((ROOT / ".github/workflows/e2e-bmaas-full-install.yml").read_text())
    test_marker = workflow["jobs"]["e2e-bmaas-full-install"]["with"]["test-marker"]
    assert "and not requires_netris" in test_marker


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
            "workflow_name": "E2E Stretched Networking Netris PR suite",
            "check": "E2E / Stretched Networking Netris Full Install",
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


def test_stretched_networking_test_uses_a_backend_agnostic_marker() -> None:
    markers = _pytest_markers(ROOT / "tests/e2e/networking/stretched/test_shared_subnet_bmi_compute_instance.py")
    assert {"regression", "requires_stretched_networking"} <= markers
    assert not any(marker.startswith("requires_netris") for marker in markers)


def test_vmaas_netris_calls_networking_runner_with_vmaas_profile() -> None:
    workflow = yaml.safe_load((ROOT / ".github/workflows/e2e-vmaas-netris-full-install.yml").read_text())
    job = workflow["jobs"]["e2e-vmaas-netris"]
    workflow_path, workflow_sha = job["uses"].rsplit("@", 1)
    assert workflow_path == "osac-project/osac-test-infra/.github/workflows/e2e-vmaas-netris-full-install.yml"
    assert job["with"]["suite"] == "vmaas_networking"
    assert job["with"]["osac-profile"] == "cudn-evpn-netris-test"
    test_infra_sha = job["with"]["test-infra-sha"]
    assert workflow_sha == test_infra_sha
    assert re.fullmatch(r"[0-9a-f]{40}", test_infra_sha)

    caller = yaml.safe_load((ROOT / ".github/workflows/e2e-vmaas-netris-full-install-caller.yml").read_text())
    assert caller["jobs"]["e2e-vmaas-netris"]["uses"] == "./.github/workflows/e2e-vmaas-netris-full-install.yml"

    pr_workflow = yaml.safe_load((ROOT / ".github/workflows/e2e-pr.yml").read_text())
    pr_job = pr_workflow["jobs"]["vmaas-netris"]
    assert pr_job["uses"] == "./.github/workflows/e2e-vmaas-netris-full-install.yml"
    assert "contains(needs.decide.outputs.suites, '\"vmaas-netris\"')" in pr_job["if"]


def test_bmaas_netris_calls_networking_runner_with_bmaas_profile() -> None:
    workflow = yaml.safe_load((ROOT / ".github/workflows/e2e-bmaas-netris-full-install.yml").read_text())
    job = workflow["jobs"]["e2e-bmaas-netris"]
    assert "/e2e-bmaas-netris-full-install.yml@" in job["uses"]
    assert job["with"]["suite"] == "bmaas_networking"
    assert job["with"]["osac-profile"] == "bmaas-ci"
    assert job["uses"].endswith(f"@{job['with']['test-infra-sha']}")
    assert re.fullmatch(r"[0-9a-f]{40}", job["with"]["test-infra-sha"])


def test_netris_workflows_share_the_test_infra_revision() -> None:
    bmaas = yaml.safe_load((ROOT / ".github/workflows/e2e-bmaas-netris-full-install.yml").read_text())
    vmaas = yaml.safe_load((ROOT / ".github/workflows/e2e-vmaas-netris-full-install.yml").read_text())
    stretched = yaml.safe_load((ROOT / ".github/workflows/e2e-stretched-netris-full-install.yml").read_text())
    assert (
        bmaas["jobs"]["e2e-bmaas-netris"]["with"]["test-infra-sha"]
        == vmaas["jobs"]["e2e-vmaas-netris"]["with"]["test-infra-sha"]
        == stretched["jobs"]["e2e-stretched-netris"]["with"]["test-infra-sha"]
    )


def test_stretched_netris_calls_its_runner_with_full_profile() -> None:
    workflow = yaml.safe_load((ROOT / ".github/workflows/e2e-stretched-netris-full-install.yml").read_text())
    job = workflow["jobs"]["e2e-stretched-netris"]
    assert "/e2e-stretched-netris-full-install.yml@" in job["uses"]
    assert job["with"]["suite"] == "stretched_networking"
    assert job["with"]["osac-profile"] == "full-ci"
    assert job["uses"].endswith(f"@{job['with']['test-infra-sha']}")
    assert re.fullmatch(r"[0-9a-f]{40}", job["with"]["test-infra-sha"])


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
