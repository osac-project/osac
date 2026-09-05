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

-- Create tables for AddOnOperator resources.
-- AddOnOperator is tenant-scoped: operators can be globally visible (tenant='shared') or scoped to a
-- specific tenant.

create table add_on_operators (
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

create table archived_add_on_operators (
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

create index add_on_operators_by_creator on add_on_operators (creator);
create index add_on_operators_by_tenant on add_on_operators (tenant);
create index add_on_operators_by_label on add_on_operators using gin (labels);

-- Name uniqueness per tenant, active records only.
create unique index add_on_operators_unique_name_per_tenant
  on add_on_operators (name, tenant)
  where deletion_timestamp = 'epoch'
    and name != '';

-- Tenant foreign key referencing the tenants table.
alter table add_on_operators
  add constraint add_on_operators_tenant_fk
  foreign key (tenant) references tenants (name);

-- Project foreign key referencing the projects table.
alter table add_on_operators
  add constraint add_on_operators_project_fk
  foreign key (tenant, project) references projects (tenant, name);

-- Enforce immutability of id, name, tenant, and project columns at the database level.
create trigger check_immutable_columns
  before update on add_on_operators
  for each row
  execute function check_immutable_columns('id', 'name', 'tenant', 'project');

-- Active companion table for add_on_operators (see migration 97 for the pattern).
create table active_add_on_operators (
  id text not null primary key references add_on_operators (id) on delete cascade
);

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on add_on_operators
  for each row execute function materialize_active_objects();

insert into active_add_on_operators (id)
select id from add_on_operators where deletion_timestamp = 'epoch';
