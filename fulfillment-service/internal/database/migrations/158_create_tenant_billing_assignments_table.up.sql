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

-- Assignments retain interval history: reassignment closes the active row and inserts a new one, and rows are never
-- deleted. A write-time trigger validates live tenant references; the tenant column has no foreign key so history
-- survives physical tenant deletion.
begin;

create table tenant_billing_assignments (
  id text not null primary key,
  name text not null check (name <> ''),
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
  constraint tenant_billing_assignments_valid_interval check ((
    jsonb_typeof(data #> '{status,effective_from_timestamp}') = 'string'
    and (
      data #> '{status,effective_to_timestamp}' is null
      or jsonb_typeof(data #> '{status,effective_to_timestamp}') = 'string'
    )
    and (
      data #> '{status,effective_to_timestamp}' is null
      or (data #>> '{status,effective_to_timestamp}')::timestamptz >=
        (data #>> '{status,effective_from_timestamp}')::timestamptz
    )
  ) is true),
  constraint tenant_billing_assignments_canonical_owner check ((
    tenant <> '' and annotations->>'osac.openshift.io/tenant' is not distinct from tenant
  ) is true)
);

-- At most one active assignment per tenant and billing account.
create unique index tenant_billing_assignments_unique_active_tenant
  on tenant_billing_assignments (tenant)
  where data->'status'->>'effective_to_timestamp' is null;

create unique index tenant_billing_assignments_unique_active_account
  on tenant_billing_assignments ((data->'spec'->'billing_account'->>'id'))
  where data->'status'->>'effective_to_timestamp' is null;

create index tenant_billing_assignments_by_tenant on tenant_billing_assignments (tenant);
create index tenant_billing_assignments_by_account
  on tenant_billing_assignments ((data->'spec'->'billing_account'->>'id'));

-- Assignment names, unlike account names, are NOT lifetime unique: reassignment deliberately creates another
-- interval with the same name.
create index tenant_billing_assignments_by_name on tenant_billing_assignments (name);

-- History is retained, and the List default order is effective_from desc.
create index tenant_billing_assignments_by_effective_from
  on tenant_billing_assignments ((data->'status'->>'effective_from_timestamp') desc);

-- Validate the JSON contract that was formerly checked by the interval projector.
create function validate_tenant_billing_assignment_data() returns trigger as $$
declare
  spec jsonb := new.data->'spec';
  s jsonb := new.data->'status';
  field text;
  billing_account_id text;
  effective_from timestamptz;
  effective_to timestamptz;
  connection_state text;
begin
  if jsonb_typeof(new.data) is distinct from 'object' or new.data - 'spec' - 'status' <> '{}'::jsonb
    or jsonb_typeof(spec) is distinct from 'object' or spec - 'billing_account' <> '{}'::jsonb
    or jsonb_typeof(s) is distinct from 'object'
    or s - array['connection_state', 'effective_from_timestamp', 'effective_to_timestamp', 'message'] <> '{}'::jsonb
  then
    raise exception 'invalid billing assignment payload' using errcode = '23514';
  end if;

  foreach field in array array['billing_account'] loop
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

  billing_account_id := spec#>>'{billing_account,id}';
  effective_from := project_billing_timestamp(s->>'effective_from_timestamp', true);
  effective_to := project_billing_timestamp(s->>'effective_to_timestamp', true);
  connection_state := case coalesce(s->>'connection_state',
      'TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_UNSPECIFIED')
    when 'TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_UNSPECIFIED' then 'UNSPECIFIED'
    when 'TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_PENDING' then 'PENDING'
    when 'TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED' then 'CONNECTED'
    when 'TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_FAILED' then 'FAILED' end;

  if new.tenant = '' or new.annotations->>'osac.openshift.io/tenant' is distinct from new.tenant
    or spec#>>'{billing_account,name}' is distinct from
      ('ba-' || left(encode(sha256(convert_to(billing_account_id, 'UTF8')), 'hex'), 60))
    or connection_state is null or effective_from is null
  then
    raise exception 'inconsistent canonical billing assignment' using errcode = '23514';
  end if;

  return new;
end;
$$ language plpgsql;

create trigger check_tenant_billing_assignment_data
  before insert or update on tenant_billing_assignments
  for each row execute function validate_tenant_billing_assignment_data();

-- New writes must refer to a live tenant. A key-share lock serializes assignment writes with tenant deletion while
-- allowing already-retained assignment rows to outlive their tenant.
create function check_tenant_billing_assignment_tenant_ref() returns trigger as $$
declare
  live_tenant text;
begin
  select t.name into live_tenant
  from tenants t
  where t.name = new.tenant
    and t.deletion_timestamp = 'epoch'::timestamptz
  for key share;

  if not found then
    raise exception using
      errcode = 'Z0002',
      message = format('tenant ''%s'' does not exist or is deleting', new.tenant);
  end if;

  return new;
end;
$$ language plpgsql;

create trigger check_tenant_billing_assignment_tenant_ref
  before insert or update of tenant on tenant_billing_assignments
  for each row execute function check_tenant_billing_assignment_tenant_ref();

create function check_tenant_billing_assignment_account_ref() returns trigger as $$
declare
  account_id text;
  found_id text;
begin
  account_id := new.data->'spec'->'billing_account'->>'id';
  select id into found_id
  from billing_accounts
  where id = account_id
  for share;

  if found_id is null then
    raise exception using
      errcode = 'Z0002',
      message = format('billing account ''%s'' does not exist', account_id);
  end if;

  return new;
end;
$$ language plpgsql;

create trigger check_tenant_billing_assignment_account_ref
  before insert or update on tenant_billing_assignments
  for each row execute function check_tenant_billing_assignment_account_ref();

create function check_billing_account_not_in_use_by_assignments() returns trigger as $$
begin
  if exists (
    select 1
    from tenant_billing_assignments
    where data->'spec'->'billing_account'->>'id' = old.id
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete billing account ''%s'': it is in use by at least one tenant billing assignment',
        old.id
      );
  end if;

  return old;
end;
$$ language plpgsql;

create trigger check_billing_account_not_in_use_by_assignments
  before delete on billing_accounts
  for each row execute function check_billing_account_not_in_use_by_assignments();

create function protect_tenant_billing_assignment_history() returns trigger as $$
begin
  if TG_OP = 'DELETE' then
    raise exception 'billing interval history cannot be deleted' using errcode = '23514';
  end if;

  if old.data->'status'->>'effective_to_timestamp' is not null and new is distinct from old then
    raise exception 'closed billing history is immutable' using errcode = '23514';
  end if;

  if row(new.id, new.name, new.tenant, new.project, new.creation_timestamp, new.creator,
      new.data->'spec'->'billing_account'->>'id',
      project_billing_timestamp(new.data->'status'->>'effective_from_timestamp', true))
    is distinct from row(old.id, old.name, old.tenant, old.project, old.creation_timestamp, old.creator,
      old.data->'spec'->'billing_account'->>'id',
      project_billing_timestamp(old.data->'status'->>'effective_from_timestamp', true))
  then
    raise exception 'immutable billing interval identity' using errcode = '23514';
  end if;

  return new;
end;
$$ language plpgsql;

create trigger protect_tenant_billing_assignment_history
  before update or delete on tenant_billing_assignments
  for each row execute function protect_tenant_billing_assignment_history();

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
