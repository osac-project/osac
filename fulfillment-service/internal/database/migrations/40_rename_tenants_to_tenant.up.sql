--
-- Copyright (c) 2026 Red Hat Inc.
--
-- Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
-- the License. You may obtain a copy of the License at
--
--   http://www.apache.org/licenses/LICENSE-2.0
--
-- Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
-- an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
-- specific language governing permissions and limitations under the License.
--

-- This migration replaces the multi-valued 'tenants' column (text[]) with a single-valued 'tenant' column (text) on
-- all resource tables and their archived counterparts. The first element of the array is preserved as the new scalar
-- value.

-- Helper function to migrate a single table from tenants (text[]) to tenant (text):
create or replace function migrate_tenants_to_tenant(table_name text) returns void as $$
begin
  execute format('alter table %I add column tenant text not null default ''''', table_name);
  execute format('update %I set tenant = coalesce(tenants[1], '''')', table_name);
  execute format('alter table %I drop column tenants', table_name);
end;
$$ language plpgsql;

-- Helper function to drop a GIN index on tenants and create a B-tree index on tenant:
create or replace function migrate_tenants_index(table_name text) returns void as $$
begin
  execute format('drop index if exists %I', table_name || '_by_tenant');
  execute format('create index %I on %I (tenant)', table_name || '_by_tenant', table_name);
end;
$$ language plpgsql;

-- Migrate all main tables:
select migrate_tenants_to_tenant('cluster_templates');
select migrate_tenants_to_tenant('clusters');
select migrate_tenants_to_tenant('hubs');
select migrate_tenants_to_tenant('compute_instance_templates');
select migrate_tenants_to_tenant('compute_instances');
select migrate_tenants_to_tenant('host_types');
select migrate_tenants_to_tenant('network_classes');
select migrate_tenants_to_tenant('virtual_networks');
select migrate_tenants_to_tenant('subnets');
select migrate_tenants_to_tenant('security_groups');
select migrate_tenants_to_tenant('leases');
select migrate_tenants_to_tenant('public_ip_pools');
select migrate_tenants_to_tenant('public_ips');
select migrate_tenants_to_tenant('organizations');
select migrate_tenants_to_tenant('users');
select migrate_tenants_to_tenant('roles');
select migrate_tenants_to_tenant('role_bindings');
select migrate_tenants_to_tenant('cluster_catalog_items');
select migrate_tenants_to_tenant('compute_instance_catalog_items');

-- Migrate all archived tables:
select migrate_tenants_to_tenant('archived_cluster_templates');
select migrate_tenants_to_tenant('archived_clusters');
select migrate_tenants_to_tenant('archived_hubs');
select migrate_tenants_to_tenant('archived_compute_instance_templates');
select migrate_tenants_to_tenant('archived_compute_instances');
select migrate_tenants_to_tenant('archived_host_types');
select migrate_tenants_to_tenant('archived_network_classes');
select migrate_tenants_to_tenant('archived_virtual_networks');
select migrate_tenants_to_tenant('archived_subnets');
select migrate_tenants_to_tenant('archived_security_groups');
select migrate_tenants_to_tenant('archived_leases');
select migrate_tenants_to_tenant('archived_public_ip_pools');
select migrate_tenants_to_tenant('archived_public_ips');
select migrate_tenants_to_tenant('archived_organizations');
select migrate_tenants_to_tenant('archived_users');
select migrate_tenants_to_tenant('archived_roles');
select migrate_tenants_to_tenant('archived_role_bindings');
select migrate_tenants_to_tenant('archived_cluster_catalog_items');
select migrate_tenants_to_tenant('archived_compute_instance_catalog_items');

-- Recreate indexes on main tables as B-tree instead of GIN:
select migrate_tenants_index('cluster_templates');
select migrate_tenants_index('clusters');
select migrate_tenants_index('hubs');
select migrate_tenants_index('compute_instance_templates');
select migrate_tenants_index('compute_instances');
select migrate_tenants_index('host_types');
select migrate_tenants_index('network_classes');
select migrate_tenants_index('virtual_networks');
select migrate_tenants_index('subnets');
select migrate_tenants_index('security_groups');
select migrate_tenants_index('leases');
select migrate_tenants_index('public_ip_pools');
select migrate_tenants_index('public_ips');
select migrate_tenants_index('organizations');
select migrate_tenants_index('users');
select migrate_tenants_index('roles');
select migrate_tenants_index('role_bindings');
select migrate_tenants_index('cluster_catalog_items');
select migrate_tenants_index('compute_instance_catalog_items');

-- Drop the helper functions:
drop function migrate_tenants_to_tenant;
drop function migrate_tenants_index;
