"""End-to-end core smoke test with a fake lab and synthetic pytest tree."""

from __future__ import annotations

from pathlib import Path

import pytest
import yaml

from tests.conformance.area import load_areas
from tests.conformance.cli import Runtime, main
from tests.conformance.config import load_config
from tests.conformance.output import OutputSink
from tests.conformance.profiles import ProfileDefinition
from tests.conformance.redact import Redactor
from tests.conformance.testing.fake_lab import FakeLab, FakeLabClient
from tests.conformance.testtree import PytestTestTree

_FAKE_SECRET = "not-a-real-secret"


def _config(path: Path, token: Path, ca: Path, hub: Path) -> Path:
    path.write_text(
        yaml.safe_dump(
            {
                "osacRelease": "1.4.0",
                "profiles": ["vm"],
                "lab": {
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
                "identityFields": ["name"],
                "configurationErrors": [],
                "supportCondition": "conformant",
                "registrationSource": "networking",
                "titles": {},
                "selectors": {"generic": ["test_passes"], "vmaas": ["test_passes"]},
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


def _catalog() -> dict[str, ProfileDefinition]:
    return {"generic": ProfileDefinition("generic", ("generic",)), "vm": ProfileDefinition("vm", ("vmaas",))}


def _tree(path: Path) -> Path:
    path.mkdir()
    path.joinpath("test_sample.py").write_text(
        "import pytest\n"
        "@pytest.mark.conformance_generic\n"
        "@pytest.mark.conformance_vmaas\n"
        "@pytest.mark.sig_networking\n"
        "@pytest.mark.conformance_blocking\n"
        "def test_passes():\n"
        f"    print('{_FAKE_SECRET}')\n"
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
    (tmp_path / "podman.sock").write_text("socket")

    config = load_config(config_path, area_names={"networking"})
    redactor = Redactor(secrets=(_FAKE_SECRET,))
    runtime = Runtime(
        config=config,
        areas=load_areas(areas_dir),
        profile_catalog=_catalog(),
        tree=PytestTestTree(root=tree_dir),
        lab=FakeLabClient(FakeLab()),
        results=results,
        redactor=redactor,
        output=OutputSink(results, redactor),
        suite_image_digest="sha256:test",
        podman_socket=tmp_path / "podman.sock",
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
    assert _FAKE_SECRET not in (results / "conformance.log").read_text()

    focused_results = tmp_path / "focused-results"
    focused_results.mkdir()
    focused_runtime = Runtime(
        config=config,
        areas=load_areas(areas_dir),
        profile_catalog=_catalog(),
        tree=PytestTestTree(root=tree_dir),
        lab=FakeLabClient(FakeLab()),
        results=focused_results,
        redactor=redactor,
        output=OutputSink(focused_results, redactor),
        suite_image_digest="sha256:test",
        podman_socket=tmp_path / "podman.sock",
    )
    focused_plan = focused_results / "conformance-plan.yaml"
    focused_plan.write_text("tests:\n  - id: test_passes\n")
    assert main(["run", "--plan", str(focused_plan)], runtime=focused_runtime) == 0
    focused_report = yaml.safe_load((focused_results / "conformance-report.yaml").read_text())
    vm = next(profile for profile in focused_report["profiles"] if profile["name"] == "vm")
    assert vm["required"] is False
    assert vm["result"] == "skip"
    for path in results.rglob("*"):
        if path.is_file():
            assert _FAKE_SECRET not in path.read_text(errors="ignore")


def test_missing_podman_socket_and_empty_plan_are_config_errors(
    tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
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
    socket = tmp_path / "podman.sock"
    socket.write_text("socket")
    config = load_config(config_path, area_names={"networking"})
    redactor = Redactor()

    def runtime_for(path: Path, podman_socket: Path) -> Runtime:
        return Runtime(
            config=config,
            areas=load_areas(areas_dir),
            profile_catalog=_catalog(),
            tree=PytestTestTree(root=tree_dir),
            lab=FakeLabClient(FakeLab()),
            results=path,
            redactor=redactor,
            output=OutputSink(path, redactor),
            suite_image_digest="sha256:test",
            podman_socket=podman_socket,
        )

    good = runtime_for(results, socket)
    assert main(["list"], runtime=good) == 0
    plan = results / "conformance-plan.yaml"

    missing_socket = tmp_path / "missing.sock"
    bad_results = tmp_path / "bad-results"
    bad_results.mkdir()
    assert main(["run", "--plan", str(plan)], runtime=runtime_for(bad_results, missing_socket)) == 2
    assert "podman socket" in capsys.readouterr().err

    empty_plan = tmp_path / "empty-plan.yaml"
    empty_plan.write_text("tests: []\n")
    empty_results = tmp_path / "empty-results"
    empty_results.mkdir()
    assert main(["run", "--plan", str(empty_plan)], runtime=runtime_for(empty_results, socket)) == 2
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
                "lab": {
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
    areas_dir = tmp_path / "areas"
    areas_dir.mkdir()
    _area(areas_dir / "networking.yaml")
    results = tmp_path / "results"

    assert (
        main(
            [
                "list",
                "--config",
                str(config_path),
                "--results",
                str(results),
                "--areas",
                str(areas_dir),
                "--tree",
                str(tmp_path / "tree"),
            ]
        )
        == 2
    )
    assert "frobnicate" in capsys.readouterr().err
