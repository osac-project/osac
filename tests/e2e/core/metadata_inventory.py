from __future__ import annotations

import re
from collections.abc import Mapping
from dataclasses import dataclass
from pathlib import Path
from types import MappingProxyType

CRUD_METHODS = frozenset({"Create", "Delete", "Get", "List", "Update"})
PRIVATE_METHODS = CRUD_METHODS | {"Signal"}


@dataclass(frozen=True, slots=True)
class MetadataResource:
    name: str
    service_name: str
    public_methods: frozenset[str]

    @property
    def public(self) -> bool:
        return bool(self.public_methods)

    @property
    def public_update(self) -> bool:
        return "Update" in self.public_methods

    @property
    def private_methods(self) -> frozenset[str]:
        return PRIVATE_METHODS


_PUBLIC_CRUD = CRUD_METHODS
_PUBLIC_READ = frozenset({"Get", "List"})
_PUBLIC_CREATE_READ_DELETE = frozenset({"Create", "Delete", "Get", "List"})

METADATA_RESOURCES = (
    MetadataResource("Cluster", "Clusters", _PUBLIC_CRUD),
    MetadataResource("ClusterTemplate", "ClusterTemplates", _PUBLIC_CRUD),
    MetadataResource("ClusterCatalogItem", "ClusterCatalogItems", _PUBLIC_CRUD),
    MetadataResource("ClusterVersion", "ClusterVersions", _PUBLIC_CRUD),
    MetadataResource("ComputeInstance", "ComputeInstances", _PUBLIC_CRUD),
    MetadataResource("ComputeInstanceTemplate", "ComputeInstanceTemplates", _PUBLIC_CRUD),
    MetadataResource("ComputeInstanceCatalogItem", "ComputeInstanceCatalogItems", _PUBLIC_CRUD),
    MetadataResource("InstanceType", "InstanceTypes", _PUBLIC_CRUD),
    MetadataResource("DiskImage", "DiskImages", _PUBLIC_CRUD),
    MetadataResource("BareMetalInstance", "BareMetalInstances", _PUBLIC_CRUD),
    MetadataResource("BareMetalInstanceTemplate", "BareMetalInstanceTemplates", _PUBLIC_CRUD),
    MetadataResource("BareMetalInstanceCatalogItem", "BareMetalInstanceCatalogItems", _PUBLIC_CRUD),
    MetadataResource("BareMetalInstanceType", "BareMetalInstanceTypes", _PUBLIC_READ),
    MetadataResource("AddOnOperator", "AddOnOperators", _PUBLIC_READ),
    MetadataResource("NetworkClass", "NetworkClasses", frozenset()),
    MetadataResource("VirtualNetwork", "VirtualNetworks", _PUBLIC_CREATE_READ_DELETE),
    MetadataResource("Subnet", "Subnets", _PUBLIC_CREATE_READ_DELETE),
    MetadataResource("SecurityGroup", "SecurityGroups", _PUBLIC_CRUD),
    MetadataResource("ExternalIPPool", "ExternalIPPools", _PUBLIC_READ),
    MetadataResource("ExternalIP", "ExternalIPs", _PUBLIC_CREATE_READ_DELETE),
    MetadataResource("ExternalIPAttachment", "ExternalIPAttachments", _PUBLIC_CREATE_READ_DELETE),
    MetadataResource("NATGateway", "NATGateways", _PUBLIC_CREATE_READ_DELETE),
    MetadataResource("FabricDomain", "FabricDomains", _PUBLIC_CRUD),
    MetadataResource("StorageBackend", "StorageBackends", frozenset()),
    MetadataResource("StorageTier", "StorageTiers", _PUBLIC_READ),
    MetadataResource("Volume", "Volumes", _PUBLIC_CRUD),
    MetadataResource("Hub", "Hubs", frozenset()),
    MetadataResource("Tenant", "Tenants", _PUBLIC_CRUD),
    MetadataResource("User", "Users", _PUBLIC_CRUD),
    MetadataResource("IdentityProvider", "IdentityProviders", _PUBLIC_CRUD),
    MetadataResource("Project", "Projects", _PUBLIC_CRUD),
    MetadataResource("ProjectMembership", "ProjectMemberships", _PUBLIC_CRUD),
    MetadataResource("Role", "Roles", _PUBLIC_CRUD),
    MetadataResource("RoleBinding", "RoleBindings", _PUBLIC_CRUD),
    MetadataResource("Secret", "Secrets", _PUBLIC_CRUD),
)

METADATA_RESOURCES_BY_NAME: Mapping[str, MetadataResource] = MappingProxyType(
    {resource.name: resource for resource in METADATA_RESOURCES}
)


def _service_methods(proto_directory: Path) -> dict[str, frozenset[str]]:
    services: dict[str, frozenset[str]] = {}
    service_pattern = re.compile(r"(?ms)^service\s+(\w+)\s*\{(.*?)^\}")
    method_pattern = re.compile(r"\brpc\s+(\w+)\s*\(")
    for proto_file in proto_directory.glob("*_service.proto"):
        source = proto_file.read_text(encoding="utf-8")
        source = re.sub(r"(?m)^\s*//[^\n]*$", "", source)
        source = re.sub(r"/\*.*?\*/", "", source, flags=re.S)
        for match in service_pattern.finditer(source):
            services[match.group(1)] = frozenset(method_pattern.findall(match.group(2)))
    return services


def validate_metadata_inventory() -> None:
    names = [resource.name for resource in METADATA_RESOURCES]
    service_names = [resource.service_name for resource in METADATA_RESOURCES]
    if len(names) != 35:
        raise ValueError(f"metadata inventory has {len(names)} resources; expected 35")
    if len(set(names)) != len(names):
        raise ValueError("metadata inventory contains duplicate resource names")
    if len(set(service_names)) != len(service_names):
        raise ValueError("metadata inventory contains duplicate service names")
    if sum(resource.public for resource in METADATA_RESOURCES) != 32:
        raise ValueError("metadata inventory must contain exactly 32 public resources")
    for resource in METADATA_RESOURCES:
        if not CRUD_METHODS.issubset(resource.private_methods):
            raise ValueError(f"{resource.name} is missing a private CRUD method")

    private_only = {resource.name for resource in METADATA_RESOURCES if not resource.public}
    if private_only != {"Hub", "NetworkClass", "StorageBackend"}:
        raise ValueError(f"unexpected private-only resources: {sorted(private_only)}")

    repo_root = Path(__file__).resolve().parents[3]
    private_services = _service_methods(repo_root / "proto/private/osac/private/v1")
    public_services = _service_methods(repo_root / "proto/public/osac/public/v1")
    inventory_services = {resource.service_name: resource for resource in METADATA_RESOURCES}
    persistent_private_services = {
        service_name for service_name, methods in private_services.items() if CRUD_METHODS.issubset(methods)
    }
    if persistent_private_services != set(inventory_services):
        missing = sorted(persistent_private_services - set(inventory_services))
        extra = sorted(set(inventory_services) - persistent_private_services)
        raise ValueError(f"metadata service inventory differs from private protos; missing={missing}, extra={extra}")
    for service_name, resource in inventory_services.items():
        if private_services.get(service_name) != resource.private_methods:
            raise ValueError(f"{service_name} private methods differ from the private proto service")
        if public_services.get(service_name, frozenset()) != resource.public_methods:
            raise ValueError(f"{service_name} public methods differ from the public proto service")
