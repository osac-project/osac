-- Before this migration, both discovery and assignment could establish the local token.
-- Only a committed assignment could have used it for attribution. Keep tokens for all
-- accounts referenced by any assignment interval (including history); release the
-- discovery-only reservations. The first discovery reads the provider values afresh.
alter table billing_accounts disable trigger project_billing_account;
update billing_accounts a
  set data = a.data #- '{status,external_billing_account_id}',
      external_billing_account_id = null,
      version = a.version + 1
  where a.external_billing_account_id is not null
    and not exists (select 1 from tenant_billing_assignments t where t.billing_account_id = a.id);
alter table billing_accounts enable trigger project_billing_account;

create or replace function project_billing_account() returns trigger as $$
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
