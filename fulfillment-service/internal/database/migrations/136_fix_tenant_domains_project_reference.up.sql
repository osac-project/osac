--
-- Copyright (c) 2026 Red Hat Inc.
--
-- Licensed under the Apache License, Version 2.0 (the "License");
-- you may not use this file except in compliance with the License.
-- You may obtain a copy of the License at
--
--   http://www.apache.org/licenses/LICENSE-2.0
--
-- Unless required by applicable law or agreed to in writing, software
-- distributed under the License is distributed on an "AS IS" BASIS,
-- WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
-- See the License for the specific language governing permissions and
-- limitations under the License.
--

-- tenant_domains is a uniqueness helper derived from tenant.spec.domains. It
-- is tenant-scoped, not project-scoped. Migration 69 added a project column
-- and FK to every table with a tenant column, which made these helper rows
-- prevent deletion of a tenant's root project.
alter table tenant_domains
  drop constraint tenant_domains_project_fk,
  drop column project;

-- Restore the cascade that tenant_domains had before migration 68 replaced
-- tenant foreign keys. The helper rows should be removed with their tenant.
alter table tenant_domains
  drop constraint tenant_domains_tenant_fk,
  add constraint tenant_domains_tenant_fk
    foreign key (tenant) references tenants (name) on delete cascade not valid;
