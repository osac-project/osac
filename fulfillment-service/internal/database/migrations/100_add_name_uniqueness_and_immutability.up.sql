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

-- Enforce name uniqueness and immutability across all resource tables.
--
-- For each resource table that has a 'name' column:
--   1. Append 'name' to the check_immutable_columns trigger arguments (if not already present)
--   2. Drop any existing unique index that includes the 'name' column (replaces partial indexes)
--   3. Create a scope-specific uniqueness index:
--        - roles, role_bindings: UNIQUE(name) — all rows
--        - users: UNIQUE(tenant, name) WHERE active — partial, excludes soft-deleted
--        - all others: UNIQUE(tenant, project, name) — all rows

do $$
declare
  tbl text;
  existing_args text[];
  col_args text;
  idx_name text;
  has_deletion_timestamp boolean;
  partition_key text;
begin
  for tbl in
    select
      c.table_name
    from
      information_schema.columns c
    join
      information_schema.tables tb on
        tb.table_schema = c.table_schema and
        tb.table_name = c.table_name
    where
      c.table_schema = 'public' and
      c.column_name = 'name' and
      tb.table_type = 'BASE TABLE' and
      c.table_name not in (
        'schema_migrations',
        'notifications',
        'tenant_domains',
        'project_membership_subjects',
        'storage_tier_backends',
        'tenants',
        'projects',
        'storage_backends',
        'storage_tiers',
        'bare_metal_instance_types',
        'identity_providers'
      )
    order by
      c.table_name
  loop
    if tbl like 'archived_%' then
      continue;
    end if;

    -- Append 'name' to the immutability trigger if not already present.
    select
      string_to_array(encode(t.tgargs, 'escape'), '\000')
    into
      existing_args
    from
      pg_trigger t
    join
      pg_class c on c.oid = t.tgrelid
    where
      c.relname = tbl and
      t.tgname = 'check_immutable_columns';

    if existing_args is not null then
      existing_args := array_remove(existing_args, '');
      if not 'name' = any(existing_args) then
        existing_args := array_append(existing_args, 'name');
        select
          string_agg(quote_literal(col), ', ')
        into
          col_args
        from
          unnest(existing_args) as col;
        execute format(
          'drop trigger if exists check_immutable_columns on %I',
          tbl
        );
        execute format(
          'create trigger check_immutable_columns '
          'before update on %I '
          'for each row '
          'execute function check_immutable_columns(%s)',
          tbl, col_args
        );
      end if;
    else
      execute format(
        'drop trigger if exists check_immutable_columns on %I',
        tbl
      );
      execute format(
        'create trigger check_immutable_columns '
        'before update on %I '
        'for each row '
        'execute function check_immutable_columns(''id'', ''tenant'', ''project'', ''name'')',
        tbl
      );
    end if;

    -- Drop any existing unique index that includes the 'name' column.
    for idx_name in
      select
        i.relname
      from
        pg_index ix
      join
        pg_class t on t.oid = ix.indrelid
      join
        pg_class i on i.oid = ix.indexrelid
      join
        pg_attribute a on a.attrelid = t.oid and a.attnum = any(ix.indkey)
      where
        t.relname = tbl and
        ix.indisunique and
        a.attname = 'name' and
        not ix.indisprimary
    loop
      execute format('drop index if exists %I', idx_name);
    end loop;

    -- Resolve any remaining name collisions (e.g. active + soft-deleted rows sharing a name)
    -- so the full unique index can be created. Active rows keep their names; duplicates are
    -- renamed with an ID suffix.
    select exists(
      select 1 from information_schema.columns
      where table_schema = 'public' and table_name = tbl and column_name = 'deletion_timestamp'
    ) into has_deletion_timestamp;

    if has_deletion_timestamp then
      if tbl in ('roles', 'role_bindings') then
        partition_key := 'name';
      elsif tbl = 'users' then
        partition_key := 'tenant, name';
      else
        partition_key := 'tenant, project, name';
      end if;

      execute format('alter table %I disable trigger check_immutable_columns', tbl);
      execute format(
        'with dups as ('
        '  select id,'
        '    row_number() over (partition by %s'
        '      order by (case when deletion_timestamp = ''epoch'' then 0 else 1 end), id) as rn'
        '  from %I'
        ')'
        'update %I t set name = left(t.name, 26) || ''-'' || t.id '
        'from dups d where d.id = t.id and d.rn > 1',
        partition_key, tbl, tbl
      );
      execute format('alter table %I enable trigger check_immutable_columns', tbl);
    end if;

    -- Create the scope-specific uniqueness index.
    if tbl in ('roles', 'role_bindings') then
      execute format(
        'create unique index %I on %I (name)',
        'idx_' || tbl || '_unique_name',
        tbl
      );
    elsif tbl = 'users' then
      execute format(
        'create unique index %I on %I (tenant, name) where deletion_timestamp = ''epoch''',
        'idx_' || tbl || '_unique_name',
        tbl
      );
    else
      execute format(
        'create unique index %I on %I (tenant, project, name)',
        'idx_' || tbl || '_unique_name',
        tbl
      );
    end if;
  end loop;
end $$;
