from pathlib import Path

import pytest
import yaml


NETRIS_COLLECTION = Path(__file__).parents[2] / "collections" / "ansible_collections" / "netris"


def _uri_tasks():
    for task_file in NETRIS_COLLECTION.rglob("*.yaml"):
        for document in yaml.safe_load_all(task_file.read_text()):
            yield from _find_uri_tasks(document, task_file)


def _find_uri_tasks(value, task_file):
    if isinstance(value, dict):
        if "netris.controller.protected_uri" in value:
            yield task_file, value.get("name", "<unnamed>"), value["netris.controller.protected_uri"]
        for child in value.values():
            yield from _find_uri_tasks(child, task_file)
    elif isinstance(value, list):
        for child in value:
            yield from _find_uri_tasks(child, task_file)


def _direct_uri_tasks():
    for task_file in NETRIS_COLLECTION.rglob("*.yaml"):
        for document in yaml.safe_load_all(task_file.read_text()):
            yield from _find_direct_uri_tasks(document, task_file)


def _find_direct_uri_tasks(value, task_file):
    if isinstance(value, dict):
        for module_name in ("ansible.builtin.uri", "uri"):
            if module_name in value:
                yield task_file, value.get("name", "<unnamed>"), module_name
        for child in value.values():
            yield from _find_direct_uri_tasks(child, task_file)
    elif isinstance(value, list):
        for child in value:
            yield from _find_direct_uri_tasks(child, task_file)


def test_netris_collection_does_not_bypass_protected_uri():
    direct_uri_tasks = [
        f"{task_file}:{task_name} ({module_name})"
        for task_file, task_name, module_name in _direct_uri_tasks()
    ]

    assert not direct_uri_tasks, (
        "Netris API calls must use netris.controller.protected_uri: "
        f"{direct_uri_tasks}"
    )


def test_every_netris_uri_call_accepts_the_shared_ca_path():
    uri_tasks = list(_uri_tasks())

    assert uri_tasks, "expected Netris collection URI tasks"
    missing_ca_path = [
        f"{task_file}:{task_name}"
        for task_file, task_name, task in uri_tasks
        if not isinstance(task, dict)
        or task.get("ca_path") != "{{ netris_ca_path | default(omit, true) }}"
    ]

    assert not missing_ca_path, f"Netris URI tasks missing ca_path: {missing_ca_path}"


def test_netris_ca_path_is_loaded_from_the_job_environment():
    configuration = yaml.safe_load((
        Path(__file__).parents[2]
        / "group_vars"
        / "all"
        / "configuration.yaml"
    ).read_text())

    assert configuration["netris_ca_path"] == (
        "{{ lookup('env', 'NETRIS_CA_PATH', default='') }}"
    )

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


def _render_auth_tls_settings(monkeypatch, tmp_path, existing=None):
    monkeypatch.setenv("ANSIBLE_LOCAL_TEMP", str(tmp_path))
    from ansible.parsing.dataloader import DataLoader
    from ansible.plugins.loader import lookup_loader
    from ansible.template import Templar

    monkeypatch.setitem(lookup_loader.aliases, "ansible.builtin.env", "env")
    tasks = yaml.safe_load(AUTH_TASKS.read_text())
    facts = next(
        task["ansible.builtin.set_fact"]
        for task in tasks
        if task.get("name") == "Resolve Netris TLS settings from the worker environment"
    )
    templar = Templar(loader=DataLoader(), variables=existing or {})

    return {
        name: templar.template(expression)
        for name, expression in facts.items()
    }


@pytest.mark.parametrize(
    ("environment", "existing", "expected"),
    [
        pytest.param({}, {}, (True, ""), id="unset"),
        pytest.param(
            {
                "NETRIS_VALIDATE_CERTS": "false",
                "NETRIS_CA_PATH": "/env/ca.pem",
            },
            {},
            (False, "/env/ca.pem"),
            id="configured",
        ),
        pytest.param(
            {
                "NETRIS_VALIDATE_CERTS": "",
                "NETRIS_CA_PATH": "",
            },
            {},
            (True, ""),
            id="empty",
        ),
        pytest.param(
            {
                "NETRIS_VALIDATE_CERTS": "not-a-boolean",
                "NETRIS_CA_PATH": "",
            },
            {},
            (True, ""),
            id="malformed",
        ),
        pytest.param(
            {
                "NETRIS_VALIDATE_CERTS": "true",
                "NETRIS_CA_PATH": "/env/ca.pem",
            },
            {
                "netris_validate_certs": False,
                "netris_ca_path": "/existing/ca.pem",
            },
            (True, "/env/ca.pem"),
            id="worker-environment-is-authoritative",
        ),
    ],
)
def test_auth_role_renders_tls_settings(
    monkeypatch, tmp_path, environment, existing, expected
):
    for name in ("NETRIS_VALIDATE_CERTS", "NETRIS_CA_PATH"):
        monkeypatch.delenv(name, raising=False)
    for name, value in environment.items():
        monkeypatch.setenv(name, value)

    facts = _render_auth_tls_settings(monkeypatch, tmp_path, existing)

    assert facts["netris_validate_certs"] is expected[0]
    assert facts["netris_ca_path"] == expected[1]


def test_auth_request_consumes_resolved_tls_settings():
    tasks = yaml.safe_load(AUTH_TASKS.read_text())
    auth_task = next(task for task in tasks if task.get("name") == "Login to Netris controller")
    uri = auth_task["netris.controller.protected_uri"]

    assert uri["validate_certs"] == "{{ netris_validate_certs | default(true) }}"
    assert uri["ca_path"] == "{{ netris_ca_path | default(omit, true) }}"
