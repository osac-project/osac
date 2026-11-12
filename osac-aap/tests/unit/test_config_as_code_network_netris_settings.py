"""Netris transport settings belong to the networking operations workers."""

from pathlib import Path

import yaml
from ansible.parsing.dataloader import DataLoader
from ansible.template import Templar

CONFIG_AS_CODE = (
    Path(__file__).parents[2] / "collections" / "ansible_collections" / "osac" / "config_as_code"
)
CONFIG_VARS = CONFIG_AS_CODE / "playbooks" / "vars" / "config.yml"
TEMPLATES = CONFIG_AS_CODE / "roles" / "aap" / "templates"
NETWORKING_TEMPLATE = TEMPLATES / "networking-operations-ig.j2"
CLUSTER_TEMPLATE = TEMPLATES / "cluster-fulfillment-ig.j2"


def render_pod(monkeypatch, template_path, environment):
    monkeypatch.delenv("NETRIS_CA_CONFIGMAP", raising=False)
    for name, value in environment.items():
        monkeypatch.setenv(name, value)
    variables = yaml.safe_load(CONFIG_VARS.read_text())
    variables["aap_ee_image"] = "example.invalid/osac-ee:test"
    templar = Templar(loader=DataLoader(), variables=variables)
    pod = yaml.safe_load(templar.template(template_path.read_text()))
    worker = pod["spec"]["containers"][0]
    env = {item["name"]: item.get("value") for item in worker.get("env", [])}
    return pod, worker, env


def test_networking_fulfillment_mounts_the_configured_netris_ca(monkeypatch):
    pod, worker, env = render_pod(
        monkeypatch,
        NETWORKING_TEMPLATE,
        {"NETRIS_CA_CONFIGMAP": "customer-netris-ca"},
    )

    assert env["NETRIS_CA_PATH"] == "/etc/netris/ca/bundle.pem"
    assert "ANSIBLE_STRATEGY" not in env
    assert env["OSAC_LAUNCH_EXTRA_VARS_PATH"] == "/runner/env/extravars"
    assert {"configMapRef": {"name": "network-fulfillment-ig", "optional": True}} in worker["envFrom"]
    assert {"secretRef": {"name": "network-fulfillment-ig"}} in worker["envFrom"]

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


def test_networking_fulfillment_without_a_netris_ca(monkeypatch):
    pod, worker, env = render_pod(monkeypatch, NETWORKING_TEMPLATE, {})

    assert env["NETRIS_CA_PATH"] == ""
    assert "ANSIBLE_STRATEGY" not in env
    assert all(v["name"] != "netris-ca-bundle" for v in pod["spec"]["volumes"])
    assert all(m["name"] != "netris-ca-bundle" for m in worker["volumeMounts"])


def test_cluster_fulfillment_does_not_receive_manager_settings(monkeypatch):
    pod, worker, _ = render_pod(
        monkeypatch,
        CLUSTER_TEMPLATE,
        {"NETRIS_CA_CONFIGMAP": "customer-netris-ca"},
    )

    assert "NETRIS_" not in str(pod)
    assert "netris-credentials" not in str(pod)
    assert "agentless-net-inventory" not in str(pod)
    assert {"configMapRef": {"name": "cluster-fulfillment-ig", "optional": True}} in worker["envFrom"]
    assert {"secretRef": {"name": "cluster-fulfillment-ig"}} in worker["envFrom"]
