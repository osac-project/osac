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
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Billing history canonical persistence", func() {
	var (
		ctx         context.Context
		tx          database.Tx
		accounts    *GenericDAO[*privatev1.BillingAccount]
		assignments *GenericDAO[*privatev1.TenantBillingAssignment]
		tenants     *GenericDAO[*privatev1.Tenant]
	)
	const tenant = "billing-history-tenant"
	const ownerAnnotation = "osac.openshift.io/tenant"
	BeforeEach(func() {
		ctx = context.Background()
		db, err := server.NewInstance().Build()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(db.Close)
		pool, err := db.Pool(ctx)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(pool.Close)
		tm, err := database.NewTxManager().SetLogger(logger).SetPool(pool).Build()
		Expect(err).NotTo(HaveOccurred())
		tx, err = tm.Begin(ctx)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(tx.End(context.Background())).To(Succeed()) })
		ctx = database.TxIntoContext(ctx, tx)
		ctrl := gomock.NewController(GinkgoT())
		tenancy := auth.NewMockTenancyLogic(ctrl)
		tenancy.EXPECT().DetermineVisibility(gomock.Any()).Return(auth.TotalVisibility(), nil).AnyTimes()
		accounts, err = NewGenericDAO[*privatev1.BillingAccount]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
		Expect(err).NotTo(HaveOccurred())
		assignments, err = NewGenericDAO[*privatev1.TenantBillingAssignment]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
		Expect(err).NotTo(HaveOccurred())
		tenants, err = NewGenericDAO[*privatev1.Tenant]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
		Expect(err).NotTo(HaveOccurred())
		// The migrated template already contains the platform system tenant.
		_, err = tenants.Create().SetObject(privatev1.Tenant_builder{Id: tenant,
			Metadata: privatev1.Metadata_builder{Name: tenant, Tenant: tenant, Creator: "system"}.Build(),
		}.Build()).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
	})

	newAccount := func(id string) *privatev1.BillingAccount {
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte(id)))
		response, err := accounts.Create().SetObject(privatev1.BillingAccount_builder{Id: id,
			Metadata: privatev1.Metadata_builder{Name: "ba-" + hash[:60], Tenant: auth.SystemTenant, Creator: "system"}.Build(),
			Spec:     privatev1.BillingAccountSpec_builder{}.Build(),
			Status: privatev1.BillingAccountStatus_builder{ProviderKey: "test", ProviderAccountReference: id,
				State:                     privatev1.BillingAccountState_BILLING_ACCOUNT_STATE_ACTIVE,
				StateObservationTimestamp: timestamppb.Now(),
			}.Build(),
		}.Build()).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		return response.GetObject()
	}
	newIntervalFor := func(tenant, id string, from time.Time, to *time.Time) *privatev1.TenantBillingAssignment {
		account := newAccount("account-" + id)
		s := privatev1.TenantBillingAssignmentStatus_builder{
			ConnectionState:        privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_PENDING,
			EffectiveFromTimestamp: timestamppb.New(from),
		}.Build()
		if to != nil {
			s.SetEffectiveToTimestamp(timestamppb.New(*to))
		}
		response, err := assignments.Create().SetObject(privatev1.TenantBillingAssignment_builder{Id: id,
			Metadata: privatev1.Metadata_builder{Name: "same-history-name", Tenant: tenant, Creator: "original-actor",
				Annotations: map[string]string{ownerAnnotation: tenant}, Labels: map[string]string{"test": "retained"},
			}.Build(),
			Spec: privatev1.TenantBillingAssignmentSpec_builder{
				Tenant:         privatev1.TenantReference_builder{Id: tenant, Name: tenant}.Build(),
				BillingAccount: privatev1.BillingAccountReference_builder{Id: account.GetId(), Name: account.GetMetadata().GetName()}.Build(),
			}.Build(), Status: s,
		}.Build()).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		return response.GetObject()
	}
	newInterval := func(id string, from time.Time, to *time.Time) *privatev1.TenantBillingAssignment {
		return newIntervalFor(tenant, id, from, to)
	}
	get := func(id string) *privatev1.TenantBillingAssignment {
		response, err := assignments.Get().SetId(id).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		return response.GetObject()
	}

	It("includes closed-only history in the floor and clamps one DB clock sample to it", func() {
		future := time.Now().UTC().Truncate(time.Microsecond).Add(24 * time.Hour)
		newInterval("closed-only", future.Add(-time.Hour), &future)
		floor, err := LockTenantBillingAssignmentForDeletion(ctx, tenant)
		Expect(err).NotTo(HaveOccurred())
		Expect(floor).To(BeTemporally("==", future))
		at, err := BillingFinalizationTime(ctx, floor)
		Expect(err).NotTo(HaveOccurred())
		Expect(at).To(BeTemporally("==", future))
		Expect(at.Location()).To(Equal(time.UTC))
		Expect(at.Nanosecond() % 1000).To(BeZero())
	})

	It("samples clock_timestamp after the transaction began, in UTC microseconds", func() {
		var transactionStart time.Time
		Expect(tx.QueryRow(ctx, "select transaction_timestamp()").Scan(&transactionStart)).To(Succeed())
		at, err := BillingFinalizationTime(ctx, time.Time{})
		Expect(err).NotTo(HaveOccurred())
		Expect(at).To(BeTemporally(">", transactionStart))
		Expect(at.Location()).To(Equal(time.UTC))
		Expect(at.Nanosecond() % 1000).To(BeZero())
	})

	It("round-trips an explicitly contiguous replacement through GenericDAO without billing enqueue", func() {
		from := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
		previous := newInterval("previous", from, nil)
		floor, err := LockTenantBillingAssignmentForDeletion(ctx, tenant)
		Expect(err).NotTo(HaveOccurred())
		at, err := BillingFinalizationTime(ctx, floor)
		Expect(err).NotTo(HaveOccurred())
		previous.GetStatus().SetEffectiveToTimestamp(timestamppb.New(at))
		closed, err := assignments.Update().SetObject(previous).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		next := newInterval("replacement", at, nil)
		Expect(next.GetId()).NotTo(Equal(closed.GetObject().GetId()))
		Expect(next.GetMetadata().GetName()).To(Equal(previous.GetMetadata().GetName()))
		Expect(proto.Equal(next.GetStatus().GetEffectiveFromTimestamp(), get(previous.GetId()).GetStatus().GetEffectiveToTimestamp())).To(BeTrue())
		var storedStart, storedEnd time.Time
		Expect(tx.QueryRow(ctx, `select a.effective_to, b.effective_from
			from tenant_billing_assignments a, tenant_billing_assignments b where a.id=$1 and b.id=$2`,
			previous.GetId(), next.GetId()).Scan(&storedEnd, &storedStart)).To(Succeed())
		Expect(storedStart).To(BeTemporally("==", at))
		Expect(storedEnd).To(BeTemporally("==", at))
		accountID := next.GetSpec().GetBillingAccount().GetId()
		account, err := accounts.Get().SetId(accountID).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		account.GetObject().GetStatus().SetDisplayName("new display label")
		_, err = accounts.Update().SetObject(account.GetObject()).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		var count int
		Expect(tx.QueryRow(ctx, `select count(*) from changes where "table" in ('billing_accounts','tenant_billing_assignments')`).Scan(&count)).To(Succeed())
		Expect(count).To(BeZero(), "direct canonical create/update of both billing kinds remains unpublished")
	})

	It("rolls billing and nonbilling writes back together without running a publisher", func() {
		from := time.Now().UTC().Truncate(time.Microsecond)
		err := tx.Savepoint(ctx, func(spCtx context.Context) error {
			_, createErr := tenants.Create().SetObject(privatev1.Tenant_builder{Id: "rolled-back-control",
				Metadata: privatev1.Metadata_builder{Name: "rolled-back-control", Tenant: "rolled-back-control"}.Build(),
			}.Build()).Do(spCtx)
			Expect(createErr).NotTo(HaveOccurred())
			// Both fixtures must use the savepoint's ambient transaction, not the assertion transaction.
			original := ctx
			ctx = spCtx
			defer func() { ctx = original }()
			newInterval("rolled-back-billing", from, nil)
			return fmt.Errorf("intentional rollback")
		})
		Expect(err).To(MatchError("intentional rollback"))
		for _, query := range []string{
			`select count(*) from tenant_billing_assignments where id = 'rolled-back-billing'`,
			`select count(*) from billing_accounts where id = 'account-rolled-back-billing'`,
			`select count(*) from tenants where id = 'rolled-back-control'`,
			`select count(*) from changes where data->>'id' = 'rolled-back-control'`,
		} {
			var count int
			Expect(tx.QueryRow(ctx, query).Scan(&count)).To(Succeed())
			Expect(count).To(BeZero())
		}
	})

	It("closes canonical JSON once without rewriting historical metadata or emitting billing changes", func() {
		from := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
		closedAt := from.Add(-time.Minute)
		history := newInterval("old", from.Add(-time.Hour), &closedAt)
		current := newInterval("current", from, nil)
		floor, err := LockTenantBillingAssignmentForDeletion(ctx, tenant)
		Expect(err).NotTo(HaveOccurred())
		at, err := BillingFinalizationTime(ctx, floor)
		Expect(err).NotTo(HaveOccurred())
		Expect(CloseTenantBillingAssignmentOnDeletion(ctx, tenant, at)).To(Succeed())
		got := get(current.GetId())
		Expect(got.GetStatus().GetEffectiveToTimestamp().AsTime()).To(BeTemporally("==", at))
		Expect(got.GetStatus().GetMessage()).To(Equal(database.TenantBillingAssignmentDeletedMessage))
		Expect(got.GetMetadata().GetVersion()).To(Equal(current.GetMetadata().GetVersion() + 1))
		want := proto.Clone(current).(*privatev1.TenantBillingAssignment)
		want.GetMetadata().SetVersion(got.GetMetadata().GetVersion())
		want.GetStatus().SetEffectiveToTimestamp(timestamppb.New(at))
		want.GetStatus().SetMessage(database.TenantBillingAssignmentDeletedMessage)
		Expect(proto.Equal(got, want)).To(BeTrue(), "only close/message/version may change")
		Expect(proto.Equal(get(history.GetId()), history)).To(BeTrue())
		Expect(CloseTenantBillingAssignmentOnDeletion(ctx, tenant, at.Add(time.Hour))).To(Succeed())
		Expect(proto.Equal(get(current.GetId()), got)).To(BeTrue(), "repeated deletion is an idempotent no-op")
		var count int
		Expect(tx.QueryRow(ctx, `select count(*) from changes where "table" in ('billing_accounts','tenant_billing_assignments')`).Scan(&count)).To(Succeed())
		Expect(count).To(BeZero())
		Expect(tx.QueryRow(ctx, `select count(*) from changes where "table" = 'tenants'`).Scan(&count)).To(Succeed())
		Expect(count).To(BeNumerically(">", 0), "ordinary nonbilling enqueue remains enabled, with no publisher running")
	})

	DescribeTable("rejects invalid closure times without changing the current row", func(kind string) {
		from := time.Now().UTC().Truncate(time.Microsecond)
		current := newInterval("current", from, nil)
		_, err := LockTenantBillingAssignmentForDeletion(ctx, tenant)
		Expect(err).NotTo(HaveOccurred())
		at := time.Time{}
		if kind == "before start" {
			at = from.Add(-time.Microsecond)
		}
		err = tx.Savepoint(ctx, func(spCtx context.Context) error {
			return CloseTenantBillingAssignmentOnDeletion(spCtx, tenant, at)
		})
		if kind == "before start" {
			var inUse *ErrInUse
			Expect(errors.As(err, &inUse)).To(BeTrue())
			Expect(inUse.Error()).To(ContainSubstring("repair the billing history"))
		} else {
			Expect(status.Code(err)).To(Equal(codes.Aborted))
		}
		Expect(proto.Equal(get(current.GetId()), current)).To(BeTrue())
	}, Entry("zero", "zero"), Entry("before start", "before start"))

	It("returns an empty floor and permits idempotent deletion without any history", func() {
		floor, err := LockTenantBillingAssignmentForDeletion(ctx, tenant)
		Expect(err).NotTo(HaveOccurred())
		Expect(floor.IsZero()).To(BeTrue())
		at, err := BillingFinalizationTime(ctx, floor)
		Expect(err).NotTo(HaveOccurred())
		Expect(CloseTenantBillingAssignmentOnDeletion(ctx, tenant, at)).To(Succeed())
	})

	It("permits an exact zero-length closure without changing the interval start", func() {
		from := time.Now().UTC().Truncate(time.Microsecond)
		current := newInterval("zero-length", from, nil)
		floor, err := LockTenantBillingAssignmentForDeletion(ctx, tenant)
		Expect(err).NotTo(HaveOccurred())
		Expect(CloseTenantBillingAssignmentOnDeletion(ctx, tenant, floor)).To(Succeed())
		closed := get(current.GetId())
		Expect(proto.Equal(closed.GetStatus().GetEffectiveFromTimestamp(), closed.GetStatus().GetEffectiveToTimestamp())).To(BeTrue())
	})

	It("ignores unrelated open intervals even when their start is later than the accepted deletion", func() {
		const other = "other-billing-tenant"
		_, err := tenants.Create().SetObject(privatev1.Tenant_builder{Id: other,
			Metadata: privatev1.Metadata_builder{Name: other, Tenant: other}.Build(),
		}.Build()).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		at := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
		unrelated := newIntervalFor(other, "unrelated-current", at.Add(time.Hour), nil)
		current := newInterval("delete-current", at.Add(-time.Hour), nil)
		floor, err := LockTenantBillingAssignmentForDeletion(ctx, tenant)
		Expect(err).NotTo(HaveOccurred())
		Expect(floor).To(BeTemporally("==", at.Add(-time.Hour)))
		Expect(CloseTenantBillingAssignmentOnDeletion(ctx, tenant, at)).To(Succeed())
		Expect(get(current.GetId()).GetStatus().GetEffectiveToTimestamp().AsTime()).To(BeTemporally("==", at))
		Expect(proto.Equal(get(unrelated.GetId()), unrelated)).To(BeTrue())
	})
})

// billingHistoryRow controls only SQL outcome guards. Canonical SQL/projection/rollback behavior
// is covered by the real PostgreSQL cases above, not by this test double.
type billingHistoryRow func(...any) error

func (r billingHistoryRow) Scan(dest ...any) error { return r(dest...) }

var _ = Describe("Billing history helper guards", func() {
	It("requires an ambient transaction for every operation", func() {
		ctx := context.Background()
		_, err := TenantBillingHistoryFloor(ctx, "tenant")
		Expect(err).To(HaveOccurred())
		_, err = LockTenantBillingAssignmentForDeletion(ctx, "tenant")
		Expect(err).To(HaveOccurred())
		_, err = BillingFinalizationTime(ctx, time.Time{})
		Expect(err).To(HaveOccurred())
		Expect(CloseTenantBillingAssignmentOnDeletion(ctx, "tenant", time.Now())).NotTo(Succeed())
	})

	It("aborts and reports rollback when a selected current interval is not actually closed", func() {
		ctrl := gomock.NewController(GinkgoT())
		tx := database.NewMockTx(ctrl)
		ctx := database.TxIntoContext(context.Background(), tx)
		at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		gomock.InOrder(
			tx.EXPECT().QueryRow(ctx, gomock.Any(), "tenant").Return(billingHistoryRow(func(dest ...any) error {
				*dest[0].(*string) = "expected-current"
				*dest[1].(*time.Time) = at
				return nil
			})),
			tx.EXPECT().Exec(ctx, gomock.Any(), "expected-current", "tenant", at,
				database.TenantBillingAssignmentDeletedMessage).Return(pgconn.NewCommandTag("UPDATE 0"), nil),
		)
		var reported []error
		tx.EXPECT().ReportError(gomock.Any()).Do(func(err *error) {
			if *err != nil {
				reported = append(reported, *err)
			}
		}).AnyTimes()
		err := CloseTenantBillingAssignmentOnDeletion(ctx, "tenant", at)
		Expect(status.Code(err)).To(Equal(codes.Aborted))
		Expect(reported).To(ContainElement(err))
	})

	It("takes exactly one clock sample and never rounds a supplied history floor downward", func() {
		ctrl := gomock.NewController(GinkgoT())
		tx := database.NewMockTx(ctrl)
		ctx := database.TxIntoContext(context.Background(), tx)
		floor := time.Date(2027, 1, 1, 0, 0, 0, 1, time.UTC)
		tx.EXPECT().QueryRow(ctx, "select clock_timestamp()").Return(billingHistoryRow(func(dest ...any) error {
			*dest[0].(*time.Time) = floor.Add(-time.Hour)
			return nil
		})).Times(1)
		tx.EXPECT().ReportError(gomock.Any()).Times(1)
		at, err := BillingFinalizationTime(ctx, floor)
		Expect(err).NotTo(HaveOccurred())
		Expect(at).To(BeTemporally(">=", floor))
		Expect(at.Nanosecond()).To(Equal(1000))
	})
})
