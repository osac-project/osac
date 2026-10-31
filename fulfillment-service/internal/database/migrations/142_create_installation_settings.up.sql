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

-- Installation settings stored at the first start of the fulfillment service. Later starts compare their own settings
-- with these and refuse to start on a change that the installation doesn't support (unified networking design,
-- OSAC-1433: provider networking control, service changes and release upgrades). The table holds one row.
create table installation_settings (
  id integer not null primary key default 1 check (id = 1),
  provisioning_enabled boolean not null,
  enabled_services text[] not null default '{}',
  release text not null default '',
  created_at timestamp with time zone not null default now()
);
