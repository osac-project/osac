"""Capture-only AAP: records HTTP inputs and never executes provisioning.

This is the operator Kind suite's copy of
fulfillment-service/it/testdata/compute_storage_aap.py, which keeps the
recorder in the same component as the test that consumes it. Keep the
endpoints in sync when either suite changes.
"""

import json
import ssl
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.error import HTTPError
from urllib.parse import parse_qs, quote, urlencode, urlsplit
from urllib.request import Request, urlopen

TEMPLATES = {
    1: "osac-create-compute-instance",
    2: "osac-delete-compute-instance",
    3: "osac-apply-network-attachment",
    4: "osac-query-dhcp-lease",
    5: "osac-delete-network-attachment",
    6: "osac-create-bare-metal-instance",
    7: "osac-delete-bare-metal-instance",
}


class RecorderServer(ThreadingHTTPServer):
    """Threaded HTTP server holding recorded jobs and labels."""

    def __init__(self, address):
        super().__init__(address, RecorderHandler)
        self.lock = threading.Lock()
        self.jobs = {}
        self.labels = {}
        self.unexpected = []
        self.handoff_complete = {}
        self.next_id = 100

    @staticmethod
    def open_baremetalinstance_watch(target_id, resource_version):
        service_account = "/var/run/secrets/kubernetes.io/serviceaccount"
        with open(f"{service_account}/namespace", encoding="utf-8") as stream:
            namespace = stream.read().strip()
        with open(f"{service_account}/token", encoding="utf-8") as stream:
            token = stream.read().strip()

        query = urlencode(
            {
                "watch": "true",
                "fieldSelector": f"metadata.name={target_id}",
                "resourceVersion": resource_version,
            }
        )
        url = (
            "https://kubernetes.default.svc/apis/osac.openshift.io/v1alpha1/namespaces/"
            f"{quote(namespace, safe='')}/baremetalinstances?{query}"
        )
        request = Request(url, headers={"Authorization": f"Bearer {token}"})
        tls = ssl.create_default_context(cafile=f"{service_account}/ca.crt")
        return urlopen(request, context=tls)

    def track_baremetalinstance(self, target_id):
        # Establish the filtered watch before the test creates its BMI. The
        # AAP handler can then compare launch arrival with already-seen events.
        response = self.open_baremetalinstance_watch(target_id, "0")
        thread = threading.Thread(
            target=self.consume_baremetalinstance_watch,
            args=(target_id, response, "0"),
            daemon=True,
        )
        thread.start()

    def consume_baremetalinstance_watch(self, target_id, response, resource_version):
        while True:
            try:
                with response:
                    for line in response:
                        if not line:
                            continue
                        event = json.loads(line)
                        if event.get("type") == "ERROR":
                            error = event.get("object", {})
                            if error.get("code") == 410:
                                resource_version = "0"
                                break
                            raise ValueError("Kubernetes watch returned an error")

                        instance = event.get("object", {})
                        metadata = instance.get("metadata", {})
                        resource_version = metadata.get("resourceVersion", resource_version)
                        labels = metadata.get("labels", {})
                        if labels.get("osac.openshift.io/baremetalinstance-uuid") != target_id:
                            continue
                        conditions = instance.get("status", {}).get("conditions", [])
                        handoff_complete = any(
                            condition.get("type") == "NetworkHandoffComplete"
                            and condition.get("status") == "True"
                            for condition in conditions
                        )
                        with self.lock:
                            self.handoff_complete[target_id] = (
                                handoff_complete and event.get("type") != "DELETED",
                                time.monotonic(),
                            )
                        if event.get("type") == "DELETED":
                            return
            except Exception as error:  # noqa: BLE001 - report only a safe error class
                if isinstance(error, (OSError, TimeoutError)):
                    time.sleep(0.2)
                else:
                    with self.lock:
                        self.unexpected.append(
                            f"BareMetalInstance watch failed: {type(error).__name__}"
                        )
                    return
            time.sleep(0.2)
            try:
                response = self.open_baremetalinstance_watch(target_id, resource_version)
            except HTTPError as error:
                if error.code == 410:
                    resource_version = "0"
                    continue
                if error.code not in (429, 500, 502, 503, 504):
                    with self.lock:
                        self.unexpected.append("BareMetalInstance watch failed: HTTPError")
                    return
            except (OSError, TimeoutError):
                continue


class RecorderHandler(BaseHTTPRequestHandler):
    """Minimal AAP-compatible HTTP surface for test captures."""

    def log_message(self, *_args):
        # AAP payloads can contain credentials; never write request data to logs.
        pass

    def reply(self, code, value):
        body = json.dumps(value).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def unknown(self):
        self.server.unexpected.append(f"{self.command} {urlsplit(self.path).path}")
        self.reply(404, {"detail": "unsupported fixture operation"})

    @staticmethod
    def job_response(job):
        return {
            "id": job["id"],
            "status": job["status"],
            "extra_vars": json.dumps(job["payload"]["extra_vars"]),
            "artifacts": job.get("artifacts", {}),
        }

    def network_handoff_complete_at_launch(self, payload, received_at):
        """Use only BMI condition events observed before the DHCP launch."""
        job_vars = payload.get("extra_vars", {}).get("osac_job_vars", {})
        resource = job_vars.get("resource", {})
        target_id = resource.get("spec", {}).get("target", {}).get("id")
        if not isinstance(target_id, str) or not target_id:
            raise ValueError("DHCP launch has no BareMetalInstance target")
        handoff = self.server.handoff_complete.get(target_id)
        return handoff is not None and handoff[0] and handoff[1] < received_at

    def do_GET(self):
        """Serve health, captures, template lookups, labels, and job status."""
        url = urlsplit(self.path)
        with self.server.lock:
            if url.path == "/healthz":
                self.reply(200, {"ready": True})
            elif url.path == "/captures":
                self.reply(
                    200,
                    {
                        "jobs": list(self.server.jobs.values()),
                        "unexpected": self.server.unexpected,
                    },
                )
            elif url.path == "/api/v2/job_templates/":
                name = parse_qs(url.query).get("name", [""])[0]
                results = [
                    {"id": key, "name": value}
                    for key, value in TEMPLATES.items()
                    if value == name
                ]
                self.reply(200, {"count": len(results), "results": results})
            elif url.path == "/api/v2/workflow_job_templates/":
                self.reply(200, {"count": 0, "results": []})
            elif url.path == "/api/v2/organizations/":
                self.reply(200, {"results": [{"id": 1, "name": "osac"}]})
            elif url.path == "/api/v2/labels/":
                name = parse_qs(url.query).get("name", [""])[0]
                labels = [item for item in self.server.labels.values() if item["name"] == name]
                self.reply(200, {"results": labels})
            elif url.path == "/api/v2/jobs/":
                name = parse_qs(url.query).get("labels__name", [""])[0]
                labels = {key for key, item in self.server.labels.items() if item["name"] == name}
                jobs = [
                    self.job_response(job)
                    for job in self.server.jobs.values()
                    if labels.intersection(job["payload"].get("labels", []))
                ]
                self.reply(200, {"results": jobs, "next": None})
            elif url.path.startswith("/api/v2/jobs/"):
                parts = url.path.strip("/").split("/")
                job = self.server.jobs.get(parts[3]) if len(parts) == 4 else None
                if job is None:
                    self.unknown()
                    return
                self.reply(200, self.job_response(job))
            else:
                self.unknown()

    def do_POST(self):
        """Record launches, cancellations, labels, completions, and resets."""
        received_at = time.monotonic()
        try:
            length = int(self.headers.get("Content-Length", "0"))
            if length < 0 or length > 2 * 1024 * 1024:
                raise ValueError("invalid body length")
            payload = json.loads(self.rfile.read(length)) if length else {}
            if not isinstance(payload, dict):
                raise ValueError("expected object")
        except (ValueError, TypeError):
            self.reply(400, {"detail": "invalid fixture request"})
            return

        path = urlsplit(self.path).path
        parts = path.strip("/").split("/")
        with self.server.lock:
            if path == "/reset":
                self.server.jobs.clear()
                self.server.labels.clear()
                self.server.unexpected.clear()
                self.server.handoff_complete.clear()
                self.reply(200, {})
            elif len(parts) == 2 and parts[0] == "track" and parts[1]:
                try:
                    self.server.track_baremetalinstance(parts[1])
                except Exception as error:  # noqa: BLE001 - report only a safe error class
                    self.server.unexpected.append(
                        f"BareMetalInstance watch setup failed: {type(error).__name__}"
                    )
                    self.reply(503, {"detail": "could not track BareMetalInstance"})
                    return
                self.reply(200, {})
            elif path == "/api/v2/labels/":
                label = {"id": len(self.server.labels) + 1, "name": payload["name"]}
                self.server.labels[label["id"]] = label
                self.reply(201, label)
            elif len(parts) == 2 and parts[0] == "complete":
                job = self.server.jobs.get(parts[1])
                state = payload.get("status")
                if job is None or state not in ("successful", "failed", "canceled"):
                    self.reply(400, {"detail": "invalid completion"})
                    return
                artifacts = payload.get("artifacts", {})
                if not isinstance(artifacts, dict):
                    self.reply(400, {"detail": "artifacts must be an object"})
                    return
                job["status"] = state
                job["artifacts"] = artifacts
                self.reply(200, {})
            elif (
                len(parts) == 5
                and parts[:3] == ["api", "v2", "job_templates"]
                and parts[4] == "launch"
                and (
                    (parts[3].isdigit() and int(parts[3]) in TEMPLATES)
                    or parts[3] in TEMPLATES.values()
                )
            ):
                if not isinstance(payload.get("extra_vars"), dict):
                    self.reply(400, {"detail": "extra_vars must be an object"})
                    return
                template = TEMPLATES[int(parts[3])] if parts[3].isdigit() else parts[3]
                handoff_complete = None
                if template == "osac-query-dhcp-lease":
                    try:
                        handoff_complete = self.network_handoff_complete_at_launch(payload, received_at)
                    except Exception as error:  # noqa: BLE001 - report only a safe error class
                        self.server.unexpected.append(
                            f"DHCP launch condition lookup failed: {type(error).__name__}"
                        )
                        self.reply(503, {"detail": "could not inspect handoff condition"})
                        return
                    if not handoff_complete:
                        self.server.unexpected.append(
                            "DHCP launch before NetworkHandoffComplete=True"
                        )
                        self.reply(409, {"detail": "network handoff is incomplete"})
                        return
                self.server.next_id += 1
                job_id = self.server.next_id
                self.server.jobs[str(job_id)] = {
                    "id": job_id,
                    "template": template,
                    "status": "running",
                    "payload": payload,
                    "artifacts": {},
                }
                if handoff_complete is not None:
                    self.server.jobs[str(job_id)]["network_handoff_complete_at_launch"] = handoff_complete
                self.reply(201, {"id": job_id})
            elif (
                len(parts) == 5
                and parts[:3] == ["api", "v2", "jobs"]
                and parts[4] == "cancel"
                and parts[3] in self.server.jobs
            ):
                self.server.jobs[parts[3]]["status"] = "canceled"
                self.reply(202, {})
            else:
                self.unknown()


if __name__ == "__main__":
    RecorderServer(("0.0.0.0", 8080)).serve_forever()
