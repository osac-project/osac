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

create schema private;

--
-- Private cluster orders.
--
create table private.cluster_orders (
  id text not null primary key references cluster_orders(id) on delete cascade,
  creation_timestamp timestamp with time zone not null default now(),
  deletion_timestamp timestamp with time zone not null default 'epoch',
  data jsonb not null
);

create function private.create_empty_cluster_order() returns trigger as $$
begin
  insert into private.cluster_orders (
    id,
    creation_timestamp,
    data
  )
  values (
    new.id,
    new.creation_timestamp,
    '{}'
  );
  return null;
end;
$$ language plpgsql;

create trigger create_empty_private_cluster_order after insert on cluster_orders
for each row execute function private.create_empty_cluster_order();

--
-- Private clusters.
---
create table private.clusters (
  id text not null primary key references clusters(id) on delete cascade,
  creation_timestamp timestamp with time zone not null default now(),
  deletion_timestamp timestamp with time zone not null default 'epoch',
  data jsonb not null
);

create function private.create_empty_cluster() returns trigger as $$
begin
  insert into private.clusters (
    id,
    creation_timestamp,
    data
  )
  values (
    new.id,
    new.creation_timestamp,
    '{}'
  );
  return null;
end;
$$ language plpgsql;

create trigger create_empty_private_cluster after insert on clusters
for each row execute function private.create_empty_cluster();

--
-- Private hubs.
--
create table private.hubs (
  id text not null primary key,
  creation_timestamp timestamp with time zone not null default now(),
  deletion_timestamp timestamp with time zone not null default 'epoch',
  data jsonb not null
);
