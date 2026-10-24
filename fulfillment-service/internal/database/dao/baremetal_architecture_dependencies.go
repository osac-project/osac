/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package dao

import (
	"context"
	"strings"

	"github.com/osac-project/osac/fulfillment-service/internal/database"
)

// BareMetalReferencesDiskImageArch reports whether an active BareMetalInstance or
// BareMetalInstanceCatalogItem references the DiskImage and requires any of the given
// architectures. It compares the CPU architecture of the effective BareMetalInstanceType,
// including locked/default catalog types and template defaults. The query covers all
// tenants without returning dependent identities. The caller must hold the DiskImage
// lock until the transaction ends.
func BareMetalReferencesDiskImageArch(ctx context.Context, imageID string, architectures []string) (inUse bool, err error) {
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return
	}
	defer tx.ReportError(&err)
	err = tx.QueryRow(ctx, `
		select exists (
			select 1
			from bare_metal_instances i
			join bare_metal_instance_types t on t.id = i.data->'spec'->'instance_type'->>'id'
			where i.deletion_timestamp = 'epoch'
			  and i.data->'spec'->'disk_image'->>'id' = $1
			  and t.data->'spec'->'hardware'->'cpu'->>'architecture' = any($2::text[])
		) or exists (
			select 1
			from bare_metal_instance_catalog_items c
			left join bare_metal_instance_templates template on template.id = c.data->'template'->>'id'
			join bare_metal_instance_types t on t.id = case
				when c.data->'fields'->'instance_type' is not null then coalesce(
					c.data->'fields'->'instance_type'->'locked'->>'id',
					c.data->'fields'->'instance_type'->'editable'->'default_value'->>'id'
				)
				else template.data->'instance_type'->>'id'
			end
			where c.deletion_timestamp = 'epoch'
			  and coalesce(
				c.data->'fields'->'disk_image'->'locked'->>'id',
				c.data->'fields'->'disk_image'->'editable'->'default_value'->>'id'
			  ) = $1
			  and t.data->'spec'->'hardware'->'cpu'->>'architecture' = any($2::text[])
		)
	`, imageID, architectures).Scan(&inUse)
	return
}

// BareMetalTemplateArchitectureConflicts reports whether an active BareMetalInstanceCatalogItem
// that inherits the template's BareMetalInstanceType selects a locked or default DiskImage
// that does not support the candidate type's CPU architecture. It checks all tenants and
// holds image locks until the transaction ends. The caller must already hold the template
// and candidate BareMetalInstanceType locks.
func BareMetalTemplateArchitectureConflicts(ctx context.Context, templateID, architecture string) (conflicts bool, err error) {
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return
	}
	defer tx.ReportError(&err)

	// Lock first, then read the architectures in a new statement so a concurrent image
	// update that held a lock is visible after it commits.
	_, err = tx.Exec(ctx, `
		select i.id from disk_images i
		where exists (
			select 1 from bare_metal_instance_catalog_items c
			where c.deletion_timestamp = 'epoch'
			  and c.data->'template'->>'id' = $1
			  and c.data->'fields'->'instance_type' is null
			  and coalesce(
				c.data->'fields'->'disk_image'->'locked'->>'id',
				c.data->'fields'->'disk_image'->'editable'->'default_value'->>'id'
			  ) = i.id
		)
		order by i.id for update
	`, templateID)
	if err != nil {
		return
	}
	err = tx.QueryRow(ctx, `
		select exists (
			select 1 from bare_metal_instance_catalog_items c
			join disk_images i on i.id = coalesce(
				c.data->'fields'->'disk_image'->'locked'->>'id',
				c.data->'fields'->'disk_image'->'editable'->'default_value'->>'id'
			)
			where c.deletion_timestamp = 'epoch'
			  and c.data->'template'->>'id' = $1
			  and c.data->'fields'->'instance_type' is null
			  and not (coalesce(i.data->'spec'->'architecture', '[]'::jsonb) ? $2)
		)
	`, templateID, "ARCHITECTURE_"+strings.ToUpper(architecture)).Scan(&conflicts)
	return
}
