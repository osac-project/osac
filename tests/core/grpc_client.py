from __future__ import annotations

import json
from typing import Any

from tests.core.runner import run

PUBLIC_API: str = "osac.public.v1"
PRIVATE_API: str = "osac.private.v1"


class GRPCClient:
    def __init__(self, *, address: str, token: str) -> None:
        self.address: str = address
        self.token: str = token

    def call(self, *, service: str, data: dict[str, Any] | None = None) -> dict[str, Any]:
        args: list[str] = ["grpcurl", "-insecure", "-H", f"Authorization: Bearer {self.token}"]
        if data is not None:
            args.extend(["-d", json.dumps(data)])
        args.extend([self.address, service])
        return json.loads(run(*args))

    def list_compute_instance_ids(self) -> list[str]:
        response: dict[str, Any] = self.call(service=f"{PUBLIC_API}.ComputeInstances/List")
        return [item["id"] for item in response.get("items", [])]

    def get_compute_instance(self, *, ci_id: str) -> dict[str, Any]:
        return self.call(service=f"{PUBLIC_API}.ComputeInstances/Get", data={"id": ci_id})

    def get_hub(self, *, hub_id: str) -> dict[str, Any]:
        return self.call(service=f"{PRIVATE_API}.Hubs/Get", data={"id": hub_id})

    def update_restart(self, *, uuid: str, template: str, timestamp: str) -> dict[str, Any]:
        return self.call(
            service=f"{PUBLIC_API}.ComputeInstances/Update",
            data={
                "object": {"id": uuid, "spec": {"template": template, "restart_requested_at": timestamp}},
                "updateMask": {"paths": ["spec.restart_requested_at"]},
            },
        )

    # VirtualNetwork operations

    def create_virtual_network(self, *, name: str, network_class: str, ipv4_cidr: str) -> str:
        response: dict[str, Any] = self.call(
            service=f"{PUBLIC_API}.VirtualNetworks/Create",
            data={
                "object": {"metadata": {"name": name}, "spec": {"network_class": network_class, "ipv4_cidr": ipv4_cidr}}
            },
        )
        return response["object"]["id"]

    def get_virtual_network(self, *, vn_id: str) -> dict[str, Any]:
        return self.call(service=f"{PUBLIC_API}.VirtualNetworks/Get", data={"id": vn_id})

    def list_virtual_network_ids(self) -> list[str]:
        response: dict[str, Any] = self.call(service=f"{PUBLIC_API}.VirtualNetworks/List")
        return [item["id"] for item in response.get("items", [])]

    def delete_virtual_network(self, *, vn_id: str) -> None:
        self.call(service=f"{PUBLIC_API}.VirtualNetworks/Delete", data={"id": vn_id})

    # Subnet operations

    def create_subnet(self, *, name: str, virtual_network: str, ipv4_cidr: str) -> str:
        response: dict[str, Any] = self.call(
            service=f"{PUBLIC_API}.Subnets/Create",
            data={
                "object": {
                    "metadata": {"name": name},
                    "spec": {"virtual_network": virtual_network, "ipv4_cidr": ipv4_cidr},
                }
            },
        )
        return response["object"]["id"]

    def get_subnet(self, *, subnet_id: str) -> dict[str, Any]:
        return self.call(service=f"{PUBLIC_API}.Subnets/Get", data={"id": subnet_id})

    def list_subnet_ids(self) -> list[str]:
        response: dict[str, Any] = self.call(service=f"{PUBLIC_API}.Subnets/List")
        return [item["id"] for item in response.get("items", [])]

    def delete_subnet(self, *, subnet_id: str) -> None:
        self.call(service=f"{PUBLIC_API}.Subnets/Delete", data={"id": subnet_id})

    # Cluster operations

    def list_cluster_ids(self) -> list[str]:
        response: dict[str, Any] = self.call(service=f"{PUBLIC_API}.Clusters/List")
        return [item["id"] for item in response.get("items", [])]

    def get_cluster(self, *, cluster_id: str) -> dict[str, Any]:
        return self.call(service=f"{PUBLIC_API}.Clusters/Get", data={"id": cluster_id})

    # SecurityGroup operations

    def create_security_group(self, *, name: str, virtual_network: str) -> str:
        response: dict[str, Any] = self.call(
            service=f"{PUBLIC_API}.SecurityGroups/Create",
            data={"object": {"metadata": {"name": name}, "spec": {"virtual_network": virtual_network}}},
        )
        return response["object"]["id"]

    def get_security_group(self, *, sg_id: str) -> dict[str, Any]:
        return self.call(service=f"{PUBLIC_API}.SecurityGroups/Get", data={"id": sg_id})

    def list_security_group_ids(self) -> list[str]:
        response: dict[str, Any] = self.call(service=f"{PUBLIC_API}.SecurityGroups/List")
        return [item["id"] for item in response.get("items", [])]

    def delete_security_group(self, *, sg_id: str) -> None:
        self.call(service=f"{PUBLIC_API}.SecurityGroups/Delete", data={"id": sg_id})
