"""Fixtures and selection rules shared by the Netris VMaaS networking tests."""

from __future__ import annotations

from collections.abc import Iterator
from pathlib import Path
from typing import Any

import pytest

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.metering import MeteringCollector
from tests.e2e.core.wait_kinds import WaitResourceKind
from tests.e2e.networking.teardown import wait_owned_gone
from tests.e2e.networking.vmaas import stack
from tests.e2e.vmaas.conftest import default_disk_image, default_instance_type, test_run_id  # noqa: F401

# They send no traffic and need no ordered state, but they run after the main suite: a failure here must
# not hide it under -x. The job runs them as a second pytest run, so a main suite failure cannot hide them.
_AFTER_MAIN_SUITE = tuple(
    Path(__file__).parent / name
    for name in ("test_external_ip_billing.py", "test_external_ip_reattach_billing.py", "test_nat_gateway_billing.py")
)


def pytest_collection_modifyitems(config: pytest.Config, items: list[pytest.Item]) -> None:
    """Run the main suite before the billing tests. Items outside this directory keep their positions."""
    suite_dir = Path(__file__).parent
    positions = [i for i, item in enumerate(items) if item.path.is_relative_to(suite_dir)]
    ordered = sorted((items[i] for i in positions), key=lambda item: item.path in _AFTER_MAIN_SUITE)
    for position, item in zip(positions, ordered, strict=True):
        items[position] = item


@pytest.fixture
def billing_metering(request: pytest.FixtureRequest) -> MeteringCollector:
    """The metering collector, or a skip when this profile has no echo adapter.

    The tests carry no ``metering`` marker on purpose: that marker makes the whole session fail at
    collection when no adapter is reachable (tests/e2e/conftest.py), which would take every other test of
    the run down for a profile that only lacks this one dependency.
    """
    try:
        request.getfixturevalue("metering_adapter_url")
    except (RuntimeError, OSError) as exc:
        pytest.skip(f"no metering echo adapter: set METERING_ADAPTER_URL or enable metering.echoAdapter ({exc})")
    return request.getfixturevalue("metering")


@pytest.fixture(scope="module")
def billing_pool(
    private_grpc: GRPCClient, k8s_hub_client: K8sClient, external_ip_pool_cidr: str, net_test_run_id: str
) -> Iterator[dict[str, Any]]:
    """The routed ExternalIPPool of the main suite: adopted when it exists, otherwise created and removed here."""
    pool: dict[str, Any] = {}
    try:
        stack.resolve_external_ip_pool(
            private_grpc=private_grpc,
            k8s_hub_client=k8s_hub_client,
            name=f"netris-billing-pool-{net_test_run_id}",
            cidr=external_ip_pool_cidr,
            record=pool,
        )
        yield pool
    finally:
        if pool.get("owned"):
            pool_id = pool["id"]
            wait_owned_gone(
                description=f"ExternalIPPool {pool_id} gone",
                api_gone=lambda: pool_id not in private_grpc.list_external_ip_pool_ids(),
                cr_gone=lambda: k8s_hub_client.get_external_ip_pool_name(uuid=pool_id, checked=False) == "",
                delete=lambda: private_grpc.delete_external_ip_pool(pool_id=pool_id),
                resource_kind=WaitResourceKind.EXTERNAL_IP_POOL,
                resource_id=pool_id,
            )
