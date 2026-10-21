"""Tests for storage TLS validation defaults and task wiring."""

from pathlib import Path

import yaml
from ansible.parsing.dataloader import DataLoader
from ansible.template import Templar


AAP_ROOT = Path(__file__).parents[2]
ROLE_ROOT = (
    AAP_ROOT / "collections/ansible_collections/osac/templates/roles/vast_storage"
)


def resolve_storage_role_validation():
    """Render the storage role's VAST TLS policy from the environment."""
    defaults = yaml.safe_load((ROLE_ROOT / "defaults/main.yaml").read_text())
    templar = Templar(loader=DataLoader(), variables={})
    return templar.template(defaults["vast_storage_validate_certs"])


def iter_validation_values(node):
    """Yield every nested ``validate_certs`` value in an Ansible task tree."""
    if isinstance(node, dict):
        for key, value in node.items():
            if key == "validate_certs":
                yield value
            yield from iter_validation_values(value)
    elif isinstance(node, list):
        for value in node:
            yield from iter_validation_values(value)


def test_certificate_validation_defaults_to_enabled(monkeypatch):
    """Keep TLS verification enabled when the VAST setting is absent."""
    monkeypatch.delenv("VAST_VALIDATE_CERTS", raising=False)

    assert resolve_storage_role_validation() is True


def test_certificate_validation_uses_runtime_environment(monkeypatch):
    """Allow VAST's worker environment setting to disable TLS verification."""
    monkeypatch.setenv("VAST_VALIDATE_CERTS", "false")

    assert resolve_storage_role_validation() is False


def test_unrecognized_certificate_validation_value_keeps_secure_default(monkeypatch):
    """Treat unrecognized setting values as enabled to preserve the secure default."""
    monkeypatch.setenv("VAST_VALIDATE_CERTS", "unexpected")

    assert resolve_storage_role_validation() is True


def test_generic_setting_is_not_consumed(monkeypatch):
    """Keep the storage worker contract provider-scoped to VAST."""
    monkeypatch.delenv("VAST_VALIDATE_CERTS", raising=False)
    monkeypatch.setenv("ENABLE_CERTIFICATE_VALIDATION", "false")

    assert resolve_storage_role_validation() is True


def test_storage_http_clients_use_the_shared_validation_setting():
    """Ensure every storage task wires HTTP certificate checks to the VAST policy."""
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
