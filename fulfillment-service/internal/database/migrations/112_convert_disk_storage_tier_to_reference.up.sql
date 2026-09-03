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

-- This migration converts the storage_tier field on ComputeInstanceDisk from a bare string (tier name)
-- to a typed reference object {"id": "...", "name": "..."}, matching the StorageTierReference proto message.
--
-- Affected paths:
--   spec.boot_disk.storage_tier
--   spec.additional_disks[*].storage_tier
--
-- For each bare string value, we join against the storage_tiers table to resolve name → id.
-- Rows where storage_tier is already an object or is null are skipped.

-- =============================================================================================================
-- PART 1: compute_instances — boot_disk.storage_tier
-- =============================================================================================================

update compute_instances set data = jsonb_set(
  data, '{spec,boot_disk,storage_tier}',
  coalesce(
    (select jsonb_build_object('id', st.id, 'name', st.name)
     from storage_tiers st
     where st.name = data->'spec'->'boot_disk'->>'storage_tier'
       and st.deletion_timestamp = 'epoch'
     limit 1),
    jsonb_build_object('name', data->'spec'->'boot_disk'->>'storage_tier')
  )
) where data->'spec'->'boot_disk'->>'storage_tier' is not null
  and jsonb_typeof(data->'spec'->'boot_disk'->'storage_tier') = 'string';

-- =============================================================================================================
-- PART 2: compute_instances — additional_disks[*].storage_tier
-- =============================================================================================================

update compute_instances set data = jsonb_set(
  data, '{spec,additional_disks}',
  (
    select coalesce(jsonb_agg(
      case
        when jsonb_typeof(elem->'storage_tier') = 'string' then
          jsonb_set(elem, '{storage_tier}',
            coalesce(
              (select jsonb_build_object('id', st.id, 'name', st.name)
               from storage_tiers st
               where st.name = elem->>'storage_tier'
                 and st.deletion_timestamp = 'epoch'
               limit 1),
              jsonb_build_object('name', elem->>'storage_tier')
            )
          )
        else elem
      end
    order by disk_index), '[]'::jsonb)
    from jsonb_array_elements(data->'spec'->'additional_disks') with ordinality as t(elem, disk_index)
  )
) where data->'spec'->'additional_disks' is not null
  and jsonb_typeof(data->'spec'->'additional_disks') = 'array'
  and jsonb_array_length(data->'spec'->'additional_disks') > 0
  and exists (
    select 1 from jsonb_array_elements(data->'spec'->'additional_disks') as elem
    where jsonb_typeof(elem->'storage_tier') = 'string'
  );

-- =============================================================================================================
-- PART 3: archived_compute_instances — boot_disk.storage_tier
-- =============================================================================================================

update archived_compute_instances set data = jsonb_set(
  data, '{spec,boot_disk,storage_tier}',
  coalesce(
    (select jsonb_build_object('id', st.id, 'name', st.name)
     from storage_tiers st
     where st.name = data->'spec'->'boot_disk'->>'storage_tier'
       and st.deletion_timestamp = 'epoch'
     limit 1),
    jsonb_build_object('name', data->'spec'->'boot_disk'->>'storage_tier')
  )
) where data->'spec'->'boot_disk'->>'storage_tier' is not null
  and jsonb_typeof(data->'spec'->'boot_disk'->'storage_tier') = 'string';

-- =============================================================================================================
-- PART 4: archived_compute_instances — additional_disks[*].storage_tier
-- =============================================================================================================

update archived_compute_instances set data = jsonb_set(
  data, '{spec,additional_disks}',
  (
    select coalesce(jsonb_agg(
      case
        when jsonb_typeof(elem->'storage_tier') = 'string' then
          jsonb_set(elem, '{storage_tier}',
            coalesce(
              (select jsonb_build_object('id', st.id, 'name', st.name)
               from storage_tiers st
               where st.name = elem->>'storage_tier'
                 and st.deletion_timestamp = 'epoch'
               limit 1),
              jsonb_build_object('name', elem->>'storage_tier')
            )
          )
        else elem
      end
    order by disk_index), '[]'::jsonb)
    from jsonb_array_elements(data->'spec'->'additional_disks') with ordinality as t(elem, disk_index)
  )
) where data->'spec'->'additional_disks' is not null
  and jsonb_typeof(data->'spec'->'additional_disks') = 'array'
  and jsonb_array_length(data->'spec'->'additional_disks') > 0
  and exists (
    select 1 from jsonb_array_elements(data->'spec'->'additional_disks') as elem
    where jsonb_typeof(elem->'storage_tier') = 'string'
  );
