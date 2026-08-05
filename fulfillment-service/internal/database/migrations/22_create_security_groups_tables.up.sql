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

-- Create the security_groups tables:
--
-- This migration establishes the database schema for SecurityGroup resources following the generic schema pattern.
-- SecurityGroup acts as a stateful virtual firewall controlling inbound and outbound traffic for compute instances
-- within a VirtualNetwork. Rules follow a default-deny policy where only explicitly allowed traffic can pass.
--
-- The data column stores:
-- - spec: SecurityGroupSpec
--   - virtual_network: Parent VirtualNetwork ID reference (required, immutable)
--   - ingress: Array of SecurityRule (inbound traffic rules)
--   - egress: Array of SecurityRule (outbound traffic rules)
-- - status: SecurityGroupStatus (state, message)
--
-- SecurityRule fields:
-- - protocol: Protocol enum (tcp, udp, icmp, all)
-- - port_from: Starting port (1-65535, required for tcp/udp)
-- - port_to: Ending port (1-65535, required for tcp/udp)
-- - ipv4_cidr: IPv4 source/destination CIDR (optional for IPv6-only rules)
-- - ipv6_cidr: IPv6 source/destination CIDR (optional for IPv4-only rules)
--
-- Parent-child relationship with VirtualNetwork is established via metadata.annotations using the
-- 'osac.openshift.io/owner-reference' key. This enables proper resource hierarchy for garbage collection.
--
create table security_groups (
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

create table archived_security_groups (
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
create index security_groups_by_name on security_groups (name);

-- Add indexes on the creators column for owner-based queries:
create index security_groups_by_owner on security_groups using gin (creators);

-- Add indexes on the tenants column for tenant isolation:
create index security_groups_by_tenant on security_groups using gin (tenants);

-- Add indexes on the labels column for label-based queries:
create index security_groups_by_label on security_groups using gin (labels);
