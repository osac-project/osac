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

-- Create the identity_providers tables following the generic schema pattern.

create table identity_providers (
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

create table archived_identity_providers (
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

create index identity_providers_by_name on identity_providers (name);
create index identity_providers_by_creator on identity_providers (creator);
create index identity_providers_by_tenant on identity_providers (tenant);
create index identity_providers_by_label on identity_providers using gin (labels);

-- Identity providers must belong to a specific organization (tenant).
-- They cannot be in the 'shared' or 'system' tenants - each IDP is scoped to one organization.
alter table identity_providers add constraint identity_providers_tenant_fk foreign key (tenant) references tenants (id);

-- Add unique constraint to prevent duplicate identity provider names within a tenant.
-- This ensures each tenant can use common names like "corporate-ldap" without conflicts,
-- while preventing accidental duplicates within the same tenant.
alter table identity_providers add constraint identity_providers_tenant_name_unique unique (tenant, name);

-- Attach the trigger to the table to enforce immutability of the id, name and tenant columns:
create trigger check_immutable_columns
  before update on identity_providers
  for each row
  execute function check_immutable_columns('id', 'name', 'tenant');
