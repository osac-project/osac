"""Capture-only AAP: records HTTP inputs and never executes provisioning."""

import json
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlsplit

TEMPLATES = {1: "osac-create-compute-instance", 2: "osac-delete-compute-instance"}


class RecorderServer(ThreadingHTTPServer):
    def __init__(self, address):
        super().__init__(address, RecorderHandler)
        self.lock = threading.Lock()
        self.jobs = {}
        self.labels = {}
        self.unexpected = []
        self.next_id = 100


class RecorderHandler(BaseHTTPRequestHandler):
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
            "artifacts": {},
        }

    def do_GET(self):
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
                job["status"] = state
                self.reply(200, {})
            elif (
                len(parts) == 5
                and parts[:3] == ["api", "v2", "job_templates"]
                and parts[4] == "launch"
                and parts[3] in ("1", "2", *TEMPLATES.values())
            ):
                if not isinstance(payload.get("extra_vars"), dict):
                    self.reply(400, {"detail": "extra_vars must be an object"})
                    return
                self.server.next_id += 1
                job_id = self.server.next_id
                self.server.jobs[str(job_id)] = {
                    "id": job_id,
                    "template": TEMPLATES[int(parts[3])] if parts[3].isdigit() else parts[3],
                    "status": "running",
                    "payload": payload,
                }
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
