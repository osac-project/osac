"""Subnet teardown removes only its own ACL rows and observes cleanup before continuing."""

import copy
import importlib.util
import json
import os
import shutil
import subprocess
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any

import pytest
import yaml
from ansible.errors import AnsibleFilterError

ROOT = Path(__file__).resolve().parents[2]
COLLECTIONS = ROOT / "collections/ansible_collections"
FILTER = COLLECTIONS / "netris/controller/plugins/filter/subnet_acl.py"
spec = importlib.util.spec_from_file_location("subnet_acl", FILTER)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
select_ids = module.subnet_acl_ids


def _rule(
    id: int | None = 1, name: str = "securitygroup-owner-egress-0-1", vpc: int = 7, cidr: str = "192.0.2.0/24"
) -> dict[str, Any]:
    return {"id": id, "name": name, "vpc": {"id": vpc}, "src_prefix": cidr, "dst_prefix": "0.0.0.0/0"}


def test_selects_directional_prefix_not_subnet_position() -> None:
    ingress = _rule(2, "securitygroup-owner-ingress-6-2")
    ingress.update(src_prefix="0.0.0.0/0", dst_prefix="192.0.2.0/24")
    rules = [
        _rule(),
        ingress,
        _rule(3, cidr="198.51.100.0/24"),
        _rule(4, vpc=8),
        _rule(5, name="securitygroup-owner-extra-egress-0-1"),
        _rule(6, name="manual-rule"),
        _rule(7, cidr="192.0.0.0/16"),
        _rule(8, name="securitygroup-owner-egress-9"),
    ]
    assert select_ids(rules, "7", "192.0.2.0/24", ["securitygroup-owner"]) == [1, 2, 8]
    assert select_ids(rules, 7, "192.0.2.0/24", []) == []


@pytest.mark.parametrize(
    "rules,vpc,cidr,names",
    [
        ({}, 7, "192.0.2.0/24", ["securitygroup-owner"]),
        ([None], 7, "192.0.2.0/24", ["securitygroup-owner"]),
        ([_rule()], 0, "192.0.2.0/24", ["securitygroup-owner"]),
        ([_rule()], True, "192.0.2.0/24", ["securitygroup-owner"]),
        ([_rule()], 7, "192.0.2.1/24", ["securitygroup-owner"]),
        ([_rule()], 7, "::/64", ["securitygroup-owner"]),
        ([_rule()], 7, "192.0.2.0/24", "securitygroup-owner"),
        ([_rule()], 7, "192.0.2.0/24", [""]),
        ([{**_rule(), "vpc": None}], 7, "192.0.2.0/24", ["securitygroup-owner"]),
        ([{**_rule(), "src_prefix": "invalid"}], 7, "192.0.2.0/24", ["securitygroup-owner"]),
        ([_rule(id=None)], 7, "192.0.2.0/24", ["securitygroup-owner"]),
    ],
)
def test_rejects_ambiguous_or_malformed_identity(rules: object, vpc: object, cidr: str, names: object) -> None:
    with pytest.raises(AnsibleFilterError):
        select_ids(rules, vpc, cidr, names)


def test_group_names_are_matched_literally() -> None:
    assert select_ids([_rule(name="sgx1-egress-0-0")], 7, "192.0.2.0/24", ["sg.1"]) == []


@pytest.mark.parametrize("scenario", ["success", "absent", "delete_failure", "retained", "malformed"])
def test_executable_acl_cleanup_role(tmp_path: Path, scenario: str) -> None:
    playbook = shutil.which("ansible-playbook")
    if not playbook:
        pytest.fail("ansible-playbook is required for the executable ACL cleanup regression")
    state = {"rules": [_rule(), _rule(9, cidr="198.51.100.0/24"), _rule(10, vpc=8)], "calls": []}
    if scenario == "absent":
        state["rules"] = state["rules"][1:]

    class Handler(BaseHTTPRequestHandler):
        def respond(self, status: int, data: object) -> None:
            content = json.dumps(data).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(content)))
            self.end_headers()
            self.wfile.write(content)

        def do_GET(self) -> None:
            assert self.path == "/api/acl"
            state["calls"].append("GET")
            self.respond(200, {"data": {} if scenario == "malformed" else state["rules"]})

        def do_DELETE(self) -> None:
            assert self.path == "/api/acl"
            body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            state["calls"].append(body)
            assert body == {"id": [1]}
            if scenario == "delete_failure":
                self.respond(503, {"message": "Injected provider failure"})
            else:
                if scenario != "retained":
                    state["rules"] = [row for row in state["rules"] if row["id"] not in body["id"]]
                self.respond(200, {})

        def log_message(self, *args: object) -> None:
            pass

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    play = [
        {
            "hosts": "localhost",
            "gather_facts": False,
            "connection": "local",
            "vars": {
                "ansible_python_interpreter": os.sys.executable,
                "netris_controller_url": f"http://127.0.0.1:{server.server_port}",
                "netris_session_cookie": "fixture-only",
                "acl_vpc_id": 7,
                "acl_subnet_cidr": "192.0.2.0/24",
                "acl_security_group_names": ["securitygroup-owner"],
            },
            "tasks": [
                {
                    "name": "Run production Subnet ACL cleanup",
                    "ansible.builtin.include_role": {
                        "name": "netris.controller.acl",
                        "tasks_from": "delete_for_subnet",
                    },
                },
                {
                    "name": "Represent next destructive provider operation",
                    "ansible.builtin.copy": {
                        "content": "network removal may proceed",
                        "dest": str(tmp_path / "next-stage"),
                    },
                },
            ],
        }
    ]
    path = tmp_path / "playbook.yaml"
    path.write_text(yaml.safe_dump(play))
    before = copy.deepcopy(state["rules"])
    try:
        result = subprocess.run(
            [playbook, "-i", "localhost,", str(path)],
            capture_output=True,
            text=True,
            env={
                **os.environ,
                "ANSIBLE_COLLECTIONS_PATH": str(ROOT / "collections"),
                "ANSIBLE_NOCOLOR": "1",
                "NETRIS_CONTROLLER_URL": f"http://127.0.0.1:{server.server_port}",
                "NETRIS_VALIDATE_CERTS": "true",
                "NETRIS_CA_PATH": "",
            },
            timeout=60,
        )
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)
    succeeded = scenario in ("success", "absent")
    assert (result.returncode == 0) == succeeded, result.stdout + result.stderr
    assert (tmp_path / "next-stage").exists() == succeeded
    if scenario == "success":
        assert state["rules"] == before[1:]
        assert state["calls"] == ["GET", {"id": [1]}, "GET"]
    elif scenario == "absent":
        assert state["rules"] == before
        assert state["calls"] == ["GET", "GET"]
    else:
        assert state["rules"] == before
        expected_calls = {
            "delete_failure": ["GET", {"id": [1]}],
            "retained": ["GET", {"id": [1]}, "GET"],
            "malformed": ["GET"],
        }
        assert state["calls"] == expected_calls[scenario]


def test_subnet_acl_cleanup_is_before_provider_network_deletion() -> None:
    path = COLLECTIONS / "osac/templates/roles/netris/tasks/delete_subnet.yaml"
    tasks = yaml.safe_load(path.read_text())
    names = [task["name"] for task in tasks]
    cleanup = names.index("Delete SecurityGroup ACL rules for this Subnet")
    assert cleanup < names.index("Delete V-Net from Netris controller")
    assert cleanup < names.index("Delete V-Net for a non-EVPN Subnet")
    assert tasks[cleanup]["vars"]["acl_vpc_id"] == "{{ _subnet_parent_vpc_id | default('') }}"


def test_security_group_create_excludes_terminating_subnets() -> None:
    from jinja2 import Environment

    tasks = yaml.safe_load((COLLECTIONS / "osac/templates/roles/netris/tasks/create_security_group.yaml").read_text())
    nested = [child for task in tasks for child in task.get("block", [])]
    task = next(task for task in nested if task["name"] == "Filter subnets belonging to this VirtualNetwork")
    expression = task["ansible.builtin.set_fact"]["_sg_subnet_cidrs"].strip()[2:-2]
    evaluate = Environment().compile_expression(expression)
    resources = [
        {"metadata": {}, "spec": {"virtualNetwork": "vn", "ipv4Cidr": "192.0.2.0/24"}},
        {
            "metadata": {"deletionTimestamp": "2026-10-31T00:00:00Z"},
            "spec": {"virtualNetwork": "vn", "ipv4Cidr": "198.51.100.0/24"},
        },
        {"metadata": {}, "spec": {"virtualNetwork": "other", "ipv4Cidr": "203.0.113.0/24"}},
    ]
    assert evaluate(_sg_all_subnets={"resources": resources}, sg_virtual_network="vn") == ["192.0.2.0/24"]


def test_subnet_cleanup_waits_for_inflight_security_group_jobs() -> None:
    from jinja2 import Environment

    tasks = yaml.safe_load((COLLECTIONS / "osac/templates/roles/netris/tasks/delete_subnet.yaml").read_text())
    task = next(task for task in tasks if task["name"] == "Look up SecurityGroups for Subnet ACL cleanup")
    env = Environment()
    env.filters["flatten"] = lambda lists: [item for items in lists for item in items]
    ready = env.compile_expression(task["until"])
    group = {"spec": {"virtualNetwork": "vn"}, "status": {"provisioningJobs": [{"state": "Running"}]}}
    args = {"subnet_virtual_network_id": "vn", "_subnet_security_groups": {"resources": [group]}}
    assert not ready(**args)
    group["status"]["provisioningJobs"][0]["state"] = "Succeeded"
    assert ready(**args)
    del group["status"]
    assert ready(**args)
