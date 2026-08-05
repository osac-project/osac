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

-- Create the catalog items tables:
--
-- ClusterCatalogItem and ComputeInstanceCatalogItem are curated infrastructure offerings
-- that reference underlying templates. They control which fields users can set, enforce
-- defaults, and validate input via JSON Schema.
--
-- The data column stores the full protobuf as JSONB including:
-- - title: Human-friendly short description
-- - description: Human-friendly long description (Markdown)
-- - template: Reference to the underlying template by ID
-- - published: Whether the item is visible in the public API
-- - tenant: Tenant scope (empty = global, non-empty = scoped to that tenant)
-- - field_definitions: Repeated FieldDefinition messages controlling user-settable fields
--

create table cluster_catalog_items (
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

create table archived_cluster_catalog_items (
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

create index cluster_catalog_items_by_name on cluster_catalog_items (name);
create index cluster_catalog_items_by_owner on cluster_catalog_items using gin (creators);
create index cluster_catalog_items_by_tenant on cluster_catalog_items using gin (tenants);
create index cluster_catalog_items_by_label on cluster_catalog_items using gin (labels);

create table compute_instance_catalog_items (
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

create table archived_compute_instance_catalog_items (
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

create index compute_instance_catalog_items_by_name on compute_instance_catalog_items (name);
create index compute_instance_catalog_items_by_owner on compute_instance_catalog_items using gin (creators);
create index compute_instance_catalog_items_by_tenant on compute_instance_catalog_items using gin (tenants);
create index compute_instance_catalog_items_by_label on compute_instance_catalog_items using gin (labels);
