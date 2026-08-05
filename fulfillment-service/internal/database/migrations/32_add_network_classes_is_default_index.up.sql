--
-- Copyright (c) 2025 Red Hat Inc.
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

-- Enforce the single-default invariant at the database level via a unique partial index.
-- Only one active (non-soft-deleted) NetworkClass can have is_default=true at a time.
-- This prevents the TOCTOU race where concurrent default-swap requests could both succeed,
-- leaving multiple defaults. The second concurrent transaction receives a unique constraint
-- violation, preserving the invariant.
--
-- Soft-deleted rows (deletion_timestamp != zero) are excluded so that deleting a default NC
-- does not block setting a new default.
--
-- Also serves as a performance index for findDefaultNetworkClass, which filters with
-- 'this.is_default == true' (translates to cast(data->>'is_default' as bool) = true).
create unique index network_classes_single_default
  on network_classes ((cast(data->>'is_default' as bool)))
  where cast(data->>'is_default' as bool) = true
    and deletion_timestamp = 'epoch';
