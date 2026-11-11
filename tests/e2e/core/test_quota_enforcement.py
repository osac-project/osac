"""Quota enforcement in the request path.

An admin caps a project's storage, a tenant user submits an order that would
exceed it, and the API refuses the order before anything is written: the
rejection names the scope and the dimension, and the resource count is
unchanged.
"""

from __future__ import annotations

import contextlib
from typing import Any

import pytest

from tests.e2e.core.grpc_client import PRIVATE_API, GRPCClient
from tests.e2e.core.helpers import assert_grpc_rejected, grpc_error_message, unique_name
from tests.e2e.core.runner import run

pytestmark = [pytest.mark.regression]

# The topology segment a node-local volume needs; the cluster under test is single-node.
NODE_TOPOLOGY_SEGMENT = "osac.io/node"
VOLUME_SIZE_GIB = 5


@pytest.fixture
def quota_project(grpc: GRPCClient):
    """A project the quota journey caps, isolates its limits from the rest of the session."""
    name = unique_name("e2e-quota-project")
    project_id: str = grpc.create_project(name=name)
    yield name
    with contextlib.suppress(Exception):
        grpc.delete_project(project_id=project_id)


def _set_project_limit(private_grpc: GRPCClient, tenant: str, project: str, dimension: str, value: int) -> str:
    response: dict[str, Any] = private_grpc.call(
        service=f"{PRIVATE_API}.QuotaLimits/Create",
        data={
            "object": {
                "metadata": {"tenant": tenant, "project": project},
                "spec": {"dimension": dimension, "value": str(value)},
            }
        },
    )
    return response["object"]["id"]


def _delete_limit(private_grpc: GRPCClient, limit_id: str) -> None:
    with contextlib.suppress(Exception):
        private_grpc.call(service=f"{PRIVATE_API}.QuotaLimits/Delete", data={"id": limit_id})


def _create_volume(private_grpc: GRPCClient, tenant: str, project: str, name: str, node: str) -> dict[str, Any]:
    return private_grpc.call(
        service=f"{PRIVATE_API}.Volumes/Create",
        data={
            "object": {
                "metadata": {"name": name, "tenant": tenant, "project": project},
                "spec": {
                    "storageTier": "local",
                    "sizeGib": str(VOLUME_SIZE_GIB),
                    "accessMode": "VOLUME_ACCESS_MODE_READ_WRITE_ONCE",
                    "topology": {"segments": {NODE_TOPOLOGY_SEGMENT: node}},
                },
            }
        },
    )


def _count_project_volumes(private_grpc: GRPCClient, tenant: str, project: str) -> int:
    response: dict[str, Any] = private_grpc.call(
        service=f"{PRIVATE_API}.Volumes/List",
        data={"filter": f'this.metadata.tenant == "{tenant}" && this.metadata.project == "{project}"'},
    )
    return len(response.get("items") or [])


def test_over_limit_order_is_refused_and_writes_nothing(
    grpc: GRPCClient, private_grpc: GRPCClient, quota_project: str, jwt_tenant: str
) -> None:
    """An order over the project's volume limit is refused with a rejection that
    names the project and the dimension, and creates nothing."""
    node = run("kubectl", "get", "nodes", "-o", "jsonpath={.items[0].metadata.name}")

    volumes_limit = _set_project_limit(private_grpc, jwt_tenant, quota_project, "storage.volumes", 1)
    capacity_limit = _set_project_limit(private_grpc, jwt_tenant, quota_project, "storage.local.gib", 100)
    try:
        first = _create_volume(private_grpc, jwt_tenant, quota_project, unique_name("e2e-quota-vol"), node)
        assert _count_project_volumes(private_grpc, jwt_tenant, quota_project) == 1

        with pytest.raises(Exception) as exc_info:
            _create_volume(private_grpc, jwt_tenant, quota_project, unique_name("e2e-quota-vol"), node)
        assert_grpc_rejected(exc_info, "FailedPrecondition")
        message = grpc_error_message(exc_info)
        assert "quota exceeded" in message, f"the rejection names the quota: {message}"
        assert "storage.volumes" in message, f"the rejection names the dimension: {message}"
        assert quota_project in message, f"the rejection names the project: {message}"

        assert _count_project_volumes(private_grpc, jwt_tenant, quota_project) == 1

        with contextlib.suppress(Exception):
            private_grpc.call(service=f"{PRIVATE_API}.Volumes/Delete", data={"id": first["object"]["id"]})
    finally:
        _delete_limit(private_grpc, volumes_limit)
        _delete_limit(private_grpc, capacity_limit)
