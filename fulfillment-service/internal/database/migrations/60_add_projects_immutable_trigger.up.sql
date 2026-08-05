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

-- Attach the immutable column trigger to the projects table to prevent mutation of server-assigned fields.
-- The creator field is server-assigned at creation time and used by the project reconciler to grant
-- system:managers group membership. Allowing clients to mutate this field would enable privilege escalation.
create trigger check_immutable_columns
  before update on projects
  for each row
  execute function check_immutable_columns('name', 'tenant', 'creator');
