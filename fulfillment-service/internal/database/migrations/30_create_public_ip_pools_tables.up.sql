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

-- Create the public_ip_pools tables:
--
-- This migration establishes the database schema for PublicIPPool resources following the generic schema pattern.
-- PublicIPPool represents a pool of public IP addresses available for allocation to compute instances.
--
-- The data column stores:
-- - spec: PublicIPPoolSpec (cidrs, ip_family, implementation_strategy)
-- - status: PublicIPPoolStatus (state, message, hub, total, allocated, available)
-- as JSONB.
--
create table public_ip_pools (
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

create table archived_public_ip_pools (
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
create index public_ip_pools_by_name on public_ip_pools (name);

-- Add indexes on the creators column for owner-based queries:
create index public_ip_pools_by_owner on public_ip_pools using gin (creators);

-- Add indexes on the tenants column for tenant isolation:
create index public_ip_pools_by_tenant on public_ip_pools using gin (tenants);

-- Add indexes on the labels column for label-based queries:
create index public_ip_pools_by_label on public_ip_pools using gin (labels);
