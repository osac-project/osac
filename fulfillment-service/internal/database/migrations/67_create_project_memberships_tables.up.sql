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

-- Create the project_memberships tables:
--
-- This migration establishes the database schema for ProjectMembership objects following the generic schema pattern.
-- ProjectMemberships define which users have access to a project and what their role is (viewer or manager).
--
-- The data column stores:
-- - spec: ProjectMembershipSpec (project, role, user)
-- - status: ProjectMembershipStatus (state, message)
-- as JSONB.
--
create table project_memberships (
  id text not null primary key,
  name text not null default '',
  creation_timestamp timestamp with time zone not null default now(),
  deletion_timestamp timestamp with time zone not null default 'epoch',
  finalizers text[] not null default '{}',
  creator text not null default '',
  tenant text not null default '',
  labels jsonb not null default '{}'::jsonb,
  annotations jsonb not null default '{}'::jsonb,
  data jsonb not null,
  version integer not null default 0
);

create table archived_project_memberships (
  id text not null,
  name text not null default '',
  creation_timestamp timestamp with time zone not null,
  deletion_timestamp timestamp with time zone not null,
  archival_timestamp timestamp with time zone not null default now(),
  creator text not null default '',
  tenant text not null default '',
  labels jsonb not null default '{}'::jsonb,
  annotations jsonb not null default '{}'::jsonb,
  data jsonb not null,
  version integer not null default 0
);

create index project_memberships_by_name on project_memberships (name);
create index project_memberships_by_creator on project_memberships (creator);
create index project_memberships_by_tenant on project_memberships (tenant);
create index project_memberships_by_label on project_memberships using gin (labels);

-- Project memberships must belong to a specific organization (tenant).
alter table project_memberships add constraint project_memberships_tenant_fk foreign key (tenant) references tenants (id);

-- Helper table to enforce uniqueness of (tenant, project, user) tuples.
-- A user can only have one membership per project within a tenant.
create table project_membership_subjects (
  tenant text not null,
  project text not null,
  "user" text not null,
  membership text not null references project_memberships(id) on delete cascade,
  primary key (tenant, project, "user")
);
create index project_membership_subjects_by_membership on project_membership_subjects (membership);

-- Trigger function that materializes the (tenant, project, user) tuple from the JSONB data column.
create function materialize_project_membership_subjects() returns trigger as $$
declare
  v_project text;
  v_user text;
begin
  -- Delete stale rows for this membership:
  delete from project_membership_subjects where membership = new.id;

  -- Extract project and user from the spec:
  v_project := new.data->'spec'->>'project';
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

create trigger materialize_project_membership_subjects
  after insert or update on project_memberships
  for each row
  execute function materialize_project_membership_subjects();

-- Backfill existing rows to populate the helper table:
update project_memberships set data = data;
