#
# Copyright (c) 2025 Red Hat Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
# the License. You may obtain a copy of the License at
#
#   http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
# "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.
#

package authz

import rego.v1

default allow := false

# Emergency service accounts are Kubernetes service accounts that are allowed to act as administrators in case of
# emergency situations where it isn't possible to get a valid JWT token from the identity provider. The full service
# account names are provided via the external data passed to the policy.
emergency_service_accounts := {name | some name in data.emergency_service_accounts}

# Admin service accounts are service accounts that are allowed to act as administrators.
admin_service_accounts := {
  "service-account-osac-admin",
  "service-account-osac-controller",
}

# Admin groups are groups that are allowed to act as administrators.
admin_groups := {
  "admins",
}

# Tenant admin roles - users with these roles can manage users in their tenant
tenant_admin_roles := {
  "tenant-admin",
}

# Tenant IdP manager roles - users with these roles can manage IdP config and assign roles
tenant_idp_manager_roles := {
  "tenant-admin",
  "tenant-idp-manager",
}

# Get the gRPC method:
grpc_method := input.context.request.http.path

# Get the subject name:
subject_name = input.auth.identity.user.username if {
  input.auth.identity.authnMethod == "serviceaccount"
}
subject_name = input.auth.identity.username if {
  input.auth.identity.authnMethod == "jwt"
}

# Get the subject groups:
subject_groups = input.auth.identity.user.groups if {
  input.auth.identity.authnMethod == "serviceaccount"
}
subject_groups = input.auth.identity.groups if {
  input.auth.identity.authnMethod == "jwt"
}

# Get the subject's tenant(s) from JWT claims or service account namespace
# For JWT users, this comes from the "organization" scope which is in defaultClientScopes.
# JWT users without an organization claim will have an empty tenant list.
# For service accounts, groups are used as tenants.
default subject_tenants = []

# Extract tenant names from organization array (simple format: ["org-name"])
subject_tenants = input.auth.identity.organization if {
  input.auth.identity.authnMethod == "jwt"
  is_array(input.auth.identity.organization)
}

# Extract tenant names from organization object (Keycloak format: {"org-name": {"groups": [...]}})
subject_tenants = [org_name | input.auth.identity.organization[org_name]] if {
  input.auth.identity.authnMethod == "jwt"
  is_object(input.auth.identity.organization)
}

# For service accounts, use groups as tenants
subject_tenants = subject_groups if {
  input.auth.identity.authnMethod == "serviceaccount"
}

# Get the subject's realm roles from JWT
default subject_realm_roles = []
subject_realm_roles = input.auth.identity.realm_access.roles if {
  input.auth.identity.authnMethod == "jwt"
  input.auth.identity.realm_access
  input.auth.identity.realm_access.roles
}

# Organization dictionary extraction
default subject_org_groups = {}

# If organization is an object with groups, use it directly
subject_org_groups := input.auth.identity.organization if {
  input.auth.identity.authnMethod == "jwt"
  is_object(input.auth.identity.organization)
}

# If organization is an array, build org groups from top-level groups claim
# This creates a structure: {"org-name": {"groups": ["group1", "group2"]}}
subject_org_groups := {tenant: {"groups": input.auth.identity.groups} |
  some tenant in input.auth.identity.organization
} if {
  input.auth.identity.authnMethod == "jwt"
  is_array(input.auth.identity.organization)
  input.auth.identity.groups
}

# Check if an account is an admin account:
default is_admin = false
is_admin if {
  subject_name in emergency_service_accounts
}
is_admin if {
  subject_name in admin_service_accounts
}
is_admin if {
  some group in subject_groups
  group in admin_groups
}

# Check if an account is a tenant admin (can manage users AND IdP in their tenant):
default is_tenant_admin = false
is_tenant_admin if {
  some role in subject_realm_roles
  role in tenant_admin_roles
}

# Check if an account is a tenant IdP manager (can ONLY manage IdP, NOT users):
default is_tenant_idp_manager = false
is_tenant_idp_manager if {
  some role in subject_realm_roles
  role in tenant_idp_manager_roles
}

# Check if an account is a regular client (no admin, tenant management roles):
default is_client = false
is_client if {
  not is_admin
  not is_tenant_admin
  not is_tenant_idp_manager
}

# Check if account has client-level permissions (clients, tenant admins, or IdP managers):
default has_client_permissions = false
has_client_permissions if {
  is_client
}
has_client_permissions if {
  is_tenant_admin
}
has_client_permissions if {
  is_tenant_idp_manager
}

# Allow metadata, reflection and health to everyone:
allow if {
  startswith(grpc_method, "/metadata.")
}
allow if {
  startswith(grpc_method, "/grpc.reflection.")
}
allow if {
  startswith(grpc_method, "/grpc.health.")
}

# Allow specific methods to clients (and tenant admins/IdP managers who inherit client permissions):
allow if {
  has_client_permissions
  grpc_method in {
    "/osac.public.v1.BareMetalInstanceCatalogItems/Get",
    "/osac.public.v1.BareMetalInstanceCatalogItems/List",
    "/osac.public.v1.BareMetalInstanceTemplates/Get",
    "/osac.public.v1.BareMetalInstanceTemplates/List",
    "/osac.public.v1.BareMetalInstanceTypes/Get",
    "/osac.public.v1.BareMetalInstanceTypes/List",
    "/osac.public.v1.BareMetalInstances/Create",
    "/osac.public.v1.BareMetalInstances/Delete",
    "/osac.public.v1.BareMetalInstances/Get",
    "/osac.public.v1.BareMetalInstances/List",
    "/osac.public.v1.BareMetalInstances/Update",
    "/osac.public.v1.ClusterCatalogItems/Get",
    "/osac.public.v1.ClusterCatalogItems/List",
    "/osac.public.v1.ClusterTemplates/Get",
    "/osac.public.v1.ClusterTemplates/List",
    "/osac.public.v1.ClusterVersions/Get",
    "/osac.public.v1.ClusterVersions/List",
    "/osac.public.v1.Clusters/Create",
    "/osac.public.v1.Clusters/Delete",
    "/osac.public.v1.Clusters/Get",
    "/osac.public.v1.Clusters/GetKubeconfig",
    "/osac.public.v1.Clusters/GetKubeconfigViaHttp",
    "/osac.public.v1.Clusters/GetPassword",
    "/osac.public.v1.Clusters/GetPasswordViaHttp",
    "/osac.public.v1.Clusters/List",
    "/osac.public.v1.Clusters/Update",
    "/osac.public.v1.ComputeInstanceCatalogItems/Get",
    "/osac.public.v1.ComputeInstanceCatalogItems/List",
    "/osac.public.v1.ComputeInstanceTemplates/Get",
    "/osac.public.v1.ComputeInstanceTemplates/List",
    "/osac.public.v1.ComputeInstances/Create",
    "/osac.public.v1.ComputeInstances/Delete",
    "/osac.public.v1.ComputeInstances/Get",
    "/osac.public.v1.ComputeInstances/List",
    "/osac.public.v1.ComputeInstances/Update",
    "/osac.public.v1.ConsoleSessions/Create",
    "/osac.public.v1.Events/Watch",
    "/osac.public.v1.HostTypes/Get",
    "/osac.public.v1.HostTypes/List",
    "/osac.public.v1.InstanceTypes/Get",
    "/osac.public.v1.InstanceTypes/List",
    "/osac.public.v1.NetworkClasses/Create",
    "/osac.public.v1.NetworkClasses/Delete",
    "/osac.public.v1.NetworkClasses/Get",
    "/osac.public.v1.NetworkClasses/List",
    "/osac.public.v1.NetworkClasses/Update",
    "/osac.public.v1.Subnets/Create",
    "/osac.public.v1.Subnets/Delete",
    "/osac.public.v1.Subnets/Get",
    "/osac.public.v1.Subnets/List",
    "/osac.public.v1.Subnets/Update",
    "/osac.public.v1.VirtualNetworks/Create",
    "/osac.public.v1.VirtualNetworks/Delete",
    "/osac.public.v1.VirtualNetworks/Get",
    "/osac.public.v1.VirtualNetworks/List",
    "/osac.public.v1.VirtualNetworks/Update",
    "/osac.public.v1.SecurityGroups/Create",
    "/osac.public.v1.SecurityGroups/Delete",
    "/osac.public.v1.SecurityGroups/Get",
    "/osac.public.v1.SecurityGroups/List",
    "/osac.public.v1.SecurityGroups/Update",
    "/osac.public.v1.ExternalIPAttachments/Create",
    "/osac.public.v1.ExternalIPAttachments/Delete",
    "/osac.public.v1.ExternalIPAttachments/Get",
    "/osac.public.v1.ExternalIPAttachments/List",
    "/osac.public.v1.ExternalIPAttachments/Update",
    "/osac.public.v1.ExternalIPPools/Get",
    "/osac.public.v1.ExternalIPPools/List",
    "/osac.public.v1.ExternalIPs/Create",
    "/osac.public.v1.ExternalIPs/Delete",
    "/osac.public.v1.ExternalIPs/Get",
    "/osac.public.v1.ExternalIPs/List",
    "/osac.public.v1.ExternalIPs/Update",
    "/osac.public.v1.NATGateways/Create",
    "/osac.public.v1.NATGateways/Delete",
    "/osac.public.v1.NATGateways/Get",
    "/osac.public.v1.NATGateways/List",
    "/osac.public.v1.NATGateways/Update",
    "/osac.public.v1.Roles/Get",
    "/osac.public.v1.Roles/List",
    "/osac.public.v1.RoleBindings/Get",
    "/osac.public.v1.RoleBindings/List",
    "/osac.public.v1.Secrets/Create",
    "/osac.public.v1.Secrets/Delete",
    "/osac.public.v1.Secrets/Get",
    "/osac.public.v1.Secrets/List",
    "/osac.public.v1.Secrets/Update",
  }
}

# Tenant admins can manage catalog items, users, identity providers,
# projects, and project memberships within their tenant. The application layer
# (generic server) enforces resource-level authorization via tenant field validation.
allow if {
  is_tenant_admin
  grpc_method in {
    "/osac.public.v1.BareMetalInstanceCatalogItems/Create",
    "/osac.public.v1.BareMetalInstanceCatalogItems/Update",
    "/osac.public.v1.BareMetalInstanceCatalogItems/Delete",
    "/osac.public.v1.ClusterCatalogItems/Create",
    "/osac.public.v1.ClusterCatalogItems/Update",
    "/osac.public.v1.ClusterCatalogItems/Delete",
    "/osac.public.v1.ComputeInstanceCatalogItems/Create",
    "/osac.public.v1.ComputeInstanceCatalogItems/Update",
    "/osac.public.v1.ComputeInstanceCatalogItems/Delete",
    "/osac.public.v1.IdentityProviders/Create",
    "/osac.public.v1.IdentityProviders/Get",
    "/osac.public.v1.IdentityProviders/List",
    "/osac.public.v1.IdentityProviders/Update",
    "/osac.public.v1.IdentityProviders/Delete",
    "/osac.public.v1.Users/Create",
    "/osac.public.v1.Users/Get",
    "/osac.public.v1.Users/List",
    "/osac.public.v1.Users/Update",
    "/osac.public.v1.Users/Delete",
    "/osac.public.v1.Projects/Create",
    "/osac.public.v1.Projects/Get",
    "/osac.public.v1.Projects/List",
    "/osac.public.v1.Projects/Update",
    "/osac.public.v1.Projects/Delete",
    "/osac.public.v1.ProjectMemberships/Create",
    "/osac.public.v1.ProjectMemberships/Get",
    "/osac.public.v1.ProjectMemberships/List",
    "/osac.public.v1.ProjectMemberships/Update",
    "/osac.public.v1.ProjectMemberships/Delete",
  }
}

# Tenant IdP managers can manage identity providers (but not users or project memberships).
allow if {
  is_tenant_idp_manager
  grpc_method in {
    "/osac.public.v1.IdentityProviders/Create",
    "/osac.public.v1.IdentityProviders/Get",
    "/osac.public.v1.IdentityProviders/List",
    "/osac.public.v1.IdentityProviders/Update",
    "/osac.public.v1.IdentityProviders/Delete",
  }
}

# Allow everything to admins:
allow if {
  is_admin
}

# Project authorization
# All authenticated users with client permissions can list projects
# (application layer filters results based on actual project memberships)
allow if {
  has_client_permissions
  grpc_method == "/osac.public.v1.Projects/List"
}

# Project Get authorization - allow viewers or managers
allow if {
  grpc_method == "/osac.public.v1.Projects/Get"
  tenant := input.context.context_extensions.tenant
  name := input.context.context_extensions.name
  input.auth.identity.authnMethod == "jwt"
  some group in subject_org_groups[tenant].groups
  group in {
    sprintf("/%s/system:viewers", [name]),
    sprintf("/%s/system:managers", [name]),
  }
}

# Project Update/Delete authorization via organization groups
allow if {
  grpc_method in {
    "/osac.public.v1.Projects/Update",
    "/osac.public.v1.Projects/Delete",
  }
  tenant := input.context.context_extensions.tenant
  name := input.context.context_extensions.name
  input.auth.identity.authnMethod == "jwt"
  some group in subject_org_groups[tenant].groups
  group == sprintf("/%s/system:managers", [name])
}

# All authenticated users with client permissions can list project memberships
# (application layer filters results based on actual project memberships)
allow if {
  has_client_permissions
  grpc_method == "/osac.public.v1.ProjectMemberships/List"
}

# Project managers can create memberships for their project.
# For Create, only the project name is available from the request body (no DB lookup),
# so we iterate over the user's tenants to find a matching manager group.
allow if {
  grpc_method == "/osac.public.v1.ProjectMemberships/Create"
  input.auth.identity.authnMethod == "jwt"
  project := input.context.context_extensions.project
  some tenant in subject_tenants
  some group in subject_org_groups[tenant].groups
  group == sprintf("/%s/system:managers", [project])
}

# Project managers can get, update, and delete memberships for their project.
# Uses DB-authoritative tenant and project name from context extensions.
allow if {
  grpc_method in {
    "/osac.public.v1.ProjectMemberships/Get",
    "/osac.public.v1.ProjectMemberships/Update",
    "/osac.public.v1.ProjectMemberships/Delete",
  }
  input.auth.identity.authnMethod == "jwt"
  tenant := input.context.context_extensions.tenant
  project := input.context.context_extensions.project
  some group in subject_org_groups[tenant].groups
  group == sprintf("/%s/system:managers", [project])
}

# Subject construction:
subject_user = input.auth.identity.username if {
  input.auth.identity.authnMethod == "jwt"
}
subject_user = split(input.auth.identity.user.username, ":")[3] if {
  input.auth.identity.authnMethod == "serviceaccount"
}
subject_tenant_result = ["*"] if {
  is_admin
}
subject_tenant_result = subject_tenants if {
  not is_admin
  input.auth.identity.authnMethod == "jwt"
}
subject_tenant_result = [split(input.auth.identity.user.username, ":")[2]] if {
  not is_admin
  input.auth.identity.authnMethod == "serviceaccount"
}
