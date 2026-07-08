from __future__ import annotations

import logging
import subprocess
from collections.abc import Callable, Generator
from uuid import uuid4

import pytest

from tests.core.grpc_client import GRPCClient
from tests.core.helpers import (
    wait_for_cr,
    wait_for_deletion,
    wait_for_external_ip_attachment_deletion,
    wait_for_external_ip_deletion,
    wait_for_external_ip_pool_cr,
    wait_for_external_ip_pool_deletion,
    wait_for_external_ip_pool_grpc_ready,
    wait_for_external_ip_pool_ready,
    wait_for_running,
)
from tests.core.k8s_client import K8sClient
from tests.core.osac_cli import OsacCLI
from tests.vmaas.external_ip.helpers import allocate_worker_subnet, create_ip

logger = logging.getLogger(__name__)


@pytest.fixture(scope="class")
def make_pool(
    grpc: GRPCClient, private_grpc: GRPCClient, k8s_hub_client: K8sClient
) -> Generator[..., None, None]:
    created: list[tuple[str, str]] = []

    def _make(*, prefix: int = 24, name_prefix: str = "test-pool") -> tuple[str, str]:
        pool_name = f"{name_prefix}-{uuid4().hex[:8]}"
        subnet = allocate_worker_subnet(prefix=prefix)
        pool_id = private_grpc.create_external_ip_pool(name=pool_name, cidrs=[str(subnet)])
        pool_cr_name = wait_for_external_ip_pool_cr(k8s=k8s_hub_client, uuid=pool_id)
        wait_for_external_ip_pool_ready(k8s=k8s_hub_client, name=pool_cr_name)
        wait_for_external_ip_pool_grpc_ready(private_grpc=private_grpc, pool_id=pool_id)
        created.append((pool_id, pool_cr_name))
        return pool_id, pool_cr_name

    yield _make

    for pool_id, pool_cr_name in reversed(created):
        if not k8s_hub_client.is_present(resource="externalippool", name=pool_cr_name):
            continue
        try:
            _cleanup_pool_children(grpc, k8s_hub_client, pool_id)
            private_grpc.delete_external_ip_pool(pool_id=pool_id)
        except subprocess.CalledProcessError as exc:
            stderr = exc.stderr or ""
            if "Unavailable" in stderr or "no route to host" in stderr or "connection" in stderr.lower():
                logger.warning("ExternalIPPool %s teardown skipped — gRPC unreachable: %s", pool_id, stderr.strip())
                continue
            if "NotFound" not in stderr:
                logger.warning("ExternalIPPool %s teardown delete failed: %s", pool_id, stderr.strip())
                continue
            logger.warning("ExternalIPPool %s already deleted", pool_id)
        wait_for_external_ip_pool_deletion(k8s=k8s_hub_client, name=pool_cr_name)


def _cleanup_pool_children(grpc: GRPCClient, k8s: K8sClient, pool_id: str) -> None:
    """Delete all attachments and IPs belonging to a pool before deleting it."""
    pool_ip_ids: set[str] = set()
    for ip_id in grpc.list_external_ip_ids():
        try:
            ip_obj = grpc.get_external_ip(external_ip_id=ip_id)
            if ip_obj["object"]["spec"].get("pool") == pool_id:
                pool_ip_ids.add(ip_id)
        except subprocess.CalledProcessError:
            continue

    for att_id in grpc.list_external_ip_attachment_ids():
        try:
            att = grpc.get_external_ip_attachment(attachment_id=att_id)
            if att["object"]["spec"].get("external_ip") in pool_ip_ids:
                grpc.delete_external_ip_attachment(attachment_id=att_id)
                att_name = k8s.get_external_ip_attachment_name(uuid=att_id, checked=False)
                if att_name:
                    wait_for_external_ip_attachment_deletion(k8s=k8s, name=att_name)
        except subprocess.CalledProcessError:
            continue

    for ip_id in pool_ip_ids:
        try:
            grpc.delete_external_ip(external_ip_id=ip_id)
            ip_name = k8s.get_external_ip_name(uuid=ip_id, checked=False)
            if ip_name:
                wait_for_external_ip_deletion(k8s=k8s, name=ip_name)
        except subprocess.CalledProcessError:
            continue


@pytest.fixture
def external_ip_pool(make_pool: Callable[..., tuple[str, str]]) -> tuple[str, str]:
    return make_pool()


@pytest.fixture(scope="class")
def small_pool(make_pool: Callable[..., tuple[str, str]]) -> tuple[str, str]:
    """A /30 pool with 2 usable IPs."""
    return make_pool(prefix=30, name_prefix="test-small-capacity-pool")


@pytest.fixture(scope="class")
def created_ips(grpc: GRPCClient) -> Generator[list[tuple[str, str]], None, None]:
    """Track IPs across chained tests; clean up any survivors on teardown."""
    ips: list[tuple[str, str]] = []
    yield ips
    for ip_id, _ in reversed(ips):
        try:
            grpc.delete_external_ip(external_ip_id=ip_id)
        except subprocess.CalledProcessError:
            logger.warning("ExternalIP %s teardown failed, may need manual cleanup", ip_id)


@pytest.fixture
def external_ip(
    grpc: GRPCClient, k8s_hub_client: K8sClient, external_ip_pool: tuple[str, str]
) -> Generator[tuple[str, str], None, None]:
    pool_id, _ = external_ip_pool
    ip_id, ip_cr_name = create_ip(grpc, k8s_hub_client, pool_id)
    yield ip_id, ip_cr_name
    if k8s_hub_client.is_present(resource="externalip", name=ip_cr_name):
        try:
            grpc.delete_external_ip(external_ip_id=ip_id)
        except subprocess.CalledProcessError as exc:
            logger.warning("ExternalIP %s gRPC delete failed in teardown: %s", ip_id, (exc.stderr or "").strip())
        wait_for_external_ip_deletion(k8s=k8s_hub_client, name=ip_cr_name)


@pytest.fixture(scope="class")
def make_compute_instances(
    cli: OsacCLI,
    k8s_hub_client: K8sClient,
    vm_template: str,
    default_subnet: str,
) -> Generator[Callable[..., tuple[tuple[str, str], ...]], None, None]:
    created: list[tuple[str, str]] = []

    def _make(count: int = 2) -> tuple[tuple[str, str], ...]:
        instances: list[tuple[str, str]] = []
        for _ in range(count):
            uuid = cli.create_compute_instance(
                template=vm_template,
                network_attachments=[{"subnet": default_subnet}],
            )
            name = wait_for_cr(k8s=k8s_hub_client, uuid=uuid)
            created.append((uuid, name))
            instances.append((uuid, name))
        for _, name in instances:
            wait_for_running(k8s=k8s_hub_client, name=name)
        return tuple(instances)

    yield _make

    for ci_uuid, ci_name in reversed(created):
        if not k8s_hub_client.is_present(resource="computeinstance", name=ci_name):
            continue
        try:
            cli.delete_compute_instance(uuid=ci_uuid)
        except subprocess.CalledProcessError as exc:
            logger.warning("ComputeInstance %s teardown failed: %s", ci_uuid, (exc.stderr or "").strip())
            continue
        wait_for_deletion(k8s=k8s_hub_client, name=ci_name)
