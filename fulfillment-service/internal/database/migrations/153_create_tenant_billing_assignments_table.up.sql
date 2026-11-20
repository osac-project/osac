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

-- Create the table for time-versioned tenant <-> billing account assignments. Like billing_accounts, this uses the
-- ordinary metadata columns plus the private protobuf JSON body, with the interval columns maintained as a one-way
-- projection by the 'project_tenant_billing_assignment' trigger.
--
-- Assignments are append-only interval history: reassignment closes the active row and inserts a new one, and rows
-- are never deleted. The generic single-row CRUD shape cannot model that, which is why this is a dedicated table.
--
-- 'tenant_id' is a plain text token with deliberately NO foreign key to the live 'tenants' table: retained billing
-- history must survive tenant deletion, so a live-tenant link would either block or cascade it. The account
-- reference keeps its foreign key, which is why account rows are never physically removed.
begin;

create table tenant_billing_assignments (
  id text not null primary key,
  name text not null check (name <> ''),
  tenant_id text not null,
  billing_account_id text not null references billing_accounts (id),
  effective_from timestamp with time zone not null,
  -- NULL means this is the current assignment.
  effective_to timestamp with time zone,
  connection_state text not null,
  message text not null,
  creation_timestamp timestamp with time zone not null default now(),
  creator text not null default '',
  deletion_timestamp timestamp with time zone not null default 'epoch',
  finalizers text[] not null default '{}',
  tenant text not null default '',
  project ltree not null default ''::ltree,
  labels jsonb not null default '{}'::jsonb,
  annotations jsonb not null default '{}'::jsonb,
  data jsonb not null,
  version integer not null default 0,
  constraint tenant_billing_assignments_nonempty_id check (id <> ''),
  constraint tenant_billing_assignments_no_project check (project = ''::ltree),
  constraint tenant_billing_assignments_valid_interval check (effective_to >= effective_from),
  constraint tenant_billing_assignments_canonical_owner check (
    tenant = tenant_id and annotations->>'osac.openshift.io/tenant' is not distinct from tenant_id)
);

-- At most one active (effective_to is null) assignment per tenant. This is the authoritative defense for the 1:1
-- invariant; application code must not reimplement it.
create unique index tenant_billing_assignments_unique_active_tenant
  on tenant_billing_assignments (tenant_id)
  where effective_to is null;

-- At most one active (effective_to is null) assignment per billing account.
create unique index tenant_billing_assignments_unique_active_account
  on tenant_billing_assignments (billing_account_id)
  where effective_to is null;

create index tenant_billing_assignments_by_tenant on tenant_billing_assignments (tenant_id);
create index tenant_billing_assignments_by_account on tenant_billing_assignments (billing_account_id);

-- Assignment names, unlike account names, are NOT lifetime unique: reassignment deliberately creates another
-- interval with the same name.
create index tenant_billing_assignments_by_name on tenant_billing_assignments (name);

-- History is never pruned, and the List default order is effective_from desc, so the unfiltered order must not pay
-- a sequential scan plus top-N sort as history grows.
create index tenant_billing_assignments_by_effective_from
  on tenant_billing_assignments (effective_from desc);

-- Derive the interval columns from the authoritative payload. Both endpoints are projected exactly; an endpoint
-- that cannot be represented at microsecond precision is rejected instead of silently rounded.
create function project_tenant_billing_assignment() returns trigger as $$
declare
  spec jsonb := new.data->'spec';
  s jsonb := new.data->'status';
  p tenant_billing_assignments%rowtype;
  field text;
begin
  if TG_OP = 'DELETE' then
    raise exception 'billing interval history cannot be deleted' using errcode = '23514';
  end if;
  if jsonb_typeof(new.data) is distinct from 'object' or new.data - 'spec' - 'status' <> '{}'::jsonb
    or jsonb_typeof(spec) is distinct from 'object' or spec - 'tenant' - 'billing_account' <> '{}'::jsonb
    or jsonb_typeof(s) is distinct from 'object'
    or s - array['connection_state', 'effective_from_timestamp', 'effective_to_timestamp', 'message'] <> '{}'::jsonb
  then
    raise exception 'invalid billing assignment payload' using errcode = '23514';
  end if;
  foreach field in array array['tenant', 'billing_account'] loop
    if jsonb_typeof(spec->field) is distinct from 'object' or (spec->field) - 'id' - 'name' <> '{}'::jsonb
      or jsonb_typeof(spec->field->'id') is distinct from 'string'
      or jsonb_typeof(spec->field->'name') is distinct from 'string'
      or spec->field->>'id' = '' or spec->field->>'name' = ''
    then
      raise exception 'invalid canonical billing reference' using errcode = '23514';
    end if;
  end loop;
  foreach field in array array['connection_state', 'effective_from_timestamp', 'effective_to_timestamp', 'message'] loop
    if s ? field and jsonb_typeof(s->field) is distinct from 'string' then
      raise exception 'invalid billing assignment field type' using errcode = '23514';
    end if;
  end loop;
  p.tenant_id := spec#>>'{tenant,id}';
  p.billing_account_id := spec#>>'{billing_account,id}';
  -- Interval constraints and ordering use these columns, so neither endpoint may lose precision.
  p.effective_from := project_billing_timestamp(s->>'effective_from_timestamp', true);
  p.effective_to := project_billing_timestamp(s->>'effective_to_timestamp', true);
  p.message := coalesce(s->>'message', '');
  p.connection_state := case coalesce(s->>'connection_state', 'TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_UNSPECIFIED')
    when 'TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_UNSPECIFIED' then 'UNSPECIFIED'
    when 'TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_PENDING' then 'PENDING'
    when 'TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED' then 'CONNECTED'
    when 'TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_FAILED' then 'FAILED' end;
  if new.tenant is distinct from p.tenant_id or spec#>>'{tenant,name}' is distinct from p.tenant_id
    or spec#>>'{billing_account,name}' is distinct from
      ('ba-' || left(encode(sha256(convert_to(p.billing_account_id, 'UTF8')), 'hex'), 60))
    or p.connection_state is null or p.effective_from is null
  then
    raise exception 'inconsistent canonical billing assignment' using errcode = '23514';
  end if;
  if TG_OP = 'UPDATE' then
    if old.effective_to is not null and new is distinct from old then
      raise exception 'closed billing history is immutable' using errcode = '23514';
    end if;
    if row(new.id, new.name, new.tenant, new.project, new.creation_timestamp, new.creator,
        p.tenant_id, p.billing_account_id, p.effective_from)
      is distinct from row(old.id, old.name, old.tenant, old.project, old.creation_timestamp, old.creator,
        old.tenant_id, old.billing_account_id, old.effective_from)
    then
      raise exception 'immutable billing interval identity' using errcode = '23514';
    end if;
    if row(new.tenant_id, new.billing_account_id, new.effective_from, new.effective_to, new.connection_state, new.message)
        is distinct from row(old.tenant_id, old.billing_account_id, old.effective_from, old.effective_to, old.connection_state, old.message)
      and row(new.tenant_id, new.billing_account_id, new.effective_from, new.effective_to, new.connection_state, new.message)
        is distinct from row(p.tenant_id, p.billing_account_id, p.effective_from, p.effective_to, p.connection_state, p.message)
    then
      raise exception 'billing assignment projections are read only' using errcode = '23514';
    end if;
  else
    if (new.tenant_id is not null and new.tenant_id is distinct from p.tenant_id)
      or (new.billing_account_id is not null and new.billing_account_id is distinct from p.billing_account_id)
      or (new.effective_from is not null and new.effective_from is distinct from p.effective_from)
      or (new.effective_to is not null and new.effective_to is distinct from p.effective_to)
      or (new.connection_state is not null and new.connection_state is distinct from p.connection_state)
      or (new.message is not null and new.message is distinct from p.message)
    then
      raise exception 'inconsistent billing assignment insert' using errcode = '23514';
    end if;
  end if;
  new.tenant_id := p.tenant_id;
  new.billing_account_id := p.billing_account_id;
  new.effective_from := p.effective_from;
  new.effective_to := p.effective_to;
  new.connection_state := p.connection_state;
  new.message := p.message;
  return new;
end;
$$ language plpgsql;

create trigger project_tenant_billing_assignment before insert or update or delete on tenant_billing_assignments
  for each row execute function project_tenant_billing_assignment();

-- Ordinary lifecycle companions, NOT a second interval-history store: closed intervals stay in the main table.
create table active_tenant_billing_assignments (
  id text not null primary key references tenant_billing_assignments (id) on delete cascade
);

create table archived_tenant_billing_assignments (
  id text not null,
  name text not null default '',
  creation_timestamp timestamptz not null,
  deletion_timestamp timestamptz not null,
  archival_timestamp timestamptz not null default now(),
  creator text not null default '',
  tenant text not null default '',
  project ltree not null default ''::ltree,
  labels jsonb not null default '{}'::jsonb,
  annotations jsonb not null default '{}'::jsonb,
  data jsonb not null,
  version integer not null default 0
);

create trigger materialize_active_objects after insert or update of deletion_timestamp on tenant_billing_assignments
  for each row execute function materialize_active_objects();

commit;
