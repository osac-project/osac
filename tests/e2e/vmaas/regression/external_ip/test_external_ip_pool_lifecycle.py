from __future__ import annotations

import itertools
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
from tests.e2e.core.metering import HEARTBEAT_TYPE, MeteringCollector, parse_time
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
        ip_project: str,
    ) -> None:
        pool_id, pool_cr_name = external_ip_pool
        pool_name = private_grpc.get_external_ip_pool(pool_id=pool_id)["object"]["metadata"]["name"]
        ip_id, ip_cr_name = external_ip
        (ci1_uuid, _ci1_name), (ci2_uuid, _ci2_name) = make_compute_instances(2)

        assert pool_id in private_grpc.list_external_ip_pool_ids()
        assert ip_id in grpc.list_external_ip_ids()
        wait_for_external_ip_allocated(k8s=k8s_hub_client, name=ip_cr_name)
        # The IP's heartbeats carry the project it was created in, so the
        # project dimension is a real attribution check.
        metering.expect(HEARTBEAT_TYPE, resource_id=ip_id, timeout=180)
        metering.verify()
        allocated = metering.get_event(HEARTBEAT_TYPE, resource_id=ip_id)
        assert allocated["data"]["meter"] == "allocation", f"meter is {allocated['data']['meter']!r}, want 'allocation'"
        # References render by name in heartbeats: the pool dimension is the pool's
        # name, the same contract as the instance_type dimension.
        assert allocated["data"]["billing_dimensions"]["pool"] == pool_name
        assert allocated["data"]["billing_dimensions"]["ip_family"] == "ipv4"
        assert allocated["data"]["billing_dimensions"]["attached"] == "false"
        assert allocated["data"]["billing_dimensions"]["project"] == ip_project, (
            f"project dimension is {allocated['data']['billing_dimensions'].get('project')!r}, want {ip_project!r}"
        )

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
        # starts billing: a heartbeat arrives with the attached dimensions.
        attached_event = metering.heartbeat_with_dimensions(ip_id, {"attached": "true"}, delay=5)
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
        attached_to = parse_time(str(attached_event["data"]["usage"]["to"]))
        detached_event = metering.heartbeat_with_dimensions(ip_id, {"attached": "false"}, after=attached_to, delay=5)
        assert detached_event is not None, "no heartbeat with the detached dimensions arrived"
        detached_to = parse_time(str(detached_event["data"]["usage"]["to"]))

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
        reattached_event = metering.heartbeat_with_dimensions(ip_id, {"attached": "true"}, after=detached_to, delay=5)
        assert reattached_event is not None, "no heartbeat with the reattached dimensions arrived"

        ip_obj = grpc.get_external_ip(external_ip_id=ip_id)
        assert ip_obj["object"]["status"]["address"] == attached_ip_address, (
            "Re-attached ExternalIP should keep the same address"
        )

        # The attached and unattached meters are disjoint: the net intervals each
        # dimension set bills never overlap, so no second is billed on both during
        # a transition. A wrong attachment transition time bills both meters over
        # the same span, which the per-phase existence checks above cannot see.
        positive: dict[tuple[tuple[str, str], ...], list[tuple[datetime, datetime]]] = {}
        negative: dict[tuple[tuple[str, str], ...], list[tuple[datetime, datetime]]] = {}
        for event in metering.get_all_events(HEARTBEAT_TYPE, ip_id):
            dimensions = event.get("data", {}).get("billing_dimensions", {})
            key = tuple(sorted((name, str(value)) for name, value in dimensions.items()))
            usage = event["data"]["usage"]
            span = (parse_time(usage["from"]), parse_time(usage["to"]))
            target = negative if str(usage["quantity"]).startswith("-") else positive
            target.setdefault(key, []).append(span)

        def _net_spans(
            spans: list[tuple[datetime, datetime]], cuts: list[tuple[datetime, datetime]]
        ) -> list[tuple[datetime, datetime]]:
            # Subtract each negative correction from the positive spans: a round
            # that ran before a transition was folded bills the old value past
            # it, and the correction cancels only the tail. The raw spans would
            # overlap on correct billing; the net does not.
            kept: list[tuple[datetime, datetime]] = []
            for start, end in spans:
                pieces = [(start, end)]
                for cut_start, cut_end in cuts:
                    remainder = []
                    for piece_start, piece_end in pieces:
                        if cut_end <= piece_start or cut_start >= piece_end:
                            remainder.append((piece_start, piece_end))
                            continue
                        if piece_start < cut_start:
                            remainder.append((piece_start, cut_start))
                        if cut_end < piece_end:
                            remainder.append((cut_end, piece_end))
                    pieces = remainder
                kept.extend(pieces)
            return sorted(kept)

        dimension_sets = [_net_spans(spans, negative.get(key, [])) for key, spans in positive.items()]
        for first, second in itertools.combinations(dimension_sets, 2):
            for first_start, first_end in first:
                for second_start, second_end in second:
                    overlaps = first_start < second_end and second_start < first_end
                    assert not overlaps, (
                        f"two attachment meters billed the same span: "
                        f"{first_start}..{first_end} overlaps {second_start}..{second_end}"
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
