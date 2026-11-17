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

-- Forward-only normalization of the actual populated version-155 schema. Metadata columns and private protobuf
-- JSON are authoritative. The old business columns remain only as one-way projections for existing selectors,
-- foreign keys and named unique indexes. In particular, never trim/canonicalize attribution tokens.
-- No enqueue_change triggers: billing objects deliberately remain unpublished.
alter table billing_accounts
  add column name text not null default '',
  add column creation_timestamp timestamptz not null default 'epoch',
  add column deletion_timestamp timestamptz not null default 'epoch',
  add column finalizers text[] not null default '{}',
  add column creator text not null default '',
  add column tenant text not null default 'system',
  add column project ltree not null default ''::ltree,
  add column labels jsonb not null default '{}'::jsonb,
  add column annotations jsonb not null default '{}'::jsonb,
  add column data jsonb,
  add column version integer not null default 0;

alter table tenant_billing_assignments
  add column deletion_timestamp timestamptz not null default 'epoch',
  add column finalizers text[] not null default '{}',
  add column tenant text not null default '',
  add column project ltree not null default ''::ltree,
  add column labels jsonb not null default '{}'::jsonb,
  add column annotations jsonb not null default '{}'::jsonb,
  add column data jsonb,
  add column version integer not null default 0;

-- Reject values that cannot be represented faithfully, rather than inventing states, dates or identities.
do $$
begin
  if exists (select 1 from billing_accounts where id = '' or state not in ('ACTIVE', 'INACTIVE', 'UNKNOWN')
      or status_observed_at < '0001-01-01Z'::timestamptz or status_observed_at >= '10000-01-01Z'::timestamptz)
    or exists (select 1 from tenant_billing_assignments
      where connection_state not in ('PENDING', 'CONNECTED', 'FAILED')
        or effective_to < effective_from
        or effective_from < '0001-01-01Z'::timestamptz or effective_from >= '10000-01-01Z'::timestamptz
        or effective_to < '0001-01-01Z'::timestamptz or effective_to >= '10000-01-01Z'::timestamptz
        or creation_timestamp < '0001-01-01Z'::timestamptz or creation_timestamp >= '10000-01-01Z'::timestamptz)
  then
    raise exception 'unrepresentable legacy billing object' using errcode = '23514';
  end if;
end;
$$;

-- Accounts had no creation/creator evidence. Epoch and empty creator mean unknown; observation is not creation.
-- PostgreSQL's built-in sha256(bytea) hashes UTF-8 bytes, matching Go's sha256.Sum256([]byte(id)).
update billing_accounts set
  name = 'ba-' || left(encode(sha256(convert_to(id, 'UTF8')), 'hex'), 60),
  data = jsonb_build_object('spec', '{}'::jsonb, 'status',
    jsonb_build_object(
      'provider_key', provider_key,
      'provider_account_reference', provider_account_reference,
      'display_name', display_name,
      'base_currency', base_currency,
      'state', 'BILLING_ACCOUNT_STATE_' || state,
      'state_observation_timestamp', to_char(status_observed_at at time zone 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'))
    || case when external_billing_account_id is null then '{}'::jsonb
       else jsonb_build_object('external_billing_account_id', external_billing_account_id) end);

-- Assignment creation_timestamp and creator ARE historical evidence in 155: keep them verbatim.
update tenant_billing_assignments a set
  tenant = tenant_id,
  annotations = jsonb_build_object('osac.openshift.io/tenant', tenant_id),
  data = jsonb_build_object(
    'spec', jsonb_build_object(
      'tenant', jsonb_build_object('id', tenant_id, 'name', tenant_id),
      'billing_account', jsonb_build_object('id', billing_account_id,
        'name', (select b.name from billing_accounts b where b.id = a.billing_account_id))),
    'status', jsonb_build_object(
      'connection_state', 'TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_' || connection_state,
      'effective_from_timestamp', to_char(effective_from at time zone 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
      'message', message)
    || case when effective_to is null then '{}'::jsonb else jsonb_build_object(
      'effective_to_timestamp', to_char(effective_to at time zone 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"')) end);

alter table billing_accounts
  alter column data set not null,
  alter column creation_timestamp set default now(),
  alter column creator set default 'system',
  -- Projections must be distinguishable from values explicitly supplied to INSERT. The BEFORE trigger fills them.
  alter column display_name drop default,
  alter column base_currency drop default,
  alter column state drop default,
  alter column status_observed_at drop default,
  add constraint billing_accounts_nonempty_id check (id <> ''),
  add constraint billing_accounts_stable_name check (
    name = 'ba-' || left(encode(sha256(convert_to(id, 'UTF8')), 'hex'), 60)),
  add constraint billing_accounts_system_scope check (tenant = 'system' and project = ''::ltree),
  add constraint billing_accounts_tenant_fk foreign key (tenant) references tenants (name);

alter table tenant_billing_assignments
  alter column data set not null,
  alter column effective_from drop default,
  alter column connection_state drop default,
  alter column message drop default,
  add constraint tenant_billing_assignments_nonempty_id check (id <> ''),
  add constraint tenant_billing_assignments_no_project check (project = ''::ltree),
  add constraint tenant_billing_assignments_valid_interval check (effective_to >= effective_from),
  add constraint tenant_billing_assignments_canonical_owner check (
    tenant = tenant_id and annotations->>'osac.openshift.io/tenant' is not distinct from tenant_id);

-- Hash collisions fail, never silently rename. Assignment names, unlike account names, are NOT lifetime unique:
-- reassignment deliberately creates another interval with the same name, including in the lifecycle-active set.
create unique index billing_accounts_unique_name on billing_accounts (name);
create index tenant_billing_assignments_by_name on tenant_billing_assignments (name);

create function project_billing_account() returns trigger as $$
declare
  s jsonb := new.data->'status';
  p billing_accounts%rowtype;
  field text;
begin
  if jsonb_typeof(new.data) is distinct from 'object' or new.data - 'spec' - 'status' <> '{}'::jsonb
    or (new.data ? 'spec' and new.data->'spec' <> '{}'::jsonb)
    or jsonb_typeof(s) is distinct from 'object'
    or s - array['provider_key', 'provider_account_reference', 'display_name', 'base_currency', 'state',
      'state_observation_timestamp', 'external_billing_account_id'] <> '{}'::jsonb
  then
    raise exception 'invalid billing account payload' using errcode = '23514';
  end if;
  foreach field in array array['provider_key', 'provider_account_reference', 'display_name', 'base_currency',
    'state', 'state_observation_timestamp', 'external_billing_account_id'] loop
    if s ? field and jsonb_typeof(s->field) is distinct from 'string' then
      raise exception 'invalid billing account field type' using errcode = '23514';
    end if;
  end loop;
  p.provider_key := coalesce(s->>'provider_key', '');
  p.provider_account_reference := coalesce(s->>'provider_account_reference', '');
  p.display_name := coalesce(s->>'display_name', '');
  p.base_currency := coalesce(s->>'base_currency', '');
  p.external_billing_account_id := s->>'external_billing_account_id';
  p.state := case coalesce(s->>'state', 'BILLING_ACCOUNT_STATE_UNSPECIFIED')
    when 'BILLING_ACCOUNT_STATE_UNSPECIFIED' then 'UNSPECIFIED'
    when 'BILLING_ACCOUNT_STATE_ACTIVE' then 'ACTIVE'
    when 'BILLING_ACCOUNT_STATE_INACTIVE' then 'INACTIVE'
    when 'BILLING_ACCOUNT_STATE_UNKNOWN' then 'UNKNOWN' end;
  p.status_observed_at := (s->>'state_observation_timestamp')::timestamptz;
  if p.provider_key = '' or p.provider_account_reference = '' or p.state is null
    or p.status_observed_at is null or p.status_observed_at < '0001-01-01Z'::timestamptz
    or p.status_observed_at >= '10000-01-01Z'::timestamptz
    or (p.base_currency <> '' and p.base_currency !~ '^[A-Z]{3}$')
  then
    raise exception 'invalid billing account status' using errcode = '23514';
  end if;
  if TG_OP = 'UPDATE' then
    if row(new.id, new.name, new.tenant, new.project, new.creation_timestamp, new.creator,
        p.provider_key, p.provider_account_reference)
      is distinct from row(old.id, old.name, old.tenant, old.project, old.creation_timestamp, old.creator,
        old.provider_key, old.provider_account_reference)
    then
      raise exception 'immutable billing account identity' using errcode = '23514';
    end if;
    -- Whitespace is only used to detect an established token; its stored value is NEVER stripped or rewritten.
    if old.external_billing_account_id ~ '[^[:space:]]'
      and p.external_billing_account_id is distinct from old.external_billing_account_id
    then
      raise exception 'established billing attribution is immutable' using errcode = '23514';
    end if;
    if row(new.provider_key, new.provider_account_reference, new.display_name, new.base_currency,
        new.external_billing_account_id, new.state, new.status_observed_at)
        is distinct from row(old.provider_key, old.provider_account_reference, old.display_name, old.base_currency,
          old.external_billing_account_id, old.state, old.status_observed_at)
      and row(new.provider_key, new.provider_account_reference, new.display_name, new.base_currency,
        new.external_billing_account_id, new.state, new.status_observed_at)
        is distinct from row(p.provider_key, p.provider_account_reference, p.display_name, p.base_currency,
          p.external_billing_account_id, p.state, p.status_observed_at)
    then
      raise exception 'billing account projections are read only' using errcode = '23514';
    end if;
  else
    if new.creator <> 'system'
      or (new.provider_key is not null and new.provider_key is distinct from p.provider_key)
      or (new.provider_account_reference is not null and new.provider_account_reference is distinct from p.provider_account_reference)
      or (new.display_name is not null and new.display_name is distinct from p.display_name)
      or (new.base_currency is not null and new.base_currency is distinct from p.base_currency)
      or (new.external_billing_account_id is not null and new.external_billing_account_id is distinct from p.external_billing_account_id)
      or (new.state is not null and new.state is distinct from p.state)
      or (new.status_observed_at is not null and new.status_observed_at is distinct from p.status_observed_at)
    then
      raise exception 'inconsistent billing account insert' using errcode = '23514';
    end if;
  end if;
  new.provider_key := p.provider_key;
  new.provider_account_reference := p.provider_account_reference;
  new.display_name := p.display_name;
  new.base_currency := p.base_currency;
  new.external_billing_account_id := p.external_billing_account_id;
  new.state := p.state;
  new.status_observed_at := p.status_observed_at;
  return new;
end;
$$ language plpgsql;

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
  p.effective_from := (s->>'effective_from_timestamp')::timestamptz;
  p.effective_to := (s->>'effective_to_timestamp')::timestamptz;
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
    or p.effective_from < '0001-01-01Z'::timestamptz or p.effective_from >= '10000-01-01Z'::timestamptz
    or p.effective_to < '0001-01-01Z'::timestamptz or p.effective_to >= '10000-01-01Z'::timestamptz
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

create trigger project_billing_account before insert or update on billing_accounts
  for each row execute function project_billing_account();
create trigger project_tenant_billing_assignment before insert or update or delete on tenant_billing_assignments
  for each row execute function project_tenant_billing_assignment();

-- Exercise the same payload/type/tuple validation on every backfilled row before the migration commits.
update billing_accounts set data = data;
update tenant_billing_assignments set data = data;

-- Ordinary lifecycle companions, NOT a second interval-history store. Closed intervals stay in the main table.
create table active_billing_accounts (
  id text not null primary key references billing_accounts (id) on delete cascade
);
create table active_tenant_billing_assignments (
  id text not null primary key references tenant_billing_assignments (id) on delete cascade
);
insert into active_billing_accounts select id from billing_accounts where deletion_timestamp = 'epoch';
insert into active_tenant_billing_assignments select id from tenant_billing_assignments where deletion_timestamp = 'epoch';
create trigger materialize_active_objects after insert or update of deletion_timestamp on billing_accounts
  for each row execute function materialize_active_objects();
create trigger materialize_active_objects after insert or update of deletion_timestamp on tenant_billing_assignments
  for each row execute function materialize_active_objects();

create table archived_billing_accounts (
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
