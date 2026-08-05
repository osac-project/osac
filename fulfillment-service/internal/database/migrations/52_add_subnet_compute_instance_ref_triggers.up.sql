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

-- This migration adds two triggers that enforce bidirectional referential integrity between subnets and compute
-- instances:
--
-- 1. A BEFORE UPDATE trigger on subnets that prevents soft-deleting a subnet while active compute instances still
--    reference it.
--
-- 2. A BEFORE INSERT trigger on compute_instances that prevents creating a compute instance that references a subnet
--    that does not exist or has been soft-deleted. This trigger uses FOR SHARE to acquire a shared lock on the subnet
--    row, which conflicts with the exclusive lock held by a concurrent subnet soft-delete. This bidirectional locking
--    eliminates the TOCTOU race condition between subnet deletion and compute instance creation. The trigger only
--    fires on INSERT because subnet references in network_attachments are immutable (enforced by the application).

-- Index to speed up the JSONB containment query used by the trigger:
create index compute_instances_network_attachments on compute_instances
  using gin ((data->'spec'->'network_attachments'));

-- Trigger function that checks whether any active compute instance references the subnet being deleted:
create function check_subnet_not_in_use() returns trigger as $$
declare
  ci_id text;
begin
  select id into ci_id
  from compute_instances
  where deletion_timestamp = 'epoch'
    and data->'spec'->'network_attachments' @>
        jsonb_build_array(jsonb_build_object('subnet', old.id))
  limit 1;

  if ci_id is not null then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete subnet ''%s'': it is in use by at least compute instance ''%s''',
        old.id, ci_id
      );
  end if;

  return new;
end;
$$ language plpgsql;

-- Attach the trigger so that it only fires when the row transitions from active to soft-deleted:
create trigger check_subnet_not_in_use
  before update on subnets
  for each row
  when (old.deletion_timestamp = 'epoch' and new.deletion_timestamp != 'epoch')
  execute function check_subnet_not_in_use();

-- Trigger function that validates subnet references in a compute instance's network_attachments:
create function check_compute_instance_subnet_refs() returns trigger as $$
declare
  attachment jsonb;
  subnet_id text;
  found_id text;
begin
  for attachment in select jsonb_array_elements(coalesce(new.data->'spec'->'network_attachments', '[]'::jsonb))
  loop
    subnet_id := attachment->>'subnet';
    if coalesce(subnet_id, '') != '' then
      select id into found_id
      from subnets
      where id = subnet_id
        and deletion_timestamp = 'epoch'
      for share;

      if found_id is null then
        raise exception using
          errcode = 'Z0002',
          message = format(
            'subnet ''%s'' does not exist or has been deleted',
            subnet_id
          );
      end if;
    end if;
  end loop;

  return new;
end;
$$ language plpgsql;

-- Attach the trigger so that it fires on insert of active compute instances:
create trigger check_compute_instance_subnet_refs
  before insert on compute_instances
  for each row
  when (new.deletion_timestamp = 'epoch')
  execute function check_compute_instance_subnet_refs();
