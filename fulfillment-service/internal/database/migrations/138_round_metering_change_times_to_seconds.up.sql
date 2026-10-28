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

-- This migration moves the metering source contract to whole-second times and removes the
-- clamp machinery that mixed precision made necessary.
--
-- No source clock in the pipeline is finer than a second: Kubernetes CR timestamps carry no
-- fractional part, so every state time the operator reports is second-granular, while the
-- database clock and the service's own stamps carried microseconds. That mix is what the
-- clamps of migrations 129, 131, and 133 patched, and what migration 137's rejection turned
-- into a permanent wedge whenever a truncated CR time landed against a microsecond stored
-- time in the same second (run 40244192868: every ExternalIP and NATGateway delete).
--
-- The guard now normalizes every supplied time to whole seconds at the storage boundary and
-- rejects only a time strictly earlier than the stored one, which no truncation can produce.
-- An equal time is stored as supplied: two changes of one field within the same second
-- collapse to the value the event log carries last, and the fold applies events in log
-- order. The first time of a value is stored as supplied too -- the creation-fallback clamp
-- of migration 131 is gone: a first fact has no earlier time to be consistent with, and the
-- fold keys it exactly where the writer said it happened.
--
-- Stored times written before this migration are normalized first. The guard and the
-- outbox triggers are disabled per table while its own rows are rewritten, or the guard's
-- keep-the-old-time branch would revert the normalization and the outbox would publish a
-- change event for every row.

do $$
declare
  tbl text;
  field jsonb;
  time_path text[];
begin
  for tbl in
    select pg_catalog.pg_class.relname
    from pg_catalog.pg_class
    join pg_catalog.pg_namespace
      on pg_catalog.pg_namespace.oid = pg_catalog.pg_class.relnamespace
    where
      pg_catalog.pg_namespace.nspname = 'public' and
      pg_catalog.pg_class.relkind = 'r' and
      pg_catalog.pg_class.relname not like 'active_%' and
      pg_catalog.pg_class.relname not like 'archived_%' and
      pg_catalog.pg_class.relname not in (
        'changes',
        'notifications',
        'project_membership_subjects',
        'schema_migrations',
        'storage_tier_backends',
        'tenant_domains'
      )
  loop
    if metering_tracked_fields(tbl) is null then
      continue;
    end if;
    execute format('alter table %I disable trigger enforce_metering_fields', tbl);
    execute format('alter table %I disable trigger enqueue_change', tbl);
    for field in select * from jsonb_array_elements(metering_tracked_fields(tbl)) loop
      if field->>'kind' != 'varies' then
        continue;
      end if;
      time_path := string_to_array(field->>'transition_time', '.');
      execute format(
        'update %I set data = jsonb_set(
           data, %L,
           to_jsonb(to_char(
             date_trunc(''second'', (data #>> %L)::timestamptz) at time zone ''utc'',
             ''YYYY-MM-DD"T"HH24:MI:SS"Z"''
           ))
         )
         where data #>> %L is not null
           and (data #>> %L)::timestamptz <> date_trunc(''second'', (data #>> %L)::timestamptz)',
        tbl, time_path, time_path, time_path, time_path, time_path);
    end loop;
    execute format('alter table %I enable trigger enqueue_change', tbl);
    execute format('alter table %I enable trigger enforce_metering_fields', tbl);
  end loop;
end;
$$;

create or replace function enforce_metering_fields() returns trigger as $$
declare
  tracked jsonb;
  field jsonb;
  field_path text[];
  time_path text[];
  old_time text;
  new_time text;
  second_time text;
  changed_times text[] := '{}';
  seen_times text[] := '{}';
begin
  tracked := coalesce(metering_tracked_fields(TG_TABLE_NAME), '[]'::jsonb);

  if TG_OP = 'INSERT' then
    -- Normalize the supplied times to whole seconds on insert as well: a first
    -- time has no earlier stored time to compare against, but a microsecond
    -- first time stored verbatim would wedge the resource the same way this
    -- migration removes for updates -- the first whole-second change after it
    -- is earlier than the stored microseconds and rolls back forever.
    for field in select * from jsonb_array_elements(tracked) loop
      if field->>'kind' != 'varies' then
        continue;
      end if;
      time_path := string_to_array(field->>'transition_time', '.');
      if new.data #>> time_path is not null then
        new.data := jsonb_set(new.data, time_path, to_jsonb(to_char(
          date_trunc('second', (new.data #>> time_path)::timestamptz) at time zone 'utc',
          'YYYY-MM-DD"T"HH24:MI:SS"Z"'
        )));
      end if;
    end loop;
    return new;
  end if;

  -- Collect the change times whose value changed:
  for field in select * from jsonb_array_elements(tracked) loop
    if field->>'kind' != 'varies' then
      continue;
    end if;
    field_path := string_to_array(field->>'path', '.');
    if not metering_field_equal(old.data #> field_path, new.data #> field_path) then
      if not (field->>'transition_time' = any(changed_times)) then
        changed_times := array_append(changed_times, field->>'transition_time');
      end if;
    end if;
  end loop;

  -- Constant fields never change, and fixed-once-set fields never change once they are set:
  for field in select * from jsonb_array_elements(tracked) loop
    if field->>'kind' = 'varies' then
      continue;
    end if;
    field_path := string_to_array(field->>'path', '.');
    if not metering_field_equal(old.data #> field_path, new.data #> field_path) then
      if field->>'kind' = 'constant' or old.data #> field_path is not null then
        raise exception using
          errcode = 'Z0006',
          message = format(
            'cannot change %s of %s ''%s'': it never changes after it is set',
            field->>'path', TG_TABLE_NAME, new.id
          );
      end if;
    end if;
  end loop;

  -- Check each change time once:
  for field in select * from jsonb_array_elements(tracked) loop
    if field->>'kind' != 'varies' then
      continue;
    end if;
    if field->>'transition_time' = any(seen_times) then
      continue;
    end if;
    seen_times := array_append(seen_times, field->>'transition_time');
    time_path := string_to_array(field->>'transition_time', '.');
    old_time := old.data #>> time_path;
    new_time := new.data #>> time_path;

    if field->>'transition_time' = any(changed_times) then
      -- A value changed, so the write carries the time of the change. A missing time means
      -- the writer does not know when the change happened, and a time a full second or more
      -- before the stored one means the writer is backdating the new value: both are bugs,
      -- and the write rolls back. The supplied time is normalized to whole seconds first:
      -- no source clock in the pipeline is finer, so finer digits are dropped rather than
      -- compared. A normalized time equal to the stored one is stored as supplied -- two
      -- changes within one second collapse to the value the event log carries last.
      if new_time is null then
        raise exception using
          errcode = 'Z0006',
          message = format(
            'a change of %s ''%s'' needs a later %s',
            TG_TABLE_NAME, new.id, field->>'transition_time'
          );
      end if;
      second_time := to_char(
        date_trunc('second', new_time::timestamptz) at time zone 'utc',
        'YYYY-MM-DD"T"HH24:MI:SS"Z"'
      );
      if old_time is not null and second_time::timestamptz < old_time::timestamptz then
        raise exception using
          errcode = 'Z0006',
          message = format(
            'a change of %s ''%s'' needs a later %s',
            TG_TABLE_NAME, new.id, field->>'transition_time'
          );
      end if;
      new.data := jsonb_set(new.data, time_path, to_jsonb(second_time));
    elsif (new.data #> time_path) is distinct from (old.data #> time_path) then
      -- No value changed, so the old time is the time of the fact. A new time would turn one
      -- fact into two, so the trigger keeps the old one.
      if old.data #> time_path is null then
        new.data := new.data #- time_path;
      else
        new.data := jsonb_set(new.data, time_path, old.data #> time_path);
      end if;
    end if;
  end loop;

  return new;
end;
$$ language plpgsql;

-- The trigger migration 128 installed fires only on UPDATE, so the insert
-- normalization above never ran: recreate it to fire on INSERT and UPDATE.
do $$
declare
  tbl text;
begin
  for tbl in
    select pg_catalog.pg_class.relname
    from pg_catalog.pg_class
    join pg_catalog.pg_namespace
      on pg_catalog.pg_namespace.oid = pg_catalog.pg_class.relnamespace
    where
      pg_catalog.pg_namespace.nspname = 'public' and
      pg_catalog.pg_class.relkind = 'r' and
      pg_catalog.pg_class.relname not like 'active_%' and
      pg_catalog.pg_class.relname not like 'archived_%' and
      pg_catalog.pg_class.relname not in (
        'changes',
        'notifications',
        'project_membership_subjects',
        'schema_migrations',
        'storage_tier_backends',
        'tenant_domains'
      )
  loop
    if metering_tracked_fields(tbl) is null then
      continue;
    end if;
    execute format('drop trigger if exists enforce_metering_fields on %I', tbl);
    execute format(
      'create trigger enforce_metering_fields
         before insert or update on %I
         for each row
         execute function enforce_metering_fields()',
      tbl);
  end loop;
end;
$$;
