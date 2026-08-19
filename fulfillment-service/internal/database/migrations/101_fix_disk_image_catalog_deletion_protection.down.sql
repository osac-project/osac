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

-- Revert check_disk_image_not_in_use() to its migration 99 definition, with one deliberate
-- exception: the catalog-items clause is kept in the corrected form the up migration introduced.
-- The up migration's field_definitions data normalization is one-way and is NOT reverted here
-- (best-effort down, per convention), so a mig-99-shaped catalog clause (path 'spec.disk_image',
-- default = old.id) would no longer match the on-disk data and would silently lose deletion
-- protection after a rollback. The corrected clause (top-level field_definitions, path
-- 'disk_image', default->>'name' = old.name) stays consistent with the normalized data.
-- The compute_instances and compute_instance_templates clauses are byte-identical to migration 99.
create or replace function check_disk_image_not_in_use() returns trigger as $$
begin
  if exists (
    select 1
    from compute_instances
    where deletion_timestamp = 'epoch'
      and data->'spec'->'disk_image'->>'id' = old.id
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete disk image ''%s'': it is in use by at least one compute instance',
        old.id
      );
  end if;

  if exists (
    select 1
    from compute_instance_templates
    where deletion_timestamp = 'epoch'
      and data->'spec_defaults'->'disk_image'->>'id' = old.id
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete disk image ''%s'': it is in use by at least one compute instance template',
        old.id
      );
  end if;

  if exists (
    select 1
    from compute_instance_catalog_items
    cross join lateral jsonb_array_elements(
      case when jsonb_typeof(compute_instance_catalog_items.data->'field_definitions') = 'array'
        then compute_instance_catalog_items.data->'field_definitions'
        else '[]'::jsonb
      end
    ) as fd
    where compute_instance_catalog_items.deletion_timestamp = 'epoch'
      and fd->>'path' = 'disk_image'
      and fd->'default'->>'name' = old.name
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete disk image ''%s'': it is in use by at least one compute instance catalog item',
        old.id
      );
  end if;

  return new;
end;
$$ language plpgsql;
