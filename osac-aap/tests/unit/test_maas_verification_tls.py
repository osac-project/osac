from pathlib import Path

import yaml


AAP_ROOT = Path(__file__).parents[2]
MAAS_ROLE = AAP_ROOT / "collections/ansible_collections/osac/templates/roles/ocp_4_20_ai_maas"


def _walk(value):
    if isinstance(value, dict):
        yield value
        for child in value.values():
            yield from _walk(child)
    elif isinstance(value, list):
        for child in value:
            yield from _walk(child)


def _tasks(path: Path, module: str):
    document = yaml.safe_load(path.read_text())
    return [node[module] for node in _walk(document) if module in node]


def test_maas_verification_http_requests_validate_tls_and_use_ingress_ca():
    uri_tasks = []
    for filename in ("verify_tenant.yaml", "verify_isolation.yaml"):
        uri_tasks.extend(_tasks(MAAS_ROLE / "tasks" / filename, "ansible.builtin.uri"))

    assert uri_tasks
    for task in uri_tasks:
        assert task["validate_certs"] is True
        assert "_verify_ingress_ca_file.path" in task["ca_path"]
        assert "default(omit)" in task["ca_path"]


def test_maas_verification_commands_keep_dynamic_values_in_argv():
    for filename in ("verify_tenant.yaml", "verify_isolation.yaml"):
        tasks = list(_walk(yaml.safe_load((MAAS_ROLE / "tasks" / filename).read_text())))
        shell_tasks = [task["ansible.builtin.shell"] for task in tasks if "ansible.builtin.shell" in task]
        assert all("{{" not in command for command in shell_tasks)
        commands = [task["ansible.builtin.command"] for task in tasks if "ansible.builtin.command" in task]
        assert commands
        for command in commands:
            assert "argv" in command
            assert "cmd" not in command

    for filename in ("verify.yaml", "configure_maas.yaml"):
        tasks = list(_walk(yaml.safe_load((MAAS_ROLE / "tasks" / filename).read_text())))
        for task in tasks:
            if "ansible.builtin.shell" in task:
                assert "{{" not in task["ansible.builtin.shell"]


def test_maas_verification_loads_and_cleans_optional_ingress_ca_file():
    document = yaml.safe_load((MAAS_ROLE / "tasks/verify.yaml").read_text())
    tasks = list(_walk(document))

    assert any(
        task.get("kubernetes.core.k8s_info", {}).get("kind") == "ConfigMap"
        and task["kubernetes.core.k8s_info"].get("name") == "default-ingress-cert"
        and task["kubernetes.core.k8s_info"].get("namespace") == "openshift-config-managed"
        for task in tasks
    )
    assert any(
        task.get("ansible.builtin.tempfile", {}).get("suffix") == ".pem"
        and task.get("register") == "_verify_ingress_ca_file"
        and task.get("when") == "_verify_ingress_ca_bundle | length > 0"
        for task in tasks
    )
    assert any(
        task.get("ansible.builtin.copy", {}).get("content") == "{{ _verify_ingress_ca_bundle }}"
        and task["ansible.builtin.copy"].get("dest") == "{{ _verify_ingress_ca_file.path }}"
        and task["ansible.builtin.copy"].get("mode") == "0600"
        for task in tasks
    )
    assert any(
        any(
            task.get("ansible.builtin.file", {}).get("path") == "{{ _verify_ingress_ca_file.path }}"
            and task["ansible.builtin.file"].get("state") == "absent"
            for task in block
        )
        for node in tasks
        for block in [node.get("always", [])]
        if isinstance(block, list)
    )
