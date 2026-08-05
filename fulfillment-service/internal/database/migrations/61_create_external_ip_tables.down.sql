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

drop index if exists external_ip_attachments_one_per_compute_instance;

drop index if exists external_ip_attachments_one_per_external_ip;

drop index if exists external_ip_attachments_by_label;

drop index if exists external_ip_attachments_by_tenant;

drop index if exists external_ip_attachments_by_creator;

drop index if exists external_ip_attachments_by_name;

drop table if exists archived_external_ip_attachments;

drop table if exists external_ip_attachments;

drop index if exists external_ips_by_label;

drop index if exists external_ips_by_tenant;

drop index if exists external_ips_by_creator;

drop index if exists external_ips_by_name;

drop table if exists archived_external_ips;

drop table if exists external_ips;

drop index if exists external_ip_pools_by_label;

drop index if exists external_ip_pools_by_tenant;

drop index if exists external_ip_pools_by_creator;

drop index if exists external_ip_pools_by_name;

drop table if exists archived_external_ip_pools;

drop table if exists external_ip_pools;
