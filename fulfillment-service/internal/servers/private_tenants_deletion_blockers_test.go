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
	"fmt"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Tenant archive dependency blockers", func() {
	var (
		tenants  *PrivateTenantsServer
		projects *PrivateProjectsServer
		accounts *PrivateServiceAccountsServer
	)

	BeforeEach(func() {
		var err error
		tenants, err = NewPrivateTenantsServer().SetLogger(logger).
			SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
		Expect(err).NotTo(HaveOccurred())
		projects, err = NewPrivateProjectsServer().SetLogger(logger).
			SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
		Expect(err).NotTo(HaveOccurred())
		accounts, err = NewPrivateServiceAccountsServer().SetLogger(logger).
			SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
		Expect(err).NotTo(HaveOccurred())
	})

	inTx := func(fn func(context.Context, database.Tx) error) error {
		_, err := database.WithNewTx(ctx, func(ctx context.Context) (struct{}, error) {
			tx, err := database.TxFromContext(ctx)
			if err == nil {
				err = fn(ctx, tx)
			}
			return struct{}{}, err
		})
		return err
	}
	commitSetup := func() {
		GinkgoHelper()
		Expect(suiteTx.End(ctx)).To(Succeed())
		suiteTx = nil
		ctx = database.TxManagerIntoContext(context.Background(), tm)
	}
	createTenant := func(withFinalizer, billed bool) string {
		GinkgoHelper()
		metadata := privatev1.Metadata_builder{Name: "dependency-tenant"}.Build()
		if withFinalizer {
			metadata.SetFinalizers([]string{finalizers.TenantLifecycle})
		}
		created, err := tenants.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{Metadata: metadata}.Build(),
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		id := created.GetObject().GetId()
		listed, err := projects.List(ctx, privatev1.ProjectsListRequest_builder{
			Filter: new(fmt.Sprintf("this.metadata.tenant == %q", id)),
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		for _, project := range listed.GetItems() {
			_, err = projects.Delete(ctx, privatev1.ProjectsDeleteRequest_builder{Id: project.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
		}
		if billed {
			tx, err := database.TxFromContext(ctx)
			Expect(err).NotTo(HaveOccurred())
			previous := ""
			for range 2 {
				account, err := database.UpsertBillingAccount(ctx, tx, database.BillingAccountUpsert{
					ID: uuid.New(), ProviderKey: "m360", ProviderAccountReference: uuid.New(), State: "ACTIVE",
				})
				Expect(err).NotTo(HaveOccurred())
				previous, err = database.CloseAndInsertTenantBillingAssignment(ctx, tx, previous,
					database.TenantBillingAssignmentInsert{
						ID: uuid.New(), Name: uuid.New(), TenantID: id,
						BillingAccountID: account, ConnectionState: "CONNECTED",
					})
				Expect(err).NotTo(HaveOccurred())
			}
		}
		return id
	}
	createAccount := func(ctx context.Context, tenant string) (string, error) {
		created, err := accounts.Create(ctx, privatev1.ServiceAccountsCreateRequest_builder{
			Object: privatev1.ServiceAccount_builder{
				Metadata: privatev1.Metadata_builder{Name: "automation", Tenant: tenant}.Build(),
			}.Build(),
		}.Build())
		if err != nil {
			return "", err
		}
		return created.GetObject().GetId(), nil
	}
	completeDeletion := func(ctx context.Context, tenant string, withFinalizer bool) error {
		if !withFinalizer {
			_, err := tenants.Delete(ctx, privatev1.TenantsDeleteRequest_builder{Id: tenant}.Build())
			return err
		}
		read, err := tenants.Get(ctx, privatev1.TenantsGetRequest_builder{Id: tenant}.Build())
		if err != nil {
			return err
		}
		read.GetObject().GetMetadata().SetFinalizers(nil)
		_, err = tenants.Update(ctx, privatev1.TenantsUpdateRequest_builder{
			Object: read.GetObject(), UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.finalizers"}},
			Lock: true,
		}.Build())
		return err
	}
	forceBillingFirst := func(tenant string) {
		GinkgoHelper()
		tx, err := database.TxFromContext(ctx)
		Expect(err).NotTo(HaveOccurred())
		var trigger string
		Expect(tx.QueryRow(ctx, `select tgname from pg_trigger join pg_constraint on tgconstraint = pg_constraint.oid
			where tgrelid = 'tenants'::regclass and (tgtype & 8) <> 0
			and conname = 'tenant_billing_assignments_tenant_id_fkey'`).Scan(&trigger)).To(Succeed())
		// AFTER triggers run in name order. Rename only the billing action trigger in this disposable database;
		// every FK remains immediate and enforced, including concurrent INSERT checks.
		_, err = tx.Exec(ctx, `alter trigger `+pgx.Identifier{trigger}.Sanitize()+
			` on tenants rename to "000_billing_first"`)
		Expect(err).NotTo(HaveOccurred())
		err = tx.Savepoint(ctx, func(ctx context.Context) error {
			tx, err := database.TxFromContext(ctx)
			if err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `delete from tenants where id = $1`, tenant)
			return err
		})
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal(pgerrcode.ForeignKeyViolation))
		Expect(pgErr.ConstraintName).To(Equal("tenant_billing_assignments_tenant_id_fkey"),
			"prove the regression reaches billing recovery, not an incidental earlier FK")
	}
	snapshot := func(ctx context.Context, tx database.Tx, tenant string) (string, string) {
		GinkgoHelper()
		var object, history string
		Expect(tx.QueryRow(ctx, `select to_jsonb(t)::text from tenants t where id = $1`, tenant).
			Scan(&object)).To(Succeed())
		Expect(tx.QueryRow(ctx, `select coalesce(jsonb_agg(to_jsonb(a) order by id), '[]'::jsonb)::text
			from tenant_billing_assignments a where tenant_id = $1`, tenant).Scan(&history)).To(Succeed())
		var archived, projectCount int
		Expect(tx.QueryRow(ctx, `select count(*) from archived_tenants where id = $1`, tenant).
			Scan(&archived)).To(Succeed())
		Expect(archived).To(BeZero())
		Expect(tx.QueryRow(ctx, `select count(*) from projects where tenant = $1`, tenant).
			Scan(&projectCount)).To(Succeed())
		Expect(projectCount).To(BeZero(), "the service account must be an independent blocker")
		return object, history
	}

	DescribeTable("preserves a service-account blocker and recovers after its cleanup",
		func(withFinalizer, billed, billingFirst bool) {
			tenant := createTenant(withFinalizer, billed)
			account, err := createAccount(ctx, tenant)
			Expect(err).NotTo(HaveOccurred())
			if billingFirst {
				forceBillingFirst(tenant)
			}
			if withFinalizer {
				_, err = tenants.Delete(ctx, privatev1.TenantsDeleteRequest_builder{Id: tenant}.Build())
				Expect(err).NotTo(HaveOccurred())
			}
			tx, err := database.TxFromContext(ctx)
			Expect(err).NotTo(HaveOccurred())
			objectBefore, historyBefore := snapshot(ctx, tx, tenant)
			commitSetup()

			err = inTx(func(ctx context.Context, _ database.Tx) error {
				return completeDeletion(ctx, tenant, withFinalizer)
			})
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
			Expect(inTx(func(ctx context.Context, tx database.Tx) error {
				objectAfter, historyAfter := snapshot(ctx, tx, tenant)
				Expect(objectAfter).To(Equal(objectBefore), "timestamp, version and finalizers must roll back")
				Expect(historyAfter).To(Equal(historyBefore), "both closed and current history must survive")
				read, err := accounts.Get(ctx, privatev1.ServiceAccountsGetRequest_builder{Id: account}.Build())
				if err != nil {
					return err
				}
				Expect(read.GetObject().GetMetadata().GetFinalizers()).To(ConsistOf(finalizers.Controller))
				return nil
			})).To(Succeed())

			Expect(inTx(func(ctx context.Context, _ database.Tx) error {
				_, err := accounts.Delete(ctx, privatev1.ServiceAccountsDeleteRequest_builder{Id: account}.Build())
				return err
			})).To(Succeed())
			err = inTx(func(ctx context.Context, _ database.Tx) error {
				return completeDeletion(ctx, tenant, withFinalizer)
			})
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition),
				"a deleting account is still a blocker until its cleanup finalizer completes")
			Expect(inTx(func(ctx context.Context, tx database.Tx) error {
				objectAfter, historyAfter := snapshot(ctx, tx, tenant)
				Expect(objectAfter).To(Equal(objectBefore))
				Expect(historyAfter).To(Equal(historyBefore))
				return nil
			})).To(Succeed())
			Expect(inTx(func(ctx context.Context, _ database.Tx) error {
				// Model successful controller/Keycloak cleanup through the DAO. This test proves the database
				// lifecycle, not real Keycloak deletion; the account must be archived, not merely soft-deleted.
				accountDAO, err := dao.NewGenericDAO[*privatev1.ServiceAccount]().SetLogger(logger).
					SetTenancyLogic(tenancy).Build()
				if err != nil {
					return err
				}
				read, err := accountDAO.Get().SetId(account).SetLock(true).Do(ctx)
				if err != nil {
					return err
				}
				read.GetObject().GetMetadata().SetFinalizers(nil)
				_, err = accountDAO.Update().SetObject(read.GetObject()).Do(ctx)
				return err
			})).To(Succeed())
			Expect(inTx(func(ctx context.Context, _ database.Tx) error {
				return completeDeletion(ctx, tenant, withFinalizer)
			})).To(Succeed())
			Expect(inTx(func(ctx context.Context, tx database.Tx) error {
				var live, archived, history, open int
				Expect(tx.QueryRow(ctx, `select count(*) from tenants where id = $1`, tenant).Scan(&live)).To(Succeed())
				Expect(tx.QueryRow(ctx, `select count(*) from archived_tenants where id = $1`, tenant).
					Scan(&archived)).To(Succeed())
				Expect(tx.QueryRow(ctx, `select count(*), count(*) filter (where effective_to is null)
					from tenant_billing_assignments where tenant_id = $1`, tenant).Scan(&history, &open)).To(Succeed())
				Expect(open).To(BeZero())
				if billed {
					Expect(live).To(Equal(1))
					Expect(archived).To(BeZero())
					Expect(history).To(Equal(2))
					read, err := tenants.Get(ctx, privatev1.TenantsGetRequest_builder{Id: tenant}.Build())
					if err != nil {
						return err
					}
					Expect(read.GetObject().GetMetadata().GetFinalizers()).To(BeEmpty())
					Expect(read.GetObject().GetMetadata().GetDeletionTimestamp()).NotTo(BeNil())
				} else {
					Expect(live).To(BeZero())
					Expect(archived).To(Equal(1))
					Expect(history).To(BeZero())
				}
				return nil
			})).To(Succeed())
		},
		Entry("direct Delete without billing", false, false, false),
		Entry("direct Delete with billing, normal order", false, true, false),
		Entry("direct Delete with billing first", false, true, true),
		Entry("last-finalizer Update without billing", true, false, false),
		Entry("last-finalizer Update with billing, normal order", true, true, false),
		Entry("last-finalizer Update with billing first", true, true, true),
	)

	DescribeTable("sees a concurrent service-account creation before completing deletion",
		func(withFinalizer bool) {
			tenant := createTenant(withFinalizer, true)
			forceBillingFirst(tenant)
			if withFinalizer {
				_, err := tenants.Delete(ctx, privatev1.TenantsDeleteRequest_builder{Id: tenant}.Build())
				Expect(err).NotTo(HaveOccurred())
			}
			tx, err := database.TxFromContext(ctx)
			Expect(err).NotTo(HaveOccurred())
			objectBefore, historyBefore := snapshot(ctx, tx, tenant)
			commitSetup()

			creator, err := tm.Begin(ctx)
			Expect(err).NotTo(HaveOccurred())
			createCtx := database.TxIntoContext(ctx, creator)
			creatorEnded := false
			DeferCleanup(func() {
				if !creatorEnded {
					Expect(creator.End(createCtx)).To(Succeed())
				}
			})
			var creatorPID int32
			Expect(creator.QueryRow(createCtx, `select pg_backend_pid()`).Scan(&creatorPID)).To(Succeed())
			if withFinalizer {
				// A normal DAO create now rejects a deleting tenant. Model a legacy/direct-SQL writer
				// here to keep exercising archive's independent FK lock and fresh-snapshot safeguard.
				// The application-path rejection is covered by Service-account tenant liveness at creation.
				_, err = creator.Exec(createCtx, `insert into service_accounts (id, name, tenant, data)
					values ($1, 'concurrent-legacy', $2, '{}'::jsonb)`, uuid.New(), tenant)
			} else {
				_, err = createAccount(createCtx, tenant)
			}
			Expect(err).NotTo(HaveOccurred())
			deleteCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			DeferCleanup(cancel)
			result := make(chan error, 1)
			go func() {
				defer GinkgoRecover()
				_, err := database.WithNewTx(deleteCtx, func(ctx context.Context) (struct{}, error) {
					return struct{}{}, completeDeletion(ctx, tenant, withFinalizer)
				})
				result <- err
			}()
			Eventually(func() (bool, error) {
				var blocked bool
				err := testPool.QueryRow(deleteCtx, `select exists(select 1 from pg_stat_activity
					where $1 = any(pg_blocking_pids(pid)))`, creatorPID).Scan(&blocked)
				return blocked, err
			}, 5*time.Second, 10*time.Millisecond).Should(BeTrue(), "observe real FK/tenant lock contention")
			Expect(creator.End(createCtx)).To(Succeed())
			creatorEnded = true
			var deleteErr error
			Eventually(result, 10*time.Second).Should(Receive(&deleteErr))
			Expect(status.Code(deleteErr)).To(Equal(codes.FailedPrecondition))
			Expect(inTx(func(ctx context.Context, tx database.Tx) error {
				objectAfter, historyAfter := snapshot(ctx, tx, tenant)
				Expect(objectAfter).To(Equal(objectBefore))
				Expect(historyAfter).To(Equal(historyBefore))
				return nil
			})).To(Succeed())
		},
		Entry("direct Delete", false),
		Entry("last-finalizer Update", true),
	)

	It("checks new restrictive foreign keys without a hard-coded tenant column or table list", func() {
		tenant := createTenant(false, true)
		forceBillingFirst(tenant)
		tx, err := database.TxFromContext(ctx)
		Expect(err).NotTo(HaveOccurred())
		_, err = tx.Exec(ctx, `create table extra_tenant_dependency (
			owner text not null references tenants(name) on delete restrict)`)
		Expect(err).NotTo(HaveOccurred())
		_, err = tx.Exec(ctx, `insert into extra_tenant_dependency(owner) values ($1)`, tenant)
		Expect(err).NotTo(HaveOccurred())
		commitSetup()
		err = inTx(func(ctx context.Context, _ database.Tx) error {
			return completeDeletion(ctx, tenant, false)
		})
		Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
	})

	It("inventories every tenant foreign key in the fully migrated PostgreSQL schema", func() {
		tx, err := database.TxFromContext(ctx)
		Expect(err).NotTo(HaveOccurred())
		var inventory string
		Expect(tx.QueryRow(ctx, `select string_agg(format('%s: %s [%s]',
			conrelid::regclass, pg_get_constraintdef(oid), conname), E'\n' order by conrelid::regclass::text)
			from pg_constraint where contype = 'f' and confrelid = 'tenants'::regclass`).Scan(&inventory)).To(Succeed())
		fmt.Fprintln(GinkgoWriter, inventory)
		Expect(inventory).To(ContainSubstring("service_accounts: FOREIGN KEY (tenant) REFERENCES tenants(name)"))
		Expect(inventory).To(ContainSubstring("tenant_billing_assignments: FOREIGN KEY (tenant_id) REFERENCES tenants(name)"))
		Expect(inventory).To(ContainSubstring("tenants: FOREIGN KEY (tenant) REFERENCES tenants(name)"))
		Expect(inventory).To(ContainSubstring("active_tenants: FOREIGN KEY (id) REFERENCES tenants(id) ON DELETE CASCADE"))
		Expect(inventory).To(ContainSubstring("tenant_domains: FOREIGN KEY (tenant) REFERENCES tenants(name) ON DELETE CASCADE"))
	})
})
