"""The config-as-code vcluster settings: credential, groups and pod specs.

With VCLUSTER_API_URL set, the shared AAP creates this run's job pods inside
the run's vcluster: config-as-code creates one bearer-token credential for
that API, attaches it to every container group, and points every group's pod
spec at the release namespace inside the vcluster. Without it none of that
exists. The render mirrors include_vars: config.yml and controller.yml load
as YAML first, then a real Ansible Templar resolves the values lazily, so
sibling keys (aap_prefix inside aap_vcluster_credential_name) resolve the
way they do in a live run.
"""

from pathlib import Path

import yaml
from ansible.parsing.dataloader import DataLoader
from ansible.template import Templar

CONFIG_AS_CODE = (
    Path(__file__).parents[2] / "collections" / "ansible_collections" / "osac" / "config_as_code"
)
CONFIG_VARS = CONFIG_AS_CODE / "playbooks" / "vars" / "config.yml"
CONTROLLER_VARS = CONFIG_AS_CODE / "roles" / "aap" / "vars" / "controller.yml"
TEMPLATES = CONFIG_AS_CODE / "roles" / "aap" / "templates"

API_URL = "https://pr2100v1.vc-pr2100v1.svc.cluster.local"
TOKEN_SECRET = {"name": "pr2100v1-vcluster-token", "key": "token"}

VCLUSTER_ENV = {
    "NAMESPACE": "osac",
    "AAP_PASSWORD": "not-read-here",
    "VCLUSTER_API_URL": API_URL,
    "VCLUSTER_TOKEN": "run-token",
    "VCLUSTER_TOKEN_SECRET_NAME": TOKEN_SECRET["name"],
    "VCLUSTER_TOKEN_SECRET_KEY": TOKEN_SECRET["key"],
}

BASE_ENV = {"NAMESPACE": "osac", "AAP_PASSWORD": "not-read-here"}


def render_controller(monkeypatch, environment):
    # Keep the run's settings hermetic: a developer shell exporting
    # AAP_ORGANIZATION_NAME or OSAC_INSTANCE_ID must not change the result.
    for name in (
        "NAMESPACE",
        "AAP_INSTANCE_NAME",
        "AAP_HOSTNAME",
        "AAP_USERNAME",
        "AAP_PASSWORD",
        "AAP_VALIDATE_CERTS",
        "AAP_ORGANIZATION_NAME",
        "AAP_PREFIX",
        "AAP_PROJECT_NAME",
        "OSAC_INSTANCE_ID",
        "VCLUSTER_API_URL",
        "VCLUSTER_TOKEN",
        "VCLUSTER_TOKEN_SECRET_NAME",
        "VCLUSTER_TOKEN_SECRET_KEY",
        "NETRIS_CA_CONFIGMAP",
    ):
        monkeypatch.delenv(name, raising=False)
    for name, value in environment.items():
        monkeypatch.setenv(name, value)

    variables = {
        **yaml.safe_load(CONFIG_VARS.read_text()),
        **yaml.safe_load(CONTROLLER_VARS.read_text()),
    }
    templar = Templar(loader=DataLoader(), variables=variables)
    real_lookup = templar.environment.globals["lookup"]

    def template_lookup(lookup_type, *args, **kwargs):
        """The template lookup as real Ansible runs it: one flattened string.

        A mock returning a list would hide a `| first` pipe at the call
        site (first would then yield a single character); the string
        return and the multi-line assertions below catch it.
        """
        if lookup_type.rsplit(".", 1)[-1] != "template":
            return real_lookup(lookup_type, *args, **kwargs)
        sub_templar = Templar(loader=DataLoader(), variables=dict(variables))
        sub_templar.environment.globals["lookup"] = real_lookup
        result = sub_templar.template((TEMPLATES / args[0]).read_text())
        assert isinstance(result, str), "the template lookup returns a string"
        return result

    templar.environment.globals["lookup"] = template_lookup

    # Only the keys under test: aap_project_archive_uri is Undefined by
    # design when its env var is unset, like in a live run.
    wanted = (
        "osac_instance_id",
        "aap_organization_name",
        "aap_prefix",
        "aap_vcluster_credential_name",
        "controller_credentials",
        "controller_instance_groups",
    )
    return {key: templar.template(variables[key]) for key in wanted}


def groups_of(controller):
    return controller["controller_instance_groups"]


def pod_spec(group):
    spec = group["pod_spec_override"]
    assert isinstance(spec, str) and len(spec) > 1, group["name"]
    return yaml.safe_load(spec)


def test_without_the_vcluster_url_no_credential_exists(monkeypatch):
    controller = render_controller(monkeypatch, BASE_ENV)

    assert controller["controller_credentials"] == []
    assert controller["aap_vcluster_credential_name"] == ""
    assert all(group["credential"] == "" for group in groups_of(controller))


def test_without_the_vcluster_url_the_two_bare_groups_keep_no_pod_spec(monkeypatch):
    controller = render_controller(monkeypatch, BASE_ENV)
    groups = {group["name"]: group for group in groups_of(controller)}

    assert groups["osac-create-hosted-cluster-post-install-ig"]["pod_spec_override"] == ""
    assert groups["osac-addon-operators-ig"]["pod_spec_override"] == ""


def test_without_the_vcluster_url_no_pod_spec_names_a_namespace(monkeypatch):
    controller = render_controller(monkeypatch, BASE_ENV)

    spec_groups = [g for g in groups_of(controller) if g["pod_spec_override"]]
    assert len(spec_groups) == 8
    for group in spec_groups:
        assert "namespace" not in pod_spec(group)["metadata"], group["name"]


def test_with_the_vcluster_url_the_credential_targets_the_vcluster(monkeypatch):
    controller = render_controller(monkeypatch, VCLUSTER_ENV)

    assert controller["controller_credentials"] == [
        {
            "name": "osac-vcluster",
            "description": "Bearer token for the vcluster API of osac",
            "organization": "osac",
            "credential_type": "OpenShift or Kubernetes API Bearer Token",
            "inputs": {
                "host": API_URL,
                "bearer_token": "run-token",
                "verify_ssl": False,
            },
        }
    ]
    assert controller["aap_vcluster_credential_name"] == "osac-vcluster"


def test_with_the_vcluster_url_every_group_carries_the_credential(monkeypatch):
    controller = render_controller(monkeypatch, VCLUSTER_ENV)
    groups = groups_of(controller)

    assert len(groups) == 10
    assert all(group["credential"] == "osac-vcluster" for group in groups)


def test_with_the_vcluster_url_every_pod_spec_names_the_release_namespace(monkeypatch):
    controller = render_controller(monkeypatch, VCLUSTER_ENV)

    for group in groups_of(controller):
        assert pod_spec(group)["metadata"]["namespace"] == "osac", group["name"]


def test_with_the_vcluster_url_the_bare_groups_get_the_minimal_spec(monkeypatch):
    controller = render_controller(monkeypatch, VCLUSTER_ENV)
    groups = {group["name"]: group for group in groups_of(controller)}

    for name in (
        "osac-create-hosted-cluster-post-install-ig",
        "osac-addon-operators-ig",
    ):
        spec = pod_spec(groups[name])
        assert spec["spec"]["serviceAccountName"] == "osac-sa"
        assert spec["metadata"]["namespace"] == "osac"
        assert spec["spec"]["containers"][0]["name"] == "worker"


def test_with_the_vcluster_url_the_rerun_pod_reads_the_token_secret(monkeypatch):
    controller = render_controller(monkeypatch, VCLUSTER_ENV)
    group = next(
        g for g in groups_of(controller) if g["name"] == "osac-config-as-code-ig"
    )

    env = {item["name"]: item for item in pod_spec(group)["spec"]["containers"][0]["env"]}
    assert env["VCLUSTER_TOKEN"]["valueFrom"]["secretKeyRef"] == TOKEN_SECRET


# The groups whose pods run the provisioning roles, by suffix: with a run ID
# set the prefix (and so every group name) follows it. The config-as-code
# group reads OSAC_INSTANCE_ID from the chart Secret through envFrom instead.
ROLE_RUNNING_GROUP_SUFFIXES = (
    "-cluster-fulfillment-ig",
    "-compute-instance-operations-ig",
    "-networking-operations-ig",
    "-storage-operations-ig",
    "-bare-metal-fulfillment-ig",
    "-create-hosted-cluster-post-install-ig",
    "-addon-operators-ig",
)


def _worker_env(pod):
    worker = pod["spec"]["containers"][0]
    return {item["name"]: item.get("value") for item in worker.get("env", [])}


def test_with_a_run_id_every_role_running_pod_carries_it(monkeypatch):
    """The labeling roles read OSAC_INSTANCE_ID from their own process env,
    so every group that runs them must bake it into its pod spec."""
    controller = render_controller(
        monkeypatch, {**VCLUSTER_ENV, "OSAC_INSTANCE_ID": "pr2100v1"}
    )

    for suffix in ROLE_RUNNING_GROUP_SUFFIXES:
        group = next(g for g in groups_of(controller) if g["name"].endswith(suffix))
        env = _worker_env(pod_spec(group))
        assert env["OSAC_INSTANCE_ID"] == "pr2100v1", group["name"]


def test_without_a_run_id_no_pod_spec_carries_the_instance_env(monkeypatch):
    controller = render_controller(monkeypatch, VCLUSTER_ENV)

    for group in groups_of(controller):
        assert "OSAC_INSTANCE_ID" not in _worker_env(pod_spec(group)), group["name"]


def test_template_based_pod_specs_stay_single_strings(monkeypatch):
    """The plain lookup returns one string; `| first` would yield one char."""
    controller = render_controller(monkeypatch, VCLUSTER_ENV)

    for name in (
        "osac-cluster-fulfillment-ig",
        "osac-compute-instance-operations-ig",
        "osac-networking-operations-ig",
        "osac-storage-operations-ig",
    ):
        group = next(g for g in groups_of(controller) if g["name"] == name)
        assert group["pod_spec_override"].startswith("apiVersion: v1"), name


def test_the_run_id_names_the_organization_and_the_prefix(monkeypatch):
    controller = render_controller(
        monkeypatch, {**BASE_ENV, "OSAC_INSTANCE_ID": "pr2100v1"}
    )

    assert controller["aap_organization_name"] == "pr2100v1"
    assert controller["aap_prefix"] == "pr2100v1"
    assert controller["osac_instance_id"] == "pr2100v1"


def test_without_a_run_id_the_organization_stays_osac(monkeypatch):
    controller = render_controller(monkeypatch, BASE_ENV)

    assert controller["osac_instance_id"] == ""
    assert controller["aap_organization_name"] == "osac"
    assert controller["aap_prefix"] == "osac"


def test_an_explicit_organization_still_wins(monkeypatch):
    controller = render_controller(
        monkeypatch,
        {**BASE_ENV, "OSAC_INSTANCE_ID": "pr2100v1", "AAP_ORGANIZATION_NAME": "custom-org"},
    )

    assert controller["aap_organization_name"] == "custom-org"
    assert controller["aap_prefix"] == "custom-org"


def test_the_run_id_names_the_credential_with_the_prefix(monkeypatch):
    controller = render_controller(
        monkeypatch, {**VCLUSTER_ENV, "OSAC_INSTANCE_ID": "pr2100v1"}
    )

    assert controller["aap_vcluster_credential_name"] == "pr2100v1-vcluster"
    assert controller["controller_credentials"][0]["name"] == "pr2100v1-vcluster"
    assert controller["controller_credentials"][0]["organization"] == "pr2100v1"
