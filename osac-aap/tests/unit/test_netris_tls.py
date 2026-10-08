from pathlib import Path


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
    configuration = (
        Path(__file__).parents[2]
        / "group_vars"
        / "all"
        / "configuration.yaml"
    ).read_text()

    assert "netris_ca_path" in configuration
    assert "NETRIS_CA_PATH" in configuration
