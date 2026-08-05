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

-- Create tables for BareMetalInstanceType resources.
-- BareMetalInstanceType is platform-scoped (tenant is always 'shared').

create table bare_metal_instance_types (
  id text not null primary key,
  name text not null check (name <> ''),
  creation_timestamp timestamp with time zone not null default now(),
  deletion_timestamp timestamp with time zone not null default 'epoch',
  finalizers text[] not null default '{}',
  creator text not null default '',
  tenant text not null default '',
  project ltree not null default ''::ltree,
  labels jsonb not null default '{}'::jsonb,
  annotations jsonb not null default '{}'::jsonb,
  data jsonb not null,
  version integer not null default 0
);

create table archived_bare_metal_instance_types (
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
  data jsonb not null,
  version integer not null default 0
);

create index bare_metal_instance_types_by_creator on bare_metal_instance_types (creator);
create index bare_metal_instance_types_by_tenant on bare_metal_instance_types (tenant);
create index bare_metal_instance_types_by_label on bare_metal_instance_types using gin (labels);

-- Name uniqueness across all bare metal instance types (active and soft-deleted).
create unique index bare_metal_instance_types_unique_name
  on bare_metal_instance_types (name);

-- Tenant foreign key referencing the tenants table.
alter table bare_metal_instance_types
  add constraint bare_metal_instance_types_tenant_fk
  foreign key (tenant) references tenants (name);

-- Project foreign key referencing the projects table.
alter table bare_metal_instance_types
  add constraint bare_metal_instance_types_project_fk
  foreign key (tenant, project) references projects (tenant, name);

-- Enforce immutability of id, name, tenant, and project columns at the database level.
create trigger check_immutable_columns
  before update on bare_metal_instance_types
  for each row
  execute function check_immutable_columns('id', 'name', 'tenant', 'project');
