"""Exercise Netris contract roles against a stateful localhost HTTP provider."""

import json
import os
import subprocess
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

import pytest
import yaml

ROOT = Path(__file__).resolve().parents[2]


def snapshot(kind, name, spec=None):
    return {
        "apiVersion": "osac.openshift.io/v1alpha1",
        "kind": kind,
        "id": name + "-api-id",
        "metadata": {"name": name, "namespace": "fixture", "uid": name + "-uid", "generation": 1},
        "spec": spec or {},
    }


def run_task(
    tmp_path, task, extra, url="http://127.0.0.1:1", contract=True, role_overrides=None, configuration_env=None
):
    variables = {"netris_session_cookie": "fixture-cookie", **extra}
    if contract:
        variables = {
            "netris_session_cookie": "fixture-cookie",
            "osac_job_vars": {
                "manager": {"name": "netris", "type": "fabric", "ansible_role": "osac.templates.netris"},
                "task": task,
                "tenant": "tenant-api-id",
                **extra,
            },
        }
    variables.update(role_overrides or {})
    play = [
        {
            "hosts": "localhost",
            "gather_facts": False,
            "vars_files": [str(ROOT / "group_vars/all/configuration.yaml")],
            "vars": variables,
            "tasks": [
                {
                    "name": "Exercise the Netris contract entry point",
                    "ansible.builtin.include_role": {"name": "osac.templates.netris", "tasks_from": task},
                }
            ],
        }
    ]
    path = tmp_path / f"{task}.yaml"
    path.write_text(yaml.safe_dump(play))
    env = {
        **os.environ,
        "PATH": f"{ROOT}/.venv/bin:" + os.environ["PATH"],
        "ANSIBLE_LOCAL_TEMP": str(tmp_path / "ansible-local"),
        "ANSIBLE_STDOUT_CALLBACK": "json",
        "NETRIS_CONTROLLER_URL": url,
        "NETRIS_USERNAME": "fixture",
        "NETRIS_PASSWORD": "fixture",
        "NETRIS_SITE_ID": "1",
        "NETRIS_TENANT_ID": "1",
        "NETRIS_MGMT_VPC_ID": "9",
        "NETRIS_BM_PROVISIONING_VNET": "provisioning",
        "NETRIS_VALIDATE_CERTS": "false",
        "NETRIS_CA_PATH": "",
        **(configuration_env or {}),
    }
    result = subprocess.run(
        ["ansible-playbook", "-i", "localhost,", "-c", "local", str(path)],
        cwd=ROOT,
        env=env,
        capture_output=True,
        text=True,
        timeout=90,
    )
    (tmp_path / f"{task}.stdout").write_text(result.stdout)
    (tmp_path / f"{task}.stderr").write_text(result.stderr)
    data = json.loads(result.stdout[result.stdout.index("{") :])
    stats = {}
    for play in data["plays"]:
        for item in play["tasks"]:
            for host in item["hosts"].values():
                stats.update(host.get("ansible_stats", {}).get("data", {}))
                if "_dhcp_leases" in host.get("ansible_facts", {}):
                    stats["leases"] = host["ansible_facts"]["_dhcp_leases"]
    return result, stats


@pytest.fixture
def provider():
    state = {
        "calls": [],
        "subnets": [],
        "hosts": [],
        "inventory": [],
        "clusters": [],
        "subnet_http_status": 200,
        "vpcs": [],
        "vnets": [],
        "ports": [],
        "fail_put": None,
        "retain_tags": True,
    }

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass

        def respond(self, value, status=200):
            body = json.dumps({"data": value}).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def do_GET(self):
            state["calls"].append(("GET", self.path))
            if self.path == "/api/v2/ipam/subnets":
                return self.respond(state["subnets"], state["subnet_http_status"])
            if self.path.startswith("/api/v2/ipam/hosts/"):
                return self.respond(state["hosts"])
            if self.path == "/api/v2/server-cluster":
                return self.respond(state["clusters"])
            if self.path == "/api/v2/hw?type=server":
                return self.respond(state["inventory"])
            if self.path == "/api/v2/server-cluster/23":
                return self.respond({**state["clusters"][0], "status": {"label": "Active"}})
            if self.path == "/api/v2/vpc":
                return self.respond(state["vpcs"])
            if self.path == "/api/v2/vnet":
                return self.respond(state["vnets"])
            if self.path.startswith("/api/v2/vnet/"):
                return self.respond(
                    next(item for item in state["vnets"] if str(item["id"]) == self.path.rsplit("/", 1)[-1])
                )
            if self.path.startswith("/api/v2/ports?switchID="):
                return self.respond(state["ports"])
            return self.respond([], 404)

        def do_PUT(self):
            body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            state["calls"].append(("PUT", self.path, body))
            vnet = next(item for item in state["vnets"] if str(item["id"]) == self.path.rsplit("/", 1)[-1])
            if state["fail_put"] == self.path:
                return self.respond({}, 503)
            if not state["retain_tags"]:
                body.pop("tags", None)
            vnet.update(body)
            return self.respond(vnet)

        def do_POST(self):
            body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            state["calls"].append(("POST", self.path, body))
            if self.path == "/api/v2/server-cluster":
                created = {**body, "id": 23}
                if body.get("vpc", {}).get("id") == 0:
                    created["vpc"] = {"id": 99, "name": "provider-created-vpc"}
                state["clusters"].append(created)
                return self.respond(created, 201)
            return self.respond([], 400)

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield f"http://127.0.0.1:{server.server_port}", state
    finally:
        server.shutdown()
        server.server_close()
        thread.join()


@pytest.mark.parametrize("records", [1, 2])
def test_dhcp_result_is_ipv4_not_the_matching_mac(tmp_path, provider, records):
    url, state = provider
    state["subnets"] = [{"id": 5, "name": "subnet-a"}]
    state["hosts"] = [
        {"address": "10.0.1.130", "mac": [{"address": "aa:bb:cc:dd:ee:ff", "source": "dhcp", "state": "active"}]}
    ] * records
    extra = {
        "resource": snapshot("NetworkAttachment", "attachment"),
        "target": {"kind": "BareMetalInstance", "id": "bmi-id", "host_name": "server-a"},
        "subnet": snapshot("Subnet", "subnet-a", {"ipv4Cidr": "10.0.1.0/24"}),
        "network_attachment": {"interface": {"type": "physical", "name": "eth1", "mac_address": "aa:bb:cc:dd:ee:ff"}},
    }
    result, stats = run_task(tmp_path, "query_dhcp_lease", extra, url)
    assert result.returncode == 0, result.stderr + result.stdout[-3000:]
    assert stats["osac_result"]["dhcp_lease"]["ip_address"] == "10.0.1.130", stats
    assert stats["leases"][0]["ip_address"] == "10.0.1.130", stats


@pytest.mark.parametrize("task", ["create_external_ip", "reserve_cluster_ingress_ip"])
@pytest.mark.parametrize("status, retryable", [(400, False), (403, False), (429, True), (500, True)])
def test_provider_retry_policy_survives_the_role_boundary(tmp_path, provider, task, status, retryable):
    url, state = provider
    state["subnet_http_status"] = status
    extra = {
        "resource": snapshot("ExternalIP", "eip-a", {"pool": "pool-a-api-id"}),
        "external_ip_pool": snapshot("ExternalIPPool", "pool-a", {"cidrs": ["198.51.100.0/29"]}),
    }
    if task == "reserve_cluster_ingress_ip":
        extra = {
            "resource": snapshot("ClusterOrder", "cluster-a"),
            "cluster_id": "cluster-a-api-id",
            "subnet": snapshot("Subnet", "subnet-a", {"ipv4Cidr": "10.0.1.0/24"}),
            "ipam": {"gateway_ipv4": "10.0.1.1", "lb_vip_ipv4_cidrs": ["10.0.1.192/26"]},
        }
    result, stats = run_task(tmp_path, task, extra, url)
    assert result.returncode != 0
    assert ("GET", "/api/v2/ipam/subnets") in state["calls"]
    assert stats["osac_error"]["retryable"] is retryable, stats


def test_legacy_malformed_vpc_is_rejected_before_provider_mutation(tmp_path, provider):
    url, state = provider
    state["inventory"] = [{"id": 4, "name": "server-a", "site": {"id": 1}}]
    resource = {
        "metadata": {"name": "legacy-sc"},
        "spec": {"servers": ["server-a"], "templateId": "42", "vpcId": "vpc-7", "region": "lab"},
    }
    result, stats = run_task(tmp_path, "create_server_cluster", {"server_cluster": resource}, url, contract=False)
    assert result.returncode != 0
    assert state["calls"] == []
    mutations = [call for call in state["calls"] if call[0] != "GET"]
    assert not mutations, {"mutations": mutations, "returncode": result.returncode, "stats": stats}


def attachment_fixture(state):
    state["vpcs"] = [{"id": 3, "name": "vn-a", "adminTenant": {"id": 1}}]
    state["inventory"] = [{"id": 4, "name": "server-a", "site": {"id": 1}}]
    state["ports"] = [{"id": 101, "port": "eth1"}, {"id": 102, "port": "eth2"}, {"id": 103, "port": "east-west"}]
    state["vnets"] = [
        {"id": 7, "name": "subnet-a", "vpc": {"id": 3}, "tenant": {"id": 1}, "state": "active", "ports": []},
        {
            "id": 8,
            "name": "provisioning",
            "vpc": {"id": 9},
            "tenant": {"id": 1},
            "state": "active",
            "ports": [{"id": 101}, {"id": 102}, {"id": 201}],
            "tags": ["operator-owned-tag"],
        },
        {
            "id": 10,
            "name": "east-west",
            "vpc": {"id": 3},
            "tenant": {"id": 1},
            "state": "active",
            "ports": [{"id": 103}],
        },
        {
            "id": 11,
            "name": "provisioning",
            "vpc": {"id": 99},
            "tenant": {"id": 2},
            "state": "active",
            "ports": [{"id": 301}],
        },
    ]
    return {
        "resource": snapshot("NetworkAttachment", "attachment"),
        "target": {"kind": "BareMetalInstance", "id": "bmi-id", "host_name": "server-a"},
        "virtual_network": snapshot("VirtualNetwork", "vn-a", {"ipv4Cidr": "10.0.0.0/16"}),
        "subnet": snapshot("Subnet", "subnet-a", {"ipv4Cidr": "10.0.1.0/24"}),
        "ipam": {"gateway_ipv4": "10.0.1.1", "physical_ipv4_cidrs": ["10.0.1.128/26"]},
        "network_attachment": {"interface": {"type": "physical", "name": "eth1", "mac_address": "aa:bb:cc:dd:ee:ff"}},
    }


def port_ids(vnet):
    return sorted(int(port["id"]) for port in vnet["ports"])


@pytest.mark.parametrize("legacy_env", [False, True], ids=["provisioning-setting", "parking-fallback"])
def test_all_fabric_ports_are_isolated_and_restored_in_fresh_jobs(tmp_path, provider, legacy_env):
    url, state = provider
    extra = attachment_fixture(state)
    env = {"NETRIS_BM_PROVISIONING_VNET": "", "NETRIS_BM_PARKING_VNET": "provisioning"} if legacy_env else {}
    for index, task in enumerate(["apply_network_attachment"] * 2 + ["delete_network_attachment"] * 2):
        writes_before = len([call for call in state["calls"] if call[0] == "PUT"])
        result, _ = run_task(tmp_path, task, extra, url, configuration_env=env)
        assert result.returncode == 0, result.stderr + result.stdout[-5000:]
        if index in (1, 3):
            assert len([call for call in state["calls"] if call[0] == "PUT"]) == writes_before
        if task == "apply_network_attachment":
            assert port_ids(state["vnets"][0]) == [101]
            assert port_ids(state["vnets"][1]) == [201]
            assert "osac-na:attachment-uid:101,102" in state["vnets"][1]["tags"]
        else:
            assert port_ids(state["vnets"][0]) == []
            assert port_ids(state["vnets"][1]) == [101, 102, 201]
            assert state["vnets"][1]["tags"] == ["operator-owned-tag"]
        assert port_ids(state["vnets"][2]) == [103]
        assert port_ids(state["vnets"][3]) == [301]
    assert not any(call[0] == "PUT" and call[1] in ["/api/v2/vnet/10", "/api/v2/vnet/11"] for call in state["calls"])


@pytest.mark.parametrize("recovery_task", ["apply_network_attachment", "delete_network_attachment"])
def test_lost_apply_can_be_retried_or_deleted_from_saved_snapshot(tmp_path, provider, recovery_task):
    url, state = provider
    extra = attachment_fixture(state)
    state["fail_put"] = "/api/v2/vnet/7"
    result, _ = run_task(tmp_path, "apply_network_attachment", extra, url)
    assert result.returncode != 0
    assert port_ids(state["vnets"][1]) == [201]
    assert "osac-na:attachment-uid:101,102" in state["vnets"][1]["tags"]
    state["fail_put"] = None
    result, _ = run_task(tmp_path, recovery_task, extra, url)
    assert result.returncode == 0, result.stderr + result.stdout[-5000:]
    expected = [201] if recovery_task == "apply_network_attachment" else [101, 102, 201]
    assert port_ids(state["vnets"][1]) == expected
    assert port_ids(state["vnets"][2]) == [103]


def test_failed_delete_keeps_snapshot_until_full_restoration(tmp_path, provider):
    url, state = provider
    extra = attachment_fixture(state)
    result, _ = run_task(tmp_path, "apply_network_attachment", extra, url)
    assert result.returncode == 0, result.stdout[-5000:]
    state["fail_put"] = "/api/v2/vnet/8"
    result, _ = run_task(tmp_path, "delete_network_attachment", extra, url)
    assert result.returncode != 0
    assert "osac-na:attachment-uid:101,102" in state["vnets"][1]["tags"]
    state["fail_put"] = None
    result, _ = run_task(tmp_path, "delete_network_attachment", extra, url)
    assert result.returncode == 0, result.stdout[-5000:]
    assert port_ids(state["vnets"][1]) == [101, 102, 201]
    assert state["vnets"][1]["tags"] == ["operator-owned-tag"]


def test_missing_durable_snapshot_refuses_to_disconnect_ports(tmp_path, provider):
    url, state = provider
    extra = attachment_fixture(state)
    state["retain_tags"] = False
    result, _ = run_task(tmp_path, "apply_network_attachment", extra, url)
    assert result.returncode != 0
    assert port_ids(state["vnets"][0]) == []
    assert port_ids(state["vnets"][1]) == [101, 102, 201]
