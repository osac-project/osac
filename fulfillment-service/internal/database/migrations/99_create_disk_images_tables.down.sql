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

-- Reverse migration 99: drop disk_images tables, triggers, indexes.

-- Drop active companion table.
drop table if exists active_disk_images;

-- Drop JSONB indexes on referencing tables.
drop index if exists compute_instance_templates_disk_image;
drop index if exists compute_instances_disk_image;

-- Drop Z0003 reverse-reference trigger and function.
drop trigger if exists check_disk_image_not_in_use on disk_images;
drop function if exists check_disk_image_not_in_use();

-- Drop immutability trigger.
drop trigger if exists check_immutable_columns on disk_images;

-- Drop foreign key constraints.
alter table disk_images drop constraint if exists disk_images_project_fk;
alter table disk_images drop constraint if exists disk_images_tenant_fk;

-- Drop indexes.
drop index if exists disk_images_unique_name_per_tenant;
drop index if exists disk_images_by_label;
drop index if exists disk_images_by_tenant;
drop index if exists disk_images_by_creator;

-- Drop tables.
drop table if exists archived_disk_images;
drop table if exists disk_images;
