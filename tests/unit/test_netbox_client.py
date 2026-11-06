from __future__ import annotations

import os
from types import SimpleNamespace
from typing import Any, cast

import pynetbox
import pytest
from pynetbox.core.api import Api

from tests.e2e.core.netbox_client import NetBoxClient

_TOKEN = "nbt_test-token-value"


class FakeRecord:
    """Minimal pynetbox Record stand-in."""

    def __init__(self, data: dict[str, Any]) -> None:
        self._data = dict(data)
        self.id: int = int(data.get("id", 0))
        if "status" in data:
            self.status = data["status"]

    def serialize(self) -> dict[str, Any]:
        return dict(self._data)


class FakeLookupEndpoint:
    """sites / device_roles stand-in: get(slug=...) resolves slugs to ids."""

    def __init__(self, by_slug: dict[str, int]) -> None:
        self.by_slug = by_slug
        self.get_calls: list[dict[str, Any]] = []

    def get(self, key: str | None = None, **kwargs: object) -> SimpleNamespace | None:
        self.get_calls.append({"key": key, **kwargs})
        slug = kwargs.get("slug")
        if slug is not None and str(slug) in self.by_slug:
            return SimpleNamespace(id=self.by_slug[str(slug)])
        return None


def _filter_matches(record: FakeRecord, key: str, value: object) -> bool:
    data = record.serialize()
    if key.startswith("cf_"):
        return data.get("custom_fields", {}).get(key[3:]) == value
    return data.get(key) == value


class FakeDevicesEndpoint:
    """dcim.devices stand-in recording create/delete/filter calls."""

    def __init__(self, records: list[FakeRecord] | None = None) -> None:
        self.records: list[FakeRecord] = list(records or [])
        self.create_kwargs: list[dict[str, Any]] = []
        self.deleted: list[list[FakeRecord]] = []
        self.filter_calls: list[dict[str, Any]] = []
        self.next_id = 100

    def create(self, **kwargs: object) -> FakeRecord:
        self.create_kwargs.append(kwargs)
        record = FakeRecord(
            {
                "id": self.next_id,
                "name": kwargs.get("name"),
                "status": kwargs.get("status"),
                "custom_fields": kwargs.get("custom_fields"),
            }
        )
        self.next_id += 1
        self.records.append(record)
        return record

    def get(self, key: str | None = None, **_kwargs: object) -> FakeRecord | None:
        for record in self.records:
            if str(record.id) == str(key):
                return record
        return None

    def delete(self, objects: list[FakeRecord]) -> None:
        self.deleted.append(list(objects))
        for record in objects:
            self.records.remove(record)

    def filter(self, **kwargs: object) -> list[FakeRecord]:
        self.filter_calls.append(kwargs)
        return [record for record in self.records if all(_filter_matches(record, k, v) for k, v in kwargs.items())]


class FakeAPI:
    def __init__(
        self,
        sites: FakeLookupEndpoint,
        device_roles: FakeLookupEndpoint,
        device_types: FakeLookupEndpoint,
        devices: FakeDevicesEndpoint,
    ) -> None:
        self.dcim = SimpleNamespace(sites=sites, device_roles=device_roles, device_types=device_types, devices=devices)


def _client(api: FakeAPI) -> NetBoxClient:
    return NetBoxClient(url="https://netbox.example.test", token=_TOKEN, api=cast(Api, api))


def _api(
    sites: FakeLookupEndpoint | None = None,
    device_roles: FakeLookupEndpoint | None = None,
    device_types: FakeLookupEndpoint | None = None,
    devices: FakeDevicesEndpoint | None = None,
) -> FakeAPI:
    return FakeAPI(
        sites=sites or FakeLookupEndpoint({"dc1": 7}),
        device_roles=device_roles or FakeLookupEndpoint({"server": 3}),
        device_types=device_types or FakeLookupEndpoint({"virtual-host": 5}),
        devices=devices or FakeDevicesEndpoint(),
    )


def test_create_device_resolves_slugs_and_defaults_to_staged() -> None:
    sites = FakeLookupEndpoint({"dc1": 7})
    device_roles = FakeLookupEndpoint({"server": 3})
    devices = FakeDevicesEndpoint()
    client = _client(_api(sites=sites, device_roles=device_roles, devices=devices))

    device_id = client.create_device(
        name="host-1", role="server", site="dc1", device_type="virtual-host", custom_fields={"osac_managed": True}
    )

    assert device_id == "100"
    # Role resolution must go through device_roles, not roles.
    assert sites.get_calls == [{"key": None, "slug": "dc1"}]
    assert device_roles.get_calls == [{"key": None, "slug": "server"}]
    assert devices.create_kwargs == [
        {
            "name": "host-1",
            "role": 3,
            "site": 7,
            "device_type": 5,
            "status": "staged",
            "custom_fields": {"osac_managed": True},
        }
    ]


def test_create_device_passes_numeric_ids_through_without_lookup() -> None:
    sites = FakeLookupEndpoint({})
    device_roles = FakeLookupEndpoint({})
    devices = FakeDevicesEndpoint()
    client = _client(_api(sites=sites, device_roles=device_roles, devices=devices))

    device_id = client.create_device(name="host-1", role="9", site="4", device_type="6")

    assert device_id == "100"
    assert sites.get_calls == []
    assert device_roles.get_calls == []
    assert devices.create_kwargs[0]["role"] == 9
    assert devices.create_kwargs[0]["site"] == 4
    assert devices.create_kwargs[0]["device_type"] == 6
    assert devices.create_kwargs[0]["status"] == "staged"


def test_create_device_without_custom_fields_sends_empty_mapping() -> None:
    devices = FakeDevicesEndpoint()
    client = _client(_api(device_roles=FakeLookupEndpoint({"server": 2}), devices=devices))

    client.create_device(name="host-1", role="server", site="dc1", device_type="virtual-host")

    assert devices.create_kwargs[0]["custom_fields"] == {}


def test_create_device_missing_slug_fails_fast() -> None:
    client = _client(_api(device_roles=FakeLookupEndpoint({})))

    with pytest.raises(RuntimeError, match="device role 'nope' was not found"):
        client.create_device(name="host-1", role="nope", site="dc1", device_type="virtual-host")


def test_create_device_missing_site_slug_fails_fast() -> None:
    client = _client(_api(sites=FakeLookupEndpoint({})))

    with pytest.raises(RuntimeError, match="site 'nope' was not found"):
        client.create_device(name="host-1", role="server", site="nope", device_type="virtual-host")


def test_delete_device_is_idempotent() -> None:
    devices = FakeDevicesEndpoint()
    client = _client(_api(devices=devices))

    client.delete_device("999")

    assert devices.deleted == []


def test_delete_device_deletes_the_record() -> None:
    record = FakeRecord({"id": 5, "name": "host-1", "status": "staged"})
    devices = FakeDevicesEndpoint([record])
    client = _client(_api(devices=devices))

    client.delete_device("5")

    assert devices.deleted == [[record]]
    assert devices.records == []


def test_get_device_status_normalizes_string_and_choice_forms() -> None:
    for status, expected in (
        ("staged", "staged"),
        ({"value": "active", "label": "Active"}, "active"),
        (SimpleNamespace(value="failed"), "failed"),
    ):
        devices = FakeDevicesEndpoint([FakeRecord({"id": 1, "status": status})])
        client = _client(_api(devices=devices))
        assert client.get_device_status("1") == expected


def test_get_device_status_missing_device_returns_empty() -> None:
    client = _client(_api(devices=FakeDevicesEndpoint()))

    assert client.get_device_status("999") == ""


def test_get_device_returns_serialized_record() -> None:
    devices = FakeDevicesEndpoint([FakeRecord({"id": 1, "name": "host-1", "custom_fields": {"osac_managed": True}})])
    client = _client(_api(devices=devices))

    assert client.get_device("1") == {"id": 1, "name": "host-1", "custom_fields": {"osac_managed": True}}


def test_get_device_missing_device_raises() -> None:
    client = _client(_api(devices=FakeDevicesEndpoint()))

    with pytest.raises(RuntimeError, match="was not found"):
        client.get_device("999")


def test_list_devices_prefixes_custom_field_filters() -> None:
    devices = FakeDevicesEndpoint()
    client = _client(_api(devices=devices))

    client.list_devices(status="staged", custom_fields={"osac_managed": True, "osac_instance_id__empty": True})

    assert devices.filter_calls == [{"status": "staged", "cf_osac_managed": True, "cf_osac_instance_id__empty": True}]


def test_list_devices_filters_by_exact_name() -> None:
    devices = FakeDevicesEndpoint([FakeRecord({"id": 1, "name": "host-1"}), FakeRecord({"id": 2, "name": "host-2"})])
    client = _client(_api(devices=devices))

    result = client.list_devices(name="host-1")

    assert devices.filter_calls == [{"name": "host-1"}]
    assert [device["id"] for device in result] == [1]


def test_list_devices_without_filters_queries_everything() -> None:
    devices = FakeDevicesEndpoint()
    client = _client(_api(devices=devices))

    client.list_devices()

    assert devices.filter_calls == [{}]


class FakeRequestsResponse:
    """requests.Response stand-in carrying a would-be token leak."""

    def __init__(self) -> None:
        self.status_code = 500
        self.url = "https://netbox.example.test/api/dcim/devices/1/"
        self.reason = "Server Error"
        self.text = f"body mentions {_TOKEN}"
        self.request = SimpleNamespace(method="GET", body=None)

    def json(self) -> dict[str, str]:
        return {"error": f"json mentions {_TOKEN}"}


def test_request_error_is_sanitized_and_never_leaks_the_token() -> None:
    devices = FakeDevicesEndpoint()

    class ExplodingDevicesEndpoint(FakeDevicesEndpoint):
        def get(self, key: str | None = None, **_kwargs: object) -> FakeRecord:
            raise pynetbox.RequestError(FakeRequestsResponse())

    client = _client(_api(devices=ExplodingDevicesEndpoint(devices.records)))

    with pytest.raises(RuntimeError) as excinfo:
        client.get_device_status("1")

    message = str(excinfo.value)
    assert "NetBox get_device_status failed" in message
    assert "GET" in message
    assert "https://netbox.example.test/api/dcim/devices/1/" in message
    assert "HTTP 500" in message
    assert _TOKEN not in message


def test_ca_pem_materializes_a_temp_bundle_removed_on_close() -> None:
    client = NetBoxClient(url="https://netbox.example.test", token=_TOKEN, ca_pem="FAKE-CA-CERTIFICATE")

    verify = client.api.http_session.verify
    assert isinstance(verify, str)
    with open(verify) as handle:
        assert handle.read() == "FAKE-CA-CERTIFICATE"

    client.close()
    assert not os.path.exists(verify)


def test_insecure_disables_tls_verification() -> None:
    client = NetBoxClient(url="https://netbox.example.test", token=_TOKEN, insecure=True)

    assert client.api.http_session.verify is False


def test_default_construction_keeps_default_verification() -> None:
    client = NetBoxClient(url="https://netbox.example.test", token=_TOKEN)

    assert client.api.http_session.verify is True
