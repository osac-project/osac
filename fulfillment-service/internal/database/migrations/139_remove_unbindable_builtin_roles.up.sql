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

-- Remove built-in Role catalog entries that cannot be explicitly bound.
-- The tenant user permissions remain the implicit default for authenticated users.
-- Match the seed IDs so user-created roles are not affected.
delete from roles
where id in (
  '019dd9f6-36cf-77bc-8041-c6a6a415c185'
);

-- Add tenant-idp-manager role for IdP configuration management.
insert into roles (id, name, tenant, data) values
(
  '019dd9f6-d573-beaa-41c5-96965e594cc6',
  'tenant-idp-manager',
  'shared',
  '{
    "spec": {
      "title": "Tenant IdP manager",
      "description": "IdP configuration permissions within a tenant."
    },
    "status": {
      "state": "ROLE_STATE_READY"
    }
  }'
);
