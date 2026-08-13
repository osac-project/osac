--
-- Copyright (c) 2026 Red Hat Inc.
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

-- Introduce a generated 'path' column on projects that holds the full hierarchical project identity
-- (parent path + leaf name). Retarget the primary key and all foreign keys from (tenant, name) to
-- (tenant, path), store only the leaf segment in 'name', and change 'name' from ltree to text.

-- Drop every foreign key that references projects, including the self-referential parent FK and all
-- resource table project FKs. They will be recreated against (tenant, path).
do $$
declare
  r record;
begin
  for r in
    select
      rel.relname as table_name,
      con.conname as constraint_name
    from
      pg_constraint con
    join
      pg_class rel on rel.oid = con.conrelid
    join
      pg_namespace nsp on nsp.oid = rel.relnamespace
    join
      pg_class ref on ref.oid = con.confrelid
    where
      con.contype = 'f' and
      nsp.nspname = 'public' and
      ref.relname = 'projects'
  loop
    execute format(
      'alter table %I drop constraint %I',
      r.table_name, r.constraint_name
    );
  end loop;
end;
$$;

-- Primary key must be dropped before name values are rewritten to non-unique leaf segments.
alter table projects
  drop constraint projects_pkey;

-- Temporarily disable the immutable column trigger so that the one-time name rewrite is allowed.
-- The trigger is re-enabled after the rewrite; leaf names remain immutable afterwards.
alter table projects
  disable trigger check_immutable_columns;

-- Rewrite the name column from the full hierarchical path down to the leaf label. Root and
-- top-level projects already store a single label (or empty), so they are unchanged.
update projects
set
  name = case
    when nlevel(name) <= 1 then name
    else subpath(name, -1)
  end;

alter table projects
  enable trigger check_immutable_columns;

-- Leaf names are plain text; only 'project' and 'path' remain ltree.
alter table projects
  alter column name drop default,
  alter column name type text using name::text,
  alter column name set default '';

-- Full path is derived from the parent path and the leaf name. Empty parent means the leaf is the
-- full path (top-level or root project).
alter table projects
  add column path ltree
  generated always as (
    case
      when project = ''::ltree then name::ltree
      else (project::text || '.' || name)::ltree
    end
  ) stored;

-- The canonical identity of a project is now (tenant, path).
alter table projects
  add primary key (tenant, path);

-- Sibling uniqueness: two projects under the same parent cannot share a leaf name.
create unique index projects_unique_name_per_parent
  on projects (tenant, project, name);

-- Parent project must exist; the parent column stores the parent's full path.
alter table projects
  add constraint projects_parent_project_fk
  foreign key (tenant, project) references projects (tenant, path);

-- Recreate project foreign keys on every non-archived table that has a project column.
do $$
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
      c.column_name = 'project' and
      tb.table_type = 'BASE TABLE' and
      c.table_name not like 'archived_%' and
      c.table_name not in (
        'projects',
        'schema_migrations',
        'tenants',
        'notifications',
        'project_membership_subjects'
      )
    order by
      c.table_name
  loop
    execute format(
      'alter table %I add constraint %I foreign key (tenant, project) references projects (tenant, path)',
      table_name, table_name || '_project_fk'
    );
  end loop;
end;
$$;

-- Default project creation must insert a text name now that the column is no longer ltree.
create or replace function create_default_project() returns trigger as $$
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
    '',
    'system',
    '{}'
  );
  return new;
end;
$$ language plpgsql;
