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

-- This migration extends the clamp of migration 129 to the first change time of a varying
-- field. The trigger does nothing on INSERT, so a resource can be created with a tracked value
-- and no time, and the fold keys that fact at the resource's creation timestamp. The first
-- update was then unchecked: a supplied time on or before the creation timestamp produced a
-- second fact with the same key as the creation-time fact, and shuffled delivery kept either
-- one, so the same history could bill from creation or not bill at all.
--
-- On UPDATE, when a varying value changes, no time is stored yet, and the supplied time is on
-- or before the resource's creation timestamp, the stored time becomes the creation timestamp
-- plus one microsecond. A first time after creation passes as supplied. For symmetry, the
-- database-clock stamp that migration 129 writes for a missing first time is never below the
-- creation fallback either.
--
-- The clamp logs the same WARNING shape as migration 129, so it is visible like the others.

create or replace function enforce_metering_fields() returns trigger as $$
declare
  tracked jsonb;
  field jsonb;
  field_path text[];
  time_path text[];
  old_time text;
  new_time text;
  clamped_time text;
  changed_times text[] := '{}';
  seen_times text[] := '{}';
begin
  if TG_OP != 'UPDATE' then
    return new;
  end if;

  tracked := coalesce(metering_tracked_fields(TG_TABLE_NAME), '[]'::jsonb);

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
      -- A value changed, so the write carries the time of the change. A time that is missing or
      -- not later than the stored one is wrong by at most the writer's clock skew, so the write
      -- is accepted and the stored time clamped: to the old stored time plus one microsecond,
      -- or, when no time is stored yet, to the database clock.
      if new_time is null or (old_time is not null and new_time::timestamptz <= old_time::timestamptz) then
        if old_time is null then
          -- The database clock, never below the creation fallback: a value observed at creation
          -- would otherwise collide with the creation-time fact in the fold.
          clamped_time := to_char(
            greatest(now(), new.creation_timestamp + interval '1 microsecond') at time zone 'utc',
            'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'
          );
        else
          clamped_time := to_char(
            (old_time::timestamptz + interval '1 microsecond') at time zone 'utc',
            'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'
          );
        end if;
        new.data := jsonb_set(new.data, time_path, to_jsonb(clamped_time));
        raise warning using
          message = format(
            'clamped %s of %s ''%s'': supplied %s, stored %s, stored time becomes %s',
            field->>'transition_time', TG_TABLE_NAME, new.id,
            coalesce(new_time, 'no time'), coalesce(old_time, 'no time'), clamped_time
          );
      elsif old_time is null and new_time::timestamptz <= new.creation_timestamp then
        -- The first time of a value collides with the fold's creation fallback when it is on or
        -- before the creation timestamp: the creation-time fact and this change would share one
        -- key, and shuffled delivery would keep either. The first time moves past the collision.
        clamped_time := to_char(
          (new.creation_timestamp + interval '1 microsecond') at time zone 'utc',
          'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'
        );
        new.data := jsonb_set(new.data, time_path, to_jsonb(clamped_time));
        raise warning using
          message = format(
            'clamped %s of %s ''%s'': supplied %s, created %s, stored time becomes %s',
            field->>'transition_time', TG_TABLE_NAME, new.id,
            coalesce(new_time, 'no time'),
            to_char(new.creation_timestamp at time zone 'utc', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
            clamped_time
          );
      end if;
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
