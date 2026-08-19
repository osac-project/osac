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

-- Fix DiskImage deletion protection for compute instance catalog items.
--
-- Migration 99's check_disk_image_not_in_use() catalog-items clause never matched a real catalog
-- item, on three counts:
--   1. It scanned data->'spec'->'field_definitions', but field_definitions is a top-level field of
--      ComputeInstanceCatalogItem, so real data lives at data->'field_definitions'.
--   2. It matched the path string 'spec.disk_image', but the stored, apply- and UI-facing
--      convention is the prefix-less, spec-relative 'disk_image'.
--   3. It compared the stored default against old.id, but the default holds the disk image name,
--      not its id.
-- As a result a DiskImage referenced by a functional catalog item could be soft-deleted.
--
-- This migration:
--   1. Replaces the function so the catalog-items clause mirrors the cluster version reference
--      clause (migration 94): scan the top-level field_definitions array, match path 'disk_image',
--      and match the reference object default's name (fd->'default'->>'name') against the disk image
--      name. The compute_instances and compute_instance_templates clauses are unchanged from
--      migration 99 (they correctly read spec/spec_defaults and match on ->>'id').
--   2. Normalizes existing compute_instance_catalog_items field_definitions to the settled shape
--      (path 'disk_image', default {"name": <name>}), following the version conversion in
--      migration 93.

-- 1. Replace the trigger function. The trigger wiring from migration 99 is unchanged.
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

-- 2. Normalize existing catalog-item field_definitions to path 'disk_image' with a {"name": ...}
--    reference-object default, following the version conversion in migration 93. Handles both the
--    prefix-less 'disk_image' and the legacy 'spec.disk_image' path, and only rewrites string
--    defaults (already-object defaults are left untouched).
update compute_instance_catalog_items
set data = jsonb_set(data, '{field_definitions}',
  (select coalesce(jsonb_agg(
    case
      when fd->>'path' in ('disk_image', 'spec.disk_image') then
        case
          when fd->'default' is not null and jsonb_typeof(fd->'default') = 'string' then
            jsonb_set(
              jsonb_set(fd, '{path}', '"disk_image"'),
              '{default}',
              jsonb_build_object('name', fd->>'default')
            )
          else
            jsonb_set(fd, '{path}', '"disk_image"')
        end
      else fd
    end
  ), '[]'::jsonb)
  from jsonb_array_elements(data->'field_definitions') as fd)
)
where data->'field_definitions' is not null
  and exists (
    select 1 from jsonb_array_elements(data->'field_definitions') as fd
    where fd->>'path' in ('disk_image', 'spec.disk_image')
  );

-- Archived catalog items: same conversion, for data consistency (mirrors migration 93).
update archived_compute_instance_catalog_items
set data = jsonb_set(data, '{field_definitions}',
  (select coalesce(jsonb_agg(
    case
      when fd->>'path' in ('disk_image', 'spec.disk_image') then
        case
          when fd->'default' is not null and jsonb_typeof(fd->'default') = 'string' then
            jsonb_set(
              jsonb_set(fd, '{path}', '"disk_image"'),
              '{default}',
              jsonb_build_object('name', fd->>'default')
            )
          else
            jsonb_set(fd, '{path}', '"disk_image"')
        end
      else fd
    end
  ), '[]'::jsonb)
  from jsonb_array_elements(data->'field_definitions') as fd)
)
where data->'field_definitions' is not null
  and exists (
    select 1 from jsonb_array_elements(data->'field_definitions') as fd
    where fd->>'path' in ('disk_image', 'spec.disk_image')
  );
