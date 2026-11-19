/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package database_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// The external test package avoids database -> dao -> database's import cycle. It uses the same
// native Container/NewInstance harness as TestDatabase, not a substitute persistence implementation.
// Only the container is shared; every spec gets a fresh migrated database and transaction.
type billingPersistenceFixture struct {
	ctx         context.Context
	tx          database.Tx
	tm          database.TxManager
	pool        *pgxpool.Pool
	accounts    *dao.GenericDAO[*privatev1.BillingAccount]
	assignments *dao.GenericDAO[*privatev1.TenantBillingAssignment]
	tenants     *dao.GenericDAO[*privatev1.Tenant]
	visibility  *auth.Visibility
}

var _ = Describe("Canonical billing database contracts", Ordered, ContinueOnFailure, func() {
	var container *database.Container
	var logger *slog.Logger
	var f *billingPersistenceFixture
	BeforeAll(func() {
		logger = slog.New(slog.NewTextHandler(GinkgoWriter, nil))
		var err error
		container, err = database.NewContainer().SetLogger(logger).Build()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(container.Stop(context.Background())).To(Succeed()) })
		Expect(container.Start(context.Background())).To(Succeed())
	})
	BeforeEach(func() {
		f = &billingPersistenceFixture{ctx: context.Background(), visibility: auth.TotalVisibility()}
		db, err := container.NewInstance().Build()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(db.Close)
		f.pool, err = db.Pool(f.ctx)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(f.pool.Close)
		f.tm, err = database.NewTxManager().SetLogger(logger).SetPool(f.pool).Build()
		Expect(err).NotTo(HaveOccurred())
		f.tx, err = f.tm.Begin(f.ctx)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(f.tx.End(context.Background())).To(Succeed()) })
		f.ctx = database.TxIntoContext(f.ctx, f.tx)
		tenancy := auth.NewMockTenancyLogic(gomock.NewController(GinkgoT()))
		tenancy.EXPECT().DetermineVisibility(gomock.Any()).DoAndReturn(func(context.Context) (*auth.Visibility, error) {
			return f.visibility, nil
		}).AnyTimes()
		f.accounts, err = dao.NewGenericDAO[*privatev1.BillingAccount]().SetLogger(logger).SetTenancyLogic(tenancy).
			SetFilterDesc((*publicv1.BillingAccount)(nil).ProtoReflect().Descriptor()).Build()
		Expect(err).NotTo(HaveOccurred())
		f.assignments, err = dao.NewGenericDAO[*privatev1.TenantBillingAssignment]().SetLogger(logger).
			SetTenancyLogic(tenancy).Build()
		Expect(err).NotTo(HaveOccurred())
		f.tenants, err = dao.NewGenericDAO[*privatev1.Tenant]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
		Expect(err).NotTo(HaveOccurred())
	})

	Describe("account queries", func() {
		BeforeEach(func() {
			for i, label := range []string{"Beta", "Alpha"} {
				object := billingAccountFixture(fmt.Sprintf("query-%d", i), nil)
				object.GetStatus().SetDisplayName(label)
				if i == 1 {
					object.GetStatus().SetState(privatev1.BillingAccountState_BILLING_ACCOUNT_STATE_INACTIVE)
				}
				_, err := f.accounts.Create().SetObject(object).Do(f.ctx)
				Expect(err).NotTo(HaveOccurred())
			}
		})
		It("filters an exact nested enum and returns the total independently of its page", func() {
			page, err := f.accounts.List().SetFilter("this.status.state == 2").SetLimit(1).Do(f.ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(page.GetTotal()).To(Equal(int32(1)))
			Expect(page.GetItems()).To(HaveLen(1))
			Expect(page.GetItems()[0].GetStatus().GetDisplayName()).To(Equal("Alpha"))
		})
		DescribeTable("rejects private, unknown and retired filter paths", func(filter string) {
			err := f.tx.Savepoint(f.ctx, func(ctx context.Context) error {
				_, err := f.accounts.List().SetFilter(filter).Do(ctx)
				return err
			})
			var invalid *dao.ErrInvalidFilter
			Expect(errors.As(err, &invalid)).To(BeTrue())
		}, Entry("private", `this.status.provider_account_reference == "query-1"`),
			Entry("old flat", `this.state == "ACTIVE"`), Entry("old token", `this.status.correlation_id == "x"`))
		It("rejects negative limits before SQL", func() {
			err := f.tx.Savepoint(f.ctx, func(ctx context.Context) error {
				_, err := f.accounts.List().SetLimit(-1).Do(ctx)
				return err
			})
			var invalid *dao.ErrValidation
			Expect(errors.As(err, &invalid)).To(BeTrue())
		})
	})

	Describe("raw account tokens", func() {
		BeforeEach(func() {
			for id, token := range map[string]*string{
				"token-null": nil, "token-empty": proto.String(""), "token-space": proto.String(" \t "),
				"token-plain": proto.String("ext-1"), "token-padded": proto.String(" ext-1 "),
			} {
				_, err := f.accounts.Create().SetObject(billingAccountFixture(id, token)).Do(f.ctx)
				Expect(err).NotTo(HaveOccurred())
			}
		})
		DescribeTable("filters present values without normalizing bytes or matching NULL", func(raw, id string) {
			page, err := f.accounts.List().SetFilter(fmt.Sprintf("has(this.status.external_billing_account_id) && "+
				"this.status.external_billing_account_id == %q", raw)).Do(f.ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(page.GetTotal()).To(Equal(int32(1)))
			Expect(page.GetItems()).To(HaveLen(1))
			Expect(page.GetItems()[0].GetId()).To(Equal(id))
		}, Entry("empty", "", "token-empty"), Entry("whitespace", " \t ", "token-space"),
			Entry("plain", "ext-1", "token-plain"), Entry("padded", " ext-1 ", "token-padded"))
	})

	DescribeTable("paginates accounts at the old boundary without overlap or loss", func(count int) {
		ids := make([]string, count)
		for i := range count {
			ids[i] = fmt.Sprintf("seed-%03d", i)
			object := billingAccountFixture(ids[i], nil)
			object.GetStatus().SetDisplayName("Same Name")
			_, err := f.accounts.Create().SetObject(object).Do(f.ctx)
			Expect(err).NotTo(HaveOccurred())
		}
		var got []string
		for _, offset := range []int32{0, 100} {
			page, err := f.accounts.List().SetOffset(offset).SetLimit(100).Do(f.ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(page.GetTotal()).To(Equal(int32(count)))
			Expect(page.GetItems()).To(HaveLen(max(0, min(count-int(offset), 100))))
			for _, object := range page.GetItems() {
				got = append(got, object.GetId())
			}
		}
		Expect(got).To(Equal(ids))
	}, Entry("105 accounts", 105), Entry("exactly 100", 100))
	It("returns an empty account collection with total zero", func() {
		page, err := f.accounts.List().Do(f.ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(page.GetTotal()).To(BeZero())
		Expect(page.GetItems()).To(BeEmpty())
	})

	tenantBillingPersistenceSpecs(func() *billingPersistenceFixture { return f })
})

func billingAccountFixture(id string, token *string) *privatev1.BillingAccount {
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(id)))
	return privatev1.BillingAccount_builder{Id: id,
		Metadata: privatev1.Metadata_builder{Name: "ba-" + hash[:60], Tenant: auth.SystemTenant, Creator: "system"}.Build(),
		Spec:     privatev1.BillingAccountSpec_builder{}.Build(),
		Status: privatev1.BillingAccountStatus_builder{ProviderKey: "m360", ProviderAccountReference: id,
			State: privatev1.BillingAccountState_BILLING_ACCOUNT_STATE_ACTIVE, ExternalBillingAccountId: token,
			StateObservationTimestamp: timestamppb.New(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
		}.Build(),
	}.Build()
}
