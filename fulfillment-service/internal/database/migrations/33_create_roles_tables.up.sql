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

-- Create the roles tables:
--
-- This migration establishes the database schema for Role resources following the generic schema pattern. Roles
-- represent named permission sets that can be assigned to groups to control what actions their members can perform.
-- The data column is an empty JSON object since roles carry no data beyond their identity and metadata.
--
create table roles (
  id text not null primary key,
  name text not null default '',
  creation_timestamp timestamp with time zone not null default now(),
  deletion_timestamp timestamp with time zone not null default 'epoch',
  finalizers text[] not null default '{}',
  creators text[] not null default '{}',
  tenants text[] not null default '{}',
  labels jsonb not null default '{}'::jsonb,
  annotations jsonb not null default '{}'::jsonb,
  version integer not null default 0,
  data jsonb not null
);

create table archived_roles (
  id text not null,
  name text not null default '',
  creation_timestamp timestamp with time zone not null,
  deletion_timestamp timestamp with time zone not null,
  archival_timestamp timestamp with time zone not null default now(),
  creators text[] not null default '{}',
  tenants text[] not null default '{}',
  labels jsonb not null default '{}'::jsonb,
  annotations jsonb not null default '{}'::jsonb,
  version integer not null default 0,
  data jsonb not null
);

-- Add indexes on the name column for fast lookups:
create index roles_by_name on roles (name);

-- Add indexes on the creators column for owner-based queries:
create index roles_by_owner on roles using gin (creators);

-- Add indexes on the tenants column for tenant isolation:
create index roles_by_tenant on roles using gin (tenants);

-- Add indexes on the labels column for label-based queries:
create index roles_by_label on roles using gin (labels);

-- Insert predefined roles with the "shared" tenant so that all users can see them:
insert into roles (id, name, tenants, data) values
(
  '019dd9f6-36cb-7824-ac1c-6455d8a7a9b5',
  'cloud-provider-admin',
  array['shared'],
  '{
    "spec": {
      "title": "Cloud provider administrator",
      "description": "Full administrative access to all cloud provider resources."
    },
    "status": {
      "state": "ROLE_STATE_READY"
    }
  }'
),
(
  '019dd9f6-36cc-7a35-bfc5-242833f11c2f',
  'cloud-provider-reader',
  array['shared'],
  '{
    "spec": {
      "title": "Cloud provider reader",
      "description": "Read-only access to all cloud provider resources."
    },
    "status": {
      "state": "ROLE_STATE_READY"
    }
  }'
),
(
  '019dd9f6-36cd-78bf-9fa2-1be4464108b0',
  'tenant-admin',
  array['shared'],
  '{
    "spec": {
      "title": "Tenant administrator",
      "description": "Full administrative access to all resources within a tenant."
    },
    "status": {
      "state": "ROLE_STATE_READY"
    }
  }'
),
(
  '019dd9f6-36ce-78fc-96d8-b87cf58bf14c',
  'tenant-reader',
  array['shared'],
  '{
    "spec": {
      "title": "Tenant reader",
      "description": "Read-only access to all resources within a tenant."
    },
    "status": {
      "state": "ROLE_STATE_READY"
    }
  }'
),
(
  '019dd9f6-36cf-77bc-8041-c6a6a415c185',
  'tenant-user',
  array['shared'],
  '{
    "spec": {
      "title": "Tenant user",
      "description": "Standard user access within a tenant."
    },
    "status": {
      "state": "ROLE_STATE_READY"
    }
  }'
);
