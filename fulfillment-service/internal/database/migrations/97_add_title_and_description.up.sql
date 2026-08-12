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

-- Add optional Metadata title and description columns to every GenericDAO object and archive table.
-- Tables are selected by the presence of both `name` and `data` columns (the GenericDAO layout).

create procedure add_title_and_description_columns() language plpgsql as $$
declare
  table_name text;
begin
  for table_name in
    select
      t.table_name
    from
      information_schema.tables t
    where
      t.table_schema = 'public' and
      t.table_type = 'BASE TABLE' and
      exists (
        select 1 from information_schema.columns c
        where c.table_schema = 'public'
          and c.table_name = t.table_name
          and c.column_name = 'name'
      ) and
      exists (
        select 1 from information_schema.columns c
        where c.table_schema = 'public'
          and c.table_name = t.table_name
          and c.column_name = 'data'
      ) and
      not exists (
        select 1 from information_schema.columns c
        where c.table_schema = 'public'
          and c.table_name = t.table_name
          and c.column_name = 'title'
      )
    order by
      t.table_name
  loop
    execute format(
      'alter table %I add column title text not null default ''''',
      table_name
    );
    execute format(
      'alter table %I add column description text not null default ''''',
      table_name
    );
  end loop;
end;
$$;

call add_title_and_description_columns();

drop procedure add_title_and_description_columns();
