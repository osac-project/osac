"""Contract-path tests for the Netris delete entry points."""
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


def test_virtual_network_delete_scopes_the_vpc_lookup_to_the_tenant() -> None:
    tasks = list(_walk(_tasks("delete_virtual_network")))
    lookups = [
        task
        for task in tasks
        if task.get("ansible.builtin.include_role", {}).get("name")
        == "netris.controller.vpc"
        and task["ansible.builtin.include_role"].get("tasks_from") == "read"
    ]

    assert lookups, "the delete must resolve the parent VPC"
    assert lookups[0]["vars"]["vpc_tenant_id"] == "{{ netris_tenant_id }}"


def test_virtual_network_delete_always_frees_the_ipam_allocation() -> None:
    tasks = list(_walk(_tasks("delete_virtual_network")))
    deletes = [
        task
        for task in tasks
        if task.get("ansible.builtin.include_role", {}).get("name")
        == "netris.controller.ipam"
        and task["ansible.builtin.include_role"].get("tasks_from")
        == "delete_allocation"
    ]

    assert len(deletes) == 1
    assert "when" not in deletes[0], (
        "an empty VPC lookup must not skip the IPAM allocation delete"
    )


def test_subnet_delete_derives_evpn_cleanup_from_the_network_type() -> None:
    tasks = _tasks("delete_subnet")
    identity = next(
        task
        for task in tasks
        if "_osac_network_type" in str(task.get("ansible.builtin.set_fact", {}))
    )
    expression = str(identity["ansible.builtin.set_fact"]["is_cudn_evpn"])

    assert "_osac_network_type" in expression
    assert "_osac_contract" in expression
    assert "k8s-cleanup-strategy" in expression


def test_subnet_delete_uses_the_supplied_virtual_network_snapshot() -> None:
    tasks = list(_walk(_tasks("delete_subnet")))

    snapshot = [
        task
        for task in tasks
        if "subnet_virtual_network_name" in str(task.get("ansible.builtin.set_fact", {}))
    ]
    assert any(
        "_osac_virtual_network.metadata.name"
        in str(task["ansible.builtin.set_fact"])
        for task in snapshot
    ), "the contract path must take the parent name from the supplied snapshot"

    kubernetes = [
        task for task in tasks if any(key.startswith("kubernetes.core.") for key in task)
    ]
    assert kubernetes, "the legacy path keeps its CR lookups"
    for task in kubernetes:
        assert "_osac_contract" in str(task.get("when")), task.get("name")


def test_subnet_delete_requires_a_parent_vpc_on_the_contract_path() -> None:
    tasks = list(_walk(_tasks("delete_subnet")))

    assert any(
        "_subnet_parent_vpc_id" in str(task.get("ansible.builtin.assert", {}))
        and "_osac_contract" in str(task.get("when"))
        for task in tasks
    ), "the contract path must refuse an unscoped Subnet delete"


def test_subnet_delete_keeps_the_fabric_output_configmap_legacy_only() -> None:
    tasks = _tasks("delete_subnet")
    configmap = next(
        task
        for task in tasks
        if task.get("kubernetes.core.k8s", {}).get("kind") == "ConfigMap"
    )

    assert "not _osac_contract" in str(configmap.get("when"))


def test_subnet_delete_runs_the_evpn_cleanup_on_the_contract_path(
    tmp_path: Path,
) -> None:
    calls: list[tuple[str, str]] = []
    vpc = {"id": 3, "name": "vn-1", "adminTenant": {"id": 1}}
    subnet = {
        "id": 5,
        "name": "sub-1",
        "prefix": "10.0.1.0/24",
        "parentID": 0,
        "purpose": "common",
        "vpc": {"id": 3},
        "tenant": {"id": 1},
        "sites": [{"id": 1}],
    }
    vnet = {
        "id": 7,
        "name": "sub-1",
        "vpc": {"id": 3},
        "tenant": {"id": 1},
        "sites": [{"id": 1}],
    }

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
            if self.path == "/api/v2/vpc":
                self.respond({"data": [vpc]})
                return
            if self.path == "/api/v2/ipam/subnets":
                self.respond({"data": [subnet]})
                return
            if self.path == "/api/v2/vnet":
                self.respond({"data": [vnet]})
                return
            raise AssertionError(self.path)

        def do_DELETE(self):
            calls.append(("DELETE", self.path))
            assert self.path in ("/api/v2/vnet/7", "/api/v2/ipam/subnet/5")
            self.respond({"data": {"id": self.path.rsplit("/", 1)[-1]}})

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    fixture_url = f"http://127.0.0.1:{server.server_port}"
    thread = Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        playbook = tmp_path / "delete_subnet_contract.yaml"
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
                            "osac_job_vars": {
                                "manager": {
                                    "name": "netris",
                                    "type": "fabric",
                                    "ansible_role": "osac.templates.netris",
                                },
                                "task": "delete_subnet",
                                "tenant": "tenant-1",
                                "network_type": "evpn",
                                "resource": {
                                    "apiVersion": "osac.openshift.io/v1alpha1",
                                    "kind": "Subnet",
                                    "id": "sub-1",
                                    "metadata": {
                                        "name": "sub-1",
                                        "namespace": "ns",
                                        "uid": "uid-subnet",
                                        "generation": 1,
                                    },
                                    "spec": {"ipv4Cidr": "10.0.1.0/24"},
                                },
                                "virtual_network": {
                                    "apiVersion": "osac.openshift.io/v1alpha1",
                                    "kind": "VirtualNetwork",
                                    "id": "vn-1",
                                    "metadata": {
                                        "name": "vn-1",
                                        "namespace": "ns",
                                        "uid": "uid-vn",
                                        "generation": 1,
                                    },
                                    "spec": {"ipv4Cidr": "10.0.0.0/16"},
                                },
                                "ipam": {
                                    "gateway_ipv4": "10.0.1.1",
                                    "physical_ipv4_cidrs": ["10.0.1.0/25"],
                                    "overlay_ipv4_cidrs": ["10.0.1.128/25"],
                                    "lb_vip_ipv4_cidrs": [],
                                },
                            },
                        },
                        "tasks": [
                            {
                                "name": "Delete the Subnet on the contract path",
                                "ansible.builtin.include_tasks": str(
                                    TASKS / "delete_subnet.yaml"
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
    assert ("GET", "/api/v2/vpc") in calls
    assert ("DELETE", "/api/v2/vnet/7") in calls
    assert ("DELETE", "/api/v2/ipam/subnet/5") in calls


def _contract_nat_delete(tasks: list[dict]) -> dict:
    return next(
        task
        for task in tasks
        if task.get("ansible.builtin.include_role", {}).get("name")
        == "netris.controller.nat"
        and "_osac_resource.id" in str(task.get("vars", {}).get("nat_name", ""))
    )


def test_nat_gateway_contract_delete_verifies_the_snat_rule() -> None:
    fields = _contract_nat_delete(list(_walk(_tasks("delete_nat_gateway"))))["vars"][
        "nat_expected_fields"
    ]

    assert fields["action"] == "SNAT"
    assert "site.id" in fields
    assert fields["vpc.id"] == "{{ netris_vpc.id }}"
    assert fields["sourceAddress"] == "{{ _osac_virtual_network.spec.ipv4Cidr }}"
    assert fields["snatToIP"] == "{{ _osac_external_ip_ref.address }}/32"


def test_external_ip_contract_detach_verifies_the_dnat_rule() -> None:
    fields = _contract_nat_delete(list(_walk(_tasks("detach_external_ip"))))["vars"][
        "nat_expected_fields"
    ]

    assert fields["action"] == "DNAT"
    assert "site.id" in fields
    assert fields["vpc.id"] == "{{ netris_mgmt_vpc_id }}"
    assert fields["destinationAddress"] == "{{ _osac_external_ip_ref.address }}/32"
    assert fields["dnatToIP"] == "{{ _osac_destination.address }}/32"
