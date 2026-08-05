--
-- Copyright (c) 2025 Red Hat Inc.
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

--
-- We want to merge cluster orders and clusters, and the result will be in the `clusters` table. But some cluster orders
-- may not have yet a corresponding cluster. To handle that we first merge everything into the existing `cluster_orders`
-- table, and then we rename it to `clusters`.
--

-- Delete the cluster that aren't ready:
delete from clusters where data->'status'->>'state' <> 'CLUSTER_STATE_READY';

-- Copy the clusters order 'spec' into the clusters:
update clusters as c set
  data = jsonb_set(
    c.data,
    '{spec}',
    (c.data->'spec') || (
      select
        o.data->'spec'
      from
        cluster_orders as o
      where
        o.data->'status'->>'cluster_id' = c.id
    )
  )
;

-- Drop the cluster orders tables:
drop table cluster_orders;
drop table archived_cluster_orders;

-- Rename 'spec.template_id' to 'spec.template':
update clusters set
  data = jsonb_set(
    data,
    '{spec}',
    data->'spec' || jsonb_build_object('template', data->'spec'->'template_id')
  )
where
  data->'spec' ? 'template_id'
;

update clusters set
  data = jsonb_set(
    data,
    '{spec}',
    (data->'spec') - 'template_id'
  )
where
  data->'spec' ? 'template_id'
;

-- Rename 'hub_id' to 'status.hub':
update clusters set
  data = jsonb_set(
    data,
    '{status}',
    data->'status' || jsonb_build_object('hub', data->'hub_id')
  )
where
  data ? 'hub_id'
;

update clusters set
  data = data - 'hub_id'
where
  data ? 'hub_id'
;

-- Update the identifiers of the clusters so that they are the identifiers of the cluster order that
-- created them:
update clusters set id = data->>'order_id' where data ? 'order_id';

-- Delete the 'order_id' field:
update clusters set data = data - 'order_id';
