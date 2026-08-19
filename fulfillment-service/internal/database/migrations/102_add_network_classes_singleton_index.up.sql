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

-- Enforce "one NetworkClass per deployment" (unified networking design, OSAC-1433) at the database level via a
-- unique partial index on a constant expression. Since every non-soft-deleted row evaluates the indexed expression
-- to the same value (true), at most one such row can exist at a time — the second concurrent insert receives a
-- unique constraint violation.
--
-- This closes the TOCTOU race that an application-level "does one already exist" check alone cannot: two concurrent
-- Create requests could both pass that check before either commits. The server-side check (OSAC-4073) still runs
-- first to produce a clear, fast validation error in the common (non-racing) case; this index is the last line of
-- defense, analogous to how network_classes_single_default (migration 32) backs the is_default swap logic.
--
-- Soft-deleted rows (deletion_timestamp != epoch) are excluded so that deleting the only NetworkClass does not
-- block creating a new one.
create unique index network_classes_singleton
  on network_classes ((true))
  where deletion_timestamp = 'epoch';
