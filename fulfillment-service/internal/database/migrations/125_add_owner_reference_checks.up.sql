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

-- This migration enforces the owner-reference attribution contract on the resources that record
-- one in their status. Today that is one case: a cluster's workers, BareMetalInstances whose
-- status.cluster is a ClusterLocalReference naming the cluster they serve for their whole life.
--
-- 1. An owner reference and the node set it serves are set once: any update that changes either
--    is rejected, including clearing them.
--
-- 2. An owner reference must target a live resource in the same tenant and project. The target is
--    resolved through the table mapping below and locked FOR SHARE so a concurrent soft delete of
--    the target either fails this check or is itself rejected by the not-in-use guard below.
--
-- 3. An owner finishes deleting only after everything it owns is gone: archiving (hard delete of)
--    a referenced resource is rejected while any row still carries an owner reference to it.
--
-- The checks are generic: the mappings below select which tables carry references, where each
-- reference lives in the row's data, and which tables are referenced; the triggers are vacuous
-- for rows without an owner reference, and CheckSchema fails a mapped table that is missing
-- its triggers. A future resource that the system creates for one other resource extends the
-- mappings (and the registry in database_tool.go) and gets the same checks.

-- Maps a table that records an owner reference to the table holding the referenced resource.
create or replace function owner_reference_target_table(source_table text) returns text as $$
  select case source_table
    when 'bare_metal_instances' then 'clusters'
  end;
$$ language sql immutable;

-- Maps a referenced table to the tables that hold owner references to it. Extend together with
-- owner_reference_target_table.
create or replace function owner_reference_source_tables(target_table text) returns text[] as $$
  select case target_table
    when 'clusters' then array['bare_metal_instances']
  end;
$$ language sql immutable;

-- The JSON path of the owner reference in the recording table's data column, in proto field
-- path syntax. Extend together with owner_reference_target_table.
create or replace function owner_reference_path(source_table text) returns text as $$
  select case source_table
    when 'bare_metal_instances' then 'status.cluster'
  end;
$$ language sql immutable;

-- The JSON paths that hold the owner reference and everything set once alongside it, in proto
-- field path syntax. Extend together with owner_reference_path.
create or replace function owner_reference_set_once_paths(source_table text) returns text[] as $$
  select case source_table
    when 'bare_metal_instances' then array['status.cluster', 'status.node_set']
  end;
$$ language sql immutable;

-- Set-once and live-target checks. Attached before insert or update on the tables that record an
-- owner reference.
create function check_owner_reference() returns trigger as $$
declare
  owner_reference jsonb;
  set_once_path text;
  target_table text;
  target_id text;
  found_id text;
  found_tenant text;
  found_project ltree;
  found_deletion timestamptz;
begin
  if TG_OP = 'UPDATE' then
    foreach set_once_path in array coalesce(owner_reference_set_once_paths(TG_TABLE_NAME), '{}'::text[]) loop
      if coalesce(new.data #> string_to_array(set_once_path, '.'), 'null'::jsonb) is distinct from
         coalesce(old.data #> string_to_array(set_once_path, '.'), 'null'::jsonb) then
        raise exception using
          errcode = 'Z0005',
          message = format(
            'cannot change %s of %s ''%s'': it is set once at creation',
            set_once_path, TG_TABLE_NAME, new.id
          );
      end if;
    end loop;
    return new;
  end if;

  owner_reference := new.data #> string_to_array(owner_reference_path(TG_TABLE_NAME), '.');
  if owner_reference is null then
    return new;
  end if;

  target_table := owner_reference_target_table(TG_TABLE_NAME);
  if target_table is null then
    return new;
  end if;

  target_id := owner_reference->>'id';
  if coalesce(target_id, '') = '' then
    raise exception using
      errcode = 'Z0005',
      message = format(
        'the owner reference of %s ''%s'' must set the target id',
        TG_TABLE_NAME, new.id
      );
  end if;

  execute format('select id, tenant, project, deletion_timestamp from %I where id = $1 for share', target_table)
    into found_id, found_tenant, found_project, found_deletion
    using target_id;

  if found_id is null then
    raise exception using
      errcode = 'Z0005',
      message = format(
        'the owner reference of %s ''%s'' targets a missing %s ''%s''',
        TG_TABLE_NAME, new.id, target_table, target_id
      );
  end if;
  if found_deletion != 'epoch' then
    raise exception using
      errcode = 'Z0005',
      message = format(
        'the owner reference of %s ''%s'' targets a deleting %s ''%s''',
        TG_TABLE_NAME, new.id, target_table, target_id
      );
  end if;
  if found_tenant != new.tenant or found_project != new.project then
    raise exception using
      errcode = 'Z0005',
      message = format(
        'the owner reference of %s ''%s'' must target a %s in the same tenant and project',
        TG_TABLE_NAME, new.id, target_table
      );
  end if;

  return new;
end;
$$ language plpgsql;

-- Owner deletion ordering. Attached before delete on the tables that other tables hold owner
-- references to. A row is blocked until no table holds an owner reference to it, so an owner is
-- archived only after everything it owns is gone.
create function check_owner_reference_not_in_use() returns trigger as $$
declare
  sources text[];
  tbl text;
  dependents int;
begin
  sources := coalesce(owner_reference_source_tables(TG_TABLE_NAME), '{}');
  foreach tbl in array sources loop
    execute format(
      'select count(*) from %I
       where data #> string_to_array(%L, ''.'') is not null
         and (data #> string_to_array(%L, ''.'')) ->> ''id'' = $1',
      tbl, owner_reference_path(tbl), owner_reference_path(tbl))
      into dependents
      using old.id;
    if dependents > 0 then
      raise exception using
        errcode = 'Z0003',
        message = format(
          'cannot delete %s ''%s'': %s resource(s) still hold an owner reference to it',
          TG_TABLE_NAME, old.id, dependents
        );
    end if;
  end loop;

  return old;
end;
$$ language plpgsql;

do $$
declare
  tbl text;
begin
  for tbl in
    select
      c.relname
    from
      pg_catalog.pg_class c
    join
      pg_catalog.pg_namespace n on n.oid = c.relnamespace
    where
      n.nspname = 'public' and
      c.relkind = 'r' and
      c.relname not like 'active_%' and
      c.relname not like 'archived_%' and
      c.relname not in (
        'changes',
        'notifications',
        'project_membership_subjects',
        'schema_migrations',
        'storage_tier_backends',
        'tenant_domains'
      )
  loop
    -- Tables that record an owner reference get the set-once and live-target checks plus the
    -- index the not-in-use guard uses.
    if owner_reference_target_table(tbl) is not null then
      execute format(
        'create trigger check_owner_reference
           before insert or update on %I
           for each row
           execute function check_owner_reference()',
        tbl);
      execute format(
        'create index %I on %I
           (((data #> string_to_array(%L, ''.'')) ->> ''id''))
           where data #> string_to_array(%L, ''.'') is not null',
        tbl || '_owner_reference', tbl, owner_reference_path(tbl), owner_reference_path(tbl));
    end if;

    -- Tables that other tables hold owner references to get the owner not-in-use guard.
    if coalesce(array_length(owner_reference_source_tables(tbl), 1), 0) > 0 then
      execute format(
        'create trigger check_owner_reference_not_in_use
           before delete on %I
           for each row
           execute function check_owner_reference_not_in_use()',
        tbl);
    end if;
  end loop;
end;
$$;
