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

-- This migration converts compute instances from subnet and security_groups fields to the
-- network_attachments array format.
--
-- Transformation: {subnet: "x", security_groups: ["y", "z"]} → {network_attachments: [{subnet: "x", security_groups: ["y", "z"]}]}

-- Migrate compute_instances table
UPDATE compute_instances
SET data = jsonb_set(
    -- First, remove the old fields from spec
    data #- '{spec,subnet}' #- '{spec,security_groups}',
    -- Then add network_attachments array
    '{spec,network_attachments}',
    -- Build array with single NetworkAttachment from old fields
    jsonb_build_array(
        jsonb_build_object(
            'subnet', data->'spec'->>'subnet',
            'security_groups', COALESCE(data->'spec'->'security_groups', '[]'::jsonb)
        )
    )
)
WHERE
    -- Only update rows that have a subnet to migrate
    data->'spec' ? 'subnet'
    -- And don't already have network_attachments (avoid overwriting)
    AND NOT (data->'spec' ? 'network_attachments');

-- Strip orphaned security_groups from rows that have no subnet.
-- These shouldn't exist per API validation, but handle defensively to avoid
-- creating invalid network_attachments with null subnet.
UPDATE compute_instances
SET data = data #- '{spec,security_groups}'
WHERE
    data->'spec' ? 'security_groups'
    AND NOT (data->'spec' ? 'subnet')
    AND NOT (data->'spec' ? 'network_attachments');

-- Migrate archived_compute_instances table
UPDATE archived_compute_instances
SET data = jsonb_set(
    data #- '{spec,subnet}' #- '{spec,security_groups}',
    '{spec,network_attachments}',
    jsonb_build_array(
        jsonb_build_object(
            'subnet', data->'spec'->>'subnet',
            'security_groups', COALESCE(data->'spec'->'security_groups', '[]'::jsonb)
        )
    )
)
WHERE
    data->'spec' ? 'subnet'
    AND NOT (data->'spec' ? 'network_attachments');

UPDATE archived_compute_instances
SET data = data #- '{spec,security_groups}'
WHERE
    data->'spec' ? 'security_groups'
    AND NOT (data->'spec' ? 'subnet')
    AND NOT (data->'spec' ? 'network_attachments');
