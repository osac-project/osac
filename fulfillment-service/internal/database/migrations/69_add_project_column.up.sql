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

-- Enable the 'ltree' extension, which provides the hierarchical label data type used for the project names.
create extension if not exists ltree;

-- Change the 'name' column of the projects table to use the 'ltree' type. It wil contain the full name of the project.
alter table projects
  alter column name drop default,
  alter column name type ltree using name::ltree;

-- Add the 'project' column to the projects table, using the 'ltree' type. It will contain the full name of the parent
-- project. We add it as nullable first, backfill existing rows, and then enforce 'not null' so the migration works on
-- populated tables.
alter table projects add column project ltree;
update projects
  set project = ''::ltree
where
  project is null;
alter table projects alter column project set not null;

-- Change the primary key of the projects table to use the 'tenant' and 'name' columns instead of the 'id' column. The
-- 'id' column will be preserved, but will be demoted to a unique constraint.
alter table projects
  drop constraint projects_pkey,
  add primary key (tenant, name),
  add constraint projects_id_unique unique (id);

-- Enforce immutability of 'id', 'tenant', 'project' and 'name' in the projects table.
drop trigger if exists check_immutable_columns on projects;
create trigger check_immutable_columns
  before update on projects
  for each row
  execute function check_immutable_columns('id', 'tenant', 'project', 'name');

-- Add a foreign key constraint to the projects table to ensure that the parent project exists.
alter table projects
  add constraint projects_parent_project_fk foreign key (tenant, project) references projects (tenant, name);

-- Add the 'project' column to the tenants table. This is for schema consistency, but it doesn't really have any
-- meaning, as tenants do not belong to projects.
alter table tenants add column project ltree not null default ''::ltree;

-- The new 'project' column will be empty for all existing objects, so we need to insert a default empty project for
-- each existing tenant in order to satisfy the new foreign key constraint.
insert into projects (
  id,
  tenant,
  project,
  name,
  creator,
  data
)
select
  uuidv7(),
  name,
  ''::ltree,
  ''::ltree,
  'system',
  '{}'
from
  tenants
;

-- Automatically create the empty project for new tenants.
create function create_default_project() returns trigger as $$
begin
  insert into projects (
    id,
    tenant,
    project,
    name,
    creator,
    data
  )
  values (
    uuidv7(),
    new.tenant,
    ''::ltree,
    ''::ltree,
    'system',
    '{}'
  );
  return new;
end;
$$ language plpgsql;

create trigger create_default_project
  after insert on tenants
  for each row
  execute function create_default_project();

-- For all other tables we need to add the 'project' column, with the foreign key constraint referencing the projects
-- table.
create procedure add_project_columns() language plpgsql as $$
declare
  table_name text;
begin
  for table_name in
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
      c.table_name not in (
        'notifications',
        'project_membership_subjects',
        'projects',
        'schema_migrations',
        'tenants'
      )
    order by
      c.table_name
  loop
    execute format(
      'alter table %I add column project ltree not null default ''''::ltree',
      table_name
    );
    if table_name not like 'archived_%' then
      -- Add the tenant, project pair as a foreign key constraint:
      execute format(
        'alter table %I add constraint %I foreign key (tenant, project) references projects (tenant, name)',
        table_name, table_name || '_project_fk'
      );

      -- If the table already has a 'check_immutable_columns' trigger, read its current column list, append 'project' if
      -- not already present, then drop and recreate it. Otherwise create a new trigger with the default set of
      -- immutable columns.
      declare
        existing_args text[];
        col_args text;
      begin
        select
          string_to_array(encode(t.tgargs, 'escape'), '\000')
        into
          existing_args
        from
          pg_trigger t
        join
          pg_class c on c.oid = t.tgrelid
        where
          c.relname = table_name and
          t.tgname = 'check_immutable_columns';
        if existing_args is not null then
          existing_args := array_remove(existing_args, '');
          if not 'project' = any(existing_args) then
            existing_args := array_append(existing_args, 'project');
          end if;
        else
          existing_args := array['id', 'tenant', 'project'];
        end if;
        select
          string_agg(quote_literal(col), ', ')
        into
          col_args
        from
          unnest(existing_args) as col;
        execute format(
          'drop trigger if exists check_immutable_columns on %I',
          table_name
        );
        execute
          format('create trigger check_immutable_columns '
            'before update on %I '
            'for each row '
            'execute function check_immutable_columns(', table_name) ||
          col_args || ')';
      end;
    end if;
  end loop;
end;
$$;

call add_project_columns();

drop procedure add_project_columns();
