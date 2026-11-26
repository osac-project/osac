#!/usr/bin/env python3
"""Test the Keycloak MCP reconciliation contract against a local fake Admin API."""

from __future__ import annotations

import importlib.util
import json
import os
import pathlib
import shutil
import subprocess
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any
from unittest.mock import patch
from urllib.parse import parse_qs, urlsplit

INSTALLER = pathlib.Path(__file__).resolve().parents[1]
RECONCILER_PATH = INSTALLER / "charts/osac-infra/files/hooks/reconcile_mcp_exchange_client.py"
SPEC = importlib.util.spec_from_file_location("mcp_reconciler", RECONCILER_PATH)
assert SPEC is not None and SPEC.loader is not None
RECONCILER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(RECONCILER)

API_CLIENT_IDS = ("osac-cli", "osac-ui", "osac-admin", "osac-ui-backend", "osac-csi-driver")


class FakeKeycloak(ThreadingHTTPServer):
    daemon_threads = True

    def __init__(self) -> None:
        super().__init__(("127.0.0.1", 0), FakeKeycloakHandler)
        self.scopes: list[dict[str, Any]] = []
        self.clients: list[dict[str, Any]] = [
            {
                "id": "exchange-client-id",
                "clientId": "https://mcp.example.test/",
                "defaultClientScopeIds": [],
            }
        ] + [
            {"id": f"{client_id}-id", "clientId": client_id, "defaultClientScopeIds": []}
            for client_id in API_CLIENT_IDS
        ]
        self.fail_scope_assignment: str | None = None
        self.scope_creates = 0
        self.mapper_creates = 0
        self.client_creates = 0
        self.scope_assignment_puts = 0


class FakeKeycloakHandler(BaseHTTPRequestHandler):
    def log_message(self, _format: str, *_args: Any) -> None:
        return

    @property
    def state(self) -> FakeKeycloak:
        return self.server  # type: ignore[return-value]

    def _body(self) -> dict[str, Any]:
        length = int(self.headers.get("Content-Length", "0"))
        return json.loads(self.rfile.read(length)) if length else {}

    def _reply(self, status: int, payload: Any = None) -> None:
        body = json.dumps(payload).encode() if payload is not None else b""
        self.send_response(status)
        if body:
            self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        if body:
            self.wfile.write(body)

    def do_POST(self) -> None:
        path = urlsplit(self.path).path
        if path == "/realms/master/protocol/openid-connect/token":
            length = int(self.headers.get("Content-Length", "0"))
            form = parse_qs(self.rfile.read(length).decode())
            if form.get("username") != ["admin"] or form.get("password") != ["password"]:
                self._reply(401, {"error": "unauthorized"})
                return
            self._reply(200, {"access_token": "fake-admin-token"})
            return

        payload = self._body()
        if path == "/admin/realms/osac/client-scopes":
            self.state.scope_creates += 1
            scope = dict(payload, id="scope-osac-api", protocolMappers=[])
            self.state.scopes.append(scope)
            self._reply(201)
        elif path == "/admin/realms/osac/client-scopes/scope-osac-api/protocol-mappers/models":
            self.state.mapper_creates += 1
            self.state.scopes[0]["protocolMappers"].append(dict(payload, id="audience-mapper"))
            self._reply(201)
        elif path == "/admin/realms/osac/clients":
            self.state.client_creates += 1
            self.state.clients.append(dict(payload, id="new-exchange-client", defaultClientScopeIds=[]))
            self._reply(201)
        else:
            self._reply(404)

    def do_GET(self) -> None:
        parsed = urlsplit(self.path)
        path = parsed.path
        parts = path.strip("/").split("/")
        if path == "/admin/realms/osac/client-scopes":
            self._reply(200, self.state.scopes)
        elif len(parts) == 7 and parts[3] == "client-scopes" and parts[5:] == ["protocol-mappers", "models"]:
            scope = next((item for item in self.state.scopes if item.get("id") == parts[4]), None)
            self._reply(200, scope.get("protocolMappers", []) if scope else [])
        elif path == "/admin/realms/osac/clients":
            client_id = parse_qs(parsed.query).get("clientId", [""])[0]
            clients = [item for item in self.state.clients if item.get("clientId") == client_id]
            self._reply(200, clients)
        elif len(parts) == 6 and parts[3] == "clients" and parts[5:] == ["default-client-scopes"]:
            client = next((item for item in self.state.clients if item.get("id") == parts[4]), None)
            scope_ids = client.get("defaultClientScopeIds", []) if client else []
            scopes = [item for item in self.state.scopes if item.get("id") in scope_ids]
            self._reply(200, scopes)
        else:
            self._reply(404)

    def do_PUT(self) -> None:
        path = urlsplit(self.path).path
        parts = path.strip("/").split("/")
        payload = self._body()
        if len(parts) == 5 and parts[3] == "client-scopes":
            scope = next((item for item in self.state.scopes if item.get("id") == parts[4]), None)
            if scope:
                scope.update(payload)
                self._reply(204)
            else:
                self._reply(404)
        elif len(parts) == 8 and parts[3] == "client-scopes" and parts[5:7] == ["protocol-mappers", "models"]:
            scope = next((item for item in self.state.scopes if item.get("id") == parts[4]), None)
            if scope:
                scope["protocolMappers"] = [
                    dict(payload) if item.get("id") == parts[7] else item for item in scope["protocolMappers"]
                ]
                self._reply(204)
            else:
                self._reply(404)
        elif len(parts) == 5 and parts[3] == "clients":
            client = next((item for item in self.state.clients if item.get("id") == parts[4]), None)
            if client:
                client.update(payload)
                self._reply(204)
            else:
                self._reply(404)
        elif len(parts) == 7 and parts[3] == "clients" and parts[5] == "default-client-scopes":
            client = next((item for item in self.state.clients if item.get("id") == parts[4]), None)
            if client:
                self.state.scope_assignment_puts += 1
            if client and client.get("clientId") == self.state.fail_scope_assignment:
                self._reply(403, {"error": "forbidden"})
                return
            if client and parts[6] not in client["defaultClientScopeIds"]:
                client["defaultClientScopeIds"].append(parts[6])
            self._reply(204 if client else 404)
        else:
            self._reply(404)


class KeycloakReconciliationTest(unittest.TestCase):
    def start_keycloak(self, *, fail_scope_assignment: str | None = None) -> tuple[FakeKeycloak, str]:
        server = FakeKeycloak()
        server.fail_scope_assignment = fail_scope_assignment
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()

        def stop_server() -> None:
            server.shutdown()
            thread.join(timeout=5)
            server.server_close()

        self.addCleanup(stop_server)
        return server, f"http://127.0.0.1:{server.server_port}"

    def assert_api_clients_have_scope(self, server: FakeKeycloak) -> None:
        for client_id in API_CLIENT_IDS:
            client = next(item for item in server.clients if item["clientId"] == client_id)
            self.assertEqual(client["defaultClientScopeIds"], ["scope-osac-api"], client_id)

    def reconcile(self, keycloak_url: str, *, client_id: str = "https://mcp.example.test/") -> None:
        RECONCILER.reconcile(keycloak_url, "admin", "password", client_id, "exchange-secret")

    def test_missing_scope_and_mapper_are_created_and_exchange_client_requests_scope(self) -> None:
        server, keycloak_url = self.start_keycloak()

        self.reconcile(keycloak_url)
        self.reconcile(keycloak_url)

        self.assertEqual(server.scope_creates, 1)
        self.assertEqual(server.mapper_creates, 1)
        self.assertEqual(len(server.scopes), 1)
        self.assertEqual(len(server.scopes[0]["protocolMappers"]), 1)
        mapper = server.scopes[0]["protocolMappers"][0]
        self.assertEqual(mapper["name"], RECONCILER.AUDIENCE_MAPPER["name"])
        self.assertEqual(mapper["config"]["included.custom.audience"], "osac-api")
        self.assertEqual(server.clients[0]["defaultClientScopeIds"], ["scope-osac-api"])
        self.assert_api_clients_have_scope(server)

    def test_missing_exchange_client_is_created_and_assigned_scope(self) -> None:
        server, keycloak_url = self.start_keycloak()
        server.clients = [client for client in server.clients if client["clientId"] in API_CLIENT_IDS]

        self.reconcile(keycloak_url)

        self.assertEqual(server.client_creates, 1)
        exchange_client = next(item for item in server.clients if item["clientId"] == "https://mcp.example.test/")
        self.assertEqual(exchange_client["defaultClientScopeIds"], ["scope-osac-api"])
        self.assert_api_clients_have_scope(server)

    def test_scope_only_installation_restores_api_client_assignments(self) -> None:
        server, keycloak_url = self.start_keycloak()

        RECONCILER.reconcile(keycloak_url, "admin", "password")

        self.assertEqual(server.scope_creates, 1)
        self.assertEqual(server.clients[0]["defaultClientScopeIds"], [])
        self.assert_api_clients_have_scope(server)

    def test_existing_scope_assignments_are_not_rewritten(self) -> None:
        server, keycloak_url = self.start_keycloak()

        self.reconcile(keycloak_url)
        self.reconcile(keycloak_url)

        self.assertEqual(server.scope_assignment_puts, len(API_CLIENT_IDS) + 1)

    def test_https_requires_a_configured_ca(self) -> None:
        with patch.dict(os.environ, {"KEYCLOAK_CA_PATH": ""}):
            with self.assertRaisesRegex(RECONCILER.KeycloakError, "KEYCLOAK_CA_PATH is required"):
                self.reconcile("https://keycloak:443")

    def test_existing_bundled_scope_mapper_is_not_duplicated(self) -> None:
        server, keycloak_url = self.start_keycloak()
        realm = json.loads((INSTALLER / "charts/osac-infra/files/realm.json").read_text(encoding="utf-8"))
        scope = next(item for item in realm["clientScopes"] if item["name"] == RECONCILER.SCOPE_NAME)
        scope["id"] = "scope-osac-api"
        server.scopes.append(scope)

        self.reconcile(keycloak_url)

        self.assertEqual(server.mapper_creates, 0)
        self.assertEqual(len(scope["protocolMappers"]), 1)
        self.assertEqual(scope["protocolMappers"][0]["name"], RECONCILER.AUDIENCE_MAPPER["name"])

    def test_existing_incorrect_audience_mapper_is_repaired(self) -> None:
        server, keycloak_url = self.start_keycloak()
        server.scopes.append(
            {
                "id": "scope-osac-api",
                "name": "osac-api",
                "protocol": "openid-connect",
                "attributes": RECONCILER.SCOPE_ATTRIBUTES,
                "protocolMappers": [
                    {
                        "id": "audience-mapper",
                        "name": RECONCILER.AUDIENCE_MAPPER["name"],
                        "protocol": "openid-connect",
                        "protocolMapper": "oidc-audience-mapper",
                        "consentRequired": False,
                        "config": {"included.custom.audience": "wrong-audience"},
                    }
                ],
            }
        )

        self.reconcile(keycloak_url)

        self.assertEqual(server.mapper_creates, 0)
        self.assertEqual(
            server.scopes[0]["protocolMappers"][0]["config"]["included.custom.audience"],
            "osac-api",
        )
        self.assert_api_clients_have_scope(server)

    def test_existing_incorrect_scope_is_repaired(self) -> None:
        server, keycloak_url = self.start_keycloak()
        server.scopes.append(
            {
                "id": "scope-osac-api",
                "name": "osac-api",
                "protocol": "saml",
                "attributes": {"display.on.consent.screen": "true"},
                "protocolMappers": [],
            }
        )

        self.reconcile(keycloak_url)

        self.assertEqual(server.scopes[0]["id"], "scope-osac-api")
        self.assertEqual(server.scopes[0]["protocol"], "openid-connect")
        self.assertEqual(server.scopes[0]["attributes"], RECONCILER.SCOPE_ATTRIBUTES)
        self.assert_api_clients_have_scope(server)

    def test_assignment_failure_is_actionable(self) -> None:
        _, keycloak_url = self.start_keycloak(fail_scope_assignment="https://mcp.example.test/")

        with self.assertRaisesRegex(
            RECONCILER.KeycloakError,
            "could not assign osac-api.*MCP exchange client.*HTTP 403",
        ):
            self.reconcile(keycloak_url)

    def test_keycloak_chart_embeds_and_runs_the_reconciler(self) -> None:
        template = (INSTALLER / "charts/osac-infra/templates/keycloak/resources.yaml").read_text(encoding="utf-8")
        self.assertIn('.Files.Get "files/hooks/reconcile_mcp_exchange_client.py"', template)
        self.assertIn('command: ["python3", "/mcp-reconciler/reconcile_mcp_exchange_client.py"]', template)
        self.assertIn("python3 /mcp-reconciler/reconcile_mcp_exchange_client.py", template)
        self.assertIn("{{- if not .Values.keycloak.devFixtures.enabled }}", template)

    def test_chart_mounts_the_reconciler_and_orders_scope_before_client_assignment(self) -> None:
        if not shutil.which("helm"):
            self.skipTest("helm is required to render the chart contract")
        try:
            import yaml
        except ImportError:
            self.skipTest("PyYAML is required to inspect rendered chart resources")
        command = [
            "helm",
            "template",
            "osac-infra",
            str(INSTALLER / "charts/osac-infra"),
            "--values",
            str(INSTALLER / "values/dev/kind-infra.yaml"),
        ]

        def render_documents(extra_args: list[str] | None = None) -> list[dict[str, Any]]:
            rendered = subprocess.run([*command, *(extra_args or [])], check=False, capture_output=True, text=True)
            self.assertEqual(rendered.returncode, 0, rendered.stderr)
            return [doc for doc in yaml.safe_load_all(rendered.stdout) if isinstance(doc, dict)]

        documents = render_documents()
        config_map = next(
            doc
            for doc in documents
            if doc.get("kind") == "ConfigMap" and doc.get("metadata", {}).get("name") == "keycloak-mcp-reconciler"
        )
        self.assertEqual(
            config_map["data"]["reconcile_mcp_exchange_client.py"], RECONCILER_PATH.read_text(encoding="utf-8")
        )
        jobs = {
            doc.get("metadata", {}).get("name"): doc
            for doc in documents
            if doc.get("kind") == "Job"
        }
        self.assertNotIn("keycloak-reconcile-mcp-scope", jobs)
        password_job = jobs["keycloak-set-passwords"]
        self.assertEqual(password_job["metadata"]["annotations"]["helm.sh/hook-weight"], "16")
        password_config = next(
            doc
            for doc in documents
            if doc.get("kind") == "ConfigMap" and doc.get("metadata", {}).get("name") == "keycloak-password-setup"
        )
        password_script = password_config["data"]["set-passwords.sh"]
        curl_commands = [
            line.strip() for line in password_script.splitlines() if line.lstrip().startswith("curl ")
        ]
        self.assertEqual(curl_commands, ['curl --cacert "${KEYCLOAK_CA_FILE}" "$@"'])
        for curl_command in curl_commands:
            self.assertNotRegex(curl_command, r"(^|\s)-k(\s|$)")
            self.assertNotIn("--insecure", curl_command)
        self.assertIn(
            "python3 /mcp-reconciler/reconcile_mcp_exchange_client.py",
            password_script,
        )
        self.assertEqual(password_job["spec"]["template"]["spec"]["automountServiceAccountToken"], False)
        self.assertIn("runAsNonRoot", password_job["spec"]["template"]["spec"]["securityContext"])
        password_container = password_job["spec"]["template"]["spec"]["containers"][0]
        self.assertIn("KEYCLOAK_CA_PATH", {item["name"] for item in password_container["env"]})
        self.assertIn("keycloak-ca", {item["name"] for item in password_container["volumeMounts"]})
        self.assertIn("mcp-reconciler", {item["name"] for item in password_container["volumeMounts"]})

        no_fixture_documents = render_documents(["--set", "keycloak.devFixtures.enabled=false"])
        self.assertTrue(
            any(
                doc.get("kind") == "Job"
                and doc.get("metadata", {}).get("name") == "keycloak-reconcile-mcp-scope"
                for doc in no_fixture_documents
            )
        )
        scope_job = next(
            doc
            for doc in no_fixture_documents
            if doc.get("kind") == "Job" and doc.get("metadata", {}).get("name") == "keycloak-reconcile-mcp-scope"
        )
        self.assertEqual(scope_job["metadata"]["annotations"]["helm.sh/hook-weight"], "16")
        scope_pod = scope_job["spec"]["template"]["spec"]
        self.assertEqual(scope_pod["automountServiceAccountToken"], False)
        self.assertIn("runAsNonRoot", scope_pod["securityContext"])
        scope_container = scope_pod["containers"][0]
        self.assertIn("KEYCLOAK_CA_PATH", {item["name"] for item in scope_container["env"]})
        self.assertIn("keycloak-ca", {item["name"] for item in scope_container["volumeMounts"]})
        self.assertIn(
            "keycloak-ca",
            {item["name"] for item in scope_pod["volumes"]},
        )
        self.assertFalse(
            any(
                doc.get("kind") == "Job" and doc.get("metadata", {}).get("name") == "keycloak-set-passwords"
                for doc in no_fixture_documents
            )
        )


if __name__ == "__main__":
    unittest.main(verbosity=2)
