"""End-to-end core smoke test with a fake lab and synthetic pytest tree."""

from __future__ import annotations

from pathlib import Path

import yaml

from tests.conformance.area import load_areas
from tests.conformance.cli import Runtime, main
from tests.conformance.config import load_config
from tests.conformance.output import OutputSink
from tests.conformance.redact import Redactor
from tests.conformance.testing.fake_lab import FakeLab, FakeLabClient
from tests.conformance.testtree import PytestTestTree


def _config(path: Path, token: Path, ca: Path, hub: Path) -> Path:
    path.write_text(
        yaml.safe_dump(
            {
                "osacRelease": "1.4.0",
                "lab": {
                    "selectors": ["generic"],
                    "kubeconfig": str(hub),
                    "apiURL": "http://lab.example.test",
                    "tokenFile": str(token),
                    "caFile": str(ca),
                },
                "networking": {},
            },
            sort_keys=False,
        )
    )
    return path


def _area(path: Path) -> Path:
    path.write_text(
        yaml.safe_dump(
            {
                "name": "networking",
                "componentTests": ["test_passes"],
                "profiles": {},
                "identityFields": ["name"],
                "configurationErrors": [],
                "supportCondition": "conformant",
                "registrationSource": "registration",
                "titles": {},
                "selectors": {"generic": ["test_passes"]},
                "checks": [],
                "timeoutDefaults": {},
                "componentJobTimeout": "60m",
                "settingsSource": "settings",
                "evidence": [],
                "referenceComponents": ["mock"],
            },
            sort_keys=False,
        )
    )
    return path


def _tree(path: Path) -> Path:
    path.mkdir()
    path.joinpath("test_sample.py").write_text(
        "import pytest\n"
        "@pytest.mark.conformance_generic\n"
        "@pytest.mark.sig_networking\n"
        "@pytest.mark.conformance_blocking\n"
        "def test_passes():\n"
        "    print('secret-value')\n"
    )
    return path


def test_list_and_run_the_core_against_a_fake_lab(tmp_path: Path) -> None:
    token = tmp_path / "token"
    token.write_text("admin")
    ca = tmp_path / "ca.pem"
    ca.write_text("ca")
    hub = tmp_path / "hub"
    hub.write_text("hub")
    config_path = _config(tmp_path / "conformance.yaml", token, ca, hub)
    areas_dir = tmp_path / "areas"
    areas_dir.mkdir()
    _area(areas_dir / "networking.yaml")
    tree_dir = _tree(tmp_path / "tree")
    results = tmp_path / "results"
    results.mkdir()

    config = load_config(config_path, area_names={"networking"})
    redactor = Redactor(secrets=("secret-value",))
    runtime = Runtime(
        config=config,
        areas=load_areas(areas_dir),
        tree=PytestTestTree(root=tree_dir),
        lab=FakeLabClient(FakeLab()),
        results=results,
        redactor=redactor,
        output=OutputSink(results, redactor),
        suite_image_digest="sha256:test",
    )

    assert main(["list"], runtime=runtime) == 0
    plan = results / "conformance-plan.yaml"
    assert plan.exists()
    assert "test_passes" in plan.read_text()

    assert main(["run", "--plan", str(plan)], runtime=runtime) == 0

    report = yaml.safe_load((results / "conformance-report.yaml").read_text())
    assert report["result"] == "conformant"
    assert report["complete"] is True
    assert report["tests"][0]["result"] == "pass"
    assert (results / "junit.xml").exists()
    assert "secret-value" not in (results / "conformance.log").read_text()
    for path in results.rglob("*"):
        if path.is_file():
            assert "secret-value" not in path.read_text(errors="ignore")
