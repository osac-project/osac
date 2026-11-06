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

alter table workload_clusters
  add constraint workload_clusters_tenant_fk
  foreign key (tenant) references tenants (name);

-- Keep the WorkloadCluster spec in the generic JSONB resource payload, but
-- project its OSAC Hub relationship into a relational table. This gives the
-- relationship a real foreign key and lets PostgreSQL enforce physical
-- uniqueness without adding a non-generic column to the resource tables.
create table workload_cluster_hub_refs (
  workload_cluster_id text not null primary key references workload_clusters(id) on delete cascade,
  source_hub_id text not null references hubs(id),
  managed_cluster_name text not null,
  constraint workload_cluster_hub_refs_unique_identity unique (source_hub_id, managed_cluster_name)
);

create table active_workload_clusters (
  id text not null primary key references workload_clusters(id) on delete cascade
);

create function sync_workload_cluster_hub_ref() returns trigger as $$
begin
  if new.deletion_timestamp != 'epoch' then
    delete from workload_cluster_hub_refs where workload_cluster_id = new.id;
    return new;
  end if;

  insert into workload_cluster_hub_refs (workload_cluster_id, source_hub_id, managed_cluster_name)
  values (
    new.id,
    new.data->'spec'->>'source_hub_id',
    new.data->'spec'->>'managed_cluster_name'
  )
  on conflict (workload_cluster_id) do update set
    source_hub_id = excluded.source_hub_id,
    managed_cluster_name = excluded.managed_cluster_name;

  return new;
end;
$$ language plpgsql;

create trigger sync_workload_cluster_hub_ref
  after insert or update of data, deletion_timestamp on workload_clusters
  for each row execute function sync_workload_cluster_hub_ref();

create function prevent_hub_soft_delete_with_workload_clusters() returns trigger as $$
begin
  if old.deletion_timestamp = 'epoch' and new.deletion_timestamp != 'epoch'
    and exists (
      select 1
      from workload_cluster_hub_refs
      where source_hub_id = old.id
    ) then
    raise exception using
      errcode = 'Z0002',
      message = format('Hub ''%s'' is referenced by an active WorkloadCluster', old.id);
  end if;
  return new;
end;
$$ language plpgsql;

create trigger prevent_hub_soft_delete_with_workload_clusters
  before update of deletion_timestamp on hubs
  for each row execute function prevent_hub_soft_delete_with_workload_clusters();

insert into active_workload_clusters (id)
select id from workload_clusters where deletion_timestamp = 'epoch';

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on workload_clusters
  for each row execute function materialize_active_objects();

create trigger enqueue_change
  after insert or update or delete on workload_clusters
  for each row execute function enqueue_change();

create trigger check_immutable_columns
  before update on workload_clusters
  for each row
  execute function check_immutable_columns('id', 'tenant', 'name');
