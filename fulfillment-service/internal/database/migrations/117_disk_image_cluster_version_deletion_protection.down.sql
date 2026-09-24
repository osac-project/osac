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

-- Revert to the 5-clause version from migration 114 (without cluster_versions).
create or replace function check_disk_image_not_in_use() returns trigger as $$
begin
  if exists (
    select 1 from compute_instances
    where deletion_timestamp = 'epoch'
      and data->'spec'->'disk_image'->>'id' = old.id
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format('cannot delete disk image ''%s'': it is in use by at least one compute instance', old.id);
  end if;

  if exists (
    select 1 from compute_instance_templates
    where deletion_timestamp = 'epoch'
      and data->'spec_defaults'->'disk_image'->>'id' = old.id
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format('cannot delete disk image ''%s'': it is in use by at least one compute instance template', old.id);
  end if;

  if exists (
    select 1 from bare_metal_instances
    where deletion_timestamp = 'epoch'
      and data->'spec'->'disk_image'->>'id' = old.id
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format('cannot delete disk image ''%s'': it is in use by at least one bare metal instance', old.id);
  end if;

  if exists (
    select 1 from compute_instance_catalog_items c
    where c.deletion_timestamp = 'epoch'
      and (
        c.data->'fields'->'disk_image'->'locked'->>'id' = old.id
        or c.data->'fields'->'disk_image'->'editable'->'default_value'->>'id' = old.id
      )
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format('cannot delete disk image ''%s'': it is in use by at least one compute instance catalog item', old.id);
  end if;

  if exists (
    select 1 from bare_metal_instance_catalog_items c
    where c.deletion_timestamp = 'epoch'
      and (
        c.data->'fields'->'disk_image'->'locked'->>'id' = old.id
        or c.data->'fields'->'disk_image'->'editable'->'default_value'->>'id' = old.id
      )
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format('cannot delete disk image ''%s'': it is in use by at least one bare metal instance catalog item', old.id);
  end if;

  return new;
end;
$$ language plpgsql;

-- Drop the index added by the up migration.
drop index if exists cluster_versions_disk_image_id;
