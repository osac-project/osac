--
-- Copyright (c) 2026 Red Hat Inc.
--
-- Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
-- the License. You may obtain a copy of the License at
--
--   http://www.apache.org/licenses/LICENSE-2.0
--
-- Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
-- "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
-- specific language governing permissions and limitations under the License.
--

-- Create tables for Volume resources.
--
-- Volume is a tenant-scoped resource that represents block storage provisioned on a backend
-- storage array through the OSAC CSI driver. The fulfillment-service tracks volume inventory
-- and the osac-operator Volume controller handles vendor provisioning.

create table volumes (
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
  data jsonb not null,
  version integer not null default 0
);

create table archived_volumes (
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

create index volumes_by_name on volumes (name);
create index volumes_by_creator on volumes (creator);
create index volumes_by_tenant on volumes (tenant);
create index volumes_by_label on volumes using gin (labels);

-- Unique name per tenant on active records only.
create unique index volumes_unique_name_per_tenant
  on volumes (name, tenant)
  where deletion_timestamp = 'epoch'
    and name != '';

-- Tenant foreign key referencing the tenants table.
alter table volumes
  add constraint volumes_tenant_fk
  foreign key (tenant) references tenants (name);

-- Project foreign key.
alter table volumes
  add constraint volumes_project_fk
  foreign key (tenant, project) references projects (tenant, name);

-- Enforce immutability of id, tenant, and project columns at the database level.
create trigger check_immutable_columns
  before update on volumes
  for each row
  execute function check_immutable_columns('id', 'name', 'tenant', 'project');
