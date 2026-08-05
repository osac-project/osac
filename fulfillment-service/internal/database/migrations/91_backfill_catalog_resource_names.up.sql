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

-- Backfill metadata.name for catalog resources that historically never set it.
-- The name column is derived from the resource's domain-specific identifier:
--   - Templates: role name extracted from the ID (after last dot), underscores → hyphens
--   - NetworkClass: implementation_strategy field, underscores → hyphens
--   - HostType: id field, underscores → hyphens

-- Cluster templates: id = "osac.templates.ocp_small" → name = "ocp-small"
update cluster_templates
set name = replace(
  reverse(split_part(reverse(id), '.', 1)),
  '_', '-'
)
where name = '' and id like '%.%';

-- Compute instance templates: same pattern
update compute_instance_templates
set name = replace(
  reverse(split_part(reverse(id), '.', 1)),
  '_', '-'
)
where name = '' and id like '%.%';

-- Bare metal instance templates: same pattern
update bare_metal_instance_templates
set name = replace(
  reverse(split_part(reverse(id), '.', 1)),
  '_', '-'
)
where name = '' and id like '%.%';

-- Network classes: derive from implementation_strategy
update network_classes
set name = replace(data->>'implementation_strategy', '_', '-')
where name = '' and data->>'implementation_strategy' is not null
  and data->>'implementation_strategy' != '';

-- Host types: derive from id
update host_types
set name = replace(id, '_', '-')
where name = '';

-- Sync the data column's metadata.name with the name column for all affected tables.
-- The generic DAO reads metadata.name from the data column, so both must be consistent.
-- First ensure metadata key exists, then set metadata.name.
update cluster_templates
set data = jsonb_set(
  jsonb_set(data, '{metadata}', coalesce(data->'metadata', '{}'::jsonb)),
  '{metadata,name}',
  to_jsonb(name)
)
where name != '' and (
  data->'metadata' is null
  or data->'metadata'->>'name' is null
  or data->'metadata'->>'name' = ''
);

update compute_instance_templates
set data = jsonb_set(
  jsonb_set(data, '{metadata}', coalesce(data->'metadata', '{}'::jsonb)),
  '{metadata,name}',
  to_jsonb(name)
)
where name != '' and (
  data->'metadata' is null
  or data->'metadata'->>'name' is null
  or data->'metadata'->>'name' = ''
);

update bare_metal_instance_templates
set data = jsonb_set(
  jsonb_set(data, '{metadata}', coalesce(data->'metadata', '{}'::jsonb)),
  '{metadata,name}',
  to_jsonb(name)
)
where name != '' and (
  data->'metadata' is null
  or data->'metadata'->>'name' is null
  or data->'metadata'->>'name' = ''
);

update network_classes
set data = jsonb_set(
  jsonb_set(data, '{metadata}', coalesce(data->'metadata', '{}'::jsonb)),
  '{metadata,name}',
  to_jsonb(name)
)
where name != '' and (
  data->'metadata' is null
  or data->'metadata'->>'name' is null
  or data->'metadata'->>'name' = ''
);

update host_types
set data = jsonb_set(
  jsonb_set(data, '{metadata}', coalesce(data->'metadata', '{}'::jsonb)),
  '{metadata,name}',
  to_jsonb(name)
)
where name != '' and (
  data->'metadata' is null
  or data->'metadata'->>'name' is null
  or data->'metadata'->>'name' = ''
);

-- Add unique name per tenant constraints for resources that don't have them yet.
-- Follows the catalog item pattern from migration 53.
create unique index cluster_templates_unique_name_per_tenant
  on cluster_templates (name, tenant)
  where deletion_timestamp = 'epoch'
    and name != '';

create unique index compute_instance_templates_unique_name_per_tenant
  on compute_instance_templates (name, tenant)
  where deletion_timestamp = 'epoch'
    and name != '';

create unique index bare_metal_instance_templates_unique_name_per_tenant
  on bare_metal_instance_templates (name, tenant)
  where deletion_timestamp = 'epoch'
    and name != '';

create unique index network_classes_unique_name_per_tenant
  on network_classes (name, tenant)
  where deletion_timestamp = 'epoch'
    and name != '';

create unique index host_types_unique_name_per_tenant
  on host_types (name, tenant)
  where deletion_timestamp = 'epoch'
    and name != '';
