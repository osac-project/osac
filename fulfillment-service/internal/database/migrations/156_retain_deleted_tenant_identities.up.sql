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

-- Create the credential-free store that reserves deleted tenant names and records who deleted them. Deploy it
-- together with its writers while tenant writes are quiesced: the lock blocks writers on both source tables for the
-- validation and backfill below.
begin;
set local lock_timeout = '5s';
do $$
begin
  lock table tenants, archived_tenants in share row exclusive mode;
exception when lock_not_available then
  raise exception using
    errcode = '55P03',
    message = 'Migration 156 timed out waiting for a SHARE ROW EXCLUSIVE lock. Drain tenant writers of'
      ' tenants and archived_tenants before retrying the upgrade.',
    detail = 'Roll back the failed transaction if the migration session is still open. After confirming rollback,'
      ' reset the dirty migration version to 155 before retrying.';
end;
$$;

create table deleted_tenant_identities (
  tenant_name text primary key check (tenant_name <> ''),
  tenant_created_at timestamptz not null,
  deletion_timestamp timestamptz not null,
  deleted_by text null,
  finalized_at timestamptz null,
  billing_retained boolean not null,
  constraint deleted_tenant_identities_chronology check (
    tenant_created_at >= '0001-01-01Z'::timestamptz and tenant_created_at < '10000-01-01Z'::timestamptz
    and tenant_created_at <> 'epoch'::timestamptz
    and deletion_timestamp >= '0001-01-01Z'::timestamptz and deletion_timestamp < '10000-01-01Z'::timestamptz
    and deletion_timestamp <> 'epoch'::timestamptz and tenant_created_at <= deletion_timestamp
    and (finalized_at is null or (finalized_at >= deletion_timestamp and finalized_at < '10000-01-01Z'::timestamptz)))
);

-- Refuse to backfill from evidence that cannot be represented faithfully, rather than inventing identities, dates
-- or incarnations.
do $$
declare
  discrepancies bigint;
begin
  if exists (select 1 from tenants where name = '' or id <> name or tenant <> name
      or creation_timestamp < '0001-01-01Z'::timestamptz or creation_timestamp >= '10000-01-01Z'::timestamptz
      or creation_timestamp = 'epoch'::timestamptz
      or deletion_timestamp < '0001-01-01Z'::timestamptz or deletion_timestamp >= '10000-01-01Z'::timestamptz
      or (deletion_timestamp <> 'epoch'::timestamptz and deletion_timestamp < creation_timestamp))
    or exists (select 1 from archived_tenants where name = ''
      or creation_timestamp < '0001-01-01Z'::timestamptz or creation_timestamp >= '10000-01-01Z'::timestamptz
      or creation_timestamp = 'epoch'::timestamptz
      or deletion_timestamp < creation_timestamp or deletion_timestamp = 'epoch'::timestamptz
      or archival_timestamp < deletion_timestamp or archival_timestamp >= '10000-01-01Z'::timestamptz)
  then
    raise exception 'invalid recorded tenant identity chronology; audit required' using errcode = '23514';
  end if;

  if exists (select 1 from (
      select creation_timestamp, lag(archival_timestamp) over
        (partition by name order by creation_timestamp, deletion_timestamp, archival_timestamp) previous_finalized
      from archived_tenants) a where creation_timestamp <= previous_finalized)
    or exists (select 1 from tenants t join archived_tenants a on a.name = t.name
      where t.creation_timestamp <= a.archival_timestamp)
  then
    raise exception 'ambiguous or overlapping tenant incarnations; audit required' using errcode = '23514';
  end if;

  -- Old archive ids/self-tenants predate canonical live identity rules. Keep the original revocation name,
  -- report only a count (never payload or identifiers), and do not silently normalize that evidence.
  select count(*) into discrepancies from archived_tenants where id <> name or tenant <> name;
  if discrepancies > 0 then
    raise warning '% legacy archived tenant identity discrepancies retained without normalization', discrepancies;
  end if;
end;
$$;

-- Strict ordering was proved above; there is exactly one latest archive per name, with no arbitrary tie winner.
-- archival_timestamp is recorded transaction-start evidence, NOT a newly measured post-DELETE wall clock.
insert into deleted_tenant_identities
  (tenant_name, tenant_created_at, deletion_timestamp, deleted_by, finalized_at, billing_retained)
select a.name, a.creation_timestamp, a.deletion_timestamp, null, a.archival_timestamp, false
from archived_tenants a where not exists
  (select 1 from archived_tenants later where later.name = a.name and later.creation_timestamp > a.creation_timestamp);

-- Physical presence wins over an older archive. Even an empty finalizer array is not proof of physical removal.
insert into deleted_tenant_identities
  (tenant_name, tenant_created_at, deletion_timestamp, deleted_by, finalized_at, billing_retained)
select t.name, t.creation_timestamp, t.deletion_timestamp, null, null,
  exists (select 1 from tenant_billing_assignments h where h.tenant_id = t.name)
from tenants t where t.deletion_timestamp <> 'epoch'::timestamptz
on conflict (tenant_name) do update set
  tenant_created_at = excluded.tenant_created_at,
  deletion_timestamp = excluded.deletion_timestamp,
  deleted_by = excluded.deleted_by,
  finalized_at = excluded.finalized_at,
  billing_retained = excluded.billing_retained;

commit;
