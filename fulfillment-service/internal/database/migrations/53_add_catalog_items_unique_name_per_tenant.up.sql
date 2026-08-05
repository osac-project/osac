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

-- Enforce that catalog item names are unique within each tenant scope.
--
-- Without this constraint, an admin can create multiple catalog items with the same
-- metadata.name (e.g., two "dev-sandbox" items), which causes ambiguity when the CLI
-- resolves catalog items by name (--catalog-item dev-sandbox).
--
-- The partial unique index covers only active (non-deleted) rows where name is set.
-- The tenant column determines the scope (migration 40 converted it from text[] to text).

create unique index cluster_catalog_items_unique_name_per_tenant
  on cluster_catalog_items (name, tenant)
  where deletion_timestamp = 'epoch'
    and name != '';

create unique index compute_instance_catalog_items_unique_name_per_tenant
  on compute_instance_catalog_items (name, tenant)
  where deletion_timestamp = 'epoch'
    and name != '';
