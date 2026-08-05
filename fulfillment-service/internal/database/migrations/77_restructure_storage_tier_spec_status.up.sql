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

-- This migration restructures the StorageTier JSONB data column from flat fields to spec/status sub-objects,
-- aligning with the standard OSAC object structure (id, metadata, spec, status).
--
-- Before: {"description": "...", "backends": [...], "state": 1, ...}
-- After:  {"spec": {"description": "...", "backends": [...]}, "status": {"state": 1}, ...}

-- Update trigger functions to read backends from the new spec.backends path:

create or replace function materialize_storage_tier_backends() returns trigger as $$
declare
  bid text;
begin
  delete from storage_tier_backends where storage_tier_id = new.id;

  for bid in
    select jsonb_array_elements(new.data->'spec'->'backends')->>'backend_id'
  loop
    insert into storage_tier_backends (storage_tier_id, backend_id)
    values (new.id, bid);
  end loop;

  return new;
end;
$$ language plpgsql;

create or replace function check_storage_tier_backend_refs() returns trigger as $$
declare
  bid text;
  found_id text;
begin
  for bid in
    select jsonb_array_elements(new.data->'spec'->'backends')->>'backend_id'
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

-- Backfill existing rows: move flat fields into spec/status sub-objects and re-fire triggers:
update storage_tiers
set data = jsonb_build_object(
  'spec', jsonb_build_object(
    'description', coalesce(data->>'description', ''),
    'backends', coalesce(data->'backends', '[]'::jsonb)
  ),
  'status', jsonb_build_object(
    'state', coalesce((data->>'state')::int, 0)
  )
) || (data - 'description' - 'backends' - 'state');
