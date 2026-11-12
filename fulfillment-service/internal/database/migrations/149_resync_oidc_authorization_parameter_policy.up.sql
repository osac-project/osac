--
-- Copyright (c) 2026 Red Hat Inc.
--
-- Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
-- the License. You may obtain a copy of the License at
--
--   http://www.apache.org/licenses/LICENSE-2.0
--
-- Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
-- "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
-- specific language governing permissions and limitations under the License.
--

-- Existing READY OIDC providers must be reconciled once so Keycloak receives the explicit
-- empty forwardParameters list instead of retaining its default acr_values forwarding.
update identity_providers
set data = jsonb_set(
      jsonb_set(
        jsonb_set(data, '{status,phase}', '"IDENTITY_PROVIDER_PHASE_UNKNOWN"'::jsonb),
        '{status,message}',
        '"Reconciliation required after OIDC authorization parameter forwarding update"'::jsonb
      ),
      '{status,conditions}',
      coalesce(data->'status'->'conditions', '[]'::jsonb) || jsonb_build_array(jsonb_build_object(
        'type', 'OIDCForwardParametersResync',
        'status', 'CONDITION_STATUS_UNSPECIFIED',
        'reason', 'ResyncRequired',
        'message', 'Reconciliation required after OIDC authorization parameter forwarding update'
      ))
    ),
    version = version + 1
where deletion_timestamp = 'epoch'
  and (data->'spec') ? 'oidc'
  and data->'status'->>'phase' = 'IDENTITY_PROVIDER_PHASE_READY';
