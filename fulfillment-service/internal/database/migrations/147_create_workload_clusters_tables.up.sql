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

-- WorkloadClusters are provider-level records. The shared tenant is used by
-- the generic resource layer so the resource remains visible to CPAs only.
create table workload_clusters (
  id text not null primary key,
  name text not null default '',
  creation_timestamp timestamp with time zone not null default now(),
  deletion_timestamp timestamp with time zone not null default 'epoch',
  finalizers text[] not null default '{}',
  creator text not null default '',
  tenant text not null default '',
  project ltree not null default ''::ltree,
  labels jsonb not null default '{}'::jsonb,
  annotations jsonb not null default '{}'::jsonb,
  data jsonb not null,
  version integer not null default 0
);

create table archived_workload_clusters (
  id text not null,
  name text not null default '',
  creation_timestamp timestamp with time zone not null,
  deletion_timestamp timestamp with time zone not null,
  archival_timestamp timestamp with time zone not null default now(),
  creator text not null default '',
  tenant text not null default '',
  project ltree not null default ''::ltree,
  labels jsonb not null default '{}'::jsonb,
  annotations jsonb not null default '{}'::jsonb,
  data jsonb not null,
  version integer not null default 0
);

create index workload_clusters_by_creator on workload_clusters (creator);
create index workload_clusters_by_label on workload_clusters using gin (labels);

create unique index workload_clusters_unique_name
  on workload_clusters (name);

create unique index workload_clusters_unique_hub_identity
  on workload_clusters (
    (data->'spec'->>'source_hub_id'),
    (data->'spec'->>'managed_cluster_name')
  ) where deletion_timestamp = 'epoch';

alter table workload_clusters
  add constraint workload_clusters_tenant_fk
  foreign key (tenant) references tenants (name);

create trigger check_immutable_columns
  before update on workload_clusters
  for each row
  execute function check_immutable_columns('id', 'tenant', 'name');
