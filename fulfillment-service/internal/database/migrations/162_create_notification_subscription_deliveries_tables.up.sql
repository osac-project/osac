--
-- Copyright (c) 2026 Red Hat Inc.
--
-- Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
-- the License. You may obtain a copy of the License at
--
--   http://www.apache.org/licenses/LICENSE-2.0
--

-- Webhook delivery history for subscription matches.

create table notification_subscription_deliveries (
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

create table archived_notification_subscription_deliveries (
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

create table active_notification_subscription_deliveries (
  id text not null primary key references notification_subscription_deliveries (id) on delete cascade
);

create index notification_subscription_deliveries_by_creator on notification_subscription_deliveries (creator);
create index notification_subscription_deliveries_by_tenant on notification_subscription_deliveries (tenant);
create index notification_subscription_deliveries_by_label on notification_subscription_deliveries using gin (labels);

create unique index notification_subscription_deliveries_unique_name_per_tenant
  on notification_subscription_deliveries (tenant, name)
  where deletion_timestamp = 'epoch'
    and name <> '';

-- One delivery per (tenant, subscription, CloudEvent identity).
create unique index notification_subscription_deliveries_unique_match
  on notification_subscription_deliveries (
    tenant,
    (data #>> '{spec,subscription,id}'),
    (data #>> '{spec,event,source}'),
    (data #>> '{spec,event,id}')
  )
  where deletion_timestamp = 'epoch'
    and coalesce(data #>> '{spec,subscription,id}', '') <> ''
    and coalesce(data #>> '{spec,event,source}', '') <> ''
    and coalesce(data #>> '{spec,event,id}', '') <> '';

create index notification_subscription_deliveries_by_subscription
  on notification_subscription_deliveries ((data #>> '{spec,subscription,id}'))
  where deletion_timestamp = 'epoch';

create index notification_subscription_deliveries_pending_by_next_attempt
  on notification_subscription_deliveries ((data #>> '{status,next_attempt_time}'))
  where deletion_timestamp = 'epoch'
    and coalesce(data #>> '{status,state}', '') = 'NOTIFICATION_SUBSCRIPTION_DELIVERY_STATE_PENDING';

alter table notification_subscription_deliveries
  add constraint notification_subscription_deliveries_tenant_fk
  foreign key (tenant) references tenants (name);

alter table notification_subscription_deliveries
  add constraint notification_subscription_deliveries_no_project check (project = ''::ltree);

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on notification_subscription_deliveries
  for each row execute function materialize_active_objects();

create trigger check_immutable_columns
  before update on notification_subscription_deliveries
  for each row execute function check_immutable_columns('id', 'name', 'tenant', 'project');

create trigger check_immutable_data_fields
  before update on notification_subscription_deliveries
  for each row
  execute function check_immutable_data_fields(
    'spec.subscription',
    'spec.event'
  );

create trigger enqueue_change
  after insert or update or delete on notification_subscription_deliveries
  for each row execute function enqueue_change();
