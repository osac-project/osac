"""Cross-tenant BMaaS + Netris networking (OSAC-5444).

What is new here (not covered by test_bmaas_networking.py)
---------------------------------------------------------
- Flow 14: tenant1 ↔ tenant2 BMI L3 isolation (separate VPCs / EVPN domains).
- Dual-tenant stacks: vnet/subnet/SG/NAT per tenant, auto-EIP on both tenants,
  NAT egress + ingress DNAT, optional SG deny, owned-resource cleanup.

What is intentionally NOT re-tested (already in test_bmaas_networking.py)
-------------------------------------------------------------------------
- Flow 11: same tenant, same subnet — L2 arping + L3 ping succeed
  (test_06_l2_arping_same_subnet, test_07_l3_ping_same_subnet).
- Flow 12: same tenant, different subnets — L3 ping succeeds, L2 arping fails
  (test_08_l3_ping_cross_subnet, test_09_l2_arping_cross_subnet_fails).

Why a separate suite
--------------------
Cross-tenant needs a second VPC and BMI (extra host time). Keeping it out of the
single-tenant networking job avoids duplicating Flow 11/12 cost on every run.
"""

from __future__ import annotations

import os
import subprocess
from collections.abc import Iterator
from typing import Any, ClassVar

import pytest

from tests.e2e.bmaas.regression.networking import bmi_ssh, guest_ssh
from tests.e2e.core.grpc_client import PRIVATE_API, GRPCClient
from tests.e2e.core.helpers import (
    wait_for_bmh_available,
    wait_for_bmi_cr,
    wait_for_bmi_deletion,
    wait_for_bmi_grpc_removal,
    wait_for_bmi_running,
    wait_for_external_ip_allocated,
    wait_for_external_ip_attachment_cr,
    wait_for_external_ip_attachment_ready,
    wait_for_external_ip_cr,
    wait_for_external_ip_pool_cr,
    wait_for_external_ip_pool_grpc_ready,
    wait_for_external_ip_pool_ready,
    wait_for_security_group_cr,
    wait_for_security_group_ready,
    wait_for_subnet_cr,
    wait_for_subnet_ready,
    wait_for_virtual_network_cr,
    wait_for_virtual_network_ready,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.runner import poll_until

pytestmark = pytest.mark.regression

_NETRIS_BMI_RUNNING_RETRIES = 240
# Poll often; proceed as soon as API + CR are gone. Caps match existing helpers (~5m / ~10m).
_POLL_DELAY = 2
_CHILD_RETRIES = 150
_FABRIC_RETRIES = 300
_SSH_ICMP_INGRESS = [
    {"protocol": "PROTOCOL_TCP", "port_from": 22, "port_to": 22, "ipv4_cidr": "0.0.0.0/0"},
    {"protocol": "PROTOCOL_ICMP", "ipv4_cidr": "0.0.0.0/0"},
]
_ALL_EGRESS = [{"protocol": "PROTOCOL_ALL", "ipv4_cidr": "0.0.0.0/0"}]


def _require(state: dict[str, Any], *keys: str) -> None:
    missing = [k for k in keys if k not in state]
    if missing:
        pytest.skip(f"Prerequisite state missing: {', '.join(missing)}")


def _disk_image_from_catalog_item(item: dict[str, Any]) -> str:
    fields = item.get("fields") or {}
    policy = fields.get("diskImage") or fields.get("disk_image") or {}
    locked = policy.get("locked") or {}
    editable = policy.get("editable") or {}
    default = editable.get("defaultValue") or editable.get("default_value") or {}
    return str(locked.get("name") or default.get("name") or "").strip()


def _lab_disk_image_name(grpc: GRPCClient, *catalog_names: str) -> str | None:
    """Optional DiskImage name. Lab catalogs (ci-bm-default) have no disk_image policy.

    DiskImages RPCs are VMaaS-gated on this hub, so do not List/Create them.
    """
    named = os.environ.get("OSAC_BMI_DISK_IMAGE", "").strip()
    if named:
        return named
    items = grpc.call(service=f"{PRIVATE_API}.BareMetalInstanceCatalogItems/List").get("items") or []
    wanted = {n for n in catalog_names if n}
    for item in items:
        meta_name = str((item.get("metadata") or {}).get("name") or "")
        if wanted and meta_name not in wanted:
            continue
        di = _disk_image_from_catalog_item(item)
        if di:
            return di
    for item in items:
        di = _disk_image_from_catalog_item(item)
        if di:
            return di
    return None


def _sg_deny_workaround() -> bool:
    return os.environ.get("OSAC_SG_DENY_WORKAROUND", "") == "1"


def _create_bmi(
    grpc: GRPCClient,
    *,
    name: str,
    catalog: str,
    subnet_id: str,
    security_group_id: str,
    ssh_public_key: str,
    tenant: str,
    disk_image: str | None,
    user_data: str,
    auto_eip: bool,
    instance_type: str | None = None,
) -> str:
    """Create a BMI. If auto_eip, set spec.auto_external_ip_attachment unless the catalog locks it."""
    kwargs: dict[str, Any] = {
        "name": name,
        "catalog_item": catalog,
        "subnet_id": subnet_id,
        "security_group_id": security_group_id,
        "ssh_public_key": ssh_public_key,
        "tenant": tenant,
        "disk_image": disk_image,
        "user_data": user_data,
        "instance_type": instance_type,
    }
    if not auto_eip:
        return grpc.create_baremetal_instance(**kwargs)
    try:
        return grpc.create_baremetal_instance(**kwargs, auto_external_ip_attachment=True)
    except RuntimeError as exc:
        detail = str(exc)
        if "InvalidArgument" not in detail:
            raise
        return grpc.create_baremetal_instance(**kwargs)


def _nat_gateway_ids(grpc: GRPCClient) -> list[str]:
    return [str(item.get("id") or "") for item in grpc.call(service="osac.public.v1.NATGateways/List").get("items", [])]


def _wait_owned_gone(
    *,
    description: str,
    api_gone,
    cr_gone,
    delete=None,
    retries: int = _CHILD_RETRIES,
    delay: int = _POLL_DELAY,
) -> None:
    """Poll until API and K8s CR are gone. Re-issue delete while they linger."""

    def _tick() -> bool:
        if delete is not None:
            try:
                delete()
            except Exception:  # noqa: BLE001 — already gone / still deprovisioning
                pass
        return bool(api_gone()) and bool(cr_gone())

    poll_until(
        fn=_tick,
        until=lambda gone: gone is True,
        retries=retries,
        delay=delay,
        description=description,
    )


def _teardown_nat(grpc: GRPCClient, k8s: K8sClient, net: dict[str, Any]) -> None:
    nat_id = net.get("nat_id")
    if not nat_id:
        return
    vnet_name = net.get("vnet_name") or nat_id
    _wait_owned_gone(
        description=f"{vnet_name} NATGateway gone",
        api_gone=lambda nid=nat_id: nid not in _nat_gateway_ids(grpc),
        cr_gone=lambda nid=nat_id: k8s.get_nat_gateway_name(uuid=nid, checked=False) == "",
        delete=lambda nid=nat_id: grpc.delete_nat_gateway(nat_gateway_id=nid),
        retries=_FABRIC_RETRIES,
    )


def _teardown_nat_eip(grpc: GRPCClient, k8s: K8sClient, net: dict[str, Any]) -> None:
    eip_id = net.get("nat_eip_id")
    if not eip_id:
        return
    vnet_name = net.get("vnet_name") or eip_id
    _wait_owned_gone(
        description=f"{vnet_name} NAT EIP gone",
        api_gone=lambda eid=eip_id: eid not in grpc.list_external_ip_ids(),
        cr_gone=lambda eid=eip_id: k8s.get_external_ip_name(uuid=eid, checked=False) == "",
        delete=lambda eid=eip_id: grpc.delete_external_ip(external_ip_id=eid),
    )


def _teardown_sg(grpc: GRPCClient, k8s: K8sClient, net: dict[str, Any]) -> None:
    sg_id = net.get("sg_id")
    if not sg_id:
        return
    vnet_name = net.get("vnet_name") or sg_id
    _wait_owned_gone(
        description=f"{vnet_name} SecurityGroup gone",
        api_gone=lambda sid=sg_id: sid not in grpc.list_security_group_ids(),
        cr_gone=lambda sid=sg_id: k8s.get_security_group_name(uuid=sid, checked=False) == "",
        delete=lambda sid=sg_id: grpc.delete_security_group(sg_id=sid),
    )


def _teardown_subnets(grpc: GRPCClient, k8s: K8sClient, net: dict[str, Any]) -> None:
    vnet_name = net.get("vnet_name") or "subnet"
    for sub_id in (net.get("sub_a_id"), net.get("sub_b_id")):
        if not sub_id:
            continue
        _wait_owned_gone(
            description=f"{vnet_name} subnet {sub_id} gone",
            api_gone=lambda sid=sub_id: sid not in grpc.list_subnet_ids(),
            cr_gone=lambda sid=sub_id: k8s.get_subnet_name(uuid=sid, checked=False) == "",
            delete=lambda sid=sub_id: grpc.delete_subnet(subnet_id=sid),
            retries=_FABRIC_RETRIES,
        )


def _teardown_vnet(grpc: GRPCClient, k8s: K8sClient, net: dict[str, Any]) -> None:
    vnet_id = net.get("vnet_id")
    if not vnet_id:
        return
    vnet_name = net.get("vnet_name") or vnet_id
    _wait_owned_gone(
        description=f"{vnet_name} VirtualNetwork gone",
        api_gone=lambda vid=vnet_id: vid not in grpc.list_virtual_network_ids(),
        cr_gone=lambda vid=vnet_id: k8s.get_virtual_network_name(uuid=vid, checked=False) == "",
        delete=lambda vid=vnet_id: grpc.delete_virtual_network(vn_id=vid),
        retries=_FABRIC_RETRIES,
    )


def _teardown_owned_resources(
    state: dict[str, Any], grpc: GRPCClient, k8s: K8sClient, bmh_namespace: str
) -> None:
    """Delete only IDs recorded in ``state``. Safe on a partial run."""
    if state.get("_teardown_done"):
        return
    errors: list[BaseException] = []

    def _phase(fn) -> None:  # noqa: ANN001
        try:
            fn()
        except Exception as exc:  # noqa: BLE001 — keep remaining teardown phases
            errors.append(exc)

    if "ingress_attach_id" in state:
        aid = state["ingress_attach_id"]
        _phase(
            lambda: _wait_owned_gone(
                description=f"ingress attachment {aid} gone",
                api_gone=lambda: aid not in grpc.list_external_ip_attachment_ids(),
                cr_gone=lambda: k8s.get_external_ip_attachment_name(uuid=aid, checked=False) == "",
                delete=lambda: grpc.delete_external_ip_attachment(attachment_id=aid),
            )
        )
    if "ingress_eip_id" in state:
        eip_id = state["ingress_eip_id"]
        _phase(
            lambda: _wait_owned_gone(
                description=f"ingress EIP {eip_id} gone",
                api_gone=lambda: eip_id not in grpc.list_external_ip_ids(),
                cr_gone=lambda: k8s.get_external_ip_name(uuid=eip_id, checked=False) == "",
                delete=lambda: grpc.delete_external_ip(external_ip_id=eip_id),
            )
        )

    for key in ("t2_b", "t1_a"):
        bmi = state.get(key) or {}
        auto_attach_id = bmi.get("auto_attach_id")
        auto_eip_id = bmi.get("auto_eip_id")
        if auto_attach_id:
            _phase(
                lambda aid=auto_attach_id, k=key: _wait_owned_gone(
                    description=f"{k} auto-EIP attachment gone",
                    api_gone=lambda: aid not in grpc.list_external_ip_attachment_ids(),
                    cr_gone=lambda: k8s.get_external_ip_attachment_name(uuid=aid, checked=False) == "",
                    delete=lambda: grpc.delete_external_ip_attachment(attachment_id=aid),
                )
            )
        if auto_eip_id:
            _phase(
                lambda eid=auto_eip_id, k=key: _wait_owned_gone(
                    description=f"{k} auto-EIP gone",
                    api_gone=lambda: eid not in grpc.list_external_ip_ids(),
                    cr_gone=lambda: k8s.get_external_ip_name(uuid=eid, checked=False) == "",
                    delete=lambda: grpc.delete_external_ip(external_ip_id=eid),
                )
            )

    bmis = list(state.get("bmis") or [])
    for bmi in bmis:
        try:
            grpc.delete_baremetal_instance(bmi_id=bmi["id"])
        except Exception:  # noqa: BLE001 — already gone
            pass
    for bmi in bmis:

        def _wait_bmi(target: dict[str, Any] = bmi) -> None:
            if target.get("cr"):
                wait_for_bmi_deletion(k8s=k8s, name=target["cr"])
            wait_for_bmi_grpc_removal(grpc=grpc, uuid=target["id"])
            if target.get("bmh"):
                wait_for_bmh_available(k8s=k8s, name=target["bmh"], bmh_namespace=bmh_namespace)

        _phase(_wait_bmi)

    stacks = [net for net in (state.get("t1_net"), state.get("t2_net")) if net]
    for net in stacks:
        _phase(lambda n=net: _teardown_nat(grpc, k8s, n))
    for net in stacks:
        _phase(lambda n=net: _teardown_nat_eip(grpc, k8s, n))
    for net in stacks:
        _phase(lambda n=net: _teardown_sg(grpc, k8s, n))
    for net in stacks:
        _phase(lambda n=net: _teardown_subnets(grpc, k8s, n))
    for net in stacks:
        _phase(lambda n=net: _teardown_vnet(grpc, k8s, n))

    if state.get("pool_owned") and state.get("pool_id"):
        pool_id = state["pool_id"]

        def _wait_pool() -> None:
            try:
                grpc.delete_external_ip_pool(pool_id=pool_id)
            except Exception:  # noqa: BLE001 — already gone
                pass
            _wait_owned_gone(
                description=f"ExternalIPPool {pool_id} gone",
                api_gone=lambda: pool_id not in grpc.list_external_ip_pool_ids(),
                cr_gone=lambda: k8s.get_external_ip_pool_name(uuid=pool_id, checked=False) == "",
                retries=_FABRIC_RETRIES,
            )

        _phase(_wait_pool)
    if errors:
        raise RuntimeError(f"{len(errors)} teardown phase(s) failed: {errors[0]}") from errors[0]
    state["_teardown_done"] = True


class TestCrossTenantBmaasNetworking:
    state: ClassVar[dict[str, Any]] = {}

    @pytest.fixture(scope="class", autouse=True)
    @classmethod
    def _owned_resource_finalizer(
        cls, private_grpc: GRPCClient, k8s_hub_client: K8sClient, bmh_namespace: str
    ) -> Iterator[None]:
        yield
        _teardown_owned_resources(cls.state, private_grpc, k8s_hub_client, bmh_namespace)

    def test_00_resolve_tenants(self, private_grpc: GRPCClient) -> None:
        """Reuse session tenants (ensure_tenants: tenant1/tenant2). Do not create or delete them."""
        t1, t2 = "tenant1", "tenant2"
        for name in (t1, t2):
            assert name != "shared"
            assert private_grpc.find_tenant_id(name=name)
        self.__class__.state.update(t1=t1, t2=t2)

    def test_01_resolve_external_ip_pool(
        self,
        private_grpc: GRPCClient,
        k8s_hub_client: K8sClient,
        net_test_run_id: str,
        external_ip_pool_cidr: str,
    ) -> None:
        def _cidrs(item: dict[str, Any]) -> list[str]:
            spec = item.get("spec") or {}
            return [str(c) for c in (spec.get("cidrs") or [])]

        def _state(item: dict[str, Any]) -> str:
            return str((item.get("status") or {}).get("state") or "")

        def _adopt(item: dict[str, Any], *, owned: bool) -> None:
            pool_id = str(item.get("id") or "")
            assert pool_id
            print(f"Reusing ExternalIPPool {pool_id} name={(item.get('metadata') or {}).get('name')} state={_state(item)}")
            self.__class__.state.update(pool_id=pool_id, pool_owned=owned)
            if _state(item) in ("EXTERNAL_IP_POOL_STATE_READY", "Ready"):
                return
            pool_cr = wait_for_external_ip_pool_cr(k8s=k8s_hub_client, uuid=pool_id)
            wait_for_external_ip_pool_ready(k8s=k8s_hub_client, name=pool_cr)
            wait_for_external_ip_pool_grpc_ready(private_grpc=private_grpc, pool_id=pool_id)
            self.__class__.state["pool_cr"] = pool_cr

        listed = private_grpc.call(service="osac.private.v1.ExternalIPPools/List").get("items") or []
        ready = next(
            (
                item
                for item in listed
                if item.get("id") and _state(item) in ("EXTERNAL_IP_POOL_STATE_READY", "Ready")
            ),
            None,
        )
        matching = next(
            (item for item in listed if item.get("id") and external_ip_pool_cidr in _cidrs(item)),
            None,
        )
        if ready is not None:
            _adopt(ready, owned=False)
            return
        if matching is not None:
            _adopt(matching, owned=False)
            return

        pool_name = f"xt-pool-{net_test_run_id}"
        try:
            pool_id = private_grpc.create_external_ip_pool(
                name=pool_name, cidrs=[external_ip_pool_cidr], implementation_strategy="netris"
            )
        except subprocess.CalledProcessError as exc:
            output = (exc.output or "") + (getattr(exc, "stderr", None) or "")
            if "AlreadyExists" not in output and "already exists" not in output.lower():
                raise RuntimeError(f"ExternalIPPools/Create failed: {output}") from exc
            listed = private_grpc.call(service="osac.private.v1.ExternalIPPools/List").get("items") or []
            matching = next(
                (item for item in listed if item.get("id") and external_ip_pool_cidr in _cidrs(item)),
                None,
            )
            if matching is None:
                matching = next((item for item in listed if item.get("id")), None)
            if matching is None:
                raise RuntimeError(f"ExternalIPPools/Create AlreadyExists but List is empty: {output}") from exc
            _adopt(matching, owned=False)
            return
        self.__class__.state.update(pool_id=pool_id, pool_owned=True)
        pool_cr = wait_for_external_ip_pool_cr(k8s=k8s_hub_client, uuid=pool_id)
        wait_for_external_ip_pool_ready(k8s=k8s_hub_client, name=pool_cr)
        wait_for_external_ip_pool_grpc_ready(private_grpc=private_grpc, pool_id=pool_id)
        self.__class__.state["pool_cr"] = pool_cr

    def test_02_create_tenant_networks(
        self, private_grpc: GRPCClient, k8s_hub_client: K8sClient, net_test_run_id: str
    ) -> None:
        _require(self.state, "t1", "t2", "pool_id")
        for key, cidr_base in (("t2", "10.101"), ("t1", "10.102")):
            tenant = self.state[key]
            vnet_name = f"{key}-net-{net_test_run_id}"
            net: dict[str, Any] = {"tenant": tenant, "vnet_name": vnet_name, "cidr_base": cidr_base}
            self.__class__.state[f"{key}_net"] = net
            net["vnet_id"] = private_grpc.create_virtual_network(
                name=vnet_name, ipv4_cidr=f"{cidr_base}.0.0/16", tenant=tenant
            )
            net["vnet_cr"] = wait_for_virtual_network_cr(k8s=k8s_hub_client, uuid=net["vnet_id"])
            wait_for_virtual_network_ready(k8s=k8s_hub_client, name=net["vnet_cr"])
            net["sub_a_id"] = private_grpc.create_subnet(
                name=f"{key}-sa-{net_test_run_id}",
                virtual_network=net["vnet_id"],
                ipv4_cidr=f"{cidr_base}.1.0/24",
                tenant=tenant,
            )
            net["sub_b_id"] = private_grpc.create_subnet(
                name=f"{key}-sb-{net_test_run_id}",
                virtual_network=net["vnet_id"],
                ipv4_cidr=f"{cidr_base}.2.0/24",
                tenant=tenant,
            )
            net["sub_a_cr"] = wait_for_subnet_cr(k8s=k8s_hub_client, uuid=net["sub_a_id"])
            net["sub_b_cr"] = wait_for_subnet_cr(k8s=k8s_hub_client, uuid=net["sub_b_id"])
            wait_for_subnet_ready(k8s=k8s_hub_client, name=net["sub_a_cr"])
            wait_for_subnet_ready(k8s=k8s_hub_client, name=net["sub_b_cr"])
            net["sg_id"] = private_grpc.create_security_group_with_rules(
                name=f"{key}-sg-{net_test_run_id}",
                virtual_network=net["vnet_id"],
                ingress=_SSH_ICMP_INGRESS,
                egress=_ALL_EGRESS,
                tenant=tenant,
            )
            net["sg_cr"] = wait_for_security_group_cr(k8s=k8s_hub_client, uuid=net["sg_id"])
            wait_for_security_group_ready(k8s=k8s_hub_client, name=net["sg_cr"])
            nat_eip_name = f"{key}-nat-eip-{net_test_run_id}"
            net["nat_eip_name"] = nat_eip_name
            net["nat_eip_id"] = private_grpc.create_external_ip(
                name=nat_eip_name, pool=self.state["pool_id"], tenant=tenant
            )
            net["nat_eip_cr"] = wait_for_external_ip_cr(k8s=k8s_hub_client, uuid=net["nat_eip_id"])
            wait_for_external_ip_allocated(k8s=k8s_hub_client, name=net["nat_eip_cr"])
            net["nat_id"] = private_grpc.create_nat_gateway(
                name=f"{key}-nat-{net_test_run_id}",
                virtual_network_name=vnet_name,
                external_ip_name=nat_eip_name,
                tenant=tenant,
            )
            poll_until(
                fn=lambda nid=net["nat_id"]: (
                    private_grpc.call(service="osac.public.v1.NATGateways/Get", data={"id": nid})
                    .get("object", {})
                    .get("status", {})
                    .get("state", "")
                ),
                until=lambda s: s in ("NAT_GATEWAY_STATE_READY", "Ready"),
                retries=45,
                delay=5,
                description=f"NATGateway for {tenant}",
            )
            net["nat_cr"] = poll_until(
                fn=lambda nid=net["nat_id"]: k8s_hub_client.get_nat_gateway_name(uuid=nid, checked=False),
                until=lambda name: name != "",
                retries=30,
                delay=2,
                description=f"NATGateway CR for {tenant}",
            )

    def test_03_create_bmis(
        self,
        private_grpc: GRPCClient,
        k8s_hub_client: K8sClient,
        catalog_item_name: str,
        auto_eip_catalog_item_name: str,
        net_ssh_public_key: str,
        net_test_run_id: str,
        bmh_ssh_hosts: dict[str, str],
        bmi_user_data: str,
        bmi_instance_type: str,
    ) -> None:
        _require(self.state, "t2_net", "t1_net")
        t2 = self.state["t2_net"]
        t1 = self.state["t1_net"]
        specs = [
            ("t2_a", "t2a", t2["tenant"], catalog_item_name, t2["sub_a_id"], t2["sg_id"], False, t2["cidr_base"] + ".1."),
            (
                "t2_b",
                "t2b",
                t2["tenant"],
                auto_eip_catalog_item_name,
                t2["sub_b_id"],
                t2["sg_id"],
                True,
                t2["cidr_base"] + ".2.",
            ),
            (
                "t1_a",
                "t1a",
                t1["tenant"],
                auto_eip_catalog_item_name,
                t1["sub_a_id"],
                t1["sg_id"],
                True,
                t1["cidr_base"] + ".1.",
            ),
        ]
        disk_image = _lab_disk_image_name(private_grpc, catalog_item_name, auto_eip_catalog_item_name)
        bmis: list[dict[str, Any]] = list(self.__class__.state.get("bmis") or [])
        for key, name_prefix, tenant, catalog, subnet, sg, auto_eip, ip_prefix in specs:
            name = f"{name_prefix}-{net_test_run_id}"
            bmi_id = _create_bmi(
                private_grpc,
                name=name,
                catalog=catalog,
                subnet_id=subnet,
                security_group_id=sg,
                ssh_public_key=net_ssh_public_key,
                tenant=tenant,
                disk_image=disk_image,
                user_data=bmi_user_data,
                auto_eip=auto_eip,
                instance_type=bmi_instance_type,
            )
            assert tenant != "shared"
            rec = {
                "key": key,
                "name": name,
                "id": bmi_id,
                "tenant": tenant,
                "ip_prefix": ip_prefix,
                "auto_eip": auto_eip,
            }
            bmis.append(rec)
            self.__class__.state[key] = rec
            self.__class__.state["bmis"] = bmis
        for bmi in bmis:
            bmi["cr"] = wait_for_bmi_cr(k8s=k8s_hub_client, uuid=bmi["id"])
            wait_for_bmi_running(grpc=private_grpc, bmi_id=bmi["id"], retries=_NETRIS_BMI_RUNNING_RETRIES)
            bmi["ip"] = poll_until(
                fn=lambda b=bmi: k8s_hub_client.get_baremetal_instance_tenant_ip(name=b["cr"]),
                until=lambda ip: ip != "",
                retries=60,
                delay=10,
                description=f"tenant IP for {bmi['name']}",
            )
            assert bmi["ip"].startswith(bmi["ip_prefix"]), bmi["ip"]
            ext_host = k8s_hub_client.get_baremetal_instance_external_host_id(name=bmi["cr"])
            bmi["bmh"] = ext_host.split("/", 1)[1]
            bmi["ssh_host"] = bmi_ssh.get_ssh_host(bmi["bmh"], bmh_ssh_hosts)
        by_key = {b["key"]: b for b in bmis}
        self.__class__.state.update(t2_a=by_key["t2_a"], t2_b=by_key["t2_b"], t1_a=by_key["t1_a"], bmis=bmis)

    def test_04_verify_auto_eip(self, private_grpc: GRPCClient) -> None:
        _require(self.state, "t2_b", "t1_a")
        for key in ("t2_b", "t1_a"):
            bmi = self.state[key]

            bmi_id = bmi["id"]

            def find(target: str = bmi_id) -> dict[str, Any] | None:
                attachments = private_grpc.call(service="osac.public.v1.ExternalIPAttachments/List")
                for item in attachments.get("items", []):
                    spec = item.get("spec", {})
                    bmi_ref = (spec.get("baremetalInstance") or spec.get("baremetal_instance") or {}).get("id", "")
                    if bmi_ref == target:
                        return item
                return None

            attachment = poll_until(
                fn=find,
                until=lambda a: a is not None,
                retries=60,
                delay=5,
                description=f"auto-EIP {key}",
            )
            eip_id = (attachment.get("spec", {}).get("externalIp") or attachment.get("spec", {}).get("external_ip") or {}).get(
                "id", ""
            )
            assert eip_id
            bmi["auto_attach_id"] = attachment["id"]
            bmi["auto_eip_id"] = eip_id

    @staticmethod
    def _subnet_gateway(tenant_ip: str) -> str:
        """Netris V-Net gateway is .1 on the BMI's /24 (e.g. 10.101.1.2 → 10.101.1.1)."""
        return tenant_ip.rsplit(".", 1)[0] + ".1"

    def test_05_cross_tenant_isolation(self) -> None:
        """Flow 14: tenant1 ↔ tenant2 BMI traffic must fail (separate VPCs).

        Precondition: each BMI can reach its own subnet gateway so a failed
        cross-tenant ping is isolation, not a dead tenant NIC.

        Same-tenant same-subnet (Flow 11) and same-tenant cross-subnet (Flow 12)
        are covered by test_bmaas_networking — do not re-assert them here.
        """
        _require(self.state, "t2_a", "t1_a")
        t2_a = self.state["t2_a"]
        t1_a = self.state["t1_a"]
        t2_gw = self._subnet_gateway(t2_a["ip"])
        t1_gw = self._subnet_gateway(t1_a["ip"])

        poll_until(
            fn=lambda: guest_ssh.ping(t2_a["ssh_host"], t2_gw),
            until=lambda ok: ok,
            retries=12,
            delay=5,
            description=f"tenant2 BMI reachable to own GW {t2_gw} (from {t2_a['ip']})",
        )
        poll_until(
            fn=lambda: guest_ssh.ping(t1_a["ssh_host"], t1_gw),
            until=lambda ok: ok,
            retries=12,
            delay=5,
            description=f"tenant1 BMI reachable to own GW {t1_gw} (from {t1_a['ip']})",
        )

        assert not guest_ssh.ping(t2_a["ssh_host"], t1_a["ip"]), (
            f"ping tenant2 ({t2_a['ip']}) → tenant1 ({t1_a['ip']}) succeeded — "
            f"cross-tenant traffic must be blocked"
        )
        assert not guest_ssh.ping(t1_a["ssh_host"], t2_a["ip"]), (
            f"ping tenant1 ({t1_a['ip']}) → tenant2 ({t2_a['ip']}) succeeded — "
            f"cross-tenant traffic must be blocked"
        )

    def test_06_nat_egress_and_ingress_dnat(
        self, private_grpc: GRPCClient, k8s_hub_client: K8sClient, net_test_run_id: str
    ) -> None:
        _require(self.state, "t2_a", "pool_id", "t2")
        t2_a = self.state["t2_a"]
        poll_until(
            fn=lambda: guest_ssh.curl_status(t2_a["ssh_host"], "https://quay.io"),
            until=lambda status: status == 200,
            retries=5,
            delay=15,
            description="NAT egress",
        )
        eip_name = f"xt-ing-{net_test_run_id}"
        eip_id = private_grpc.create_external_ip(name=eip_name, pool=self.state["pool_id"], tenant=self.state["t2"])
        self.__class__.state["ingress_eip_id"] = eip_id
        eip_cr = wait_for_external_ip_cr(k8s=k8s_hub_client, uuid=eip_id)
        wait_for_external_ip_allocated(k8s=k8s_hub_client, name=eip_cr)
        attach_id = private_grpc.create_external_ip_attachment_bmi(
            name=f"xt-ing-att-{net_test_run_id}",
            external_ip=eip_id,
            baremetal_instance=t2_a["id"],
            tenant=self.state["t2"],
        )
        self.__class__.state["ingress_attach_id"] = attach_id
        attach_cr = wait_for_external_ip_attachment_cr(k8s=k8s_hub_client, uuid=attach_id)
        wait_for_external_ip_attachment_ready(k8s=k8s_hub_client, name=attach_cr)
        ext_addr = (
            private_grpc.get_external_ip(external_ip_id=eip_id).get("object", {}).get("status", {}).get("address", "")
        )
        assert ext_addr
        def _try_ssh() -> str:
            try:
                return guest_ssh.ssh_via_external_ip(ext_addr)
            except subprocess.CalledProcessError:
                return ""

        poll_until(
            fn=_try_ssh,
            until=lambda h: bool(h),
            retries=12,
            delay=20,
            description=f"SSH via {ext_addr}",
        )
        self.__class__.state.update(
            ingress_eip_id=eip_id, ingress_eip_cr=eip_cr, ingress_attach_id=attach_id, ingress_attach_cr=attach_cr
        )

    def test_07_security_group_deny(self, private_grpc: GRPCClient) -> None:
        """ICMP deny after dropping ICMP from the SG. OSAC-4888: set OSAC_SG_DENY_WORKAROUND=1 to skip."""
        _require(self.state, "t2_net", "t2_a", "t2_b")
        t2_net = self.state["t2_net"]
        t2_a = self.state["t2_a"]
        t2_b = self.state["t2_b"]
        poll_until(
            fn=lambda: guest_ssh.ping(t2_a["ssh_host"], t2_b["ip"]),
            until=lambda ok: ok,
            retries=12,
            delay=5,
            description="ICMP allowed before SG deny",
        )
        private_grpc.update_security_group_rules(
            sg_id=t2_net["sg_id"],
            ingress=[{"protocol": "PROTOCOL_TCP", "port_from": 22, "port_to": 22, "ipv4_cidr": "0.0.0.0/0"}],
            egress=_ALL_EGRESS,
        )
        try:
            poll_until(
                fn=lambda: not guest_ssh.ping(t2_a["ssh_host"], t2_b["ip"]),
                until=lambda ok: ok,
                retries=8,
                delay=5,
                description="ICMP blocked after SG deny",
            )
        except TimeoutError:
            if _sg_deny_workaround():
                pytest.skip("OSAC_SG_DENY_WORKAROUND=1: OSAC-4888 deny not enforced")
            raise

    def test_08_delete_owned_resources(
        self, private_grpc: GRPCClient, k8s_hub_client: K8sClient, bmh_namespace: str
    ) -> None:
        """Delete only objects this class created (IDs in self.state).

        Networking teardown is reverse of test_02, by kind across both tenants:
        NAT → NAT EIP → SG → subnets → VNet. VNets are last. Adopted
        ExternalIPPool (pool_owned=False) is left in place. The class autouse
        finalizer runs the same path if this test is skipped by ``-x``.
        """
        _teardown_owned_resources(self.state, private_grpc, k8s_hub_client, bmh_namespace)
