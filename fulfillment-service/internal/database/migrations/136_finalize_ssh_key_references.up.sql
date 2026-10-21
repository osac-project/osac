--
-- Copyright (c) 2026 Red Hat, Inc.
--
-- Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
-- License. You may obtain a copy of the License at
--
--   http://www.apache.org/licenses/LICENSE-2.0
--
-- Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
-- "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
-- specific language governing permissions and limitations under the License.
--

-- Finalize the SSH-key Secret reference migration (OSAC-5429): remove the
-- deprecated inline key material from stored resources and close the
-- create/reference-change race for Cluster SSH-key references.
--
-- Legacy inline values are dropped without conversion: no Secret is
-- synthesized on a tenant's behalf, and an existing Cluster that needs later
-- provisioning must select a registered key through spec.ssh_key.
update bare_metal_instances
  set data = data - 'spec' || jsonb_build_object(
    'spec', (data->'spec') - 'ssh_public_key'
  )
  where data ? 'spec' and data->'spec' ? 'ssh_public_key';

update clusters
  set data = data - 'spec' || jsonb_build_object(
    'spec', (data->'spec') - 'ssh_public_key'
  )
  where data ? 'spec' and data->'spec' ? 'ssh_public_key';

update bare_metal_instance_catalog_items
  set data = data - 'fields' || jsonb_build_object(
    'fields', (data->'fields') - 'ssh_public_key'
  )
  where data ? 'fields' and data->'fields' ? 'ssh_public_key';

update cluster_catalog_items
  set data = data - 'fields' || jsonb_build_object(
    'fields', (data->'fields') - 'ssh_public_key'
  )
  where data ? 'fields' and data->'fields' ? 'ssh_public_key';

-- Cluster stores only the canonical Secret ID in spec.ssh_key. The reference
-- stays mutable so a Cluster can switch away from a deleted key, so this
-- trigger closes the insertion and explicit-replacement race only: a newly
-- selected non-empty ID must reference a live Secret. Same-reference updates,
-- clearing, and status changes are never blocked. Deliberately do not add the
-- Cluster path to check_secret_not_in_use: deleting a referenced SSH-key
-- Secret is allowed, and Cluster updates and deletions must stay unblocked so
-- reconciliation can report the resulting resolution failure.
create function check_cluster_ssh_key_secret_ref_exists() returns trigger as $$
declare
  new_id text;
  old_id text;
  found_id text;
begin
  new_id := new.data->'spec'->'ssh_key'->>'id';
  old_id := old.data->'spec'->'ssh_key'->>'id';

  if coalesce(new_id, '') = '' or new_id = old_id then
    return new;
  end if;

  select id into found_id
  from secrets
  where id = new_id
    and tenant = new.tenant
    and deletion_timestamp = 'epoch'
  for share;

  if found_id is null then
    raise exception using
      errcode = 'Z0002',
      message = format('Secret ''%s'' does not exist, does not belong to the tenant, or has been deleted', new_id);
  end if;

  return new;
end;
$$ language plpgsql;

create trigger check_cluster_ssh_key_secret_ref_exists
  before insert or update on clusters
  for each row
  when (new.deletion_timestamp = 'epoch')
  execute function check_cluster_ssh_key_secret_ref_exists();
