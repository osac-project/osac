from __future__ import annotations

import base64
import json
import subprocess
from pathlib import Path
from typing import Any, cast

import pytest

from tests.e2e.core.inventory import (
    Metal3Inventory,
    NetBoxInventory,
    load_netbox_fixture_spec,
    make_inventory_backend,
    netbox_settings_from_bmf,
)
from tests.e2e.core.k8s_client import K8sClient

_NETBOX_ENV_VARS = (
    "OSAC_INVENTORY_BACKEND",
    "OSAC_NETBOX_URL",
    "OSAC_NETBOX_TOKEN",
    "OSAC_NETBOX_CA_FILE",
    "OSAC_NETBOX_INSECURE",
    "OSAC_NETBOX_SITE",
    "OSAC_NETBOX_ROLE",
    "OSAC_NETBOX_FIXTURE_HOSTS",
)


@pytest.fixture(autouse=True)
def _clean_env(monkeypatch: pytest.MonkeyPatch) -> None:
    for name in _NETBOX_ENV_VARS:
        monkeypatch.delenv(name, raising=False)


# Metal3 backend -----------------------------------------------------------------


class FakeMetal3K8s:
    """K8sClient stand-in covering the BMH queries Metal3Inventory delegates to."""

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
        self.nics = nics if nics is not None else ["AA:BB:CC:DD:EE:01", "aa:bb:cc:dd:ee:02"]
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


def test_metal3_host_for_bmi_splits_namespace_and_name() -> None:
    backend = _metal3(FakeMetal3K8s(external_host_id="host-inventory/bmh-7"))

    assert backend.host_for_bmi(bmi_cr_name="bmi-cr") == "bmh-7"


def test_metal3_host_for_bmi_rejects_wrong_namespace() -> None:
    backend = _metal3(FakeMetal3K8s(external_host_id="other-ns/bmh-1"))

    with pytest.raises(AssertionError, match="landed in other-ns"):
        backend.host_for_bmi(bmi_cr_name="bmi-cr")


def test_metal3_host_for_bmi_rejects_malformed_host_id() -> None:
    backend = _metal3(FakeMetal3K8s(external_host_id="bmh-1"))

    with pytest.raises(AssertionError, match="namespace/name"):
        backend.host_for_bmi(bmi_cr_name="bmi-cr")


def test_metal3_count_available_uses_available_and_ready_states() -> None:
    k8s = FakeMetal3K8s(available_count=3)
    backend = _metal3(k8s)

    assert backend.count_available() == 3
    assert k8s.count_calls == [{"bmh_namespace": "host-inventory", "states": {"available", "ready"}}]


def test_metal3_wait_for_allocated_bundles_the_provisioned_assertions() -> None:
    backend = _metal3(FakeMetal3K8s(state="provisioned", image_url="oci://img:1", consumer_ref="bmi-1", online="true"))

    backend.wait_for_allocated(ref="bmh-1", bmi_id="bmi-uid", expected_image_url="oci://img:1")


def test_metal3_wait_for_allocated_rejects_wrong_image() -> None:
    backend = _metal3(FakeMetal3K8s(state="provisioned", image_url="oci://other:1"))

    with pytest.raises(AssertionError, match="does not match the expected"):
        backend.wait_for_allocated(ref="bmh-1", bmi_id="bmi-uid", expected_image_url="oci://img:1")


def test_metal3_wait_for_allocated_without_expected_image_requires_non_empty() -> None:
    backend = _metal3(FakeMetal3K8s(state="provisioned", image_url=""))

    with pytest.raises(AssertionError, match="no image URL"):
        backend.wait_for_allocated(ref="bmh-1", bmi_id="bmi-uid")


def test_metal3_wait_for_released_requires_cleared_image_and_consumer() -> None:
    backend = _metal3(FakeMetal3K8s(state="available", image_url="", consumer_ref=""))

    backend.wait_for_released(ref="bmh-1")

    backend2 = _metal3(FakeMetal3K8s(state="available", image_url="oci://leftover:1", consumer_ref=""))
    with pytest.raises(AssertionError, match="image not cleared"):
        backend2.wait_for_released(ref="bmh-1")


def test_metal3_wait_for_powered_polls_the_bmh_flag() -> None:
    backend = _metal3(FakeMetal3K8s(powered_on="false"))

    backend.wait_for_powered(ref="bmh-1", on=False, bmi_id="bmi-uid")


def test_metal3_hardware_macs_returns_inspection_nics() -> None:
    # get_bmh_hardware_nics already lowercases per its contract.
    backend = _metal3(FakeMetal3K8s(nics=["aa:bb:cc:dd:ee:01", "aa:bb:cc:dd:ee:02"]))

    assert backend.hardware_macs(ref="bmh-1") == {"aa:bb:cc:dd:ee:01", "aa:bb:cc:dd:ee:02"}


def test_metal3_ensure_available_hosts_requires_at_least_one() -> None:
    backend = _metal3(FakeMetal3K8s(available_count=0))

    with pytest.raises(AssertionError, match="at least 1 available BMH"):
        backend.ensure_available_hosts()

    _metal3(FakeMetal3K8s(available_count=2)).ensure_available_hosts()


def test_metal3_release_and_close_are_no_ops() -> None:
    backend = _metal3(FakeMetal3K8s())

    backend.release_created_hosts()
    backend.close()


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

    def create_device(self, *, name: str, role: str, site: str, custom_fields: dict[str, Any] | None = None) -> str:
        self.created.append({"name": name, "role": role, "site": site, "custom_fields": custom_fields})
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


class FakeGRPC:
    def __init__(
        self,
        state: str = "BARE_METAL_INSTANCE_STATE_STOPPED",
        bmi_spec: dict[str, Any] | None = None,
        disk_images: list[dict[str, Any]] | None = None,
    ) -> None:
        self.state = state
        self.bmi_spec = bmi_spec if bmi_spec is not None else {}
        self.disk_images = disk_images if disk_images is not None else []

    def get_baremetal_instance_state(self, *, bmi_id: str) -> str:
        return self.state

    def get_baremetal_instance(self, *, bmi_id: str) -> dict[str, Any]:
        return {"object": {"spec": self.bmi_spec}}

    def call(self, *, service: str, data: dict[str, Any] | None = None) -> dict[str, Any]:
        assert service.endswith("DiskImages/List")
        return {"items": self.disk_images}


class FakeDeviceK8s:
    def __init__(self, external_host_id: str) -> None:
        self.external_host_id = external_host_id

    def get_baremetal_instance_external_host_id(self, *, name: str) -> str:
        return self.external_host_id


def _netbox(
    netbox: FakeNetBox,
    *,
    grpc: FakeGRPC | None = None,
    fixture_spec: list[dict[str, Any]] | None = None,
    k8s: FakeDeviceK8s | None = None,
) -> NetBoxInventory:
    return NetBoxInventory(
        k8s=cast(K8sClient, k8s or FakeDeviceK8s("42")),
        netbox=cast(Any, netbox),
        grpc=cast(Any, grpc or FakeGRPC()),
        fixture_spec=fixture_spec or [],
        name_prefix="run1",
    )


def test_netbox_host_for_bmi_returns_the_device_id() -> None:
    backend = _netbox(FakeNetBox(), k8s=FakeDeviceK8s("42"))

    assert backend.host_for_bmi(bmi_cr_name="bmi-cr") == "42"


def test_netbox_host_for_bmi_rejects_non_device_id_refs() -> None:
    backend = _netbox(FakeNetBox(), k8s=FakeDeviceK8s("host-inventory/bmh-1"))

    with pytest.raises(AssertionError, match="Expected a NetBox device id"):
        backend.host_for_bmi(bmi_cr_name="bmi-cr")

    backend2 = _netbox(FakeNetBox(), k8s=FakeDeviceK8s(""))
    with pytest.raises(AssertionError, match="no externalHostID"):
        backend2.host_for_bmi(bmi_cr_name="bmi-cr")


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


def test_netbox_wait_for_allocated_requires_active_claimed_and_matching_image(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr("tests.e2e.core.runner.time.sleep", lambda *_: None)
    netbox = FakeNetBox([{"id": "42", "status": "staged", "custom_fields": {"osac_instance_id": ""}}])
    grpc = FakeGRPC(
        bmi_spec={"diskImage": {"id": "di-1", "name": "e2e-di"}},
        disk_images=[{"id": "di-1", "metadata": {"name": "e2e-di"}, "spec": {"sourceRef": "oci://img:1"}}],
    )
    backend = _netbox(netbox, grpc=grpc)

    with pytest.raises(TimeoutError, match="timed out"):
        backend.wait_for_allocated(ref="42", bmi_id="bmi-uid", expected_image_url="oci://img:1")

    netbox.devices[0]["status"] = "active"
    netbox.devices[0]["custom_fields"]["osac_instance_id"] = "bmi-uid"
    backend.wait_for_allocated(ref="42", bmi_id="bmi-uid", expected_image_url="oci://img:1")


def test_netbox_wait_for_allocated_verifies_bmi_disk_image_source() -> None:
    netbox = FakeNetBox([{"id": "42", "status": "active", "custom_fields": {"osac_instance_id": "bmi-uid"}}])
    grpc = FakeGRPC(
        bmi_spec={"diskImage": {"id": "di-1", "name": "e2e-di"}},
        disk_images=[{"id": "di-1", "metadata": {"name": "e2e-di"}, "spec": {"sourceRef": "oci://other:1"}}],
    )
    backend = _netbox(netbox, grpc=grpc)

    with pytest.raises(AssertionError, match="selected DiskImage source"):
        backend.wait_for_allocated(ref="42", bmi_id="bmi-uid", expected_image_url="oci://img:1")


def test_netbox_wait_for_allocated_rejects_invisible_disk_image() -> None:
    netbox = FakeNetBox([{"id": "42", "status": "active", "custom_fields": {"osac_instance_id": "bmi-uid"}}])
    grpc = FakeGRPC(bmi_spec={"diskImage": {"name": "gone"}})
    backend = _netbox(netbox, grpc=grpc)

    with pytest.raises(AssertionError, match="not visible"):
        backend.wait_for_allocated(ref="42", bmi_id="bmi-uid", expected_image_url="oci://img:1")


def test_netbox_wait_for_released_requires_staged_and_unclaimed(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr("tests.e2e.core.runner.time.sleep", lambda *_: None)
    netbox = FakeNetBox([{"id": "42", "status": "active", "custom_fields": {"osac_instance_id": "bmi-uid"}}])
    backend = _netbox(netbox)

    with pytest.raises(TimeoutError, match="timed out"):
        backend.wait_for_released(ref="42")

    netbox.devices[0]["status"] = "staged"
    netbox.devices[0]["custom_fields"]["osac_instance_id"] = ""
    backend.wait_for_released(ref="42")


def test_netbox_consumer_ref_reads_the_claim_custom_field() -> None:
    backend = _netbox(FakeNetBox([{"id": "42", "status": "active", "custom_fields": {"osac_instance_id": "bmi-uid"}}]))

    assert backend.consumer_ref(ref="42") == "bmi-uid"


def test_netbox_wait_for_powered_uses_the_bmi_api_state() -> None:
    backend = _netbox(FakeNetBox(), grpc=FakeGRPC("BARE_METAL_INSTANCE_STATE_STOPPED"))

    backend.wait_for_powered(ref="42", on=False, bmi_id="bmi-uid")

    backend2 = _netbox(FakeNetBox(), grpc=FakeGRPC("BARE_METAL_INSTANCE_STATE_RUNNING"))
    backend2.wait_for_powered(ref="42", on=True, bmi_id="bmi-uid")


def test_netbox_hardware_macs_reads_interface_custom_field() -> None:
    device = {"id": "42", "status": "active", "custom_fields": {"osac_interface_macs": {"data-0": "AA:BB:CC:DD:EE:01"}}}
    backend = _netbox(FakeNetBox([device]))

    assert backend.hardware_macs(ref="42") == {"aa:bb:cc:dd:ee:01"}


def test_netbox_hardware_macs_accepts_json_string_custom_field() -> None:
    backend = _netbox(
        FakeNetBox(
            [
                {
                    "id": "42",
                    "status": "active",
                    "custom_fields": {"osac_interface_macs": json.dumps({"data-0": "AA:BB:CC:DD:EE:01"})},
                }
            ]
        )
    )

    assert backend.hardware_macs(ref="42") == {"aa:bb:cc:dd:ee:01"}


def test_netbox_fixture_devices_are_created_idempotently_and_deleted(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("OSAC_NETBOX_SITE", "dc1")
    netbox = FakeNetBox()
    backend = _netbox(
        netbox,
        fixture_spec=[
            {"bmc_address": "redfish://10.0.0.1", "boot_mac": "aa:bb:cc:dd:ee:01"},
            {
                "bmc_address": "redfish://10.0.0.2",
                "boot_mac": "aa:bb:cc:dd:ee:02",
                "interface_macs": {"data-0": "AA:BB:CC:DD:EE:02"},
                "custom_fields": {"host-type": "default"},
            },
        ],
    )

    backend.ensure_available_hosts()

    assert [call["name"] for call in netbox.created] == ["run1-netbox-1", "run1-netbox-2"]
    assert [call["site"] for call in netbox.created] == ["dc1", "dc1"]
    # Default role when OSAC_NETBOX_ROLE is unset.
    assert [call["role"] for call in netbox.created] == ["server", "server"]
    first, second = netbox.created
    assert first["custom_fields"] == {
        "osac_managed": True,
        "osac_bmc_address": "redfish://10.0.0.1",
        "osac_boot_mac": "aa:bb:cc:dd:ee:01",
    }
    assert second["custom_fields"]["osac_interface_macs"] == {"data-0": "AA:BB:CC:DD:EE:02"}
    assert second["custom_fields"]["host-type"] == "default"

    # Second ensure is a no-op: the run-prefixed devices already exist.
    backend.ensure_available_hosts()
    assert len(netbox.created) == 2

    backend.release_created_hosts()
    assert netbox.deleted == ["101", "102"]
    # Teardown is idempotent.
    backend.release_created_hosts()
    assert netbox.deleted == ["101", "102"]


def test_netbox_fixture_role_env_override(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("OSAC_NETBOX_SITE", "dc1")
    monkeypatch.setenv("OSAC_NETBOX_ROLE", "compute")
    netbox = FakeNetBox()
    backend = _netbox(netbox, fixture_spec=[{"bmc_address": "redfish://10.0.0.1", "boot_mac": "aa:bb:cc:dd:ee:01"}])

    backend.ensure_available_hosts()

    assert netbox.created[0]["role"] == "compute"


def test_netbox_fixture_requires_a_host_spec_and_site(monkeypatch: pytest.MonkeyPatch) -> None:
    backend = _netbox(FakeNetBox(), fixture_spec=[])

    with pytest.raises(RuntimeError, match="OSAC_NETBOX_FIXTURE_HOSTS is empty"):
        backend.ensure_available_hosts()

    monkeypatch.setenv("OSAC_NETBOX_SITE", "")
    backend2 = _netbox(FakeNetBox(), fixture_spec=[{"bmc_address": "redfish://x", "boot_mac": "aa:bb:cc:dd:ee:01"}])
    with pytest.raises(RuntimeError, match="OSAC_NETBOX_SITE"):
        backend2.ensure_available_hosts()


def test_netbox_release_created_hosts_aggregates_deletion_failures() -> None:
    class ExplodingNetBox(FakeNetBox):
        def delete_device(self, device_id: str) -> None:
            raise RuntimeError(f"boom {device_id}")

    netbox = ExplodingNetBox()
    backend = _netbox(netbox, fixture_spec=[{"bmc_address": "redfish://10.0.0.1", "boot_mac": "aa:bb:cc:dd:ee:01"}])
    monkeypatch = pytest.MonkeyPatch()
    monkeypatch.setenv("OSAC_NETBOX_SITE", "dc1")
    try:
        backend.ensure_available_hosts()
        with pytest.raises(RuntimeError, match="1 NetBox fixture device deletion failure"):
            backend.release_created_hosts()
        # Failed deletions are not retried on a second teardown call.
        backend.release_created_hosts()
    finally:
        monkeypatch.undo()


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
    *, inventory_yaml: str = INVENTORY_YAML, token: str = "render-test-netbox-token", with_ca: bool = True
) -> dict[str, dict[str, str]]:
    secrets: dict[str, dict[str, str]] = {
        "osac-inventory-config": {"inventory.yaml": inventory_yaml},
        "osac-netbox-token": {"token": token},
    }
    if with_ca:
        secrets["osac-netbox-ca"] = {"ca.crt": "TEST-CA-CERTIFICATE\n"}
    return secrets


def test_settings_from_full_bmf_configuration() -> None:
    settings = netbox_settings_from_bmf(k8s=cast(K8sClient, FakeSecretsK8s(_bmf_secrets())), namespace="osac")

    assert settings.url == "https://netbox.example.test"
    assert settings.token == "render-test-netbox-token"
    assert settings.ca_pem == "TEST-CA-CERTIFICATE\n"
    assert settings.skip_tls_verify is False


def test_settings_tolerate_missing_ca_secret() -> None:
    settings = netbox_settings_from_bmf(
        k8s=cast(K8sClient, FakeSecretsK8s(_bmf_secrets(with_ca=False, inventory_yaml=INVENTORY_YAML_NO_CA))),
        namespace="osac",
    )

    assert settings.ca_pem is None
    # allowInsecureHTTP is http://-scheme permission only, never a
    # TLS-verification switch.
    assert settings.skip_tls_verify is False


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


def test_settings_env_overrides_skip_secret_reads(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("OSAC_NETBOX_URL", "https://external.example.test")
    monkeypatch.setenv("OSAC_NETBOX_TOKEN", "external-token")

    # A fully env-driven run against an external NetBox reads no Secrets.
    settings = netbox_settings_from_bmf(k8s=cast(K8sClient, FakeSecretsK8s({})), namespace="osac")

    assert settings.url == "https://external.example.test"
    assert settings.token == "external-token"
    assert settings.ca_pem is None
    assert settings.skip_tls_verify is False


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


def test_fixture_spec_empty_when_unset() -> None:
    assert load_netbox_fixture_spec() == []


def test_fixture_spec_parses_and_validates() -> None:
    monkeypatch = pytest.MonkeyPatch()
    monkeypatch.setenv(
        "OSAC_NETBOX_FIXTURE_HOSTS",
        json.dumps([{"bmc_address": "redfish://10.0.0.1", "boot_mac": "aa:bb:cc:dd:ee:01"}]),
    )
    try:
        assert load_netbox_fixture_spec() == [{"bmc_address": "redfish://10.0.0.1", "boot_mac": "aa:bb:cc:dd:ee:01"}]

        monkeypatch.setenv("OSAC_NETBOX_FIXTURE_HOSTS", "{not json")
        with pytest.raises(RuntimeError, match="not valid JSON"):
            load_netbox_fixture_spec()

        monkeypatch.setenv("OSAC_NETBOX_FIXTURE_HOSTS", json.dumps({"bmc_address": "redfish://x"}))
        with pytest.raises(RuntimeError, match="must be a JSON list"):
            load_netbox_fixture_spec()

        monkeypatch.setenv("OSAC_NETBOX_FIXTURE_HOSTS", json.dumps([{"bmc_address": "redfish://x"}]))
        with pytest.raises(RuntimeError, match="missing 'boot_mac'"):
            load_netbox_fixture_spec()
    finally:
        monkeypatch.undo()


def test_make_inventory_backend_defaults_to_metal3() -> None:
    backend = make_inventory_backend(
        k8s=cast(K8sClient, FakeMetal3K8s()),
        namespace="osac",
        grpc=cast(Any, FakeGRPC()),
        bmh_namespace="host-inventory",
        test_run_id="run1",
    )

    assert isinstance(backend, Metal3Inventory)


def test_make_inventory_backend_netbox_uses_bmf_settings(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("OSAC_INVENTORY_BACKEND", "netbox")
    monkeypatch.setenv(
        "OSAC_NETBOX_FIXTURE_HOSTS",
        json.dumps([{"bmc_address": "redfish://10.0.0.1", "boot_mac": "aa:bb:cc:dd:ee:01"}]),
    )

    backend = make_inventory_backend(
        k8s=cast(K8sClient, FakeSecretsK8s(_bmf_secrets())),
        namespace="osac",
        grpc=cast(Any, FakeGRPC()),
        bmh_namespace="host-inventory",
        test_run_id="run1",
    )

    assert isinstance(backend, NetBoxInventory)
    assert backend.fixture_spec == [{"bmc_address": "redfish://10.0.0.1", "boot_mac": "aa:bb:cc:dd:ee:01"}]
    assert backend.name_prefix == "run1"
    backend.close()


def test_make_inventory_backend_rejects_unknown_backend(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("OSAC_INVENTORY_BACKEND", "bcm")

    with pytest.raises(RuntimeError, match="valid values: metal3, netbox"):
        make_inventory_backend(
            k8s=cast(K8sClient, FakeMetal3K8s()),
            namespace="osac",
            grpc=cast(Any, FakeGRPC()),
            bmh_namespace="host-inventory",
            test_run_id="run1",
        )
