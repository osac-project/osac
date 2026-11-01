"""Create, wait and teardown helpers for the Netris VMaaS networking suite.

This module holds no tests. The test module records every ID in a dict before it waits
on the object, so a failed wait leaves something for the fixture finalizer to clean.
"""

from __future__ import annotations

import ipaddress
import os
import re
import shutil
import subprocess
import threading
from collections.abc import Callable, Sequence
from concurrent.futures import ThreadPoolExecutor
from typing import Any, TypeVar

import pytest

from tests.e2e.core import failure_snapshot
from tests.e2e.core.failures import E2EFailure, Kind
from tests.e2e.core.grpc_client import PRIVATE_API, PUBLIC_API, GRPCClient
from tests.e2e.core.helpers import (
    wait_for_cr,
    wait_for_external_ip_pool_cr,
    wait_for_external_ip_pool_grpc_ready,
    wait_for_external_ip_pool_ready,
    wait_for_grpc_security_group_ready,
    wait_for_grpc_subnet_ready,
    wait_for_grpc_virtual_network_ready,
    wait_for_provision,
    wait_for_running,
    wait_for_security_group_cr,
    wait_for_security_group_ready,
    wait_for_subnet_cr,
    wait_for_subnet_ready,
    wait_for_virtual_network_cr,
    wait_for_virtual_network_ready,
    wait_for_vmi_ip,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import poll_until
from tests.e2e.core.wait_kinds import WaitResourceKind
from tests.e2e.networking.cidrs import TENANT_SECOND_OCTETS, tenant_vn_cidr
from tests.e2e.networking.teardown import teardown_sg, teardown_subnets, teardown_vnet, wait_owned_gone
from tests.e2e.networking.vmaas import guest_ssh

T = TypeVar("T")

# Local debugging only: set it to true to keep what the suite created after the run. No job sets it.
KEEP_RESOURCES_ENV = "OSAC_E2E_KEEP_RESOURCES"

EXTERNAL_PATH_HINT = (
    "Check on the Netris lab: the VPC VRF and the VNIs exist on the SoftGates and leaves (Netris deploys them "
    "only with member ports), the osac-pool/osac-eip/osac-natgw/osac-eipa objects exist, the hub imports the "
    "type-5 route, and the host routes the pool via the ISP."
)

_NETWORKS_PER_WORKER = 3


def network_cidrs() -> tuple[str, str, str]:
    """Return the three tenant /16 networks reserved for this xdist worker; no two workers share one."""
    worker_id = os.environ.get("PYTEST_XDIST_WORKER", "gw0")
    match = re.fullmatch(r"gw(\d+)", worker_id)
    first = (int(match.group(1)) if match else 0) * _NETWORKS_PER_WORKER
    if first + _NETWORKS_PER_WORKER > len(TENANT_SECOND_OCTETS):
        raise RuntimeError(f"Worker {worker_id} has no reserved VMaaS Netris test CIDR")
    return tuple(tenant_vn_cidr(first + n) for n in range(_NETWORKS_PER_WORKER))  # type: ignore[return-value]


def require_host_tools() -> None:
    """Fail before creating anything when the pytest host lacks a tool the suite shells out to."""
    missing = [tool for tool in ("kubectl", "ssh", "ssh-keygen") if shutil.which(tool) is None]
    if missing:
        pytest.fail(
            f"The pytest host lacks {', '.join(missing)} on PATH, and the suite cannot run without it. "
            "Install kubectl (or oc) and the OpenSSH client."
        )


def keep_resources() -> bool:
    """True when a person debugging on a live lab wants the suite's resources left in place."""
    return os.environ.get(KEEP_RESOURCES_ENV, "").strip().lower() == "true"


def run_parallel(calls: Sequence[Callable[[], T]]) -> list[T]:
    """Run independent calls at the same time; return their results in submit order.

    The `with` block returns only when every worker has finished, so a fixture finalizer never races a
    worker. Every future is read in submit order and the first failure is re-raised after that.
    `pytest.fail` raises a BaseException, so it is caught and re-raised like any other failure.
    Use it only inside one dependency level: a child must never be submitted with its own parent.
    """
    if not calls:
        return []
    results: list[T] = []
    first_failure: BaseException | None = None
    with ThreadPoolExecutor(max_workers=len(calls)) as executor:
        futures = [executor.submit(call) for call in calls]
        for future in futures:
            try:
                results.append(future.result())
            except BaseException as exc:
                if first_failure is None:
                    first_failure = exc
    if first_failure is not None:
        raise first_failure
    return results


_STAGE_LOCK = threading.Lock()


def run_stage(
    items: Sequence[tuple[str, Callable[[], object]] | tuple[str, Callable[[], object], Callable[[], object]]],
    errors: list[str] | None = None,
) -> list[bool]:
    """Run the independent items of one teardown stage at the same time; return which of them succeeded.

    An item is `(label, delete)` or `(label, delete, forget)`. `forget` runs right after that item's `delete`
    succeeded, under a lock, so a stage that fails partway has forgotten exactly what it deleted.
    Without `errors` the first failure in submit order is raised once every item has finished (a test).
    With `errors` each failure is recorded as `<label>: <ExceptionType>` and the stage goes on (a finalizer).
    """

    def guarded(item: tuple[Any, ...]) -> Callable[[], bool]:
        label, delete = item[0], item[1]
        forget = item[2] if len(item) > 2 else None

        def run() -> bool:
            try:
                delete()
            except Exception as exc:
                if errors is None:
                    raise
                errors.append(f"{label}: {type(exc).__name__}")
                return False
            if forget is not None:
                with _STAGE_LOCK:
                    forget()
            return True

        return run

    return run_parallel([guarded(item) for item in items])


def poll_or_fail(
    *,
    hint: str,
    fn: Callable[[], T],
    until: Callable[[T], bool],
    description: str,
    resource_kind: WaitResourceKind,
    resource_id: str,
    retries: int = 60,
    delay: int = 5,
    kind: Kind | None = None,
    ids: dict[str, str] | None = None,
    refine_kind: Callable[[Kind], tuple[Kind, str]] | None = None,
) -> T:
    """`poll_until`, with a timeout turned into a failure that carries a diagnosis.

    `kind` names what the timeout means (for example traffic-blocked) and `ids` the objects involved; without
    a kind the failure is a plain `pytest.fail`, which still classifies as stuck-state.
    """
    try:
        return poll_until(
            fn=fn,
            until=until,
            retries=retries,
            delay=delay,
            description=description,
            resource_kind=resource_kind,
            resource_id=resource_id,
        )
    except TimeoutError as exc:
        message = f"{description} timed out. {hint}\n{exc}"
        if kind is None:
            pytest.fail(message)
        if refine_kind is not None:
            kind, note = refine_kind(kind)
            message += note
        raise E2EFailure(kind, message, ids=ids) from exc


def traffic_kind_for_guest(vm_namespace: str, vm_name: str, identity_file: str) -> Callable[[Kind], tuple[Kind, str]]:
    """For a wait that expects traffic to get through: a guest we cannot log in to says nothing on the network.

    The wait itself stays as it is. After it times out, `guest_ssh.login_problem` decides: a problem makes the
    failure test-ssh-failed, otherwise the given kind (traffic-blocked) stands.
    """

    def refine(kind: Kind) -> tuple[Kind, str]:
        try:
            problem = guest_ssh.login_problem(vm_namespace, vm_name, identity_file)
        except Exception as exc:  # the check must not hide the timeout it explains
            problem = f"the login check itself failed: {type(exc).__name__}"
        if problem is None:
            return kind, ""
        return (
            Kind.TEST_SSH_FAILED,
            f"\nOur own SSH to the guest is failing, so this says nothing on the network: {problem}",
        )

    return refine


def register_guest_snapshot(name: str, instances: list[dict[str, str]], identity_file: str) -> None:
    """Add the guests in `instances` to the failure snapshot; the list is read at failure time."""

    def runner_for(vm: dict[str, str]) -> Callable[[str, int], tuple[str, int]]:
        return lambda command, timeout: guest_ssh.ssh_guest_unchecked(
            vm_namespace=vm["vm_namespace"],
            vm_name=vm["cr"],
            identity_file=identity_file,
            command=command,
            timeout=timeout,
        )

    def guests() -> list[tuple[str, Callable[[str, int], tuple[str, int]]]]:
        return [(vm["name"], runner_for(vm)) for vm in list(instances) if vm.get("vm_namespace") and vm.get("cr")]

    failure_snapshot.SOURCES.register(name, failure_snapshot.guest_source(guests), kinds=failure_snapshot.GUEST_KINDS)


def unregister_guest_snapshot(name: str) -> None:
    failure_snapshot.SOURCES.unregister(name)


def create_network(
    *,
    client: GRPCClient,
    k8s_hub_client: K8sClient,
    networks: list[dict[str, str]],
    label: str,
    vn_cidr: str,
    subnet_cidr: str,
    run_id: str,
    ingress: list[dict[str, Any]],
    egress: list[dict[str, Any]],
) -> dict[str, str]:
    """Create a VirtualNetwork, wait for it, then its Subnet and SecurityGroup at once; wait until all are READY."""
    vnet_name = f"netris-vn-{label}-{run_id}"
    vnet_id = client.create_virtual_network(name=vnet_name, ipv4_cidr=vn_cidr)
    network: dict[str, str] = {"label": label, "vnet_id": vnet_id, "vnet_name": vnet_name, "vn_cidr": vn_cidr}
    networks.append(network)
    network["vnet_cr"] = wait_for_virtual_network_cr(k8s=k8s_hub_client, uuid=vnet_id)
    wait_for_virtual_network_ready(k8s=k8s_hub_client, name=network["vnet_cr"])
    # The server rejects a child of a VirtualNetwork that is not READY in the API, whatever the CR says.
    wait_for_grpc_virtual_network_ready(grpc=client, vn_id=vnet_id)

    # The Subnet and the SecurityGroup need only the READY VirtualNetwork, not each other. Each records its ID
    # before it waits and writes distinct keys of `network`.
    def create_subnet() -> None:
        sub_id = client.create_subnet(
            name=f"netris-subnet-{label}-{run_id}", virtual_network=vnet_id, ipv4_cidr=subnet_cidr
        )
        network.update(sub_a_id=sub_id, subnet_cidr=subnet_cidr)
        network["subnet_cr"] = wait_for_subnet_cr(k8s=k8s_hub_client, uuid=sub_id)
        wait_for_subnet_ready(k8s=k8s_hub_client, name=network["subnet_cr"])
        wait_for_grpc_subnet_ready(grpc=client, subnet_id=sub_id)

    def create_group() -> None:
        sg_id = client.create_security_group_with_rules(
            name=f"netris-sg-{label}-{run_id}", virtual_network=vnet_id, ingress=ingress, egress=egress
        )
        network["sg_id"] = sg_id
        network["sg_cr"] = wait_for_security_group_cr(k8s=k8s_hub_client, uuid=sg_id)
        wait_for_security_group_ready(k8s=k8s_hub_client, name=network["sg_cr"])
        wait_for_grpc_security_group_ready(grpc=client, sg_id=sg_id)

    run_parallel([create_subnet, create_group])
    return network


def create_security_group(
    *,
    client: GRPCClient,
    k8s_hub_client: K8sClient,
    network: dict[str, str],
    key: str,
    name: str,
    ingress: list[dict[str, Any]],
    egress: list[dict[str, Any]],
) -> None:
    """Create an extra SecurityGroup on `network`, recorded as `<key>_sg_id` and `<key>_sg_cr`."""
    sg_id = client.create_security_group_with_rules(
        name=name, virtual_network=network["vnet_id"], ingress=ingress, egress=egress
    )
    network[f"{key}_sg_id"] = sg_id
    network[f"{key}_sg_cr"] = wait_for_security_group_cr(k8s=k8s_hub_client, uuid=sg_id)
    wait_for_security_group_ready(k8s=k8s_hub_client, name=network[f"{key}_sg_cr"])
    wait_for_grpc_security_group_ready(grpc=client, sg_id=sg_id)


def create_vm(
    *,
    cli: OsacCLI,
    k8s_hub_client: K8sClient,
    instances: list[dict[str, str]],
    name: str,
    network: dict[str, str],
    sg_id: str,
    key_secret: str,
    vm_template: str,
    instance_type: str,
    disk_image: str,
    storage_tier: str,
    external_ip_attachment: bool = False,
) -> dict[str, str]:
    """Create a ComputeInstance on the network's Subnet; the record is appended before the CR wait."""
    ci_id = cli.create_compute_instance(
        template=vm_template,
        name=name,
        network_attachments=[{"subnet": network["sub_a_id"], "security_groups": [sg_id]}],
        ssh_key=key_secret,
        external_ip_attachment=external_ip_attachment,
        instance_type=instance_type,
        disk_image=disk_image,
        boot_disk_storage_tier=storage_tier,
    )
    instance: dict[str, str] = {
        "id": ci_id,
        "name": name,
        "subnet_cidr": network["subnet_cidr"],
        "network": network["label"],
        "vnet_id": network["vnet_id"],
    }
    instances.append(instance)
    instance["cr"] = wait_for_cr(k8s=k8s_hub_client, uuid=ci_id)
    return instance


def wait_for_vm(
    *, k8s_hub_client: K8sClient, k8s_virt_client: K8sClient, instance: dict[str, str], identity_file: str
) -> None:
    """Wait for the VM to run, take an address from its Subnet, answer SSH and carry the guest tools."""
    wait_for_provision(k8s=k8s_hub_client, name=instance["cr"])
    wait_for_running(k8s=k8s_hub_client, name=instance["cr"])
    instance["vm_namespace"] = poll_until(
        fn=lambda: k8s_hub_client.get_compute_instance_vm_namespace(name=instance["cr"]),
        until=bool,
        retries=60,
        delay=5,
        description=f"VMI namespace for {instance['name']} to be set",
        resource_kind=WaitResourceKind.COMPUTE_INSTANCE,
        resource_id=instance["cr"],
    )
    guest_ssh.note_namespace(instance["vnet_id"], instance["vm_namespace"])
    instance["ip"] = wait_for_vmi_ip(
        k8s=k8s_virt_client, vmi_namespace=instance["vm_namespace"], compute_instance_name=instance["cr"]
    )
    assert ipaddress.IPv4Address(instance["ip"]) in ipaddress.IPv4Network(instance["subnet_cidr"]), (
        f"ComputeInstance {instance['name']} IP {instance['ip']} is outside {instance['subnet_cidr']}"
    )
    guest_ssh.wait_for_guest_ssh(instance["vm_namespace"], instance["cr"], identity_file)
    guest_ssh.require_guest_tools(instance["vm_namespace"], instance["cr"], identity_file)


def delete_vm(*, client: GRPCClient, k8s_hub_client: K8sClient, instance: dict[str, str]) -> None:
    """Delete a ComputeInstance through the owning tenant's client; safe when it is already gone."""
    wait_owned_gone(
        description=f"ComputeInstance {instance['name']} gone",
        api_gone=lambda: instance["id"] not in client.list_compute_instance_ids(),
        cr_gone=lambda: (
            not instance.get("cr") or not k8s_hub_client.is_present(resource="computeinstance", name=instance["cr"])
        ),
        delete=lambda: client.delete_compute_instance(ci_id=instance["id"]),
        retries=120,
        delay=5,
        resource_kind=WaitResourceKind.COMPUTE_INSTANCE,
        resource_id=instance["id"],
    )


def delete_network(*, client: GRPCClient, k8s_hub_client: K8sClient, network: dict[str, str]) -> None:
    """Delete the SSH helpers, SecurityGroups, Subnet and VirtualNetwork recorded on `network`, in that order."""
    helper_error: Exception | None = None
    try:
        guest_ssh.remove_helpers_for(network["vnet_id"])
    except RuntimeError as exc:
        # Keep deleting the rest; a leftover Pod can pin the network, which the final error then explains.
        helper_error = exc
    if network.get("restricted_sg_id"):
        teardown_sg(client, k8s_hub_client, {"sg_id": network["restricted_sg_id"], "vnet_name": network["vnet_name"]})
    teardown_sg(client, k8s_hub_client, network)
    teardown_subnets(client, k8s_hub_client, network)
    teardown_vnet(client, k8s_hub_client, network)
    if helper_error is not None:
        raise helper_error


def resolve_external_ip_pool(
    *, private_grpc: GRPCClient, k8s_hub_client: K8sClient, name: str, cidr: str, record: dict[str, Any] | None = None
) -> dict[str, Any]:
    """Adopt the pool whose CIDRs are exactly `[cidr]`, or create it.

    Any other READY pool is not accepted: this suite needs the routed one. `record`, when given,
    is filled in place as soon as the ID is known, so a failed wait still leaves the ID to clean.
    """
    pool = record if record is not None else {}
    items = private_grpc.call(service=f"{PRIVATE_API}.ExternalIPPools/List").get("items", [])
    existing = next((item for item in items if item.get("spec", {}).get("cidrs") == [cidr]), None)
    if existing is not None:
        pool.update(id=existing["id"], cidr=cidr, owned=False)
        pool["cr"] = wait_for_external_ip_pool_cr(k8s=k8s_hub_client, uuid=existing["id"])
        if existing.get("status", {}).get("state") != "EXTERNAL_IP_POOL_STATE_READY":
            wait_for_external_ip_pool_ready(k8s=k8s_hub_client, name=pool["cr"])
            wait_for_external_ip_pool_grpc_ready(private_grpc=private_grpc, pool_id=pool["id"])
        return pool
    pool_id = private_grpc.create_external_ip_pool(name=name, cidrs=[cidr])
    pool.update(id=pool_id, cidr=cidr, owned=True)
    pool["cr"] = wait_for_external_ip_pool_cr(k8s=k8s_hub_client, uuid=pool_id)
    wait_for_external_ip_pool_ready(k8s=k8s_hub_client, name=pool["cr"])
    wait_for_external_ip_pool_grpc_ready(private_grpc=private_grpc, pool_id=pool_id)
    return pool


def _route_tokens(line: str) -> tuple[str | None, str | None]:
    tokens = line.split()

    def after(keyword: str) -> str | None:
        return tokens[tokens.index(keyword) + 1] if keyword in tokens[:-1] else None

    return after("via"), after("dev")


def route_uses_default(route_get: str, default_route: str) -> bool:
    """True when `ip -o route get <ip>` leaves through the same `via` and `dev` as a default route."""
    chosen = _route_tokens(route_get.strip().splitlines()[0]) if route_get.strip() else (None, None)
    if chosen == (None, None):
        return False
    return any(_route_tokens(line) == chosen for line in default_route.splitlines() if line.strip())


def assert_pool_routed(pool_cidr: str) -> None:
    """Fail unless the pytest host has a specific route for the pool, not just its default route."""
    first_host = str(next(ipaddress.ip_network(pool_cidr, strict=False).hosts()))
    message = (
        f"pytest host has no route for ExternalIP pool {pool_cidr}; run pytest on the lab host "
        "or set OSAC_EXTERNAL_IP_POOL_CIDR to a routed range"
    )
    outputs: list[str] = []
    for command in (["ip", "-o", "route", "get", first_host], ["ip", "-o", "route", "show", "default"]):
        try:
            result = subprocess.run(command, capture_output=True, text=True, timeout=15, check=False)
        except (OSError, subprocess.TimeoutExpired) as exc:
            pytest.fail(f"{message} (`{' '.join(command)}` failed to run: {exc})")
        if result.returncode != 0:
            pytest.fail(f"{message} (`{' '.join(command)}` exited {result.returncode}: {result.stderr.strip()})")
        outputs.append(result.stdout)
    if route_uses_default(outputs[0], outputs[1]):
        pytest.fail(message)


def find_auto_attachment(client: GRPCClient, ci_id: str) -> dict[str, Any] | None:
    response = client.call(service=f"{PUBLIC_API}.ExternalIPAttachments/List")
    return next(
        (
            item
            for item in response.get("items", [])
            if item.get("spec", {}).get("computeInstance", {}).get("id") == ci_id
        ),
        None,
    )


def external_ip_address(client: GRPCClient, eip_id: str) -> str:
    """Wait for the allocated address of an ExternalIP."""
    return poll_or_fail(
        hint=EXTERNAL_PATH_HINT,
        fn=lambda: client.get_external_ip(external_ip_id=eip_id).get("object", {}).get("status", {}).get("address", ""),
        until=bool,
        retries=30,
        delay=5,
        description=f"ExternalIP {eip_id} address to be allocated",
        resource_kind=WaitResourceKind.EXTERNAL_IP,
        resource_id=eip_id,
    )


def wait_for_attribution(*, private_grpc: GRPCClient, eip_id: str, ci_id: str) -> None:
    """Wait until the operator reports the ExternalIP as attributed to the ComputeInstance."""
    poll_or_fail(
        hint=EXTERNAL_PATH_HINT,
        fn=lambda: private_grpc.get_private_external_ip(external_ip_id=eip_id)["object"].get("status", {}),
        until=lambda status: (
            status.get("attribution", {}).get("computeInstance", {}).get("id") == ci_id
            and bool(status.get("attachmentTransitionTime"))
        ),
        retries=30,
        delay=5,
        description=f"ExternalIP {eip_id} attribution to ComputeInstance {ci_id}",
        resource_kind=WaitResourceKind.EXTERNAL_IP,
        resource_id=eip_id,
    )


def check_pool_ready(private_grpc: GRPCClient, pool: dict[str, Any], *, cidr: str, needed: int) -> None:
    """Assert the routed pool is READY with at least `needed` free addresses.

    Also gives the pool an empty `suite_eip_ids` set. Every ExternalIP the suite creates is added to it, so the
    teardown can prove that none of them is left. Tenant default ExternalIPs also draw from the pool and appear
    and disappear on their own, so the pool's own count at this moment is not recorded.
    """
    status = private_grpc.get_external_ip_pool(pool_id=pool["id"])["object"].get("status", {})
    assert status.get("state") == "EXTERNAL_IP_POOL_STATE_READY", f"ExternalIPPool {pool['id']} is not READY: {status}"
    available = int(status.get("available", 0))
    assert available >= needed, (
        f"ExternalIPPool {pool['id']} ({cidr}) has {available} free addresses and this suite needs {needed}; "
        "an earlier run probably leaked ExternalIPs"
    )
    pool["suite_eip_ids"] = set()


def wait_for_grpc_external_ip_allocated(client: GRPCClient, eip_id: str) -> str:
    """Wait until the API reports the ExternalIP ALLOCATED with an address; a NATGateway or attachment needs that."""
    return poll_or_fail(
        hint=EXTERNAL_PATH_HINT,
        fn=lambda: (
            lambda status: status.get("address", "") if status.get("state") == "EXTERNAL_IP_STATE_ALLOCATED" else ""
        )(client.get_external_ip(external_ip_id=eip_id).get("object", {}).get("status", {})),
        until=bool,
        retries=30,
        delay=5,
        description=f"ExternalIP {eip_id} API state ALLOCATED",
        resource_kind=WaitResourceKind.EXTERNAL_IP,
        resource_id=eip_id,
    )
