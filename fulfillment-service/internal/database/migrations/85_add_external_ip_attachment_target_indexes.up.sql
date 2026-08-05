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

-- Migration 61 added unique indexes for the external_ip and compute_instance targets of
-- ExternalIPAttachment but omitted the cluster and baremetal_instance targets. This migration adds
-- the missing indexes.
--
-- For cluster targets the index is compound (cluster, target_endpoint) so that a cluster may have
-- one attachment per endpoint type (API and Ingress).

-- Enforce that each (Cluster, TargetEndpoint) pair has at most one active ExternalIPAttachment.
create unique index external_ip_attachments_one_per_cluster_endpoint
  on external_ip_attachments (
    (data -> 'spec' ->> 'cluster'),
    (data -> 'spec' ->> 'target_endpoint')
  )
  where data -> 'spec' ->> 'cluster' is not null
    and data -> 'spec' ->> 'cluster' != ''
    and deletion_timestamp = 'epoch';

-- Enforce that each BareMetalInstance has at most one active ExternalIPAttachment.
create unique index external_ip_attachments_one_per_baremetal_instance
  on external_ip_attachments ((data -> 'spec' ->> 'baremetal_instance'))
  where data -> 'spec' ->> 'baremetal_instance' is not null
    and data -> 'spec' ->> 'baremetal_instance' != ''
    and deletion_timestamp = 'epoch';
