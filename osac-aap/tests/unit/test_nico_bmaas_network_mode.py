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
        "target_machine_id": "",
        "network_virtualization_type": mode,
        "flat_ipv4_cidr": "172.16.200.0/24" if mode == "FLAT" else "",
        "subnet_id": "subnet-id" if mode == "ETHERNET_VIRTUALIZER" else "",
        "vpc_prefix_id": "prefix-id" if mode == "FNN" else "",
        "operating_system_id": "os-id",
        "user_data": "",
        "ssh_public_key": "",
        "phone_home_enabled": True,
        "instance_id": "",
        "machine_id": "",
        "create_allowed": True,
        "timeout": 1,
        "poll_interval": 1,
    }
    result.update(overrides)
    return result


def ready_operating_system(**overrides):
    definition = {
        "id": "os-id",
        "status": "Ready",
        "type": "TemplatedIpxe",
        "phoneHomeEnabled": True,
        "ipxeTemplateId": "qcow-template-id",
        "ipxeTemplateParameters": [
            {"name": "image_url", "value": "http://artifacts/centos.qcow2"},
            {"name": "ds", "value": "nocloud-net;s=${tenant-cloudinit-url}"},
        ],
        "siteAssociations": [{"site": {"id": "site-id"}, "status": "Synced"}],
    }
    definition.update(overrides)
    return definition


NO_DATASOURCE_PARAMETERS = [{"name": "image_url", "value": "http://artifacts/centos.qcow2"}]


class FakeClient:
    def __init__(self, parameters, interfaces=None, target_machine=None, ready_machine_id="machine-id",
                 operating_system=None, template_body="chain qcow-imager.efi image_url={{image_url}} {{extra}}"):
        self.params = parameters
        self.interfaces = interfaces
        self.target_machine = target_machine
        self.ready_machine_id = ready_machine_id
        self.operating_system = ready_operating_system() if operating_system is None else operating_system
        self.template_body = template_body
        self.posts = []

    def request(self, scope, method, resource, body=None):
        if (scope, method, resource) == ("provider", "GET", "machine/machine-id"):
            if self.target_machine is None:
                raise nico.NicoHTTPError(404, "not found")
            return 200, self.target_machine
        assert scope == "tenant"
        if (method, resource) == ("GET", "vpc/vpc-id"):
            return 200, {
                "tenantId": "nico-tenant",
                "siteId": "site-id",
                "networkVirtualizationType": self.params["network_virtualization_type"],
            }
        if (method, resource) == ("GET", "instance?name=osac-bmi-uid"):
            return 200, []
        if (method, resource) == ("GET", "operating-system/os-id"):
            if self.operating_system is False:
                raise nico.NicoHTTPError(404, "not found")
            return 200, self.operating_system
        if (method, resource) == ("GET", "ipxe-template/qcow-template-id"):
            return 200, {"id": "qcow-template-id", "template": self.template_body}
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
                "machineId": self.ready_machine_id,
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
    assert client.posts[0]["instanceTypeId"] == "instance-type-id"
    assert "machineId" not in client.posts[0]
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


def ready_target_machine(**overrides):
    machine = {"siteId": "site-id", "status": "Ready", "instanceId": None}
    machine.update(overrides)
    return machine


def targeted_fnn_params(**overrides):
    return params("FNN", instance_type_id="", target_machine_id="machine-id", **overrides)


def test_targeted_create_pins_only_the_approved_machine():
    client = FakeClient(targeted_fnn_params(),
                        interfaces=[{"vpcPrefixId": "prefix-id", "ipAddresses": ["198.51.100.20"]}],
                        target_machine=ready_target_machine())

    result = nico.ensure_present(client)

    assert result["machine_id"] == "machine-id"
    assert client.posts[0]["machineId"] == "machine-id"
    assert "instanceTypeId" not in client.posts[0]
    assert client.posts[0]["interfaces"] == [{"isPhysical": True, "vpcPrefixId": "prefix-id"}]


@pytest.mark.parametrize(
    "target_machine",
    [None,
     ready_target_machine(siteId="another-site"),
     ready_target_machine(status="Error"),
     ready_target_machine(instanceId="another-instance")],
)
def test_targeted_create_refuses_unavailable_or_wrong_site_machine(target_machine):
    client = FakeClient(targeted_fnn_params(), target_machine=target_machine)

    with pytest.raises(nico.NicoError):
        nico.ensure_present(client)

    assert client.posts == []


@pytest.mark.parametrize("instance_type_id,target_machine_id", [("", ""), ("instance-type-id", "machine-id")])
def test_create_requires_one_placement_selector(instance_type_id, target_machine_id):
    client = FakeClient(params("FNN", instance_type_id=instance_type_id,
                               target_machine_id=target_machine_id))

    with pytest.raises(nico.NicoError, match="exactly one"):
        nico.ensure_present(client)

    assert client.posts == []


def test_create_boots_only_the_approved_operating_system():
    client = FakeClient(params("FNN"),
                        interfaces=[{"vpcPrefixId": "prefix-id", "ipAddresses": ["198.51.100.20"]}])

    nico.ensure_present(client)

    assert client.posts[0]["operatingSystemId"] == "os-id"
    assert "ipxeScript" not in client.posts[0]


def test_create_requires_an_operating_system():
    client = FakeClient(params("FNN", operating_system_id=""))

    with pytest.raises(nico.NicoError, match="operating_system_id is required"):
        nico.ensure_present(client)

    assert client.posts == []


@pytest.mark.parametrize(
    ("operating_system", "message"),
    [
        (False, "not found"),
        (ready_operating_system(status="Syncing"), "is not Ready"),
        (ready_operating_system(type="Ipxe"), "not a templated iPXE definition"),
        (ready_operating_system(ipxeTemplateParameters=NO_DATASOURCE_PARAMETERS), "does not deliver user data"),
        (ready_operating_system(ipxeTemplateParameters=NO_DATASOURCE_PARAMETERS, ipxeTemplateId=None),
         "does not deliver user data"),
        (ready_operating_system(siteAssociations=[]), "not synced to the VPC's Site"),
        (ready_operating_system(siteAssociations=[{"site": {"id": "site-id"}, "status": "Syncing"}]),
         "not synced to the VPC's Site"),
        (ready_operating_system(siteAssociations=[{"site": {"id": "another-site"}, "status": "Synced"}]),
         "not synced to the VPC's Site"),
    ],
)
def test_create_refuses_an_unusable_operating_system_before_post(operating_system, message):
    client = FakeClient(params("FNN"), operating_system=operating_system)

    with pytest.raises(nico.NicoError, match=message):
        nico.ensure_present(client)

    assert client.posts == []


def test_create_accepts_a_template_that_embeds_the_cloud_init_datasource():
    # For example ubuntu-autoinstall carries ds=nocloud-net in its template body.
    client = FakeClient(params("FNN"),
                        operating_system=ready_operating_system(ipxeTemplateParameters=NO_DATASOURCE_PARAMETERS),
                        template_body="kernel {{kernel}} autoinstall ds=nocloud-net;s=${tenant-cloudinit-url}",
                        interfaces=[{"vpcPrefixId": "prefix-id", "ipAddresses": ["198.51.100.20"]}])

    nico.ensure_present(client)

    assert len(client.posts) == 1


def test_create_accepts_an_operating_system_without_phone_home():
    # The Instance phoneHomeEnabled overrides the OS value in NICo.
    client = FakeClient(params("FNN"), operating_system=ready_operating_system(phoneHomeEnabled=False),
                        interfaces=[{"vpcPrefixId": "prefix-id", "ipAddresses": ["198.51.100.20"]}])

    nico.ensure_present(client)

    assert client.posts[0]["phoneHomeEnabled"] is True


@pytest.mark.parametrize("enabled", [True, False])
def test_create_sends_the_configured_phone_home_setting(enabled):
    client = FakeClient(params("FNN", phone_home_enabled=enabled),
                        interfaces=[{"vpcPrefixId": "prefix-id", "ipAddresses": ["198.51.100.20"]}])

    nico.ensure_present(client)

    assert client.posts[0]["phoneHomeEnabled"] is enabled


def test_targeted_create_rejects_a_different_machine_in_ready_response():
    client = FakeClient(targeted_fnn_params(),
                        target_machine=ready_target_machine(),
                        ready_machine_id="another-machine")

    with pytest.raises(nico.NicoError, match="configured target Machine"):
        nico.ensure_present(client)

    assert len(client.posts) == 1
