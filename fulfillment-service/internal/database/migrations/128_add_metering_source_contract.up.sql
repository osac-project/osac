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

-- This migration enforces the metering source contract on the billable resources: the writes
-- that would corrupt the event log metering computes usage from roll back.
--
-- The tracked fields of each table are the ones annotated for metering on the resource's proto
-- message. The mapping below holds, per table, each field's proto path inside the row's 'data'
-- JSON document, its kind, and, for varying fields, the path of the field that holds the time
-- of the last change. The drift test in this package compares this mapping with the registry
-- generated from the proto annotations, so the two can't drift apart silently.
--
-- The rules, for every update of a table with tracked fields, including direct SQL:
--
-- 1. A changed varying field must carry a later change time. The write rolls back otherwise.
--
-- 2. A write that changes a change time without changing its value keeps the old time. The
--    write succeeds. Two writers with different clocks would otherwise turn one fact into two.
--
-- 3. Constant fields never change, and fixed-once-set fields never change once they are set.
--    The write rolls back. A fixed-once-set field that is still empty may be set once.
--
-- Timestamps are stored inside 'data' as RFC 3339 text and are cast to timestamptz for
-- comparison, so fractional seconds compare correctly.
--
-- Violations fail with SQLSTATE 'Z0006', the code reserved for this contract. Only buggy
-- writers or direct SQL can produce them.

-- The tracked fields of each table with metering-tracked fields. Extend together with the
-- metering annotations on the private protos.
create or replace function metering_tracked_fields(source_table text) returns jsonb as $$
  select case source_table
    when 'compute_instances' then $tracked$
      [
        {"path": "status.instance_type", "kind": "varies", "transition_time": "status.instance_type_transition_time"},
        {"path": "status.state", "kind": "varies", "transition_time": "status.state_transition_time"},
        {"path": "spec.boot_disk.size_gib", "kind": "constant"},
        {"path": "spec.disk_image", "kind": "constant"}
      ]
    $tracked$::jsonb
    when 'clusters' then $tracked$
      [
        {"path": "spec.version", "kind": "varies", "transition_time": "status.version_transition_time"},
        {"path": "status.state", "kind": "varies", "transition_time": "status.state_transition_time"},
        {"path": "spec.template", "kind": "constant"}
      ]
    $tracked$::jsonb
    when 'bare_metal_instances' then $tracked$
      [
        {"path": "status.state", "kind": "varies", "transition_time": "status.state_transition_time"},
        {"path": "spec.instance_type", "kind": "constant"},
        {"path": "status.cluster", "kind": "fixed_once_set"},
        {"path": "status.node_set", "kind": "fixed_once_set"}
      ]
    $tracked$::jsonb
    when 'volumes' then $tracked$
      [
        {"path": "status.state", "kind": "varies", "transition_time": "status.state_transition_time"},
        {"path": "spec.storage_tier", "kind": "constant"},
        {"path": "status.protocol", "kind": "constant"},
        {"path": "status.provisioned_size_gib", "kind": "fixed_once_set"},
        {"path": "status.vendor_volume_id", "kind": "fixed_once_set"}
      ]
    $tracked$::jsonb
    when 'external_ips' then $tracked$
      [
        {"path": "status.address", "kind": "fixed_once_set"},
        {"path": "status.attached", "kind": "varies", "transition_time": "status.attachment_transition_time"},
        {"path": "status.attribution", "kind": "varies", "transition_time": "status.attachment_transition_time"},
        {"path": "status.state", "kind": "varies", "transition_time": "status.state_transition_time"},
        {"path": "spec.pool", "kind": "constant"}
      ]
    $tracked$::jsonb
    when 'nat_gateways' then $tracked$
      [
        {"path": "status.state", "kind": "varies", "transition_time": "status.state_transition_time"},
        {"path": "spec.external_ip", "kind": "constant"},
        {"path": "spec.virtual_network", "kind": "constant"}
      ]
    $tracked$::jsonb
  end;
$$ language sql immutable;

-- The source contract checks. The trigger is named to fire after 'check_owner_reference' and
-- before 'enqueue_change': triggers on the same event fire in name order. The owner-reference
-- trigger rejects a changed owner reference with its own error, so this trigger only sees
-- writes that pass it, and the change event is enqueued after this trigger has kept the old
-- time. The change time is kept per time field, not per tracked field, because varying fields
-- can share one: an external IP's attachment and attribution both move
-- 'attachment_transition_time'.
create function enforce_metering_fields() returns trigger as $$
declare
  tracked jsonb;
  field jsonb;
  field_path text[];
  time_path text[];
  old_time text;
  new_time text;
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
    if old.data #> field_path is distinct from new.data #> field_path then
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
    if old.data #> field_path is distinct from new.data #> field_path then
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
      -- A value changed, so the write must carry a later time:
      if new_time is null or (old_time is not null and new_time::timestamptz <= old_time::timestamptz) then
        raise exception using
          errcode = 'Z0006',
          message = format(
            'a change of %s ''%s'' needs a later %s',
            TG_TABLE_NAME, new.id, field->>'transition_time'
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
    if metering_tracked_fields(tbl) is not null then
      execute format(
        'create trigger enforce_metering_fields
           before update on %I
           for each row
           execute function enforce_metering_fields()',
        tbl);
    end if;
  end loop;
end;
$$;
