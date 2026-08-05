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

-- Drop the hosts and host_pools tables created in migration 12
DROP TABLE IF EXISTS hosts;
DROP TABLE IF EXISTS archived_hosts;
DROP TABLE IF EXISTS host_pools;
DROP TABLE IF EXISTS archived_host_pools;

-- Indexes (hosts_by_owner, hosts_by_tenant, host_pools_by_owner, host_pools_by_tenant)
-- are dropped automatically with the tables
