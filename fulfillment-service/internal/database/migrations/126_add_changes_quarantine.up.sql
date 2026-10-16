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

-- Quarantine for unpublishable change rows. The metering design rebuilds all usage from the per-tenant Kafka topics
-- fed by the changes table, so a captured change must never be lost. Until now the event publisher deleted rows that
-- could not be converted to events; from now on it marks them with 'quarantined_at' and 'quarantine_reason' and holds
-- back that tenant's later rows so the topic stays in order. An operator fixes the cause and republishes by clearing
-- the two columns, which makes the row eligible for the drain again.

alter table changes
  add column quarantined_at timestamptz,
  add column quarantine_reason text;

-- The partial index over the quarantined rows is created by migration 127, in its own file, because
-- it is built concurrently and 'create index concurrently' cannot run together with other
-- statements.
