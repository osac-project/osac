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

-- InstanceType deletion protection for compute instance catalog items, matched by instance type name.
--
-- The existing check_instance_type_not_in_use trigger (migration 90) guards compute_instances and
-- compute_instance_templates but does not guard compute_instance_catalog_items. A catalog item can
-- reference an instance type via a field_definition with path 'spec.instance_type' and a bare-string
-- default holding the instance type name. Without this clause, soft-deleting the instance type
-- succeeds and leaves a dangling reference.
--
-- InstanceType uses name-as-primary-key (id = metadata.name), so old.name matches the bare-string
-- default stored in the catalog item's field_definition.
--
-- This migration uses CREATE OR REPLACE to supersede migration 90's version, re-declaring the
-- existing compute_instances and compute_instance_templates clauses and adding the catalog-items
-- clause. The trigger wiring (BEFORE UPDATE on instance_types) is unchanged from migration 90.

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
    cross join lateral jsonb_array_elements(
      case when jsonb_typeof(compute_instance_catalog_items.data->'field_definitions') = 'array'
        then compute_instance_catalog_items.data->'field_definitions'
        else '[]'::jsonb
      end
    ) as fd
    where compute_instance_catalog_items.deletion_timestamp = 'epoch'
      and fd->>'path' = 'spec.instance_type'
      and fd->>'default' = old.name
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
