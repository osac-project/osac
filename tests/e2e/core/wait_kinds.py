"""The resource kinds a poll_until wait can name on its span.

Every wait must declare the single resource it observes, so a wait span in
Grafana links to that resource. The values match the CR/API kind spelling
the wait_for_* helpers already used, keeping the span attribute values
stable. External systems a wait observes (a Keycloak realm) name their own
kind.
"""

from __future__ import annotations

from enum import StrEnum


class WaitResourceKind(StrEnum):
    COMPUTE_INSTANCE = "ComputeInstance"
    VIRTUAL_MACHINE = "VirtualMachine"
    VIRTUAL_MACHINE_INSTANCE = "VirtualMachineInstance"
    VIRTUAL_NETWORK = "VirtualNetwork"
    SUBNET = "Subnet"
    SECURITY_GROUP = "SecurityGroup"
    EXTERNAL_IP_POOL = "ExternalIPPool"
    EXTERNAL_IP = "ExternalIP"
    EXTERNAL_IP_ATTACHMENT = "ExternalIPAttachment"
    NAT_GATEWAY = "NATGateway"
    CLUSTER_ORDER = "ClusterOrder"
    CLUSTER = "Cluster"
    HOSTED_CONTROL_PLANE = "HostedControlPlane"
    NODE_POOL = "NodePool"
    INFRA_ENV = "InfraEnv"
    TENANT = "Tenant"
    IDENTITY_PROVIDER = "IdentityProvider"
    ROLE_BINDING = "RoleBinding"
    PROJECT = "Project"
    PROJECT_MEMBERSHIP = "ProjectMembership"
    USER = "User"
    BARE_METAL_INSTANCE = "BareMetalInstance"
    BARE_METAL_HOST = "BareMetalHost"
    NAMESPACE = "Namespace"
    CONFIG_MAP = "ConfigMap"
    DEPLOYMENT = "Deployment"
    VOLUME = "Volume"
    KEYCLOAK_REALM = "KeycloakRealm"
    KEYCLOAK_ORGANIZATION = "KeycloakOrganization"
