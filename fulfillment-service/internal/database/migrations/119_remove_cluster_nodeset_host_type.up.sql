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

-- The JSONB column holds proto fields directly. Discard the obsolete node-set
-- key without inferring a BareMetalInstanceType from a HostType.
create function strip_cluster_nodeset_host_type(node_sets jsonb) returns jsonb as $$
  select coalesce(jsonb_object_agg(key, case when jsonb_typeof(value) = 'object' then value - 'host_type' else value end), '{}'::jsonb)
  from jsonb_each(node_sets);
$$ language sql immutable strict;

update clusters
set data = jsonb_set(data, '{spec,node_sets}', strip_cluster_nodeset_host_type(data->'spec'->'node_sets'))
where jsonb_typeof(data->'spec'->'node_sets') = 'object'
  and exists (select 1 from jsonb_each(data->'spec'->'node_sets') node where node.value ? 'host_type');

update clusters
set data = jsonb_set(data, '{status,node_sets}', strip_cluster_nodeset_host_type(data->'status'->'node_sets'))
where jsonb_typeof(data->'status'->'node_sets') = 'object'
  and exists (select 1 from jsonb_each(data->'status'->'node_sets') node where node.value ? 'host_type');

update cluster_templates
set data = jsonb_set(data, '{node_sets}', strip_cluster_nodeset_host_type(data->'node_sets'))
where jsonb_typeof(data->'node_sets') = 'object'
  and exists (select 1 from jsonb_each(data->'node_sets') node where node.value ? 'host_type');

update cluster_catalog_items
set data = jsonb_set(data, '{fields,node_sets,locked,items}',
  strip_cluster_nodeset_host_type(data->'fields'->'node_sets'->'locked'->'items'))
where jsonb_typeof(data->'fields'->'node_sets'->'locked'->'items') = 'object'
  and exists (select 1 from jsonb_each(data->'fields'->'node_sets'->'locked'->'items') node where node.value ? 'host_type');

update cluster_catalog_items
set data = jsonb_set(data, '{fields,node_sets,editable,default_value,items}',
  strip_cluster_nodeset_host_type(data->'fields'->'node_sets'->'editable'->'default_value'->'items'))
where jsonb_typeof(data->'fields'->'node_sets'->'editable'->'default_value'->'items') = 'object'
  and exists (select 1 from jsonb_each(data->'fields'->'node_sets'->'editable'->'default_value'->'items') node where node.value ? 'host_type');

drop function strip_cluster_nodeset_host_type(jsonb);

-- HostType is still used by bare-metal instance templates, but no longer by clusters.
create or replace function check_host_type_not_in_use() returns trigger as $$
begin
  if exists (
    select 1 from bare_metal_instance_templates t
    where t.deletion_timestamp = 'epoch' and t.data->>'host_type' = old.id
  ) then
    raise exception using errcode = 'Z0003', message = format('cannot delete host type ''%s'': it is in use', old.id);
  end if;
  return new;
end;
$$ language plpgsql;

-- All active node-set references now point to BareMetalInstanceTypes.
create or replace function check_bare_metal_instance_type_not_in_use() returns trigger as $$
begin
  if exists (select 1 from bare_metal_instances where deletion_timestamp = 'epoch' and data->'spec'->'instance_type'->>'id' = old.id)
    or exists (select 1 from bare_metal_instance_catalog_items where deletion_timestamp = 'epoch' and (
      data->'fields'->'instance_type'->'locked'->>'id' = old.id
      or data->'fields'->'instance_type'->'editable'->'default_value'->>'id' = old.id
    ))
    or exists (
      select 1 from clusters c cross join lateral jsonb_each(c.data->'spec'->'node_sets') node
      where c.deletion_timestamp = 'epoch' and node.value->'baremetal_instance_type'->>'id' = old.id
    )
    or exists (
      select 1 from cluster_templates t cross join lateral jsonb_each(t.data->'node_sets') node
      where t.deletion_timestamp = 'epoch' and node.value->'baremetal_instance_type'->>'id' = old.id
    )
    or exists (
      select 1 from cluster_catalog_items c cross join lateral jsonb_each(c.data->'fields'->'node_sets'->'locked'->'items') node
      where c.deletion_timestamp = 'epoch' and node.value->'baremetal_instance_type'->>'id' = old.id
    )
    or exists (
      select 1 from cluster_catalog_items c cross join lateral jsonb_each(c.data->'fields'->'node_sets'->'editable'->'default_value'->'items') node
      where c.deletion_timestamp = 'epoch' and node.value->'baremetal_instance_type'->>'id' = old.id
    ) then
    raise exception using errcode = 'Z0003', message = format('cannot delete bare metal instance type ''%s'': it is in use', old.id);
  end if;
  return new;
end;
$$ language plpgsql;
