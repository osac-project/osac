import subprocess
from pathlib import Path

import yaml

CHART_PATH = Path(__file__).resolve().parents[2] / "charts/aap"


def _render(*set_strings, show_only=None):
    args = ["helm", "template", "aap-test", str(CHART_PATH)]
    if show_only:
        args += ["--show-only", show_only]
    for value in set_strings:
        args += ["--set", value]
    return subprocess.run(args, capture_output=True, text=True)


def _bootstrap_job(*set_strings):
    result = _render(*set_strings, show_only="templates/bootstrap-job.yaml")
    assert result.returncode == 0, result.stderr
    return list(yaml.safe_load_all(result.stdout))


def _env_map(container):
    return {item["name"]: item for item in container["env"]}


EXTERNAL_ARGS = (
    "aap.instance.enabled=false",
    "controllerUrl=https://aap.example.com",
    "bootstrap.adminPasswordSecret.name=pr2100v1-aap-admin",
    "bootstrap.adminPasswordSecret.key=admin-password",
)


def test_external_aap_renders_no_instance_cr():
    result = _render(*EXTERNAL_ARGS)
    assert result.returncode == 0, result.stderr
    kinds = {doc["kind"] for doc in yaml.safe_load_all(result.stdout) if doc}

    assert "AnsibleAutomationPlatform" not in kinds


def test_in_cluster_instance_still_renders_the_instance_cr():
    result = _render()
    assert result.returncode == 0, result.stderr
    kinds = {doc["kind"] for doc in yaml.safe_load_all(result.stdout) if doc}

    assert "AnsibleAutomationPlatform" in kinds


def test_external_aap_bootstrap_job_uses_url_and_admin_secret():
    job = next(doc for doc in _bootstrap_job(*EXTERNAL_ARGS) if doc["kind"] == "Job")
    spec = job["spec"]["template"]["spec"]

    wait_for_aap = next(c for c in spec["initContainers"] if c["name"] == "wait-for-aap")
    assert _env_map(wait_for_aap)["AAP_URL"]["value"] == "https://aap.example.com"
    assert (
        _env_map(wait_for_aap)["AAP_PASSWORD"]["valueFrom"]["secretKeyRef"]
        == {"name": "pr2100v1-aap-admin", "key": "admin-password"}
    )

    bootstrap = next(c for c in spec["containers"] if c["name"] == "bootstrap")
    # AAP_HOSTNAME is what config-as-code's aap_hostname reads; the ansible
    # controller collection appends /api/controller/v2 itself.
    assert _env_map(bootstrap)["AAP_HOSTNAME"]["value"] == "https://aap.example.com"
    assert (
        _env_map(bootstrap)["AAP_PASSWORD"]["valueFrom"]["secretKeyRef"]
        == {"name": "pr2100v1-aap-admin", "key": "admin-password"}
    )

    wait_for_secret = next(
        c for c in spec["initContainers"] if c["name"] == "wait-for-admin-secret"
    )
    assert _env_map(wait_for_secret)["AAP_ADMIN_SECRET_NAME"]["value"] == "pr2100v1-aap-admin"

    role = next(doc for doc in _bootstrap_job(*EXTERNAL_ARGS) if doc["kind"] == "Role")
    assert role["rules"][0]["resourceNames"] == ["pr2100v1-aap-admin"]


def test_external_aap_create_token_hook_uses_url_and_admin_secret():
    result = _render(*EXTERNAL_ARGS, show_only="templates/hooks/create-api-token.yaml")
    assert result.returncode == 0, result.stderr
    job = yaml.safe_load(result.stdout)
    spec = job["spec"]["template"]["spec"]

    create_token = next(c for c in spec["containers"] if c["name"] == "create-token")
    assert _env_map(create_token)["AAP_URL"]["value"] == "https://aap.example.com"
    # The token request and the netrc machine must both derive from the URL.
    script = create_token["command"][2]
    assert '"${AAP_URL}/api/gateway/v1/tokens/"' in script
    assert '"${AAP_URL#*://}"' in script

    volume = next(v for v in spec["volumes"] if v["name"] == "aap-admin-password")
    assert volume["secret"]["secretName"] == "pr2100v1-aap-admin"
    # The mounted file keeps the fixed path the script reads.
    assert volume["secret"]["items"] == [{"key": "admin-password", "path": "password"}]


def test_external_aap_without_url_still_renders_and_fails_loudly_in_the_job():
    job = next(
        doc for doc in _bootstrap_job("aap.instance.enabled=false") if doc["kind"] == "Job"
    )
    wait_for_aap = next(
        c
        for c in job["spec"]["template"]["spec"]["initContainers"]
        if c["name"] == "wait-for-aap"
    )

    assert _env_map(wait_for_aap)["AAP_URL"]["value"] == ""
    script = wait_for_aap["command"][2]
    assert "aap.controllerUrl is empty" in script
    assert "exit 1" in script


def test_external_aap_rejects_a_malformed_url_at_render_time():
    for url in ("aap.example.com", "https://aap.example.com/api/controller"):
        result = _render("aap.instance.enabled=false", f"controllerUrl={url}")

        assert result.returncode != 0
        assert "controllerUrl must be an http(s) AAP base URL without a path" in result.stderr


def test_in_cluster_instance_keeps_hostname_env_and_instance_derived_secret():
    job = next(doc for doc in _bootstrap_job() if doc["kind"] == "Job")
    spec = job["spec"]["template"]["spec"]

    wait_for_aap = next(c for c in spec["initContainers"] if c["name"] == "wait-for-aap")
    env = _env_map(wait_for_aap)
    assert env["AAP_GATEWAY_HOSTNAME"]["value"] == "osac-aap"
    assert env["AAP_CONTROLLER_GATEWAY_HOSTNAME"]["value"] == "osac-aap-controller-service"
    assert "AAP_URL" not in env
    assert (
        env["AAP_PASSWORD"]["valueFrom"]["secretKeyRef"]
        == {"name": "osac-aap-admin-password", "key": "password"}
    )

    bootstrap = next(c for c in spec["containers"] if c["name"] == "bootstrap")
    assert "AAP_HOSTNAME" not in _env_map(bootstrap)
