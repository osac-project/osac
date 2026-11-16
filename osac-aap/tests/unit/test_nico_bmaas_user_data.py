"""NICo BMI tenant user data and SSH key composition."""

import importlib.util
from pathlib import Path

import pytest
import yaml

# Reuse the NICo REST fake and the loaded module from the network-mode tests,
# so both files share one module instance and its exception classes.
_SPEC = importlib.util.spec_from_file_location(
    "nico_bmaas_network_mode_tests", Path(__file__).with_name("test_nico_bmaas_network_mode.py"))
_network_mode = importlib.util.module_from_spec(_SPEC)
_SPEC.loader.exec_module(_network_mode)
FakeClient, nico, params = _network_mode.FakeClient, _network_mode.nico, _network_mode.params

KEY = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample tenant@example.com"


def parse(user_data):
    assert user_data.startswith("#cloud-config\n")
    return yaml.safe_load(user_data)


def test_nothing_to_send_lets_nico_generate_its_minimal_cloud_config():
    assert nico.compose_user_data("", "") is None


def test_ssh_key_only_builds_a_minimal_cloud_config():
    assert parse(nico.compose_user_data("", KEY)) == {"ssh_authorized_keys": [KEY]}


def test_tenant_cloud_config_keeps_its_content_and_gains_the_key():
    tenant = "#cloud-config\npackages: [htop]\nssh_authorized_keys:\n  - ssh-rsa AAAA other@example.com\n"

    document = parse(nico.compose_user_data(tenant, KEY))

    assert document["packages"] == ["htop"]
    assert document["ssh_authorized_keys"] == ["ssh-rsa AAAA other@example.com", KEY]


def test_the_key_is_not_duplicated():
    tenant = f"#cloud-config\nssh_authorized_keys: ['{KEY}']\n"

    assert parse(nico.compose_user_data(tenant, KEY))["ssh_authorized_keys"] == [KEY]


def test_tenant_users_keep_the_default_user_for_the_osac_key():
    tenant = "#cloud-config\nusers:\n  - name: app\n    groups: [wheel]\n"

    document = parse(nico.compose_user_data(tenant, KEY))

    assert document["users"][0] == "default"
    assert document["users"][1]["name"] == "app"


def test_tenant_users_with_default_are_left_unchanged():
    tenant = "#cloud-config\nusers: [default, {name: app}]\n"

    assert parse(nico.compose_user_data(tenant, KEY))["users"] == ["default", {"name": "app"}]


def test_tenant_cloud_config_without_a_key_is_sent_as_is():
    tenant = "#cloud-config\nruncmd: [[touch, /tmp/x]]\n"

    assert parse(nico.compose_user_data(tenant, "")) == {"runcmd": [["touch", "/tmp/x"]]}


def test_an_empty_cloud_config_is_accepted():
    assert parse(nico.compose_user_data("#cloud-config\n", KEY)) == {"ssh_authorized_keys": [KEY]}


@pytest.mark.parametrize(
    ("user_data", "message"),
    [
        ("#!/bin/bash\necho hi\n", "must start with #cloud-config"),
        ("## template: jinja\n#cloud-config\n", "must start with #cloud-config"),
        ("#cloud-config-archive\n- type: text/cloud-config\n", "must start with #cloud-config"),
        ("packages: [htop]\n", "must start with #cloud-config"),
        ("#cloud-config\npackages: [htop\n", "not valid cloud-config YAML"),
        ("#cloud-config\na: 1\n---\nb: 2\n", "single cloud-config YAML mapping"),
        ("#cloud-config\n- item\n", "single cloud-config YAML mapping"),
        ("#cloud-config\nssh_authorized_keys: not-a-list\n", "ssh_authorized_keys must be a list"),
        ("#cloud-config\nusers: app\n", "users must be a list"),
    ],
)
def test_unsupported_user_data_is_rejected(user_data, message):
    with pytest.raises(nico.NicoError, match=message):
        nico.compose_user_data(user_data, KEY)


def test_create_sends_the_composed_user_data():
    client = FakeClient(params("FNN", ssh_public_key=KEY, user_data="#cloud-config\npackages: [htop]\n"),
                        interfaces=[{"vpcPrefixId": "prefix-id", "ipAddresses": ["198.51.100.20"]}])

    nico.ensure_present(client)

    assert parse(client.posts[0]["userData"]) == {"packages": ["htop"], "ssh_authorized_keys": [KEY]}


def test_create_without_key_or_user_data_sends_no_user_data():
    client = FakeClient(params("FNN"),
                        interfaces=[{"vpcPrefixId": "prefix-id", "ipAddresses": ["198.51.100.20"]}])

    nico.ensure_present(client)

    assert "userData" not in client.posts[0]


def test_invalid_user_data_is_rejected_before_any_nico_call():
    client = FakeClient(params("FNN", user_data="#!/bin/bash\n"))
    calls = []
    original = client.request

    def recording(scope, method, resource, body=None):
        calls.append((scope, method, resource))
        return original(scope, method, resource, body)

    client.request = recording

    with pytest.raises(nico.NicoError, match="must start with #cloud-config"):
        nico.ensure_present(client)

    assert calls == []
