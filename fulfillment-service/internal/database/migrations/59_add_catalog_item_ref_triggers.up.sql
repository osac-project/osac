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

-- This migration adds bidirectional referential integrity triggers between catalog items and their consumers:
--
-- 1. BEFORE UPDATE triggers on cluster_catalog_items and compute_instance_catalog_items that prevent soft-deleting a
--    catalog item while active clusters or compute instances still reference it.
--
-- 2. BEFORE INSERT OR UPDATE triggers on clusters and compute_instances that prevent creating or updating a resource
--    that references a catalog item that does not exist or has been soft-deleted. These triggers use FOR SHARE to
--    acquire a shared lock on the catalog item row, which conflicts with the exclusive lock held by a concurrent
--    catalog item soft-delete. This bidirectional locking eliminates the TOCTOU race condition.

-- Indexes to speed up the lookup of clusters and compute instances by their catalog_item reference:
create index clusters_catalog_item on clusters
  using btree ((data->'spec'->>'catalog_item'))
  where data->'spec'->>'catalog_item' is not null;

create index compute_instances_catalog_item on compute_instances
  using btree ((data->'spec'->>'catalog_item'))
  where data->'spec'->>'catalog_item' is not null;

-- Trigger function that checks whether any active cluster references the cluster catalog item being deleted:
create function check_cluster_catalog_item_not_in_use() returns trigger as $$
declare
  child_id text;
begin
  select id into child_id
  from clusters
  where deletion_timestamp = 'epoch'
    and (data->'spec'->>'catalog_item' = old.id or data->'spec'->>'catalog_item' = old.name)
    and (old.tenant = 'shared' or clusters.tenant = old.tenant)
  limit 1;

  if child_id is not null then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete cluster catalog item ''%s'': it is in use by at least cluster ''%s''',
        old.id, child_id
      );
  end if;

  return new;
end;
$$ language plpgsql;

-- Attach the trigger so that it only fires when the row transitions from active to soft-deleted:
create trigger check_cluster_catalog_item_not_in_use
  before update on cluster_catalog_items
  for each row
  when (old.deletion_timestamp = 'epoch' and new.deletion_timestamp != 'epoch')
  execute function check_cluster_catalog_item_not_in_use();

-- Trigger function that checks whether any active compute instance references the compute instance catalog item
-- being deleted:
create function check_ci_catalog_item_not_in_use() returns trigger as $$
declare
  child_id text;
begin
  select id into child_id
  from compute_instances
  where deletion_timestamp = 'epoch'
    and (data->'spec'->>'catalog_item' = old.id or data->'spec'->>'catalog_item' = old.name)
    and (old.tenant = 'shared' or compute_instances.tenant = old.tenant)
  limit 1;

  if child_id is not null then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete compute instance catalog item ''%s'': it is in use by at least compute instance ''%s''',
        old.id, child_id
      );
  end if;

  return new;
end;
$$ language plpgsql;

-- Attach the trigger so that it only fires when the row transitions from active to soft-deleted:
create trigger check_ci_catalog_item_not_in_use
  before update on compute_instance_catalog_items
  for each row
  when (old.deletion_timestamp = 'epoch' and new.deletion_timestamp != 'epoch')
  execute function check_ci_catalog_item_not_in_use();

-- Trigger function that validates the cluster catalog item reference when creating or updating a cluster:
create function check_cluster_catalog_item_ref() returns trigger as $$
declare
  catalog_item_ref text;
  found_id text;
begin
  catalog_item_ref := new.data->'spec'->>'catalog_item';
  if coalesce(catalog_item_ref, '') != '' then
    select id into found_id
    from cluster_catalog_items
    where (id = catalog_item_ref or name = catalog_item_ref)
      and deletion_timestamp = 'epoch'
      and (tenant = new.tenant or tenant = 'shared')
    for share;

    if found_id is null then
      raise exception using
        errcode = 'Z0002',
        message = format(
          'cluster catalog item ''%s'' does not exist or has been deleted',
          catalog_item_ref
        );
    end if;
  end if;

  return new;
end;
$$ language plpgsql;

-- Attach the trigger so that it fires on insert of active clusters:
create trigger check_cluster_catalog_item_ref_on_insert
  before insert on clusters
  for each row
  when (new.deletion_timestamp = 'epoch')
  execute function check_cluster_catalog_item_ref();

-- Attach the trigger so that it fires on update of active clusters, but only when the
-- catalog item reference actually changes (avoids a FOR SHARE lock on every status update):
create trigger check_cluster_catalog_item_ref_on_update
  before update on clusters
  for each row
  when (new.deletion_timestamp = 'epoch' and
    (new.data->'spec'->>'catalog_item') is distinct from (old.data->'spec'->>'catalog_item'))
  execute function check_cluster_catalog_item_ref();

-- Trigger function that validates the compute instance catalog item reference when creating or updating a compute
-- instance:
create function check_ci_catalog_item_ref() returns trigger as $$
declare
  catalog_item_ref text;
  found_id text;
begin
  catalog_item_ref := new.data->'spec'->>'catalog_item';
  if coalesce(catalog_item_ref, '') != '' then
    select id into found_id
    from compute_instance_catalog_items
    where (id = catalog_item_ref or name = catalog_item_ref)
      and deletion_timestamp = 'epoch'
      and (tenant = new.tenant or tenant = 'shared')
    for share;

    if found_id is null then
      raise exception using
        errcode = 'Z0002',
        message = format(
          'compute instance catalog item ''%s'' does not exist or has been deleted',
          catalog_item_ref
        );
    end if;
  end if;

  return new;
end;
$$ language plpgsql;

-- Attach the trigger so that it fires on insert of active compute instances:
create trigger check_ci_catalog_item_ref_on_insert
  before insert on compute_instances
  for each row
  when (new.deletion_timestamp = 'epoch')
  execute function check_ci_catalog_item_ref();

-- Attach the trigger so that it fires on update of active compute instances, but only
-- when the catalog item reference actually changes:
create trigger check_ci_catalog_item_ref_on_update
  before update on compute_instances
  for each row
  when (new.deletion_timestamp = 'epoch' and
    (new.data->'spec'->>'catalog_item') is distinct from (old.data->'spec'->>'catalog_item'))
  execute function check_ci_catalog_item_ref();
