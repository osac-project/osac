-- Copyright (c) 2026 Red Hat Inc.
--
-- Licensed under the Apache License, Version 2.0 (the "License");
-- you may not use this file except in compliance with the License.
-- You may obtain a copy of the License at
--
--   http://www.apache.org/licenses/LICENSE-2.0
--
-- Unless required by applicable law or agreed to in writing, software
-- distributed under the License is distributed on an "AS IS" BASIS,
-- WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
-- See the License for the specific language governing permissions and
-- limitations under the License.

-- Ordinary index builds block writes to each table while the migration runs.
create index users_by_live_username
  on users (tenant, (data -> 'spec' ->> 'username')) include (id, name)
  where deletion_timestamp = 'epoch';

create index role_bindings_by_user_subject
  on role_bindings using gin ((data -> 'spec' -> 'users') jsonb_path_ops)
  where deletion_timestamp = 'epoch';
