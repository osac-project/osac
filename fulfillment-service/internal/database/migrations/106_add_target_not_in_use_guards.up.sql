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

-- Adds DB-level archival guards for resources that ExternalIPAttachments reference as targets.
-- Without these, a target (BMI, CI, ClusterOrder) can be archived (hard-deleted from the main
-- table) while an ExternalIPAttachment still references it, causing the fulfillment-controller
-- to fail in a loop when the reference validator can't find the archived target.
--
-- These triggers fire BEFORE DELETE (the archive step), not before soft-delete — the target
-- must be allowed to enter the deleting state so its cleanup controller can run. The guard
-- only prevents the final removal from the main table while children still reference it.
--
-- Also extends check_subnet_not_in_use to cover BareMetalInstance network_attachments.

-- =============================================================================
-- 1. Prevent archiving a BareMetalInstance while ExternalIPAttachments target it
-- =============================================================================

create function check_bare_metal_instance_not_in_use() returns trigger as $$
begin
  if exists (
    select 1
    from active_external_ip_attachments a
    join external_ip_attachments e on e.id = a.id
    where e.data->'spec'->'baremetal_instance'->>'id' = old.id
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot archive BareMetalInstance ''%s'': it is referenced by at least one active ExternalIPAttachment',
        old.id
      );
  end if;

  return old;
end;
$$ language plpgsql;

create trigger check_bare_metal_instance_not_in_use
  before delete on bare_metal_instances
  for each row
  execute function check_bare_metal_instance_not_in_use();

-- =============================================================================
-- 2. Prevent archiving a ComputeInstance while ExternalIPAttachments target it
-- =============================================================================

create function check_compute_instance_not_in_use() returns trigger as $$
begin
  if exists (
    select 1
    from active_external_ip_attachments a
    join external_ip_attachments e on e.id = a.id
    where e.data->'spec'->'compute_instance'->>'id' = old.id
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot archive ComputeInstance ''%s'': it is referenced by at least one active ExternalIPAttachment',
        old.id
      );
  end if;

  return old;
end;
$$ language plpgsql;

create trigger check_compute_instance_not_in_use
  before delete on compute_instances
  for each row
  execute function check_compute_instance_not_in_use();

-- =============================================================================
-- 3. Prevent archiving a ClusterOrder while ExternalIPAttachments target it
-- =============================================================================

create function check_cluster_not_in_use() returns trigger as $$
begin
  if exists (
    select 1
    from active_external_ip_attachments a
    join external_ip_attachments e on e.id = a.id
    where e.data->'spec'->'cluster'->>'id' = old.id
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot archive Cluster ''%s'': it is referenced by at least one active ExternalIPAttachment',
        old.id
      );
  end if;

  return old;
end;
$$ language plpgsql;

create trigger check_cluster_not_in_use
  before delete on clusters
  for each row
  execute function check_cluster_not_in_use();

-- =============================================================================
-- 4. Extend check_subnet_not_in_use to cover BareMetalInstance network_attachments
-- =============================================================================

create or replace function check_subnet_not_in_use() returns trigger as $$
declare
  vn_id text;
  sg_count bigint;
begin
  -- Existing check: compute instance references
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

  -- Existing check: cluster references
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

  -- New check: bare metal instance references
  if exists (
    select 1
    from bare_metal_instances
    where deletion_timestamp = 'epoch'
      and data->'spec'->'network_attachments' @>
          jsonb_build_array(jsonb_build_object('subnet', jsonb_build_object('id', old.id)))
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete subnet ''%s'': it is in use by at least one bare metal instance',
        old.id
      );
  end if;

  -- Existing check: security groups on the same VirtualNetwork
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
