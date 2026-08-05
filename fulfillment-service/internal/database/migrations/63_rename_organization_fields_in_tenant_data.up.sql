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

-- Rename JSON fields inside the data column of the tenants (formerly organizations) table to match the new Tenant
-- proto field names. Without this, protojson deserialization with DiscardUnknown silently drops the old field names.

-- Rename idp_organization_name to idp_tenant_name in the status object:
update tenants
set data = jsonb_set(
  data #- '{status,idp_organization_name}',
  '{status,idp_tenant_name}',
  data->'status'->'idp_organization_name'
)
where data->'status' ? 'idp_organization_name';

update archived_tenants
set data = jsonb_set(
  data #- '{status,idp_organization_name}',
  '{status,idp_tenant_name}',
  data->'status'->'idp_organization_name'
)
where data->'status' ? 'idp_organization_name';

-- Rename ORGANIZATION_STATE_* enum values to TENANT_STATE_* in the status.state field:
update tenants
set data = jsonb_set(
  data,
  '{status,state}',
  to_jsonb(replace(data->'status'->>'state', 'ORGANIZATION_STATE_', 'TENANT_STATE_'))
)
where data->'status'->>'state' like 'ORGANIZATION_STATE_%';

update archived_tenants
set data = jsonb_set(
  data,
  '{status,state}',
  to_jsonb(replace(data->'status'->>'state', 'ORGANIZATION_STATE_', 'TENANT_STATE_'))
)
where data->'status'->>'state' like 'ORGANIZATION_STATE_%';
