/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package serviceaccounts_test

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/serviceaccounts"
)

var _ = Describe("Service account authorization state", func() {
	It("resolves active tenant identity and current RoleBinding grants", func(ctx context.Context) {
		accountID := "d3c4e3db-95b8-481d-8b5c-963da477da12"
		clientID := serviceaccounts.ClientID("tenant-a", accountID)
		identityJSON, err := json.Marshal(map[string]any{
			"status": map[string]any{
				"client_secret":        map[string]string{"id": "credential-secret-id"},
				"state":                "SERVICE_ACCOUNT_STATE_READY",
				"keycloak_client_uuid": "00000000-0000-4000-8000-000000000001",
			},
		})
		Expect(err).ToNot(HaveOccurred())

		ctrl := gomock.NewController(GinkgoT())
		tx := database.NewMockTx(ctrl)
		tx.EXPECT().QueryRow(gomock.Any(), gomock.Any(), accountID, "tenant-a").Return(authorizationRow{
			values: []any{
				accountID,
				"tenant-a",
				[]byte(`{"osac.openshift.io/tenant":"tenant-a"}`),
				identityJSON,
			},
		})
		tx.EXPECT().Query(gomock.Any(), gomock.Any(), "tenant-a", accountID).Return(
			&authorizationRows{values: []string{"tenant-automation"}}, nil,
		)

		ctx = database.TxIntoContext(ctx, tx)
		identity, err := serviceaccounts.ResolveAuthorization(ctx, clientID, "tenant-a")
		Expect(err).ToNot(HaveOccurred())
		Expect(identity).ToNot(BeNil())
		Expect(identity.ID).To(Equal(accountID))
		Expect(identity.Tenant).To(Equal("tenant-a"))
		Expect(identity.ClientID).To(Equal(clientID))
		Expect(identity.Roles).To(Equal([]string{"tenant-automation"}))
	})

	It("fails closed when the tenant annotation does not match", func(ctx context.Context) {
		accountID := "d3c4e3db-95b8-481d-8b5c-963da477da12"
		clientID := serviceaccounts.ClientID("tenant-a", accountID)
		identityJSON := []byte(`{"status":{"client_secret":{"id":"credential-secret-id"},"state":"SERVICE_ACCOUNT_STATE_READY","keycloak_client_uuid":"00000000-0000-4000-8000-000000000001"}}`)

		ctrl := gomock.NewController(GinkgoT())
		tx := database.NewMockTx(ctrl)
		tx.EXPECT().QueryRow(gomock.Any(), gomock.Any(), accountID, "tenant-a").Return(authorizationRow{
			values: []any{
				accountID,
				"tenant-a",
				[]byte(`{"osac.openshift.io/tenant":"tenant-b"}`),
				identityJSON,
			},
		})

		ctx = database.TxIntoContext(ctx, tx)
		identity, err := serviceaccounts.ResolveAuthorization(ctx, clientID, "tenant-a")
		Expect(err).ToNot(HaveOccurred())
		Expect(identity).To(BeNil())
	})

	It("fails closed until the credential Secret is ready", func(ctx context.Context) {
		accountID := "d3c4e3db-95b8-481d-8b5c-963da477da12"
		clientID := serviceaccounts.ClientID("tenant-a", accountID)
		identityJSON := []byte(`{"status":{"keycloak_client_uuid":"00000000-0000-4000-8000-000000000001"}}`)

		ctrl := gomock.NewController(GinkgoT())
		tx := database.NewMockTx(ctrl)
		tx.EXPECT().QueryRow(gomock.Any(), gomock.Any(), accountID, "tenant-a").Return(authorizationRow{
			values: []any{
				accountID,
				"tenant-a",
				[]byte(`{"osac.openshift.io/tenant":"tenant-a"}`),
				identityJSON,
			},
		})

		ctx = database.TxIntoContext(ctx, tx)
		identity, err := serviceaccounts.ResolveAuthorization(ctx, clientID, "tenant-a")
		Expect(err).ToNot(HaveOccurred())
		Expect(identity).To(BeNil())
	})
})

type authorizationRow struct {
	values []any
	err    error
}

func (r authorizationRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return fmt.Errorf("expected %d scan destinations, got %d", len(r.values), len(dest))
	}
	for index, value := range r.values {
		switch target := dest[index].(type) {
		case *string:
			*target = value.(string)
		case *[]byte:
			*target = value.([]byte)
		default:
			return fmt.Errorf("unsupported scan destination %T", target)
		}
	}
	return nil
}

type authorizationRows struct {
	values []string
	next   int
}

func (r *authorizationRows) Close() {}

func (r *authorizationRows) Err() error { return nil }

func (r *authorizationRows) CommandTag() pgconn.CommandTag { return pgconn.CommandTag{} }

func (r *authorizationRows) FieldDescriptions() []pgconn.FieldDescription { return nil }

func (r *authorizationRows) Next() bool {
	if r.next >= len(r.values) {
		return false
	}
	r.next++
	return true
}

func (r *authorizationRows) Scan(dest ...any) error {
	if len(dest) != 1 {
		return fmt.Errorf("expected one scan destination, got %d", len(dest))
	}
	target, ok := dest[0].(*string)
	if !ok {
		return fmt.Errorf("unsupported scan destination %T", dest[0])
	}
	*target = r.values[r.next-1]
	return nil
}

func (r *authorizationRows) Values() ([]any, error) {
	if r.next == 0 || r.next > len(r.values) {
		return nil, fmt.Errorf("no current row")
	}
	return []any{r.values[r.next-1]}, nil
}

func (r *authorizationRows) RawValues() [][]byte { return nil }

func (r *authorizationRows) Conn() *pgx.Conn { return nil }

var _ pgx.Row = authorizationRow{}
var _ pgx.Rows = (*authorizationRows)(nil)
