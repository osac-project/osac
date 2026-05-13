from __future__ import annotations

from tests.core.grpc_client import GRPCClient
from tests.core.helpers import (
    wait_for_public_ip_allocated,
    wait_for_public_ip_deletion,
    wait_for_public_ip_pool_deletion,
)
from tests.core.k8s_client import K8sClient
from tests.core.runner import poll_until


def test_public_ip_pool_lifecycle(
    public_ip_pool: tuple[str, str],
    public_ip: tuple[str, str],
    grpc: GRPCClient,
    private_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
) -> None:
    pool_id, pool_cr_name = public_ip_pool
    ip_id, ip_cr_name = public_ip

    assert pool_id in private_grpc.list_public_ip_pool_ids()

    assert ip_id in grpc.list_public_ip_ids()
    wait_for_public_ip_allocated(k8s=k8s_hub_client, name=ip_cr_name)

    # Delete the PublicIP first, then the pool
    grpc.delete_public_ip(public_ip_id=ip_id)
    wait_for_public_ip_deletion(k8s=k8s_hub_client, name=ip_cr_name)
    poll_until(
        fn=lambda: ip_id not in grpc.list_public_ip_ids(),
        until=lambda v: v is True,
        retries=30,
        delay=5,
        description=f"PublicIP {ip_id} removal from API",
    )

    # TODO Attach the PublicIP to a ComputeInstance and verify attach -> detach lifecycle

    private_grpc.delete_public_ip_pool(pool_id=pool_id)
    wait_for_public_ip_pool_deletion(k8s=k8s_hub_client, name=pool_cr_name)
    poll_until(
        fn=lambda: pool_id not in private_grpc.list_public_ip_pool_ids(),
        until=lambda v: v is True,
        retries=30,
        delay=5,
        description=f"PublicIPPool {pool_id} removal from API",
    )
