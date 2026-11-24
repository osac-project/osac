--
-- Copyright (c) 2026 Red Hat Inc.
--
-- Licensed under the Apache License, Version 2.0 (the "License");
-- you may not use this file except in compliance with the License.
-- You may obtain a copy of the License at
--
--   http://www.apache.org/licenses/LICENSE-2.0
--

update tenants as t
set data = jsonb_set(t.data, '{spec}',
  coalesce(nullif(t.data->'spec', 'null'::jsonb), '{}'::jsonb) || jsonb_build_object(
    'default_role', jsonb_build_object('id', r.id, 'name', r.name, 'tenant', r.tenant, 'shared', true)))
from roles as r
where t.id = 'shared'
  and nullif(t.data #> '{spec,default_role}', 'null'::jsonb) is null
  and r.id = '019dd9f6-36cf-77bc-8041-c6a6a415c185'
  and r.name = 'tenant-user'
  and r.tenant = 'shared'
  and r.deletion_timestamp = 'epoch';
