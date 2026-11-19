/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package database

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = Describe("Billing history boundaries", func() {
	var ctx context.Context
	BeforeEach(func() {
		ctx = context.Background()
		db, err := server.NewInstance().Build()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(db.Close)
		pool, err := db.Pool(ctx)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(pool.Close)
		tm, err := NewTxManager().SetLogger(logger).SetPool(pool).Build()
		Expect(err).NotTo(HaveOccurred())
		tx, err := tm.Begin(ctx)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(tx.End(context.Background())).To(Succeed()) })
		ctx = TxIntoContext(ctx, tx)

		_, err = tx.Exec(ctx, `insert into tenants (id, name, tenant, creator, data)
			values ('billing-history', 'billing-history', 'billing-history', 'system', '{}')`)
		Expect(err).NotTo(HaveOccurred())
		_, err = tx.Exec(ctx, `insert into billing_accounts (id, name, tenant, data)
			values ('billing-account', 'ba-' || left(encode(sha256(convert_to('billing-account', 'UTF8')), 'hex'), 60),
			'system', '{"spec":{},"status":{"provider_key":"test","provider_account_reference":"native",
			"state":"BILLING_ACCOUNT_STATE_ACTIVE","state_observation_timestamp":"2026-01-01T00:00:00Z"}}')`)
		Expect(err).NotTo(HaveOccurred())
		for _, interval := range []struct{ id, from, to string }{
			{"first", "2026-01-01T00:00:00.000000Z", "2026-01-02T00:00:00.000000Z"},
			{"second", "2026-01-02T00:00:00.000000Z", "2026-01-03T00:00:00.000000Z"},
		} {
			_, err = tx.Exec(ctx, `insert into tenant_billing_assignments (id, name, tenant, annotations, data)
				values ($1, $1, 'billing-history', '{"osac.openshift.io/tenant":"billing-history"}',
				jsonb_build_object('spec', jsonb_build_object(
				  'tenant', jsonb_build_object('id','billing-history','name','billing-history'),
				  'billing_account', jsonb_build_object('id','billing-account','name',
				    'ba-' || left(encode(sha256(convert_to('billing-account', 'UTF8')), 'hex'), 60))),
				  'status', jsonb_build_object('connection_state',
				    'TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED',
				    'effective_from_timestamp',$2::text,'effective_to_timestamp',$3::text)))`,
				interval.id, interval.from, interval.to)
			Expect(err).NotTo(HaveOccurred())
		}
	})

	It("uses closed-only tenant and account history as the shared interval floor", func() {
		floor, err := TenantBillingHistoryFloor(ctx, "billing-history")
		Expect(err).NotTo(HaveOccurred())
		Expect(floor).To(BeTemporally("==", time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)))
		accountFloor, err := BillingAccountHistoryFloor(ctx, "billing-account")
		Expect(err).NotTo(HaveOccurred())
		Expect(accountFloor).To(BeTemporally("==", floor))
		at, err := BillingFinalizationTime(ctx, floor)
		Expect(err).NotTo(HaveOccurred())
		Expect(at).To(BeTemporally(">=", floor))
		Expect(at.Location()).To(Equal(time.UTC))
		Expect(at.Nanosecond() % 1000).To(BeZero())
	})

	It("samples a post-transaction database clock and rejects unrepresentable floors", func() {
		var transactionStart time.Time
		tx, err := TxFromContext(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(tx.QueryRow(ctx, `select transaction_timestamp()`).Scan(&transactionStart)).To(Succeed())
		at, err := BillingFinalizationTime(ctx, time.Time{})
		Expect(err).NotTo(HaveOccurred())
		Expect(at).To(BeTemporally(">", transactionStart))
		invalid := time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
		_, err = BillingFinalizationTime(ctx, invalid)
		Expect(err).To(HaveOccurred())
	})

	It("requires the ambient request transaction", func() {
		plain := context.Background()
		_, err := TenantBillingHistoryFloor(plain, "billing-history")
		Expect(err).To(HaveOccurred())
		_, err = BillingAccountHistoryFloor(plain, "billing-account")
		Expect(err).To(HaveOccurred())
		_, err = BillingFinalizationTime(plain, time.Time{})
		Expect(err).To(HaveOccurred())
	})
})
