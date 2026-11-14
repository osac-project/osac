from __future__ import annotations

import base64
import subprocess
from pathlib import Path
from typing import Any, cast

import pytest

from tests.e2e.core.helpers import (
    HostRef,
    host_consumer_ref,
    host_hardware_macs,
    wait_for_host_allocated,
    wait_for_host_powered,
)
from tests.e2e.core.inventory import Metal3Inventory, NetBoxInventory, make_inventory_backend, netbox_settings_from_bmf
from tests.e2e.core.k8s_client import K8sClient

_NETBOX_ENV_VARS = (
    "OSAC_INVENTORY_BACKEND",
    "OSAC_NETBOX_URL",
    "OSAC_NETBOX_TOKEN",
    "OSAC_NETBOX_CA_FILE",
    "OSAC_NETBOX_INSECURE",
)


@pytest.fixture(autouse=True)
def _clean_env(monkeypatch: pytest.MonkeyPatch) -> None:
    for name in _NETBOX_ENV_VARS:
        monkeypatch.delenv(name, raising=False)


# Metal3 backend -----------------------------------------------------------------


class FakeMetal3K8s:
    """K8sClient stand-in covering the BMH queries the metal3 path uses."""

    def __init__(
        self,
        *,
        external_host_id: str = "host-inventory/bmh-1",
        state: str = "provisioned",
        image_url: str = "oci://quay.io/osac-project/fedora-cloud-bmi:44",
        consumer_ref: str = "bmi-uid-1",
        online: str = "true",
        powered_on: str = "true",
        nics: list[str] | None = None,
        available_count: int = 1,
    ) -> None:
        self.external_host_id = external_host_id
        self.state = state
        self.image_url = image_url
        self.consumer_ref = consumer_ref
        self.online = online
        self.powered_on = powered_on
        self.nics = nics if nics is not None else ["aa:bb:cc:dd:ee:01", "aa:bb:cc:dd:ee:02"]
        self.available_count = available_count
        self.count_calls: list[dict[str, Any]] = []

    def get_baremetal_instance_external_host_id(self, *, name: str) -> str:
        return self.external_host_id

    def count_bmhs_by_provisioning_state(self, *, bmh_namespace: str, states: set[str]) -> int:
        self.count_calls.append({"bmh_namespace": bmh_namespace, "states": states})
        return self.available_count

    def get_bmh_provisioning_state(self, *, name: str, bmh_namespace: str) -> str:
        return self.state

    def get_bmh_image_url(self, *, name: str, bmh_namespace: str) -> str:
        return self.image_url

    def get_bmh_consumer_ref(self, *, name: str, bmh_namespace: str) -> str:
        return self.consumer_ref

    def get_bmh_online(self, *, name: str, bmh_namespace: str) -> str:
        return self.online

    def get_bmh_powered_on(self, *, name: str, bmh_namespace: str) -> str:
        return self.powered_on

    def get_bmh_hardware_nics(self, *, name: str, bmh_namespace: str) -> list[str]:
        return self.nics


def _metal3(k8s: FakeMetal3K8s) -> Metal3Inventory:
    return Metal3Inventory(k8s=cast(K8sClient, k8s), bmh_namespace="host-inventory")


def test_metal3_host_for_bmi_returns_the_bmh_ref() -> None:
    backend = _metal3(FakeMetal3K8s(external_host_id="host-inventory/bmh-7"))

    assert backend.host_for_bmi(bmi_cr_name="bmi-cr") == HostRef(namespace="host-inventory", name="bmh-7")


def test_metal3_host_for_bmi_rejects_wrong_namespace() -> None:
    backend = _metal3(FakeMetal3K8s(external_host_id="other-ns/bmh-1"))

    with pytest.raises(AssertionError, match="landed in other-ns"):
        backend.host_for_bmi(bmi_cr_name="bmi-cr")


def test_metal3_host_for_bmi_rejects_malformed_host_id() -> None:
    backend = _metal3(FakeMetal3K8s(external_host_id="bmh-1"))

    with pytest.raises(AssertionError, match="namespace/name"):
        backend.host_for_bmi(bmi_cr_name="bmi-cr")


def test_metal3_default_host_selector_matches_the_bmh_label() -> None:
    backend = _metal3(FakeMetal3K8s())

    assert backend.default_host_selector() == {"osac.openshift.io/host-type": "default"}


def test_metal3_count_available_uses_available_and_ready_states() -> None:
    k8s = FakeMetal3K8s(available_count=3)
    backend = _metal3(k8s)

    assert backend.count_available() == 3
    assert k8s.count_calls == [{"bmh_namespace": "host-inventory", "states": {"available", "ready"}}]


def test_metal3_wait_for_released_requires_cleared_image_and_consumer() -> None:
    backend = _metal3(FakeMetal3K8s(state="available", image_url="", consumer_ref=""))

    backend.wait_for_released(host=HostRef(namespace="host-inventory", name="bmh-1"))

    backend2 = _metal3(FakeMetal3K8s(state="available", image_url="oci://leftover:1", consumer_ref=""))
    with pytest.raises(AssertionError, match="image not cleared"):
        backend2.wait_for_released(host=HostRef(namespace="host-inventory", name="bmh-1"))


def test_metal3_ensure_available_hosts_requires_at_least_one() -> None:
    backend = _metal3(FakeMetal3K8s(available_count=0))

    with pytest.raises(AssertionError, match="at least 1 available BMH"):
        backend.ensure_available_hosts()

    _metal3(FakeMetal3K8s(available_count=2)).ensure_available_hosts()


def test_metal3_close_is_a_no_op() -> None:
    _metal3(FakeMetal3K8s()).close()


# Shared provisioning helpers (BMH, backend-agnostic) ------------------------------


def test_wait_for_host_allocated_bundles_the_provisioned_assertions() -> None:
    k8s = FakeMetal3K8s(state="provisioned", image_url="oci://img:1", consumer_ref="bmi-1", online="true")

    wait_for_host_allocated(
        k8s=cast(K8sClient, k8s),
        host=HostRef(namespace="host-inventory", name="bmh-1"),
        expected_image_url="oci://img:1",
    )


def test_wait_for_host_allocated_rejects_wrong_image() -> None:
    k8s = FakeMetal3K8s(state="provisioned", image_url="oci://other:1")

    with pytest.raises(AssertionError, match="does not match the expected"):
        wait_for_host_allocated(
            k8s=cast(K8sClient, k8s),
            host=HostRef(namespace="host-inventory", name="bmh-1"),
            expected_image_url="oci://img:1",
        )


def test_wait_for_host_allocated_without_expected_image_requires_non_empty() -> None:
    k8s = FakeMetal3K8s(state="provisioned", image_url="")

    with pytest.raises(AssertionError, match="no image URL"):
        wait_for_host_allocated(k8s=cast(K8sClient, k8s), host=HostRef(namespace="host-inventory", name="bmh-1"))


def test_host_consumer_ref_reads_the_bmh_marker() -> None:
    k8s = FakeMetal3K8s(consumer_ref="bmi-1")

    assert (
        host_consumer_ref(k8s=cast(K8sClient, k8s), host=HostRef(namespace="host-inventory", name="bmh-1")) == "bmi-1"
    )


def test_wait_for_host_powered_polls_the_bmh_flag() -> None:
    k8s = FakeMetal3K8s(powered_on="false")

    wait_for_host_powered(k8s=cast(K8sClient, k8s), host=HostRef(namespace="host-inventory", name="bmh-1"), on=False)


def test_host_hardware_macs_returns_inspection_nics() -> None:
    # get_bmh_hardware_nics already lowercases per its contract.
    k8s = FakeMetal3K8s(nics=["aa:bb:cc:dd:ee:01", "aa:bb:cc:dd:ee:02"])

    assert host_hardware_macs(k8s=cast(K8sClient, k8s), host=HostRef(namespace="host-inventory", name="bmh-1")) == {
        "aa:bb:cc:dd:ee:01",
        "aa:bb:cc:dd:ee:02",
    }


# NetBox backend -----------------------------------------------------------------


class FakeNetBox:
    """NetBoxClient stand-in over serialized device dicts."""

    def __init__(self, devices: list[dict[str, Any]] | None = None) -> None:
        self.devices: list[dict[str, Any]] = [dict(device) for device in devices or []]
        self.created: list[dict[str, Any]] = []
        self.deleted: list[str] = []
        self.list_calls: list[dict[str, Any]] = []
        self.closed = False

    def list_devices(
        self, *, name: str | None = None, status: str | None = None, custom_fields: dict[str, Any] | None = None
    ) -> list[dict[str, Any]]:
        self.list_calls.append({"name": name, "status": status, "custom_fields": custom_fields})
        devices = self.devices
        if name is not None:
            devices = [device for device in devices if device.get("name") == name]
        if status is not None:
            devices = [device for device in devices if device.get("status") == status]
        return [dict(device) for device in devices]

    def create_device(
        self, *, name: str, role: str, site: str, device_type: str, custom_fields: dict[str, Any] | None = None
    ) -> str:
        self.created.append(
            {"name": name, "role": role, "site": site, "device_type": device_type, "custom_fields": custom_fields}
        )
        device_id = str(100 + len(self.created))
        self.devices.append(
            {"id": device_id, "name": name, "status": "staged", "custom_fields": dict(custom_fields or {})}
        )
        return device_id

    def delete_device(self, device_id: str) -> None:
        self.deleted.append(device_id)
        self.devices = [device for device in self.devices if device["id"] != device_id]

    def get_device(self, device_id: str) -> dict[str, Any]:
        return next(device for device in self.devices if device["id"] == device_id)

    def get_device_status(self, device_id: str) -> str:
        return self.get_device(device_id)["status"]

    def close(self) -> None:
        self.closed = True


class FakeDeviceK8s:
    def __init__(self, external_host_id: str, bmh_present: bool = True) -> None:
        self.external_host_id = external_host_id
        self.bmh_present = bmh_present

    def get_baremetal_instance_external_host_id(self, *, name: str) -> str:
        return self.external_host_id

    def is_bmh_present(self, *, name: str, bmh_namespace: str) -> bool:
        return self.bmh_present


def _claimed_device() -> dict[str, Any]:
    return {"id": "42", "status": "active", "custom_fields": {"osac_instance_id": "bmi-uid"}}


def _netbox(netbox: FakeNetBox, *, k8s: FakeDeviceK8s | None = None, bmh_namespace: str = "") -> NetBoxInventory:
    return NetBoxInventory(
        k8s=cast(K8sClient, k8s or FakeDeviceK8s("host-inventory/netbox-42")),
        netbox=cast(Any, netbox),
        bmh_namespace=bmh_namespace,
    )


def test_netbox_host_for_bmi_resolves_the_created_bmh() -> None:
    backend = _netbox(FakeNetBox([_claimed_device()]), k8s=FakeDeviceK8s("host-inventory/netbox-42"))

    assert backend.host_for_bmi(bmi_cr_name="bmi-cr") == HostRef(namespace="host-inventory", name="netbox-42")


def test_netbox_host_for_bmi_waits_for_the_inventory_claim(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr("tests.e2e.core.runner.time.sleep", lambda *_: None)
    netbox = FakeNetBox([{"id": "42", "status": "staged", "custom_fields": {"osac_instance_id": ""}}])
    backend = _netbox(netbox, k8s=FakeDeviceK8s("host-inventory/netbox-42"))

    with pytest.raises(TimeoutError, match="not reached"):
        backend.host_for_bmi(bmi_cr_name="bmi-cr")

    netbox.devices[0]["status"] = "active"
    netbox.devices[0]["custom_fields"]["osac_instance_id"] = "bmi-uid"
    assert backend.host_for_bmi(bmi_cr_name="bmi-cr") == HostRef(namespace="host-inventory", name="netbox-42")


def test_netbox_host_for_bmi_rejects_non_netbox_derived_names() -> None:
    backend = _netbox(FakeNetBox([_claimed_device()]), k8s=FakeDeviceK8s("host-inventory/virtual-bmh-1"))

    with pytest.raises(AssertionError, match=r"netbox-<device-id>"):
        backend.host_for_bmi(bmi_cr_name="bmi-cr")

    backend2 = _netbox(FakeNetBox([_claimed_device()]), k8s=FakeDeviceK8s("netbox-42"))
    with pytest.raises(AssertionError, match="namespace/name"):
        backend2.host_for_bmi(bmi_cr_name="bmi-cr")


def test_netbox_host_for_bmi_cross_checks_the_deployed_bmh_namespace() -> None:
    backend = _netbox(
        FakeNetBox([_claimed_device()]), k8s=FakeDeviceK8s("host-inventory/netbox-42"), bmh_namespace="other-ns"
    )

    with pytest.raises(AssertionError, match="landed in host-inventory"):
        backend.host_for_bmi(bmi_cr_name="bmi-cr")


def test_netbox_default_host_selector_uses_a_custom_field_name() -> None:
    backend = _netbox(FakeNetBox())

    # Must be a valid NetBox custom-field name: the operator queries it as
    # cf_<key>, and label-style keys with dots/slashes are rejected.
    assert backend.default_host_selector() == {"host_type": "default"}


def test_netbox_count_available_uses_the_free_device_query() -> None:
    netbox = FakeNetBox(
        [
            {"id": "1", "status": "staged", "custom_fields": {"osac_managed": True}},
            {"id": "2", "status": "staged", "custom_fields": {"osac_managed": True}},
            {"id": "3", "status": "active", "custom_fields": {"osac_managed": True}},
        ]
    )
    backend = _netbox(netbox)

    assert backend.count_available() == 2
    assert netbox.list_calls == [
        {"name": None, "status": "staged", "custom_fields": {"osac_managed": True, "osac_instance_id__empty": True}}
    ]


def test_netbox_wait_for_released_requires_bmh_deleted_and_device_staged(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr("tests.e2e.core.runner.time.sleep", lambda *_: None)
    netbox = FakeNetBox([_claimed_device()])
    k8s = FakeDeviceK8s("host-inventory/netbox-42", bmh_present=True)
    backend = _netbox(netbox, k8s=k8s)
    host = HostRef(namespace="host-inventory", name="netbox-42")

    with pytest.raises(TimeoutError, match="not reached"):
        backend.wait_for_released(host=host)

    k8s.bmh_present = False
    netbox.devices[0]["status"] = "staged"
    netbox.devices[0]["custom_fields"]["osac_instance_id"] = ""
    backend.wait_for_released(host=host)


def test_netbox_ensure_available_hosts_requires_at_least_one() -> None:
    backend = _netbox(FakeNetBox())

    with pytest.raises(AssertionError, match="at least 1 staged, unclaimed osac_managed device"):
        backend.ensure_available_hosts()

    staged = [{"id": "7", "name": "virtual-bmh-1", "status": "staged", "custom_fields": {}}]
    _netbox(FakeNetBox(staged)).ensure_available_hosts()


def test_netbox_close_closes_the_client() -> None:
    netbox = FakeNetBox()
    backend = _netbox(netbox)

    backend.close()

    assert netbox.closed


# BMF settings discovery -----------------------------------------------------------


# Rendered shapes mirror osac-installer/scripts/test-bmf-netbox-render.py's
# values (PR osac-project/osac#1530): the BMF chart's netbox-mode Secrets.
INVENTORY_YAML = """\
name: netbox-inventory
type: netbox
hostClass: metal3
options:
  netbox:
    url: https://netbox.example.test
    tokenFile: /etc/osac/secrets/netbox/token
    caFile: /etc/osac/secrets/netbox-ca/ca.crt
    allowInsecureHTTP: false
"""
INVENTORY_YAML_NO_CA = """\
name: netbox-inventory
type: netbox
options:
  netbox:
    url: https://netbox.example.test
    allowInsecureHTTP: true
"""
INVENTORY_YAML_HTTP = """\
name: netbox-inventory
type: netbox
options:
  netbox:
    url: http://netbox.example.test
    allowInsecureHTTP: false
"""
INVENTORY_YAML_HTTP_OPT_IN = """\
name: netbox-inventory
type: netbox
options:
  netbox:
    url: http://netbox.example.test
    allowInsecureHTTP: true
"""
MANAGEMENT_YAML = """\
name: metal3-management
type: metal3
options:
  metal3:
    namespace: host-inventory
"""


class FakeSecretsK8s:
    def __init__(self, secrets: dict[str, dict[str, str]]) -> None:
        self.secrets = secrets

    def get_json(self, *, resource: str, name: str, namespace: str | None = None) -> dict[str, Any]:
        if resource == "secret" and name in self.secrets:
            return {
                "data": {key: base64.b64encode(value.encode()).decode() for key, value in self.secrets[name].items()}
            }
        raise subprocess.CalledProcessError(
            1, ["kubectl"], stderr=f'Error from server (NotFound): secrets "{name}" not found'
        )


def _bmf_secrets(
    *,
    inventory_yaml: str = INVENTORY_YAML,
    token: str = "render-test-netbox-token",
    with_ca: bool = True,
    with_management: bool = True,
) -> dict[str, dict[str, str]]:
    secrets: dict[str, dict[str, str]] = {
        "osac-inventory-config": {"inventory.yaml": inventory_yaml},
        "osac-netbox-token": {"token": token},
    }
    if with_ca:
        secrets["osac-netbox-ca"] = {"ca.crt": "TEST-CA-CERTIFICATE\n"}
    if with_management:
        secrets["osac-management-config"] = {"management.yaml": MANAGEMENT_YAML}
    return secrets


def test_settings_from_full_bmf_configuration() -> None:
    settings = netbox_settings_from_bmf(k8s=cast(K8sClient, FakeSecretsK8s(_bmf_secrets())), namespace="osac")

    assert settings.url == "https://netbox.example.test"
    assert settings.token == "render-test-netbox-token"
    assert settings.ca_pem == "TEST-CA-CERTIFICATE\n"
    assert settings.skip_tls_verify is False
    assert settings.bmh_namespace == "host-inventory"


def test_settings_tolerate_missing_ca_secret() -> None:
    settings = netbox_settings_from_bmf(
        k8s=cast(K8sClient, FakeSecretsK8s(_bmf_secrets(with_ca=False, inventory_yaml=INVENTORY_YAML_NO_CA))),
        namespace="osac",
    )

    assert settings.ca_pem is None
    # allowInsecureHTTP is http://-scheme permission only, never a
    # TLS-verification switch.
    assert settings.skip_tls_verify is False


def test_settings_tolerate_missing_management_config() -> None:
    settings = netbox_settings_from_bmf(
        k8s=cast(K8sClient, FakeSecretsK8s(_bmf_secrets(with_management=False))), namespace="osac"
    )

    # The BMH-namespace cross-check source is best-effort.
    assert settings.bmh_namespace == ""


def test_settings_reject_http_url_without_deployed_opt_in() -> None:
    with pytest.raises(RuntimeError, match="does not set allowInsecureHTTP"):
        netbox_settings_from_bmf(
            k8s=cast(K8sClient, FakeSecretsK8s(_bmf_secrets(inventory_yaml=INVENTORY_YAML_HTTP))), namespace="osac"
        )


def test_settings_allow_http_url_with_deployed_opt_in() -> None:
    settings = netbox_settings_from_bmf(
        k8s=cast(K8sClient, FakeSecretsK8s(_bmf_secrets(inventory_yaml=INVENTORY_YAML_HTTP_OPT_IN))), namespace="osac"
    )

    assert settings.url == "http://netbox.example.test"
    assert settings.skip_tls_verify is False


def test_settings_fail_fast_on_missing_token_secret() -> None:
    secrets = _bmf_secrets()
    del secrets["osac-netbox-token"]

    with pytest.raises(RuntimeError, match="osac-netbox-token"):
        netbox_settings_from_bmf(k8s=cast(K8sClient, FakeSecretsK8s(secrets)), namespace="osac")


def test_settings_fail_fast_on_missing_url() -> None:
    with pytest.raises(RuntimeError, match="osac-inventory-config"):
        netbox_settings_from_bmf(
            k8s=cast(K8sClient, FakeSecretsK8s(_bmf_secrets(inventory_yaml="options: {}\n"))), namespace="osac"
        )


def test_settings_env_overrides_win(monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> None:
    ca_file = tmp_path / "ca.pem"
    ca_file.write_text("OVERRIDE-CA")
    monkeypatch.setenv("OSAC_NETBOX_URL", "https://override.example.test")
    monkeypatch.setenv("OSAC_NETBOX_TOKEN", "override-token")
    monkeypatch.setenv("OSAC_NETBOX_CA_FILE", str(ca_file))
    monkeypatch.setenv("OSAC_NETBOX_INSECURE", "true")

    settings = netbox_settings_from_bmf(k8s=cast(K8sClient, FakeSecretsK8s(_bmf_secrets())), namespace="osac")

    assert settings.url == "https://override.example.test"
    assert settings.token == "override-token"
    assert settings.ca_pem == "OVERRIDE-CA"
    assert settings.skip_tls_verify is True


def test_settings_env_overrides_skip_connection_secret_reads(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("OSAC_NETBOX_URL", "https://external.example.test")
    monkeypatch.setenv("OSAC_NETBOX_TOKEN", "external-token")

    class NoConnectionSecretReadsK8s(FakeSecretsK8s):
        def get_json(self, *, resource: str, name: str, namespace: str | None = None) -> dict[str, Any]:
            assert name == "osac-management-config", f"unexpected Secret read: {name}"
            return super().get_json(resource=resource, name=name, namespace=namespace)

    settings = netbox_settings_from_bmf(
        k8s=cast(K8sClient, NoConnectionSecretReadsK8s(_bmf_secrets())), namespace="osac"
    )

    assert settings.url == "https://external.example.test"
    assert settings.token == "external-token"
    # The deployed CA (present in the fake) must not leak into the override
    # connection: an override URL uses system roots unless OSAC_NETBOX_CA_FILE
    # is set.
    assert settings.ca_pem is None
    assert settings.skip_tls_verify is False
    # The cluster-scoped BMH-namespace cross-check is still read.
    assert settings.bmh_namespace == "host-inventory"


def test_settings_propagate_non_not_found_secret_errors() -> None:
    class UnreachableCAK8s(FakeSecretsK8s):
        def get_json(self, *, resource: str, name: str, namespace: str | None = None) -> dict[str, Any]:
            if name == "osac-netbox-ca":
                raise subprocess.CalledProcessError(1, ["kubectl"], stderr="connection refused")
            return super().get_json(resource=resource, name=name, namespace=namespace)

    # A real read failure on the optional CA Secret must not be swallowed
    # as "absent".
    with pytest.raises(subprocess.CalledProcessError):
        netbox_settings_from_bmf(k8s=cast(K8sClient, UnreachableCAK8s(_bmf_secrets())), namespace="osac")


# Fixture spec and factory -----------------------------------------------------------


def test_make_inventory_backend_defaults_to_metal3() -> None:
    backend = make_inventory_backend(
        k8s=cast(K8sClient, FakeMetal3K8s()), namespace="osac", bmh_namespace="host-inventory"
    )

    assert isinstance(backend, Metal3Inventory)


def test_make_inventory_backend_netbox_uses_bmf_settings(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("OSAC_INVENTORY_BACKEND", "netbox")

    backend = make_inventory_backend(
        k8s=cast(K8sClient, FakeSecretsK8s(_bmf_secrets())), namespace="osac", bmh_namespace="host-inventory"
    )

    assert isinstance(backend, NetBoxInventory)
    assert backend.bmh_namespace == "host-inventory"
    backend.close()


def test_make_inventory_backend_rejects_unknown_backend(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("OSAC_INVENTORY_BACKEND", "bcm")

    with pytest.raises(RuntimeError, match="valid values: metal3, netbox"):
        make_inventory_backend(k8s=cast(K8sClient, FakeMetal3K8s()), namespace="osac", bmh_namespace="host-inventory")
