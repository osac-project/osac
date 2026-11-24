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

-- Extend Secret delete protection (migration 111) so Secrets referenced by an
-- active DiskImage.spec.pull_secret cannot be soft-deleted. Also take a FOR SHARE
-- lock when a DiskImage write sets a pull_secret id so concurrent Secret deletes
-- cannot race past validation.

create index disk_images_pull_secret on disk_images
  ((data->'spec'->'pull_secret'->>'id'))
  where data->'spec'->'pull_secret'->>'id' is not null;

create or replace function check_secret_ref_exists() returns trigger as $$
declare
  secret_id text;
  old_secret_id text;
  found_id text;
begin
  secret_id := case tg_table_name
    when 'clusters' then new.data->'spec'->'pull_secret_secret'->>'id'
    when 'cluster_templates' then new.data->'spec_defaults'->'pull_secret_secret'->>'id'
    when 'hubs' then new.data->'spec'->'kubeconfig_secret'->>'id'
    when 'identity_providers' then new.data->'spec'->'open_id_connect'->'client_secret_secret'->>'id'
    when 'storage_backends' then new.data->'spec'->'credentials'->'password_secret'->>'id'
    when 'disk_images' then new.data->'spec'->'pull_secret'->>'id'
  end;

  if tg_op = 'UPDATE' and old.deletion_timestamp = 'epoch' then
    old_secret_id := case tg_table_name
      when 'clusters' then old.data->'spec'->'pull_secret_secret'->>'id'
      when 'cluster_templates' then old.data->'spec_defaults'->'pull_secret_secret'->>'id'
      when 'hubs' then old.data->'spec'->'kubeconfig_secret'->>'id'
      when 'identity_providers' then old.data->'spec'->'open_id_connect'->'client_secret_secret'->>'id'
      when 'storage_backends' then old.data->'spec'->'credentials'->'password_secret'->>'id'
      when 'disk_images' then old.data->'spec'->'pull_secret'->>'id'
    end;

    if secret_id is not distinct from old_secret_id then
      return new;
    end if;
  end if;

  if coalesce(secret_id, '') = '' then
    return new;
  end if;

  select id into found_id
  from secrets
  where id = secret_id
    and deletion_timestamp = 'epoch'
  for share;

  if found_id is null then
    raise exception using
      errcode = 'Z0002',
      message = format('Secret ''%s'' does not exist or has been deleted', secret_id);
  end if;

  return new;
end;
$$ language plpgsql;

create trigger check_secret_ref_exists
  before insert or update of data, deletion_timestamp on disk_images
  for each row
  when (new.deletion_timestamp = 'epoch')
  execute function check_secret_ref_exists();

create or replace function check_secret_not_in_use() returns trigger as $$
begin
  -- Lock the Secret row before dependent lookups so this soft-delete conflicts
  -- with FOR SHARE taken by check_secret_ref_exists on concurrent writers.
  perform 1 from secrets where id = old.id for update;

  -- Catalog and DiskImage dependencies are strong for every backend (including
  -- HUB); retain the legacy HUB exemption only for the resource checks below.
  if exists (
    select 1 from cluster_catalog_items c
    where c.deletion_timestamp = 'epoch'
      and (
        c.data->'fields'->'pull_secret_secret'->'locked'->>'id' = old.id
        or c.data->'fields'->'pull_secret_secret'->'editable'->'default_value'->>'id' = old.id
      )
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format('cannot delete Secret ''%s'': it is in use by a catalog policy', old.id);
  end if;

  if exists (
    select 1
    from active_disk_images a
    join disk_images d on d.id = a.id
    where d.data->'spec'->'pull_secret'->>'id' = old.id
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format('cannot delete Secret ''%s'': it is in use by at least one active resource', old.id);
  end if;

  if old.data->>'backend' = 'SECRET_BACKEND_HUB' then
    return new;
  end if;

  if exists (
    select 1
    from active_clusters a
    join clusters c on c.id = a.id
    where c.data->'spec'->'pull_secret_secret'->>'id' = old.id
  ) or exists (
    select 1
    from active_cluster_templates a
    join cluster_templates c on c.id = a.id
    where c.data->'spec_defaults'->'pull_secret_secret'->>'id' = old.id
  ) or exists (
    select 1
    from active_hubs a
    join hubs h on h.id = a.id
    where h.data->'spec'->'kubeconfig_secret'->>'id' = old.id
  ) or exists (
    select 1
    from active_identity_providers a
    join identity_providers i on i.id = a.id
    where i.data->'spec'->'open_id_connect'->'client_secret_secret'->>'id' = old.id
  ) or exists (
    select 1
    from active_storage_backends a
    join storage_backends s on s.id = a.id
    where s.data->'spec'->'credentials'->'password_secret'->>'id' = old.id
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format('cannot delete Secret ''%s'': it is in use by at least one active resource or catalog policy', old.id);
  end if;

  return new;
end;
$$ language plpgsql;
