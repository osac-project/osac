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
	"fmt"
	"iter"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/osac-project/osac/fulfillment-service/internal/authz"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// GrantStore reads authorization grants using the transaction in the context. These internal
// queries use an explicit verified tenant, independent of API object/project visibility.
type GrantStore struct{}

var _ authz.Store = GrantStore{}

// Base query for users or service accounts
const grantColumns = `
 select b.id, r.id, r.name, r.tenant, r.data
 from role_bindings b
 join roles r on r.id = b.data->'spec'->'role'->>'id'
  and r.tenant in ($1, 'shared') and r.deletion_timestamp = 'epoch'
 where b.tenant = $1 and b.deletion_timestamp = 'epoch'
`

// Match canonical User IDs through the subject GIN index. The live User lookup preserves
// revocation; the semijoin prevents duplicate grants from repeated references or User rows.
const userGrantsQuery = grantColumns + `
 and b.id in (
  select candidate.id
  from users u
  join role_bindings candidate
   on candidate.data->'spec'->'users' @> jsonb_build_array(jsonb_build_object('id', u.id))
  where u.id <> '' and u.tenant = $1 and u.data->'spec'->>'username' = $2
   and u.deletion_timestamp = 'epoch'
   and candidate.tenant = $1 and candidate.deletion_timestamp = 'epoch'
 )
 order by b.id
`

const serviceAccountGrantsQuery = grantColumns + `
 and b.data->'spec'->'service_accounts' @> jsonb_build_array(jsonb_build_object('id', $2::text))
 order by b.id
`

// ListUserGrants streams grants matching live User IDs in tenant with the supplied username.
func (s GrantStore) ListUserGrants(ctx context.Context, tenant, username string) iter.Seq2[authz.BindingGrant, error] {
	return s.listGrants(ctx, userGrantsQuery, tenant, username)
}

// ListServiceAccountGrants streams grants matching the authenticated service-account ID in tenant.
func (s GrantStore) ListServiceAccountGrants(ctx context.Context, tenant, accountID string) iter.Seq2[authz.BindingGrant, error] {
	return s.listGrants(ctx, serviceAccountGrantsQuery, tenant, accountID)
}

func (s GrantStore) listGrants(ctx context.Context, query, tenant, subject string) iter.Seq2[authz.BindingGrant, error] {
	return func(yield func(authz.BindingGrant, error) bool) {
		tx, err := database.TxFromContext(ctx)
		if err != nil {
			yield(authz.BindingGrant{}, err)
			return
		}
		defer tx.ReportError(&err)
		rows, err := tx.Query(ctx, query, tenant, subject)
		if err != nil {
			yield(authz.BindingGrant{}, err)
			return
		}
		defer rows.Close()
		for rows.Next() {
			var grant authz.BindingGrant
			var data []byte
			err = rows.Scan(&grant.BindingID, &grant.RoleID,
				&grant.RoleName, &grant.RoleTenant, &data)
			if err != nil {
				yield(authz.BindingGrant{}, err)
				return
			}
			role := &privatev1.Role{}
			err = (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(data, role)
			if err != nil {
				err = fmt.Errorf("decode Role %q: %w", grant.RoleID, err)
				yield(authz.BindingGrant{}, err)
				return
			}
			grant.Rules = role.GetSpec().GetPermissions()
			if !yield(grant, nil) {
				return
			}
		}
		err = rows.Err()
		if err != nil {
			yield(authz.BindingGrant{}, err)
		}
	}
}
