-- Copyright (c) 2026 Red Hat Inc.
--
-- Licensed under the Apache License, Version 2.0 (the "License");
-- you may not use this file except in compliance with the License.
-- You may obtain a copy of the License at
--
--   http://www.apache.org/licenses/LICENSE-2.0
--
-- Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
-- "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
-- specific language governing permissions and limitations under the License.

begin;
set local lock_timeout = '5s';
do $$
begin
  lock table tenants, billing_accounts, tenant_billing_assignments, deleted_tenant_identities
    in share row exclusive mode;
exception when lock_not_available then
  raise exception using
    errcode = '55P03',
    message = 'Migration 161 timed out waiting for tenant lifecycle tables. Drain tenant and billing writes before retrying.',
    detail = 'Roll back the failed transaction if the migration session is still open. After confirming rollback, reset the dirty migration version to 160 before retrying.';
end;
$$;

-- Tenant identity validation and deleted-name reservation are tenant-table invariants, not GenericDAO behavior. The
-- advisory key serializes a new INSERT with the tenant server's lifecycle writers before any tenant row is inserted.
create function osac_validate_tenant_insert() returns trigger as $$
begin
  if new.name = '' or new.id is distinct from new.name or new.tenant is distinct from new.name then
    raise exception 'tenant id, name and self-tenant must be nonempty and equal' using errcode = '23514';
  end if;

  perform pg_advisory_xact_lock(hashtextextended('osac/tenant-identity/' || new.name, 0));
  if exists (select 1 from tenants t where t.name = new.name) then
    return new;
  end if;
  if exists (select 1 from deleted_tenant_identities d where d.tenant_name = new.name) then
    raise exception 'tenant identity is retained after deletion' using errcode = 'Z0002';
  end if;
  return new;
end;
$$ language plpgsql;

create trigger validate_tenant_insert
  before insert on tenants
  for each row execute function osac_validate_tenant_insert();

-- The tenant row is already locked by the UPDATE/DELETE. Keep the established order: tenant, current billing accounts
-- in ID order, then all assignment intervals in ID order. Assignment finalization uses the same order.
create function osac_close_tenant_billing_history(
  tenant_name text,
  tenant_created_at timestamptz,
  requested_deletion_at timestamptz
) returns timestamptz as $$
declare
  locked_id text;
  history_floor timestamptz;
  deletion_at timestamptz;
  active_start timestamptz;
  changed bigint;
begin
  for locked_id in
    select a.id
    from billing_accounts a
    where a.id in (
      select h.data->'spec'->'billing_account'->>'id'
      from tenant_billing_assignments h
      where h.tenant = osac_close_tenant_billing_history.tenant_name
        and h.data->'status'->>'effective_to_timestamp' is null
    )
    order by a.id
    for update
  loop
    null;
  end loop;

  for locked_id in
    select h.id
    from tenant_billing_assignments h
    where h.tenant = osac_close_tenant_billing_history.tenant_name
    order by h.id
    for update
  loop
    null;
  end loop;

  select max(greatest((h.data->'status'->>'effective_from_timestamp')::timestamptz,
      (h.data->'status'->>'effective_to_timestamp')::timestamptz))
    into history_floor
    from tenant_billing_assignments h
    where h.tenant = osac_close_tenant_billing_history.tenant_name;

  if requested_deletion_at is null then
    deletion_at := greatest(clock_timestamp(), tenant_created_at, coalesce(history_floor, tenant_created_at));
  else
    deletion_at := requested_deletion_at;
  end if;

  select (h.data->'status'->>'effective_from_timestamp')::timestamptz into active_start
    from tenant_billing_assignments h
    where h.tenant = osac_close_tenant_billing_history.tenant_name
      and h.data->'status'->>'effective_to_timestamp' is null;
  if found and deletion_at < active_start then
    raise exception 'tenant deletion predates the start of its active billing interval'
      using errcode = 'Z0003';
  end if;

  update tenant_billing_assignments h
    set data = jsonb_set(h.data, '{status}', (h.data->'status') || jsonb_build_object(
          'effective_to_timestamp',
          to_char(deletion_at at time zone 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
          'message', 'TENANT_DELETED: tenant deleted; assignment retained as billing history')),
        version = h.version + 1
    where h.tenant = osac_close_tenant_billing_history.tenant_name
      and h.data->'status'->>'effective_to_timestamp' is null;
  get diagnostics changed = row_count;
  if changed > 1 then
    raise exception 'tenant has multiple active billing intervals' using errcode = '23514';
  end if;

  return deletion_at;
end;
$$ language plpgsql;

create function osac_capture_deleted_tenant_identity(
  p_tenant_name text,
  p_tenant_created_at timestamptz,
  p_deletion_at timestamptz,
  p_deleted_by text
) returns void as $$
declare
  retained boolean;
  changed bigint;
begin
  select exists (
    select 1 from tenant_billing_assignments h
    where h.tenant = p_tenant_name
  ) into retained;

  insert into deleted_tenant_identities
    (tenant_name, tenant_created_at, deletion_timestamp, deleted_by, finalized_at, billing_retained)
  values (
    p_tenant_name,
    p_tenant_created_at,
    p_deletion_at,
    nullif(p_deleted_by, ''),
    null,
    retained)
  on conflict (tenant_name) do update set
    deleted_by = coalesce(deleted_tenant_identities.deleted_by, excluded.deleted_by),
    billing_retained = deleted_tenant_identities.billing_retained or excluded.billing_retained
  where deleted_tenant_identities.tenant_created_at = excluded.tenant_created_at
    and deleted_tenant_identities.deletion_timestamp = excluded.deletion_timestamp
    and deleted_tenant_identities.finalized_at is null;
  get diagnostics changed = row_count;
  if changed <> 1 then
    raise exception 'tenant deletion identity conflicts with retained history' using errcode = '23514';
  end if;
end;
$$ language plpgsql;

create function osac_capture_tenant_deletion() returns trigger as $$
declare
  deletion_at timestamptz;
begin
  if old.deletion_timestamp = 'epoch'::timestamptz and new.deletion_timestamp <> 'epoch'::timestamptz then
    deletion_at := osac_close_tenant_billing_history(old.name, old.creation_timestamp, null);
    new.deletion_timestamp := deletion_at;
    perform osac_capture_deleted_tenant_identity(
      old.name, old.creation_timestamp, deletion_at,
      current_setting('osac.tenant_deletion_actor', true));
  end if;
  return new;
end;
$$ language plpgsql;

create trigger capture_tenant_deletion
  before update of deletion_timestamp on tenants
  for each row execute function osac_capture_tenant_deletion();

-- Older deployments may have deletion-marked rows without billing closure evidence. Complete closure at their stored
-- deletion boundary before physical removal; never move that accepted boundary forward or backward.
create function osac_prepare_tenant_finalization() returns trigger as $$
declare
  deletion_at timestamptz;
begin
  if old.deletion_timestamp = 'epoch'::timestamptz then
    -- Direct physical deletion has no earlier soft-delete transition. Preserve the history boundary and a tombstone
    -- before removing the live row; no authenticated actor is inferred for direct database writes.
    deletion_at := osac_close_tenant_billing_history(old.name, old.creation_timestamp, null);
    perform osac_capture_deleted_tenant_identity(
      old.name, old.creation_timestamp, deletion_at,
      current_setting('osac.tenant_deletion_actor', true));
  else
    perform osac_close_tenant_billing_history(old.name, old.creation_timestamp, old.deletion_timestamp);
    perform osac_capture_deleted_tenant_identity(
      old.name, old.creation_timestamp, old.deletion_timestamp,
      current_setting('osac.tenant_deletion_actor', true));
  end if;
  return old;
end;
$$ language plpgsql;

create trigger prepare_tenant_finalization
  before delete on tenants
  for each row execute function osac_prepare_tenant_finalization();

create function osac_finalize_deleted_tenant_identity() returns trigger as $$
declare
  history_floor timestamptz;
  accepted_deletion_at timestamptz;
  v_finalized_at timestamptz;
  changed bigint;
begin
  select d.deletion_timestamp into accepted_deletion_at
    from deleted_tenant_identities d
    where d.tenant_name = old.name
      and d.tenant_created_at = old.creation_timestamp
      and d.finalized_at is null
    for update;
  if not found then
    raise exception 'tenant finalization has no pending deletion identity' using errcode = '23514';
  end if;

  select max(greatest((h.data->'status'->>'effective_from_timestamp')::timestamptz,
      (h.data->'status'->>'effective_to_timestamp')::timestamptz))
    into history_floor
    from tenant_billing_assignments h
    where h.tenant = old.name;
  v_finalized_at := greatest(clock_timestamp(), old.creation_timestamp, accepted_deletion_at,
    coalesce(history_floor, old.creation_timestamp));

  update deleted_tenant_identities d
    set finalized_at = v_finalized_at
    where d.tenant_name = old.name
      and d.tenant_created_at = old.creation_timestamp
      and d.deletion_timestamp = accepted_deletion_at
      and d.finalized_at is null;
  get diagnostics changed = row_count;
  if changed <> 1 then
    raise exception 'tenant finalization has no pending deletion identity' using errcode = '23514';
  end if;
  return old;
end;
$$ language plpgsql;

create trigger finalize_deleted_tenant_identity
  after delete on tenants
  for each row execute function osac_finalize_deleted_tenant_identity();

-- Tenants already deletion-marked when this migration runs never pass through the triggers above. Close their
-- active billing interval at the stored deletion boundary so the account is no longer bound.
select osac_close_tenant_billing_history(t.name, t.creation_timestamp, t.deletion_timestamp)
from tenants t
where t.deletion_timestamp <> 'epoch'::timestamptz;

commit;
