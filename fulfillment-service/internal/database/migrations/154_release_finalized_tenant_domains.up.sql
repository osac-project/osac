--
-- Copyright (c) 2026 Red Hat, Inc.
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

-- Billing history can retain a tenant after cleanup instead of physically deleting it. Preserve the historical
-- spec.domains, but release its uniqueness helpers once deletion is marked AND no finalizers remain. This is the
-- user-approved cleanup compatibility policy, not a domain-release timing requirement of the merged billing design
-- (OSAC-3784, 051ac691925218d5fb8ae745918c7099a4a7e81e) or organization design (OSAC-1030).
-- Serialize the trigger replacement/backfill against tenant writes for the duration of this migration.
begin;
lock table tenants in share row exclusive mode;

create or replace function materialize_tenant_domains() returns trigger as $$
declare
  domain_value text;
begin
  -- Freeze existing reservations during cleanup; release them only at completion. Never acquire new ownership
  -- for a deleting tenant, even if a later private update changes its historical domains or finalizers.
  if new.deletion_timestamp != 'epoch' then
    if cardinality(new.finalizers) = 0 then
      delete from tenant_domains where tenant = new.id;
    end if;
    return new;
  end if;

  delete from tenant_domains where tenant = new.id;

  for domain_value in select jsonb_array_elements_text(new.data->'spec'->'domains')
  loop
    begin
      insert into tenant_domains (domain, tenant) values (domain_value, new.id);
    exception when unique_violation then
      raise exception using
        errcode = 'Z0004',
        message = format('value ''%s'' in field ''spec.domains'' is already used by another tenant', domain_value);
    end;
  end loop;

  return new;
end;
$$ language plpgsql;

-- Repair already-retained, finalized tombstones without updating historical tenant or assignment records.
delete from tenant_domains domains using tenants tenant
where domains.tenant = tenant.id
  and tenant.deletion_timestamp != 'epoch'
  and cardinality(tenant.finalizers) = 0;
commit;
