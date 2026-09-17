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

-- Instance types use their name as their primary key, and compute instance catalog items store instance type defaults
-- as bare strings in the top-level field_definitions array. Extend the existing delete-protection function from
-- migration 90 so an active catalog item cannot retain a dangling spec.instance_type reference. The trigger wiring is
-- unchanged because CREATE OR REPLACE updates the function executed by the existing trigger.

-- Index active catalog-item field definitions so the outbound reference check doesn't scan and expand every catalog
-- item when an instance type is deleted. jsonb_path_ops supports the containment lookup used by the trigger function.
create index compute_instance_catalog_items_instance_type
  on compute_instance_catalog_items using gin ((data->'field_definitions') jsonb_path_ops)
  where deletion_timestamp = 'epoch';

-- OSAC-4211: Validate each catalog-item instance type reference while holding a row lock that conflicts with a
-- concurrent instance type soft-delete. The application-level validation is an unlocked read, so it cannot by itself
-- prevent a delete from committing between validation and the catalog-item write.
create function check_compute_instance_catalog_item_instance_type_ref() returns trigger as $$
declare
  field_definition jsonb;
  instance_type_name text;
  found_id text;
begin
  if jsonb_typeof(new.data->'field_definitions') != 'array' then
    return new;
  end if;

  for field_definition in
    select value from jsonb_array_elements(new.data->'field_definitions')
  loop
    if field_definition->>'path' = 'spec.instance_type'
      and jsonb_typeof(field_definition->'default') = 'string'
    then
      instance_type_name := field_definition->>'default';
      if coalesce(instance_type_name, '') != '' then
        found_id := null;
        select id into found_id
        from instance_types
        where id = instance_type_name
          and deletion_timestamp = 'epoch'
        for share;

        if found_id is null then
          raise exception using
            errcode = 'Z0002',
            message = format(
              'instance type ''%s'' does not exist or has been deleted',
              instance_type_name
            );
        end if;
      end if;
    end if;
  end loop;

  return new;
end;
$$ language plpgsql;

create trigger check_compute_instance_catalog_item_instance_type_ref
  before insert or update of data, deletion_timestamp on compute_instance_catalog_items
  for each row
  when (new.deletion_timestamp = 'epoch')
  execute function check_compute_instance_catalog_item_instance_type_ref();

create or replace function check_instance_type_not_in_use() returns trigger as $$
begin
  if exists (
    select 1
    from compute_instances
    where deletion_timestamp = 'epoch'
      and data->'spec'->'instance_type'->>'id' = old.id
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete instance type ''%s'': it is in use by at least one compute instance',
        old.id
      );
  end if;

  if exists (
    select 1
    from compute_instance_templates
    where deletion_timestamp = 'epoch'
      and data->'spec_defaults'->'instance_type'->>'id' = old.id
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete instance type ''%s'': it is in use by at least one compute instance template',
        old.id
      );
  end if;

  if exists (
    select 1
    from compute_instance_catalog_items
    where compute_instance_catalog_items.deletion_timestamp = 'epoch'
      and jsonb_typeof(compute_instance_catalog_items.data->'field_definitions') = 'array'
      and compute_instance_catalog_items.data->'field_definitions' @> jsonb_build_array(
        jsonb_build_object('path', 'spec.instance_type', 'default', old.name)
      )
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete instance type ''%s'': it is in use by at least one compute instance catalog item',
        old.id
      );
  end if;

  return new;
end;
$$ language plpgsql;
