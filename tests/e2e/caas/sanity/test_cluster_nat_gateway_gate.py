from __future__ import annotations

import pytest

from tests.e2e.core.grpc_client import PRIVATE_API, PUBLIC_API, GRPCClient
from tests.e2e.core.helpers import (
    unique_name,
    wait_for_grpc_subnet_ready,
    wait_for_grpc_virtual_network_ready,
    wait_for_security_group_cr,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.runner import poll_until
from tests.e2e.core.wait_kinds import WaitResourceKind
from tests.e2e.networking.teardown import teardown_sg, teardown_subnets, teardown_vnet

pytestmark = pytest.mark.sanity


def _wait_for_security_group_api_ready(*, grpc: GRPCClient, security_group_id: str) -> None:
    def _state() -> str:
        status = grpc.get_security_group(sg_id=security_group_id).get("object", {}).get("status", {})
        return status.get("state", "")

    poll_until(
        fn=_state,
        until=lambda state: state == "SECURITY_GROUP_STATE_READY",
        retries=60,
        delay=2,
        description=f"SecurityGroup {security_group_id} gRPC READY",
        retry_on_error=True,
        resource_kind=WaitResourceKind.SECURITY_GROUP,
        resource_id=security_group_id,
    )


def test_cluster_create_requires_ready_nat_gateway(
    grpc: GRPCClient,
    private_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    cluster_template: str,
    caas_disk_image_version: str,
    pull_secret_name: str,
    ssh_key_secret: str,
) -> None:
    settings = private_grpc.call(service=f"{PRIVATE_API}.InstallationSettings/Get")
    if not settings.get("networkingProvisioningEnabled", False):
        pytest.skip("Cluster NATGateway readiness is not required when provider networking is disabled")

    private_grpc.ensure_bare_metal_instance_type(
        name="ci-worker-bm", host_label_selector={"osac.openshift.io/host-type": "default"}
    )

    network = {"vnet_name": unique_name("nat-gate-vnet")}
    try:
        network["vnet_id"] = grpc.create_virtual_network(name=network["vnet_name"], ipv4_cidr="172.22.0.0/16")
        wait_for_grpc_virtual_network_ready(grpc=grpc, vn_id=network["vnet_id"])

        network["sub_a_id"] = grpc.create_subnet(
            name=unique_name("nat-gate-subnet"), virtual_network=network["vnet_id"], ipv4_cidr="172.22.1.0/24"
        )
        wait_for_grpc_subnet_ready(grpc=grpc, subnet_id=network["sub_a_id"])

        network["sg_id"] = grpc.create_security_group(
            name=unique_name("nat-gate-sg"), virtual_network=network["vnet_id"]
        )
        wait_for_security_group_cr(k8s=k8s_hub_client, uuid=network["sg_id"])
        _wait_for_security_group_api_ready(grpc=grpc, security_group_id=network["sg_id"])

        cluster_name = unique_name("nat-gate-cluster")
        output, return_code = grpc.call_unchecked(
            service=f"{PUBLIC_API}.Clusters/Create",
            data={
                "object": {
                    "metadata": {"name": cluster_name},
                    "spec": {
                        "template": {"name": cluster_template},
                        "version": {"name": caas_disk_image_version},
                        "node_sets": {"workers": {"size": 1, "baremetal_instance_type": {"name": "ci-worker-bm"}}},
                        "pull_secret_secret": {"name": pull_secret_name},
                        "ssh_key": {"name": ssh_key_secret},
                        "network_attachment": {
                            "subnet": {"id": network["sub_a_id"]},
                            "security_groups": [{"id": network["sg_id"]}],
                        },
                    },
                }
            },
        )
        assert return_code != 0, "Cluster Create unexpectedly accepted a VirtualNetwork without a NATGateway"
        assert "FailedPrecondition" in output, f"Expected FailedPrecondition, got: {output}"
        assert "READY NATGateway" in output, f"Expected the NATGateway readiness detail, got: {output}"
        assert not grpc.list_with_filter(
            service=f"{PUBLIC_API}.Clusters/List", filter_expr=f'this.metadata.name == "{cluster_name}"'
        ), "Rejected Cluster Create must not store a Cluster"
    finally:
        teardown_sg(grpc, k8s_hub_client, network)
        teardown_subnets(grpc, k8s_hub_client, network)
        teardown_vnet(grpc, k8s_hub_client, network)
