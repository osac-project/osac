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

-- Create tables for DiskImage resources.
-- DiskImage is tenant-scoped: Cloud Provider Admins create globally visible images (tenant='shared'),
-- Tenant Admins and Tenant Users create tenant-scoped images.

create table disk_images (
  id text not null primary key,
  name text not null check (name <> ''),
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

create table archived_disk_images (
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

create index disk_images_by_creator on disk_images (creator);
create index disk_images_by_tenant on disk_images (tenant);
create index disk_images_by_label on disk_images using gin (labels);

-- Name uniqueness per tenant, active records only.
create unique index disk_images_unique_name_per_tenant
  on disk_images (name, tenant)
  where deletion_timestamp = 'epoch'
    and name != '';

-- Tenant foreign key referencing the tenants table.
alter table disk_images
  add constraint disk_images_tenant_fk
  foreign key (tenant) references tenants (name);

-- Project foreign key referencing the projects table.
alter table disk_images
  add constraint disk_images_project_fk
  foreign key (tenant, project) references projects (tenant, name);

-- Enforce immutability of id, name, tenant, and project columns at the database level.
create trigger check_immutable_columns
  before update on disk_images
  for each row
  execute function check_immutable_columns('id', 'name', 'tenant', 'project');

-- Z0003 reverse-reference trigger: prevent soft-delete of a DiskImage that is still referenced
-- by active compute_instances, compute_instance_templates, or compute_instance_catalog_items.
create or replace function check_disk_image_not_in_use() returns trigger as $$
begin
  if exists (
    select 1
    from compute_instances
    where deletion_timestamp = 'epoch'
      and data->'spec'->'disk_image'->>'id' = old.id
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete disk image ''%s'': it is in use by at least one compute instance',
        old.id
      );
  end if;

  if exists (
    select 1
    from compute_instance_templates
    where deletion_timestamp = 'epoch'
      and data->'spec_defaults'->'disk_image'->>'id' = old.id
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete disk image ''%s'': it is in use by at least one compute instance template',
        old.id
      );
  end if;

  if exists (
    select 1
    from compute_instance_catalog_items,
         jsonb_array_elements(data->'spec'->'field_definitions') as fd
    where deletion_timestamp = 'epoch'
      and fd->>'path' = 'spec.disk_image'
      and fd->>'default' = old.id
  ) then
    raise exception using
      errcode = 'Z0003',
      message = format(
        'cannot delete disk image ''%s'': it is in use by at least one compute instance catalog item',
        old.id
      );
  end if;

  return new;
end;
$$ language plpgsql;

create trigger check_disk_image_not_in_use
  before update on disk_images
  for each row
  when (old.deletion_timestamp = 'epoch' and new.deletion_timestamp != 'epoch')
  execute function check_disk_image_not_in_use();

-- JSONB indexes on referencing tables for disk_image lookups.
create index compute_instances_disk_image
  on compute_instances ((data->'spec'->'disk_image'->>'id'))
  where deletion_timestamp = 'epoch';

create index compute_instance_templates_disk_image
  on compute_instance_templates ((data->'spec_defaults'->'disk_image'->>'id'))
  where deletion_timestamp = 'epoch';

-- Active companion table for disk_images (see migration 97 for the pattern).
create table active_disk_images (
  id text not null primary key references disk_images (id) on delete cascade
);

create trigger materialize_active_objects
  after insert or update of deletion_timestamp on disk_images
  for each row execute function materialize_active_objects();

insert into active_disk_images (id)
select id from disk_images where deletion_timestamp = 'epoch';
