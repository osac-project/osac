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

-- Add the creators column to the tables:
alter table cluster_templates add column creators text[] not null default '{}';
alter table clusters add column creators text[] not null default '{}';
alter table host_classes add column creators text[] not null default '{}';
alter table hubs add column creators text[] not null default '{}';
alter table virtual_machine_templates add column creators text[] not null default '{}';
alter table virtual_machines add column creators text[] not null default '{}';

-- Add indexes on the creators column:
create index cluster_templates_by_owner on cluster_templates using gin (creators);
create index clusters_by_owner on clusters using gin (creators);
create index host_classes_by_owner on host_classes using gin (creators);
create index hubs_by_owner on hubs using gin (creators);
create index virtual_machine_templates_by_owner on virtual_machine_templates using gin (creators);
create index virtual_machines_by_owner on virtual_machines using gin (creators);

-- Add the owner column to the archive tables:
alter table archived_cluster_templates add column creators text[] not null default '{}';
alter table archived_clusters add column creators text[] not null default '{}';
alter table archived_host_classes add column creators text[] not null default '{}';
alter table archived_hubs add column creators text[] not null default '{}';
alter table archived_virtual_machine_templates add column creators text[] not null default '{}';
alter table archived_virtual_machines add column creators text[] not null default '{}';
