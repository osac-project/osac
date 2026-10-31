import copy
import io
import json
import sys
from pathlib import Path

import pytest

MODULE_UTILS = (
    Path(__file__).resolve().parents[2]
    / "collections"
    / "ansible_collections"
    / "osac"
    / "templates"
    / "plugins"
    / "module_utils"
)
sys.path.insert(0, str(MODULE_UTILS))
sys.path.insert(0, str(MODULE_UTILS.parents[4]))

from ansible_collections.osac.templates.plugins.module_utils import (  # noqa: E402
    netris_external_ip as netris_eip,
)

# Contract inputs (network manager contract section 4.5): the ExternalIP
# snapshot carries its API ID, the pool snapshot its API ID and spec.cidrs.
EXTERNAL_IP_ID = "44444444-4444-4444-8444-444444444444"
POOL_ID = "22222222-2222-4222-8222-222222222222"
RESERVATION_NAME = f"osac-eip-{EXTERNAL_IP_ID}"  # built by netris_contract_names
POOL_CIDR = "10.20.0.0/24"
NETRIS_SITE_ID = 1
NETRIS_TENANT_ID = 7


class netris_eip_test_missing:
    """Sentinel marking a key that must be removed from a fixture."""


def make_resource(**overrides):
    resource = {
        "kind": "ExternalIP",
        "id": EXTERNAL_IP_ID,
        "metadata": {"name": "eip-test", "namespace": "osac", "uid": "kube-uid", "generation": 3},
        "spec": {"pool": POOL_ID},
    }
    resource.update(overrides)
    return {key: value for key, value in resource.items() if value is not netris_eip_test_missing}


def make_pool(**overrides):
    pool = {
        "kind": "ExternalIPPool",
        "id": POOL_ID,
        "metadata": {"name": "pool", "namespace": "osac", "uid": "kube-pool-uid", "generation": 1},
        "spec": {"cidrs": [POOL_CIDR]},
    }
    pool.update(overrides)
    return {key: value for key, value in pool.items() if value is not netris_eip_test_missing}


def make_reservation(address="10.20.0.5", **overrides):
    record = {
        "id": 101,
        "name": f"osac-eip-{EXTERNAL_IP_ID}",
        "prefix": f"{address}/32",
        "purpose": "nat",
        "site_id": NETRIS_SITE_ID,
        "tenant_id": NETRIS_TENANT_ID,
    }
    record.update(overrides)
    return {key: value for key, value in record.items() if value is not netris_eip_test_missing}


# U01 - valid request, unchanged inputs, name keyed by the ExternalIP API ID


@pytest.mark.parametrize("operation", ["external_ip.allocate", "external_ip.release"])
def test_valid_request_normalizes_exact_identity(operation):
    request = netris_eip.validate_request(make_resource(), make_pool(), operation, RESERVATION_NAME)

    assert request == {
        "operation": operation,
        "reservation_name": f"osac-eip-{EXTERNAL_IP_ID}",
        "resource_id": EXTERNAL_IP_ID,
        "pool_id": POOL_ID,
        "cidr": POOL_CIDR,
    }


def test_valid_request_does_not_mutate_inputs():
    resource = make_resource()
    pool = make_pool()
    resource_before = copy.deepcopy(resource)
    pool_before = copy.deepcopy(pool)

    netris_eip.validate_request(resource, pool, "external_ip.allocate", RESERVATION_NAME)

    assert resource == resource_before
    assert pool == pool_before


def test_reservation_name_is_the_one_the_role_passes_in():
    """The module keeps no name prefix of its own: the request carries the given name."""
    request = netris_eip.validate_request(make_resource(), make_pool(), "external_ip.allocate", "osac-eip-given")
    assert request["reservation_name"] == "osac-eip-given"
    assert not hasattr(netris_eip, "RESERVATION_NAME_PREFIX")


@pytest.mark.parametrize("bad", [None, "", " ", 5, ["osac-eip-x"]])
def test_reservation_name_must_be_a_non_empty_string(bad):
    with pytest.raises(ValueError):
        netris_eip.validate_request(make_resource(), make_pool(), "external_ip.allocate", bad)


def test_validation_needs_no_metadata():
    request = netris_eip.validate_request(
        make_resource(metadata=netris_eip_test_missing), make_pool(metadata=netris_eip_test_missing),
        "external_ip.allocate", RESERVATION_NAME,
    )
    assert request["reservation_name"] == f"osac-eip-{EXTERNAL_IP_ID}"


# U02 - malformed ids, a pool that is not exactly one canonical IPv4 CIDR


@pytest.mark.parametrize(
    "override",
    [
        pytest.param({"id": netris_eip_test_missing}, id="id-absent"),
        pytest.param({"id": None}, id="id-null"),
        pytest.param({"id": ""}, id="id-empty"),
        pytest.param({"id": 12345}, id="id-wrong-type"),
        pytest.param({"id": "not-a-uuid"}, id="id-not-a-uuid"),
        pytest.param({"id": "ABCDEF12-4444-4444-8444-444444444444"}, id="id-uppercase"),
        pytest.param({"id": EXTERNAL_IP_ID + " "}, id="id-whitespace"),
    ],
)
def test_invalid_resource_field_rejects_request(override):
    with pytest.raises(ValueError):
        netris_eip.validate_request(make_resource(**override), make_pool(), "external_ip.allocate", RESERVATION_NAME)


@pytest.mark.parametrize(
    "override",
    [
        pytest.param({"id": netris_eip_test_missing}, id="pool-id-absent"),
        pytest.param({"id": ""}, id="pool-id-empty"),
        pytest.param({"id": "pool"}, id="pool-id-not-a-uuid"),
        pytest.param({"spec": netris_eip_test_missing}, id="spec-absent"),
        pytest.param({"spec": {}}, id="cidrs-absent"),
        pytest.param({"spec": {"cidrs": []}}, id="cidrs-empty"),
        pytest.param({"spec": {"cidrs": "10.20.0.0/24"}}, id="cidrs-not-a-list"),
        pytest.param({"spec": {"cidrs": ["10.20.0.0/24", "10.30.0.0/24"]}}, id="cidrs-two"),
        pytest.param({"spec": {"cidrs": [""]}}, id="cidr-empty"),
        pytest.param({"spec": {"cidrs": ["10.20.0.1/24"]}}, id="cidr-host-bits"),
        pytest.param({"spec": {"cidrs": ["2001:db8::/64"]}}, id="cidr-ipv6"),
        pytest.param({"spec": {"cidrs": ["not-a-cidr"]}}, id="cidr-malformed"),
        pytest.param({"spec": {"cidrs": [None]}}, id="cidr-null"),
    ],
)
def test_invalid_pool_field_rejects_request(override):
    with pytest.raises(ValueError):
        netris_eip.validate_request(make_resource(), make_pool(**override), "external_ip.allocate", RESERVATION_NAME)


@pytest.mark.parametrize("bad", [None, [], "resource", 3])
def test_snapshots_must_be_dictionaries(bad):
    with pytest.raises(ValueError):
        netris_eip.validate_request(bad, make_pool(), "external_ip.allocate", RESERVATION_NAME)
    with pytest.raises(ValueError):
        netris_eip.validate_request(make_resource(), bad, "external_ip.allocate", RESERVATION_NAME)


@pytest.mark.parametrize("operation", ["allocate", "external_ip", "", "external_ip.delete"])
def test_unsupported_operation_rejects_request(operation):
    with pytest.raises(ValueError):
        netris_eip.validate_request(make_resource(), make_pool(), operation, RESERVATION_NAME)


# U04 — canonical IPv4, /31, /32, exhaustion, sparse CIDR, overlaps, malformed prefixes


@pytest.mark.parametrize(
    "cidr,reserved,expected",
    [
        pytest.param("10.20.0.0/24", [], "10.20.0.1", id="empty-pool-first-host"),
        pytest.param("10.20.0.0/24", ["10.20.0.5/32"], "10.20.0.1", id="first-host-free"),
        pytest.param(
            "10.20.0.0/24",
            ["10.20.0.1/32"],
            "10.20.0.2",
            id="reserved-network-address",
        ),
        pytest.param(
            "10.20.0.0/24",
            ["10.20.0.254/32"],
            "10.20.0.1",
            id="reserved-broadcast-address",
        ),
        pytest.param("10.20.0.2/31", [], "10.20.0.2", id="slash31-first-address"),
        pytest.param(
            "10.20.0.2/31", ["10.20.0.2/32"], "10.20.0.3", id="slash31-second-address"
        ),
        pytest.param("10.20.0.9/32", [], "10.20.0.9", id="slash32-single-address"),
        pytest.param(
            "10.20.0.0/24",
            ["10.20.0.0/25", "10.20.0.128/25"],
            None,
            id="exhausted-by-subnets",
        ),
        pytest.param(
            "10.20.0.0/24",
            ["10.20.0.1/32", "10.20.0.2/32", "10.20.0.2/32"],
            "10.20.0.3",
            id="duplicate-reservations",
        ),
        pytest.param(
            "10.20.0.0/24",
            ["192.168.1.0/24"],
            "10.20.0.1",
            id="non-intersecting-prefix-ignored",
        ),
    ],
)
def test_select_address_returns_first_eligible_host(cidr, reserved, expected):
    if expected is None:
        with pytest.raises(ValueError):
            netris_eip.select_address(cidr, reserved)
    else:
        assert netris_eip.select_address(cidr, reserved) == expected


def test_select_address_jumps_broad_occupied_intervals_in_large_cidr():
    occupied = ["10.0.0.0/16"]

    assert netris_eip.select_address("10.0.0.0/8", occupied) == "10.1.0.0"


def test_select_address_finds_gap_between_overlapping_intervals():
    occupied = [
        "10.20.0.0/25",
        "10.20.0.64/26",
        "10.20.0.192/26",
    ]

    assert netris_eip.select_address("10.20.0.0/24", occupied) == "10.20.0.128"


@pytest.mark.parametrize(
    "cidr,reserved",
    [
        pytest.param("10.20.0.1/24", [], id="non-canonical-cidr"),
        pytest.param("2001:db8::/64", [], id="ipv6-cidr"),
        pytest.param("10.20.0.0/24", ["garbage"], id="malformed-reserved-prefix"),
        pytest.param("10.20.0.0/24", ["10.20.0.1"], id="reserved-prefix-without-mask"),
        pytest.param("10.20.0.0/24", [None], id="reserved-prefix-wrong-type"),
    ],
)
def test_select_address_fails_closed_on_malformed_input(cidr, reserved):
    with pytest.raises(ValueError):
        netris_eip.select_address(cidr, reserved)


# U05 — owned reservation reuse; ambiguity, foreign identity, non-/32, out-of-pool


def test_owned_address_reuses_the_single_uid_reservation():
    request = netris_eip.validate_request(make_resource(), make_pool(), "external_ip.allocate", RESERVATION_NAME)

    address = netris_eip.owned_address(
        request,
        [
            make_reservation("10.20.0.5"),
            make_reservation("10.20.0.9", name="osac-eip-some-other-uid"),
        ],
        NETRIS_SITE_ID,
        NETRIS_TENANT_ID,
    )

    assert address == "10.20.0.5"


def test_owned_address_returns_none_when_reservation_is_absent():
    request = netris_eip.validate_request(make_resource(), make_pool(), "external_ip.allocate", RESERVATION_NAME)

    address = netris_eip.owned_address(
        request,
        [make_reservation("10.20.0.5", name="osac-eip-some-other-uid")],
        NETRIS_SITE_ID,
        NETRIS_TENANT_ID,
    )

    assert address is None


def test_owned_address_rejects_ambiguous_reservations():
    request = netris_eip.validate_request(make_resource(), make_pool(), "external_ip.allocate", RESERVATION_NAME)

    with pytest.raises(ValueError):
        netris_eip.owned_address(
            request,
            [
                make_reservation("10.20.0.5"),
                make_reservation("10.20.0.6"),
            ],
            NETRIS_SITE_ID,
            NETRIS_TENANT_ID,
        )


@pytest.mark.parametrize(
    "override",
    [
        pytest.param({"prefix": "10.20.0.5/24"}, id="non-slash32-prefix"),
        pytest.param({"prefix": "192.168.0.5/32"}, id="out-of-pool-prefix"),
        pytest.param({"prefix": "10.20.0.5"}, id="prefix-without-mask"),
        pytest.param({"prefix": "10.20.0.5/33"}, id="malformed-prefix"),
        pytest.param({"prefix": "2001:db8::5/128"}, id="ipv6-prefix"),
        pytest.param({"purpose": "dhcp"}, id="wrong-purpose"),
        pytest.param({"purpose": netris_eip_test_missing}, id="purpose-absent"),
        pytest.param({"site_id": NETRIS_SITE_ID + 1}, id="foreign-site"),
        pytest.param({"site_id": netris_eip_test_missing}, id="site-absent"),
        pytest.param({"tenant_id": NETRIS_TENANT_ID + 1}, id="foreign-provider-tenant"),
        pytest.param({"tenant_id": netris_eip_test_missing}, id="provider-tenant-absent"),
        pytest.param({"name": netris_eip_test_missing}, id="name-absent"),
        pytest.param({"prefix": netris_eip_test_missing}, id="prefix-absent"),
    ],
)
def test_owned_address_fails_closed_on_mismatched_reservation(override):
    request = netris_eip.validate_request(make_resource(), make_pool(), "external_ip.allocate", RESERVATION_NAME)

    with pytest.raises(ValueError):
        netris_eip.owned_address(
            request, [make_reservation(**override)], NETRIS_SITE_ID, NETRIS_TENANT_ID
        )


# U06 — reconcile: new durable reservation, retry reuse, same UID after changed identity


class FakeNetrisClient:
    def __init__(self, lists=(), create_error=None, delete_error=None):
        self._lists = list(lists)
        self._create_error = create_error
        self._delete_error = delete_error
        self.list_calls = 0
        self.created = []
        self.deleted = []

    def list_subnets(self):
        if not self._lists:
            raise AssertionError("unexpected list_subnets call")
        records = self._lists[min(self.list_calls, len(self._lists) - 1)]
        self.list_calls += 1
        return records

    def create_subnet(self, name, prefix, site_id, tenant_id):
        self.created.append((name, prefix, site_id, tenant_id))
        if self._create_error is not None:
            raise self._create_error

    def delete_subnet(self, subnet_id):
        self.deleted.append(subnet_id)
        if self._delete_error is not None:
            raise self._delete_error


def make_request(operation="external_ip.allocate", **overrides):
    return netris_eip.validate_request(make_resource(**overrides), make_pool(), operation, RESERVATION_NAME)


def test_reconcile_allocates_new_durable_reservation():
    client = FakeNetrisClient(lists=[[], [make_reservation("10.20.0.1")]])
    request = make_request()

    result = netris_eip.reconcile(
        request, client, NETRIS_SITE_ID, NETRIS_TENANT_ID, delay_seconds=0
    )

    assert result == {"changed": True, "address": "10.20.0.1"}
    assert client.created == [
        (f"osac-eip-{EXTERNAL_IP_ID}", "10.20.0.1/32", NETRIS_SITE_ID, NETRIS_TENANT_ID)
    ]


def test_reconcile_reuses_existing_uid_reservation_without_writing():
    client = FakeNetrisClient(lists=[[make_reservation("10.20.0.5")]])
    request = make_request()

    result = netris_eip.reconcile(
        request, client, NETRIS_SITE_ID, NETRIS_TENANT_ID, delay_seconds=0
    )

    assert result == {"changed": False, "address": "10.20.0.5"}
    assert client.created == []
    assert client.deleted == []


def test_reconcile_retry_same_id_after_changed_name_and_generation():
    first_request = make_request()
    retry_request = make_request(metadata={"name": "renamed", "uid": "other", "generation": 42})
    assert first_request["reservation_name"] == retry_request["reservation_name"]

    client = FakeNetrisClient(lists=[[make_reservation("10.20.0.9")]])

    first = netris_eip.reconcile(
        first_request, client, NETRIS_SITE_ID, NETRIS_TENANT_ID, delay_seconds=0
    )
    retry = netris_eip.reconcile(
        retry_request, client, NETRIS_SITE_ID, NETRIS_TENANT_ID, delay_seconds=0
    )

    assert first == retry == {"changed": False, "address": "10.20.0.9"}


# U07 — conflict, lost POST response, bounded retries, malformed response fails


def provider_error(category="conflict", status=409):
    return netris_eip.NetrisExternalIPError(
        "Netris IPAM subnet write was rejected", category=category, http_status=status
    )


def test_reconcile_conflict_refreshes_occupancy_and_retries():
    other = make_reservation("10.20.0.1", name="osac-eip-another-uid")
    ours = make_reservation("10.20.0.2")
    client = FakeNetrisClient(
        lists=[[], [other], [other, ours]],
        create_error=provider_error(),
    )
    request = make_request()

    result = netris_eip.reconcile(
        request, client, NETRIS_SITE_ID, NETRIS_TENANT_ID, delay_seconds=0
    )

    assert result == {"changed": True, "address": "10.20.0.2"}
    assert [prefix for _, prefix, _, _ in client.created] == [
        "10.20.0.1/32",
        "10.20.0.2/32",
    ]
    assert client.deleted == []


def test_reconcile_excludes_pool_covering_common_subnet_from_occupancy():
    common = {
        "id": 202,
        "name": "common",
        "prefix": POOL_CIDR,
        "purpose": "shared-network",
        "site_id": NETRIS_SITE_ID,
        "tenant_id": NETRIS_TENANT_ID,
    }
    client = FakeNetrisClient(
        lists=[[common], [common, make_reservation("10.20.0.1")]]
    )
    request = make_request()

    result = netris_eip.reconcile(
        request, client, NETRIS_SITE_ID, NETRIS_TENANT_ID, delay_seconds=0
    )

    assert result == {"changed": True, "address": "10.20.0.1"}
    assert client.created == [
        (f"osac-eip-{EXTERNAL_IP_ID}", "10.20.0.1/32", NETRIS_SITE_ID, NETRIS_TENANT_ID)
    ]
    assert client.deleted == []


def test_reconcile_lost_create_response_reuses_uid_reservation():
    ours = make_reservation("10.20.0.1")
    client = FakeNetrisClient(
        lists=[[], [ours]],
        create_error=netris_eip.NetrisExternalIPError(
            "Netris controller request failed",
            category="transport_error",
        ),
    )
    request = make_request()

    result = netris_eip.reconcile(
        request, client, NETRIS_SITE_ID, NETRIS_TENANT_ID, delay_seconds=0
    )

    assert result == {"changed": True, "address": "10.20.0.1"}
    assert len(client.created) == 1
    assert client.deleted == []


def test_reconcile_fails_closed_after_bounded_retries():
    client = FakeNetrisClient(lists=[[], []], create_error=provider_error())
    request = make_request()

    with pytest.raises(netris_eip.NetrisExternalIPError):
        netris_eip.reconcile(
            request, client, NETRIS_SITE_ID, NETRIS_TENANT_ID, delay_seconds=0
        )
    assert len(client.created) == netris_eip.MAX_ALLOCATE_ATTEMPTS
    assert client.list_calls == netris_eip.MAX_ALLOCATE_ATTEMPTS


def test_reconcile_fails_on_malformed_provider_response():
    client = FakeNetrisClient(lists=[["garbage"]])
    request = make_request()

    with pytest.raises(netris_eip.NetrisExternalIPError):
        netris_eip.reconcile(
            request, client, NETRIS_SITE_ID, NETRIS_TENANT_ID, delay_seconds=0
        )
    assert client.created == []


def test_reconcile_fails_when_verification_shows_a_different_address():
    ours = make_reservation("10.20.0.6")
    client = FakeNetrisClient(lists=[[], [ours]])
    request = make_request()

    with pytest.raises(netris_eip.NetrisExternalIPError):
        netris_eip.reconcile(
            request, client, NETRIS_SITE_ID, NETRIS_TENANT_ID, delay_seconds=0
        )


def test_reconcile_fails_on_pool_exhaustion_without_retrying():
    exhausted_pool = make_pool(spec={"cidrs": ["10.20.0.5/32"]})
    resource = make_resource()
    request = netris_eip.validate_request(resource, exhausted_pool, "external_ip.allocate", RESERVATION_NAME)
    client = FakeNetrisClient(
        lists=[[make_reservation("10.20.0.5", name="osac-eip-other-1")]]
    )

    with pytest.raises(netris_eip.NetrisExternalIPError):
        netris_eip.reconcile(
            request, client, NETRIS_SITE_ID, NETRIS_TENANT_ID, delay_seconds=0
        )
    assert client.list_calls == 1
    assert client.created == []


# U08 — release: absence, delete-then-absence, delayed absence, 404 readback


def test_reconcile_release_absent_is_idempotent_success():
    client = FakeNetrisClient(lists=[[]])
    request = make_request("external_ip.release")

    result = netris_eip.reconcile(
        request, client, NETRIS_SITE_ID, NETRIS_TENANT_ID, delay_seconds=0
    )

    assert result == {"changed": False, "address": ""}
    assert client.deleted == []


def test_reconcile_release_deletes_then_confirms_absence():
    client = FakeNetrisClient(lists=[[make_reservation("10.20.0.5")], []])
    request = make_request("external_ip.release")

    result = netris_eip.reconcile(
        request, client, NETRIS_SITE_ID, NETRIS_TENANT_ID, delay_seconds=0
    )

    assert result == {"changed": True, "address": ""}
    assert client.deleted == [101]


def test_reconcile_release_404_requires_absence_read():
    client = FakeNetrisClient(
        lists=[[make_reservation("10.20.0.5")], []],
        delete_error=netris_eip.NetrisExternalIPError(
            "Netris IPAM subnet is already absent", category="absent", http_status=404
        ),
    )
    request = make_request("external_ip.release")

    result = netris_eip.reconcile(
        request, client, NETRIS_SITE_ID, NETRIS_TENANT_ID, delay_seconds=0
    )

    assert result == {"changed": True, "address": ""}
    assert client.deleted == [101]


def test_reconcile_release_delayed_absence_fails_without_success():
    client = FakeNetrisClient(lists=[[make_reservation("10.20.0.5")]])
    request = make_request("external_ip.release")

    with pytest.raises(netris_eip.NetrisExternalIPError):
        netris_eip.reconcile(
            request, client, NETRIS_SITE_ID, NETRIS_TENANT_ID, delay_seconds=0
        )
    assert len(client.deleted) == netris_eip.MAX_RELEASE_OBSERVATIONS
    assert client.list_calls == netris_eip.MAX_RELEASE_OBSERVATIONS


def test_reconcile_release_unrelated_delete_error_fails():
    client = FakeNetrisClient(
        lists=[[make_reservation("10.20.0.5")]],
        delete_error=netris_eip.NetrisExternalIPError(
            "Netris IPAM subnet delete failed", category="provider_error", http_status=400
        ),
    )
    request = make_request("external_ip.release")

    with pytest.raises(netris_eip.NetrisExternalIPError):
        netris_eip.reconcile(
            request, client, NETRIS_SITE_ID, NETRIS_TENANT_ID, delay_seconds=0
        )
    assert client.deleted == [101]


def test_reconcile_release_deletes_only_the_owned_subnet():
    other = make_reservation("10.20.0.5", name="osac-eip-another-uid", id=101)
    ours = make_reservation("10.20.0.9", id=102)
    client = FakeNetrisClient(lists=[[other, ours], []])
    request = make_request("external_ip.release")

    result = netris_eip.reconcile(
        request, client, NETRIS_SITE_ID, NETRIS_TENANT_ID, delay_seconds=0
    )

    assert result == {"changed": True, "address": ""}
    assert client.deleted == [102]


# Transport client — raw provider record mapping at the boundary


def test_client_maps_raw_provider_records_to_normalized_shape(monkeypatch):
    raw = {
        "id": 101,
        "name": f"osac-eip-{EXTERNAL_IP_ID}",
        "prefix": "10.20.0.5/32",
        "purpose": "nat",
        "sites": [{"id": 3}],
        "tenant": {"id": 7},
        "vpc": {"id": 9},
    }
    client = netris_eip.NetrisSubnetClient(object(), "https://netris.example", "secret")

    monkeypatch.setattr(
        netris_eip, "fetch_url", lambda *args, **kwargs: (io.BytesIO(json.dumps({"data": [raw]}).encode()), {"status": 200})
    )
    records = client.list_subnets()

    assert records == [
        {
            "id": 101,
            "name": f"osac-eip-{EXTERNAL_IP_ID}",
            "prefix": "10.20.0.5/32",
            "purpose": "nat",
            "site_id": 3,
            "tenant_id": 7,
        }
    ]


@pytest.mark.parametrize(
    "raw",
    [
        pytest.param({"name": "x", "prefix": "10.0.0.1/32"}, id="missing-fields"),
        pytest.param(
            {
                "id": "not-an-int",
                "name": "x",
                "prefix": "10.0.0.1/32",
                "purpose": "nat",
                "sites": [{"id": 1}],
                "tenant": {"id": 1},
            },
            id="wrong-types",
        ),
        pytest.param(
            {
                "id": 1,
                "name": "x",
                "prefix": "garbage",
                "purpose": "nat",
                "sites": [{"id": 1}],
                "tenant": {"id": 1},
            },
            id="malformed-prefix",
        ),
    ],
)
def test_client_fails_closed_on_unknown_record_shape(monkeypatch, raw):
    client = netris_eip.NetrisSubnetClient(object(), "https://netris.example", "secret")

    monkeypatch.setattr(
        netris_eip,
        "fetch_url",
        lambda *args, **kwargs: (io.BytesIO(json.dumps({"data": [raw]}).encode()), {"status": 200}),
    )

    with pytest.raises(netris_eip.NetrisExternalIPError):
        client.list_subnets()
