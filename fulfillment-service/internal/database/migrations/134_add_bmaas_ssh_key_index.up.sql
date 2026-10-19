--
-- Copyright (c) 2026 Red Hat, Inc.
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

-- BMaaS stores only the canonical Secret ID in spec.ssh_key. This index
-- supports looking up instances by that reference. It is created
-- concurrently to avoid blocking writes to bare_metal_instances during the
-- build, matching the bare_metal_instances_disk_image precedent in migration
-- 115. It is a standalone single-statement migration so the statement
-- autocommits, which CREATE INDEX CONCURRENTLY requires.
create index concurrently bare_metal_instances_ssh_key on bare_metal_instances
  ((data->'spec'->'ssh_key'->>'id'))
  where data->'spec'->'ssh_key'->>'id' is not null;
