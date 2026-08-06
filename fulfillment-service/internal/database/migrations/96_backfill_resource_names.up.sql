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

-- Backfill empty and non-RFC-1123 name columns with generated or normalized DNS labels and resolve duplicate name
-- tuples across all resource tables. This prepares the database for unique name constraints added by a subsequent
-- migration.
--
-- Down migration: No-op — the cleanup is not reversible (original empty/duplicate names are not preserved).

create function normalize_dns_name(raw text) returns text language sql immutable as $$
  select regexp_replace(
    left(
      regexp_replace(
        regexp_replace(
          regexp_replace(lower(raw), '[^a-z0-9-]', '-', 'g'),
          '-{2,}', '-', 'g'
        ),
        '^-+|-+$', '', 'g'
      ),
      63
    ),
    '-+$', ''
  )
$$;

create procedure backfill_resource_names() language plpgsql as $$
declare
  tbl text;
  singular text;
  backfilled bigint;
  deduped bigint;
  has_tenant boolean;
  has_project boolean;
  has_deletion_timestamp boolean;
  has_data boolean;
  has_immutable_trigger boolean;
  scope_cols text;
  scope_match text;
  deletion_filter text;
  collision_deletion_filter text;
  dup_rec record;
  suffix_num int;
  base_name text;
  candidate_name text;
  max_base_len int;
  collision boolean;
  norm_rec record;
  has_preexisting_name_constraint boolean;
  norm_scope_match text;
  norm_collision_deletion_filter text;
  normalized_name text;
begin
  for tbl in
    select
      c.table_name
    from
      information_schema.columns c
    join
      information_schema.tables tb on
        tb.table_schema = c.table_schema and
        tb.table_name = c.table_name
    where
      c.table_schema = 'public' and
      c.column_name = 'name' and
      tb.table_type = 'BASE TABLE' and
      c.table_name not in (
        'schema_migrations',
        'notifications',
        'tenant_domains',
        'project_membership_subjects',
        'storage_tier_backends',
        'projects',
        'tenants'
      ) and
      c.table_name not like 'archived_%'
    order by
      c.table_name
  loop
    -- Derive the singular, hyphenated resource type name from the table name.
    singular := case tbl
      when 'bare_metal_instance_templates' then 'bare-metal-instance-template'
      when 'bare_metal_instance_types' then 'bare-metal-instance-type'
      when 'bare_metal_instances' then 'bare-metal-instance'
      when 'cluster_catalog_items' then 'cluster-catalog-item'
      when 'cluster_templates' then 'cluster-template'
      when 'cluster_versions' then 'cluster-version'
      when 'clusters' then 'cluster'
      when 'compute_instance_catalog_items' then 'compute-instance-catalog-item'
      when 'compute_instance_templates' then 'compute-instance-template'
      when 'compute_instances' then 'compute-instance'
      when 'external_ip_attachments' then 'external-ip-attachment'
      when 'external_ip_pools' then 'external-ip-pool'
      when 'external_ips' then 'external-ip'
      when 'host_pools' then 'host-pool'
      when 'host_types' then 'host-type'
      when 'hosts' then 'host'
      when 'hubs' then 'hub'
      when 'identity_providers' then 'identity-provider'
      when 'instance_types' then 'instance-type'
      when 'nat_gateways' then 'nat-gateway'
      when 'network_classes' then 'network-class'
      when 'objects' then 'object'
      when 'project_memberships' then 'project-membership'
      when 'role_bindings' then 'role-binding'
      when 'roles' then 'role'
      when 'secrets' then 'secret'
      when 'security_groups' then 'security-group'
      when 'storage_backends' then 'storage-backend'
      when 'storage_tiers' then 'storage-tier'
      when 'subnets' then 'subnet'
      when 'users' then 'user'
      when 'virtual_networks' then 'virtual-network'
      else replace(regexp_replace(tbl, 's$', ''), '_', '-')
    end;

    -- Detect which columns and triggers exist for this table.
    select exists(
      select 1 from information_schema.columns
      where table_schema = 'public' and table_name = tbl and column_name = 'tenant'
    ) into has_tenant;

    select exists(
      select 1 from information_schema.columns
      where table_schema = 'public' and table_name = tbl and column_name = 'project'
    ) into has_project;

    select exists(
      select 1 from information_schema.columns
      where table_schema = 'public' and table_name = tbl and column_name = 'deletion_timestamp'
    ) into has_deletion_timestamp;

    select exists(
      select 1 from information_schema.columns
      where table_schema = 'public' and table_name = tbl and column_name = 'data'
    ) into has_data;

    select exists(
      select 1
      from pg_trigger t
      join pg_class c on c.oid = t.tgrelid
      where c.relname = tbl and t.tgname = 'check_immutable_columns'
    ) into has_immutable_trigger;

    -- Temporarily disable the immutable column trigger so that name updates are allowed.
    if has_immutable_trigger then
      execute format('alter table %I disable trigger check_immutable_columns', tbl);
    end if;

    -- Determine whether this table has a pre-existing unique constraint on name whose scope
    -- is broader than the auto-detected dedup scope or that covers soft-deleted rows. Such
    -- tables need row-by-row normalization to avoid unique constraint violations when
    -- different non-compliant names normalize to the same value.
    has_preexisting_name_constraint := false;
    norm_scope_match := '';
    norm_collision_deletion_filter := '';
    if tbl in ('storage_backends', 'storage_tiers', 'bare_metal_instance_types') then
      -- Global UNIQUE(name): collision checks must ignore tenant/project and cover all rows.
      has_preexisting_name_constraint := true;
    elsif tbl = 'identity_providers' then
      -- UNIQUE(tenant, name) covers soft-deleted rows: scope by tenant but no deletion filter.
      has_preexisting_name_constraint := true;
      norm_scope_match := format(' and c.tenant = (select tenant from %I where id = $1)', tbl);
    end if;

    -- Step 1: Backfill empty names with {singular}-{id-prefix} and normalize non-RFC-1123 names.
    backfilled := 0;
    if has_preexisting_name_constraint then
      -- Row-by-row normalization with collision detection for tables whose pre-existing
      -- unique constraint could be violated by a bulk UPDATE (e.g. two rows in different
      -- tenants whose names normalize to the same value under a global UNIQUE(name), or an
      -- active row normalizing to a name held by a soft-deleted row).
      for norm_rec in execute format(
        'select id, name from %I '
        'where name = '''' or name !~ ''^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$''',
        tbl
      )
      loop
        if norm_rec.name = '' or normalize_dns_name(norm_rec.name) = '' then
          normalized_name := singular || '-' || coalesce(
            nullif(regexp_replace(lower(left(norm_rec.id, 8)), '[^a-z0-9]', '', 'g'), ''),
            'generated'
          );
        else
          normalized_name := normalize_dns_name(norm_rec.name);
        end if;

        execute format(
          'select exists(select 1 from %I c where c.name = $2 and c.id != $1%s%s)',
          tbl, norm_scope_match, norm_collision_deletion_filter
        ) using norm_rec.id, normalized_name into collision;

        if not collision then
          candidate_name := normalized_name;
        else
          suffix_num := 2;
          loop
            max_base_len := 63 - 1 - length(suffix_num::text);
            base_name := left(normalized_name, max_base_len);
            base_name := regexp_replace(base_name, '-+$', '');
            if base_name = '' then base_name := 'r'; end if;
            candidate_name := base_name || '-' || suffix_num::text;

            execute format(
              'select exists(select 1 from %I c where c.name = $2 and c.id != $1%s%s)',
              tbl, norm_scope_match, norm_collision_deletion_filter
            ) using norm_rec.id, candidate_name into collision;

            if not collision then exit; end if;
            suffix_num := suffix_num + 1;
          end loop;
        end if;

        execute format('update %I set name = $2 where id = $1', tbl)
        using norm_rec.id, candidate_name;
        backfilled := backfilled + 1;
      end loop;
    else
      -- Bulk normalization is safe for tables without pre-existing name constraints.
      execute format(
        'update %1$I set name = case '
        '  when name = '''' or normalize_dns_name(name) = '''' then '
        '    %2$L || ''-'' || coalesce('
        '      nullif(regexp_replace(lower(left(id, 8)), ''[^a-z0-9]'', '''', ''g''), ''''),'
        '      ''generated'''
        '    ) '
        '  else normalize_dns_name(name) '
        'end '
        'where name = '''' or name !~ ''^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$''',
        tbl, singular
      );
      get diagnostics backfilled = row_count;
    end if;
    if backfilled > 0 then
      raise notice 'backfill_resource_names: backfilled % names in %', backfilled, tbl;
    end if;

    -- Step 2: Deduplicate names within the table's uniqueness scope. The oldest row (by creation_timestamp, then id
    -- as tiebreaker) keeps its name; all others get a collision-free, DNS-bounded -{n} suffix.
    if tbl in ('roles', 'role_bindings') then
      scope_cols := 'name';
    elsif tbl = 'users' then
      scope_cols := 'tenant, name';
    elsif has_tenant and has_project then
      scope_cols := 'tenant, project, name';
    elsif has_tenant then
      scope_cols := 'tenant, name';
    else
      scope_cols := 'name';
    end if;

    if has_deletion_timestamp then
      deletion_filter := ' and deletion_timestamp = ''epoch''';
    else
      deletion_filter := '';
    end if;

    -- Build scope matching clause for collision checking against the active uniqueness scope.
    if tbl in ('roles', 'role_bindings') then
      scope_match := '';
    elsif tbl = 'users' then
      scope_match := format(' and c.tenant = (select tenant from %I where id = $1)', tbl);
    elsif has_tenant and has_project then
      scope_match := format(
        ' and c.tenant = (select tenant from %I where id = $1)'
        ' and c.project = (select project from %I where id = $1)',
        tbl, tbl
      );
    elsif has_tenant then
      scope_match := format(' and c.tenant = (select tenant from %I where id = $1)', tbl);
    else
      scope_match := '';
    end if;

    if has_deletion_timestamp then
      collision_deletion_filter := ' and c.deletion_timestamp = ''epoch''';
    else
      collision_deletion_filter := '';
    end if;

    deduped := 0;
    for dup_rec in execute format(
      'select id, name from ('
      '  select id, name, row_number() over (partition by %s order by creation_timestamp, id) as rn '
      '  from %I where true%s'
      ') sub where sub.rn > 1 order by name, rn',
      scope_cols, tbl, deletion_filter
    )
    loop
      suffix_num := 2;
      loop
        max_base_len := 63 - 1 - length(suffix_num::text);
        base_name := left(dup_rec.name, max_base_len);
        base_name := regexp_replace(base_name, '-+$', '');
        if base_name = '' then
          base_name := 'r';
        end if;
        candidate_name := base_name || '-' || suffix_num::text;

        execute format(
          'select exists(select 1 from %I c where c.name = $2 and c.id != $1%s%s)',
          tbl, scope_match, collision_deletion_filter
        ) using dup_rec.id, candidate_name into collision;

        if not collision then
          execute format('update %I set name = $2 where id = $1', tbl)
          using dup_rec.id, candidate_name;
          deduped := deduped + 1;
          exit;
        end if;

        suffix_num := suffix_num + 1;
      end loop;
    end loop;

    if deduped > 0 then
      raise notice 'backfill_resource_names: deduplicated % rows in %', deduped, tbl;
    end if;

    -- Step 3: Sync the data column's metadata.name with the name column for any rows that were modified.
    if has_data and (backfilled > 0 or deduped > 0) then
      execute format(
        'update %I set data = jsonb_set('
        '  jsonb_set(data, ''{metadata}'', coalesce(data->''metadata'', ''{}''::jsonb)),'
        '  ''{metadata,name}'','
        '  to_jsonb(name)'
        ') where name != '''' and ('
        '  data->''metadata'' is null'
        '  or data->''metadata''->''name'' is null'
        '  or data->''metadata''->''name'' is distinct from to_jsonb(name)'
        ')',
        tbl
      );
    end if;

    -- Re-enable the immutable column trigger.
    if has_immutable_trigger then
      execute format('alter table %I enable trigger check_immutable_columns', tbl);
    end if;
  end loop;
end;
$$;

call backfill_resource_names();

drop procedure backfill_resource_names();

drop function normalize_dns_name(text);
