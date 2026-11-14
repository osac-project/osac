"""Contract tests for reading target facts."""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from tests.conformance.config import Config
from tests.conformance.domain import DomainDeclaration
from tests.conformance.errors import ConfigError
from tests.conformance.target import HttpTargetClient, KubectlTargetClient, read_target
from tests.conformance.testing.fake_target import FakeTarget, FakeTargetClient

_FAKE_TOKEN = "not-a-real-credential"


def _config(tmp_path: Path, *, token: str = _FAKE_TOKEN) -> Config:
    token_file = tmp_path / "token"
    token_file.write_text(token)
    ca_file = tmp_path / "ca.pem"
    ca_file.write_text("ca")
    kubeconfig = tmp_path / "hub.kubeconfig"
    kubeconfig.write_text("hub")
    return Config(
        osac_release="1.4.0",
        path=tmp_path / "conformance.yaml",
        profiles=("generic",),
        target_namespace="osac-devel",
        target_kubeconfig=kubeconfig,
        target_api_url="http://127.0.0.1:1",
        target_token_file=token_file,
        target_ca_file=ca_file,
        target_aap=None,
        timeouts={},
        domains={},
        raw={},
    )


def test_read_target_reports_enabled_services_components_and_release(tmp_path: Path) -> None:
    fake = FakeTarget(
        enabled_services=("vmaas",),
        registrations={"networking": {"name": "acme", "ansibleRole": "acme.fabric"}},
        installed_release="1.4.0",
    )

    facts = read_target(_config(tmp_path), FakeTargetClient(fake))

    assert facts.enabled_services == ("vmaas",)
    assert facts.installed_release == "1.4.0"
    assert facts.components[0].domain == "networking"
    assert facts.components[0].fields["name"] == "acme"


def test_missing_token_file_is_a_config_error(tmp_path: Path) -> None:
    config = _config(tmp_path)
    config.target_token_file.unlink()

    with pytest.raises(ConfigError) as exc:
        read_target(config, FakeTargetClient(FakeTarget()))

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
        read_target(_config(tmp_path), Broken())  # type: ignore[arg-type]

    assert "api down" in "\n".join(exc.value.checks)


def test_http_client_reads_capabilities(monkeypatch: pytest.MonkeyPatch) -> None:
    class Response:
        def __enter__(self) -> Response:
            return self

        def __exit__(self, *_args: object) -> None:
            return None

        def read(self) -> bytes:
            return json.dumps({"enabled_services": ["vmaas", "bmaas"]}).encode()

    monkeypatch.setattr("tests.conformance.target.urlopen", lambda *_args, **_kwargs: Response())

    payload = HttpTargetClient().capabilities(url="http://target.example.test", token=_FAKE_TOKEN, ca_file=None)

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

    monkeypatch.setattr("tests.conformance.target.run", fake_run)

    client = KubectlTargetClient()
    registrations = client.registrations(kubeconfig=tmp_path / "hub", namespace="osac-devel")
    release = client.installed_release(kubeconfig=tmp_path / "hub", namespace="osac-devel")

    assert registrations == {"networking": {"name": "acme", "ansibleRole": "acme.fabric"}}
    assert release == "osac-7"
    assert commands


def test_kubectl_client_uses_the_configured_namespace(monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> None:
    commands: list[tuple[str, ...]] = []

    def fake_run(*args: str, timeout: int = 300) -> str:
        commands.append(args)
        if "configmaps" in args:
            return json.dumps({"items": []})
        return json.dumps({"items": [{"metadata": {"labels": {"name": "osac", "version": "7"}}}]})

    monkeypatch.setattr("tests.conformance.target.run", fake_run)

    client = KubectlTargetClient()
    client.registrations(kubeconfig=tmp_path / "hub", namespace="osac-devel")
    client.installed_release(kubeconfig=tmp_path / "hub", namespace="osac-devel")

    assert commands
    for command in commands:
        assert command[command.index("-n") + 1] == "osac-devel"


def test_read_target_filters_registrations_by_domain_source(tmp_path: Path) -> None:
    domain = DomainDeclaration(
        name="networking",
        component_tests=(),
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
    fake = FakeTarget(registrations={"networking": {"name": "acme"}, "unrelated": {"name": "other"}})

    facts = read_target(_config(tmp_path), FakeTargetClient(fake), {"networking": domain})

    assert [component.domain for component in facts.components] == ["networking"]
