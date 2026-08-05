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

-- Convert embedded reference fields from string to object format.
-- The type-safe references PR changed proto fields from string to reference messages:
--   ClusterTemplateNodeSet.host_type: string → HostTypeReference
--   ComputeInstanceTemplateSpecDefaults.instance_type: string → InstanceTypeReference
--
-- Existing data has these as bare strings (e.g., "host_type": "fc430").
-- The new proto expects objects (e.g., "host_type": {"id": "fc430"}).
-- Without this migration, protojson.Unmarshal fails when reading templates from the DB.

-- Cluster templates: convert node_sets.*.host_type from string to {"id": value}
update cluster_templates
set data = (
  select jsonb_set(data, '{node_sets}', coalesce(
    (select jsonb_object_agg(
      key,
      case
        when jsonb_typeof(value->'host_type') = 'string'
        then jsonb_set(value, '{host_type}', jsonb_build_object('id', value->>'host_type'))
        else value
      end
    )
    from jsonb_each(data->'node_sets')),
    '{}'::jsonb
  ))
)
where data->'node_sets' is not null
  and exists (
    select 1 from jsonb_each(data->'node_sets') as ns(key, value)
    where jsonb_typeof(ns.value->'host_type') = 'string'
  );

-- Compute instance templates: convert spec_defaults.instance_type from string to {"id": value}
update compute_instance_templates
set data = jsonb_set(
  data,
  '{spec_defaults,instance_type}',
  jsonb_build_object('id', data->'spec_defaults'->>'instance_type')
)
where data->'spec_defaults' is not null
  and data->'spec_defaults'->'instance_type' is not null
  and jsonb_typeof(data->'spec_defaults'->'instance_type') = 'string';
