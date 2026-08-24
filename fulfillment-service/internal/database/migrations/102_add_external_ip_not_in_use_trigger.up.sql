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

-- This migration adds referential integrity guards for ExternalIP, ExternalIPPool, and their consumers
-- (ExternalIPAttachment, NATGateway). Follows the same patterns as migrations 52 (subnet guards) and
-- 86 (ExternalIP exclusivity).

-- =============================================================================
-- 1. Prevent deleting an ExternalIP while an active consumer still references it
-- =============================================================================

-- ExternalIPAttachment stores spec.external_ip as an object {"id": "uuid"} while
-- NATGateway stores it as a plain string "uuid". Use coalesce to handle both.

create function check_external_ip_not_in_use() returns trigger as $$
begin
  if exists (
    select 1
    from active_external_ip_attachments a
    join external_ip_attachments e on e.id = a.id
    where coalesce(e.data->'spec'->'external_ip'->>'id', e.data->'spec'->>'external_ip') = old.id
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete ExternalIP ''%s'': it is in use by at least one ExternalIPAttachment',
        old.id
      );
  end if;

  if exists (
    select 1
    from active_nat_gateways a
    join nat_gateways n on n.id = a.id
    where coalesce(n.data->'spec'->'external_ip'->>'id', n.data->'spec'->>'external_ip') = old.id
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete ExternalIP ''%s'': it is in use by at least one NATGateway',
        old.id
      );
  end if;

  return new;
end;
$$ language plpgsql;

create trigger check_external_ip_not_in_use
  before update on external_ips
  for each row
  when (old.deletion_timestamp = 'epoch' and new.deletion_timestamp != 'epoch')
  execute function check_external_ip_not_in_use();

-- =============================================================================
-- 2. Prevent deleting an ExternalIPPool while active ExternalIPs reference it
-- =============================================================================

create function check_external_ip_pool_not_in_use() returns trigger as $$
begin
  if exists (
    select 1
    from active_external_ips a
    join external_ips e on e.id = a.id
    where e.data->'spec'->'pool'->>'id' = old.id
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete ExternalIPPool ''%s'': it is in use by at least one ExternalIP',
        old.id
      );
  end if;

  return new;
end;
$$ language plpgsql;

create trigger check_external_ip_pool_not_in_use
  before update on external_ip_pools
  for each row
  when (old.deletion_timestamp = 'epoch' and new.deletion_timestamp != 'epoch')
  execute function check_external_ip_pool_not_in_use();

-- =============================================================================
-- 3. Validate ExternalIP ref exists when creating ExternalIPAttachment/NATGateway
--    Uses FOR SHARE to serialize against concurrent ExternalIP soft-deletes.
-- =============================================================================

create function check_external_ip_ref_exists() returns trigger as $$
declare
  eip_id text;
  found_id text;
begin
  eip_id := coalesce(new.data->'spec'->'external_ip'->>'id', new.data->'spec'->>'external_ip');
  if coalesce(eip_id, '') = '' then
    return new;
  end if;

  select id into found_id
  from external_ips
  where id = eip_id
    and deletion_timestamp = 'epoch'
  for share;

  if found_id is null then
    raise exception using
      errcode = 'Z0002',
      message = format(
        'ExternalIP ''%s'' does not exist or has been deleted',
        eip_id
      );
  end if;

  return new;
end;
$$ language plpgsql;

create trigger check_external_ip_ref_exists
  before insert on external_ip_attachments
  for each row
  when (new.deletion_timestamp = 'epoch')
  execute function check_external_ip_ref_exists();

create trigger check_external_ip_ref_exists
  before insert on nat_gateways
  for each row
  when (new.deletion_timestamp = 'epoch')
  execute function check_external_ip_ref_exists();

-- =============================================================================
-- 4. Validate ExternalIPPool ref exists when creating an ExternalIP
--    Uses FOR SHARE to serialize against concurrent pool soft-deletes.
-- =============================================================================

create function check_external_ip_pool_ref_exists() returns trigger as $$
declare
  pool_id text;
  found_id text;
begin
  pool_id := new.data->'spec'->'pool'->>'id';
  if coalesce(pool_id, '') = '' then
    return new;
  end if;

  select id into found_id
  from external_ip_pools
  where id = pool_id
    and deletion_timestamp = 'epoch'
  for share;

  if found_id is null then
    raise exception using
      errcode = 'Z0002',
      message = format(
        'ExternalIPPool ''%s'' does not exist or has been deleted',
        pool_id
      );
  end if;

  return new;
end;
$$ language plpgsql;

create trigger check_external_ip_pool_ref_exists
  before insert on external_ips
  for each row
  when (new.deletion_timestamp = 'epoch')
  execute function check_external_ip_pool_ref_exists();
