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

-- The 'archived_public_ip_attachments' table was not included in the migrations that renamed the 'creators' and
-- 'tenants' array columns to singular 'creator' and 'tenant' text columns (migrations 40-42).
alter table archived_public_ip_attachments add column creator text not null default '';
update archived_public_ip_attachments set creator = coalesce(creators[1], '');
alter table archived_public_ip_attachments drop column creators;
alter table archived_public_ip_attachments add column tenant text not null default '';
update archived_public_ip_attachments set tenant = coalesce(tenants[1], '');
alter table archived_public_ip_attachments drop column tenants;

-- The 'archived_organizations' and 'archived_users' tables were created with a 'finalizers' column that the DAO never
-- writes to archived tables. Drop it for consistency with all other archived tables.
alter table archived_organizations drop column finalizers;
alter table archived_users drop column finalizers;
