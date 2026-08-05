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

-- Add the name column to the tables:
alter table cluster_templates add column name text not null default '';
alter table clusters add column name text not null default '';
alter table host_classes add column name text not null default '';
alter table hubs add column name text not null default '';
alter table virtual_machine_templates add column name text not null default '';
alter table virtual_machines add column name text not null default '';
alter table hosts add column name text not null default '';
alter table host_pools add column name text not null default '';

-- Add indexes on the name column for fast lookups:
create index cluster_templates_by_name on cluster_templates (name);
create index clusters_by_name on clusters (name);
create index host_classes_by_name on host_classes (name);
create index hubs_by_name on hubs (name);
create index virtual_machine_templates_by_name on virtual_machine_templates (name);
create index virtual_machines_by_name on virtual_machines (name);
create index hosts_by_name on hosts (name);
create index host_pools_by_name on host_pools (name);

-- Add the name column to the archive tables:
alter table archived_cluster_templates add column name text not null default '';
alter table archived_clusters add column name text not null default '';
alter table archived_host_classes add column name text not null default '';
alter table archived_hubs add column name text not null default '';
alter table archived_virtual_machine_templates add column name text not null default '';
alter table archived_virtual_machines add column name text not null default '';
alter table archived_hosts add column name text not null default '';
alter table archived_host_pools add column name text not null default '';
