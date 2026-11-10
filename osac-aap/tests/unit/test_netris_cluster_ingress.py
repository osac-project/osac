"""Contract and IPAM behavior of the Netris ClusterOrder ingress reservation."""

from __future__ import annotations

import contextlib
import importlib.util
import secrets
import sys
import threading
from collections.abc import Iterator
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
from types import ModuleType, SimpleNamespace
from typing import Any

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
sys.path.insert(0, str(MODULE_UTILS.parents[4]))

from ansible_collections.osac.templates.plugins.module_utils import netris_cluster_ingress as ingress  # noqa: E402
from ansible_collections.osac.templates.plugins.module_utils import netris_external_ip  # noqa: E402

FAKE_NETRIS_PATH = (
    Path(__file__).resolve().parents[1]
    / "integration"
    / "fixtures"
    / "netris"
    / "fake_netris.py"
)
FAKE_NETRIS_SPEC = importlib.util.spec_from_file_location("fake_netris", FAKE_NETRIS_PATH)
fake_netris = importlib.util.module_from_spec(FAKE_NETRIS_SPEC)
FAKE_NETRIS_SPEC.loader.exec_module(fake_netris)


def _load_fake_netris(
    monkeypatch: pytest.MonkeyPatch, supplied_cookie: str | None
) -> ModuleType:
    """Load the fake provider with either a supplied or generated cookie."""
    if supplied_cookie is None:
        monkeypatch.delenv("NF_SESSION_COOKIE", raising=False)
    else:
        monkeypatch.setenv("NF_SESSION_COOKIE", supplied_cookie)
    spec = importlib.util.spec_from_file_location("fake_netris_auth_test", FAKE_NETRIS_PATH)
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


@pytest.mark.parametrize("supplied", [False, True], ids=["generated", "supplied"])
def test_fake_netris_authenticates_generated_and_supplied_cookies(
    monkeypatch: pytest.MonkeyPatch, supplied: bool
) -> None:
    """Accept the fake server's generated cookie and an environment-supplied one."""
    supplied_cookie = secrets.token_urlsafe(32) if supplied else None
    module = _load_fake_netris(monkeypatch, supplied_cookie)
    if supplied_cookie is None:
        assert len(module.COOKIE) >= 32

    handler = object.__new__(module.Handler)
    valid_cookie = supplied_cookie if supplied_cookie is not None else module.COOKIE
    handler.headers = {
        "Cookie": f"other={secrets.token_urlsafe(32)}; connect.sid={valid_cookie}"
    }
    assert handler._logged_in()
    handler.headers = {"Cookie": "connect.sid="}
    assert not handler._logged_in()


def test_fake_netris_redacts_authentication_bodies_from_request_logs():
    credentials = {"username": "fixture-user", "password": secrets.token_urlsafe(32)}

    assert fake_netris.Handler._logged_body("/api/auth", credentials) == {"redacted": True}
    assert fake_netris.Handler._logged_body("/api/v2/vnet", {"name": "fixture-vnet"}) == {
        "name": "fixture-vnet"
    }

CLUSTER_ORDER_ID = "9f8e7d6c-5b4a-4321-8765-4c3b2a109876"
VIRTUAL_NETWORK_ID = "3f1c9a2e-7b4d-4e8a-9c6f-0d2e4b6a8c10"
OTHER_VIRTUAL_NETWORK_ID = "4a2d0b3f-8c5e-4f9a-a7d0-1e3f5b7c9d20"
SUBNET_ID = "8a2b4c6d-1e3f-4a5b-9c7d-2e4f6a8b0c12"
RESERVATION_NAME = f"osac-ci-{CLUSTER_ORDER_ID}"
SITE_ID = 1
TENANT_ID = 7
VPC_ID = 9


def make_resource(**overrides):
    resource = {
        "kind": "ClusterOrder",
        "id": CLUSTER_ORDER_ID,
        "metadata": {"name": "cluster", "namespace": "clusters"},
        "spec": {"networkAttachment": {"subnetRef": "cluster-subnet"}},
    }
    resource.update(overrides)
    return resource


def make_subnet(**overrides):
    subnet = {
        "kind": "Subnet",
        "id": SUBNET_ID,
        "metadata": {"name": "cluster-subnet", "namespace": "osac-networking"},
        "spec": {"ipv4Cidr": "10.20.1.0/24"},
    }
    subnet.update(overrides)
    return subnet


def make_virtual_network(**overrides):
    virtual_network = {
        "kind": "VirtualNetwork",
        "id": VIRTUAL_NETWORK_ID,
        "metadata": {"name": "cluster-vn", "namespace": "osac-networking"},
        "spec": {"ipv4Cidr": "10.20.0.0/16"},
    }
    virtual_network.update(overrides)
    return virtual_network


def make_ipam(cidrs=None):
    return {
        "gateway_ipv4": "10.20.1.1",
        "lb_vip_ipv4_cidrs": ["10.20.1.16/28"] if cidrs is None else cidrs,
    }


def request(operation="cluster_ingress.allocate", **overrides):
    args = {
        "resource": make_resource(),
        "virtual_network": make_virtual_network(),
        "cluster_id": CLUSTER_ORDER_ID,
        "subnet": make_subnet(),
        "ipam": make_ipam(),
        "operation": operation,
        "reservation_name": RESERVATION_NAME,
        "vpc_id": VPC_ID,
    }
    args.update(overrides)
    return ingress.validate_request(**args)


def reservation(address="10.20.1.17", **overrides):
    record = {
        "id": 101,
        "name": RESERVATION_NAME,
        "prefix": f"{address}/32",
        "purpose": "nat",
        "site_id": SITE_ID,
        "tenant_id": TENANT_ID,
        "vpc_id": VPC_ID,
    }
    record.update(overrides)
    return record


class FakeNetrisClient:
    def __init__(self, records=(), create_error=None, delete_error=None):
        self.records = list(records)
        self.create_error = create_error
        self.delete_error = delete_error
        self.created = []
        self.deleted = []

    def list_subnets(self):
        return list(self.records)

    def create_subnet(self, name, prefix, site_id, tenant_id, vpc_id):
        self.created.append((name, prefix, site_id, tenant_id, vpc_id))
        self.records.append({
            "id": 101 + len(self.created), "name": name, "prefix": prefix,
            "purpose": "nat", "site_id": site_id, "tenant_id": tenant_id,
            "vpc_id": vpc_id,
        })
        if self.create_error is not None:
            raise self.create_error

    def delete_subnet(self, subnet_id):
        self.deleted.append(subnet_id)
        if self.delete_error is not None:
            raise self.delete_error
        self.records = [record for record in self.records if record["id"] != subnet_id]


class DelayedReservationVisibilityClient(FakeNetrisClient):
    def __init__(self, stale_reads=0, never_visible=False, **kwargs):
        super().__init__(**kwargs)
        self.stale_reads = stale_reads
        self.never_visible = never_visible

    def list_subnets(self):
        records = super().list_subnets()
        if not self.created:
            return records
        if self.never_visible:
            return [record for record in records if record["name"] != RESERVATION_NAME]
        if self.stale_reads > 0:
            self.stale_reads -= 1
            return [record for record in records if record["name"] != RESERVATION_NAME]
        return records


class DelayedConflictVisibilityClient(FakeNetrisClient):
    def __init__(self, stale_reads):
        super().__init__()
        self.stale_conflict_reads = stale_reads
        self.conflict_started = False
        self.create_attempts = []

    def list_subnets(self):
        records = super().list_subnets()
        if self.conflict_started and self.stale_conflict_reads > 0:
            self.stale_conflict_reads -= 1
            return [record for record in records if record["name"] != "osac-ci-other"]
        return records

    def create_subnet(self, name, prefix, site_id, tenant_id, vpc_id):
        self.create_attempts.append((name, prefix, site_id, tenant_id, vpc_id))
        if len(self.create_attempts) == 1:
            self.conflict_started = True
            self.records.append(reservation(
                "10.20.1.16", name="osac-ci-other", id=300
            ))
            raise netris_external_ip.NetrisExternalIPError(
                "Netris IPAM subnet write was rejected",
                category="provider_error",
                http_status=409,
            )
        return super().create_subnet(name, prefix, site_id, tenant_id, vpc_id)


class RejectedCreateClient(FakeNetrisClient):
    def __init__(self, error):
        super().__init__()
        self.error = error

    def create_subnet(self, name, prefix, site_id, tenant_id, vpc_id):
        self.created.append((name, prefix, site_id, tenant_id, vpc_id))
        raise self.error


@contextlib.contextmanager
def running_fake_netris() -> Iterator[Any]:
    server = fake_netris.Netris()
    worker = threading.Thread(target=server.serve_forever, daemon=True)
    worker.start()
    try:
        yield server
    finally:
        server.shutdown()
        server.server_close()
        worker.join(timeout=5)


class FirstReadBarrierClient:
    """Make two allocator calls use the same initial provider snapshot."""

    def __init__(self, client: Any, barrier: threading.Barrier) -> None:
        self.client = client
        self.barrier = barrier
        self.first_read = True

    def list_subnets(self) -> list[dict[str, Any]]:
        subnets = self.client.list_subnets()
        if self.first_read:
            self.first_read = False
            self.barrier.wait(timeout=5)
        return subnets

    def create_subnet(self, *args: Any, **kwargs: Any) -> Any:
        return self.client.create_subnet(*args, **kwargs)

    def delete_subnet(self, *args: Any, **kwargs: Any) -> Any:
        return self.client.delete_subnet(*args, **kwargs)


def make_http_client(server: Any, tmp_path: Path) -> Any:
    module = SimpleNamespace(params={"use_netrc": False}, tmpdir=str(tmp_path))
    return netris_external_ip.NetrisSubnetClient(
        module,
        f"http://127.0.0.1:{server.server_port}",
        fake_netris.COOKIE,
    )


def test_valid_request_is_bound_to_clusterorder_and_subnet():
    parsed = request()

    assert parsed == {
        "operation": "cluster_ingress.allocate",
        "reservation_name": RESERVATION_NAME,
        "resource_id": CLUSTER_ORDER_ID,
        "cluster_id": CLUSTER_ORDER_ID,
        "virtual_network_id": VIRTUAL_NETWORK_ID,
        "subnet_id": SUBNET_ID,
        "vpc_id": VPC_ID,
        "gateway_ipv4": "10.20.1.1",
        "subnet_network": "10.20.1.0",
        "subnet_broadcast": "10.20.1.255",
        "cidrs": ["10.20.1.16/28"],
    }


@pytest.mark.parametrize(
    "overrides, message",
    [
        ({"resource": {"kind": "Subnet", "id": CLUSTER_ORDER_ID}}, "ClusterOrder"),
        ({"reservation_name": "osac-ci-someone-else"}, "derived"),
        ({"subnet": make_subnet(metadata={"name": "other-subnet"})}, "subnetRef"),
        (
            {"ipam": {"gateway_ipv4": "10.20.1.1", "lb_vip_ipv4_cidrs": ["10.20.1.17/24"]}},
            "canonical IPv4 CIDR",
        ),
        ({"ipam": make_ipam(["10.20.2.0/24"])}, "inside subnet.spec.ipv4Cidr"),
        (
            {
                "ipam": {
                    "gateway_ipv4": "10.20.1.1",
                    "lb_vip_ipv4_cidrs": ["10.20.1.16/28", "10.20.1.24/29"],
                }
            },
            "overlap",
        ),
        (
            {"ipam": make_ipam(), "subnet": make_subnet(spec={"ipv4Cidr": "10.20.2.0/24"})},
            "ipam.gateway_ipv4 must be inside",
        ),
    ],
)
def test_invalid_request_fails_before_provider_access(overrides, message):
    with pytest.raises(ValueError, match=message):
        request(**overrides)


def test_reservation_name_uses_the_explicit_cluster_api_id():
    cluster_id = "0f1e2d3c-4b5a-4968-8776-655443322110"
    parsed = request(cluster_id=cluster_id, reservation_name=f"osac-ci-{cluster_id}")
    assert parsed["cluster_id"] == cluster_id
    assert parsed["reservation_name"] == f"osac-ci-{cluster_id}"


def test_allocate_uses_recorded_lb_vip_and_reuses_the_cluster_reservation():
    client = FakeNetrisClient(records=[{
        "id": 200, "name": "osac-sn-subnet", "prefix": "10.20.1.0/24",
        "purpose": "vnet", "site_id": SITE_ID, "tenant_id": TENANT_ID,
        "vpc_id": VPC_ID,
    }])

    first = ingress.reconcile(request(), client, SITE_ID, TENANT_ID, delay_seconds=0)
    second = ingress.reconcile(request(), client, SITE_ID, TENANT_ID, delay_seconds=0)

    assert first == {"changed": True, "address": "10.20.1.16"}
    assert second == {"changed": False, "address": "10.20.1.16"}
    assert client.created == [(RESERVATION_NAME, "10.20.1.16/32", SITE_ID, TENANT_ID, VPC_ID)]


@pytest.mark.parametrize("create_response_lost", [False, True])
def test_allocate_observes_a_pending_create_without_submitting_another(
    create_response_lost: bool,
) -> None:
    error = None
    if create_response_lost:
        error = netris_external_ip.NetrisExternalIPError(
            "Netris response was lost after the write",
            category="transport_error",
            http_status=-1,
        )
    client = DelayedReservationVisibilityClient(stale_reads=2, create_error=error)

    result = ingress.reconcile(request(), client, SITE_ID, TENANT_ID, delay_seconds=0)

    assert result == {"changed": True, "address": "10.20.1.16"}
    assert len(client.created) == 1


def test_allocate_fails_uncertain_without_repeating_an_unobserved_create() -> None:
    client = DelayedReservationVisibilityClient(never_visible=True)

    with pytest.raises(netris_external_ip.NetrisExternalIPError) as error:
        ingress.reconcile(request(), client, SITE_ID, TENANT_ID, delay_seconds=0)

    assert error.value.category == "uncertain_state"
    assert len(client.created) == 1


def test_allocate_does_not_retry_a_definitively_rejected_create() -> None:
    rejected = netris_external_ip.NetrisExternalIPError(
        "Netris rejected the reservation",
        category="provider_error",
        http_status=400,
    )
    client = RejectedCreateClient(rejected)

    with pytest.raises(netris_external_ip.NetrisExternalIPError) as error:
        ingress.reconcile(request(), client, SITE_ID, TENANT_ID, delay_seconds=0)

    assert error.value is rejected
    assert len(client.created) == 1


def test_allocate_waits_until_a_409_conflict_is_visible_before_selecting_again() -> None:
    client = DelayedConflictVisibilityClient(stale_reads=2)

    result = ingress.reconcile(request(), client, SITE_ID, TENANT_ID, delay_seconds=0)

    assert result == {"changed": True, "address": "10.20.1.17"}
    assert [attempt[1] for attempt in client.create_attempts] == [
        "10.20.1.16/32",
        "10.20.1.17/32",
    ]
    assert len(client.records) == 2


def test_allocate_observes_its_reservation_after_a_409_before_retrying() -> None:
    error = netris_external_ip.NetrisExternalIPError(
        "Netris returned conflict after saving the reservation",
        category="provider_error",
        http_status=409,
    )
    client = DelayedReservationVisibilityClient(stale_reads=2, create_error=error)

    result = ingress.reconcile(request(), client, SITE_ID, TENANT_ID, delay_seconds=0)

    assert result == {"changed": True, "address": "10.20.1.16"}
    assert len(client.created) == 1


def test_concurrent_allocations_retry_after_duplicate_prefix_conflict(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv("no_proxy", "127.0.0.1")
    monkeypatch.setenv("NO_PROXY", "127.0.0.1")
    other_cluster_id = "0f1e2d3c-4b5a-4968-8776-655443322110"
    requests = [
        request(),
        request(
            resource=make_resource(id=other_cluster_id),
            cluster_id=other_cluster_id,
            reservation_name=f"osac-ci-{other_cluster_id}",
        ),
    ]

    with running_fake_netris() as server:
        initial_reads = threading.Barrier(2)

        def allocate(cluster_request):
            client = FirstReadBarrierClient(
                make_http_client(server, tmp_path), initial_reads
            )
            return ingress.reconcile(
                cluster_request, client, SITE_ID, TENANT_ID, delay_seconds=0
            )

        with ThreadPoolExecutor(max_workers=2) as executor:
            futures = [executor.submit(allocate, item) for item in requests]
            results = [future.result() for future in futures]

        records = server.state["ipam_subnets"]
        creates = [
            call for call in server.calls
            if call[0] == "POST" and call[1] == "/api/v2/ipam/subnet"
        ]

    addresses = {result["address"] for result in results}
    assert addresses == {"10.20.1.16", "10.20.1.17"}
    assert len(creates) == 3  # one conflicting claim, then two accepted claims
    assert len(records) == 2
    assert len({record["prefix"] for record in records}) == 2


def test_netris_ipam_allows_the_same_prefix_in_different_vpcs(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv("no_proxy", "127.0.0.1")
    monkeypatch.setenv("NO_PROXY", "127.0.0.1")
    with running_fake_netris() as server:
        client = make_http_client(server, tmp_path)
        client.create_subnet("vpc-one", "10.20.1.16/32", SITE_ID, TENANT_ID, VPC_ID)
        client.create_subnet(
            "vpc-two", "10.20.1.16/32", SITE_ID, TENANT_ID, VPC_ID + 1
        )

        records = client.list_subnets()

    assert {record["vpc_id"] for record in records} == {VPC_ID, VPC_ID + 1}


def test_allocate_ignores_reservations_in_an_overlapping_virtual_network():
    request_one = request()
    request_two = request(
        resource=make_resource(
            id="0f1e2d3c-4b5a-4968-8776-655443322110",
            spec={"networkAttachment": {"subnetRef": "other-subnet"}},
        ),
        virtual_network=make_virtual_network(
            id=OTHER_VIRTUAL_NETWORK_ID,
            metadata={"name": "other-vn", "namespace": "osac-networking"},
        ),
        subnet=make_subnet(
            id="7b3c5d6e-2f4a-4b6c-8d9e-3f5a7b9c0d21",
            metadata={"name": "other-subnet", "namespace": "osac-networking"},
        ),
        cluster_id="0f1e2d3c-4b5a-4968-8776-655443322110",
        reservation_name="osac-ci-0f1e2d3c-4b5a-4968-8776-655443322110",
        vpc_id=VPC_ID + 1,
    )
    assert request_one["virtual_network_id"] != request_two["virtual_network_id"]
    assert request_one["cidrs"] == request_two["cidrs"]
    other_vpc_reservations = [
        reservation(
            f"10.20.1.{address}",
            name=f"osac-ci-other-vn-{address}",
            id=200 + address,
            vpc_id=request_two["vpc_id"],
        )
        for address in range(16, 20)
    ]
    client = FakeNetrisClient(records=other_vpc_reservations)

    result = ingress.reconcile(request(), client, SITE_ID, TENANT_ID, delay_seconds=0)

    assert result == {"changed": True, "address": "10.20.1.16"}
    assert client.created == [(RESERVATION_NAME, "10.20.1.16/32", SITE_ID, TENANT_ID, VPC_ID)]


def test_allocate_fails_closed_when_an_overlapping_record_omits_its_vpc():
    client = FakeNetrisClient(records=[{
        "id": 200, "name": "unknown-vpc", "prefix": "10.20.1.16/32",
        "purpose": "nat", "site_id": SITE_ID, "tenant_id": TENANT_ID,
    }])

    with pytest.raises(netris_external_ip.NetrisExternalIPError) as error:
        ingress.reconcile(request(), client, SITE_ID, TENANT_ID, delay_seconds=0)

    assert error.value.category == "malformed_response"
    assert client.created == []


def test_allocate_skips_common_parent_without_a_vpc():
    client = FakeNetrisClient(records=[{
        "id": 200, "name": "common-parent", "prefix": "10.20.1.0/24",
        "purpose": "common", "site_id": SITE_ID, "tenant_id": TENANT_ID,
    }])

    result = ingress.reconcile(request(), client, SITE_ID, TENANT_ID, delay_seconds=0)

    assert result == {"changed": True, "address": "10.20.1.16"}
    assert client.created == [(RESERVATION_NAME, "10.20.1.16/32", SITE_ID, TENANT_ID, VPC_ID)]


def test_allocate_fails_closed_for_reservation_without_a_vpc():
    client = FakeNetrisClient(records=[
        reservation(
            "10.20.1.16",
            name="osac-ci-other-cluster",
            prefix="10.20.1.0/24",
            vpc_id=None,
        )
    ])

    with pytest.raises(netris_external_ip.NetrisExternalIPError) as error:
        ingress.reconcile(request(), client, SITE_ID, TENANT_ID, delay_seconds=0)

    assert error.value.category == "malformed_response"
    assert client.created == []


def test_allocate_skips_an_occupied_range_and_uses_the_next_recorded_range():
    client = FakeNetrisClient(records=[
        reservation("10.20.1.16", name="osac-ci-other-zero", id=200),
        reservation("10.20.1.17", name="osac-ci-other-one", id=201),
        reservation("10.20.1.18", name="osac-ci-other-two", id=202),
        reservation("10.20.1.19", name="osac-ci-other-three", id=203),
    ])
    req = request(ipam=make_ipam(["10.20.1.16/30", "10.20.1.32/28"]))

    result = ingress.reconcile(req, client, SITE_ID, TENANT_ID, delay_seconds=0)

    assert result == {"changed": True, "address": "10.20.1.32"}
    assert client.created == [(RESERVATION_NAME, "10.20.1.32/32", SITE_ID, TENANT_ID, VPC_ID)]


def test_select_address_keeps_vip_range_edges_but_skips_parent_subnet_broadcast():
    req = request(ipam=make_ipam(["10.20.1.16/28"]))
    occupied = [
        reservation(f"10.20.1.{address}", name=f"osac-ci-other-{address}", id=200 + address)
        for address in range(16, 31)
    ]

    assert ingress._select_address(req, []) == "10.20.1.16"
    assert ingress._select_address(req, occupied) == "10.20.1.31"


def test_allocate_skips_parent_subnet_network_address():
    req = request(ipam=make_ipam(["10.20.1.0/28"]))
    client = FakeNetrisClient()

    result = ingress.reconcile(req, client, SITE_ID, TENANT_ID, delay_seconds=0)

    assert result == {"changed": True, "address": "10.20.1.2"}
    assert client.created == [
        (RESERVATION_NAME, "10.20.1.2/32", SITE_ID, TENANT_ID, VPC_ID)
    ]


def test_allocate_skips_subnet_gateway_address():
    req = request(
        subnet=make_subnet(spec={"ipv4Cidr": "10.20.1.0/28"}),
        ipam=make_ipam(["10.20.1.0/28"]),
    )
    client = FakeNetrisClient()

    result = ingress.reconcile(req, client, SITE_ID, TENANT_ID, delay_seconds=0)

    assert result == {"changed": True, "address": "10.20.1.2"}
    assert client.created == [
        (RESERVATION_NAME, "10.20.1.2/32", SITE_ID, TENANT_ID, VPC_ID)
    ]


def test_allocate_skips_parent_subnet_broadcast():
    req = request(
        subnet=make_subnet(spec={"ipv4Cidr": "10.20.1.0/28"}),
        ipam=make_ipam(["10.20.1.14/31"]),
    )
    client = FakeNetrisClient()

    result = ingress.reconcile(req, client, SITE_ID, TENANT_ID, delay_seconds=0)

    assert result == {"changed": True, "address": "10.20.1.14"}
    assert client.created == [(RESERVATION_NAME, "10.20.1.14/32", SITE_ID, TENANT_ID, VPC_ID)]


def test_allocate_replaces_an_existing_parent_broadcast_reservation():
    req = request(
        subnet=make_subnet(spec={"ipv4Cidr": "10.20.1.0/28"}),
        ipam=make_ipam(["10.20.1.14/31"]),
    )
    client = FakeNetrisClient(records=[reservation("10.20.1.15")])

    result = ingress.reconcile(req, client, SITE_ID, TENANT_ID, delay_seconds=0)

    assert result == {"changed": True, "address": "10.20.1.14"}
    assert client.deleted == [101]
    assert client.created == [(RESERVATION_NAME, "10.20.1.14/32", SITE_ID, TENANT_ID, VPC_ID)]


def test_allocate_replaces_an_existing_parent_network_reservation():
    req = request(ipam=make_ipam(["10.20.1.0/28"]))
    client = FakeNetrisClient(records=[reservation("10.20.1.0")])

    result = ingress.reconcile(req, client, SITE_ID, TENANT_ID, delay_seconds=0)

    assert result == {"changed": True, "address": "10.20.1.2"}
    assert client.deleted == [101]
    assert client.created == [
        (RESERVATION_NAME, "10.20.1.2/32", SITE_ID, TENANT_ID, VPC_ID)
    ]


def test_allocate_replaces_an_existing_subnet_gateway_reservation():
    req = request(ipam=make_ipam(["10.20.1.0/28"]))
    client = FakeNetrisClient(records=[reservation("10.20.1.1")])

    result = ingress.reconcile(req, client, SITE_ID, TENANT_ID, delay_seconds=0)

    assert result == {"changed": True, "address": "10.20.1.2"}
    assert client.deleted == [101]
    assert client.created == [
        (RESERVATION_NAME, "10.20.1.2/32", SITE_ID, TENANT_ID, VPC_ID)
    ]


def test_allocate_fails_without_writing_when_only_candidate_is_parent_broadcast():
    req = request(
        subnet=make_subnet(spec={"ipv4Cidr": "10.20.1.0/28"}),
        ipam=make_ipam(["10.20.1.15/32"]),
    )
    client = FakeNetrisClient()

    with pytest.raises(netris_external_ip.NetrisExternalIPError) as error:
        ingress.reconcile(req, client, SITE_ID, TENANT_ID, delay_seconds=0)

    assert error.value.category == "exhausted"
    assert client.created == []


def test_allocate_requires_the_vpc_to_be_available():
    with pytest.raises(ValueError, match="vpc_id is required"):
        request("cluster_ingress.allocate", vpc_id=None)


def test_allocate_skips_ipv6_subnets():
    client = FakeNetrisClient(records=[{
        "id": 200, "name": "ipv6-subnet", "prefix": "2001:db8::/64",
        "purpose": "vnet", "site_id": SITE_ID, "tenant_id": TENANT_ID,
    }])

    result = ingress.reconcile(request(), client, SITE_ID, TENANT_ID, delay_seconds=0)

    assert result == {"changed": True, "address": "10.20.1.16"}
    assert client.created == [(RESERVATION_NAME, "10.20.1.16/32", SITE_ID, TENANT_ID, VPC_ID)]


def test_allocate_treats_a_record_without_purpose_as_occupied_by_prefix():
    client = FakeNetrisClient(records=[{
        "id": 200, "name": "incomplete-subnet", "prefix": "10.20.1.16/32",
        "vpc_id": VPC_ID,
    }])

    result = ingress.reconcile(request(), client, SITE_ID, TENANT_ID, delay_seconds=0)

    assert result == {"changed": True, "address": "10.20.1.17"}
    assert client.created == [(RESERVATION_NAME, "10.20.1.17/32", SITE_ID, TENANT_ID, VPC_ID)]


def test_allocate_fails_closed_for_malformed_reservation_prefix():
    client = FakeNetrisClient(records=[
        reservation("10.20.1.17", name="osac-ci-other", prefix="not-a-cidr")
    ])

    with pytest.raises(netris_external_ip.NetrisExternalIPError) as error:
        ingress.reconcile(request(), client, SITE_ID, TENANT_ID, delay_seconds=0)

    assert error.value.category == "malformed_response"
    assert client.created == []


def test_allocate_reuses_an_existing_owner_without_writing():
    client = FakeNetrisClient(records=[reservation("10.20.1.20")])

    result = ingress.reconcile(request(), client, SITE_ID, TENANT_ID, delay_seconds=0)

    assert result == {"changed": False, "address": "10.20.1.20"}
    assert client.created == []


@pytest.mark.parametrize(
    "record",
    [
        reservation("10.20.1.20", site_id=9),
        reservation("10.20.1.20", tenant_id=9),
        reservation("10.20.1.20", vpc_id=None),
        reservation("10.20.1.20", vpc_id=VPC_ID + 1),
        reservation("10.20.1.20", purpose="vnet"),
        reservation("10.20.2.20"),
        reservation("10.20.1.20", prefix="10.20.1.20/31"),
    ],
)
def test_foreign_or_malformed_owner_is_never_used_or_deleted(record):
    client = FakeNetrisClient(records=[record])

    with pytest.raises(netris_external_ip.NetrisExternalIPError) as error:
        ingress.reconcile(request(), client, SITE_ID, TENANT_ID, delay_seconds=0)

    assert error.value.category == "foreign_object"
    assert client.created == []
    assert client.deleted == []


def test_release_deletes_only_the_cluster_order_reservation():
    other = {**reservation("10.20.1.18"), "id": 300, "name": "osac-ci-other"}
    client = FakeNetrisClient(records=[reservation(), other])

    result = ingress.reconcile(
        request("cluster_ingress.release"), client, SITE_ID, TENANT_ID, delay_seconds=0
    )

    assert result == {"changed": True, "address": ""}
    assert client.deleted == [101]
    assert client.records == [other]


def test_release_deletes_its_reservation_without_a_vpc_id():
    client = FakeNetrisClient(records=[reservation()])

    result = ingress.reconcile(
        request("cluster_ingress.release", vpc_id=None),
        client,
        SITE_ID,
        TENANT_ID,
        delay_seconds=0,
    )

    assert result == {"changed": True, "address": ""}
    assert client.deleted == [101]
    assert client.records == []


def test_release_without_a_vpc_id_still_fails_closed_on_ambiguous_names():
    duplicate = reservation("10.20.1.18", id=102, vpc_id=VPC_ID + 1)
    client = FakeNetrisClient(records=[reservation(), duplicate])

    with pytest.raises(netris_external_ip.NetrisExternalIPError) as error:
        ingress.reconcile(
            request("cluster_ingress.release", vpc_id=None),
            client,
            SITE_ID,
            TENANT_ID,
            delay_seconds=0,
        )

    assert error.value.category == "foreign_object"
    assert client.deleted == []


@pytest.mark.parametrize(
    "record",
    [
        reservation(site_id=SITE_ID + 1),
        reservation(tenant_id=TENANT_ID + 1),
        reservation(purpose="vnet"),
        reservation(prefix="10.20.2.20/32"),
    ],
)
def test_release_without_a_vpc_id_still_checks_reservation_ownership(record):
    client = FakeNetrisClient(records=[record])

    with pytest.raises(netris_external_ip.NetrisExternalIPError) as error:
        ingress.reconcile(
            request("cluster_ingress.release", vpc_id=None),
            client,
            SITE_ID,
            TENANT_ID,
            delay_seconds=0,
        )

    assert error.value.category == "foreign_object"
    assert client.deleted == []


def test_release_of_an_absent_reservation_is_idempotent():
    client = FakeNetrisClient()

    result = ingress.reconcile(
        request("cluster_ingress.release"), client, SITE_ID, TENANT_ID, delay_seconds=0
    )

    assert result == {"changed": False, "address": ""}
    assert client.deleted == []


def test_allocate_fails_finally_without_a_recorded_lb_vip_range():
    with pytest.raises(netris_external_ip.NetrisExternalIPError) as error:
        request(ipam=make_ipam([]))

    assert error.value.category == "exhausted"
