--
-- Copyright (c) 2026 Red Hat Inc.
--
-- Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
-- the License. You may obtain a copy of the License at
--
--   http://www.apache.org/licenses/LICENSE-2.0
--

-- Create the generic persistence tables for ServiceAccount objects.
create table service_accounts (
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

create table archived_service_accounts (
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

create table active_service_accounts (
  id text not null primary key references service_accounts (id) on delete cascade
);

create index service_accounts_by_creator on service_accounts (creator);
create index service_accounts_by_tenant on service_accounts (tenant);
create index service_accounts_by_label on service_accounts using gin (labels);
create unique index service_accounts_unique_name_per_tenant
  on service_accounts (tenant, name);

alter table service_accounts
  add constraint service_accounts_tenant_fk
  foreign key (tenant) references tenants (name);

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on service_accounts
  for each row execute function materialize_active_objects();

alter table service_accounts add constraint service_accounts_no_project check (project = ''::ltree);

create unique index service_accounts_by_client_id
  on service_accounts ((data -> 'status' ->> 'client_id'))
  where data -> 'status' ->> 'client_id' != '';

create trigger check_immutable_columns
  before update on service_accounts
  for each row execute function check_immutable_columns('id', 'name', 'tenant', 'project');

create trigger enqueue_change
  after insert or update or delete on service_accounts
  for each row execute function enqueue_change();

insert into roles (id, name, tenant, data) values
(
  'cd33ed2b-5a5f-4b0a-adcb-5a0a567fa511',
  'tenant-automation',
  'shared',
  '{"spec":{"title":"Tenant automation","description":"Tenant-scoped automation permissions. For service accounts only."},"status":{"state":"ROLE_STATE_READY"}}'
),
(
  '6eafb57d-4344-43fc-86eb-8ce17749f5ec',
  'osac-csi-driver',
  'shared',
  '{"spec":{"title":"OSAC CSI driver","description":"Tenant-scoped volume lifecycle permissions. For service accounts only."},"status":{"state":"ROLE_STATE_READY"}}'
);
