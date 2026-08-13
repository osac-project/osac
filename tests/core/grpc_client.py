from __future__ import annotations

import json
import re
import subprocess
import time
from collections.abc import Callable
from typing import Any

from tests.core.runner import run, run_unchecked

PUBLIC_API: str = "osac.public.v1"
PRIVATE_API: str = "osac.private.v1"


class GRPCClient:
    _TOKEN_TTL: float = 60.0

    def __init__(self, *, address: str, token: str = "", token_factory: Callable[[], str] | None = None) -> None:
        self.address: str = address
        self._static_token: str = token
        self._token_factory: Callable[[], str] | None = token_factory
        self._cached_token: str = ""
        self._cached_at: float = 0.0

    @property
    def token(self) -> str:
        if self._token_factory is not None:
            now = time.monotonic()
            if not self._cached_token or (now - self._cached_at) >= self._TOKEN_TTL:
                self._cached_token = self._token_factory()
                self._cached_at = now
            return self._cached_token
        return self._static_token

    def _build_args(self, *, service: str, data: dict[str, Any] | None = None) -> list[str]:
        args: list[str] = ["grpcurl", "-insecure", "-H", f"Authorization: Bearer {self.token}"]
        if data is not None:
            args.extend(["-d", json.dumps(data)])
        args.extend([self.address, service])
        return args

    def call(self, *, service: str, data: dict[str, Any] | None = None) -> dict[str, Any]:
        return json.loads(run(*self._build_args(service=service, data=data)))

    def create_compute_instance(self, *, catalog_item: str, subnet_ids: list[str], name: str | None = None) -> str:
        attachments = [{"subnet": {"id": sid}} for sid in subnet_ids]
        obj: dict[str, Any] = {"spec": {"catalog_item": {"id": catalog_item}, "network_attachments": attachments}}
        if name is not None:
            obj["metadata"] = {"name": name}
        response: dict[str, Any] = self.call(service=f"{PUBLIC_API}.ComputeInstances/Create", data={"object": obj})
        return response["object"]["id"]

    def update_compute_instance_run_strategy(self, *, ci_id: str, run_strategy: str) -> dict[str, Any]:
        return self.call(
            service=f"{PUBLIC_API}.ComputeInstances/Update",
            data={
                "object": {"id": ci_id, "spec": {"run_strategy": run_strategy}},
                "updateMask": {"paths": ["spec.run_strategy"]},
            },
        )

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
                "object": {"id": uuid, "spec": {"template": {"name": template}, "restart_requested_at": timestamp}},
                "updateMask": {"paths": ["spec.restart_requested_at"]},
            },
        )

    # VirtualNetwork operations

    def create_virtual_network(self, *, name: str, network_class: str, ipv4_cidr: str) -> str:
        response: dict[str, Any] = self.call(
            service=f"{PUBLIC_API}.VirtualNetworks/Create",
            data={
                "object": {
                    "metadata": {"name": name},
                    "spec": {"network_class": {"name": network_class}, "ipv4_cidr": ipv4_cidr},
                }
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
                    "spec": {"virtual_network": {"id": virtual_network}, "ipv4_cidr": ipv4_cidr},
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
            data={"object": {"metadata": {"name": name}, "spec": {"virtual_network": {"id": virtual_network}}}},
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
            self.call(service=f"{PRIVATE_API}.Tenants/Create", data={"object": {"metadata": {"name": name}}})
        except subprocess.CalledProcessError as e:
            output = (e.stdout or "") + (e.stderr or "")
            if not re.search(r"Code:\s*AlreadyExists", output):
                raise RuntimeError(f"Failed to create tenant '{name}': {output}") from e

    # ExternalIPPool operations (private API only)

    def create_external_ip_pool(
        self,
        *,
        name: str,
        cidrs: list[str],
        ip_family: str = "IP_FAMILY_IPV4",
        implementation_strategy: str = "metallb-l2",
    ) -> str:
        response: dict[str, Any] = self.call(
            service=f"{PRIVATE_API}.ExternalIPPools/Create",
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

    def get_external_ip_pool(self, *, pool_id: str) -> dict[str, Any]:
        return self.call(service=f"{PRIVATE_API}.ExternalIPPools/Get", data={"id": pool_id})

    def list_external_ip_pool_ids(self) -> list[str]:
        response: dict[str, Any] = self.call(service=f"{PRIVATE_API}.ExternalIPPools/List")
        return [item["id"] for item in response.get("items", [])]

    def delete_external_ip_pool(self, *, pool_id: str) -> None:
        self.call(service=f"{PRIVATE_API}.ExternalIPPools/Delete", data={"id": pool_id})

    # ExternalIP operations (public API)

    def create_external_ip(self, *, name: str, pool: str) -> str:
        response: dict[str, Any] = self.call(
            service=f"{PUBLIC_API}.ExternalIPs/Create",
            data={"object": {"metadata": {"name": name}, "spec": {"pool": {"id": pool}}}},
        )
        return response["object"]["id"]

    def get_external_ip(self, *, external_ip_id: str) -> dict[str, Any]:
        return self.call(service=f"{PUBLIC_API}.ExternalIPs/Get", data={"id": external_ip_id})

    def list_external_ip_ids(self) -> list[str]:
        response: dict[str, Any] = self.call(service=f"{PUBLIC_API}.ExternalIPs/List")
        return [item["id"] for item in response.get("items", [])]

    def delete_external_ip(self, *, external_ip_id: str) -> None:
        self.call(service=f"{PUBLIC_API}.ExternalIPs/Delete", data={"id": external_ip_id})

    # ExternalIPAttachment operations (public API)

    def create_external_ip_attachment(self, *, name: str, external_ip: str, compute_instance: str) -> str:
        response: dict[str, Any] = self.call(
            service=f"{PUBLIC_API}.ExternalIPAttachments/Create",
            data={
                "object": {
                    "metadata": {"name": name},
                    "spec": {"external_ip": {"id": external_ip}, "compute_instance": {"id": compute_instance}},
                }
            },
        )
        return response["object"]["id"]

    def get_external_ip_attachment(self, *, attachment_id: str) -> dict[str, Any]:
        return self.call(service=f"{PUBLIC_API}.ExternalIPAttachments/Get", data={"id": attachment_id})

    def list_external_ip_attachment_ids(self) -> list[str]:
        response: dict[str, Any] = self.call(service=f"{PUBLIC_API}.ExternalIPAttachments/List")
        return [item["id"] for item in response.get("items", [])]

    def delete_external_ip_attachment(self, *, attachment_id: str) -> None:
        self.call(service=f"{PUBLIC_API}.ExternalIPAttachments/Delete", data={"id": attachment_id})

    # ClusterCatalogItem operations

    def create_cluster_catalog_item(
        self, *, name: str, template: str, published: bool = True, field_definitions: list[dict[str, Any]] | None = None
    ) -> str:
        obj: dict[str, Any] = {
            "metadata": {"name": name},
            "title": name,
            "template": {"name": template},
            "published": published,
        }
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
        obj: dict[str, Any] = {
            "metadata": {"name": name},
            "title": name,
            "template": {"name": template},
            "published": published,
        }
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
        self, *, name: str, cores: int, memory_gib: int, description: str = "", gpu: dict[str, Any] | None = None
    ) -> str:
        spec: dict[str, Any] = {"cores": cores, "memory_gib": memory_gib, "description": description}
        if gpu is not None:
            spec["gpu"] = gpu
        response: dict[str, Any] = self.call(
            service=f"{PRIVATE_API}.InstanceTypes/Create", data={"object": {"metadata": {"name": name}, "spec": spec}}
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
            data={"object": {"id": name, "spec": {"state": state}}, "updateMask": {"paths": ["spec.state"]}},
        )

    def delete_instance_type(self, *, name: str) -> None:
        self.call(service=f"{PRIVATE_API}.InstanceTypes/Delete", data={"id": name})

    # BareMetalInstance operations (public API)

    def list_baremetal_instance_ids(self) -> list[str]:
        response: dict[str, Any] = self.call(service=f"{PUBLIC_API}.BareMetalInstances/List")
        return [item["id"] for item in response.get("items", [])]

    def get_baremetal_instance(self, *, bmi_id: str) -> dict[str, Any]:
        return self.call(service=f"{PUBLIC_API}.BareMetalInstances/Get", data={"id": bmi_id})

    def get_baremetal_instance_state(self, *, bmi_id: str) -> str:
        response: dict[str, Any] = self.get_baremetal_instance(bmi_id=bmi_id)
        return response.get("object", {}).get("status", {}).get("state", "")

    def update_baremetal_instance_run_strategy(self, *, bmi_id: str, run_strategy: str) -> dict[str, Any]:
        return self.call(
            service=f"{PUBLIC_API}.BareMetalInstances/Update",
            data={
                "object": {"id": bmi_id, "spec": {"run_strategy": run_strategy}},
                "updateMask": {"paths": ["spec.run_strategy"]},
            },
        )

    def delete_baremetal_instance(self, *, bmi_id: str) -> None:
        self.call(service=f"{PUBLIC_API}.BareMetalInstances/Delete", data={"id": bmi_id})

    # BareMetalInstanceCatalogItem operations (private API for admin setup)

    def create_baremetal_instance_catalog_item(
        self,
        *,
        name: str,
        title: str,
        description: str,
        template: str,
        field_definitions: list[dict[str, Any]] | None = None,
    ) -> str:
        obj: dict[str, Any] = {
            "metadata": {"name": name},
            "title": title,
            "description": description,
            "template": {"name": template},
            "published": True,
        }
        if field_definitions is not None:
            obj["field_definitions"] = field_definitions
        response: dict[str, Any] = self.call(
            service=f"{PRIVATE_API}.BareMetalInstanceCatalogItems/Create", data={"object": obj}
        )
        return response["object"]["id"]

    def delete_baremetal_instance_catalog_item(self, *, item_id: str) -> None:
        self.call(service=f"{PRIVATE_API}.BareMetalInstanceCatalogItems/Delete", data={"id": item_id})

    # Generic filtered list

    def list_with_filter(self, *, service: str, filter_expr: str) -> list[dict[str, Any]]:
        response: dict[str, Any] = self.call(service=service, data={"filter": filter_expr})
        return response.get("items", [])

    # NATGateway operations (public API)

    def create_nat_gateway(self, *, name: str, virtual_network_name: str, external_ip_name: str) -> str:
        response: dict[str, Any] = self.call(
            service=f"{PUBLIC_API}.NATGateways/Create",
            data={
                "object": {
                    "metadata": {"name": name},
                    "spec": {
                        "virtual_network": {"name": virtual_network_name},
                        "external_ip": {"name": external_ip_name},
                    },
                }
            },
        )
        return response["object"]["id"]

    def delete_nat_gateway(self, *, nat_gateway_id: str) -> None:
        self.call(service=f"{PUBLIC_API}.NATGateways/Delete", data={"id": nat_gateway_id})

    # RoleBinding operations (public API)

    def create_role_binding(self, *, name: str, role_name: str, user_names: list[str]) -> str:
        response: dict[str, Any] = self.call(
            service=f"{PUBLIC_API}.RoleBindings/Create",
            data={
                "object": {
                    "metadata": {"name": name},
                    "spec": {"role": {"name": role_name}, "users": [{"name": u} for u in user_names]},
                }
            },
        )
        return response["object"]["id"]

    def get_role_binding(self, *, role_binding_id: str) -> dict[str, Any]:
        return self.call(service=f"{PUBLIC_API}.RoleBindings/Get", data={"id": role_binding_id})

    def delete_role_binding(self, *, role_binding_id: str) -> None:
        self.call(service=f"{PUBLIC_API}.RoleBindings/Delete", data={"id": role_binding_id})

    # ProjectMembership operations (public API)

    def create_project_membership(
        self, *, name: str, user_names: list[str], role: str = "PROJECT_MEMBERSHIP_ROLE_VIEWER"
    ) -> str:
        response: dict[str, Any] = self.call(
            service=f"{PUBLIC_API}.ProjectMemberships/Create",
            data={
                "object": {
                    "metadata": {"name": name},
                    "spec": {"role": role, "users": [{"name": u} for u in user_names]},
                }
            },
        )
        return response["object"]["id"]

    def delete_project_membership(self, *, membership_id: str) -> None:
        self.call(service=f"{PUBLIC_API}.ProjectMemberships/Delete", data={"id": membership_id})
