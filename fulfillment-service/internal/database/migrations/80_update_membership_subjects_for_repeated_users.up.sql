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

-- Update the trigger function to read 'users' (repeated/array) instead of 'user' (singular) from the JSONB data
-- column. The ProjectMembershipSpec proto field was changed from `oneof member { string user = 3; }` to
-- `repeated string users = 3;`, so the JSON now stores an array at `spec.users` instead of a single value at
-- `spec.user`.
create or replace function materialize_project_membership_subjects() returns trigger as $$
declare
  v_project text;
  v_user text;
begin
  -- Delete stale rows for this membership:
  delete from project_membership_subjects where membership = new.id;

  -- Read project from the standard column:
  v_project := new.project::text;

  -- Iterate over each user in the repeated users field:
  for v_user in select jsonb_array_elements_text(new.data->'spec'->'users')
  loop
    begin
      insert into project_membership_subjects (tenant, project, "user", membership)
        values (new.tenant, v_project, v_user, new.id);
    exception when unique_violation then
      declare
        existing_membership_name text;
      begin
        select pm.name into existing_membership_name
          from project_membership_subjects pms
          join project_memberships pm on pm.id = pms.membership
          where pms.tenant = new.tenant and pms.project = v_project and pms."user" = v_user;

        raise exception using
          errcode = 'Z0004',
          message = format('user ''%s'' is already a member of project ''%s'' via membership ''%s''',
            v_user, v_project, existing_membership_name);
      end;
    end;
  end loop;

  return new;
end;
$$ language plpgsql;

-- Convert any existing rows that still use the old singular 'user' field to the new 'users' array format.
-- This must happen before the backfill so the new trigger can read the data correctly.
update project_memberships
  set data = jsonb_set(
    data #- '{spec,user}',
    '{spec,users}',
    jsonb_build_array(data->'spec'->'user')
  )
  where data->'spec' ? 'user'
    and not (data->'spec' ? 'users');

-- Backfill existing rows to repopulate the helper table with the new array-based extraction:
update project_memberships set data = data;
