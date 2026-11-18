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
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
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
			seedTenantBillingInterval(ctx, tenant, seedTenantBillingAccount(ctx), "liveness-assignment", false)
		}
		return tenant
	}
	commitSetup := func() {
		GinkgoHelper()
		Expect(suiteTx.End(ctx)).To(Succeed())
		suiteTx = nil
		ctx = database.TxManagerIntoContext(context.Background(), tm)
	}
	begin := func() (context.Context, database.Tx, func() error) {
		GinkgoHelper()
		txCtx, cancelTx := context.WithTimeout(ctx, 20*time.Second)
		DeferCleanup(cancelTx)
		tx, err := tm.Begin(txCtx)
		ended := false
		end := func() error {
			ended = true
			endCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return tx.End(endCtx)
		}
		if err == nil {
			DeferCleanup(func() {
				if !ended {
					rollbackErr := context.Canceled
					tx.ReportError(&rollbackErr)
					// A canceled wait can already have closed the connection. End still returns it to the pool.
					if err := end(); !errors.Is(err, pgconn.ErrConnClosed) {
						Expect(err).NotTo(HaveOccurred())
					}
				}
			})
		}
		Expect(err).NotTo(HaveOccurred())
		return database.TxIntoContext(txCtx, tx), tx, end
	}
	startWaiter := func(waitCtx context.Context, run func(context.Context) error) func() error {
		waitCtx, cancel := context.WithCancel(waitCtx)
		done := make(chan struct{})
		var result error
		// This runs before the transaction cleanup registered by begin.
		DeferCleanup(func() {
			cancel()
			<-done
		})
		go func() {
			defer close(done)
			defer GinkgoRecover()
			result = run(waitCtx)
		}()
		return func() error {
			GinkgoHelper()
			Eventually(done, 10*time.Second).Should(BeClosed())
			return result
		}
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
		probeCtx, cancel := context.WithTimeout(waitCtx, 5*time.Second)
		defer cancel()
		Eventually(func() (bool, error) {
			var blocked bool
			err := testPool.QueryRow(probeCtx, `select $1 = any(pg_blocking_pids($2))`, blocker, waiter).
				Scan(&blocked)
			return blocked, err
		}, 5*time.Second, 10*time.Millisecond).Should(BeTrue(), "observe the exact writer waiting on the tenant lock")
	}

	DescribeTable("rejects a create waiting behind deletion when deletion commits first",
		func(withFinalizer, complete, billed bool) {
			tenant := createTenant(withFinalizer, billed)
			var createdAt time.Time
			Expect(suiteTx.QueryRow(ctx, `select creation_timestamp from tenants where name=$1`, tenant).
				Scan(&createdAt)).To(Succeed())
			if complete {
				_, err := tenants.Delete(ctx, privatev1.TenantsDeleteRequest_builder{Id: tenant}.Build())
				Expect(err).NotTo(HaveOccurred())
			}
			commitSetup()
			deleteCtx, deleting, endDelete := begin()
			var deletePID int32
			Expect(deleting.QueryRow(deleteCtx, `select pg_backend_pid()`).Scan(&deletePID)).To(Succeed())
			if complete {
				Expect(completeFinalizer(deleteCtx, tenant)).To(Succeed())
			} else {
				_, err := tenants.Delete(deleteCtx, privatev1.TenantsDeleteRequest_builder{Id: tenant}.Build())
				Expect(err).NotTo(HaveOccurred())
			}

			createCtx, creator, endCreate := begin()
			var createPID int32
			Expect(creator.QueryRow(createCtx, `select pg_backend_pid()`).Scan(&createPID)).To(Succeed())
			wait := startWaiter(createCtx, func(c context.Context) error {
				_, err := createAccount(c, tenant)
				return err
			})
			waitForBlock(createCtx, createPID, deletePID)
			Expect(endDelete()).To(Succeed())
			createErr := wait()
			Expect(endCreate()).To(Succeed())
			Expect(status.Code(createErr)).To(Equal(codes.InvalidArgument))
			var count int
			Expect(testPool.QueryRow(ctx, `select count(*) from service_accounts where tenant = $1`, tenant).
				Scan(&count)).To(Succeed())
			Expect(count).To(BeZero(), "no account may commit under a deleting or physically absent tenant")
			pending := withFinalizer && !complete
			Expect(testPool.QueryRow(ctx, `select count(*) from tenants where name=$1`, tenant).
				Scan(&count)).To(Succeed())
			if pending {
				Expect(count).To(Equal(1))
				var deleted bool
				Expect(testPool.QueryRow(ctx, `select deletion_timestamp <> 'epoch' from tenants where name=$1`, tenant).
					Scan(&deleted)).To(Succeed())
				Expect(deleted).To(BeTrue())
			} else {
				Expect(count).To(BeZero(), "completed deletion must not retain a live Tenant for billing history")
			}
			Expect(testPool.QueryRow(ctx, `select count(*) from archived_tenants where name=$1`, tenant).
				Scan(&count)).To(Succeed())
			if pending {
				Expect(count).To(BeZero())
			} else {
				Expect(count).To(Equal(1))
			}
			marker, err := database.WithNewTx(ctx, func(c context.Context) (database.DeletedTenantIdentity, error) {
				marker, found, err := database.GetDeletedTenantIdentity(c, tenant)
				Expect(err).NotTo(HaveOccurred())
				Expect(found).To(BeTrue())
				return marker, err
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(marker.TenantCreatedAt).To(BeTemporally("==", createdAt))
			Expect(marker.DeletedBy).To(BeNil(), "the fixture's creator is not a deletion actor")
			Expect(marker.BillingRetained).To(Equal(billed))
			Expect(marker.DeletionTimestamp.Before(createdAt)).To(BeFalse())
			if pending {
				Expect(marker.FinalizedAt).To(BeNil())
			} else {
				Expect(marker.FinalizedAt).NotTo(BeNil())
				Expect(marker.FinalizedAt.Before(marker.DeletionTimestamp)).To(BeFalse())
				_, err = database.WithNewTx(ctx, func(c context.Context) (any, error) {
					return tenants.Get(c, privatev1.TenantsGetRequest_builder{Id: tenant}.Build())
				})
				Expect(status.Code(err)).To(Equal(codes.NotFound))
				publicTenants, err := NewTenantsServer().SetLogger(logger).
					SetTenancyLogic(tenancy).SetAttributionLogic(attribution).Build()
				Expect(err).NotTo(HaveOccurred())
				_, err = database.WithNewTx(ctx, func(c context.Context) (any, error) {
					return publicTenants.Get(c, publicv1.TenantsGetRequest_builder{Id: tenant}.Build())
				})
				Expect(status.Code(err)).To(Equal(codes.NotFound))
			}
			liveNames, err := database.ResolveLiveTenantNames(ctx, []string{tenant})
			Expect(err).NotTo(HaveOccurred())
			Expect(liveNames).To(BeEmpty(), "an unbilled marker revokes scope just as a billed marker does")
			if billed {
				Expect(testPool.QueryRow(ctx, `select count(*) from tenant_billing_assignments
					where tenant_id = $1 and effective_to is not null`, tenant).Scan(&count)).To(Succeed())
				Expect(count).To(Equal(1), "preserve the read-only billing history")
			}
			if !billed && !pending {
				_, err = database.WithNewTx(ctx, func(c context.Context) (any, error) {
					_, err := tenants.Create(c, privatev1.TenantsCreateRequest_builder{
						Object: privatev1.Tenant_builder{Metadata: privatev1.Metadata_builder{Name: tenant}.Build()}.Build(),
					}.Build())
					Expect(err).NotTo(HaveOccurred())
					unchanged, found, err := database.GetDeletedTenantIdentity(c, tenant)
					Expect(err).NotTo(HaveOccurred())
					Expect(found).To(BeTrue())
					Expect(unchanged).To(Equal(marker))
					return createAccount(c, tenant)
				})
				Expect(err).NotTo(HaveOccurred(), "revocation is not a permanent unbilled name ban")
				liveNames, err = database.ResolveLiveTenantNames(ctx, []string{tenant})
				Expect(err).NotTo(HaveOccurred())
				Expect(liveNames).To(Equal([]string{tenant}))
			}
		},
		Entry("billed direct deletion", false, false, true),
		Entry("billed finalizer completion", true, true, true),
		Entry("billed deletion acceptance with cleanup pending", true, false, true),
		Entry("unbilled direct deletion", false, false, false),
		Entry("unbilled finalizer completion", true, true, false),
		Entry("unbilled deletion acceptance with cleanup pending", true, false, false),
	)

	It("preserves a live tenant when a service-account insert commits before deletion", func() {
		tenant := createTenant(false, true)
		commitSetup()
		// Snapshot complete rows, not only deletion timestamps: a failed Delete
		// must roll back history closure, metadata, capture and archival together.
		const snapshotSQL = `select jsonb_build_object(
			'tenant', (select to_jsonb(t) from tenants t where name=$1),
			'history', (select jsonb_agg(to_jsonb(h) order by id) from tenant_billing_assignments h where tenant_id=$1),
			'marker', (select to_jsonb(m) from deleted_tenant_identities m where tenant_name=$1),
			'archive', (select jsonb_agg(to_jsonb(a) order by archival_timestamp) from archived_tenants a where name=$1)
		)::text`
		var before string
		Expect(testPool.QueryRow(ctx, snapshotSQL, tenant).Scan(&before)).To(Succeed())
		createCtx, creator, endCreate := begin()
		var createPID int32
		Expect(creator.QueryRow(createCtx, `select pg_backend_pid()`).Scan(&createPID)).To(Succeed())
		_, err := createAccount(createCtx, tenant)
		Expect(err).NotTo(HaveOccurred())
		deleteCtx, deleting, endDelete := begin()
		var deletePID int32
		Expect(deleting.QueryRow(deleteCtx, `select pg_backend_pid()`).Scan(&deletePID)).To(Succeed())
		wait := startWaiter(deleteCtx, func(c context.Context) error {
			_, err := tenants.Delete(c, privatev1.TenantsDeleteRequest_builder{Id: tenant}.Build())
			return err
		})
		waitForBlock(deleteCtx, deletePID, createPID)
		Expect(endCreate()).To(Succeed())
		deleteErr := wait()
		Expect(endDelete()).To(Succeed())
		Expect(status.Code(deleteErr)).To(Equal(codes.FailedPrecondition))
		var live bool
		Expect(testPool.QueryRow(ctx, `select deletion_timestamp = 'epoch' from tenants where name = $1`, tenant).
			Scan(&live)).To(Succeed())
		Expect(live).To(BeTrue())
		var after string
		Expect(testPool.QueryRow(ctx, snapshotSQL, tenant).Scan(&after)).To(Succeed())
		Expect(after).To(Equal(before), "the failed deletion must roll back every tenant/history/marker/archive change")
		var count int
		Expect(testPool.QueryRow(ctx, `select count(*) from service_accounts where tenant=$1`, tenant).
			Scan(&count)).To(Succeed())
		Expect(count).To(Equal(1), "the insert committed first remains a restrictive dependency")
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
