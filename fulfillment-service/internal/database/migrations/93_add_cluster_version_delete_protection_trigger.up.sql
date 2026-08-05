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

-- This migration adds bidirectional referential integrity triggers between cluster versions and the resources that
-- reference them by version_name:
--
-- 1. A BEFORE UPDATE trigger on cluster_versions that prevents soft-deleting a version while an active cluster,
--    cluster template, or cluster catalog item still references it.
--
-- 2. BEFORE INSERT OR UPDATE triggers on clusters, cluster_templates, and cluster_catalog_items that prevent
--    creating or updating a resource that references a cluster version that does not exist or has been
--    soft-deleted. These triggers use FOR SHARE to acquire a shared lock on the cluster version row, which
--    conflicts with the lock held by a concurrent version soft-delete. This bidirectional locking eliminates the
--    TOCTOU race condition.

-- Index to speed up the JSONB text extraction used by the outbound trigger:
create index clusters_version_name on clusters
  ((data->'spec'->>'version_name'))
  where data->'spec'->>'version_name' is not null;

-- Shared helper that locks and validates a cluster version reference. Called by both the scalar trigger function
-- (check_cluster_version_ref) and the array trigger function (check_cluster_catalog_item_version_name_refs).
create function ensure_active_cluster_version(version_name text) returns void as $$
begin
  if nullif(version_name, '') is null then
    return;
  end if;

  perform 1
  from cluster_versions
  where name = version_name
    and deletion_timestamp = 'epoch'
  for share;

  if not found then
    raise exception
      'cluster version ''%'' does not exist or has been deleted',
      version_name
      using errcode = 'Z0002';
  end if;
end;
$$ language plpgsql;

-- Trigger function that checks whether any active cluster, cluster template, or cluster catalog item references the
-- cluster version being deleted. Uses exists(select 1 ...) rather than selecting the referencing resource's
-- identifier, to avoid leaking cross-resource identity information (see migration 72).
create function check_cluster_version_not_in_use() returns trigger as $$
begin
  if exists (
    select 1
    from clusters
    where deletion_timestamp = 'epoch'
      and data->'spec'->>'version_name' = old.name
  ) then
    raise exception
      'cannot delete cluster version ''%'': it is in use by at least one cluster',
      old.name
      using errcode = 'Z0003';
  end if;

  if exists (
    select 1
    from cluster_templates
    where deletion_timestamp = 'epoch'
      and data->'spec_defaults'->>'version_name' = old.name
  ) then
    raise exception
      'cannot delete cluster version ''%'': it is in use by at least one cluster template',
      old.name
      using errcode = 'Z0003';
  end if;

  if exists (
    select 1
    from cluster_catalog_items
    cross join lateral jsonb_array_elements(
      case when jsonb_typeof(cluster_catalog_items.data->'field_definitions') = 'array'
        then cluster_catalog_items.data->'field_definitions'
        else '[]'::jsonb
      end
    ) as elem
    where cluster_catalog_items.deletion_timestamp = 'epoch'
      and elem->>'path' = 'version_name'
      and elem->>'default' = old.name
  ) then
    raise exception
      'cannot delete cluster version ''%'': it is in use by at least one cluster catalog item',
      old.name
      using errcode = 'Z0003';
  end if;

  return new;
end;
$$ language plpgsql;

-- Attach the trigger so that it only fires when the row transitions from active to soft-deleted:
create trigger check_cluster_version_not_in_use
  before update on cluster_versions
  for each row
  when (old.deletion_timestamp = 'epoch' and new.deletion_timestamp != 'epoch')
  execute function check_cluster_version_not_in_use();

-- Reusable trigger function that validates a version_name reference at a JSONB path given as trigger arguments
-- (e.g. 'spec', 'version_name'). Uses jsonb_extract_path_text with VARIADIC tg_argv to avoid custom path encoding.
create function check_cluster_version_ref() returns trigger as $$
begin
  perform ensure_active_cluster_version(
    jsonb_extract_path_text(new.data, variadic tg_argv)
  );

  return new;
end;
$$ language plpgsql;

-- Attach the trigger so that it fires on insert of active clusters:
create trigger check_cluster_version_name_ref_on_insert
  before insert on clusters
  for each row
  when (new.deletion_timestamp = 'epoch')
  execute function check_cluster_version_ref('spec', 'version_name');

-- Attach the trigger so that it fires on update of active clusters, but only when the version_name reference
-- actually changes (avoids a FOR SHARE lock on every status update):
create trigger check_cluster_version_name_ref_on_update
  before update on clusters
  for each row
  when (new.deletion_timestamp = 'epoch' and
    (new.data->'spec'->>'version_name') is distinct from (old.data->'spec'->>'version_name'))
  execute function check_cluster_version_ref('spec', 'version_name');

-- Attach the trigger so that it fires on insert of active cluster templates:
create trigger check_cluster_template_version_name_ref_on_insert
  before insert on cluster_templates
  for each row
  when (new.deletion_timestamp = 'epoch')
  execute function check_cluster_version_ref('spec_defaults', 'version_name');

-- Attach the trigger so that it fires on update of active cluster templates, but only when the version_name
-- reference actually changes:
create trigger check_cluster_template_version_name_ref_on_update
  before update on cluster_templates
  for each row
  when (new.deletion_timestamp = 'epoch' and
    (new.data->'spec_defaults'->>'version_name') is distinct from (old.data->'spec_defaults'->>'version_name'))
  execute function check_cluster_version_ref('spec_defaults', 'version_name');

-- Trigger function that validates version_name references in a cluster catalog item's field_definitions. Unlike
-- check_cluster_version_ref(), this scans an array rather than a single scalar path, so it is a dedicated function.
create function check_cluster_catalog_item_version_name_refs() returns trigger as $$
declare
  version_name text;
begin
  for version_name in
    select elem->>'default'
    from jsonb_array_elements(
      case
        when jsonb_typeof(new.data->'field_definitions') = 'array'
          then new.data->'field_definitions'
        else '[]'::jsonb
      end
    ) as elem
    where elem->>'path' = 'version_name'
  loop
    perform ensure_active_cluster_version(version_name);
  end loop;

  return new;
end;
$$ language plpgsql;

-- Attach the trigger so that it fires on insert of active cluster catalog items:
create trigger check_cluster_catalog_item_version_name_refs_on_insert
  before insert on cluster_catalog_items
  for each row
  when (new.deletion_timestamp = 'epoch')
  execute function check_cluster_catalog_item_version_name_refs();

-- Attach the trigger so that it fires on update of active cluster catalog items, but only when field_definitions
-- actually changes:
create trigger check_cluster_catalog_item_version_name_refs_on_update
  before update on cluster_catalog_items
  for each row
  when (new.deletion_timestamp = 'epoch' and
    (new.data->'field_definitions') is distinct from (old.data->'field_definitions'))
  execute function check_cluster_catalog_item_version_name_refs();
