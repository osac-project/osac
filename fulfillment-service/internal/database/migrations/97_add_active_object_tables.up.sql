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

-- This migration introduces active_<table> companion tables for all object tables. Each active_<table> contains only
-- the primary keys of objects that are not soft-deleted (deletion_timestamp = 'epoch'). A generic trigger function
-- keeps these tables in sync automatically, enabling cross-object constraints that respect soft deletion via standard
-- PostgreSQL foreign key constraints.

-- Generic trigger function that maintains active_<table> companion tables. Derives the target table name dynamically
-- from TG_TABLE_NAME so a single function definition works for all object tables.
create function materialize_active_objects() returns trigger as $$
declare
  active_table text := 'active_' || TG_TABLE_NAME;
begin
  if TG_OP = 'INSERT' then
    if new.deletion_timestamp = 'epoch' then
      execute format('insert into %I (id) values ($1)', active_table) using new.id;
    end if;
    return new;
  elsif TG_OP = 'UPDATE' then
    if old.deletion_timestamp = 'epoch' and new.deletion_timestamp != 'epoch' then
      execute format('delete from %I where id = $1', active_table) using old.id;
    elsif old.deletion_timestamp != 'epoch' and new.deletion_timestamp = 'epoch' then
      execute format('insert into %I (id) values ($1)', active_table) using new.id;
    end if;
    return new;
  end if;
  return null;
end;
$$ language plpgsql;

-- Active companion tables. Each references the base table with ON DELETE CASCADE so that physical row removal
-- (after finalizer completion) automatically cleans up the active entry.

create table active_bare_metal_instance_catalog_items (
  id text not null primary key references bare_metal_instance_catalog_items (id) on delete cascade
);

create table active_bare_metal_instance_templates (
  id text not null primary key references bare_metal_instance_templates (id) on delete cascade
);

create table active_bare_metal_instance_types (
  id text not null primary key references bare_metal_instance_types (id) on delete cascade
);

create table active_bare_metal_instances (
  id text not null primary key references bare_metal_instances (id) on delete cascade
);

create table active_cluster_catalog_items (
  id text not null primary key references cluster_catalog_items (id) on delete cascade
);

create table active_cluster_templates (
  id text not null primary key references cluster_templates (id) on delete cascade
);

create table active_cluster_versions (
  id text not null primary key references cluster_versions (id) on delete cascade
);

create table active_clusters (
  id text not null primary key references clusters (id) on delete cascade
);

create table active_compute_instance_catalog_items (
  id text not null primary key references compute_instance_catalog_items (id) on delete cascade
);

create table active_compute_instance_templates (
  id text not null primary key references compute_instance_templates (id) on delete cascade
);

create table active_compute_instances (
  id text not null primary key references compute_instances (id) on delete cascade
);

create table active_external_ip_attachments (
  id text not null primary key references external_ip_attachments (id) on delete cascade
);

create table active_external_ip_pools (
  id text not null primary key references external_ip_pools (id) on delete cascade
);

create table active_external_ips (
  id text not null primary key references external_ips (id) on delete cascade
);

create table active_host_types (
  id text not null primary key references host_types (id) on delete cascade
);

create table active_hubs (
  id text not null primary key references hubs (id) on delete cascade
);

create table active_identity_providers (
  id text not null primary key references identity_providers (id) on delete cascade
);

create table active_instance_types (
  id text not null primary key references instance_types (id) on delete cascade
);

create table active_nat_gateways (
  id text not null primary key references nat_gateways (id) on delete cascade
);

create table active_network_classes (
  id text not null primary key references network_classes (id) on delete cascade
);

create table active_objects (
  id text not null primary key references objects (id) on delete cascade
);

create table active_project_memberships (
  id text not null primary key references project_memberships (id) on delete cascade
);

create table active_projects (
  id text not null primary key references projects (id) on delete cascade
);

create table active_role_bindings (
  id text not null primary key references role_bindings (id) on delete cascade
);

create table active_roles (
  id text not null primary key references roles (id) on delete cascade
);

create table active_secrets (
  id text not null primary key references secrets (id) on delete cascade
);

create table active_security_groups (
  id text not null primary key references security_groups (id) on delete cascade
);

create table active_storage_backends (
  id text not null primary key references storage_backends (id) on delete cascade
);

create table active_storage_tiers (
  id text not null primary key references storage_tiers (id) on delete cascade
);

create table active_subnets (
  id text not null primary key references subnets (id) on delete cascade
);

create table active_tenants (
  id text not null primary key references tenants (id) on delete cascade
);

create table active_users (
  id text not null primary key references users (id) on delete cascade
);

create table active_virtual_networks (
  id text not null primary key references virtual_networks (id) on delete cascade
);

create table active_volumes (
  id text not null primary key references volumes (id) on delete cascade
);

-- Attach triggers to all object tables. The trigger fires on INSERT and on UPDATE of the deletion_timestamp column,
-- so regular data updates do not incur trigger overhead.

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on bare_metal_instance_catalog_items
  for each row execute function materialize_active_objects();

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on bare_metal_instance_templates
  for each row execute function materialize_active_objects();

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on bare_metal_instance_types
  for each row execute function materialize_active_objects();

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on bare_metal_instances
  for each row execute function materialize_active_objects();

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on cluster_catalog_items
  for each row execute function materialize_active_objects();

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on cluster_templates
  for each row execute function materialize_active_objects();

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on cluster_versions
  for each row execute function materialize_active_objects();

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on clusters
  for each row execute function materialize_active_objects();

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on compute_instance_catalog_items
  for each row execute function materialize_active_objects();

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on compute_instance_templates
  for each row execute function materialize_active_objects();

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on compute_instances
  for each row execute function materialize_active_objects();

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on external_ip_attachments
  for each row execute function materialize_active_objects();

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on external_ip_pools
  for each row execute function materialize_active_objects();

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on external_ips
  for each row execute function materialize_active_objects();

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on host_types
  for each row execute function materialize_active_objects();

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on hubs
  for each row execute function materialize_active_objects();

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on identity_providers
  for each row execute function materialize_active_objects();

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on instance_types
  for each row execute function materialize_active_objects();

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on nat_gateways
  for each row execute function materialize_active_objects();

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on network_classes
  for each row execute function materialize_active_objects();

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on objects
  for each row execute function materialize_active_objects();

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on project_memberships
  for each row execute function materialize_active_objects();

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on projects
  for each row execute function materialize_active_objects();

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on role_bindings
  for each row execute function materialize_active_objects();

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on roles
  for each row execute function materialize_active_objects();

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on secrets
  for each row execute function materialize_active_objects();

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on security_groups
  for each row execute function materialize_active_objects();

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on storage_backends
  for each row execute function materialize_active_objects();

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on storage_tiers
  for each row execute function materialize_active_objects();

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on subnets
  for each row execute function materialize_active_objects();

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on tenants
  for each row execute function materialize_active_objects();

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on users
  for each row execute function materialize_active_objects();

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on virtual_networks
  for each row execute function materialize_active_objects();

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on volumes
  for each row execute function materialize_active_objects();

-- Backfill: populate active tables from existing active rows.

insert into active_bare_metal_instance_catalog_items (id)
select id from bare_metal_instance_catalog_items where deletion_timestamp = 'epoch';

insert into active_bare_metal_instance_templates (id)
select id from bare_metal_instance_templates where deletion_timestamp = 'epoch';

insert into active_bare_metal_instance_types (id)
select id from bare_metal_instance_types where deletion_timestamp = 'epoch';

insert into active_bare_metal_instances (id)
select id from bare_metal_instances where deletion_timestamp = 'epoch';

insert into active_cluster_catalog_items (id)
select id from cluster_catalog_items where deletion_timestamp = 'epoch';

insert into active_cluster_templates (id)
select id from cluster_templates where deletion_timestamp = 'epoch';

insert into active_cluster_versions (id)
select id from cluster_versions where deletion_timestamp = 'epoch';

insert into active_clusters (id)
select id from clusters where deletion_timestamp = 'epoch';

insert into active_compute_instance_catalog_items (id)
select id from compute_instance_catalog_items where deletion_timestamp = 'epoch';

insert into active_compute_instance_templates (id)
select id from compute_instance_templates where deletion_timestamp = 'epoch';

insert into active_compute_instances (id)
select id from compute_instances where deletion_timestamp = 'epoch';

insert into active_external_ip_attachments (id)
select id from external_ip_attachments where deletion_timestamp = 'epoch';

insert into active_external_ip_pools (id)
select id from external_ip_pools where deletion_timestamp = 'epoch';

insert into active_external_ips (id)
select id from external_ips where deletion_timestamp = 'epoch';

insert into active_host_types (id)
select id from host_types where deletion_timestamp = 'epoch';

insert into active_hubs (id)
select id from hubs where deletion_timestamp = 'epoch';

insert into active_identity_providers (id)
select id from identity_providers where deletion_timestamp = 'epoch';

insert into active_instance_types (id)
select id from instance_types where deletion_timestamp = 'epoch';

insert into active_nat_gateways (id)
select id from nat_gateways where deletion_timestamp = 'epoch';

insert into active_network_classes (id)
select id from network_classes where deletion_timestamp = 'epoch';

insert into active_objects (id)
select id from objects where deletion_timestamp = 'epoch';

insert into active_project_memberships (id)
select id from project_memberships where deletion_timestamp = 'epoch';

insert into active_projects (id)
select id from projects where deletion_timestamp = 'epoch';

insert into active_role_bindings (id)
select id from role_bindings where deletion_timestamp = 'epoch';

insert into active_roles (id)
select id from roles where deletion_timestamp = 'epoch';

insert into active_secrets (id)
select id from secrets where deletion_timestamp = 'epoch';

insert into active_security_groups (id)
select id from security_groups where deletion_timestamp = 'epoch';

insert into active_storage_backends (id)
select id from storage_backends where deletion_timestamp = 'epoch';

insert into active_storage_tiers (id)
select id from storage_tiers where deletion_timestamp = 'epoch';

insert into active_subnets (id)
select id from subnets where deletion_timestamp = 'epoch';

insert into active_tenants (id)
select id from tenants where deletion_timestamp = 'epoch';

insert into active_users (id)
select id from users where deletion_timestamp = 'epoch';

insert into active_virtual_networks (id)
select id from virtual_networks where deletion_timestamp = 'epoch';

insert into active_volumes (id)
select id from volumes where deletion_timestamp = 'epoch';
