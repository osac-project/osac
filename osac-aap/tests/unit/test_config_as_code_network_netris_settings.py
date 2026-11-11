"""Netris CA trust reaches the networking operations workers.

Config-as-code resolves NETRIS_CA_CONFIGMAP in playbooks/vars/config.yml and
renders the optional CA bundle mount into the networking-operations container
group, which runs the Fabric Manager tasks.
"""

from .config_as_code_render import CONFIG_AS_CODE, render_pod

NETWORKING_TEMPLATE = CONFIG_AS_CODE / "roles" / "aap" / "templates" / "networking-operations-ig.j2"


def test_networking_fulfillment_mounts_the_configured_netris_ca(monkeypatch):
    pod, worker, env = render_pod(
        monkeypatch,
        NETWORKING_TEMPLATE,
        {"NETRIS_CA_CONFIGMAP": "customer-netris-ca"},
    )

    assert env["NETRIS_CA_PATH"] == "/etc/netris/ca/bundle.pem"
    assert "NETRIS_CONTROLLER_URL" not in env
    assert "NETRIS_VALIDATE_CERTS" not in env
    assert "ANSIBLE_STRATEGY" not in env
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

    assert "NETRIS_CA_PATH" not in env
    assert all(v["name"] != "netris-ca-bundle" for v in pod["spec"]["volumes"])
    assert all(m["name"] != "netris-ca-bundle" for m in worker["volumeMounts"])
