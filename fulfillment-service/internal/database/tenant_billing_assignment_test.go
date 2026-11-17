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
	"errors"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func tenantBillingPersistenceSpecs(fixture func() *billingPersistenceFixture) {
	Describe("assignment intervals", func() {
		var f *billingPersistenceFixture
		var account *privatev1.BillingAccount
		BeforeEach(func() {
			f = fixture()
			// Ordinary tenant creation uses the real DAO's self-bootstrap path.
			for _, tenant := range []string{"tenant-a", "tenant-b"} {
				_, err := f.tenants.Create().SetObject(privatev1.Tenant_builder{Id: tenant,
					Metadata: privatev1.Metadata_builder{Name: tenant, Tenant: tenant, Creator: "system"}.Build(),
				}.Build()).Do(f.ctx)
				Expect(err).NotTo(HaveOccurred())
			}
			created, err := f.accounts.Create().SetObject(billingAccountFixture("interval-account", proto.String("corr-1"))).Do(f.ctx)
			Expect(err).NotTo(HaveOccurred())
			account = created.GetObject()
		})
		It("opens the first interval with typed references and retrieves its complete stored object", func() {
			created, err := f.assignments.Create().SetObject(billingAssignmentFixture("first", "tenant-a", account)).Do(f.ctx)
			Expect(err).NotTo(HaveOccurred())
			got, err := f.assignments.Get().SetId("first").Do(f.ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(proto.Equal(got.GetObject(), created.GetObject())).To(BeTrue())
			Expect(got.GetObject().GetSpec().GetTenant().GetId()).To(Equal("tenant-a"))
			Expect(got.GetObject().GetStatus().HasEffectiveToTimestamp()).To(BeFalse())
			storedAccount, err := f.accounts.Get().SetId(account.GetId()).Do(f.ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(storedAccount.GetObject().GetStatus().GetProviderAccountReference()).To(Equal("interval-account"))
			Expect(storedAccount.GetObject().GetStatus().GetExternalBillingAccountId()).To(Equal("corr-1"))
			Expect(storedAccount.GetObject().GetStatus().GetState()).To(Equal(
				privatev1.BillingAccountState_BILLING_ACCOUNT_STATE_ACTIVE))
		})
		It("closes and replaces an interval atomically with exactly matching boundaries", func() {
			first, err := f.assignments.Create().SetObject(billingAssignmentFixture("first", "tenant-a", account)).Do(f.ctx)
			Expect(err).NotTo(HaveOccurred())
			floor, err := dao.LockTenantBillingAssignmentForDeletion(f.ctx, "tenant-a")
			Expect(err).NotTo(HaveOccurred())
			at, err := dao.BillingFinalizationTime(f.ctx, floor)
			Expect(err).NotTo(HaveOccurred())
			first.GetObject().GetStatus().SetEffectiveToTimestamp(timestamppb.New(at))
			_, err = f.assignments.Update().SetObject(first.GetObject()).Do(f.ctx)
			Expect(err).NotTo(HaveOccurred())
			next := billingAssignmentFixture("second", "tenant-a", account)
			next.GetStatus().SetEffectiveFromTimestamp(timestamppb.New(at))
			_, err = f.assignments.Create().SetObject(next).Do(f.ctx)
			Expect(err).NotTo(HaveOccurred())
			old, err := f.assignments.Get().SetId("first").Do(f.ctx)
			Expect(err).NotTo(HaveOccurred())
			page, err := f.assignments.List().SetFilter(`this.spec.tenant.id == "tenant-a" && !has(this.status.effective_to_timestamp)`).Do(f.ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(page.GetTotal()).To(Equal(int32(1)))
			Expect(page.GetItems()[0].GetId()).To(Equal("second"))
			Expect(proto.Equal(old.GetObject().GetStatus().GetEffectiveToTimestamp(),
				page.GetItems()[0].GetStatus().GetEffectiveFromTimestamp())).To(BeTrue())
		})
		It("closes only the current interval and is idempotent without changing closed history or another tenant", func() {
			history := billingAssignmentFixture("history", "tenant-a", account)
			history.GetStatus().SetEffectiveToTimestamp(history.GetStatus().GetEffectiveFromTimestamp())
			history.GetStatus().SetMessage("original history message")
			old, err := f.assignments.Create().SetObject(history).Do(f.ctx)
			Expect(err).NotTo(HaveOccurred())
			current, err := f.assignments.Create().SetObject(billingAssignmentFixture("current", "tenant-a", account)).Do(f.ctx)
			Expect(err).NotTo(HaveOccurred())
			otherAccount, err := f.accounts.Create().SetObject(billingAccountFixture("other-account", nil)).Do(f.ctx)
			Expect(err).NotTo(HaveOccurred())
			other, err := f.assignments.Create().SetObject(billingAssignmentFixture("other", "tenant-b", otherAccount.GetObject())).Do(f.ctx)
			Expect(err).NotTo(HaveOccurred())
			floor, err := dao.LockTenantBillingAssignmentForDeletion(f.ctx, "tenant-a")
			Expect(err).NotTo(HaveOccurred())
			at, err := dao.BillingFinalizationTime(f.ctx, floor)
			Expect(err).NotTo(HaveOccurred())
			Expect(dao.CloseTenantBillingAssignmentOnDeletion(f.ctx, "tenant-a", at)).To(Succeed())
			closed, err := f.assignments.Get().SetId("current").Do(f.ctx)
			Expect(err).NotTo(HaveOccurred())
			want := proto.Clone(current.GetObject()).(*privatev1.TenantBillingAssignment)
			want.GetStatus().SetEffectiveToTimestamp(timestamppb.New(at))
			want.GetStatus().SetMessage(database.TenantBillingAssignmentDeletedMessage)
			want.GetMetadata().SetVersion(want.GetMetadata().GetVersion() + 1)
			Expect(proto.Equal(closed.GetObject(), want)).To(BeTrue())
			Expect(dao.CloseTenantBillingAssignmentOnDeletion(f.ctx, "tenant-a", at.Add(time.Hour))).To(Succeed())
			for id, expected := range map[string]*privatev1.TenantBillingAssignment{
				"history": old.GetObject(), "current": closed.GetObject(), "other": other.GetObject(),
			} {
				got, err := f.assignments.Get().SetId(id).Do(f.ctx)
				Expect(err).NotTo(HaveOccurred())
				Expect(proto.Equal(got.GetObject(), expected)).To(BeTrue())
			}
			page, err := f.assignments.List().SetFilter(`this.spec.billing_account.id == "interval-account" && !has(this.status.effective_to_timestamp)`).Do(f.ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(page.GetTotal()).To(BeZero())
		})
		It("is a no-op for tenants with no current assignment, including a missing tenant", func() {
			for _, tenant := range []string{"tenant-a", "missing"} {
				floor, err := dao.LockTenantBillingAssignmentForDeletion(f.ctx, tenant)
				Expect(err).NotTo(HaveOccurred())
				at, err := dao.BillingFinalizationTime(f.ctx, floor)
				Expect(err).NotTo(HaveOccurred())
				Expect(dao.CloseTenantBillingAssignmentOnDeletion(f.ctx, tenant, at)).To(Succeed())
			}
		})
		It("returns typed not-found for missing accounts and assignments without a row mapper", func() {
			for _, get := range []func(context.Context) error{
				func(ctx context.Context) error { _, err := f.accounts.Get().SetId("missing").Do(ctx); return err },
				func(ctx context.Context) error { _, err := f.assignments.Get().SetId("missing").Do(ctx); return err },
			} {
				err := f.tx.Savepoint(f.ctx, get)
				var missing *dao.ErrNotFound
				Expect(errors.As(err, &missing)).To(BeTrue())
			}
			page, err := f.assignments.List().SetFilter(`!has(this.status.effective_to_timestamp)`).Do(f.ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(page.GetItems()).To(BeEmpty())
		})
		DescribeTable("finalizes after a later committed assignment without using the old transaction timestamp", func(future bool) {
			deletion, err := f.tm.Begin(f.ctx)
			Expect(err).NotTo(HaveOccurred())
			deletionCtx := database.TxIntoContext(f.ctx, deletion)
			DeferCleanup(func() { Expect(deletion.End(context.Background())).To(Succeed()) })
			var transactionStart time.Time
			Expect(deletion.QueryRow(deletionCtx, `select now()`).Scan(&transactionStart)).To(Succeed())
			from := time.Now().UTC().Truncate(time.Microsecond)
			if future {
				from = from.Add(time.Hour)
			}
			Expect(f.tm.Run(f.ctx, func(tx database.Tx) error {
				ctx := database.TxIntoContext(f.ctx, tx)
				_, err := f.tenants.Create().SetObject(privatev1.Tenant_builder{Id: "later-tenant",
					Metadata: privatev1.Metadata_builder{Name: "later-tenant", Tenant: "later-tenant"}.Build(),
				}.Build()).Do(ctx)
				if err != nil {
					return err
				}
				created, err := f.accounts.Create().SetObject(billingAccountFixture("later-account", nil)).Do(ctx)
				if err != nil {
					return err
				}
				object := billingAssignmentFixture("later-interval", "later-tenant", created.GetObject())
				object.GetStatus().SetEffectiveFromTimestamp(timestamppb.New(from))
				_, err = f.assignments.Create().SetObject(object).Do(ctx)
				return err
			})).To(Succeed())
			Expect(from).To(BeTemporally(">", transactionStart))
			_, err = f.tenants.Get().SetId("later-tenant").SetLock(true).Do(deletionCtx)
			Expect(err).NotTo(HaveOccurred())
			floor, err := dao.LockTenantBillingAssignmentForDeletion(deletionCtx, "later-tenant")
			Expect(err).NotTo(HaveOccurred())
			at, err := dao.BillingFinalizationTime(deletionCtx, floor)
			Expect(err).NotTo(HaveOccurred())
			Expect(dao.CloseTenantBillingAssignmentOnDeletion(deletionCtx, "later-tenant", at)).To(Succeed())
			closed, err := f.assignments.Get().SetId("later-interval").Do(deletionCtx)
			Expect(err).NotTo(HaveOccurred())
			Expect(closed.GetObject().GetStatus().GetEffectiveToTimestamp().AsTime()).To(BeTemporally("==", at))
			Expect(at).To(BeTemporally(">=", from))
			if future {
				Expect(at).To(BeTemporally("==", from), "a future interval floor permits an exact zero-length close")
			}
		}, Entry("post-lock database clock", false), Entry("future interval floor", true))

		DescribeTable("retains the actual unique-index race between independent canonical creates", func(sameTenant bool) {
			Expect(f.tm.Run(f.ctx, func(tx database.Tx) error {
				ctx := database.TxIntoContext(f.ctx, tx)
				for _, tenant := range []string{"tenant-c", "tenant-d"} {
					_, err := f.tenants.Create().SetObject(privatev1.Tenant_builder{Id: tenant,
						Metadata: privatev1.Metadata_builder{Name: tenant, Tenant: tenant}.Build(),
					}.Build()).Do(ctx)
					if err != nil {
						return err
					}
				}
				for _, id := range []string{"race-account-a", "race-account-b"} {
					_, err := f.accounts.Create().SetObject(billingAccountFixture(id, nil)).Do(ctx)
					if err != nil {
						return err
					}
				}
				return nil
			})).To(Succeed())
			holder, err := f.tm.Begin(f.ctx)
			Expect(err).NotTo(HaveOccurred())
			holderEnded := false
			endHolder := func() {
				if !holderEnded {
					holderEnded = true
					Expect(holder.End(context.Background())).To(Succeed())
				}
			}
			DeferCleanup(endHolder)
			holderCtx := database.TxIntoContext(f.ctx, holder)
			var holderPID int32
			Expect(holder.QueryRow(holderCtx, `select pg_backend_pid()`).Scan(&holderPID)).To(Succeed())
			_, err = f.assignments.Create().SetObject(billingAssignmentFixture("race-winner", "tenant-c",
				billingAccountFixture("race-account-a", nil))).Do(holderCtx)
			Expect(err).NotTo(HaveOccurred())
			waiter, err := f.tm.Begin(f.ctx)
			Expect(err).NotTo(HaveOccurred())
			waiterCtx := database.TxIntoContext(f.ctx, waiter)
			var waiterPID int32
			Expect(waiter.QueryRow(waiterCtx, `select pg_backend_pid()`).Scan(&waiterPID)).To(Succeed())
			Expect(waiterPID).NotTo(Equal(holderPID))
			tenant, accountID, constraint := "tenant-d", "race-account-a", "tenant_billing_assignments_unique_active_account"
			if sameTenant {
				tenant, accountID, constraint = "tenant-c", "race-account-b", "tenant_billing_assignments_unique_active_tenant"
			}
			waitCtx, cancel := context.WithTimeout(waiterCtx, 30*time.Second)
			result := make(chan error, 1)
			DeferCleanup(func() {
				endHolder()
				cancel()
				<-result
				Expect(waiter.End(context.Background())).To(Succeed())
			})
			go func() {
				_, err := f.assignments.Create().SetObject(billingAssignmentFixture("race-loser", tenant,
					billingAccountFixture(accountID, nil))).Do(waitCtx)
				result <- err
				close(result)
			}()
			Eventually(func() ([]int32, error) {
				var blocked []int32
				err := f.pool.QueryRow(f.ctx, `select pg_blocking_pids($1)`, waiterPID).Scan(&blocked)
				return blocked, err
			}, 10*time.Second, 50*time.Millisecond).Should(ContainElement(holderPID))
			endHolder()
			var raced error
			Eventually(result, 10*time.Second).Should(Receive(&raced))
			var duplicate *dao.ErrAlreadyExists
			Expect(errors.As(raced, &duplicate)).To(BeTrue())
			Expect(duplicate.ConstraintName).To(Equal(constraint))
			Expect(f.tm.Run(f.ctx, func(tx database.Tx) error {
				page, err := f.assignments.List().SetFilter(`!has(this.status.effective_to_timestamp)`).
					Do(database.TxIntoContext(f.ctx, tx))
				if err == nil {
					Expect(page.GetTotal()).To(Equal(int32(1)))
					Expect(page.GetItems()[0].GetId()).To(Equal("race-winner"))
				}
				return err
			})).To(Succeed())
		}, Entry("same tenant", true), Entry("same account", false))

		Describe("assignment queries", func() {
			BeforeEach(func() {
				for i, tenant := range []string{"tenant-a", "tenant-a", "tenant-b"} {
					selected := account
					if i == 2 {
						created, err := f.accounts.Create().SetObject(billingAccountFixture("account-b", nil)).Do(f.ctx)
						Expect(err).NotTo(HaveOccurred())
						selected = created.GetObject()
					}
					object := billingAssignmentFixture(fmt.Sprintf("query-%d", i), tenant, selected)
					if i == 0 {
						object.GetStatus().SetEffectiveToTimestamp(object.GetStatus().GetEffectiveFromTimestamp())
					}
					_, err := f.assignments.Create().SetObject(object).Do(f.ctx)
					Expect(err).NotTo(HaveOccurred())
				}
			})
			DescribeTable("filters canonical references and current/history with SQL counts", func(filter string, count int32) {
				page, err := f.assignments.List().SetFilter(filter).SetLimit(1).Do(f.ctx)
				Expect(err).NotTo(HaveOccurred())
				Expect(page.GetTotal()).To(Equal(count))
				Expect(page.GetItems()).To(HaveLen(1))
			}, Entry("tenant", `this.spec.tenant.id == "tenant-a"`, int32(2)),
				Entry("account", `this.spec.billing_account.id == "account-b"`, int32(1)),
				Entry("current", `!has(this.status.effective_to_timestamp)`, int32(2)),
				Entry("history", `has(this.status.effective_to_timestamp)`, int32(1)),
				Entry("combined", `this.spec.tenant.id == "tenant-a" && !has(this.status.effective_to_timestamp)`, int32(1)))
			DescribeTable("intersects finite, universal and empty visibility before counting and paging", func(tenants []string, total int32) {
				if tenants != nil {
					var err error
					f.visibility, err = auth.NewVisibility().AddVisibleTenants(tenants...).Build()
					Expect(err).NotTo(HaveOccurred())
				}
				seen := map[string]bool{}
				for offset := int32(0); offset <= total; offset++ {
					page, err := f.assignments.List().SetOrder("id").SetLimit(1).SetOffset(offset).Do(f.ctx)
					Expect(err).NotTo(HaveOccurred())
					Expect(page.GetTotal()).To(Equal(total))
					if offset == total {
						Expect(page.GetItems()).To(BeEmpty())
						continue
					}
					Expect(page.GetItems()).To(HaveLen(1))
					id := page.GetItems()[0].GetId()
					Expect(seen[id]).To(BeFalse())
					seen[id] = true
				}
				Expect(seen).To(HaveLen(int(total)))
			}, Entry("one tenant", []string{"tenant-a"}, int32(2)), Entry("two tenants", []string{"tenant-a", "tenant-b"}, int32(3)),
				Entry("universal", []string(nil), int32(3)), Entry("empty", []string{}, int32(0)),
				Entry("SQL-looking tenant", []string{`tenant-a' OR true --`}, int32(0)))
			It("intersects caller filters and exclusion predicates for both historical and current rows", func() {
				var err error
				f.visibility, err = auth.NewVisibility().AddVisibleTenant("tenant-a").Build()
				Expect(err).NotTo(HaveOccurred())
				for _, filter := range []string{`this.spec.tenant.id == "tenant-b"`,
					`this.spec.billing_account.id == "account-b" && !has(this.status.effective_to_timestamp)`} {
					page, err := f.assignments.List().SetFilter(filter).Do(f.ctx)
					Expect(err).NotTo(HaveOccurred())
					Expect(page.GetTotal()).To(BeZero())
					Expect(page.GetItems()).To(BeEmpty())
				}
				page, err := f.assignments.List().SetFilter(`this.status.connection_state == 2 && has(this.status.effective_to_timestamp)`).Do(f.ctx)
				Expect(err).NotTo(HaveOccurred())
				Expect(page.GetTotal()).To(Equal(int32(1)))
				Expect(page.GetItems()[0].GetSpec().GetTenant().GetId()).To(Equal("tenant-a"))
				f.visibility = auth.TotalVisibility()
				page, err = f.assignments.List().SetFilter(`this.metadata.tenant != "tenant-b"`).Do(f.ctx)
				Expect(err).NotTo(HaveOccurred())
				Expect(page.GetTotal()).To(Equal(int32(2)))
			})
			It("rejects unknown and retired reference fields", func() {
				for _, filter := range []string{`this.spec.message == "x"`, `this.spec.tenant_id == "tenant-a"`} {
					err := f.tx.Savepoint(f.ctx, func(ctx context.Context) error {
						_, err := f.assignments.List().SetFilter(filter).Do(ctx)
						return err
					})
					var invalid *dao.ErrInvalidFilter
					Expect(errors.As(err, &invalid)).To(BeTrue())
				}
			})
		})

		DescribeTable("paginates closed history with a stable id tie breaker", func(count int) {
			ids := make([]string, count)
			for i := range count {
				ids[i] = fmt.Sprintf("hist-%03d", i)
				object := billingAssignmentFixture(ids[i], "tenant-a", account)
				object.GetStatus().SetEffectiveToTimestamp(object.GetStatus().GetEffectiveFromTimestamp())
				_, err := f.assignments.Create().SetObject(object).Do(f.ctx)
				Expect(err).NotTo(HaveOccurred())
			}
			var got []string
			for _, offset := range []int32{0, 100} {
				page, err := f.assignments.List().SetOrder("status.effective_from_timestamp desc, id").SetOffset(offset).SetLimit(100).Do(f.ctx)
				Expect(err).NotTo(HaveOccurred())
				Expect(page.GetTotal()).To(Equal(int32(count)))
				Expect(page.GetItems()).To(HaveLen(max(0, min(count-int(offset), 100))))
				for _, object := range page.GetItems() {
					got = append(got, object.GetId())
				}
			}
			Expect(got).To(Equal(ids))
		}, Entry("105 intervals", 105), Entry("exactly 100", 100))
	})
}

func billingAssignmentFixture(id, tenant string, account *privatev1.BillingAccount) *privatev1.TenantBillingAssignment {
	return privatev1.TenantBillingAssignment_builder{Id: id,
		Metadata: privatev1.Metadata_builder{Name: id, Tenant: tenant, Creator: "original-actor",
			Annotations: map[string]string{"osac.openshift.io/tenant": tenant},
		}.Build(),
		Spec: privatev1.TenantBillingAssignmentSpec_builder{
			Tenant: privatev1.TenantReference_builder{Id: tenant, Name: tenant}.Build(),
			BillingAccount: privatev1.BillingAccountReference_builder{Id: account.GetId(),
				Name: account.GetMetadata().GetName()}.Build(),
		}.Build(),
		Status: privatev1.TenantBillingAssignmentStatus_builder{
			ConnectionState:        privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED,
			EffectiveFromTimestamp: timestamppb.New(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
		}.Build(),
	}.Build()
}
