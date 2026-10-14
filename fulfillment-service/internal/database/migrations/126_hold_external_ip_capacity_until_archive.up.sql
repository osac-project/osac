-- Keep ExternalIP consumers exclusive until their rows are archived.
-- These non-partial indexes include terminating rows for the exclusivity lookup.
create index external_ip_attachments_by_external_ip_ref
  on external_ip_attachments ((data->'spec'->'external_ip'->>'id'));
create index external_ip_attachments_by_external_ip_name_ref
  on external_ip_attachments ((data->'spec'->'external_ip'->>'name'));
create index nat_gateways_by_external_ip_ref
  on nat_gateways ((data->'spec'->'external_ip'->>'id'));
create index nat_gateways_by_external_ip_name_ref
  on nat_gateways ((data->'spec'->'external_ip'->>'name'));

create or replace function check_external_ip_available() returns trigger as $$
declare
  eip_id text;
  locked_id text;
  consumer_id text;
  consumer_type text;
begin
  eip_id := new.data->'spec'->'external_ip'->>'id';
  if coalesce(eip_id, '') = '' then
    return new;
  end if;

  select id into locked_id from external_ips where id = eip_id for update;
  if locked_id is null then
    return new;
  end if;

  select id, type into consumer_id, consumer_type
  from (
    select id, 'ExternalIPAttachment' as type
    from external_ip_attachments
    where data->'spec'->'external_ip'->>'id' = eip_id
    union all
    select id, 'NATGateway' as type
    from nat_gateways
    where data->'spec'->'external_ip'->>'id' = eip_id
  ) consumers
  limit 1;

  if consumer_id is not null then
    raise exception using
      errcode = 'Z0004',
      message = format(
        'ExternalIP ''%s'' is already in use by %s ''%s''',
        eip_id, consumer_type, consumer_id
      );
  end if;
  return new;
end;
$$ language plpgsql;

-- Rebuild counters from every ExternalIP still in the primary table.
do $$
declare
  pool_row record;
  allocated_count bigint;
  total_count bigint;
begin
  for pool_row in select id, data from external_ip_pools loop
    select count(*) into allocated_count
    from external_ips
    where data->'spec'->'pool'->>'id' = pool_row.id;

    if pool_row.data->'status'->>'total' is null then
      raise exception 'ExternalIPPool % has incomplete capacity status', pool_row.id;
    end if;
    total_count := (pool_row.data->'status'->>'total')::bigint;

    update external_ip_pools
    set data = jsonb_set(
          data,
          '{status}',
          coalesce(data->'status', '{}'::jsonb) ||
            jsonb_build_object(
              'allocated', allocated_count::text,
              'available', greatest(total_count - allocated_count, 0)::text
            ),
          true
        ),
        version = version + 1
    where id = pool_row.id;
  end loop;
end;
$$;

-- Keep a pool while any ExternalIP row still references it.
create or replace function check_external_ip_pool_not_in_use() returns trigger as $$
begin
  if exists (
    select 1 from external_ips
    where data->'spec'->'pool'->>'id' = old.id
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete ExternalIPPool ''%s'': it is in use by at least one ExternalIP',
        old.id
      );
  end if;
  return new;
end;
$$ language plpgsql;

-- Capacity returns in the same transaction that removes the archived ExternalIP row.
create function release_external_ip_pool_capacity() returns trigger as $$
declare
  pool_id text;
  pool_data jsonb;
  allocated_count bigint;
  total_count bigint;
  available_count bigint;
begin
  pool_id := old.data->'spec'->'pool'->>'id';
  if coalesce(pool_id, '') = '' then
    return old;
  end if;

  select data into pool_data
  from external_ip_pools where id = pool_id for update;
  if not found then
    raise exception 'ExternalIPPool % is missing while archiving an ExternalIP', pool_id;
  end if;
  if pool_data->'status'->>'allocated' is null or pool_data->'status'->>'total' is null then
    raise exception 'ExternalIPPool % has incomplete capacity status', pool_id;
  end if;

  allocated_count := greatest((pool_data->'status'->>'allocated')::bigint - 1, 0);
  total_count := (pool_data->'status'->>'total')::bigint;
  available_count := greatest(total_count - allocated_count, 0);

  update external_ip_pools
  set data = jsonb_set(
        data,
        '{status}',
        coalesce(data->'status', '{}'::jsonb) ||
          jsonb_build_object(
            'allocated', allocated_count::text,
            'available', available_count::text
          ),
        true
      ),
      version = version + 1
  where id = pool_id;
  return old;
end;
$$ language plpgsql;

create trigger release_external_ip_pool_capacity
  after delete on external_ips
  for each row
  execute function release_external_ip_pool_capacity();
