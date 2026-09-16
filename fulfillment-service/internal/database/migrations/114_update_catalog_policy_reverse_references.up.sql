--
-- Copyright (c) 2026 Red Hat, Inc.
--
-- Licensed under the Apache License, Version 2.0 (the "License");
--

-- Catalog items retain only weak provenance after a resource is created.  Remove the
-- old consumer-to-catalog deletion guards before adding guards for the resources
-- referenced by typed catalog policies.
drop trigger if exists check_cluster_catalog_item_not_in_use on cluster_catalog_items;
drop function if exists check_cluster_catalog_item_not_in_use();
drop trigger if exists check_ci_catalog_item_not_in_use on compute_instance_catalog_items;
drop function if exists check_ci_catalog_item_not_in_use();

-- ClusterVersion is referenced by clusters, templates, and the typed version
-- policy on cluster catalog items. Prefer canonical IDs, while retaining the
-- existing protection for legacy name-only references.
create or replace function check_cluster_version_not_in_use() returns trigger as $$
begin
  if exists (
    select 1 from clusters
    where deletion_timestamp = 'epoch'
      and (data->'spec'->'version'->>'id' = old.id
        or (coalesce(data->'spec'->'version'->>'id', '') = ''
          and data->'spec'->'version'->>'name' = old.name))
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format('cannot delete cluster version ''%s'': it is in use by at least one cluster', old.name);
  end if;

  if exists (
    select 1 from cluster_templates
    where deletion_timestamp = 'epoch'
      and (data->'spec_defaults'->'version'->>'id' = old.id
        or (coalesce(data->'spec_defaults'->'version'->>'id', '') = ''
          and data->'spec_defaults'->'version'->>'name' = old.name))
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format('cannot delete cluster version ''%s'': it is in use by at least one cluster template', old.name);
  end if;

  if exists (
    select 1 from cluster_catalog_items c
    where c.deletion_timestamp = 'epoch'
      and (
        (c.data->'fields'->'version'->'locked'->>'id' = old.id
        or (coalesce(c.data->'fields'->'version'->'locked'->>'id', '') = ''
          and c.data->'fields'->'version'->'locked'->>'name' = old.name))
        or (c.data->'fields'->'version'->'editable'->'default_value'->>'id' = old.id
        or (coalesce(c.data->'fields'->'version'->'editable'->'default_value'->>'id', '') = ''
          and c.data->'fields'->'version'->'editable'->'default_value'->>'name' = old.name))
      )
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format('cannot delete cluster version ''%s'': it is in use by at least one cluster catalog item', old.name);
  end if;

  return new;
end;
$$ language plpgsql;

-- InstanceType references are id-based after reference canonicalization.
create or replace function check_instance_type_not_in_use() returns trigger as $$
begin
  if exists (
    select 1 from compute_instances
    where deletion_timestamp = 'epoch'
      and data->'spec'->'instance_type'->>'id' = old.id
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format('cannot delete instance type ''%s'': it is in use by at least one compute instance', old.id);
  end if;

  if exists (
    select 1 from compute_instance_templates
    where deletion_timestamp = 'epoch'
      and data->'spec_defaults'->'instance_type'->>'id' = old.id
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format('cannot delete instance type ''%s'': it is in use by at least one compute instance template', old.id);
  end if;

  if exists (
    select 1 from compute_instance_catalog_items c
    where c.deletion_timestamp = 'epoch'
      and (
        c.data->'fields'->'instance_type'->'locked'->>'id' = old.id
        or c.data->'fields'->'instance_type'->'editable'->'default_value'->>'id' = old.id
      )
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format('cannot delete instance type ''%s'': it is in use by an active resource or catalog policy', old.id);
  end if;

  return new;
end;
$$ language plpgsql;

-- DiskImage references are id-based.  Catalog policies have both locked and
-- editable default branches, and bare-metal resources use the same reference.
create or replace function check_disk_image_not_in_use() returns trigger as $$
begin
  if exists (
    select 1 from compute_instances
    where deletion_timestamp = 'epoch'
      and data->'spec'->'disk_image'->>'id' = old.id
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format('cannot delete disk image ''%s'': it is in use by at least one compute instance', old.id);
  end if;

  if exists (
    select 1 from compute_instance_templates
    where deletion_timestamp = 'epoch'
      and data->'spec_defaults'->'disk_image'->>'id' = old.id
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format('cannot delete disk image ''%s'': it is in use by at least one compute instance template', old.id);
  end if;

  if exists (
    select 1 from bare_metal_instances
    where deletion_timestamp = 'epoch'
      and data->'spec'->'disk_image'->>'id' = old.id
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format('cannot delete disk image ''%s'': it is in use by at least one bare metal instance', old.id);
  end if;

  if exists (
    select 1 from compute_instance_catalog_items c
    where c.deletion_timestamp = 'epoch'
      and (
        c.data->'fields'->'disk_image'->'locked'->>'id' = old.id
        or c.data->'fields'->'disk_image'->'editable'->'default_value'->>'id' = old.id
      )
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format('cannot delete disk image ''%s'': it is in use by at least one compute instance catalog item', old.id);
  end if;

  if exists (
    select 1 from bare_metal_instance_catalog_items c
    where c.deletion_timestamp = 'epoch'
      and (
        c.data->'fields'->'disk_image'->'locked'->>'id' = old.id
        or c.data->'fields'->'disk_image'->'editable'->'default_value'->>'id' = old.id
      )
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format('cannot delete disk image ''%s'': it is in use by at least one bare metal instance catalog item', old.id);
  end if;

  return new;
end;
$$ language plpgsql;

-- Secret policies are only defined for clusters.  Preserve the existing HUB
-- exemption and add the cluster catalog policy branches.
create or replace function check_secret_not_in_use() returns trigger as $$
begin
  -- Catalog dependencies are strong for every backend; retain the legacy HUB exemption below.
  if exists (
    select 1 from cluster_catalog_items c
    where c.deletion_timestamp = 'epoch'
      and (
        c.data->'fields'->'pull_secret_secret'->'locked'->>'id' = old.id
        or c.data->'fields'->'pull_secret_secret'->'editable'->'default_value'->>'id' = old.id
      )
  ) then
    raise exception using errcode = 'Z0003',
      message = format('cannot delete Secret ''%s'': it is in use by a catalog policy', old.id);
  end if;

  if old.data->>'backend' = 'SECRET_BACKEND_HUB' then
    return new;
  end if;

  if exists (
    select 1 from active_clusters a
    join clusters c on c.id = a.id
    where c.data->'spec'->'pull_secret_secret'->>'id' = old.id
  ) or exists (
    select 1 from active_cluster_templates a
    join cluster_templates c on c.id = a.id
    where c.data->'spec_defaults'->'pull_secret_secret'->>'id' = old.id
  ) or exists (
    select 1 from active_hubs a
    join hubs h on h.id = a.id
    where h.data->'spec'->'kubeconfig_secret'->>'id' = old.id
  ) or exists (
    select 1 from active_identity_providers a
    join identity_providers i on i.id = a.id
    where i.data->'spec'->'open_id_connect'->'client_secret_secret'->>'id' = old.id
  ) or exists (
    select 1 from active_storage_backends a
    join storage_backends s on s.id = a.id
    where s.data->'spec'->'credentials'->'password_secret'->>'id' = old.id

  ) then
    raise exception using
      errcode = 'Z0003',
      message = format('cannot delete Secret ''%s'': it is in use by at least one active resource or catalog policy', old.id);
  end if;

  return new;
end;
$$ language plpgsql;

-- Subnet references in catalog policies are nested in list wrappers.  Keep the
-- existing resource and same-VirtualNetwork security-group protections intact.
create or replace function check_subnet_not_in_use() returns trigger as $$
declare
  vn_id text;
begin
  -- Existing check: compute instance references (from migration 90)
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

  -- Existing check: cluster references (from migration 90)
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

  if exists (
    select 1 from bare_metal_instances
    where deletion_timestamp = 'epoch'
      and data->'spec'->'network_attachments' @>
          jsonb_build_array(jsonb_build_object('subnet', jsonb_build_object('id', old.id)))
  ) then
    raise exception using errcode = 'Z0003',
      message = format('cannot delete subnet ''%s'': it is in use by at least one bare metal instance', old.id);
  end if;

  if exists (
    select 1
    from compute_instance_catalog_items c
    cross join lateral jsonb_array_elements(
      coalesce(c.data->'fields'->'network_attachments'->'locked'->'items', '[]'::jsonb)
      || coalesce(c.data->'fields'->'network_attachments'->'editable'->'default_value'->'items', '[]'::jsonb)
    ) attachment
    where c.deletion_timestamp = 'epoch'
      and attachment->'subnet'->>'id' = old.id
  ) or exists (
    select 1 from cluster_catalog_items c
    where c.deletion_timestamp = 'epoch'
      and (
        c.data->'fields'->'network_attachment'->'locked'->'subnet'->>'id' = old.id
        or c.data->'fields'->'network_attachment'->'editable'->'default_value'->'subnet'->>'id' = old.id
      )
  ) or exists (
    select 1
    from bare_metal_instance_catalog_items c
    cross join lateral jsonb_array_elements(
      coalesce(c.data->'fields'->'network_attachments'->'locked'->'items', '[]'::jsonb)
      || coalesce(c.data->'fields'->'network_attachments'->'editable'->'default_value'->'items', '[]'::jsonb)
    ) attachment
    where c.deletion_timestamp = 'epoch'
      and attachment->'subnet'->>'id' = old.id
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format('cannot delete subnet ''%s'': it is in use by a catalog policy', old.id);
  end if;

  vn_id := old.data->'spec'->'virtual_network'->>'id';
  if vn_id is not null then
    if exists (
      select 1
      from active_security_groups a
      join security_groups s on s.id = a.id
      where s.data->'spec'->'virtual_network'->>'id' = vn_id
    ) then
      raise exception using
        errcode = 'Z0003',
        message = format('cannot delete Subnet ''%s'': SecurityGroups still reference its VirtualNetwork', old.id);
    end if;
  end if;

  return new;
end;
$$ language plpgsql;

-- Security groups did not previously have a reverse guard.  Typed catalog
-- network policies make the dependency explicit, so protect all active
-- consumers and both policy branches.
create or replace function check_security_group_not_in_use() returns trigger as $$
begin
  if exists (
    select 1 from compute_instances
    where deletion_timestamp = 'epoch'
      and data->'spec'->'network_attachments' @>
          jsonb_build_array(jsonb_build_object('security_groups', jsonb_build_array(jsonb_build_object('id', old.id))))
  ) or exists (
    select 1 from clusters
    where deletion_timestamp = 'epoch'
      and data->'spec'->'network_attachment'->'security_groups' @>
          jsonb_build_array(jsonb_build_object('id', old.id))
  ) or exists (
    select 1 from bare_metal_instances
    where deletion_timestamp = 'epoch'
      and data->'spec'->'network_attachments' @>
          jsonb_build_array(jsonb_build_object('security_groups', jsonb_build_array(jsonb_build_object('id', old.id))))
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format('cannot delete security group ''%s'': it is in use by an active resource', old.id);
  end if;

  if exists (
    select 1
    from compute_instance_catalog_items c
    cross join lateral jsonb_array_elements(
      coalesce(c.data->'fields'->'network_attachments'->'locked'->'items', '[]'::jsonb)
      || coalesce(c.data->'fields'->'network_attachments'->'editable'->'default_value'->'items', '[]'::jsonb)
    ) attachment
    where c.deletion_timestamp = 'epoch'
      and attachment->'security_groups' @> jsonb_build_array(jsonb_build_object('id', old.id))
  ) or exists (
    select 1 from cluster_catalog_items c
    where c.deletion_timestamp = 'epoch'
      and (
        c.data->'fields'->'network_attachment'->'locked'->'security_groups' @> jsonb_build_array(jsonb_build_object('id', old.id))
        or c.data->'fields'->'network_attachment'->'editable'->'default_value'->'security_groups' @> jsonb_build_array(jsonb_build_object('id', old.id))
      )
  ) or exists (
    select 1
    from bare_metal_instance_catalog_items c
    cross join lateral jsonb_array_elements(
      coalesce(c.data->'fields'->'network_attachments'->'locked'->'items', '[]'::jsonb)
      || coalesce(c.data->'fields'->'network_attachments'->'editable'->'default_value'->'items', '[]'::jsonb)
    ) attachment
    where c.deletion_timestamp = 'epoch'
      and attachment->'security_groups' @> jsonb_build_array(jsonb_build_object('id', old.id))
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format('cannot delete security group ''%s'': it is in use by a catalog policy', old.id);
  end if;

  return new;
end;
$$ language plpgsql;

drop trigger if exists check_security_group_not_in_use on security_groups;
create trigger check_security_group_not_in_use
  before update on security_groups
  for each row
  when (old.deletion_timestamp = 'epoch' and new.deletion_timestamp != 'epoch')
  execute function check_security_group_not_in_use();

-- StorageTier is referenced by compute boot disks and additional disks.  The
-- catalog policy paths include both locked and editable default branches.
create or replace function check_storage_tier_not_in_use() returns trigger as $$
begin
  if exists (
    select 1 from compute_instances
    where deletion_timestamp = 'epoch'
      and data->'spec'->'boot_disk'->'storage_tier'->>'id' = old.id
  ) or exists (
    select 1
    from compute_instances c
    cross join lateral jsonb_array_elements(c.data->'spec'->'additional_disks') disk
    where c.deletion_timestamp = 'epoch'
      and disk->'storage_tier'->>'id' = old.id
  ) or exists (
    select 1 from compute_instance_templates
    where deletion_timestamp = 'epoch'
      and data->'spec_defaults'->'boot_disk'->'storage_tier'->>'id' = old.id
  ) or exists (
    select 1 from compute_instance_templates t
    cross join lateral jsonb_array_elements(t.data->'spec_defaults'->'additional_disks') disk
    where t.deletion_timestamp = 'epoch' and disk->'storage_tier'->>'id' = old.id
  ) or exists (
    select 1 from compute_instance_catalog_items c
    where c.deletion_timestamp = 'epoch' and (
      c.data->'fields'->'boot_disk'->'storage_tier'->'locked'->>'id' = old.id
      or c.data->'fields'->'boot_disk'->'storage_tier'->'editable'->'default_value'->>'id' = old.id
    )
  ) or exists (
    select 1 from compute_instance_catalog_items c
    cross join lateral jsonb_array_elements(
      coalesce(c.data->'fields'->'additional_disks'->'locked'->'items', '[]'::jsonb)
      || coalesce(c.data->'fields'->'additional_disks'->'editable'->'default_value'->'items', '[]'::jsonb)
    ) disk
    where c.deletion_timestamp = 'epoch' and disk->'storage_tier'->>'id' = old.id
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format('cannot delete storage tier ''%s'': it is in use by an active resource or catalog policy', old.id);
  end if;

  return new;
end;
$$ language plpgsql;

drop trigger if exists check_storage_tier_not_in_use on storage_tiers;
create trigger check_storage_tier_not_in_use
  before update on storage_tiers
  for each row
  when (old.deletion_timestamp = 'epoch' and new.deletion_timestamp != 'epoch')
  execute function check_storage_tier_not_in_use();

-- Materialized resources and catalogs both retain strong Template references.
create or replace function check_compute_instance_template_not_in_use() returns trigger as $$
begin
  if exists (select 1 from compute_instances where deletion_timestamp = 'epoch' and data->'spec'->'template'->>'id' = old.id)
    or exists (select 1 from compute_instance_catalog_items where deletion_timestamp = 'epoch' and data->'template'->>'id' = old.id) then
    raise exception using errcode = 'Z0003', message = format('cannot delete template ''%s'': it is in use', old.id);
  end if;
  return new;
end;
$$ language plpgsql;
drop trigger if exists check_compute_instance_template_not_in_use on compute_instance_templates;
create trigger check_compute_instance_template_not_in_use before update on compute_instance_templates
  for each row when (old.deletion_timestamp = 'epoch' and new.deletion_timestamp != 'epoch')
  execute function check_compute_instance_template_not_in_use();

-- Materialized resources and catalogs both retain strong Template references.
create or replace function check_cluster_template_not_in_use() returns trigger as $$
begin
  if exists (select 1 from clusters where deletion_timestamp = 'epoch' and data->'spec'->'template'->>'id' = old.id)
    or exists (select 1 from cluster_catalog_items where deletion_timestamp = 'epoch' and data->'template'->>'id' = old.id) then
    raise exception using errcode = 'Z0003', message = format('cannot delete template ''%s'': it is in use', old.id);
  end if;
  return new;
end;
$$ language plpgsql;
drop trigger if exists check_cluster_template_not_in_use on cluster_templates;
create trigger check_cluster_template_not_in_use before update on cluster_templates
  for each row when (old.deletion_timestamp = 'epoch' and new.deletion_timestamp != 'epoch')
  execute function check_cluster_template_not_in_use();

-- Materialized resources and catalogs both retain strong Template references.
create or replace function check_bare_metal_instance_template_not_in_use() returns trigger as $$
begin
  if exists (select 1 from bare_metal_instances where deletion_timestamp = 'epoch' and data->'spec'->'template'->>'id' = old.id)
    or exists (select 1 from bare_metal_instance_catalog_items where deletion_timestamp = 'epoch' and data->'template'->>'id' = old.id) then
    raise exception using errcode = 'Z0003', message = format('cannot delete template ''%s'': it is in use', old.id);
  end if;
  return new;
end;
$$ language plpgsql;
drop trigger if exists check_bare_metal_instance_template_not_in_use on bare_metal_instance_templates;
create trigger check_bare_metal_instance_template_not_in_use before update on bare_metal_instance_templates
  for each row when (old.deletion_timestamp = 'epoch' and new.deletion_timestamp != 'epoch')
  execute function check_bare_metal_instance_template_not_in_use();

create or replace function check_bare_metal_instance_type_not_in_use() returns trigger as $$
begin
  if exists (select 1 from bare_metal_instances where deletion_timestamp = 'epoch' and data->'spec'->'instance_type'->>'id' = old.id)
    or exists (select 1 from bare_metal_instance_catalog_items where deletion_timestamp = 'epoch' and (
      data->'fields'->'instance_type'->'locked'->>'id' = old.id
      or data->'fields'->'instance_type'->'editable'->'default_value'->>'id' = old.id
    )) then
    raise exception using errcode = 'Z0003', message = format('cannot delete bare metal instance type ''%s'': it is in use', old.id);
  end if;
  return new;
end;
$$ language plpgsql;
create trigger check_bare_metal_instance_type_not_in_use before update on bare_metal_instance_types
  for each row when (old.deletion_timestamp = 'epoch' and new.deletion_timestamp != 'epoch')
  execute function check_bare_metal_instance_type_not_in_use();

-- Host types in arbitrary node-set maps are strong references too.
create function check_host_type_not_in_use() returns trigger as $$
begin
  if exists (
    select 1 from clusters c cross join lateral jsonb_each(c.data->'spec'->'node_sets') node
    where c.deletion_timestamp = 'epoch' and node.value->'host_type'->>'id' = old.id
  ) or exists (
    select 1 from cluster_templates c cross join lateral jsonb_each(c.data->'node_sets') node
    where c.deletion_timestamp = 'epoch' and node.value->'host_type'->>'id' = old.id
  ) or exists (
    select 1 from cluster_catalog_items c
    cross join lateral jsonb_each(
      coalesce(c.data->'fields'->'node_sets'->'locked'->'items', '{}'::jsonb)
      || coalesce(c.data->'fields'->'node_sets'->'editable'->'default_value'->'items', '{}'::jsonb)
    ) node
    where c.deletion_timestamp = 'epoch' and node.value->'host_type'->>'id' = old.id
  ) or exists (
    select 1 from bare_metal_instance_templates t
    where t.deletion_timestamp = 'epoch' and t.data->>'host_type' = old.id
  ) then
    raise exception using errcode = 'Z0003', message = format('cannot delete host type ''%s'': it is in use', old.id);
  end if;
  return new;
end;
$$ language plpgsql;
create trigger check_host_type_not_in_use before update on host_types
  for each row when (old.deletion_timestamp = 'epoch' and new.deletion_timestamp != 'epoch')
  execute function check_host_type_not_in_use();
