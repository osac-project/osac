--
-- Copyright (c) 2026 Red Hat Inc.
--
-- Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
-- the License. You may obtain a copy of the License at
--
--   http://www.apache.org/licenses/LICENSE-2.0
--

-- Create the generic persistence tables for Notification objects (inbox items and announcements).

create table notifications (
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

create table archived_notifications (
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

create table active_notifications (
  id text not null primary key references notifications (id) on delete cascade
);

create index notifications_by_creator on notifications (creator);
create index notifications_by_tenant on notifications (tenant);
create index notifications_by_label on notifications using gin (labels);
create index notifications_by_recipient on notifications ((data #>> '{spec,recipient_id}'))
  where deletion_timestamp = 'epoch'
    and coalesce(data #>> '{spec,recipient_id}', '') <> '';

create unique index notifications_unique_name_per_tenant
  on notifications (tenant, name)
  where deletion_timestamp = 'epoch'
    and name <> '';

-- Event-driven inbox rows are unique on tenant + producer event + recipient.
create unique index notifications_unique_inbox_event
  on notifications (
    tenant,
    (data #>> '{spec,event,source}'),
    (data #>> '{spec,event,id}'),
    (data #>> '{spec,recipient_id}')
  )
  where deletion_timestamp = 'epoch'
    and coalesce(data #>> '{spec,event,id}', '') <> ''
    and coalesce(data #>> '{spec,event,source}', '') <> ''
    and coalesce(data #>> '{spec,recipient_id}', '') <> '';

alter table notifications
  add constraint notifications_tenant_fk
  foreign key (tenant) references tenants (name);

alter table notifications add constraint notifications_no_project check (project = ''::ltree);

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on notifications
  for each row execute function materialize_active_objects();

create trigger check_immutable_columns
  before update on notifications
  for each row execute function check_immutable_columns('id', 'name', 'tenant', 'project');

-- spec.state is intentionally omitted so inbox read/unread updates can persist.
create trigger check_immutable_data_fields
  before update on notifications
  for each row
  execute function check_immutable_data_fields(
    'spec.kind',
    'spec.severity',
    'spec.type',
    'spec.event',
    'spec.recipient_id',
    'spec.scope',
    'spec.title',
    'spec.description',
    'spec.resource',
    'spec.details'
  );

create trigger enqueue_change
  after insert or update or delete on notifications
  for each row execute function enqueue_change();
