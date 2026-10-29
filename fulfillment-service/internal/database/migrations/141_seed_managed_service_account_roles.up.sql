--
-- Copyright (c) 2026 Red Hat Inc.
--
-- Licensed under the Apache License, Version 2.0 (the "License");
-- you may not use this file except in compliance with the License.
-- You may obtain a copy of the License at
--
--   http://www.apache.org/licenses/LICENSE-2.0
--

-- Seed the catalog roles used by OSAC-6554 managed service-account grants.
-- tenant-admin is seeded by migration 33.
-- tenant-idp-manager is seeded by migration 139.
insert into roles (id, name, tenant, data) values
(
  'cd33ed2b-5a5f-4b0a-adcb-5a0a567fa511',
  'tenant-automation',
  'shared',
  '{"spec":{"title":"Tenant automation","description":"Tenant-scoped automation permissions. For service accounts only."},"status":{"state":"ROLE_STATE_READY"}}'
),
(
  '6eafb57d-4344-43fc-86eb-8ce17749f5ec',
  'osac-csi-driver',
  'shared',
  '{"spec":{"title":"OSAC CSI driver","description":"Tenant-scoped volume lifecycle permissions. For service accounts only."},"status":{"state":"ROLE_STATE_READY"}}'
);
