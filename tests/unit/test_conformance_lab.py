"""Contract tests for reading lab facts."""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from tests.conformance.area import AreaDeclaration
from tests.conformance.config import Config
from tests.conformance.errors import ConfigError
from tests.conformance.lab import HttpLabClient, KubectlLabClient, read_lab
from tests.conformance.testing.fake_lab import FakeLab, FakeLabClient


def _config(tmp_path: Path, *, token: str = "admin-token") -> Config:
    token_file = tmp_path / "admin-token"
    token_file.write_text(token)
    ca_file = tmp_path / "ca.pem"
    ca_file.write_text("ca")
    kubeconfig = tmp_path / "hub.kubeconfig"
    kubeconfig.write_text("hub")
    return Config(
        osac_release="1.4.0",
        selectors=("generic",),
        lab_kubeconfig=kubeconfig,
        lab_api_url="http://127.0.0.1:1",
        lab_token_file=token_file,
        lab_ca_file=ca_file,
        lab_aap=None,
        timeouts={},
        areas={},
        raw={},
    )


def test_read_lab_reports_enabled_services_components_and_release(tmp_path: Path) -> None:
    fake = FakeLab(
        enabled_services=("vmaas",),
        registrations={"networking": {"name": "acme", "ansibleRole": "acme.fabric"}},
        installed_release="1.4.0",
    )

    facts = read_lab(_config(tmp_path), FakeLabClient(fake))

    assert facts.enabled_services == ("vmaas",)
    assert facts.installed_release == "1.4.0"
    assert facts.components[0].area == "networking"
    assert facts.components[0].fields["name"] == "acme"


def test_missing_token_file_is_a_config_error(tmp_path: Path) -> None:
    config = _config(tmp_path)
    config.lab_token_file.unlink()

    with pytest.raises(ConfigError) as exc:
        read_lab(config, FakeLabClient(FakeLab()))

    assert "token" in "\n".join(exc.value.checks).lower()


def test_client_failure_is_a_config_error(tmp_path: Path) -> None:
    class Broken:
        def capabilities(self, **_kwargs: object) -> dict[str, object]:
            raise OSError("api down")

        def registrations(self, **_kwargs: object) -> dict[str, object]:
            return {}

        def installed_release(self, **_kwargs: object) -> str:
            return ""

    with pytest.raises(ConfigError) as exc:
        read_lab(_config(tmp_path), Broken())  # type: ignore[arg-type]

    assert "api down" in "\n".join(exc.value.checks)


def test_http_client_reads_capabilities(monkeypatch: pytest.MonkeyPatch) -> None:
    class Response:
        def __enter__(self) -> Response:
            return self

        def __exit__(self, *_args: object) -> None:
            return None

        def read(self) -> bytes:
            return json.dumps({"enabled_services": ["vmaas", "bmaas"]}).encode()

    monkeypatch.setattr("tests.conformance.lab.urlopen", lambda *_args, **_kwargs: Response())

    payload = HttpLabClient().capabilities(url="http://lab.example.test", token="t", ca_file=None)

    assert payload["enabled_services"] == ["vmaas", "bmaas"]


def test_kubectl_client_parses_registrations_and_release(monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> None:
    commands: list[tuple[str, ...]] = []

    def fake_run(*args: str, timeout: int = 300) -> str:
        commands.append(args)
        if "configmaps" in args:
            return json.dumps(
                {
                    "items": [
                        {"metadata": {"name": "networking"}, "data": {"name": "acme", "ansibleRole": "acme.fabric"}}
                    ]
                }
            )
        return json.dumps({"items": [{"metadata": {"labels": {"name": "osac", "version": "7"}}}]})

    monkeypatch.setattr("tests.conformance.lab.run", fake_run)

    client = KubectlLabClient()
    registrations = client.registrations(kubeconfig=tmp_path / "hub")
    release = client.installed_release(kubeconfig=tmp_path / "hub")

    assert registrations == {"networking": {"name": "acme", "ansibleRole": "acme.fabric"}}
    assert release == "osac-7"
    assert commands


def test_read_lab_filters_registrations_by_area_source(tmp_path: Path) -> None:
    area = AreaDeclaration(
        name="networking",
        component_tests=(),
        profiles={},
        identity_fields=(),
        configuration_errors=(),
        support_condition="",
        registration_source="networking",
        titles={},
        selectors={},
        checks=(),
        timeout_defaults={},
        component_job_timeout="60m",
        settings_source="",
        evidence=(),
        reference_components=(),
    )
    fake = FakeLab(registrations={"networking": {"name": "acme"}, "unrelated": {"name": "other"}})

    facts = read_lab(_config(tmp_path), FakeLabClient(fake), {"networking": area})

    assert [component.area for component in facts.components] == ["networking"]
