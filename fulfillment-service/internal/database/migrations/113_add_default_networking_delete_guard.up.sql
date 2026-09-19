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

-- Prevent soft-deleting default networking resources (labeled osac.openshift.io/default=true).
-- The server-level validateNotDefault() check already blocks user-facing Delete requests;
-- this trigger provides defense-in-depth for any code path that soft-deletes via the DAO
-- directly. The system Deprovision path (tenant teardown) sets the transaction-local GUC
-- osac.deprovision_in_progress = 'true' to bypass this guard.

create function check_not_default_networking() returns trigger as $$
begin
  if old.labels @> '{"osac.openshift.io/default": "true"}'::jsonb then
    if coalesce(current_setting('osac.deprovision_in_progress', true), '') != 'true' then
      raise exception using
        errcode = 'Z0003',
        message = format(
          'cannot delete default %s: default networking resources are system-managed',
          tg_argv[0]
        );
    end if;
  end if;
  return new;
end;
$$ language plpgsql;

create trigger check_not_default_networking
  before update on virtual_networks
  for each row
  when (old.deletion_timestamp = 'epoch' and new.deletion_timestamp != 'epoch')
  execute function check_not_default_networking('virtual network');

create trigger check_not_default_networking
  before update on subnets
  for each row
  when (old.deletion_timestamp = 'epoch' and new.deletion_timestamp != 'epoch')
  execute function check_not_default_networking('subnet');

create trigger check_not_default_networking
  before update on nat_gateways
  for each row
  when (old.deletion_timestamp = 'epoch' and new.deletion_timestamp != 'epoch')
  execute function check_not_default_networking('NAT gateway');

create trigger check_not_default_networking
  before update on security_groups
  for each row
  when (old.deletion_timestamp = 'epoch' and new.deletion_timestamp != 'epoch')
  execute function check_not_default_networking('security group');

create trigger check_not_default_networking
  before update on external_ips
  for each row
  when (old.deletion_timestamp = 'epoch' and new.deletion_timestamp != 'epoch')
  execute function check_not_default_networking('external IP');
