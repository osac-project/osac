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

-- This migration adds two triggers that enforce bidirectional referential integrity between instance types and compute
-- instances:
--
-- 1. A BEFORE UPDATE trigger on instance_types that prevents soft-deleting an instance type while active compute
--    instances still reference it.
--
-- 2. A BEFORE INSERT OR UPDATE trigger on compute_instances that prevents creating or updating a compute instance with
--    an instance_type that does not exist or has been soft-deleted. This trigger uses FOR SHARE to acquire a shared lock
--    on the instance type row, which conflicts with the exclusive lock held by a concurrent instance type soft-delete.
--    This bidirectional locking eliminates the TOCTOU race condition between instance type deletion and compute instance
--    creation.

-- Index to speed up the JSONB lookup used by the trigger:
create index compute_instances_instance_type on compute_instances ((data->'spec'->>'instance_type'))
  where data->'spec'->>'instance_type' is not null;

-- Trigger function that checks whether any active compute instance references the instance type being deleted:
create function check_instance_type_not_in_use() returns trigger as $$
declare
  ci_id text;
begin
  select id into ci_id
  from compute_instances
  where deletion_timestamp = 'epoch'
    and data->'spec'->>'instance_type' = old.id
  limit 1;

  if ci_id is not null then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete instance type ''%s'': it is in use by at least compute instance ''%s''',
        old.id, ci_id
      );
  end if;

  return new;
end;
$$ language plpgsql;

-- Attach the trigger so that it only fires when the row transitions from active to soft-deleted:
create trigger check_instance_type_not_in_use
  before update on instance_types
  for each row
  when (old.deletion_timestamp = 'epoch' and new.deletion_timestamp != 'epoch')
  execute function check_instance_type_not_in_use();

-- Trigger function that validates the instance_type reference in a compute instance:
create function check_compute_instance_instance_type_ref() returns trigger as $$
declare
  it_id text;
  found_id text;
begin
  it_id := new.data->'spec'->>'instance_type';

  if coalesce(it_id, '') = '' then
    return new;
  end if;

  select id into found_id
  from instance_types
  where id = it_id
    and deletion_timestamp = 'epoch'
  for share;

  if found_id is null then
    raise exception using
      errcode = 'Z0002',
      message = format(
        'instance type ''%s'' does not exist or has been deleted',
        it_id
      );
  end if;

  return new;
end;
$$ language plpgsql;

-- Attach the trigger so that it fires on insert and update of active compute instances:
create trigger check_compute_instance_instance_type_ref
  before insert or update on compute_instances
  for each row
  when (new.deletion_timestamp = 'epoch')
  execute function check_compute_instance_instance_type_ref();
