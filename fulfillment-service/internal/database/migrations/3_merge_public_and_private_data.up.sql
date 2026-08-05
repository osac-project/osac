--
-- Copyright (c) 2025 Red Hat Inc.
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

--
-- Merge the data.
--
update cluster_orders as public set data = public.data || (select data from private.cluster_orders as private where private.id = public.id);
update clusters as public set data = public.data || (select data from private.clusters as private where private.id = public.id);

--
-- Move the hubs table to the public schema.
--
alter table private.hubs set schema public;

--
-- Drop the triggers the create the private rows automatically.
--
drop trigger create_empty_private_cluster_order on cluster_orders;
drop trigger create_empty_private_cluster on clusters;

--
-- Drop the private schema and the associated objects.
--
drop schema private cascade;
