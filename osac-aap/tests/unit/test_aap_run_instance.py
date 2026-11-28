"""The AAP chart's run-instance wiring: global.instanceId and aap.vcluster.*.

One AAP serves many shared-infrastructure E2E runs, so a run names its AAP
organization after its run ID (global.instanceId) and gets its job pods
created inside its own vcluster (aap.vcluster.apiServerUrl plus the token
Secret). Both settings default empty and must leave the render byte-identical
to the vcluster-e2e base, captured in fixtures/aap_vcluster/.
"""

import subprocess
from pathlib import Path

import yaml
from ansible.parsing.dataloader import DataLoader
from ansible.template import Templar

CHART_PATH = Path(__file__).resolve().parents[2] / "charts/aap"
FIXTURES = Path(__file__).resolve().parent / "fixtures" / "aap_vcluster"

INSTANCE = "pr2100v1"
VCLUSTER_ARGS = (
    f"vcluster.apiServerUrl=https://{INSTANCE}.vc-{INSTANCE}.svc.cluster.local",
    f"vcluster.tokenSecret.name={INSTANCE}-vcluster-token",
    "vcluster.tokenSecret.key=token",
)
EXTERNAL_ARGS = (
    "aap.instance.enabled=false",
    "controllerUrl=https://aap.example.com",
    "bootstrap.adminPasswordSecret.name=pr2100v1-aap-admin",
    "bootstrap.adminPasswordSecret.key=admin-password",
)


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


def _secret(*set_strings):
    result = _render(*set_strings, show_only="templates/config-as-code-secret.yaml")
    assert result.returncode == 0, result.stderr
    return next(doc for doc in yaml.safe_load_all(result.stdout) if doc)


def _env_map(container):
    return {item["name"]: item for item in container["env"]}


def _normalize(text):
    """helm's wait-for-aap script renders lines with trailing spaces; the
    repo's trailing-whitespace hook strips them from the committed goldens,
    so compare with them stripped on both sides."""
    return "\n".join(line.rstrip() for line in text.splitlines())


def test_unset_settings_render_byte_identical_to_the_vcluster_e2e_base():
    """Every vcluster wiring omits cleanly: regenerate the goldens with

    helm template aap-test charts/aap --show-only templates/bootstrap-job.yaml \
        > tests/unit/fixtures/aap_vcluster/bootstrap-job-default.yaml
    and the external-AAP and secret variants below, after a chart change.
    """
    cases = [
        ([], "bootstrap-job-default.yaml"),
        (list(EXTERNAL_ARGS), "bootstrap-job-external.yaml"),
    ]
    for set_strings, golden in cases:
        result = _render(*set_strings, show_only="templates/bootstrap-job.yaml")
        assert result.returncode == 0, result.stderr
        assert _normalize(result.stdout) == _normalize(
            (FIXTURES / golden).read_text()
        ), golden

    result = _render(show_only="templates/config-as-code-secret.yaml")
    assert result.returncode == 0, result.stderr
    assert _normalize(result.stdout) == _normalize(
        (FIXTURES / "config-as-code-secret-default.yaml").read_text()
    )


def test_instance_id_names_the_config_as_code_run():
    secret = _secret(f"global.instanceId={INSTANCE}")

    assert secret["stringData"]["OSAC_INSTANCE_ID"] == INSTANCE
    # The organization and prefix follow in config-as-code (config.yml
    # defaults them to the run ID), not through more chart env.
    assert not any(key.startswith("AAP_ORGANIZATION_NAME") for key in secret["stringData"])
    assert not any(key.startswith("AAP_PREFIX") for key in secret["stringData"])


def test_unset_instance_id_adds_no_secret_key():
    secret = _secret()

    assert "OSAC_INSTANCE_ID" not in secret["stringData"]


def test_vcluster_settings_reach_the_bootstrap_job():
    job = next(doc for doc in _bootstrap_job(*VCLUSTER_ARGS) if doc["kind"] == "Job")
    spec = job["spec"]["template"]["spec"]

    bootstrap = next(c for c in spec["containers"] if c["name"] == "bootstrap")
    env = _env_map(bootstrap)
    assert env["VCLUSTER_API_URL"]["value"] == (
        f"https://{INSTANCE}.vc-{INSTANCE}.svc.cluster.local"
    )
    assert env["VCLUSTER_TOKEN"]["valueFrom"]["secretKeyRef"] == {
        "name": f"{INSTANCE}-vcluster-token",
        "key": "token",
    }

    wait_for_token = next(
        c for c in spec["initContainers"] if c["name"] == "wait-for-vcluster-token"
    )
    assert _env_map(wait_for_token)["VCLUSTER_TOKEN_SECRET_NAME"]["value"] == (
        f"{INSTANCE}-vcluster-token"
    )
    script = wait_for_token["command"][2]
    assert "oc get secret" in script
    assert "exit 1" in script


def test_vcluster_settings_add_the_wait_containers_rbac_rule():
    docs = _bootstrap_job(*VCLUSTER_ARGS)
    role = next(doc for doc in docs if doc["kind"] == "Role")
    token_rule = next(
        rule for rule in role["rules"] if rule["resourceNames"] == [f"{INSTANCE}-vcluster-token"]
    )
    assert token_rule["verbs"] == ["get"]


def test_unset_vcluster_settings_keep_the_rbac_and_containers_as_today():
    docs = _bootstrap_job()
    role = next(doc for doc in docs if doc["kind"] == "Role")
    assert len(role["rules"]) == 1

    job = next(doc for doc in docs if doc["kind"] == "Job")
    spec = job["spec"]["template"]["spec"]
    names = [c["name"] for c in spec["initContainers"]]
    assert names == ["wait-for-admin-secret", "wait-for-aap"]
    bootstrap = next(c for c in spec["containers"] if c["name"] == "bootstrap")
    assert "VCLUSTER_API_URL" not in _env_map(bootstrap)
    assert "VCLUSTER_TOKEN" not in _env_map(bootstrap)


def test_vcluster_settings_reach_the_config_as_code_secret():
    secret = _secret(*VCLUSTER_ARGS)

    assert secret["stringData"]["VCLUSTER_API_URL"] == (
        f"https://{INSTANCE}.vc-{INSTANCE}.svc.cluster.local"
    )
    assert secret["stringData"]["VCLUSTER_TOKEN_SECRET_NAME"] == f"{INSTANCE}-vcluster-token"
    assert secret["stringData"]["VCLUSTER_TOKEN_SECRET_KEY"] == "token"
    # The token itself never passes through chart values.
    assert "VCLUSTER_TOKEN" not in secret["stringData"]


def test_unset_vcluster_settings_add_no_secret_keys():
    secret = _secret()

    assert not any(key.startswith("VCLUSTER_") for key in secret["stringData"])


def test_half_set_vcluster_pair_fails_the_render_naming_what_is_missing():
    url_only = _render("vcluster.apiServerUrl=https://pr2100v1.vc-pr2100v1.svc.cluster.local")
    assert url_only.returncode != 0
    assert "vcluster.tokenSecret.name is required when vcluster.apiServerUrl is set" in (
        url_only.stderr
    )

    name_only = _render("vcluster.tokenSecret.name=pr2100v1-vcluster-token")
    assert name_only.returncode != 0
    assert "vcluster.apiServerUrl is required when vcluster.tokenSecret is set" in (
        name_only.stderr
    )

    no_key = _render(
        "vcluster.apiServerUrl=https://pr2100v1.vc-pr2100v1.svc.cluster.local",
        "vcluster.tokenSecret.name=pr2100v1-vcluster-token",
    )
    assert no_key.returncode != 0
    assert "vcluster.tokenSecret.key is required when vcluster.apiServerUrl is set" in (
        no_key.stderr
    )


def test_malformed_vcluster_url_fails_the_render():
    result = _render(
        "vcluster.apiServerUrl=https://pr2100v1.vc-pr2100v1.svc.cluster.local/api/controller",
        "vcluster.tokenSecret.name=pr2100v1-vcluster-token",
        "vcluster.tokenSecret.key=token",
    )

    assert result.returncode != 0
    assert "vcluster.apiServerUrl must be an https:// URL without a path" in result.stderr


def test_plain_http_vcluster_url_fails_the_render():
    # The controller sends the token to this URL as a bearer credential.
    result = _render(
        "vcluster.apiServerUrl=http://pr2100v1.vc-pr2100v1.svc.cluster.local",
        "vcluster.tokenSecret.name=pr2100v1-vcluster-token",
        "vcluster.tokenSecret.key=token",
    )

    assert result.returncode != 0
    assert "vcluster.apiServerUrl must be an https:// URL without a path" in result.stderr


# --- The roles' run-ID plumbing (group_vars/all/osac_instance.yaml) ---

AAP_ROOT = Path(__file__).resolve().parents[2]
INSTANCE_VARS = AAP_ROOT / "group_vars" / "all" / "osac_instance.yaml"
CSI_DEFAULTS = (
    AAP_ROOT
    / "collections/ansible_collections/osac/service/roles/csi_driver_install/defaults/main.yaml"
)
LVMS_DEFAULTS = (
    AAP_ROOT
    / "collections/ansible_collections/osac/templates/roles/lvms_storage/defaults/main.yaml"
)
INSTANCE_LABEL = "osac.openshift.io/instance"


def _render_vars(monkeypatch, instance_id):
    monkeypatch.delenv("OSAC_INSTANCE_ID", raising=False)
    if instance_id is not None:
        monkeypatch.setenv("OSAC_INSTANCE_ID", instance_id)
    variables = yaml.safe_load(INSTANCE_VARS.read_text())
    templar = Templar(loader=DataLoader(), variables=variables)
    return {key: templar.template(value) for key, value in variables.items()}


def _templar_with_instance(instance_id):
    variables = {**yaml.safe_load(INSTANCE_VARS.read_text()), "osac_instance_id": instance_id}
    return Templar(loader=DataLoader(), variables=variables)


def test_the_run_id_label_var_follows_the_env(monkeypatch):
    rendered = _render_vars(monkeypatch, "pr2100v1")

    assert rendered["osac_instance_id"] == "pr2100v1"
    assert rendered["osac_instance_labels"] == {INSTANCE_LABEL: "pr2100v1"}


def test_without_a_run_id_the_label_var_is_empty(monkeypatch):
    rendered = _render_vars(monkeypatch, None)

    assert rendered["osac_instance_id"] == ""
    assert rendered["osac_instance_labels"] == {}


def test_the_storage_class_prefix_carries_the_run_id():
    templar = _templar_with_instance("pr2100v1")

    csi = yaml.safe_load(CSI_DEFAULTS.read_text())
    assert templar.template(csi["csi_driver_install_storage_class_prefix"]) == "osac-pr2100v1-"

    lvms = yaml.safe_load(LVMS_DEFAULTS.read_text())
    assert templar.template(lvms["lvms_storage_storage_class_prefix"]) == "osac-pr2100v1-"
    # The tenant teardown selects by the instance label, so it never deletes
    # another run's classes with the same tenant name.
    assert templar.template(lvms["lvms_storage_instance_selectors"]) == [
        "osac.openshift.io/instance=pr2100v1"
    ]


def test_without_a_run_id_the_storage_class_prefix_stays_osac():
    templar = _templar_with_instance("")

    csi = yaml.safe_load(CSI_DEFAULTS.read_text())
    assert templar.template(csi["csi_driver_install_storage_class_prefix"]) == "osac-"

    lvms = yaml.safe_load(LVMS_DEFAULTS.read_text())
    assert templar.template(lvms["lvms_storage_storage_class_prefix"]) == "osac-"
    assert templar.template(lvms["lvms_storage_instance_selectors"]) == []
