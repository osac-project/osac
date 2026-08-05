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

-- This 'objects' table is used only for testing purposes, but it is very convenient to have it here, because otherwise
-- we frequently forget to add the columns and constraints that all tables should have.

create table objects (
  id text not null primary key default uuidv7(),
  tenant text not null default '',
  project ltree not null default ''::ltree,
  name text not null default '',
  creator text not null default '',
  creation_timestamp timestamp with time zone not null default now(),
  deletion_timestamp timestamp with time zone not null default 'epoch',
  finalizers text[] not null default '{}',
  labels jsonb not null default '{}'::jsonb,
  annotations jsonb not null default '{}'::jsonb,
  data jsonb not null default '{}'::jsonb,
  version integer not null default 0
);

alter table objects add constraint objects_tenant_fk foreign key (tenant) references tenants (name);
alter table objects add constraint objects_project_fk foreign key (tenant, project) references projects (tenant, name);

create trigger check_immutable_columns
  before update on objects
  for each row
  execute function check_immutable_columns('id', 'tenant', 'project', 'name');

create table archived_objects (
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
  version integer not null default 0,
  data jsonb not null
);
