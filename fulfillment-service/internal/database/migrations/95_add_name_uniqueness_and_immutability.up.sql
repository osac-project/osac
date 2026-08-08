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
--   3. Create a scope-specific uniqueness index covering all rows (including soft-deleted):
--        - roles, role_bindings: UNIQUE(name)
--        - users: UNIQUE(tenant, name)
--        - all others: UNIQUE(tenant, project, name)

do $$
declare
  tbl text;
  existing_args text[];
  col_args text;
  idx_name text;
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
        execute
          format('create trigger check_immutable_columns '
            'before update on %I '
            'for each row '
            'execute function check_immutable_columns(', tbl) ||
          col_args || ')';
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

    -- Create the scope-specific uniqueness index.
    if tbl in ('roles', 'role_bindings') then
      execute format(
        'create unique index %I on %I (name)',
        'idx_' || tbl || '_unique_name',
        tbl
      );
    elsif tbl = 'users' then
      execute format(
        'create unique index %I on %I (tenant, name)',
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
