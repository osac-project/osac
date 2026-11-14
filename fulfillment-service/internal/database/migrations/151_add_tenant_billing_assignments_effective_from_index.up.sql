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

-- This migration adds the index backing the tenant billing assignments List default order.
--
-- Migration 150 created tenant_billing_assignments as append-only history: reassignment closes the active row and
-- inserts a new one, and rows are never pruned. The List default order is effective_from desc, so without an index
-- every unfiltered List paid a sequential scan plus a top-N sort that degraded as history grew. 150 is immutable
-- once applied, so the index lands here.
--

create index tenant_billing_assignments_by_effective_from
  on tenant_billing_assignments (effective_from desc);
