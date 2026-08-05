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

-- Add version column to all resource tables:
alter table clusters add column version integer not null default 0;
alter table cluster_templates add column version integer not null default 0;
alter table hubs add column version integer not null default 0;
alter table host_classes add column version integer not null default 0;
alter table compute_instances add column version integer not null default 0;
alter table compute_instance_templates add column version integer not null default 0;
alter table network_classes add column version integer not null default 0;
alter table virtual_networks add column version integer not null default 0;
alter table subnets add column version integer not null default 0;
alter table security_groups add column version integer not null default 0;
alter table leases add column version integer not null default 0;

-- Add version column to all archived tables:
alter table archived_clusters add column version integer not null default 0;
alter table archived_cluster_templates add column version integer not null default 0;
alter table archived_hubs add column version integer not null default 0;
alter table archived_host_classes add column version integer not null default 0;
alter table archived_compute_instances add column version integer not null default 0;
alter table archived_compute_instance_templates add column version integer not null default 0;
alter table archived_network_classes add column version integer not null default 0;
alter table archived_virtual_networks add column version integer not null default 0;
alter table archived_subnets add column version integer not null default 0;
alter table archived_security_groups add column version integer not null default 0;
