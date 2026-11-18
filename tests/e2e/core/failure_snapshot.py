"""Snapshot of the system at the moment a test fails, taken by named sources into one directory per failure.

A source is a callable `fn(directory, window)` registered by name, optionally for some failure kinds only.
Sources run concurrently under a total time budget and can never fail the test: an exception, or running
past the budget, is written to `snapshot-errors.txt`. Everything written passes the sanitizer. Sources write
through `write_text`/`write_json`. CaaS reads only the referenced guest kubeconfig Secret, in memory,
and passes that credential to the guest client through stdin.
"""

from __future__ import annotations

import base64
import concurrent.futures
import hashlib
import json
import logging
import os
import re
import shlex
import subprocess
import threading
import time
from collections.abc import Callable, Iterable
from dataclasses import dataclass
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

from tests.e2e.core.failures import Kind
from tests.e2e.core.grpc_client import PRIVATE_API, GRPCClient
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.runner import CommandFailed, run
from tests.e2e.core.sanitize import sanitize_obj, sanitize_text

logger = logging.getLogger(__name__)

ERRORS_FILE = "snapshot-errors.txt"
CAP_REACHED = "cap reached"
MAX_SNAPSHOTS_PER_WORKER = 12
BUDGET_S = 60.0
MAX_WORKERS = 6
SOURCE_COMMAND_TIMEOUT_S = 30
CAAS_COMMAND_TIMEOUT_S = 5
CHECKPOINT_BUDGET_S = 10.0
GUEST_KINDS = (Kind.TRAFFIC_BLOCKED, Kind.TEST_SSH_FAILED, Kind.UNCLASSIFIED, Kind.ASSERTION)
# The capture time and the boot time first, so the dump lines up with the logs and a reboot shows.
GUEST_COMMAND = "date -u +%FT%TZ; uptime -s; ip -br addr; ip route; ip neigh; ss -tln; cat /etc/resolv.conf"
GUEST_PER_VM_TIMEOUT_S = 10
GUEST_TOTAL_BUDGET_S = 45
FABRIC_COMMAND_ENV = "OSAC_E2E_FABRIC_SNAPSHOT_CMD"

# Private API resources and the CR plural each one maps to.
_RESOURCES = (
    ("ComputeInstances", "computeinstances.osac.openshift.io"),
    ("VirtualNetworks", "virtualnetworks.osac.openshift.io"),
    ("Subnets", "subnets.osac.openshift.io"),
    ("SecurityGroups", "securitygroups.osac.openshift.io"),
    ("NATGateways", "natgateways.osac.openshift.io"),
    ("ExternalIPs", "externalips.osac.openshift.io"),
    ("ExternalIPAttachments", "externalipattachments.osac.openshift.io"),
    ("ExternalIPPools", "externalippools.osac.openshift.io"),
    ("BareMetalInstances", "baremetalinstances.osac.openshift.io"),
)
# Deployment names in the OSAC namespace (fulfillment-service, osac-operator and bare-metal-fulfillment-operator
# charts). A deployment the install does not have is recorded in the errors file and skipped.
_LOG_DEPLOYMENTS = (
    "fulfillment-grpc-server",
    "fulfillment-controller",
    "osac-operator",
    "bmf-operator-controller-manager",
)


@dataclass(frozen=True)
class Window:
    """The log window and the monotonic deadline shared by the snapshot sources.

    `started_at` is the start of the failed test. `logs_since` is where the logs begin: the start of the failed
    test's module, because ordered suites build the failing resources in earlier tests, and logs from the failed
    test alone (often under a second) miss how they were built.
    """

    started_at: datetime
    logs_since: datetime | None = None
    deadline: float | None = None

    @property
    def log_start(self) -> datetime:
        return min(self.logs_since, self.started_at) if self.logs_since is not None else self.started_at

    def timeout(self, maximum: float = SOURCE_COMMAND_TIMEOUT_S) -> float:
        if self.deadline is None:
            return maximum
        remaining = self.deadline - time.monotonic()
        if remaining <= 0:
            raise TimeoutError("snapshot budget exhausted")
        return min(maximum, remaining)


SourceFn = Callable[[Path, Window], None]


class SnapshotSources:
    """Named snapshot sources. Registering a name again replaces the earlier source."""

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._sources: dict[str, tuple[SourceFn, frozenset[Kind] | None]] = {}

    def register(self, name: str, fn: SourceFn, kinds: Iterable[Kind] | None = None) -> None:
        with self._lock:
            self._sources[name] = (fn, frozenset(kinds) if kinds is not None else None)

    def unregister(self, name: str) -> None:
        with self._lock:
            self._sources.pop(name, None)

    def for_kind(self, kind: Kind) -> list[tuple[str, SourceFn]]:
        with self._lock:
            return [(n, fn) for n, (fn, kinds) in self._sources.items() if kinds is None or kind in kinds]


SOURCES = SnapshotSources()

_errors_lock = threading.Lock()


def write_text(directory: Path, name: str, text: str) -> None:
    (directory / name).write_text(sanitize_text(text), encoding="utf-8")


def write_json(directory: Path, name: str, value: object) -> None:
    write_text(directory, name, json.dumps(sanitize_obj(value), indent=2, sort_keys=True, default=str))


def record_error(directory: Path, name: str, message: str) -> None:
    line = sanitize_text(f"{name}: {message}").replace("\n", " ")
    with _errors_lock, (directory / ERRORS_FILE).open("a", encoding="utf-8") as handle:
        handle.write(line + "\n")


def _safe(name: str) -> str:
    return re.sub(r"[^A-Za-z0-9_.-]+", "_", name)[:80]


class Snapshotter:
    def __init__(
        self,
        output_dir: Path,
        worker: str,
        sources: SnapshotSources = SOURCES,
        *,
        budget_s: float = BUDGET_S,
        cap: int = MAX_SNAPSHOTS_PER_WORKER,
    ) -> None:
        self.output_dir = output_dir
        self.worker = worker
        self.sources = sources
        self.budget_s = budget_s
        self.cap = cap
        self._taken = 0
        self._checkpoints = 0

    def take(
        self,
        test: str,
        kind: Kind,
        started_at: datetime,
        record: dict[str, Any] | None = None,
        logs_since: datetime | None = None,
        *,
        checkpoint: bool = False,
    ) -> str:
        """Capture a failure or a pre-delete checkpoint; return its path relative to the output."""
        count = self._checkpoints if checkpoint else self._taken
        if count >= self.cap:
            return CAP_REACHED
        if checkpoint:
            self._checkpoints += 1
        else:
            self._taken += 1
        category = "snapshots" if checkpoint else "failures"
        relative = f"{category}/{self.worker}/{count + 1:02d}-{_safe(test)}"
        directory = self.output_dir / relative
        directory.mkdir(parents=True, exist_ok=True, mode=0o700)
        write_json(
            directory,
            "snapshot.json" if checkpoint else "failure.json",
            {
                **(record or {}),
                "test": test,
                "kind": None if checkpoint else str(kind),
                "snapshot": relative,
                "captured_at": datetime.now(UTC).isoformat(),
            },
        )
        budget = min(self.budget_s, CHECKPOINT_BUDGET_S) if checkpoint else self.budget_s
        window = Window(started_at, logs_since=logs_since, deadline=time.monotonic() + budget)
        self._run_sources(directory, window, self.sources.for_kind(kind))
        self._scrub(directory)
        return relative

    def _run_sources(self, directory: Path, window: Window, sources: list[tuple[str, SourceFn]]) -> None:
        if not sources:
            return
        executor = concurrent.futures.ThreadPoolExecutor(max_workers=MAX_WORKERS, thread_name_prefix="e2e-snapshot")
        futures = {executor.submit(fn, directory, window): name for name, fn in sources}
        try:
            timeout = max(0.0, window.deadline - time.monotonic()) if window.deadline is not None else self.budget_s
            done, pending = concurrent.futures.wait(futures, timeout=timeout)
            for future in done:
                exc = future.exception()
                if exc is not None:
                    record_error(directory, futures[future], f"{type(exc).__name__}: {exc}")
            if pending:
                names = ", ".join(sorted(futures[f] for f in pending))
                record_error(directory, "budget", f"snapshot budget expired; unfinished sources: {names}")
        finally:
            # Commands share the snapshot deadline; queued work is cancelled.
            executor.shutdown(wait=False, cancel_futures=True)

    @staticmethod
    def _scrub(directory: Path) -> None:
        """Second pass over what finished sources wrote, for any that wrote without the helpers."""
        for path in directory.iterdir():
            try:
                text = path.read_text(encoding="utf-8", errors="replace")
                clean = sanitize_text(text)
                if clean != text:
                    path.write_text(clean, encoding="utf-8")
            except OSError:
                logger.warning("snapshot scrub could not read one file")


# Generic sources


def api_source(private_grpc: GRPCClient) -> SourceFn:
    """Private gRPC List of every resource the networking and compute suites create."""

    def source(directory: Path, window: Window) -> None:
        for service, _ in _RESOURCES:
            try:
                data = private_grpc.call(service=f"{PRIVATE_API}.{service}/List", timeout=window.timeout())
            except Exception as exc:  # one failed List must not stop the others
                record_error(directory, f"api:{service}", f"{type(exc).__name__}: {exc}")
            else:
                write_json(directory, f"api-{service.lower()}.json", data)

    return source


def k8s_source(k8s: K8sClient, namespace: str) -> SourceFn:
    """The matching CRs and the events of the OSAC namespace. Secrets are never read."""

    def source(directory: Path, window: Window) -> None:
        def get_json(*args: str) -> object:
            return json.loads(run(*k8s.base_args(), "get", *args, "-o", "json", timeout=window.timeout()))

        for service, resource in _RESOURCES:
            try:
                write_json(directory, f"k8s-{service.lower()}.json", get_json(resource, "-n", namespace))
            except Exception as exc:
                record_error(directory, f"k8s:{service}", f"{type(exc).__name__}: {exc}")
        try:
            write_json(directory, "k8s-events.json", get_json("events", "-n", namespace))
        except Exception as exc:
            record_error(directory, "k8s:events", f"{type(exc).__name__}: {exc}")

    return source


def logs_source(k8s: K8sClient, namespace: str) -> SourceFn:
    """Logs of the fulfillment API server and controller and the operators since the failed test's module began."""

    def source(directory: Path, window: Window) -> None:
        since = window.log_start.isoformat().replace("+00:00", "Z")
        for deployment in _LOG_DEPLOYMENTS:
            try:
                output = run(
                    *k8s.base_args(),
                    "logs",
                    f"deployment/{deployment}",
                    "-n",
                    namespace,
                    "--all-containers",
                    "--timestamps",
                    f"--since-time={since}",
                    "--tail=20000",
                    timeout=window.timeout(),
                )
            except Exception as exc:
                record_error(directory, f"logs:{deployment}", f"{type(exc).__name__}: {exc}")
            else:
                write_text(directory, f"log-{deployment}.txt", output)

    return source


def _resource_state(value: dict[str, Any]) -> dict[str, Any]:
    """Keep approved identity, ownership, status and guest networking configuration."""
    if "items" in value:
        return {
            "apiVersion": value.get("apiVersion"),
            "kind": value.get("kind"),
            "items": [_resource_state(item) for item in value["items"]],
        }
    metadata = value.get("metadata") or {}
    identity = {
        key: metadata[key]
        for key in (
            "name",
            "namespace",
            "uid",
            "resourceVersion",
            "generation",
            "creationTimestamp",
            "deletionTimestamp",
            "deletionGracePeriodSeconds",
            "labels",
            "ownerReferences",
            "finalizers",
        )
        if key in metadata
    }
    identity["annotations"] = {
        key: item
        for key, item in (metadata.get("annotations") or {}).items()
        if key in ("osac.openshift.io/tenant", "osac.openshift.io/owner-reference")
    }
    kind = value.get("kind")
    status = value.get("status") or {}
    status_fields = {
        "ClusterOrder": (
            "observedVersion",
            "upgradeObservation",
            "phase",
            "phaseTransitionTime",
            "conditions",
            "clusterReference",
            "nodeRequests",
            "provisioningJobs",
            "clusterStorageJobs",
            "addOnOperatorJobs",
            "fulfillmentTrustBundleHash",
            "desiredConfigVersion",
            "apiEndpoint",
            "ingressEndpoint",
            "apiURL",
            "consoleURL",
            "apiDNSRecordsPublishedTime",
            "apiDNSHostedClusterName",
            "ingressDNSHostedClusterName",
            "ingressAllocatedAddress",
            "jobs",
            "desiredWorkers",
            "currentWorkers",
            "readyWorkers",
            "workers",
        ),
        "Agent": ("conditions", "progress", "validationsInfo"),
        "InfraEnv": ("conditions", "agentLabelSelector", "createdTime"),
        "HostedCluster": (
            "conditions",
            "version",
            "controlPlaneVersion",
            "controlPlaneEndpoint",
            "kubeconfig",
            "ignitionEndpoint",
        ),
        "HostedControlPlane": (
            "conditions",
            "ready",
            "initialized",
            "externalManagedControlPlane",
            "controlPlaneEndpoint",
            "releaseImage",
            "version",
            "controlPlaneVersion",
            "versionStatus",
            "lastReleaseImageTransitionTime",
            "kubeConfig",
            "nodeCount",
            "initialization",
        ),
        "NodePool": ("conditions", "replicas", "version", "nodesInfo"),
        "Node": ("conditions", "addresses", "capacity", "allocatable", "nodeInfo"),
        "Pod": (
            "phase",
            "conditions",
            "reason",
            "message",
            "hostIP",
            "hostIPs",
            "podIP",
            "podIPs",
            "startTime",
            "containerStatuses",
            "initContainerStatuses",
            "ephemeralContainerStatuses",
            "qosClass",
        ),
        "CertificateSigningRequest": ("conditions",),
        "ClusterVersion": ("conditions", "desired", "history", "observedGeneration"),
        "ClusterOperator": ("conditions", "versions", "relatedObjects"),
        "Service": ("conditions", "loadBalancer"),
        "IPAddressPool": ("assignedIPv4", "assignedIPv6", "availableIPv4", "availableIPv6"),
        "L2Advertisement": (),
        "EndpointSlice": (),
        "Event": (),
    }.get(kind, ())
    status = {key: status[key] for key in status_fields if key in status}
    if kind == "Agent":
        debug = (value.get("status") or {}).get("debugInfo") or {}
        status["debugInfo"] = {key: debug[key] for key in ("state", "stateInfo") if key in debug}
    elif kind == "CertificateSigningRequest":
        status = {"conditions": status.get("conditions", [])}
    result = {"apiVersion": value.get("apiVersion"), "kind": kind, "metadata": identity, "status": status}
    safe_spec = {
        "CertificateSigningRequest": ("username", "groups", "signerName", "usages"),
        "Service": ("type", "clusterIP", "loadBalancerIP", "externalTrafficPolicy", "ports", "selector"),
        "IPAddressPool": ("addresses", "autoAssign", "avoidBuggyIPs"),
        "L2Advertisement": ("ipAddressPools", "ipAddressPoolSelectors", "interfaces", "nodeSelectors"),
        "NodePool": ("clusterName", "replicas"),
    }.get(kind, ())
    if safe_spec:
        result["spec"] = {key: item for key, item in value.get("spec", {}).items() if key in safe_spec}
    for key in (
        "addressType",
        "endpoints",
        "ports",
        "involvedObject",
        "reason",
        "message",
        "type",
        "firstTimestamp",
        "lastTimestamp",
        "eventTime",
        "count",
        "series",
    ):
        if key in value:
            result[key] = value[key]
    return result


def _collect_state(directory: Path, name: str, command: Callable[..., str], *args: str) -> dict[str, Any]:
    try:
        data = json.loads(command("get", *args, "-o", "json"))
        write_json(directory, f"{name}.json", _resource_state(data))
        return data
    except Exception as exc:
        record_error(directory, name, f"{type(exc).__name__}: {exc}")
        return {}


def _pod_logs(
    directory: Path, prefix: str, namespace: str, pods: dict[str, Any], command: Callable[..., str], window: Window
) -> None:
    since = window.log_start.isoformat().replace("+00:00", "Z")
    for pod in pods.get("items", []):
        name = pod["metadata"]["name"]
        pod_name = _safe(name)
        if pod_name != name:
            # Preserve identity when a long pod name is shortened for the filename.
            pod_name = f"{pod_name[:63]}-{hashlib.sha256(name.encode()).hexdigest()[:16]}"
        spec = pod.get("spec", {})
        for container in [*spec.get("containers", []), *spec.get("initContainers", [])]:
            for previous in (False, True):
                filename = f"{prefix}__{pod_name}__{_safe(container['name'])}__{'previous' if previous else 'current'}"
                try:
                    window.timeout()
                    text = command(
                        "logs",
                        name,
                        "-n",
                        namespace,
                        "-c",
                        container["name"],
                        "--timestamps",
                        f"--since-time={since}",
                        "--tail=2000",
                        *(["--previous"] if previous else []),
                    )
                    write_text(directory, f"{filename}.log", text)
                except Exception as exc:
                    record_error(directory, filename, f"{type(exc).__name__}: {exc}")
                if window.deadline is not None and time.monotonic() >= window.deadline:
                    return


def _hosted_references(orders: dict[str, Any]) -> list[tuple[str, str]]:
    return sorted(
        {
            (ref["namespace"], ref["hostedClusterName"])
            for order in orders.get("items", [])
            if (ref := (order.get("status") or {}).get("clusterReference") or {}).get("namespace")
            and ref.get("hostedClusterName")
        }
    )


def caas_source(k8s: K8sClient, namespace: str) -> SourceFn:
    """CaaS resources and hosted/controller namespace logs while the test's workers still exist."""

    def source(directory: Path, window: Window) -> None:
        def command(*args: str) -> str:
            return run(*k8s.base_args(), *args, timeout=window.timeout(CAAS_COMMAND_TIMEOUT_S))

        orders = _collect_state(
            directory, "caas-clusterorders", command, "clusterorders.osac.openshift.io", "-n", namespace
        )
        agent_namespace = os.environ.get("OSAC_AGENT_NAMESPACE") or namespace
        for resource, target in (
            ("agents.agent-install.openshift.io", agent_namespace),
            ("infraenvs.agent-install.openshift.io", namespace),
        ):
            _collect_state(directory, f"caas-{target}-{resource}", command, resource, "-n", target)
        namespaces = {
            namespace,
            agent_namespace,
            "multicluster-engine",
            "hypershift",
            *os.environ.get("OSAC_LOG_NAMESPACES", "").split(),
        }
        hosted_namespaces = set()
        for hosted_namespace, name in _hosted_references(orders):
            hosted_namespaces.update((hosted_namespace, f"{hosted_namespace}-{name}"))
            for resource, target in (
                ("hostedclusters.hypershift.openshift.io", hosted_namespace),
                ("nodepools.hypershift.openshift.io", hosted_namespace),
                ("hostedcontrolplanes.hypershift.openshift.io", f"{hosted_namespace}-{name}"),
            ):
                _collect_state(directory, f"caas-{target}-{resource}", command, resource, "-n", target)
        namespaces.update(hosted_namespaces)
        pods_by_namespace = {}
        for target in sorted(namespaces, key=lambda target: (target not in hosted_namespaces, target)):
            prefix = f"caas-{target}"
            pods_by_namespace[target] = _collect_state(directory, f"{prefix}-pods", command, "pods", "-n", target)
            _collect_state(directory, f"{prefix}-events", command, "events", "-n", target)
        for target, pods in pods_by_namespace.items():
            _pod_logs(directory, f"caas-{target}", target, pods, command, window)

    return source


def caas_guest_source(k8s: K8sClient, namespace: str) -> SourceFn:
    """One-shot guest reads using only each referenced HCP credential, held in memory."""

    def source(directory: Path, window: Window) -> None:
        __tracebackhide__ = True

        def management(*args: str) -> str:
            return run(*k8s.base_args(), *args, timeout=window.timeout(CAAS_COMMAND_TIMEOUT_S))

        orders = json.loads(management("get", "clusterorders.osac.openshift.io", "-n", namespace, "-o", "json"))
        references = _hosted_references(orders)
        if not references:
            record_error(directory, "caas-guest", "skipped: ClusterOrders have no hosted-cluster references")
        for hosted_namespace, name in references:
            hcp_namespace = f"{hosted_namespace}-{name}"
            prefix = f"guest-{hcp_namespace}"
            try:
                hcp = json.loads(management("get", "hostedcontrolplane", name, "-n", hcp_namespace, "-o", "json"))
                reference = (hcp.get("status") or {}).get("kubeConfig") or {}
                if not reference.get("name") or not reference.get("key"):
                    record_error(directory, prefix, "HostedControlPlane has no kubeconfig reference")
                    continue
                secret = json.loads(management("get", "secret", reference["name"], "-n", hcp_namespace, "-o", "json"))
                kubeconfig = base64.b64decode(secret["data"][reference["key"]], validate=True)
                if not kubeconfig:
                    record_error(directory, prefix, "referenced guest kubeconfig is empty")
                    continue
            except Exception as exc:
                record_error(directory, prefix, f"guest credential unavailable ({type(exc).__name__})")
                continue

            def guest(*args: str, binary: str = "kubectl", credential: bytes = kubeconfig) -> str:
                __tracebackhide__ = True
                argv = [binary, "--kubeconfig=/dev/stdin", *args]
                try:
                    result = subprocess.run(
                        argv,
                        input=credential,
                        capture_output=True,
                        timeout=window.timeout(CAAS_COMMAND_TIMEOUT_S),
                        check=False,
                    )
                except subprocess.TimeoutExpired:
                    raise TimeoutError("guest API command timed out") from None
                if result.returncode:
                    raise CommandFailed(result.returncode, argv, stderr=result.stderr)
                return result.stdout.decode()

            nodes = {}
            for resource, scope in (
                ("nodes", ()),
                ("certificatesigningrequests.certificates.k8s.io", ()),
                ("events", ("-A",)),
                ("clusterversions.config.openshift.io", ()),
                ("clusteroperators.config.openshift.io", ()),
                ("services", ("-n", "openshift-ingress")),
                ("endpointslices.discovery.k8s.io", ("-n", "openshift-ingress")),
                ("ipaddresspools.metallb.io", ("-n", "metallb-system")),
                ("l2advertisements.metallb.io", ("-n", "metallb-system")),
            ):
                data = _collect_state(directory, f"{prefix}-{resource}", guest, resource, *scope)
                if resource == "nodes":
                    nodes = data
            guest_pods = {
                target: _collect_state(directory, f"{prefix}-{target}-pods", guest, "pods", "-n", target)
                for target in ("openshift-ingress", "metallb-system")
            }
            for node in nodes.get("items", []):
                node_name = node["metadata"]["name"]
                try:
                    text = guest(
                        "adm",
                        "node-logs",
                        node_name,
                        "--unit=kubelet",
                        "--unit=crio",
                        "--tail=300",
                        f"--since={window.log_start.astimezone(UTC).strftime('%Y-%m-%d %H:%M:%S')}",
                        binary="oc",
                    )
                    write_text(directory, f"{prefix}-{_safe(node_name)}-node.log", text)
                except Exception as exc:
                    record_error(directory, f"{prefix}:{node_name}", f"{type(exc).__name__}: {exc}")
            for target, pods in guest_pods.items():
                _pod_logs(directory, f"{prefix}-{target}", target, pods, guest, window)

    return source


def fabric_source(command: str | None = None) -> SourceFn | None:
    """Run the backend's own snapshot command. None when `OSAC_E2E_FABRIC_SNAPSHOT_CMD` is unset."""
    command = command if command is not None else os.environ.get(FABRIC_COMMAND_ENV, "")
    if not command.strip():
        return None

    def source(directory: Path, window: Window) -> None:
        try:
            result = subprocess.run(
                shlex.split(command), capture_output=True, text=True, timeout=window.timeout(), check=False
            )
        except subprocess.TimeoutExpired as exc:
            record_error(directory, "fabric", f"timed out after {exc.timeout:g}s")
            return
        write_text(directory, "fabric.json", result.stdout)
        if result.stderr.strip():
            record_error(directory, "fabric", result.stderr.strip())
        if result.returncode != 0:
            record_error(directory, "fabric", f"exited {result.returncode}")

    return source


def guest_source(guests: Callable[[], list[tuple[str, Callable[[str, int], tuple[str, int]]]]]) -> SourceFn:
    """Network state of each guest, one after another: 10s per guest and a stop after 45s in total.

    `guests` returns (name, run) pairs, where `run(command, timeout)` returns (output, status).
    """

    def source(directory: Path, window: Window) -> None:
        deadline = min(time.monotonic() + GUEST_TOTAL_BUDGET_S, window.deadline or float("inf"))
        for name, run_command in guests():
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                record_error(directory, f"guest:{name}", "skipped: guest budget used up")
                continue
            try:
                output, status = run_command(GUEST_COMMAND, int(min(GUEST_PER_VM_TIMEOUT_S, remaining)))
            except Exception as exc:
                record_error(directory, f"guest:{name}", f"{type(exc).__name__}: {exc}")
            else:
                write_text(directory, f"guest-{_safe(name)}.txt", f"exit status {status}\n{output}\n")

    return source


def register_generic_sources(
    sources: SnapshotSources, *, private_grpc: GRPCClient, k8s: K8sClient, namespace: str
) -> list[str]:
    """Register api, k8s, logs and (when configured) fabric; return the names so the caller can remove them."""
    registered = {
        "api": api_source(private_grpc),
        "k8s": k8s_source(k8s, namespace),
        "logs": logs_source(k8s, namespace),
    }
    fabric = fabric_source()
    if fabric is not None:
        registered["fabric"] = fabric
    for name, fn in registered.items():
        sources.register(name, fn)
    return list(registered)
