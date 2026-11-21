"""Quota on cluster hosts: a cluster's hosts count once, at a full tenant.

One world carries both journeys: a tenant whose limit for the worker type's
dimension leaves room for exactly one cluster's node-set ask.

1. A cluster's hosts count once, before and after they exist: the usage
   report shows the full ask the moment the create commits, with no host in
   existence yet, and still shows exactly the ask once every host exists and
   the cluster is Ready. The hosts the operator created for the cluster
   replace the cluster's ask instead of adding to it: the counter nets the
   ask against the hosts that carry the cluster link, and a second cluster
   of the same size is refused upfront because the tenant is full.
2. The operator's host creates pass at a full tenant: the limit equals the
   ask exactly, and the cluster still reaches Ready with all its hosts.
   Each system-created host nets against the room the cluster's ask already
   reserved for it, so a host adds nothing at a tenant with no room to
   spare. This journey is folded into the first one rather than duplicated:
   the same world proves it, because the cluster can only reach Ready with
   its hosts if every host create passed the quota check at the full tenant.

The retry journey (a host create that must be retried after the cluster's
ask was already consumed) is deliberately not covered here: the retrying
count is not implemented, tracked by
[OSAC-7791](https://redhat.atlassian.net/browse/OSAC-7791).
"""

from __future__ import annotations

import contextlib
import subprocess
from typing import Any

import pytest

from tests.e2e.core.grpc_client import PRIVATE_API, PUBLIC_API, GRPCClient
from tests.e2e.core.helpers import (
    unique_name,
    wait_for_cluster_deleting,
    wait_for_cluster_deletion,
    wait_for_cluster_grpc_deleting_or_archived,
    wait_for_cluster_grpc_removal,
    wait_for_cluster_order_cr,
    wait_for_cluster_progressing,
    wait_for_cluster_ready,
    wait_for_worker_agents,
    wait_for_worker_aggregates,
    wait_for_worker_installation,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import poll_until
from tests.e2e.core.wait_kinds import WaitResourceKind

pytestmark = pytest.mark.regression

# The shared CI worker type. Its name is fixed, so the session's quota
# bootstrap keeps its dimension quotable for every other journey.
WORKER_INSTANCE_TYPE = "ci-worker-bm"
NODE_SET = "workers"
NODE_SET_SIZE = 1


def _host_dimension(instance_type: str) -> str:
    """The quota dimension a host of this type counts against."""
    return f"bare_metal.instances.{instance_type}"


def _host_usage(private_grpc: GRPCClient, tenant: str, dimension: str) -> tuple[int, int]:
    """The tenant's usage and limit of one dimension, from the usage report."""
    report = private_grpc.call(service=f"{PRIVATE_API}.QuotaUsage/Get", data={"tenant": tenant})["report"]
    entries = [entry for entry in report.get("dimensions", []) if entry.get("dimension") == dimension]
    assert entries, f"the usage report of tenant {tenant} has no {dimension} entry"
    entry = entries[0]
    return int(entry["usage"]), int(entry["limit"])


def _cluster_hosts(grpc: GRPCClient, cluster_id: str) -> list[dict[str, Any]]:
    """The tenant-visible BMIs the system created for one cluster.

    Selection reads status.cluster.id, the exact link the usage counter nets
    the cluster's ask against, so the hosts this returns are the hosts the
    counting rule sees.
    """
    hosts = []
    for bmi_id in grpc.list_baremetal_instance_ids():
        bmi = grpc.get_baremetal_instance(bmi_id=bmi_id)["object"]
        cluster_ref = bmi.get("status", {}).get("cluster", {}) or {}
        if cluster_ref.get("id") == cluster_id:
            hosts.append(bmi)
    return hosts


def _ensure_tenant_limit(private_grpc: GRPCClient, tenant: str, dimension: str, value: int) -> str:
    """Create or replace the tenant's override of one dimension, return its id.

    A tenant's limit with no project is its override, which only the private
    (cloud provider admin) identity may set. Replaces the value when a prior
    run left an override behind, so repeated runs converge.
    """
    items = private_grpc.list_with_filter(
        service=f"{PRIVATE_API}.QuotaLimits/List",
        filter_expr=f'this.metadata.tenant == "{tenant}" && this.spec.dimension == "{dimension}"',
    )
    overrides = [item for item in items if not item.get("metadata", {}).get("project")]
    if overrides:
        limit_id = overrides[0]["id"]
        private_grpc.call(
            service=f"{PRIVATE_API}.QuotaLimits/Update",
            data={"object": {"id": limit_id, "spec": {"value": str(value)}}, "updateMask": {"paths": ["spec.value"]}},
        )
        return limit_id
    response = private_grpc.call(
        service=f"{PRIVATE_API}.QuotaLimits/Create",
        data={"object": {"metadata": {"tenant": tenant}, "spec": {"dimension": dimension, "value": str(value)}}},
    )
    return response["object"]["id"]


def test_cluster_hosts_count_once_at_a_full_tenant(
    cli: OsacCLI,
    grpc: GRPCClient,
    private_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    cluster_template: str,
    caas_disk_image_version: str,
    pull_secret_name: str,
    ssh_key_secret: str,
    jwt_tenant: str,
) -> None:
    """A cluster's ask counts in full before its hosts exist, the hosts the
    operator creates replace the ask instead of doubling it, and a second
    cluster of the same size is refused upfront at the exactly-full tenant."""
    dimension = _host_dimension(WORKER_INSTANCE_TYPE)
    private_grpc.ensure_bare_metal_instance_type(name=WORKER_INSTANCE_TYPE)

    baseline_usage, _ = _host_usage(private_grpc, jwt_tenant, dimension)
    # The limit leaves room for exactly this cluster's ask: whatever the
    # tenant already holds of the dimension plus the node-set size. Once the
    # cluster exists the tenant is full.
    limit_id = _ensure_tenant_limit(private_grpc, jwt_tenant, dimension, baseline_usage + NODE_SET_SIZE)

    name = unique_name("e2e-quota-cluster")
    refused_name = unique_name("e2e-quota-cluster-refused")
    uuid = ""
    try:
        uuid = cli.create_cluster(
            name=name,
            template=cluster_template,
            version=caas_disk_image_version,
            node_sets={NODE_SET: {"size": NODE_SET_SIZE, "baremetal_instance_type": {"name": WORKER_INSTANCE_TYPE}}},
            pull_secret=pull_secret_name,
            ssh_key=ssh_key_secret,
        )

        # Journey 1, before the hosts exist. No host can exist yet: the
        # operator creates hosts only after the fulfillment reconciler
        # materializes the ClusterOrder, which happens on its poll cycle
        # after the create commits. The ask counts in full regardless: the
        # report is taken in the same breath as the create's return.
        assert not _cluster_hosts(grpc, uuid), "no worker host can exist before the ClusterOrder does"
        usage, limit = _host_usage(private_grpc, jwt_tenant, dimension)
        assert usage == baseline_usage + NODE_SET_SIZE, (
            f"the ask must count immediately: usage {usage}, want {baseline_usage + NODE_SET_SIZE}"
        )
        assert limit == baseline_usage + NODE_SET_SIZE, "the tenant's override is the limit in force"

        # Journey 2, folded in: the cluster reaches Ready with all its hosts
        # at a tenant that is exactly full. Every host create the operator
        # issued had to pass the quota check with no room to spare; a host
        # that added its full price would have been refused and the waits
        # below would never pass.
        co_name = wait_for_cluster_order_cr(k8s=k8s_hub_client, uuid=uuid)
        wait_for_cluster_progressing(k8s=k8s_hub_client, name=co_name)
        wait_for_worker_agents(
            k8s=k8s_hub_client, co_name=co_name, namespace=k8s_hub_client.namespace, expected_agents=NODE_SET_SIZE
        )
        wait_for_worker_installation(
            k8s=k8s_hub_client, co_name=co_name, namespace=k8s_hub_client.namespace, expected_agents=NODE_SET_SIZE
        )
        wait_for_worker_aggregates(k8s=k8s_hub_client, co_name=co_name, expected_workers=NODE_SET_SIZE)
        wait_for_cluster_ready(k8s=k8s_hub_client, name=co_name)

        hosts = _cluster_hosts(grpc, uuid)
        assert len(hosts) == NODE_SET_SIZE, f"the cluster must own exactly its ask of hosts: {len(hosts)}"
        for host in hosts:
            assert host["metadata"]["tenant"] == jwt_tenant, "the hosts are tenant-owned"
            instance_type = host.get("spec", {}).get("instanceType", host.get("spec", {}).get("instance_type", {}))
            assert (instance_type or {}).get("name") == WORKER_INSTANCE_TYPE, "the hosts are of the limited type"

        # Journey 1, after the hosts exist: the hosts replaced the ask, so
        # the count did not double. The tenant is exactly full.
        usage, limit = _host_usage(private_grpc, jwt_tenant, dimension)
        assert usage == baseline_usage + NODE_SET_SIZE, (
            f"the hosts must not double the count: usage {usage}, want {baseline_usage + NODE_SET_SIZE}"
        )
        assert usage == limit, "the tenant is exactly full with the cluster's hosts"

        # A second cluster of the same size is refused upfront: the check
        # runs in the request's transaction, so nothing is stored.
        output, return_code = grpc.call_unchecked(
            service=f"{PUBLIC_API}.Clusters/Create",
            data={
                "object": {
                    "metadata": {"name": refused_name},
                    "spec": {
                        "template": {"name": cluster_template},
                        "version": {"name": caas_disk_image_version},
                        "node_sets": {
                            NODE_SET: {"size": NODE_SET_SIZE, "baremetal_instance_type": {"name": WORKER_INSTANCE_TYPE}}
                        },
                        "pull_secret_secret": {"name": pull_secret_name},
                        "ssh_key": {"name": ssh_key_secret},
                    },
                }
            },
        )
        assert return_code != 0, "a second cluster of the same size was accepted at a full tenant"
        assert "FailedPrecondition" in output, f"expected a FailedPrecondition refusal, got: {output}"
        assert "quota exceeded" in output, f"the refusal names the quota: {output}"
        assert dimension in output, f"the refusal names the dimension: {output}"
        assert f"tenant {jwt_tenant}" in output, f"the refusal names the tenant: {output}"
        assert not grpc.list_with_filter(
            service=f"{PUBLIC_API}.Clusters/List", filter_expr=f'this.metadata.name == "{refused_name}"'
        ), "the refused create must store no cluster"

        cli.delete_cluster(uuid=uuid)
        wait_for_cluster_deleting(k8s=k8s_hub_client, name=co_name)
        wait_for_cluster_grpc_deleting_or_archived(grpc=grpc, uuid=uuid)
        wait_for_cluster_deletion(k8s=k8s_hub_client, name=co_name)
        wait_for_cluster_grpc_removal(grpc=grpc, uuid=uuid)

        # The counted ask left with the cluster: the cluster's row is archived
        # once it leaves the list, and its hosts' rows archive with the
        # teardown, so the tenant's room comes back.
        poll_until(
            fn=lambda: _host_usage(private_grpc, jwt_tenant, dimension)[0],
            until=lambda usage: usage == baseline_usage,
            retries=30,
            delay=5,
            description=f"{co_name} host quota released",
            retry_on_error=True,
            resource_kind=WaitResourceKind.CLUSTER_ORDER,
            resource_id=co_name,
        )
    finally:
        if uuid:
            with contextlib.suppress(subprocess.SubprocessError):
                cli.delete_cluster(uuid=uuid)
        with contextlib.suppress(Exception):
            private_grpc.call(service=f"{PRIVATE_API}.QuotaLimits/Delete", data={"id": limit_id})
