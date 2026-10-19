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

RESOURCE_UID = "11111111-1111-4111-8111-111111111111"
POOL_FULFILLMENT_ID = "22222222-2222-4222-8222-222222222222"
POOL_UID = "33333333-3333-4333-8333-333333333333"
EXTERNAL_IP_ID = "44444444-4444-4444-8444-444444444444"
TENANT = "tenant-a"
POOL_CIDR = "10.20.0.0/24"
NETRIS_SITE_ID = 1
NETRIS_TENANT_ID = 7


def make_resource(**overrides):
    resource = {
        "metadata": {
            "name": "eip-test",
            "namespace": "osac",
            "uid": RESOURCE_UID,
            "generation": 3,
            "labels": {
                "osac.openshift.io/externalip-uuid": EXTERNAL_IP_ID,
            },
            "annotations": {
                "osac.openshift.io/tenant": TENANT,
                "osac.openshift.io/externalip-contract-version": "v1",
            },
        },
        "spec": {"pool": POOL_FULFILLMENT_ID},
    }
    for path, value in overrides.items():
        field = path.split(".")
        target = resource
        for key in field[:-1]:
            if value is netris_eip_test_missing:
                target.pop(key, None)
                target = None
                break
            target = target.setdefault(key, {})
        else:
            if value is netris_eip_test_missing:
                target.pop(field[-1], None)
            else:
                target[field[-1]] = value
    return resource


def make_pool(**overrides):
    pool = {
        "fulfillment_id": POOL_FULFILLMENT_ID,
        "kubernetes_uid": POOL_UID,
        "tenant_id": TENANT,
        "cidr": POOL_CIDR,
        "phase": "Ready",
    }
    pool.update(overrides)
    return {key: value for key, value in pool.items() if value is not netris_eip_test_missing}


class netris_eip_test_missing:
    """Sentinel marking a key that must be removed from a fixture."""


def make_reservation(address="10.20.0.5", **overrides):
    record = {
        "id": 101,
        "name": f"osac-eip-{RESOURCE_UID}",
        "prefix": f"{address}/32",
        "purpose": "nat",
        "site_id": NETRIS_SITE_ID,
        "tenant_id": NETRIS_TENANT_ID,
    }
    record.update(overrides)
    return {key: value for key, value in record.items() if value is not netris_eip_test_missing}


# U01 — valid request, unchanged inputs, stable UID name, exact normalized identity


def test_valid_allocate_request_normalizes_exact_identity():
    resource = make_resource()
    pool = make_pool()

    request = netris_eip.validate_request(resource, pool, "external_ip.allocate")

    assert request == {
        "operation": "external_ip.allocate",
        "uid": RESOURCE_UID,
        "generation": 3,
        "reservation_name": f"osac-eip-{RESOURCE_UID}",
        "resource_id": EXTERNAL_IP_ID,
        "tenant_id": TENANT,
        "pool_id": POOL_FULFILLMENT_ID,
        "pool_uid": POOL_UID,
        "pool_tenant_id": TENANT,
        "cidr": POOL_CIDR,
        "phase": "Ready",
    }


def test_valid_release_request_normalizes_exact_identity():
    resource = make_resource(**{"metadata.deletionTimestamp": "2026-10-19T00:00:00Z"})
    pool = make_pool(phase="Deleting")

    request = netris_eip.validate_request(resource, pool, "external_ip.release")

    assert request["operation"] == "external_ip.release"
    assert request["reservation_name"] == f"osac-eip-{RESOURCE_UID}"
    assert request["phase"] == "Deleting"


def test_valid_request_does_not_mutate_inputs():
    resource = make_resource()
    pool = make_pool()
    resource_before = copy.deepcopy(resource)
    pool_before = copy.deepcopy(pool)

    netris_eip.validate_request(resource, pool, "external_ip.allocate")

    assert resource == resource_before
    assert pool == pool_before


def test_reservation_name_is_stable_across_display_name_and_generation():
    first = netris_eip.validate_request(
        make_resource(), make_pool(), "external_ip.allocate"
    )
    second = netris_eip.validate_request(
        make_resource(**{"metadata.name": "renamed", "metadata.generation": 99}),
        make_pool(),
        "external_ip.allocate",
    )

    assert first["reservation_name"] == second["reservation_name"]
    assert second["reservation_name"] == f"osac-eip-{RESOURCE_UID}"


# U02 — absent/wrong-typed required fields, mismatched pool/tenant, shared pool


@pytest.mark.parametrize(
    "override",
    [
        pytest.param({"metadata": netris_eip_test_missing}, id="metadata-absent"),
        pytest.param({"metadata.uid": None}, id="uid-null"),
        pytest.param({"metadata.uid": ""}, id="uid-empty"),
        pytest.param({"metadata.uid": 12345}, id="uid-wrong-type"),
        pytest.param({"metadata.uid": netris_eip_test_missing}, id="uid-absent"),
        pytest.param({"metadata.generation": 0}, id="generation-zero"),
        pytest.param({"metadata.generation": -1}, id="generation-negative"),
        pytest.param({"metadata.generation": True}, id="generation-boolean"),
        pytest.param({"metadata.generation": "3"}, id="generation-string"),
        pytest.param({"metadata.generation": netris_eip_test_missing}, id="generation-absent"),
        pytest.param(
            {"metadata.labels": netris_eip_test_missing}, id="labels-absent"
        ),
        pytest.param(
            {"metadata.labels": {}}, id="externalip-uuid-label-absent"
        ),
        pytest.param(
            {"metadata.labels": {"osac.openshift.io/externalip-uuid": ""}},
            id="externalip-uuid-label-empty",
        ),
        pytest.param(
            {
                "metadata.labels": {
                    "osac.openshift.io/externalip-uuid": EXTERNAL_IP_ID,
                },
                "metadata.annotations": {"osac.openshift.io/tenant": TENANT},
            },
            id="contract-version-annotation-absent",
        ),
        pytest.param(
            {
                "metadata.annotations": {
                    "osac.openshift.io/tenant": TENANT,
                    "osac.openshift.io/externalip-contract-version": "",
                }
            },
            id="contract-version-annotation-empty",
        ),
        pytest.param(
            {
                "metadata.annotations": {
                    "osac.openshift.io/tenant": "",
                    "osac.openshift.io/externalip-contract-version": "v1",
                }
            },
            id="resource-tenant-empty",
        ),
        pytest.param(
            {
                "metadata.annotations": {
                    "osac.openshift.io/externalip-contract-version": "v1",
                },
            },
            id="resource-tenant-absent",
        ),
        pytest.param({"spec": {"pool": ""}}, id="pool-reference-empty"),
        pytest.param({"spec": {}}, id="pool-reference-absent"),
        pytest.param({"spec": netris_eip_test_missing}, id="spec-absent"),
    ],
)
def test_invalid_resource_field_rejects_request(override):
    resource = make_resource(**override)
    pool = make_pool()

    with pytest.raises(ValueError):
        netris_eip.validate_request(resource, pool, "external_ip.allocate")


@pytest.mark.parametrize(
    "override",
    [
        pytest.param({"fulfillment_id": None}, id="pool-fulfillment-id-null"),
        pytest.param({"fulfillment_id": ""}, id="pool-fulfillment-id-empty"),
        pytest.param(
            {"fulfillment_id": netris_eip_test_missing}, id="pool-fulfillment-id-absent"
        ),
        pytest.param({"kubernetes_uid": ""}, id="pool-uid-empty"),
        pytest.param({"kubernetes_uid": netris_eip_test_missing}, id="pool-uid-absent"),
        pytest.param({"tenant_id": ""}, id="pool-tenant-empty"),
        pytest.param({"tenant_id": netris_eip_test_missing}, id="pool-tenant-absent"),
        pytest.param({"cidr": ""}, id="pool-cidr-empty"),
        pytest.param({"cidr": "10.20.0.1/24"}, id="pool-cidr-host-bits"),
        pytest.param({"cidr": "2001:db8::/64"}, id="pool-cidr-ipv6"),
        pytest.param({"cidr": "not-a-cidr"}, id="pool-cidr-malformed"),
        pytest.param({"cidr": netris_eip_test_missing}, id="pool-cidr-absent"),
        pytest.param({"phase": ""}, id="pool-phase-empty"),
        pytest.param({"phase": netris_eip_test_missing}, id="pool-phase-absent"),
        pytest.param(
            {"fulfillment_id": "99999999-9999-4999-8999-999999999999"},
            id="pool-reference-mismatch",
        ),
        pytest.param({"tenant_id": "tenant-b"}, id="tenant-mismatch"),
    ],
)
def test_invalid_pool_field_rejects_request(override):
    resource = make_resource()
    pool = make_pool(**override)

    with pytest.raises(ValueError):
        netris_eip.validate_request(resource, pool, "external_ip.allocate")


def test_shared_pool_tenant_is_allowed():
    resource = make_resource()
    pool = make_pool(tenant_id="shared")

    request = netris_eip.validate_request(resource, pool, "external_ip.allocate")

    assert request["pool_tenant_id"] == "shared"
    assert request["tenant_id"] == TENANT


# U03 — allocate/release phase and deletion ordering, version/operation guards


def test_allocate_requires_ready_pool():
    with pytest.raises(ValueError):
        netris_eip.validate_request(
            make_resource(), make_pool(phase="Deleting"), "external_ip.allocate"
        )
    with pytest.raises(ValueError):
        netris_eip.validate_request(
            make_resource(), make_pool(phase="Provisioning"), "external_ip.allocate"
        )


def test_allocate_rejects_deleting_resource():
    resource = make_resource(**{"metadata.deletionTimestamp": "2026-10-19T00:00:00Z"})

    with pytest.raises(ValueError):
        netris_eip.validate_request(resource, make_pool(), "external_ip.allocate")


def test_release_accepts_non_ready_pool():
    resource = make_resource(**{"metadata.deletionTimestamp": "2026-10-19T00:00:00Z"})

    request = netris_eip.validate_request(
        resource, make_pool(phase="Deleting"), "external_ip.release"
    )

    assert request["operation"] == "external_ip.release"


def test_release_requires_deleting_resource():
    with pytest.raises(ValueError):
        netris_eip.validate_request(make_resource(), make_pool(), "external_ip.release")


@pytest.mark.parametrize("version", ["v2", "", "V1"])
def test_unsupported_contract_version_rejects_request(version):
    resource = make_resource(
        **{"metadata.annotations": {
            "osac.openshift.io/tenant": TENANT,
            "osac.openshift.io/externalip-contract-version": version,
        }}
    )

    with pytest.raises(ValueError):
        netris_eip.validate_request(resource, make_pool(), "external_ip.allocate")


@pytest.mark.parametrize("operation", ["allocate", "external_ip", "", "external_ip.delete"])
def test_unsupported_operation_rejects_request(operation):
    with pytest.raises(ValueError):
        netris_eip.validate_request(make_resource(), make_pool(), operation)


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
    request = netris_eip.validate_request(make_resource(), make_pool(), "external_ip.allocate")

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
    request = netris_eip.validate_request(make_resource(), make_pool(), "external_ip.allocate")

    address = netris_eip.owned_address(
        request,
        [make_reservation("10.20.0.5", name="osac-eip-some-other-uid")],
        NETRIS_SITE_ID,
        NETRIS_TENANT_ID,
    )

    assert address is None


def test_owned_address_rejects_ambiguous_reservations():
    request = netris_eip.validate_request(make_resource(), make_pool(), "external_ip.allocate")

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
    request = netris_eip.validate_request(make_resource(), make_pool(), "external_ip.allocate")

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
    if operation == "external_ip.release":
        overrides.setdefault("metadata.deletionTimestamp", "2026-10-19T00:00:00Z")
    resource = make_resource(**overrides)
    pool = make_pool(**({"phase": "Deleting"} if operation == "external_ip.release" else {}))
    return netris_eip.validate_request(resource, pool, operation)


def test_reconcile_allocates_new_durable_reservation():
    client = FakeNetrisClient(lists=[[], [make_reservation("10.20.0.1")]])
    request = make_request()

    result = netris_eip.reconcile(
        request, client, NETRIS_SITE_ID, NETRIS_TENANT_ID, delay_seconds=0
    )

    assert result == {"changed": True, "address": "10.20.0.1"}
    assert client.created == [
        (f"osac-eip-{RESOURCE_UID}", "10.20.0.1/32", NETRIS_SITE_ID, NETRIS_TENANT_ID)
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


def test_reconcile_retry_same_uid_after_changed_name_and_generation():
    first_request = make_request()
    retry_request = make_request(**{"metadata.name": "renamed", "metadata.generation": 42})
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
        (f"osac-eip-{RESOURCE_UID}", "10.20.0.1/32", NETRIS_SITE_ID, NETRIS_TENANT_ID)
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
    exhausted_pool = make_pool(cidr="10.20.0.5/32")
    resource = make_resource()
    request = netris_eip.validate_request(resource, exhausted_pool, "external_ip.allocate")
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
        "name": f"osac-eip-{RESOURCE_UID}",
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
            "name": f"osac-eip-{RESOURCE_UID}",
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
