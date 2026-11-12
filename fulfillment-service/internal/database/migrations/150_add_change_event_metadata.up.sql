--
-- Copyright (c) 2026 Red Hat Inc.
--
-- Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
-- the License. You may obtain a copy of the License at
--
--   http://www.apache.org/licenses/LICENSE-2.0
--

alter table changes
  add column reason text,
  add column severity text;

create or replace function enqueue_change() returns trigger as $$
declare
  rec record;
  change_op text;
  change_id uuid;
  event_reason text := nullif(current_setting('osac.event_reason', true), '');
  event_severity text := nullif(current_setting('osac.event_severity', true), '');
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
    data,
    reason,
    severity
  )
  values (
    TG_TABLE_NAME,
    change_op,
    to_jsonb(rec),
    event_reason,
    event_severity
  )
  returning id into change_id;

  perform pg_notify('changes', change_id::text);

  if TG_OP = 'DELETE' then
    return old;
  end if;
  return new;
end;
$$ language plpgsql;
