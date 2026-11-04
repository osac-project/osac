"""NICo BMI network-mode contract at the REST boundary."""

import importlib.util
from pathlib import Path

import pytest


MODULE_PATH = (
    Path(__file__).resolve().parents[2]
    / "collections/ansible_collections/nico/steps/plugins/modules/bare_metal_instance.py"
)
SPEC = importlib.util.spec_from_file_location("nico_bare_metal_instance", MODULE_PATH)
nico = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(nico)


def params(mode="FLAT", **overrides):
    result = {
        "name": "osac-bmi-uid",
        "tenant_id": "nico-tenant",
        "bmi_uid": "bmi-uid",
        "vpc_id": "vpc-id",
        "instance_type_id": "instance-type-id",
        "network_virtualization_type": mode,
        "flat_ipv4_cidr": "172.16.200.0/24" if mode == "FLAT" else "",
        "subnet_id": "subnet-id" if mode == "ETHERNET_VIRTUALIZER" else "",
        "vpc_prefix_id": "prefix-id" if mode == "FNN" else "",
        "operating_system_id": "",
        "ipxe_script": "#!ipxe\nexit",
        "instance_id": "",
        "machine_id": "",
        "create_allowed": True,
        "timeout": 1,
        "poll_interval": 1,
    }
    result.update(overrides)
    return result


class FakeClient:
    def __init__(self, parameters, interfaces=None):
        self.params = parameters
        self.interfaces = interfaces
        self.posts = []

    def request(self, scope, method, resource, body=None):
        assert scope == "tenant"
        if (method, resource) == ("GET", "vpc/vpc-id"):
            return 200, {
                "tenantId": "nico-tenant",
                "networkVirtualizationType": self.params["network_virtualization_type"],
            }
        if (method, resource) == ("GET", "instance?name=osac-bmi-uid"):
            return 200, []
        if (method, resource) == ("POST", "instance"):
            self.posts.append(body)
            return 201, {"id": "instance-id"}
        if (method, resource) == ("GET", "instance/instance-id"):
            return 200, {
                "id": "instance-id",
                "status": "Ready",
                "tenantId": "nico-tenant",
                "vpcId": "vpc-id",
                "instanceTypeId": "instance-type-id",
                "machineId": "machine-id",
                "labels": {"osacBmiUid": "bmi-uid"},
                "autoNetwork": self.params["network_virtualization_type"] == "FLAT",
                "interfaces": self.interfaces or [],
            }
        raise AssertionError((scope, method, resource))


def test_flat_create_uses_auto_network_and_selects_the_bound_address():
    client = FakeClient(params(), interfaces=[
        {"ipAddresses": ["192.0.2.10"]},
        {"ipAddresses": ["172.16.200.42"]},
    ])

    result = nico.ensure_present(client)

    assert result["ip_address"] == "172.16.200.42"
    assert client.posts[0]["autoNetwork"] is True
    assert "interfaces" not in client.posts[0]


@pytest.mark.parametrize(
    ("mode", "network_key", "network_id"),
    [("ETHERNET_VIRTUALIZER", "subnetId", "subnet-id"),
     ("FNN", "vpcPrefixId", "prefix-id")],
)
def test_dpu_modes_keep_an_explicit_physical_interface(mode, network_key, network_id):
    client = FakeClient(params(mode), interfaces=[{
        network_key: network_id,
        "ipAddresses": ["198.51.100.20"],
    }])

    result = nico.ensure_present(client)

    assert result["ip_address"] == "198.51.100.20"
    assert client.posts[0]["interfaces"] == [{"isPhysical": True, network_key: network_id}]
    assert "autoNetwork" not in client.posts[0]


@pytest.mark.parametrize(
    "overrides",
    [
        {"subnet_id": "subnet-id"},
        {"vpc_prefix_id": "prefix-id"},
        {"flat_ipv4_cidr": ""},
        {"flat_ipv4_cidr": "172.16.200.9/24"},
        {"flat_ipv4_cidr": "2001:db8::/64"},
    ],
)
def test_flat_rejects_inconsistent_network_binding_before_post(overrides):
    client = FakeClient(params(**overrides))

    with pytest.raises(nico.NicoError):
        nico.ensure_present(client)

    assert client.posts == []


def test_flat_requires_one_unambiguous_ip_in_the_bound_cidr():
    client = FakeClient(params(), interfaces=[
        {"ipAddresses": ["172.16.200.41"]},
        {"ipAddresses": ["172.16.200.42"]},
    ])

    with pytest.raises(nico.NicoError, match="no unique address"):
        nico.ensure_present(client)


def test_flat_rejects_an_instance_without_auto_network():
    client = FakeClient(params(), interfaces=[{"ipAddresses": ["172.16.200.42"]}])
    original = client.request

    def response(scope, method, resource, body=None):
        code, value = original(scope, method, resource, body)
        if (method, resource) == ("GET", "instance/instance-id"):
            value["autoNetwork"] = False
        return code, value

    client.request = response

    with pytest.raises(nico.NicoError, match="autoNetwork true"):
        nico.ensure_present(client)


@pytest.mark.parametrize(
    ("field", "value"),
    [("tenantId", "another-tenant"),
     ("networkVirtualizationType", "FNN")],
)
def test_flat_rejects_wrong_vpc_before_post(field, value):
    client = FakeClient(params())
    original = client.request

    def response(scope, method, resource, body=None):
        code, result = original(scope, method, resource, body)
        if (method, resource) == ("GET", "vpc/vpc-id"):
            result[field] = value
        return code, result

    client.request = response

    with pytest.raises(nico.NicoError):
        nico.ensure_present(client)

    assert client.posts == []
