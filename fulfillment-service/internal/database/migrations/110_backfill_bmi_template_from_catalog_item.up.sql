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

-- Backfill spec.template on bare metal instances from their catalog item's template reference.
-- The direct-template provisioning alignment changed the reconciler to read spec.template directly
-- instead of fetching the catalog item to extract its template ID. Existing rows only have
-- spec.catalog_item set; this migration copies the catalog item's template reference into
-- spec.template so the reconciler can find it.

update bare_metal_instances bmi
set data = jsonb_set(bmi.data, '{spec,template}', ci.template)
from (
  select
    b.id as bmi_id,
    (
      select c.data->'template'
      from bare_metal_instance_catalog_items c
      where c.data ? 'template'
        and (
          c.id = b.data->'spec'->'catalog_item'->>'id'
          or c.name = b.data->'spec'->'catalog_item'->>'name'
        )
      order by (c.id = b.data->'spec'->'catalog_item'->>'id') desc, c.id
      limit 1
    ) as template
  from bare_metal_instances b
  where b.data->'spec'->'catalog_item' is not null
    and b.data->'spec'->'template' is null
) ci
where bmi.id = ci.bmi_id
  and ci.template is not null;
