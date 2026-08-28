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

-- Networking dependency guards:
--
-- 1. Extends check_subnet_not_in_use for BareMetalInstance network_attachments (reverse ref).
-- 2. Adds insert-time validation of BareMetalInstance subnet references (forward ref).
-- 3. Adds insert-time validation of ExternalIPAttachment target references (forward ref).
--
-- These fill gaps identified by audit against the existing CI/Cluster/ExternalIP patterns.

-- =============================================================================
-- 1. Extend check_subnet_not_in_use for BareMetalInstance network_attachments
--    (matches existing CI and Cluster checks from migrations 52/87/103)
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

-- =============================================================================
-- 2. Validate BareMetalInstance subnet references on insert
--    (matches the CI pattern from migration 52 — dropped by migration 90)
-- =============================================================================

create function check_bmi_subnet_refs() returns trigger as $$
declare
  attachment jsonb;
  subnet_id text;
  found_id text;
begin
  for attachment in select jsonb_array_elements(coalesce(new.data->'spec'->'network_attachments', '[]'::jsonb))
  loop
    subnet_id := attachment->'subnet'->>'id';
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
            'Subnet ''%s'' does not exist or has been deleted',
            subnet_id
          );
      end if;
    end if;
  end loop;

  return new;
end;
$$ language plpgsql;

create trigger check_bmi_subnet_refs
  before insert on bare_metal_instances
  for each row
  when (new.deletion_timestamp = 'epoch')
  execute function check_bmi_subnet_refs();

-- =============================================================================
-- 3. Validate ExternalIPAttachment target reference on insert
--    (matches the ExternalIP ref pattern from migration 102)
-- =============================================================================

create function check_eipa_target_ref_exists() returns trigger as $$
declare
  target_id text;
  found_id text;
begin
  -- Check baremetal_instance target
  target_id := new.data->'spec'->'baremetal_instance'->>'id';
  if coalesce(target_id, '') != '' then
    select id into found_id
    from bare_metal_instances
    where id = target_id
      and deletion_timestamp = 'epoch'
    for share;

    if found_id is null then
      raise exception using
        errcode = 'Z0002',
        message = format(
          'BareMetalInstance ''%s'' does not exist or has been deleted',
          target_id
        );
    end if;
    return new;
  end if;

  -- Check compute_instance target
  target_id := new.data->'spec'->'compute_instance'->>'id';
  if coalesce(target_id, '') != '' then
    select id into found_id
    from compute_instances
    where id = target_id
      and deletion_timestamp = 'epoch'
    for share;

    if found_id is null then
      raise exception using
        errcode = 'Z0002',
        message = format(
          'ComputeInstance ''%s'' does not exist or has been deleted',
          target_id
        );
    end if;
    return new;
  end if;

  -- Check cluster target
  target_id := new.data->'spec'->'cluster'->>'id';
  if coalesce(target_id, '') != '' then
    select id into found_id
    from clusters
    where id = target_id
      and deletion_timestamp = 'epoch'
    for share;

    if found_id is null then
      raise exception using
        errcode = 'Z0002',
        message = format(
          'Cluster ''%s'' does not exist or has been deleted',
          target_id
        );
    end if;
    return new;
  end if;

  return new;
end;
$$ language plpgsql;

create trigger check_eipa_target_ref_exists
  before insert on external_ip_attachments
  for each row
  when (new.deletion_timestamp = 'epoch')
  execute function check_eipa_target_ref_exists();
