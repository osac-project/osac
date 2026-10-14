--
-- Copyright (c) 2026 Red Hat Inc.
--
-- Licensed under the Apache License, Version 2.0 (the "License");
-- you may not use this file except in compliance with the License.
-- You may obtain a copy of the License at
--
--   http://www.apache.org/licenses/LICENSE-2.0
--
-- Unless required by applicable law or agreed to in writing, software
-- distributed under the License is distributed on an "AS IS" BASIS,
-- WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
-- See the License for the specific language governing permissions and
-- limitations under the License.
--

-- Provider accounts are global to this OSAC deployment, so they use a small
-- dedicated table instead of the tenant-scoped generic object DAO.
create table billing_accounts (
  id text not null primary key,
  provider_key text not null,
  provider_account_reference text not null,
  display_name text not null,
  base_currency text not null default '',
  state text not null check (state in ('ACTIVE', 'INACTIVE', 'UNKNOWN')),
  status_observed_at timestamptz not null,
  unique (provider_key, provider_account_reference)
);

create index billing_accounts_by_display_name on billing_accounts (display_name);

-- Tenant assignments use the fulfillment generic-object layout so they retain
-- metadata, optimistic versioning, soft deletion, and transactional events.
-- These generated columns let PostgreSQL enforce the active-assignment rules
-- while the protobuf object remains the source of truth.
create table tenant_billing_assignments (
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
  version integer not null default 0,
  billing_account_id text generated always as (data #>> '{spec,billing_account_id}') stored,
  assignment_tenant_id text generated always as (data #>> '{spec,tenant_id}') stored,
  effective_to text generated always as (data #>> '{status,effective_to}') stored,
  constraint tenant_billing_assignments_account_required check (billing_account_id is not null),
  constraint tenant_billing_assignments_tenant_matches_metadata check (assignment_tenant_id = tenant),
  constraint tenant_billing_assignments_account_fk
    foreign key (billing_account_id) references billing_accounts (id),
  constraint tenant_billing_assignments_tenant_fk
    foreign key (tenant) references tenants (name)
);

create table archived_tenant_billing_assignments (
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

create index tenant_billing_assignments_by_tenant on tenant_billing_assignments (tenant);
create index tenant_billing_assignments_by_account on tenant_billing_assignments (billing_account_id);
create index tenant_billing_assignments_by_creator on tenant_billing_assignments (creator);
create index tenant_billing_assignments_by_label on tenant_billing_assignments using gin (labels);
create unique index tenant_billing_assignments_unique_name on tenant_billing_assignments (name, tenant, project);

create unique index tenant_billing_assignments_one_active_per_tenant
  on tenant_billing_assignments (tenant)
  where deletion_timestamp = 'epoch' and coalesce(effective_to, '') = '';

create unique index tenant_billing_assignments_one_active_per_account
  on tenant_billing_assignments (billing_account_id)
  where deletion_timestamp = 'epoch' and coalesce(effective_to, '') = '';

create trigger check_immutable_columns
  before update on tenant_billing_assignments
  for each row
  execute function check_immutable_columns('id', 'name', 'tenant', 'project');

create table active_tenant_billing_assignments (
  id text not null primary key references tenant_billing_assignments (id) on delete cascade
);

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on tenant_billing_assignments
  for each row execute function materialize_active_objects();

create trigger enqueue_change
  after insert or update or delete on tenant_billing_assignments
  for each row execute function enqueue_change();
