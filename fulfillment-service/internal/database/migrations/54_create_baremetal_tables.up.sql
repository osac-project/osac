--
-- Copyright (c) 2025 Red Hat Inc.
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

-- Create tables for bare metal resources:
--
-- BareMetalInstanceTemplate defines hardware profiles (host type, OS image, network
-- configuration) used to create bare metal instances via catalog items.
--
-- BareMetalInstanceCatalogItem is a curated offering that references a template and
-- controls which fields users can set, enforces defaults, and validates input.
--
-- BareMetalInstance represents a provisioned bare metal machine assigned to a tenant.
--

create table bare_metal_instance_templates (
  id text not null primary key,
  name text not null default '',
  creation_timestamp timestamp with time zone not null default now(),
  deletion_timestamp timestamp with time zone not null default 'epoch',
  finalizers text[] not null default '{}',
  creator text not null default '',
  tenant text not null default '',
  labels jsonb not null default '{}'::jsonb,
  annotations jsonb not null default '{}'::jsonb,
  data jsonb not null,
  version integer not null default 0
);

create table archived_bare_metal_instance_templates (
  id text not null,
  name text not null default '',
  creation_timestamp timestamp with time zone not null,
  deletion_timestamp timestamp with time zone not null,
  archival_timestamp timestamp with time zone not null default now(),
  creator text not null default '',
  tenant text not null default '',
  labels jsonb not null default '{}'::jsonb,
  annotations jsonb not null default '{}'::jsonb,
  data jsonb not null,
  version integer not null default 0
);

create index bare_metal_instance_templates_by_name on bare_metal_instance_templates (name);
create index bare_metal_instance_templates_by_creator on bare_metal_instance_templates (creator);
create index bare_metal_instance_templates_by_tenant on bare_metal_instance_templates (tenant);
create index bare_metal_instance_templates_by_label on bare_metal_instance_templates using gin (labels);

create table bare_metal_instance_catalog_items (
  id text not null primary key,
  name text not null default '',
  creation_timestamp timestamp with time zone not null default now(),
  deletion_timestamp timestamp with time zone not null default 'epoch',
  finalizers text[] not null default '{}',
  creator text not null default '',
  tenant text not null default '',
  labels jsonb not null default '{}'::jsonb,
  annotations jsonb not null default '{}'::jsonb,
  data jsonb not null,
  version integer not null default 0
);

create table archived_bare_metal_instance_catalog_items (
  id text not null,
  name text not null default '',
  creation_timestamp timestamp with time zone not null,
  deletion_timestamp timestamp with time zone not null,
  archival_timestamp timestamp with time zone not null default now(),
  creator text not null default '',
  tenant text not null default '',
  labels jsonb not null default '{}'::jsonb,
  annotations jsonb not null default '{}'::jsonb,
  data jsonb not null,
  version integer not null default 0
);

create index bare_metal_instance_catalog_items_by_name on bare_metal_instance_catalog_items (name);
create index bare_metal_instance_catalog_items_by_creator on bare_metal_instance_catalog_items (creator);
create index bare_metal_instance_catalog_items_by_tenant on bare_metal_instance_catalog_items (tenant);
create index bare_metal_instance_catalog_items_by_label on bare_metal_instance_catalog_items using gin (labels);

create table bare_metal_instances (
  id text not null primary key,
  name text not null default '',
  creation_timestamp timestamp with time zone not null default now(),
  deletion_timestamp timestamp with time zone not null default 'epoch',
  finalizers text[] not null default '{}',
  creator text not null default '',
  tenant text not null default '',
  labels jsonb not null default '{}'::jsonb,
  annotations jsonb not null default '{}'::jsonb,
  data jsonb not null,
  version integer not null default 0
);

create table archived_bare_metal_instances (
  id text not null,
  name text not null default '',
  creation_timestamp timestamp with time zone not null,
  deletion_timestamp timestamp with time zone not null,
  archival_timestamp timestamp with time zone not null default now(),
  creator text not null default '',
  tenant text not null default '',
  labels jsonb not null default '{}'::jsonb,
  annotations jsonb not null default '{}'::jsonb,
  data jsonb not null,
  version integer not null default 0
);

create index bare_metal_instances_by_name on bare_metal_instances (name);
create index bare_metal_instances_by_creator on bare_metal_instances (creator);
create index bare_metal_instances_by_tenant on bare_metal_instances (tenant);
create index bare_metal_instances_by_label on bare_metal_instances using gin (labels);
create index bare_metal_instances_by_catalog_item on bare_metal_instances ((data->'spec'->>'catalog_item'));

alter table bare_metal_instance_templates
  add constraint bare_metal_instance_templates_tenant_fk
  foreign key (tenant) references organizations (id);

alter table bare_metal_instance_catalog_items
  add constraint bare_metal_instance_catalog_items_tenant_fk
  foreign key (tenant) references organizations (id);

alter table bare_metal_instances
  add constraint bare_metal_instances_tenant_fk
  foreign key (tenant) references organizations (id);
