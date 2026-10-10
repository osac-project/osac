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

-- ComputeInstanceSpec.additional_disks is now a message that stores its list in `items`.
-- Wrap existing arrays so active and archived resources can still be read as protobuf JSON.
update compute_instances
set data = jsonb_set(data, '{spec,additional_disks}', jsonb_build_object('items', data->'spec'->'additional_disks'), false)
where jsonb_typeof(data->'spec'->'additional_disks') = 'array';

update archived_compute_instances
set data = jsonb_set(data, '{spec,additional_disks}', jsonb_build_object('items', data->'spec'->'additional_disks'), false)
where jsonb_typeof(data->'spec'->'additional_disks') = 'array';

-- Keep storage-tier reverse references aligned with the wrapped field shape.
create or replace function check_storage_tier_not_in_use() returns trigger as $$
begin
  if exists (
    select 1 from compute_instances
    where deletion_timestamp = 'epoch'
      and data->'spec'->'boot_disk'->'storage_tier'->>'id' = old.id
  ) or exists (
    select 1
    from compute_instances c
    cross join lateral jsonb_array_elements(c.data->'spec'->'additional_disks'->'items') disk
    where c.deletion_timestamp = 'epoch'
      and disk->'storage_tier'->>'id' = old.id
  ) or exists (
    select 1 from compute_instance_templates
    where deletion_timestamp = 'epoch'
      and data->'spec_defaults'->'boot_disk'->'storage_tier'->>'id' = old.id
  ) or exists (
    select 1 from compute_instance_templates t
    cross join lateral jsonb_array_elements(t.data->'spec_defaults'->'additional_disks') disk
    where t.deletion_timestamp = 'epoch' and disk->'storage_tier'->>'id' = old.id
  ) or exists (
    select 1 from compute_instance_catalog_items c
    where c.deletion_timestamp = 'epoch' and (
      c.data->'fields'->'boot_disk'->'storage_tier'->'locked'->>'id' = old.id
      or c.data->'fields'->'boot_disk'->'storage_tier'->'editable'->'default_value'->>'id' = old.id
    )
  ) or exists (
    select 1 from compute_instance_catalog_items c
    cross join lateral jsonb_array_elements(
      coalesce(c.data->'fields'->'additional_disks'->'locked'->'items', '[]'::jsonb)
      || coalesce(c.data->'fields'->'additional_disks'->'editable'->'default_value'->'items', '[]'::jsonb)
    ) disk
    where c.deletion_timestamp = 'epoch' and disk->'storage_tier'->>'id' = old.id
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format('cannot delete storage tier ''%s'': it is in use by an active resource or catalog policy', old.id);
  end if;

  return new;
end;
$$ language plpgsql;
