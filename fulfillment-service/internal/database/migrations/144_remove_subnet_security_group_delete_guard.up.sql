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

-- SecurityGroups reference their VirtualNetwork, not an individual Subnet. The accepted unified networking contract
-- guards VirtualNetwork deletion on SecurityGroups, while Subnet deletion is guarded by workloads and catalog policies
-- that reference that Subnet. Keep those reverse-reference checks without making an unrelated SecurityGroup block
-- Subnet deletion.
create or replace function check_subnet_not_in_use() returns trigger as $$
begin
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

  if exists (
    select 1
    from bare_metal_instances
    where deletion_timestamp = 'epoch'
      and data->'spec'->'network_attachments' @>
          jsonb_build_array(jsonb_build_object('subnet', jsonb_build_object('id', old.id)))
  ) then
    raise exception using errcode = 'Z0003',
      message = format('cannot delete subnet ''%s'': it is in use by at least one bare metal instance', old.id);
  end if;

  if exists (
    select 1
    from compute_instance_catalog_items c
    cross join lateral jsonb_array_elements(
      coalesce(c.data->'fields'->'network_attachments'->'locked'->'items', '[]'::jsonb)
      || coalesce(c.data->'fields'->'network_attachments'->'editable'->'default_value'->'items', '[]'::jsonb)
    ) attachment
    where c.deletion_timestamp = 'epoch'
      and attachment->'subnet'->>'id' = old.id
  ) or exists (
    select 1
    from cluster_catalog_items c
    where c.deletion_timestamp = 'epoch'
      and (
        c.data->'fields'->'network_attachment'->'locked'->'subnet'->>'id' = old.id
        or c.data->'fields'->'network_attachment'->'editable'->'default_value'->'subnet'->>'id' = old.id
      )
  ) or exists (
    select 1
    from bare_metal_instance_catalog_items c
    cross join lateral jsonb_array_elements(
      coalesce(c.data->'fields'->'network_attachments'->'locked'->'items', '[]'::jsonb)
      || coalesce(c.data->'fields'->'network_attachments'->'editable'->'default_value'->'items', '[]'::jsonb)
    ) attachment
    where c.deletion_timestamp = 'epoch'
      and attachment->'subnet'->>'id' = old.id
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format('cannot delete subnet ''%s'': it is in use by a catalog policy', old.id);
  end if;

  return new;
end;
$$ language plpgsql;
