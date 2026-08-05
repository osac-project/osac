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

-- Create tables for NATGateway resources.
--
-- NATGateway provides a dedicated outbound NAT (SNAT) for resources in a VirtualNetwork, giving
-- them a known, stable source IP for egress traffic. One NATGateway is allowed per VirtualNetwork.
-- Both spec fields (virtual_network, external_ip) are immutable after creation.

create table nat_gateways (
  id text not null primary key,
  name text not null default '',
  creation_timestamp timestamp with time zone not null default now(),
  deletion_timestamp timestamp with time zone not null default 'epoch',
  finalizers text[] not null default '{}',
  creator text not null default '',
  tenant text not null default '',
  project ltree not null default ''::ltree,
  labels jsonb not null default '{}'::jsonb,
  annotations jsonb not null default '{}'::jsonb,
  data jsonb not null,
  version integer not null default 0
);

create table archived_nat_gateways (
  id text not null,
  name text not null default '',
  creation_timestamp timestamp with time zone not null,
  deletion_timestamp timestamp with time zone not null,
  archival_timestamp timestamp with time zone not null default now(),
  creator text not null default '',
  tenant text not null default '',
  project ltree not null default ''::ltree,
  labels jsonb not null default '{}'::jsonb,
  annotations jsonb not null default '{}'::jsonb,
  data jsonb not null,
  version integer not null default 0
);

create index nat_gateways_by_name on nat_gateways (name);
create index nat_gateways_by_creator on nat_gateways (creator);
create index nat_gateways_by_tenant on nat_gateways (tenant);
create index nat_gateways_by_label on nat_gateways using gin (labels);

alter table nat_gateways
  add constraint nat_gateways_tenant_fk
  foreign key (tenant) references tenants (name);

alter table nat_gateways
  add constraint nat_gateways_project_fk
  foreign key (tenant, project) references projects (tenant, name);

create trigger check_immutable_columns
  before update on nat_gateways
  for each row
  execute function check_immutable_columns('id', 'tenant', 'project');

-- Enforce one active NATGateway per VirtualNetwork.
create unique index nat_gateways_one_per_virtual_network
  on nat_gateways ((data -> 'spec' ->> 'virtual_network'))
  where data -> 'spec' ->> 'virtual_network' is not null
    and data -> 'spec' ->> 'virtual_network' != ''
    and deletion_timestamp = 'epoch';

-- Update the existing check_virtual_network_not_in_use function to also check for active NATGateways.
create or replace function check_virtual_network_not_in_use() returns trigger as $$
declare
  subnet_count bigint;
  sg_count bigint;
  ng_count bigint;
begin
  select count(*) into subnet_count
  from subnets
  where deletion_timestamp = 'epoch'
    and data->'spec'->>'virtual_network' = old.id;

  if subnet_count > 0 then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete VirtualNetwork ''%s'': %s Subnet(s) still reference it',
        old.id, subnet_count
      );
  end if;

  select count(*) into sg_count
  from security_groups
  where deletion_timestamp = 'epoch'
    and data->'spec'->>'virtual_network' = old.id;

  if sg_count > 0 then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete VirtualNetwork ''%s'': %s SecurityGroup(s) still reference it',
        old.id, sg_count
      );
  end if;

  select count(*) into ng_count
  from nat_gateways
  where deletion_timestamp = 'epoch'
    and data->'spec'->>'virtual_network' = old.id;

  if ng_count > 0 then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete VirtualNetwork ''%s'': %s NATGateway(s) still reference it',
        old.id, ng_count
      );
  end if;

  return new;
end;
$$ language plpgsql;

-- Trigger function that validates the virtual_network reference in a NATGateway.
create function check_nat_gateway_virtual_network_ref() returns trigger as $$
declare
  vn_id text;
  found_id text;
begin
  vn_id := new.data->'spec'->>'virtual_network';
  if coalesce(vn_id, '') = '' then
    return new;
  end if;

  select id into found_id
  from virtual_networks
  where id = vn_id
    and deletion_timestamp = 'epoch'
  for share;

  if found_id is null then
    raise exception using
      errcode = 'Z0002',
      message = format(
        'VirtualNetwork ''%s'' does not exist or has been deleted',
        vn_id
      );
  end if;

  return new;
end;
$$ language plpgsql;

create trigger check_nat_gateway_virtual_network_ref
  before insert on nat_gateways
  for each row
  when (new.deletion_timestamp = 'epoch')
  execute function check_nat_gateway_virtual_network_ref();
