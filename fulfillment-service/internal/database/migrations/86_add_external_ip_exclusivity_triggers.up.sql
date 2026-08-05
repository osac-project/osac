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

-- This migration enforces that an ExternalIP can only be consumed by one resource at a time.
-- Currently the known consumers are ExternalIPAttachment and NATGateway.
--
-- A single trigger function checks all consumer tables. Because BEFORE INSERT fires before the new
-- row is visible, checking the inserting table works correctly for same-table dedup too. Adding a
-- new consumer type requires only adding its table to the query and attaching the trigger.
--
-- The trigger acquires FOR UPDATE on the external_ips row to serialize concurrent creates across
-- all consumer tables, eliminating TOCTOU races.

create function check_external_ip_available() returns trigger as $$
declare
  eip_id text;
  locked_id text;
  consumer_id text;
  consumer_type text;
begin
  eip_id := new.data->'spec'->>'external_ip';
  if coalesce(eip_id, '') = '' then
    return new;
  end if;

  select id into locked_id
  from external_ips
  where id = eip_id
  for update;

  if locked_id is null then
    return new;
  end if;

  select id, type into consumer_id, consumer_type
  from (
    select id, 'ExternalIPAttachment' as type
    from external_ip_attachments
    where deletion_timestamp = 'epoch'
      and data->'spec'->>'external_ip' = eip_id
    union all
    select id, 'NATGateway' as type
    from nat_gateways
    where deletion_timestamp = 'epoch'
      and data->'spec'->>'external_ip' = eip_id
  ) consumers
  limit 1;

  if consumer_id is not null then
    raise exception using
      errcode = 'Z0004',
      message = format(
        'ExternalIP ''%s'' is already in use by %s ''%s''',
        eip_id, consumer_type, consumer_id
      );
  end if;

  return new;
end;
$$ language plpgsql;

create trigger check_external_ip_available
  before insert on external_ip_attachments
  for each row
  when (new.deletion_timestamp = 'epoch')
  execute function check_external_ip_available();

create trigger check_external_ip_available
  before insert on nat_gateways
  for each row
  when (new.deletion_timestamp = 'epoch')
  execute function check_external_ip_available();
