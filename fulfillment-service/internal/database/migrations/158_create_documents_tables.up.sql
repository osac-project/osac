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

-- Create the generic persistence tables for Document objects and add the
-- metadata.description reference column to every existing object table.

create table documents (
  id text not null primary key,
  name text not null default '',
  creation_timestamp timestamp with time zone not null default now(),
  deletion_timestamp timestamp with time zone not null default 'epoch',
  finalizers text[] not null default '{}',
  creator text not null default '',
  tenant text not null default '',
  project ltree not null default ''::ltree,
  labels jsonb not null default '{}'::jsonb,
  annotations jsonb not null default '{}'::jsonb,
  description text,
  data jsonb not null,
  version integer not null default 0
);

create table archived_documents (
  id text not null,
  name text not null default '',
  creation_timestamp timestamp with time zone not null,
  deletion_timestamp timestamp with time zone not null,
  archival_timestamp timestamp with time zone not null default now(),
  creator text not null default '',
  tenant text not null default '',
  project ltree not null default ''::ltree,
  labels jsonb not null default '{}'::jsonb,
  annotations jsonb not null default '{}'::jsonb,
  description text,
  data jsonb not null,
  version integer not null default 0
);

create table active_documents (
  id text not null primary key references documents (id) on delete cascade
);

create index documents_by_creator on documents (creator);
create index documents_by_tenant on documents (tenant);
create index documents_by_label on documents using gin (labels);

create unique index documents_unique_name_per_tenant_project
  on documents (tenant, project, name)
  where deletion_timestamp = 'epoch' and name != '';

alter table documents
  add constraint documents_tenant_fk
  foreign key (tenant) references tenants (name);

alter table documents
  add constraint documents_project_fk
  foreign key (tenant, project) references projects (tenant, name);

create trigger check_immutable_columns
  before update on documents
  for each row
  execute function check_immutable_columns('id', 'name', 'tenant', 'project');

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on documents
  for each row execute function materialize_active_objects();

create trigger enqueue_change
  after insert or update or delete on documents
  for each row execute function enqueue_change();

-- Add the metadata.description reference column to every existing object table.
alter table add_on_operators add column if not exists description text;
alter table archived_add_on_operators add column if not exists description text;
alter table bare_metal_instances add column if not exists description text;
alter table archived_bare_metal_instances add column if not exists description text;
alter table bare_metal_instance_catalog_items add column if not exists description text;
alter table archived_bare_metal_instance_catalog_items add column if not exists description text;
alter table bare_metal_instance_templates add column if not exists description text;
alter table archived_bare_metal_instance_templates add column if not exists description text;
alter table bare_metal_instance_types add column if not exists description text;
alter table archived_bare_metal_instance_types add column if not exists description text;
alter table clusters add column if not exists description text;
alter table archived_clusters add column if not exists description text;
alter table cluster_catalog_items add column if not exists description text;
alter table archived_cluster_catalog_items add column if not exists description text;
alter table cluster_templates add column if not exists description text;
alter table archived_cluster_templates add column if not exists description text;
alter table cluster_versions add column if not exists description text;
alter table archived_cluster_versions add column if not exists description text;
alter table compute_instances add column if not exists description text;
alter table archived_compute_instances add column if not exists description text;
alter table compute_instance_catalog_items add column if not exists description text;
alter table archived_compute_instance_catalog_items add column if not exists description text;
alter table compute_instance_templates add column if not exists description text;
alter table archived_compute_instance_templates add column if not exists description text;
alter table disk_images add column if not exists description text;
alter table archived_disk_images add column if not exists description text;
alter table external_ips add column if not exists description text;
alter table archived_external_ips add column if not exists description text;
alter table external_ip_attachments add column if not exists description text;
alter table archived_external_ip_attachments add column if not exists description text;
alter table external_ip_pools add column if not exists description text;
alter table archived_external_ip_pools add column if not exists description text;
alter table fabric_domains add column if not exists description text;
alter table archived_fabric_domains add column if not exists description text;
alter table hubs add column if not exists description text;
alter table archived_hubs add column if not exists description text;
alter table identity_providers add column if not exists description text;
alter table archived_identity_providers add column if not exists description text;
alter table instance_types add column if not exists description text;
alter table archived_instance_types add column if not exists description text;
alter table nat_gateways add column if not exists description text;
alter table archived_nat_gateways add column if not exists description text;
alter table network_classes add column if not exists description text;
alter table archived_network_classes add column if not exists description text;
alter table projects add column if not exists description text;
alter table archived_projects add column if not exists description text;
alter table project_memberships add column if not exists description text;
alter table archived_project_memberships add column if not exists description text;
alter table quota_default_limits add column if not exists description text;
alter table archived_quota_default_limits add column if not exists description text;
alter table quota_limits add column if not exists description text;
alter table archived_quota_limits add column if not exists description text;
alter table quota_limit_requests add column if not exists description text;
alter table archived_quota_limit_requests add column if not exists description text;
alter table quota_thresholds add column if not exists description text;
alter table archived_quota_thresholds add column if not exists description text;
alter table roles add column if not exists description text;
alter table archived_roles add column if not exists description text;
alter table role_bindings add column if not exists description text;
alter table archived_role_bindings add column if not exists description text;
alter table secrets add column if not exists description text;
alter table archived_secrets add column if not exists description text;
alter table security_groups add column if not exists description text;
alter table archived_security_groups add column if not exists description text;
alter table service_accounts add column if not exists description text;
alter table archived_service_accounts add column if not exists description text;
alter table storage_backends add column if not exists description text;
alter table archived_storage_backends add column if not exists description text;
alter table storage_tiers add column if not exists description text;
alter table archived_storage_tiers add column if not exists description text;
alter table subnets add column if not exists description text;
alter table archived_subnets add column if not exists description text;
alter table tenants add column if not exists description text;
alter table archived_tenants add column if not exists description text;
alter table users add column if not exists description text;
alter table archived_users add column if not exists description text;
alter table virtual_networks add column if not exists description text;
alter table archived_virtual_networks add column if not exists description text;
alter table volumes add column if not exists description text;
alter table archived_volumes add column if not exists description text;
