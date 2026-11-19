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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Deleted tenant identity store", Ordered, ContinueOnFailure, func() {
	var template *Instance
	var pool *pgxpool.Pool
	var tm TxManager
	var tx Tx
	var ctx context.Context
	var identity DeletedTenantIdentity
	stamp := func(value string) time.Time {
		GinkgoHelper()
		t, err := time.Parse(time.RFC3339Nano, value)
		Expect(err).NotTo(HaveOccurred())
		return t
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
		identity = DeletedTenantIdentity{TenantName: "identity",
			TenantCreatedAt:   stamp("2026-01-01T00:00:00.000001Z"),
			DeletionTimestamp: stamp("2026-02-01T00:00:00.000002Z")}
	})
	// Capture's precondition is owned by the caller: the identity key precedes the original tenant row lock.
	// These tests exercise the store, not the DAO's physical archival or its creation timestamp binding.
	lockTenant := func() {
		GinkgoHelper()
		Expect(LockTenantIdentity(ctx, identity.TenantName)).To(Succeed())
		_, err := tx.Exec(ctx, `insert into tenants (id, name, tenant, creation_timestamp, deletion_timestamp, data)
		 values ($1, $1, $1, $2, $3, '{}')`, identity.TenantName, identity.TenantCreatedAt, identity.DeletionTimestamp)
		Expect(err).NotTo(HaveOccurred())
		var name string
		Expect(tx.QueryRow(ctx, `select name from tenants where name = $1 for update`, identity.TenantName).Scan(&name)).To(Succeed())
	}
	seedMarker := func(in DeletedTenantIdentity) {
		GinkgoHelper()
		_, err := tx.Exec(ctx, `insert into deleted_tenant_identities
		 (tenant_name, tenant_created_at, deletion_timestamp, deleted_by, finalized_at, billing_retained)
		 values ($1,$2,$3,$4,$5,$6)`, in.TenantName, in.TenantCreatedAt, in.DeletionTimestamp,
			in.DeletedBy, in.FinalizedAt, in.BillingRetained)
		Expect(err).NotTo(HaveOccurred())
	}
	It("returns an expected miss without poisoning a subsequent successful commit", func() {
		value, found, err := GetDeletedTenantIdentity(ctx, "missing")
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeFalse())
		Expect(value).To(BeZero())
		lockTenant()
		Expect(CaptureDeletedTenantIdentity(ctx, identity)).To(Succeed())
		Expect(tx.End(ctx)).To(Succeed())
		var foundAfterCommit bool
		Expect(pool.QueryRow(ctx, `select exists(select 1 from deleted_tenant_identities
		 where tenant_name = 'identity')`).Scan(&foundAfterCommit)).To(Succeed())
		Expect(foundAfterCommit).To(BeTrue())
	})
	It("preserves the first deletion, first known actor, first finalization and true retention", func() {
		lockTenant()
		Expect(CaptureDeletedTenantIdentity(ctx, identity)).To(Succeed())
		firstDeletion := identity.DeletionTimestamp
		actor := "first-actor"
		firstFinalized := stamp("2026-03-01T00:00:00.000003Z")
		identity.DeletedBy, identity.FinalizedAt, identity.BillingRetained = &actor, &firstFinalized, true
		Expect(CaptureDeletedTenantIdentity(ctx, identity)).To(Succeed())
		otherActor := "later-actor"
		laterFinalized := firstFinalized.Add(time.Hour)
		identity.DeletedBy, identity.FinalizedAt, identity.BillingRetained = &otherActor, &laterFinalized, false
		identity.DeletionTimestamp = firstDeletion.Add(time.Minute)
		Expect(CaptureDeletedTenantIdentity(ctx, identity)).To(Succeed())
		got, found, err := GetDeletedTenantIdentity(ctx, identity.TenantName)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(got.DeletionTimestamp).To(Equal(firstDeletion))
		Expect(got.DeletedBy).To(HaveValue(Equal(actor)))
		Expect(got.FinalizedAt).To(HaveValue(Equal(firstFinalized)))
		Expect(got.BillingRetained).To(BeTrue())
	})
	It("replaces only a strictly later incarnation of an unbilled finalized identity", func() {
		prior := identity
		finalized := identity.DeletionTimestamp.Add(time.Hour)
		prior.FinalizedAt = &finalized
		seedMarker(prior)
		identity.TenantCreatedAt = finalized.Add(time.Microsecond)
		identity.DeletionTimestamp = identity.TenantCreatedAt.Add(time.Hour)
		identity.BillingRetained = true
		lockTenant()
		Expect(CaptureDeletedTenantIdentity(ctx, identity)).To(Succeed())
		got, found, err := GetDeletedTenantIdentity(ctx, identity.TenantName)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(got).To(Equal(identity))
	})
	DescribeTable("rejects conflicting captures", func(change string) {
		old := identity
		finalized := old.DeletionTimestamp.Add(time.Hour)
		old.FinalizedAt = &finalized
		switch change {
		case "billed":
			old.BillingRetained = true
		case "pending":
			old.FinalizedAt = nil
		}
		seedMarker(old)
		switch change {
		case "stale":
			identity.TenantCreatedAt = identity.TenantCreatedAt.Add(-time.Hour)
		case "tie":
			identity.TenantCreatedAt = finalized
			identity.DeletionTimestamp = finalized.Add(time.Hour)
		case "overlap":
			identity.TenantCreatedAt = finalized.Add(-time.Microsecond)
			identity.DeletionTimestamp = finalized.Add(time.Hour)
		case "billed", "pending":
			identity.TenantCreatedAt = finalized.Add(time.Microsecond)
			identity.DeletionTimestamp = finalized.Add(time.Hour)
		case "earlier deletion":
			identity.DeletionTimestamp = identity.DeletionTimestamp.Add(-time.Microsecond)
		}
		lockTenant()
		Expect(tx.Savepoint(ctx, func(sp context.Context) error {
			return CaptureDeletedTenantIdentity(sp, identity)
		})).NotTo(Succeed())
		got, _, err := GetDeletedTenantIdentity(ctx, identity.TenantName)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(old))
	}, Entry("stale incarnation", "stale"), Entry("tied incarnation", "tie"),
		Entry("overlapping incarnation", "overlap"), Entry("retained incarnation", "billed"),
		Entry("unfinalized incarnation", "pending"), Entry("earlier deletion", "earlier deletion"))
	DescribeTable("rejects unrepresentable or inverted captures", func(change string) {
		lockTenant()
		switch change {
		case "epoch":
			identity.TenantCreatedAt = time.Unix(0, 0).UTC()
		case "precision":
			identity.DeletionTimestamp = identity.DeletionTimestamp.Add(time.Nanosecond)
		case "inverted":
			identity.DeletionTimestamp = identity.TenantCreatedAt.Add(-time.Microsecond)
		case "range":
			identity.DeletionTimestamp = stamp("9999-12-31T23:59:59.999999Z").Add(time.Microsecond)
		case "finalized":
			before := identity.DeletionTimestamp.Add(-time.Microsecond)
			identity.FinalizedAt = &before
		case "empty":
			identity.TenantName = ""
		}
		Expect(tx.Savepoint(ctx, func(sp context.Context) error {
			return CaptureDeletedTenantIdentity(sp, identity)
		})).NotTo(Succeed())
	}, Entry("epoch identity", "epoch"), Entry("submicrosecond precision", "precision"),
		Entry("creation after deletion", "inverted"), Entry("outside protobuf range", "range"),
		Entry("finalization before deletion", "finalized"), Entry("empty name", "empty"))
	It("keeps existing live/deleting duplicate precedence over an old pending or retained marker", func() {
		lockTenant()
		identity.BillingRetained = true
		seedMarker(identity)
		Expect(CheckTenantIdentityReusable(ctx, identity.TenantName)).To(Succeed())
		created, err := TenantIdentityCreationTime(ctx, identity.TenantName)
		Expect(err).NotTo(HaveOccurred())
		Expect(created.Nanosecond() % 1000).To(BeZero())
		err = tx.Savepoint(ctx, func(sp context.Context) error {
			spTx, err := TxFromContext(sp)
			Expect(err).NotTo(HaveOccurred())
			_, err = spTx.Exec(sp, `insert into tenants (id,name,tenant,data) values ($1,$1,$1,'{}')`, identity.TenantName)
			return err
		})
		var duplicate *pgconn.PgError
		Expect(errors.As(err, &duplicate)).To(BeTrue())
		Expect(duplicate.Code).To(Equal("23505"), "ordinary INSERT owns duplicate classification")
	})
	It("does not apply an old marker's reservation to a current live duplicate", func() {
		lockTenant()
		finalized := stamp("9999-12-31T23:59:59.999999Z")
		identity.FinalizedAt = &finalized
		seedMarker(identity)
		created, err := TenantIdentityCreationTime(ctx, identity.TenantName)
		Expect(err).NotTo(HaveOccurred())
		Expect(created.Before(finalized)).To(BeTrue())
	})
	DescribeTable("denies absent retained, unbilled or pending identities", func(mode string) {
		switch mode {
		case "billed":
			identity.BillingRetained = true
		case "unbilled":
			finalized := identity.DeletionTimestamp.Add(time.Hour)
			identity.FinalizedAt = &finalized
		}
		seedMarker(identity)
		err := tx.Savepoint(ctx, func(sp context.Context) error {
			return CheckTenantIdentityReusable(sp, identity.TenantName)
		})
		switch mode {
		case "pending":
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, ErrTenantIdentityRetained)).To(BeFalse())
		default:
			Expect(errors.Is(err, ErrTenantIdentityRetained)).To(BeTrue())
		}
	}, Entry("billed marker", "billed"), Entry("pending is an audit error", "pending"),
		Entry("finalized unbilled marker", "unbilled"))
	DescribeTable("checks actual retained history independently of marker billing", func(withMarker bool) {
		_, err := tx.Exec(ctx, `insert into billing_accounts (id,name,tenant,data) values ('account',
		 'ba-' || left(encode(sha256(convert_to('account','UTF8')), 'hex'),60), 'system',
		 '{"spec":{},"status":{"provider_key":"m360","provider_account_reference":"native",
		 "state":"BILLING_ACCOUNT_STATE_ACTIVE","state_observation_timestamp":"2026-01-01T00:00:00Z"}}');
		 insert into tenant_billing_assignments (id,name,tenant,annotations,data) values ('history','history','identity',
		 '{"osac.openshift.io/tenant":"identity"}', jsonb_build_object('spec', jsonb_build_object(
		 'tenant', jsonb_build_object('id','identity','name','identity'), 'billing_account', jsonb_build_object(
		 'id','account','name','ba-' || left(encode(sha256(convert_to('account','UTF8')), 'hex'),60))),
		 'status', jsonb_build_object('connection_state','TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_PENDING',
		 'effective_from_timestamp','2026-01-01T00:00:00Z','effective_to_timestamp','2026-02-01T00:00:00Z')))`)
		Expect(err).NotTo(HaveOccurred())
		if withMarker {
			finalized := identity.DeletionTimestamp.Add(time.Hour)
			identity.FinalizedAt = &finalized
			seedMarker(identity)
		}
		err = tx.Savepoint(ctx, func(sp context.Context) error { return CheckTenantIdentityReusable(sp, "identity") })
		Expect(errors.Is(err, ErrTenantIdentityRetained)).To(BeTrue())
	}, Entry("missing marker", false), Entry("old unbilled marker", true))
	It("returns a fresh UTC microsecond database sample for a never-used name", func() {
		var before, after time.Time
		Expect(tx.QueryRow(ctx, `select clock_timestamp()`).Scan(&before)).To(Succeed())
		created, err := TenantIdentityCreationTime(ctx, "new-name")
		Expect(err).NotTo(HaveOccurred())
		Expect(tx.QueryRow(ctx, `select clock_timestamp()`).Scan(&after)).To(Succeed())
		Expect(created.Before(before)).To(BeFalse())
		Expect(created.After(after)).To(BeFalse())
		Expect(created.Location()).To(Equal(time.UTC))
		Expect(created.Nanosecond() % 1000).To(BeZero())
		Expect(created.After(time.Unix(0, 0))).To(BeTrue())
	})
	It("requires an ambient transaction instead of opening one implicitly", func() {
		plain := context.Background()
		Expect(LockTenantIdentity(plain, "name")).NotTo(Succeed())
		Expect(CheckTenantIdentityReusable(plain, "name")).NotTo(Succeed())
		_, err := TenantIdentityCreationTime(plain, "name")
		Expect(err).To(HaveOccurred())
		Expect(CaptureDeletedTenantIdentity(plain, identity)).NotTo(Succeed())
		_, _, err = GetDeletedTenantIdentity(plain, "name")
		Expect(err).To(HaveOccurred())
	})
	DescribeTable("denies a finalized deleted identity without replacing the marker", func(finalized string) {
		value := stamp(finalized)
		identity.FinalizedAt = &value
		seedMarker(identity)
		Expect(tx.Savepoint(ctx, func(sp context.Context) error {
			_, err := TenantIdentityCreationTime(sp, identity.TenantName)
			return err
		})).To(MatchError(ErrTenantIdentityRetained))
		got, found, err := GetDeletedTenantIdentity(ctx, identity.TenantName)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(got).To(Equal(identity))
	}, Entry("ordinary finalized marker", "2099-12-31T23:59:59.999999Z"),
		Entry("marker at the protobuf upper bound", "9999-12-31T23:59:59.999999Z"))
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
		workerStarted := false
		DeferCleanup(func() {
			cancel()
			if workerStarted {
				Eventually(workerDone, 3*time.Second).Should(BeClosed(),
					"the lock worker must stop before the transaction ends")
			}
		})
		go func() {
			defer GinkgoRecover()
			defer close(workerDone)
			done <- LockTenantIdentity(otherCtx, "identity")
		}()
		workerStarted = true
		Eventually(func() bool {
			var waiting bool
			Expect(pool.QueryRow(ctx, `select exists(select 1 from pg_locks
			 where pid = $1 and locktype = 'advisory' and not granted)`, pid).Scan(&waiting)).To(Succeed())
			return waiting
		}).WithTimeout(3 * time.Second).Should(BeTrue())
		Expect(tx.End(ctx)).To(Succeed())
		Eventually(done).WithTimeout(3 * time.Second).Should(Receive(Succeed()))
	})
	It("releases the key and rolls back captured evidence together", func() {
		lockTenant()
		Expect(CaptureDeletedTenantIdentity(ctx, identity)).To(Succeed())
		rollback := errors.New("deliberate rollback")
		tx.ReportError(&rollback)
		Expect(tx.End(ctx)).To(Succeed())
		other, err := tm.Begin(context.Background())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(other.End(context.Background())).To(Succeed()) })
		otherCtx, cancel := context.WithTimeout(TxIntoContext(context.Background(), other), 3*time.Second)
		DeferCleanup(cancel)
		Expect(LockTenantIdentity(otherCtx, identity.TenantName)).To(Succeed())
		_, found, err := GetDeletedTenantIdentity(otherCtx, identity.TenantName)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeFalse())
	})
})
