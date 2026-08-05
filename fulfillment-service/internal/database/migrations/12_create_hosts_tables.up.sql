--
-- Copyright (c) 2025 Red Hat Inc.
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

-- Create the hosts tables:
--
create table hosts (
  id text not null primary key,
  creation_timestamp timestamp with time zone not null default now(),
  deletion_timestamp timestamp with time zone not null default 'epoch',
  finalizers text[] not null default '{}',
  creators text[] not null default '{}',
  tenants text[] not null default array['shared'],
  data jsonb not null
);

create table archived_hosts (
  id text not null,
  creation_timestamp timestamp with time zone not null,
  deletion_timestamp timestamp with time zone not null,
  archival_timestamp timestamp with time zone not null default now(),
  creators text[] not null default '{}',
  tenants text[] not null default '{}',
  data jsonb not null
);

-- Create the host_pools tables:
--
create table host_pools (
  id text not null primary key,
  creation_timestamp timestamp with time zone not null default now(),
  deletion_timestamp timestamp with time zone not null default 'epoch',
  finalizers text[] not null default '{}',
  creators text[] not null default '{}',
  tenants text[] not null default array['shared'],
  data jsonb not null
);

create table archived_host_pools (
  id text not null,
  creation_timestamp timestamp with time zone not null,
  deletion_timestamp timestamp with time zone not null,
  archival_timestamp timestamp with time zone not null default now(),
  creators text[] not null default '{}',
  tenants text[] not null default '{}',
  data jsonb not null
);

-- Add indexes on the creators column:
create index hosts_by_owner on hosts using gin (creators);
create index host_pools_by_owner on host_pools using gin (creators);

-- Add indexes on the tenants column:
create index hosts_by_tenant on hosts using gin (tenants);
create index host_pools_by_tenant on host_pools using gin (tenants);
