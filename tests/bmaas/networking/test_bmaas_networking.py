from __future__ import annotations

import subprocess
from typing import Any, ClassVar

import pytest

from tests.bmaas.networking import bmi_ssh
from tests.core.grpc_client import GRPCClient
from tests.core.helpers import (
    wait_for_bmh_available,
    wait_for_bmi_cr,
    wait_for_bmi_deletion,
    wait_for_bmi_grpc_removal,
    wait_for_bmi_running,
    wait_for_external_ip_allocated,
    wait_for_external_ip_attachment_cr,
    wait_for_external_ip_attachment_deletion,
    wait_for_external_ip_attachment_ready,
    wait_for_external_ip_cr,
    wait_for_external_ip_deletion,
    wait_for_external_ip_pool_cr,
    wait_for_external_ip_pool_deletion,
    wait_for_external_ip_pool_grpc_ready,
    wait_for_external_ip_pool_ready,
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
from tests.core.k8s_client import K8sClient
from tests.core.osac_cli import OsacCLI
from tests.core.runner import poll_until


def _require(state: dict[str, Any], *keys: str) -> None:
    missing = [k for k in keys if k not in state]
    if missing:
        pytest.skip(f"Prerequisite state missing: {', '.join(missing)}")


class TestBmaasNetworking:
    state: ClassVar[dict[str, Any]] = {}

    # ── Phase 0: External IP Pool ─────────────────────────────────────

    def test_00_create_external_ip_pool(
        self,
        private_grpc: GRPCClient,
        k8s_hub_client: K8sClient,
        external_ip_pool_name: str,
        external_ip_pool_cidr: str,
    ) -> None:
        pool_id = private_grpc.create_external_ip_pool(
            name=external_ip_pool_name,
            cidrs=[external_ip_pool_cidr],
            implementation_strategy="netris",
        )
        pool_cr = wait_for_external_ip_pool_cr(k8s=k8s_hub_client, uuid=pool_id)
        wait_for_external_ip_pool_ready(k8s=k8s_hub_client, name=pool_cr)
        wait_for_external_ip_pool_grpc_ready(private_grpc=private_grpc, pool_id=pool_id)

        self.__class__.state.update(pool_id=pool_id, pool_cr=pool_cr)
        print(f"Created ExternalIPPool {external_ip_pool_name}: {pool_id}")

    # ── Phase 1: Build the Network ──────────────────────────────────────

    def test_01_create_virtual_network(
        self, grpc: GRPCClient, k8s_hub_client: K8sClient, net_test_run_id: str
    ) -> None:
        name = f"net-{net_test_run_id}"
        vnet_id = grpc.create_virtual_network(name=name, ipv4_cidr="10.100.0.0/16")
        vnet_cr = wait_for_virtual_network_cr(k8s=k8s_hub_client, uuid=vnet_id)
        wait_for_virtual_network_ready(k8s=k8s_hub_client, name=vnet_cr)

        self.__class__.state["vnet_id"] = vnet_id
        self.__class__.state["vnet_cr"] = vnet_cr
        self.__class__.state["vnet_name"] = name

    def test_02_create_subnets(self, grpc: GRPCClient, k8s_hub_client: K8sClient, net_test_run_id: str) -> None:
        _require(self.state, "vnet_id")

        subnet_a_name = f"sub-a-{net_test_run_id}"
        subnet_a_id = grpc.create_subnet(
            name=subnet_a_name, virtual_network=self.state["vnet_id"], ipv4_cidr="10.100.1.0/24"
        )
        subnet_a_cr = wait_for_subnet_cr(k8s=k8s_hub_client, uuid=subnet_a_id)
        wait_for_subnet_ready(k8s=k8s_hub_client, name=subnet_a_cr)

        subnet_b_name = f"sub-b-{net_test_run_id}"
        subnet_b_id = grpc.create_subnet(
            name=subnet_b_name, virtual_network=self.state["vnet_id"], ipv4_cidr="10.100.2.0/24"
        )
        subnet_b_cr = wait_for_subnet_cr(k8s=k8s_hub_client, uuid=subnet_b_id)
        wait_for_subnet_ready(k8s=k8s_hub_client, name=subnet_b_cr)

        self.__class__.state.update(
            subnet_a_id=subnet_a_id, subnet_a_cr=subnet_a_cr, subnet_b_id=subnet_b_id, subnet_b_cr=subnet_b_cr
        )

    def test_03_create_security_group(self, grpc: GRPCClient, k8s_hub_client: K8sClient, net_test_run_id: str) -> None:
        _require(self.state, "vnet_id")

        sg_name = f"sg-{net_test_run_id}"
        sg_id = grpc.create_security_group_with_rules(
            name=sg_name,
            virtual_network=self.state["vnet_id"],
            ingress=[
                {"protocol": "PROTOCOL_TCP", "port_from": 22, "port_to": 22, "ipv4_cidr": "0.0.0.0/0"},
                {"protocol": "PROTOCOL_ICMP", "ipv4_cidr": "0.0.0.0/0"},
            ],
            egress=[{"protocol": "PROTOCOL_ALL", "ipv4_cidr": "0.0.0.0/0"}],
        )
        sg_cr = wait_for_security_group_cr(k8s=k8s_hub_client, uuid=sg_id)
        wait_for_security_group_ready(k8s=k8s_hub_client, name=sg_cr)

        self.__class__.state.update(sg_id=sg_id, sg_cr=sg_cr, sg_name=sg_name)

    def test_04_create_nat_gateway(
        self, grpc: GRPCClient, k8s_hub_client: K8sClient, net_test_run_id: str
    ) -> None:
        _require(self.state, "vnet_name", "pool_id")

        nat_eip_name = f"nat-eip-{net_test_run_id}"
        nat_eip_id = grpc.create_external_ip(name=nat_eip_name, pool=self.state["pool_id"])
        nat_eip_cr = wait_for_external_ip_cr(k8s=k8s_hub_client, uuid=nat_eip_id)
        wait_for_external_ip_allocated(k8s=k8s_hub_client, name=nat_eip_cr)

        nat_name = f"nat-{net_test_run_id}"
        nat_id = grpc.create_nat_gateway(
            name=nat_name, virtual_network_name=self.state["vnet_name"], external_ip_name=nat_eip_name
        )
        poll_until(
            fn=lambda: (
                grpc.call(service="osac.public.v1.NATGateways/Get", data={"id": nat_id})
                .get("object", {})
                .get("status", {})
                .get("state", "")
            ),
            until=lambda s: s in ("NAT_GATEWAY_STATE_READY", "Ready"),
            retries=30,
            delay=5,
            description=f"NATGateway {nat_name} to become Ready",
        )

        self.__class__.state.update(
            nat_eip_id=nat_eip_id, nat_eip_cr=nat_eip_cr, nat_eip_name=nat_eip_name, nat_id=nat_id, nat_name=nat_name
        )

    # ── Phase 2: Provision Servers ──────────────────────────────────────

    def test_05_create_three_bmis(
        self,
        cli: OsacCLI,
        grpc: GRPCClient,
        k8s_hub_client: K8sClient,
        catalog_item_name: str,
        auto_eip_catalog_item_name: str,
        net_ssh_public_key: str,
        bmh_namespace: str,
        net_test_run_id: str,
    ) -> None:
        _require(self.state, "subnet_a_id", "subnet_b_id", "sg_id")

        subnet_a = self.state["subnet_a_id"]
        subnet_b = self.state["subnet_b_id"]
        sg = self.state["sg_id"]

        bmis: list[dict[str, str]] = []
        for i, (name_suffix, subnet_id) in enumerate([("bmi1", subnet_a), ("bmi2", subnet_a), ("bmi3", subnet_b)]):
            bmi_name = f"{name_suffix}-{net_test_run_id}"
            is_auto_eip = i == 2
            catalog = auto_eip_catalog_item_name if is_auto_eip else catalog_item_name
            bmi_id = cli.create_baremetal_instance(
                name=bmi_name,
                catalog_item=catalog,
                ssh_key=net_ssh_public_key,
                network_attachments=[f"subnet={subnet_id},interface=eth9,primary,security-groups={sg}"],
                external_ip_attachment=is_auto_eip,
            )
            print(f"Created BMI {bmi_name}: {bmi_id} (catalog: {catalog})")
            bmis.append({"name": bmi_name, "id": bmi_id, "subnet": "a" if i < 2 else "b"})

        for bmi in bmis:
            bmi["cr"] = wait_for_bmi_cr(k8s=k8s_hub_client, uuid=bmi["id"])
            print(f"BMI {bmi['name']} CR: {bmi['cr']}")

        for bmi in bmis:
            wait_for_bmi_running(grpc=grpc, bmi_id=bmi["id"])
            print(f"BMI {bmi['name']} is RUNNING")

        for bmi in bmis:
            bmi["ip"] = poll_until(
                fn=lambda b=bmi: k8s_hub_client.get_baremetal_instance_tenant_ip(name=b["cr"]),
                until=lambda ip: ip != "",
                retries=60,
                delay=10,
                description=f"BMI {bmi['name']} tenant IP assignment",
            )

            ext_host = k8s_hub_client.get_baremetal_instance_external_host_id(name=bmi["cr"])
            bmi["bmh"] = ext_host.split("/", 1)[1]
            bmi["bmc_ip"] = bmi_ssh.get_bmc_ip(bmi["bmh"])
            print(f"BMI {bmi['name']}: tenant_ip={bmi['ip']}, bmh={bmi['bmh']}, bmc_ip={bmi['bmc_ip']}")

        for bmi in bmis:
            if bmi["subnet"] == "a":
                assert bmi["ip"].startswith("10.100.1."), f"BMI {bmi['name']} IP {bmi['ip']} not in subnet A"
            else:
                assert bmi["ip"].startswith("10.100.2."), f"BMI {bmi['name']} IP {bmi['ip']} not in subnet B"

        self.__class__.state["bmi1"] = bmis[0]
        self.__class__.state["bmi2"] = bmis[1]
        self.__class__.state["bmi3"] = bmis[2]

    def test_05b_verify_auto_eip_on_bmi3(self, grpc: GRPCClient) -> None:
        _require(self.state, "bmi3")
        bmi3 = self.state["bmi3"]

        def find_auto_attachment() -> dict[str, Any] | None:
            attachments = grpc.call(service="osac.public.v1.ExternalIPAttachments/List")
            for item in attachments.get("items", []):
                bmi_ref = item.get("spec", {}).get("baremetalInstance", {}).get("id", "")
                if bmi_ref == bmi3["id"]:
                    return item
            return None

        attachment = poll_until(
            fn=find_auto_attachment,
            until=lambda a: a is not None,
            retries=60,
            delay=5,
            description="auto-created ExternalIPAttachment for BMI3",
        )

        auto_attach_id = attachment["id"]
        auto_eip_ref = attachment.get("spec", {}).get("externalIp", {}).get("id", "")
        assert auto_eip_ref, "Auto-created attachment has no ExternalIP reference"

        eip_data = grpc.get_external_ip(external_ip_id=auto_eip_ref)
        auto_ext_addr = eip_data.get("object", {}).get("status", {}).get("address", "")
        assert auto_ext_addr, "Auto-created ExternalIP has no allocated address"

        self.__class__.state.update(
            auto_attach_id=auto_attach_id, auto_eip_id=auto_eip_ref, auto_ext_addr=auto_ext_addr
        )
        print(f"BMI3 auto EIP: {auto_ext_addr}, attachment: {auto_attach_id}")

    # ── Phase 3: Connectivity Tests ─────────────────────────────────────

    def test_06_l2_arping_same_subnet(self) -> None:
        _require(self.state, "bmi1", "bmi2")
        bmi1 = self.state["bmi1"]
        bmi2 = self.state["bmi2"]

        poll_until(
            fn=lambda: bmi_ssh.arping(bmi1["bmc_ip"], bmi2["ip"]),
            until=lambda ok: ok,
            retries=5,
            delay=10,
            description=f"L2 arping BMI1 ({bmi1['ip']}) → BMI2 ({bmi2['ip']})",
        )

    def test_07_l3_ping_same_subnet(self) -> None:
        _require(self.state, "bmi1", "bmi2")
        bmi1 = self.state["bmi1"]
        bmi2 = self.state["bmi2"]

        poll_until(
            fn=lambda: bmi_ssh.ping(bmi1["bmc_ip"], bmi2["ip"]),
            until=lambda ok: ok,
            retries=5,
            delay=10,
            description=f"L3 ping BMI1 ({bmi1['ip']}) → BMI2 ({bmi2['ip']})",
        )

    def test_08_l3_ping_cross_subnet(self) -> None:
        _require(self.state, "bmi1", "bmi3")
        bmi1 = self.state["bmi1"]
        bmi3 = self.state["bmi3"]

        poll_until(
            fn=lambda: bmi_ssh.ping(bmi1["bmc_ip"], bmi3["ip"]),
            until=lambda ok: ok,
            retries=5,
            delay=10,
            description=f"L3 ping BMI1 ({bmi1['ip']}) → BMI3 ({bmi3['ip']}) cross-subnet",
        )

    def test_09_l2_arping_cross_subnet_fails(self) -> None:
        _require(self.state, "bmi1", "bmi3")
        bmi1 = self.state["bmi1"]
        bmi3 = self.state["bmi3"]

        assert not bmi_ssh.arping(bmi1["bmc_ip"], bmi3["ip"]), (
            f"arping from BMI1 ({bmi1['ip']}, subnet A) to BMI3 ({bmi3['ip']}, subnet B) "
            f"succeeded unexpectedly — different subnets should be different broadcast domains"
        )

    def test_10_tenant_isolation(self, mgmt_cluster_ip: str) -> None:
        _require(self.state, "bmi1")
        bmi1 = self.state["bmi1"]

        assert not bmi_ssh.ping(bmi1["bmc_ip"], mgmt_cluster_ip), (
            f"ping from BMI1 ({bmi1['ip']}) to management cluster ({mgmt_cluster_ip}) "
            f"succeeded unexpectedly — tenant isolation should prevent cross-VNet traffic"
        )

    def test_11_nat_gateway_egress(self) -> None:
        _require(self.state, "bmi1")
        bmi1 = self.state["bmi1"]

        poll_until(
            fn=lambda: bmi_ssh.curl_status(bmi1["bmc_ip"], "https://quay.io"),
            until=lambda status: status == 200,
            retries=5,
            delay=15,
            description=f"NAT gateway egress curl quay.io (bmc_ip={bmi1['bmc_ip']}, tenant_ip={bmi1['ip']})",
        )

    def test_12_external_ip_ingress(
        self, grpc: GRPCClient, k8s_hub_client: K8sClient, net_test_run_id: str
    ) -> None:
        _require(self.state, "bmi1", "pool_id")
        bmi1 = self.state["bmi1"]

        eip_name = f"ingress-eip-{net_test_run_id}"
        eip_id = grpc.create_external_ip(name=eip_name, pool=self.state["pool_id"])
        eip_cr = wait_for_external_ip_cr(k8s=k8s_hub_client, uuid=eip_id)
        wait_for_external_ip_allocated(k8s=k8s_hub_client, name=eip_cr)

        attach_name = f"ingress-attach-{net_test_run_id}"
        attach_id = grpc.create_external_ip_attachment_bmi(
            name=attach_name, external_ip=eip_id, baremetal_instance=bmi1["id"]
        )
        attach_cr = wait_for_external_ip_attachment_cr(k8s=k8s_hub_client, uuid=attach_id)
        wait_for_external_ip_attachment_ready(k8s=k8s_hub_client, name=attach_cr)

        eip_data = grpc.get_external_ip(external_ip_id=eip_id)
        ext_addr = eip_data.get("object", {}).get("status", {}).get("address", "")
        assert ext_addr, "ExternalIP has no allocated address"

        def _try_ssh_eip() -> str:
            try:
                return bmi_ssh.ssh_via_external_ip(ext_addr, timeout=10)
            except subprocess.CalledProcessError:
                return ""

        hostname = poll_until(
            fn=_try_ssh_eip,
            until=lambda h: bool(h),
            retries=5,
            delay=15,
            description=f"SSH via external IP {ext_addr}",
        )

        self.__class__.state.update(
            ingress_eip_id=eip_id,
            ingress_eip_cr=eip_cr,
            ingress_attach_id=attach_id,
            ingress_attach_cr=attach_cr,
            ingress_ext_addr=ext_addr,
        )

    # ── Phase 4: Teardown ───────────────────────────────────────────────

    def test_13_delete_external_ip_attachment(self, grpc: GRPCClient, k8s_hub_client: K8sClient) -> None:
        if "ingress_attach_id" not in self.state:
            pytest.skip("No ExternalIPAttachment to delete")

        grpc.delete_external_ip_attachment(attachment_id=self.state["ingress_attach_id"])
        wait_for_external_ip_attachment_deletion(k8s=k8s_hub_client, name=self.state["ingress_attach_cr"])

        grpc.delete_external_ip(external_ip_id=self.state["ingress_eip_id"])
        wait_for_external_ip_deletion(k8s=k8s_hub_client, name=self.state["ingress_eip_cr"])

    def test_14_delete_bmis(
        self, cli: OsacCLI, grpc: GRPCClient, k8s_hub_client: K8sClient, bmh_namespace: str
    ) -> None:
        for key in ("bmi1", "bmi2", "bmi3"):
            if key not in self.state:
                continue
            bmi = self.state[key]
            print(f"Deleting {bmi['name']}...")
            cli.delete_baremetal_instance(uuid=bmi["id"])

        for key in ("bmi1", "bmi2", "bmi3"):
            if key not in self.state:
                continue
            bmi = self.state[key]
            wait_for_bmi_deletion(k8s=k8s_hub_client, name=bmi["cr"])
            wait_for_bmi_grpc_removal(grpc=grpc, uuid=bmi["id"])
            wait_for_bmh_available(k8s=k8s_hub_client, name=bmi["bmh"], bmh_namespace=bmh_namespace)
            print(f"{bmi['name']} deprovisioned, BMH {bmi['bmh']} available")

    def test_14b_verify_auto_eip_garbage_collected(self, grpc: GRPCClient) -> None:
        if "auto_attach_id" not in self.state:
            pytest.skip("No auto EIP to verify")

        poll_until(
            fn=lambda: self.state["auto_attach_id"] not in grpc.list_external_ip_attachment_ids(),
            until=lambda gone: gone is True,
            retries=30,
            delay=5,
            description="auto-created ExternalIPAttachment garbage collection",
        )

        poll_until(
            fn=lambda: self.state["auto_eip_id"] not in grpc.list_external_ip_ids(),
            until=lambda gone: gone is True,
            retries=30,
            delay=5,
            description="auto-created ExternalIP garbage collection",
        )
        print("Auto EIP and attachment garbage collected after BMI3 deletion")

    def test_15_delete_nat_gateway(self, grpc: GRPCClient, k8s_hub_client: K8sClient) -> None:
        if "nat_id" not in self.state:
            pytest.skip("No NATGateway to delete")

        grpc.delete_nat_gateway(nat_gateway_id=self.state["nat_id"])
        poll_until(
            fn=lambda: (
                self.state["nat_id"]
                not in [item["id"] for item in grpc.call(service="osac.public.v1.NATGateways/List").get("items", [])]
            ),
            until=lambda gone: gone is True,
            retries=60,
            delay=5,
            description=f"NATGateway {self.state['nat_name']} deletion",
        )

        grpc.delete_external_ip(external_ip_id=self.state["nat_eip_id"])
        wait_for_external_ip_deletion(k8s=k8s_hub_client, name=self.state["nat_eip_cr"])

    def test_16_delete_security_group(self, grpc: GRPCClient, k8s_hub_client: K8sClient) -> None:
        if "sg_id" not in self.state:
            pytest.skip("No SecurityGroup to delete")

        grpc.delete_security_group(sg_id=self.state["sg_id"])
        wait_for_security_group_deletion(k8s=k8s_hub_client, name=self.state["sg_cr"])

    def test_17_delete_subnets(self, grpc: GRPCClient, k8s_hub_client: K8sClient) -> None:
        for key, cr_key in [("subnet_a_id", "subnet_a_cr"), ("subnet_b_id", "subnet_b_cr")]:
            if key not in self.state:
                continue
            grpc.delete_subnet(subnet_id=self.state[key])
            wait_for_subnet_deletion(k8s=k8s_hub_client, name=self.state[cr_key])

    def test_18_delete_virtual_network(self, grpc: GRPCClient, k8s_hub_client: K8sClient) -> None:
        if "vnet_id" not in self.state:
            pytest.skip("No VirtualNetwork to delete")

        grpc.delete_virtual_network(vn_id=self.state["vnet_id"])
        wait_for_virtual_network_deletion(k8s=k8s_hub_client, name=self.state["vnet_cr"])

        remaining = grpc.list_virtual_network_ids()
        assert self.state["vnet_id"] not in remaining, "VirtualNetwork still in API after deletion"

    def test_19_delete_external_ip_pool(self, private_grpc: GRPCClient, k8s_hub_client: K8sClient) -> None:
        if "pool_id" not in self.state:
            pytest.skip("No ExternalIPPool to delete")

        private_grpc.delete_external_ip_pool(pool_id=self.state["pool_id"])
        wait_for_external_ip_pool_deletion(k8s=k8s_hub_client, name=self.state["pool_cr"])

        remaining = private_grpc.list_external_ip_pool_ids()
        assert self.state["pool_id"] not in remaining, "ExternalIPPool still in API after deletion"
