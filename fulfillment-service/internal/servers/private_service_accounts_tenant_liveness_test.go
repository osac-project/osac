/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Service-account tenant liveness at creation", func() {
	var (
		tenants  *PrivateTenantsServer
		accounts *PrivateServiceAccountsServer
	)
	BeforeEach(func() {
		var err error
		tenants, err = NewPrivateTenantsServer().SetLogger(logger).
			SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
		Expect(err).NotTo(HaveOccurred())
		accounts, err = NewPrivateServiceAccountsServer().SetLogger(logger).
			SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
		Expect(err).NotTo(HaveOccurred())
	})
	createAccount := func(ctx context.Context, tenant string) (*privatev1.ServiceAccountsCreateResponse, error) {
		return accounts.Create(ctx, privatev1.ServiceAccountsCreateRequest_builder{
			Object: privatev1.ServiceAccount_builder{
				Metadata: privatev1.Metadata_builder{Name: "automation", Tenant: tenant}.Build(),
			}.Build(),
		}.Build())
	}
	createTenant := func(withFinalizer, billed bool) string {
		GinkgoHelper()
		metadata := privatev1.Metadata_builder{Name: "creation-liveness"}.Build()
		if withFinalizer {
			metadata.SetFinalizers([]string{finalizers.TenantLifecycle})
		}
		created, err := tenants.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{Metadata: metadata}.Build(),
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		tenant := created.GetObject().GetId()
		// Remove the automatic default project to isolate the independent service-account dependency.
		_, err = suiteTx.Exec(ctx, `delete from projects where tenant = $1`, tenant)
		Expect(err).NotTo(HaveOccurred())
		if billed {
			account, err := database.UpsertBillingAccount(ctx, suiteTx, database.BillingAccountUpsert{
				ID: uuid.New(), ProviderKey: "m360", ProviderAccountReference: uuid.New(), State: "ACTIVE",
			})
			Expect(err).NotTo(HaveOccurred())
			_, err = database.CloseAndInsertTenantBillingAssignment(ctx, suiteTx, "",
				database.TenantBillingAssignmentInsert{
					ID: uuid.New(), Name: uuid.New(), TenantID: tenant,
					BillingAccountID: account, ConnectionState: "CONNECTED",
				})
			Expect(err).NotTo(HaveOccurred())
		}
		return tenant
	}
	commitSetup := func() {
		GinkgoHelper()
		Expect(suiteTx.End(ctx)).To(Succeed())
		suiteTx = nil
		ctx = database.TxManagerIntoContext(context.Background(), tm)
	}
	begin := func() (context.Context, database.Tx) {
		GinkgoHelper()
		tx, err := tm.Begin(ctx)
		Expect(err).NotTo(HaveOccurred())
		txCtx := database.TxIntoContext(ctx, tx)
		return txCtx, tx
	}
	completeFinalizer := func(ctx context.Context, tenant string) error {
		read, err := tenants.Get(ctx, privatev1.TenantsGetRequest_builder{Id: tenant}.Build())
		if err != nil {
			return err
		}
		read.GetObject().GetMetadata().SetFinalizers(nil)
		_, err = tenants.Update(ctx, privatev1.TenantsUpdateRequest_builder{
			Object: read.GetObject(), Lock: true,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.finalizers"}},
		}.Build())
		return err
	}
	waitForBlock := func(waitCtx context.Context, waiter, blocker int32) {
		GinkgoHelper()
		Eventually(func() (bool, error) {
			var blocked bool
			err := testPool.QueryRow(waitCtx, `select $1 = any(pg_blocking_pids($2))`, blocker, waiter).
				Scan(&blocked)
			return blocked, err
		}, 5*time.Second, 10*time.Millisecond).Should(BeTrue(), "observe the exact writer waiting on the tenant lock")
	}

	DescribeTable("rejects a create waiting behind deletion when deletion commits first",
		func(withFinalizer, complete, billed bool) {
			tenant := createTenant(withFinalizer, billed)
			if complete {
				_, err := tenants.Delete(ctx, privatev1.TenantsDeleteRequest_builder{Id: tenant}.Build())
				Expect(err).NotTo(HaveOccurred())
			}
			commitSetup()
			deleteCtx, deleting := begin()
			deleteEnded := false
			DeferCleanup(func() {
				if !deleteEnded {
					Expect(deleting.End(deleteCtx)).To(Succeed())
				}
			})
			var deletePID int32
			Expect(deleting.QueryRow(deleteCtx, `select pg_backend_pid()`).Scan(&deletePID)).To(Succeed())
			if complete {
				Expect(completeFinalizer(deleteCtx, tenant)).To(Succeed())
			} else {
				_, err := tenants.Delete(deleteCtx, privatev1.TenantsDeleteRequest_builder{Id: tenant}.Build())
				Expect(err).NotTo(HaveOccurred())
			}

			createCtx, creator := begin()
			waitCtx, cancel := context.WithTimeout(createCtx, 15*time.Second)
			DeferCleanup(cancel)
			var createPID int32
			Expect(creator.QueryRow(waitCtx, `select pg_backend_pid()`).Scan(&createPID)).To(Succeed())
			result := make(chan error, 1)
			go func() {
				defer GinkgoRecover()
				_, createErr := createAccount(waitCtx, tenant)
				endErr := creator.End(waitCtx)
				if endErr != nil {
					result <- endErr
					return
				}
				result <- createErr
			}()
			waitForBlock(waitCtx, createPID, deletePID)
			Expect(deleting.End(deleteCtx)).To(Succeed())
			deleteEnded = true
			var createErr error
			Eventually(result, 10*time.Second).Should(Receive(&createErr))
			Expect(status.Code(createErr)).To(Equal(codes.InvalidArgument))
			var count int
			Expect(testPool.QueryRow(ctx, `select count(*) from service_accounts where tenant = $1`, tenant).
				Scan(&count)).To(Succeed())
			Expect(count).To(BeZero(), "no account may commit under a retained deletion-marked identity")
			if billed {
				var deleted bool
				Expect(testPool.QueryRow(ctx, `select deletion_timestamp <> 'epoch' from tenants where name = $1`, tenant).
					Scan(&deleted)).To(Succeed())
				Expect(deleted).To(BeTrue())
				Expect(testPool.QueryRow(ctx, `select count(*) from tenant_billing_assignments
					where tenant_id = $1 and effective_to is not null`, tenant).Scan(&count)).To(Succeed())
				Expect(count).To(Equal(1), "preserve the read-only billing history")
			}
		},
		Entry("billed direct deletion", false, false, true),
		Entry("billed finalizer completion", true, true, true),
		Entry("billed deletion acceptance with cleanup pending", true, false, true),
		Entry("unbilled direct deletion", false, false, false),
		Entry("unbilled finalizer completion", true, true, false),
	)

	It("preserves a live tenant when a service-account insert commits before deletion", func() {
		tenant := createTenant(false, true)
		commitSetup()
		createCtx, creator := begin()
		creatorEnded := false
		DeferCleanup(func() {
			if !creatorEnded {
				Expect(creator.End(createCtx)).To(Succeed())
			}
		})
		var createPID int32
		Expect(creator.QueryRow(createCtx, `select pg_backend_pid()`).Scan(&createPID)).To(Succeed())
		_, err := createAccount(createCtx, tenant)
		Expect(err).NotTo(HaveOccurred())
		deleteCtx, deleting := begin()
		waitCtx, cancel := context.WithTimeout(deleteCtx, 15*time.Second)
		DeferCleanup(cancel)
		var deletePID int32
		Expect(deleting.QueryRow(waitCtx, `select pg_backend_pid()`).Scan(&deletePID)).To(Succeed())
		result := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			_, deleteErr := tenants.Delete(waitCtx, privatev1.TenantsDeleteRequest_builder{Id: tenant}.Build())
			endErr := deleting.End(waitCtx)
			if endErr != nil {
				result <- endErr
				return
			}
			result <- deleteErr
		}()
		waitForBlock(waitCtx, deletePID, createPID)
		Expect(creator.End(createCtx)).To(Succeed())
		creatorEnded = true
		var deleteErr error
		Eventually(result, 10*time.Second).Should(Receive(&deleteErr))
		Expect(status.Code(deleteErr)).To(Equal(codes.FailedPrecondition))
		var live bool
		Expect(testPool.QueryRow(ctx, `select deletion_timestamp = 'epoch' from tenants where name = $1`, tenant).
			Scan(&live)).To(Succeed())
		Expect(live).To(BeTrue())
	})

	DescribeTable("preserves live-tenant and reserved-system creation",
		func(tenant string) {
			created, err := createAccount(ctx, tenant)
			Expect(err).NotTo(HaveOccurred())
			if tenant == "" {
				tenant = testTenant
			}
			Expect(created.GetObject().GetMetadata().GetTenant()).To(Equal(tenant))
			Expect(created.GetObject().GetMetadata().GetAnnotations()["osac.openshift.io/tenant"]).To(Equal(tenant))
		},
		Entry("default tenant normalized before DAO validation", ""),
		Entry("explicit live tenant", testTenant),
		Entry("reserved system tenant", auth.SystemTenant),
	)

	It("keeps dry-run creation write-free", func() {
		created, err := createAccount(dryRunCtx(), "")
		Expect(err).NotTo(HaveOccurred())
		Expect(created.GetObject().GetMetadata().GetTenant()).To(Equal(testTenant))
		var count int
		Expect(suiteTx.QueryRow(ctx, `select count(*) from service_accounts`).Scan(&count)).To(Succeed())
		Expect(count).To(BeZero())
	})
})
