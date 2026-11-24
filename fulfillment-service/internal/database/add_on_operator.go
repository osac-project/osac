/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package database

import (
	"context"
	"fmt"
)

// HasPublishedAddOnOperatorDependency reports whether a published shared
// operator directly references the specified operator in its serialized data.
func HasPublishedAddOnOperatorDependency(
	ctx context.Context, tx Tx, operatorID, operatorName string,
) (exists bool, err error) {
	err = tx.QueryRow(ctx, `
		select exists (
			select 1
			from add_on_operators
			where deletion_timestamp = 'epoch'
			  and tenant = 'shared'
			  and coalesce(project, '') = ''
			  and coalesce(data->>'published', 'false') = 'true'
			  and id <> $1
			  and (
				data->'dependencies' @> jsonb_build_array(jsonb_build_object('id', $1::text))
				or data->'dependencies' @> jsonb_build_array(jsonb_build_object('name', $2::text))
			  )
		)`, operatorID, operatorName).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check add-on operator dependents: %w", err)
	}
	return exists, nil
}

// LockAddOnOperatorDependencyGraph serializes add-on operator graph mutations
// for the lifetime of the supplied transaction.
func LockAddOnOperatorDependencyGraph(ctx context.Context, tx Tx) error {
	if _, err := tx.Exec(ctx, `
		select pg_advisory_xact_lock(hashtextextended($1, 0))`,
		"osac.add_on_operator_dependency_graph"); err != nil {
		return fmt.Errorf("lock add-on operator dependency graph: %w", err)
	}
	return nil
}
