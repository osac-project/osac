from __future__ import annotations

import json
import re
import subprocess
from typing import Any

from tests.core.runner import run, run_unchecked

PUBLIC_API: str = "osac.public.v1"
PRIVATE_API: str = "osac.private.v1"


class GRPCClient:
    def __init__(self, *, address: str, token: str) -> None:
        self.address: str = address
        self.token: str = token

    def _build_args(self, *, service: str, data: dict[str, Any] | None = None) -> list[str]:
        args: list[str] = ["grpcurl", "-insecure", "-H", f"Authorization: Bearer {self.token}"]
        if data is not None:
            args.extend(["-d", json.dumps(data)])
        args.extend([self.address, service])
        return args

    def call(self, *, service: str, data: dict[str, Any] | None = None) -> dict[str, Any]:
        return json.loads(run(*self._build_args(service=service, data=data)))

    def create_compute_instance(self, *, catalog_item: str, subnet_ids: list[str]) -> str:
        attachments = [{"subnet": sid} for sid in subnet_ids]
        response: dict[str, Any] = self.call(
            service=f"{PUBLIC_API}.ComputeInstances/Create",
            data={"object": {"spec": {"catalog_item": catalog_item, "network_attachments": attachments}}},
        )
        return response["object"]["id"]

    def delete_compute_instance(self, *, ci_id: str) -> None:
        self.call(service=f"{PUBLIC_API}.ComputeInstances/Delete", data={"id": ci_id})

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

    def call_unchecked(self, *, service: str, data: dict[str, Any] | None = None) -> tuple[str, int]:
        return run_unchecked(*self._build_args(service=service, data=data))

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

    # Console operations

    def create_console_session(
        self, *, resource_type: str, resource_id: str, console_type: str, client_id: str = ""
    ) -> dict[str, Any]:
        data: dict[str, Any] = {
            "object": {"resourceType": resource_type, "resourceId": resource_id, "type": console_type}
        }
        if client_id:
            data["object"]["clientId"] = client_id
        response: dict[str, Any] = self.call(service=f"{PUBLIC_API}.ConsoleSessions/Create", data=data)
        return response["object"]

    # Tenant operations

    def ensure_tenant(self, *, name: str) -> None:
        try:
            self.call(
                service=f"{PRIVATE_API}.Tenants/Create",
                data={"object": {"metadata": {"name": name}}},
            )
        except subprocess.CalledProcessError as e:
            output = (e.stdout or "") + (e.stderr or "")
            if not re.search(r"Code:\s*AlreadyExists", output):
                raise RuntimeError(f"Failed to create tenant '{name}': {output}") from e

    # PublicIPPool operations (private API only)

    def create_public_ip_pool(
        self,
        *,
        name: str,
        cidrs: list[str],
        ip_family: str = "IP_FAMILY_IPV4",
        implementation_strategy: str = "metallb-l2",
    ) -> str:
        response: dict[str, Any] = self.call(
            service=f"{PRIVATE_API}.PublicIPPools/Create",
            data={
                "object": {
                    "metadata": {"name": name},
                    "spec": {
                        "cidrs": cidrs,
                        "ip_family": ip_family,
                        "implementation_strategy": implementation_strategy,
                    },
                }
            },
        )
        return response["object"]["id"]

    def get_public_ip_pool(self, *, pool_id: str) -> dict[str, Any]:
        return self.call(service=f"{PRIVATE_API}.PublicIPPools/Get", data={"id": pool_id})

    def list_public_ip_pool_ids(self) -> list[str]:
        response: dict[str, Any] = self.call(service=f"{PRIVATE_API}.PublicIPPools/List")
        return [item["id"] for item in response.get("items", [])]

    def delete_public_ip_pool(self, *, pool_id: str) -> None:
        self.call(service=f"{PRIVATE_API}.PublicIPPools/Delete", data={"id": pool_id})

    # PublicIP operations (public API)

    def create_public_ip(self, *, name: str, pool: str) -> str:
        response: dict[str, Any] = self.call(
            service=f"{PUBLIC_API}.PublicIPs/Create",
            data={"object": {"metadata": {"name": name}, "spec": {"pool": pool}}},
        )
        return response["object"]["id"]

    def get_public_ip(self, *, public_ip_id: str) -> dict[str, Any]:
        return self.call(service=f"{PUBLIC_API}.PublicIPs/Get", data={"id": public_ip_id})

    def list_public_ip_ids(self) -> list[str]:
        response: dict[str, Any] = self.call(service=f"{PUBLIC_API}.PublicIPs/List")
        return [item["id"] for item in response.get("items", [])]

    def delete_public_ip(self, *, public_ip_id: str) -> None:
        self.call(service=f"{PUBLIC_API}.PublicIPs/Delete", data={"id": public_ip_id})

    # PublicIPAttachment operations (public API)

    def create_public_ip_attachment(self, *, name: str, public_ip: str, compute_instance: str) -> str:
        response: dict[str, Any] = self.call(
            service=f"{PUBLIC_API}.PublicIPAttachments/Create",
            data={
                "object": {
                    "metadata": {"name": name},
                    "spec": {"public_ip": public_ip, "compute_instance": compute_instance},
                }
            },
        )
        return response["object"]["id"]

    def get_public_ip_attachment(self, *, attachment_id: str) -> dict[str, Any]:
        return self.call(service=f"{PUBLIC_API}.PublicIPAttachments/Get", data={"id": attachment_id})

    def list_public_ip_attachment_ids(self) -> list[str]:
        response: dict[str, Any] = self.call(service=f"{PUBLIC_API}.PublicIPAttachments/List")
        return [item["id"] for item in response.get("items", [])]

    def delete_public_ip_attachment(self, *, attachment_id: str) -> None:
        self.call(service=f"{PUBLIC_API}.PublicIPAttachments/Delete", data={"id": attachment_id})

    # ClusterCatalogItem operations

    def create_cluster_catalog_item(
        self, *, name: str, template: str, published: bool = True, field_definitions: list[dict[str, Any]] | None = None
    ) -> str:
        obj: dict[str, Any] = {"metadata": {"name": name}, "title": name, "template": template, "published": published}
        if field_definitions is not None:
            obj["field_definitions"] = field_definitions
        response: dict[str, Any] = self.call(service=f"{PRIVATE_API}.ClusterCatalogItems/Create", data={"object": obj})
        return response["object"]["id"]

    def get_cluster_catalog_item(self, *, catalog_item_id: str) -> dict[str, Any]:
        return self.call(service=f"{PUBLIC_API}.ClusterCatalogItems/Get", data={"id": catalog_item_id})

    def list_cluster_catalog_item_ids(self) -> list[str]:
        response: dict[str, Any] = self.call(service=f"{PUBLIC_API}.ClusterCatalogItems/List")
        return [item["id"] for item in response.get("items", [])]

    def update_cluster_catalog_item(self, *, catalog_item_id: str, **fields: Any) -> dict[str, Any]:
        if not fields:
            raise ValueError("update_cluster_catalog_item requires at least one field to update")
        obj: dict[str, Any] = {"id": catalog_item_id, **fields}
        data: dict[str, Any] = {"object": obj, "update_mask": {"paths": list(fields.keys())}}
        return self.call(service=f"{PRIVATE_API}.ClusterCatalogItems/Update", data=data)

    def delete_cluster_catalog_item(self, *, catalog_item_id: str) -> None:
        self.call(service=f"{PRIVATE_API}.ClusterCatalogItems/Delete", data={"id": catalog_item_id})

    # ComputeInstanceCatalogItem operations

    def create_compute_instance_catalog_item(
        self, *, name: str, template: str, published: bool = True, field_definitions: list[dict[str, Any]] | None = None
    ) -> str:
        obj: dict[str, Any] = {"metadata": {"name": name}, "title": name, "template": template, "published": published}
        if field_definitions is not None:
            obj["field_definitions"] = field_definitions
        response: dict[str, Any] = self.call(
            service=f"{PRIVATE_API}.ComputeInstanceCatalogItems/Create", data={"object": obj}
        )
        return response["object"]["id"]

    def get_compute_instance_catalog_item(self, *, catalog_item_id: str) -> dict[str, Any]:
        return self.call(service=f"{PUBLIC_API}.ComputeInstanceCatalogItems/Get", data={"id": catalog_item_id})

    def list_compute_instance_catalog_item_ids(self) -> list[str]:
        response: dict[str, Any] = self.call(service=f"{PUBLIC_API}.ComputeInstanceCatalogItems/List")
        return [item["id"] for item in response.get("items", [])]

    def update_compute_instance_catalog_item(self, *, catalog_item_id: str, **fields: Any) -> dict[str, Any]:
        if not fields:
            raise ValueError("update_compute_instance_catalog_item requires at least one field to update")
        obj: dict[str, Any] = {"id": catalog_item_id, **fields}
        data: dict[str, Any] = {"object": obj, "update_mask": {"paths": list(fields.keys())}}
        return self.call(service=f"{PRIVATE_API}.ComputeInstanceCatalogItems/Update", data=data)

    def delete_compute_instance_catalog_item(self, *, catalog_item_id: str) -> None:
        self.call(service=f"{PRIVATE_API}.ComputeInstanceCatalogItems/Delete", data={"id": catalog_item_id})

    # InstanceType operations (private API only)

    def create_instance_type(
        self,
        *,
        name: str,
        cores: int,
        memory_gib: int,
        description: str = "",
    ) -> str:
        response: dict[str, Any] = self.call(
            service=f"{PRIVATE_API}.InstanceTypes/Create",
            data={
                "object": {
                    "metadata": {"name": name},
                    "spec": {
                        "cores": cores,
                        "memory_gib": memory_gib,
                        "description": description,
                    },
                }
            },
        )
        return response["object"]["id"]

    def get_instance_type(self, *, name: str) -> dict[str, Any]:
        return self.call(service=f"{PRIVATE_API}.InstanceTypes/Get", data={"id": name})

    def list_instance_type_names(self) -> list[str]:
        response: dict[str, Any] = self.call(service=f"{PRIVATE_API}.InstanceTypes/List")
        return [item["metadata"]["name"] for item in response.get("items", [])]

    def update_instance_type(self, *, name: str, state: str) -> dict[str, Any]:
        return self.call(
            service=f"{PRIVATE_API}.InstanceTypes/Update",
            data={
                "object": {"id": name, "spec": {"state": state}},
                "updateMask": {"paths": ["spec.state"]},
            },
        )

    def delete_instance_type(self, *, name: str) -> None:
        self.call(service=f"{PRIVATE_API}.InstanceTypes/Delete", data={"id": name})
