"""VMaaS networking E2E for a CUDN EVPN network class, at parity with the BMaaS suites.

One ordered module, four phases:

- A and B (test_00..08): connectivity, isolation, DNS and SecurityGroup enforcement. No external path.
- C (test_09..13): NATGateway egress and ExternalIP ingress for two tenants. The routed ExternalIPPool is
  checked by the `netris_external_path` fixture, so a lab without a routed pool errors the phase.
- D (test_14): one teardown test, ordered stages, that must leave nothing behind.

Setup runs the independent resources at the same time (`stack.run_parallel`), but a child never starts
before its own parent is READY: VirtualNetwork, then Subnet and SecurityGroup, then VMs. Teardown is the
reverse. The job stops at the first failed test (`-x`). The suite is ordered and cannot be sliced with -k.

Inter-subnet routing is not covered: every VirtualNetwork here has one Subnet.
"""

from __future__ import annotations

import contextlib
import ipaddress
import logging
import subprocess
import time
from collections.abc import Callable, Iterator, Sequence
from functools import partial
from pathlib import Path
from typing import Any

import pytest

from tests.e2e.core import failure_report
from tests.e2e.core.failures import E2EFailure, Kind
from tests.e2e.core.grpc_client import PUBLIC_API, GRPCClient
from tests.e2e.core.helpers import (
    wait_for_external_ip_allocated,
    wait_for_external_ip_attachment_cr,
    wait_for_external_ip_attachment_deletion,
    wait_for_external_ip_attachment_ready,
    wait_for_external_ip_cr,
    wait_for_external_ip_deletion,
    wait_for_nat_gateway_ready,
    wait_for_security_group_update,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import StuckState, poll_until
from tests.e2e.networking import cidrs
from tests.e2e.networking.teardown import nat_gateway_ids, teardown_nat, teardown_nat_eip, wait_owned_gone
from tests.e2e.networking.vmaas import guest_ssh, stack
from tests.e2e.networking.vmaas.cleanup import _run_cleanup
from tests.e2e.vmaas.conftest import DEFAULT_DISK_IMAGE_SOURCE_REF

pytestmark = [pytest.mark.regression]

_LOG = logging.getLogger(__name__)
_DEFAULT_LABEL = "osac.openshift.io/default"

H = stack.EXTERNAL_PATH_HINT

_ALL = {"protocol": "PROTOCOL_ALL", "ipv4_cidr": "0.0.0.0/0"}
_TCP_22 = {"protocol": "PROTOCOL_TCP", "port_from": 22, "port_to": 22, "ipv4_cidr": "0.0.0.0/0"}
_LISTENER_PORTS = (8080, 8081)


def _tcp(port: int, cidr: str = "0.0.0.0/0") -> dict[str, Any]:
    return {"protocol": "PROTOCOL_TCP", "port_from": port, "port_to": port, "ipv4_cidr": cidr}


# The first group allows ssh and all (all includes the ICMP the connectivity checks send). The restricted
# group allows only tcp 22 and 8080.
_OPEN_INGRESS = [_TCP_22, _ALL]
_RESTRICTED_INGRESS = [_TCP_22, _tcp(8080)]


def _raise_cleanup_errors(errors: list[str]) -> None:
    if errors:
        raise RuntimeError("E2E resource cleanup failed: " + "; ".join(errors))


def _generate_key(directory: Path) -> tuple[Path, Path]:
    private_key = directory / "id_ed25519"
    subprocess.run(
        ["ssh-keygen", "-t", "ed25519", "-f", str(private_key), "-N", "", "-C", "osac-vmaas-netris-e2e"],
        check=True,
        capture_output=True,
        text=True,
    )
    return private_key, private_key.with_suffix(".pub")


@contextlib.contextmanager
def _diagnose(description: str) -> Iterator[None]:
    """Turn a timeout of a canned wait into a failure that carries the external-path diagnosis."""
    try:
        yield
    except TimeoutError as exc:
        pytest.fail(f"{description} timed out. {H}\n{exc}")


# Teardown stages. A test runs them without `errors` (the first failure is raised after the stage finished);
# a finalizer passes `errors` and goes on. Either way a stage deletes at the same time only what does not
# depend on each other. Each item leaves its state list right after its own delete succeeded (under a lock), so a
# stage that fails partway has forgotten exactly what it deleted and the finalizer deletes only the rest.


def _delete_instances(
    *,
    targets: Sequence[tuple[GRPCClient, list[dict[str, str]]]],
    k8s_hub_client: K8sClient,
    errors: list[str] | None = None,
) -> None:
    """Delete the ComputeInstances of every (client, instances) target at once."""
    stack.run_stage(
        [
            (
                f"ComputeInstance {instance['id']}",
                partial(stack.delete_vm, client=client, k8s_hub_client=k8s_hub_client, instance=instance),
                partial(instances.remove, instance),
            )
            for client, instances in targets
            for instance in list(instances)
        ],
        errors,
    )


def _delete_networks(
    *,
    targets: Sequence[tuple[GRPCClient, list[dict[str, str]]]],
    k8s_hub_client: K8sClient,
    errors: list[str] | None = None,
    after_delete: Callable[[dict[str, str]], None] | None = None,
) -> None:
    """Delete the networks of every (client, networks) target at once; `after_delete` runs right after each."""

    def delete(client: GRPCClient, network: dict[str, str]) -> None:
        stack.delete_network(client=client, k8s_hub_client=k8s_hub_client, network=network)
        if after_delete is not None:
            after_delete(network)

    stack.run_stage(
        [
            (f"network {network['vnet_name']}", partial(delete, client, network), partial(networks.remove, network))
            for client, networks in targets
            for network in list(networks)
        ],
        errors,
    )


def _delete_secrets_and_images(
    tenants: dict[str, dict[str, Any]], *, cli: OsacCLI, errors: list[str] | None = None
) -> None:
    """Delete both Secrets and the tenant2 DiskImage at once."""
    t1, t2 = tenants["t1"], tenants["t2"]

    def secret(state: dict[str, Any], client: OsacCLI) -> None:
        client.delete_secret(name=state["key_secret"])

    items: list[tuple[str, Callable[[], object], Callable[[], object]]] = []
    for who, state, client in (("", t1, cli), ("tenant2 ", t2, t2["cli"])):
        if state["secret_live"]:
            items.append(
                (
                    f"{who}SSH key Secret {state['key_secret']}",
                    partial(secret, state, client),
                    partial(state.__setitem__, "secret_live", False),
                )
            )
    if t2["disk_image_id"]:
        items.append(
            (
                f"tenant2 DiskImage {t2['disk_image_id']}",
                partial(t2["grpc"].delete_disk_image, disk_image_id=t2["disk_image_id"]),
                partial(t2.__setitem__, "disk_image_id", ""),
            )
        )
    stack.run_stage(items, errors)


def _tenant_targets(
    tenants: dict[str, dict[str, Any]], grpc: GRPCClient, key: str
) -> list[tuple[GRPCClient, list[dict[str, str]]]]:
    return [(grpc, tenants["t1"][key]), (tenants["t2"]["grpc"], tenants["t2"][key])]


def _cleanup_tenants(
    tenants: dict[str, dict[str, Any]], *, cli: OsacCLI, grpc: GRPCClient, k8s_hub_client: K8sClient, errors: list[str]
) -> None:
    """What a finalizer removes of both tenants: VMs, then Secrets and the DiskImage, then networks."""
    if stack.keep_resources():
        _LOG.warning(
            "%s is true: leaving the tenant resources in place, delete them by hand: VMs %s, networks %s",
            stack.KEEP_RESOURCES_ENV,
            [item["id"] for tenant in tenants.values() for item in tenant.get("instances", [])],
            [item["vnet_id"] for tenant in tenants.values() for item in tenant.get("networks", [])],
        )
        return
    _delete_instances(targets=_tenant_targets(tenants, grpc, "instances"), k8s_hub_client=k8s_hub_client, errors=errors)
    _delete_secrets_and_images(tenants, cli=cli, errors=errors)
    _delete_networks(targets=_tenant_targets(tenants, grpc, "networks"), k8s_hub_client=k8s_hub_client, errors=errors)


# Setup. Independent resources are created at the same time; a child starts only after its parent is READY.


def _create_networks(
    *,
    grpc: GRPCClient,
    tenant2_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    run_id: str,
    vn_cidrs: Sequence[str],
    networks: list[dict[str, str]],
    tenant2_networks: list[dict[str, str]],
) -> tuple[dict[str, str], dict[str, str], dict[str, str]]:
    """Create network a, network b and tenant2's network at once; return them as (a, b, t2).

    The three results come from the futures in submit order, never from the order the networks were
    recorded in. The restricted SecurityGroup needs network a READY, so it is part of the call that creates a.
    """

    def create(client: GRPCClient, records: list[dict[str, str]], label: str, vn_cidr: str) -> dict[str, str]:
        return stack.create_network(
            client=client,
            k8s_hub_client=k8s_hub_client,
            networks=records,
            label=label,
            vn_cidr=vn_cidr,
            subnet_cidr=cidrs.subnet_cidr(vn_cidr, 1),
            run_id=run_id,
            ingress=_OPEN_INGRESS,
            egress=[_ALL],
        )

    def create_a() -> dict[str, str]:
        network = create(grpc, networks, "a", vn_cidrs[0])
        stack.create_security_group(
            client=grpc,
            k8s_hub_client=k8s_hub_client,
            network=network,
            key="restricted",
            name=f"netris-sg-restricted-{run_id}",
            ingress=_RESTRICTED_INGRESS,
            egress=[_ALL],
        )
        return network

    network_a, network_b, network_t2 = stack.run_parallel(
        [
            create_a,
            partial(create, grpc, networks, "b", vn_cidrs[1]),
            partial(create, tenant2_grpc, tenant2_networks, "t2", vn_cidrs[2]),
        ]
    )
    return network_a, network_b, network_t2


def _create_tenant_vms(
    *,
    cli: OsacCLI,
    k8s_hub_client: K8sClient,
    instances: list[dict[str, str]],
    key_secret: str,
    disk_image: str,
    vm_template: str,
    instance_type: str,
    storage_tier: str,
    run_id: str,
    specs: Sequence[tuple[str, dict[str, str], str]],
) -> dict[str, dict[str, str]]:
    """Create the VMs of one tenant one after the other: one OsacCLI instance is not safe across threads."""
    return {
        key: stack.create_vm(
            cli=cli,
            k8s_hub_client=k8s_hub_client,
            instances=instances,
            name=f"netris-vm-{key}-{run_id}",
            network=network,
            sg_id=sg_id,
            key_secret=key_secret,
            vm_template=vm_template,
            instance_type=instance_type,
            disk_image=disk_image,
            storage_tier=storage_tier,
        )
        for key, network, sg_id in specs
    }


def _create_vms(
    *,
    t1: dict[str, Any],
    t2: dict[str, Any],
    network_a: dict[str, str],
    network_b: dict[str, str],
    network_t2: dict[str, str],
) -> tuple[dict[str, dict[str, str]], dict[str, dict[str, str]]]:
    """Create tenant1's four VMs and tenant2's one; each tenant's VMs one after the other, the tenants at once.

    `t1` and `t2` hold the keyword arguments of `_create_tenant_vms` that belong to each tenant.
    """
    specs_t1 = [
        ("a1", network_a, network_a["sg_id"]),
        ("a2", network_a, network_a["sg_id"]),
        ("b1", network_b, network_b["sg_id"]),
        ("r1", network_a, network_a["restricted_sg_id"]),
    ]
    vms_t1, vms_t2 = stack.run_parallel(
        [
            partial(_create_tenant_vms, specs=specs_t1, **t1),
            partial(_create_tenant_vms, specs=[("t2a", network_t2, network_t2["sg_id"])], **t2),
        ]
    )
    return vms_t1, vms_t2


def _wait_for_vms(
    *, k8s_hub_client: K8sClient, k8s_virt_client: K8sClient, instances: Sequence[dict[str, str]], identity_file: str
) -> None:
    """Wait for every VM at once; all of them were created, so the waits do not depend on each other."""
    stack.run_parallel(
        [
            partial(
                stack.wait_for_vm,
                k8s_hub_client=k8s_hub_client,
                k8s_virt_client=k8s_virt_client,
                instance=instance,
                identity_file=identity_file,
            )
            for instance in instances
        ]
    )


@pytest.fixture(scope="module")
def netris_tenants(
    request: pytest.FixtureRequest,
    cli: OsacCLI,
    grpc: GRPCClient,
    jwt_cli_tenant2_admin: OsacCLI,
    jwt_grpc_tenant2_admin: GRPCClient,
    k8s_hub_client: K8sClient,
    k8s_virt_client: K8sClient,
    vm_template: str,
    net_test_run_id: str,
    tmp_path_factory: pytest.TempPathFactory,
    default_instance_type: str,
    default_disk_image: str,
    default_storage_tier: str,
) -> Iterator[dict[str, dict[str, Any]]]:
    """Both tenants: t1 has networks a and b and VMs a1, a2, r1, b1; t2 has one network and VM t2a.

    Prerequisites run one after the other, the three networks at once, then the VMs (the tenants at once),
    then the five VM waits at once.
    """
    stack.require_host_tools()
    vn_cidrs = stack.network_cidrs()
    private_key, public_key_file = _generate_key(tmp_path_factory.mktemp(f"vmaas-netris-{net_test_run_id}"))
    identity_file = str(private_key)
    secret_name = f"netris-vm-key-{net_test_run_id}"
    secret_name_t2 = f"netris-vm-key-t2-{net_test_run_id}"
    image_name = f"netris-vm-di-t2-{net_test_run_id}"
    t1: dict[str, Any] = {
        "identity_file": identity_file,
        "key_secret": secret_name,
        "secret_live": False,
        "instances": [],
        "networks": [],
    }
    t2: dict[str, Any] = {
        "cli": jwt_cli_tenant2_admin,
        "grpc": jwt_grpc_tenant2_admin,
        "key_secret": secret_name_t2,
        "secret_live": False,
        "disk_image_id": "",
        "networks": [],
        "instances": [],
    }
    tenants = {"t1": t1, "t2": t2}
    started = time.time()
    # Registered before the setup: the sources read the lists as they fill, so a failed setup still has the guests
    # that exist.
    stack.register_guest_snapshot("guests-tenant1", t1["instances"], identity_file)
    stack.register_guest_snapshot("guests-tenant2", t2["instances"], identity_file)

    try:
        cli.create_secret(
            name=secret_name, secret_type="ssh-public-key", from_files={"public_key": str(public_key_file)}
        )
        t1["secret_live"] = True
        jwt_cli_tenant2_admin.create_secret(
            name=secret_name_t2, secret_type="ssh-public-key", from_files={"public_key": str(public_key_file)}
        )
        t2["secret_live"] = True
        t2["disk_image_id"] = jwt_grpc_tenant2_admin.create_disk_image(
            name=image_name,
            source_ref=DEFAULT_DISK_IMAGE_SOURCE_REF,
            guest_os_family="GUEST_OS_FAMILY_LINUX",
            architecture=["ARCHITECTURE_AMD64"],
        )

        network_a, network_b, network_t2 = _create_networks(
            grpc=grpc,
            tenant2_grpc=jwt_grpc_tenant2_admin,
            k8s_hub_client=k8s_hub_client,
            run_id=net_test_run_id,
            vn_cidrs=vn_cidrs,
            networks=t1["networks"],
            tenant2_networks=t2["networks"],
        )

        common = {
            "k8s_hub_client": k8s_hub_client,
            "vm_template": vm_template,
            "instance_type": default_instance_type,
            "storage_tier": default_storage_tier,
            "run_id": net_test_run_id,
        }
        vms_t1, vms_t2 = _create_vms(
            t1={
                "cli": cli,
                "instances": t1["instances"],
                "key_secret": secret_name,
                "disk_image": default_disk_image,
                **common,
            },
            t2={
                "cli": jwt_cli_tenant2_admin,
                "instances": t2["instances"],
                "key_secret": secret_name_t2,
                "disk_image": image_name,
                **common,
            },
            network_a=network_a,
            network_b=network_b,
            network_t2=network_t2,
        )
        _wait_for_vms(
            k8s_hub_client=k8s_hub_client,
            k8s_virt_client=k8s_virt_client,
            instances=[*t1["instances"], *t2["instances"]],
            identity_file=identity_file,
        )

        t1.update(network_a=network_a, network_b=network_b, vm_a1=vms_t1["a1"], vm_a2=vms_t1["a2"], vm_b1=vms_t1["b1"])
        t1["vm_r1"] = vms_t1["r1"]
        t2.update(network=network_t2, vm_t2a=vms_t2["t2a"], disk_image_name=image_name, identity_file=identity_file)
        yield tenants
    except GeneratorExit:
        raise
    except BaseException as exc:
        # Report now, while the stack and the guests are still there; the finally below deletes them.
        failure_report.capture_fixture_failure(request.config, exc, "fixture netris_tenants", started)
        raise
    finally:
        stack.unregister_guest_snapshot("guests-tenant1")
        stack.unregister_guest_snapshot("guests-tenant2")
        errors: list[str] = []
        _cleanup_tenants(tenants, cli=cli, grpc=grpc, k8s_hub_client=k8s_hub_client, errors=errors)
        _raise_cleanup_errors(errors)


@pytest.fixture(scope="module")
def netris_vm_network(netris_tenants: dict[str, dict[str, Any]]) -> dict[str, Any]:
    """Tenant1: networks a and b, VMs a1, a2, r1 and b1."""
    return netris_tenants["t1"]


@pytest.fixture(scope="module")
def netris_tenant2_network(netris_tenants: dict[str, dict[str, Any]]) -> dict[str, Any]:
    """Tenant2: its own Secret, DiskImage, network and VM t2a."""
    return netris_tenants["t2"]


@pytest.fixture(scope="module")
def netris_tcp_listeners(netris_vm_network: dict[str, Any]) -> dict[str, Any]:
    """Python listeners on 8080 and 8081 on a1 and r1. They die with the VM, so nothing needs cleanup."""
    for key in ("vm_a1", "vm_r1"):
        vm = netris_vm_network[key]
        for port in _LISTENER_PORTS:
            guest_ssh.start_tcp_listener(vm["vm_namespace"], vm["cr"], netris_vm_network["identity_file"], port)
    return netris_vm_network


def _teardown_ingress_record(record: dict[str, Any], k8s_hub_client: K8sClient) -> None:
    """Delete one manual ExternalIP and its attachment, attachment first; safe when already gone."""
    client: GRPCClient = record["client"]
    if record.get("attachment_id"):
        attachment_id = record["attachment_id"]
        wait_owned_gone(
            description=f"{record['label']} ExternalIPAttachment gone",
            api_gone=lambda: attachment_id not in client.list_external_ip_attachment_ids(),
            cr_gone=lambda: k8s_hub_client.get_external_ip_attachment_name(uuid=attachment_id, checked=False) == "",
            delete=lambda: client.delete_external_ip_attachment(attachment_id=attachment_id),
        )
        record["attachment_id"] = ""
    if record.get("eip_id"):
        eip_id = record["eip_id"]
        wait_owned_gone(
            description=f"{record['label']} ExternalIP gone",
            api_gone=lambda: eip_id not in client.list_external_ip_ids(),
            cr_gone=lambda: k8s_hub_client.get_external_ip_name(uuid=eip_id, checked=False) == "",
            delete=lambda: client.delete_external_ip(external_ip_id=eip_id),
        )
        record["eip_id"] = ""


def _teardown_pool(pool: dict[str, Any], private_grpc: GRPCClient, k8s_hub_client: K8sClient) -> None:
    pool_id = pool["id"]
    wait_owned_gone(
        description=f"ExternalIPPool {pool_id} gone",
        api_gone=lambda: pool_id not in private_grpc.list_external_ip_pool_ids(),
        cr_gone=lambda: k8s_hub_client.get_external_ip_pool_name(uuid=pool_id, checked=False) == "",
        delete=lambda: private_grpc.delete_external_ip_pool(pool_id=pool_id),
    )


# The most ExternalIPs the serial tests hold at once. test_09 holds the tenant1 NAT address; test_10 adds a
# manual one, which test_11 releases; test_12 adds an automatic one for its own lifetime; test_13 holds the
# tenant1 and tenant2 NAT addresses and tenant2's attached one. That last state is the peak: 3. (With the
# removed tenant2 automatic-address test it was 4.)
_EXTERNAL_IP_PEAK = 3


def _delete_ingress_records(external: dict[str, Any], k8s_hub_client: K8sClient, errors: list[str] | None) -> None:
    """Detach and release every manual or automatic ExternalIP at once: they do not depend on each other."""
    ingress = external["ingress"]
    stack.run_stage(
        [
            (
                f"ExternalIP {record['label']}",
                partial(_teardown_ingress_record, record, k8s_hub_client),
                partial(ingress.remove, record),
            )
            for record in list(ingress)
        ],
        errors,
    )


def _nat_targets(external: dict[str, Any], grpc: GRPCClient, tenant2_grpc: GRPCClient) -> list[tuple[Any, ...]]:
    return [(external["t1"], grpc), (external["t2"], tenant2_grpc)]


def _delete_nat_gateways(
    external: dict[str, Any],
    grpc: GRPCClient,
    tenant2_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    errors: list[str] | None,
) -> None:
    stack.run_stage(
        [
            (f"NATGateway {network['vnet_name']}", partial(_drop_nat, client, k8s_hub_client, network))
            for network, client in _nat_targets(external, grpc, tenant2_grpc)
        ],
        errors,
    )


def _delete_nat_external_ips(
    external: dict[str, Any],
    grpc: GRPCClient,
    tenant2_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    errors: list[str] | None,
) -> None:
    """The NAT ExternalIPs go after the NATGateways that use them."""
    stack.run_stage(
        [
            (f"NAT ExternalIP {network['vnet_name']}", partial(_drop_nat_eip, client, k8s_hub_client, network))
            for network, client in _nat_targets(external, grpc, tenant2_grpc)
        ],
        errors,
    )


def _cleanup_external_path(
    external: dict[str, Any],
    *,
    grpc: GRPCClient,
    tenant2_grpc: GRPCClient,
    private_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    errors: list[str],
) -> None:
    """What the finalizer removes: ExternalIPs, then NATGateways, then their ExternalIPs, then an owned pool."""
    if stack.keep_resources():
        _LOG.warning(
            "%s is true: leaving the ExternalIPs, NATGateways and any owned pool in place, delete them by hand",
            stack.KEEP_RESOURCES_ENV,
        )
        return
    _delete_ingress_records(external, k8s_hub_client, errors)
    _delete_nat_gateways(external, grpc, tenant2_grpc, k8s_hub_client, errors)
    _delete_nat_external_ips(external, grpc, tenant2_grpc, k8s_hub_client, errors)
    pool = external["pool"]
    if pool is not None and pool.get("owned"):
        stack.run_stage(
            [(f"ExternalIPPool {pool['id']}", partial(_release_pool, pool, private_grpc, k8s_hub_client))], errors
        )


@pytest.fixture(scope="module")
def netris_external_path(
    request: pytest.FixtureRequest,
    netris_tenants: dict[str, dict[str, Any]],
    grpc: GRPCClient,
    private_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    external_ip_pool_cidr: str,
    net_test_run_id: str,
) -> Iterator[dict[str, Any]]:
    """The routed ExternalIPPool, checked once, plus every NAT gateway and manual ExternalIP of both tenants.

    A pool the host does not route, that is not READY, or that has fewer free addresses than the suite's peak
    errors every phase C test here; the finalizer still removes what was created.
    """
    pool: dict[str, Any] = {}
    external: dict[str, Any] = {
        "pool": pool,
        "ingress": [],
        "t1": netris_tenants["t1"]["network_a"],
        "t2": netris_tenants["t2"]["network"],
    }
    started = time.time()
    try:
        stack.resolve_external_ip_pool(
            private_grpc=private_grpc,
            k8s_hub_client=k8s_hub_client,
            name=f"netris-vm-pool-{net_test_run_id}",
            cidr=external_ip_pool_cidr,
            record=pool,
        )
        stack.assert_pool_routed(external_ip_pool_cidr)
        stack.check_pool_ready(private_grpc, pool, cidr=external_ip_pool_cidr, needed=_EXTERNAL_IP_PEAK)
        yield external
    except GeneratorExit:
        raise
    except BaseException as exc:
        failure_report.capture_fixture_failure(request.config, exc, "fixture netris_external_path", started)
        raise
    finally:
        errors: list[str] = []
        _cleanup_external_path(
            external,
            grpc=grpc,
            tenant2_grpc=netris_tenants["t2"]["grpc"],
            private_grpc=private_grpc,
            k8s_hub_client=k8s_hub_client,
            errors=errors,
        )
        _raise_cleanup_errors(errors)


def _drop_nat(client: GRPCClient, k8s_hub_client: K8sClient, network: dict[str, str]) -> None:
    """Delete the NATGateway of `network` and forget it, so the finalizer has nothing left to do."""
    teardown_nat(client, k8s_hub_client, network)
    network.pop("nat_id", None)


def _drop_nat_eip(client: GRPCClient, k8s_hub_client: K8sClient, network: dict[str, str]) -> None:
    teardown_nat_eip(client, k8s_hub_client, network)
    network.pop("nat_eip_id", None)


def _identity(state: dict[str, Any]) -> str:
    return str(state["identity_file"])


def _ping(state: dict[str, Any], source: dict[str, str], target_ip: str) -> bool:
    return guest_ssh.ping(source["vm_namespace"], source["cr"], _identity(state), target_ip)


def _arping(state: dict[str, Any], source: dict[str, str], target_ip: str) -> bool:
    return guest_ssh.arping(source["vm_namespace"], source["cr"], _identity(state), target_ip)


def _tcp_state(state: dict[str, Any], source: dict[str, str], host: str, port: int) -> str:
    return guest_ssh.tcp_state(source["vm_namespace"], source["cr"], _identity(state), host, port)


def _tcp_state_once(state: dict[str, Any], source: dict[str, str], host: str, port: int) -> str:
    """One-shot probe: a transport error is a failure of the test setup, not a TCP result."""
    result = _tcp_state(state, source, host, port)
    if result == guest_ssh.TRANSPORT_ERROR:
        raise E2EFailure(
            Kind.TEST_SSH_FAILED,
            f"SSH from {source['name']} to run the TCP probe for {host}:{port} failed; this says nothing about TCP",
            ids={"ci_id": source["id"], "address": host},
        )
    return result


def _wait_tcp(
    state: dict[str, Any], source: dict[str, str], host: str, port: int, *, want_open: bool, retries: int, delay: int
) -> str:
    try:
        return poll_until(
            fn=lambda: _tcp_state(state, source, host, port),
            # A transport error is neither open nor closed: only refused or blocked proves a closed port.
            until=lambda value: value == "open" if want_open else value in ("refused", "blocked"),
            retries=retries,
            delay=delay,
            description=f"TCP {host}:{port} from {source['ip']} {'open' if want_open else 'not open'}",
        )
    except StuckState as exc:
        ids = {"ci_id": source["id"], "address": host}
        # A last value of transport-error means our own SSH kept failing, so the wait says nothing on the rules,
        # whichever direction it waited for.
        if guest_ssh.TRANSPORT_ERROR in exc.last_value:
            raise E2EFailure(Kind.TEST_SSH_FAILED, str(exc), ids=ids) from exc
        if not want_open:
            # Only refused or blocked ends this wait, so any other timeout means the traffic was still allowed.
            raise E2EFailure(Kind.ASSERTION, f"{exc} (the traffic was still allowed)", ids=ids) from exc
        kind, note = stack.traffic_kind_for_guest(source["vm_namespace"], source["cr"], _identity(state))(
            Kind.TRAFFIC_BLOCKED
        )
        raise E2EFailure(kind, str(exc) + note, ids=ids) from exc


def _assert_listener_alive(state: dict[str, Any], vm: dict[str, str], port: int) -> None:
    assert _tcp_state_once(state, vm, "127.0.0.1", port) == "open", (
        f"the listener on {vm['name']}:{port} is not running, so a closed result proves nothing"
    )


def _cluster_ips(k8s_virt_client: K8sClient) -> tuple[str, str]:
    """ClusterIPs of the cluster DNS and the kubernetes API service."""

    def cluster_ip(namespace: str, name: str) -> str:
        items = k8s_virt_client.list_json(resource="services", namespace=namespace).get("items", [])
        found = next((i["spec"]["clusterIP"] for i in items if i["metadata"]["name"] == name), "")
        if not found:
            pytest.fail(f"Service {namespace}/{name} was not found on the VM cluster, so cluster DNS cannot be checked")
        return found

    return cluster_ip("openshift-dns", "dns-default"), cluster_ip("default", "kubernetes")


def _assert_cluster_dns(state: dict[str, Any], vm: dict[str, str], dns_ip: str, api_ip: str) -> None:
    fail = "DHCP on the primary UDN did not hand out the cluster DNS, or the path to it failed; no NAT is involved."
    stack.poll_or_fail(
        hint=f"{vm['name']} was not given {dns_ip}. {fail}",
        fn=lambda: guest_ssh.dhcp_nameservers(vm["vm_namespace"], vm["cr"], _identity(state)),
        until=lambda servers: dns_ip in servers,
        retries=6,
        delay=10,
        description=f"cluster DNS {dns_ip} in the DHCP nameservers of {vm['name']}",
    )
    stack.poll_or_fail(
        hint=f"{vm['name']} could not resolve kubernetes.default.svc.cluster.local at {dns_ip}. {fail}",
        fn=lambda: guest_ssh.dns_a_record(
            vm["vm_namespace"], vm["cr"], _identity(state), "kubernetes.default.svc.cluster.local", dns_ip
        ),
        until=lambda address: address == api_ip,
        retries=6,
        delay=10,
        description=f"cluster DNS answer from {vm['name']}",
    )


def _create_nat(
    *,
    client: GRPCClient,
    k8s_hub_client: K8sClient,
    network: dict[str, str],
    pool: dict[str, Any],
    run_id: str,
    key: str,
) -> None:
    """Allocate an ExternalIP and bind a NATGateway to the VirtualNetwork; records every ID first."""
    eip_name = f"netris-vm-nat-eip-{key}-{run_id}"
    eip_id = client.create_external_ip(name=eip_name, pool=pool["id"])
    pool.setdefault("suite_eip_ids", set()).add(eip_id)
    network.update(nat_eip_id=eip_id, nat_eip_name=eip_name)
    with _diagnose(f"ExternalIP {eip_name} allocation"):
        network["nat_eip_cr"] = wait_for_external_ip_cr(k8s=k8s_hub_client, uuid=eip_id)
        wait_for_external_ip_allocated(k8s=k8s_hub_client, name=network["nat_eip_cr"])
    # The NATGateway needs its ExternalIP ALLOCATED in the API too, not only in the CR.
    address = stack.wait_for_grpc_external_ip_allocated(client, eip_id)
    assert ipaddress.IPv4Address(address) in ipaddress.IPv4Network(pool["cidr"]), (
        f"NAT ExternalIP {address} is outside the pool {pool['cidr']}"
    )

    nat_id = client.create_nat_gateway(
        name=f"netris-vm-nat-{key}-{run_id}", virtual_network_name=network["vnet_name"], external_ip_name=eip_name
    )
    network["nat_id"] = nat_id
    nat_cr = stack.poll_or_fail(
        hint=H,
        fn=lambda: k8s_hub_client.get_nat_gateway_name(uuid=nat_id, checked=False),
        until=bool,
        retries=30,
        delay=5,
        description=f"NATGateway CR for {network['vnet_name']} to appear",
    )
    with _diagnose(f"NATGateway {nat_cr} readiness"):
        wait_for_nat_gateway_ready(k8s=k8s_hub_client, name=nat_cr)
    stack.poll_or_fail(
        hint=H,
        fn=lambda: (
            client.call(service=f"{PUBLIC_API}.NATGateways/Get", data={"id": nat_id})
            .get("object", {})
            .get("status", {})
            .get("state", "")
        ),
        until=lambda state: state in ("NAT_GATEWAY_STATE_READY", "Ready"),
        retries=30,
        delay=5,
        description=f"NATGateway {nat_id} API state READY",
    )


def _assert_no_egress_without_nat(state: dict[str, Any], vm: dict[str, str], who: str) -> None:
    """Without a NATGateway the VM must not reach the internet, or a later 200 proves nothing about NAT."""
    # Raises on a transport failure, so a non-200 here means the guest ran curl and it could not get out.
    status = guest_ssh.curl_status_checked(vm["vm_namespace"], vm["cr"], _identity(state))
    assert status != 200, (
        f"{who} VM {vm['ip']} reached https://quay.io (HTTP {status}) before any NATGateway existed, "
        "so egress exists without NAT and the NAT egress check would prove nothing"
    )


def _assert_nat_egress(state: dict[str, Any], vm: dict[str, str], who: str) -> None:
    stack.poll_or_fail(
        hint=(f"NATGateway READY only means Netris accepted the rule, not that traffic flows. {H}"),
        fn=lambda: guest_ssh.curl_status(vm["vm_namespace"], vm["cr"], _identity(state)),
        until=lambda status: status == 200,
        retries=12,
        delay=15,
        description=f"NAT egress from {who} VM {vm['ip']} to quay.io",
        kind=Kind.TRAFFIC_BLOCKED,
        ids={"ci_id": vm["id"], "address": vm["ip"]},
        refine_kind=stack.traffic_kind_for_guest(vm["vm_namespace"], vm["cr"], _identity(state)),
    )


def _attach_external_ip(
    *,
    client: GRPCClient,
    private_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    external: dict[str, Any],
    vm: dict[str, str],
    label: str,
    run_id: str,
) -> dict[str, Any]:
    """Create an ExternalIP and attach it to `vm`; the record joins `external['ingress']` before any wait."""
    pool = external["pool"]
    eip_id = client.create_external_ip(name=f"netris-vm-{label}-eip-{run_id}", pool=pool["id"])
    record: dict[str, Any] = {"label": label, "client": client, "eip_id": eip_id, "vm": vm}
    external["ingress"].append(record)
    pool.setdefault("suite_eip_ids", set()).add(eip_id)
    with _diagnose(f"ExternalIP {label} allocation"):
        record["eip_cr"] = wait_for_external_ip_cr(k8s=k8s_hub_client, uuid=eip_id)
        wait_for_external_ip_allocated(k8s=k8s_hub_client, name=record["eip_cr"])
    # The attachment needs its ExternalIP ALLOCATED in the API too, not only in the CR.
    stack.wait_for_grpc_external_ip_allocated(client, eip_id)
    attachment_id = client.create_external_ip_attachment(
        name=f"netris-vm-{label}-attachment-{run_id}", external_ip=eip_id, compute_instance=vm["id"]
    )
    record["attachment_id"] = attachment_id
    with _diagnose(f"ExternalIPAttachment {label} readiness"):
        record["attachment_cr"] = wait_for_external_ip_attachment_cr(k8s=k8s_hub_client, uuid=attachment_id)
        wait_for_external_ip_attachment_ready(k8s=k8s_hub_client, name=record["attachment_cr"])
    record["address"] = stack.external_ip_address(client, eip_id)
    assert ipaddress.IPv4Address(record["address"]) in ipaddress.IPv4Network(pool["cidr"]), (
        f"ExternalIP {record['address']} is outside the pool {pool['cidr']}"
    )
    stack.wait_for_attribution(private_grpc=private_grpc, eip_id=eip_id, ci_id=vm["id"])
    return record


def _assert_external_ip_ssh(*, state: dict[str, Any], vm: dict[str, str], external_ip: str, description: str) -> None:
    """SSH to the ExternalIP with the host key read through the helper Pod: it proves the address reaches this VM."""
    identity_file = _identity(state)
    expected_host_key = guest_ssh.guest_host_key(vm["vm_namespace"], vm["cr"], identity_file)
    last_error: list[tuple[str, Kind]] = []

    def try_ssh() -> str:
        try:
            return guest_ssh.ssh_via_external_ip(
                external_ip, identity_file, expected_host_key=expected_host_key, timeout=10
            )
        except (OSError, subprocess.CalledProcessError, subprocess.TimeoutExpired) as exc:
            last_error[:] = [guest_ssh.external_ssh_error(exc)]
            return ""

    try:
        poll_until(fn=try_ssh, until=bool, retries=12, delay=20, description=description)
    except TimeoutError as exc:
        # The guest answered through the helper Pod above, so a timeout or refusal here is the path under test.
        detail, kind = last_error[0] if last_error else ("none", Kind.TRAFFIC_BLOCKED)
        note = ""
        if kind is Kind.TRAFFIC_BLOCKED:
            kind, note = stack.traffic_kind_for_guest(vm["vm_namespace"], vm["cr"], identity_file)(kind)
        raise E2EFailure(
            kind,
            f"{description} timed out (last error: {detail}). {H}{note}",
            ids={"ci_id": vm["id"], "address": external_ip},
        ) from exc


def _wait_gone(description: str, check: Callable[[], bool], retries: int = 30, delay: int = 5) -> None:
    poll_until(fn=check, until=bool, retries=retries, delay=delay, description=description)


def _assert_auto_external_ip_lifecycle(
    *,
    tenant: dict[str, Any],
    network: dict[str, str],
    external: dict[str, Any],
    private_grpc: GRPCClient,
    cli: OsacCLI,
    client: GRPCClient,
    k8s_hub_client: K8sClient,
    k8s_virt_client: K8sClient,
    vm_template: str,
    run_id: str,
    label: str,
    instance_type: str,
    disk_image: str,
    storage_tier: str,
) -> None:
    """An auto-ExternalIP VM gets a pooled address with working ingress; deleting the VM collects all of it."""
    instances: list[dict[str, str]] = tenant["instances"]
    pool = external["pool"]
    vm_name = f"netris-vm-auto-{label}-{run_id}"
    try:
        vm = stack.create_vm(
            cli=cli,
            k8s_hub_client=k8s_hub_client,
            instances=instances,
            name=vm_name,
            network=network,
            sg_id=network["sg_id"],
            key_secret=tenant["key_secret"],
            vm_template=vm_template,
            instance_type=instance_type,
            disk_image=disk_image,
            storage_tier=storage_tier,
            external_ip_attachment=True,
        )
        stack.wait_for_vm(
            k8s_hub_client=k8s_hub_client, k8s_virt_client=k8s_virt_client, instance=vm, identity_file=_identity(tenant)
        )
        attachment = stack.poll_or_fail(
            hint=f"The pool must be READY before the VM is created. {H}",
            fn=lambda: stack.find_auto_attachment(client, vm["id"]),
            until=lambda value: value is not None,
            retries=60,
            delay=5,
            description=f"auto-created ExternalIPAttachment for {vm_name}",
        )
        attachment_id = attachment["id"]
        eip_id = attachment.get("spec", {}).get("externalIp", {}).get("id", "")
        # Record before asserting, so broken garbage collection cannot leak these into the pool finalizer.
        record = {"label": f"auto-{label}", "client": client, "attachment_id": attachment_id, "eip_id": eip_id}
        external["ingress"].append(record)
        pool.setdefault("suite_eip_ids", set()).add(eip_id)
        assert eip_id, "Auto-created ExternalIPAttachment has no ExternalIP reference"
        with _diagnose(f"auto ExternalIPAttachment {attachment_id} readiness"):
            attachment_cr = wait_for_external_ip_attachment_cr(k8s=k8s_hub_client, uuid=attachment_id)
            wait_for_external_ip_attachment_ready(k8s=k8s_hub_client, name=attachment_cr)
            eip_cr = wait_for_external_ip_cr(k8s=k8s_hub_client, uuid=eip_id)
            wait_for_external_ip_allocated(k8s=k8s_hub_client, name=eip_cr)
        address = stack.external_ip_address(client, eip_id)
        assert ipaddress.IPv4Address(address) in ipaddress.IPv4Network(pool["cidr"]), (
            f"auto ExternalIP {address} is outside the routed pool {pool['cidr']}; "
            "another READY pool with more free addresses was selected"
        )
        stack.wait_for_attribution(private_grpc=private_grpc, eip_id=eip_id, ci_id=vm["id"])
        _assert_external_ip_ssh(
            state=tenant, vm=vm, external_ip=address, description=f"SSH ingress through auto ExternalIP {address}"
        )

        stack.delete_vm(client=client, k8s_hub_client=k8s_hub_client, instance=vm)
        instances.remove(vm)
        wait_for_external_ip_attachment_deletion(k8s=k8s_hub_client, name=attachment_cr)
        _wait_gone(
            f"auto ExternalIPAttachment {attachment_id} garbage collection",
            lambda: attachment_id not in client.list_external_ip_attachment_ids(),
        )
        wait_for_external_ip_deletion(k8s=k8s_hub_client, name=eip_cr)
        _wait_gone(f"auto ExternalIP {eip_id} garbage collection", lambda: eip_id not in client.list_external_ip_ids())
        external["ingress"].remove(record)
    finally:
        # create_vm records the instance before it waits, so find it even when create_vm raised.
        leftover = next((i for i in instances if i["name"] == vm_name), None)
        if leftover is not None:
            errors: list[str] = []

            def delete() -> None:
                stack.delete_vm(client=client, k8s_hub_client=k8s_hub_client, instance=leftover)
                instances.remove(leftover)

            _run_cleanup(errors, f"ComputeInstance {leftover['id']}", delete)
            _raise_cleanup_errors(errors)


def _pool_external_ips(private_grpc: GRPCClient, pool_id: str) -> tuple[list[dict[str, Any]], list[dict[str, Any]]]:
    """(every live ExternalIP, the ones on `pool_id`), through the private API."""
    live = private_grpc.list_private_external_ips()
    return live, [item for item in live if item.get("spec", {}).get("pool", {}).get("id") == pool_id]


def _is_default(item: dict[str, Any]) -> bool:
    return (item.get("metadata", {}).get("labels") or {}).get(_DEFAULT_LABEL) == "true"


def _describe(item: dict[str, Any]) -> str:
    metadata = item.get("metadata", {})
    return f"{metadata.get('name', '?')} ({item.get('id', '?')}, tenant {metadata.get('tenant', '?')})"


def _assert_pool_accounting(pool: dict[str, Any], private_grpc: GRPCClient) -> None:
    """None of the suite's ExternalIPs is left, and the pool's `allocated` equals the live ExternalIPs on it.

    Tenant default ExternalIPs (`default-nat`) draw from the same pool and come and go on their own, so the count
    is compared with what is live on the pool now, not with a value read at the start. That keeps the proof that
    a deleted ExternalIP's address reaches the pool's capacity.
    """
    live, _ = _pool_external_ips(private_grpc, pool["id"])
    left = pool.get("suite_eip_ids", set()) & {item["id"] for item in live}
    assert not left, f"ExternalIPs created by this suite are still listed after deletion: {sorted(left)}"
    stack.poll_or_fail(
        hint="an ExternalIP was deleted from the API but its address did not return to the pool.",
        fn=lambda: (
            int(private_grpc.get_external_ip_pool(pool_id=pool["id"])["object"]["status"].get("allocated", 0)),
            len(_pool_external_ips(private_grpc, pool["id"])[1]),
        ),
        until=lambda counts: counts[0] == counts[1],
        retries=12,
        delay=5,
        description=f"ExternalIPPool {pool['id']} allocated equals its live ExternalIPs",
        kind=Kind.ASSERTION,
        ids={"pool_id": pool["id"]},
    )


def _release_pool(pool: dict[str, Any], private_grpc: GRPCClient, k8s_hub_client: K8sClient) -> bool:
    """Delete an owned pool; return False when tenant default ExternalIPs hold it, so it cannot be deleted.

    Defaults are created by the platform for every tenant and belong to no test. Any other holder is a failure.
    """
    _, holders = _pool_external_ips(private_grpc, pool["id"])
    others = [item for item in holders if not _is_default(item)]
    assert not others, f"ExternalIPPool {pool['id']} is still held by: {', '.join(_describe(i) for i in others)}"
    if holders:
        _LOG.warning(
            "ExternalIPPool %s is not deleted: it is held by tenant default ExternalIPs: %s",
            pool["id"],
            ", ".join(_describe(item) for item in holders),
        )
        return False
    _teardown_pool(pool, private_grpc, k8s_hub_client)
    return True


def _teardown_leaves_nothing(
    *,
    tenants: dict[str, dict[str, Any]],
    external: dict[str, Any],
    cli: OsacCLI,
    grpc: GRPCClient,
    private_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    k8s_virt_client: K8sClient,
) -> None:
    """Delete everything in dependency order; inside a stage the independent deletes run at the same time.

    A manual attachment blocks ComputeInstance deletion, so attachments and ExternalIPs go first. Each item leaves
    its state list as soon as its own delete succeeded, so a failure leaves exactly the undeleted rest to the
    fixture finalizers.
    """
    if stack.keep_resources():
        pytest.skip(f"{stack.KEEP_RESOURCES_ENV} is true: the resources stay for debugging")
    t1, t2 = tenants["t1"], tenants["t2"]
    tenant2_grpc = t2["grpc"]

    # 1. Attachments and manual ExternalIPs, across records.
    _delete_ingress_records(external, k8s_hub_client, None)

    # 2. VMs of both tenants.
    listed = [(client, list(instances)) for client, instances in _tenant_targets(tenants, grpc, "instances")]
    _delete_instances(targets=_tenant_targets(tenants, grpc, "instances"), k8s_hub_client=k8s_hub_client)
    for client, deleted in listed:
        remaining = [i["name"] for i in deleted if i["id"] in client.list_compute_instance_ids()]
        assert not remaining, f"ComputeInstances still listed after deletion: {remaining}"

    # 3. NATGateways, then the ExternalIPs they used.
    nat_ids = [(network.get("nat_id"), client) for network, client in _nat_targets(external, grpc, tenant2_grpc)]
    _delete_nat_gateways(external, grpc, tenant2_grpc, k8s_hub_client, None)
    _delete_nat_external_ips(external, grpc, tenant2_grpc, k8s_hub_client, None)
    for nat_id, client in nat_ids:
        assert nat_id not in nat_gateway_ids(client), f"NATGateway {nat_id} is still listed after deletion"

    # 4. Networks, each followed by the removal of its VM namespace on the VM cluster.
    namespaces = {
        network["vnet_id"]: vm["vm_namespace"]
        for network, vm in (
            (t1["network_a"], t1["vm_a1"]),
            (t1["network_b"], t1["vm_b1"]),
            (t2["network"], t2["vm_t2a"]),
        )
    }

    def namespace_gone(network: dict[str, str]) -> None:
        namespace = namespaces[network["vnet_id"]]
        poll_until(
            fn=lambda: k8s_virt_client.is_absent(resource="namespace", name=namespace),
            until=bool,
            retries=60,
            delay=5,
            description=f"VM namespace {namespace} removed with {network['vnet_name']}",
        )

    _delete_networks(
        targets=_tenant_targets(tenants, grpc, "networks"), k8s_hub_client=k8s_hub_client, after_delete=namespace_gone
    )

    # 5. Secrets and the DiskImage.
    _delete_secrets_and_images(tenants, cli=cli)

    # 6. The pool: the suite's addresses are back. An owned pool goes unless tenant default ExternalIPs hold it;
    # an adopted one stays.
    pool = external["pool"]
    _assert_pool_accounting(pool, private_grpc)
    if not pool.get("owned"):
        assert pool["id"] in private_grpc.list_external_ip_pool_ids(), (
            f"adopted ExternalIPPool {pool['id']} was removed; this suite did not create it"
        )
        return
    if _release_pool(pool, private_grpc, k8s_hub_client):
        assert pool["id"] not in private_grpc.list_external_ip_pool_ids(), (
            f"ExternalIPPool {pool['id']} is still listed after deletion"
        )
    external["pool"] = None


class TestVmaasNetworking:
    """Ordered connectivity, enforcement and ExternalIP flows for a CUDN EVPN VMaaS network class."""

    # Phase A: connectivity and isolation

    def test_00_l2_arping_same_subnet(self, netris_vm_network: dict[str, Any]) -> None:
        first = netris_vm_network["vm_a1"]
        second = netris_vm_network["vm_a2"]

        # Every guest check goes through the helper Pod: prove for each VM that it can log in, and say
        # which of Pod, TCP 22 and key failed if not.
        for key in ("vm_a1", "vm_a2", "vm_b1", "vm_r1"):
            vm = netris_vm_network[key]
            guest_ssh.assert_guest_login(vm["vm_namespace"], vm["cr"], _identity(netris_vm_network))

        for source, target in ((first, second), (second, first)):
            assert poll_until(
                fn=lambda src=source, dst=target: _arping(netris_vm_network, src, dst["ip"]),
                until=bool,
                retries=5,
                delay=10,
                description=f"same-Subnet VMaaS ARP {source['ip']} to {target['ip']}",
            )

    def test_01_l3_ping_same_subnet(self, netris_vm_network: dict[str, Any]) -> None:
        first = netris_vm_network["vm_a1"]
        second = netris_vm_network["vm_a2"]

        for source, target in ((first, second), (second, first)):
            assert poll_until(
                fn=lambda src=source, dst=target: _ping(netris_vm_network, src, dst["ip"]),
                until=bool,
                retries=5,
                delay=10,
                description=f"same-Subnet VMaaS ICMP {source['ip']} to {target['ip']}",
            )

    def test_02_l2_arping_cross_virtual_network_fails(self, netris_vm_network: dict[str, Any]) -> None:
        first = netris_vm_network["vm_a1"]
        other_network = netris_vm_network["vm_b1"]

        for source, target in ((first, other_network), (other_network, first)):
            assert not _arping(netris_vm_network, source, target["ip"]), (
                f"ARP from VM {source['ip']} to {target['ip']} in another VirtualNetwork succeeded unexpectedly"
            )

    def test_03_l3_ping_cross_virtual_network_fails(self, netris_vm_network: dict[str, Any]) -> None:
        first = netris_vm_network["vm_a1"]
        other_network = netris_vm_network["vm_b1"]

        for source, target in ((first, other_network), (other_network, first)):
            assert not _ping(netris_vm_network, source, target["ip"]), (
                f"ICMP from VM {source['ip']} to {target['ip']} in another VirtualNetwork succeeded unexpectedly"
            )

    # Phase B: DNS, SecurityGroup enforcement, tenant isolation

    def test_04_cluster_dns_from_ovn_dhcp(self, k8s_virt_client: K8sClient, netris_vm_network: dict[str, Any]) -> None:
        vm = netris_vm_network["vm_a1"]
        dns_ip, api_ip = _cluster_ips(k8s_virt_client)
        _assert_cluster_dns(netris_vm_network, vm, dns_ip, api_ip)
        stack.poll_or_fail(
            hint=f"{vm['name']} cannot resolve a public name through its system resolver; test_09 needs that.",
            fn=lambda: guest_ssh.resolves(vm["vm_namespace"], vm["cr"], _identity(netris_vm_network), "quay.io"),
            until=bool,
            retries=6,
            delay=10,
            description=f"system resolver of {vm['name']} resolving quay.io",
        )

    def test_05_security_group_tcp_ingress_allow_and_deny(self, netris_tcp_listeners: dict[str, Any]) -> None:
        state = netris_tcp_listeners
        a1, a2, r1 = state["vm_a1"], state["vm_a2"], state["vm_r1"]

        _wait_tcp(state, a1, r1["ip"], 8080, want_open=True, retries=12, delay=10)
        assert _tcp_state_once(state, a1, r1["ip"], 8081) in ("refused", "blocked"), (
            f"TCP 8081 from {a1['ip']} to {r1['ip']} succeeded although the restricted group allows only 22 and 8080"
        )
        _assert_listener_alive(state, r1, 8081)
        assert not _ping(state, a1, r1["ip"]), (
            f"ICMP from {a1['ip']} to {r1['ip']} succeeded although the restricted group has no all rule"
        )
        assert poll_until(
            fn=lambda: _ping(state, a1, a2["ip"]),
            until=bool,
            retries=5,
            delay=10,
            description=f"positive ICMP control from {a1['ip']} to {a2['ip']}",
        )

    def test_06_security_group_rule_update_swaps_ingress(
        self, grpc: GRPCClient, netris_tcp_listeners: dict[str, Any]
    ) -> None:
        state = netris_tcp_listeners
        a1, r1 = state["vm_a1"], state["vm_r1"]
        restricted_id = state["network_a"]["restricted_sg_id"]

        grpc.update_security_group_rules(sg_id=restricted_id, ingress=[_TCP_22, _tcp(8081)], egress=[_ALL])
        wait_for_security_group_update(grpc=grpc, sg_id=restricted_id)

        _wait_tcp(state, a1, r1["ip"], 8081, want_open=True, retries=12, delay=5)
        _wait_tcp(state, a1, r1["ip"], 8080, want_open=False, retries=12, delay=5)
        _assert_listener_alive(state, r1, 8080)

    def test_07_security_group_tcp_egress_allow_and_deny(
        self, grpc: GRPCClient, netris_tcp_listeners: dict[str, Any]
    ) -> None:
        state = netris_tcp_listeners
        a1, r1 = state["vm_a1"], state["vm_r1"]
        network_a = state["network_a"]
        restricted_id = network_a["restricted_sg_id"]
        ingress = [_TCP_22, _tcp(8081)]

        def restore() -> None:
            grpc.update_security_group_rules(sg_id=restricted_id, ingress=ingress, egress=[_ALL])
            wait_for_security_group_update(grpc=grpc, sg_id=restricted_id)

        try:
            grpc.update_security_group_rules(
                sg_id=restricted_id, ingress=ingress, egress=[_tcp(8080, network_a["subnet_cidr"])]
            )
            wait_for_security_group_update(grpc=grpc, sg_id=restricted_id)
            _wait_tcp(state, r1, a1["ip"], 8080, want_open=True, retries=12, delay=5)
            _wait_tcp(state, r1, a1["ip"], 8081, want_open=False, retries=12, delay=5)
            _assert_listener_alive(state, a1, 8081)
        except BaseException:
            # A failed restore must not mask the failure that got us here.
            with contextlib.suppress(Exception):
                restore()
            raise
        restore()
        _wait_tcp(state, r1, a1["ip"], 8081, want_open=True, retries=12, delay=5)

    def test_08_cross_tenant_isolation(
        self, k8s_virt_client: K8sClient, netris_vm_network: dict[str, Any], netris_tenant2_network: dict[str, Any]
    ) -> None:
        a1, a2 = netris_vm_network["vm_a1"], netris_vm_network["vm_a2"]
        t2a = netris_tenant2_network["vm_t2a"]
        dns_ip, api_ip = _cluster_ips(k8s_virt_client)
        guest_ssh.assert_guest_login(t2a["vm_namespace"], t2a["cr"], _identity(netris_tenant2_network))

        # Controls first: tenant2's NIC works (cluster DNS answers) and tenant1's path works.
        stack.poll_or_fail(
            hint=f"tenant2 VM {t2a['name']} cannot reach the cluster DNS at {dns_ip}, so a block proves nothing.",
            fn=lambda: guest_ssh.dns_a_record(
                t2a["vm_namespace"],
                t2a["cr"],
                _identity(netris_tenant2_network),
                "kubernetes.default.svc.cluster.local",
                dns_ip,
            ),
            until=lambda address: address == api_ip,
            retries=6,
            delay=10,
            description=f"positive DNS control from tenant2 VM {t2a['ip']}",
        )
        assert poll_until(
            fn=lambda: _ping(netris_vm_network, a1, a2["ip"]),
            until=bool,
            retries=5,
            delay=10,
            description=f"positive ICMP control inside tenant1 from {a1['ip']} to {a2['ip']}",
        )

        for source_state, source, target, direction in (
            (netris_vm_network, a1, t2a, "tenant1 to tenant2"),
            (netris_tenant2_network, t2a, a1, "tenant2 to tenant1"),
        ):
            assert not guest_ssh.arping(source["vm_namespace"], source["cr"], _identity(source_state), target["ip"]), (
                f"ARP {direction} ({source['ip']} to {target['ip']}) succeeded unexpectedly"
            )
            assert not guest_ssh.ping(source["vm_namespace"], source["cr"], _identity(source_state), target["ip"]), (
                f"ICMP {direction} ({source['ip']} to {target['ip']}) succeeded unexpectedly"
            )

    # Phase C: the external path. The routed pool is checked by the netris_external_path fixture.

    def test_09_nat_gateway_egress(
        self,
        grpc: GRPCClient,
        k8s_hub_client: K8sClient,
        net_test_run_id: str,
        netris_vm_network: dict[str, Any],
        netris_external_path: dict[str, Any],
    ) -> None:
        _assert_no_egress_without_nat(netris_vm_network, netris_vm_network["vm_a1"], "tenant1")
        _create_nat(
            client=grpc,
            k8s_hub_client=k8s_hub_client,
            network=netris_external_path["t1"],
            pool=netris_external_path["pool"],
            run_id=net_test_run_id,
            key="t1",
        )
        _assert_nat_egress(netris_vm_network, netris_vm_network["vm_a1"], "tenant1")

    def test_10_manual_external_ip_ingress(
        self,
        grpc: GRPCClient,
        private_grpc: GRPCClient,
        k8s_hub_client: K8sClient,
        net_test_run_id: str,
        netris_vm_network: dict[str, Any],
        netris_external_path: dict[str, Any],
    ) -> None:
        vm = netris_vm_network["vm_a1"]
        record = _attach_external_ip(
            client=grpc,
            private_grpc=private_grpc,
            k8s_hub_client=k8s_hub_client,
            external=netris_external_path,
            vm=vm,
            label="t1-a1",
            run_id=net_test_run_id,
        )
        _assert_external_ip_ssh(
            state=netris_vm_network,
            vm=vm,
            external_ip=record["address"],
            description=f"SSH ingress through manually attached ExternalIP {record['address']}",
        )
        record["ingress_proven"] = True

    def test_11_external_ip_attachment_delete_stops_ingress(
        self,
        grpc: GRPCClient,
        k8s_hub_client: K8sClient,
        netris_vm_network: dict[str, Any],
        netris_external_path: dict[str, Any],
    ) -> None:
        vm = netris_vm_network["vm_a1"]
        record = next((r for r in netris_external_path["ingress"] if r["label"] == "t1-a1"), None)
        if record is None or not record.get("ingress_proven") or not record.get("attachment_cr"):
            pytest.fail(
                "test_10 did not prove SSH ingress through an attached ExternalIP on the first tenant1 VM, "
                "so there is no working path whose removal could be shown"
            )
        identity_file = _identity(netris_vm_network)
        # Positive control one step before the deny, on the same path.
        _assert_external_ip_ssh(
            state=netris_vm_network,
            vm=vm,
            external_ip=record["address"],
            description=f"SSH ingress through ExternalIP {record['address']} before the attachment is deleted",
        )
        host_key = guest_ssh.guest_host_key(vm["vm_namespace"], vm["cr"], identity_file)
        attachment_id = record["attachment_id"]

        grpc.delete_external_ip_attachment(attachment_id=attachment_id)
        wait_for_external_ip_attachment_deletion(k8s=k8s_hub_client, name=record["attachment_cr"])
        _wait_gone(
            f"ExternalIPAttachment {attachment_id} API deletion",
            lambda: attachment_id not in grpc.list_external_ip_attachment_ids(),
        )
        record["attachment_id"] = ""

        def ingress_stopped() -> bool:
            try:
                guest_ssh.ssh_via_external_ip(record["address"], identity_file, expected_host_key=host_key, timeout=10)
            except (subprocess.CalledProcessError, subprocess.TimeoutExpired, OSError):
                return True
            return False

        stack.poll_or_fail(
            hint="the traffic was still allowed: ingress still works after the attachment was deleted.",
            fn=ingress_stopped,
            until=bool,
            retries=12,
            delay=10,
            description=f"SSH through ExternalIP {record['address']} stops after the attachment is deleted",
            # The wait ends when ssh fails, so a timeout means the traffic was still allowed.
            kind=Kind.ASSERTION,
            ids={
                "ci_id": vm["id"],
                "attachment_id": attachment_id,
                "eip_id": record["eip_id"],
                "address": record["address"],
            },
        )

        eip_id = record["eip_id"]
        grpc.delete_external_ip(external_ip_id=eip_id)
        wait_for_external_ip_deletion(k8s=k8s_hub_client, name=record["eip_cr"])
        _wait_gone(f"ExternalIP {eip_id} API deletion", lambda: eip_id not in grpc.list_external_ip_ids())
        record["eip_id"] = ""
        netris_external_path["ingress"].remove(record)

    def test_12_automatic_external_ip_ingress_and_garbage_collection(
        self,
        cli: OsacCLI,
        grpc: GRPCClient,
        private_grpc: GRPCClient,
        k8s_hub_client: K8sClient,
        k8s_virt_client: K8sClient,
        vm_template: str,
        net_test_run_id: str,
        netris_vm_network: dict[str, Any],
        netris_external_path: dict[str, Any],
        default_instance_type: str,
        default_disk_image: str,
        default_storage_tier: str,
    ) -> None:
        _assert_auto_external_ip_lifecycle(
            tenant=netris_vm_network,
            network=netris_vm_network["network_a"],
            external=netris_external_path,
            private_grpc=private_grpc,
            cli=cli,
            client=grpc,
            k8s_hub_client=k8s_hub_client,
            k8s_virt_client=k8s_virt_client,
            vm_template=vm_template,
            run_id=net_test_run_id,
            label="t1",
            instance_type=default_instance_type,
            disk_image=default_disk_image,
            storage_tier=default_storage_tier,
        )

    def test_13_tenant2_nat_egress_and_ingress(
        self,
        private_grpc: GRPCClient,
        k8s_hub_client: K8sClient,
        net_test_run_id: str,
        netris_vm_network: dict[str, Any],
        netris_tenant2_network: dict[str, Any],
        netris_external_path: dict[str, Any],
    ) -> None:
        client = netris_tenant2_network["grpc"]
        vm = netris_tenant2_network["vm_t2a"]
        _assert_no_egress_without_nat(netris_tenant2_network, vm, "tenant2")
        _create_nat(
            client=client,
            k8s_hub_client=k8s_hub_client,
            network=netris_external_path["t2"],
            pool=netris_external_path["pool"],
            run_id=net_test_run_id,
            key="t2",
        )
        _assert_nat_egress(netris_tenant2_network, vm, "tenant2")
        record = _attach_external_ip(
            client=client,
            private_grpc=private_grpc,
            k8s_hub_client=k8s_hub_client,
            external=netris_external_path,
            vm=vm,
            label="t2-t2a",
            run_id=net_test_run_id,
        )
        _assert_external_ip_ssh(
            state=netris_tenant2_network,
            vm=vm,
            external_ip=record["address"],
            description=f"SSH ingress through tenant2 ExternalIP {record['address']}",
        )
        record["ingress_proven"] = True

        # t2a now answers on its ExternalIP, so these negatives follow proof that the target is reachable.
        a1 = netris_vm_network["vm_a1"]
        assert not _ping(netris_vm_network, a1, vm["ip"]), (
            f"ICMP from tenant1 {a1['ip']} to tenant2 {vm['ip']} succeeded after NAT and ExternalIP were configured"
        )
        assert not _ping(netris_tenant2_network, vm, a1["ip"]), (
            f"ICMP from tenant2 {vm['ip']} to tenant1 {a1['ip']} succeeded after NAT and ExternalIP were configured"
        )

    # Phase D: one teardown test. It stays a test, so an assertion fails the job; a stopped run (-x) never
    # reaches it, and the fixture finalizers then clean what is left.

    def test_14_teardown_leaves_nothing(
        self,
        cli: OsacCLI,
        grpc: GRPCClient,
        private_grpc: GRPCClient,
        k8s_hub_client: K8sClient,
        k8s_virt_client: K8sClient,
        netris_tenants: dict[str, dict[str, Any]],
        netris_external_path: dict[str, Any],
    ) -> None:
        _teardown_leaves_nothing(
            tenants=netris_tenants,
            external=netris_external_path,
            cli=cli,
            grpc=grpc,
            private_grpc=private_grpc,
            k8s_hub_client=k8s_hub_client,
            k8s_virt_client=k8s_virt_client,
        )
