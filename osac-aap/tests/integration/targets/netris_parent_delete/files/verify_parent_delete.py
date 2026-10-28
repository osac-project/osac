"""Exercise top-level Netris parent delete playbooks against a local HTTP fixture."""

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


OSAC_AAP = Path(__file__).resolve().parents[5]
VIRTUAL_NETWORK_NAME = "netris-parent-delete"
EXTERNAL_IP_POOL_NAME = "netris-pool-delete"


class NetrisServer(ThreadingHTTPServer):
    """Hold one scenario and its provider request log."""

    daemon_threads = True

    def __init__(self):
        """Bind the test provider to an ephemeral loopback port."""
        super().__init__(("127.0.0.1", 0), NetrisHandler)
        self.scenario = {}
        self.calls = []


class NetrisHandler(BaseHTTPRequestHandler):
    """Serve only the Netris endpoints used by the parent delete playbooks."""

    def _respond(self, status, data=None):
        """Send one JSON response without echoing request data."""
        body = json.dumps(data if data is not None else {}).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _record(self, method):
        """Record the method and URL needed for ordering assertions."""
        self.server.calls.append((method, self.path))

    def do_GET(self):
        """Return configured resource lists and record request order."""
        self._record("GET")
        if self.path == "/api/v2/vpc":
            self._respond(200, {"data": self.server.scenario.get("vpcs", [])})
        elif self.path.startswith("/api/v2/ipam?filterByVpc=") or self.path == "/api/v2/ipam":
            self._respond(200, {"data": self.server.scenario.get("allocations", [])})
        elif self.path == "/api/v2/ipam/subnets":
            self._respond(200, {"data": self.server.scenario.get("subnets", [])})
        else:
            self._respond(404, {"message": "unexpected GET"})

    def do_DELETE(self):
        """Return a scenario-specific delete result and record request order."""
        self._record("DELETE")
        if self.path.startswith("/api/v2/vpc/"):
            status = self.server.scenario.get("vpc_status", 204)
        elif self.path.startswith("/api/v2/ipam/allocation/"):
            status = self.server.scenario.get("allocation_status", 204)
        elif self.path.startswith("/api/v2/ipam/subnet/"):
            status = self.server.scenario.get("subnet_status", 204)
        else:
            status = 404
        self._respond(status, {"message": "provider delete result"})

    def log_message(self, format, *args):
        """Keep request headers and test-only credentials out of logs."""


class ParentDeletePlaybookTests(unittest.TestCase):
    """Check observable playbook results and provider request order."""

    @classmethod
    def setUpClass(cls):
        """Start one local Netris fixture for the playbook cases."""
        cls.server = NetrisServer()
        cls.thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()

    @classmethod
    def tearDownClass(cls):
        """Stop the local Netris fixture after all cases."""
        cls.server.shutdown()
        cls.server.server_close()
        cls.thread.join(timeout=5)

    def _run_playbook(self, playbook, resource, scenario, expected_calls, succeeds):
        """Run a public deletion playbook and check its outcome and call order."""
        self.server.scenario = scenario
        self.server.calls = []
        job_vars = {
            "osac_job_vars": {"resource": resource},
            "netris_controller_url": f"http://127.0.0.1:{self.server.server_port}",
            "netris_session_cookie": secrets.token_hex(16),
            "netris_validate_certs": False,
            "netris_tenant_id": 77,
            "netris_site_id": 1,
            "ansible_connection": "local",
            "ansible_become": False,
            "ansible_python_interpreter": sys.executable,
        }
        with tempfile.NamedTemporaryFile(mode="w", suffix=".json", delete=False) as vars_file:
            json.dump(job_vars, vars_file)
            vars_path = Path(vars_file.name)
        playbook_env = os.environ.copy()
        playbook_env.update(
            {
                "NETRIS_CONTROLLER_URL": job_vars["netris_controller_url"],
                "NETRIS_VALIDATE_CERTS": "false",
                "NETRIS_CA_PATH": "",
            }
        )
        try:
            result = subprocess.run(
                [
                    "uv", "run", "ansible-playbook", "-i", "localhost,",
                    str(OSAC_AAP / playbook), "-e", f"@{vars_path}",
                ],
                cwd=OSAC_AAP,
                env=playbook_env,
                capture_output=True,
                text=True,
                timeout=120,
                check=False,
            )
        finally:
            vars_path.unlink(missing_ok=True)
        self.assertEqual(result.returncode == 0, succeeds, result.stdout + result.stderr)
        self.assertEqual(self.server.calls, expected_calls)

    def _virtual_network(self):
        """Build the tenant-scoped VirtualNetwork job input."""
        return {
            "metadata": {
                "name": VIRTUAL_NETWORK_NAME,
                "namespace": "integration-tenant",
                "annotations": {
                    "osac.openshift.io/tenant": "integration-tenant",
                    "osac.openshift.io/implementation-strategy": "netris",
                },
            },
        }

    def _external_ip_pool(self):
        """Build the tenant-scoped ExternalIPPool job input."""
        return {
            "metadata": {
                "name": EXTERNAL_IP_POOL_NAME,
                "namespace": "integration-tenant",
                "annotations": {
                    "osac.openshift.io/tenant": "integration-tenant",
                    "osac.openshift.io/implementation-strategy": "netris",
                },
            },
            "spec": {"cidrs": ["192.0.2.0/24"]},
        }

    def _virtual_network_calls(self, allocation_delete=True, vpc_delete=True):
        """List the expected VirtualNetwork provider calls in dependency order."""
        calls = [("GET", "/api/v2/vpc"), ("GET", "/api/v2/ipam?filterByVpc=101")]
        if allocation_delete:
            calls.append(("DELETE", "/api/v2/ipam/allocation/202"))
        calls.append(("GET", "/api/v2/vpc"))
        if vpc_delete:
            calls.append(("DELETE", "/api/v2/vpc/101"))
        return calls

    def test_virtual_network_allocation_404_continues_to_vpc(self):
        """A removed allocation does not prevent the parent VPC deletion."""
        scenario = {
            "vpcs": [{"id": 101, "name": VIRTUAL_NETWORK_NAME, "adminTenant": {"id": 77}}],
            "allocations": [{"id": 202, "name": VIRTUAL_NETWORK_NAME, "tenant": {"id": 77}}],
            "allocation_status": 404,
        }
        self._run_playbook(
            "playbook_osac_delete_virtual_network.yml",
            self._virtual_network(),
            scenario,
            self._virtual_network_calls(),
            True,
        )

    def test_virtual_network_vpc_404_succeeds_after_allocation(self):
        """A removed VPC completes cleanup after allocation deletion."""
        scenario = {
            "vpcs": [{"id": 101, "name": VIRTUAL_NETWORK_NAME, "adminTenant": {"id": 77}}],
            "allocations": [{"id": 202, "name": VIRTUAL_NETWORK_NAME, "tenant": {"id": 77}}],
            "vpc_status": 404,
        }
        self._run_playbook(
            "playbook_osac_delete_virtual_network.yml",
            self._virtual_network(),
            scenario,
            self._virtual_network_calls(),
            True,
        )

    def test_virtual_network_allocation_400_stops_before_vpc(self):
        """An allocation dependency error keeps the parent VPC intact."""
        scenario = {
            "vpcs": [{"id": 101, "name": VIRTUAL_NETWORK_NAME, "adminTenant": {"id": 77}}],
            "allocations": [{"id": 202, "name": VIRTUAL_NETWORK_NAME, "tenant": {"id": 77}}],
            "allocation_status": 400,
        }
        self._run_playbook(
            "playbook_osac_delete_virtual_network.yml",
            self._virtual_network(),
            scenario,
            self._virtual_network_calls()[:3],
            False,
        )

    def test_virtual_network_vpc_400_fails(self):
        """A VPC dependency error fails the playbook."""
        scenario = {
            "vpcs": [{"id": 101, "name": VIRTUAL_NETWORK_NAME, "adminTenant": {"id": 77}}],
            "allocations": [{"id": 202, "name": VIRTUAL_NETWORK_NAME, "tenant": {"id": 77}}],
            "vpc_status": 400,
        }
        self._run_playbook(
            "playbook_osac_delete_virtual_network.yml",
            self._virtual_network(),
            scenario,
            self._virtual_network_calls(),
            False,
        )

    def test_virtual_network_allocation_500_stops_before_vpc(self):
        """An allocation provider failure keeps the parent VPC intact."""
        scenario = {
            "vpcs": [{"id": 101, "name": VIRTUAL_NETWORK_NAME, "adminTenant": {"id": 77}}],
            "allocations": [{"id": 202, "name": VIRTUAL_NETWORK_NAME, "tenant": {"id": 77}}],
            "allocation_status": 500,
        }
        self._run_playbook(
            "playbook_osac_delete_virtual_network.yml",
            self._virtual_network(),
            scenario,
            self._virtual_network_calls()[:3],
            False,
        )

    def test_virtual_network_vpc_500_fails(self):
        """A VPC provider failure fails the playbook."""
        scenario = {
            "vpcs": [{"id": 101, "name": VIRTUAL_NETWORK_NAME, "adminTenant": {"id": 77}}],
            "allocations": [{"id": 202, "name": VIRTUAL_NETWORK_NAME, "tenant": {"id": 77}}],
            "vpc_status": 500,
        }
        self._run_playbook(
            "playbook_osac_delete_virtual_network.yml",
            self._virtual_network(),
            scenario,
            self._virtual_network_calls(),
            False,
        )

    def test_virtual_network_absent_needs_no_delete(self):
        """An already absent VirtualNetwork sends no provider DELETE."""
        self._run_playbook(
            "playbook_osac_delete_virtual_network.yml",
            self._virtual_network(),
            {},
            [
                ("GET", "/api/v2/vpc"),
                ("GET", "/api/v2/ipam"),
                ("GET", "/api/v2/vpc"),
            ],
            True,
        )

    def test_virtual_network_absent_still_removes_owned_orphan_allocation(self):
        """An orphaned owned allocation is removed after its VPC disappeared."""
        scenario = {
            "allocations": [
                {
                    "id": 202,
                    "name": VIRTUAL_NETWORK_NAME,
                    "tenant": {"id": 77},
                }
            ]
        }
        self._run_playbook(
            "playbook_osac_delete_virtual_network.yml",
            self._virtual_network(),
            scenario,
            [
                ("GET", "/api/v2/vpc"),
                ("GET", "/api/v2/ipam"),
                ("DELETE", "/api/v2/ipam/allocation/202"),
                ("GET", "/api/v2/vpc"),
            ],
            True,
        )

    def test_external_ip_pool_allocation_404_succeeds_after_common_subnet(self):
        """An absent allocation completes cleanup after its common subnet."""
        scenario = {
            "subnets": [{"id": 303, "name": f"{EXTERNAL_IP_POOL_NAME}-common", "prefix": "192.0.2.0/24", "purpose": "common", "tenant": {"id": 77}, "sites": [{"id": 1}]}],
            "allocations": [{"id": 202, "name": EXTERNAL_IP_POOL_NAME, "prefix": "192.0.2.0/24", "tenant": {"id": 77}}],
            "allocation_status": 404,
        }
        self._run_playbook(
            "playbook_osac_delete_external_ip_pool.yml",
            self._external_ip_pool(),
            scenario,
            [
                ("GET", "/api/v2/ipam/subnets"),
                ("DELETE", "/api/v2/ipam/subnet/303"),
                ("GET", "/api/v2/ipam"),
                ("DELETE", "/api/v2/ipam/allocation/202"),
            ],
            True,
        )

    def test_external_ip_pool_allocation_400_fails(self):
        """An allocation dependency error fails pool deletion."""
        scenario = {
            "subnets": [{"id": 303, "name": f"{EXTERNAL_IP_POOL_NAME}-common", "prefix": "192.0.2.0/24", "purpose": "common", "tenant": {"id": 77}, "sites": [{"id": 1}]}],
            "allocations": [{"id": 202, "name": EXTERNAL_IP_POOL_NAME, "prefix": "192.0.2.0/24", "tenant": {"id": 77}}],
            "allocation_status": 400,
        }
        self._run_playbook(
            "playbook_osac_delete_external_ip_pool.yml",
            self._external_ip_pool(),
            scenario,
            [
                ("GET", "/api/v2/ipam/subnets"),
                ("DELETE", "/api/v2/ipam/subnet/303"),
                ("GET", "/api/v2/ipam"),
                ("DELETE", "/api/v2/ipam/allocation/202"),
            ],
            False,
        )

    def test_external_ip_pool_allocation_500_fails(self):
        """An allocation provider failure fails pool deletion."""
        scenario = {
            "subnets": [{"id": 303, "name": f"{EXTERNAL_IP_POOL_NAME}-common", "prefix": "192.0.2.0/24", "purpose": "common", "tenant": {"id": 77}, "sites": [{"id": 1}]}],
            "allocations": [{"id": 202, "name": EXTERNAL_IP_POOL_NAME, "prefix": "192.0.2.0/24", "tenant": {"id": 77}}],
            "allocation_status": 500,
        }
        self._run_playbook(
            "playbook_osac_delete_external_ip_pool.yml",
            self._external_ip_pool(),
            scenario,
            [
                ("GET", "/api/v2/ipam/subnets"),
                ("DELETE", "/api/v2/ipam/subnet/303"),
                ("GET", "/api/v2/ipam"),
                ("DELETE", "/api/v2/ipam/allocation/202"),
            ],
            False,
        )

    def test_external_ip_pool_absent_needs_no_delete(self):
        """An already absent pool sends no provider DELETE."""
        self._run_playbook(
            "playbook_osac_delete_external_ip_pool.yml",
            self._external_ip_pool(),
            {},
            [("GET", "/api/v2/ipam/subnets"), ("GET", "/api/v2/ipam")],
            True,
        )


if __name__ == "__main__":
    unittest.main(verbosity=2)
