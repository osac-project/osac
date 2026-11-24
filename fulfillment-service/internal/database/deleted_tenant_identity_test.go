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
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Deleted tenant identities", Ordered, ContinueOnFailure, func() {
	var template *Instance
	var pool *pgxpool.Pool
	var tm TxManager
	var tx Tx
	var ctx context.Context
	identity := DeletedTenantIdentity{
		TenantName:        "identity",
		TenantCreatedAt:   time.Date(2026, 1, 1, 0, 0, 0, 1000, time.UTC),
		DeletionTimestamp: time.Date(2026, 2, 1, 0, 0, 0, 2000, time.UTC),
	}

	BeforeAll(func() {
		var err error
		template, err = server.NewInstance().Build()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(template.Close)
		_, err = template.Url(context.Background())
		Expect(err).NotTo(HaveOccurred())
	})
	BeforeEach(func() {
		ctx = context.Background()
		db, err := server.NewInstance().SetSource(template).Build()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(db.Close)
		pool, err = db.Pool(ctx)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(pool.Close)
		tm, err = NewTxManager().SetLogger(logger).SetPool(pool).Build()
		Expect(err).NotTo(HaveOccurred())
		tx, err = tm.Begin(ctx)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			err := tx.End(context.Background())
			if !errors.Is(err, pgx.ErrTxClosed) {
				Expect(err).NotTo(HaveOccurred())
			}
		})
		ctx = TxIntoContext(ctx, tx)
	})

	seedMarker := func(value DeletedTenantIdentity) {
		GinkgoHelper()
		_, err := tx.Exec(ctx, `insert into deleted_tenant_identities
			(tenant_name, tenant_created_at, deletion_timestamp, deleted_by, finalized_at, billing_retained)
			values ($1,$2,$3,$4,$5,$6)`, value.TenantName, value.TenantCreatedAt, value.DeletionTimestamp,
			value.DeletedBy, value.FinalizedAt, value.BillingRetained)
		Expect(err).NotTo(HaveOccurred())
	}

	insertLiveTenant := func(name string) {
		GinkgoHelper()
		Expect(LockTenantIdentity(ctx, name)).To(Succeed())
		_, err := tx.Exec(ctx, `insert into tenants (id, name, tenant, creator, data) values ($1,$1,$1,'system','{}')`, name)
		Expect(err).NotTo(HaveOccurred())
	}

	It("returns a miss without poisoning the transaction", func() {
		value, found, err := GetDeletedTenantIdentity(ctx, "missing")
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeFalse())
		Expect(value).To(BeZero())
		Expect(tx.End(ctx)).To(Succeed())
	})

	It("reserves a finalized deleted name but preserves live-row duplicate precedence", func() {
		finalized := identity.DeletionTimestamp.Add(time.Hour)
		identity.DeletedBy = new("original-actor")
		identity.FinalizedAt = &finalized
		identity.BillingRetained = true
		seedMarker(identity)
		err := tx.Savepoint(ctx, func(sp context.Context) error {
			return CheckTenantIdentityReusable(sp, identity.TenantName)
		})
		Expect(errors.Is(err, ErrTenantIdentityRetained)).To(BeTrue())
		got, found, err := GetDeletedTenantIdentity(ctx, identity.TenantName)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(got).To(Equal(identity))

		insertLiveTenant("live-duplicate")
		liveMarker := identity
		liveMarker.TenantName = "live-duplicate"
		seedMarker(liveMarker)
		Expect(CheckTenantIdentityReusable(ctx, "live-duplicate")).To(Succeed(),
			"a live row keeps normal duplicate precedence over an older marker")
	})

	It("checks billing history independently of the marker retention bit", func() {
		_, err := tx.Exec(ctx, `insert into tenants (id,name,tenant,creator,data) values ('billed','billed','billed','system','{}');
			insert into billing_accounts (id,name,tenant,data) values ('account',
			 'ba-' || left(encode(sha256(convert_to('account','UTF8')), 'hex'),60), 'system',
			 '{"spec":{},"status":{"provider_key":"m360","provider_account_reference":"native",
			 "state":"BILLING_ACCOUNT_STATE_ACTIVE","state_observation_timestamp":"2026-01-01T00:00:00Z"}}');
			insert into tenant_billing_assignments (id,name,tenant,annotations,data) values ('history','history','billed',
			 '{"osac.openshift.io/tenant":"billed"}', jsonb_build_object('spec', jsonb_build_object(
			 'tenant', jsonb_build_object('id','billed','name','billed'), 'billing_account', jsonb_build_object(
			 'id','account','name','ba-' || left(encode(sha256(convert_to('account','UTF8')), 'hex'),60))),
				 'status', jsonb_build_object('connection_state','TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED',
				 'effective_from_timestamp','2026-01-01T00:00:00Z','effective_to_timestamp','2026-02-01T00:00:00Z')))`)
		Expect(err).NotTo(HaveOccurred())
		_, err = tx.Exec(ctx, `delete from projects where tenant = 'billed'; delete from tenants where name = 'billed'`)
		Expect(err).NotTo(HaveOccurred())
		err = tx.Savepoint(ctx, func(sp context.Context) error { return CheckTenantIdentityReusable(sp, "billed") })
		Expect(errors.Is(err, ErrTenantIdentityRetained)).To(BeTrue())
	})

	It("keeps a missing tenant key-free and requires an ambient transaction", func() {
		plain := context.Background()
		Expect(LockTenantIdentity(plain, "name")).NotTo(Succeed())
		Expect(CheckTenantIdentityReusable(plain, "name")).NotTo(Succeed())
		_, _, err := GetDeletedTenantIdentity(plain, "name")
		Expect(err).To(HaveOccurred())
		Expect(LockTenantIdentity(ctx, "never-seen")).To(Succeed())
		_, _, err = GetDeletedTenantIdentity(ctx, "never-seen")
		Expect(err).NotTo(HaveOccurred())
	})

	It("allows reentrant keys and makes another connection wait until commit", func() {
		Expect(LockTenantIdentity(ctx, "identity")).To(Succeed())
		Expect(LockTenantIdentity(ctx, "identity")).To(Succeed())
		other, err := tm.Begin(context.Background())
		Expect(err).NotTo(HaveOccurred())
		otherCtx, cancel := context.WithTimeout(TxIntoContext(context.Background(), other), 5*time.Second)
		DeferCleanup(func() { Expect(other.End(context.Background())).To(Succeed()) })
		var pid int
		Expect(other.QueryRow(otherCtx, `select pg_backend_pid()`).Scan(&pid)).To(Succeed())
		done := make(chan error, 1)
		workerDone := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			defer close(workerDone)
			done <- LockTenantIdentity(otherCtx, "identity")
		}()
		DeferCleanup(func() {
			cancel()
			Eventually(workerDone, 3*time.Second).Should(BeClosed())
		})
		Eventually(func() bool {
			var waiting bool
			Expect(pool.QueryRow(ctx, `select exists(select 1 from pg_locks
				where pid = $1 and locktype = 'advisory' and not granted)`, pid).Scan(&waiting)).To(Succeed())
			return waiting
		}).WithTimeout(3 * time.Second).Should(BeTrue())
		Expect(tx.End(ctx)).To(Succeed())
		Eventually(done).WithTimeout(3 * time.Second).Should(Receive(Succeed()))
	})
})
