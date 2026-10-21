--
-- Copyright (c) 2026 Red Hat, Inc.
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

-- BMaaS stores only the canonical Secret ID in spec.ssh_key. The reference is
-- immutable after creation and the API rejects changes to it, so this trigger
-- only closes the insertion race between Create-time validation and
-- persistence. Deliberately do not add the BMaaS path to
-- check_secret_not_in_use: deleting a referenced SSH-key Secret is allowed,
-- and later instance updates and deletions must stay unblocked so
-- reconciliation can report the resulting resolution failure.
create function check_bmaas_ssh_key_secret_ref_exists() returns trigger as $$
declare
  secret_id text;
  found_id text;
begin
  secret_id := new.data->'spec'->'ssh_key'->>'id';

  if coalesce(secret_id, '') = '' then
    return new;
  end if;

  select id into found_id
  from secrets
  where id = secret_id
    and tenant = new.tenant
    and deletion_timestamp = 'epoch'
  for share;

  if found_id is null then
    raise exception using
      errcode = 'Z0002',
      message = format('Secret ''%s'' does not exist, does not belong to the tenant, or has been deleted', secret_id);
  end if;

  return new;
end;
$$ language plpgsql;

create trigger check_bmaas_ssh_key_secret_ref_exists
  before insert on bare_metal_instances
  for each row
  when (new.deletion_timestamp = 'epoch')
  execute function check_bmaas_ssh_key_secret_ref_exists();
