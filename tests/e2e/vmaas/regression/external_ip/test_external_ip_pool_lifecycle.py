from __future__ import annotations

import subprocess
from collections.abc import Callable
from datetime import datetime
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
from tests.e2e.core.metering import HEARTBEAT_TYPE, MeteringCollector
from tests.e2e.core.runner import poll_until

pytestmark = pytest.mark.regression


def _heartbeat_with_dimensions(
    metering: MeteringCollector, resource_id: str, wanted: dict[str, str], *, after: datetime | None = None
) -> dict[str, object] | None:
    """Return the first heartbeat of the resource whose dimensions match, waiting for one.

    ``after`` bounds the search: only heartbeats whose usage interval starts at or after
    that moment match, so a check that follows an event cannot be satisfied by a
    heartbeat from before it.
    """

    def find() -> bool:
        return _heartbeat_with_dimensions_once(metering, resource_id, wanted, after=after) is not None

    poll_until(
        fn=find,
        until=lambda found: found is True,
        retries=60,
        delay=5,
        description=f"heartbeat of {resource_id} with dimensions {wanted}",
    )
    return _heartbeat_with_dimensions_once(metering, resource_id, wanted, after=after)


def _heartbeat_with_dimensions_once(
    metering: MeteringCollector, resource_id: str, wanted: dict[str, str], *, after: datetime | None = None
) -> dict[str, object] | None:
    for event in metering.get_all_events(HEARTBEAT_TYPE, resource_id):
        dimensions = event.get("data", {}).get("billing_dimensions", {})
        if not all(dimensions.get(key) == value for key, value in wanted.items()):
            continue
        if after is not None:
            usage_from = _parse_time(event["data"]["usage"]["from"])
            if usage_from < after:
                continue
        return event
    return None


def _parse_time(value: str) -> datetime:
    if value.endswith("Z"):
        value = value[:-1] + "+00:00"
    return datetime.fromisoformat(value)


def _attribution_dimension(resource_type: str, ref: str) -> str:
    """The encoded attribution dimension: kind/id-or-name/endpoint, not a bare UUID."""
    return f"{resource_type}/{ref}/API"


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
        metering.expect(HEARTBEAT_TYPE, resource_id=ip_id, timeout=180)
        metering.verify()
        allocated = metering.get_event(HEARTBEAT_TYPE, resource_id=ip_id)
        assert allocated["data"]["billing_dimensions"]["pool"] == pool_id
        assert allocated["data"]["billing_dimensions"]["ip_family"] == "ipv4"
        assert allocated["data"]["billing_dimensions"]["attached"] == "false"

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
        # The settled attachment is a dimension change, so the attached meter
        # starts billing: a heartbeat arrives with the attached dimensions. The
        # attribution is the encoded reference, not the bare UUID.
        attached_event = _heartbeat_with_dimensions(
            metering, ip_id, {"attached": "true", "attribution": _attribution_dimension("compute_instance", ci1_uuid)}
        )
        assert attached_event is not None, "no heartbeat with the attached dimensions arrived"

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
        # Detaching is another dimension change: the unattached meter bills again. The
        # window starts at the attached heartbeat's interval end, so the check cannot be
        # satisfied by the unattached heartbeats from before the attachment.
        assert attached_event is not None
        attached_to = _parse_time(str(attached_event["data"]["usage"]["to"]))
        detached_event = _heartbeat_with_dimensions(metering, ip_id, {"attached": "false"}, after=attached_to)
        assert detached_event is not None, "no heartbeat with the detached dimensions arrived"

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
        reattached_event = _heartbeat_with_dimensions(
            metering,
            ip_id,
            {"attached": "true", "attribution": _attribution_dimension("compute_instance", ci2_uuid)},
            after=attached_to,
        )
        assert reattached_event is not None, "no heartbeat with the reattached dimensions arrived"

        ip_obj = grpc.get_external_ip(external_ip_id=ip_id)
        assert ip_obj["object"]["status"]["address"] == attached_ip_address, (
            "Re-attached ExternalIP should keep the same address"
        )

        # --- Cleanup: delete attachment, ExternalIP, then pool ---
        grpc.delete_external_ip_attachment(attachment_id=att2_id)
        wait_for_external_ip_attachment_deletion(k8s=k8s_hub_client, name=att2_cr_name)

        grpc.delete_external_ip(external_ip_id=ip_id)
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
