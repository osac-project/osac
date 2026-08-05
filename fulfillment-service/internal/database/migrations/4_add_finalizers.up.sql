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

--
-- Add the finalizers column to the existing tables.
--
alter table cluster_orders add column finalizers text[] not null default '{}';
alter table cluster_templates add column finalizers text[] not null default '{}';
alter table clusters add column finalizers text[] not null default '{}';
alter table hubs add column finalizers text[] not null default '{}';

--
-- Create the archive tables.
--

create table archived_cluster_orders (
  id text not null,
  creation_timestamp timestamp with time zone not null,
  deletion_timestamp timestamp with time zone not null,
  archival_timestamp timestamp with time zone not null default now(),
  data jsonb not null
);

create table archived_cluster_templates (
  id text not null,
  creation_timestamp timestamp with time zone not null,
  deletion_timestamp timestamp with time zone not null,
  archival_timestamp timestamp with time zone not null default now(),
  data jsonb not null
);

create table archived_clusters (
  id text not null,
  creation_timestamp timestamp with time zone not null,
  deletion_timestamp timestamp with time zone not null,
  archival_timestamp timestamp with time zone not null default now(),
  data jsonb not null
);

create table archived_hubs (
  id text not null,
  creation_timestamp timestamp with time zone not null,
  deletion_timestamp timestamp with time zone not null,
  archival_timestamp timestamp with time zone not null default now(),
  data jsonb not null
);
