--
-- Copyright (c) 2026 Red Hat Inc.
--
-- Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
-- License. You may obtain a copy of the License at
--
--   http://www.apache.org/licenses/LICENSE-2.0
--
-- Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
-- an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
-- specific language governing permissions and limitations under the License.
--

-- The metering annotation of a bare metal instance's cluster reference now names the owner
-- resource, so the calculator can resolve the ownership root and gate the worker's billing on
-- the root's states. The trigger does not read the new attribute: the set-once rule already
-- covers the field. The copy embedded here carries it anyway, because the drift test compares
-- the installed tracked-field list against the annotations wholesale.

create or replace function metering_tracked_fields(source_table text) returns jsonb as $$
  select case source_table
    when 'compute_instances' then $tracked$
      [
        {"path": "status.instance_type", "kind": "varies", "transition_time": "status.instance_type_transition_time"},
        {"path": "status.state", "kind": "varies", "transition_time": "status.state_transition_time"},
        {"path": "spec.boot_disk.size_gib", "kind": "constant"},
        {"path": "spec.catalog_item", "kind": "constant"},
        {"path": "spec.disk_image", "kind": "constant"}
      ]
    $tracked$::jsonb
    when 'clusters' then $tracked$
      [
        {"path": "spec.version", "kind": "varies", "transition_time": "status.version_transition_time"},
        {"path": "status.state", "kind": "varies", "transition_time": "status.state_transition_time"},
        {"path": "spec.template", "kind": "constant"}
      ]
    $tracked$::jsonb
    when 'bare_metal_instances' then $tracked$
      [
        {"path": "status.state", "kind": "varies", "transition_time": "status.state_transition_time"},
        {"path": "spec.catalog_item", "kind": "constant"},
        {"path": "spec.instance_type", "kind": "constant"},
        {"path": "status.cluster", "kind": "fixed_once_set", "owner_resource": "Cluster"},
        {"path": "status.node_set", "kind": "fixed_once_set"}
      ]
    $tracked$::jsonb
    when 'volumes' then $tracked$
      [
        {"path": "status.state", "kind": "varies", "transition_time": "status.state_transition_time"},
        {"path": "spec.storage_tier", "kind": "constant"},
        {"path": "status.protocol", "kind": "constant"},
        {"path": "status.provisioned_size_gib", "kind": "fixed_once_set"},
        {"path": "status.vendor_volume_id", "kind": "fixed_once_set"}
      ]
    $tracked$::jsonb
    when 'external_ips' then $tracked$
      [
        {"path": "status.address", "kind": "fixed_once_set"},
        {"path": "status.attached", "kind": "varies", "transition_time": "status.attachment_transition_time"},
        {"path": "status.attribution", "kind": "varies", "transition_time": "status.attachment_transition_time"},
        {"path": "status.state", "kind": "varies", "transition_time": "status.state_transition_time"},
        {"path": "spec.pool", "kind": "constant"}
      ]
    $tracked$::jsonb
    when 'nat_gateways' then $tracked$
      [
        {"path": "status.state", "kind": "varies", "transition_time": "status.state_transition_time"},
        {"path": "spec.external_ip", "kind": "constant"},
        {"path": "spec.virtual_network", "kind": "constant"}
      ]
    $tracked$::jsonb
  end;
$$ language sql immutable;
