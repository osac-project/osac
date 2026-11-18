#!/usr/bin/env python3
"""Check the BMF NetBox, osac-infra, and umbrella Helm render contracts."""

from __future__ import annotations

import argparse
import base64
import contextlib
import json
import pathlib
import secrets
import subprocess
import tempfile
import time
import uuid
from collections.abc import Callable
from typing import Any

import yaml

SCRIPT_DIR = pathlib.Path(__file__).resolve().parent
INSTALLER_DIR = SCRIPT_DIR.parent
REPO_ROOT = INSTALLER_DIR.parent
BMF_CHART = REPO_ROOT / "bare-metal-fulfillment-operator/charts/operator"
OSAC_CHART = INSTALLER_DIR / "charts/osac"
INFRA_CHART = INSTALLER_DIR / "charts/osac-infra"

MOCK_NETBOX = '''\
import json
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse

with open("/tokens/first", encoding="utf-8") as source:
    first_token = source.read().strip()
with open("/tokens/second", encoding="utf-8") as source:
    second_token = source.read().strip()
with open("/app/device-id", encoding="utf-8") as source:
    device_id = int(source.read().strip())

state = {"status": "staged", "owner": None, "version": 1, "requests": [0, 0]}

def device():
    return {
        "id": device_id, "url": f"/api/dcim/devices/{device_id}/",
        "display": "NetBox test device", "name": "NetBox test device",
        "device_type": {
            "id": 1, "url": "/api/dcim/device-types/1/", "display": "Test device type",
            "manufacturer": {"id": 1, "url": "/api/dcim/manufacturers/1/",
                             "display": "Test manufacturer", "name": "Test manufacturer",
                             "slug": "test-manufacturer"},
            "model": "Test model", "slug": "test-device-type",
        },
        "role": {"id": 1, "url": "/api/dcim/device-roles/1/", "display": "Server",
                 "name": "Server", "slug": "server", "_depth": 0},
        "site": {"id": 1, "url": "/api/dcim/sites/1/", "display": "Test site",
                 "name": "Test site", "slug": "test-site"},
        "status": {"value": state["status"], "label": state["status"].title()},
        "custom_fields": {
            "osac_managed": True, "osac_instance_id": state["owner"],
            "osac_bmc_address": "redfish-virtualmedia+https://192.0.2.42/redfish/v1/Systems/1",
            "osac_boot_mac": "aa:bb:cc:dd:ee:42", "contract_case": "deployed",
        },
        "tags": [], "console_port_count": 0, "console_server_port_count": 0,
        "power_port_count": 0, "power_outlet_count": 0, "front_port_count": 0,
        "rear_port_count": 0, "device_bay_count": 0, "module_bay_count": 0,
        "inventory_item_count": 0,
        "last_updated": datetime.now(timezone.utc).isoformat(),
    }

class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_args):
        pass

    def reply(self, code, body, etag=False):
        content = json.dumps(body).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(content)))
        if etag:
            self.send_header("ETag", f'"v{state["version"]}"')
        self.end_headers()
        self.wfile.write(content)

    def authorized(self):
        authorization = self.headers.get("Authorization", "")
        for index, token in enumerate((first_token, second_token)):
            if authorization == "Token " + token:
                state["requests"][index] += 1
                return True
        self.reply(401, {"detail": "Unauthorized"})
        return False

    def do_GET(self):
        url = urlparse(self.path)
        if url.path == "/admin/state" and self.client_address[0] == "127.0.0.1":
            self.reply(200, state)
            return
        if not self.authorized():
            return
        if url.path == "/api/":
            self.reply(200, {"dcim": "/api/dcim/"})
            return
        if url.path == "/api/dcim/devices/":
            query = parse_qs(url.query)
            match = (state["status"] == "staged" and state["owner"] is None
                     and query.get("status") == ["staged"]
                     and query.get("cf_osac_managed") == ["true"]
                     and query.get("cf_osac_instance_id__empty") == ["true"]
                     and query.get("cf_contract_case") == ["deployed"])
            results = [device()] if match else []
            self.reply(200, {"count": len(results), "next": None,
                             "previous": None, "results": results})
            return
        if url.path == f"/api/dcim/devices/{device_id}/":
            self.reply(200, device(), etag=True)
            return
        if url.path == "/api/dcim/interfaces/":
            self.reply(200, {"count": 0, "next": None, "previous": None, "results": []})
            return
        self.reply(404, {"detail": "Not found"})

    def do_PATCH(self):
        if not self.authorized():
            return
        if urlparse(self.path).path != f"/api/dcim/devices/{device_id}/":
            self.reply(404, {"detail": "Not found"})
            return
        if self.headers.get("If-Match") != f'"v{state["version"]}"':
            self.reply(412, {"detail": "Precondition failed"})
            return
        patch = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        state["status"] = patch.get("status", state["status"])
        state["owner"] = patch.get("custom_fields", {}).get("osac_instance_id", state["owner"])
        state["version"] += 1
        self.reply(200, device(), etag=True)

ThreadingHTTPServer(("0.0.0.0", 8080), Handler).serve_forever()
'''

BMF_NETBOX_VALUES = """\
secrets:
  inventoryConfig: osac-inventory-config
  managementConfig: osac-management-config
  netboxToken: custom-netbox-token
  netboxCA: custom-netbox-ca
netbox:
  enabled: true
  bmhNamespace: host-inventory
  url: https://netbox.example.test
  tokenValue: render-test-netbox-token
  caCert: |
    TEST-CA-CERTIFICATE
  allowInsecureHTTP: false
metal3:
  enabled: false
secretAPI:
  url: https://fulfillment-internal-api:8001
  issuerUrl: https://keycloak.example.test/realms/osac
  caBundle:
    configMap: ca-bundle
    key: bundle.pem
"""

OSAC_NETBOX_VALUES = """\
global:
  services:
    bmaas:
      enabled: true
service:
  auth:
    issuerUrl: https://keycloak.example.test/realms/osac
bmf:
  secrets:
    netboxToken: custom-netbox-token
  secretAPI:
    issuerUrl: https://keycloak.example.test/realms/osac
  netbox:
    enabled: true
    bmhNamespace: host-inventory
    url: https://netbox.example.test
    tokenValue: render-test-netbox-token
  metal3:
    enabled: false
"""

def helm_result(release: str, chart: pathlib.Path, *args: str) -> subprocess.CompletedProcess[str]:
    command = ["helm", "template", release, str(chart), *args]
    return subprocess.run(command, check=False, capture_output=True, text=True, cwd=INSTALLER_DIR)


def kubectl(*args: str) -> None:
    result = subprocess.run(["kubectl", *args], check=False, capture_output=True, text=True)
    if result.returncode:
        raise RuntimeError(f"kubectl failed:\n{result.stderr}")


def render(release: str, chart: pathlib.Path, *args: str) -> list[dict[str, Any]]:
    result = helm_result(release, chart, *args)
    if result.returncode:
        command = " ".join(["helm", "template", release, str(chart), *args])
        raise RuntimeError(f"Helm render failed: {command}\n{result.stderr}")
    return [doc for doc in yaml.safe_load_all(result.stdout) if isinstance(doc, dict)]


def assert_helm_failure(expected: str, release: str, chart: pathlib.Path, *args: str) -> None:
    result = helm_result(release, chart, *args)
    output = result.stdout + result.stderr
    assert result.returncode != 0, f"Helm unexpectedly accepted invalid values; expected: {expected}"
    assert expected in output, f"Helm failed without the expected error: {expected}\n{output}"


def resource(docs: list[dict[str, Any]], kind: str, name: str) -> dict[str, Any]:
    matches = [doc for doc in docs if doc.get("kind") == kind and doc.get("metadata", {}).get("name") == name]
    assert len(matches) == 1, f"expected one {kind}/{name}, found {len(matches)}"
    return matches[0]


def assert_resource_absent(docs: list[dict[str, Any]], kind: str, name: str) -> None:
    matches = [doc for doc in docs if doc.get("kind") == kind and doc.get("metadata", {}).get("name") == name]
    assert not matches, f"expected {kind}/{name} to be omitted"


def embedded_config(docs: list[dict[str, Any]], secret_name: str, key: str) -> dict[str, Any]:
    secret = resource(docs, "Secret", secret_name)
    config = yaml.safe_load(secret["stringData"][key])
    assert isinstance(config, dict), f"expected {secret_name}.{key} to contain a YAML mapping"
    return config


def container(resource_doc: dict[str, Any], name: str) -> dict[str, Any]:
    containers = resource_doc["spec"]["template"]["spec"]["containers"]
    matches = [item for item in containers if item.get("name") == name]
    assert len(matches) == 1, f"expected one container named {name}, found {len(matches)}"
    return matches[0]


def environment(container_doc: dict[str, Any]) -> dict[str, dict[str, Any]]:
    return {item["name"]: item for item in container_doc.get("env", [])}


def bmf_render(*args: str) -> list[dict[str, Any]]:
    return render("bmf", BMF_CHART, "--namespace", "osac", *args)


def infra_render(*values: str) -> list[dict[str, Any]]:
    return render(
        "osac-infra",
        INFRA_CHART,
        "--namespace",
        "osac-infra",
        "--set",
        "osacNamespace=osac",
        *[flag for value in values for flag in ("--values", value)],
    )


def check_infra_netbox_shared_postgres() -> None:
    """NetBox must use the shared bundled PostgreSQL, not its own database."""
    docs = infra_render("values/bmaas-ci/infra.yaml", "values/bmaas-netbox-ci/infra.yaml")

    cert = resource(docs, "Certificate", "postgres-client-netbox")
    assert cert["spec"]["commonName"] == "netbox"
    assert cert["spec"]["usages"] == ["client auth"]
    assert cert["spec"]["secretName"] == "postgres-client-cert-netbox"

    init = resource(docs, "Secret", "postgres-init")["stringData"]["init.sh"]
    assert "create user netbox;" in init
    assert "create database netbox owner netbox;" in init

    # The subchart's Bitnami PostgreSQL stays undeployed; the shared one remains.
    assert_resource_absent(docs, "StatefulSet", "osac-infra-postgresql")
    assert_resource_absent(docs, "Secret", "osac-infra-postgresql")
    resource(docs, "Deployment", "postgres")
    resource(docs, "Service", "postgres")

    config = yaml.safe_load(resource(docs, "ConfigMap", "osac-infra-netbox")["data"]["netbox.yaml"])
    database = config["DATABASES"]["default"]
    assert database["HOST"] == "postgres"
    assert database["PORT"] == 5432
    assert database["USER"] == "netbox"
    assert database["NAME"] == "netbox"
    options = database["OPTIONS"]
    assert options["sslmode"] == "verify-full"
    assert options["sslcert"] == "/secrets/database-client/tls.crt"
    assert options["sslkey"] == "/secrets/database-client/tls.key"
    assert options["sslrootcert"] == "/secrets/database-client/ca.crt"

    db_password = resource(docs, "Secret", "osac-infra-netbox-postgresql")["data"]["db_password"]
    assert db_password == ""

    for name in ("osac-infra-netbox", "osac-infra-netbox-worker"):
        spec = resource(docs, "Deployment", name)["spec"]["template"]["spec"]
        volume = next(v for v in spec["volumes"] if v["name"] == "database-client-cert")
        assert volume["secret"]["secretName"] == "postgres-client-cert-netbox"
        assert volume["secret"]["defaultMode"] == 0o440
        mount = next(m for m in spec["containers"][0]["volumeMounts"] if m["name"] == "database-client-cert")
        assert mount["mountPath"] == "/secrets/database-client"
        assert mount["readOnly"] is True

    # The Valkey cache never talks to PostgreSQL and must not carry its credentials.
    for doc in docs:
        if doc.get("kind") in ("StatefulSet", "Deployment") and "valkey" in doc["metadata"]["name"]:
            assert "postgres-client-cert-netbox" not in str(doc), doc["metadata"]["name"]


def check_infra_netbox_disabled() -> None:
    """Without the NetBox overlay the shared PostgreSQL gains no NetBox artifacts."""
    docs = infra_render("values/bmaas-ci/infra.yaml")
    assert_resource_absent(docs, "Certificate", "postgres-client-netbox")
    init = resource(docs, "Secret", "postgres-init")["stringData"]["init.sh"]
    assert "netbox" not in init


def check_bmf_netbox(docs: list[dict[str, Any]]) -> None:
    inventory = embedded_config(docs, "osac-inventory-config", "inventory.yaml")
    assert inventory["type"] == "netbox"
    assert inventory["hostClass"] == "metal3"
    netbox = inventory["options"]["netbox"]
    assert netbox["url"] == "https://netbox.example.test"
    assert netbox["tokenFile"] == "/etc/osac/secrets/netbox/token"
    assert netbox["caFile"] == "/etc/osac/secrets/netbox-ca/ca.crt"
    assert netbox["allowInsecureHTTP"] is False
    assert "render-test-netbox-token" not in str(inventory)
    assert "TEST-CA-CERTIFICATE" not in str(inventory)

    management = embedded_config(docs, "osac-management-config", "management.yaml")
    assert management["options"]["metal3"]["namespace"] == "host-inventory"

    token_secret = resource(docs, "Secret", "custom-netbox-token")
    assert token_secret["stringData"]["token"] == "render-test-netbox-token"
    ca_secret = resource(docs, "Secret", "custom-netbox-ca")
    assert "TEST-CA-CERTIFICATE" in str(ca_secret.get("stringData", {}))

    bmc_role = resource(docs, "Role", "bmf-operator-bmc-secrets")
    assert bmc_role["metadata"]["namespace"] == "host-inventory"
    bmc_verbs = {verb for rule in bmc_role["rules"] for verb in rule.get("verbs", [])}
    assert not {"list", "watch"}.intersection(bmc_verbs)

    deployment = resource(docs, "Deployment", "bmf-operator-controller-manager")
    manager = container(deployment, "manager")
    env = environment(manager)
    assert "OSAC_SECRET_API_URL" in env
    assert env["OSAC_SECRET_API_ISSUER_URL"]["value"] == "https://keycloak.example.test/realms/osac"
    assert "OSAC_SECRET_API_CLIENT_ID_FILE" in env
    assert "OSAC_SECRET_API_CLIENT_SECRET_FILE" in env
    assert "OSAC_SECRET_API_CA_FILE" in env
    assert "OSAC_SECRET_API_TOKEN_FILE" not in env

    volume_secret_names = {
        volume.get("secret", {}).get("secretName") for volume in deployment["spec"]["template"]["spec"]["volumes"]
    }
    assert "fulfillment-controller-credentials" in volume_secret_names
    mount_paths = {mount["mountPath"] for mount in manager.get("volumeMounts", [])}
    assert "/etc/osac/secrets/netbox/" in mount_paths
    assert "/etc/osac/secrets/netbox-ca/" in mount_paths
    assert "/etc/osac/secret-api/credentials/" in mount_paths
    assert "/etc/osac/secret-api/ca/" in mount_paths
    assert sum(mount.get("readOnly") is True for mount in manager.get("volumeMounts", [])) >= 3


def check_bmf_secret_api_is_independent(netbox_values: pathlib.Path) -> None:
    """Check URL opt-in independently of NetBox, preserving required NetBox settings."""
    for netbox_enabled in (False, True):
        for configured in (False, True):
            endpoint = "https://fulfillment-internal-api:8001" if configured else ""
            args = (
                "--values", str(netbox_values),
                "--set", f"netbox.enabled={str(netbox_enabled).lower()}",
                "--set-string", f"secretAPI.url={endpoint}",
            )
            if netbox_enabled and not configured:
                expect_bmf_failure("secretAPI.url is required when netbox.enabled is true", *args)
                continue
            docs = bmf_render(*args)
            deployment = resource(docs, "Deployment", "bmf-operator-controller-manager")
            manager = container(deployment, "manager")
            env = environment(manager)
            for suffix in ("URL", "ISSUER_URL", "CLIENT_ID_FILE", "CLIENT_SECRET_FILE", "CA_FILE"):
                assert (f"OSAC_SECRET_API_{suffix}" in env) == configured
            assert env["OSAC_INSTALLATION_API_URL"]["value"] == endpoint
            if configured:
                assert env["OSAC_SECRET_API_URL"]["value"] == endpoint
            pod_spec = deployment["spec"]["template"]["spec"]
            for name in ("secret-api-client-credentials", "secret-api-ca"):
                assert any(volume["name"] == name for volume in pod_spec["volumes"]) == configured
                assert any(mount["name"] == name for mount in manager["volumeMounts"]) == configured


def check_bmf_ca_bundle_is_opt_in() -> None:
    docs = bmf_render()
    deployment = resource(docs, "Deployment", "bmf-operator-controller-manager")
    manager = container(deployment, "manager")
    env = environment(manager)
    pod_spec = deployment["spec"]["template"]["spec"]
    assert "OSAC_INSTALLATION_API_CA_FILE" not in env
    assert not any(volume.get("name") == "secret-api-ca" for volume in pod_spec["volumes"])
    assert not any(mount.get("name") == "secret-api-ca" for mount in manager.get("volumeMounts", []))

    configured_docs = bmf_render("--set-string", "secretAPI.caBundle.configMap=ca-bundle")
    configured_deployment = resource(configured_docs, "Deployment", "bmf-operator-controller-manager")
    configured_manager = container(configured_deployment, "manager")
    configured_env = environment(configured_manager)
    configured_pod_spec = configured_deployment["spec"]["template"]["spec"]
    assert configured_env["OSAC_INSTALLATION_API_CA_FILE"]["value"] == "/etc/osac/secret-api/ca/bundle.pem"
    volume = next(volume for volume in configured_pod_spec["volumes"] if volume.get("name") == "secret-api-ca")
    assert volume["configMap"]["name"] == "ca-bundle"
    mount = next(mount for mount in configured_manager["volumeMounts"] if mount.get("name") == "secret-api-ca")
    assert mount["mountPath"] == "/etc/osac/secret-api/ca/"


def check_bmf_custom_token_key(netbox_values: pathlib.Path) -> None:
    docs = bmf_render("--values", str(netbox_values), "--set-string", "netbox.tokenSecretKey=true")
    token_secret = resource(docs, "Secret", "custom-netbox-token")
    assert token_secret["stringData"]["true"] == "render-test-netbox-token"

    deployment = resource(docs, "Deployment", "bmf-operator-controller-manager")
    token_volume = next(
        volume for volume in deployment["spec"]["template"]["spec"]["volumes"] if volume.get("name") == "netbox-token"
    )
    assert token_volume["secret"]["items"][0]["key"] == "true"


def check_bmf_without_ca(netbox_values: pathlib.Path) -> None:
    docs = bmf_render("--values", str(netbox_values), "--set-string", "netbox.caCert=")
    inventory = embedded_config(docs, "osac-inventory-config", "inventory.yaml")
    assert "caFile" not in inventory["options"]["netbox"]
    assert_resource_absent(docs, "Secret", "custom-netbox-ca")
    deployment = resource(docs, "Deployment", "bmf-operator-controller-manager")
    pod_spec = deployment["spec"]["template"]["spec"]
    assert not any(
        volume.get("name") == "netbox-ca" or volume.get("secret", {}).get("secretName") == "custom-netbox-ca"
        for volume in pod_spec["volumes"]
    )
    manager = container(deployment, "manager")
    assert not any(mount.get("name") == "netbox-ca" for mount in manager.get("volumeMounts", []))


def check_bmf_metal3() -> None:
    docs = bmf_render("--set", "metal3.enabled=true", "--set-string", "metal3.namespace=host-inventory")
    inventory = embedded_config(docs, "osac-inventory-config", "inventory.yaml")
    assert inventory["type"] == "metal3"
    assert "netbox" not in str(inventory)


def check_bmf_bcm() -> None:
    docs = bmf_render(
        "--set",
        "bcm.enabled=true",
        "--set-string",
        "bcm.url=https://bcm.example.test",
        "--set-string",
        "bcm.bmhNamespace=host-inventory",
        "--set-string",
        "bcm.cert=TEST-BCM-CERT",
        "--set-string",
        "bcm.key=TEST-BCM-KEY",
    )
    inventory = embedded_config(docs, "osac-inventory-config", "inventory.yaml")
    assert inventory["type"] == "bcm"
    assert inventory["options"]["bcm"]["credentialsSecret"] == "osac-bcm-certs"

    management = embedded_config(docs, "osac-management-config", "management.yaml")
    assert management["type"] == "metal3"
    assert management["options"]["metal3"]["namespace"] == "host-inventory"

    bmc_role = resource(docs, "Role", "bmf-operator-bmc-secrets")
    assert bmc_role["metadata"]["namespace"] == "host-inventory"
    certs = resource(docs, "Secret", "osac-bcm-certs")
    assert "TEST-BCM-CERT" in str(certs)
    assert "TEST-BCM-KEY" in str(certs)


def expect_bmf_failure(expected: str, *args: str) -> None:
    assert_helm_failure(expected, "bmf", BMF_CHART, "--namespace", "osac", *args)


def check_bmf_invalid_values(netbox_values: pathlib.Path) -> None:
    expect_bmf_failure(
        "bcm.enabled and metal3.enabled are mutually exclusive",
        "--set",
        "bcm.enabled=true",
        "--set",
        "metal3.enabled=true",
    )
    expect_bmf_failure(
        "netbox.url is required",
        "--set",
        "netbox.enabled=true",
        "--set-string",
        "netbox.tokenValue=render-test",
        "--set-string",
        "netbox.bmhNamespace=host-inventory",
    )
    expect_bmf_failure(
        "netbox.tokenValue is required",
        "--set",
        "netbox.enabled=true",
        "--set-string",
        "netbox.url=https://netbox.example.test",
        "--set-string",
        "netbox.tokenValue=",
        "--set-string",
        "netbox.bmhNamespace=host-inventory",
    )
    expect_bmf_failure(
        "netbox.bmhNamespace is required",
        "--set",
        "netbox.enabled=true",
        "--set-string",
        "netbox.url=https://netbox.example.test",
        "--set-string",
        "netbox.tokenValue=render-test",
    )
    expect_bmf_failure(
        "netbox.enabled and metal3.enabled are mutually exclusive",
        "--values",
        str(netbox_values),
        "--set",
        "metal3.enabled=true",
    )
    expect_bmf_failure(
        "netbox.enabled and bcm.enabled are mutually exclusive",
        "--values",
        str(netbox_values),
        "--set",
        "bcm.enabled=true",
    )
    expect_bmf_failure(
        "netbox.url must start with http:// or https://",
        "--values",
        str(netbox_values),
        "--set-string",
        "netbox.url=netbox.example.test",
    )
    expect_bmf_failure(
        "netbox.url must start with http:// or https://",
        "--values",
        str(netbox_values),
        "--set-string",
        "netbox.url=ftp://netbox.example.test",
    )
    expect_bmf_failure(
        "netbox.url uses HTTP", "--values", str(netbox_values), "--set-string", "netbox.url=http://netbox.example.test"
    )
    expect_bmf_failure(
        "secretAPI.url must use HTTPS when NetBox is enabled",
        "--values",
        str(netbox_values),
        "--set-string",
        "secretAPI.url=http://fulfillment-internal-api:8001",
    )
    expect_bmf_failure(
        "secretAPI.issuerUrl is required when netbox.enabled is true",
        "--values",
        str(netbox_values),
        "--set-string",
        "secretAPI.issuerUrl=",
    )

    allowed_http = bmf_render(
        "--values",
        str(netbox_values),
        "--set-string",
        "netbox.url=http://netbox.example.test",
        "--set",
        "netbox.allowInsecureHTTP=true",
    )
    inventory = embedded_config(allowed_http, "osac-inventory-config", "inventory.yaml")
    assert inventory["options"]["netbox"]["allowInsecureHTTP"] is True


def check_bmf_netbox_secret_lookup(netbox_values: pathlib.Path) -> None:
    namespace = f"bmf-netbox-lookup-{uuid.uuid4().hex[:8]}"
    release = "bmf-netbox-lookup"
    secret_name = "custom-netbox-token"

    def create_secret(value: str, *, key: str = "token", owned_by_release: bool = False) -> None:
        kubectl(
            "create", "secret", "generic", secret_name, "--namespace", namespace, "--from-literal", f"{key}={value}"
        )
        kubectl(
            "annotate",
            "secret",
            secret_name,
            "--namespace",
            namespace,
            "osac.openshift.io/tenant=bmf-netbox-lookup",
            "osac.openshift.io/owner-reference=bmf-netbox-lookup",
        )
        if owned_by_release:
            kubectl("label", "secret", secret_name, "--namespace", namespace, "app.kubernetes.io/managed-by=Helm")
            kubectl(
                "annotate",
                "secret",
                secret_name,
                "--namespace",
                namespace,
                f"meta.helm.sh/release-name={release}",
                f"meta.helm.sh/release-namespace={namespace}",
            )

    def server_render() -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [
                "helm",
                "template",
                release,
                str(BMF_CHART),
                "--namespace",
                namespace,
                "--dry-run=server",
                "--show-only",
                "templates/netbox-secrets.yaml",
                "--values",
                str(netbox_values),
            ],
            check=False,
            capture_output=True,
            text=True,
            cwd=INSTALLER_DIR,
        )

    def server_docs() -> list[dict[str, Any]]:
        result = server_render()
        if result.returncode:
            raise RuntimeError(f"Server-side Helm render failed:\n{result.stderr}")
        return [doc for doc in yaml.safe_load_all(result.stdout) if isinstance(doc, dict)]

    try:
        kubectl("create", "namespace", namespace)
        kubectl(
            "annotate",
            "namespace",
            namespace,
            "osac.openshift.io/tenant=bmf-netbox-lookup",
            "osac.openshift.io/owner-reference=bmf-netbox-lookup",
        )

        create_secret("existing-token", owned_by_release=True)
        managed_docs = server_docs()
        managed_secret = resource(managed_docs, "Secret", secret_name)
        assert managed_secret["data"]["token"] == base64.b64encode(b"existing-token").decode(), (
            "Helm lookup did not preserve data from the current release's Secret"
        )
        assert "stringData" not in managed_secret, "Helm lookup replaced existing Secret data"

        kubectl("delete", "secret", secret_name, "--namespace", namespace)
        create_secret("external-token")
        external_docs = server_docs()
        assert_resource_absent(external_docs, "Secret", secret_name)

        kubectl("delete", "secret", secret_name, "--namespace", namespace)
        create_secret("wrong-key", key="other")
        missing_key = server_render()
        assert missing_key.returncode != 0, "Helm accepted an existing token Secret without the configured key"
        assert "must contain key" in missing_key.stderr, "Helm failed without identifying the missing Secret key"

        kubectl("delete", "secret", secret_name, "--namespace", namespace)
        fallback_docs = server_docs()
        fallback_secret = resource(fallback_docs, "Secret", secret_name)
        assert fallback_secret["stringData"]["token"] == "render-test-netbox-token", (
            "Helm did not create the token Secret from netbox.tokenValue when lookup found no Secret"
        )
    finally:
        subprocess.run(
            ["kubectl", "delete", "namespace", namespace, "--ignore-not-found=true", "--wait=false"],
            check=False,
            capture_output=True,
            text=True,
        )


def preflight_report(docs: list[dict[str, Any]]) -> dict[str, Any]:
    """The render-time preflight report ConfigMap, parsed."""
    for doc in docs:
        name = doc.get("metadata", {}).get("name", "")
        if doc.get("kind") == "ConfigMap" and name.endswith("-preflight-report"):
            return json.loads(doc["data"]["render.json"])
    raise AssertionError("no preflight report ConfigMap rendered")


def check_osac_wiring(temp_dir: pathlib.Path) -> None:
    values_path = temp_dir / "osac-netbox-values.yaml"
    values_path.write_text(OSAC_NETBOX_VALUES, encoding="utf-8")
    default_values = OSAC_CHART / "ci/default-values.yaml"
    docs = render("osac", OSAC_CHART, "--values", str(default_values), "--values", str(values_path))

    inventory_resources = [
        doc
        for doc in docs
        if doc.get("kind") == "Secret" and doc.get("metadata", {}).get("name") == "osac-inventory-config"
    ]
    assert len(inventory_resources) == 1
    inventory = yaml.safe_load(inventory_resources[0]["stringData"]["inventory.yaml"])
    assert inventory["type"] == "netbox"

    deployment = resource(docs, "Deployment", "bmf-operator-controller-manager")
    manager = container(deployment, "manager")
    env = environment(manager)
    assert "OSAC_SECRET_API_ISSUER_URL" in env
    assert env["OSAC_SECRET_API_ISSUER_URL"]["value"] == "https://keycloak.example.test/realms/osac"
    assert env["OSAC_SECRET_API_CA_FILE"]["value"] == "/etc/osac/secret-api/ca/bundle.pem"
    assert "fulfillment-controller-credentials" in str(deployment)
    assert "OSAC_SECRET_API_TOKEN_FILE" not in env
    secret_api_ca = next(
        volume
        for volume in deployment["spec"]["template"]["spec"]["volumes"]
        if volume.get("name") == "secret-api-ca"
    )
    assert secret_api_ca["configMap"]["name"] == "ca-bundle"

    management = embedded_config(docs, "osac-management-config", "management.yaml")
    assert management["type"] == "metal3"
    assert management["options"]["metal3"]["namespace"] == "host-inventory"

    # The shell pre-install validation hook was retired in #1845 (OSAC-6561).
    # Its Metal3 prerequisites moved into the render-time preflight engine, so
    # assert the Job stays gone AND that the checks still run here. Without
    # this, dropping the hook a second time would silently lose coverage.
    assert not [
        doc
        for doc in docs
        if doc.get("kind") == "Job"
        and doc.get("metadata", {}).get("name") == "osac-pre-install-validate"
    ], "the retired pre-install validation Job is rendered again"

    check_ids = {result["id"] for result in preflight_report(docs)["results"]}
    assert {"metal3-bmh-crd", "metal3-watch-all-namespaces"} <= check_ids, sorted(check_ids)

    # The Provisioning grant left this chart in #1845 (OSAC-6561) and now
    # renders from osac-infra/templates/hooks/configure-metal3.yaml, which
    # check_infra_metal3_rbac asserts below.

    assert_helm_failure(
        "bmf.secretAPI.issuerUrl must match service.auth.issuerUrl when bmf.netbox.enabled is true",
        "osac",
        OSAC_CHART,
        "--values",
        str(default_values),
        "--values",
        str(values_path),
        "--set-string",
        "bmf.secretAPI.issuerUrl=https://wrong-issuer.example.test/realms/osac",
    )


def check_infra_metal3_rbac() -> None:
    """The Metal3 Provisioning grant renders from the osac-infra hook."""
    docs = render(
        "osac-infra",
        INFRA_CHART,
        "--namespace",
        "osac-infra",
        "--set",
        "osacNamespace=osac",
        "--values",
        "values/bmaas-ci/infra.yaml",
        "--values",
        "values/bmaas-netbox-ci/infra.yaml",
        "--set",
        "metal3.enabled=true",
    )
    role = resource(docs, "ClusterRole", "osac-infra-configure-metal3")
    provisioning_rules = [rule for rule in role["rules"] if rule.get("resources") == ["provisionings"]]
    assert len(provisioning_rules) == 1
    assert set(provisioning_rules[0]["verbs"]) == {"get", "list", "create", "patch"}


def live_command(command: list[str], *, input_text: str | None = None) -> str:
    result = subprocess.run(command, input=input_text, text=True, capture_output=True, check=False)
    if result.returncode:
        raise RuntimeError(
            f"{command[0]} {command[1]} failed with exit code {result.returncode}: {result.stderr.strip()}"
        )
    return result.stdout


def live_kubectl(*args: str, input_text: str | None = None) -> str:
    return live_command(["kubectl", *args], input_text=input_text)


def live_get(kind: str, name: str, namespace: str) -> dict[str, Any]:
    return json.loads(live_kubectl("get", kind, name, "-n", namespace, "-o", "json"))


def live_apply(documents: list[dict[str, Any]]) -> None:
    live_kubectl("apply", "-f", "-", input_text=yaml.safe_dump_all(documents))


def wait_for(description: str, check: Callable[[], bool], timeout: int = 120) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if check():
            return
        time.sleep(2)
    raise AssertionError(f"timed out waiting for {description}")


def live_mock_state(namespace: str) -> dict[str, Any]:
    output = live_kubectl(
        "exec", "-n", namespace, "deployment/netbox-mock", "--", "python3", "-c",
        "import urllib.request; print(urllib.request.urlopen('http://127.0.0.1:8080/admin/state').read().decode())",
    )
    return json.loads(output)


def live_annotated_metadata(name: str, namespace: str) -> dict[str, Any]:
    return {
        "name": name, "namespace": namespace,
        "annotations": {
            "osac.openshift.io/tenant": "system",
            "osac.openshift.io/owner-reference": "netbox-install-test",
        },
    }


def live_copy_dependency(kind: str, name: str, source: str, target: str) -> None:
    original = live_get(kind, name, source)
    copied = {
        "apiVersion": "v1", "kind": kind,
        "metadata": live_annotated_metadata(name, target),
        "data": original["data"],
    }
    live_apply([copied])


def live_mock_resources(namespace: str, device_id: int, first: str, second: str) -> list[dict[str, Any]]:
    def metadata(name: str) -> dict[str, Any]:
        return live_annotated_metadata(name, namespace)

    return [
        {
            "apiVersion": "v1", "kind": "ConfigMap", "metadata": metadata("netbox-mock"),
            "data": {"server.py": MOCK_NETBOX, "device-id": str(device_id)},
        },
        {
            "apiVersion": "v1", "kind": "Secret", "metadata": metadata("netbox-mock-tokens"),
            "type": "Opaque",
            "data": {
                "first": base64.b64encode(first.encode()).decode(),
                "second": base64.b64encode(second.encode()).decode(),
            },
        },
        {
            "apiVersion": "apps/v1", "kind": "Deployment", "metadata": metadata("netbox-mock"),
            "spec": {
                "replicas": 1, "selector": {"matchLabels": {"app": "netbox-mock"}},
                "template": {
                    "metadata": {"labels": {"app": "netbox-mock"},
                                 "annotations": metadata("netbox-mock")["annotations"]},
                    "spec": {
                        "containers": [{
                            "name": "server", "image": "python:3.12-alpine",
                            "command": ["python3", "/app/server.py"],
                            "ports": [{"containerPort": 8080}],
                            "volumeMounts": [
                                {"name": "code", "mountPath": "/app", "readOnly": True},
                                {"name": "tokens", "mountPath": "/tokens", "readOnly": True},
                            ],
                            "securityContext": {
                                "allowPrivilegeEscalation": False,
                                "capabilities": {"drop": ["ALL"]},
                                "runAsNonRoot": True, "runAsUser": 1000,
                                "readOnlyRootFilesystem": True,
                            },
                        }],
                        "volumes": [
                            {"name": "code", "configMap": {"name": "netbox-mock"}},
                            {"name": "tokens", "secret": {"secretName": "netbox-mock-tokens"}},
                        ],
                    },
                },
            },
        },
        {
            "apiVersion": "v1", "kind": "Service", "metadata": metadata("netbox-mock"),
            "spec": {"selector": {"app": "netbox-mock"}, "ports": [{"port": 8080, "targetPort": 8080}]},
        },
    ]


def live_values(namespace: str, token: str, token_secret: str) -> dict[str, Any]:
    return {
        "fullnameOverride": "bmf-netbox-it",
        "global": {"networking": {"provisioningEnabled": False}},
        "image": {"repository": "localhost/bmf-operator", "tag": "it", "pullPolicy": "Never"},
        "secrets": {"netboxToken": token_secret},
        "netbox": {
            "enabled": True, "bmhNamespace": namespace,
            "url": f"http://netbox-mock.{namespace}.svc.cluster.local:8080",
            "allowInsecureHTTP": True, "tokenValue": token,
        },
        "secretAPI": {
            "url": "https://fulfillment-internal-api.osac.svc.cluster.local:8001",
            "issuerUrl": "https://keycloak.keycloak.svc.cluster.local:8443/realms/osac",
            "caBundle": {"configMap": "ca-bundle", "key": "bundle.pem"},
        },
    }


def live_write_values(path: pathlib.Path, values: dict[str, Any]) -> None:
    path.touch(mode=0o600)
    path.write_text(yaml.safe_dump(values), encoding="utf-8")


def live_helm_install(namespace: str, values_path: pathlib.Path) -> None:
    live_command([
        "helm", "upgrade", "--install", "bmf-netbox-it", str(BMF_CHART),
        "--namespace", namespace, "--values", str(values_path), "--wait", "--timeout", "5m",
    ])


def live_check_rbac(namespace: str) -> None:
    role = live_get("role", "bmf-netbox-it-bmc-secrets", namespace)
    binding = live_get("rolebinding", "bmf-netbox-it-bmc-secrets", namespace)
    assert binding["subjects"] == [{
        "kind": "ServiceAccount", "name": "bmf-netbox-it", "namespace": namespace,
    }]
    assert binding["roleRef"]["name"] == role["metadata"]["name"]
    for rule in role["rules"]:
        if "secrets" in rule.get("resources", []):
            assert not {"list", "watch"}.intersection(rule["verbs"])
    cluster_role = live_get("clusterrole", "bmf-netbox-it-manager-role", namespace)
    for rule in cluster_role["rules"]:
        if "secrets" in rule.get("resources", []):
            assert not {"list", "watch"}.intersection(rule["verbs"])


def live_exists(kind: str, name: str, namespace: str) -> bool:
    result = subprocess.run(
        ["kubectl", "get", kind, name, "-n", namespace, "-o", "name"],
        capture_output=True, text=True, check=False,
    )
    if result.returncode not in (0, 1):
        raise RuntimeError(f"kubectl get {kind} failed with exit code {result.returncode}")
    return result.returncode == 0


def live_cycle(namespace: str, device_id: int, name: str, token_secret: str) -> None:
    bmi = {
        "apiVersion": "osac.openshift.io/v1alpha1", "kind": "BareMetalInstance",
        "metadata": live_annotated_metadata(name, namespace),
        "spec": {
            "selector": {"hostSelector": {"contract_case": "deployed"}},
            "externalHostID": "", "templateID": "noop", "runStrategy": "Always",
        },
    }
    live_apply([bmi])
    host_name = f"netbox-{device_id}"
    runtime_secret = host_name + "-bmc-secret"

    def allocated() -> bool:
        current = live_get("baremetalinstance", name, namespace)
        state = live_mock_state(namespace)
        return (
            current["spec"].get("externalHostID") == f"{namespace}/{host_name}"
            and state["status"] == "active"
            and state["owner"] == current["metadata"]["uid"]
            and live_exists("baremetalhost", host_name, namespace)
            and live_exists("secret", runtime_secret, namespace)
        )

    wait_for(f"allocation of {name}", allocated)
    live_kubectl("delete", "baremetalinstance", name, "-n", namespace, "--wait=false")

    def released() -> bool:
        state = live_mock_state(namespace)
        return (
            not live_exists("baremetalinstance", name, namespace)
            and not live_exists("baremetalhost", host_name, namespace)
            and not live_exists("secret", runtime_secret, namespace)
            and state["status"] == "staged" and state["owner"] is None
        )

    wait_for(f"release of {name}", released)
    assert live_exists("secret", token_secret, namespace)
    assert live_exists("secret", "fulfillment-controller-credentials", namespace)


def live_failure_details(namespace: str) -> str:
    sections = []
    for label, command in (
        ("resources", ["kubectl", "get", "pods,baremetalinstances,baremetalhosts", "-n", namespace, "-o", "wide"]),
        ("controller logs", ["kubectl", "logs", "deployment/bmf-netbox-it-controller-manager", "-n", namespace,
                             "--tail=80"]),
        ("events", ["kubectl", "get", "events", "-n", namespace, "--sort-by=.lastTimestamp"]),
    ):
        result = subprocess.run(command, capture_output=True, text=True, check=False)
        if result.stdout:
            sections.append(f"{label}:\n" + "\n".join(result.stdout.splitlines()[-80:]))
    with contextlib.suppress(RuntimeError, ValueError):
        sections.append("mock state:\n" + json.dumps(live_mock_state(namespace), sort_keys=True))
    return "\n\n".join(sections)


def check_bmf_live_install(device_id: int) -> None:
    """Install BMF in Kind with a private mock NetBox API."""
    namespace = f"osac-netbox-it-{uuid.uuid4().hex[:6]}"
    first, second = secrets.token_hex(20), secrets.token_hex(20)
    with tempfile.TemporaryDirectory(prefix="bmf-netbox-install-") as temp:
        values_path = pathlib.Path(temp) / "values.yaml"
        live_write_values(values_path, live_values(namespace, first, "osac-netbox-token"))

        # The mock Service does not exist yet; rendering must still succeed.
        assert helm_result("bmf-netbox-it", BMF_CHART, "--namespace", namespace,
                           "--values", str(values_path)).returncode == 0

        phase = "starting mock NetBox"
        try:
            live_kubectl("create", "namespace", namespace)
            live_kubectl("annotate", "namespace", namespace,
                         "osac.openshift.io/tenant=system",
                         "osac.openshift.io/owner-reference=netbox-install-test")
            live_copy_dependency("Secret", "fulfillment-controller-credentials", "osac", namespace)
            live_copy_dependency("ConfigMap", "ca-bundle", "osac", namespace)
            live_apply(live_mock_resources(namespace, device_id, first, second))
            live_kubectl("rollout", "status", "deployment/netbox-mock", "-n", namespace, "--timeout=3m")

            phase = "installing BMF"
            live_helm_install(namespace, values_path)
            live_check_rbac(namespace)
            inventory = yaml.safe_load(base64.b64decode(
                live_get("secret", "osac-inventory-config", namespace)["data"]["inventory.yaml"]
            ))
            management = yaml.safe_load(base64.b64decode(
                live_get("secret", "osac-management-config", namespace)["data"]["management.yaml"]
            ))
            assert inventory["type"] == "netbox" and inventory["hostClass"] == "metal3"
            assert management["type"] == "metal3"
            phase = "initial allocation and release"
            live_cycle(namespace, device_id, "netbox-it-first", "osac-netbox-token")

            # A Helm-owned token Secret keeps its current data on upgrade.
            # Point the release at a new Secret to rotate through Helm.
            phase = "rotating the NetBox token"
            live_write_values(values_path, live_values(namespace, second, "osac-netbox-token-rotated"))
            live_helm_install(namespace, values_path)
            live_kubectl("rollout", "restart", "deployment/bmf-netbox-it-controller-manager", "-n", namespace)
            live_kubectl("rollout", "status", "deployment/bmf-netbox-it-controller-manager",
                         "-n", namespace, "--timeout=3m")
            phase = "allocation and release after token rotation"
            live_cycle(namespace, device_id, "netbox-it-after-rotation", "osac-netbox-token-rotated")
            assert live_mock_state(namespace)["requests"][1] > 0, "operator did not use the rotated token"
        except Exception as error:
            raise AssertionError(f"{phase} failed: {error}\n{live_failure_details(namespace)}") from error
        finally:
            subprocess.run(["helm", "uninstall", "bmf-netbox-it", "-n", namespace],
                           capture_output=True, text=True, check=False)
            subprocess.run(["kubectl", "delete", "namespace", namespace,
                            "--ignore-not-found=true", "--wait=false"],
                           capture_output=True, text=True, check=False)


def main() -> None:
    """Run render checks or a selected Kind check."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--live-lookup-only",
        action="store_true",
        help="exercise NetBox Secret lookup against the current Kubernetes cluster",
    )
    parser.add_argument(
        "--live-install-only", action="store_true",
        help="run the deployed BMF check against a mock NetBox API in Kind",
    )
    parser.add_argument("--device-id", type=int, help="numeric NetBox device ID for the live install fixture")
    args = parser.parse_args()

    if args.live_install_only:
        if args.device_id is None or args.device_id <= 0:
            parser.error("--live-install-only requires a positive --device-id")
        check_bmf_live_install(args.device_id)
        print("BMF NetBox Kind install checks passed.")
        return

    with tempfile.TemporaryDirectory(prefix="bmf-netbox-render-") as temp:
        temp_dir = pathlib.Path(temp)
        netbox_values = temp_dir / "netbox-values.yaml"
        netbox_values.write_text(BMF_NETBOX_VALUES, encoding="utf-8")

        if args.live_lookup_only:
            check_bmf_netbox_secret_lookup(netbox_values)
            print("BMF NetBox Secret lookup checks passed.")
            return

        bmf_docs = bmf_render("--values", str(netbox_values))
        check_bmf_netbox(bmf_docs)
        check_bmf_secret_api_is_independent(netbox_values)
        check_bmf_ca_bundle_is_opt_in()
        check_bmf_custom_token_key(netbox_values)
        check_bmf_without_ca(netbox_values)
        check_bmf_metal3()
        check_bmf_bcm()
        check_bmf_invalid_values(netbox_values)
        check_infra_netbox_shared_postgres()
        check_infra_metal3_rbac()
        check_infra_netbox_disabled()
        check_osac_wiring(temp_dir)

    print("BMF NetBox Helm render checks passed.")


if __name__ == "__main__":
    main()
