import json
from pathlib import Path

import pytest
import yaml
from ansible.parsing.dataloader import DataLoader
from ansible.plugins.loader import lookup_loader
from ansible.template import Templar
from ansible_collections.osac.service.plugins.test.fulfillment_api_url import fulfillment_api_url


CONFIG_AS_CODE = (
    Path(__file__).resolve().parents[2]
    / "collections/ansible_collections/osac/config_as_code"
)
ISSUER_URL = "https://keycloak.example/realms/osac"
API_URL = "https://fulfillment.example:8001"
CREDENTIAL_NAME = "test-prefix-fulfillment-secret-reader"
CLIENT_SECRET_FIXTURE = "fixture-client-secret"


@pytest.fixture
def controller_config(monkeypatch):
    monkeypatch.setitem(lookup_loader.aliases, "ansible.builtin.env", "env")
    monkeypatch.setenv("AAP_PREFIX", "test-prefix")
    monkeypatch.setenv("AAP_ORGANIZATION_NAME", "test-org")
    monkeypatch.delenv("OSAC_AAP_SECRET_READER_ISSUER_URL", raising=False)
    monkeypatch.delenv("OSAC_AAP_SECRET_READER_API_URL", raising=False)
    monkeypatch.delenv("OSAC_AAP_SECRET_READER_CLIENT_SECRET", raising=False)

    config = yaml.safe_load((CONFIG_AS_CODE / "playbooks/vars/config.yml").read_text())
    config.update(yaml.safe_load((CONFIG_AS_CODE / "roles/aap/vars/controller.yml").read_text()))

    return Templar(loader=DataLoader(), variables=config)


def test_registers_fulfillment_secret_reader_credential(controller_config, monkeypatch):
    monkeypatch.setenv("OSAC_AAP_SECRET_READER_ISSUER_URL", ISSUER_URL)
    monkeypatch.setenv("OSAC_AAP_SECRET_READER_API_URL", API_URL)
    monkeypatch.setenv("OSAC_AAP_SECRET_READER_CLIENT_SECRET", CLIENT_SECRET_FIXTURE)

    [credential_type] = controller_config.template("{{ controller_credential_types }}")
    [credential] = controller_config.template("{{ controller_credentials }}")

    assert credential_type["name"] == CREDENTIAL_NAME
    assert credential_type["kind"] == "cloud"
    assert credential["name"] == CREDENTIAL_NAME
    assert credential["credential_type"] == CREDENTIAL_NAME
    assert credential["organization"] == "test-org"
    assert credential["update_secrets"] is True
    assert controller_config.template("{{ controller_configuration_credentials_secure_logging }}") is True
    assert credential["inputs"] == {
        "client_id": "osac-aap-secret-reader",
        "client_secret": CLIENT_SECRET_FIXTURE,
        "issuer_url": ISSUER_URL,
        "api_url": API_URL,
    }

    inputs = credential_type["inputs"]
    fields = {field["id"]: field for field in inputs["fields"]}
    assert set(inputs["required"]) == set(fields) == set(credential["inputs"])
    assert all(field["type"] == "string" for field in fields.values())
    assert fields["client_secret"]["secret"] is True

    # Controller's credential-types role restores the split opening braces.
    injectors = json.loads(credential_type["injectors"].replace("{  {", "{{"))
    assert injectors == {
        "file": {"template": "{{ client_secret }}"},
        "env": {
            "OSAC_FULFILLMENT_CLIENT_SECRET_FILE": "{{ tower.filename }}",
            "OSAC_FULFILLMENT_CLIENT_ID": "{{ client_id }}",
            "OSAC_FULFILLMENT_ISSUER_URL": "{{ issuer_url }}",
            "OSAC_FULFILLMENT_INTERNAL_API_URL": "{{ api_url }}",
            "OSAC_FULFILLMENT_CA_FILE": "/etc/ca-bundle/bundle.pem",
        },
    }


def test_omits_reader_credentials_without_a_fulfillment_issuer(controller_config):
    assert controller_config.template("{{ controller_credential_types }}") == []
    assert controller_config.template("{{ controller_credentials }}") == []


@pytest.mark.parametrize(
    ("url", "accepted"),
    [
        (API_URL, True),
        ("https://[2001:db8::1]:8001/", True),
        ("https://fulfillment.example/route", False),
        ("https://fulfillment.example?query=1", False),
        ("https://fulfillment.example#", False),
        ("https://user@fulfillment.example", False),
        ("https://fulfillment.example:invalid", False),
        ("https://fulfillment.example:65536", False),
        ("https://fulfillment example", False),
        ("http://fulfillment.example", False),
        ("", False),
    ],
)
def test_bootstrap_requires_fulfillment_api_listener_root(controller_config, monkeypatch, url, accepted):
    controller_config.environment.tests["osac.service.fulfillment_api_url"] = fulfillment_api_url
    monkeypatch.setenv("OSAC_AAP_SECRET_READER_API_URL", url)
    tasks = yaml.safe_load((CONFIG_AS_CODE / "roles/aap/tasks/main.yml").read_text())
    [validation] = [task for task in tasks if task["name"] == "Validate Fulfillment Secret reader configuration"]
    conditions = validation["block"][1]["ansible.builtin.assert"]["that"]
    [condition] = [condition for condition in conditions if "aap_fulfillment_reader_api_url" in condition]
    assert controller_config.template("{{ " + condition + " }}") is accepted
