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

-- Enforce that each ComputeInstance has at most one PublicIP attached.
--
-- Without this constraint, concurrent attach requests could both pass the application-level
-- uniqueness check (List query) and succeed, leaving two PublicIPs attached to the same
-- ComputeInstance. The partial unique index covers only active (non-deleted) rows where
-- compute_instance is set.
create unique index public_ips_one_per_compute_instance
  on public_ips ((data -> 'spec' ->> 'compute_instance'))
  where data -> 'spec' ->> 'compute_instance' is not null
    and data -> 'spec' ->> 'compute_instance' != ''
    and deletion_timestamp = 'epoch';
