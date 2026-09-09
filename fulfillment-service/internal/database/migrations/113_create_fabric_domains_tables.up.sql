--
-- Copyright (c) 2026 Red Hat Inc.
--
-- Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
-- the License. You may obtain a copy of the License at
--
--   http://www.apache.org/licenses/LICENSE-2.0
--

-- Create the generic persistence tables for FabricDomain objects.
create table fabric_domains (
  id text not null primary key,
  name text not null default '',
  creation_timestamp timestamp with time zone not null default now(),
  deletion_timestamp timestamp with time zone not null default 'epoch',
  finalizers text[] not null default '{}',
  creator text not null default '',
  tenant text not null default '',
  project ltree not null default ''::ltree,
  labels jsonb not null default '{}'::jsonb,
  annotations jsonb not null default '{}'::jsonb,
  data jsonb not null,
  version integer not null default 0
);

create table archived_fabric_domains (
  id text not null,
  name text not null default '',
  creation_timestamp timestamp with time zone not null,
  deletion_timestamp timestamp with time zone not null,
  archival_timestamp timestamp with time zone not null default now(),
  creator text not null default '',
  tenant text not null default '',
  project ltree not null default ''::ltree,
  labels jsonb not null default '{}'::jsonb,
  annotations jsonb not null default '{}'::jsonb,
  data jsonb not null,
  version integer not null default 0
);

create table active_fabric_domains (
  id text not null primary key references fabric_domains (id) on delete cascade
);

create index fabric_domains_by_creator on fabric_domains (creator);
create index fabric_domains_by_tenant on fabric_domains (tenant);
create index fabric_domains_by_label on fabric_domains using gin (labels);
create unique index fabric_domains_unique_name_per_tenant
  on fabric_domains (name, tenant)
  where deletion_timestamp = 'epoch' and name != '';

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on fabric_domains
  for each row execute function materialize_active_objects();
