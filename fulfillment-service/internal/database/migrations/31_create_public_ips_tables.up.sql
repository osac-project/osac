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

-- Create the public_ips tables:
--
-- This migration establishes the database schema for PublicIP resources following the generic schema pattern.
-- PublicIP represents a floating public IP address allocated from a PublicIPPool and optionally attached to a
-- ComputeInstance. The pool assignment is immutable after creation, while the compute_instance attachment can
-- be changed.
--
-- The data column stores PublicIPSpec (pool, compute_instance) and PublicIPStatus (state, message, hub,
-- address, pool) as JSONB.
--
create table public_ips (
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

create table archived_public_ips (
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

-- Indexes on the active table:
create index public_ips_by_name on public_ips (name);
create index public_ips_by_owner on public_ips using gin (creators);
create index public_ips_by_tenant on public_ips using gin (tenants);
create index public_ips_by_label on public_ips using gin (labels);

-- Indexes on the archived table:
create index archived_public_ips_by_name on archived_public_ips (name);
create index archived_public_ips_by_owner on archived_public_ips using gin (creators);
create index archived_public_ips_by_tenant on archived_public_ips using gin (tenants);
create index archived_public_ips_by_label on archived_public_ips using gin (labels);
