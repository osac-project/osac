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

-- Change the primary key of the tenants table from 'id' to 'name'. All foreign key constraints that reference
-- tenants(id) must be dropped first and re-created afterwards to reference the new primary key column.

-- Drop all foreign key constraints that reference tenants(id):
do $$
declare
  r record;
begin
  for r in
    select
      con.conname as constraint_name,
      c.relname as table_name
    from
      pg_catalog.pg_constraint con
    join
      pg_catalog.pg_class c on c.oid = con.conrelid
    join
      pg_catalog.pg_class fc on fc.oid = con.confrelid
    join
      pg_catalog.pg_namespace fn on fn.oid = fc.relnamespace
    where
      con.contype = 'f' and
      fn.nspname = 'public' and
      fc.relname = 'tenants'
  loop
    execute format('alter table %I drop constraint %I', r.table_name, r.constraint_name);
  end loop;
end;
$$;

-- Replace the primary key with 'name' and demote 'id' to a unique constraint:
alter table tenants
  drop constraint organizations_pkey,
  add constraint tenants_pkey primary key (name),
  add constraint tenants_id_unique unique (id);

-- Re-create all foreign key constraints referencing tenants(name) via the 'tenant' column:
do $$
declare
  t text;
begin
  for t in
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
      c.column_name = 'tenant' and
      tb.table_type = 'BASE TABLE' and
      c.table_name not like 'archived_%' and
      c.table_name not in ('notifications', 'schema_migrations')
    order by
      c.table_name
  loop
    execute format(
      'alter table %I add constraint %I foreign key (tenant) references tenants (name)',
      t, t || '_tenant_fk'
    );
  end loop;
end;
$$;

-- Ensure in the tenants table the 'tenant', 'id' and 'name' columns form a single identity, i.e. they have the same
-- value. These duplicated columns exist for consistency with other tables.
alter table tenants
  add constraint tenants_single_identity_chk check (id = name and tenant = name);
