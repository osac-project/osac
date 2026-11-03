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

-- A deployment has exactly one NetworkClass record: a second create fails also while the first one is being deleted,
-- until its record is archived (unified networking design, OSAC-1433). Archived records live in
-- archived_network_classes, so the index covers every row of the table.
-- An earlier release allowed a new NetworkClass while the old one was being deleted. Such a database can't get the
-- index; fail with an error that tells the operator what to do instead of a bare unique violation.
do $$
declare
  row_count bigint;
begin
  select count(*) into row_count from network_classes;
  if row_count > 1 then
    raise exception 'cannot upgrade: % NetworkClass records exist, but a deployment has exactly one; finish '
      'deleting the old NetworkClass (wait until its record is removed) or reinstall, then upgrade again',
      row_count;
  end if;
end
$$;

drop index if exists network_classes_singleton;

create unique index network_classes_singleton
  on network_classes ((true));
