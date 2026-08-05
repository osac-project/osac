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

-- This migration adds referential integrity between subnets and clusters for the network_attachment field:
--
-- 1. Replaces check_subnet_not_in_use() to also check clusters (previously only checked compute_instances).
--
-- 2. A BEFORE INSERT trigger on clusters that prevents creating a cluster that references a subnet that does not exist
--    or has been soft-deleted. This trigger uses FOR SHARE to acquire a shared lock on the subnet row, which conflicts
--    with the exclusive lock held by a concurrent subnet soft-delete. The trigger only fires on INSERT because the subnet
--    reference in network_attachment is immutable (enforced by the application).

-- Index to speed up the JSONB text extraction used by the trigger:
create index clusters_network_attachment_subnet on clusters
  ((data->'spec'->'network_attachment'->>'subnet'))
  where data->'spec'->'network_attachment'->>'subnet' is not null;

-- Replace check_subnet_not_in_use to also check clusters:
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

  if exists (
    select 1
    from clusters
    where deletion_timestamp = 'epoch'
      and data->'spec'->'network_attachment'->>'subnet' = old.id
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete subnet ''%s'': it is in use by at least one cluster',
        old.id
      );
  end if;

  return new;
end;
$$ language plpgsql;

-- Trigger function that validates the subnet reference in a cluster's network_attachment:
create function check_cluster_subnet_refs() returns trigger as $$
declare
  subnet_id text;
  found_id text;
begin
  subnet_id := new.data->'spec'->'network_attachment'->>'subnet';

  if coalesce(subnet_id, '') = '' then
    return new;
  end if;

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

  return new;
end;
$$ language plpgsql;

-- Attach the trigger so that it fires on insert of active clusters:
create trigger check_cluster_subnet_refs
  before insert on clusters
  for each row
  when (new.deletion_timestamp = 'epoch')
  execute function check_cluster_subnet_refs();
