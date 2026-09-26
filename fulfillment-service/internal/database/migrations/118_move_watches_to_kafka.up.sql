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

-- Watch now consumes events from Kafka, so the old PostgreSQL notification queue is no longer needed.
drop table notifications;

-- GenericDAO marks no-op updates with a transaction-local setting so that they produce signal events.
create or replace function enqueue_change() returns trigger as $$
declare
  rec record;
  change_op text;
  change_id uuid;
begin
  if TG_OP = 'DELETE' then
    rec := old;
  else
    rec := new;
  end if;

  if TG_OP = 'UPDATE' and current_setting('osac.signal', true) = 'on' then
    change_op := 'SIGNAL';
  else
    change_op := TG_OP;
  end if;

  insert into changes (
    "table",
    op,
    data
  )
  values (
    TG_TABLE_NAME,
    change_op,
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
