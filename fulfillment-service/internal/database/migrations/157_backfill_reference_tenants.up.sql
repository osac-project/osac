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

-- Convert selectors in every resource data column, including archived rows and the generic objects table. The
-- recursive walk covers optional, repeated, and map-contained messages. A full-reference field-name allowlist keeps
-- arbitrary Any/Value payloads and generic JSON from being mistaken for references.
-- `changes` stores snapshots of complete resource rows, so migrate its embedded data as well. The production rollout
-- must drain Task 3 writers before this migration runs; this migration runs at service startup before that instance
-- begins serving requests.

create function osac_5424_is_full_reference_field(field_name text)
returns boolean as $$
  select field_name = any(array[
    'add_on_operators', 'baremetal_instance_type', 'catalog_item', 'default_value', 'disk_image',
    'instance_type', 'items', 'locked', 'pool', 'reference', 'role', 'ssh_key', 'template', 'users', 'version'
  ]::text[])
$$ language sql immutable;

create function osac_5424_rewrite_reference_tenants(value jsonb, owner_tenant text, json_path text, field_name text)
returns jsonb as $$
declare
  result jsonb;
  selected_tenant text;
begin
  -- Any and Value payloads are user-controlled JSON, not protobuf reference messages.
  if field_name in ('default', 'template_parameters') then
    return value;
  end if;

  if jsonb_typeof(value) = 'object' then
    -- Restrict conversion to fields whose protobuf type is one of the full-reference messages. Requiring only
    -- an id/name shape is unsafe because template parameters and generic JSON may contain the same keys.
    if value ? 'shared' and osac_5424_is_full_reference_field(field_name) then
      if (value - 'id' - 'name' - 'project' - 'tenant' - 'shared') <> '{}'::jsonb then
        raise exception 'unrecognized shared full-reference fields at %', json_path;
      end if;
      if jsonb_typeof(value->'shared') <> 'boolean' then
        raise exception 'shared selector is not a boolean at %', json_path;
      end if;
      if value ? 'tenant' and jsonb_typeof(value->'tenant') <> 'string' then
        raise exception 'tenant selector is not a string at %', json_path;
      end if;

      selected_tenant := value->>'tenant';
      if value->'shared' = 'true'::jsonb then
        if coalesce(selected_tenant, '') not in ('', 'shared') then
          raise exception 'shared=true conflicts with tenant at %', json_path;
        end if;
        return jsonb_set(value - 'shared', '{tenant}', '"shared"'::jsonb, true);
      end if;

      if coalesce(selected_tenant, '') <> '' and selected_tenant is distinct from owner_tenant then
        raise exception 'shared=false tenant does not match owning tenant at %', json_path;
      end if;
      return value - 'shared';
    end if;

    select coalesce(
      jsonb_object_agg(
        entry.key,
        osac_5424_rewrite_reference_tenants(
          entry.value, owner_tenant, json_path || '.' || entry.key, entry.key
        )
      ),
      '{}'::jsonb
    )
    into result
    from jsonb_each(value) as entry;
    return result;
  elsif jsonb_typeof(value) = 'array' then
    select coalesce(
      jsonb_agg(
        osac_5424_rewrite_reference_tenants(
          item.value, owner_tenant, json_path || '[' || (item.ordinality - 1) || ']', field_name
        )
        order by item.ordinality
      ),
      '[]'::jsonb
    )
    into result
    from jsonb_array_elements(value) with ordinality as item(value, ordinality);
    return result;
  end if;

  return value;
end;
$$ language plpgsql;

create function osac_5424_has_shared_reference(value jsonb, field_name text)
returns boolean as $$
declare
  result boolean;
begin
  if field_name in ('default', 'template_parameters') then
    return false;
  end if;

  if jsonb_typeof(value) = 'object' then
    if value ? 'shared' and osac_5424_is_full_reference_field(field_name) then
      return true;
    end if;
    select exists (
      select 1
      from jsonb_each(value) as entry
      where osac_5424_has_shared_reference(entry.value, entry.key)
    ) into result;
    return result;
  elsif jsonb_typeof(value) = 'array' then
    select exists (
      select 1
      from jsonb_array_elements(value) as item
      where osac_5424_has_shared_reference(item.value, field_name)
    ) into result;
    return result;
  end if;
  return false;
end;
$$ language plpgsql;

do $$
declare
  target record;
  affected_id text;
begin
  for target in
    select
      n.nspname as schema_name,
      c.relname as table_name,
      c.relname = 'changes' as is_change_log,
      exists (
        select 1 from pg_catalog.pg_trigger t
        where t.tgrelid = c.oid and t.tgname = 'enqueue_change' and not t.tgisinternal
      ) as has_change_trigger,
      exists (
        select 1 from pg_catalog.pg_trigger t
        where t.tgrelid = c.oid and t.tgname = 'enforce_metering_fields' and not t.tgisinternal
      ) as has_metering_trigger
    from pg_catalog.pg_class c
    join pg_catalog.pg_namespace n on n.oid = c.relnamespace
    where n.nspname = 'public'
      and c.relkind in ('r', 'p')
      and exists (
        select 1 from pg_catalog.pg_attribute a
        where a.attrelid = c.oid and a.attname = 'data' and a.atttypid = 'jsonb'::regtype
          and not a.attisdropped
      )
      and (
        c.relname = 'changes'
        or exists (
          select 1 from pg_catalog.pg_attribute a
          where a.attrelid = c.oid and a.attname = 'tenant' and not a.attisdropped
        )
      )
    order by c.relname
  loop
    if target.has_change_trigger then
      execute format('alter table %I.%I disable trigger enqueue_change', target.schema_name, target.table_name);
    end if;
    if target.has_metering_trigger then
      execute format('alter table %I.%I disable trigger enforce_metering_fields', target.schema_name, target.table_name);
    end if;

    -- The selector predicate finds every row needing conversion. Verify converted rows from RETURNING in the same
    -- statement, so startup does not perform a second full-table scan.
    affected_id := null;
    if target.is_change_log then
      execute format(
        'with updated as (
           update %I.%I
           set data = osac_5424_rewrite_reference_tenants(data, coalesce(data->>''tenant'', ''''), %L, '''')
           where osac_5424_has_shared_reference(data, '''')
           returning id::text, osac_5424_has_shared_reference(data, '''') as has_shared
         )
         select id from updated where has_shared limit 1',
        target.schema_name, target.table_name, format('%I.%I:$', target.schema_name, target.table_name)
      ) into affected_id;
    else
      execute format(
        'with updated as (
           update %I.%I
           set data = osac_5424_rewrite_reference_tenants(data, tenant, %L, '''')
           where osac_5424_has_shared_reference(data, '''')
           returning id::text, osac_5424_has_shared_reference(data, '''') as has_shared
         )
         select id from updated where has_shared limit 1',
        target.schema_name, target.table_name, format('%I.%I:$', target.schema_name, target.table_name)
      ) into affected_id;
    end if;

    if target.has_change_trigger then
      execute format('alter table %I.%I enable trigger enqueue_change', target.schema_name, target.table_name);
    end if;
    if target.has_metering_trigger then
      execute format('alter table %I.%I enable trigger enforce_metering_fields', target.schema_name, target.table_name);
    end if;

    if affected_id is not null then
      raise exception 'shared full-reference selector remains in %.%',
        target.schema_name, target.table_name;
    end if;
  end loop;
end;
$$;

drop function osac_5424_has_shared_reference(jsonb, text);
drop function osac_5424_rewrite_reference_tenants(jsonb, text, text, text);
drop function osac_5424_is_full_reference_field(text);
