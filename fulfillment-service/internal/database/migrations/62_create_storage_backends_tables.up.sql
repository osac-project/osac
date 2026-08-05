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

-- Create the storage_backends tables following the generic schema pattern.
-- StorageBackend is platform-scoped (tenant is always 'shared').

create table storage_backends (
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

create table archived_storage_backends (
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

create index storage_backends_by_creator on storage_backends (creator);
create index storage_backends_by_label on storage_backends using gin (labels);

-- Name uniqueness across all backends (active and soft-deleted).
create unique index storage_backends_unique_name
  on storage_backends (name);

-- Tenant foreign key referencing the tenants table.
alter table storage_backends
  add constraint storage_backends_tenant_fk
  foreign key (tenant) references tenants (id);

-- Enforce immutability of id and name columns at the database level.
create trigger check_immutable_columns
  before update on storage_backends
  for each row
  execute function check_immutable_columns('id', 'name', 'tenant');
