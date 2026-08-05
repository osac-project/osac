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

-- This migration creates a reusable trigger function that enforces immutability on specified columns. The column names
-- are passed as trigger arguments, so the same function can be attached to any table with different sets of immutable
-- columns. The trigger converts OLD and NEW rows to JSONB and compares the values of the specified columns, raising an
-- exception if any of them have changed.

create function check_immutable_columns() returns trigger as $$
declare
  i integer;
  n integer;
  col text;
  old_json jsonb;
  new_json jsonb;
  changed text[];
  changed_json jsonb;
  quoted text[];
  columns_text text;
begin
  old_json := to_jsonb(old);
  new_json := to_jsonb(new);
  changed := '{}';
  for i in 0..tg_nargs - 1 loop
    col := tg_argv[i];
    if old_json -> col is distinct from new_json -> col then
      changed := array_append(changed, col);
    end if;
  end loop;
  n := coalesce(array_length(changed, 1), 0);
  if n > 0 then
    changed_json := to_jsonb(changed);
    quoted := '{}';
    for i in 1..n loop
      quoted := array_append(quoted, format('''%s''', changed[i]));
    end loop;
    if n = 1 then
      raise exception using
        errcode = 'Z0001',
        message = format('column ''%s'' of table ''%s'' is immutable', changed[1], tg_table_name),
        detail = changed_json::text;
    else
      columns_text := array_to_string(quoted[1:n-1], ', ') || ' and ' || quoted[n];
      raise exception using
        errcode = 'Z0001',
        message = format('columns %s of table ''%s'' are immutable', columns_text, tg_table_name),
        detail = changed_json::text;
    end if;
  end if;
  return new;
end;
$$ language plpgsql;

-- Attach the trigger to the organizations table to enforce immutability of the name and tenant columns:
create trigger check_immutable_columns
  before update on organizations
  for each row
  execute function check_immutable_columns('name', 'tenant');
