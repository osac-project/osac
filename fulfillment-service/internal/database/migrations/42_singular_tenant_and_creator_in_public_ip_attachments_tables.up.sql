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

-- This migration replaces the multi-valued 'creators' column (text[]) with a single-valued 'creator' column (text) on
-- all resource tables and their archived counterparts. The first element of the array is preserved as the new scalar
-- value.

-- Migrate the tenants array to a single tenant value:
alter table public_ip_attachments add column tenant text not null default '';
update public_ip_attachments set tenant = coalesce(tenants[1], '');
alter table public_ip_attachments drop column tenants;

-- Recreate index on the tenant column:
drop index if exists public_ip_attachments_by_tenant;
create index public_ip_attachments_by_tenant on public_ip_attachments (tenant);

-- Migrate the creators array to a single creator value:
alter table public_ip_attachments add column creator text not null default '';
update public_ip_attachments set creator = coalesce(creators[1], '');
alter table public_ip_attachments drop column creators;

-- Recreate index on the creator column:
drop index if exists public_ip_attachments_by_owner;
create index public_ip_attachments_by_owner on public_ip_attachments (creator);
