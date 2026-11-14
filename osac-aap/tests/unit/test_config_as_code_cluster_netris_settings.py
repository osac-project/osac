"""CaaS Netris settings from the config-as-code Secret to cluster fulfillment.

The installer writes NETRIS_CONTROLLER_URL, NETRIS_VALIDATE_CERTS and
NETRIS_CA_CONFIGMAP into the config-as-code Secret. Config-as-code resolves them
in playbooks/vars/config.yml and renders them into the cluster-fulfillment
container group, which runs the CaaS Netris steps (NETWORK_CLASS=netris).
"""

import pytest

from .config_as_code_render import CONFIG_AS_CODE, render_pod

CLUSTER_TEMPLATE = CONFIG_AS_CODE / "roles" / "aap" / "templates" / "cluster-fulfillment-ig.j2"


def test_cluster_fulfillment_receives_the_installation_netris_settings(monkeypatch):
    pod, worker, env = render_pod(
        monkeypatch,
        CLUSTER_TEMPLATE,
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
    pod, worker, env = render_pod(monkeypatch, CLUSTER_TEMPLATE, environment)

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
