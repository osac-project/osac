"""End-to-end core smoke test with a synthetic pytest tree."""

from __future__ import annotations

from pathlib import Path

import pytest
import yaml

from tests.conformance.catalog import Catalog, ProfileDefinition
from tests.conformance.cli import Runtime, main
from tests.conformance.config import load_config
from tests.conformance.interrupt import Interrupted, InterruptGate
from tests.conformance.model import Plan, Result, RunContext, Selector, TestIdentity, TestOutcome
from tests.conformance.output import OutputSink
from tests.conformance.redact import Redactor
from tests.conformance.testtree import PytestTestTree

_REDACTION_MARKER = "redaction-marker"


def _config(path: Path, token: Path, ca: Path, hub: Path) -> Path:
    path.write_text(
        yaml.safe_dump(
            {
                "osacRelease": "1.4.0",
                "profiles": ["vm"],
                "target": {
                    "kubeconfig": str(hub),
                    "apiURL": "http://target.example.test",
                    "tokenFile": str(token),
                    "caFile": str(ca),
                },
            },
            sort_keys=False,
        )
    )
    return path


def _catalog() -> Catalog:
    return Catalog(
        selectors={
            "generic": Selector("generic", "marker", ("conformance_generic",)),
            "vmaas": Selector("vmaas", "marker", ("conformance_vmaas",)),
            "bmaas": Selector("bmaas", "marker", ("conformance_bmaas",)),
        },
        profiles={
            "generic": ProfileDefinition("generic", ("generic",)),
            "vm": ProfileDefinition("vm", ("vmaas",)),
            "baremetal": ProfileDefinition("baremetal", ("bmaas",)),
        },
    )


def _tree(path: Path) -> Path:
    path.mkdir()
    path.joinpath("test_sample.py").write_text(
        "import pytest\n"
        "@pytest.mark.conformance_generic\n"
        "@pytest.mark.conformance_vmaas\n"
        "@pytest.mark.conformance_blocking\n"
        "def test_passes():\n"
        f"    print('{_REDACTION_MARKER}')\n"
        "@pytest.mark.conformance_generic\n"
        "@pytest.mark.conformance_vmaas\n"
        "@pytest.mark.conformance_blocking\n"
        "def test_also_passes():\n"
        f"    print('{_REDACTION_MARKER}')\n"
    )
    return path


def _runtime(tmp_path: Path, results: Path, tree_dir: Path) -> Runtime:
    token = tmp_path / "token"
    token.write_text("admin")
    ca = tmp_path / "ca.pem"
    ca.write_text("ca")
    hub = tmp_path / "hub"
    hub.write_text("hub")
    config = load_config(_config(tmp_path / "conformance.yaml", token, ca, hub))
    redactor = Redactor(secrets=(_REDACTION_MARKER,))
    return Runtime(
        config=config,
        catalog=_catalog(),
        tree=PytestTestTree(root=tree_dir),
        results=results,
        redactor=redactor,
        output=OutputSink(results, redactor),
        suite_image_digest="sha256:test",
    )


def test_list_and_run_the_core_against_a_synthetic_tree(tmp_path: Path) -> None:
    tree_dir = _tree(tmp_path / "tree")
    results = tmp_path / "results"
    results.mkdir()
    runtime = _runtime(tmp_path, results, tree_dir)

    assert main(["list"], runtime=runtime) == 0
    plan = results / "conformance-plan.yaml"
    assert plan.exists()
    assert "test_passes" in plan.read_text()

    assert main(["run", "--plan", str(plan)], runtime=runtime) == 0

    report = yaml.safe_load((results / "conformance-report.yaml").read_text())
    assert report["result"] == "conformant"
    assert report["complete"] is True
    assert report["tests"][0]["result"] == "pass"
    # The report describes the selection, not the whole catalog.
    assert [selector["id"] for selector in report["selectors"]] == ["generic", "vmaas"]
    assert [profile["id"] for profile in report["profiles"]] == ["generic", "vm"]
    assert [check["id"] for check in report["checks"]] == ["all-blocking-tests-pass", "no-leftovers", "complete-plan"]
    assert (results / "junit.xml").exists()
    assert _REDACTION_MARKER not in (results / "conformance.log").read_text()

    focused_results = tmp_path / "focused-results"
    focused_results.mkdir()
    focused_runtime = _runtime(tmp_path, focused_results, tree_dir)
    focused_plan = focused_results / "conformance-plan.yaml"
    focused_plan.write_text("tests:\n  - id: test_sample.py::test_passes\n")
    assert main(["run", "--plan", str(focused_plan)], runtime=focused_runtime) == 0
    focused_report = yaml.safe_load((focused_results / "conformance-report.yaml").read_text())
    vm = next(profile for profile in focused_report["profiles"] if profile["id"] == "vm")
    assert vm["required"] is True
    assert vm["result"] == "pass"
    assert focused_report["complete"] is False
    for path in results.rglob("*"):
        if path.is_file():
            assert _REDACTION_MARKER not in path.read_text(errors="ignore")


def test_empty_plan_is_a_config_error(tmp_path: Path, capsys: pytest.CaptureFixture[str]) -> None:
    tree_dir = _tree(tmp_path / "tree")
    results = tmp_path / "results"
    results.mkdir()
    runtime = _runtime(tmp_path, results, tree_dir)
    empty_plan = tmp_path / "empty-plan.yaml"
    empty_plan.write_text("tests: []\n")

    assert main(["run", "--plan", str(empty_plan)], runtime=runtime) == 2
    assert "no tests" in capsys.readouterr().err


def test_unknown_profile_is_a_config_error(tmp_path: Path, capsys: pytest.CaptureFixture[str]) -> None:
    token = tmp_path / "token"
    token.write_text("admin")
    ca = tmp_path / "ca.pem"
    ca.write_text("ca")
    hub = tmp_path / "hub"
    hub.write_text("hub")
    config_path = tmp_path / "conformance.yaml"
    config_path.write_text(
        yaml.safe_dump(
            {
                "osacRelease": "1.4.0",
                "profiles": ["frobnicate"],
                "target": {
                    "kubeconfig": str(hub),
                    "apiURL": "http://target.example.test",
                    "tokenFile": str(token),
                    "caFile": str(ca),
                },
            },
            sort_keys=False,
        )
    )
    catalog_path = tmp_path / "catalog.yaml"
    catalog_path.write_text(
        yaml.safe_dump(
            {
                "selectors": [{"id": "generic", "type": "marker", "markers": ["conformance_generic"]}],
                "profiles": [{"id": "generic", "selectors": ["generic"]}],
            },
            sort_keys=False,
        )
    )

    assert (
        main(
            [
                "list",
                "--config",
                str(config_path),
                "--results",
                str(tmp_path / "results"),
                "--catalog",
                str(catalog_path),
                "--tree",
                str(tmp_path / "tree"),
            ]
        )
        == 2
    )
    assert "frobnicate" in capsys.readouterr().err


def test_list_leaves_no_artifact_that_blocks_a_plan_run(tmp_path: Path) -> None:
    tree_dir = _tree(tmp_path / "tree")
    token = tmp_path / "token"
    token.write_text("admin")
    ca = tmp_path / "ca.pem"
    ca.write_text("ca")
    hub = tmp_path / "hub"
    hub.write_text("hub")
    config = _config(tmp_path / "conformance.yaml", token, ca, hub)
    catalog = tmp_path / "catalog.yaml"
    catalog.write_text(
        yaml.safe_dump(
            {
                "selectors": [
                    {"id": "generic", "type": "marker", "markers": ["conformance_generic"]},
                    {"id": "vmaas", "type": "marker", "markers": ["conformance_vmaas"]},
                ],
                "profiles": [{"id": "generic", "selectors": ["generic"]}, {"id": "vm", "selectors": ["vmaas"]}],
            },
            sort_keys=False,
        )
    )
    results = tmp_path / "results"
    args = ["--config", str(config), "--results", str(results), "--catalog", str(catalog), "--tree", str(tree_dir)]

    assert main(["list", *args]) == 0
    assert not (results / "conformance.log").exists()

    assert main(["run", "--plan", str(results / "conformance-plan.yaml"), *args]) == 0
    assert (results / "conformance-report.yaml").exists()


class _InterruptingTree:
    """A tree whose only run stops after the first test finished."""

    def __init__(self, tests: tuple[TestIdentity, ...]) -> None:
        self.tests = tests

    def collect(self) -> tuple[TestIdentity, ...]:
        return self.tests

    def run(
        self, plan: Plan, context: RunContext, *, keep: bool, interrupt: InterruptGate | None = None
    ) -> tuple[TestOutcome, ...]:
        raise Interrupted((TestOutcome(plan.tests[0], Result.PASS, 1.0, "", context.results_dir),))


def test_an_interrupt_reports_the_tests_that_already_finished(tmp_path: Path) -> None:
    tree_dir = _tree(tmp_path / "tree")
    results = tmp_path / "results"
    results.mkdir()
    runtime = _runtime(tmp_path, results, tree_dir)
    runtime.tree = _InterruptingTree(runtime.tree.collect())

    assert main(["run"], runtime=runtime) == 130

    report = yaml.safe_load((results / "conformance-report.yaml").read_text())
    assert [test["result"] for test in report["tests"]] == ["pass", "skip"]
    assert report["result"] == "in progress"
