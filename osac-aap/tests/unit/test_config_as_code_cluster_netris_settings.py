"""CaaS Netris settings from the config-as-code Secret to cluster fulfillment.

The installer writes NETRIS_CONTROLLER_URL, NETRIS_VALIDATE_CERTS and
NETRIS_CA_CONFIGMAP into the config-as-code Secret. Config-as-code resolves them
in playbooks/vars/config.yml and renders them into the cluster-fulfillment
container group, which runs the CaaS Netris steps (NETWORK_CLASS=netris).
"""

from pathlib import Path

import pytest
import yaml
from ansible.parsing.dataloader import DataLoader
from ansible.template import Templar

CONFIG_AS_CODE = (
    Path(__file__).parents[2] / "collections" / "ansible_collections" / "osac" / "config_as_code"
)
CONFIG_VARS = CONFIG_AS_CODE / "playbooks" / "vars" / "config.yml"
CLUSTER_TEMPLATE = CONFIG_AS_CODE / "roles" / "aap" / "templates" / "cluster-fulfillment-ig.j2"
NETRIS_ENV = ("NETRIS_CONTROLLER_URL", "NETRIS_VALIDATE_CERTS", "NETRIS_CA_CONFIGMAP")


def render_cluster_pod(monkeypatch, environment):
    for name in NETRIS_ENV:
        monkeypatch.delenv(name, raising=False)
    for name, value in environment.items():
        monkeypatch.setenv(name, value)
    variables = yaml.safe_load(CONFIG_VARS.read_text())
    variables["aap_ee_image"] = "example.invalid/osac-ee:test"
    templar = Templar(loader=DataLoader(), variables=variables)
    pod = yaml.safe_load(templar.template(CLUSTER_TEMPLATE.read_text()))
    worker = pod["spec"]["containers"][0]
    env = {item["name"]: item.get("value") for item in worker.get("env", [])}
    return pod, worker, env


def test_cluster_fulfillment_receives_the_installation_netris_settings(monkeypatch):
    pod, worker, env = render_cluster_pod(
        monkeypatch,
        {
            "NETRIS_CONTROLLER_URL": "https://netris.example.com",
            "NETRIS_VALIDATE_CERTS": "false",
            "NETRIS_CA_CONFIGMAP": "customer-netris-ca",
        },
    )

    assert env["NETRIS_CONTROLLER_URL"] == "https://netris.example.com"
    assert env["NETRIS_VALIDATE_CERTS"] == "false"
    assert env["NETRIS_CA_PATH"] == "/etc/netris/ca/bundle.pem"
    assert env["ANSIBLE_STRATEGY"] == "netris.controller.protected_linear"
    assert env["OSAC_LAUNCH_EXTRA_VARS_PATH"] == "/runner/env/extravars"
    assert {"secretRef": {"name": "netris-credentials", "optional": True}} in worker["envFrom"]
    ca_mount = next(m for m in worker["volumeMounts"] if m["name"] == "netris-ca-bundle")
    assert ca_mount == {
        "name": "netris-ca-bundle",
        "mountPath": "/etc/netris/ca/bundle.pem",
        "subPath": "bundle.pem",
        "readOnly": True,
    }
    ca_volume = next(v for v in pod["spec"]["volumes"] if v["name"] == "netris-ca-bundle")
    assert ca_volume["configMap"]["name"] == "customer-netris-ca"
    assert ca_volume["configMap"]["optional"] is False


@pytest.mark.parametrize("validate_certs", [None, "true"])
def test_cluster_fulfillment_defaults_without_netris(monkeypatch, validate_certs):
    environment = {} if validate_certs is None else {"NETRIS_VALIDATE_CERTS": validate_certs}
    pod, worker, env = render_cluster_pod(monkeypatch, environment)

    assert env["NETRIS_CONTROLLER_URL"] == ""
    assert env["NETRIS_VALIDATE_CERTS"] == "true"
    assert env["NETRIS_CA_PATH"] == ""
    assert all(v["name"] != "netris-ca-bundle" for v in pod["spec"]["volumes"])
    inventory = next(v for v in pod["spec"]["volumes"] if v["name"] == "agentless-net-inventory")
    assert inventory["configMap"] == {"name": "agentless-net-inventory", "optional": True}
    assert {
        "name": "agentless-net-inventory",
        "mountPath": "/var/config/agentless-net",
        "readOnly": True,
    } in worker["volumeMounts"]
