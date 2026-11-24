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

-- Create the quota tables (OSAC-4500 epic 1).
--
-- quota_limits holds one dimension's limit at one scope: a tenant override (empty project) or a
-- project limit. quota_default_limits holds the default limit of a dimension for all tenants; its
-- rows belong to the shared tenant, the way every platform-scoped kind does. quota_thresholds
-- holds a warning threshold per dimension, at a project's scope or the tenant's own; a threshold
-- is a percent of the limit it watches. quota_limit_requests holds a tenant's limit increase
-- requests, pending until a cloud provider admin approves or denies them. quota_outbox holds
-- unsent quota warnings; nothing writes it yet, the enforcement stories own its writer and
-- publisher.
--
-- Every limit change is audited: the object tables get the enqueue_change trigger, so each
-- insert, update and delete lands in the changes table with the complete row as JSON. The row
-- is the new state; the previous value is the preceding change row of the same identifier.

create table quota_limits (
  id text not null primary key,
  name text not null default '',
  creation_timestamp timestamp with time zone not null default now(),
  deletion_timestamp timestamp with time zone not null default 'epoch',
  finalizers text[] not null default '{}',
  creator text not null default '',
  tenant text not null default '',
  project ltree not null default ''::ltree,
  labels jsonb not null default '{}'::jsonb,
  annotations jsonb not null default '{}'::jsonb,
  data jsonb not null,
  version integer not null default 0
);

create table archived_quota_limits (
  id text not null,
  name text not null default '',
  creation_timestamp timestamp with time zone not null,
  deletion_timestamp timestamp with time zone not null,
  archival_timestamp timestamp with time zone not null default now(),
  creator text not null default '',
  tenant text not null default '',
  project ltree not null default ''::ltree,
  labels jsonb not null default '{}'::jsonb,
  annotations jsonb not null default '{}'::jsonb,
  data jsonb not null,
  version integer not null default 0
);

create index quota_limits_by_creator on quota_limits (creator);
create index quota_limits_by_tenant on quota_limits (tenant);
create index quota_limits_by_label on quota_limits using gin (labels);

alter table quota_limits
  add constraint quota_limits_tenant_fk
  foreign key (tenant) references tenants (name);

alter table quota_limits
  add constraint quota_limits_project_fk
  foreign key (tenant, project) references projects (tenant, name);

create trigger check_immutable_columns
  before update on quota_limits
  for each row
  execute function check_immutable_columns('id', 'tenant', 'project', 'name');

-- The unique indexes cover the live rows only, so a deleted limit can be set again before the
-- archiver moves the deleted row away:
create unique index idx_quota_limits_unique_name
  on quota_limits (tenant, project, name)
  where deletion_timestamp = 'epoch';

-- One limit per dimension per scope: tenant override or project limit.
create unique index quota_limits_unique_dimension
  on quota_limits (tenant, project, (data -> 'spec' ->> 'dimension'))
  where deletion_timestamp = 'epoch';

create table active_quota_limits (
  id text not null primary key references quota_limits (id) on delete cascade
);

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on quota_limits
  for each row
  execute function materialize_active_objects();

create trigger enqueue_change
  after insert or update or delete on quota_limits
  for each row
  execute function enqueue_change();

create table quota_default_limits (
  id text not null primary key,
  name text not null default '',
  creation_timestamp timestamp with time zone not null default now(),
  deletion_timestamp timestamp with time zone not null default 'epoch',
  finalizers text[] not null default '{}',
  creator text not null default '',
  tenant text not null default 'shared',
  project ltree not null default ''::ltree,
  labels jsonb not null default '{}'::jsonb,
  annotations jsonb not null default '{}'::jsonb,
  data jsonb not null,
  version integer not null default 0
);

alter table quota_default_limits
  add constraint quota_default_limits_tenant_fk
  foreign key (tenant) references tenants (name);

-- A default belongs to every tenant, so its row sits on the shared tenant, the home of
-- every platform-scoped kind:
alter table quota_default_limits
  add constraint quota_default_limits_shared_tenant check (tenant = 'shared');

alter table quota_default_limits
  add constraint quota_default_limits_no_project check (project = ''::ltree);

create trigger check_immutable_columns
  before update on quota_default_limits
  for each row
  execute function check_immutable_columns('id', 'tenant', 'project', 'name');

-- The unique indexes cover the live rows only, so a cleared default can be set again
-- before the archiver moves the deleted row away:
create unique index idx_quota_default_limits_unique_name
  on quota_default_limits (name)
  where deletion_timestamp = 'epoch';

-- One default per dimension:
create unique index quota_default_limits_unique_dimension
  on quota_default_limits ((data -> 'spec' ->> 'dimension'))
  where deletion_timestamp = 'epoch';

-- Default limits are cleared by deleting the row, so the changes table keeps their history.
create table active_quota_default_limits (
  id text not null primary key references quota_default_limits (id) on delete cascade
);

create table archived_quota_default_limits (
  id text not null,
  name text not null default '',
  creation_timestamp timestamp with time zone not null,
  deletion_timestamp timestamp with time zone not null,
  archival_timestamp timestamp with time zone not null default now(),
  creator text not null default '',
  tenant text not null default '',
  project ltree not null default ''::ltree,
  labels jsonb not null default '{}'::jsonb,
  annotations jsonb not null default '{}'::jsonb,
  data jsonb not null,
  version integer not null default 0
);

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on quota_default_limits
  for each row
  execute function materialize_active_objects();

create trigger enqueue_change
  after insert or update or delete on quota_default_limits
  for each row
  execute function enqueue_change();

create table quota_thresholds (
  id text not null primary key,
  name text not null default '',
  creation_timestamp timestamp with time zone not null default now(),
  deletion_timestamp timestamp with time zone not null default 'epoch',
  finalizers text[] not null default '{}',
  creator text not null default '',
  tenant text not null default '',
  project ltree not null default ''::ltree,
  labels jsonb not null default '{}'::jsonb,
  annotations jsonb not null default '{}'::jsonb,
  data jsonb not null,
  version integer not null default 0
);

create table archived_quota_thresholds (
  id text not null,
  name text not null default '',
  creation_timestamp timestamp with time zone not null,
  deletion_timestamp timestamp with time zone not null,
  archival_timestamp timestamp with time zone not null default now(),
  creator text not null default '',
  tenant text not null default '',
  project ltree not null default ''::ltree,
  labels jsonb not null default '{}'::jsonb,
  annotations jsonb not null default '{}'::jsonb,
  data jsonb not null,
  version integer not null default 0
);

create index quota_thresholds_by_creator on quota_thresholds (creator);
create index quota_thresholds_by_tenant on quota_thresholds (tenant);
create index quota_thresholds_by_label on quota_thresholds using gin (labels);

alter table quota_thresholds
  add constraint quota_thresholds_tenant_fk
  foreign key (tenant) references tenants (name);

alter table quota_thresholds
  add constraint quota_thresholds_project_fk
  foreign key (tenant, project) references projects (tenant, name);

create trigger check_immutable_columns
  before update on quota_thresholds
  for each row
  execute function check_immutable_columns('id', 'tenant', 'project', 'name');

-- The unique indexes cover the live rows only, so a deleted threshold can be set again before
-- the archiver moves the deleted row away. The project is part of the scope, so a tenant's own
-- threshold and one of its projects' can watch the same dimension:
create unique index idx_quota_thresholds_unique_name
  on quota_thresholds (tenant, project, name)
  where deletion_timestamp = 'epoch';

-- One threshold per dimension per scope.
create unique index quota_thresholds_unique_dimension
  on quota_thresholds (tenant, project, (data -> 'spec' ->> 'dimension'))
  where deletion_timestamp = 'epoch';

create table active_quota_thresholds (
  id text not null primary key references quota_thresholds (id) on delete cascade
);

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on quota_thresholds
  for each row
  execute function materialize_active_objects();

create trigger enqueue_change
  after insert or update or delete on quota_thresholds
  for each row
  execute function enqueue_change();

create table quota_limit_requests (
  id text not null primary key,
  name text not null default '',
  creation_timestamp timestamp with time zone not null default now(),
  deletion_timestamp timestamp with time zone not null default 'epoch',
  finalizers text[] not null default '{}',
  creator text not null default '',
  tenant text not null default '',
  project ltree not null default ''::ltree,
  labels jsonb not null default '{}'::jsonb,
  annotations jsonb not null default '{}'::jsonb,
  data jsonb not null,
  version integer not null default 0
);

create table archived_quota_limit_requests (
  id text not null,
  name text not null default '',
  creation_timestamp timestamp with time zone not null,
  deletion_timestamp timestamp with time zone not null,
  archival_timestamp timestamp with time zone not null default now(),
  creator text not null default '',
  tenant text not null default '',
  project ltree not null default ''::ltree,
  labels jsonb not null default '{}'::jsonb,
  annotations jsonb not null default '{}'::jsonb,
  data jsonb not null,
  version integer not null default 0
);

create index quota_limit_requests_by_creator on quota_limit_requests (creator);
create index quota_limit_requests_by_tenant on quota_limit_requests (tenant);
create index quota_limit_requests_by_label on quota_limit_requests using gin (labels);

alter table quota_limit_requests
  add constraint quota_limit_requests_tenant_fk
  foreign key (tenant) references tenants (name);

alter table quota_limit_requests
  add constraint quota_limit_requests_project_fk
  foreign key (tenant, project) references projects (tenant, name);

-- A limit request asks for a tenant limit, never a project limit.
alter table quota_limit_requests
  add constraint quota_limit_requests_no_project check (project = ''::ltree);

create trigger check_immutable_columns
  before update on quota_limit_requests
  for each row
  execute function check_immutable_columns('id', 'tenant', 'project', 'name');

-- The unique index covers the live rows only, so a deleted request's name can be used again
-- before the archiver moves the deleted row away:
create unique index idx_quota_limit_requests_unique_name
  on quota_limit_requests (tenant, project, name)
  where deletion_timestamp = 'epoch';

-- One pending request per dimension per tenant; a denied or approved request doesn't block a new
-- one, and neither does a deleted one.
create unique index quota_limit_requests_one_pending
  on quota_limit_requests (tenant, (data -> 'spec' ->> 'dimension'))
  where (data -> 'status' ->> 'state') = 'QUOTA_LIMIT_REQUEST_STATE_PENDING'
    and deletion_timestamp = 'epoch';

create table active_quota_limit_requests (
  id text not null primary key references quota_limit_requests (id) on delete cascade
);

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on quota_limit_requests
  for each row
  execute function materialize_active_objects();

create trigger enqueue_change
  after insert or update or delete on quota_limit_requests
  for each row
  execute function enqueue_change();

create table quota_outbox (
  id uuid primary key default uuidv7(),
  tenant text not null,
  project ltree not null default ''::ltree,
  dimension text not null,
  data jsonb not null,
  created_at timestamp with time zone not null default now()
);

-- The outbox is ephemeral warning data with no owning lifecycle, so the database removes its
-- rows with their scope: a warning an in-flight request writes can commit after the servers
-- already cleared the scope's outbox, and a blocking foreign key would then strand the scope's
-- archival forever. The servers' Delete-time cleanup stays as belt-and-suspenders for the rows
-- that exist when the deletion is accepted.
alter table quota_outbox
  add constraint quota_outbox_tenant_fk
  foreign key (tenant) references tenants (name) on delete cascade;

alter table quota_outbox
  add constraint quota_outbox_project_fk
  foreign key (tenant, project) references projects (tenant, name) on delete cascade;

-- This index serves both the servers' cleanup and the foreign-key checks:
create index quota_outbox_by_scope on quota_outbox (tenant, project);

create index quota_outbox_by_created_at on quota_outbox (created_at);
