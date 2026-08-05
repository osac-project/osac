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

-- This migration adds triggers that enforce bidirectional referential integrity between virtual networks and their
-- child subnets and security groups:
--
-- 1. A BEFORE UPDATE trigger on virtual_networks that prevents soft-deleting a virtual network while active subnets
--    or security groups still reference it.
--
-- 2. BEFORE INSERT triggers on subnets and security_groups that prevent creating a child that references a virtual
--    network that does not exist or has been soft-deleted. Each trigger uses FOR SHARE to acquire a shared lock on
--    the virtual network row, which conflicts with the exclusive lock held by a concurrent virtual network
--    soft-delete. This bidirectional locking eliminates the TOCTOU race condition between virtual network deletion
--    and child creation. The triggers only fire on INSERT because virtual_network references are immutable
--    (enforced by the application).

-- Indexes to speed up child reference lookups used by the delete trigger:
create index subnets_by_virtual_network on subnets ((data->'spec'->>'virtual_network'))
  where deletion_timestamp = 'epoch';

create index security_groups_by_virtual_network on security_groups ((data->'spec'->>'virtual_network'))
  where deletion_timestamp = 'epoch';

-- Trigger function that checks whether any active subnet or security group references the virtual network being deleted:
create function check_virtual_network_not_in_use() returns trigger as $$
declare
  subnet_count bigint;
  sg_count bigint;
begin
  select count(*) into subnet_count
  from subnets
  where deletion_timestamp = 'epoch'
    and data->'spec'->>'virtual_network' = old.id;

  if subnet_count > 0 then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete VirtualNetwork ''%s'': %s Subnet(s) still reference it',
        old.id, subnet_count
      );
  end if;

  select count(*) into sg_count
  from security_groups
  where deletion_timestamp = 'epoch'
    and data->'spec'->>'virtual_network' = old.id;

  if sg_count > 0 then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete VirtualNetwork ''%s'': %s SecurityGroup(s) still reference it',
        old.id, sg_count
      );
  end if;

  return new;
end;
$$ language plpgsql;

-- Attach the trigger so that it only fires when the row transitions from active to soft-deleted:
create trigger check_virtual_network_not_in_use
  before update on virtual_networks
  for each row
  when (old.deletion_timestamp = 'epoch' and new.deletion_timestamp != 'epoch')
  execute function check_virtual_network_not_in_use();

-- Trigger function that validates the virtual_network reference in a subnet:
create function check_subnet_virtual_network_ref() returns trigger as $$
declare
  vn_id text;
  found_id text;
begin
  vn_id := new.data->'spec'->>'virtual_network';
  if coalesce(vn_id, '') = '' then
    return new;
  end if;

  select id into found_id
  from virtual_networks
  where id = vn_id
    and deletion_timestamp = 'epoch'
  for share;

  if found_id is null then
    raise exception using
      errcode = 'Z0002',
      message = format(
        'VirtualNetwork ''%s'' does not exist or has been deleted',
        vn_id
      );
  end if;

  return new;
end;
$$ language plpgsql;

-- Attach the trigger so that it fires on insert of active subnets:
create trigger check_subnet_virtual_network_ref
  before insert on subnets
  for each row
  when (new.deletion_timestamp = 'epoch')
  execute function check_subnet_virtual_network_ref();

-- Trigger function that validates the virtual_network reference in a security group:
create function check_security_group_virtual_network_ref() returns trigger as $$
declare
  vn_id text;
  found_id text;
begin
  vn_id := new.data->'spec'->>'virtual_network';
  if coalesce(vn_id, '') = '' then
    return new;
  end if;

  select id into found_id
  from virtual_networks
  where id = vn_id
    and deletion_timestamp = 'epoch'
  for share;

  if found_id is null then
    raise exception using
      errcode = 'Z0002',
      message = format(
        'VirtualNetwork ''%s'' does not exist or has been deleted',
        vn_id
      );
  end if;

  return new;
end;
$$ language plpgsql;

-- Attach the trigger so that it fires on insert of active security groups:
create trigger check_security_group_virtual_network_ref
  before insert on security_groups
  for each row
  when (new.deletion_timestamp = 'epoch')
  execute function check_security_group_virtual_network_ref();
