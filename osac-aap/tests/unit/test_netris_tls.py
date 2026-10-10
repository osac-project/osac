from pathlib import Path

import yaml


NETRIS_COLLECTION = Path(__file__).parents[2] / "collections" / "ansible_collections" / "netris"
AUTH_TASKS = NETRIS_COLLECTION / "controller" / "roles" / "auth" / "tasks" / "main.yaml"


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
        if 'ca_path: "{{ netris_ca_path | default(omit, true) }}"' not in task
    ]

    assert not missing_ca_path, f"Netris URI tasks missing ca_path: {missing_ca_path}"


def test_netris_ca_path_is_loaded_from_the_job_environment():
    configuration = (Path(__file__).parents[2] / "group_vars" / "all" / "configuration.yaml").read_text()

    assert "netris_ca_path" in configuration
    assert "NETRIS_CA_PATH" in configuration


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
    assert uri["ca_path"] == "{{ netris_ca_path | default(omit, true) }}"
