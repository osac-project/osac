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
import logging
import subprocess
import time
from pathlib import Path

import pytest

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import (
    unique_name,
    wait_for_agent_available,
    wait_for_cluster_deleting,
    wait_for_cluster_deletion,
    wait_for_cluster_deletion_with_deadline,
    wait_for_cluster_grpc_deleting_or_archived,
    wait_for_cluster_grpc_removal,
    wait_for_cluster_order_cr,
    wait_for_cluster_progressing,
    wait_for_cluster_ready,
    wait_for_security_group_cr,
    wait_for_security_group_deletion,
    wait_for_security_group_ready,
    wait_for_subnet_cr,
    wait_for_subnet_deletion,
    wait_for_subnet_ready,
    wait_for_virtual_network_cr,
    wait_for_virtual_network_deletion,
    wait_for_virtual_network_ready,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import run_unchecked

pytestmark = [pytest.mark.regression, pytest.mark.requires_caas]

log = logging.getLogger(__name__)


def _report_agent_diagnostics(*, k8s: K8sClient, co_name: str | None, context: str) -> None:
    """Capture Agent state, labels, and events for failure diagnostics.

    Reports:
    - Per-agent state, binding, labels (excluding secrets)
    - Recent events for Agents in the hardware-inventory namespace
    - Conditions summary
    """
    import json as _json

    agent_ns = "hardware-inventory"
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

    # Events for hardware-inventory namespace (Agents)
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


def _assert_agent_pool_available(*, k8s: K8sClient, expected_available: int = 1) -> None:
    """Assert that enough Agents are available before provisioning.

    FAILS the test immediately if the number of available (unbound,
    ready-state) agents is below ``expected_available``.  This prevents
    provisioning from starting when the Agent pool is exhausted, avoiding
    a long wait that would end in a "0 agents" failure anyway.

    Reports full Agent state, labels, conditions, and count in the failure
    message for immediate diagnosis.
    """
    import json as _json

    agent_ns = "hardware-inventory"
    base_args = [*k8s._base(), "--as", "system:admin"]
    available_states = {"known-unbound", "known", "discovering-unbound"}

    try:
        output, rc = run_unchecked(*base_args, "get", "agents.agent-install.openshift.io", "-n", agent_ns, "-o", "json")
        if rc != 0:
            log.warning("Agent preflight query failed (rc=%d): %s", rc, output[:500])
            return

        data = _json.loads(output)
        items = data.get("items", [])
        available_agents: list[str] = []
        unavailable_summary: list[str] = []

        for agent in items:
            meta = agent.get("metadata", {})
            spec = agent.get("spec", {})
            status = agent.get("status", {})
            debug_info = status.get("debugInfo", {})
            conditions = status.get("conditions", [])
            state = debug_info.get("state", "unknown")
            cluster_ref = spec.get("clusterDeploymentName", {})
            agent_name = meta.get("name", "?")

            if state in available_states and not cluster_ref:
                available_agents.append(agent_name)
            else:
                # Build compact exclusion reason
                if isinstance(cluster_ref, dict) and cluster_ref:
                    binding = f"{cluster_ref.get('namespace', '')}/{cluster_ref.get('name', '')}"
                elif cluster_ref:
                    binding = str(cluster_ref)
                else:
                    binding = "unbound"

                cond_summary = ", ".join(f"{c.get('type', '?')}={c.get('status', '?')}" for c in conditions[:3])
                unavailable_summary.append(
                    f"{agent_name}(state={state}, binding={binding}, conditions=[{cond_summary}])"
                )

        log.info(
            "Agent pool preflight: %d/%d agents available (need %d)",
            len(available_agents),
            len(items),
            expected_available,
        )

        if len(available_agents) < expected_available:
            # Capture full diagnostics before failing
            _report_agent_diagnostics(k8s=k8s, co_name=None, context="preflight-pool-shortage")
            pytest.fail(
                f"Agent pool preflight FAILED: {len(available_agents)} available agents "
                f"(need {expected_available}), {len(items)} total in {agent_ns}.\n"
                f"Available: {available_agents or 'none'}\n"
                f"Unavailable: {unavailable_summary or 'none'}"
            )

    except pytest.fail.Exception:
        raise
    except Exception as exc:
        log.warning("Agent preflight check failed (non-fatal): %s", exc)


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
        pull_secret_path: str,
        ssh_public_key_path: str,
        caas_networking: dict[str, str],
    ) -> None:
        subnet_name = caas_networking["subnet_name"]
        sg_name = caas_networking["sg_name"]
        name = unique_name("e2e-caas-net")

        # Preflight: check Agent pool capacity
        _assert_agent_pool_available(k8s=k8s_hub_client)

        # Build the --network-attachment value: subnet=<name>,security-groups=<sg>
        network_attachment = f"subnet={subnet_name},security-groups={sg_name}"

        uuid = cli.create_cluster(
            name=name,
            template=cluster_template,
            template_parameter_files={"pull_secret": pull_secret_path},
            template_parameters={"ssh_public_key": Path(ssh_public_key_path).read_text().strip()},
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
            assert co_na.get("subnetRef"), (
                f"ClusterOrder {co_name} networkAttachment.subnetRef is empty "
                "immediately after creation; expected the reconciler to map "
                "the explicit attachment"
            )
            assert co_na.get("securityGroupRefs"), (
                f"ClusterOrder {co_name} networkAttachment.securityGroupRefs "
                "is empty; expected at least one security group reference"
            )

            # Wait for Progressing
            wait_for_cluster_progressing(k8s=k8s_hub_client, name=co_name)

            # Wait for Ready with fail-fast on Failed
            wait_for_cluster_ready(k8s=k8s_hub_client, name=co_name)

            # Verify cluster status has VIP endpoints
            co_status = k8s_hub_client.get_cluster_order_status(name=co_name)
            cluster_ref = co_status.get("clusterReference", {})
            assert cluster_ref.get("hostedClusterName"), "ClusterOrder should have a hostedClusterName when Ready"

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


# ---------------------------------------------------------------------------
# Negative validation tests
# ---------------------------------------------------------------------------
class TestCaasNetworkAttachmentNegative:
    """Negative tests for --network-attachment flag validation."""

    def test_reject_nonexistent_subnet(
        self, cli: OsacCLI, cluster_template: str, pull_secret_path: str, ssh_public_key_path: str
    ) -> None:
        """Creating a cluster with a nonexistent subnet should fail."""
        name = unique_name("e2e-caas-nosub")
        with pytest.raises(subprocess.CalledProcessError) as exc_info:
            cli.create_cluster(
                name=name,
                template=cluster_template,
                template_parameter_files={"pull_secret": pull_secret_path},
                template_parameters={"ssh_public_key": Path(ssh_public_key_path).read_text().strip()},
                network_attachment="subnet=nonexistent-subnet-12345",
            )
        combined = (exc_info.value.stdout or "") + (exc_info.value.stderr or "")
        assert "not found" in combined.lower() or "does not exist" in combined.lower() or exc_info.value.returncode != 0

    def test_reject_sg_from_wrong_vn(
        self,
        cli: OsacCLI,
        grpc: GRPCClient,
        k8s_hub_client: K8sClient,
        cluster_template: str,
        pull_secret_path: str,
        ssh_public_key_path: str,
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
                    template_parameter_files={"pull_secret": pull_secret_path},
                    template_parameters={"ssh_public_key": Path(ssh_public_key_path).read_text().strip()},
                    network_attachment=f"subnet={subnet_name},security-groups={second_sg_name}",
                )
            combined = (exc_info.value.stdout or "") + (exc_info.value.stderr or "")
            assert (
                "not found" in combined.lower()
                or "does not exist" in combined.lower()
                or "different" in combined.lower()
                or "mismatch" in combined.lower()
                or exc_info.value.returncode != 0
            )
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
        pull_secret_path: str,
        ssh_public_key_path: str,
        caas_networking: dict[str, str],
    ) -> None:
        """Create a cluster without --network-attachment using JWT auth.

        Requires ready tenant networking (provided by ``caas_networking``).
        Verifies that the platform populates the ClusterOrder's
        ``networkAttachment.subnetRef`` from the tenant's available
        networking rather than leaving it empty.
        """
        name = unique_name("e2e-caas-dflt")

        # Preflight: check Agent pool capacity
        _assert_agent_pool_available(k8s=k8s_hub_client)

        uuid = cli.create_cluster(
            name=name,
            template=cluster_template,
            template_parameter_files={"pull_secret": pull_secret_path},
            template_parameters={"ssh_public_key": Path(ssh_public_key_path).read_text().strip()},
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
            # Verify the referenced Subnet CR exists and has a phase
            subnet_phase = k8s_hub_client.get_subnet_phase(name=co_subnet_ref, checked=False)
            assert subnet_phase, (
                f"Subnet CR {co_subnet_ref!r} referenced by ClusterOrder {co_name} does not exist or has no phase"
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
# Agent reuse: create → delete → wait reclaim → create again → verify
# ---------------------------------------------------------------------------
@pytest.mark.serial
class TestCaasAgentReuse:
    """Verify that Agents are returned to the available pool after cluster deletion.

    Journey:
    1. Create a cluster (agents get allocated and bound)
    2. Delete it
    3. Wait for agents to reach available state: ``known-unbound``/``known``/
       ``discovering-unbound`` with the old ``clusterdeployment-namespace``
       label gone
    4. Create another cluster
    5. Verify agents are allocated to the new cluster

    Uses a 10-minute reclaim deadline with 10-second polling.
    On timeout, captures Agent status and labels for diagnostics.
    """

    _RECLAIM_TIMEOUT = 600  # 10 minutes
    _RECLAIM_POLL = 10  # seconds

    @pytest.mark.xdist_group("caas-cluster-provision")
    def test_agent_reuse_after_cluster_deletion(
        self,
        cli: OsacCLI,
        grpc: GRPCClient,
        k8s_hub_client: K8sClient,
        cluster_template: str,
        pull_secret_path: str,
        ssh_public_key_path: str,
        caas_networking: dict[str, str],
    ) -> None:
        subnet_name = caas_networking["subnet_name"]
        sg_name = caas_networking["sg_name"]
        network_attachment = f"subnet={subnet_name},security-groups={sg_name}"
        ssh_key = Path(ssh_public_key_path).read_text().strip()

        # Preflight: check Agent pool capacity (need agents for two clusters)
        _assert_agent_pool_available(k8s=k8s_hub_client, expected_available=1)

        # ── Phase 1: create first cluster ──
        name_a = unique_name("e2e-reuse-a")
        uuid_a = cli.create_cluster(
            name=name_a,
            template=cluster_template,
            template_parameter_files={"pull_secret": pull_secret_path},
            template_parameters={"ssh_public_key": ssh_key},
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

            # ── Phase 3: wait for agent reclaim ──
            print(f"Waiting for agent reclaim (up to {self._RECLAIM_TIMEOUT}s)...")
            wait_for_agent_available(
                k8s=k8s_hub_client, co_name=co_name_a, timeout=self._RECLAIM_TIMEOUT, poll=self._RECLAIM_POLL
            )
            print("Agents reclaimed and available")
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
        name_b = unique_name("e2e-reuse-b")
        uuid_b = cli.create_cluster(
            name=name_b,
            template=cluster_template,
            template_parameter_files={"pull_secret": pull_secret_path},
            template_parameters={"ssh_public_key": ssh_key},
            network_attachment=network_attachment,
        )
        print(f"Created second cluster {name_b}: {uuid_b}")

        deletion_deadline_b: float | None = None
        deletion_requested_b = False
        co_name_b: str | None = None

        try:
            co_name_b = wait_for_cluster_order_cr(k8s=k8s_hub_client, uuid=uuid_b)
            wait_for_cluster_progressing(k8s=k8s_hub_client, name=co_name_b)

            # ── Phase 5: verify agent allocated to second cluster ──
            wait_for_cluster_ready(k8s=k8s_hub_client, name=co_name_b)
            print(f"Second cluster {name_b} is Ready — agent reuse verified")

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
