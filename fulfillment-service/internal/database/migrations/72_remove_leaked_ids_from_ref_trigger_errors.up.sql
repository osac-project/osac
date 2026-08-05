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

-- This migration replaces the "not in use" trigger functions so that error messages no longer include the identifier
-- of the referencing resource. Including the referencing resource's ID in the error message leaked cross-resource
-- identity information to callers who may not be authorized to see it.
--
-- Affected triggers (from migrations 52, 56, 59):
--   - check_subnet_not_in_use           (migration 52)
--   - check_instance_type_not_in_use    (migration 56)
--   - check_cluster_catalog_item_not_in_use   (migration 59)
--   - check_ci_catalog_item_not_in_use        (migration 59)
--
-- Not affected (already uses a count-based pattern without IDs):
--   - check_virtual_network_not_in_use  (migration 55)

-- Replace check_subnet_not_in_use to stop leaking compute instance IDs:
create or replace function check_subnet_not_in_use() returns trigger as $$
begin
  if exists (
    select 1
    from compute_instances
    where deletion_timestamp = 'epoch'
      and data->'spec'->'network_attachments' @>
          jsonb_build_array(jsonb_build_object('subnet', old.id))
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete subnet ''%s'': it is in use by at least one compute instance',
        old.id
      );
  end if;

  return new;
end;
$$ language plpgsql;

-- Replace check_instance_type_not_in_use to stop leaking compute instance IDs:
create or replace function check_instance_type_not_in_use() returns trigger as $$
begin
  if exists (
    select 1
    from compute_instances
    where deletion_timestamp = 'epoch'
      and data->'spec'->>'instance_type' = old.id
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete instance type ''%s'': it is in use by at least one compute instance',
        old.id
      );
  end if;

  return new;
end;
$$ language plpgsql;

-- Replace check_cluster_catalog_item_not_in_use to stop leaking cluster IDs:
create or replace function check_cluster_catalog_item_not_in_use() returns trigger as $$
begin
  if exists (
    select 1
    from clusters
    where deletion_timestamp = 'epoch'
      and (data->'spec'->>'catalog_item' = old.id or data->'spec'->>'catalog_item' = old.name)
      and (old.tenant = 'shared' or clusters.tenant = old.tenant)
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete cluster catalog item ''%s'': it is in use by at least one cluster',
        old.id
      );
  end if;

  return new;
end;
$$ language plpgsql;

-- Replace check_ci_catalog_item_not_in_use to stop leaking compute instance IDs:
create or replace function check_ci_catalog_item_not_in_use() returns trigger as $$
begin
  if exists (
    select 1
    from compute_instances
    where deletion_timestamp = 'epoch'
      and (data->'spec'->>'catalog_item' = old.id or data->'spec'->>'catalog_item' = old.name)
      and (old.tenant = 'shared' or compute_instances.tenant = old.tenant)
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete compute instance catalog item ''%s'': it is in use by at least one compute instance',
        old.id
      );
  end if;

  return new;
end;
$$ language plpgsql;
