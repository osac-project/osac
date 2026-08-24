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

-- Add CHECK (name <> '') to the instance_types table, matching disk_images and
-- bare_metal_instance_types. The archived table is intentionally left without
-- this constraint (consistent with disk_images).

-- Both the pre-migration check and the ALTER TABLE live in a single DO block
-- so that a RAISE EXCEPTION is the only error the caller sees. With two
-- top-level statements, PostgreSQL's simple-protocol implicit transaction
-- would replace the descriptive exception with a generic "current transaction
-- is aborted" from the second statement.
do $$
begin
  if exists (select 1 from instance_types where name = '') then
    raise exception 'instance_types contains rows with an empty name -- resolve before migrating';
  end if;
  execute 'alter table instance_types add constraint instance_types_name_not_empty check (name <> '''')';
  alter table instance_types alter column name drop default;
end $$;
