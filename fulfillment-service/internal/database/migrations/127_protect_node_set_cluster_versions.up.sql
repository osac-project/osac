--
-- Copyright (c) 2026 Red Hat, Inc.
--
-- Licensed under the Apache License, Version 2.0 (the "License");
--

-- Extend the existing ClusterVersion soft-deletion guard to node-set version
-- references. The trigger installed by migration 94 calls this function.
-- No Cluster data is changed by this migration.
create or replace function check_cluster_version_not_in_use() returns trigger as $$
begin
  if exists (
    select 1
    from clusters c
    where c.deletion_timestamp = 'epoch'
      and (
        c.data #>> '{spec,version,id}' = old.id
        or exists (
          select 1
          from jsonb_each(
            case when jsonb_typeof(c.data #> '{spec,node_sets}') = 'object'
              then c.data #> '{spec,node_sets}'
              else '{}'::jsonb
            end
          ) as ns(name, node_set)
          where ns.node_set #>> '{version,id}' = old.id
        )
      )
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format('cannot delete cluster version ''%s'': it is in use by at least one cluster', old.name);
  end if;

  if exists (
    select 1
    from cluster_templates t
    where t.deletion_timestamp = 'epoch'
      and t.data #>> '{spec_defaults,version,id}' = old.id
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format('cannot delete cluster version ''%s'': it is in use by at least one cluster template', old.name);
  end if;

  if exists (
    select 1 from cluster_catalog_items c
    where c.deletion_timestamp = 'epoch'
      and (
        c.data #>> '{fields,version,locked,id}' = old.id
        or c.data #>> '{fields,version,editable,default_value,id}' = old.id
      )
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format('cannot delete cluster version ''%s'': it is in use by at least one cluster catalog item', old.name);
  end if;

  return new;
end;
$$ language plpgsql;
