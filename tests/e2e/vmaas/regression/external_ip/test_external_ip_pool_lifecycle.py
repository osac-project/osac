from __future__ import annotations

import subprocess
from collections.abc import Callable
from uuid import uuid4

import pytest

from tests.e2e.core.grpc_client import PUBLIC_API, GRPCClient
from tests.e2e.core.helpers import (
    assert_grpc_method_unavailable,
    assert_grpc_rejected,
    wait_for_external_ip_allocated,
    wait_for_external_ip_attachment_cr,
    wait_for_external_ip_attachment_deletion,
    wait_for_external_ip_attachment_ready,
    wait_for_external_ip_deletion,
    wait_for_external_ip_pool_deletion,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.metering import MeteringCollector
from tests.e2e.core.runner import poll_until

pytestmark = pytest.mark.regression


class TestExternalIPPoolLifecycle:
    @pytest.mark.metering
    def test_attach_detach_reattach(
        self,
        external_ip_pool: tuple[str, str],
        external_ip: tuple[str, str],
        make_compute_instances: Callable[..., tuple[tuple[str, str], ...]],
        grpc: GRPCClient,
        private_grpc: GRPCClient,
        k8s_hub_client: K8sClient,
        metering: MeteringCollector,
    ) -> None:
        pool_id, pool_cr_name = external_ip_pool
        ip_id, ip_cr_name = external_ip
        (ci1_uuid, _ci1_name), (ci2_uuid, _ci2_name) = make_compute_instances(2)

        assert pool_id in private_grpc.list_external_ip_pool_ids()
        assert ip_id in grpc.list_external_ip_ids()
        wait_for_external_ip_allocated(k8s=k8s_hub_client, name=ip_cr_name)
        metering.expect("osac.resource.started.v1", resource_id=ip_id)
        metering.verify()
        started = metering.get_event("osac.resource.started.v1", resource_id=ip_id)
        assert started["data"]["billing_dimensions"]["pool"] == pool_id
        assert started["data"]["billing_dimensions"]["ip_family"] == "ipv4"
        assert started["data"]["billing_dimensions"]["attached"] is False

        # --- Attach ExternalIP to ComputeInstance 1 ---
        att_id: str = grpc.create_external_ip_attachment(
            name=f"test-att-{uuid4().hex[:8]}", external_ip=ip_id, compute_instance=ci1_uuid
        )
        att_cr_name: str = wait_for_external_ip_attachment_cr(k8s=k8s_hub_client, uuid=att_id)
        wait_for_external_ip_attachment_ready(k8s=k8s_hub_client, name=att_cr_name)

        private_ip_obj = poll_until(
            fn=lambda: private_grpc.get_private_external_ip(external_ip_id=ip_id)["object"],
            until=lambda item: (
                item["status"].get("attribution", {}).get("computeInstance", {}).get("id") == ci1_uuid
                and bool(item["status"].get("attachmentTransitionTime"))
            ),
            retries=30,
            delay=5,
            description="ExternalIP attribution settlement",
        )
        ip_obj = grpc.get_external_ip(external_ip_id=ip_id)
        assert ip_obj["object"]["status"].get("attached") is True
        attached_ip_address: str = ip_obj["object"]["status"]["address"]
        assert attached_ip_address, "ExternalIP should have an allocated address"
        assert private_ip_obj["status"]["attribution"]["computeInstance"]["id"] == ci1_uuid
        assert private_ip_obj["status"].get("attachmentTransitionTime")
        metering.expect("osac.resource.updated.v1", resource_id=ip_id)
        metering.verify()
        # The update closes the old unattached slice; the settled attached
        # dimensions apply to the next heartbeat and slice.
        attached_event = metering.get_event("osac.resource.updated.v1", resource_id=ip_id)
        assert attached_event["data"]["billing_dimensions"]["attached"] is False

        # --- Detach (delete attachment) ---
        grpc.delete_external_ip_attachment(attachment_id=att_id)
        wait_for_external_ip_attachment_deletion(k8s=k8s_hub_client, name=att_cr_name)

        poll_until(
            fn=lambda: grpc.get_external_ip(external_ip_id=ip_id)["object"]["status"].get("attached"),
            until=lambda v: v is not True,
            retries=30,
            delay=5,
            description=f"ExternalIP {ip_id} detached",
        )
        metering.expect("osac.resource.updated.v1", resource_id=ip_id)
        metering.verify()
        detached_event = metering.get_event("osac.resource.updated.v1", resource_id=ip_id)
        detached_dimensions = detached_event["data"]["billing_dimensions"]
        assert detached_dimensions["attached"] is True
        assert detached_dimensions["attribution_id"] == ci1_uuid

        # --- Re-attach same IP to ComputeInstance 2 ---
        att2_id: str = grpc.create_external_ip_attachment(
            name=f"test-att-{uuid4().hex[:8]}", external_ip=ip_id, compute_instance=ci2_uuid
        )
        att2_cr_name: str = wait_for_external_ip_attachment_cr(k8s=k8s_hub_client, uuid=att2_id)
        wait_for_external_ip_attachment_ready(k8s=k8s_hub_client, name=att2_cr_name)
        second_private_ip = poll_until(
            fn=lambda: private_grpc.get_private_external_ip(external_ip_id=ip_id)["object"],
            until=lambda item: (
                item["status"].get("attribution", {}).get("computeInstance", {}).get("id") == ci2_uuid
                and bool(item["status"].get("attachmentTransitionTime"))
            ),
            retries=30,
            delay=5,
            description="reattached ExternalIP attribution settlement",
        )
        assert second_private_ip["status"]["attribution"]["computeInstance"]["id"] == ci2_uuid
        metering.expect("osac.resource.updated.v1", resource_id=ip_id)
        metering.verify()
        reattached_event = metering.get_event("osac.resource.updated.v1", resource_id=ip_id)
        assert reattached_event["data"]["billing_dimensions"]["attached"] is False

        ip_obj = grpc.get_external_ip(external_ip_id=ip_id)
        assert ip_obj["object"]["status"]["address"] == attached_ip_address, (
            "Re-attached ExternalIP should keep the same address"
        )

        # --- Cleanup: delete attachment, ExternalIP, then pool ---
        grpc.delete_external_ip_attachment(attachment_id=att2_id)
        wait_for_external_ip_attachment_deletion(k8s=k8s_hub_client, name=att2_cr_name)

        metering.expect("osac.resource.suspended.v1", resource_id=ip_id)
        grpc.delete_external_ip(external_ip_id=ip_id)
        metering.verify()
        wait_for_external_ip_deletion(k8s=k8s_hub_client, name=ip_cr_name)
        poll_until(
            fn=lambda: ip_id not in grpc.list_external_ip_ids(),
            until=lambda v: v is True,
            retries=30,
            delay=5,
            description=f"ExternalIP {ip_id} removal from API",
        )

        private_grpc.delete_external_ip_pool(pool_id=pool_id)
        wait_for_external_ip_pool_deletion(k8s=k8s_hub_client, name=pool_cr_name)
        poll_until(
            fn=lambda: pool_id not in private_grpc.list_external_ip_pool_ids(),
            until=lambda v: v is True,
            retries=30,
            delay=5,
            description=f"ExternalIPPool {pool_id} removal from API",
        )

    def test_validation_rejections(
        self,
        external_ip_pool: tuple[str, str],
        external_ip: tuple[str, str],
        make_compute_instances: Callable[..., tuple[tuple[str, str], ...]],
        grpc: GRPCClient,
        private_grpc: GRPCClient,
        k8s_hub_client: K8sClient,
    ) -> None:
        _pool_id, _pool_cr_name = external_ip_pool
        ip_id, ip_cr_name = external_ip
        ci1_uuid, _ = make_compute_instances(1)[0]

        wait_for_external_ip_allocated(k8s=k8s_hub_client, name=ip_cr_name)

        # Nonexistent ComputeInstance
        fake_ci_uuid: str = str(uuid4())
        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            grpc.create_external_ip_attachment(
                name=f"test-att-{uuid4().hex[:8]}", external_ip=ip_id, compute_instance=fake_ci_uuid
            )
        assert_grpc_rejected(exc_info, "InvalidArgument")

        # Nonexistent ExternalIP
        fake_ip_uuid: str = str(uuid4())
        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            grpc.create_external_ip_attachment(
                name=f"test-att-{uuid4().hex[:8]}", external_ip=fake_ip_uuid, compute_instance=ci1_uuid
            )
        assert_grpc_rejected(exc_info, "InvalidArgument")

        # Duplicate attachment — attach same ExternalIP twice
        att_id: str = grpc.create_external_ip_attachment(
            name=f"test-att-{uuid4().hex[:8]}", external_ip=ip_id, compute_instance=ci1_uuid
        )
        att_cr_name: str = wait_for_external_ip_attachment_cr(k8s=k8s_hub_client, uuid=att_id)
        wait_for_external_ip_attachment_ready(k8s=k8s_hub_client, name=att_cr_name)
        poll_until(
            fn=lambda: private_grpc.get_private_external_ip(external_ip_id=ip_id)["object"],
            until=lambda item: (
                bool(item["status"].get("attribution")) and bool(item["status"].get("attachmentTransitionTime"))
            ),
            retries=30,
            delay=5,
            description="duplicate-attachment ExternalIP settlement",
        )

        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            grpc.create_external_ip_attachment(
                name=f"test-att-{uuid4().hex[:8]}", external_ip=ip_id, compute_instance=ci1_uuid
            )
        assert_grpc_rejected(exc_info, "AlreadyExists")

        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            grpc.call(
                service=f"{PUBLIC_API}.ExternalIPs/Update",
                data={"object": {"id": ip_id, "status": {"attached": False}}},
            )
        assert_grpc_method_unavailable(exc_info, service=f"{PUBLIC_API}.ExternalIPs", method="Update")

        grpc.delete_external_ip_attachment(attachment_id=att_id)
        wait_for_external_ip_attachment_deletion(k8s=k8s_hub_client, name=att_cr_name)
