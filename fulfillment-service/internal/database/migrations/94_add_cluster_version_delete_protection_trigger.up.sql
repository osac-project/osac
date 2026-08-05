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

-- This migration adds a BEFORE UPDATE trigger on cluster_versions that prevents soft-deleting a version while an
-- active cluster, cluster template, or cluster catalog item still references it. Inbound (forward-reference)
-- validation is handled by the gRPC reference validation interceptor, not by database triggers.
--
-- Unlike other Z0003 triggers (migration 90) which match on ->>'id', this trigger matches on ->>'name' because
-- the ClusterVersionReference stores the version name, and migration 93 backfilled only the name field.

-- Index to speed up the JSONB text extraction used by the outbound trigger:
create index clusters_version on clusters
  ((data->'spec'->'version'->>'name'))
  where data->'spec'->'version'->>'name' is not null;

-- Trigger function that checks whether any active cluster, cluster template, or cluster catalog item references the
-- cluster version being deleted. Uses exists(select 1 ...) rather than selecting the referencing resource's
-- identifier, to avoid leaking cross-resource identity information (see migration 72).
create function check_cluster_version_not_in_use() returns trigger as $$
begin
  if exists (
    select 1
    from clusters
    where deletion_timestamp = 'epoch'
      and data->'spec'->'version'->>'name' = old.name
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete cluster version ''%s'': it is in use by at least one cluster',
        old.name
      );
  end if;

  if exists (
    select 1
    from cluster_templates
    where deletion_timestamp = 'epoch'
      and data->'spec_defaults'->'version'->>'name' = old.name
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete cluster version ''%s'': it is in use by at least one cluster template',
        old.name
      );
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
      and elem->>'path' = 'version'
      and elem->'default'->>'name' = old.name
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete cluster version ''%s'': it is in use by at least one cluster catalog item',
        old.name
      );
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
