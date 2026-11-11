--
-- Copyright (c) 2026 Red Hat, Inc.
--
-- Licensed under the Apache License, Version 2.0 (the "License");
-- you may not use this file except in compliance with the License.
-- You may obtain a copy of the License at
--
--   http://www.apache.org/licenses/LICENSE-2.0
--
-- Unless required by applicable law or agreed to in writing, software
-- distributed under the License is distributed on an "AS IS" BASIS,
-- WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
-- See the License for the specific language governing permissions and
-- limitations under the License.
--

-- Node-set selected ClusterVersions are stored in server-owned Cluster
-- status. Keep delete protection keyed by the canonical reference ID.
create or replace function check_cluster_version_not_in_use() returns trigger as $$
begin
  if exists (
    select 1
    from clusters c
    where c.deletion_timestamp = 'epoch'
      and (
        c.data #>> '{spec,version,id}' = old.id
        or exists (
          select 1
          from jsonb_each(
            case when jsonb_typeof(c.data #> '{status,node_sets}') = 'object'
              then c.data #> '{status,node_sets}'
              else '{}'::jsonb
            end
          ) as ns(name, node_set)
          where ns.node_set #>> '{version,id}' = old.id
        )
      )
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format('cannot delete cluster version ''%s'': it is in use by at least one cluster', old.name);
  end if;

  if exists (
    select 1
    from cluster_templates t
    where t.deletion_timestamp = 'epoch'
      and t.data #>> '{spec_defaults,version,id}' = old.id
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format('cannot delete cluster version ''%s'': it is in use by at least one cluster template', old.name);
  end if;

  if exists (
    select 1 from cluster_catalog_items c
    where c.deletion_timestamp = 'epoch'
      and (
        c.data #>> '{fields,version,locked,id}' = old.id
        or c.data #>> '{fields,version,editable,default_value,id}' = old.id
      )
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format('cannot delete cluster version ''%s'': it is in use by at least one cluster catalog item', old.name);
  end if;

  return new;
end;
$$ language plpgsql;
