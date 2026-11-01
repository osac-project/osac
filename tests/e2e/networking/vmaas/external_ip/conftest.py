"""Fixtures for ExternalIP tests that need provider networking.

The ExternalIPPool fixtures and the VMaaS default networking are shared with the standard suites
and imported from their conftest modules rather than copied. The instance type and disk image
come from tests/e2e/networking/vmaas/conftest.py.
"""

from __future__ import annotations

import contextlib
import logging
import subprocess
from collections.abc import Callable, Generator

import pytest

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import (
    unique_name,
    wait_for_cr,
    wait_for_deletion,
    wait_for_external_ip_deletion,
    wait_for_running,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.networking.vmaas.external_ip.helpers import create_ip
from tests.e2e.vmaas.conftest import (  # noqa: F401
    default_network_attachment,
    default_networking,
    default_security_group,
    default_subnet,
)
from tests.e2e.vmaas.regression.external_ip.conftest import make_pool, small_pool  # noqa: F401

logger = logging.getLogger(__name__)


@pytest.fixture
def external_ip_pool(make_pool: Callable[..., tuple[str, str]]) -> tuple[str, str]:  # noqa: F811
    return make_pool()


@pytest.fixture(scope="class")
def created_ips(grpc: GRPCClient, k8s_hub_client: K8sClient) -> Generator[list[tuple[str, str]], None, None]:
    """Track IPs across chained tests; clean up any survivors on teardown."""
    ips: list[tuple[str, str]] = []
    yield ips
    for ip_id, ip_cr_name in reversed(ips):
        if not k8s_hub_client.is_present(resource="externalip", name=ip_cr_name):
            continue
        try:
            grpc.delete_external_ip(external_ip_id=ip_id)
            wait_for_external_ip_deletion(k8s=k8s_hub_client, name=ip_cr_name)
        except subprocess.CalledProcessError:
            logger.warning("ExternalIP %s teardown failed, may need manual cleanup", ip_id)


@pytest.fixture
def external_ip(
    grpc: GRPCClient, k8s_hub_client: K8sClient, external_ip_pool: tuple[str, str], ip_project: str
) -> Generator[tuple[str, str], None, None]:
    pool_id, _ = external_ip_pool
    ip_id, ip_cr_name = create_ip(grpc, k8s_hub_client, pool_id, project=ip_project)
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
    default_network_attachment: dict[str, object],  # noqa: F811
    default_instance_type: str,
    default_disk_image: str,
    default_storage_tier: str,
) -> Generator[Callable[..., tuple[tuple[str, str], ...]], None, None]:
    # The VMaaS suite sets these as CLI defaults through autouse fixtures that do not apply here.
    created: list[tuple[str, str]] = []

    def _make(count: int = 2) -> tuple[tuple[str, str], ...]:
        instances: list[tuple[str, str]] = []
        for _ in range(count):
            name = unique_name("e2e-ci")
            uuid = cli.create_compute_instance(
                name=name,
                template=vm_template,
                network_attachments=[default_network_attachment],
                instance_type=default_instance_type,
                disk_image=default_disk_image,
                boot_disk_storage_tier=default_storage_tier,
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


@pytest.fixture(scope="class")
def ip_project(grpc: GRPCClient) -> Generator[str, None, None]:
    """A project the lifecycle test's IP is attributed to, so the heartbeat's
    project dimension is a real attribution check rather than an absent field."""
    name = unique_name("e2e-eip-metering-project")
    project_id: str = grpc.create_project(name=name)
    yield name
    with contextlib.suppress(Exception):
        grpc.delete_project(project_id=project_id)
