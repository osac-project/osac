--
-- Copyright (c) 2026 Red Hat, Inc.
--
-- Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
-- the License. You may obtain a copy of the License at
--
--   http://www.apache.org/licenses/LICENSE-2.0
--
-- Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
-- "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
-- language governing permissions and limitations under the License.
--

-- AddOnOperator references in active ClusterCatalogItems are strong
-- dependencies. Published operator dependency protection is handled by the
-- add-on operator graph validation and lifecycle checks.

create or replace function check_add_on_operator_catalog_item_not_in_use() returns trigger as $$
declare
  catalog_item_in_use boolean;
  operation text;
begin
  -- Only soft-deletion and unpublishing require lifecycle protection.
  if not (
    (old.deletion_timestamp = 'epoch' and new.deletion_timestamp <> 'epoch')
    or (
      coalesce(old.data->>'published', 'false') = 'true'
      and coalesce(new.data->>'published', 'false') <> 'true'
    )
  ) then
    return new;
  end if;
  operation := case when new.deletion_timestamp <> 'epoch' then 'delete' else 'unpublish' end;

  select exists (
    select 1
    from cluster_catalog_items catalog_item
    cross join lateral jsonb_array_elements(
      coalesce(catalog_item.data->'fields'->'add_on_operators'->'locked'->'items', '[]'::jsonb)
      || coalesce(catalog_item.data->'fields'->'add_on_operators'->'editable'->'default_value'->'items', '[]'::jsonb)
    ) reference
    where catalog_item.deletion_timestamp = 'epoch'
      and reference->>'id' = old.id
  )
  into catalog_item_in_use;

  if catalog_item_in_use then
    raise exception using
      errcode = 'Z0003',
      message = format('cannot %s add-on operator ''%s'': it is in use by an active catalog item', operation, old.name);
  end if;

  return new;
end;
$$ language plpgsql;

create trigger check_add_on_operator_catalog_item_not_in_use
  before update on add_on_operators
  for each row
  execute function check_add_on_operator_catalog_item_not_in_use();
