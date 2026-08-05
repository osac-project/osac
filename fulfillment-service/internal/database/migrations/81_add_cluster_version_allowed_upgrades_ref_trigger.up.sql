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

-- This migration adds an inbound referential integrity trigger on cluster_versions that validates each entry in
-- spec.allowed_upgrades.version_names references an existing, non-deleted ClusterVersion within the same tenant and
-- project. The trigger acquires FOR SHARE locks on all referenced rows in a single query, preventing a concurrent
-- transaction from soft-deleting a referenced ClusterVersion while this row is being inserted or updated.

-- Trigger function that validates allowed_upgrades.version_names references.
-- All referenced rows are locked in a single ordered query rather than iteratively, so concurrent executions of
-- this trigger acquire locks in a consistent order and cannot deadlock with each other.
create function check_cluster_version_allowed_upgrade_refs() returns trigger as $$
declare
  ref_names text[];
  found_names text[];
  missing_name text;
begin
  ref_names := array(
    select jsonb_array_elements_text(
      coalesce(new.data #> '{spec,allowed_upgrades,version_names}', '[]'::jsonb)));

  if coalesce(array_length(ref_names, 1), 0) = 0 then
    return new;
  end if;

  -- Lock all matching rows in a single query, then collect names. The ORDER BY ensures a consistent
  -- lock acquisition order across concurrent transactions, preventing deadlocks:
  select array_agg(locked.name) into found_names
  from (
    select cv.name
    from cluster_versions cv
    where cv.name = any(ref_names)
      and cv.tenant = new.tenant
      and cv.project = new.project
      and cv.deletion_timestamp = 'epoch'
    order by cv.name
    for share
  ) locked;

  -- In-memory set difference to find the first missing name:
  select ref into missing_name
  from unnest(ref_names) as ref
  where ref != all(coalesce(found_names, '{}'))
  limit 1;

  if missing_name is not null then
    raise exception using
      errcode = 'Z0002',
      message = format(
        'ClusterVersion ''%s'' does not exist or has been deleted', missing_name);
  end if;

  return new;
end;
$$ language plpgsql;

-- INSERT trigger — validate on new active rows:
create trigger check_cluster_version_allowed_upgrade_refs_on_insert
  before insert on cluster_versions
  for each row
  when (new.deletion_timestamp = 'epoch')
  execute function check_cluster_version_allowed_upgrade_refs();

-- UPDATE trigger — only validate when allowed_upgrades.version_names actually changes:
create trigger check_cluster_version_allowed_upgrade_refs_on_update
  before update on cluster_versions
  for each row
  when (new.deletion_timestamp = 'epoch' and
    (new.data #> '{spec,allowed_upgrades,version_names}')
    is distinct from
    (old.data #> '{spec,allowed_upgrades,version_names}'))
  execute function check_cluster_version_allowed_upgrade_refs();
