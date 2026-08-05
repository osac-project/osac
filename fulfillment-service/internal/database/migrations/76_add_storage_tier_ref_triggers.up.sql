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

-- This migration adds triggers that enforce bidirectional referential integrity between storage tiers and storage
-- backends:
--
-- 1. A materialized helper table (storage_tier_backends) that mirrors the backend references embedded in the JSONB
--    data column of storage_tiers, enabling efficient reverse lookups.
--
-- 2. An AFTER INSERT/UPDATE trigger on storage_tiers that keeps the helper table in sync with the JSONB data.
--
-- 3. A BEFORE INSERT/UPDATE trigger on storage_tiers that validates each backend reference exists and is active,
--    using FOR SHARE to acquire a shared lock that prevents concurrent soft-deletion of the referenced backend.
--
-- 4. A BEFORE UPDATE trigger on storage_backends that prevents soft-deleting a backend while active storage tiers
--    still reference it.

-- Helper table that mirrors the backend references from the JSONB data column:
create table storage_tier_backends (
  storage_tier_id text not null references storage_tiers(id) on delete cascade,
  backend_id text not null,
  primary key (storage_tier_id, backend_id)
);

create index storage_tier_backends_by_backend on storage_tier_backends (backend_id);

-- Trigger function that materializes backend references from the JSONB data column into the helper table:
create function materialize_storage_tier_backends() returns trigger as $$
declare
  bid text;
begin
  delete from storage_tier_backends where storage_tier_id = new.id;

  for bid in
    select jsonb_array_elements(new.data->'backends')->>'backend_id'
  loop
    insert into storage_tier_backends (storage_tier_id, backend_id)
    values (new.id, bid);
  end loop;

  return new;
end;
$$ language plpgsql;

-- Attach the trigger so that it fires on insert or update of active storage tiers:
create trigger materialize_storage_tier_backends
  after insert or update on storage_tiers
  for each row
  when (new.deletion_timestamp = 'epoch')
  execute function materialize_storage_tier_backends();

-- Trigger function that validates each backend reference in a storage tier:
create function check_storage_tier_backend_refs() returns trigger as $$
declare
  bid text;
  found_id text;
begin
  for bid in
    select jsonb_array_elements(new.data->'backends')->>'backend_id'
  loop
    select id into found_id
    from storage_backends
    where id = bid
      and deletion_timestamp = 'epoch'
    for share;

    if found_id is null then
      raise exception using
        errcode = 'Z0002',
        message = format('StorageBackend ''%s'' does not exist or has been deleted', bid);
    end if;
  end loop;

  return new;
end;
$$ language plpgsql;

-- Attach the trigger so that it fires on insert or update of active storage tiers:
create trigger check_storage_tier_backend_refs
  before insert or update on storage_tiers
  for each row
  when (new.deletion_timestamp = 'epoch')
  execute function check_storage_tier_backend_refs();

-- Trigger function that prevents soft-deleting a storage backend while active storage tiers reference it:
create function check_storage_backend_not_in_use_by_tier() returns trigger as $$
declare
  tier_count bigint;
begin
  select count(*) into tier_count
  from storage_tier_backends stb
  join storage_tiers st on st.id = stb.storage_tier_id
  where stb.backend_id = old.id
    and st.deletion_timestamp = 'epoch';

  if tier_count > 0 then
    raise exception using
      errcode = 'Z0003',
      message = format('cannot delete StorageBackend ''%s'': %s StorageTier(s) still reference it', old.id, tier_count);
  end if;

  return new;
end;
$$ language plpgsql;

-- Attach the trigger so that it only fires when the row transitions from active to soft-deleted:
create trigger check_storage_backend_not_in_use_by_tier
  before update on storage_backends
  for each row
  when (old.deletion_timestamp = 'epoch' and new.deletion_timestamp != 'epoch')
  execute function check_storage_backend_not_in_use_by_tier();

-- Backfill: touch every existing storage tier so the materialization trigger populates the helper table:
update storage_tiers set data = data;
