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

-- Move the 'kubeconfig' and 'namespace' fields of hubs into a nested 'spec' object and add an empty 'status'.
update hubs
set data = (data - 'kubeconfig' - 'namespace') ||
  jsonb_build_object(
    'spec', jsonb_strip_nulls(jsonb_build_object(
      'kubeconfig', data->'kubeconfig',
      'namespace', data->'namespace'
    )),
    'status', '{}'::jsonb
  );

update archived_hubs
set data = (data - 'kubeconfig' - 'namespace') ||
  jsonb_build_object(
    'spec', jsonb_strip_nulls(jsonb_build_object(
      'kubeconfig', data->'kubeconfig',
      'namespace', data->'namespace'
    )),
    'status', '{}'::jsonb
  );
