--
-- Copyright (c) 2026 Red Hat Inc.
--
-- Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
-- the License. You may obtain a copy of the License at
--
--   http://www.apache.org/licenses/LICENSE-2.0
--
-- Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
-- "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
-- specific language governing permissions and limitations under the License.
--

-- This migration adds a missing index on compute_instance_templates to support the instance_type
-- deletion protection trigger.
--
-- Migration 90 added a trigger that prevents deleting an instance_type while compute_instance_templates
-- still reference it, but never added the supporting index. Without this index, every instance_type
-- delete performs a full sequential scan of compute_instance_templates. This index mirrors the pattern
-- established for disk_image in migration 99.
--

create index compute_instance_templates_instance_type
  on compute_instance_templates ((data->'spec_defaults'->'instance_type'->>'id'))
  where deletion_timestamp = 'epoch';
