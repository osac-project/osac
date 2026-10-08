from pathlib import Path

import yaml


AUTH_TASKS = (
    Path(__file__).parents[2]
    / "collections"
    / "ansible_collections"
    / "netris"
    / "controller"
    / "roles"
    / "auth"
    / "tasks"
    / "main.yaml"
)


def test_auth_role_resolves_tls_settings_from_environment():
    tasks = yaml.safe_load(AUTH_TASKS.read_text())
    resolve_task = tasks[0]

    assert resolve_task["name"] == "Resolve Netris TLS settings from the worker environment"
    facts = resolve_task["ansible.builtin.set_fact"]
    assert "NETRIS_VALIDATE_CERTS" in facts["netris_validate_certs"]
    assert "NETRIS_CA_PATH" in facts["netris_ca_path"]
    assert facts["netris_ca_path"].endswith("true) }}")


def test_auth_request_consumes_resolved_tls_settings():
    tasks = yaml.safe_load(AUTH_TASKS.read_text())
    auth_task = tasks[1]
    uri = auth_task["ansible.builtin.uri"]

    assert uri["validate_certs"] == "{{ netris_validate_certs | default(true) }}"
