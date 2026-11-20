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

-- Create the table for discovered billing accounts. A billing account is a provider-global read model with the
-- ordinary metadata columns and the private protobuf JSON body in 'data'. The provider observation columns are a
-- one-way projection of that body, maintained by the 'project_billing_account' trigger: they back the existing
-- indexes and selectors and are never a second writable model.
--
-- Accounts are not tenant-scoped resources: their metadata tenant is fixed to 'system' and their project is empty.
-- Spec is empty; provider observations live in Status. They deliberately publish no changes, so this migration
-- installs no enqueue trigger.
--
-- Adding the tenant foreign key takes a lock on the pre-existing 'tenants' table, so the wait is bounded instead of
-- stalling a rolling upgrade behind tenant writers.
begin;
set local lock_timeout = '5s';

create table billing_accounts (
  id text not null primary key,
  -- Provider-native account id used for backend calls. Never returned by any API.
  provider_key text not null check (provider_key <> ''),
  provider_account_reference text not null check (provider_account_reference <> ''),
  display_name text not null,
  -- ISO-4217 currency, empty when the provider does not supply one (e.g. M360).
  base_currency text not null,
  -- OSAC's provider-agnostic attribution id. Nullable until discovery or assignment-time resolution sets one.
  external_billing_account_id text,
  -- Normalized state: ACTIVE, INACTIVE or UNKNOWN. Never the provider's raw status text.
  state text not null,
  status_observed_at timestamp with time zone not null,
  name text not null default '',
  creation_timestamp timestamp with time zone not null default now(),
  deletion_timestamp timestamp with time zone not null default 'epoch',
  finalizers text[] not null default '{}',
  creator text not null default 'system',
  tenant text not null default 'system',
  project ltree not null default ''::ltree,
  labels jsonb not null default '{}'::jsonb,
  annotations jsonb not null default '{}'::jsonb,
  data jsonb not null,
  version integer not null default 0,
  constraint billing_accounts_nonempty_id check (id <> ''),
  -- Names are derived from the immutable id, so they are stable for the life of the account.
  constraint billing_accounts_stable_name check (
    name = 'ba-' || left(encode(sha256(convert_to(id, 'UTF8')), 'hex'), 60)),
  constraint billing_accounts_system_scope check (tenant = 'system' and project = ''::ltree),
  constraint billing_accounts_tenant_fk foreign key (tenant) references tenants (name)
);

-- One row per provider account: the provider key and native account reference identify exactly one row.
create unique index billing_accounts_unique_provider_account
  on billing_accounts (provider_key, provider_account_reference);

-- At most one account may hold a given attribution id, backing the ACCOUNT_AMBIGUOUS check. Partial so multiple
-- NULLs (not yet established) are allowed.
create unique index billing_accounts_unique_external_billing_account_id
  on billing_accounts (external_billing_account_id)
  where external_billing_account_id is not null;

create unique index billing_accounts_unique_name on billing_accounts (name);

-- Validate protobuf timestamp spellings before PostgreSQL can normalize alternate forms (including 24:00 and leap
-- seconds). Shared with the assignment interval projection.
create function project_billing_timestamp(value text, require_exact boolean) returns timestamptz as $$
begin
  if value collate "C" !~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}T([01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9]([.]([0-9]{3}|[0-9]{6}|[0-9]{9}))?Z$'
    or left(value, 4) = '0000'
  then
    raise exception 'invalid protobuf billing timestamp' using errcode = '23514';
  end if;
  if length(value) = 30 then
    if require_exact and substring(value from 27 for 3) <> '000' then
      raise exception 'billing interval endpoint is not representable at microsecond precision' using errcode = '23514';
    end if;
    -- Convert at most six fractional digits, so even the last nanosecond of year 9999 cannot round up.
    value := left(value, 26) || 'Z';
  end if;
  -- The cast checks the calendar date after lexical and precision validation.
  return value::timestamptz;
exception
  when invalid_datetime_format or datetime_field_overflow then
    raise exception 'invalid protobuf billing timestamp' using errcode = '23514';
end;
$$ language plpgsql strict;

-- Derive every provider observation column from the authoritative payload. 'provider_external_id' is an
-- observation-only field: it is accepted in Status and validated, but it has no projection column.
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
      'state_observation_timestamp', 'external_billing_account_id', 'provider_external_id'] <> '{}'::jsonb
  then
    raise exception 'invalid billing account payload' using errcode = '23514';
  end if;
  foreach field in array array['provider_key', 'provider_account_reference', 'display_name', 'base_currency',
    'state', 'state_observation_timestamp', 'external_billing_account_id', 'provider_external_id'] loop
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
  -- Observation freshness uses the exact nanoseconds in data; this SQL mirror truncates to microseconds.
  p.status_observed_at := project_billing_timestamp(s->>'state_observation_timestamp', false);
  if p.provider_key = '' or p.provider_account_reference = '' or p.state is null
    or p.status_observed_at is null
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

create trigger project_billing_account before insert or update on billing_accounts
  for each row execute function project_billing_account();

-- Ordinary lifecycle companions, not extra storage: the archive table keeps the row evidence on deletion and the
-- active table is the cheap liveness index used by the generic DAO.
create table active_billing_accounts (
  id text not null primary key references billing_accounts (id) on delete cascade
);

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

create trigger materialize_active_objects after insert or update of deletion_timestamp on billing_accounts
  for each row execute function materialize_active_objects();

commit;
