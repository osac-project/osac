"""Contract tests for the Netris attachment apply and delete entry points."""
from __future__ import annotations

import json
import os
import subprocess
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from threading import Thread

import yaml

ROOT = Path(__file__).resolve().parents[2]
TASKS = ROOT / "collections/ansible_collections/osac/templates/roles/netris/tasks"


def _tasks(name: str) -> list[dict]:
    return yaml.safe_load((TASKS / f"{name}.yaml").read_text(encoding="utf-8"))


def _walk(tasks):
    for task in tasks:
        yield task
        for key in ("block", "rescue", "always"):
            yield from _walk(task.get(key) or [])


def _move_vars(tasks):
    for task in _walk(tasks):
        if task.get("ansible.builtin.include_tasks") == "_move_port.yaml":
            return (task.get("vars") or {}).get("network_attachment")
    return None


def test_apply_moves_a_physical_port_from_provisioning_to_the_subnet() -> None:
    move = _move_vars(_tasks("apply_network_attachment"))

    assert move["from_vnet_name"] == "{{ provisioning_vnet }}"
    assert move["to_vnet_name"] == "{{ subnet.metadata.name }}"
    assert move["host_name"] == "{{ _osac_target.host_name }}"
    assert move["logical_interface_name"] == "{{ _osac_interface.name }}"


def test_delete_returns_a_physical_port_to_the_provisioning_network() -> None:
    move = _move_vars(_tasks("delete_network_attachment"))

    assert move["from_vnet_name"] == "{{ subnet.metadata.name }}"
    assert move["to_vnet_name"] == "{{ provisioning_vnet }}"


def test_overlay_attachments_change_nothing_on_the_fabric() -> None:
    for name in ("apply_network_attachment", "delete_network_attachment"):
        tasks = _tasks(name)
        overlay = [t for t in _walk(tasks) if "overlay" in str(t.get("when"))]
        assert overlay, f"{name} has no overlay no-op"
        physical = [t for t in tasks if "physical" in str(t.get("when")) and t.get("block")]
        assert physical, f"{name} does not gate the port move on a physical interface"


def test_legacy_move_entry_point_keeps_using_the_shared_port_helper() -> None:
    tasks = _tasks("move_network_attachment")

    assert any(
        task.get("ansible.builtin.include_tasks") == "_move_port.yaml"
        for task in _walk(tasks)
    )


LEGACY_ANNOTATION_CHECK = "Validate ExternalIPAttachment ownership annotations"


def test_detach_keeps_the_legacy_annotation_check_out_of_the_contract_path() -> None:
    tasks = _tasks("detach_external_ip")

    legacy = next(
        task
        for task in tasks
        if "not _osac_contract" in str(task.get("when")) and task.get("block")
    )
    assert LEGACY_ANNOTATION_CHECK in [
        task.get("name") for task in _walk(legacy["block"])
    ]

    top_level = [task.get("name") for task in tasks if "block" not in task]
    assert LEGACY_ANNOTATION_CHECK not in top_level


def test_detach_runs_on_the_contract_path_without_annotations(tmp_path: Path) -> None:
    calls: list[tuple[str, str]] = []

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass

        def respond(self, body, status=200):
            encoded = json.dumps(body).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(encoded)))
            self.end_headers()
            self.wfile.write(encoded)

        def do_GET(self):
            calls.append(("GET", self.path))
            assert self.path == "/api/v2/nat"
            return self.respond(
                {
                    "data": [
                        {
                            "id": 42,
                            "name": "att-1-dnat",
                            "action": "DNAT",
                            "site": {"id": 1},
                            "vpc": {"id": 9},
                            "destinationAddress": "198.51.100.10/32",
                            "dnatToIP": "10.0.1.17/32",
                        }
                    ]
                }
            )

        def do_DELETE(self):
            calls.append(("DELETE", self.path))
            assert self.path == "/api/v2/nat/42"
            self.respond({"data": {"id": 42}})

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    fixture_url = f"http://127.0.0.1:{server.server_port}"
    thread = Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        playbook = tmp_path / "detach_contract.yaml"
        playbook.write_text(
            yaml.safe_dump(
                [
                    {
                        "hosts": "localhost",
                        "gather_facts": False,
                        "vars": {
                            "netris_controller_url": fixture_url,
                            "netris_session_cookie": "fixture",
                            "netris_validate_certs": False,
                            "netris_site_id": 1,
                            "netris_tenant_id": 1,
                            "netris_mgmt_vpc_id": 9,
                            "osac_job_vars": {
                                "manager": {
                                    "name": "netris",
                                    "type": "fabric",
                                    "ansible_role": "osac.templates.netris",
                                },
                                "task": "detach_external_ip",
                                "tenant": "tenant-1",
                                "resource": {
                                    "apiVersion": "osac.openshift.io/v1alpha1",
                                    "kind": "ExternalIPAttachment",
                                    "id": "att-1",
                                    "metadata": {
                                        "name": "att",
                                        "namespace": "ns",
                                        "uid": "uid-1",
                                        "generation": 1,
                                    },
                                    "spec": {},
                                },
                                "external_ip": {
                                    "id": "eip-1",
                                    "address": "198.51.100.10",
                                },
                                "destination": {
                                    "address": "10.0.1.17",
                                    "routing_domain": {
                                        "type": "provider_management",
                                    },
                                },
                            },
                        },
                        "tasks": [
                            {
                                "name": "Detach on the contract path",
                                "ansible.builtin.include_tasks": str(
                                    TASKS / "detach_external_ip.yaml"
                                ),
                            }
                        ],
                    }
                ],
                sort_keys=False,
            )
        )
        result = subprocess.run(
            ["ansible-playbook", "-i", "localhost,", "-c", "local", str(playbook)],
            cwd=ROOT,
            env={
                **os.environ,
                "ANSIBLE_LOCAL_TEMP": str(tmp_path / "local"),
                "NETRIS_CONTROLLER_URL": fixture_url,
                "NETRIS_VALIDATE_CERTS": "false",
                "NETRIS_CA_PATH": "",
            },
            capture_output=True,
            text=True,
            timeout=60,
        )
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)

    output = result.stdout + result.stderr
    assert result.returncode == 0, output
    assert "required ownership annotations" not in output
    assert ("DELETE", "/api/v2/nat/42") in calls


def test_apply_scopes_the_port_move_to_the_tenant_vpc() -> None:
    move = _move_vars(_tasks("apply_network_attachment"))

    assert move["to_vpc_id"] == "{{ netris_vpc.id }}"
    assert move["to_tenant_id"] == "{{ netris_tenant_id }}"
    assert move["from_vpc_id"] == "{{ netris_mgmt_vpc_id | default(0) | int }}"


def test_delete_scopes_the_port_move_to_the_source_vpc() -> None:
    move = _move_vars(_tasks("delete_network_attachment"))

    assert move["from_vpc_id"] == "{{ netris_vpc.id }}"
    assert move["from_tenant_id"] == "{{ netris_tenant_id }}"
    assert move["to_vpc_id"] == "{{ netris_mgmt_vpc_id | default(0) | int }}"


def test_port_helpers_refuse_an_ambiguous_vnet_identity() -> None:
    for name in ("_attach_port_to_vnet", "_detach_port_from_vnet"):
        asserts = [
            task["ansible.builtin.assert"]
            for task in _walk(_tasks(name))
            if "ansible.builtin.assert" in task
        ]
        assert any(
            "_vnet_candidates | length <= 1" in str(item["that"]) for item in asserts
        ), f"{name} must refuse an ambiguous V-Net match"


def test_dhcp_query_reads_the_contract_attachment_inputs() -> None:
    extract = next(
        task
        for task in _walk(_tasks("query_dhcp_lease"))
        if task.get("name") == "Extract network attachment configuration"
    )
    facts = extract["ansible.builtin.set_fact"]

    assert "_osac_target.host_name" in facts["_dq_host_name"]
    assert "_osac_subnet.id" in facts["_dq_subnet_ref"]
    assert "_osac_interface.name" in facts["_dq_interface"]
