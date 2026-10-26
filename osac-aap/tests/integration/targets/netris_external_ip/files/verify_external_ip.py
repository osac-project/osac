"""Exercise the Netris ExternalIP v1 and legacy playbooks against a fixture.

Runs the actual top-level ``playbook_osac_create_external_ip.yml`` and
``playbook_osac_delete_external_ip.yml`` in fresh child Ansible processes
against a stateful loopback provider fixture that implements the documented
Netris IPAM transport (list/create/delete subnets) and records safe request
fields (method and path only; bodies, cookies, and credentials are never
stored).

Fixture assumption: a ``threading.Lock`` serializes every provider state
change in the TEST SERVER to simulate atomic provider behavior. These cases
prove OSAC handling of a provider that already implements the documented
atomic rules (duplicate reservation name and duplicate /32 prefix in the
same site/tenant domain are rejected before a write commits); they do not
prove Netris's rules. Real-provider guarantees stay separate Contract
coverage under OSAC-4843.

Every v1 child process receives an unusable kubeconfig and points
``K8S_AUTH_HOST`` at a loopback sentinel server; each case asserts the
sentinel recorded zero requests. Legacy child jobs and the Kind fixture
setup/teardown use the real dedicated test kubeconfig. Concurrent race
cases coordinate through fixture barriers that expire after 10 seconds and
fail loudly; child playbooks have a 90 second wall timeout.

Cases I01-I10 need the integrated module and role files from Tasks 2 and 3
to turn green; this harness only wires the executable coverage. Requires
the dedicated ``kubeconfig-osac-test`` Kind environment (see
``./setup_test_env.sh`` from tests/integration/).

Usage (from osac-aap/):
    uv run python tests/integration/targets/netris_external_ip/files/verify_external_ip.py
"""

import contextlib
import hmac
import json
import os
import secrets
import signal
import subprocess
import sys
import tempfile
import threading
import time
import unittest
import uuid
from pathlib import Path
from urllib.error import HTTPError

import yaml
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.request import Request, urlopen

OSAC_AAP = Path(__file__).resolve().parents[5]
CREATE_PLAYBOOK = "playbook_osac_create_external_ip.yml"
DELETE_PLAYBOOK = "playbook_osac_delete_external_ip.yml"

TENANT_ID = "integration-tenant"
PROVIDER_SITE_ID = 1
PROVIDER_TENANT_ID = 2
POOL_ID = "77777777-7777-4777-8777-777777777777"
POOL_CIDR = "192.0.2.0/24"
LEGACY_POOL_NAME = "netris-eip-legacy-pool"
LEGACY_ALLOC_PREFIX = "198.51.100.0/30"
LEGACY_ALLOCATED = "198.51.100.1"
SUBPROCESS_TIMEOUT = 90
# Grace for reaping pipes after a process-group kill; uv does not forward
# SIGKILL to its ansible-playbook child, so the kill must cover the group.
REAP_GRACE_SECONDS = 10
BARRIER_TIMEOUT = 10
DELETION_POLL_RETRIES = 20


class NetrisFixtureHandler(BaseHTTPRequestHandler):
    """Serve the Netris IPAM transport and the test control probes."""

    def _respond(self, status, data=None):
        """Send one JSON response without echoing request data."""
        body = json.dumps(data if data is not None else {}).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _record(self, method, path=None):
        """Record the method and path; never the bodies or credentials."""
        with self.server.lock:
            self.server.calls.append(
                {"method": method, "path": path or self.path.split("?")[0]}
            )

    def _read_body(self):
        """Read one JSON request body, or None when it is invalid."""
        length = int(self.headers.get("Content-Length", 0))
        try:
            body = json.loads(self.rfile.read(length)) if length else {}
        except (json.JSONDecodeError, ValueError):
            return None
        return body if isinstance(body, dict) else {}

    def _authorized(self):
        """Require the per-run session on provider endpoints only."""
        if not self.path.startswith("/api/"):
            return True
        if hmac.compare_digest(
            self.headers.get("Cookie", "").encode("utf-8"),
            f"connect.sid={self.server.session_cookie}".encode("utf-8"),
        ):
            return True
        self._respond(401, {"message": "authentication required"})
        return False

    def do_GET(self):
        """Return provider lists, state, and record the request order."""
        if not self._authorized():
            return
        path = self.path.split("?")[0]
        if path == "/_state":
            with self.server.lock:
                state = {
                    "subnets": [dict(s) for s in self.server.subnets],
                    "calls": [dict(c) for c in self.server.calls],
                    "barriers_expired": list(self.server.barriers_expired),
                }
            self._respond(200, state)
        elif path == "/_health":
            self._respond(200)
        elif path == "/api/v2/ipam/subnets":
            self._record("GET")
            with self.server.lock:
                # Provider wire format: raw records carry nested sites and
                # tenant objects; the module normalizes them at its boundary.
                data = [
                    {
                        "id": s["id"],
                        "name": s["name"],
                        "prefix": s["prefix"],
                        "purpose": s["purpose"],
                        "sites": [{"id": s["site_id"]}],
                        "tenant": {"id": s["tenant_id"]},
                    }
                    for s in self.server.subnets
                ]
            self._respond(200, {"data": data})
        elif path == "/api/v2/ipam":
            self._record("GET")
            with self.server.lock:
                data = [dict(a) for a in self.server.allocations]
            self._respond(200, {"data": data})
        else:
            self._respond(404, {"message": "unexpected GET %s" % path})

    def do_POST(self):
        """Serve scenario control and the subnet create endpoint."""
        if not self._authorized():
            return
        path = self.path.split("?")[0]
        if path == "/_scenario":
            body = self._read_body()
            if body is None:
                self._respond(400, {"message": "invalid scenario body"})
                return
            self.server.apply_scenario(body)
            self._respond(200)
        elif path == "/_reset":
            self.server.apply_scenario({})
            self._respond(200)
        elif path in ("/api/v2/ipam/subnet", "/api/v2/ipam/subnets"):
            self._create_subnet()
        else:
            self._respond(404, {"message": "unexpected POST %s" % path})

    def _create_subnet(self):
        """Apply the fixture's atomic create rules before acknowledging."""
        self._record("POST")
        body = self._read_body()
        if body is None:
            self._respond(400, {"message": "invalid subnet body"})
            return
        if not self.server.arrive_barrier():
            # Fail loudly: a barrier expiry means a concurrent case never
            # produced its participants within the 10 second bound.
            self._respond(500, {"message": "fixture barrier expired"})
            return
        if self.server.scenario.get("reject_create"):
            # A genuine provider error: the request is rejected and no
            # reservation commits, so the module must not recover.
            self._respond(
                int(self.server.scenario.get("create_status", 500)),
                {"message": "provider rejected the create"},
            )
            return
        record = self.server.commit_subnet(body)
        if record is None:
            # Fixture assumption: the test provider atomically rejects a
            # duplicate reservation name or a duplicate /32 prefix in the
            # same site/tenant domain.
            self._respond(
                int(self.server.scenario.get("conflict_status", 400)),
                {"message": "subnet already exists"},
            )
            return
        if self.server.scenario.get("lost_create_reply"):
            # The write committed but the acknowledgement is lost; the
            # module must reconcile its own UID before another write.
            self._respond(500, {"message": "lost create acknowledgement"})
            return
        self._respond(
            int(self.server.scenario.get("create_status", 201)),
            {"data": {"id": record["id"]}},
        )

    def do_DELETE(self):
        """Return a scenario-specific delete result and honor sticky mode."""
        if not self._authorized():
            return
        path = self.path.split("?")[0]
        if path.startswith("/api/v2/ipam/subnet/"):
            self._record("DELETE", path)
            subnet_id = path.rsplit("/", 1)[-1]
            with self.server.lock:
                if not self.server.scenario.get("sticky_delete"):
                    self.server.subnets = [
                        s for s in self.server.subnets
                        if str(s.get("id")) != subnet_id
                    ]
            self._respond(
                int(self.server.scenario.get("delete_status", 204)),
                {"message": "provider delete result"},
            )
        else:
            self._respond(404, {"message": "unexpected DELETE %s" % path})

    def log_message(self, format, *args):
        """Keep request details and test-only credentials out of logs."""


class NetrisFixtureServer(ThreadingHTTPServer):
    """Hold the provider store, scenario knobs, and the request log."""

    daemon_threads = True

    def __init__(self):
        """Bind the test provider to an ephemeral loopback port."""
        super().__init__(("127.0.0.1", 0), NetrisFixtureHandler)
        self.session_cookie = secrets.token_hex(16)
        self.lock = threading.Lock()
        self.subnets = []
        self.allocations = []
        self.calls = []
        self.scenario = {}
        self.next_subnet_id = 1
        self.barrier = None
        self.barrier_arrivals = 0
        self.barriers_expired = []

    def apply_scenario(self, payload):
        """Replace provider contents, scenario knobs, and the call log."""
        barrier = payload.get("barrier")
        with self.lock:
            self.subnets = [dict(s) for s in payload.get("subnets", [])]
            self.allocations = [dict(a) for a in payload.get("allocations", [])]
            for subnet in self.subnets:
                self.next_subnet_id = max(
                    self.next_subnet_id, int(subnet["id"]) + 1
                )
            self.calls = []
            self.scenario = dict(payload)
            self.barrier = (
                threading.Barrier(int(barrier["parties"])) if barrier else None
            )
            self.barrier_arrivals = 0
            self.barriers_expired = []

    def commit_subnet(self, body):
        """Commit one create under the lock, or return None on a duplicate.

        Fixture assumption: the lock makes the name and /32 checks atomic
        with the commit, simulating a provider that rejects duplicate
        reservation names and duplicate /32 prefixes in the same
        site/tenant domain before any write commits.
        """
        sites = body.get("sites") or []
        site_id = (
            sites[0].get("id")
            if sites and isinstance(sites[0], dict)
            else body.get("site_id")
        )
        tenant = body.get("tenant")
        tenant_id = (
            tenant.get("id")
            if isinstance(tenant, dict)
            else body.get("tenant_id")
        )
        with self.lock:
            for subnet in self.subnets:
                if subnet.get("name") == body.get("name"):
                    return None
                if (
                    subnet.get("prefix") == body.get("prefix")
                    and subnet.get("site_id") == site_id
                    and subnet.get("tenant_id") == tenant_id
                ):
                    return None
            record = {
                "id": self.next_subnet_id,
                "name": body.get("name"),
                "prefix": body.get("prefix"),
                "purpose": body.get("purpose"),
                "site_id": site_id,
                "tenant_id": tenant_id,
            }
            self.next_subnet_id += 1
            self.subnets.append(record)
            return record

    def arrive_barrier(self):
        """Wait for concurrent participants, or expire loudly after 10s."""
        with self.lock:
            barrier = self.barrier
            if barrier is None or self.barrier_arrivals >= barrier.parties:
                # Single-use: later retry creates must not wait again.
                return True
            self.barrier_arrivals += 1
        try:
            barrier.wait(timeout=BARRIER_TIMEOUT)
        except threading.BrokenBarrierError:
            with self.lock:
                self.barriers_expired.append("create")
            return False
        return True

    def handle_error(self, request, client_address):
        """Keep dropped-connection noise out of the test output."""


class SentinelHandler(BaseHTTPRequestHandler):
    """Count every request a v1 child job must never send."""

    def _count(self):
        with self.server.lock:
            self.server.requests.append(
                {"method": self.command, "path": self.path.split("?")[0]}
            )
        body = b"{}"
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    do_GET = _count
    do_POST = _count
    do_PUT = _count
    do_DELETE = _count
    do_PATCH = _count

    def log_message(self, format, *args):
        """Keep request details out of logs."""


class SentinelServer(ThreadingHTTPServer):
    """Hold the loopback request log for the K8S_AUTH_HOST sentinel."""

    daemon_threads = True

    def __init__(self):
        """Bind the sentinel to an ephemeral loopback port."""
        super().__init__(("127.0.0.1", 0), SentinelHandler)
        self.lock = threading.Lock()
        self.requests = []


class ExternalIPPlaybookTests(unittest.TestCase):
    """Check playbook results, provider state, and no-Kubernetes isolation."""

    @classmethod
    def setUpClass(cls):
        """Start the fixtures and create the isolated Kind resources."""
        cls._require_test_kubeconfig()
        cls.server = NetrisFixtureServer()
        cls.sentinel = SentinelServer()
        cls.threads = []
        for server in (cls.server, cls.sentinel):
            thread = threading.Thread(target=server.serve_forever, daemon=True)
            thread.start()
            cls.threads.append(thread)
        cls.fixture_url = f"http://127.0.0.1:{cls.server.server_port}"
        cls.sentinel_url = f"http://127.0.0.1:{cls.sentinel.server_port}"
        cls.namespace = "osac-netris-eip-" + uuid.uuid4().hex[:8]
        cls.unusable_kubeconfig = None
        cls.addClassCleanup(cls._cleanup)
        cls._create_kind_fixtures()

    @classmethod
    def _cleanup(cls):
        """Remove the isolated Kind resources and stop the fixtures."""
        try:
            for name in ("netris-eip-v1", "netris-eip-isolated",
                         "netris-eip-legacy"):
                cls._kubectl(
                    "patch", "externalip", name, "-n", cls.namespace,
                    "--type=merge", "-p", '{"metadata":{"finalizers":null}}',
                    check=False,
                )
            cls._kubectl(
                "delete", "namespace", cls.namespace, "--ignore-not-found"
            )
        finally:
            if cls.unusable_kubeconfig:
                Path(cls.unusable_kubeconfig).unlink(missing_ok=True)
            for server, thread in zip((cls.server, cls.sentinel), cls.threads):
                server.shutdown()
                server.server_close()
                thread.join(timeout=5)

    @classmethod
    def _require_test_kubeconfig(cls):
        """Fail loudly unless the dedicated test kubeconfig is in use."""
        for var in ("KUBECONFIG", "K8S_AUTH_KUBECONFIG"):
            value = os.environ.get(var, "")
            if "kubeconfig-osac-test" not in value:
                raise AssertionError(
                    f"{var} must point at the dedicated kubeconfig-osac-test "
                    "cluster. Run via `make test` from osac-aap/, or after "
                    "./setup_test_env.sh from tests/integration/ with "
                    'KUBECONFIG="$PWD/kubeconfig-osac-test" '
                    'K8S_AUTH_KUBECONFIG="$PWD/kubeconfig-osac-test".'
                )

    @classmethod
    def _kubectl(cls, *args, check=True, input=None):
        """Run one kubectl command and fail loudly on unexpected errors."""
        result = subprocess.run(
            ["kubectl", *args],
            env=os.environ.copy(),
            input=input,
            capture_output=True,
            text=True,
            timeout=SUBPROCESS_TIMEOUT,
            check=False,
        )
        if check and result.returncode != 0:
            raise AssertionError(
                f"kubectl {' '.join(args)} failed: {result.stderr.strip()}"
            )
        return result.stdout

    @classmethod
    def _external_ip_definition(cls, name, versioned):
        """Build one ExternalIP CR definition for the isolated namespace."""
        annotations = {
            "osac.openshift.io/tenant": TENANT_ID,
            "osac.openshift.io/implementation-strategy": "netris",
        }
        if versioned:
            annotations["osac.openshift.io/externalip-contract-version"] = "v1"
        else:
            annotations["osac.openshift.io/externalippool-name"] = (
                LEGACY_POOL_NAME
            )
        return {
            "apiVersion": "osac.openshift.io/v1alpha1",
            "kind": "ExternalIP",
            "metadata": {
                "name": name,
                "namespace": cls.namespace,
                "labels": {
                    "osac.openshift.io/externalip-uuid": str(uuid.uuid4()),
                },
                "annotations": annotations,
                "finalizers": ["osac.openshift.io/test-hold"],
            },
            "spec": {"pool": POOL_ID},
        }

    @classmethod
    def _create_kind_fixtures(cls):
        """Apply the namespace, pool, and ExternalIP CRs; capture identity."""
        # Write an intentionally invalid kubeconfig so v1 child jobs cannot
        # reach the API even if a stale file exists at a fixed path.
        with tempfile.NamedTemporaryFile(
            "w", suffix="-kubeconfig", delete=False
        ) as handle:
            handle.write("apiVersion: v1\nkind: Config\n")
        cls.unusable_kubeconfig = handle.name
        documents = [
            {
                "apiVersion": "v1",
                "kind": "Namespace",
                "metadata": {"name": cls.namespace},
            },
            {
                "apiVersion": "osac.openshift.io/v1alpha1",
                "kind": "ExternalIPPool",
                "metadata": {
                    "name": "netris-eip-pool",
                    "namespace": cls.namespace,
                    "labels": {
                        "osac.openshift.io/externalippool-uuid": POOL_ID,
                    },
                    "annotations": {"osac.openshift.io/tenant": TENANT_ID},
                },
                "spec": {"ipFamily": "IPv4", "cidrs": [POOL_CIDR]},
            },
        ]
        documents.append(
            cls._external_ip_definition("netris-eip-v1", versioned=True)
        )
        documents.append(
            cls._external_ip_definition("netris-eip-isolated", versioned=True)
        )
        documents.append(
            cls._external_ip_definition("netris-eip-legacy", versioned=False)
        )
        manifest = "\n---\n".join(
            yaml.safe_dump(d, default_flow_style=False).strip() for d in documents
        )
        cls._kubectl("apply", "-f", "-", input=manifest)
        cls.pool_uid = cls._fetched("externalippool", "netris-eip-pool")[
            "metadata"
        ]["uid"]
        cls.v1_resource = cls._fetched("externalip", "netris-eip-v1")
        cls.isolated_resource = cls._fetched("externalip", "netris-eip-isolated")
        cls.legacy_resource = cls._fetched("externalip", "netris-eip-legacy")

    @classmethod
    def _fetched(cls, kind, name):
        """Fetch one CR without kubectl's last-applied bookkeeping.

        Client-side ``kubectl apply`` embeds the full manifest in the
        ``kubectl.kubernetes.io/last-applied-configuration`` annotation.
        Stripping it keeps provider identifiers (for example the pool
        UUID in ``spec.pool``) out of snapshots that child jobs debug
        into their callback output.
        """
        snapshot = json.loads(
            cls._kubectl("get", kind, name, "-n", cls.namespace, "-o", "json")
        )
        annotations = snapshot["metadata"].setdefault("annotations", {})
        annotations.pop("kubectl.kubernetes.io/last-applied-configuration", None)
        return snapshot

    def _pool_snapshot(self):
        """Build the supplied external_ip_pool snapshot."""
        return {
            "fulfillment_id": POOL_ID,
            "kubernetes_uid": self.pool_uid,
            "tenant_id": TENANT_ID,
            "cidr": POOL_CIDR,
            "phase": "Ready",
        }

    def _job_vars(self, resource, pool=True):
        """Build the child job extra-vars with provider transport config."""
        job_vars = {
            "osac_job_vars": {"resource": resource},
            "netris_controller_url": self.fixture_url,
            "netris_session_cookie": self.server.session_cookie,
            "netris_site_id": PROVIDER_SITE_ID,
            "netris_tenant_id": PROVIDER_TENANT_ID,
            "netris_validate_certs": False,
            "ansible_connection": "local",
            "ansible_become": False,
            "ansible_python_interpreter": sys.executable,
        }
        if pool:
            job_vars["osac_job_vars"]["external_ip_pool"] = self._pool_snapshot()
        return job_vars

    def _constructed_resource(self, uid, deleting=None):
        """Build a v1 resource snapshot for cases that need custom identity."""
        metadata = {
            "name": "netris-eip-v1",
            "namespace": self.namespace,
            "uid": uid,
            "generation": 1,
            "labels": {"osac.openshift.io/externalip-uuid": str(uuid.uuid4())},
            "annotations": {
                "osac.openshift.io/tenant": TENANT_ID,
                "osac.openshift.io/implementation-strategy": "netris",
                "osac.openshift.io/externalip-contract-version": "v1",
            },
        }
        if deleting:
            metadata["deletionTimestamp"] = deleting
        return {"metadata": metadata, "spec": {"pool": POOL_ID}}

    @staticmethod
    def _write_vars(job_vars):
        """Write one child job extra-vars file and return its path."""
        with tempfile.NamedTemporaryFile(
            mode="w", suffix=".json", delete=False
        ) as vars_file:
            json.dump(job_vars, vars_file)
            return Path(vars_file.name)

    def _child_env(self, isolated):
        """Build the child Ansible environment for one playbook run."""
        env = os.environ.copy()
        env["ANSIBLE_STDOUT_CALLBACK"] = "ansible.posix.json"
        env["ANSIBLE_SHOW_CUSTOM_STATS"] = "true"
        env["ANSIBLE_INVENTORY_UNPARSED_WARNING"] = "False"
        env["ANSIBLE_LOCALHOST_WARNING"] = "False"
        # The netris.controller.protected_uri action plugin (OSAC-6331)
        # enforces installation-owned connection settings from the process
        # environment; mirror the job's extra-vars there so the legacy path
        # reaches the fixture.
        env.update(
            {
                "NETRIS_CONTROLLER_URL": self.fixture_url,
                "NETRIS_VALIDATE_CERTS": "false",
                "NETRIS_CA_PATH": "",
            }
        )
        if isolated:
            # Prove v1 jobs never reach Kubernetes: unusable credentials
            # plus a loopback sentinel for any K8S_AUTH_HOST request.
            env["KUBECONFIG"] = self.unusable_kubeconfig
            env["K8S_AUTH_KUBECONFIG"] = self.unusable_kubeconfig
            env["K8S_AUTH_HOST"] = self.sentinel_url
        return env

    def _command(self, playbook, vars_path):
        """Build one child ansible-playbook command."""
        return [
            "uv", "run", "ansible-playbook", "-i", "localhost,",
            str(OSAC_AAP / playbook), "-e", f"@{vars_path}",
        ]

    def _run_child(self, playbook, job_vars, isolated=True,
                   timeout=SUBPROCESS_TIMEOUT):
        """Run one public playbook in a fresh Ansible process."""
        process, vars_path = self._spawn_child(
            playbook, job_vars, isolated=isolated
        )
        return self._collect(process, vars_path, timeout=timeout)

    def _spawn_child(self, playbook, job_vars, isolated=True):
        """Start one child Ansible process for a concurrent race case."""
        vars_path = self._write_vars(job_vars)
        try:
            process = subprocess.Popen(
                self._command(playbook, vars_path),
                cwd=OSAC_AAP,
                env=self._child_env(isolated),
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
                # Own process group so a kill reaches the uv wrapper's
                # ansible-playbook child; uv does not forward SIGKILL.
                start_new_session=True,
            )
        except BaseException:
            vars_path.unlink(missing_ok=True)
            raise
        return process, vars_path

    def _spawn_all(self, playbook, job_vars_list):
        """Start every child; reap the started ones if a later spawn fails."""
        spawned = []
        try:
            for job_vars in job_vars_list:
                spawned.append(self._spawn_child(playbook, job_vars))
        except BaseException:
            self._reap(spawned)
            raise
        return spawned

    def _collect(self, process, vars_path, timeout=SUBPROCESS_TIMEOUT):
        """Collect one spawned child and enforce the wall timeout."""
        try:
            stdout, stderr = process.communicate(timeout=timeout)
        except subprocess.TimeoutExpired:
            self._kill_tree(process)
            try:
                stdout, stderr = process.communicate(timeout=REAP_GRACE_SECONDS)
            except subprocess.TimeoutExpired:
                for stream in (process.stdout, process.stderr):
                    if stream is not None:
                        stream.close()
            vars_path.unlink(missing_ok=True)
            self.fail(f"child playbook exceeded the {timeout}s wall timeout")
        vars_path.unlink(missing_ok=True)
        return process.returncode, self._parse_artifact(stdout), stdout + stderr

    def _kill_tree(self, process):
        """Kill the child's process group. uv does not forward SIGKILL to
        its ansible-playbook child, so killing uv alone leaves the job
        running and holding the captured pipes open. The group ID is
        process.pid itself (start_new_session=True) and survives the uv
        leader exiting, so killpg still reaches a playbook that outlives
        it; getpgid would fail exactly there."""
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except (ProcessLookupError, PermissionError):
            with contextlib.suppress(ProcessLookupError):
                process.kill()

    def _reap(self, spawned):
        """Kill still-live children and remove their vars files."""
        for process, vars_path in spawned:
            # Kill unconditionally: uv may have exited while its
            # ansible-playbook descendant still holds the captured pipes,
            # and the process group survives its leader's exit.
            self._kill_tree(process)
            if process.poll() is None:
                try:
                    process.communicate(timeout=REAP_GRACE_SECONDS)
                except subprocess.TimeoutExpired:
                    for stream in (process.stdout, process.stderr):
                        if stream is not None:
                            stream.close()
            vars_path.unlink(missing_ok=True)

    def _collect_all(self, spawned, timeout=SUBPROCESS_TIMEOUT):
        """Collect every spawned child, even when one collection fails."""
        try:
            return [self._collect(*p, timeout=timeout) for p in spawned]
        finally:
            # A failing _collect raises before the remaining children are
            # collected: kill any still-live process and remove its vars
            # file so neither leaks past the test method.
            self._reap(spawned)

    @staticmethod
    def _parse_artifact(stdout):
        """Parse global_custom_stats.osac_result from the json callback."""
        try:
            payload = json.loads(stdout)
        except json.JSONDecodeError:
            start, end = stdout.find("{"), stdout.rfind("}")
            if start < 0 or end <= start:
                return {}
            try:
                payload = json.loads(stdout[start : end + 1])
            except json.JSONDecodeError:
                return {}
        if not isinstance(payload, dict):
            return {}
        stats = payload.get("global_custom_stats")
        if not isinstance(stats, dict):
            return {}
        result = stats.get("osac_result")
        return result if isinstance(result, dict) else {}

    def _scenario(self, **kwargs):
        """Replace the fixture scenario and clear its request log."""
        self.server.apply_scenario(kwargs)
        with self.sentinel.lock:
            self.sentinel.requests.clear()

    def _state(self):
        """Return the provider subnets, call log, and barrier results."""
        request = Request(
            f"{self.fixture_url}/_state", method="GET"
        )
        with urlopen(request, timeout=10) as response:
            return json.loads(response.read().decode())

    def _reservations(self):
        """Return the provider subnet store."""
        return self._state()["subnets"]

    def _uid_reservations(self, uid):
        """Return the provider reservations named osac-eip-<uid>."""
        name = f"osac-eip-{uid}"
        return [s for s in self._reservations() if s.get("name") == name]

    def _assert_no_kubernetes(self, context):
        """Assert the sentinel recorded zero requests, failures included."""
        self.assertEqual(self.sentinel.requests, [], context)

    def _assert_barriers_held(self):
        """Assert no fixture barrier expired during the concurrent case."""
        self.assertEqual(self._state()["barriers_expired"], [])

    def _assert_artifact(self, artifact, operation, resource, data):
        """Assert the exact parsed osac_result artifact fields."""
        self.assertEqual(artifact.get("schemaVersion"), "v1")
        self.assertEqual(artifact.get("operation"), operation)
        self.assertEqual(artifact.get("resourceUID"), resource["metadata"]["uid"])
        self.assertEqual(
            int(artifact.get("observedGeneration", -1)),
            int(resource["metadata"]["generation"]),
        )
        self.assertEqual(artifact.get("data"), data)

    def _assert_no_provider_secrets(self, output, context):
        """Assert the callback output hides controller and pool values."""
        for secret in (self.fixture_url, POOL_ID, self.pool_uid, POOL_CIDR):
            self.assertNotIn(secret, output, context)

    def _deleting_resource(self, name):
        """Put a real CR into deleting and return its snapshot."""
        self._kubectl(
            "delete", "externalip", name, "-n", self.namespace, "--wait=false"
        )
        for _ in range(DELETION_POLL_RETRIES):
            snapshot = json.loads(
                self._kubectl(
                    "get", "externalip", name,
                    "-n", self.namespace, "-o", "json",
                )
            )
            snapshot["metadata"].setdefault("annotations", {}).pop(
                "kubectl.kubernetes.io/last-applied-configuration", None
            )
            if snapshot["metadata"].get("deletionTimestamp"):
                return snapshot
            time.sleep(0.5)
        self.fail(f"ExternalIP {name} never reported a deletionTimestamp")

    def test_i00_provider_rejects_missing_or_incorrect_session(self):
        """Reject unauthenticated requests before reading or mutating state."""
        self._scenario()
        for method, path in (
            ("GET", "/api/v2/ipam/subnets"),
            ("POST", "/api/v2/ipam/subnet"),
            ("DELETE", "/api/v2/ipam/subnet/1"),
        ):
            for headers in ({}, {"Cookie": "connect.sid=invalid-test-session"}):
                request = Request(
                    f"{self.fixture_url}{path}", method=method, headers=headers
                )
                with self.assertRaises(HTTPError) as raised:
                    urlopen(request, timeout=10)
                self.assertEqual(raised.exception.code, 401)
                raised.exception.close()
        self.assertEqual(self._state()["calls"], [])
        self.assertEqual(self._state()["subnets"], [])

    # I01: allocate from a supplied snapshot; the UID /32 persists and the
    # artifact matches UID, generation, and address exactly.
    def test_i01_allocate_persists_uid_reservation_and_artifact(self):
        """I01: allocation commits a UID /32 and emits the exact artifact."""
        self._scenario()
        resource = self.v1_resource
        rc, artifact, output = self._run_child(
            CREATE_PLAYBOOK, self._job_vars(resource)
        )
        self.assertEqual(rc, 0, output)
        self._assert_artifact(
            artifact, "external_ip.allocate", resource, {"address": "192.0.2.1"}
        )
        reservations = self._uid_reservations(resource["metadata"]["uid"])
        self.assertEqual(len(reservations), 1)
        reservation = reservations[0]
        self.assertEqual(reservation["prefix"], "192.0.2.1/32")
        self.assertEqual(reservation["purpose"], "nat")
        self.assertEqual(reservation["site_id"], PROVIDER_SITE_ID)
        self.assertEqual(reservation["tenant_id"], PROVIDER_TENANT_ID)
        self._assert_no_provider_secrets(
            output, "I01 v1 callback exposed provider secrets"
        )
        self._assert_no_kubernetes("I01 v1 allocate hit the Kubernetes sentinel")

    # I02: retrying the same UID returns the same address, and a new UID
    # with the same display name owns a distinct reservation.
    def test_i02_retry_reuses_address_and_distinct_uid_owns_distinct(self):
        """I02: same-UID retry reuses the address; new UID owns distinct."""
        self._scenario()
        resource = self.v1_resource
        uid = resource["metadata"]["uid"]
        for attempt in ("allocate", "retry"):
            rc, artifact, output = self._run_child(
                CREATE_PLAYBOOK, self._job_vars(resource)
            )
            self.assertEqual(rc, 0, f"I02 {attempt} failed: {output}")
            self.assertEqual(artifact["data"]["address"], "192.0.2.1")
        self.assertEqual(len(self._reservations()), 1)
        same_name = self._constructed_resource(str(uuid.uuid4()))
        rc, artifact, output = self._run_child(
            CREATE_PLAYBOOK, self._job_vars(same_name)
        )
        self.assertEqual(rc, 0, output)
        self.assertEqual(artifact["data"]["address"], "192.0.2.2")
        names = [s["name"] for s in self._reservations()]
        self.assertEqual(
            names,
            [
                f"osac-eip-{uid}",
                f"osac-eip-{same_name['metadata']['uid']}",
            ],
        )
        self._assert_no_kubernetes("I02 v1 jobs hit the Kubernetes sentinel")

    # I03: release removes only the UID reservation, confirms absence, and
    # emits the exact empty-data artifact; a repeat release still succeeds.
    def test_i03_release_removes_only_uid_reservation_and_repeats(self):
        """I03: release deletes only its own UID reservation, then repeats."""
        self._scenario()
        resource = self.v1_resource
        uid = resource["metadata"]["uid"]
        rc, artifact, output = self._run_child(
            CREATE_PLAYBOOK, self._job_vars(resource)
        )
        self.assertEqual(rc, 0, output)
        other = self._constructed_resource(str(uuid.uuid4()))
        rc, artifact, output = self._run_child(
            CREATE_PLAYBOOK, self._job_vars(other)
        )
        self.assertEqual(rc, 0, output)
        target = self._uid_reservations(uid)[0]
        deleting = self._deleting_resource("netris-eip-v1")
        rc, artifact, output = self._run_child(
            DELETE_PLAYBOOK, self._job_vars(deleting)
        )
        self.assertEqual(rc, 0, output)
        self._assert_artifact(artifact, "external_ip.release", deleting, {})
        remaining = self._reservations()
        self.assertEqual(len(remaining), 1)
        self.assertEqual(
            remaining[0]["name"], f"osac-eip-{other['metadata']['uid']}"
        )
        calls = self._state()["calls"]
        self.assertTrue(
            any(
                c["method"] == "DELETE"
                and c["path"] == f"/api/v2/ipam/subnet/{target['id']}"
                for c in calls
            ),
            f"I03 never deleted reservation {target['id']}: {calls}",
        )
        self.assertEqual(calls[-1]["method"], "GET")
        # Repeat release succeeds idempotently without another write.
        self._scenario(subnets=self._reservations())
        rc, artifact, output = self._run_child(
            DELETE_PLAYBOOK, self._job_vars(deleting)
        )
        self.assertEqual(rc, 0, output)
        self._assert_artifact(artifact, "external_ip.release", deleting, {})
        self.assertEqual(
            self._state()["calls"],
            [{"method": "GET", "path": "/api/v2/ipam/subnets"}],
        )
        self._assert_no_kubernetes("I03 v1 jobs hit the Kubernetes sentinel")

    # I04: an exhausted pool and a provider error both fail with nonzero rc
    # and no successful artifact.
    def test_i04_exhausted_pool_and_provider_errors_fail(self):
        """I04: exhaustion and provider errors produce no success artifact."""
        uid = str(uuid.uuid4())
        resource = self._constructed_resource(uid)
        pool_hosts = range(1, 255)
        self._scenario(
            subnets=[
                {
                    "id": 5000 + host, "name": f"occupied-{host}",
                    "prefix": f"192.0.2.{host}/32", "purpose": "nat",
                    "site_id": PROVIDER_SITE_ID,
                    "tenant_id": PROVIDER_TENANT_ID,
                }
                for host in pool_hosts
            ]
        )
        rc, artifact, output = self._run_child(
            CREATE_PLAYBOOK, self._job_vars(resource)
        )
        self.assertNotEqual(rc, 0, f"I04 exhaustion unexpectedly passed: {output}")
        self.assertEqual(artifact, {})
        self.assertEqual(self._uid_reservations(uid), [])
        self._scenario(reject_create=True, create_status=500)
        rc, artifact, output = self._run_child(
            CREATE_PLAYBOOK, self._job_vars(resource)
        )
        self.assertNotEqual(rc, 0, f"I04 provider error unexpectedly passed: {output}")
        self.assertEqual(artifact, {})
        self.assertIn(
            "failed (provider_error",
            output,
            "I04 sanitized failure category is hidden",
        )
        self._assert_no_provider_secrets(
            output, "I04 v1 callback exposed provider secrets"
        )
        self.assertEqual(self._uid_reservations(uid), [])
        self._assert_no_kubernetes("I04 v1 jobs hit the Kubernetes sentinel")

    # I05: malformed snapshots, tenant/pool mismatch, an unsupported
    # version, and a missing pool fail before any provider mutation.
    def test_i05_invalid_snapshots_fail_before_provider_mutation(self):
        """I05: invalid snapshots fail without touching the provider."""
        cases = {}
        missing_labels = self._constructed_resource(str(uuid.uuid4()))
        del missing_labels["metadata"]["labels"]
        cases["missing labels"] = (missing_labels, self._pool_snapshot())
        mismatched = self._constructed_resource(str(uuid.uuid4()))
        pool = self._pool_snapshot()
        pool["tenant_id"] = "other-tenant"
        cases["tenant mismatch"] = (mismatched, pool)
        wrong_version = self._constructed_resource(str(uuid.uuid4()))
        wrong_version["metadata"]["annotations"][
            "osac.openshift.io/externalip-contract-version"
        ] = "v2"
        cases["unsupported version"] = (wrong_version, self._pool_snapshot())
        for description, (resource, snapshot) in cases.items():
            with self.subTest(description):
                self._scenario()
                job_vars = self._job_vars(resource)
                job_vars["osac_job_vars"]["external_ip_pool"] = snapshot
                rc, artifact, output = self._run_child(CREATE_PLAYBOOK, job_vars)
                self.assertNotEqual(rc, 0, f"I05 {description} passed: {output}")
                self.assertEqual(artifact, {})
                self.assertEqual(
                    self._state()["calls"],
                    [],
                    f"I05 {description} mutated the provider",
                )
        self._scenario()
        rc, artifact, output = self._run_child(
            CREATE_PLAYBOOK, self._job_vars(self.v1_resource, pool=False)
        )
        self.assertNotEqual(rc, 0, f"I05 missing pool passed: {output}")
        self.assertEqual(artifact, {})
        self.assertEqual(self._state()["calls"], [])

    # I06: a deletion that stays visible past the observation bound and a
    # stale/ambiguous owner both fail without success.
    def test_i06_delayed_deletion_and_ambiguous_owner_fail(self):
        """I06: sticky deletes and ambiguous owners never succeed."""
        uid = str(uuid.uuid4())
        resource = self._constructed_resource(
            uid, deleting="2026-10-19T00:00:00Z"
        )
        self._scenario(
            subnets=[
                {
                    "id": 6001, "name": f"osac-eip-{uid}",
                    "prefix": "192.0.2.1/32", "purpose": "nat",
                    "site_id": PROVIDER_SITE_ID,
                    "tenant_id": PROVIDER_TENANT_ID,
                }
            ],
            sticky_delete=True,
        )
        rc, artifact, output = self._run_child(
            DELETE_PLAYBOOK, self._job_vars(resource)
        )
        self.assertNotEqual(rc, 0, f"I06 sticky delete passed: {output}")
        self.assertEqual(artifact, {})
        ambiguous = str(uuid.uuid4())
        resource = self._constructed_resource(
            ambiguous, deleting="2026-10-19T00:00:00Z"
        )
        duplicate = {
            "name": f"osac-eip-{ambiguous}",
            "prefix": "192.0.2.1/32",
            "purpose": "nat",
            "site_id": PROVIDER_SITE_ID,
            "tenant_id": PROVIDER_TENANT_ID,
        }
        self._scenario(
            subnets=[dict(duplicate, id=7001), dict(duplicate, id=7002)]
        )
        rc, artifact, output = self._run_child(
            DELETE_PLAYBOOK, self._job_vars(resource)
        )
        self.assertNotEqual(rc, 0, f"I06 ambiguous owner passed: {output}")
        self.assertEqual(artifact, {})
        methods = [c["method"] for c in self._state()["calls"]]
        self.assertNotIn("DELETE", methods)
        self._assert_no_kubernetes("I06 v1 jobs hit the Kubernetes sentinel")

    # I07: v1 allocate and release send no Kubernetes sentinel request and
    # leave the real ExternalIP annotations and status unchanged.
    def test_i07_no_kubernetes_access_and_v1_resource_unchanged(self):
        """I07: v1 jobs never touch Kubernetes or mutate the real CR."""
        self._scenario()
        before = self.isolated_resource
        rc, artifact, output = self._run_child(
            CREATE_PLAYBOOK, self._job_vars(before)
        )
        self.assertEqual(rc, 0, output)
        self._assert_artifact(
            artifact, "external_ip.allocate", before, {"address": "192.0.2.1"}
        )
        self._assert_no_kubernetes("I07 allocate hit the Kubernetes sentinel")
        after = self._fetched("externalip", "netris-eip-isolated")
        self.assertEqual(
            after["metadata"]["annotations"], before["metadata"]["annotations"]
        )
        self.assertEqual(after["metadata"]["labels"], before["metadata"]["labels"])
        self.assertEqual(after.get("status", {}), before.get("status", {}))
        self.assertNotIn(
            "osac.openshift.io/allocated-address",
            after["metadata"]["annotations"],
        )
        deleting = self._deleting_resource("netris-eip-isolated")
        before = deleting
        rc, artifact, output = self._run_child(
            DELETE_PLAYBOOK, self._job_vars(deleting)
        )
        self.assertEqual(rc, 0, output)
        self._assert_artifact(artifact, "external_ip.release", deleting, {})
        self._assert_no_kubernetes("I07 release hit the Kubernetes sentinel")
        after = self._fetched("externalip", "netris-eip-isolated")
        self.assertEqual(
            after["metadata"]["annotations"], before["metadata"]["annotations"]
        )
        self.assertEqual(after.get("status", {}), before.get("status", {}))

    # I08: the empty-version legacy create writes the allocated-address
    # annotation and a name-owned reservation; the legacy delete removes it;
    # neither emits a v1 artifact.
    def test_i08_legacy_create_and_delete_name_owned(self):
        """I08: legacy create/delete keep name-owned provider behavior."""
        self._scenario(
            allocations=[
                {
                    "id": 9001, "name": LEGACY_POOL_NAME,
                    "prefix": LEGACY_ALLOC_PREFIX,
                }
            ]
        )
        resource = self.legacy_resource
        # Legacy child jobs use the real Kind configuration; only v1 child
        # jobs are isolated from Kubernetes.
        rc, artifact, output = self._run_child(
            CREATE_PLAYBOOK, self._job_vars(resource), isolated=False
        )
        self.assertEqual(rc, 0, output)
        self.assertEqual(artifact, {})
        cr = json.loads(
            self._kubectl(
                "get", "externalip", "netris-eip-legacy",
                "-n", self.namespace, "-o", "json",
            )
        )
        self.assertEqual(
            cr["metadata"]["annotations"].get(
                "osac.openshift.io/allocated-address"
            ),
            LEGACY_ALLOCATED,
        )
        reservations = [
            s for s in self._reservations()
            if s.get("name") == resource["metadata"]["name"]
        ]
        self.assertEqual(len(reservations), 1)
        self.assertEqual(
            reservations[0]["prefix"], f"{LEGACY_ALLOCATED}/32"
        )
        methods = [c["method"] for c in self._state()["calls"]]
        self.assertIn("POST", methods)
        rc, artifact, output = self._run_child(
            DELETE_PLAYBOOK, self._job_vars(resource), isolated=False
        )
        self.assertEqual(rc, 0, output)
        self.assertEqual(artifact, {})
        self.assertEqual(
            [
                s for s in self._reservations()
                if s.get("name") == resource["metadata"]["name"]
            ],
            [],
        )
        self._assert_no_kubernetes("legacy jobs hit the Kubernetes sentinel")

    # I09: two concurrent UIDs cannot share an address; concurrent same-UID
    # requests converge on one address; a lost create reply retries reuse.
    def test_i09_concurrent_allocations(self):
        """I09: concurrent allocation races converge without duplicates."""
        # Two concurrent distinct UIDs commit distinct /32 addresses.
        self._scenario(barrier={"name": "create", "parties": 2})
        first = self.v1_resource
        second = self._constructed_resource(str(uuid.uuid4()))
        spawned = self._spawn_all(
            CREATE_PLAYBOOK, [self._job_vars(r) for r in (first, second)]
        )
        results = self._collect_all(spawned)
        for rc, artifact, output in results:
            self.assertEqual(rc, 0, output)
        addresses = {a["data"]["address"] for _, a, _ in results}
        self.assertEqual(addresses, {"192.0.2.1", "192.0.2.2"})
        self.assertEqual(len(self._reservations()), 2)
        self._assert_barriers_held()
        self._assert_no_kubernetes("I09 distinct-UID race hit the sentinel")

        # Two concurrent same-UID requests converge on one address.
        self._scenario(barrier={"name": "create", "parties": 2})
        same = self._constructed_resource(str(uuid.uuid4()))
        spawned = self._spawn_all(
            CREATE_PLAYBOOK, [self._job_vars(same) for _ in range(2)]
        )
        results = self._collect_all(spawned)
        for rc, artifact, output in results:
            self.assertEqual(rc, 0, output)
        addresses = {a["data"]["address"] for _, a, _ in results}
        self.assertEqual(len(addresses), 1)
        self.assertEqual(len(self._reservations()), 1)
        self.assertEqual(
            self._reservations()[0]["name"],
            f"osac-eip-{same['metadata']['uid']}",
        )
        self._assert_barriers_held()
        self._assert_no_kubernetes("I09 same-UID race hit the sentinel")

        # A lost create acknowledgement reconciles the UID and reuses it.
        self._scenario(lost_create_reply=True)
        lost = self._constructed_resource(str(uuid.uuid4()))
        rc, artifact, output = self._run_child(
            CREATE_PLAYBOOK, self._job_vars(lost)
        )
        self.assertEqual(rc, 0, output)
        reservations = self._reservations()
        self.assertEqual(len(reservations), 1)
        self.assertEqual(
            artifact["data"]["address"],
            reservations[0]["prefix"].split("/")[0],
        )
        self._assert_no_kubernetes("I09 lost reply hit the Kubernetes sentinel")

    # I10: a non-nat common subnet covering the whole pool does not
    # exhaust allocation; only nat reservations occupy addresses.
    def test_i10_pool_covering_common_subnet_does_not_exhaust(self):
        """I10: pool-covering common subnet does not exhaust allocation."""
        uid = str(uuid.uuid4())
        resource = self._constructed_resource(uid)
        self._scenario(
            subnets=[
                {
                    "id": 4001, "name": "common",
                    "prefix": POOL_CIDR, "purpose": "shared-network",
                    "site_id": PROVIDER_SITE_ID,
                    "tenant_id": PROVIDER_TENANT_ID,
                }
            ]
        )
        rc, artifact, output = self._run_child(
            CREATE_PLAYBOOK, self._job_vars(resource)
        )
        self.assertEqual(rc, 0, output)
        self._assert_artifact(
            artifact, "external_ip.allocate", resource, {"address": "192.0.2.1"}
        )
        reservations = self._uid_reservations(uid)
        self.assertEqual(len(reservations), 1)
        self.assertEqual(reservations[0]["prefix"], "192.0.2.1/32")
        self._assert_no_kubernetes("I10 v1 allocate hit the Kubernetes sentinel")


if __name__ == "__main__":
    unittest.main(verbosity=2)
