--
-- Copyright (c) 2026 Red Hat, Inc.
--
-- Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
-- License. You may obtain a copy of the License at
--
--   http://www.apache.org/licenses/LICENSE-2.0
--
-- Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
-- "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
-- specific language governing permissions and limitations under the License.
--

-- Finalize the SSH-key Secret reference migration (OSAC-5429): remove the
-- deprecated inline key material from stored resources.
--
-- Legacy inline values are dropped without conversion: no Secret is
-- synthesized on a tenant's behalf, and an existing resource that needs later
-- provisioning must select a registered key through spec.ssh_key.
--
-- Like the BMaaS and VMaaS registered-key support, reference integrity is
-- enforced at the API layer (resolveAndCanonicalizeSSHKeyReference); no
-- database trigger guards the reference.
update bare_metal_instances
  set data = data - 'spec' || jsonb_build_object(
    'spec', (data->'spec') - 'ssh_public_key'
  )
  where data ? 'spec' and data->'spec' ? 'ssh_public_key';

update clusters
  set data = data - 'spec' || jsonb_build_object(
    'spec', (data->'spec') - 'ssh_public_key'
  )
  where data ? 'spec' and data->'spec' ? 'ssh_public_key';

update bare_metal_instance_catalog_items
  set data = data - 'fields' || jsonb_build_object(
    'fields', (data->'fields') - 'ssh_public_key'
  )
  where data ? 'fields' and data->'fields' ? 'ssh_public_key';

update cluster_catalog_items
  set data = data - 'fields' || jsonb_build_object(
    'fields', (data->'fields') - 'ssh_public_key'
  )
  where data ? 'fields' and data->'fields' ? 'ssh_public_key';
