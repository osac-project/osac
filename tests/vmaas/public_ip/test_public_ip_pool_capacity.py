from __future__ import annotations

import subprocess
from uuid import uuid4

import pytest

from tests.core.grpc_client import GRPCClient
from tests.core.helpers import wait_for_public_ip_pool_deletion
from tests.core.k8s_client import K8sClient
from tests.core.runner import poll_until
from tests.vmaas.public_ip.helpers import create_ip, delete_ip, pool_status


class TestPoolCapacity:
    """
    Tests for PublicIPPool capacity management.

    Note: Tests are order-dependent - each builds on prevous state.  This is a
    purposeful tradeoff to greatly reduce test execution time.
    """

    def test_capacity_initialized_from_cidr(
        self,
        small_pool: tuple[str, str],
        private_grpc: GRPCClient,
    ) -> None:
        pool_id, _ = small_pool
        status = pool_status(private_grpc, pool_id)
        assert status["total"] == 2, f"Expected small pool to have 2 usable IPs, got {status['total']}"
        assert status["available"] == 2
        assert status["allocated"] == 0

    def test_allocation_decrements_available(
        self,
        small_pool: tuple[str, str],
        grpc: GRPCClient,
        private_grpc: GRPCClient,
        k8s_hub_client: K8sClient,
        created_ips: list[tuple[str, str]],
    ) -> None:
        pool_id, _ = small_pool
        created_ips.append(create_ip(grpc, k8s_hub_client, pool_id))
        status = pool_status(private_grpc, pool_id)
        assert status["total"] == 2
        assert status["allocated"] == 1
        assert status["available"] == 1

    def test_exhaustion_rejects_creation(
        self,
        small_pool: tuple[str, str],
        grpc: GRPCClient,
        private_grpc: GRPCClient,
        k8s_hub_client: K8sClient,
        created_ips: list[tuple[str, str]],
    ) -> None:
        pool_id, _ = small_pool
        created_ips.append(create_ip(grpc, k8s_hub_client, pool_id))

        status = pool_status(private_grpc, pool_id)
        assert status["available"] == 0, f"Pool should be full, available={status['available']}"

        try:
            ip_id = grpc.create_public_ip(name=f"test-ip-{uuid4().hex[:8]}", pool=pool_id)
            created_ips.append((ip_id, ""))
            pytest.fail("create_public_ip should have been rejected on a full pool")
        except subprocess.CalledProcessError as exc:
            combined = (exc.stderr or "") + (exc.stdout or "")
            assert "FailedPrecondition" in combined

    def test_release_restores_capacity(
        self,
        small_pool: tuple[str, str],
        grpc: GRPCClient,
        private_grpc: GRPCClient,
        k8s_hub_client: K8sClient,
        created_ips: list[tuple[str, str]],
    ) -> None:
        pool_id, _ = small_pool
        ip_id, _ = created_ips.pop(0)
        delete_ip(grpc, ip_id)

        poll_until(
            fn=lambda: pool_status(private_grpc, pool_id)["available"],
            until=lambda v: v == 1,
            retries=30,
            delay=2,
            description="Pool available restored to 1 after IP release",
        )

        created_ips.append(create_ip(grpc, k8s_hub_client, pool_id))
        status = pool_status(private_grpc, pool_id)
        assert status["allocated"] == 2
        assert status["available"] == 0

    def test_pool_deletion_blocked_while_ips_allocated(
        self,
        small_pool: tuple[str, str],
        private_grpc: GRPCClient,
    ) -> None:
        pool_id, _ = small_pool
        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            private_grpc.delete_public_ip_pool(pool_id=pool_id)
        combined = (exc_info.value.stderr or "") + (exc_info.value.stdout or "")
        assert "FailedPrecondition" in combined

    def test_pool_deletion_succeeds_after_all_ips_released(
        self,
        small_pool: tuple[str, str],
        grpc: GRPCClient,
        private_grpc: GRPCClient,
        k8s_hub_client: K8sClient,
        created_ips: list[tuple[str, str]],
    ) -> None:
        pool_id, pool_cr_name = small_pool
        for ip_id, _ in created_ips:
            delete_ip(grpc, ip_id)
        created_ips.clear()

        poll_until(
            fn=lambda: pool_status(private_grpc, pool_id)["allocated"],
            until=lambda v: v == 0,
            retries=30,
            delay=2,
            description="Pool allocated drops to 0",
        )

        private_grpc.delete_public_ip_pool(pool_id=pool_id)
        wait_for_public_ip_pool_deletion(k8s=k8s_hub_client, name=pool_cr_name)
