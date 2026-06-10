from __future__ import annotations

import subprocess
from collections.abc import Callable
from uuid import uuid4

import pytest

from tests.core.grpc_client import GRPCClient
from tests.core.helpers import (
    assert_grpc_rejected,
    wait_for_public_ip_allocated,
    wait_for_public_ip_attachment_cr,
    wait_for_public_ip_attachment_deletion,
    wait_for_public_ip_attachment_ready,
    wait_for_public_ip_deletion,
    wait_for_public_ip_pool_deletion,
)
from tests.core.k8s_client import K8sClient
from tests.core.runner import poll_until


class TestPublicIPPoolLifecycle:
    def test_attach_detach_reattach(
        self,
        public_ip_pool: tuple[str, str],
        public_ip: tuple[str, str],
        make_compute_instances: Callable[..., tuple[tuple[str, str], ...]],
        grpc: GRPCClient,
        private_grpc: GRPCClient,
        k8s_hub_client: K8sClient,
    ) -> None:
        pool_id, pool_cr_name = public_ip_pool
        ip_id, ip_cr_name = public_ip
        (ci1_uuid, _ci1_name), (ci2_uuid, _ci2_name) = make_compute_instances(2)

        assert pool_id in private_grpc.list_public_ip_pool_ids()
        assert ip_id in grpc.list_public_ip_ids()
        wait_for_public_ip_allocated(k8s=k8s_hub_client, name=ip_cr_name)

        # --- Attach PublicIP to ComputeInstance 1 ---
        att_id: str = grpc.create_public_ip_attachment(
            name=f"test-att-{uuid4().hex[:8]}",
            public_ip=ip_id,
            compute_instance=ci1_uuid,
        )
        att_cr_name: str = wait_for_public_ip_attachment_cr(k8s=k8s_hub_client, uuid=att_id)
        wait_for_public_ip_attachment_ready(k8s=k8s_hub_client, name=att_cr_name)

        ip_obj = grpc.get_public_ip(public_ip_id=ip_id)
        assert ip_obj["object"]["status"].get("attached") is True
        attached_ip_address: str = ip_obj["object"]["status"]["address"]
        assert attached_ip_address, "PublicIP should have an allocated address"

        # --- Detach (delete attachment) ---
        grpc.delete_public_ip_attachment(attachment_id=att_id)
        wait_for_public_ip_attachment_deletion(k8s=k8s_hub_client, name=att_cr_name)

        poll_until(
            fn=lambda: grpc.get_public_ip(public_ip_id=ip_id)["object"]["status"].get("attached"),
            until=lambda v: v is not True,
            retries=30,
            delay=5,
            description=f"PublicIP {ip_id} detached",
        )

        # --- Re-attach same IP to ComputeInstance 2 ---
        att2_id: str = grpc.create_public_ip_attachment(
            name=f"test-att-{uuid4().hex[:8]}",
            public_ip=ip_id,
            compute_instance=ci2_uuid,
        )
        att2_cr_name: str = wait_for_public_ip_attachment_cr(k8s=k8s_hub_client, uuid=att2_id)
        wait_for_public_ip_attachment_ready(k8s=k8s_hub_client, name=att2_cr_name)

        ip_obj = grpc.get_public_ip(public_ip_id=ip_id)
        assert ip_obj["object"]["status"]["address"] == attached_ip_address, (
            "Re-attached PublicIP should keep the same address"
        )

        # --- Cleanup: delete attachment, PublicIP, then pool ---
        grpc.delete_public_ip_attachment(attachment_id=att2_id)
        wait_for_public_ip_attachment_deletion(k8s=k8s_hub_client, name=att2_cr_name)

        grpc.delete_public_ip(public_ip_id=ip_id)
        wait_for_public_ip_deletion(k8s=k8s_hub_client, name=ip_cr_name)
        poll_until(
            fn=lambda: ip_id not in grpc.list_public_ip_ids(),
            until=lambda v: v is True,
            retries=30,
            delay=5,
            description=f"PublicIP {ip_id} removal from API",
        )

        private_grpc.delete_public_ip_pool(pool_id=pool_id)
        wait_for_public_ip_pool_deletion(k8s=k8s_hub_client, name=pool_cr_name)
        poll_until(
            fn=lambda: pool_id not in private_grpc.list_public_ip_pool_ids(),
            until=lambda v: v is True,
            retries=30,
            delay=5,
            description=f"PublicIPPool {pool_id} removal from API",
        )

    def test_validation_rejections(
        self,
        public_ip_pool: tuple[str, str],
        public_ip: tuple[str, str],
        make_compute_instances: Callable[..., tuple[tuple[str, str], ...]],
        grpc: GRPCClient,
        k8s_hub_client: K8sClient,
    ) -> None:
        _pool_id, _pool_cr_name = public_ip_pool
        ip_id, ip_cr_name = public_ip
        ci1_uuid, _ = make_compute_instances(1)[0]

        wait_for_public_ip_allocated(k8s=k8s_hub_client, name=ip_cr_name)

        # Nonexistent ComputeInstance
        fake_ci_uuid: str = str(uuid4())
        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            grpc.create_public_ip_attachment(
                name=f"test-att-{uuid4().hex[:8]}",
                public_ip=ip_id,
                compute_instance=fake_ci_uuid,
            )
        assert_grpc_rejected(exc_info, "InvalidArgument")

        # Nonexistent PublicIP
        fake_ip_uuid: str = str(uuid4())
        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            grpc.create_public_ip_attachment(
                name=f"test-att-{uuid4().hex[:8]}",
                public_ip=fake_ip_uuid,
                compute_instance=ci1_uuid,
            )
        assert_grpc_rejected(exc_info, "InvalidArgument")

        # Duplicate attachment — attach same PublicIP twice
        att_id: str = grpc.create_public_ip_attachment(
            name=f"test-att-{uuid4().hex[:8]}",
            public_ip=ip_id,
            compute_instance=ci1_uuid,
        )
        att_cr_name: str = wait_for_public_ip_attachment_cr(k8s=k8s_hub_client, uuid=att_id)
        wait_for_public_ip_attachment_ready(k8s=k8s_hub_client, name=att_cr_name)

        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            grpc.create_public_ip_attachment(
                name=f"test-att-{uuid4().hex[:8]}",
                public_ip=ip_id,
                compute_instance=ci1_uuid,
            )
        assert_grpc_rejected(exc_info, "AlreadyExists")

        grpc.delete_public_ip_attachment(attachment_id=att_id)
        wait_for_public_ip_attachment_deletion(k8s=k8s_hub_client, name=att_cr_name)
