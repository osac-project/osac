"""Exercise the IPAM helper through real Ansible and a local HTTP fixture."""
import json
import os
from pathlib import Path
import socket
import subprocess
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from threading import Thread

import pytest
import yaml

ROOT = Path(__file__).resolve().parents[2]
TASKS = ROOT / "collections/ansible_collections/osac/templates/roles/netris/tasks"
IPAM_ROLE_TASKS = ROOT / "collections/ansible_collections/netris/controller/roles/ipam/tasks"
CURRENT_PREFIX = "192.0.2.0/26"
EXPECTED_PREFIX = "192.0.2.0/25"


@pytest.mark.parametrize(
    "scenario",
    [
        "new",
        "reuse",
        "replace",
        "lost-create-response",
        "lost-delete-response",
        "delete-race-create",
        "wrong-parent",
        "wrong-vpc",
        "missing-parent",
        "prefix-conflict",
        "hosts-present",
        "delete-rejected",
    ],
)
def test_reserved_prefix_reconciliation_is_idempotent_and_checks_identity(tmp_path: Path, scenario: str) -> None:
    parent = {
        "id": 5,
        "name": "test-subnet",
        "prefix": "192.0.2.0/24",
        "parentID": 0,
        "vpc": {"id": 3},
        "tenant": {"id": 1},
    }
    child = {
        "id": 6,
        "name": "test-subnet-fabric",
        "prefix": EXPECTED_PREFIX,
        "parentID": 5,
        "children": [],
        "vpc": {"id": 3, "name": "test-vpc"},
        "tenant": {"id": 1, "name": "Admin"},
    }
    pools = [parent]
    if scenario in [
        "reuse",
        "replace",
        "lost-delete-response",
        "delete-race-create",
        "wrong-parent",
        "hosts-present",
        "delete-rejected",
    ]:
        child["prefix"] = EXPECTED_PREFIX if scenario == "reuse" else CURRENT_PREFIX
        pools.append(child)
    if scenario == "wrong-parent":
        child["parentID"] = 99
    if scenario == "wrong-vpc":
        parent["vpc"] = {"id": 99}
    if scenario == "missing-parent":
        pools.clear()
    if scenario == "prefix-conflict":
        pools.append({**child, "id": 7, "name": "some-other-subnet", "prefix": EXPECTED_PREFIX})
    if scenario == "hosts-present":
        child["hasHosts"] = True

    calls = []
    drop_create_response = scenario == "lost-create-response"
    drop_delete_response = scenario == "lost-delete-response"

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

        def drop_response(self):
            self.close_connection = True
            try:
                self.connection.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
            self.connection.close()

        def do_GET(self):
            calls.append(("GET", self.path, None))
            if self.path == "/api/v2/ipam/subnets?filterByVpc=3":
                self.respond({"data": pools})
                return
            if self.path == "/api/v2/ipam/hosts/6":
                hosts = [{"id": 10, "address": "192.0.2.10"}] if scenario == "hosts-present" else []
                self.respond({"data": hosts})
                return
            raise AssertionError(self.path)

        def do_DELETE(self):
            nonlocal drop_delete_response
            calls.append(("DELETE", self.path, None))
            assert self.path == "/api/v2/ipam/subnet/6"
            pools[:] = [item for item in pools if item["id"] != 6]
            if scenario == "delete-race-create":
                pools.append({**child, "id": 7, "prefix": EXPECTED_PREFIX})
                self.respond({"message": "already deleted"}, status=404)
                return
            if scenario == "delete-rejected":
                pools.append(child)
                self.respond({"message": "subnet is in use"}, status=400)
                return
            if drop_delete_response:
                drop_delete_response = False
                self.drop_response()
                return
            self.respond({"data": {"id": 6}})

        def do_POST(self):
            nonlocal drop_create_response
            body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            calls.append(("POST", self.path, body))
            assert self.path == "/api/v2/ipam/subnet"
            pools.append({**body, "id": 6, "children": [], "vpc": {"id": 3}, "tenant": {"id": 1}})
            if drop_create_response:
                drop_create_response = False
                self.drop_response()
                return
            self.respond({"data": {"id": 6}})

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        def run_helper(attempt: int, repeats: int = 1) -> subprocess.CompletedProcess:
            playbook = tmp_path / f"fabric_ipam_{attempt}.yaml"
            helper_tasks = [
                {
                    "name": f"Ensure reserved prefix on attempt {attempt}.{repeat}",
                    "ansible.builtin.include_tasks": str(TASKS / "ensure_fabric_ipam.yaml"),
                }
                for repeat in range(repeats)
            ]
            playbook.write_text(
                yaml.safe_dump(
                    [
                        {
                            "hosts": "localhost",
                            "gather_facts": False,
                            "vars": {
                                "netris_controller_url": f"http://127.0.0.1:{server.server_port}",
                                "netris_session_cookie": "fixture",
                                "netris_site_id": 1,
                                "netris_tenant_id": 1,
                                "_parent_vpc": {"id": 3},
                                "ipam_subnet_id": 5,
                                "subnet_name": "test-subnet",
                                "subnet_ipv4_cidr": "192.0.2.0/24",
                                "_fabric_reserved_range": EXPECTED_PREFIX,
                            },
                            "tasks": helper_tasks,
                        }
                    ],
                    sort_keys=False,
                )
            )
            return subprocess.run(
                ["ansible-playbook", "-i", "localhost,", "-c", "local", str(playbook)],
                cwd=ROOT,
                env={**os.environ, "ANSIBLE_LOCAL_TEMP": str(tmp_path / f"local-{attempt}")},
                capture_output=True,
                text=True,
                timeout=45,
            )

        if scenario in ["lost-create-response", "lost-delete-response"]:
            first_attempt = run_helper(1)
            assert first_attempt.returncode != 0, first_attempt.stdout + first_attempt.stderr
            retry_attempt = run_helper(2)
            assert retry_attempt.returncode == 0, retry_attempt.stdout + retry_attempt.stderr
            result = retry_attempt
        else:
            result = run_helper(1, repeats=2)
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)

    posts = [call for call in calls if call[0] == "POST"]
    deletes = [call for call in calls if call[0] == "DELETE"]
    if scenario in ["new", "lost-create-response", "replace", "lost-delete-response"]:
        assert result.returncode == 0, result.stdout + result.stderr
        assert len(posts) == 1
        assert len(deletes) == (1 if scenario in ["replace", "lost-delete-response"] else 0)
        assert posts[0][2] == {
            "name": "test-subnet-fabric",
            "prefix": EXPECTED_PREFIX,
            "parentID": 5,
            "purpose": "common",
            "sites": [{"id": 1}],
            "tenant": {"id": 1},
            "vpc": {"id": 3},
        }
        assert any(item.get("name") == "test-subnet-fabric" and item.get("prefix") == EXPECTED_PREFIX for item in pools)
    elif scenario == "reuse":
        assert result.returncode == 0, result.stdout + result.stderr
        assert not posts
        assert not deletes
        assert "changed=0" in result.stdout
    elif scenario == "delete-race-create":
        assert result.returncode == 0, result.stdout + result.stderr
        assert not posts
        assert len(deletes) == 1
        assert any(item.get("id") == 7 and item.get("prefix") == EXPECTED_PREFIX for item in pools)
    else:
        assert result.returncode != 0
        assert not posts
        assert len(deletes) == (1 if scenario == "delete-rejected" else 0)
        expected_failure_messages = {
            "wrong-parent": "does not match this Subnet's name",
            "wrong-vpc": "Refusing to create a fabric prefix under an IPAM parent",
            "missing-parent": "Refusing to create a fabric prefix under an IPAM parent",
            "prefix-conflict": "does not match this Subnet's name",
            "hosts-present": "has hosts or child prefixes",
            "delete-rejected": "Netris refused to delete the old reserved fabric IPAM prefix",
        }
        assert expected_failure_messages[scenario] in result.stdout


def test_gateway_creation_waits_for_reserved_ipam_and_cleanup_removes_child_first() -> None:
    tasks = yaml.safe_load((TASKS / "create_subnet.yaml").read_text())
    names = [task["name"] for task in tasks]
    helper = tasks[names.index("Ensure the reserved EVPN fabric prefix exists in Netris IPAM")]
    assert helper["when"] == "is_cudn_evpn"
    assert names.index(helper["name"]) < names.index("Create V-Net in Netris controller")
    tasks = yaml.safe_load((TASKS / "delete_subnet.yaml").read_text())
    names = [task["name"] for task in tasks]
    assert names.index("Delete V-Net from Netris controller") < names.index("Delete the reserved EVPN fabric IPAM subnet")
    assert names.index("Delete the reserved EVPN fabric IPAM subnet") < names.index("Delete IPAM subnet from Netris controller")


def test_every_fabric_ipam_request_uses_the_configured_ca_path() -> None:
    tasks = yaml.safe_load((TASKS / "ensure_fabric_ipam.yaml").read_text())
    uri_tasks = [task["ansible.builtin.uri"] for task in tasks if "ansible.builtin.uri" in task]

    assert uri_tasks
    assert all(uri.get("ca_path") == "{{ netris_ca_path | default(omit, true) }}" for uri in uri_tasks)


def test_subnet_delete_resolves_ipam_parent_through_identity_scoped_role() -> None:
    tasks = yaml.safe_load((TASKS / "delete_subnet.yaml").read_text())
    lookup = next(task for task in tasks if task.get("name") == "Find the exact IPAM parent for this Subnet")
    assert lookup["ansible.builtin.include_role"] == {
        "name": "netris.controller.ipam",
        "tasks_from": "find_subnet_by_identity",
    }
    assert lookup["vars"] == {
        "ipam_name": "{{ subnet_name }}",
        "ipam_cidr": "{{ subnet_ipv4_cidr }}",
        "ipam_vpc_id": "{{ _parent_vpc.id }}",
        "ipam_tenant_id": "{{ netris_tenant_id }}",
    }

    child_delete = next(task for task in tasks if task.get("name") == "Delete the reserved EVPN fabric IPAM subnet")
    assert child_delete["vars"]["ipam_parent_id"] == "{{ _subnet_ipam_parent.id }}"
    assert "_subnet_ipam_parent is mapping" in child_delete["when"]

    orphan_lookup = next(
        task for task in tasks if task.get("name") == "Look for a reserved child when its parent IPAM subnet is missing"
    )
    assert orphan_lookup["vars"] == {
        "ipam_name": "{{ subnet_name }}-fabric",
        "ipam_vpc_id": "{{ _parent_vpc.id }}",
        "ipam_tenant_id": "{{ netris_tenant_id }}",
    }
    orphan_guard = next(
        task for task in tasks if task.get("name") == "Fail if a reserved child remains without its parent identity"
    )
    assert orphan_guard["ansible.builtin.assert"]["that"] == ["ipam_subnet_by_identity is not mapping"]


def test_ipam_parent_identity_lookup_filters_and_rejects_ambiguity() -> None:
    tasks = yaml.safe_load((IPAM_ROLE_TASKS / "find_subnet_by_identity.yaml").read_text())
    query = next(task for task in tasks if task.get("name") == "Query IPAM subnets for the requested identity")
    assert query["netris.controller.protected_uri"]["force"] is True

    find = next(task for task in tasks if task.get("name") == "Find IPAM subnets matching the supplied identity")
    expression = find["ansible.builtin.set_fact"]["ipam_subnets_by_identity"]
    assert "selectattr('name', 'equalto', ipam_name)" in expression
    assert "selectattr('vpc.id', 'equalto', ipam_vpc_id | int)" in expression
    assert "selectattr('tenant.id', 'equalto', ipam_tenant_id | int)" in expression

    cidr_filter = next(task for task in tasks if task.get("name") == "Filter IPAM subnets by CIDR")
    assert cidr_filter["when"] == "ipam_cidr is defined"
    assert "selectattr('prefix', 'equalto', ipam_cidr)" in cidr_filter["ansible.builtin.set_fact"][
        "ipam_subnets_by_identity"
    ]
    parent_filter = next(task for task in tasks if task.get("name") == "Filter IPAM subnets by parent")
    assert parent_filter["when"] == "ipam_parent_id is defined"

    ambiguity = next(task for task in tasks if task.get("name") == "Fail if the IPAM subnet identity is ambiguous")
    assert ambiguity["ansible.builtin.assert"]["that"] == ["ipam_subnets_by_identity | length <= 1"]
    assert "ipam_cidr | default('unspecified')" in ambiguity["ansible.builtin.assert"]["fail_msg"]
