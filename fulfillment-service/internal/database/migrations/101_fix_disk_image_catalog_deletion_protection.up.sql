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

-- DiskImage deletion protection for compute instance catalog items, matched by disk image id.
--
-- DiskImage names are unique only per (name, tenant), so a shared "fedora" and a tenant "fedora"
-- can coexist. Matching a catalog default on name would over-block: soft-deleting either image
-- would be refused whenever any catalog item defaults to that name. The id is unique, so the
-- catalog clause matches the resolved id, like the compute_instances and compute_instance_templates
-- clauses.
--
-- This migration:
--   1. Defines check_disk_image_not_in_use() so the catalog-items clause scans the top-level
--      field_definitions array, matches path 'disk_image', and matches the default object's id
--      (fd->'default'->>'id') against old.id. The compute_instances and compute_instance_templates
--      clauses match spec/spec_defaults ->>'id'. CREATE OR REPLACE redefines the whole body, so all
--      three clauses appear here.
--   2. Normalizes existing compute_instance_catalog_items field_definitions to path 'disk_image'
--      with a default {"id": <resolved>, "name": <name>}. The id is resolved from the default's name
--      against disk_images, own tenant first then shared — the precedence the server applies at
--      authoring time (auth.TenancyLogic.DetermineDefaultTenant). An unresolvable name (image gone)
--      is left name-only, best-effort.

-- 1. Define the trigger function (CREATE OR REPLACE; the trigger wiring is defined elsewhere).
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
      and fd->'default'->>'id' = old.id
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

-- 2. Normalize existing catalog-item field_definitions to path 'disk_image' with an id-keyed
--    reference-object default {"id": <resolved>, "name": <name>}. Handles the prefix-less
--    'disk_image' and the legacy 'spec.disk_image' path, and both bare-string and {"name": ...}
--    defaults. The lateral extracts the lookup name once: it is null when the default already
--    carries an id (nothing to resolve) or has no name, which routes the row to path-only
--    normalization and avoids jsonb_set's strict-null clobbering the field definition. The id is
--    resolved with own-tenant-then-shared precedence; unresolvable names are left name-only.
update compute_instance_catalog_items
set data = jsonb_set(data, '{field_definitions}',
  (select coalesce(jsonb_agg(
    case
      when fd->>'path' in ('disk_image', 'spec.disk_image') then
        case
          when dn.name is not null and dn.name <> '' then
            jsonb_set(
              jsonb_set(fd, '{path}', '"disk_image"'),
              '{default}',
              coalesce(
                (select jsonb_build_object('id', di.id, 'name', dn.name)
                   from disk_images di
                  where di.name = dn.name
                    and di.tenant = compute_instance_catalog_items.tenant
                    and di.deletion_timestamp = 'epoch'
                  limit 1),
                (select jsonb_build_object('id', di.id, 'name', dn.name)
                   from disk_images di
                  where di.name = dn.name
                    and di.tenant = 'shared'
                    and di.deletion_timestamp = 'epoch'
                  limit 1),
                jsonb_build_object('name', dn.name)
              )
            )
          else
            jsonb_set(fd, '{path}', '"disk_image"')
        end
      else fd
    end
  ), '[]'::jsonb)
  from jsonb_array_elements(data->'field_definitions') as fd
  cross join lateral (
    select case
      when jsonb_typeof(fd->'default') = 'string' then fd->>'default'
      when jsonb_typeof(fd->'default') = 'object'
           and coalesce(fd->'default'->>'id', '') = '' then fd->'default'->>'name'
      else null
    end as name
  ) as dn)
)
where data->'field_definitions' is not null
  and exists (
    select 1 from jsonb_array_elements(data->'field_definitions') as fd
    where fd->>'path' in ('disk_image', 'spec.disk_image')
  );

-- Archived catalog items: same conversion, for data consistency. Their tenant column drives the
-- same own-tenant-then-shared id resolution.
update archived_compute_instance_catalog_items
set data = jsonb_set(data, '{field_definitions}',
  (select coalesce(jsonb_agg(
    case
      when fd->>'path' in ('disk_image', 'spec.disk_image') then
        case
          when dn.name is not null and dn.name <> '' then
            jsonb_set(
              jsonb_set(fd, '{path}', '"disk_image"'),
              '{default}',
              coalesce(
                (select jsonb_build_object('id', di.id, 'name', dn.name)
                   from disk_images di
                  where di.name = dn.name
                    and di.tenant = archived_compute_instance_catalog_items.tenant
                    and di.deletion_timestamp = 'epoch'
                  limit 1),
                (select jsonb_build_object('id', di.id, 'name', dn.name)
                   from disk_images di
                  where di.name = dn.name
                    and di.tenant = 'shared'
                    and di.deletion_timestamp = 'epoch'
                  limit 1),
                jsonb_build_object('name', dn.name)
              )
            )
          else
            jsonb_set(fd, '{path}', '"disk_image"')
        end
      else fd
    end
  ), '[]'::jsonb)
  from jsonb_array_elements(data->'field_definitions') as fd
  cross join lateral (
    select case
      when jsonb_typeof(fd->'default') = 'string' then fd->>'default'
      when jsonb_typeof(fd->'default') = 'object'
           and coalesce(fd->'default'->>'id', '') = '' then fd->'default'->>'name'
      else null
    end as name
  ) as dn)
)
where data->'field_definitions' is not null
  and exists (
    select 1 from jsonb_array_elements(data->'field_definitions') as fd
    where fd->>'path' in ('disk_image', 'spec.disk_image')
  );
