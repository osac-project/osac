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

-- ComputeInstance status holds the instance type that is actually running. It keeps the old type
-- until a restart picks up a resize, so it can name a type that spec.instance_type no longer
-- names. The delete guard only read spec, so that type could be deleted while a stored instance
-- still referenced it. Every quota check of the tenant then failed with Internal, because the
-- counter refused the missing-type sentinel.
--
-- This migration recreates the guard so it reads status.instance_type too. The spec, template and
-- catalog policy checks stay as they are.
--
-- The compute instance check doesn't filter on the deletion timestamp. The quota counter counts a
-- compute instance until it is archived, so it still prices the type of an instance that is only
-- soft-deleted. Freeing the type at soft delete would leave that counted row pointing at a type
-- that no longer exists, and every quota check of the tenant would then fail with Internal. The
-- template and catalog item checks keep the filter: neither kind holds any quota, so no counter
-- reads those rows.

create or replace function check_instance_type_not_in_use() returns trigger as $$
begin
  if exists (
    select 1 from compute_instances
    where
      data->'spec'->'instance_type'->>'id' = old.id
      or data->'status'->'instance_type'->>'id' = old.id
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete instance type ''%s'': it is in use by at least one compute instance',
        old.id
      );
  end if;

  if exists (
    select 1 from compute_instance_templates
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
    select 1 from compute_instance_catalog_items c
    where c.deletion_timestamp = 'epoch'
      and (
        c.data->'fields'->'instance_type'->'locked'->>'id' = old.id
        or c.data->'fields'->'instance_type'->'editable'->'default_value'->>'id' = old.id
      )
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete instance type ''%s'': it is in use by an active resource or catalog policy',
        old.id
      );
  end if;

  return new;
end;
$$ language plpgsql;
