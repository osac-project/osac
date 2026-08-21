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

-- Extend check_subnet_not_in_use to prevent deleting a Subnet while active SecurityGroups
-- still reference the same VirtualNetwork. SG ACL rules are fanned out per-subnet, so
-- deleting a subnet while SGs exist would orphan ACL rules in the fabric manager.

create or replace function check_subnet_not_in_use() returns trigger as $$
declare
  vn_id text;
  sg_count bigint;
begin
  -- Existing check: compute instance references (from migration 90)
  if exists (
    select 1
    from compute_instances
    where deletion_timestamp = 'epoch'
      and data->'spec'->'network_attachments' @>
          jsonb_build_array(jsonb_build_object('subnet', jsonb_build_object('id', old.id)))
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete subnet ''%s'': it is in use by at least one compute instance',
        old.id
      );
  end if;

  -- Existing check: cluster references (from migration 90)
  if exists (
    select 1
    from clusters
    where deletion_timestamp = 'epoch'
      and data->'spec'->'network_attachment'->'subnet'->>'id' = old.id
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete subnet ''%s'': it is in use by at least one cluster',
        old.id
      );
  end if;

  -- New check: security groups on the same VirtualNetwork
  vn_id := old.data->'spec'->'virtual_network'->>'id';
  if vn_id is not null then
    select count(*) into sg_count
    from active_security_groups a
    join security_groups s on s.id = a.id
    where s.data->'spec'->'virtual_network'->>'id' = vn_id;

    if sg_count > 0 then
      raise exception using
        errcode = 'Z0003',
        message = format(
          'cannot delete Subnet ''%s'': %s SecurityGroup(s) still reference its VirtualNetwork',
          old.id, sg_count
        );
    end if;
  end if;

  return new;
end;
$$ language plpgsql;
