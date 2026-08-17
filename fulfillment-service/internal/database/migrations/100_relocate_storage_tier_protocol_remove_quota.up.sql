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

-- This migration relocates StorageTier's "protocol" field from each backend association up to the tier's spec,
-- and removes "quota_gib" entirely.
--
-- Before: {"spec":{"backends":[{"backend_id":"...","protocol":"STORAGE_PROTOCOL_NFS","quota_gib":500,...}]}}
-- After:  {"spec":{"protocol":"STORAGE_PROTOCOL_NFS","backends":[{"backend_id":"...",...}]}}
--
-- The "protocol" value is a JSON string holding the proto enum name (e.g. "STORAGE_PROTOCOL_NFS") when set, or
-- absent/JSON null for the zero value -- never a bare integer -- because the DAO's custom JSON encoder writes
-- non-zero enum values as their proto name string. The same is true of the legacy flat shape's "state" field
-- below. This migration only ever relocates these values as-is; it never casts either to a SQL scalar type.
--
-- storage_tiers was already fully normalized to the spec/status shape by migration 77, so every row here has a
-- "spec" object. archived_storage_tiers shares the same "data jsonb" column (created alongside storage_tiers in
-- migration 75) but migration 77 only ever updated storage_tiers, so tiers archived before migration 77 shipped
-- are still in that migration's pre-restructure flat shape -- this migration applies migration 77's spec/status
-- restructuring and this migration's protocol relocation together, in the same pass, for those rows.

update storage_tiers
set data = jsonb_set(
  data,
  '{spec}',
  ((data->'spec') - 'backends') || jsonb_build_object(
    'protocol', coalesce(data->'spec'->'backends'->0->'protocol', 'null'::jsonb),
    'backends', (
      select coalesce(jsonb_agg(backend - 'protocol' - 'quota_gib'), '[]'::jsonb)
      from jsonb_array_elements(coalesce(data->'spec'->'backends', '[]'::jsonb)) as backend
    )
  )
);

update archived_storage_tiers
set data = case
  when data ? 'spec' then
    jsonb_set(
      data,
      '{spec}',
      ((data->'spec') - 'backends') || jsonb_build_object(
        'protocol', coalesce(data->'spec'->'backends'->0->'protocol', 'null'::jsonb),
        'backends', (
          select coalesce(jsonb_agg(backend - 'protocol' - 'quota_gib'), '[]'::jsonb)
          from jsonb_array_elements(coalesce(data->'spec'->'backends', '[]'::jsonb)) as backend
        )
      )
    )
  else
    jsonb_build_object(
      'spec', jsonb_build_object(
        'description', coalesce(data->>'description', ''),
        'protocol', coalesce(data->'backends'->0->'protocol', 'null'::jsonb),
        'backends', (
          select coalesce(jsonb_agg(backend - 'protocol' - 'quota_gib'), '[]'::jsonb)
          from jsonb_array_elements(coalesce(data->'backends', '[]'::jsonb)) as backend
        )
      ),
      'status', jsonb_build_object(
        'state', coalesce(data->'state', '0'::jsonb)
      )
    ) || (data - 'description' - 'backends' - 'state')
end;
