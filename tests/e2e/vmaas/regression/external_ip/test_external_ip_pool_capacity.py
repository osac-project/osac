from __future__ import annotations

import pytest

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.vmaas.regression.external_ip.helpers import pool_status

pytestmark = pytest.mark.regression


class TestPoolCapacity:
    """ExternalIPPool capacity that the Fulfillment Service derives from the pool CIDRs.

    Allocation, exhaustion and release need ExternalIPs, which only provider networking
    offers; the Public external IPs server specs in fulfillment-service/internal/servers
    cover them.
    """

    def test_capacity_initialized_from_cidr(self, small_pool: tuple[str, str], private_grpc: GRPCClient) -> None:
        pool_id, _ = small_pool
        status = pool_status(private_grpc, pool_id)
        assert status["total"] == 2, f"Expected small pool to have 2 usable IPs, got {status['total']}"
        assert status["available"] == 2
        assert status["allocated"] == 0
