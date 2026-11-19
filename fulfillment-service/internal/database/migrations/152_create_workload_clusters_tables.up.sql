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
-- project its OSAC Hub relationship into a relational table. This lets
-- PostgreSQL enforce physical uniqueness without adding non-generic columns
-- to the resource tables while retaining the Hub identity after Hub deletion.
create table workload_cluster_hub_refs (
  workload_cluster_id text not null primary key references workload_clusters(id) on delete cascade,
  -- Keep the Hub identity after the Hub is deleted so existing
  -- WorkloadClusters remain inert registrations and can report HubUnreachable.
  -- New registrations validate the Hub at the API layer.
  source_hub_id text not null,
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
    new.data->'spec'->'maas'->>'source_hub_id',
    new.data->'spec'->'maas'->>'managed_cluster_name'
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

insert into active_workload_clusters (id)
select id from workload_clusters where deletion_timestamp = 'epoch';

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on workload_clusters
  for each row execute function materialize_active_objects();

alter table changes
  add column reason text,
  add column severity text;

create or replace function enqueue_change() returns trigger as $$
declare
  rec record;
  change_op text;
  change_id uuid;
  event_reason text := nullif(current_setting('osac.event_reason', true), '');
  event_severity text := nullif(current_setting('osac.event_severity', true), '');
  old_available jsonb;
  new_available jsonb;
begin
  if TG_OP = 'DELETE' then
    rec := old;
  else
    rec := new;
  end if;

  if TG_OP = 'UPDATE' and current_setting('osac.signal', true) = 'on' then
    change_op := 'SIGNAL';
  else
    change_op := TG_OP;
  end if;

  -- WorkloadCluster availability events must be based on the old and new
  -- condition values. Timestamps alone cannot distinguish a transition from
  -- an unrelated metadata update that preserves the condition.
  if TG_TABLE_NAME = 'workload_clusters' and TG_OP = 'UPDATE' and event_reason is null then
    select condition into old_available
    from jsonb_array_elements(coalesce(old.data->'status'->'conditions', '[]'::jsonb)) condition
    where condition->>'type' = 'Available';
    select condition into new_available
    from jsonb_array_elements(coalesce(new.data->'status'->'conditions', '[]'::jsonb)) condition
    where condition->>'type' = 'Available';

    if old_available->>'status' is distinct from new_available->>'status'
       or old_available->>'reason' is distinct from new_available->>'reason' then
      if new_available->>'reason' = 'ClusterIdentityMismatch' then
        event_reason := 'IdentityMismatch';
        event_severity := 'Warning';
      elsif new_available->>'status' = 'True' then
        event_reason := 'Available';
        event_severity := 'Normal';
      elsif new_available->>'status' = 'False' then
        event_reason := 'Unavailable';
        event_severity := 'Warning';
      end if;
    end if;
  end if;

  insert into changes (
    "table",
    op,
    data,
    reason,
    severity
  )
  values (
    TG_TABLE_NAME,
    change_op,
    to_jsonb(rec),
    event_reason,
    event_severity
  )
  returning id into change_id;

  perform pg_notify('changes', change_id::text);

  if TG_OP = 'DELETE' then
    return old;
  end if;
  return new;
end;
$$ language plpgsql;

create trigger enqueue_change
  after insert or update or delete on workload_clusters
  for each row execute function enqueue_change();

create trigger check_immutable_columns
  before update on workload_clusters
  for each row
  execute function check_immutable_columns('id', 'tenant', 'name');
