from pathlib import Path

import pytest
import yaml


NETRIS_COLLECTION = Path(__file__).parents[2] / "collections" / "ansible_collections" / "netris"


def _uri_task_blocks():
    for task_file in NETRIS_COLLECTION.rglob("*.yaml"):
        contents = task_file.read_text()
        for block in contents.split("ansible.builtin.uri:")[1:]:
            yield task_file, block.split("\n- name:", 1)[0]


def test_every_netris_uri_call_accepts_the_shared_ca_path():
    uri_tasks = list(_uri_task_blocks())

    assert uri_tasks, "expected Netris collection URI tasks"
    missing_ca_path = [
        str(task_file)
        for task_file, task in uri_tasks
        if "ca_path: \"{{ netris_ca_path | default(omit, true) }}\"" not in task
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
    facts = tasks[0]["ansible.builtin.set_fact"]
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
            (False, ""),
            id="empty",
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
            (False, "/existing/ca.pem"),
            id="existing-values-take-precedence",
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
    auth_task = tasks[1]
    uri = auth_task["ansible.builtin.uri"]

    assert uri["validate_certs"] == "{{ netris_validate_certs | default(true) }}"
