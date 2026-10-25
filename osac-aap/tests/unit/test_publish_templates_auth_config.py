import subprocess
from pathlib import Path

import pytest
import yaml


def test_publish_templates_group_uses_keycloak_credentials_and_keeps_kubernetes_identity():
    controller_config_path = (
        Path(__file__).resolve().parents[2]
        / "collections/ansible_collections/osac/config_as_code/roles/aap/vars/controller.yml"
    )
    controller_config = yaml.safe_load(controller_config_path.read_text())
    publish_group = next(
        group
        for group in controller_config["controller_instance_groups"]
        if group["name"] == "{{ aap_prefix }}-publish-templates-ig"
    )
    pod_spec = yaml.safe_load(publish_group["pod_spec_override"])
    worker = pod_spec["spec"]["containers"][0]
    env = {item["name"]: item.get("valueFrom", {}) for item in worker["env"]}

    assert pod_spec["spec"]["serviceAccountName"] == "template-publisher"
    assert env["OSAC_FULFILLMENT_ISSUER_URL"]["configMapKeyRef"] == {
        "name": "aap-fulfillment-auth-config",
        "key": "OSAC_FULFILLMENT_ISSUER_URL",
    }
    assert env["OSAC_FULFILLMENT_CLIENT_ID"]["secretKeyRef"] == {
        "name": "fulfillment-controller-credentials",
        "key": "client-id",
    }
    assert env["OSAC_FULFILLMENT_CLIENT_SECRET"]["secretKeyRef"] == {
        "name": "fulfillment-controller-credentials",
        "key": "client-secret",
    }
    kube_api_access = next(
        volume
        for volume in pod_spec["spec"]["volumes"]
        if volume["name"] == "kube-api-access"
    )
    assert any(
        "serviceAccountToken" in source
        for source in kube_api_access["projected"]["sources"]
    )


def test_config_as_code_secret_tracks_publish_templates_enabled():
    chart_path = Path(__file__).resolve().parents[2] / "charts/aap"

    for enabled in (True, False):
        result = subprocess.run(
            [
                "helm",
                "template",
                "aap-test",
                str(chart_path),
                "--show-only",
                "templates/config-as-code-secret.yaml",
                "--set",
                f"instanceGroups.publishTemplates.enabled={str(enabled).lower()}",
            ],
            check=True,
            capture_output=True,
            text=True,
        )
        secret = yaml.safe_load(result.stdout)

        assert secret["stringData"]["OSAC_PUBLISH_TEMPLATES_ENABLED"] == str(enabled).lower()


def _render_config_as_code_secret(*set_strings):
    chart_path = Path(__file__).resolve().parents[2] / "charts/aap"
    args = []
    for value in set_strings:
        args += ["--set-string", value]
    return subprocess.run(
        [
            "helm",
            "template",
            "aap-test",
            str(chart_path),
            "--show-only",
            "templates/config-as-code-secret.yaml",
            *args,
        ],
        capture_output=True,
        text=True,
    )


def test_config_as_code_secret_takes_caas_netris_settings_from_network_fulfillment():
    prefix = "instanceGroups.networkFulfillment.config"
    result = _render_config_as_code_secret(
        f"{prefix}.NETRIS_CONTROLLER_URL=https://netris.example.com",
        f"{prefix}.NETRIS_VALIDATE_CERTS=false",
        f"{prefix}.NETRIS_CA_CONFIGMAP=customer.netris-ca",
        f"{prefix}.NETRIS_USERNAME=netris",
    )
    assert result.returncode == 0, result.stderr
    data = yaml.safe_load(result.stdout)["stringData"]

    assert data["NETRIS_CONTROLLER_URL"] == "https://netris.example.com"
    assert data["NETRIS_VALIDATE_CERTS"] == "false"
    assert data["NETRIS_CA_CONFIGMAP"] == "customer.netris-ca"
    assert "NETRIS_USERNAME" not in data


def test_config_as_code_secret_omits_netris_settings_without_netris():
    result = _render_config_as_code_secret()
    assert result.returncode == 0, result.stderr
    data = yaml.safe_load(result.stdout)["stringData"]

    assert not any(key.startswith("NETRIS_") for key in data)


def test_config_as_code_secret_rejects_an_invalid_netris_ca_configmap_name():
    chart_path = Path(__file__).resolve().parents[2] / "charts/aap"
    result = subprocess.run(
        [
            "helm",
            "template",
            "aap-test",
            str(chart_path),
            "--set-string",
            "instanceGroups.networkFulfillment.config.NETRIS_CA_CONFIGMAP=.customer-netris-ca",
        ],
        capture_output=True,
        text=True,
    )

    assert result.returncode != 0
    assert "NETRIS_CA_CONFIGMAP must be a ConfigMap name" in result.stderr


def _render_aap_chart(*set_strings):
    chart_path = Path(__file__).resolve().parents[2] / "charts/aap"
    args = []
    for value in set_strings:
        args += ["--set-string", value]
    return subprocess.run(
        ["helm", "template", "aap-test", str(chart_path), *args],
        capture_output=True,
        text=True,
    )


@pytest.mark.parametrize("value", ["", "flase", "no", "yes", "0", " true"])
def test_config_as_code_secret_rejects_a_non_boolean_netris_validate_certs(value):
    result = _render_aap_chart(f"instanceGroups.networkFulfillment.config.NETRIS_VALIDATE_CERTS={value}")

    assert result.returncode != 0
    assert 'NETRIS_VALIDATE_CERTS must be "true" or "false"' in result.stderr


@pytest.mark.parametrize("value,rendered", [("true", "true"), ("false", "false"), ("False", "false"), ("TRUE", "true")])
def test_config_as_code_secret_accepts_a_boolean_netris_validate_certs(value, rendered):
    result = _render_config_as_code_secret(f"instanceGroups.networkFulfillment.config.NETRIS_VALIDATE_CERTS={value}")

    assert result.returncode == 0, result.stderr
    assert yaml.safe_load(result.stdout)["stringData"]["NETRIS_VALIDATE_CERTS"] == rendered


@pytest.mark.parametrize(
    "url",
    [
        "http://netris.example.com",
        "https://netris.example.com/api path",
        "https:// netris.example.com",
        "https://",
        "netris.example.com",
        "https://user:pass@netris.example.com",
    ],
)
def test_config_as_code_secret_rejects_a_non_https_netris_controller_url(url):
    result = _render_aap_chart(f"instanceGroups.networkFulfillment.config.NETRIS_CONTROLLER_URL={url}")

    assert result.returncode != 0
    assert "NETRIS_CONTROLLER_URL must be an https:// URL" in result.stderr


@pytest.mark.parametrize(
    "url",
    ["https://netris.example.com", "https://netris.example.com:8443", "https://10.0.0.1/netris/"],
)
def test_config_as_code_secret_accepts_an_https_netris_controller_url(url):
    result = _render_config_as_code_secret(f"instanceGroups.networkFulfillment.config.NETRIS_CONTROLLER_URL={url}")

    assert result.returncode == 0, result.stderr
    assert yaml.safe_load(result.stdout)["stringData"]["NETRIS_CONTROLLER_URL"] == url


def test_config_as_code_secret_tracks_osac_service_account():
    chart_path = Path(__file__).resolve().parents[2] / "charts/aap"
    result = subprocess.run(
        [
            "helm",
            "template",
            "aap-test",
            str(chart_path),
            "--show-only",
            "templates/config-as-code-secret.yaml",
            "--set-string",
            "serviceAccounts.osacSa=customer-aap-sa",
        ],
        check=True,
        capture_output=True,
        text=True,
    )
    secret = yaml.safe_load(result.stdout)

    assert secret["stringData"]["AAP_OSAC_SERVICE_ACCOUNT"] == "customer-aap-sa"



def test_config_as_code_secret_tracks_its_own_name():
    chart_path = Path(__file__).resolve().parents[2] / "charts/aap"
    result = subprocess.run(
        [
            "helm",
            "template",
            "aap-test",
            str(chart_path),
            "--show-only",
            "templates/config-as-code-secret.yaml",
            "--set-string",
            "configAsCode.secret=customer-config-as-code",
        ],
        check=True,
        capture_output=True,
        text=True,
    )
    secret = yaml.safe_load(result.stdout)

    assert secret["metadata"]["name"] == "customer-config-as-code"
    assert secret["stringData"]["AAP_CONFIG_AS_CODE_SECRET"] == "customer-config-as-code"
