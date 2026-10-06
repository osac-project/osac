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

-- Durable log of object-table row changes. Object-table triggers insert the complete row as JSON and pg_notify the
-- event publisher, which converts committed rows into protobuf Events and publishes them to Kafka. The existing
-- notifications table and Watch NOTIFY path are unchanged. id and timestamp identify and order events; object identity
-- lives inside data. table and op are not part of the protobuf Event: the publisher uses them to select the payload
-- oneof field and EventType.

create table changes (
  id uuid primary key default uuidv7(),
  "table" text not null,
  op text not null,
  data jsonb not null,
  "timestamp" timestamptz not null default now()
);

create function enqueue_change() returns trigger as $$
declare
  rec record;
  change_id uuid;
begin
  if TG_OP = 'DELETE' then
    rec := old;
  else
    rec := new;
  end if;

  insert into changes (
    "table",
    op,
    data
  )
  values (
    TG_TABLE_NAME,
    TG_OP,
    to_jsonb(rec)
  )
  returning id into change_id;

  perform pg_notify('changes', change_id::text);

  if TG_OP = 'DELETE' then
    return old;
  end if;
  return new;
end;
$$ language plpgsql;

-- Install the trigger on every public object table, using the same exclusions as listObjectTables plus changes itself
-- so the table cannot recurse.
do $$
declare
  tbl text;
begin
  for tbl in
    select
      c.relname
    from
      pg_catalog.pg_class c
    join
      pg_catalog.pg_namespace n on n.oid = c.relnamespace
    where
      n.nspname = 'public' and
      c.relkind = 'r' and
      c.relname not like 'active_%' and
      c.relname not like 'archived_%' and
      c.relname not in (
        'changes',
        'notifications',
        'project_membership_subjects',
        'schema_migrations',
        'storage_tier_backends',
        'tenant_domains'
      )
  loop
    execute format(
      'create trigger enqueue_change
         after insert or update or delete on %I
         for each row
         execute function enqueue_change()',
      tbl
    );
  end loop;
end;
$$;
