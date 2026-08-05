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

-- This migration updates the database for the transition from plain-string resource references to typed reference
-- messages (e.g. spec.virtual_network changed from "abc-123" to {"id": "abc-123", "name": "my-vn"}).
--
-- Four things happen, in order:
--
-- 1. BACKFILL existing rows so that every reference field stored as a bare string is wrapped in a JSON object
--    with the string value placed under the "id" key.
--
-- 2. DROP all Z0002 (forward-reference / inbound) triggers and their functions. The reference validation
--    interceptor in the gRPC layer now performs these checks before the request reaches the database.
--
-- 3. UPDATE all Z0003 (reverse-reference / "not in use") trigger functions so that their JSON path expressions
--    navigate into the new nested reference objects (->>'id' instead of the former ->>).
--
-- 4. DROP and RECREATE indexes that used the old flat JSON paths, plus update the Z0004 exclusivity trigger.

-- =============================================================================================================
-- PART 1: DATA BACKFILL
-- =============================================================================================================
-- Transform every string-valued reference field into {"id": <old_value>}. Each UPDATE is guarded by a
-- jsonb_typeof check so that rows already in the new format (objects) or with NULL values are skipped.

-- subnets: spec.virtual_network
update subnets set data = jsonb_set(
  data, '{spec,virtual_network}',
  jsonb_build_object('id', data->'spec'->>'virtual_network')
) where data->'spec'->>'virtual_network' is not null
  and jsonb_typeof(data->'spec'->'virtual_network') = 'string';

-- security_groups: spec.virtual_network
update security_groups set data = jsonb_set(
  data, '{spec,virtual_network}',
  jsonb_build_object('id', data->'spec'->>'virtual_network')
) where data->'spec'->>'virtual_network' is not null
  and jsonb_typeof(data->'spec'->'virtual_network') = 'string';

-- virtual_networks: spec.network_class
update virtual_networks set data = jsonb_set(
  data, '{spec,network_class}',
  jsonb_build_object('id', data->'spec'->>'network_class')
) where data->'spec'->>'network_class' is not null
  and jsonb_typeof(data->'spec'->'network_class') = 'string';

-- compute_instances: spec.instance_type
update compute_instances set data = jsonb_set(
  data, '{spec,instance_type}',
  jsonb_build_object('id', data->'spec'->>'instance_type')
) where data->'spec'->>'instance_type' is not null
  and jsonb_typeof(data->'spec'->'instance_type') = 'string';

-- compute_instances: spec.catalog_item
update compute_instances set data = jsonb_set(
  data, '{spec,catalog_item}',
  jsonb_build_object('id', data->'spec'->>'catalog_item')
) where data->'spec'->>'catalog_item' is not null
  and jsonb_typeof(data->'spec'->'catalog_item') = 'string';

-- compute_instances: spec.template
update compute_instances set data = jsonb_set(
  data, '{spec,template}',
  jsonb_build_object('id', data->'spec'->>'template')
) where data->'spec'->>'template' is not null
  and jsonb_typeof(data->'spec'->'template') = 'string';

-- compute_instances: spec.network_attachments — transform subnet and security_groups within each element.
-- Each network_attachment element: subnet string → {"id": old}, security_groups string[] → [{"id": old}, ...].
update compute_instances set data = jsonb_set(
  data, '{spec,network_attachments}',
  (
    select coalesce(jsonb_agg(
      -- Start with the original attachment object
      case
        -- If subnet is a string, wrap it
        when jsonb_typeof(elem->'subnet') = 'string' then
          jsonb_set(elem, '{subnet}', jsonb_build_object('id', elem->>'subnet'))
        else elem
      end
      -- Then handle security_groups array: if any element is a string, transform the whole array
      || case
        when elem->'security_groups' is not null
          and jsonb_typeof(elem->'security_groups') = 'array'
          and jsonb_array_length(elem->'security_groups') > 0
          and jsonb_typeof(elem->'security_groups'->0) = 'string'
        then jsonb_build_object('security_groups', (
          select jsonb_agg(jsonb_build_object('id', sg_elem #>> '{}'))
          from jsonb_array_elements(elem->'security_groups') as sg_elem
        ))
        else '{}'::jsonb
      end
    ), '[]'::jsonb)
    from jsonb_array_elements(data->'spec'->'network_attachments') as elem
  )
) where data->'spec'->'network_attachments' is not null
  and jsonb_typeof(data->'spec'->'network_attachments') = 'array'
  and jsonb_array_length(data->'spec'->'network_attachments') > 0
  and exists (
    select 1 from jsonb_array_elements(data->'spec'->'network_attachments') as elem
    where jsonb_typeof(elem->'subnet') = 'string'
       or (elem->'security_groups' is not null
           and jsonb_typeof(elem->'security_groups') = 'array'
           and jsonb_array_length(elem->'security_groups') > 0
           and jsonb_typeof(elem->'security_groups'->0) = 'string')
  );

-- compute_instance_templates: spec_defaults.instance_type
update compute_instance_templates set data = jsonb_set(
  data, '{spec_defaults,instance_type}',
  jsonb_build_object('id', data->'spec_defaults'->>'instance_type')
) where data->'spec_defaults'->>'instance_type' is not null
  and jsonb_typeof(data->'spec_defaults'->'instance_type') = 'string';

-- compute_instance_catalog_items: spec.template
update compute_instance_catalog_items set data = jsonb_set(
  data, '{spec,template}',
  jsonb_build_object('id', data->'spec'->>'template')
) where data->'spec'->>'template' is not null
  and jsonb_typeof(data->'spec'->'template') = 'string';

-- clusters: spec.template
update clusters set data = jsonb_set(
  data, '{spec,template}',
  jsonb_build_object('id', data->'spec'->>'template')
) where data->'spec'->>'template' is not null
  and jsonb_typeof(data->'spec'->'template') = 'string';

-- clusters: spec.catalog_item
update clusters set data = jsonb_set(
  data, '{spec,catalog_item}',
  jsonb_build_object('id', data->'spec'->>'catalog_item')
) where data->'spec'->>'catalog_item' is not null
  and jsonb_typeof(data->'spec'->'catalog_item') = 'string';

-- clusters: spec.node_sets — a JSON object (map) where each value has a host_type field.
-- Transform host_type string → {"id": old} inside every map value.
update clusters set data = jsonb_set(
  data, '{spec,node_sets}',
  (
    select coalesce(jsonb_object_agg(
      kv.key,
      case
        when jsonb_typeof(kv.value->'host_type') = 'string' then
          jsonb_set(kv.value, '{host_type}', jsonb_build_object('id', kv.value->>'host_type'))
        else kv.value
      end
    ), '{}'::jsonb)
    from jsonb_each(data->'spec'->'node_sets') as kv
  )
) where data->'spec'->'node_sets' is not null
  and jsonb_typeof(data->'spec'->'node_sets') = 'object'
  and exists (
    select 1 from jsonb_each(data->'spec'->'node_sets') as kv
    where jsonb_typeof(kv.value->'host_type') = 'string'
  );

-- cluster_templates: node_sets — same map structure as clusters.spec.node_sets.
-- Transform host_type string → {"id": old} inside every map value.
update cluster_templates set data = jsonb_set(
  data, '{node_sets}',
  (
    select coalesce(jsonb_object_agg(
      kv.key,
      case
        when jsonb_typeof(kv.value->'host_type') = 'string' then
          jsonb_set(kv.value, '{host_type}', jsonb_build_object('id', kv.value->>'host_type'))
        else kv.value
      end
    ), '{}'::jsonb)
    from jsonb_each(data->'node_sets') as kv
  )
) where data->'node_sets' is not null
  and jsonb_typeof(data->'node_sets') = 'object'
  and exists (
    select 1 from jsonb_each(data->'node_sets') as kv
    where jsonb_typeof(kv.value->'host_type') = 'string'
  );

-- cluster_catalog_items: spec.template
update cluster_catalog_items set data = jsonb_set(
  data, '{spec,template}',
  jsonb_build_object('id', data->'spec'->>'template')
) where data->'spec'->>'template' is not null
  and jsonb_typeof(data->'spec'->'template') = 'string';

-- nat_gateways: spec.virtual_network
update nat_gateways set data = jsonb_set(
  data, '{spec,virtual_network}',
  jsonb_build_object('id', data->'spec'->>'virtual_network')
) where data->'spec'->>'virtual_network' is not null
  and jsonb_typeof(data->'spec'->'virtual_network') = 'string';

-- nat_gateways: spec.external_ip
update nat_gateways set data = jsonb_set(
  data, '{spec,external_ip}',
  jsonb_build_object('id', data->'spec'->>'external_ip')
) where data->'spec'->>'external_ip' is not null
  and jsonb_typeof(data->'spec'->'external_ip') = 'string';

-- external_ips: spec.pool
update external_ips set data = jsonb_set(
  data, '{spec,pool}',
  jsonb_build_object('id', data->'spec'->>'pool')
) where data->'spec'->>'pool' is not null
  and jsonb_typeof(data->'spec'->'pool') = 'string';

-- external_ip_attachments: spec.external_ip
update external_ip_attachments set data = jsonb_set(
  data, '{spec,external_ip}',
  jsonb_build_object('id', data->'spec'->>'external_ip')
) where data->'spec'->>'external_ip' is not null
  and jsonb_typeof(data->'spec'->'external_ip') = 'string';

-- external_ip_attachments: spec.compute_instance
update external_ip_attachments set data = jsonb_set(
  data, '{spec,compute_instance}',
  jsonb_build_object('id', data->'spec'->>'compute_instance')
) where data->'spec'->>'compute_instance' is not null
  and jsonb_typeof(data->'spec'->'compute_instance') = 'string';

-- external_ip_attachments: spec.cluster
update external_ip_attachments set data = jsonb_set(
  data, '{spec,cluster}',
  jsonb_build_object('id', data->'spec'->>'cluster')
) where data->'spec'->>'cluster' is not null
  and jsonb_typeof(data->'spec'->'cluster') = 'string';

-- external_ip_attachments: spec.baremetal_instance
update external_ip_attachments set data = jsonb_set(
  data, '{spec,baremetal_instance}',
  jsonb_build_object('id', data->'spec'->>'baremetal_instance')
) where data->'spec'->>'baremetal_instance' is not null
  and jsonb_typeof(data->'spec'->'baremetal_instance') = 'string';

-- bare_metal_instances: spec.catalog_item
update bare_metal_instances set data = jsonb_set(
  data, '{spec,catalog_item}',
  jsonb_build_object('id', data->'spec'->>'catalog_item')
) where data->'spec'->>'catalog_item' is not null
  and jsonb_typeof(data->'spec'->'catalog_item') = 'string';

-- bare_metal_instances: spec.template (if present in legacy data)
update bare_metal_instances set data = jsonb_set(
  data, '{spec,template}',
  jsonb_build_object('id', data->'spec'->>'template')
) where data->'spec'->>'template' is not null
  and jsonb_typeof(data->'spec'->'template') = 'string';

-- bare_metal_instances: spec.network_attachments — same pattern as compute_instances
update bare_metal_instances set data = jsonb_set(
  data, '{spec,network_attachments}',
  (
    select coalesce(jsonb_agg(
      case
        when jsonb_typeof(elem->'subnet') = 'string' then
          jsonb_set(elem, '{subnet}', jsonb_build_object('id', elem->>'subnet'))
        else elem
      end
      || case
        when elem->'security_groups' is not null
          and jsonb_typeof(elem->'security_groups') = 'array'
          and jsonb_array_length(elem->'security_groups') > 0
          and jsonb_typeof(elem->'security_groups'->0) = 'string'
        then jsonb_build_object('security_groups', (
          select jsonb_agg(jsonb_build_object('id', sg_elem #>> '{}'))
          from jsonb_array_elements(elem->'security_groups') as sg_elem
        ))
        else '{}'::jsonb
      end
    ), '[]'::jsonb)
    from jsonb_array_elements(data->'spec'->'network_attachments') as elem
  )
) where data->'spec'->'network_attachments' is not null
  and jsonb_typeof(data->'spec'->'network_attachments') = 'array'
  and jsonb_array_length(data->'spec'->'network_attachments') > 0
  and exists (
    select 1 from jsonb_array_elements(data->'spec'->'network_attachments') as elem
    where jsonb_typeof(elem->'subnet') = 'string'
       or (elem->'security_groups' is not null
           and jsonb_typeof(elem->'security_groups') = 'array'
           and jsonb_array_length(elem->'security_groups') > 0
           and jsonb_typeof(elem->'security_groups'->0) = 'string')
  );

-- clusters: spec.network_attachment.subnet
update clusters set data = jsonb_set(
  data, '{spec,network_attachment,subnet}',
  jsonb_build_object('id', data->'spec'->'network_attachment'->>'subnet')
) where data->'spec'->'network_attachment'->>'subnet' is not null
  and jsonb_typeof(data->'spec'->'network_attachment'->'subnet') = 'string';

-- clusters: spec.network_attachment.security_groups — array of strings → array of {"id": old}
update clusters set data = jsonb_set(
  data, '{spec,network_attachment,security_groups}',
  (
    select coalesce(jsonb_agg(jsonb_build_object('id', elem #>> '{}')), '[]'::jsonb)
    from jsonb_array_elements(data->'spec'->'network_attachment'->'security_groups') as elem
  )
) where data->'spec'->'network_attachment'->'security_groups' is not null
  and jsonb_typeof(data->'spec'->'network_attachment'->'security_groups') = 'array'
  and jsonb_array_length(data->'spec'->'network_attachment'->'security_groups') > 0
  and jsonb_typeof(data->'spec'->'network_attachment'->'security_groups'->0) = 'string';

-- bare_metal_instance_catalog_items: spec.template
update bare_metal_instance_catalog_items set data = jsonb_set(
  data, '{spec,template}',
  jsonb_build_object('id', data->'spec'->>'template')
) where data->'spec'->>'template' is not null
  and jsonb_typeof(data->'spec'->'template') = 'string';

-- role_bindings: spec.role
update role_bindings set data = jsonb_set(
  data, '{spec,role}',
  jsonb_build_object('id', data->'spec'->>'role')
) where data->'spec'->>'role' is not null
  and jsonb_typeof(data->'spec'->'role') = 'string';

-- role_bindings: spec.users — array of strings → array of {"id": old}
update role_bindings set data = jsonb_set(
  data, '{spec,users}',
  (
    select coalesce(jsonb_agg(jsonb_build_object('id', elem #>> '{}')), '[]'::jsonb)
    from jsonb_array_elements(data->'spec'->'users') as elem
  )
) where data->'spec'->'users' is not null
  and jsonb_typeof(data->'spec'->'users') = 'array'
  and jsonb_array_length(data->'spec'->'users') > 0
  and jsonb_typeof(data->'spec'->'users'->0) = 'string';

-- project_memberships: spec.users — array of strings → array of {"id": old}
update project_memberships set data = jsonb_set(
  data, '{spec,users}',
  (
    select coalesce(jsonb_agg(jsonb_build_object('id', elem #>> '{}')), '[]'::jsonb)
    from jsonb_array_elements(data->'spec'->'users') as elem
  )
) where data->'spec'->'users' is not null
  and jsonb_typeof(data->'spec'->'users') = 'array'
  and jsonb_array_length(data->'spec'->'users') > 0
  and jsonb_typeof(data->'spec'->'users'->0) = 'string';

-- instance_types: spec.deprecation.replacement (nested inside deprecation object)
update instance_types set data = jsonb_set(
  data, '{spec,deprecation,replacement}',
  jsonb_build_object('id', data->'spec'->'deprecation'->>'replacement')
) where data->'spec'->'deprecation'->>'replacement' is not null
  and jsonb_typeof(data->'spec'->'deprecation'->'replacement') = 'string';

-- =============================================================================================================
-- PART 2: DROP Z0002 (FORWARD REFERENCE) TRIGGERS AND FUNCTIONS
-- =============================================================================================================
-- The reference validation interceptor now handles all inbound reference checks at the gRPC layer.
-- These triggers are no longer needed and would conflict with the new JSON structure.

-- From migration 55: subnet → virtual_network
drop trigger if exists check_subnet_virtual_network_ref on subnets;
drop function if exists check_subnet_virtual_network_ref();

-- From migration 55: security_group → virtual_network
drop trigger if exists check_security_group_virtual_network_ref on security_groups;
drop function if exists check_security_group_virtual_network_ref();

-- From migration 52: compute_instance → subnet (via network_attachments)
drop trigger if exists check_compute_instance_subnet_refs on compute_instances;
drop function if exists check_compute_instance_subnet_refs();

-- From migration 56: compute_instance → instance_type
drop trigger if exists check_compute_instance_instance_type_ref on compute_instances;
drop function if exists check_compute_instance_instance_type_ref();

-- From migration 59: cluster → cluster_catalog_item (insert and update triggers share one function)
drop trigger if exists check_cluster_catalog_item_ref_on_insert on clusters;
drop trigger if exists check_cluster_catalog_item_ref_on_update on clusters;
drop function if exists check_cluster_catalog_item_ref();

-- From migration 59: compute_instance → compute_instance_catalog_item (insert and update triggers share one function)
drop trigger if exists check_ci_catalog_item_ref_on_insert on compute_instances;
drop trigger if exists check_ci_catalog_item_ref_on_update on compute_instances;
drop function if exists check_ci_catalog_item_ref();

-- From migration 73: nat_gateway → virtual_network
drop trigger if exists check_nat_gateway_virtual_network_ref on nat_gateways;
drop function if exists check_nat_gateway_virtual_network_ref();

-- From migration 76: storage_tier → storage_backend
drop trigger if exists check_storage_tier_backend_refs on storage_tiers;
drop function if exists check_storage_tier_backend_refs();

-- From migration 81: cluster_version → allowed_upgrades version_names
drop trigger if exists check_cluster_version_allowed_upgrade_refs_on_insert on cluster_versions;
drop trigger if exists check_cluster_version_allowed_upgrade_refs_on_update on cluster_versions;
drop function if exists check_cluster_version_allowed_upgrade_refs();

-- From migration 87: cluster → subnet (via network_attachment)
drop trigger if exists check_cluster_subnet_refs on clusters;
drop function if exists check_cluster_subnet_refs();

-- =============================================================================================================
-- PART 3: UPDATE Z0003 (REVERSE REFERENCE / "NOT IN USE") TRIGGER FUNCTIONS
-- =============================================================================================================
-- These triggers prevent deleting a parent when children still reference it. The JSON path expressions
-- must navigate into the nested reference object to extract the id field.

-- (a) check_virtual_network_not_in_use — checks subnets, security_groups, and nat_gateways.
-- Updated: data->'spec'->>'virtual_network' → data->'spec'->'virtual_network'->>'id'
create or replace function check_virtual_network_not_in_use() returns trigger as $$
declare
  subnet_count bigint;
  sg_count bigint;
  ng_count bigint;
begin
  select count(*) into subnet_count
  from subnets
  where deletion_timestamp = 'epoch'
    and data->'spec'->'virtual_network'->>'id' = old.id;

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
    and data->'spec'->'virtual_network'->>'id' = old.id;

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
    and data->'spec'->'virtual_network'->>'id' = old.id;

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

-- (b) check_subnet_not_in_use — checks compute_instances and clusters.
-- Updated: containment target from {"subnet": old.id} to {"subnet": {"id": old.id}}
-- Also includes cluster check from migration 87, updated to nested reference path.
create or replace function check_subnet_not_in_use() returns trigger as $$
begin
  if exists (
    select 1
    from compute_instances
    where deletion_timestamp = 'epoch'
      and data->'spec'->'network_attachments' @>
          jsonb_build_array(jsonb_build_object('subnet', jsonb_build_object('id', old.id)))
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete subnet ''%s'': it is in use by at least one compute instance',
        old.id
      );
  end if;

  if exists (
    select 1
    from clusters
    where deletion_timestamp = 'epoch'
      and data->'spec'->'network_attachment'->'subnet'->>'id' = old.id
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete subnet ''%s'': it is in use by at least one cluster',
        old.id
      );
  end if;

  return new;
end;
$$ language plpgsql;

-- (c) check_instance_type_not_in_use — checks compute_instances and compute_instance_templates.
-- Updated: data->'spec'->>'instance_type' → data->'spec'->'instance_type'->>'id'
-- Also adds the compute_instance_templates check that was previously missing.
create or replace function check_instance_type_not_in_use() returns trigger as $$
begin
  if exists (
    select 1
    from compute_instances
    where deletion_timestamp = 'epoch'
      and data->'spec'->'instance_type'->>'id' = old.id
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete instance type ''%s'': it is in use by at least one compute instance',
        old.id
      );
  end if;

  if exists (
    select 1
    from compute_instance_templates
    where deletion_timestamp = 'epoch'
      and data->'spec_defaults'->'instance_type'->>'id' = old.id
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete instance type ''%s'': it is in use by at least one compute instance template',
        old.id
      );
  end if;

  return new;
end;
$$ language plpgsql;

-- (d) check_cluster_catalog_item_not_in_use — checks clusters.
-- Updated: data->'spec'->>'catalog_item' → data->'spec'->'catalog_item'->>'id' (and ->>'name' for name match)
create or replace function check_cluster_catalog_item_not_in_use() returns trigger as $$
begin
  if exists (
    select 1
    from clusters
    where deletion_timestamp = 'epoch'
      and (data->'spec'->'catalog_item'->>'id' = old.id or data->'spec'->'catalog_item'->>'name' = old.name)
      and (old.tenant = 'shared' or clusters.tenant = old.tenant)
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete cluster catalog item ''%s'': it is in use by at least one cluster',
        old.id
      );
  end if;

  return new;
end;
$$ language plpgsql;

-- (d) check_ci_catalog_item_not_in_use — checks compute_instances.
-- Updated: data->'spec'->>'catalog_item' → data->'spec'->'catalog_item'->>'id' (and ->>'name' for name match)
create or replace function check_ci_catalog_item_not_in_use() returns trigger as $$
begin
  if exists (
    select 1
    from compute_instances
    where deletion_timestamp = 'epoch'
      and (data->'spec'->'catalog_item'->>'id' = old.id or data->'spec'->'catalog_item'->>'name' = old.name)
      and (old.tenant = 'shared' or compute_instances.tenant = old.tenant)
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete compute instance catalog item ''%s'': it is in use by at least one compute instance',
        old.id
      );
  end if;

  return new;
end;
$$ language plpgsql;

-- (e) check_external_ip_available (Z0004 exclusivity trigger from migration 86).
-- Updated: data->'spec'->>'external_ip' → data->'spec'->'external_ip'->>'id'
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

  select id into locked_id
  from external_ips
  where id = eip_id
  for update;

  if locked_id is null then
    return new;
  end if;

  select id, type into consumer_id, consumer_type
  from (
    select id, 'ExternalIPAttachment' as type
    from external_ip_attachments
    where deletion_timestamp = 'epoch'
      and data->'spec'->'external_ip'->>'id' = eip_id
    union all
    select id, 'NATGateway' as type
    from nat_gateways
    where deletion_timestamp = 'epoch'
      and data->'spec'->'external_ip'->>'id' = eip_id
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

-- =============================================================================================================
-- PART 4: DROP AND RECREATE INDEXES
-- =============================================================================================================

-- From migration 55: subnets_by_virtual_network
drop index if exists subnets_by_virtual_network;
create index subnets_by_virtual_network on subnets ((data->'spec'->'virtual_network'->>'id'))
  where deletion_timestamp = 'epoch';

-- From migration 55: security_groups_by_virtual_network
drop index if exists security_groups_by_virtual_network;
create index security_groups_by_virtual_network on security_groups ((data->'spec'->'virtual_network'->>'id'))
  where deletion_timestamp = 'epoch';

-- From migration 56: compute_instances_instance_type
drop index if exists compute_instances_instance_type;
create index compute_instances_instance_type on compute_instances ((data->'spec'->'instance_type'->>'id'))
  where data->'spec'->'instance_type'->>'id' is not null;

-- From migration 59: clusters_catalog_item
drop index if exists clusters_catalog_item;
create index clusters_catalog_item on clusters
  using btree ((data->'spec'->'catalog_item'->>'id'))
  where data->'spec'->'catalog_item'->>'id' is not null;

-- From migration 59: compute_instances_catalog_item
drop index if exists compute_instances_catalog_item;
create index compute_instances_catalog_item on compute_instances
  using btree ((data->'spec'->'catalog_item'->>'id'))
  where data->'spec'->'catalog_item'->>'id' is not null;

-- From migration 73: nat_gateways_one_per_virtual_network (unique constraint)
drop index if exists nat_gateways_one_per_virtual_network;
create unique index nat_gateways_one_per_virtual_network
  on nat_gateways ((data->'spec'->'virtual_network'->>'id'))
  where data->'spec'->'virtual_network'->>'id' is not null
    and data->'spec'->'virtual_network'->>'id' != ''
    and deletion_timestamp = 'epoch';

-- From migration 61: external_ip_attachments_one_per_external_ip (unique constraint)
drop index if exists external_ip_attachments_one_per_external_ip;
create unique index external_ip_attachments_one_per_external_ip
  on external_ip_attachments ((data->'spec'->'external_ip'->>'id'))
  where data->'spec'->'external_ip'->>'id' is not null
    and data->'spec'->'external_ip'->>'id' != ''
    and deletion_timestamp = 'epoch';

-- From migration 61: external_ip_attachments_one_per_compute_instance (unique constraint)
drop index if exists external_ip_attachments_one_per_compute_instance;
create unique index external_ip_attachments_one_per_compute_instance
  on external_ip_attachments ((data->'spec'->'compute_instance'->>'id'))
  where data->'spec'->'compute_instance'->>'id' is not null
    and data->'spec'->'compute_instance'->>'id' != ''
    and deletion_timestamp = 'epoch';

-- From migration 85: external_ip_attachments_one_per_cluster_endpoint (unique constraint)
drop index if exists external_ip_attachments_one_per_cluster_endpoint;
create unique index external_ip_attachments_one_per_cluster_endpoint
  on external_ip_attachments (
    (data->'spec'->'cluster'->>'id'),
    (data->'spec'->>'target_endpoint')
  )
  where data->'spec'->'cluster'->>'id' is not null
    and data->'spec'->'cluster'->>'id' != ''
    and deletion_timestamp = 'epoch';

-- From migration 85: external_ip_attachments_one_per_baremetal_instance (unique constraint)
drop index if exists external_ip_attachments_one_per_baremetal_instance;
create unique index external_ip_attachments_one_per_baremetal_instance
  on external_ip_attachments ((data->'spec'->'baremetal_instance'->>'id'))
  where data->'spec'->'baremetal_instance'->>'id' is not null
    and data->'spec'->'baremetal_instance'->>'id' != ''
    and deletion_timestamp = 'epoch';

-- From migration 87: clusters_network_attachment_subnet
drop index if exists clusters_network_attachment_subnet;
create index clusters_network_attachment_subnet on clusters
  ((data->'spec'->'network_attachment'->'subnet'->>'id'))
  where data->'spec'->'network_attachment'->'subnet'->>'id' is not null;
