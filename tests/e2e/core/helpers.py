from __future__ import annotations

import ipaddress
import logging
import os
import re
import subprocess
import time
from typing import Any
from uuid import uuid4

import pytest

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.runner import poll_until, run_unchecked

logger = logging.getLogger(__name__)

_POOL_READY_STATE = "EXTERNAL_IP_POOL_STATE_READY"
_BMI_RUNNING_RETRIES = 180
_BMI_RUNNING_DELAY = 10


def unique_name(prefix: str) -> str:
    return f"{prefix}-{uuid4().hex[:8]}"


def allocate_worker_subnet(prefix: int = 24) -> ipaddress.IPv4Network:
    """Allocate a non-overlapping subnet for the current pytest-xdist worker."""
    worker_id = os.environ.get("PYTEST_XDIST_WORKER", "gw0")
    worker_num = int(worker_id.removeprefix("gw")) if worker_id.startswith("gw") else 0

    if not hasattr(allocate_worker_subnet, "_counters"):
        allocate_worker_subnet._counters = {}

    counter = allocate_worker_subnet._counters.get(prefix, 0)
    allocate_worker_subnet._counters[prefix] = counter + 1

    if prefix == 24:
        if worker_num >= 4:
            raise RuntimeError(f"Worker {worker_id} is outside the reserved /24 address space")
        if counter >= 32:
            raise RuntimeError(f"Worker {worker_id} exhausted /24 address space (counter={counter})")
        return ipaddress.IPv4Network(f"172.27.{worker_num * 32 + counter}.0/24")

    if prefix == 30:
        third_octet = 128 + worker_num * 32 + (counter // 64)
        if third_octet > 255:
            raise RuntimeError(f"Worker {worker_id} exhausted /30 address space (counter={counter})")
        return ipaddress.IPv4Network(f"172.27.{third_octet}.{(counter % 64) * 4}/30")

    raise NotImplementedError(f"Prefix /{prefix} not supported")


def grpc_error_message(exc: subprocess.CalledProcessError) -> str:
    combined = (exc.stderr or "") + (exc.stdout or "")
    match = re.search(r"Message:\s*(.+)", combined)
    return match.group(1).strip() if match else ""


def assert_grpc_rejected(exc_info: pytest.ExceptionInfo[subprocess.CalledProcessError], code: str) -> None:
    exc = exc_info.value
    combined: str = (exc.stderr or "") + (exc.stdout or "")
    assert re.search(rf"Code:\s*{code}", combined), f"Expected gRPC {code}, got: {combined.strip()}"


def assert_grpc_method_unavailable(
    exc_info: pytest.ExceptionInfo[subprocess.CalledProcessError], *, service: str, method: str
) -> None:
    """Assert that grpcurl cannot invoke a method absent from a public service descriptor."""
    exc = exc_info.value
    combined: str = (exc.stderr or "") + (exc.stdout or "")
    descriptor_error = f'service "{service}" does not include a method named "{method}"'
    assert descriptor_error in combined, f"Expected {service}/{method} to be unavailable, got: {combined.strip()}"


def assert_grpc_field_violation(
    exc_info: pytest.ExceptionInfo[subprocess.CalledProcessError], *, field_path: str
) -> None:
    assert_grpc_rejected(exc_info, "InvalidArgument")
    exc = exc_info.value
    combined: str = (exc.stderr or "") + (exc.stdout or "")
    assert field_path in combined, (
        f"Expected FieldViolation containing '{field_path}' in error output, got: {combined.strip()}"
    )


def wait_for_cr(*, k8s: K8sClient, uuid: str) -> str:
    return poll_until(
        fn=lambda: k8s.get_compute_instance_name(uuid=uuid, checked=False),
        until=lambda v: v != "",
        retries=30,
        delay=2,
        description=f"CR for {uuid}",
    )


def wait_for_provision(*, k8s: K8sClient, name: str) -> None:
    poll_until(
        fn=lambda: k8s.get_compute_instance_condition_status(name=name, condition_type="Provisioned", checked=False),
        until=lambda v: v == "True",
        retries=120,
        delay=5,
        description=f"{name} Provisioned condition",
    )


def wait_for_running(*, k8s: K8sClient, name: str) -> None:
    poll_until(
        fn=lambda: k8s.get_compute_instance_phase(name=name, checked=False),
        until=lambda v: v == "Running",
        retries=90,
        delay=10,
        description=f"{name} Running",
    )


def wait_for_restart(*, k8s: K8sClient, name: str, initial: str, restart_ts: str) -> None:
    poll_until(
        fn=lambda: k8s.get_compute_instance_last_restarted_at(name=name),
        until=lambda v: v != "" and v != initial and v >= restart_ts,
        retries=30,
        delay=10,
        description=f"{name} lastRestartedAt update",
    )


def wait_for_deletion(*, k8s: K8sClient, name: str) -> None:
    poll_until(
        fn=lambda: not k8s.is_present(resource="computeinstance", name=name),
        until=lambda v: v is True,
        retries=120,
        delay=5,
        description=f"{name} deletion",
    )


def wait_for_grpc_removal(*, grpc: GRPCClient, uuid: str) -> None:
    poll_until(
        fn=lambda: uuid not in grpc.list_compute_instance_ids(),
        until=lambda v: v is True,
        retries=30,
        delay=2,
        description=f"{uuid} removed from gRPC list",
    )


def wait_for_virtual_network_cr(*, k8s: K8sClient, uuid: str) -> str:
    return poll_until(
        fn=lambda: k8s.get_virtual_network_name(uuid=uuid, checked=False),
        until=lambda v: v != "",
        retries=30,
        delay=2,
        description=f"VirtualNetwork CR for {uuid}",
    )


def wait_for_virtual_network_ready(*, k8s: K8sClient, name: str) -> None:
    poll_until(
        fn=lambda: k8s.get_virtual_network_phase(name=name, checked=False),
        until=lambda v: v == "Ready",
        retries=60,
        delay=5,
        description=f"{name} VirtualNetwork Ready",
    )


def wait_for_virtual_network_deletion(*, k8s: K8sClient, name: str) -> None:
    poll_until(
        fn=lambda: not k8s.is_present(resource="virtualnetwork", name=name),
        until=lambda v: v is True,
        retries=120,
        delay=5,
        description=f"{name} VirtualNetwork deletion",
    )


def wait_for_subnet_cr(*, k8s: K8sClient, uuid: str) -> str:
    return poll_until(
        fn=lambda: k8s.get_subnet_name(uuid=uuid, checked=False),
        until=lambda v: v != "",
        retries=30,
        delay=2,
        description=f"Subnet CR for {uuid}",
    )


def wait_for_subnet_ready(*, k8s: K8sClient, name: str) -> None:
    poll_until(
        fn=lambda: k8s.get_subnet_phase(name=name, checked=False),
        until=lambda v: v == "Ready",
        retries=60,
        delay=5,
        description=f"{name} Subnet Ready",
    )


def wait_for_subnet_deletion(*, k8s: K8sClient, name: str) -> None:
    poll_until(
        fn=lambda: not k8s.is_present(resource="subnet", name=name),
        until=lambda v: v is True,
        retries=120,
        delay=5,
        description=f"{name} Subnet deletion",
    )


def wait_for_external_ip_pool_cr(*, k8s: K8sClient, uuid: str) -> str:
    return poll_until(
        fn=lambda: k8s.get_external_ip_pool_name(uuid=uuid, checked=False),
        until=lambda v: v != "",
        retries=30,
        delay=1,
        description=f"ExternalIPPool CR for {uuid}",
    )


def wait_for_external_ip_pool_ready(*, k8s: K8sClient, name: str) -> None:
    poll_until(
        fn=lambda: k8s.get_external_ip_pool_phase(name=name, checked=False),
        until=lambda v: v == "Ready",
        retries=60,
        delay=5,
        description=f"{name} ExternalIPPool Ready",
    )


def wait_for_external_ip_pool_grpc_ready(*, private_grpc: GRPCClient, pool_id: str) -> None:
    """Poll the private gRPC API until the pool state is READY.

    The K8s CR status may report Ready before the fulfillment-service database
    has been updated by the controller feedback loop.  Polling via gRPC closes
    this race so that subsequent ExternalIP creation does not hit
    FailedPrecondition.
    """

    def _state() -> str:
        try:
            pool = private_grpc.get_external_ip_pool(pool_id=pool_id)
        except subprocess.CalledProcessError:
            return ""
        return pool.get("object", {}).get("status", {}).get("state", "")

    poll_until(
        fn=_state,
        until=lambda v: v == _POOL_READY_STATE,
        retries=30,
        delay=2,
        description=f"ExternalIPPool {pool_id} gRPC READY",
    )


def wait_for_external_ip_pool_deletion(*, k8s: K8sClient, name: str) -> None:
    poll_until(
        fn=lambda: not k8s.is_present(resource="externalippool", name=name),
        until=lambda v: v is True,
        retries=120,
        delay=5,
        description=f"{name} ExternalIPPool deletion",
    )


def wait_for_external_ip_cr(*, k8s: K8sClient, uuid: str) -> str:
    return poll_until(
        fn=lambda: k8s.get_external_ip_name(uuid=uuid, checked=False),
        until=lambda v: v != "",
        retries=30,
        delay=1,
        description=f"ExternalIP CR for {uuid}",
    )


def wait_for_external_ip_allocated(*, k8s: K8sClient, name: str) -> None:
    poll_until(
        fn=lambda: k8s.get_external_ip_state(name=name, checked=False),
        until=lambda v: v == "Allocated",
        retries=60,
        delay=5,
        description=f"{name} ExternalIP Allocated",
    )


def wait_for_external_ip_deletion(*, k8s: K8sClient, name: str) -> None:
    poll_until(
        fn=lambda: not k8s.is_present(resource="externalip", name=name),
        until=lambda v: v is True,
        retries=120,
        delay=5,
        description=f"{name} ExternalIP deletion",
    )


def wait_for_external_ip_attachment_cr(*, k8s: K8sClient, uuid: str) -> str:
    return poll_until(
        fn=lambda: k8s.get_external_ip_attachment_name(uuid=uuid, checked=False),
        until=lambda v: v != "",
        retries=30,
        delay=1,
        description=f"ExternalIPAttachment CR for {uuid}",
    )


def wait_for_external_ip_attachment_ready(*, k8s: K8sClient, name: str) -> None:
    poll_until(
        fn=lambda: k8s.get_external_ip_attachment_phase(name=name, checked=False),
        until=lambda v: v == "Ready",
        retries=60,
        delay=5,
        description=f"{name} ExternalIPAttachment Ready",
    )


def wait_for_external_ip_attachment_deletion(*, k8s: K8sClient, name: str) -> None:
    poll_until(
        fn=lambda: not k8s.is_present(resource="externalipattachment", name=name),
        until=lambda v: v is True,
        retries=120,
        delay=5,
        description=f"{name} ExternalIPAttachment deletion",
    )


# NATGateway helpers


def wait_for_nat_gateway_ready(*, k8s: K8sClient, name: str) -> None:
    poll_until(
        fn=lambda: k8s.get_jsonpath(resource="natgateway", name=name, jsonpath="{.status.state}"),
        until=lambda state: state == "Ready",
        retries=30,
        delay=5,
        description=f"NATGateway {name} to become Ready",
    )


def wait_for_nat_gateway_deletion(*, k8s: K8sClient, name: str) -> None:
    poll_until(
        fn=lambda: not k8s.is_present(resource="natgateway", name=name),
        until=lambda v: v is True,
        retries=120,
        delay=5,
        description=f"{name} NATGateway deletion",
    )


def wait_for_cluster_order_cr(*, k8s: K8sClient, uuid: str) -> str:
    return poll_until(
        fn=lambda: k8s.get_cluster_order_name(uuid=uuid, checked=False),
        until=lambda v: v != "",
        retries=30,
        delay=2,
        description=f"ClusterOrder CR for {uuid}",
    )


def wait_for_cluster_progressing(*, k8s: K8sClient, name: str) -> None:
    poll_until(
        fn=lambda: k8s.get_cluster_order_phase(name=name, checked=False),
        until=lambda v: v == "Progressing",
        retries=30,
        delay=2,
        description=f"{name} ClusterOrder Progressing phase",
    )


def wait_for_cluster_order_event_reasons(
    *, k8s: K8sClient, name: str, reasons: set[str], stop_reasons: set[str] | None = None
) -> dict[str, dict[str, Any]]:
    observed_events: dict[str, dict[str, Any]] = {}

    def _observed_events() -> dict[str, dict[str, Any]]:
        observed_events.update(
            {event["reason"]: event for event in k8s.get_cluster_order_events(name=name) if event.get("reason")}
        )
        return observed_events

    stop_reasons = stop_reasons or set()
    return poll_until(
        fn=_observed_events,
        until=lambda observed: reasons.issubset(observed) or bool(stop_reasons & observed.keys()),
        retries=480,
        delay=15,
        description=f"{name} ClusterOrder provisioning events",
    )


def assert_cluster_order_events(
    *, events: dict[str, dict[str, Any]], expected: dict[str, tuple[str, str, str]]
) -> None:
    missing = set(expected) - set(events)
    assert not missing, f"Missing ClusterOrder lifecycle events: {sorted(missing)}; observed: {sorted(events)}"
    for reason, (event_type, action, message) in expected.items():
        event = events[reason]
        assert event.get("type") == event_type, f"Expected {event_type} event for {reason}: {event}"
        assert event.get("action") == action, f"Expected {action} action for {reason}: {event}"
        assert message in event.get("message", ""), f"Expected message for {reason}: {event}"


def assert_cluster_order_lifecycle_events(*, k8s: K8sClient, name: str) -> None:
    expected_events = {
        "Created": ("Normal", "Created", "ClusterOrder created"),
        "PreparingInfrastructure": ("Normal", "Provisioning", "Preparing Infrastructure"),
        "ControlPlaneStarting": ("Normal", "Provisioning", "Control Plane Starting"),
        "Ready": ("Normal", "Ready", "ClusterOrder is ready"),
    }
    if k8s.get_cluster_order_status(name=name).get("nodeSets"):
        expected_events["WorkersJoining"] = ("Normal", "Provisioning", "Workers Joining")

    events = {event["reason"]: event for event in k8s.get_cluster_order_events(name=name) if event.get("reason")}
    assert_cluster_order_events(events=events, expected=expected_events)


def assert_cluster_order_deleting_event(*, k8s: K8sClient, name: str) -> None:
    events = wait_for_cluster_order_event_reasons(k8s=k8s, name=name, reasons={"Deleting"})
    assert_cluster_order_events(
        events=events, expected={"Deleting": ("Normal", "Deleting", "ClusterOrder entered deleting phase")}
    )


def wait_for_cluster_ready(*, k8s: K8sClient, name: str) -> None:
    """Wait for ClusterOrder to reach Ready, failing fast on Failed phase.

    On **Failed**: raises immediately with phase, relevant conditions,
    recent provisioning-job IDs/states/timestamps, and messages.

    On **timeout while Progressing**: includes the same status summary plus
    elapsed time and last transition timestamp.

    Must stay safely above osac-aap's own wait_for_clusteroperators_retries
    budget (60 min) plus earlier steps in the same AAP job (create hosted
    cluster, retrieve kubeconfig, etc.), or this times out first with a
    less useful error while the ClusterOrder is still legitimately Progressing.

    No credentials or sensitive AAP output are included in the raised message;
    only the phase, condition reasons/messages, and job metadata are surfaced.
    """
    retries = 480
    delay = 15
    start = time.monotonic()

    try:
        poll_until(
            fn=lambda: k8s.get_cluster_order_phase(name=name, checked=False),
            until=lambda phase: phase in ("Ready", "Failed"),
            retries=retries,
            delay=delay,
            description=f"{name} ClusterOrder Ready (fail-fast)",
        )
    except TimeoutError:
        # Timeout while Progressing — build a detailed summary
        elapsed = time.monotonic() - start
        summary = _cluster_order_status_summary(k8s=k8s, name=name)
        raise TimeoutError(
            f"ClusterOrder {name} timed out after {elapsed:.0f}s while still Progressing.\n{summary}"
        ) from None

    phase = k8s.get_cluster_order_phase(name=name, checked=False)
    if phase == "Failed":
        summary = _cluster_order_status_summary(k8s=k8s, name=name)
        raise AssertionError(f"ClusterOrder {name} entered Failed phase.\n{summary}")


def _cluster_order_status_summary(*, k8s: K8sClient, name: str) -> str:
    """Build a bounded, credential-free status summary for diagnostics.

    Includes phase, relevant conditions (Progressing, Accepted), recent
    provisioning-job IDs/states/timestamps/messages, and warning events.
    """
    parts: list[str] = []

    # Phase
    phase = k8s.get_cluster_order_phase(name=name, checked=False)
    parts.append(f"  phase: {phase}")

    # Full status JSON for structured extraction
    try:
        status = k8s.get_cluster_order_status(name=name)
    except Exception:
        status = {}

    # Conditions (show Progressing and Accepted)
    conditions = status.get("conditions", [])
    for cond in conditions:
        ctype = cond.get("type", "")
        if ctype in ("Progressing", "Accepted", "ControlPlaneAvailable", "ClusterAvailable"):
            parts.append(
                f"  condition {ctype}: status={cond.get('status')}, "
                f"reason={cond.get('reason', '')}, "
                f"message={cond.get('message', '')!r}, "
                f"lastTransition={cond.get('lastTransitionTime', '')}"
            )

    # Provisioning jobs (most recent 3)
    jobs = status.get("provisioningJobs", [])
    if jobs:
        # Sort by timestamp descending
        sorted_jobs = sorted(jobs, key=lambda j: j.get("timestamp", ""), reverse=True)[:3]
        parts.append("  recent provisioning jobs:")
        for j in sorted_jobs:
            parts.append(
                f"    jobID={j.get('jobID', '?')}, "
                f"type={j.get('type', '?')}, "
                f"state={j.get('state', '?')}, "
                f"message={j.get('message', '')!r}, "
                f"timestamp={j.get('timestamp', '')}"
            )

    # Warning events (last 5)
    try:
        events = k8s.get_cluster_order_events(name=name)
        warnings = [e for e in events if e.get("type") == "Warning"][-5:]
        if warnings:
            parts.append("  recent warning events:")
            for ev in warnings:
                parts.append(f"    reason={ev.get('reason', '?')}, message={ev.get('message', '')!r}")
    except Exception:
        pass

    # Agent inventory on Failed phase — diagnose "0 agents" / pool exhaustion
    if phase == "Failed":
        _report_agent_inventory(k8s=k8s, context=f"create-failure for ClusterOrder {name}")

        # Surface agent-allocation failures from conditions for the better message
        for cond in conditions:
            msg = cond.get("message", "")
            if "agent" in msg.lower() and ("0" in msg or "added" in msg.lower()):
                parts.append(f"  agent-allocation-failure: {msg!r}")
                break

    return "\n".join(parts)


def wait_for_cluster_deletion(*, k8s: K8sClient, name: str) -> None:
    # HACK: HyperShift has multiple teardown bugs where controllers leave orphaned state
    # that deadlocks HostedCluster deletion. We force-clean on every poll iteration:
    #
    # 1. AgentCluster deprovision finalizer: capi-provider-agent is killed during teardown
    #    before removing its finalizer, blocking namespace termination.
    #    https://github.com/openshift/hypershift/blob/main/hypershift-operator/controllers/hostedcluster/karpenter.go#L88
    #
    # 2. Agent labels: the CAPI provider sometimes fails to clear
    #    clusterdeployment-namespace from agents after HostedCluster deletion. The delete
    #    playbook's detach_and_unlabel skips agents that still have this label set, leaving
    #    the clusterorder label stuck and blocking agent reuse for subsequent tests.
    #
    # 3. Machine pre-terminate hooks: the CAPI provider sets a pre-terminate hook
    #    annotation on Machines, but is killed before removing it. The CAPI Machine
    #    controller waits forever for the annotation to be removed, blocking the entire
    #    deletion cascade (Machine → MachineSet → CAPI Cluster → HostedCluster).
    def _check_deleted() -> bool:
        _force_cleanup_agentcluster_finalizers(k8s=k8s, name=name)
        _force_cleanup_agent_labels(k8s=k8s, name=name)
        _force_cleanup_machine_preterminate_hooks(k8s=k8s, name=name)
        phase = k8s.get_cluster_order_phase(name=name, checked=False)
        if phase is None:
            return True
        if phase == "Failed":
            _check_terminal_delete_failure(k8s=k8s, name=name)
        return False

    poll_until(
        fn=_check_deleted, until=lambda v: v is True, retries=120, delay=10, description=f"{name} ClusterOrder deletion"
    )


def wait_for_cluster_deletion_with_deadline(*, k8s: K8sClient, name: str, deadline: float) -> None:
    """Wait for ClusterOrder deletion using the remaining time from a shared deadline.

    Unlike ``wait_for_cluster_deletion``, this avoids repeating the full
    20-minute timeout when called from a ``finally`` cleanup block.  The
    caller computes a single ``deadline`` (``time.monotonic() + budget``)
    at the start and passes it through; the ``finally`` block reuses the
    same deadline so the total wall-clock stays bounded.

    On timeout, reports remaining finalizers and relevant Agent state to
    aid debugging.
    """
    remaining = max(deadline - time.monotonic(), 0)
    if remaining < 10:
        logger.warning("%s cleanup skipped — only %.0fs left on shared deadline", name, remaining)
        _report_deletion_diagnostics(k8s=k8s, name=name)
        return

    retries = max(int(remaining / 10), 1)

    def _check_deleted() -> bool:
        _force_cleanup_agentcluster_finalizers(k8s=k8s, name=name)
        _force_cleanup_agent_labels(k8s=k8s, name=name)
        _force_cleanup_machine_preterminate_hooks(k8s=k8s, name=name)
        phase = k8s.get_cluster_order_phase(name=name, checked=False)
        if phase is None:
            return True
        if phase == "Failed":
            _check_terminal_delete_failure(k8s=k8s, name=name)
        return False

    try:
        poll_until(
            fn=_check_deleted,
            until=lambda v: v is True,
            retries=retries,
            delay=10,
            description=f"{name} ClusterOrder deletion (deadline-aware)",
        )
    except TimeoutError:
        _report_deletion_diagnostics(k8s=k8s, name=name)
        raise


def _check_terminal_delete_failure(*, k8s: K8sClient, name: str) -> None:
    """Fail immediately when a delete provisioning job has terminally failed.

    During deletion the operator retries the delete AAP job with backoff.
    A transient failure (one ``Failed`` job followed by a newer ``Pending``
    or ``Running`` attempt) is **not** terminal — the operator is still
    retrying.  A terminal failure is when the **most recent** delete job
    is ``Failed`` and the ClusterOrder itself is in ``Failed`` phase,
    meaning the operator has given up.

    Raises ``AssertionError`` with the job ID, state, message, and
    standard deletion diagnostics so CI gets an actionable failure
    instead of burning through the full 20-minute poll budget.
    """
    try:
        status = k8s.get_cluster_order_status(name=name)
    except Exception:
        return  # Can't read status — not terminal, let the poller continue

    jobs = status.get("provisioningJobs", [])
    if not jobs:
        return

    # Find the most recent delete-type job
    delete_jobs = [j for j in jobs if j.get("type") == "delete"]
    if not delete_jobs:
        return

    latest = max(delete_jobs, key=lambda j: j.get("timestamp", ""))
    latest_state = latest.get("state", "")

    # Only terminal if the latest delete job is Failed — a newer
    # Pending/Running attempt means the operator is still retrying.
    if latest_state != "Failed":
        return

    # Build diagnostic summary
    _report_deletion_diagnostics(k8s=k8s, name=name)
    summary = _cluster_order_status_summary(k8s=k8s, name=name)
    raise AssertionError(
        f"ClusterOrder {name} delete failed terminally.\n"
        f"  latest delete job: ID={latest.get('jobID', '?')}, "
        f"state={latest_state}, message={latest.get('message', '')!r}, "
        f"timestamp={latest.get('timestamp', '')}\n"
        f"{summary}"
    )


def _report_deletion_diagnostics(*, k8s: K8sClient, name: str) -> None:
    """Log remaining finalizers and Agent state for a stalled deletion."""
    log = logging.getLogger(__name__)

    # Remaining finalizers
    try:
        finalizers = k8s.get_cluster_order_finalizers(name=name, checked=False)
        if finalizers:
            log.warning("ClusterOrder %s deletion stalled — remaining finalizers: %s", name, finalizers)
    except Exception:
        pass

    # Agent state in hardware-inventory — labeled agents for this ClusterOrder
    agent_ns = "hardware-inventory"
    clusterorder_label = "osac.openshift.io/clusterorder"
    base_args = [*k8s._base(), "--as", "system:admin"]
    try:
        output, rc = run_unchecked(
            *base_args,
            "get",
            "agents.agent-install.openshift.io",
            "-n",
            agent_ns,
            "-l",
            f"{clusterorder_label}={name}",
            "-o",
            "jsonpath={range .items[*]}{.metadata.name}={.status.debugInfo.state} {end}",
        )
        if rc == 0 and output.strip():
            log.warning("Agents still labeled for ClusterOrder %s: %s", name, output.strip())
    except Exception:
        pass

    # Full Agent inventory in hardware-inventory — diagnostic for stuck reclaim
    _report_agent_inventory(k8s=k8s, context=f"deletion-diagnostics for ClusterOrder {name}")

    # HostedCluster and ClusterDeployment evidence for reclaim stall diagnosis.
    # Preserve HostedCluster (do NOT delete it) and capture status/conditions.
    hc_ns = f"{k8s.namespace}-{name}"
    try:
        hc_output, rc = run_unchecked(
            *base_args, "get", "hostedcluster", name, "-n", hc_ns, "-o", "jsonpath={.status.conditions}"
        )
        if rc == 0 and hc_output.strip():
            log.warning("HostedCluster %s/%s conditions: %s", hc_ns, name, hc_output[:2000])
    except Exception:
        pass

    # ClusterDeployment status
    try:
        cd_output, rc = run_unchecked(
            *base_args,
            "get",
            "clusterdeployments.hive.openshift.io",
            "-n",
            hc_ns,
            "-o",
            "custom-columns=NAME:.metadata.name,INSTALLED:.spec.installed",
        )
        if rc == 0 and cd_output.strip():
            log.warning("ClusterDeployments in %s: %s", hc_ns, cd_output.strip()[:500])
    except Exception:
        pass


def _report_agent_inventory(*, k8s: K8sClient, context: str) -> None:
    """Log all Agents in hardware-inventory with state, binding, and labels.

    Provides a full snapshot of the Agent pool for diagnosing:
    - Pool exhaustion (all agents bound)
    - Stuck reclaim (agents in unbinding-pending-user-action)
    - Mismatched resource class or exclusion
    No credentials or secrets are included — only metadata and status fields.
    """
    log = logging.getLogger(__name__)
    agent_ns = "hardware-inventory"
    base_args = [*k8s._base(), "--as", "system:admin"]

    try:
        import json as _json

        output, rc = run_unchecked(*base_args, "get", "agents.agent-install.openshift.io", "-n", agent_ns, "-o", "json")
        if rc != 0:
            log.warning("Agent inventory query failed (rc=%d) [%s]: %s", rc, context, output[:500])
            return

        data = _json.loads(output)
        items = data.get("items", [])
        if not items:
            log.warning("Agent inventory is EMPTY in namespace %s [%s]", agent_ns, context)
            return

        log.warning("=== Agent inventory (%d agents) [%s] ===", len(items), context)
        for agent in items:
            meta = agent.get("metadata", {})
            spec = agent.get("spec", {})
            status = agent.get("status", {})
            debug_info = status.get("debugInfo", {})
            labels = meta.get("labels", {})
            conditions = status.get("conditions", [])

            # Build compact condition summary
            cond_summary = ", ".join(f"{c.get('type', '?')}={c.get('status', '?')}" for c in conditions[:5])

            cluster_ref = spec.get("clusterDeploymentName", {})
            if isinstance(cluster_ref, dict):
                binding = f"{cluster_ref.get('namespace', '')}/{cluster_ref.get('name', '')}"
            else:
                binding = str(cluster_ref) if cluster_ref else "unbound"

            state = debug_info.get("state", "unknown")
            resource_class = labels.get("osac.openshift.io/resource-class", "")
            clusterorder = labels.get("osac.openshift.io/clusterorder", "")

            # Determine exclusion reason for pool exhaustion diagnosis
            exclusion = ""
            if state == "unbinding-pending-user-action":
                exclusion = "stuck-in-unbinding"
            elif cluster_ref:
                exclusion = "already-bound"
            elif state not in ("known-unbound", "known", ""):
                exclusion = f"state-not-available({state})"

            log.warning(
                "  Agent %s: state=%s, binding=%s, resource_class=%s, clusterorder=%s, exclusion=%s, conditions=[%s]",
                meta.get("name", "?"),
                state,
                binding,
                resource_class,
                clusterorder or "none",
                exclusion or "none",
                cond_summary,
            )
    except Exception as exc:
        log.warning("Failed to query agent inventory [%s]: %s", context, exc)


def _force_cleanup_agentcluster_finalizers(*, k8s: K8sClient, name: str) -> None:
    # HCP namespace: {osac-ns}-{co-name}-{hc-name}, where hc-name == co-name
    hc_ns = f"{k8s.namespace}-{name}"
    cp_ns = f"{hc_ns}-{name}"
    finalizer = "agentclustercapi-provider.agent-install.openshift.io/deprovision"
    base_args = [*k8s._base(), "--as", "system:admin"]
    output, rc = run_unchecked(
        *base_args,
        "get",
        "agentclusters.capi-provider.agent-install.openshift.io",
        "-n",
        cp_ns,
        "-o",
        f"jsonpath={{.items[?(@.metadata.finalizers[*]=='{finalizer}')].metadata.name}}",
    )
    if rc != 0 or not output.strip():
        return
    for ac_name in output.strip().split():
        finalizers_json, rc = run_unchecked(
            *base_args,
            "get",
            f"agentclusters.capi-provider.agent-install.openshift.io/{ac_name}",
            "-n",
            cp_ns,
            "-o",
            "jsonpath={.metadata.finalizers}",
        )
        if rc != 0 or finalizer not in finalizers_json:
            continue
        import json

        idx = json.loads(finalizers_json).index(finalizer)
        run_unchecked(
            *base_args,
            "patch",
            f"agentclusters.capi-provider.agent-install.openshift.io/{ac_name}",
            "-n",
            cp_ns,
            "--type=json",
            f'-p=[{{"op": "remove", "path": "/metadata/finalizers/{idx}"}}]',
        )


def _force_cleanup_agent_labels(*, k8s: K8sClient, name: str) -> None:
    agent_ns = "hardware-inventory"
    clusterorder_label = "osac.openshift.io/clusterorder"
    clusterdeployment_ns_label = "agent-install.openshift.io/clusterdeployment-namespace"
    base_args = [*k8s._base(), "--as", "system:admin"]
    output, rc = run_unchecked(
        *base_args,
        "get",
        "agents.agent-install.openshift.io",
        "-n",
        agent_ns,
        "-l",
        f"{clusterorder_label}={name}",
        "-o",
        "jsonpath={.items[*].metadata.name}",
    )
    if rc != 0 or not output.strip():
        return
    for agent_name in output.strip().split():
        run_unchecked(
            *base_args,
            "label",
            f"agents.agent-install.openshift.io/{agent_name}",
            "-n",
            agent_ns,
            f"{clusterorder_label}-",
            f"{clusterdeployment_ns_label}-",
        )


def _force_cleanup_machine_preterminate_hooks(*, k8s: K8sClient, name: str) -> None:
    cp_ns = f"{k8s.namespace}-{name}-{name}"
    hook = "pre-terminate.delete.hook.machine.cluster.x-k8s.io/agentmachine"
    base_args = [*k8s._base(), "--as", "system:admin"]
    output, rc = run_unchecked(
        *base_args, "get", "machines.cluster.x-k8s.io", "-n", cp_ns, "-o", "jsonpath={.items[*].metadata.name}"
    )
    if rc != 0 or not output.strip():
        return
    for machine_name in output.strip().split():
        run_unchecked(*base_args, "annotate", f"machines.cluster.x-k8s.io/{machine_name}", "-n", cp_ns, f"{hook}-")


def wait_for_agent_available(*, k8s: K8sClient, co_name: str, timeout: int = 600, poll: int = 10) -> None:
    """Wait for agents previously bound to a ClusterOrder to reach available state.

    An agent is considered available when:
    - Its ``status.debugInfo.state`` is in
      (``known-unbound``, ``known``, ``discovering-unbound``), AND
    - The ``agent-install.openshift.io/clusterdeployment-namespace`` label
      is absent or empty.

    On timeout, captures and logs all Agent status and labels for debugging.

    Args:
        k8s: Hub K8s client
        co_name: ClusterOrder name used to find bound agents via the
                 ``osac.openshift.io/clusterorder`` label.
        timeout: Maximum seconds to wait (default 600 = 10 minutes).
        poll: Seconds between checks (default 10).
    """
    import json as _json

    log = logging.getLogger(__name__)
    agent_ns = "hardware-inventory"
    available_states = {"known-unbound", "known", "discovering-unbound"}
    base_args = [*k8s._base(), "--as", "system:admin"]
    deadline = time.monotonic() + timeout
    attempt = 0

    while True:
        attempt += 1
        output, rc = run_unchecked(
            *base_args,
            "get",
            "agents.agent-install.openshift.io",
            "-n",
            agent_ns,
            "-l",
            f"osac.openshift.io/clusterorder={co_name}",
            "-o",
            "json",
        )
        if rc != 0:
            log.warning("Agent query failed (rc=%d, attempt %d): %s", rc, attempt, output[:500])
        else:
            data = _json.loads(output)
            items = data.get("items", [])
            if not items:
                # No agents labeled for this cluster order — they may have
                # already been fully cleaned up by the controller.
                log.info("No agents labeled for ClusterOrder %s — reclaim complete", co_name)
                return

            all_available = True
            for agent in items:
                meta = agent.get("metadata", {})
                status = agent.get("status", {})
                labels = meta.get("labels", {})
                state = status.get("debugInfo", {}).get("state", "")
                cd_ns_label = labels.get("agent-install.openshift.io/clusterdeployment-namespace", "")
                if state not in available_states or cd_ns_label:
                    all_available = False
                    break

            if all_available:
                log.info(
                    "All %d agents for ClusterOrder %s reached available state after %d attempts",
                    len(items),
                    co_name,
                    attempt,
                )
                return

        if time.monotonic() >= deadline:
            # Timeout — capture full agent state for diagnostics
            _report_agent_inventory(k8s=k8s, context=f"agent-reuse-timeout for ClusterOrder {co_name}")
            raise TimeoutError(
                f"Agents for ClusterOrder {co_name} did not reach available state "
                f"within {timeout}s ({attempt} attempts). "
                f"Check agent inventory log above for state and label details."
            )
        time.sleep(poll)


def assert_agent_pool_available(*, k8s: K8sClient, expected_available: int = 1) -> None:
    """Assert that enough Agents are available before provisioning.

    FAILS the test immediately if the number of available (unbound,
    ready-state) agents is below ``expected_available``.  This prevents
    provisioning from starting when the Agent pool is exhausted, avoiding
    a long wait that would end in a "0 agents" failure anyway.

    Reports full Agent state, labels, conditions, and count in the failure
    message for immediate diagnosis.
    """
    import json as _json

    log = logging.getLogger(__name__)
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
            _report_agent_inventory(k8s=k8s, context="preflight-pool-shortage")
            raise AssertionError(
                f"Agent pool preflight FAILED: {len(available_agents)} available agents "
                f"(need {expected_available}), {len(items)} total in {agent_ns}.\n"
                f"Available: {available_agents or 'none'}\n"
                f"Unavailable: {unavailable_summary or 'none'}"
            )

    except AssertionError:
        raise
    except Exception as exc:
        log.warning("Agent preflight check failed (non-fatal): %s", exc)


def wait_for_cluster_deleting(*, k8s: K8sClient, name: str) -> None:
    poll_until(
        fn=lambda: k8s.get_cluster_order_phase(name=name, checked=False),
        # get_cluster_order_phase returns None only for a missing ClusterOrder;
        # an empty phase from an existing object is not a deletion signal.
        until=lambda v: v == "Deleting" or v is None,
        retries=30,
        delay=5,
        description=f"{name} ClusterOrder Deleting phase",
    )


def wait_for_cluster_grpc_deleting_or_archived(*, grpc: GRPCClient, uuid: str) -> None:
    """Succeed if we catch CLUSTER_STATE_DELETING or if the cluster is already archived.

    The DELETING window in the fulfillment-service is extremely short (one
    Update + Signal round-trip).  Polling for the exact state is racey; accepting
    either DELETING or 'already gone' makes the assertion reliable.
    """

    def _done() -> bool:
        try:
            cluster = grpc.get_cluster(cluster_id=uuid)
            state = cluster.get("object", {}).get("status", {}).get("state", "")
            return state == "CLUSTER_STATE_DELETING"
        except subprocess.CalledProcessError:
            return True

    poll_until(
        fn=_done,
        until=lambda v: v is True,
        retries=30,
        delay=2,
        description=f"{uuid} gRPC DELETING or already archived",
    )


def wait_for_cluster_grpc_removal(*, grpc: GRPCClient, uuid: str) -> None:
    # retry_on_error=True: a flaky grpcurl call hitting a momentarily-busy
    # route right after heavy cluster-deletion activity shouldn't fail the
    # whole test on the first hiccup.
    poll_until(
        fn=lambda: uuid not in grpc.list_cluster_ids(),
        until=lambda v: v is True,
        retries=60,
        delay=5,
        description=f"{uuid} removed from gRPC cluster list",
        retry_on_error=True,
    )


def wait_for_security_group_cr(*, k8s: K8sClient, uuid: str) -> str:
    return poll_until(
        fn=lambda: k8s.get_security_group_name(uuid=uuid, checked=False),
        until=lambda v: v != "",
        retries=30,
        delay=2,
        description=f"SecurityGroup CR for {uuid}",
    )


def wait_for_security_group_ready(*, k8s: K8sClient, name: str) -> None:
    poll_until(
        fn=lambda: k8s.get_security_group_phase(name=name, checked=False),
        until=lambda v: v == "Ready",
        retries=60,
        delay=5,
        description=f"{name} SecurityGroup Ready",
    )


def wait_for_security_group_deletion(*, k8s: K8sClient, name: str) -> None:
    poll_until(
        fn=lambda: not k8s.is_present(resource="securitygroup", name=name),
        until=lambda v: v is True,
        retries=120,
        delay=5,
        description=f"{name} SecurityGroup deletion",
    )


# Tenant helpers


def wait_for_tenant_cr(*, k8s: K8sClient, name: str) -> None:
    poll_until(
        fn=lambda: k8s.is_present(resource="tenant", name=name),
        until=lambda v: v is True,
        retries=30,
        delay=2,
        description=f"Tenant CR {name}",
    )


def wait_for_tenant_condition(*, k8s: K8sClient, name: str, condition_type: str, expected_status: str = "True") -> None:
    def _check() -> str:
        if not k8s.is_present(resource="tenant", name=name):
            raise AssertionError(f"Tenant {name} disappeared before {condition_type}={expected_status}")
        phase: str = k8s.get_tenant_phase(name=name, checked=False)
        if phase == "Failed":
            cond_status = k8s.get_tenant_condition_status(name=name, condition_type=condition_type, checked=False)
            if cond_status != expected_status:
                raise AssertionError(f"Tenant {name} entered Failed phase before {condition_type}={expected_status}")
        return k8s.get_tenant_condition_status(name=name, condition_type=condition_type, checked=False)

    poll_until(
        fn=_check,
        until=lambda v: v == expected_status,
        retries=120,
        delay=5,
        description=f"Tenant {name} {condition_type}={expected_status}",
    )


def wait_for_tenant_deletion(*, k8s: K8sClient, name: str) -> None:
    poll_until(
        fn=lambda: not k8s.is_present(resource="tenant", name=name),
        until=lambda v: v is True,
        retries=120,
        delay=5,
        description=f"Tenant {name} deletion",
    )


# CaaS cluster storage helpers


def wait_for_cluster_order_condition(
    *, k8s: K8sClient, name: str, condition_type: str, expected_status: str = "True"
) -> None:
    def _check() -> str:
        if not k8s.is_present(resource="clusterorder", name=name):
            raise AssertionError(f"ClusterOrder {name} disappeared before {condition_type}={expected_status}")
        phase: str = k8s.get_cluster_order_phase(name=name, checked=False)
        cond_status = k8s.get_cluster_order_condition_status(name=name, condition_type=condition_type, checked=False)
        if phase == "Failed" and cond_status != expected_status:
            raise AssertionError(f"ClusterOrder {name} entered Failed phase before {condition_type}={expected_status}")
        return cond_status

    poll_until(
        fn=_check,
        until=lambda v: v == expected_status,
        retries=120,
        delay=10,
        description=f"ClusterOrder {name} {condition_type}={expected_status}",
    )


def wait_for_tenant_cluster_storage_entry(*, k8s: K8sClient, tenant_name: str, cluster_name: str) -> dict[str, Any]:
    def _check() -> dict[str, Any] | None:
        entries = k8s.get_tenant_cluster_storage(name=tenant_name, checked=False)
        for entry in entries:
            if entry.get("clusterName") == cluster_name and entry.get("ready") is True:
                return entry
        return None

    return poll_until(
        fn=_check,
        until=lambda v: v is not None,
        retries=60,
        delay=10,
        description=f"Tenant {tenant_name} clusterStorage entry for {cluster_name}",
    )


def wait_for_tenant_cluster_storage_entry_removed(*, k8s: K8sClient, tenant_name: str, cluster_name: str) -> None:
    poll_until(
        fn=lambda: all(
            entry.get("clusterName") != cluster_name
            for entry in k8s.get_tenant_cluster_storage(name=tenant_name, checked=False)
        ),
        until=lambda v: v is True,
        retries=60,
        delay=10,
        description=f"Tenant {tenant_name} clusterStorage entry for {cluster_name} removed",
    )


# Storage resource helpers


def wait_for_storage_classes_by_tenant(*, k8s: K8sClient, tenant_name: str, min_count: int = 1) -> list[str]:
    return poll_until(
        fn=lambda: k8s.list_storage_class_names_by_tenant(tenant_name=tenant_name),
        until=lambda v: len(v) >= min_count,
        retries=120,
        delay=5,
        description=f"StorageClasses for tenant {tenant_name} (>= {min_count})",
    )


def wait_for_storage_classes_removed(*, k8s: K8sClient, tenant_name: str) -> None:
    poll_until(
        fn=lambda: k8s.count_storage_classes_by_tenant(tenant_name=tenant_name),
        until=lambda v: v == 0,
        retries=120,
        delay=5,
        description=f"StorageClasses for tenant {tenant_name} removed",
    )


def wait_for_secrets_removed(*, k8s: K8sClient, tenant_name: str, namespace: str) -> None:
    poll_until(
        fn=lambda: k8s.count_secrets_by_tenant(tenant_name=tenant_name, namespace=namespace),
        until=lambda v: v == 0,
        retries=120,
        delay=5,
        description=f"Secrets for tenant {tenant_name} in {namespace} removed",
    )


# BareMetalInstance helpers


def wait_for_bmi_cr(*, k8s: K8sClient, uuid: str) -> str:
    return poll_until(
        fn=lambda: k8s.get_baremetal_instance_name(uuid=uuid, checked=False),
        until=lambda v: v != "",
        retries=30,
        delay=2,
        description=f"BareMetalInstance CR for {uuid}",
    )


def wait_for_bmi_running(*, grpc: GRPCClient, bmi_id: str, retries: int = _BMI_RUNNING_RETRIES) -> None:
    def _check_state() -> str:
        state: str = grpc.get_baremetal_instance_state(bmi_id=bmi_id)
        assert "FAILED" not in state, f"BareMetalInstance {bmi_id} entered {state}"
        return state

    poll_until(
        fn=_check_state,
        until=lambda v: v == "BARE_METAL_INSTANCE_STATE_RUNNING",
        retries=retries,
        delay=_BMI_RUNNING_DELAY,
        description=f"{bmi_id} RUNNING",
    )


def assert_bmi_lifecycle_on_running(
    *, grpc: GRPCClient, k8s: K8sClient, bmi_id: str, bmh_namespace: str, power_cycle: bool = True
) -> tuple[str, str]:
    """Assert BMH binding/provisioning on an already-RUNNING BMI.

    Does not create or delete the instance. Inventory exhaust calls this after all
    claim BMIs reach RUNNING so provisioning is not blocked by interleaved checks.

    Returns (bmi_cr_name, bmh_name).
    """
    assert bmi_id in grpc.list_baremetal_instance_ids()
    bmi_cr_name: str = wait_for_bmi_cr(k8s=k8s, uuid=bmi_id)

    external_host_id: str = k8s.get_baremetal_instance_external_host_id(name=bmi_cr_name)
    assert "/" in external_host_id, f"Expected namespace/name format, got: {external_host_id}"
    bmh_ns, bmh_name = external_host_id.split("/", 1)
    assert bmh_ns == bmh_namespace, f"BMH landed in {bmh_ns}, expected {bmh_namespace}"

    wait_for_bmh_provisioned(k8s=k8s, name=bmh_name, bmh_namespace=bmh_ns)

    image_url: str = k8s.get_bmh_image_url(name=bmh_name, bmh_namespace=bmh_ns)
    assert image_url != "", f"BMH {bmh_name} has no image URL after provisioning"

    consumer_ref: str = k8s.get_bmh_consumer_ref(name=bmh_name, bmh_namespace=bmh_ns)
    assert consumer_ref != "", f"BMH {bmh_name} has no consumerRef after allocation"

    online: str = k8s.get_bmh_online(name=bmh_name, bmh_namespace=bmh_ns)
    assert online == "true", f"BMH {bmh_name} should be online after provisioning, got: {online}"

    if power_cycle:
        grpc.update_baremetal_instance_run_strategy(
            bmi_id=bmi_id, run_strategy="BARE_METAL_INSTANCE_RUN_STRATEGY_HALTED"
        )
        poll_until(
            fn=lambda: k8s.get_bmh_powered_on(name=bmh_name, bmh_namespace=bmh_ns),
            until=lambda v: v == "false",
            retries=60,
            delay=5,
            description=f"{bmh_name} powered off",
        )
        grpc.update_baremetal_instance_run_strategy(
            bmi_id=bmi_id, run_strategy="BARE_METAL_INSTANCE_RUN_STRATEGY_ALWAYS"
        )
        poll_until(
            fn=lambda: k8s.get_bmh_powered_on(name=bmh_name, bmh_namespace=bmh_ns),
            until=lambda v: v == "true",
            retries=60,
            delay=5,
            description=f"{bmh_name} powered on",
        )

    return bmi_cr_name, bmh_name


def assert_bmi_does_not_become_running(*, grpc: GRPCClient, bmi_id: str, retries: int = 36, delay: int = 10) -> str:
    """Observe that a BareMetalInstance never reaches RUNNING.

    Returns the last observed state. If the instance enters a FAILED state,
    returns immediately (inventory / scheduling failure).
    """
    last_state = ""
    for _ in range(retries):
        last_state = grpc.get_baremetal_instance_state(bmi_id=bmi_id)
        assert last_state != "BARE_METAL_INSTANCE_STATE_RUNNING", (
            f"BareMetalInstance {bmi_id} unexpectedly reached RUNNING with no free BMH"
        )
        if "FAILED" in last_state:
            return last_state
        time.sleep(delay)
    return last_state


def wait_for_bmi_running_after_recovery(*, grpc: GRPCClient, bmi_id: str) -> None:
    """Wait until a BMI reaches RUNNING, allowing a prior FAILED/pending state.

    Used after inventory is freed so an overflow instance can be scheduled.
    Unlike wait_for_bmi_running, does not fail-fast on FAILED.
    """
    poll_until(
        fn=lambda: grpc.get_baremetal_instance_state(bmi_id=bmi_id),
        until=lambda v: v == "BARE_METAL_INSTANCE_STATE_RUNNING",
        retries=_BMI_RUNNING_RETRIES,
        delay=_BMI_RUNNING_DELAY,
        description=f"{bmi_id} RUNNING after inventory recovery",
    )


def wait_for_bmi_deletion(*, k8s: K8sClient, name: str) -> None:
    # 2700s (45min), not the old 1200s (20min): the deprovision AAP job this
    # blocks on retries with exponential backoff up to a 30-minute ceiling
    # (osac-operator's shared pkg/provisioning, BackoffMaxDelay) when it fails
    # -- e.g. under the AAP job-pod attach flakiness tracked in OSAC-3499. A
    # 20-minute window can time out here while the operator is still correctly
    # retrying and would have succeeded; 45 minutes gives it room for a worst-case
    # backoff wait plus job execution time, without silently swallowing an
    # actually-stuck deletion (still fails, just later).
    poll_until(
        fn=lambda: not k8s.is_present(resource="baremetalinstance", name=name),
        until=lambda v: v is True,
        retries=270,
        delay=10,
        description=f"{name} BareMetalInstance deletion",
    )


def wait_for_bmi_grpc_removal(*, grpc: GRPCClient, uuid: str) -> None:
    poll_until(
        fn=lambda: uuid not in grpc.list_baremetal_instance_ids(),
        until=lambda v: v is True,
        retries=60,
        delay=5,
        description=f"{uuid} removed from gRPC BareMetalInstance list",
    )


def wait_for_bmh_provisioned(*, k8s: K8sClient, name: str, bmh_namespace: str) -> None:
    def _check() -> str:
        state: str = k8s.get_bmh_provisioning_state(name=name, bmh_namespace=bmh_namespace)
        assert state != "error", f"BMH {name} entered error state"
        return state

    poll_until(
        fn=_check, until=lambda v: v == "provisioned", retries=120, delay=10, description=f"{name} BMH provisioned"
    )


def wait_for_bmh_available(*, k8s: K8sClient, name: str, bmh_namespace: str) -> None:
    def _check() -> str:
        state: str = k8s.get_bmh_provisioning_state(name=name, bmh_namespace=bmh_namespace)
        assert state != "error", f"BMH {name} entered error state"
        return state

    poll_until(
        fn=_check,
        until=lambda v: v in ("available", "ready"),
        retries=120,
        delay=10,
        description=f"{name} BMH available",
    )
