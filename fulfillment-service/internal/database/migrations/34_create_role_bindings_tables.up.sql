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

-- Create the role_bindings tables:
--
-- This migration establishes the database schema for RoleBinding objects following the generic schema pattern.
-- RoleBindings grant the permissions defined by a role to a set of subjects (groups). The groups are identified
-- by their identity provider (IDP) identifiers.
--
-- The data column stores:
-- - spec: RoleBindingSpec (role, groups)
-- - status: RoleBindingStatus (state, message)
-- as JSONB.
--
create table role_bindings (
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

create table archived_role_bindings (
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
create index role_bindings_by_name on role_bindings (name);

-- Add indexes on the creators column for owner-based queries:
create index role_bindings_by_owner on role_bindings using gin (creators);

-- Add indexes on the tenants column for tenant isolation:
create index role_bindings_by_tenant on role_bindings using gin (tenants);

-- Add indexes on the labels column for label-based queries:
create index role_bindings_by_label on role_bindings using gin (labels);
