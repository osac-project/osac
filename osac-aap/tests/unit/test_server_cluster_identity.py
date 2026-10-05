"""Exercise real ServerCluster roles against a small Netris HTTP API stub."""

import json
import os
import subprocess
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

import pytest


PROJECT_ROOT = Path(__file__).resolve().parents[2]


@pytest.fixture
def inventory():
    return []


@pytest.fixture
def request_bodies():
    return []


@pytest.fixture
def read_requests():
    return []


@pytest.fixture
def omit_vpc_from_state_response():
    return False


@pytest.fixture
def netris_server(inventory, request_bodies, read_requests, omit_vpc_from_state_response):
    clusters = []
    writes = []

    class Handler(BaseHTTPRequestHandler):
        def respond(self, data, status=200):
            body = json.dumps({"data": data}).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def do_GET(self):
            read_requests.append(self.path)
            if self.path == "/api/v2/server-cluster":
                self.respond(clusters)
            elif self.path == "/api/v2/hw?type=server":
                self.respond(inventory)
            else:
                found = next((item for item in clusters if self.path == f"/api/v2/server-cluster/{item['id']}"), None)
                if found is None:
                    self.send_error(404)
                else:
                    state = {**found, "status": {"label": "Active"}}
                    if omit_vpc_from_state_response:
                        state.pop("vpc", None)
                        state.pop("vpcId", None)
                    self.respond(state)

        def do_DELETE(self):
            writes.append(("DELETE", self.path))
            self.send_response(204)
            self.end_headers()

        def do_PUT(self):
            writes.append(("PUT", self.path))
            body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            request_bodies.append(body)
            found = next(item for item in clusters if self.path == f"/api/v2/server-cluster/{item['id']}")
            found.update(body)
            self.respond(found)

        def do_POST(self):
            writes.append(("POST", self.path))
            body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            request_bodies.append(body)
            found = {**body, "id": 23}
            clusters.append(found)
            self.respond(found, status=201)

        def log_message(self, _format, *_args):
            pass

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield f"http://127.0.0.1:{server.server_port}", clusters, writes
    finally:
        server.shutdown()
        thread.join()
        server.server_close()


def run_role(tmp_path, url, tasks_from, role_name="netris.controller.server_cluster", role_calls=None,
             report_confirmed_vpc=False, **variables):
    playbook = [{
        "hosts": "localhost",
        "connection": "local",
        "gather_facts": False,
        "vars": {
            "netris_controller_url": url,
            "netris_session_cookie": "test-cookie",
            "server_cluster_name": "shared-name",
            "server_cluster_site_id": 1,
            **variables,
        },
        "tasks": [{
            "name": "Exercise server cluster role",
            "ansible.builtin.include_role": {
                "name": role_name,
                "tasks_from": tasks_from,
            },
            "vars": call_vars,
        } for call_vars in (role_calls if role_calls is not None else [{}])],
        "post_tasks": ([{
            "name": "Report confirmed ServerCluster VPC for test assertions",
            "ansible.builtin.debug": {"var": "_server_cluster_confirmed_vpc_id"},
        }] if report_confirmed_vpc else []),
    }]
    playbook_file = tmp_path / "server_cluster.yml"
    playbook_file.write_text(json.dumps(playbook))
    env = os.environ.copy()
    env["ANSIBLE_CONFIG"] = str(PROJECT_ROOT / "ansible.cfg")
    env["ANSIBLE_LOCAL_TEMP"] = str(tmp_path / "ansible-tmp")
    env["ANSIBLE_REMOTE_TEMP"] = str(tmp_path / "ansible-remote-tmp")
    return subprocess.run(
        ["ansible-playbook", str(playbook_file)],
        cwd=PROJECT_ROOT,
        env=env,
        capture_output=True,
        text=True,
        check=False,
        timeout=60,
    )


def cluster(cluster_id, site_id, vpc_id):
    return {"id": cluster_id, "name": "shared-name", "site": {"id": site_id}, "vpc": {"id": vpc_id}}


def test_delete_with_missing_backend_id_never_falls_back_to_name(tmp_path, netris_server):
    url, clusters, writes = netris_server
    clusters.append(cluster(99, 1, 7))
    result = run_role(tmp_path, url, "delete", server_cluster_backend_id="42", server_cluster_vpc_id=7)
    assert result.returncode == 0, result.stdout + result.stderr
    assert writes == []


def test_delete_with_backend_id_uses_exact_lookup(tmp_path, netris_server, read_requests):
    url, clusters, writes = netris_server
    clusters.append(cluster(22, 1, 7))
    result = run_role(tmp_path, url, "delete", server_cluster_backend_id="22")
    assert result.returncode == 0, result.stdout + result.stderr
    assert writes == [("DELETE", "/api/v2/server-cluster/22")]
    assert read_requests == ["/api/v2/server-cluster/22"]


@pytest.mark.parametrize("tasks_from,site_id,vpc_id", [
    ("delete", 2, 7),
    ("delete", 1, 8),
    ("create", 2, 7),
    ("create", 1, 8),
], ids=["wrong-delete-site", "wrong-delete-vpc", "wrong-update-site", "wrong-update-vpc"])
def test_backend_id_scope_mismatch_never_mutates(tmp_path, netris_server, tasks_from, site_id, vpc_id):
    url, clusters, writes = netris_server
    clusters.append(cluster(22, site_id, vpc_id))
    create_args = {"server_cluster_servers": []} if tasks_from == "create" else {}
    result = run_role(
        tmp_path, url, tasks_from, server_cluster_backend_id="22", server_cluster_site_id=1,
        server_cluster_vpc_id=7, **create_args,
    )
    assert result.returncode != 0, result.stdout + result.stderr
    assert writes == []


def test_delete_by_name_requires_site_and_vpc_match(tmp_path, netris_server):
    url, clusters, writes = netris_server
    clusters.extend([cluster(21, 2, 7), cluster(22, 1, 8), cluster(23, 1, 7)])
    result = run_role(tmp_path, url, "delete", server_cluster_vpc_id=7)
    assert result.returncode == 0, result.stdout + result.stderr
    assert writes == [("DELETE", "/api/v2/server-cluster/23")]


def test_delete_refuses_ambiguous_match(tmp_path, netris_server):
    url, clusters, writes = netris_server
    clusters.extend([cluster(23, 1, 7), cluster(24, 1, 7)])
    result = run_role(tmp_path, url, "delete", server_cluster_vpc_id=7)
    assert result.returncode != 0
    assert writes == []


def test_name_only_lookup_requires_legacy_opt_in(tmp_path, netris_server):
    url, clusters, writes = netris_server
    clusters.append(cluster(23, 1, 7))
    delete_result = run_role(tmp_path, url, "delete")
    create_result = run_role(tmp_path, url, "create", server_cluster_servers=[])
    assert delete_result.returncode != 0
    assert create_result.returncode != 0
    assert writes == []


def test_create_without_vpc_is_allowed_when_no_name_match_exists(tmp_path, netris_server, request_bodies):
    url, clusters, writes = netris_server
    result = run_role(tmp_path, url, "create", server_cluster_servers=[])
    assert result.returncode == 0, result.stdout + result.stderr
    assert clusters == [request_bodies[0] | {"id": 23}]
    assert writes == [("POST", "/api/v2/server-cluster")]
    assert request_bodies[0]["vpc"] == {"id": 0, "name": "Create New"}


def test_create_new_vpc_does_not_reuse_same_name_cluster(tmp_path, netris_server, request_bodies):
    url, clusters, writes = netris_server
    clusters.append(cluster(21, 1, 7))

    result = run_role(
        tmp_path,
        url,
        "create",
        server_cluster_vpc_id=0,
        server_cluster_servers=[],
    )

    assert result.returncode == 0, result.stdout + result.stderr
    assert writes == [("POST", "/api/v2/server-cluster")]
    assert len(clusters) == 2
    assert request_bodies[0]["vpc"] == {"id": 0, "name": "Create New"}


def test_fabric_domain_delete_uses_virtual_network_region_site(tmp_path, netris_server):
    url, clusters, writes = netris_server
    clusters.extend([cluster(21, 1, 7), cluster(22, 2, 7)])
    resource = {
        "metadata": {"name": "shared-name"},
        "spec": {"backendId": "", "vpcId": "7", "region": "region-b"},
    }
    result = run_role(
        tmp_path, url, "delete_server_cluster", role_name="osac.templates.netris",
        server_cluster=resource, netris_region_site_map={"region-b": 2}, netris_site_id=1,
    )
    assert result.returncode == 0, result.stdout + result.stderr
    assert writes == [("DELETE", "/api/v2/server-cluster/22")]


def test_fabric_domain_delete_uses_backend_id_without_vpc(tmp_path, netris_server):
    url, clusters, writes = netris_server
    clusters.extend([cluster(21, 2, 7), cluster(22, 2, 8)])
    resource = {
        "metadata": {"name": "shared-name"},
        "spec": {"backendId": "22", "region": "region-b"},
    }
    result = run_role(
        tmp_path, url, "delete_server_cluster", role_name="osac.templates.netris",
        server_cluster=resource, netris_region_site_map={"region-b": 2}, netris_site_id=1,
    )
    assert result.returncode == 0, result.stdout + result.stderr
    assert writes == [("DELETE", "/api/v2/server-cluster/22")]


@pytest.mark.parametrize("backend_id", ["not-a-number", "0", "-1"])
def test_fabric_domain_delete_rejects_malformed_backend_id(tmp_path, netris_server, backend_id):
    url, clusters, writes = netris_server
    clusters.append(cluster(23, 2, 7))
    resource = {
        "metadata": {"name": "shared-name"},
        "spec": {"backendId": backend_id, "vpcId": "7", "region": "region-b"},
    }
    result = run_role(
        tmp_path, url, "delete_server_cluster", role_name="osac.templates.netris",
        server_cluster=resource, netris_region_site_map={"region-b": 2}, netris_site_id=1,
    )
    assert result.returncode != 0, result.stdout + result.stderr
    assert writes == []


def test_fabric_domain_create_rejects_non_numeric_vpc_id(tmp_path, netris_server):
    url, _clusters, writes = netris_server
    resource = {
        "metadata": {"name": "shared-name"},
        "spec": {"servers": ["server-a"], "templateId": "42", "vpcId": "vpc-7", "region": "region-b"},
    }
    result = run_role(
        tmp_path, url, "create_server_cluster", role_name="osac.templates.netris",
        server_cluster=resource, netris_region_site_map={"region-b": 2}, netris_site_id=1,
    )
    assert result.returncode != 0
    assert writes == []


@pytest.mark.parametrize("backend_id", ["not-a-number", "0"])
def test_fabric_domain_create_rejects_malformed_backend_id(tmp_path, netris_server, backend_id):
    url, _clusters, writes = netris_server
    resource = {
        "metadata": {"name": "shared-name"},
        "spec": {
            "servers": ["server-a"], "templateId": "42", "vpcId": "7",
            "region": "region-b", "backendId": backend_id,
        },
    }
    result = run_role(
        tmp_path, url, "create_server_cluster", role_name="osac.templates.netris",
        server_cluster=resource, netris_region_site_map={"region-b": 2}, netris_site_id=1,
    )
    assert result.returncode != 0, result.stdout + result.stderr
    assert writes == []


def server(server_id, name="server-a", site_id=1):
    # Inventory GET uses site: IDName, verified against Netris's SDK:
    # https://github.com/netrisai/netriswebapi/blob/v4.14.0/v2/types/inventory/types.go
    return {"id": server_id, "name": name, "site": {"id": site_id, "name": f"site-{site_id}"}}


@pytest.mark.parametrize("existing", [False, True], ids=["create", "resize"])
@pytest.mark.parametrize("hosts", [
    [],
    [server(1, "server-a")],  # Partial resolution must not drop server-b.
    [server(1, "server-a"), server(2, "server-b", site_id=2)],
    [server(1, "server-a"), server(2, "server-b"), server(3, "server-b")],
    [{"id": 1, "name": "server-a"}, {"id": 2, "name": "server-b"}, {"id": 3, "name": "server-b"}],
    [server(1, "server-a"), {"id": 2, "name": "server-b"}, {"id": 3, "name": "server-b"}],
    [server(1, "server-a"), {"id": 2, "name": "server-b", "site": None}],
    [server(1, "server-a"), {"id": 2, "name": "server-b", "site": {"name": "site-1"}}],
    [server(1, "server-a"), server(0, "server-b")],
    [server(1, "server-a"), {"name": "server-b"}],
], ids=["absent", "partial", "wrong-site", "same-site-ambiguity", "unscoped-ambiguity",
        "mixed-site-metadata", "null-site", "site-without-id", "invalid-id", "missing-id"])
def test_invalid_inventory_never_mutates(tmp_path, netris_server, inventory, existing, hosts):
    url, clusters, writes = netris_server
    if existing:
        clusters.append({**cluster(23, 1, 7), "servers": [{"id": 1, "name": "server-a"}]})
    inventory.extend(hosts)
    result = run_role(tmp_path, url, "create", server_cluster_vpc_id=7,
                      server_cluster_servers=["server-a", "server-b"])
    assert result.returncode != 0, result.stdout + result.stderr
    assert writes == [], result.stdout + result.stderr


@pytest.mark.parametrize("requested", [
    ["server-a", "server-a"],
    ["server-a", ""],
    ["server-a", "   "],
    ["server-a", {"id": 2, "name": "server-b"}],
    [{"id": 1}],
    [{"name": "server-a"}],
    [{"id": 0, "name": "server-a"}],
    [{"id": "not-an-id", "name": "server-a"}],
    [{"id": True, "name": "server-a"}],
    [{"id": 1, "name": ""}],
    [{"id": 1, "name": "server-a"}, {"id": 2, "name": "server-a"}],
    [{"id": 1, "name": "server-a"}, {"id": "1", "name": "server-b"}],
])
def test_invalid_requested_identity_never_mutates(tmp_path, netris_server, inventory, requested):
    url, _clusters, writes = netris_server
    inventory.extend([server(1), server(2, "server-b")])
    result = run_role(tmp_path, url, "create", server_cluster_vpc_id=7, server_cluster_servers=requested)
    assert result.returncode != 0, result.stdout + result.stderr
    assert writes == [], result.stdout + result.stderr


@pytest.mark.parametrize("existing", [False, True], ids=["create", "resize"])
@pytest.mark.parametrize("scoped", [False, True], ids=["unscoped", "site-scoped"])
def test_all_requested_hosts_are_sent(tmp_path, netris_server, inventory, request_bodies, existing, scoped):
    url, clusters, writes = netris_server
    if existing:
        # Other scopes must not affect which cluster is resized.
        clusters.extend([cluster(21, 2, 7), cluster(22, 1, 8), cluster(23, 1, 7)])
        clusters[-1]["servers"] = [{"id": 1, "name": "server-a"}]
    inventory.extend([server(2, "server-b"), server(1), server(3, "unrequested")])
    if scoped:
        inventory.insert(0, server(99, site_id=2))
    else:
        for host in inventory:
            del host["site"]
    result = run_role(tmp_path, url, "create", server_cluster_vpc_id=7, server_cluster_template_id=42,
                      server_cluster_servers=["server-a", "server-b"], server_cluster_tags=["phase1"])
    assert result.returncode == 0, result.stdout + result.stderr
    expected_write = ("PUT", "/api/v2/server-cluster/23") if existing else ("POST", "/api/v2/server-cluster")
    api_writes = [write for write in writes if write[1].startswith("/api/")]
    assert api_writes == [expected_write]
    assert request_bodies[0]["servers"] == [{"id": 1, "name": "server-a"}, {"id": 2, "name": "server-b"}]
    assert request_bodies[0]["tags"] == ["phase1"]
    if not existing:
        assert request_bodies[0]["site"] == {"id": 1}
        assert request_bodies[0]["vpc"]["id"] == 7
        assert request_bodies[0]["srvClusterTemplate"] == {"id": 42}


def test_create_with_backend_id_uses_exact_lookup(tmp_path, netris_server, inventory, read_requests):
    url, clusters, writes = netris_server
    clusters.append(cluster(22, 1, 7))
    inventory.append(server(1))
    result = run_role(
        tmp_path, url, "create", server_cluster_backend_id="22", server_cluster_vpc_id=7,
        server_cluster_servers=["server-a"],
    )
    assert result.returncode == 0, result.stdout + result.stderr
    assert writes == [("PUT", "/api/v2/server-cluster/22")]
    assert "/api/v2/server-cluster" not in read_requests
    assert read_requests.count("/api/v2/server-cluster/22") == 2


def test_repeated_calls_replace_hosts_for_names_mappings_and_empty_sets(tmp_path, netris_server, inventory, request_bodies):
    url, _clusters, writes = netris_server
    inventory.extend([server(1), server(2, "server-b")])
    result = run_role(tmp_path, url, "create", server_cluster_vpc_id=7, role_calls=[
        {"server_cluster_servers": ["server-a"]},
        {"server_cluster_servers": ["server-a", "server-b"]},
        {"server_cluster_servers": [{"id": "3", "name": "server-c", "extra": "ignored"}]},
        {"server_cluster_servers": [{"id": 4, "name": "server-d"}]},
        {"server_cluster_servers": []},
        {"server_cluster_servers": ["server-b"]},
    ])
    assert result.returncode == 0, result.stdout + result.stderr
    assert [method for method, _path in writes] == ["POST", "PUT", "PUT", "PUT", "PUT", "PUT"]
    assert [body["servers"] for body in request_bodies] == [
        [{"id": 1, "name": "server-a"}],
        [{"id": 1, "name": "server-a"}, {"id": 2, "name": "server-b"}],
        [{"id": "3", "name": "server-c"}],
        [{"id": 4, "name": "server-d"}],
        [],
        [{"id": 2, "name": "server-b"}],
    ]


@pytest.mark.parametrize("missing", [False, True], ids=["complete", "missing-host"])
def test_fabric_domain_wrapper_resolves_region_and_full_membership(tmp_path, netris_server, inventory, request_bodies, missing):
    url, _clusters, writes = netris_server
    inventory.extend([server(99, site_id=1), server(1, site_id=2)])
    if not missing:
        inventory.append(server(2, "server-b", site_id=2))
    resource = {
        "metadata": {"name": "shared-name"},
        "spec": {"servers": ["server-a", "server-b"], "templateId": "42", "vpcId": "7", "region": "region-b"},
    }
    result = run_role(tmp_path, url, "create_server_cluster", role_name="osac.templates.netris",
                      server_cluster=resource, netris_region_site_map={"region-b": 2}, netris_site_id=1)
    if missing:
        assert result.returncode != 0
        assert writes == []
    else:
        assert result.returncode == 0, result.stdout + result.stderr
        assert writes == [("POST", "/api/v2/server-cluster")]
        assert request_bodies[0]["site"] == {"id": 2}
        assert request_bodies[0]["servers"] == [{"id": 1, "name": "server-a"}, {"id": 2, "name": "server-b"}]


@pytest.mark.parametrize("omit_vpc_from_state_response", [True])
def test_fabric_domain_artifact_does_not_fallback_to_requested_vpc(
    tmp_path, netris_server, inventory, omit_vpc_from_state_response,
):
    url, _clusters, writes = netris_server
    inventory.append(server(1, site_id=2))
    resource = {
        "metadata": {"name": "shared-name"},
        "spec": {"servers": ["server-a"], "templateId": "42", "vpcId": "7", "region": "region-b"},
    }
    result = run_role(
        tmp_path, url, "create_server_cluster", role_name="osac.templates.netris",
        server_cluster=resource, netris_region_site_map={"region-b": 2}, netris_site_id=1,
        report_confirmed_vpc=True,
    )
    assert result.returncode == 0, result.stdout + result.stderr
    assert writes == [("POST", "/api/v2/server-cluster")]
    assert '"_server_cluster_confirmed_vpc_id": ""' in result.stdout
