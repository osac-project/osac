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

-- Create tables for ClusterVersion resources.

create table cluster_versions (
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

create table archived_cluster_versions (
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

create index cluster_versions_by_name on cluster_versions (name);
create index cluster_versions_by_creator on cluster_versions (creator);
create index cluster_versions_by_tenant on cluster_versions (tenant);
create index cluster_versions_by_label on cluster_versions using gin (labels);

alter table cluster_versions
  add constraint cluster_versions_tenant_fk
  foreign key (tenant) references tenants (name);

alter table cluster_versions
  add constraint cluster_versions_project_fk
  foreign key (tenant, project) references projects (tenant, name);

alter table cluster_versions
  add constraint cluster_versions_spec_version_chk
  check ((
    jsonb_typeof(data #> '{spec,version}') = 'string'
    and btrim(data #>> '{spec,version}') <> ''
  ) is true);

alter table cluster_versions
  add constraint cluster_versions_spec_image_chk
  check ((
    jsonb_typeof(data #> '{spec,image}') = 'string'
    and btrim(data #>> '{spec,image}') <> ''
  ) is true);

-- Enforce unique active metadata.name per tenant and project. The index name contains
-- '_unique_name_' so that the DAO translateError includes the Name field in ErrAlreadyExists.
create unique index cluster_versions_unique_name_active
  on cluster_versions (name, tenant, project)
  where deletion_timestamp = 'epoch';

-- Enforce unique active spec.version per tenant and project.
create unique index cluster_versions_unique_spec_version
  on cluster_versions ((data -> 'spec' ->> 'version'), tenant, project)
  where deletion_timestamp = 'epoch';

-- If present, spec.is_default must be a JSON boolean.
alter table cluster_versions
  add constraint cluster_versions_spec_is_default_chk
  check (
    data #> '{spec,is_default}' is null
    or jsonb_typeof(data #> '{spec,is_default}') = 'boolean'
  );

-- At most one active default per tenant and project.
create unique index cluster_versions_single_default
  on cluster_versions (tenant, project)
  where data #> '{spec,is_default}' = 'true'::jsonb
    and deletion_timestamp = 'epoch';

-- Enforce immutability of id, tenant, and project columns at the database level.
create trigger check_immutable_columns
  before update on cluster_versions
  for each row
  execute function check_immutable_columns('id', 'tenant', 'project');

-- Reusable trigger function that enforces immutability of fields inside the JSONB data column.
-- Field paths are passed as trigger arguments in dot notation (e.g. 'spec.version').
-- Raises Z0001 with the same detail format so the existing Go error translation works.
create function check_immutable_data_fields() returns trigger as $$
declare
  i integer;
  path text;
  changed text[];
begin
  changed := '{}';
  for i in 0..tg_nargs - 1 loop
    path := tg_argv[i];
    if (old.data #> string_to_array(path, '.'))
       is distinct from
       (new.data #> string_to_array(path, '.')) then
      changed := array_append(changed, path);
    end if;
  end loop;

  if array_length(changed, 1) > 0 then
    raise exception using
      errcode = 'Z0001',
      message = format(
        'the %s %s of table ''%s'' %s immutable',
        array_to_string(changed, ', '),
        case when array_length(changed, 1) = 1 then 'field' else 'fields' end,
        tg_table_name,
        case when array_length(changed, 1) = 1 then 'is' else 'are' end
      ),
      detail = to_json(changed)::text;
  end if;

  return new;
end;
$$ language plpgsql;

create trigger check_immutable_data_fields
  before update on cluster_versions
  for each row
  execute function check_immutable_data_fields('spec.version', 'spec.image');
