"""Exercise OSAC-4923 through its public Ansible playbooks and Netris HTTP API."""

from __future__ import annotations

import copy
import json
import os
import secrets
import subprocess
import sys
import tempfile
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import urlsplit


OSAC_AAP = Path(__file__).resolve().parents[5]
NAMESPACE = os.environ.get("NETRIS_IDEMPOTENCY_NAMESPACE", "osac-netris-idem-local")
TENANT_ID = "integration-tenant"
VNET_UUID = "88888888-8888-4888-8888-888888888888"
SUBNET_UUID = "99999999-9999-4999-8999-999999999999"
PARENT_VNET_NAME = "netris-idem-parent"
SUBNET_NAME = "netris-idem-subnet"
EXTERNAL_IP_NAME = "netris-idem-eip"


def empty_state():
    """Return isolated provider state for one test scenario."""
    return {
        "vpcs": [],
        "vpc_details": {},
        "allocations": [],
        "ipam_subnets": [],
        "vnets": [],
        "vnet_details": {},
        "nat_rules": [],
        "nat_details": {},
        "servers": [{"id": 42, "name": "netris-idem-server"}],
        "ports": [{"id": 55, "port": "eth0"}],
        "acls": [],
        "responses": {},
        "after_apply_responses": {},
        "ignore_vnet_put": False,
        "next_ids": {"vpc": 101, "allocation": 201, "ipam_subnet": 202, "vnet": 301, "nat": 401},
    }


class NetrisFixture(ThreadingHTTPServer):
    """In-memory Netris API double with isolated state and a credential-free log."""

    daemon_threads = True

    def __init__(self):
        super().__init__(("127.0.0.1", 0), NetrisFixtureHandler)
        self.lock = threading.RLock()
        self.state = empty_state()
        self.calls: list[tuple[str, str]] = []
        self.created: list[tuple[str, dict]] = []

    def reset(self, state=None):
        """Replace scenario data and clear observations, preserving no prior state."""
        with self.lock:
            self.state = {**empty_state(), **copy.deepcopy(state or {})}
            self.calls = []
            self.created = []

    def clear_calls(self):
        """Clear observations without changing provider objects or failure controls."""
        with self.lock:
            self.calls = []
            self.created = []


class NetrisFixtureHandler(BaseHTTPRequestHandler):
    """Serve only endpoints reached by the Netris playbooks under test."""

    def _respond(self, status, payload=None):
        body = json.dumps(payload if payload is not None else {}).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _record(self):
        # Never retain cookies, headers, credentials, or arbitrary request bodies.
        with self.server.lock:
            self.server.calls.append((self.command, self.path))

    def _state(self):
        return self.server.state

    def _response_override(self):
        key = f"{self.command} {self.path}"
        responses = self._state().get("responses", {})
        override = responses.get(key)
        if override is None:
            override = responses.get(f"{self.command} {urlsplit(self.path).path}")
        if override is None:
            return None
        return override

    def _response_after_apply(self):
        key = f"{self.command} {self.path}"
        responses = self._state().get("after_apply_responses", {})
        override = responses.get(key)
        if override is None:
            override = responses.get(f"{self.command} {urlsplit(self.path).path}")
        return override

    def _body(self):
        size = int(self.headers.get("Content-Length", "0"))
        data = self.rfile.read(size) if size else b"{}"
        try:
            value = json.loads(data)
        except (json.JSONDecodeError, UnicodeDecodeError):
            return {}
        return value if isinstance(value, dict) else {}

    def do_GET(self):
        path = urlsplit(self.path).path
        if path == "/_health":
            self._respond(200, {"ok": True})
            return
        self._record()
        override = self._response_override()
        if override is not None:
            self._respond(override[0], override[1])
            return

        state = self._state()
        if path == "/api/v2/vpc":
            result = state["vpcs"]
        elif path == "/api/v2/ipam":
            result = state["allocations"]
        elif path == "/api/v2/ipam/subnets":
            result = state["ipam_subnets"]
        elif path == "/api/v2/vnet":
            result = state["vnets"]
        elif path == "/api/v2/nat":
            result = state["nat_rules"]
        elif path == "/api/acl":
            result = state["acls"]
        elif path == "/api/v2/hw":
            result = state["servers"]
        elif path == "/api/v2/ports":
            result = state["ports"]
        elif path.startswith("/api/v2/vpc/"):
            resource_id = path.rsplit("/", 1)[-1]
            detail = state["vpc_details"].get(str(resource_id))
            if detail is None:
                detail = next(
                    (item for item in state["vpcs"] if str(item.get("id")) == resource_id),
                    None,
                )
            if detail is None:
                self._respond(404, {"message": "VPC not found"})
                return
            self._respond(200, {"data": detail})
            return
        elif path.startswith("/api/v2/nat/"):
            resource_id = path.rsplit("/", 1)[-1]
            detail = state["nat_details"].get(str(resource_id))
            if detail is None:
                detail = next(
                    (item for item in state["nat_rules"] if str(item.get("id")) == resource_id),
                    None,
                )
            if detail is None:
                self._respond(404, {"message": "NAT rule not found"})
                return
            self._respond(200, {"data": detail})
            return
        elif path.startswith("/api/v2/vnet/"):
            vnet_id = path.rsplit("/", 1)[-1]
            detail = state["vnet_details"].get(str(vnet_id))
            if detail is None:
                detail = next(
                    (item for item in state["vnets"] if str(item.get("id")) == str(vnet_id)),
                    None,
                )
            if detail is None:
                self._respond(404, {"message": "V-Net not found"})
                return
            self._respond(200, {"data": detail})
            return
        else:
            self._respond(404, {"message": "Unexpected fixture GET"})
            return
        self._respond(200, {"data": result})

    def do_POST(self):
        path = urlsplit(self.path).path
        self._record()
        override = self._response_override()
        if override is not None and override[0] not in (200, 201):
            self._respond(override[0], override[1])
            return

        body = self._body()
        state = self._state()
        route_data = {
            "/api/v2/vpc": ("vpcs", "vpc"),
            "/api/v2/ipam/allocation": ("allocations", "allocation"),
            "/api/v2/ipam/subnet": ("ipam_subnets", "ipam_subnet"),
            "/api/v2/vnet": ("vnets", "vnet"),
            "/api/v2/nat": ("nat_rules", "nat"),
            "/api/acl": ("acls", "acl"),
        }
        if path not in route_data:
            self._respond(404, {"message": "Unexpected fixture POST"})
            return

        collection, id_key = route_data[path]
        with self.server.lock:
            new_id = state["next_ids"].get(id_key, 501)
            state["next_ids"][id_key] = new_id + 1
            created = {**copy.deepcopy(body), "id": new_id}
            if path == "/api/v2/vnet":
                created.setdefault("vxlanID", 3010 + len(state["vnets"]))
                created.setdefault("state", "active")
                created.setdefault("provisioning", False)
                state["vnet_details"][str(new_id)] = copy.deepcopy(created)
            state[collection].append(created)
            self.server.created.append((path, copy.deepcopy(body)))
        after_apply = self._response_after_apply()
        if after_apply is not None:
            self._respond(after_apply[0], after_apply[1])
        elif override is not None:
            self._respond(override[0], override[1])
        else:
            self._respond(201, {"data": {"id": new_id}})

    def do_PUT(self):
        path = urlsplit(self.path).path
        self._record()
        override = self._response_override()
        if override is not None and override[0] not in (200, 201):
            self._respond(override[0], override[1])
            return
        body = self._body()
        state = self._state()
        if path.startswith("/api/v2/vnet/"):
            vnet_id = path.rsplit("/", 1)[-1]
            with self.server.lock:
                if not state["ignore_vnet_put"]:
                    current = state["vnet_details"].get(str(vnet_id), {})
                    updated = {**current, **copy.deepcopy(body), "id": current.get("id", int(vnet_id))}
                    state["vnet_details"][str(vnet_id)] = updated
                    for index, item in enumerate(state["vnets"]):
                        if str(item.get("id")) == str(vnet_id):
                            state["vnets"][index] = {**item, **updated}
                            break
                self.server.created.append((path, copy.deepcopy(body)))
            after_apply = self._response_after_apply()
            if after_apply is not None:
                self._respond(after_apply[0], after_apply[1])
            else:
                self._respond(200, {"data": body})
            return
        self._respond(404, {"message": "Unexpected fixture PUT"})

    def do_DELETE(self):
        path = urlsplit(self.path).path
        self._record()
        override = self._response_override()
        if override is not None:
            self._respond(override[0], override[1])
            return

        route = None
        if path.startswith("/api/v2/ipam/allocation/"):
            route = ("allocations", path.rsplit("/", 1)[-1])
        elif path.startswith("/api/v2/ipam/subnet/"):
            route = ("ipam_subnets", path.rsplit("/", 1)[-1])
        elif path.startswith("/api/v2/vpc/"):
            route = ("vpcs", path.rsplit("/", 1)[-1])
        elif path.startswith("/api/v2/nat/"):
            route = ("nat_rules", path.rsplit("/", 1)[-1])
        elif path.startswith("/api/v2/vnet/"):
            route = ("vnets", path.rsplit("/", 1)[-1])
        if route is None:
            self._respond(404, {"message": "Unexpected fixture DELETE"})
            return

        collection, resource_id = route
        with self.server.lock:
            original = self._state()[collection]
            remaining = [item for item in original if str(item.get("id")) != resource_id]
            deleted = len(remaining) != len(original)
            self._state()[collection] = remaining
            if collection == "vnets" and deleted:
                self._state()["vnet_details"].pop(resource_id, None)
        after_apply = self._response_after_apply()
        if after_apply is not None and deleted:
            self._respond(after_apply[0], after_apply[1])
            return
        self._respond(200 if deleted else 404, {"data": [] if deleted else {"message": "Not found"}})

    def log_message(self, format, *args):
        """Suppress request logs so test credentials and headers are never printed."""


class NetrisIdempotencyPlaybookTests(unittest.TestCase):
    """Verify create, drift, retry, default-guard, and move behavior end to end."""

    @classmethod
    def setUpClass(cls):
        cls.server = NetrisFixture()
        cls.thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()
        cls.server.server_close()
        cls.thread.join(timeout=5)

    def setUp(self):
        self.server.reset()

    def _run_playbook(self, playbook, resource, succeeds=True, extra_vars=None, expected_message=None):
        job_vars = {
            "osac_job_vars": {"resource": resource},
            "netris_controller_url": f"http://127.0.0.1:{self.server.server_port}",
            "netris_session_cookie": secrets.token_hex(16),
            "netris_validate_certs": False,
            "netris_site_id": 1,
            "netris_tenant_id": 77,
            "netris_region_site_map": {"integration": 1},
            "netris_mgmt_vpc_id": 999,
            "netris_mgmt_vpc_name": "management",
            "netris_bm_provisioning_vnet": "provisioning",
            "netris_vnet_active_retries": 1,
            "netris_vnet_active_delay": 0,
            "_vnet_lock_max_retries": 1,
            "_vnet_lock_retry_delay": 0,
            "ansible_connection": "local",
            "ansible_become": False,
            "ansible_python_interpreter": sys.executable,
        }
        job_vars.update(extra_vars or {})
        env = os.environ.copy()
        env.update(
            {
                "NETRIS_CONTROLLER_URL": job_vars["netris_controller_url"],
                "NETRIS_VALIDATE_CERTS": str(
                    job_vars["netris_validate_certs"]
                ).lower(),
                "NETRIS_CA_PATH": job_vars.get("netris_ca_path", "") or "",
            }
        )
        with tempfile.NamedTemporaryFile(mode="w", suffix=".json", delete=False) as vars_file:
            json.dump(job_vars, vars_file)
            vars_path = Path(vars_file.name)
        try:
            result = subprocess.run(
                [
                    "uv", "run", "ansible-playbook", "-i", "localhost,",
                    str(OSAC_AAP / playbook), "-e", f"@{vars_path}",
                ],
                cwd=OSAC_AAP,
                env=env,
                capture_output=True,
                text=True,
                timeout=180,
                check=False,
            )
        finally:
            vars_path.unlink(missing_ok=True)
        output = result.stdout + result.stderr
        diagnostic = output[-12000:]
        self.assertEqual(result.returncode == 0, succeeds, diagnostic)
        if expected_message:
            self.assertTrue(expected_message in output, diagnostic)
        return output

    def _vn_resource(self, name="netris-idem-create-vn", cidr="10.232.0.0/16"):
        return {
            "metadata": {
                "name": name,
                "namespace": NAMESPACE,
                "annotations": {
                    "osac.openshift.io/tenant": TENANT_ID,
                    "osac.openshift.io/implementation-strategy": "netris",
                },
            },
            "spec": {"region": "integration", "ipv4Cidr": cidr},
        }

    def _parent_provider_state(self, **overrides):
        state = empty_state()
        state.update(
            {
                "vpcs": [{"id": 101, "name": PARENT_VNET_NAME, "vni": 9101, "adminTenant": {"id": 77}}],
            }
        )
        state.update(overrides)
        return state

    @staticmethod
    def _resource_posts(server, path):
        return [body for created_path, body in server.created if created_path == path]

    def test_virtual_network_create_and_rerun_are_idempotent(self):
        resource = self._vn_resource()
        self._run_playbook("playbook_osac_create_virtual_network.yml", resource)
        self.assertEqual(len(self._resource_posts(self.server, "/api/v2/vpc")), 1)
        self.assertEqual(len(self._resource_posts(self.server, "/api/v2/ipam/allocation")), 1)
        self.assertEqual(self.server.state["vpcs"][0]["adminTenant"], {"id": 77})
        self.assertEqual(self.server.state["allocations"][0]["prefix"], "10.232.0.0/16")

        self.server.clear_calls()
        self._run_playbook("playbook_osac_create_virtual_network.yml", resource)
        self.assertEqual(self._resource_posts(self.server, "/api/v2/vpc"), [])
        self.assertEqual(self._resource_posts(self.server, "/api/v2/ipam/allocation"), [])

    def test_virtual_network_tenant_drift_fails_without_mutation(self):
        resource = self._vn_resource()
        self.server.reset({"vpcs": [{"id": 101, "name": resource["metadata"]["name"], "adminTenant": {"id": 88}}]})
        self._run_playbook(
            "playbook_osac_create_virtual_network.yml", resource, succeeds=False,
            expected_message="field=adminTenant.id expected=77 actual=88",
        )
        self.assertEqual(self.server.created, [])
        self.assertFalse(any(method in ("POST", "PUT", "DELETE") for method, _ in self.server.calls))

    def test_virtual_network_delete_refuses_vpc_owned_by_another_tenant(self):
        resource = self._vn_resource(name="netris-idem-foreign-vpc")
        self.server.reset(
            {
                "vpcs": [
                    {
                        "id": 101,
                        "name": resource["metadata"]["name"],
                        "adminTenant": {"id": 88},
                    }
                ]
            }
        )
        self._run_playbook(
            "playbook_osac_delete_virtual_network.yml",
            resource,
            succeeds=False,
            expected_message="field=adminTenant.id expected=77 actual=88",
        )
        self.assertFalse(any(method == "DELETE" for method, _ in self.server.calls))

    def test_virtual_network_uses_detail_when_list_omits_tenant(self):
        resource = self._vn_resource()
        self.server.reset(
            {
                "vpcs": [{"id": 101, "name": resource["metadata"]["name"]}],
                "vpc_details": {
                    "101": {
                        "id": 101,
                        "name": resource["metadata"]["name"],
                        "adminTenant": {"id": 77},
                    }
                },
                "allocations": [
                    {
                        "id": 201,
                        "name": resource["metadata"]["name"],
                        "prefix": "10.232.0.0/16",
                        "tenant": {"id": 77},
                        "vpc": {"id": 101},
                    }
                ],
            }
        )
        self._run_playbook("playbook_osac_create_virtual_network.yml", resource)
        self.assertIn(("GET", "/api/v2/vpc/101"), self.server.calls)
        self.assertEqual(self.server.created, [])

    def test_virtual_network_prefix_drift_fails_before_any_create(self):
        resource = self._vn_resource()
        state = {
            "vpcs": [{"id": 101, "name": resource["metadata"]["name"], "adminTenant": {"id": 77}}],
            "allocations": [{"id": 201, "name": resource["metadata"]["name"], "prefix": "10.233.0.0/16"}],
        }
        self.server.reset(state)
        self._run_playbook(
            "playbook_osac_create_virtual_network.yml", resource, succeeds=False,
            expected_message="field=prefix expected=10.232.0.0/16 actual=10.233.0.0/16",
        )
        self.assertEqual(self.server.created, [])

    def test_virtual_network_missing_list_immutable_field_fails_closed(self):
        resource = self._vn_resource()
        self.server.reset({"vpcs": [{"id": 101, "name": resource["metadata"]["name"]}]})
        self._run_playbook(
            "playbook_osac_create_virtual_network.yml", resource, succeeds=False,
            expected_message="actual=<missing> (unverifiable)",
        )
        self.assertEqual(self.server.created, [])

    def test_virtual_network_partial_create_recovers_without_duplicate_parent(self):
        resource = self._vn_resource()
        self.server.reset({"responses": {"POST /api/v2/ipam/allocation": (500, {"message": "fixture failure"})}})
        self._run_playbook("playbook_osac_create_virtual_network.yml", resource, succeeds=False)
        self.assertEqual(len(self.server.state["vpcs"]), 1)
        self.assertEqual(self.server.state["allocations"], [])
        self.assertEqual(len(self._resource_posts(self.server, "/api/v2/vpc")), 1)

        self.server.state["responses"].clear()
        self.server.clear_calls()
        self._run_playbook("playbook_osac_create_virtual_network.yml", resource)
        self.assertEqual(len(self.server.state["vpcs"]), 1)
        self.assertEqual(len(self.server.state["allocations"]), 1)
        self.assertEqual(self._resource_posts(self.server, "/api/v2/vpc"), [])
        self.assertEqual(len(self._resource_posts(self.server, "/api/v2/ipam/allocation")), 1)

    def test_virtual_network_create_recovers_when_provider_commits_but_response_fails(self):
        resource = self._vn_resource()
        self.server.reset(
            {
                "after_apply_responses": {
                    "POST /api/v2/vpc": (500, {"message": "response lost after create"})
                }
            }
        )
        self._run_playbook("playbook_osac_create_virtual_network.yml", resource, succeeds=False)
        self.assertEqual(len(self.server.state["vpcs"]), 1)
        self.assertEqual(self.server.state["allocations"], [])

        self.server.state["after_apply_responses"].clear()
        self.server.clear_calls()
        self._run_playbook("playbook_osac_create_virtual_network.yml", resource)
        self.assertEqual(len(self._resource_posts(self.server, "/api/v2/vpc")), 0)
        self.assertEqual(len(self._resource_posts(self.server, "/api/v2/ipam/allocation")), 1)
        self.assertEqual(len(self.server.state["vpcs"]), 1)

    def test_virtual_network_list_api_error_stops_before_post(self):
        resource = self._vn_resource()
        self.server.reset({"responses": {"GET /api/v2/vpc": (503, {"message": "fixture unavailable"})}})
        cookie = "fixture-secret-cookie"
        output = self._run_playbook(
            "playbook_osac_create_virtual_network.yml",
            resource,
            succeeds=False,
            extra_vars={"netris_session_cookie": cookie},
            expected_message="HTTP 503; fixture unavailable",
        )
        self.assertIn("Get existing VPCs", output)
        self.assertNotIn(cookie, output)
        self.assertEqual(self.server.created, [])

    def _subnet_resource(self, cidr="10.231.1.0/24"):
        return {
            "metadata": {
                "name": SUBNET_NAME,
                "namespace": NAMESPACE,
                "annotations": {
                    "osac.openshift.io/tenant": TENANT_ID,
                    "osac.openshift.io/implementation-strategy": "netris",
                },
            },
            "spec": {"virtualNetwork": VNET_UUID, "ipv4Cidr": cidr},
        }

    def test_subnet_create_publishes_fabric_outputs_and_rerun_is_noop(self):
        self.server.reset(self._parent_provider_state())
        resource = self._subnet_resource()
        self._run_playbook("playbook_osac_create_subnet.yml", resource)
        self.assertEqual(len(self._resource_posts(self.server, "/api/v2/ipam/subnet")), 1)
        self.assertEqual(len(self._resource_posts(self.server, "/api/v2/vnet")), 1)
        self.assertEqual(self.server.state["ipam_subnets"][0]["prefix"], "10.231.1.0/24")
        self.assertEqual(self.server.state["vnets"][0]["vpc"]["id"], 101)

        configmap = self._kubectl_json(
            "get", "configmap", f"subnet-{SUBNET_NAME}-fabric-output", "-n", NAMESPACE, "-o", "json"
        )
        self.assertEqual(configmap["data"]["l2_vni"], "3010")
        self.assertEqual(configmap["data"]["l3_vni"], "9101")

        self.server.clear_calls()
        self._run_playbook("playbook_osac_create_subnet.yml", resource)
        self.assertEqual(self._resource_posts(self.server, "/api/v2/ipam/subnet"), [])
        self.assertEqual(self._resource_posts(self.server, "/api/v2/vnet"), [])

    def test_subnet_prefix_drift_fails_before_provider_mutation(self):
        self.server.reset(self._parent_provider_state(ipam_subnets=[
            {"id": 202, "name": SUBNET_NAME, "prefix": "10.231.2.0/24"}
        ]))
        self._run_playbook(
            "playbook_osac_create_subnet.yml", self._subnet_resource(), succeeds=False,
            expected_message="field=prefix expected=10.231.1.0/24 actual=10.231.2.0/24",
        )
        self.assertEqual(self.server.created, [])

    def test_subnet_vpc_drift_fails_without_recreating_vnet(self):
        self.server.reset(self._parent_provider_state(
            ipam_subnets=[{
                "id": 202,
                "name": SUBNET_NAME,
                "prefix": "10.231.1.0/24",
                "purpose": "common",
                "sites": [{"id": 1}],
                "tenant": {"id": 77},
                "vpc": {"id": 101},
            }],
            vnets=[{
                "id": 301,
                "name": SUBNET_NAME,
                "vpcId": 909,
                "tenantID": 77,
                "sites": [{"id": 1}],
            }],
        ))
        self._run_playbook(
            "playbook_osac_create_subnet.yml", self._subnet_resource(), succeeds=False,
            expected_message="field=vpc.id expected=101 actual=909",
        )
        self.assertEqual(self.server.created, [])

    def test_subnet_partial_create_recovers_after_vnet_post_failure(self):
        self.server.reset(self._parent_provider_state(responses={
            "POST /api/v2/vnet": (500, {"message": "fixture failure"})
        }))
        resource = self._subnet_resource()
        self._run_playbook("playbook_osac_create_subnet.yml", resource, succeeds=False)
        self.assertEqual(len(self.server.state["ipam_subnets"]), 1)
        self.assertEqual(self.server.state["vnets"], [])

        self.server.state["responses"].clear()
        self.server.clear_calls()
        self._run_playbook("playbook_osac_create_subnet.yml", resource)
        self.assertEqual(len(self.server.state["ipam_subnets"]), 1)
        self.assertEqual(len(self.server.state["vnets"]), 1)
        self.assertEqual(self._resource_posts(self.server, "/api/v2/ipam/subnet"), [])
        self.assertEqual(len(self._resource_posts(self.server, "/api/v2/vnet")), 1)

    def test_subnet_recovers_when_vnet_create_commits_but_response_fails(self):
        self.server.reset(
            self._parent_provider_state(
                after_apply_responses={
                    "POST /api/v2/vnet": (500, {"message": "response lost after V-Net create"})
                }
            )
        )
        resource = self._subnet_resource()
        self._run_playbook("playbook_osac_create_subnet.yml", resource, succeeds=False)
        self.assertEqual(len(self.server.state["ipam_subnets"]), 1)
        self.assertEqual(len(self.server.state["vnets"]), 1)

        self.server.state["after_apply_responses"].clear()
        self.server.clear_calls()
        self._run_playbook("playbook_osac_create_subnet.yml", resource)
        self.assertEqual(self._resource_posts(self.server, "/api/v2/ipam/subnet"), [])
        self.assertEqual(self._resource_posts(self.server, "/api/v2/vnet"), [])
        self.assertEqual(len(self.server.state["ipam_subnets"]), 1)
        self.assertEqual(len(self.server.state["vnets"]), 1)

    def _nat_resource(self, name="netris-idem-nat", cidr="10.231.0.0/16"):
        return {
            "metadata": {
                "name": name,
                "namespace": NAMESPACE,
                "annotations": {
                    "osac.openshift.io/implementation-strategy": "netris",
                    "osac.openshift.io/externalip-name": EXTERNAL_IP_NAME,
                    "osac.openshift.io/vn-ipv4-cidr": cidr,
                    "osac.openshift.io/virtual-network-name": PARENT_VNET_NAME,
                },
            },
            "spec": {},
        }

    def _set_external_ip_address(self, address):
        result = subprocess.run(
            [
                "kubectl", "annotate", "externalip", EXTERNAL_IP_NAME,
                "-n", NAMESPACE,
                f"osac.openshift.io/allocated-address={address}", "--overwrite",
            ],
            capture_output=True,
            text=True,
            check=False,
            env=os.environ.copy(),
            timeout=30,
        )
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def _external_ip_pool_resource(self, name="netris-idem-pool", cidrs=None):
        return {
            "metadata": {
                "name": name,
                "namespace": NAMESPACE,
                "annotations": {
                    "osac.openshift.io/implementation-strategy": "netris",
                    "osac.openshift.io/tenant": TENANT_ID,
                },
            },
            "spec": {"cidrs": cidrs or ["198.51.100.0/29"]},
        }

    def _external_ip_resource(self, name=EXTERNAL_IP_NAME, pool="netris-idem-pool"):
        return {
            "metadata": {
                "name": name,
                "namespace": NAMESPACE,
                "annotations": {
                    "osac.openshift.io/implementation-strategy": "netris",
                    "osac.openshift.io/externalippool-name": pool,
                },
            },
            "spec": {"pool": pool},
        }

    def test_external_ip_pool_create_and_rerun_are_idempotent(self):
        resource = self._external_ip_pool_resource()
        self._run_playbook("playbook_osac_create_external_ip_pool.yml", resource)
        self.assertEqual(len(self._resource_posts(self.server, "/api/v2/ipam/allocation")), 1)
        self.assertEqual(len(self._resource_posts(self.server, "/api/v2/ipam/subnet")), 1)
        self.assertEqual(self.server.state["allocations"][0]["prefix"], "198.51.100.0/29")
        self.assertEqual(self.server.state["ipam_subnets"][0]["purpose"], "common")

        self.server.clear_calls()
        self._run_playbook("playbook_osac_create_external_ip_pool.yml", resource)
        self.assertEqual(self._resource_posts(self.server, "/api/v2/ipam/allocation"), [])
        self.assertEqual(self._resource_posts(self.server, "/api/v2/ipam/subnet"), [])

    def test_external_ip_pool_recovers_after_committed_common_subnet_response_failure(self):
        resource = self._external_ip_pool_resource()
        self.server.reset(
            {
                "after_apply_responses": {
                    "POST /api/v2/ipam/subnet": (500, {"message": "response lost after subnet create"})
                }
            }
        )
        self._run_playbook("playbook_osac_create_external_ip_pool.yml", resource, succeeds=False)
        self.assertEqual(len(self.server.state["allocations"]), 1)
        self.assertEqual(len(self.server.state["ipam_subnets"]), 1)

    def test_external_ip_pool_recovers_after_allocation_commit_response_failure(self):
        resource = self._external_ip_pool_resource()
        self.server.reset(
            {
                "after_apply_responses": {
                    "POST /api/v2/ipam/allocation": (
                        500,
                        {"message": "response lost after allocation create"},
                    )
                }
            }
        )
        self._run_playbook("playbook_osac_create_external_ip_pool.yml", resource, succeeds=False)
        self.assertEqual(len(self.server.state["allocations"]), 1)
        self.assertEqual(self.server.state["ipam_subnets"], [])

        self.server.state["after_apply_responses"].clear()
        self.server.clear_calls()
        self._run_playbook("playbook_osac_create_external_ip_pool.yml", resource)
        self.assertEqual(self._resource_posts(self.server, "/api/v2/ipam/allocation"), [])
        self.assertEqual(len(self._resource_posts(self.server, "/api/v2/ipam/subnet")), 1)

        self.server.state["after_apply_responses"].clear()
        self.server.clear_calls()
        self._run_playbook("playbook_osac_create_external_ip_pool.yml", resource)
        self.assertEqual(self._resource_posts(self.server, "/api/v2/ipam/allocation"), [])
        self.assertEqual(self._resource_posts(self.server, "/api/v2/ipam/subnet"), [])
        self.assertEqual(len(self.server.state["allocations"]), 1)
        self.assertEqual(len(self.server.state["ipam_subnets"]), 1)

    def test_external_ip_create_and_rerun_reuses_owned_pool_address(self):
        self._run_playbook(
            "playbook_osac_create_external_ip_pool.yml",
            self._external_ip_pool_resource(),
        )
        resource = self._external_ip_resource()
        self._run_playbook("playbook_osac_create_external_ip.yml", resource)
        self.assertEqual(len(self.server.state["ipam_subnets"]), 2)
        eip_subnet = next(
            item for item in self.server.state["ipam_subnets"] if item.get("name") == EXTERNAL_IP_NAME
        )
        self.assertEqual(eip_subnet["prefix"], "198.51.100.1/32")

        self.server.clear_calls()
        self._run_playbook("playbook_osac_create_external_ip.yml", resource)
        self.assertEqual(self._resource_posts(self.server, "/api/v2/ipam/subnet"), [])
        self.assertEqual(len(self.server.state["ipam_subnets"]), 2)
        external_ip = self._kubectl_json(
            "get", "externalip", EXTERNAL_IP_NAME, "-n", NAMESPACE, "-o", "json"
        )
        self.assertEqual(
            external_ip["metadata"]["annotations"]["osac.openshift.io/allocated-address"],
            "198.51.100.1",
        )

    def test_external_ip_create_refuses_existing_address_from_another_pool(self):
        self.server.reset(
            {
                "allocations": [
                    {
                        "id": 201,
                        "name": "netris-idem-pool",
                        "prefix": "198.51.100.0/29",
                        "tenant": {"id": 77},
                    },
                    {
                        "id": 203,
                        "name": "other-pool",
                        "prefix": "192.0.2.0/24",
                        "tenant": {"id": 77},
                    },
                ],
                "ipam_subnets": [
                    {
                        "id": 202,
                        "name": EXTERNAL_IP_NAME,
                        "prefix": "192.0.2.25/32",
                        "purpose": "nat",
                        "tenant": {"id": 77},
                        "sites": [{"id": 1}],
                    }
                ],
            }
        )
        self._run_playbook(
            "playbook_osac_create_external_ip.yml",
            self._external_ip_resource(),
            succeeds=False,
            expected_message="is not inside an allocation for pool 'netris-idem-pool'",
        )
        self.assertFalse(any(method in ("POST", "PUT", "DELETE") for method, _ in self.server.calls))

    def test_external_ip_create_recovers_after_subnet_commit_response_failure(self):
        self._run_playbook(
            "playbook_osac_create_external_ip_pool.yml",
            self._external_ip_pool_resource(),
        )
        self.server.state["after_apply_responses"] = {
            "POST /api/v2/ipam/subnet": (
                500,
                {"message": "response lost after ExternalIP reservation"},
            )
        }
        resource = self._external_ip_resource()
        self._run_playbook("playbook_osac_create_external_ip.yml", resource, succeeds=False)
        self.assertEqual(
            len([item for item in self.server.state["ipam_subnets"] if item.get("name") == EXTERNAL_IP_NAME]),
            1,
        )

        self.server.state["after_apply_responses"].clear()
        self.server.clear_calls()
        self._run_playbook("playbook_osac_create_external_ip.yml", resource)
        self.assertEqual(self._resource_posts(self.server, "/api/v2/ipam/subnet"), [])
        external_ip = self._kubectl_json(
            "get", "externalip", EXTERNAL_IP_NAME, "-n", NAMESPACE, "-o", "json"
        )
        self.assertEqual(
            external_ip["metadata"]["annotations"]["osac.openshift.io/allocated-address"],
            "198.51.100.1",
        )

    def test_external_ip_delete_is_safe_to_repeat(self):
        resource = self._external_ip_resource()
        self.server.reset(
            {
                "ipam_subnets": [
                    {
                        "id": 202,
                        "name": EXTERNAL_IP_NAME,
                        "prefix": "198.51.100.1/32",
                        "purpose": "nat",
                        "tenant": {"id": 77},
                        "sites": [{"id": 1}],
                    }
                ]
            }
        )
        self._run_playbook("playbook_osac_delete_external_ip.yml", resource)
        self.assertEqual(self.server.state["ipam_subnets"], [])
        self.server.clear_calls()
        self._run_playbook("playbook_osac_delete_external_ip.yml", resource)
        self.assertFalse(any(method == "DELETE" for method, _ in self.server.calls))

    def test_external_ip_delete_recovers_after_provider_commits_but_response_fails(self):
        resource = self._external_ip_resource()
        self.server.reset(
            {
                "ipam_subnets": [
                    {
                        "id": 202,
                        "name": EXTERNAL_IP_NAME,
                        "prefix": "198.51.100.1/32",
                        "purpose": "nat",
                        "tenant": {"id": 77},
                        "sites": [{"id": 1}],
                    }
                ],
                "after_apply_responses": {
                    "DELETE /api/v2/ipam/subnet/202": (
                        500,
                        {"message": "response lost after delete"},
                    )
                },
            }
        )
        self._run_playbook("playbook_osac_delete_external_ip.yml", resource, succeeds=False)
        self.assertEqual(self.server.state["ipam_subnets"], [])

        self.server.state["after_apply_responses"].clear()
        self.server.clear_calls()
        self._run_playbook("playbook_osac_delete_external_ip.yml", resource)
        self.assertFalse(any(method == "DELETE" for method, _ in self.server.calls))

    def test_virtual_network_delete_recovers_after_allocation_delete_response_loss(self):
        resource = self._vn_resource(name="netris-idem-delete-vn")
        self.server.reset(
            {
                "vpcs": [
                    {
                        "id": 101,
                        "name": "netris-idem-delete-vn",
                        "adminTenant": {"id": 77},
                    }
                ],
                "allocations": [
                    {
                        "id": 201,
                        "name": "netris-idem-delete-vn",
                        "prefix": "10.232.0.0/16",
                        "tenant": {"id": 77},
                        "vpc": {"id": 101},
                    }
                ],
                "after_apply_responses": {
                    "DELETE /api/v2/ipam/allocation/201": (
                        500,
                        {"message": "response lost after allocation delete"},
                    )
                },
            }
        )
        self._run_playbook("playbook_osac_delete_virtual_network.yml", resource, succeeds=False)
        self.assertEqual(self.server.state["allocations"], [])
        self.assertEqual(len(self.server.state["vpcs"]), 1)

        self.server.state["after_apply_responses"].clear()
        self.server.clear_calls()
        self._run_playbook("playbook_osac_delete_virtual_network.yml", resource)
        self.assertEqual(self.server.state["allocations"], [])
        self.assertEqual(self.server.state["vpcs"], [])

    def test_virtual_network_delete_recovers_after_vpc_delete_response_loss(self):
        resource = self._vn_resource(name="netris-idem-delete-vpc")
        self.server.reset(
            {
                "vpcs": [
                    {
                        "id": 101,
                        "name": "netris-idem-delete-vpc",
                        "adminTenant": {"id": 77},
                    }
                ],
                "allocations": [
                    {
                        "id": 201,
                        "name": "netris-idem-delete-vpc",
                        "prefix": "10.232.0.0/16",
                        "tenant": {"id": 77},
                        "vpc": {"id": 101},
                    }
                ],
                "after_apply_responses": {
                    "DELETE /api/v2/vpc/101": (
                        500,
                        {"message": "response lost after VPC delete"},
                    )
                },
            }
        )
        self._run_playbook("playbook_osac_delete_virtual_network.yml", resource, succeeds=False)
        self.assertEqual(self.server.state["allocations"], [])
        self.assertEqual(self.server.state["vpcs"], [])

        self.server.state["after_apply_responses"].clear()
        self.server.clear_calls()
        self._run_playbook("playbook_osac_delete_virtual_network.yml", resource)
        self.assertEqual(self.server.state["allocations"], [])
        self.assertEqual(self.server.state["vpcs"], [])
        self.assertFalse(any(method == "DELETE" for method, _ in self.server.calls))

    def test_subnet_delete_recovers_after_vnet_delete_response_loss(self):
        resource = self._subnet_resource()
        self.server.reset(
            self._parent_provider_state(
                ipam_subnets=[
                    {
                        "id": 202,
                        "name": SUBNET_NAME,
                        "prefix": "10.231.1.0/24",
                        "purpose": "common",
                        "sites": [{"id": 1}],
                        "tenant": {"id": 77},
                        "vpc": {"id": 101},
                    }
                ],
                vnets=[
                    {
                        "id": 301,
                        "name": SUBNET_NAME,
                        "vpc": {"id": 101},
                        "tenantID": 77,
                        "sites": [{"id": 1}],
                    }
                ],
                after_apply_responses={
                    "DELETE /api/v2/vnet/301": (
                        500,
                        {"message": "response lost after V-Net delete"},
                    )
                },
            )
        )
        self._run_playbook("playbook_osac_delete_subnet.yml", resource, succeeds=False)
        self.assertEqual(self.server.state["vnets"], [])
        self.assertEqual(len(self.server.state["ipam_subnets"]), 1)

        self.server.state["after_apply_responses"].clear()
        self.server.clear_calls()
        self._run_playbook("playbook_osac_delete_subnet.yml", resource)
        self.assertEqual(self.server.state["vnets"], [])
        self.assertEqual(self.server.state["ipam_subnets"], [])

    def test_subnet_delete_recovers_after_ipam_subnet_response_loss(self):
        resource = self._subnet_resource()
        self.server.reset(
            self._parent_provider_state(
                ipam_subnets=[
                    {
                        "id": 202,
                        "name": SUBNET_NAME,
                        "prefix": "10.231.1.0/24",
                        "purpose": "common",
                        "sites": [{"id": 1}],
                        "tenant": {"id": 77},
                        "vpc": {"id": 101},
                    }
                ],
                vnets=[
                    {
                        "id": 301,
                        "name": SUBNET_NAME,
                        "vpc": {"id": 101},
                        "tenantID": 77,
                        "sites": [{"id": 1}],
                    }
                ],
                after_apply_responses={
                    "DELETE /api/v2/ipam/subnet/202": (
                        500,
                        {"message": "response lost after IPAM subnet delete"},
                    )
                },
            )
        )
        self._run_playbook("playbook_osac_delete_subnet.yml", resource, succeeds=False)
        self.assertEqual(self.server.state["vnets"], [])
        self.assertEqual(self.server.state["ipam_subnets"], [])

        self.server.state["after_apply_responses"].clear()
        self.server.clear_calls()
        self._run_playbook("playbook_osac_delete_subnet.yml", resource)
        self.assertFalse(any(method == "DELETE" for method, _ in self.server.calls))

    def test_external_ip_pool_delete_recovers_after_common_subnet_delete_response_loss(self):
        resource = self._external_ip_pool_resource()
        self.server.reset(
            {
                "allocations": [
                    {
                        "id": 201,
                        "name": "netris-idem-pool",
                        "prefix": "198.51.100.0/29",
                        "tenant": {"id": 77},
                    }
                ],
                "ipam_subnets": [
                    {
                        "id": 202,
                        "name": "netris-idem-pool-common",
                        "prefix": "198.51.100.0/29",
                        "purpose": "common",
                        "tenant": {"id": 77},
                        "sites": [{"id": 1}],
                    }
                ],
                "after_apply_responses": {
                    "DELETE /api/v2/ipam/subnet/202": (
                        500,
                        {"message": "response lost after common subnet delete"},
                    )
                },
            }
        )
        self._run_playbook("playbook_osac_delete_external_ip_pool.yml", resource, succeeds=False)
        self.assertEqual(self.server.state["ipam_subnets"], [])
        self.assertEqual(len(self.server.state["allocations"]), 1)

        self.server.state["after_apply_responses"].clear()
        self.server.clear_calls()
        self._run_playbook("playbook_osac_delete_external_ip_pool.yml", resource)
        self.assertEqual(self.server.state["ipam_subnets"], [])
        self.assertEqual(self.server.state["allocations"], [])

    def test_external_ip_pool_delete_recovers_after_allocation_response_loss(self):
        resource = self._external_ip_pool_resource()
        self.server.reset(
            {
                "allocations": [
                    {
                        "id": 201,
                        "name": "netris-idem-pool",
                        "prefix": "198.51.100.0/29",
                        "tenant": {"id": 77},
                    }
                ],
                "ipam_subnets": [
                    {
                        "id": 202,
                        "name": "netris-idem-pool-common",
                        "prefix": "198.51.100.0/29",
                        "purpose": "common",
                        "tenant": {"id": 77},
                        "sites": [{"id": 1}],
                    }
                ],
                "after_apply_responses": {
                    "DELETE /api/v2/ipam/allocation/201": (
                        500,
                        {"message": "response lost after pool allocation delete"},
                    )
                },
            }
        )
        self._run_playbook("playbook_osac_delete_external_ip_pool.yml", resource, succeeds=False)
        self.assertEqual(self.server.state["ipam_subnets"], [])
        self.assertEqual(self.server.state["allocations"], [])

        self.server.state["after_apply_responses"].clear()
        self.server.clear_calls()
        self._run_playbook("playbook_osac_delete_external_ip_pool.yml", resource)
        self.assertFalse(any(method == "DELETE" for method, _ in self.server.calls))

    def test_external_ip_pool_delete_refuses_allocation_owned_by_another_tenant(self):
        resource = self._external_ip_pool_resource()
        self.server.reset(
            {
                "allocations": [
                    {
                        "id": 201,
                        "name": "netris-idem-pool",
                        "prefix": "198.51.100.0/29",
                        "tenant": {"id": 88},
                    }
                ]
            }
        )
        self._run_playbook(
            "playbook_osac_delete_external_ip_pool.yml",
            resource,
            succeeds=False,
            expected_message="field=tenant.id expected=77 actual=88",
        )
        self.assertFalse(any(method == "DELETE" for method, _ in self.server.calls))

    def test_snat_create_rerun_and_scalar_vpc_id_summary(self):
        self._set_external_ip_address("198.51.100.25")
        nat_name = "netris-idem-nat-snat"
        existing = {
            "id": 401,
            "name": nat_name,
            "action": "snat",
            "vpc": 101,
            "site": {"id": 1},
            "protocol": "all",
            "state": "enabled",
            "sourceAddress": "10.231.0.0/16",
            "sourcePort": "1-65535",
            "destinationAddress": "0.0.0.0/0",
            "destinationPort": "1-65535",
            "snatToIP": "198.51.100.25/32",
        }
        self.server.reset(self._parent_provider_state(nat_rules=[existing]))
        resource = self._nat_resource(name="netris-idem-nat")
        self._run_playbook("playbook_osac_create_nat_gateway.yml", resource)
        self.assertEqual(self.server.created, [])

    def test_snat_replay_accepts_bare_provider_ip(self):
        self._set_external_ip_address("198.51.100.25")
        existing = {
            "id": 401,
            "name": "netris-idem-nat-snat",
            "action": "SNAT",
            "vpc": {"id": 101},
            "site": {"id": 1},
            "protocol": "all",
            "state": "enabled",
            "sourceAddress": "10.231.0.0/16",
            "sourcePort": "1-65535",
            "destinationAddress": "0.0.0.0/0",
            "destinationPort": "1-65535",
            "snatToIP": "198.51.100.25",
        }
        self.server.reset(self._parent_provider_state(nat_rules=[existing]))

        self._run_playbook(
            "playbook_osac_create_nat_gateway.yml", self._nat_resource()
        )

        self.assertEqual(self._resource_posts(self.server, "/api/v2/nat"), [])

    def test_nat_create_uses_detail_when_list_omits_bindings(self):
        self._set_external_ip_address("198.51.100.25")
        resource = self._nat_resource()
        nat = {
            "id": 401,
            "name": "netris-idem-nat-snat",
            "action": "SNAT",
            "vpc": {"id": 101},
            "site": {"id": 1},
            "protocol": "all",
            "state": "enabled",
            "sourceAddress": "10.231.0.0/16",
            "sourcePort": "1-65535",
            "destinationAddress": "0.0.0.0/0",
            "destinationPort": "1-65535",
            "snatToIP": "198.51.100.25/32",
        }
        self.server.reset(
            self._parent_provider_state(
                nat_rules=[{"id": 401, "name": "netris-idem-nat-snat"}],
                nat_details={"401": nat},
            )
        )
        self._run_playbook("playbook_osac_create_nat_gateway.yml", resource)
        self.assertIn(("GET", "/api/v2/nat/401"), self.server.calls)
        self.assertEqual(self.server.created, [])

        self.server.reset(self._parent_provider_state())
        self._run_playbook("playbook_osac_create_nat_gateway.yml", resource)
        self.assertEqual(len(self._resource_posts(self.server, "/api/v2/nat")), 1)
        created = self._resource_posts(self.server, "/api/v2/nat")[0]
        self.assertEqual(created["action"], "SNAT")
        self.assertEqual(created["sourceAddress"], "10.231.0.0/16")
        self.assertEqual(created["snatToIP"], "198.51.100.25/32")
        self.server.clear_calls()
        self._run_playbook("playbook_osac_create_nat_gateway.yml", resource)
        self.assertEqual(self._resource_posts(self.server, "/api/v2/nat"), [])

    def test_snat_immutable_drift_fails_without_post(self):
        self._set_external_ip_address("198.51.100.25")
        resource = self._nat_resource()
        for field, value, message in [
            ("snatToIP", "198.51.100.26/32", "field=snatToIP"),
            ("snatToIP", "198.51.100.25/24", "field=snatToIP"),
            ("sourceAddress", "10.240.0.0/16", "field=sourceAddress"),
            ("vpc", {"id": 909}, "field=vpc.id"),
        ]:
            with self.subTest(field=field):
                nat = {
                    "id": 401, "name": "netris-idem-nat-snat", "action": "SNAT",
                    "vpc": {"id": 101}, "sourceAddress": "10.231.0.0/16",
                    "snatToIP": "198.51.100.25/32",
                }
                nat[field] = value
                self.server.reset(self._parent_provider_state(nat_rules=[nat]))
                self._run_playbook(
                    "playbook_osac_create_nat_gateway.yml", resource, succeeds=False,
                    expected_message=message,
                )
                self.assertEqual(self.server.created, [])

    def test_snat_create_recovers_when_provider_commits_but_response_fails(self):
        self._set_external_ip_address("198.51.100.25")
        resource = self._nat_resource()
        self.server.reset(
            self._parent_provider_state(
                after_apply_responses={
                    "POST /api/v2/nat": (500, {"message": "response lost after NAT create"})
                }
            )
        )
        self._run_playbook("playbook_osac_create_nat_gateway.yml", resource, succeeds=False)
        self.assertEqual(len(self.server.state["nat_rules"]), 1)

        self.server.state["after_apply_responses"].clear()
        self.server.clear_calls()
        self._run_playbook("playbook_osac_create_nat_gateway.yml", resource)
        self.assertEqual(self._resource_posts(self.server, "/api/v2/nat"), [])
        self.assertEqual(len(self.server.state["nat_rules"]), 1)

    def test_dnat_create_recovers_when_provider_commits_but_response_fails(self):
        self._set_external_ip_address("198.51.100.25")
        resource = self._dnat_resource()
        self.server.reset(
            self._parent_provider_state(
                after_apply_responses={
                    "POST /api/v2/nat": (
                        500,
                        {"message": "response lost after DNAT create"},
                    )
                }
            )
        )
        self._run_playbook("playbook_osac_attach_external_ip.yml", resource, succeeds=False)
        self.assertEqual(len(self.server.state["nat_rules"]), 1)

        self.server.state["after_apply_responses"].clear()
        self.server.clear_calls()
        self._run_playbook("playbook_osac_attach_external_ip.yml", resource)
        self.assertEqual(self._resource_posts(self.server, "/api/v2/nat"), [])
        self.assertEqual(len(self.server.state["nat_rules"]), 1)

    def test_nat_gateway_delete_is_owned_and_safe_to_repeat(self):
        self._set_external_ip_address("198.51.100.25")
        resource = self._nat_resource()
        existing = {
            "id": 401,
            "name": "netris-idem-nat-snat",
            "action": "SNAT",
            "vpc": {"id": 101},
            "site": {"id": 1},
            "protocol": "all",
            "state": "enabled",
            "sourceAddress": "10.231.0.0/16",
            "sourcePort": "1-65535",
            "destinationAddress": "0.0.0.0/0",
            "destinationPort": "1-65535",
            "snatToIP": "198.51.100.25/32",
        }
        self.server.reset(self._parent_provider_state(nat_rules=[existing]))
        self._run_playbook("playbook_osac_delete_nat_gateway.yml", resource)
        self.assertEqual(self.server.state["nat_rules"], [])

        self.server.clear_calls()
        self._run_playbook("playbook_osac_delete_nat_gateway.yml", resource)
        self.assertFalse(any(method == "DELETE" for method, _ in self.server.calls))

    def test_nat_gateway_delete_recovers_after_provider_commit_response_failure(self):
        self._set_external_ip_address("198.51.100.25")
        resource = self._nat_resource()
        existing = {
            "id": 401,
            "name": "netris-idem-nat-snat",
            "action": "SNAT",
            "vpc": {"id": 101},
            "site": {"id": 1},
            "protocol": "all",
            "state": "enabled",
            "sourceAddress": "10.231.0.0/16",
            "sourcePort": "1-65535",
            "destinationAddress": "0.0.0.0/0",
            "destinationPort": "1-65535",
            "snatToIP": "198.51.100.25/32",
        }
        self.server.reset(
            self._parent_provider_state(
                nat_rules=[existing],
                after_apply_responses={
                    "DELETE /api/v2/nat/401": (
                        500,
                        {"message": "response lost after SNAT delete"},
                    )
                },
            )
        )
        self._run_playbook("playbook_osac_delete_nat_gateway.yml", resource, succeeds=False)
        self.assertEqual(self.server.state["nat_rules"], [])

        self.server.state["after_apply_responses"].clear()
        self.server.clear_calls()
        self._run_playbook("playbook_osac_delete_nat_gateway.yml", resource)
        self.assertFalse(any(method == "DELETE" for method, _ in self.server.calls))

    def _dnat_resource(self, target_ip="10.231.1.10"):
        return {
            "metadata": {
                "name": "netris-idem-attachment",
                "namespace": NAMESPACE,
                "annotations": {
                    "osac.openshift.io/implementation-strategy": "netris",
                    "osac.openshift.io/externalip-name": EXTERNAL_IP_NAME,
                    "osac.openshift.io/target-ip": target_ip,
                    "osac.openshift.io/virtual-network-name": PARENT_VNET_NAME,
                },
            },
            "spec": {},
        }

    def test_dnat_create_rerun_and_target_drift(self):
        self._set_external_ip_address("198.51.100.25")
        resource = self._dnat_resource()
        self.server.reset(self._parent_provider_state())
        self._run_playbook("playbook_osac_attach_external_ip.yml", resource)
        self.assertEqual(len(self._resource_posts(self.server, "/api/v2/nat")), 1)
        dnat = self._resource_posts(self.server, "/api/v2/nat")[0]
        self.assertEqual(dnat["action"], "DNAT")
        self.assertEqual(dnat["destinationAddress"], "198.51.100.25/32")
        self.assertEqual(dnat["dnatToIP"], "10.231.1.10/32")

        self.server.clear_calls()
        self._run_playbook("playbook_osac_attach_external_ip.yml", resource)
        self.assertEqual(self._resource_posts(self.server, "/api/v2/nat"), [])

        mismatch = {**dnat, "id": 401, "dnatToIP": "10.231.1.11/32"}
        self.server.reset(self._parent_provider_state(nat_rules=[mismatch]))
        self._run_playbook(
            "playbook_osac_attach_external_ip.yml", resource, succeeds=False,
            expected_message="field=dnatToIP",
        )
        self.assertEqual(self.server.created, [])

    def test_dnat_replay_accepts_provider_host_and_empty_port_defaults(self):
        self._set_external_ip_address("198.51.100.25")
        resource = self._dnat_resource()
        self.server.reset(self._parent_provider_state())
        self._run_playbook("playbook_osac_attach_external_ip.yml", resource)
        created = self._resource_posts(self.server, "/api/v2/nat")[0]
        existing = {
            **created,
            "id": 401,
            "destinationAddress": "198.51.100.25",
            "dnatToPort": 0,
        }
        self.server.reset(self._parent_provider_state(nat_rules=[existing]))

        self._run_playbook("playbook_osac_attach_external_ip.yml", resource)

        self.assertEqual(self._resource_posts(self.server, "/api/v2/nat"), [])

    def test_dnat_detach_recovers_after_provider_commit_response_failure(self):
        self._set_external_ip_address("198.51.100.25")
        resource = self._dnat_resource()
        existing = {
            "id": 401,
            "name": "netris-idem-attachment-dnat",
            "action": "DNAT",
            "vpc": {"id": 101},
            "site": {"id": 1},
            "dnatToIP": "10.231.1.10/32",
            "destinationAddress": "198.51.100.25/32",
        }
        self.server.reset(
            self._parent_provider_state(
                nat_rules=[existing],
                after_apply_responses={
                    "DELETE /api/v2/nat/401": (
                        500,
                        {"message": "response lost after DNAT delete"},
                    )
                },
            )
        )
        self._run_playbook("playbook_osac_detach_external_ip.yml", resource, succeeds=False)
        self.assertEqual(self.server.state["nat_rules"], [])

        self.server.state["after_apply_responses"].clear()
        self.server.clear_calls()
        self._run_playbook("playbook_osac_detach_external_ip.yml", resource)
        self.assertFalse(any(method == "DELETE" for method, _ in self.server.calls))

    def test_dnat_delete_requires_ownership_annotations(self):
        resource = self._dnat_resource()
        del resource["metadata"]["annotations"]["osac.openshift.io/target-ip"]
        self.server.reset(self._parent_provider_state())
        self._run_playbook(
            "playbook_osac_detach_external_ip.yml",
            resource,
            succeeds=False,
            expected_message="required ownership annotations",
        )
        self.assertEqual(self.server.calls, [])

    def test_nat_gateway_delete_requires_ownership_annotations(self):
        resource = self._nat_resource()
        del resource["metadata"]["annotations"]["osac.openshift.io/vn-ipv4-cidr"]
        self.server.reset(self._parent_provider_state())
        self._run_playbook(
            "playbook_osac_delete_nat_gateway.yml",
            resource,
            succeeds=False,
            expected_message="required ownership annotations",
        )
        self.assertEqual(self.server.calls, [])

    def _default_resource(self, kind, name):
        return {
            "metadata": {
                "name": name,
                "namespace": NAMESPACE,
                "labels": {"osac.openshift.io/default": "true"},
                "annotations": {"osac.openshift.io/implementation-strategy": "netris"},
            },
            "spec": {},
        }

    def test_default_delete_guards_prevent_all_provider_calls(self):
        cases = [
            ("VirtualNetwork", "playbook_osac_delete_virtual_network.yml"),
            ("Subnet", "playbook_osac_delete_subnet.yml"),
            ("SecurityGroup", "playbook_osac_delete_security_group.yml"),
            ("NATGateway", "playbook_osac_delete_nat_gateway.yml"),
        ]
        for kind, playbook in cases:
            with self.subTest(kind=kind):
                self.server.reset()
                resource = self._default_resource(kind, f"default-{kind.lower()}")
                self._run_playbook(
                    playbook, resource, succeeds=False,
                    expected_message=f"Refusing to delete default {kind}",
                )
                self.assertEqual(self.server.calls, [])
                self.assertEqual(self.server.created, [])

    def test_job_variable_cannot_override_non_security_group_default_guards(self):
        cases = [
            ("VirtualNetwork", "playbook_osac_delete_virtual_network.yml"),
            ("Subnet", "playbook_osac_delete_subnet.yml"),
            ("NATGateway", "playbook_osac_delete_nat_gateway.yml"),
        ]
        for kind, playbook in cases:
            with self.subTest(kind=kind):
                self.server.reset()
                self._run_playbook(
                    playbook,
                    self._default_resource(kind, f"default-{kind.lower()}"),
                    succeeds=False,
                    extra_vars={"osac_allow_default_resource_delete": True},
                    expected_message=f"Refusing to delete default {kind}",
                )
                self.assertEqual(self.server.calls, [])
                self.assertEqual(self.server.created, [])

    def _move_resource(self, offboard=False, interface="eth0"):
        metadata = {
            "name": "netris-idem-move-request",
            "namespace": NAMESPACE,
            "annotations": {
                "osac.openshift.io/tenant": TENANT_ID,
                "osac.openshift.io/implementation-strategy": "netris",
            },
        }
        if offboard:
            metadata["deletionTimestamp"] = "2026-01-01T00:00:00Z"
        return {
            "metadata": metadata,
            "spec": {
                "externalHostName": "netris-idem-server",
                "networkAttachments": [{"interface": interface, "subnetRef": SUBNET_UUID}],
            },
        }

    def _move_state(self, source_ports=None, target_ports=None, **overrides):
        state = empty_state()
        state["vnets"] = [
            {"id": 1, "name": "provisioning", "vpc": {"id": 101}},
            {"id": 2, "name": SUBNET_NAME, "vpc": {"id": 101}},
        ]
        state["vnet_details"] = {
            "1": {"id": 1, "name": "provisioning", "vpc": {"id": 101}, "ports": copy.deepcopy(source_ports or []), "state": "active"},
            "2": {"id": 2, "name": SUBNET_NAME, "vpc": {"id": 101}, "ports": copy.deepcopy(target_ports or []), "state": "active"},
        }
        state.update(overrides)
        return state

    def test_move_attach_is_idempotent_and_postcondition_checked(self):
        resource = self._move_resource()
        no_provisioning_vnet = {"netris_bm_provisioning_vnet": ""}
        self.server.reset(self._move_state(target_ports=[{"id": 44, "state": "active"}]))
        self._run_playbook(
            "playbook_osac_move_network_attachment.yml", resource,
            extra_vars=no_provisioning_vnet,
        )
        puts = [call for call in self.server.calls if call[0] == "PUT"]
        self.assertEqual(len(puts), 1)
        self.assertEqual(self.server.state["vnet_details"]["2"]["ports"], [
            {"id": 44, "state": "active"},
            {"id": 55, "state": "active", "accessMode": True},
        ])

        self.server.clear_calls()
        self._run_playbook(
            "playbook_osac_move_network_attachment.yml", resource,
            extra_vars=no_provisioning_vnet,
        )
        self.assertFalse(any(method == "PUT" for method, _ in self.server.calls))

    def test_move_detach_normalizes_string_port_id_and_preserves_other_ports(self):
        resource = self._move_resource(offboard=True)
        resource["metadata"]["deletionTimestamp"] = "2026-01-01T00:00:00Z"
        self.server.reset(self._move_state(target_ports=[
            {"id": "55", "state": "active"}, {"id": 66, "state": "active"}
        ]))
        only_detach = {"netris_bm_provisioning_vnet": ""}
        self._run_playbook("playbook_osac_move_network_attachment.yml", resource, extra_vars=only_detach)
        self.assertEqual(self.server.state["vnet_details"]["2"]["ports"], [{"id": 66, "state": "active"}])

        self.server.clear_calls()
        self._run_playbook("playbook_osac_move_network_attachment.yml", resource, extra_vars=only_detach)
        self.assertFalse(any(method == "PUT" for method, _ in self.server.calls))

    def test_move_retry_after_source_detach_completes_target_attach(self):
        # This is the observable state left by an interruption after detach:
        # the source has no port and the target has not received it yet.
        self.server.reset(self._move_state(source_ports=[{"id": 66, "state": "active"}], target_ports=[]))
        self._run_playbook("playbook_osac_move_network_attachment.yml", self._move_resource())
        self.assertFalse(any(port.get("id") in (55, "55") for port in self.server.state["vnet_details"]["1"]["ports"]))
        self.assertTrue(any(str(port.get("id")) == "55" for port in self.server.state["vnet_details"]["2"]["ports"]))

    def test_move_retry_recovers_after_detach_commits_but_response_fails(self):
        self.server.reset(
            self._move_state(
                source_ports=[{"id": 55, "state": "active"}],
                target_ports=[],
                after_apply_responses={
                    "PUT /api/v2/vnet/1": (500, {"message": "response lost after detach"})
                },
            )
        )
        resource = self._move_resource()
        self._run_playbook("playbook_osac_move_network_attachment.yml", resource, succeeds=False)
        self.assertFalse(
            any(
                str(port.get("id")) == "55"
                for port in self.server.state["vnet_details"]["1"]["ports"]
            )
        )
        self.server.state["after_apply_responses"].clear()
        self.server.clear_calls()
        self._run_playbook("playbook_osac_move_network_attachment.yml", resource)
        self.assertTrue(
            any(
                str(port.get("id")) == "55"
                for port in self.server.state["vnet_details"]["2"]["ports"]
            )
        )

    def test_move_retry_recovers_after_target_attach_commits_but_response_fails(self):
        self.server.reset(
            self._move_state(
                source_ports=[{"id": 55, "state": "active"}],
                target_ports=[],
                after_apply_responses={
                    "PUT /api/v2/vnet/2": (
                        500,
                        {"message": "response lost after target attach"},
                    )
                },
            )
        )
        resource = self._move_resource()
        self._run_playbook("playbook_osac_move_network_attachment.yml", resource, succeeds=False)
        self.assertFalse(
            any(str(port.get("id")) == "55" for port in self.server.state["vnet_details"]["1"]["ports"])
        )
        self.assertTrue(
            any(str(port.get("id")) == "55" for port in self.server.state["vnet_details"]["2"]["ports"])
        )

        self.server.state["after_apply_responses"].clear()
        self.server.clear_calls()
        self._run_playbook("playbook_osac_move_network_attachment.yml", resource)
        self.assertEqual(
            len(
                [
                    port
                    for port in self.server.state["vnet_details"]["2"]["ports"]
                    if str(port.get("id")) == "55"
                ]
            ),
            1,
        )
        self.assertFalse(any(method == "PUT" for method, _ in self.server.calls))
        self.assertFalse(
            any(
                str(port.get("id")) == "55"
                for port in self.server.state["vnet_details"]["1"]["ports"]
            )
        )

    def test_move_unresolved_interface_fails_before_vnet_mutation(self):
        self.server.reset(self._move_state(ports=[]))
        self._run_playbook(
            "playbook_osac_move_network_attachment.yml", self._move_resource(interface="missing0"),
            succeeds=False, expected_message="Port 'missing0' not found",
        )
        self.assertFalse(any(method in ("PUT", "POST", "DELETE") for method, _ in self.server.calls))

    def test_move_fails_if_provider_does_not_apply_attach_postcondition(self):
        self.server.reset(self._move_state(ignore_vnet_put=True))
        self._run_playbook(
            "playbook_osac_move_network_attachment.yml", self._move_resource(),
            succeeds=False, expected_message="Post-condition failed: port 'eth0'",
        )
        self.assertTrue(any(method == "PUT" for method, _ in self.server.calls))
        self.assertFalse(any(str(port.get("id")) == "55" for port in self.server.state["vnet_details"]["2"]["ports"]))

    def _kubectl_json(self, *args):
        result = subprocess.run(
            ["kubectl", *args], capture_output=True, text=True, check=False,
            env=os.environ.copy(), timeout=30,
        )
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        return json.loads(result.stdout)


if __name__ == "__main__":
    unittest.main(verbosity=2)
