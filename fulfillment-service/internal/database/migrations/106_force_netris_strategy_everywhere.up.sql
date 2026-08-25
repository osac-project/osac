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

-- TEST BRANCH ONLY: force all NetworkClasses to use netris as implementation
-- strategy and fabric manager. This ensures every resource created by the
-- fulfillment-service gets netris in its spec, eliminating races between the
-- operator annotation override and AAP job triggers.

UPDATE network_classes
SET data = jsonb_set(
    jsonb_set(data, '{implementation_strategy}', '"netris"'),
    '{fabric_manager}', '"netris"'
)
WHERE name = 'cudn-net';

-- Also update any VirtualNetworks that already have cudn_net baked in
UPDATE virtual_networks
SET data = jsonb_set(data, '{spec,implementation_strategy}', '"netris"')
WHERE data->'spec'->>'implementation_strategy' = 'cudn_net';

-- Update ExternalIPPools that use metallb-l2
UPDATE network_classes
SET data = jsonb_set(
    jsonb_set(data, '{implementation_strategy}', '"netris"'),
    '{fabric_manager}', '"netris"'
)
WHERE name = 'metallb-l2';
