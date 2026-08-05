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

-- This migration adds the builtin 'system' and 'shared' organizations. These are tenants that exist by convention and
-- are used throughout the codebase. Having them as rows in the database ensures they can be discovered via the API like
-- any other organization.

insert into organizations (id, name, tenant, creator, data)
values ('system', 'system', 'system', 'system', '{}');

insert into organizations (id, name, tenant, creator, data)
values ('shared', 'shared', 'shared', 'system', '{}');
