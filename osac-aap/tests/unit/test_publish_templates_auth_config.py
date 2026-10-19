import subprocess
from pathlib import Path

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
