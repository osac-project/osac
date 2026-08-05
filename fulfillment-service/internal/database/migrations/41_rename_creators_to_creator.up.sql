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

-- This migration replaces the multi-valued 'creators' column (text[]) with a single-valued 'creator' column (text) on
-- all resource tables and their archived counterparts. The first element of the array is preserved as the new scalar
-- value.

-- Helper function to migrate a single table from creators (text[]) to creator (text):
create or replace function migrate_creators_to_creator(table_name text) returns void as $$
begin
  execute format('alter table %I add column creator text not null default ''''', table_name);
  execute format('update %I set creator = coalesce(creators[1], '''')', table_name);
  execute format('alter table %I drop column creators', table_name);
end;
$$ language plpgsql;

-- Helper function to drop a GIN index on creators and create a B-tree index on creator:
create or replace function migrate_creators_index(table_name text) returns void as $$
begin
  execute format('drop index if exists %I', table_name || '_by_owner');
  execute format('create index %I on %I (creator)', table_name || '_by_owner', table_name);
end;
$$ language plpgsql;

-- Migrate all main tables:
select migrate_creators_to_creator('cluster_templates');
select migrate_creators_to_creator('clusters');
select migrate_creators_to_creator('hubs');
select migrate_creators_to_creator('compute_instance_templates');
select migrate_creators_to_creator('compute_instances');
select migrate_creators_to_creator('host_types');
select migrate_creators_to_creator('network_classes');
select migrate_creators_to_creator('virtual_networks');
select migrate_creators_to_creator('subnets');
select migrate_creators_to_creator('security_groups');
select migrate_creators_to_creator('leases');
select migrate_creators_to_creator('public_ip_pools');
select migrate_creators_to_creator('public_ips');
select migrate_creators_to_creator('organizations');
select migrate_creators_to_creator('users');
select migrate_creators_to_creator('roles');
select migrate_creators_to_creator('role_bindings');
select migrate_creators_to_creator('cluster_catalog_items');
select migrate_creators_to_creator('compute_instance_catalog_items');

-- Migrate all archived tables:
select migrate_creators_to_creator('archived_cluster_templates');
select migrate_creators_to_creator('archived_clusters');
select migrate_creators_to_creator('archived_hubs');
select migrate_creators_to_creator('archived_compute_instance_templates');
select migrate_creators_to_creator('archived_compute_instances');
select migrate_creators_to_creator('archived_host_types');
select migrate_creators_to_creator('archived_network_classes');
select migrate_creators_to_creator('archived_virtual_networks');
select migrate_creators_to_creator('archived_subnets');
select migrate_creators_to_creator('archived_security_groups');
select migrate_creators_to_creator('archived_leases');
select migrate_creators_to_creator('archived_public_ip_pools');
select migrate_creators_to_creator('archived_public_ips');
select migrate_creators_to_creator('archived_organizations');
select migrate_creators_to_creator('archived_users');
select migrate_creators_to_creator('archived_roles');
select migrate_creators_to_creator('archived_role_bindings');
select migrate_creators_to_creator('archived_cluster_catalog_items');
select migrate_creators_to_creator('archived_compute_instance_catalog_items');

-- Recreate indexes on main tables as B-tree instead of GIN:
select migrate_creators_index('cluster_templates');
select migrate_creators_index('clusters');
select migrate_creators_index('hubs');
select migrate_creators_index('compute_instance_templates');
select migrate_creators_index('compute_instances');
select migrate_creators_index('host_types');
select migrate_creators_index('network_classes');
select migrate_creators_index('virtual_networks');
select migrate_creators_index('subnets');
select migrate_creators_index('security_groups');
select migrate_creators_index('leases');
select migrate_creators_index('public_ip_pools');
select migrate_creators_index('public_ips');
select migrate_creators_index('organizations');
select migrate_creators_index('users');
select migrate_creators_index('roles');
select migrate_creators_index('role_bindings');
select migrate_creators_index('cluster_catalog_items');
select migrate_creators_index('compute_instance_catalog_items');

-- Drop the helper functions:
drop function migrate_creators_to_creator;
drop function migrate_creators_index;
