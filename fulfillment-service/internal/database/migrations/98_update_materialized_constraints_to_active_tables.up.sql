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

-- Update the storage_tier_backends helper table to reference active_storage_tiers instead of the base storage_tiers
-- table. This ensures that when a storage tier is soft-deleted, its stale helper rows are automatically cleaned up
-- via ON DELETE CASCADE (the active_storage_tiers row is removed by the materialize_active_objects trigger, which
-- cascades to storage_tier_backends).
--
-- The tenant_domains and project_membership_subjects helper tables are NOT updated because their materialization
-- triggers fire unconditionally on UPDATE (no WHEN clause). Changing their FK to reference active_* tables would
-- cause FK violations when the trigger re-materializes during soft deletion.

alter table storage_tier_backends
  drop constraint storage_tier_backends_storage_tier_id_fkey;

alter table storage_tier_backends
  add constraint storage_tier_backends_storage_tier_id_fkey
  foreign key (storage_tier_id) references active_storage_tiers (id) on delete cascade;
