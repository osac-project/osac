from __future__ import annotations

import network_task_file as plugin


def _role(base, ns, coll, role, files):
    tasks = base / "ansible_collections" / ns / coll / "roles" / role / "tasks"
    tasks.mkdir(parents=True)
    for name in files:
        (tasks / name).write_text("---\n")
    return tasks


def test_finds_yaml_and_yml(tmp_path):
    tasks = _role(tmp_path, "acme", "network", "fabric", ["create_subnet.yaml", "delete_subnet.yml"])
    assert plugin.find_task_file("acme.network.fabric", "create_subnet", [str(tmp_path)]) == str(
        tasks / "create_subnet.yaml"
    )
    assert plugin.find_task_file("acme.network.fabric", "delete_subnet", [str(tmp_path)]) == str(
        tasks / "delete_subnet.yml"
    )


def test_missing_role_or_task(tmp_path):
    _role(tmp_path, "acme", "network", "fabric", ["create_subnet.yaml"])
    assert plugin.find_task_file("acme.network.other", "create_subnet", [str(tmp_path)]) == ""
    assert plugin.find_task_file("acme.network.fabric", "delete_subnet", [str(tmp_path)]) == ""


def test_first_collection_with_the_role_wins(tmp_path):
    first = tmp_path / "first"
    second = tmp_path / "second"
    _role(first, "acme", "network", "fabric", ["create_subnet.yaml"])
    _role(second, "acme", "network", "fabric", ["create_subnet.yaml", "delete_subnet.yaml"])
    paths = [str(first), str(second)]
    assert plugin.find_task_file("acme.network.fabric", "delete_subnet", paths) == ""


def test_rejects_malformed_names(tmp_path):
    _role(tmp_path, "acme", "network", "fabric", ["create_subnet.yaml"])
    for role, task in [
        ("acme.network", "create_subnet"),
        ("Acme.network.fabric", "create_subnet"),
        ("acme.network.fabric", "../create_subnet"),
        ("acme.network.fabric", ""),
    ]:
        assert plugin.find_task_file(role, task, [str(tmp_path)]) == ""
