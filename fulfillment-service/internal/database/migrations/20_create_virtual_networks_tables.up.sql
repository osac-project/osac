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

-- Create the virtual_networks tables:
--
-- This migration establishes the database schema for VirtualNetwork resources following the generic schema pattern.
-- VirtualNetwork provides tenant-isolated network infrastructure for compute instances within a region. Each
-- VirtualNetwork is backed by a specific NetworkClass implementation strategy.
--
-- The data column stores:
-- - spec: VirtualNetworkSpec (region, network_class, ipv4_cidr, ipv6_cidr, capabilities)
--   - region: Region identifier (required, immutable)
--   - network_class: NetworkClass implementation_strategy reference (required, immutable)
--   - ipv4_cidr: IPv4 CIDR block (optional for IPv6-only networks)
--   - ipv6_cidr: IPv6 CIDR block (optional for IPv4-only networks)
--   - capabilities: VirtualNetworkCapabilities (enable_ipv4, enable_ipv6, enable_dual_stack)
-- - status: VirtualNetworkStatus (state, message)
-- as JSONB.
--
-- VirtualNetworks support flexible IP addressing:
-- - IPv4-only: ipv4_cidr set, ipv6_cidr empty
-- - IPv6-only: ipv6_cidr set, ipv4_cidr empty
-- - Dual-stack: both ipv4_cidr and ipv6_cidr set
--
create table virtual_networks (
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

create table archived_virtual_networks (
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
create index virtual_networks_by_name on virtual_networks (name);

-- Add indexes on the creators column for owner-based queries:
create index virtual_networks_by_owner on virtual_networks using gin (creators);

-- Add indexes on the tenants column for tenant isolation:
create index virtual_networks_by_tenant on virtual_networks using gin (tenants);

-- Add indexes on the labels column for label-based queries:
create index virtual_networks_by_label on virtual_networks using gin (labels);
