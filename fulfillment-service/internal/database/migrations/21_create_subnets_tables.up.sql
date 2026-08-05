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

-- Create the subnets tables:
--
-- This migration establishes the database schema for Subnet resources following the generic schema pattern.
-- Subnet subdivides VirtualNetwork IP address space into smaller segments. Each Subnet belongs to exactly one
-- parent VirtualNetwork and must be in the same region.
--
-- The data column stores SubnetSpec (virtual_network, ipv4_cidr, ipv6_cidr) and SubnetStatus (state, message)
-- as JSONB.
--
create table subnets (
  id text not null primary key,
  name text not null default '',
  creation_timestamp timestamp with time zone not null default now(),
  deletion_timestamp timestamp with time zone not null default 'epoch',
  finalizers text[] not null default '{}',
  creators text[] not null default '{}',
  tenants text[] not null default '{}',
  labels jsonb not null default '{}'::jsonb,
  annotations jsonb not null default '{}'::jsonb,
  data jsonb not null
);

create table archived_subnets (
  id text not null,
  name text not null default '',
  creation_timestamp timestamp with time zone not null,
  deletion_timestamp timestamp with time zone not null,
  archival_timestamp timestamp with time zone not null default now(),
  creators text[] not null default '{}',
  tenants text[] not null default '{}',
  labels jsonb not null default '{}'::jsonb,
  annotations jsonb not null default '{}'::jsonb,
  data jsonb not null
);

-- Add indexes on the name column for fast lookups:
create index subnets_by_name on subnets (name);

-- Add indexes on the creators column for owner-based queries:
create index subnets_by_owner on subnets using gin (creators);

-- Add indexes on the tenants column for tenant isolation:
create index subnets_by_tenant on subnets using gin (tenants);

-- Add indexes on the labels column for label-based queries:
create index subnets_by_label on subnets using gin (labels);
