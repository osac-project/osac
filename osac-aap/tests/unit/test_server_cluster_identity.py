"""Exercise ServerCluster lookup against a small Netris API stub."""

import json
import os
import subprocess
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

import pytest


PROJECT_ROOT = Path(__file__).resolve().parents[2]


@pytest.fixture
def netris_server():
    clusters = []
    writes = []

    class Handler(BaseHTTPRequestHandler):
        def do_GET(self):
            if self.path != "/api/v2/server-cluster":
                self.send_error(404)
                return
            body = json.dumps({"data": clusters}).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def do_DELETE(self):
            writes.append(("DELETE", self.path))
            self.send_response(204)
            self.end_headers()

        def do_PUT(self):
            writes.append(("PUT", self.path))
            self.send_response(200)
            self.end_headers()

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


def run_role(tmp_path, url, tasks_from, role_name="netris.controller.server_cluster", **variables):
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
            "ansible.builtin.include_role": {
                "name": role_name,
                "tasks_from": tasks_from,
            }
        }],
    }]
    playbook_file = tmp_path / "server_cluster.yml"
    playbook_file.write_text(json.dumps(playbook))
    env = os.environ.copy()
    env["ANSIBLE_CONFIG"] = str(PROJECT_ROOT / "ansible.cfg")
    env["ANSIBLE_LOCAL_TEMP"] = str(tmp_path / "ansible-tmp")
    return subprocess.run(
        ["ansible-playbook", str(playbook_file)],
        cwd=PROJECT_ROOT,
        env=env,
        capture_output=True,
        text=True,
        check=False,
    )


def cluster(cluster_id, site_id, vpc_id):
    return {"id": cluster_id, "name": "shared-name", "site": {"id": site_id}, "vpc": {"id": vpc_id}}


def test_delete_with_missing_backend_id_never_falls_back_to_name(tmp_path, netris_server):
    url, clusters, writes = netris_server
    clusters.append(cluster(99, 1, 7))
    result = run_role(tmp_path, url, "delete", server_cluster_backend_id="42", server_cluster_vpc_id=7)
    assert result.returncode == 0, result.stdout + result.stderr
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
