from pathlib import Path

import yaml
from ansible.parsing.dataloader import DataLoader
from ansible.template import Templar


CONFIGURATION = (
    Path(__file__).parents[2] / "group_vars" / "all" / "configuration.yaml"
)
AAP_ROOT = Path(__file__).parents[2]


def iter_uri_tasks(node):
    if isinstance(node, dict):
        for name, value in node.items():
            if name in {"ansible.builtin.uri", "uri"} and isinstance(value, dict):
                yield value
            yield from iter_uri_tasks(value)
    elif isinstance(node, list):
        for value in node:
            yield from iter_uri_tasks(value)


def resolve_netris_validate_certs():
    configuration = yaml.safe_load(CONFIGURATION.read_text())
    templar = Templar(loader=DataLoader(), variables={})
    return templar.template(configuration["netris_validate_certs"])


def test_netris_certificate_validation_defaults_to_enabled(monkeypatch):
    monkeypatch.delenv("NETRIS_VALIDATE_CERTS", raising=False)

    assert resolve_netris_validate_certs() is True


def test_netris_certificate_validation_can_be_disabled(monkeypatch):
    monkeypatch.setenv("NETRIS_VALIDATE_CERTS", "false")

    assert resolve_netris_validate_certs() is False


def test_all_netris_http_calls_use_configurable_validation_with_secure_default():
    roots = (
        AAP_ROOT / "collections/ansible_collections/netris/controller/roles",
        AAP_ROOT / "collections/ansible_collections/netris/steps/roles",
        AAP_ROOT / "collections/ansible_collections/osac/templates/roles/netris",
    )
    uri_tasks = []

    for root in roots:
        for path in sorted((*root.rglob("*.yaml"), *root.rglob("*.yml"))):
            if "tests" in path.parts:
                continue
            document = yaml.safe_load(path.read_text())
            uri_tasks.extend((path, task) for task in iter_uri_tasks(document))

    assert uri_tasks, "expected to find Netris API tasks using the uri module"

    for path, task in uri_tasks:
        assert "validate_certs" in task, path
        assert "netris_validate_certs" in task["validate_certs"], path
        assert "default(true)" in task["validate_certs"], path
