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

-- Partial index over the quarantined rows added by migration 126. The publisher checks for quarantined rows on every
-- drain and needs the earliest quarantined id per tenant to calculate hold-back boundaries, so the check is a cheap
-- indexed scan even on a large table. The index is built concurrently so the build doesn't block writes to 'changes':
-- every resource write inserts into the table through the enqueue trigger, and on a table backed up while the
-- publisher is down a plain build could block those writes for its whole duration. 'create index concurrently'
-- cannot run inside a transaction or together with other statements, hence this dedicated single-statement
-- migration, like migration 115.
--
-- If the concurrent build fails partway it leaves an invalid 'changes_quarantined' index behind. Recovery: drop the
-- invalid index ('drop index changes_quarantined;') and re-run this migration so the index is built again. The
-- schema check refuses to start the service while the index is missing or invalid, so the invalid state is detected
-- rather than silently skipping the index.

create index concurrently changes_quarantined on changes (id) where quarantined_at is not null;
