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

-- Add labels column to the tables:
alter table cluster_templates add column labels jsonb not null default '{}'::jsonb;
alter table clusters add column labels jsonb not null default '{}'::jsonb;
alter table host_classes add column labels jsonb not null default '{}'::jsonb;
alter table hubs add column labels jsonb not null default '{}'::jsonb;
alter table compute_instance_templates add column labels jsonb not null default '{}'::jsonb;
alter table compute_instances add column labels jsonb not null default '{}'::jsonb;
alter table hosts add column labels jsonb not null default '{}'::jsonb;
alter table host_pools add column labels jsonb not null default '{}'::jsonb;

-- Add indexes on the labels column:
create index cluster_templates_by_label on cluster_templates using gin (labels);
create index clusters_by_label on clusters using gin (labels);
create index host_classes_by_label on host_classes using gin (labels);
create index hubs_by_label on hubs using gin (labels);
create index compute_instance_templates_by_label on compute_instance_templates using gin (labels);
create index compute_instances_by_label on compute_instances using gin (labels);
create index hosts_by_label on hosts using gin (labels);
create index host_pools_by_label on host_pools using gin (labels);

-- Add the labels column to the archive tables:
alter table archived_cluster_templates add column labels jsonb not null default '{}'::jsonb;
alter table archived_clusters add column labels jsonb not null default '{}'::jsonb;
alter table archived_host_classes add column labels jsonb not null default '{}'::jsonb;
alter table archived_hubs add column labels jsonb not null default '{}'::jsonb;
alter table archived_compute_instance_templates add column labels jsonb not null default '{}'::jsonb;
alter table archived_compute_instances add column labels jsonb not null default '{}'::jsonb;
alter table archived_hosts add column labels jsonb not null default '{}'::jsonb;
alter table archived_host_pools add column labels jsonb not null default '{}'::jsonb;
