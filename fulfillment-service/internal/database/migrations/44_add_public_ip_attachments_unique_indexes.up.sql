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

-- Enforce that each PublicIP has at most one active PublicIPAttachment.
--
-- Without this constraint, concurrent create requests could both pass the application-level
-- uniqueness check (validateUniqueness) and succeed, leaving two attachments for the same
-- PublicIP. The partial unique index covers only active (non-deleted) rows where public_ip
-- is set.
create unique index public_ip_attachments_one_per_public_ip
  on public_ip_attachments ((data -> 'spec' ->> 'public_ip'))
  where data -> 'spec' ->> 'public_ip' is not null
    and data -> 'spec' ->> 'public_ip' != ''
    and deletion_timestamp = 'epoch';

-- Enforce that each ComputeInstance has at most one active PublicIPAttachment.
create unique index public_ip_attachments_one_per_compute_instance
  on public_ip_attachments ((data -> 'spec' ->> 'compute_instance'))
  where data -> 'spec' ->> 'compute_instance' is not null
    and data -> 'spec' ->> 'compute_instance' != ''
    and deletion_timestamp = 'epoch';
