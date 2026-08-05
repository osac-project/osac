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

-- Create tables for Secret resources.
--
-- Secrets store sensitive key-value data such as TLS certificates, pull secrets, and credentials.
-- The JSONB data column holds the SecretSpec (backend, coordinates) and SecretStatus — not the raw
-- secret bytes, which are resolved at the server layer.

create table secrets (
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

create table archived_secrets (
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

create index secrets_by_name_tenant on secrets (name, tenant);
create index secrets_by_creator on secrets (creator);
create index secrets_by_tenant on secrets (tenant);
create index secrets_by_label on secrets using gin (labels);

alter table secrets
  add constraint secrets_tenant_fk
  foreign key (tenant) references tenants (name);

alter table secrets
  add constraint secrets_project_fk
  foreign key (tenant, project) references projects (tenant, name);

create trigger check_immutable_columns
  before update on secrets
  for each row
  execute function check_immutable_columns('id', 'tenant', 'project');
