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

-- Drop indexes on the archived_public_ips table. No other archive table has indexes, and they are
-- not currently used, so they just consume space.
drop index if exists archived_public_ips_by_name;
drop index if exists archived_public_ips_by_owner;
drop index if exists archived_public_ips_by_tenant;
drop index if exists archived_public_ips_by_label;
