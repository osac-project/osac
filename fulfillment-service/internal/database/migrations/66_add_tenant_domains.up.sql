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

-- Materialized table that enforces global uniqueness of tenant e-mail domains. Each domain may belong to at most one
-- tenant. The primary key on `domain` provides the uniqueness guarantee and PostgreSQL's row-level locking on the
-- unique index ensures correct behavior under concurrent transactions.
create table tenant_domains (
  domain text not null primary key,
  tenant text not null references tenants(id) on delete cascade
);
create index tenant_domains_by_tenant on tenant_domains (tenant);

-- Trigger function that keeps the materialized table in sync with the domains stored in the tenant's JSONB data column.
-- It replaces the set of domains for the tenant on every insert or update, catching unique violations and raising a
-- custom error (SQLSTATE Z0004) with a descriptive message.
create function materialize_tenant_domains() returns trigger as $$
declare
  domain_value text;
begin
  -- Remove previous domain entries for this tenant:
  delete from tenant_domains where tenant = new.id;

  -- Insert the current domains from the JSONB data column:
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

create trigger materialize_tenant_domains
  after insert or update on tenants
  for each row
  execute function materialize_tenant_domains();
