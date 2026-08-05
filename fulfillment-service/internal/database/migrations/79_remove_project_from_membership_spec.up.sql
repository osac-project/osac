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

-- Update the trigger function to read the project from the standard 'project' column instead of the JSONB
-- 'data' column. The 'project' field was removed from the ProjectMembershipSpec proto message because it is
-- redundant with the project stored in the metadata (materialized as the 'project' column by the DAO layer).
create or replace function materialize_project_membership_subjects() returns trigger as $$
declare
  v_project text;
  v_user text;
begin
  -- Delete stale rows for this membership:
  delete from project_membership_subjects where membership = new.id;

  -- Read project from the standard column and user from the spec:
  v_project := new.project::text;
  v_user := new.data->'spec'->>'user';

  -- Insert the new tuple, catching duplicates:
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

  return new;
end;
$$ language plpgsql;

-- Backfill existing rows to repopulate the helper table with the correct project values:
update project_memberships set data = data;
