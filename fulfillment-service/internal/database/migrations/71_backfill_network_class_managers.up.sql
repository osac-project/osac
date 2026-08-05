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

-- Backfill fabric_manager on existing network classes.
-- New network classes get this set at creation time by the server; this migration
-- ensures pre-existing records are consistent. The fabric_manager is set to the
-- value of implementation_strategy, which historically served as the single
-- discriminator for the network backend.

UPDATE network_classes
SET data = jsonb_set(
  data,
  '{fabric_manager}',
  coalesce(to_jsonb(data->>'implementation_strategy'), '""')
)
WHERE NOT (data ? 'fabric_manager')
   OR data->>'fabric_manager' = '';

UPDATE archived_network_classes
SET data = jsonb_set(
  data,
  '{fabric_manager}',
  coalesce(to_jsonb(data->>'implementation_strategy'), '""')
)
WHERE NOT (data ? 'fabric_manager')
   OR data->>'fabric_manager' = '';
