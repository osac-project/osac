--
-- Copyright (c) 2026 Red Hat Inc.
--
-- Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
-- the License. You may obtain a copy of the License at
--
--   http://www.apache.org/licenses/LICENSE-2.0
--

-- Tenant Admin webhook subscriptions.

create table notification_subscriptions (
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

create table archived_notification_subscriptions (
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

create table active_notification_subscriptions (
  id text not null primary key references notification_subscriptions (id) on delete cascade
);

create index notification_subscriptions_by_creator on notification_subscriptions (creator);
create index notification_subscriptions_by_tenant on notification_subscriptions (tenant);
create index notification_subscriptions_by_label on notification_subscriptions using gin (labels);

create unique index notification_subscriptions_unique_name_per_tenant
  on notification_subscriptions (tenant, name)
  where deletion_timestamp = 'epoch'
    and name <> '';

alter table notification_subscriptions
  add constraint notification_subscriptions_tenant_fk
  foreign key (tenant) references tenants (name);

alter table notification_subscriptions
  add constraint notification_subscriptions_no_project check (project = ''::ltree);

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on notification_subscriptions
  for each row execute function materialize_active_objects();

create trigger check_immutable_columns
  before update on notification_subscriptions
  for each row execute function check_immutable_columns('id', 'name', 'tenant', 'project');

-- Spec (webhook_url, filter, paused) and status.last_delivery_status / auto_paused
-- are mutable via Update. status.signing_secret is set once on Create.
create trigger check_immutable_data_fields
  before update on notification_subscriptions
  for each row
  execute function check_immutable_data_fields(
    'status.signing_secret'
  );

create trigger enqueue_change
  after insert or update or delete on notification_subscriptions
  for each row execute function enqueue_change();
