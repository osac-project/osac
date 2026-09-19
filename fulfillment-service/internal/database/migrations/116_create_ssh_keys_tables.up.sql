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

-- Create tables for tenant-scoped SSH public keys. The public key material is
-- stored in the generic JSONB data column and is never copied to the archive
-- or exposed through a private-key field.
create table ssh_keys (
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
  version integer not null default 0,
  constraint ssh_keys_project_empty check (project = ''::ltree)
);

create table archived_ssh_keys (
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

create index ssh_keys_by_name on ssh_keys (name);
create index ssh_keys_by_creator on ssh_keys (creator);
create index ssh_keys_by_tenant on ssh_keys (tenant);

-- Keep names reserved for the lifetime of the row, including the pending
-- deletion interval before finalizer-driven archival.
create unique index ssh_keys_unique_name_per_tenant
  on ssh_keys (tenant, name);

alter table ssh_keys
  add constraint ssh_keys_tenant_fk
  foreign key (tenant) references tenants (name);

create trigger check_immutable_columns
  before update on ssh_keys
  for each row
  execute function check_immutable_columns('id', 'name', 'tenant', 'project');

-- Active companion table used by deletion-protection queries.
create table active_ssh_keys (
  id text not null primary key references ssh_keys (id) on delete cascade
);

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on ssh_keys
  for each row execute function materialize_active_objects();

insert into active_ssh_keys (id)
select id from ssh_keys where deletion_timestamp = 'epoch';
