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

-- Convert version_name from string to ClusterVersionReference object and rename to version.
-- The proto field changed from: optional string version_name = 6
-- To: ClusterVersionReference version = 6
--
-- Existing data has: "version_name": "4-17-0" (bare string)
-- New proto expects: "version": {"name": "4-17-0"} (reference object)

-- Clusters: convert spec.version_name string to spec.version reference object
update clusters
set data = jsonb_set(
  data #- '{spec,version_name}',
  '{spec,version}',
  jsonb_build_object('name', data->'spec'->>'version_name')
)
where data->'spec'->'version_name' is not null
  and jsonb_typeof(data->'spec'->'version_name') = 'string';

-- Archived clusters: same conversion
update archived_clusters
set data = jsonb_set(
  data #- '{spec,version_name}',
  '{spec,version}',
  jsonb_build_object('name', data->'spec'->>'version_name')
)
where data->'spec'->'version_name' is not null
  and jsonb_typeof(data->'spec'->'version_name') = 'string';

-- Cluster templates: convert spec_defaults.version_name to spec_defaults.version
update cluster_templates
set data = jsonb_set(
  data #- '{spec_defaults,version_name}',
  '{spec_defaults,version}',
  jsonb_build_object('name', data->'spec_defaults'->>'version_name')
)
where data->'spec_defaults' is not null
  and data->'spec_defaults'->'version_name' is not null
  and jsonb_typeof(data->'spec_defaults'->'version_name') = 'string';

-- Archived cluster templates: same conversion
update archived_cluster_templates
set data = jsonb_set(
  data #- '{spec_defaults,version_name}',
  '{spec_defaults,version}',
  jsonb_build_object('name', data->'spec_defaults'->>'version_name')
)
where data->'spec_defaults' is not null
  and data->'spec_defaults'->'version_name' is not null
  and jsonb_typeof(data->'spec_defaults'->'version_name') = 'string';

-- Cluster catalog items: convert field_definitions path "version_name" to "version"
-- and convert string defaults to reference object defaults
update cluster_catalog_items
set data = jsonb_set(data, '{field_definitions}',
  (select coalesce(jsonb_agg(
    case
      when fd->>'path' = 'version_name' then
        case
          when fd->'default' is not null and jsonb_typeof(fd->'default') = 'string' then
            jsonb_set(
              jsonb_set(fd, '{path}', '"version"'),
              '{default}',
              jsonb_build_object('name', fd->>'default')
            )
          else
            jsonb_set(fd, '{path}', '"version"')
        end
      else fd
    end
  ), '[]'::jsonb)
  from jsonb_array_elements(data->'field_definitions') as fd)
)
where data->'field_definitions' is not null
  and exists (
    select 1 from jsonb_array_elements(data->'field_definitions') as fd
    where fd->>'path' = 'version_name'
  );

-- Archived cluster catalog items: same conversion
update archived_cluster_catalog_items
set data = jsonb_set(data, '{field_definitions}',
  (select coalesce(jsonb_agg(
    case
      when fd->>'path' = 'version_name' then
        case
          when fd->'default' is not null and jsonb_typeof(fd->'default') = 'string' then
            jsonb_set(
              jsonb_set(fd, '{path}', '"version"'),
              '{default}',
              jsonb_build_object('name', fd->>'default')
            )
          else
            jsonb_set(fd, '{path}', '"version"')
        end
      else fd
    end
  ), '[]'::jsonb)
  from jsonb_array_elements(data->'field_definitions') as fd)
)
where data->'field_definitions' is not null
  and exists (
    select 1 from jsonb_array_elements(data->'field_definitions') as fd
    where fd->>'path' = 'version_name'
  );
