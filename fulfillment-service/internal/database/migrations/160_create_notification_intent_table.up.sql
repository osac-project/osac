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

-- Durable accept/dedupe + fan-out outbox for osac-notifications Kafka ingest.
-- Not an API object: unique on CloudEvent (osactenant, source, id).
-- status=pending means inbox fan-out still needed; completed means fan-out finished;
-- failed means permanent fan-out failure (also written to the tenant DLQ topic).
-- Kafka offset may be committed after insert; a drain loop finishes pending rows.

create table notification_intent (
  tenant text not null,
  source text not null,
  id text not null,
  kind text not null,
  type text not null,
  severity text not null,
  topic text not null,
  payload jsonb not null,
  status text not null default 'pending',
  creation_timestamp timestamp with time zone not null default now(),
  primary key (tenant, source, id),
  constraint notification_intent_status_check check (status in ('pending', 'completed', 'failed'))
);

create index notification_intent_by_creation on notification_intent (creation_timestamp);

create index notification_intent_pending_by_creation
  on notification_intent (creation_timestamp)
  where status = 'pending';
