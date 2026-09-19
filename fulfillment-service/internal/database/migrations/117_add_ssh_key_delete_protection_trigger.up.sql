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

-- These indexes prepare the future ComputeInstance and BareMetalInstance SSH
-- key reference fields without changing those API contracts in this story.
create index compute_instances_ssh_key
  on compute_instances ((data->'spec'->'ssh_key'->>'id'))
  where deletion_timestamp = 'epoch'
    and data->'spec'->'ssh_key'->>'id' is not null;

create index bare_metal_instances_ssh_key
  on bare_metal_instances ((data->'spec'->'ssh_key'->>'id'))
  where deletion_timestamp = 'epoch'
    and data->'spec'->'ssh_key'->>'id' is not null;

create function check_ssh_key_not_in_use() returns trigger as $$
begin
  if exists (
    select 1
    from active_compute_instances a
    join compute_instances c on c.id = a.id
    where c.data->'spec'->'ssh_key'->>'id' = old.id
  ) or exists (
    select 1
    from active_bare_metal_instances a
    join bare_metal_instances b on b.id = a.id
    where b.data->'spec'->'ssh_key'->>'id' = old.id
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format('cannot delete SSH key ''%s'': it is in use by at least one active instance', old.id);
  end if;

  return new;
end;
$$ language plpgsql;

create trigger check_ssh_key_not_in_use
  before update on ssh_keys
  for each row
  when (old.deletion_timestamp = 'epoch' and new.deletion_timestamp != 'epoch')
  execute function check_ssh_key_not_in_use();
