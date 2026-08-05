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

-- Create tables for external IP resources:
--
-- ExternalIPPool represents a pool of external IP addresses (external to the VirtualNetwork)
-- available for allocation. Each pool supports a single IP family and contains one or more
-- CIDR ranges.
--
-- ExternalIP represents a floating IP address allocated from an ExternalIPPool. The pool
-- assignment is immutable after creation.
--
-- ExternalIPAttachment represents a binding between an ExternalIP and a target resource
-- (ComputeInstance, Cluster, or BareMetalInstance).

-- ExternalIPPool tables

create table external_ip_pools (
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

create table archived_external_ip_pools (
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

create index external_ip_pools_by_name on external_ip_pools (name);
create index external_ip_pools_by_creator on external_ip_pools (creator);
create index external_ip_pools_by_tenant on external_ip_pools (tenant);
create index external_ip_pools_by_label on external_ip_pools using gin (labels);

alter table external_ip_pools
  add constraint external_ip_pools_tenant_fk
  foreign key (tenant) references tenants (id);

-- ExternalIP tables

create table external_ips (
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

create table archived_external_ips (
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

create index external_ips_by_name on external_ips (name);
create index external_ips_by_creator on external_ips (creator);
create index external_ips_by_tenant on external_ips (tenant);
create index external_ips_by_label on external_ips using gin (labels);

alter table external_ips
  add constraint external_ips_tenant_fk
  foreign key (tenant) references tenants (id);

-- ExternalIPAttachment tables

create table external_ip_attachments (
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

create table archived_external_ip_attachments (
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

create index external_ip_attachments_by_name on external_ip_attachments (name);
create index external_ip_attachments_by_creator on external_ip_attachments (creator);
create index external_ip_attachments_by_tenant on external_ip_attachments (tenant);
create index external_ip_attachments_by_label on external_ip_attachments using gin (labels);

alter table external_ip_attachments
  add constraint external_ip_attachments_tenant_fk
  foreign key (tenant) references tenants (id);

-- Unique indexes for concurrency safety

-- Enforce that each ExternalIP has at most one active ExternalIPAttachment.
create unique index external_ip_attachments_one_per_external_ip
  on external_ip_attachments ((data -> 'spec' ->> 'external_ip'))
  where data -> 'spec' ->> 'external_ip' is not null
    and data -> 'spec' ->> 'external_ip' != ''
    and deletion_timestamp = 'epoch';

-- Enforce that each ComputeInstance has at most one active ExternalIPAttachment.
create unique index external_ip_attachments_one_per_compute_instance
  on external_ip_attachments ((data -> 'spec' ->> 'compute_instance'))
  where data -> 'spec' ->> 'compute_instance' is not null
    and data -> 'spec' ->> 'compute_instance' != ''
    and deletion_timestamp = 'epoch';
