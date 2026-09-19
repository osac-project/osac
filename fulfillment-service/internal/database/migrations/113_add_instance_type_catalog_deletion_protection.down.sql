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

-- Revert to migration 90's version of check_instance_type_not_in_use: remove the catalog-items
-- clause, keeping only the compute_instances and compute_instance_templates clauses.
create or replace function check_instance_type_not_in_use() returns trigger as $$
begin
  if exists (
    select 1
    from compute_instances
    where deletion_timestamp = 'epoch'
      and data->'spec'->'instance_type'->>'id' = old.id
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete instance type ''%s'': it is in use by at least one compute instance',
        old.id
      );
  end if;

  if exists (
    select 1
    from compute_instance_templates
    where deletion_timestamp = 'epoch'
      and data->'spec_defaults'->'instance_type'->>'id' = old.id
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete instance type ''%s'': it is in use by at least one compute instance template',
        old.id
      );
  end if;

  return new;
end;
$$ language plpgsql;
