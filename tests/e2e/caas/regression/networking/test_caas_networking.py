"""CaaS cluster lifecycle tests with ClusterNetworkAttachment.

Tests cover:
- Cluster creation with managed VN/Subnet/SG via ``--network-attachment``
- Fail-fast on Failed phase during provisioning
- VIP endpoint verification after Ready
- Negative validation: nonexistent subnet, wrong-VN security group
- Default networking: cluster creation without explicit ``--network-attachment``
- Cleanup: cluster deletion with networking resources

All tests use JWT auth (tenant1_admin) so that both clusters and networking
resources land in the same tenant scope.
"""

from __future__ import annotations

import contextlib
import ipaddress
import logging
import subprocess
import tempfile
import time
from collections import Counter
from pathlib import Path

import pytest

from tests.e2e.core.grpc_client import PRIVATE_API, GRPCClient
from tests.e2e.core.helpers import (
    node_pool_ready,
    unique_name,
    wait_for_cluster_deleting,
    wait_for_cluster_deletion,
    wait_for_cluster_deletion_with_deadline,
    wait_for_cluster_grpc_deleting_or_archived,
    wait_for_cluster_grpc_removal,
    wait_for_cluster_order_cr,
    wait_for_cluster_progressing,
    wait_for_cluster_ready,
    wait_for_external_ip_allocated,
    wait_for_external_ip_attachment_cr,
    wait_for_external_ip_attachment_deletion,
    wait_for_external_ip_attachment_ready,
    wait_for_external_ip_cr,
    wait_for_external_ip_deletion,
    wait_for_hosted_cluster_kubeconfig,
    wait_for_security_group_cr,
    wait_for_security_group_deletion,
    wait_for_security_group_ready,
    wait_for_subnet_cr,
    wait_for_subnet_deletion,
    wait_for_subnet_ready,
    wait_for_virtual_network_cr,
    wait_for_virtual_network_deletion,
    wait_for_virtual_network_ready,
    wait_for_workload_cluster_health,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import poll_until, run_unchecked

pytestmark = [pytest.mark.regression, pytest.mark.requires_caas]

log = logging.getLogger(__name__)


def _report_agent_diagnostics(*, k8s: K8sClient, co_name: str | None, context: str) -> None:
    """Capture Agent state, labels, and events for failure diagnostics.

    Reports:
    - Per-agent state, binding, labels (excluding secrets)
    - Recent events for Agents in the ClusterOrder namespace
    - Conditions summary
    """
    import json as _json

    agent_ns = k8s.namespace
    base_args = [*k8s._base(), "--as", "system:admin"]

    try:
        output, rc = run_unchecked(*base_args, "get", "agents.agent-install.openshift.io", "-n", agent_ns, "-o", "json")
        if rc != 0:
            log.warning("Agent diagnostics query failed (rc=%d) [%s]: %s", rc, context, output[:500])
            return

        data = _json.loads(output)
        items = data.get("items", [])
        if not items:
            log.warning("Agent diagnostics: no agents in %s [%s]", agent_ns, context)
            return

        log.warning("=== Agent diagnostics (%d agents) [%s] ===", len(items), context)
        for agent in items:
            meta = agent.get("metadata", {})
            spec = agent.get("spec", {})
            status = agent.get("status", {})
            debug_info = status.get("debugInfo", {})
            labels = meta.get("labels", {})
            conditions = status.get("conditions", [])
            cond_summary = ", ".join(f"{c.get('type', '?')}={c.get('status', '?')}" for c in conditions[:5])
            cluster_ref = spec.get("clusterDeploymentName", {})
            if isinstance(cluster_ref, dict):
                binding = f"{cluster_ref.get('namespace', '')}/{cluster_ref.get('name', '')}"
            else:
                binding = str(cluster_ref) if cluster_ref else "unbound"
            state = debug_info.get("state", "unknown")
            resource_class = labels.get("osac.openshift.io/resource-class", "")
            clusterorder = labels.get("osac.openshift.io/clusterorder", "")
            cd_ns_label = labels.get("agent-install.openshift.io/clusterdeployment-namespace", "")
            log.warning(
                "  Agent %s: state=%s, binding=%s, rc=%s, co=%s, cd_ns=%s, conditions=[%s]",
                meta.get("name", "?"),
                state,
                binding,
                resource_class,
                clusterorder or "none",
                cd_ns_label or "empty",
                cond_summary,
            )
    except Exception as exc:
        log.warning("Agent diagnostics failed [%s]: %s", context, exc)

    # Events for the ClusterOrder namespace (Agents)
    try:
        events_output, rc = run_unchecked(
            *base_args,
            "get",
            "events",
            "-n",
            agent_ns,
            "--sort-by=.metadata.creationTimestamp",
            "-o",
            "custom-columns=TIME:.metadata.creationTimestamp,TYPE:.type,REASON:.reason,OBJECT:.involvedObject.name,MSG:.message",
        )
        if rc == 0 and events_output.strip():
            # Show last 20 events
            lines = events_output.strip().split("\n")
            log.warning("=== Recent events in %s (last 20) [%s] ===", agent_ns, context)
            for line in lines[-20:]:
                log.warning("  %s", line)
    except Exception:
        pass

    # HostedCluster and ClusterDeployment status for reclaim stall diagnosis.
    # Don't treat Kind simulation as proof of real controller behavior —
    # capture the real state so actual reclaim issues can be debugged.
    if co_name:
        _capture_reclaim_evidence(k8s=k8s, co_name=co_name, context=context)


def _capture_reclaim_evidence(*, k8s: K8sClient, co_name: str, context: str) -> None:
    """Capture HostedCluster, ClusterDeployment, and Agent YAML for reclaim stall diagnosis.

    When reclaim stalls after steps 1-2, this preserves the HostedCluster
    (does NOT delete it) and captures:
    - Agent events and full YAML for bound agents
    - HostedCluster status and conditions
    - ClusterDeployment status (if present)
    - AAP task output (recent provisioning jobs)
    - Reclaim workload/pod state on the spoke (if reachable)

    No credentials or secrets are included — only metadata and status fields.
    """
    base_args = [*k8s._base(), "--as", "system:admin"]

    # HostedCluster namespace and name
    hc_ns = f"{k8s.namespace}-{co_name}"

    # HostedCluster status
    try:
        hc_output, rc = run_unchecked(
            *base_args, "get", "hostedcluster", co_name, "-n", hc_ns, "-o", "jsonpath={.status}"
        )
        if rc == 0 and hc_output.strip():
            log.warning("=== HostedCluster %s/%s status [%s] ===", hc_ns, co_name, context)
            log.warning("  %s", hc_output[:2000])
    except Exception:
        pass

    # ClusterDeployment status (if present, in the HostedCluster namespace)
    try:
        cd_output, rc = run_unchecked(
            *base_args,
            "get",
            "clusterdeployments.hive.openshift.io",
            "-n",
            hc_ns,
            "-o",
            "custom-columns=NAME:.metadata.name,INSTALLED:.spec.installed,PLATFORM:.spec.platform",
        )
        if rc == 0 and cd_output.strip():
            log.warning("=== ClusterDeployments in %s [%s] ===", hc_ns, context)
            for line in cd_output.strip().split("\n")[:10]:
                log.warning("  %s", line)
    except Exception:
        pass

    # Provisioning jobs from ClusterOrder status
    try:
        co_status = k8s.get_cluster_order_status(name=co_name)
        jobs = co_status.get("provisioningJobs", [])
        if jobs:
            sorted_jobs = sorted(jobs, key=lambda j: j.get("timestamp", ""), reverse=True)[:5]
            log.warning("=== Recent provisioning jobs for ClusterOrder %s [%s] ===", co_name, context)
            for j in sorted_jobs:
                log.warning(
                    "  jobID=%s, type=%s, state=%s, message=%r, timestamp=%s",
                    j.get("jobID", "?"),
                    j.get("type", "?"),
                    j.get("state", "?"),
                    j.get("message", ""),
                    j.get("timestamp", ""),
                )
    except Exception:
        pass


def _assert_caas_worker_ips_in_subnet(
    *, grpc: GRPCClient, cluster_order_status: dict[str, object], subnet_cidr: str, subnet_ref: str
) -> None:
    """Verify each Ready CaaS worker has a discovered address in its tenant subnet."""
    workers = [
        worker for worker in cluster_order_status.get("workers", []) if worker.get("kind") == "BareMetalInstance"
    ]
    assert workers, "Ready ClusterOrder has no BareMetalInstance workers"

    subnet = ipaddress.ip_network(subnet_cidr)
    for worker in workers:
        assert worker.get("phase") == "Ready", f"CaaS worker {worker.get('name')} is not Ready"
        bmi_id = worker.get("resourceID", "")
        assert bmi_id, f"CaaS worker {worker.get('name')} has no BareMetalInstance resource ID"

        def network_statuses(bmi_id: str = bmi_id) -> list[dict[str, object]]:
            instance = grpc.get_baremetal_instance(bmi_id=bmi_id).get("object", {})
            return instance.get("status", {}).get("networkAttachmentStatuses", [])

        statuses = poll_until(
            fn=network_statuses,
            until=lambda items: any(item.get("primary") and item.get("ipAddress") for item in items),
            retries=30,
            delay=2,
            description=f"tenant-network IP feedback for CaaS worker {bmi_id}",
        )
        primary_statuses = [item for item in statuses if item.get("primary")]
        assert len(primary_statuses) == 1, f"CaaS worker {bmi_id} should have exactly one primary network"
        assert primary_statuses[0].get("subnetRef") == subnet_ref, (
            f"CaaS worker {bmi_id} primary network uses subnet "
            f"{primary_statuses[0].get('subnetRef')!r}, expected tenant subnet {subnet_ref!r}"
        )
        address = ipaddress.ip_address(primary_statuses[0]["ipAddress"])
        assert address in subnet, f"CaaS worker {bmi_id} address {address} is outside tenant subnet {subnet}"


def _cleanup_cluster(
    *,
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s: K8sClient,
    uuid: str,
    co_name: str | None,
    deadline: float | None = None,
    deletion_requested: bool = False,
) -> None:
    """Request cluster deletion and wait using the shared deadline.

    Uses ONE deletion deadline across the normal wait and cleanup — the
    ``finally`` cleanup does NOT repeat the full 20-minute timeout.
    Preserves the primary failure, reports cleanup failures, and avoids
    a second full deletion wait.

    Tracks deletion-requested state to avoid duplicate delete API calls.
    On timeout, reports remaining finalizers and relevant Agent state.
    """
    if not deletion_requested:
        with contextlib.suppress(subprocess.SubprocessError):
            cli.delete_cluster(uuid=uuid)

    if co_name:
        try:
            if deadline is not None:
                wait_for_cluster_deletion_with_deadline(k8s=k8s, name=co_name, deadline=deadline)
            else:
                wait_for_cluster_deletion(k8s=k8s, name=co_name)
        except Exception as cleanup_err:
            log.warning("Cluster cleanup wait failed for %s: %s", co_name, cleanup_err)
            # Enhanced diagnostics on cleanup failure
            _report_agent_diagnostics(k8s=k8s, co_name=co_name, context=f"cleanup-failure for {co_name}")

    with contextlib.suppress(Exception):
        wait_for_cluster_grpc_removal(grpc=grpc, uuid=uuid)


# ---------------------------------------------------------------------------
# Lifecycle: create cluster with network attachment, verify, delete
# ---------------------------------------------------------------------------
@pytest.mark.requires_caas_fabric
@pytest.mark.serial
class TestCaasClusterWithNetworkAttachment:
    """Full lifecycle test: create with --network-attachment, wait Ready, verify, delete."""

    @pytest.mark.xdist_group("caas-cluster-provision")
    def test_cluster_lifecycle_with_network_attachment(
        self,
        cli: OsacCLI,
        grpc: GRPCClient,
        k8s_hub_client: K8sClient,
        cluster_template: str,
        caas_disk_image_version: str,
        pull_secret_path: str,
        ssh_public_key_path: str,
        caas_worker_node_sets: dict[str, dict[str, object]],
        caas_networking: dict[str, str],
    ) -> None:
        subnet_name = caas_networking["subnet_name"]
        sg_name = caas_networking["sg_name"]
        name = unique_name("e2e-caas-net")

        # Build the --network-attachment value: subnet=<name>,security-groups=<sg>
        network_attachment = f"subnet={subnet_name},security-groups={sg_name}"

        uuid = cli.create_cluster(
            name=name,
            template=cluster_template,
            version=caas_disk_image_version,
            template_parameter_files={"pull_secret": pull_secret_path},
            template_parameters={"ssh_public_key": Path(ssh_public_key_path).read_text().strip()},
            node_sets=caas_worker_node_sets,
            network_attachment=network_attachment,
        )
        print(f"Created cluster {name}: {uuid}")

        # Deletion deadline is set WHEN deletion is requested (after Ready/
        # Failed), not at test start.  This ensures the full 20-minute budget
        # is available for the deletion phase rather than being consumed by
        # the provisioning wait.  Initialized to None; the finally cleanup
        # falls back to a fresh deadline if deletion was never requested.
        deletion_deadline: float | None = None
        deletion_requested = False

        co_name: str | None = None
        try:
            # Wait for CR to appear
            co_name = wait_for_cluster_order_cr(k8s=k8s_hub_client, uuid=uuid)
            assert uuid in grpc.list_cluster_ids()

            # Check the ClusterOrder attachment immediately after the order
            # appears — before waiting for Ready — so the reconciler mapping
            # is validated early.
            co_spec = k8s_hub_client.get_cluster_order_spec(name=co_name)
            co_na = co_spec.get("networkAttachment", {})
            assert co_na.get("subnetRef") == caas_networking["subnet_cr"], (
                f"ClusterOrder {co_name} networkAttachment.subnetRef is "
                f"{co_na.get('subnetRef')!r}; expected {caas_networking['subnet_cr']!r}"
            )
            assert co_na.get("securityGroupRefs") == [caas_networking["sg_cr"]], (
                f"ClusterOrder {co_name} networkAttachment.securityGroupRefs is "
                f"{co_na.get('securityGroupRefs')!r}; expected the security group "
                f"{caas_networking['sg_cr']!r} from the same VirtualNetwork"
            )

            # Wait for Progressing
            wait_for_cluster_progressing(k8s=k8s_hub_client, name=co_name)

            # Wait for Ready with fail-fast on Failed
            wait_for_cluster_ready(k8s=k8s_hub_client, name=co_name)

            # Verify VIP discovery reaches ClusterOrder and is synchronized back
            # to the Fulfillment Cluster resource.
            co_status = k8s_hub_client.get_cluster_order_status(name=co_name)
            cluster_ref = co_status.get("clusterReference", {})
            assert cluster_ref.get("hostedClusterName"), "ClusterOrder should have a hostedClusterName when Ready"
            _assert_caas_worker_ips_in_subnet(
                grpc=grpc,
                cluster_order_status=co_status,
                subnet_cidr=caas_networking["subnet_cidr"],
                subnet_ref=caas_networking["subnet_cr"],
            )
            api_endpoint = co_status.get("apiEndpoint", "")
            ingress_endpoint = co_status.get("ingressEndpoint", "")
            assert api_endpoint, f"ClusterOrder {co_name} has no API VIP after reaching Ready"
            assert ingress_endpoint, f"ClusterOrder {co_name} has no ingress VIP after reaching Ready"

            cluster = grpc.get_cluster(cluster_id=uuid).get("object", {})
            cluster_status = cluster.get("status", {})
            assert cluster_status.get("apiEndpoint") == api_endpoint, (
                f"Fulfillment Cluster API endpoint {cluster_status.get('apiEndpoint')!r} "
                f"does not match ClusterOrder VIP {api_endpoint!r}"
            )
            assert cluster_status.get("ingressEndpoint") == ingress_endpoint, (
                f"Fulfillment Cluster ingress endpoint {cluster_status.get('ingressEndpoint')!r} "
                f"does not match ClusterOrder VIP {ingress_endpoint!r}"
            )

            # Delete — set the shared deadline NOW, when deletion is requested,
            # so the full 20-minute budget covers only the deletion phase.
            deletion_deadline = time.monotonic() + 1200
            cli.delete_cluster(uuid=uuid)
            deletion_requested = True
            wait_for_cluster_deleting(k8s=k8s_hub_client, name=co_name)
            wait_for_cluster_grpc_deleting_or_archived(grpc=grpc, uuid=uuid)
            wait_for_cluster_deletion_with_deadline(k8s=k8s_hub_client, name=co_name, deadline=deletion_deadline)
            wait_for_cluster_grpc_removal(grpc=grpc, uuid=uuid)
            print(f"Cluster {name} deleted successfully")
        except Exception:
            # Capture diagnostics before cleanup
            _report_agent_diagnostics(k8s=k8s_hub_client, co_name=co_name, context=f"test-failure for {name}")
            raise
        finally:
            # If deletion was never requested (e.g. provisioning failed),
            # start a fresh 20-minute deadline for cleanup.
            if deletion_deadline is None:
                deletion_deadline = time.monotonic() + 1200
            _cleanup_cluster(
                cli=cli,
                grpc=grpc,
                k8s=k8s_hub_client,
                uuid=uuid,
                co_name=co_name,
                deadline=deletion_deadline,
                deletion_requested=deletion_requested,
            )


@pytest.mark.requires_caas_fabric
@pytest.mark.serial
class TestCaasMultipleWorkerTypes:
    """Exercise distinct worker instance types through real CaaS provisioning."""

    @pytest.mark.xdist_group("caas-cluster-provision")
    def test_cluster_lifecycle_with_multiple_worker_types(
        self,
        cli: OsacCLI,
        grpc: GRPCClient,
        private_grpc: GRPCClient,
        k8s_hub_client: K8sClient,
        cluster_template: str,
        caas_disk_image_version: str,
        pull_secret_path: str,
        ssh_public_key_path: str,
        caas_networking: dict[str, str],
    ) -> None:
        """Install one worker of each type and verify type-specific pools and tenant networking."""
        # Cluster NodeSets require shared BMIT references. Create uniquely named
        # test profiles in that scope, then remove them after the ClusterOrder is gone.
        instance_types = {
            "compute": unique_name("e2e-caas-compute"),
            "accelerator": unique_name("e2e-caas-accelerator"),
        }
        instance_type_ids: list[str] = []
        cluster_uuid: str | None = None
        co_name: str | None = None
        deletion_deadline: float | None = None
        deletion_requested = False

        try:
            for node_set, cores, memory_gb in (("compute", 4, 16), ("accelerator", 8, 32)):
                instance_type_ids.append(
                    private_grpc.create_bare_metal_instance_type(
                        name=instance_types[node_set],
                        cores=cores,
                        memory_gb=memory_gb,
                        host_label_selector={"osac.openshift.io/host-type": "default"},
                        description="Temporary CaaS multi-type E2E profile",
                        tenant="shared",
                    )
                )

            node_sets = {
                node_set: {"size": 1, "baremetal_instance_type": {"name": instance_type}}
                for node_set, instance_type in instance_types.items()
            }
            cluster_name = unique_name("e2e-caas-multitype")
            cluster_uuid = cli.create_cluster(
                name=cluster_name,
                template=cluster_template,
                version=caas_disk_image_version,
                template_parameter_files={"pull_secret": pull_secret_path},
                template_parameters={"ssh_public_key": Path(ssh_public_key_path).read_text().strip()},
                node_sets=node_sets,
                network_attachment=(
                    f"subnet={caas_networking['subnet_name']},security-groups={caas_networking['sg_name']}"
                ),
            )
            print(f"Created multi-type CaaS cluster {cluster_name}: {cluster_uuid}")

            co_name = wait_for_cluster_order_cr(k8s=k8s_hub_client, uuid=cluster_uuid)
            wait_for_cluster_progressing(k8s=k8s_hub_client, name=co_name)

            co_spec = k8s_hub_client.get_cluster_order_spec(name=co_name)
            node_requests = co_spec.get("nodeRequests", [])
            actual_requests = {
                request.get("bareMetal", {}).get("instanceType", ""): int(request.get("numberOfNodes", 0))
                for request in node_requests
            }
            expected_requests = {instance_type: 1 for instance_type in instance_types.values()}
            assert actual_requests == expected_requests, (
                f"ClusterOrder node requests {actual_requests!r} do not preserve the two requested worker types"
            )
            assert len(node_requests) == 2
            assert all("resourceClass" not in request for request in node_requests)
            network_attachment = co_spec.get("networkAttachment", {})
            assert network_attachment.get("subnetRef") == caas_networking["subnet_cr"]
            assert network_attachment.get("securityGroupRefs") == [caas_networking["sg_cr"]]

            wait_for_cluster_ready(k8s=k8s_hub_client, name=co_name)
            co_status = k8s_hub_client.get_cluster_order_status(name=co_name)
            cluster_ref = co_status.get("clusterReference", {})
            hosted_cluster_name = cluster_ref.get("hostedClusterName", "")
            assert hosted_cluster_name, f"ClusterOrder {co_name} has no HostedCluster reference when Ready"

            hosted_cluster_namespace = k8s_hub_client.get_cluster_order_namespace(name=co_name)
            workload_kubeconfig = wait_for_hosted_cluster_kubeconfig(
                k8s=k8s_hub_client,
                hosted_cluster_namespace=hosted_cluster_namespace,
                hosted_cluster_name=hosted_cluster_name,
            )
            with tempfile.NamedTemporaryFile(prefix="osac-multitype-workload-", suffix=".kubeconfig") as kubeconfig:
                kubeconfig.write(workload_kubeconfig)
                kubeconfig.flush()
                workload_k8s = K8sClient(
                    namespace=hosted_cluster_namespace, kubeconfig=kubeconfig.name, as_system_admin=False
                )
                wait_for_workload_cluster_health(k8s=workload_k8s, expected_workers=2)

                def _node_pools_by_instance_type() -> dict[str, dict[str, object]]:
                    items = k8s_hub_client.list_json(
                        resource="nodepools.hypershift.openshift.io", namespace=hosted_cluster_namespace
                    ).get("items", [])
                    matching = [
                        item
                        for item in items
                        if item.get("metadata", {}).get("labels", {}).get("osac.openshift.io/clusterorder") == co_name
                    ]
                    return {
                        item.get("metadata", {}).get("labels", {}).get("osac.openshift.io/instance_type", ""): item
                        for item in matching
                    }

                node_pools = poll_until(
                    fn=_node_pools_by_instance_type,
                    until=lambda pools: (
                        set(pools) == set(instance_types.values())
                        and all(node_pool_ready(pool, expected_ready_nodes=1) for pool in pools.values())
                    ),
                    retries=60,
                    delay=10,
                    description=f"{co_name} per-type NodePools to have one ready node each",
                )

            assert len(node_pools) == 2
            for instance_type in instance_types.values():
                node_pool = node_pools[instance_type]
                labels = node_pool.get("metadata", {}).get("labels", {})
                assert labels.get("osac.openshift.io/instance_type") == instance_type
                assert labels.get("osac.openshift.io/clusterorder") == co_name
                assert node_pool.get("spec", {}).get("replicas") == 1
                selector = (
                    node_pool.get("spec", {})
                    .get("platform", {})
                    .get("agent", {})
                    .get("agentLabelSelector", {})
                    .get("matchLabels", {})
                )
                assert selector.get("osac.openshift.io/instance_type") == instance_type
                assert selector.get("osac.openshift.io/clusterorder") == co_name

            cluster_order = k8s_hub_client.get_json(resource="clusterorder", name=co_name)
            agent_namespace = cluster_order.get("metadata", {}).get("namespace", k8s_hub_client.namespace)

            def _bound_agent_types() -> Counter[str]:
                agents = k8s_hub_client.list_json(
                    resource="agents.agent-install.openshift.io", namespace=agent_namespace
                ).get("items", [])
                return Counter(
                    labels.get("osac.openshift.io/instance_type", "")
                    for agent in agents
                    if (labels := agent.get("metadata", {}).get("labels", {})).get("osac.openshift.io/clusterorder")
                    == co_name
                )

            agent_types = poll_until(
                fn=_bound_agent_types,
                until=lambda counts: counts == Counter(instance_types.values()),
                retries=60,
                delay=10,
                description=f"{co_name} Agents to be labeled for both instance types",
            )
            assert agent_types == Counter(instance_types.values())

            cluster = grpc.get_cluster(cluster_id=cluster_uuid).get("object", {})
            cluster_tenant = cluster.get("metadata", {}).get("tenant", "")
            workers = [worker for worker in co_status.get("workers", []) if worker.get("kind") == "BareMetalInstance"]
            assert len(workers) == 2, f"Expected two CaaS worker BMIs, got {len(workers)}"
            observed_instance_types: Counter[str] = Counter()
            for worker in workers:
                assert worker.get("phase") == "Ready", f"Worker {worker.get('name')} is not Ready"
                bmi_id = worker.get("resourceID", "")
                assert bmi_id, f"Worker {worker.get('name')} has no BareMetalInstance resource ID"
                bmi = grpc.get_baremetal_instance(bmi_id=bmi_id).get("object", {})
                metadata = bmi.get("metadata", {})
                assert metadata.get("tenant") == cluster_tenant
                assert metadata.get("labels", {}).get("osac.openshift.io/cluster-order") == co_name
                assert metadata.get("annotations", {}).get("osac.openshift.io/owner-reference") == (
                    f"ClusterOrder/{co_name}"
                )
                spec = bmi.get("spec", {})
                type_ref = spec.get("instanceType", spec.get("instance_type", {}))
                assert type_ref.get("shared") is True
                observed_instance_types[type_ref.get("name", "")] += 1

            assert observed_instance_types == Counter(instance_types.values())
            _assert_caas_worker_ips_in_subnet(
                grpc=grpc,
                cluster_order_status=co_status,
                subnet_cidr=caas_networking["subnet_cidr"],
                subnet_ref=caas_networking["subnet_cr"],
            )

            deletion_deadline = time.monotonic() + 1200
            cli.delete_cluster(uuid=cluster_uuid)
            deletion_requested = True
            wait_for_cluster_deleting(k8s=k8s_hub_client, name=co_name)
            wait_for_cluster_grpc_deleting_or_archived(grpc=grpc, uuid=cluster_uuid)
            wait_for_cluster_deletion_with_deadline(k8s=k8s_hub_client, name=co_name, deadline=deletion_deadline)
            wait_for_cluster_grpc_removal(grpc=grpc, uuid=cluster_uuid)
        except Exception:
            _report_agent_diagnostics(k8s=k8s_hub_client, co_name=co_name, context="multi-type test failure")
            raise
        finally:
            if cluster_uuid is not None:
                if co_name is None:
                    with contextlib.suppress(Exception):
                        co_name = wait_for_cluster_order_cr(k8s=k8s_hub_client, uuid=cluster_uuid)
                if deletion_deadline is None:
                    deletion_deadline = time.monotonic() + 1200
                _cleanup_cluster(
                    cli=cli,
                    grpc=grpc,
                    k8s=k8s_hub_client,
                    uuid=cluster_uuid,
                    co_name=co_name,
                    deadline=deletion_deadline,
                    deletion_requested=deletion_requested,
                )
            for instance_type_id in reversed(instance_type_ids):
                try:
                    private_grpc.call(
                        service=f"{PRIVATE_API}.BareMetalInstanceTypes/Delete", data={"id": instance_type_id}
                    )
                except subprocess.CalledProcessError as cleanup_error:
                    log.warning(
                        "Failed to delete temporary shared BMIT %s after CaaS cleanup: %s",
                        instance_type_id,
                        (cleanup_error.stderr or cleanup_error.stdout or str(cleanup_error)).strip(),
                    )


# ---------------------------------------------------------------------------
# Automatic ExternalIP lifecycle for a CaaS Cluster
# ---------------------------------------------------------------------------
@pytest.mark.requires_caas_fabric
@pytest.mark.serial
class TestCaasClusterAutoExternalIP:
    """Verify auto-provisioned API and ingress addresses follow Cluster lifecycle."""

    @pytest.mark.xdist_group("caas-cluster-provision")
    def test_auto_external_ip_attachment_and_cleanup(
        self,
        cli: OsacCLI,
        grpc: GRPCClient,
        k8s_hub_client: K8sClient,
        cluster_template: str,
        caas_disk_image_version: str,
        pull_secret_path: str,
        ssh_public_key_path: str,
        caas_worker_node_sets: dict[str, dict[str, object]],
        caas_networking: dict[str, str],
        caas_external_ip_pool: dict[str, str],
    ) -> None:
        """Create and use a test-owned Ready pool for the auto-attachment flow."""
        name = unique_name("e2e-caas-auto-eip")
        uuid = cli.create_cluster(
            name=name,
            template=cluster_template,
            version=caas_disk_image_version,
            template_parameter_files={"pull_secret": pull_secret_path},
            template_parameters={"ssh_public_key": Path(ssh_public_key_path).read_text().strip()},
            node_sets=caas_worker_node_sets,
            external_ip_attachment=True,
        )
        print(f"Created auto-ExternalIP cluster {name}: {uuid}")

        deletion_deadline: float | None = None
        deletion_requested = False
        co_name: str | None = None
        attachment_crs: list[str] = []
        external_ip_crs: list[str] = []
        try:
            co_name = wait_for_cluster_order_cr(k8s=k8s_hub_client, uuid=uuid)
            wait_for_cluster_progressing(k8s=k8s_hub_client, name=co_name)
            wait_for_cluster_ready(k8s=k8s_hub_client, name=co_name)

            co_spec = k8s_hub_client.get_cluster_order_spec(name=co_name)
            network_attachment = co_spec.get("networkAttachment", {})
            assert network_attachment.get("subnetRef") == caas_networking["subnet_cr"]
            assert network_attachment.get("securityGroupRefs") == [caas_networking["sg_cr"]]

            def cluster_attachments() -> list[dict[str, object]]:
                items = grpc.call(service="osac.public.v1.ExternalIPAttachments/List").get("items", [])
                return [item for item in items if item.get("spec", {}).get("cluster", {}).get("id") == uuid]

            attachments = poll_until(
                fn=cluster_attachments,
                until=lambda items: len(items) == 2,
                retries=60,
                delay=5,
                description=f"two auto-provisioned ExternalIPAttachments for Cluster {uuid}",
            )
            endpoint_names = {item.get("spec", {}).get("targetEndpoint") for item in attachments}
            assert endpoint_names == {"EXTERNAL_IP_ATTACHMENT_ENDPOINT_API", "EXTERNAL_IP_ATTACHMENT_ENDPOINT_INGRESS"}

            external_ip_ids = {item.get("spec", {}).get("externalIp", {}).get("id") for item in attachments}
            assert len(external_ip_ids) == 2 and "" not in external_ip_ids
            external_ips: list[dict[str, object]] = []
            for external_ip_id in external_ip_ids:
                attachment_id = next(
                    item["id"]
                    for item in attachments
                    if item.get("spec", {}).get("externalIp", {}).get("id") == external_ip_id
                )
                attachment_cr = wait_for_external_ip_attachment_cr(k8s=k8s_hub_client, uuid=attachment_id)
                attachment_crs.append(attachment_cr)
                wait_for_external_ip_attachment_ready(k8s=k8s_hub_client, name=attachment_cr)

                external_ip = grpc.get_external_ip(external_ip_id=external_ip_id).get("object", {})
                labels = external_ip.get("metadata", {}).get("labels", {})
                assert labels.get("osac.openshift.io/auto-created") == "true"
                assert labels.get("osac.openshift.io/auto-created-for") == uuid
                assert external_ip.get("spec", {}).get("pool", {}).get("id") == caas_external_ip_pool["id"], (
                    f"Auto-created ExternalIP {external_ip_id} came from pool "
                    f"{external_ip.get('spec', {}).get('pool', {}).get('id')!r}; "
                    f"expected the test pool {caas_external_ip_pool['id']!r}"
                )
                external_ip_cr = wait_for_external_ip_cr(k8s=k8s_hub_client, uuid=external_ip_id)
                external_ip_crs.append(external_ip_cr)
                wait_for_external_ip_allocated(k8s=k8s_hub_client, name=external_ip_cr)
                external_ip = poll_until(
                    fn=lambda external_ip_id=external_ip_id: grpc.get_external_ip(external_ip_id=external_ip_id).get(
                        "object", {}
                    ),
                    until=lambda item: bool(item.get("status", {}).get("address")),
                    retries=30,
                    delay=2,
                    description=f"allocated address feedback for ExternalIP {external_ip_id}",
                )
                external_ips.append(external_ip)
            assert all(item.get("status", {}).get("address") for item in external_ips)

            # The delete API removes DB records, and the ClusterOrder controller
            # removes their Kubernetes CRs in attachment-then-IP order.
            deletion_deadline = time.monotonic() + 1200
            cli.delete_cluster(uuid=uuid)
            deletion_requested = True
            wait_for_cluster_deleting(k8s=k8s_hub_client, name=co_name)
            wait_for_cluster_grpc_deleting_or_archived(grpc=grpc, uuid=uuid)
            wait_for_cluster_deletion_with_deadline(k8s=k8s_hub_client, name=co_name, deadline=deletion_deadline)
            wait_for_cluster_grpc_removal(grpc=grpc, uuid=uuid)

            poll_until(
                fn=lambda: not cluster_attachments(),
                until=lambda removed: removed,
                retries=60,
                delay=5,
                description=f"auto-provisioned ExternalIPAttachments for Cluster {uuid} to be deleted",
            )
            poll_until(
                fn=lambda: all(external_ip_id not in grpc.list_external_ip_ids() for external_ip_id in external_ip_ids),
                until=lambda removed: removed,
                retries=60,
                delay=5,
                description=f"auto-provisioned ExternalIPs for Cluster {uuid} to be deleted",
            )
            for attachment_cr in attachment_crs:
                wait_for_external_ip_attachment_deletion(k8s=k8s_hub_client, name=attachment_cr)
            for external_ip_cr in external_ip_crs:
                wait_for_external_ip_deletion(k8s=k8s_hub_client, name=external_ip_cr)
        except Exception:
            _report_agent_diagnostics(k8s=k8s_hub_client, co_name=co_name, context=f"test-failure for {name}")
            raise
        finally:
            if deletion_deadline is None:
                deletion_deadline = time.monotonic() + 1200
            _cleanup_cluster(
                cli=cli,
                grpc=grpc,
                k8s=k8s_hub_client,
                uuid=uuid,
                co_name=co_name,
                deadline=deletion_deadline,
                deletion_requested=deletion_requested,
            )


# ---------------------------------------------------------------------------
# Negative validation tests
# ---------------------------------------------------------------------------
class TestCaasNetworkAttachmentNegative:
    """Negative tests for --network-attachment flag validation."""

    def test_reject_nonexistent_subnet(
        self,
        cli: OsacCLI,
        cluster_template: str,
        caas_disk_image_version: str,
        pull_secret_path: str,
        ssh_public_key_path: str,
        caas_worker_node_sets: dict[str, dict[str, object]],
    ) -> None:
        """Creating a cluster with a nonexistent subnet should fail."""
        name = unique_name("e2e-caas-nosub")
        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            cli.create_cluster(
                name=name,
                template=cluster_template,
                version=caas_disk_image_version,
                template_parameter_files={"pull_secret": pull_secret_path},
                template_parameters={"ssh_public_key": Path(ssh_public_key_path).read_text().strip()},
                node_sets=caas_worker_node_sets,
                network_attachment="subnet=nonexistent-subnet-12345",
            )
        combined = (exc_info.value.stdout or "") + (exc_info.value.stderr or "")
        assert "nonexistent-subnet-12345" in combined.lower()
        assert "does not exist" in combined.lower() or "not found" in combined.lower()

    def test_reject_sg_from_wrong_vn(
        self,
        cli: OsacCLI,
        grpc: GRPCClient,
        k8s_hub_client: K8sClient,
        cluster_template: str,
        caas_disk_image_version: str,
        pull_secret_path: str,
        ssh_public_key_path: str,
        caas_worker_node_sets: dict[str, dict[str, object]],
        caas_networking: dict[str, str],
    ) -> None:
        """A SecurityGroup from a different VN should be rejected by the API.

        Creates a second VirtualNetwork with its own SecurityGroup, then
        attempts to create a cluster using the subnet from the first VN
        combined with the SG from the second VN.  The API should reject
        the cross-VN mismatch.
        """
        subnet_name = caas_networking["subnet_name"]

        # Create a second VN with its own Subnet + SG in a different network
        second_vn_name = unique_name("e2e-wrongvn")
        second_subnet_name = unique_name("e2e-wrongsub")
        second_sg_name = unique_name("e2e-wrongsg")
        second_vn_id: str | None = None
        second_vn_cr: str | None = None
        second_subnet_id: str | None = None
        second_subnet_cr: str | None = None
        second_sg_id: str | None = None
        second_sg_cr: str | None = None

        try:
            # Create second VirtualNetwork
            second_vn_id = grpc.create_virtual_network(name=second_vn_name, ipv4_cidr="10.220.0.0/16")
            second_vn_cr = wait_for_virtual_network_cr(k8s=k8s_hub_client, uuid=second_vn_id)
            wait_for_virtual_network_ready(k8s=k8s_hub_client, name=second_vn_cr)

            # Create Subnet in the second VN (required for the SG to reach Ready)
            second_subnet_id = grpc.create_subnet(
                name=second_subnet_name, virtual_network=second_vn_id, ipv4_cidr="10.220.0.0/24"
            )
            second_subnet_cr = wait_for_subnet_cr(k8s=k8s_hub_client, uuid=second_subnet_id)
            wait_for_subnet_ready(k8s=k8s_hub_client, name=second_subnet_cr)

            # Create SG in the second VN
            second_sg_id = grpc.create_security_group(name=second_sg_name, virtual_network=second_vn_id)
            second_sg_cr = wait_for_security_group_cr(k8s=k8s_hub_client, uuid=second_sg_id)
            wait_for_security_group_ready(k8s=k8s_hub_client, name=second_sg_cr)

            # Attempt to create a cluster with subnet from VN1 + SG from VN2
            cluster_name = unique_name("e2e-caas-wrongsg")
            with pytest.raises(subprocess.CalledProcessError) as exc_info:
                cli.create_cluster(
                    name=cluster_name,
                    template=cluster_template,
                    version=caas_disk_image_version,
                    template_parameter_files={"pull_secret": pull_secret_path},
                    template_parameters={"ssh_public_key": Path(ssh_public_key_path).read_text().strip()},
                    node_sets=caas_worker_node_sets,
                    network_attachment=f"subnet={subnet_name},security-groups={second_sg_name}",
                )
            combined = (exc_info.value.stdout or "") + (exc_info.value.stderr or "")
            assert "different virtual network" in combined.lower()
        finally:
            # Clean up second VN resources in reverse order: SG -> Subnet -> VN
            if second_sg_id and second_sg_cr:
                try:
                    grpc.delete_security_group(sg_id=second_sg_id)
                    wait_for_security_group_deletion(k8s=k8s_hub_client, name=second_sg_cr)
                except Exception as e:
                    print(f"WARNING: Failed to delete second SG {second_sg_id}: {e}")
            if second_subnet_id and second_subnet_cr:
                try:
                    grpc.delete_subnet(subnet_id=second_subnet_id)
                    wait_for_subnet_deletion(k8s=k8s_hub_client, name=second_subnet_cr)
                except Exception as e:
                    print(f"WARNING: Failed to delete second Subnet {second_subnet_id}: {e}")
            if second_vn_id and second_vn_cr:
                try:
                    grpc.delete_virtual_network(vn_id=second_vn_id)
                    wait_for_virtual_network_deletion(k8s=k8s_hub_client, name=second_vn_cr)
                except Exception as e:
                    print(f"WARNING: Failed to delete second VN {second_vn_id}: {e}")


# ---------------------------------------------------------------------------
# Default networking: cluster without explicit --network-attachment
# ---------------------------------------------------------------------------
@pytest.mark.requires_caas_fabric
@pytest.mark.serial
class TestCaasDefaultNetworking:
    """Cluster creation without explicit --network-attachment, relying on tenant defaults."""

    @pytest.mark.xdist_group("caas-cluster-provision")
    def test_cluster_default_networking(
        self,
        cli: OsacCLI,
        grpc: GRPCClient,
        k8s_hub_client: K8sClient,
        cluster_template: str,
        caas_disk_image_version: str,
        pull_secret_path: str,
        ssh_public_key_path: str,
        caas_worker_node_sets: dict[str, dict[str, object]],
        caas_networking: dict[str, str],
    ) -> None:
        """Create a cluster without --network-attachment using JWT auth.

        Requires ready tenant networking (provided by ``caas_networking``).
        Verifies that the platform populates the ClusterOrder's
        ``networkAttachment.subnetRef`` from the tenant's available
        networking rather than leaving it empty.
        """
        name = unique_name("e2e-caas-dflt")

        uuid = cli.create_cluster(
            name=name,
            template=cluster_template,
            version=caas_disk_image_version,
            template_parameter_files={"pull_secret": pull_secret_path},
            template_parameters={"ssh_public_key": Path(ssh_public_key_path).read_text().strip()},
            node_sets=caas_worker_node_sets,
        )
        print(f"Created default-networking cluster {name}: {uuid}")

        # Deletion deadline is set WHEN deletion is requested (after Ready/
        # Failed), not at test start — see test_cluster_lifecycle_with_network_attachment.
        deletion_deadline: float | None = None
        deletion_requested = False

        co_name: str | None = None
        try:
            co_name = wait_for_cluster_order_cr(k8s=k8s_hub_client, uuid=uuid)
            assert uuid in grpc.list_cluster_ids()

            wait_for_cluster_progressing(k8s=k8s_hub_client, name=co_name)

            # Wait for Ready with fail-fast on Failed
            wait_for_cluster_ready(k8s=k8s_hub_client, name=co_name)

            # Verify the platform populated network attachment references
            co_spec = k8s_hub_client.get_cluster_order_spec(name=co_name)
            co_na = co_spec.get("networkAttachment", {})
            co_subnet_ref = co_na.get("subnetRef", "")
            assert co_subnet_ref, (
                f"ClusterOrder {co_name} has no networkAttachment.subnetRef; "
                "the platform should populate default networking references "
                "when a ready tenant network exists"
            )
            assert co_na.get("securityGroupRefs"), (
                f"ClusterOrder {co_name} has no networkAttachment.securityGroupRefs; "
                "the tenant default security group should be applied with its default subnet"
            )
            # Verify the referenced Subnet CR exists and has a phase
            subnet_phase = k8s_hub_client.get_subnet_phase(name=co_subnet_ref, checked=False)
            assert subnet_phase == "Ready", (
                f"Subnet CR {co_subnet_ref!r} referenced by ClusterOrder {co_name} "
                f"has phase {subnet_phase!r}, expected Ready"
            )

            # Verify cluster status has cluster reference
            co_status = k8s_hub_client.get_cluster_order_status(name=co_name)
            cluster_ref = co_status.get("clusterReference", {})
            assert cluster_ref.get("hostedClusterName"), "ClusterOrder should have a hostedClusterName when Ready"

            # Delete — set the shared deadline NOW, when deletion is requested.
            deletion_deadline = time.monotonic() + 1200
            cli.delete_cluster(uuid=uuid)
            deletion_requested = True
            wait_for_cluster_deleting(k8s=k8s_hub_client, name=co_name)
            wait_for_cluster_grpc_deleting_or_archived(grpc=grpc, uuid=uuid)
            wait_for_cluster_deletion_with_deadline(k8s=k8s_hub_client, name=co_name, deadline=deletion_deadline)
            wait_for_cluster_grpc_removal(grpc=grpc, uuid=uuid)
            print(f"Default-networking cluster {name} deleted successfully")
        except Exception:
            _report_agent_diagnostics(k8s=k8s_hub_client, co_name=co_name, context=f"test-failure for {name}")
            raise
        finally:
            # If deletion was never requested, start a fresh deadline for cleanup.
            if deletion_deadline is None:
                deletion_deadline = time.monotonic() + 1200
            _cleanup_cluster(
                cli=cli,
                grpc=grpc,
                k8s=k8s_hub_client,
                uuid=uuid,
                co_name=co_name,
                deadline=deletion_deadline,
                deletion_requested=deletion_requested,
            )


# ---------------------------------------------------------------------------
# Sequential CaaS provisioning: create → delete → create again
# ---------------------------------------------------------------------------
@pytest.mark.requires_caas_fabric
@pytest.mark.serial
class TestCaasSequentialProvisioning:
    """Verify a new CaaS cluster can be provisioned after deleting the previous one.

    Journey:
    1. Create a cluster and wait for it to become Ready.
    2. Delete it and wait for all ClusterOrder resources to be removed.
    3. Create a second cluster and wait for it to become Ready.

    Agent resources are scoped to a ClusterOrder and deleted after unbinding,
    so this test covers sequential provisioning rather than global Agent reuse.
    """

    @pytest.mark.xdist_group("caas-cluster-provision")
    def test_cluster_can_be_provisioned_after_deletion(
        self,
        cli: OsacCLI,
        grpc: GRPCClient,
        k8s_hub_client: K8sClient,
        cluster_template: str,
        caas_disk_image_version: str,
        pull_secret_path: str,
        ssh_public_key_path: str,
        caas_worker_node_sets: dict[str, dict[str, object]],
        caas_networking: dict[str, str],
    ) -> None:
        subnet_name = caas_networking["subnet_name"]
        sg_name = caas_networking["sg_name"]
        network_attachment = f"subnet={subnet_name},security-groups={sg_name}"
        ssh_key = Path(ssh_public_key_path).read_text().strip()

        # ── Phase 1: create first cluster ──
        name_a = unique_name("e2e-sequential-a")
        uuid_a = cli.create_cluster(
            name=name_a,
            template=cluster_template,
            version=caas_disk_image_version,
            template_parameter_files={"pull_secret": pull_secret_path},
            template_parameters={"ssh_public_key": ssh_key},
            node_sets=caas_worker_node_sets,
            network_attachment=network_attachment,
        )
        print(f"Created first cluster {name_a}: {uuid_a}")

        deletion_deadline_a: float | None = None
        deletion_requested_a = False
        co_name_a: str | None = None

        try:
            co_name_a = wait_for_cluster_order_cr(k8s=k8s_hub_client, uuid=uuid_a)
            wait_for_cluster_progressing(k8s=k8s_hub_client, name=co_name_a)
            wait_for_cluster_ready(k8s=k8s_hub_client, name=co_name_a)
            print(f"First cluster {name_a} is Ready")

            # ── Phase 2: delete first cluster ──
            deletion_deadline_a = time.monotonic() + 1200
            cli.delete_cluster(uuid=uuid_a)
            deletion_requested_a = True
            wait_for_cluster_deleting(k8s=k8s_hub_client, name=co_name_a)
            wait_for_cluster_grpc_deleting_or_archived(grpc=grpc, uuid=uuid_a)

            # Wait for deletion before issuing the next ClusterOrder. The
            # operator deletes the old order-scoped Agent after unbinding it.
            wait_for_cluster_deletion_with_deadline(k8s=k8s_hub_client, name=co_name_a, deadline=deletion_deadline_a)
            wait_for_cluster_grpc_removal(grpc=grpc, uuid=uuid_a)
            print(f"First cluster {name_a} deleted")

        except Exception:
            _report_agent_diagnostics(k8s=k8s_hub_client, co_name=co_name_a, context=f"phase1-failure for {name_a}")
            raise
        finally:
            if deletion_deadline_a is None:
                deletion_deadline_a = time.monotonic() + 1200
            _cleanup_cluster(
                cli=cli,
                grpc=grpc,
                k8s=k8s_hub_client,
                uuid=uuid_a,
                co_name=co_name_a,
                deadline=deletion_deadline_a,
                deletion_requested=deletion_requested_a,
            )

        # ── Phase 4: create second cluster ──
        name_b = unique_name("e2e-sequential-b")
        uuid_b = cli.create_cluster(
            name=name_b,
            template=cluster_template,
            version=caas_disk_image_version,
            template_parameter_files={"pull_secret": pull_secret_path},
            template_parameters={"ssh_public_key": ssh_key},
            node_sets=caas_worker_node_sets,
            network_attachment=network_attachment,
        )
        print(f"Created second cluster {name_b}: {uuid_b}")

        deletion_deadline_b: float | None = None
        deletion_requested_b = False
        co_name_b: str | None = None

        try:
            co_name_b = wait_for_cluster_order_cr(k8s=k8s_hub_client, uuid=uuid_b)
            wait_for_cluster_progressing(k8s=k8s_hub_client, name=co_name_b)

            wait_for_cluster_ready(k8s=k8s_hub_client, name=co_name_b)
            print(f"Second cluster {name_b} is Ready")

            # Delete second cluster
            deletion_deadline_b = time.monotonic() + 1200
            cli.delete_cluster(uuid=uuid_b)
            deletion_requested_b = True
            wait_for_cluster_deleting(k8s=k8s_hub_client, name=co_name_b)
            wait_for_cluster_grpc_deleting_or_archived(grpc=grpc, uuid=uuid_b)
            wait_for_cluster_deletion_with_deadline(k8s=k8s_hub_client, name=co_name_b, deadline=deletion_deadline_b)
            wait_for_cluster_grpc_removal(grpc=grpc, uuid=uuid_b)
            print(f"Second cluster {name_b} deleted successfully")

        except Exception:
            _report_agent_diagnostics(k8s=k8s_hub_client, co_name=co_name_b, context=f"phase2-failure for {name_b}")
            raise
        finally:
            if deletion_deadline_b is None:
                deletion_deadline_b = time.monotonic() + 1200
            _cleanup_cluster(
                cli=cli,
                grpc=grpc,
                k8s=k8s_hub_client,
                uuid=uuid_b,
                co_name=co_name_b,
                deadline=deletion_deadline_b,
                deletion_requested=deletion_requested_b,
            )
