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
    ) -> None:
        pool_id, _ = small_pool
        ip_id, ip_cr_name = create_ip(grpc, k8s_hub_client, pool_id)
        try:
            status = pool_status(private_grpc, pool_id)
            assert status["total"] == 2
            assert status["allocated"] == 1
            assert status["available"] == 1
        finally:
            delete_ip(grpc, k8s_hub_client, ip_id, ip_cr_name)

    def test_exhaustion_rejects_creation(
        self,
        small_pool: tuple[str, str],
        grpc: GRPCClient,
        private_grpc: GRPCClient,
        k8s_hub_client: K8sClient,
    ) -> None:
        pool_id, _ = small_pool
        created: list[tuple[str, str]] = []
        try:
            for _ in range(2):
                created.append(create_ip(grpc, k8s_hub_client, pool_id))

            status = pool_status(private_grpc, pool_id)
            assert status["available"] == 0, f"Pool should be full, available={status['available']}"

            with pytest.raises(subprocess.CalledProcessError) as exc_info:
                grpc.create_public_ip(name=f"test-ip-{uuid4().hex[:8]}", pool=pool_id)
            assert "FailedPrecondition" in exc_info.value.stderr
        finally:
            for ip_id, ip_cr_name in reversed(created):
                delete_ip(grpc, k8s_hub_client, ip_id, ip_cr_name)

    def test_release_restores_capacity(
        self,
        small_pool: tuple[str, str],
        grpc: GRPCClient,
        private_grpc: GRPCClient,
        k8s_hub_client: K8sClient,
    ) -> None:
        pool_id, _ = small_pool
        ip1_id, ip1_cr = create_ip(grpc, k8s_hub_client, pool_id)
        ip2_id, ip2_cr = create_ip(grpc, k8s_hub_client, pool_id)
        ip3_id, ip3_cr = None, None
        try:
            assert pool_status(private_grpc, pool_id)["available"] == 0

            delete_ip(grpc, k8s_hub_client, ip1_id, ip1_cr)
            ip1_id, ip1_cr = None, None

            poll_until(
                fn=lambda: pool_status(private_grpc, pool_id)["available"],
                until=lambda v: v == 1,
                retries=30,
                delay=5,
                description="Pool available restored to 1 after IP release",
            )

            ip3_id, ip3_cr = create_ip(grpc, k8s_hub_client, pool_id)
            status = pool_status(private_grpc, pool_id)
            assert status["allocated"] == 2
            assert status["available"] == 0
        finally:
            for ip_id, ip_cr in [(ip3_id, ip3_cr), (ip2_id, ip2_cr), (ip1_id, ip1_cr)]:
                if ip_id is not None:
                    delete_ip(grpc, k8s_hub_client, ip_id, ip_cr)

    def test_pool_deletion_blocked_while_ips_allocated(
        self,
        small_pool: tuple[str, str],
        grpc: GRPCClient,
        private_grpc: GRPCClient,
        k8s_hub_client: K8sClient,
    ) -> None:
        pool_id, _ = small_pool
        ip_id, ip_cr_name = create_ip(grpc, k8s_hub_client, pool_id)
        try:
            with pytest.raises(subprocess.CalledProcessError) as exc_info:
                private_grpc.delete_public_ip_pool(pool_id=pool_id)
            assert "FailedPrecondition" in exc_info.value.stderr
        finally:
            delete_ip(grpc, k8s_hub_client, ip_id, ip_cr_name)

    def test_pool_deletion_succeeds_after_all_ips_released(
        self,
        small_pool: tuple[str, str],
        grpc: GRPCClient,
        private_grpc: GRPCClient,
        k8s_hub_client: K8sClient,
    ) -> None:
        pool_id, pool_cr_name = small_pool
        ip_id, ip_cr_name = create_ip(grpc, k8s_hub_client, pool_id)
        delete_ip(grpc, k8s_hub_client, ip_id, ip_cr_name)

        poll_until(
            fn=lambda: pool_status(private_grpc, pool_id)["allocated"],
            until=lambda v: v == 0,
            retries=30,
            delay=5,
            description="Pool allocated drops to 0",
        )

        private_grpc.delete_public_ip_pool(pool_id=pool_id)
        wait_for_public_ip_pool_deletion(k8s=k8s_hub_client, name=pool_cr_name)
