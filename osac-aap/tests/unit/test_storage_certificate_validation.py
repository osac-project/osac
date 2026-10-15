from pathlib import Path

import yaml
from ansible.parsing.dataloader import DataLoader
from ansible.template import Templar


AAP_ROOT = Path(__file__).parents[2]
ROLE_ROOT = (
    AAP_ROOT
    / "collections/ansible_collections/osac/templates/roles/vast_storage"
)


def resolve_certificate_validation():
    defaults = yaml.safe_load((ROLE_ROOT / "defaults/main.yaml").read_text())
    templar = Templar(loader=DataLoader(), variables={})
    return templar.template(defaults["enable_certificate_validation"])


def resolve_storage_role_validation():
    defaults = yaml.safe_load((ROLE_ROOT / "defaults/main.yaml").read_text())
    templar = Templar(
        loader=DataLoader(),
        variables={"enable_certificate_validation": resolve_certificate_validation()},
    )
    return templar.template(defaults["vast_storage_validate_certs"])


def iter_validation_values(node):
    if isinstance(node, dict):
        for key, value in node.items():
            if key == "validate_certs":
                yield value
            yield from iter_validation_values(value)
    elif isinstance(node, list):
        for value in node:
            yield from iter_validation_values(value)


def test_certificate_validation_defaults_to_enabled(monkeypatch):
    monkeypatch.delenv("ENABLE_CERTIFICATE_VALIDATION", raising=False)

    assert resolve_certificate_validation() is True
    assert resolve_storage_role_validation() is True


def test_certificate_validation_uses_runtime_environment(monkeypatch):
    monkeypatch.setenv("ENABLE_CERTIFICATE_VALIDATION", "false")

    assert resolve_certificate_validation() is False
    assert resolve_storage_role_validation() is False


def test_unrecognized_certificate_validation_value_fails_closed(monkeypatch):
    monkeypatch.setenv("ENABLE_CERTIFICATE_VALIDATION", "unexpected")

    assert resolve_certificate_validation() is True
    assert resolve_storage_role_validation() is True


def test_storage_role_alias_uses_the_global_setting():
    defaults = yaml.safe_load((ROLE_ROOT / "defaults/main.yaml").read_text())

    assert defaults["vast_storage_validate_certs"] == "{{ enable_certificate_validation }}"


def test_storage_http_clients_use_the_shared_validation_setting():
    tasks_dir = ROLE_ROOT / "tasks"
    paths = sorted((*tasks_dir.glob("*.yaml"), *tasks_dir.glob("*.yml")))
    validation_values = []
    for path in paths:
        validation_values.extend(
            (path, value)
            for value in iter_validation_values(yaml.safe_load(path.read_text()))
        )

    assert validation_values, "expected storage task TLS options"
    for path, value in validation_values:
        assert value in {
            "{{ vast_storage_validate_certs }}",
            "{{ _vast_vms_conn.validate_certs }}",
        }, path
