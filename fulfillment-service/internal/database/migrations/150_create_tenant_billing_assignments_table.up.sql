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

-- Create the table for time-versioned tenant <-> billing account assignments (OSAC-6884). A flat table, like
-- billing_accounts, not the generic Metadata/Spec/Status jsonb object shape: reassignment closes the active row
-- and inserts a new one (history, not in-place mutation), which the generic single-row CRUD DAO does not model.
create table tenant_billing_assignments (
  id text not null primary key,
  name text not null check (name <> ''),
  tenant_id text not null references tenants (name),
  billing_account_id text not null references billing_accounts (id),
  effective_from timestamp with time zone not null default now(),
  -- NULL means this is the current assignment.
  effective_to timestamp with time zone,
  connection_state text not null default 'PENDING',
  message text not null default '',
  creation_timestamp timestamp with time zone not null default now(),
  creator text not null default ''
);

-- At most one active (effective_to is null) assignment per tenant. This is the authoritative, single point of
-- defense for the 1:1 invariant: application code must not reimplement it as a redundant check.
create unique index tenant_billing_assignments_unique_active_tenant
  on tenant_billing_assignments (tenant_id)
  where effective_to is null;

-- At most one active (effective_to is null) assignment per billing account.
create unique index tenant_billing_assignments_unique_active_account
  on tenant_billing_assignments (billing_account_id)
  where effective_to is null;

create index tenant_billing_assignments_by_tenant on tenant_billing_assignments (tenant_id);
create index tenant_billing_assignments_by_account on tenant_billing_assignments (billing_account_id);
