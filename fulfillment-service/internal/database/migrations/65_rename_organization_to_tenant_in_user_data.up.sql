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

-- Rename the "organization" JSON key to "tenant" inside the spec object of all User records. Without this, protojson
-- deserialization with DiscardUnknown silently drops the old field name.

update users
set data = jsonb_set(
  data #- '{spec,organization}',
  '{spec,tenant}',
  case
    when data->'spec' ? 'tenant' then data->'spec'->'tenant'
    else data->'spec'->'organization'
  end
)
where data->'spec' ? 'organization';

update archived_users
set data = jsonb_set(
  data #- '{spec,organization}',
  '{spec,tenant}',
  case
    when data->'spec' ? 'tenant' then data->'spec'->'tenant'
    else data->'spec'->'organization'
  end
)
where data->'spec' ? 'organization';
