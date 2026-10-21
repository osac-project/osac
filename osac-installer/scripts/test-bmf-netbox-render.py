#!/usr/bin/env python3
"""Check the BMF NetBox and umbrella Helm render contracts."""

from __future__ import annotations

import argparse
import base64
import os
import pathlib
import subprocess
import tempfile
import uuid
from typing import Any

import yaml

SCRIPT_DIR = pathlib.Path(__file__).resolve().parent
INSTALLER_DIR = SCRIPT_DIR.parent
REPO_ROOT = INSTALLER_DIR.parent
BMF_CHART = REPO_ROOT / "bare-metal-fulfillment-operator/charts/operator"
OSAC_CHART = INSTALLER_DIR / "charts/osac"

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

MOCK_KUBECTL = """#!/usr/bin/env bash
set -euo pipefail

if [[ "$1" == get && "$2" == crd ]]; then
  exit 0
fi
if [[ "$1" == get && "$2" == sc ]]; then
  echo default
  exit 0
fi
if [[ "$1" == get && "$2" == provisioning ]]; then
  printf '%s' "$MOCK_WATCH_ALL_NAMESPACES"
  exit 0
fi
echo "Unexpected kubectl call: $*" >&2
exit 1
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


def command_script(resource_doc: dict[str, Any], container_name: str) -> str:
    command = container(resource_doc, container_name).get("command", [])
    assert len(command) >= 3 and command[0:2] == ["/bin/sh", "-c"], f"expected {container_name} to run a /bin/sh script"
    return command[2]


def bmf_render(*args: str) -> list[dict[str, Any]]:
    return render("bmf", BMF_CHART, "--namespace", "osac", *args)


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


def check_osac_wiring(temp_dir: pathlib.Path) -> tuple[list[dict[str, Any]], pathlib.Path]:
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
    assert "fulfillment-controller-credentials" in str(deployment)
    assert "OSAC_SECRET_API_TOKEN_FILE" not in env

    management = embedded_config(docs, "osac-management-config", "management.yaml")
    assert management["type"] == "metal3"
    assert management["options"]["metal3"]["namespace"] == "host-inventory"

    validation_job = resource(docs, "Job", "osac-pre-install-validate")
    validation_script = command_script(validation_job, "validate")
    assert "Checking for Metal3 BareMetalHost CRD..." in validation_script
    assert "kubectl get provisioning" in validation_script

    provisioning_rules = [
        rule
        for doc in docs
        if doc.get("kind") == "ClusterRole"
        for rule in doc.get("rules", [])
        if rule.get("resources") == ["provisionings"]
    ]
    assert len(provisioning_rules) == 1
    assert provisioning_rules[0]["verbs"] == ["list"]

    validation_disabled = render(
        "osac",
        OSAC_CHART,
        "--values",
        str(default_values),
        "--values",
        str(values_path),
        "--set",
        "validation.enabled=false",
    )
    assert not any(doc.get("metadata", {}).get("name") == "osac-pre-install-validate" for doc in validation_disabled)

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
    return docs, values_path


def run_script(script: str, script_path: pathlib.Path, env: dict[str, str]) -> subprocess.CompletedProcess[str]:
    script_path.write_text(script, encoding="utf-8")
    return subprocess.run(
        ["/bin/sh", str(script_path)], check=False, capture_output=True, text=True, cwd=INSTALLER_DIR, env=env
    )


def check_validation_scripts(osac_docs: list[dict[str, Any]], temp_dir: pathlib.Path) -> None:
    mock_bin = temp_dir / "mock-bin"
    mock_bin.mkdir()
    kubectl = mock_bin / "kubectl"
    kubectl.write_text(MOCK_KUBECTL, encoding="utf-8")
    kubectl.chmod(0o755)
    env = os.environ.copy()
    env["PATH"] = f"{mock_bin}:{env['PATH']}"

    osac_job = resource(osac_docs, "Job", "osac-pre-install-validate")
    osac_script = command_script(osac_job, "validate")
    osac_env = env | {"MOCK_WATCH_ALL_NAMESPACES": "true"}
    osac_success = run_script(osac_script, temp_dir / "pre-install-validate.sh", osac_env)
    assert osac_success.returncode == 0, (
        f"OSAC pre-install validation rejected valid prerequisites:\n{osac_success.stdout}{osac_success.stderr}"
    )


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--live-lookup-only",
        action="store_true",
        help="exercise NetBox Secret lookup against the current Kubernetes cluster",
    )
    args = parser.parse_args()

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
        check_bmf_custom_token_key(netbox_values)
        check_bmf_without_ca(netbox_values)
        check_bmf_metal3()
        check_bmf_bcm()
        check_bmf_invalid_values(netbox_values)
        osac_docs, _ = check_osac_wiring(temp_dir)
        check_validation_scripts(osac_docs, temp_dir)

    print("BMF NetBox Helm render checks passed.")


if __name__ == "__main__":
    main()
