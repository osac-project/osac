--
-- Copyright (c) 2026 Red Hat, Inc.
--
-- Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
-- the License. You may obtain a copy of the License at
--
--   http://www.apache.org/licenses/LICENSE-2.0
--
-- Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
-- "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
-- language governing permissions and limitations under the License.
--

-- AddOnOperator references in active ClusterCatalogItems and published operator
-- dependencies are strong dependencies. The traversal is cycle-safe and
-- bounded to the maximum relationship graph supported by Fulfillment orders.
create index if not exists add_on_operators_dependencies_active_idx
  on add_on_operators using gin ((data->'dependencies'))
  where deletion_timestamp = 'epoch'
    and tenant = 'shared'
    and coalesce(project, '') = '';

create or replace function check_add_on_operator_not_in_use() returns trigger as $$
declare
  queue text[] := array[old.id]::text[];
  visited text[] := '{}'::text[];
  catalog_references jsonb := '[]'::jsonb;
  queue_head integer := 1;
  operator_id text;
  operator_name text;
  dependent_id text;
  is_in_use boolean := false;
  max_graph_nodes integer := 1024;
begin
  -- Only soft-deletion and unpublishing require lifecycle protection.
  if not (
    (old.deletion_timestamp = 'epoch' and new.deletion_timestamp <> 'epoch')
    or (
      coalesce(old.data->>'published', 'false') = 'true'
      and coalesce(new.data->>'published', 'false') <> 'true'
    )
  ) then
    return new;
  end if;

  select coalesce(jsonb_agg(reference), '[]'::jsonb)
  into catalog_references
  from cluster_catalog_items catalog_item
  cross join lateral jsonb_array_elements(
    coalesce(catalog_item.data->'fields'->'add_on_operators'->'locked'->'items', '[]'::jsonb)
    || coalesce(catalog_item.data->'fields'->'add_on_operators'->'editable'->'default_value'->'items', '[]'::jsonb)
  ) reference
  where catalog_item.deletion_timestamp = 'epoch';

  -- Walk from the target operator to every active operator that depends on it.
  while queue_head <= coalesce(cardinality(queue), 0) loop
    operator_id := queue[queue_head];
    queue_head := queue_head + 1;

    -- A global visited set prevents cycles and duplicate work through converging paths.
    if operator_id = any(visited) then
      continue;
    end if;
    if cardinality(visited) >= max_graph_nodes then
      raise exception using
        errcode = 'Z0003',
        message = format('cannot modify add-on operator ''%s'': dependency graph exceeds the lifecycle protection limit', old.name);
    end if;
    visited := array_append(visited, operator_id);

    if operator_id = old.id then
      operator_name := old.name;
    else
      select operator.name
      into operator_name
      from add_on_operators operator
      where operator.id = operator_id
        and operator.deletion_timestamp = 'epoch'
        and operator.tenant = 'shared'
        and coalesce(operator.project, '') = '';
    end if;

    -- A catalog reference or a published operator reached through a previous
    -- reverse-dependency step protects this node.
    if exists (
      select 1
      from jsonb_array_elements(catalog_references) reference
      where reference->>'id' = operator_id
        or (coalesce(reference->>'id', '') = '' and reference->>'name' = operator_name)
    ) or exists (
      select 1
      from add_on_operators operator
      where operator.id = operator_id
        and operator.id <> old.id
        and operator.deletion_timestamp = 'epoch'
        and operator.tenant = 'shared'
        and coalesce(operator.project, '') = ''
        and coalesce(operator.data->>'published', 'false') = 'true'
    ) then
      is_in_use := true;
      exit;
    end if;

    -- Find the next reverse-dependency edges: operators that depend on this node.
    for dependent_id in
      select parent.id
      from add_on_operators parent
      where parent.deletion_timestamp = 'epoch'
        and parent.tenant = 'shared'
        and coalesce(parent.project, '') = ''
        and (
          parent.data->'dependencies' @> jsonb_build_array(jsonb_build_object('id', operator_id))
          or parent.data->'dependencies' @> jsonb_build_array(jsonb_build_object('name', operator_name))
        )
    loop
      if not dependent_id = any(visited) and not dependent_id = any(queue) then
        if cardinality(queue) >= max_graph_nodes then
          raise exception using
            errcode = 'Z0003',
            message = format('cannot modify add-on operator ''%s'': dependency graph exceeds the lifecycle protection limit', old.name);
        end if;
        queue := array_append(queue, dependent_id);
      end if;
    end loop;
  end loop;

  if is_in_use then
    raise exception using
      errcode = 'Z0003',
      message = format('cannot modify add-on operator ''%s'': it is in use by an active catalog item or published operator dependency', old.name);
  end if;

  return new;
end;
$$ language plpgsql;

create trigger check_add_on_operator_not_in_use
  before update on add_on_operators
  for each row
  execute function check_add_on_operator_not_in_use();
