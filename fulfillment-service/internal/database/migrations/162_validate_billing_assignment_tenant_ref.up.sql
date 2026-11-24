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

-- New assignment writes require a live tenant. This is a write-time reference check rather than a foreign key because
-- closed assignment history must remain in this table after the live tenant row is physically deleted.
create function check_tenant_billing_assignment_tenant_ref() returns trigger as $$
declare
  live_tenant text;
begin
  select t.name into live_tenant
  from tenants t
  where t.name = new.tenant
    and t.deletion_timestamp = 'epoch'::timestamptz
  for key share;

  if not found then
    raise exception using
      errcode = 'Z0002',
      message = format('tenant ''%s'' does not exist or is deleting', new.tenant);
  end if;

  return new;
end;
$$ language plpgsql;

create trigger check_tenant_billing_assignment_tenant_ref
  before insert or update of tenant on tenant_billing_assignments
  for each row execute function check_tenant_billing_assignment_tenant_ref();
