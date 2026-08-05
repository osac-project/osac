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


-- Add a foreign key constraint on the 'tenant' column of every active object table, enforcing that it must reference an
-- existing tenant. Archived tables are excluded because the referenced tenant may have been deleted after archival.

create procedure add_tenant_foreign_keys() language plpgsql as $$
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
      'alter table %I add constraint %I foreign key (tenant) references organizations (id)',
      t, t || '_tenant_fk'
    );
  end loop;
end;
$$;

call add_tenant_foreign_keys();

drop procedure add_tenant_foreign_keys();
