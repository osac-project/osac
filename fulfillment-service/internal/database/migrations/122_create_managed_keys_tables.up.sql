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

create table managed_keys (
  id text not null primary key,
  name text not null check (name <> ''),
  creation_timestamp timestamp with time zone not null default now(),
  deletion_timestamp timestamp with time zone not null default 'epoch',
  finalizers text[] not null default '{}',
  creator text not null default '',
  tenant text not null default '' check (tenant <> 'shared'),
  project ltree not null default ''::ltree,
  labels jsonb not null default '{}'::jsonb,
  annotations jsonb not null default '{}'::jsonb,
  data jsonb not null,
  version integer not null default 0
);

create table archived_managed_keys (
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

create table active_managed_keys (
  id text not null primary key references managed_keys (id) on delete cascade
);

create index managed_keys_by_creator on managed_keys (creator);
create index managed_keys_by_tenant on managed_keys (tenant);
create index managed_keys_by_label on managed_keys using gin (labels);
create unique index managed_keys_unique_name_per_tenant_project
  on managed_keys (tenant, project, name);

alter table managed_keys
  add constraint managed_keys_tenant_fk
  foreign key (tenant) references tenants (name);

alter table managed_keys
  add constraint managed_keys_project_fk
  foreign key (tenant, project) references projects (tenant, name);

create trigger check_immutable_columns
  before update on managed_keys
  for each row execute function check_immutable_columns('id', 'name', 'tenant', 'project');

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on managed_keys
  for each row execute function materialize_active_objects();

create trigger enqueue_change
  after insert or update or delete on managed_keys
  for each row execute function enqueue_change();
