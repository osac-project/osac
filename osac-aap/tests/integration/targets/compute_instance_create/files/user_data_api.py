"""Run the VM role with a real Kubernetes API and synthetic OIDC and Secret responses."""

from __future__ import annotations

import base64
import json
import os
from pathlib import Path
import ssl
import subprocess
import sys
import tempfile
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer

CLIENT_SECRET = "synthetic-test-client-secret"
ACCESS_TOKEN = "synthetic-test-access-token"
USERDATA = "synthetic-test-userdata"
CLOUD_CONFIG = f"#cloud-config\nhostname: {USERDATA}"
SYSPREP = f"<unattend><!-- {USERDATA} --></unattend>"
DISCOVERY = "/realm/.well-known/openid-configuration"
TOKEN = "/realm/token"
SECRET = "/api/private/v1/secrets/canonical-fixture-id"

PLAYBOOK = Path(__file__).resolve().parents[1] / "tasks/user_data_sources.yml"


class AuthAndSecretAPI(BaseHTTPRequestHandler):
    def log_message(self, *_args):
        pass

    def reply(self, status, response):
        body = json.dumps(response).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        self.server.calls.append(self.path)
        if self.path == DISCOVERY:
            self.reply(200, {"issuer": self.server.origin + "/realm", "token_endpoint": self.server.origin + TOKEN})
        elif self.path == SECRET and self.headers.get("Authorization") == f"Bearer {ACCESS_TOKEN}":
            self.reply(200, {"data": {"userdata": self.server.payload}})
        else:
            self.reply(404, {"error": "unknown test request"})

    def do_POST(self):
        self.server.calls.append(self.path)
        expected = base64.b64encode(f"fixture:{CLIENT_SECRET}".encode()).decode()
        if self.path == TOKEN and self.headers.get("Authorization") == f"Basic {expected}":
            self.reply(200, {"access_token": ACCESS_TOKEN, "expires_in": 300})
        else:
            self.reply(401, {"error": "unexpected test credentials"})


def exercise(server, directory):
    credential = directory / "client-secret"
    credential.write_text(CLIENT_SECRET, encoding="utf-8")
    env = {
        **os.environ,
        "no_proxy": "127.0.0.1",
        "OSAC_FULFILLMENT_ISSUER_URL": server.origin + "/realm",
        "OSAC_FULFILLMENT_INTERNAL_API_URL": server.origin,
        "OSAC_FULFILLMENT_CA_FILE": str(directory / "tls.crt"),
        "OSAC_FULFILLMENT_CLIENT_ID": "fixture",
        "OSAC_FULFILLMENT_CLIENT_SECRET_FILE": str(credential),
    }
    scenarios = [
        ("linux", "none", CLOUD_CONFIG),
        ("linux", "inline", CLOUD_CONFIG),
        ("windows", "inline", SYSPREP),
        ("linux", "direct", CLOUD_CONFIG),
        ("windows", "direct", SYSPREP),
        ("linux", "ambiguous", CLOUD_CONFIG),
        ("linux", "empty-id", CLOUD_CONFIG),
        ("linux", "empty-name", CLOUD_CONFIG),
    ]
    for guest, scenario, payload in scenarios:
        server.payload = base64.b64encode(payload.encode()).decode()
        server.calls.clear()
        result = subprocess.run(
            [
                str(Path(sys.executable).with_name("ansible-playbook")),
                "-v",
                str(PLAYBOOK),
                "-e",
                json.dumps({"scenario": scenario, "guest_os_family": guest, "ansible_python_interpreter": sys.executable}),
            ],
            env={**env, "OSAC_TEST_USERDATA": payload},
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            timeout=90,
        )
        forbidden = (CLIENT_SECRET, ACCESS_TOKEN, USERDATA, server.payload)
        if any(value in result.stdout for value in forbidden):
            raise AssertionError(f"{guest} {scenario} exposed sensitive test data in Ansible output")
        if scenario in {"none", "inline", "direct"}:
            assert result.returncode == 0, result.stdout
        else:
            assert result.returncode != 0 and "a single Secret reference" in result.stdout, result.stdout
        assert server.calls == ([DISCOVERY, TOKEN, SECRET] if scenario == "direct" else []), server.calls
        print(f"Verified {guest} {scenario}")


def main():
    with tempfile.TemporaryDirectory() as tmp, HTTPServer(("127.0.0.1", 0), AuthAndSecretAPI) as server:
        directory = Path(tmp)
        certificate, key = directory / "tls.crt", directory / "tls.key"
        subprocess.run(
            [
                "openssl", "req", "-x509", "-newkey", "rsa:2048", "-noenc", "-days", "1",
                "-subj", "/CN=localhost", "-addext", "subjectAltName=IP:127.0.0.1",
                "-keyout", str(key), "-out", str(certificate),
            ],
            check=True,
            capture_output=True,
            timeout=30,
        )
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(certificate, key)
        server.socket = context.wrap_socket(server.socket, server_side=True)
        server.origin = f"https://127.0.0.1:{server.server_port}"
        server.calls = []
        worker = threading.Thread(target=server.serve_forever, daemon=True)
        worker.start()
        try:
            exercise(server, directory)
        finally:
            server.shutdown()
            worker.join(timeout=5)


if __name__ == "__main__":
    main()
