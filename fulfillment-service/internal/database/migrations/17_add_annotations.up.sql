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

-- Add annotations column to the tables:
alter table cluster_templates add column annotations jsonb not null default '{}'::jsonb;
alter table clusters add column annotations jsonb not null default '{}'::jsonb;
alter table host_classes add column annotations jsonb not null default '{}'::jsonb;
alter table hubs add column annotations jsonb not null default '{}'::jsonb;
alter table compute_instance_templates add column annotations jsonb not null default '{}'::jsonb;
alter table compute_instances add column annotations jsonb not null default '{}'::jsonb;
alter table hosts add column annotations jsonb not null default '{}'::jsonb;
alter table host_pools add column annotations jsonb not null default '{}'::jsonb;

-- Add the annotations column to the archive tables:
alter table archived_cluster_templates add column annotations jsonb not null default '{}'::jsonb;
alter table archived_clusters add column annotations jsonb not null default '{}'::jsonb;
alter table archived_host_classes add column annotations jsonb not null default '{}'::jsonb;
alter table archived_hubs add column annotations jsonb not null default '{}'::jsonb;
alter table archived_compute_instance_templates add column annotations jsonb not null default '{}'::jsonb;
alter table archived_compute_instances add column annotations jsonb not null default '{}'::jsonb;
alter table archived_hosts add column annotations jsonb not null default '{}'::jsonb;
alter table archived_host_pools add column annotations jsonb not null default '{}'::jsonb;
