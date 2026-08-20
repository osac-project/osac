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

-- Removes StorageTier's "protocol" and "quota_gib" fields from each backend association. "protocol" is
-- replaced by a new top-level StorageTierSpec.protocol field, but no backfill is performed: no real,
-- persistent OSAC deployment has StorageTier data today, so pre-existing rows simply read back with
-- protocol unset. Also restructures any pre-migration-77 archived_storage_tiers rows still in the flat
-- shape into spec/status, since migration 77 never touched that table.
--
-- Before: {"spec":{"backends":[{"backend_id":"...","protocol":"STORAGE_PROTOCOL_NFS","quota_gib":500,...}]}}
-- After:  {"spec":{"backends":[{"backend_id":"...",...}]}}

update storage_tiers
set data = jsonb_set(
  data,
  '{spec,backends}',
  (
    select coalesce(jsonb_agg(backend - 'protocol' - 'quota_gib'), '[]'::jsonb)
    from jsonb_array_elements(coalesce(data->'spec'->'backends', '[]'::jsonb)) as backend
  )
);

update archived_storage_tiers
set data = case
  when data ? 'spec' then
    jsonb_set(
      data,
      '{spec,backends}',
      (
        select coalesce(jsonb_agg(backend - 'protocol' - 'quota_gib'), '[]'::jsonb)
        from jsonb_array_elements(coalesce(data->'spec'->'backends', '[]'::jsonb)) as backend
      )
    )
  else
    jsonb_build_object(
      'spec', jsonb_build_object(
        'description', coalesce(data->>'description', ''),
        'backends', (
          select coalesce(jsonb_agg(backend - 'protocol' - 'quota_gib'), '[]'::jsonb)
          from jsonb_array_elements(coalesce(data->'backends', '[]'::jsonb)) as backend
        )
      ),
      -- "state" is a string-serialized enum for non-zero values -- never cast to int.
      'status', jsonb_build_object(
        'state', coalesce(data->'state', '0'::jsonb)
      )
    ) || (data - 'description' - 'backends' - 'state')
end;
