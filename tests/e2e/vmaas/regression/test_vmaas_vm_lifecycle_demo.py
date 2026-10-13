"""Demo 3 CLI path from NetworkClass through ComputeInstance delete.

Serial-console login stays in test_console.py. This test checks that the
guest address is inside the subnet created here.
"""

from __future__ import annotations

import ipaddress
import re
from datetime import UTC, datetime
from typing import Any
from uuid import uuid4

import pytest

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import (
    allocate_worker_subnet,
    unique_name,
    wait_for_cr,
    wait_for_deletion,
    wait_for_grpc_removal,
    wait_for_new_vmi,
    wait_for_restart,
    wait_for_running,
    wait_for_security_group_cr,
    wait_for_security_group_ready,
    wait_for_vmi_ip,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import poll_until
from tests.e2e.vmaas.networking_lifecycle_helpers import (
    create_and_wait_for_subnet,
    create_and_wait_for_virtual_network,
    delete_and_wait_for_security_group,
    delete_and_wait_for_subnet,
    delete_and_wait_for_virtual_network,
)

pytestmark = pytest.mark.regression

_CUDN_MANAGERS = {"k8s_only", "cudn_localnet", "cudn_evpn"}


def _k8s_manager(item: dict[str, Any]) -> str:
    value = item.get("k8s_manager") or item.get("k8sManager") or ""
    return str(value)


def _attachment_refs(instance: dict[str, Any]) -> tuple[str, list[str]]:
    spec = instance.get("spec") or {}
    attachments = spec.get("network_attachments") or spec.get("networkAttachments") or []
    assert len(attachments) == 1, f"expected one network attachment, got {len(attachments)}"
    attachment = attachments[0]
    subnet = attachment.get("subnet") or {}
    groups = attachment.get("security_groups") or attachment.get("securityGroups") or []
    return str(subnet.get("id") or ""), [str(group.get("id") or "") for group in groups]


def test_vmaas_vm_lifecycle_demo(
    cli: OsacCLI,
    grpc: GRPCClient,
    private_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    k8s_virt_client: K8sClient,
    vm_template: str,
    default_storage_tier: str,
    default_disk_image: str,
) -> None:
    """Order one VM on a tenant network, stop it, start it, and restart it."""
    tag = uuid4().hex[:8]
    subnet_net = allocate_worker_subnet()
    it_name = f"e2e-it-{tag}"
    it_created = False
    catalog_id: str | None = None
    vn_id: str | None = None
    vn_cr_name: str | None = None
    subnet_id: str | None = None
    subnet_cr_name: str | None = None
    sg_id: str | None = None
    sg_cr_name: str | None = None
    ci_uuid: str | None = None
    ci_name: str | None = None

    try:
        classes = private_grpc.list_network_classes()
        managers = [_k8s_manager(item) for item in classes]
        assert any(manager in _CUDN_MANAGERS for manager in managers), (
            f"expected a CUDN-backed VMaaS NetworkClass, got {managers}"
        )

        cli.create_instance_type(name=it_name, vcpus=1, memory_gib=2)
        it_created = True
        described = cli.describe_instance_type(name=it_name)
        assert "ACTIVE" in described
        assert re.search(r"vCPUs:\s+1\b", described)
        assert re.search(r"Memory \(GiB\):\s+2\b", described)

        catalog_id = grpc.create_compute_instance_catalog_item(
            name=f"e2e-catalog-{tag}",
            template=vm_template,
            fields={
                "boot_disk": {
                    "storage_tier": {"editable": {"default_value": {"name": default_storage_tier}}},
                    "size_gib": {"editable": {"default_value": 10}},
                },
                "disk_image": {"editable": {"default_value": {"name": default_disk_image}}},
                "instance_type": {"editable": {"default_value": {"name": it_name}}},
                "network_attachments": {"editable": {}},
                "run_strategy": {"editable": {}},
            },
        )
        assert catalog_id in grpc.list_compute_instance_catalog_item_ids()

        vn_id, vn_cr_name = create_and_wait_for_virtual_network(grpc, k8s_hub_client, f"e2e-vnet-{tag}", str(subnet_net))
        assert vn_id in grpc.list_virtual_network_ids()

        subnet_id, subnet_cr_name = create_and_wait_for_subnet(
            grpc, k8s_hub_client, vn_id, str(subnet_net), name_prefix=f"e2e-subnet-{tag}"
        )

        sg_name = f"e2e-sg-{tag}"
        sg_id = grpc.create_security_group_with_rules(
            name=sg_name,
            virtual_network=vn_id,
            ingress=[
                {"protocol": "PROTOCOL_TCP", "port_from": 22, "port_to": 22, "ipv4_cidr": "0.0.0.0/0"},
                {"protocol": "PROTOCOL_ICMP", "ipv4_cidr": "0.0.0.0/0"},
            ],
            egress=[{"protocol": "PROTOCOL_ALL", "ipv4_cidr": "0.0.0.0/0"}],
        )
        sg_cr_name = wait_for_security_group_cr(k8s=k8s_hub_client, uuid=sg_id)
        wait_for_security_group_ready(k8s=k8s_hub_client, name=sg_cr_name)
        assert sg_id in grpc.list_security_group_ids()

        ci_uuid = cli.create_compute_instance_with_catalog_item(
            catalog_item=catalog_id, name=unique_name("e2e-vm"), subnet=subnet_id, security_groups=[sg_id]
        )
        attached_subnet, attached_groups = _attachment_refs(grpc.get_compute_instance(ci_id=ci_uuid)["object"])
        assert attached_subnet == subnet_id
        assert attached_groups == [sg_id]

        ci_name = wait_for_cr(k8s=k8s_hub_client, uuid=ci_uuid)
        wait_for_running(k8s=k8s_hub_client, name=ci_name)
        vm_ns = k8s_hub_client.get_compute_instance_vm_namespace(name=ci_name)
        guest_ip = wait_for_vmi_ip(k8s=k8s_virt_client, vmi_namespace=vm_ns, compute_instance_name=ci_name)
        assert ipaddress.ip_address(guest_ip) in subnet_net

        grpc.update_compute_instance_run_strategy(ci_id=ci_uuid, run_strategy="Halted")
        poll_until(
            fn=lambda: k8s_hub_client.get_compute_instance_phase(name=ci_name, checked=False),
            until=lambda phase: phase == "Stopped",
            retries=60,
            delay=5,
            description=f"{ci_name} stopped",
        )
        poll_until(
            fn=lambda: k8s_virt_client.get_vmi_json(vmi_namespace=vm_ns, compute_instance_name=ci_name),
            until=lambda vmi: vmi == {},
            retries=30,
            delay=5,
            description=f"{ci_name} VMI gone",
        )

        grpc.update_compute_instance_run_strategy(ci_id=ci_uuid, run_strategy="Always")
        wait_for_running(k8s=k8s_hub_client, name=ci_name)

        original_vmi_ts = k8s_virt_client.get_vmi_creation_timestamp(vmi_namespace=vm_ns, compute_instance_name=ci_name)
        initial_last_restarted = k8s_hub_client.get_compute_instance_last_restarted_at(name=ci_name)
        restart_ts = datetime.now(tz=UTC).strftime("%Y-%m-%dT%H:%M:%SZ")
        grpc.update_restart(uuid=ci_uuid, template=vm_template, timestamp=restart_ts)
        wait_for_restart(k8s=k8s_hub_client, name=ci_name, initial=initial_last_restarted, restart_ts=restart_ts)
        wait_for_running(k8s=k8s_hub_client, name=ci_name)
        new_vmi_ts = wait_for_new_vmi(
            k8s=k8s_virt_client, vmi_namespace=vm_ns, compute_instance_name=ci_name, initial_timestamp=original_vmi_ts
        )
        assert new_vmi_ts > original_vmi_ts

        removed_ci = ci_uuid
        removed_sg = sg_id
        removed_subnet = subnet_id
        removed_vn = vn_id
        cli.delete_compute_instance(uuid=ci_uuid)
        wait_for_deletion(k8s=k8s_hub_client, name=ci_name)
        wait_for_grpc_removal(grpc=grpc, uuid=ci_uuid)
        ci_uuid = None
        ci_name = None
        delete_and_wait_for_security_group(grpc, k8s_hub_client, sg_id, sg_cr_name)
        sg_id = None
        delete_and_wait_for_subnet(grpc, k8s_hub_client, subnet_id, subnet_cr_name)
        subnet_id = None
        delete_and_wait_for_virtual_network(grpc, k8s_hub_client, vn_id, vn_cr_name)
        vn_id = None
        assert removed_ci not in grpc.list_compute_instance_ids()
        assert removed_sg not in grpc.list_security_group_ids()
        assert removed_subnet not in grpc.list_subnet_ids()
        assert removed_vn not in grpc.list_virtual_network_ids()
    finally:
        if ci_uuid is not None:
            cli.delete_compute_instance(uuid=ci_uuid)
            if ci_name is not None:
                wait_for_deletion(k8s=k8s_hub_client, name=ci_name)
            wait_for_grpc_removal(grpc=grpc, uuid=ci_uuid)
        if sg_id is not None:
            delete_and_wait_for_security_group(grpc, k8s_hub_client, sg_id, sg_cr_name)
        if subnet_id is not None:
            delete_and_wait_for_subnet(grpc, k8s_hub_client, subnet_id, subnet_cr_name)
        if vn_id is not None:
            delete_and_wait_for_virtual_network(grpc, k8s_hub_client, vn_id, vn_cr_name)
        if catalog_id is not None:
            grpc.delete_compute_instance_catalog_item(catalog_item_id=catalog_id)
        if it_created:
            cli.delete_instance_type(name=it_name)
