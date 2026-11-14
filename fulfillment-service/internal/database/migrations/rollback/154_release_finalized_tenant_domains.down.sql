-- Optional rollback script, deliberately outside the service's up-only catalog.
-- Restore the old reservation invariant atomically. If a released domain has been reused, rollback is unsafe:
-- the unique violation aborts this transaction without stealing ownership or erasing historical domain text.
-- Resolve that policy/data conflict before retrying; do not bypass it with ON CONFLICT DO NOTHING.
begin;
lock table tenants in share row exclusive mode;

insert into tenant_domains (domain, tenant)
select domain.value, tenant.id
from tenants tenant
cross join lateral jsonb_array_elements_text(tenant.data->'spec'->'domains') domain(value)
where not exists (
  select 1 from tenant_domains existing where existing.domain = domain.value and existing.tenant = tenant.id
);

create or replace function materialize_tenant_domains() returns trigger as $$
declare
  domain_value text;
begin
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
commit;
