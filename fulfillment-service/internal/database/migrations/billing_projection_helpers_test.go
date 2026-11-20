/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package migrations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

// invalidBillingTimestamps enumerates protobuf timestamp spellings that PostgreSQL would otherwise accept and
// normalize, or misparse, instead of rejecting outright.
var invalidBillingTimestamps = []string{
	"2026-02-01 00:00:00.000002Z",
	"2026-02-01t00:00:00.000002z",
	"2026-02-01T00:00:00.000002+00:00",
	"2026-02-01T00:00:00.000002",
	"2026-02-01T00:00:00.000002Z\n",
	"2026-02-01T00:00:00.0000020Z",
	"2026-01-31T24:00:00Z",
	"2026-01-31T23:59:60Z",
	"2026-02-29T00:00:00Z",
	"0000-01-01T00:00:00Z",
	"10000-01-01T00:00:00Z",
	"infinity",
}

// billingAccountName mirrors the SQL derivation of a billing account name from its immutable id.
func billingAccountName(id string) string {
	sum := sha256.Sum256([]byte(id))
	return "ba-" + hex.EncodeToString(sum[:])[:60]
}

// billingAccountData builds a valid account payload. Passing a token sets the attribution id.
func billingAccountData(id string, token *string) string {
	status := map[string]any{
		"provider_key": "m360", "provider_account_reference": "native-" + id,
		"display_name": "New label", "base_currency": "USD", "state": "BILLING_ACCOUNT_STATE_ACTIVE",
		"state_observation_timestamp": "2026-03-01T01:02:03.000004Z",
	}
	if token != nil {
		status["external_billing_account_id"] = *token
	}
	data, err := json.Marshal(map[string]any{"spec": map[string]any{}, "status": status})
	Expect(err).ToNot(HaveOccurred())
	return string(data)
}

// billingAssignmentData builds a valid assignment payload for the given interval end, if any.
func billingAssignmentData(tenant, account, end string) string {
	status := map[string]any{"connection_state": "TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_PENDING",
		"effective_from_timestamp": "2026-03-01T00:00:00.000005Z", "message": "pending"}
	if end != "" {
		status["effective_to_timestamp"] = end
	}
	data, err := json.Marshal(map[string]any{"spec": map[string]any{
		"tenant":          map[string]string{"id": tenant, "name": tenant},
		"billing_account": map[string]string{"id": account, "name": billingAccountName(account)},
	}, "status": status})
	Expect(err).ToNot(HaveOccurred())
	return string(data)
}

// rejectStatement runs a statement in a real savepoint, asserts the PostgreSQL rejection and leaves the outer
// transaction usable.
func rejectStatement(ctx context.Context, code, constraint, query string, args ...any) {
	GinkgoHelper()
	tx, err := conn.Begin(ctx)
	Expect(err).ToNot(HaveOccurred())
	defer func() { _ = tx.Rollback(ctx) }()
	sp, err := tx.Begin(ctx)
	Expect(err).ToNot(HaveOccurred())
	_, err = sp.Exec(ctx, query, args...)
	var pgErr *pgconn.PgError
	Expect(errors.As(err, &pgErr)).To(BeTrue(), "expected PostgreSQL rejection: %v", err)
	Expect(pgErr.Code).To(Equal(code))
	if constraint != "" {
		Expect(pgErr.ConstraintName).To(Equal(constraint))
	}
	Expect(sp.Rollback(ctx)).To(Succeed())
	var one int
	Expect(tx.QueryRow(ctx, `select 1`).Scan(&one)).To(Succeed())
	Expect(tx.Commit(ctx)).To(Succeed())
}

// Billing inserts deliberately write the standard DAO column set, not the projected business columns. Full
// protobuf/GenericDAO round trips are owned by the integrating DAO suite.
const insertBillingAccount = `insert into billing_accounts (id, name, tenant, creator, data)
	values ($1, $2, 'system', 'system', $3::jsonb)`

const insertBillingAssignment = `insert into tenant_billing_assignments
	(id, name, tenant, creator, annotations, data)
	values ($1, 'same-name', $2, 'request-principal',
		jsonb_build_object('osac.openshift.io/tenant', $2::text), $3::jsonb)`
