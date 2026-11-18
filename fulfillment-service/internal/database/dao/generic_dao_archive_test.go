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

	"github.com/jackc/pgx/v5/pgxpool"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// Exercise the production lifecycle against PostgreSQL, including independent transactions.
var _ = Describe("Tenant identity lifecycle", func() {
	var (
		ctx         context.Context
		pool        *pgxpool.Pool
		tm          database.TxManager
		tenants     *GenericDAO[*privatev1.Tenant]
		projects    *GenericDAO[*privatev1.Project]
		accounts    *GenericDAO[*privatev1.BillingAccount]
		assignments *GenericDAO[*privatev1.TenantBillingAssignment]
	)
	object := func(name string, finalizers ...string) *privatev1.Tenant {
		return privatev1.Tenant_builder{
			Id: name, Metadata: privatev1.Metadata_builder{
				Name: name, Tenant: name, Creator: "creator-not-deleter", Finalizers: finalizers,
			}.Build(),
		}.Build()
	}
	create := func(c context.Context, input *privatev1.Tenant) (*CreateResponse[*privatev1.Tenant], error) {
		created, err := tenants.Create().SetObject(input).Do(c)
		if err != nil {
			return nil, err
		}
		// The production tenant trigger creates its default project even for direct DAO callers.
		listed, err := projects.List().SetFilter(`this.metadata.tenant == "` + input.GetId() + `"`).Do(c)
		if err != nil {
			return nil, err
		}
		for _, project := range listed.GetItems() {
			if _, err = projects.Delete().SetId(project.GetId()).Do(c); err != nil {
				return nil, err
			}
		}
		return created, nil
	}
	seedBilling := func(c context.Context, tenant string, at time.Time) error {
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte("lifecycle-account")))
		name := "ba-" + hash[:60]
		account, err := accounts.Create().SetObject(privatev1.BillingAccount_builder{
			Id: "lifecycle-account", Metadata: privatev1.Metadata_builder{Name: name, Tenant: auth.SystemTenant, Creator: "system"}.Build(),
			Spec: privatev1.BillingAccountSpec_builder{}.Build(),
			Status: privatev1.BillingAccountStatus_builder{
				ProviderKey: "lifecycle", ProviderAccountReference: "native-lifecycle", DisplayName: "Lifecycle",
				State:                     privatev1.BillingAccountState_BILLING_ACCOUNT_STATE_ACTIVE,
				StateObservationTimestamp: timestamppb.New(at),
			}.Build(),
		}.Build()).Do(c)
		if err != nil {
			return err
		}
		_, err = assignments.Create().SetObject(privatev1.TenantBillingAssignment_builder{
			Id: "lifecycle-interval", Metadata: privatev1.Metadata_builder{
				Name: "lifecycle", Tenant: tenant, Annotations: map[string]string{"osac.openshift.io/tenant": tenant},
			}.Build(),
			Spec: privatev1.TenantBillingAssignmentSpec_builder{
				Tenant:         privatev1.TenantReference_builder{Id: tenant, Name: tenant}.Build(),
				BillingAccount: privatev1.BillingAccountReference_builder{Id: account.GetObject().GetId(), Name: name}.Build(),
			}.Build(),
			Status: privatev1.TenantBillingAssignmentStatus_builder{
				ConnectionState:        privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_PENDING,
				EffectiveFromTimestamp: timestamppb.New(at),
			}.Build(),
		}.Build()).Do(c)
		return err
	}
	inTx := func(actor string, fn func(context.Context, database.Tx) error) error {
		c := ctx
		if actor != "" {
			c = auth.ContextWithSubject(c, &auth.Subject{User: actor})
		}
		_, err := database.WithNewTx(c, func(c context.Context) (struct{}, error) {
			tx, err := database.TxFromContext(c)
			if err == nil {
				err = fn(c, tx)
			}
			return struct{}{}, err
		})
		return err
	}
	type identity struct {
		created, deleted time.Time
		actor            *string
		finalized        *time.Time
		billed           bool
	}
	readIdentity := func(c context.Context, tx database.Tx, name string) identity {
		GinkgoHelper()
		var result identity
		Expect(tx.QueryRow(c, `select tenant_created_at, deletion_timestamp, deleted_by, finalized_at,
			billing_retained from deleted_tenant_identities where tenant_name = $1`, name).
			Scan(&result.created, &result.deleted, &result.actor, &result.finalized, &result.billed)).To(Succeed())
		result.created, result.deleted = result.created.UTC(), result.deleted.UTC()
		if result.finalized != nil {
			at := result.finalized.UTC()
			result.finalized = &at
		}
		return result
	}
	BeforeEach(func() {
		ctx = context.Background()
		db, err := server.NewInstance().Build()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(db.Close)
		pool, err = db.Pool(ctx)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(pool.Close)
		tm, err = database.NewTxManager().SetLogger(logger).SetPool(pool).Build()
		Expect(err).NotTo(HaveOccurred())
		ctx = database.TxManagerIntoContext(ctx, tm)
		tenancy := auth.NewMockTenancyLogic(gomock.NewController(GinkgoT()))
		tenancy.EXPECT().DetermineVisibility(gomock.Any()).Return(auth.TotalVisibility(), nil).AnyTimes()
		tenants, err = NewGenericDAO[*privatev1.Tenant]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
		Expect(err).NotTo(HaveOccurred())
		projects, err = NewGenericDAO[*privatev1.Project]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
		Expect(err).NotTo(HaveOccurred())
		accounts, err = NewGenericDAO[*privatev1.BillingAccount]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
		Expect(err).NotTo(HaveOccurred())
		assignments, err = NewGenericDAO[*privatev1.TenantBillingAssignment]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
		Expect(err).NotTo(HaveOccurred())
	})

	DescribeTable("closes direct billed deletion at acceptance, preserves attribution and physically archives without publishing billing changes", func(finalizer bool) {
		floor := time.Date(2042, 1, 1, 0, 0, 0, 123456000, time.UTC)
		var accepted identity
		Expect(inTx("billing-deleter", func(c context.Context, tx database.Tx) error {
			input := object("billed")
			if finalizer {
				input.GetMetadata().SetFinalizers([]string{"controller"})
			}
			if _, err := create(c, input); err != nil {
				return err
			}
			if err := seedBilling(c, "billed", floor); err != nil {
				return err
			}
			if _, err := tenants.Delete().SetId("billed").Do(c); err != nil {
				return err
			}
			accepted = readIdentity(c, tx, "billed")
			Expect(accepted.billed).To(BeTrue())
			Expect(accepted.actor).To(Equal(new("billing-deleter")))
			Expect(accepted.deleted).To(BeTemporally("==", floor), "future history floors clamp the actual DAO clock")
			var closed time.Time
			var version int32
			Expect(tx.QueryRow(c, `select effective_to, version from tenant_billing_assignments where id = 'lifecycle-interval'`).
				Scan(&closed, &version)).To(Succeed())
			Expect(closed).To(BeTemporally("==", accepted.deleted))
			Expect(version).To(Equal(int32(1)))
			Expect(accepted.finalized == nil).To(Equal(finalizer))
			return nil
		})).To(Succeed())
		if finalizer {
			Expect(inTx("finalizer-controller", func(c context.Context, tx database.Tx) error {
				if _, err := tenants.Delete().SetId("billed").Do(c); err != nil {
					return err
				}
				Expect(readIdentity(c, tx, "billed")).To(Equal(accepted))
				current, err := tenants.Get().SetId("billed").Do(c)
				if err != nil {
					return err
				}
				current.GetObject().GetMetadata().SetFinalizers(nil)
				current.GetObject().GetMetadata().SetCreationTimestamp(timestamppb.New(time.Unix(1, 0)))
				current.GetObject().GetMetadata().SetDeletionTimestamp(timestamppb.New(time.Unix(2, 0)))
				_, err = tenants.Update().SetObject(current.GetObject()).Do(c)
				return err
			})).To(Succeed())
		}
		Expect(inTx("", func(c context.Context, tx database.Tx) error {
			final := readIdentity(c, tx, "billed")
			Expect(final.actor).To(Equal(accepted.actor))
			Expect(final.created).To(Equal(accepted.created))
			Expect(final.deleted).To(Equal(accepted.deleted))
			Expect(final.finalized).NotTo(BeNil())
			Expect(*final.finalized).To(BeTemporally(">=", floor))
			var live, archived, billingChanges, tenantChanges, version int
			Expect(tx.QueryRow(c, `select count(*) from tenants where name = 'billed'`).Scan(&live)).To(Succeed())
			Expect(tx.QueryRow(c, `select count(*) from archived_tenants where name = 'billed'`).Scan(&archived)).To(Succeed())
			Expect(tx.QueryRow(c, `select count(*) from changes where "table" in ('billing_accounts', 'tenant_billing_assignments')`).Scan(&billingChanges)).To(Succeed())
			Expect(tx.QueryRow(c, `select count(*) from changes where "table" = 'tenants' and data->>'id' = 'billed'`).Scan(&tenantChanges)).To(Succeed())
			Expect(tx.QueryRow(c, `select version from tenant_billing_assignments where id = 'lifecycle-interval'`).Scan(&version)).To(Succeed())
			Expect(live).To(BeZero())
			Expect(archived).To(Equal(1))
			Expect(billingChanges).To(BeZero(), "committed deletion closure is also nonpublishing")
			Expect(tenantChanges).To(BeNumerically(">", 0), "existing nonbilling enqueue remains enabled")
			Expect(version).To(Equal(1), "repeated deletion/finalization cannot rewrite a closed interval")
			return nil
		})).To(Succeed())
		err := inTx("", func(c context.Context, _ database.Tx) error {
			_, err := tenants.Create().SetObject(object("billed")).Do(c)
			return err
		})
		Expect(errors.Is(err, database.ErrTenantIdentityRetained)).To(BeTrue())
	}, Entry("immediate Delete", false), Entry("last-finalizer Update", true))

	It("captures a legacy deleting tenant and closes its later current interval through direct finalizer Update", func() {
		floor := time.Date(2043, 1, 1, 0, 0, 0, 0, time.UTC)
		Expect(inTx("", func(c context.Context, tx database.Tx) error {
			if _, err := create(c, object("legacy-finalizer", "controller")); err != nil {
				return err
			}
			if err := seedBilling(c, "legacy-finalizer", floor); err != nil {
				return err
			}
			_, err := tx.Exec(c, `update tenants set deletion_timestamp = creation_timestamp where id = 'legacy-finalizer'`)
			return err
		})).To(Succeed())
		Expect(inTx("not-the-original-deleter", func(c context.Context, tx database.Tx) error {
			current, err := tenants.Get().SetId("legacy-finalizer").Do(c)
			if err != nil {
				return err
			}
			storedDeletion := current.GetObject().GetMetadata().GetDeletionTimestamp().AsTime()
			current.GetObject().GetMetadata().SetFinalizers(nil)
			if _, err = tenants.Update().SetObject(current.GetObject()).Do(c); err != nil {
				return err
			}
			marker := readIdentity(c, tx, "legacy-finalizer")
			Expect(marker.actor).To(BeNil())
			Expect(marker.deleted).To(BeTemporally("==", storedDeletion))
			Expect(marker.finalized).NotTo(BeNil())
			Expect(*marker.finalized).To(BeTemporally(">=", floor))
			var closed time.Time
			Expect(tx.QueryRow(c, `select effective_to from tenant_billing_assignments where id = 'lifecycle-interval'`).Scan(&closed)).To(Succeed())
			Expect(closed).To(BeTemporally("==", floor), "legacy current closure may be later than original deletion")
			return nil
		})).To(Succeed())
	})

	It("captures first acceptance and preserves its actor and timestamp through repeated Delete and finalization", func() {
		var accepted identity
		Expect(inTx("first-deleter", func(c context.Context, tx database.Tx) error {
			input := object("identity", "controller")
			input.GetMetadata().SetCreationTimestamp(timestamppb.New(time.Unix(1, 0)))
			created, err := create(c, input)
			if err != nil {
				return err
			}
			Expect(created.GetObject().GetMetadata().GetCreationTimestamp().AsTime()).NotTo(Equal(time.Unix(1, 0)))
			_, err = tenants.Delete().SetId("identity").Do(c)
			if err != nil {
				return err
			}
			accepted = readIdentity(c, tx, "identity")
			Expect(accepted.actor).To(Equal(new("first-deleter")))
			Expect(accepted.created).To(BeTemporally("==", created.GetObject().GetMetadata().GetCreationTimestamp().AsTime()))
			Expect(accepted.deleted).To(BeTemporally(">=", accepted.created))
			Expect(accepted.finalized).To(BeNil())
			return nil
		})).To(Succeed())
		Expect(inTx("finalizer-controller", func(c context.Context, tx database.Tx) error {
			before, err := tenants.Get().SetId("identity").Do(c)
			if err != nil {
				return err
			}
			_, err = tenants.Delete().SetId("identity").Do(c)
			if err != nil {
				return err
			}
			Expect(readIdentity(c, tx, "identity")).To(Equal(accepted))
			current, err := tenants.Get().SetId("identity").Do(c)
			if err != nil {
				return err
			}
			Expect(current.GetObject().GetMetadata().GetVersion()).To(Equal(before.GetObject().GetMetadata().GetVersion()))
			current.GetObject().GetMetadata().SetFinalizers(nil)
			current.GetObject().GetMetadata().SetCreationTimestamp(timestamppb.New(time.Unix(2, 0)))
			current.GetObject().GetMetadata().SetDeletionTimestamp(timestamppb.New(time.Unix(3, 0)))
			_, err = tenants.Update().SetObject(current.GetObject()).Do(c)
			if err != nil {
				return err
			}
			final := readIdentity(c, tx, "identity")
			Expect(final.created).To(Equal(accepted.created))
			Expect(final.deleted).To(Equal(accepted.deleted))
			Expect(final.actor).To(Equal(accepted.actor))
			Expect(final.finalized).NotTo(BeNil())
			Expect(*final.finalized).To(BeTemporally(">=", final.deleted))
			var live, archived int
			Expect(tx.QueryRow(c, `select count(*) from tenants where name = 'identity'`).Scan(&live)).To(Succeed())
			Expect(tx.QueryRow(c, `select count(*) from archived_tenants where name = 'identity'`).Scan(&archived)).To(Succeed())
			Expect(live).To(BeZero())
			Expect(archived).To(Equal(1))
			return nil
		})).To(Succeed())
	})

	DescribeTable("keeps an unknown actor NULL", func(legacy bool) {
		Expect(inTx("", func(c context.Context, tx database.Tx) error {
			_, err := create(c, object("unknown", "controller"))
			if err == nil && legacy {
				_, err = tx.Exec(c, `update tenants set deletion_timestamp = creation_timestamp where id = 'unknown'`)
			}
			return err
		})).To(Succeed())
		actor := ""
		if legacy {
			actor = "later-caller-not-original"
		}
		Expect(inTx(actor, func(c context.Context, tx database.Tx) error {
			_, err := tenants.Delete().SetId("unknown").Do(c)
			if err != nil {
				return err
			}
			Expect(readIdentity(c, tx, "unknown").actor).To(BeNil())
			return nil
		})).To(Succeed())
	}, Entry("direct bare context", false), Entry("legacy deleting row", true))

	DescribeTable("stamps recreation after a key wait and preserves the previous marker until the next deletion", func(finalizer, futureFloor bool) {
		Expect(inTx("", func(c context.Context, _ database.Tx) error {
			if futureFloor {
				tx, err := database.TxFromContext(c)
				if err != nil {
					return err
				}
				// A valid prior unbilled incarnation forces actual DAO Create/Delete beyond the
				// wall clock. No production clock override or sleep is involved.
				_, err = tx.Exec(c, `insert into deleted_tenant_identities values
					('reusable', '2040-01-01Z', '2040-01-02Z', NULL, '2040-01-03Z', false)`)
				if err != nil {
					return err
				}
			}
			input := object("reusable")
			if finalizer {
				input.GetMetadata().SetFinalizers([]string{"controller"})
			}
			_, err := create(c, input)
			return err
		})).To(Succeed())
		waitCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		DeferCleanup(cancel)
		deleter, err := tm.Begin(waitCtx)
		Expect(err).NotTo(HaveOccurred())
		deleteCtx := auth.ContextWithSubject(database.TxIntoContext(waitCtx, deleter), &auth.Subject{User: "first"})
		deleteEnded := false
		defer func() {
			if !deleteEnded {
				Expect(deleter.End(context.Background())).To(Succeed())
			}
		}()
		var deletePID int32
		Expect(deleter.QueryRow(deleteCtx, `select pg_backend_pid()`).Scan(&deletePID)).To(Succeed())
		_, err = tenants.Delete().SetId("reusable").Do(deleteCtx)
		Expect(err).NotTo(HaveOccurred())
		if finalizer {
			read, err := tenants.Get().SetId("reusable").Do(deleteCtx)
			Expect(err).NotTo(HaveOccurred())
			read.GetObject().GetMetadata().SetFinalizers(nil)
			_, err = tenants.Update().SetObject(read.GetObject()).Do(deleteCtx)
			Expect(err).NotTo(HaveOccurred())
		}
		old := readIdentity(deleteCtx, deleter, "reusable")
		Expect(old.finalized).NotTo(BeNil())
		creator, err := tm.Begin(waitCtx)
		Expect(err).NotTo(HaveOccurred())
		createCtx := database.TxIntoContext(waitCtx, creator)
		createEnded := false
		defer func() {
			if !createEnded {
				Expect(creator.End(context.Background())).To(Succeed())
			}
		}()
		var createPID int32
		var transactionNow time.Time
		// Begin is lazy: force a real, old PostgreSQL transaction before waiting on the key.
		Expect(creator.QueryRow(createCtx, `select pg_backend_pid(), now()`).Scan(&createPID, &transactionNow)).To(Succeed())
		if futureFloor {
			Expect(transactionNow).To(BeTemporally("<", *old.finalized))
		}
		result := make(chan *CreateResponse[*privatev1.Tenant], 1)
		errors := make(chan error, 1)
		go func() {
			response, err := create(createCtx, object("reusable"))
			result <- response
			errors <- err
		}()
		Eventually(func() (bool, error) {
			var blocked bool
			err := pool.QueryRow(waitCtx, `select $1 = any(pg_blocking_pids($2))`, deletePID, createPID).Scan(&blocked)
			return blocked, err
		}, 5*time.Second, 10*time.Millisecond).Should(BeTrue())
		Expect(deleter.End(deleteCtx)).To(Succeed())
		deleteEnded = true
		var createErr error
		Eventually(errors, 5*time.Second, 10*time.Millisecond).Should(Receive(&createErr))
		Expect(createErr).NotTo(HaveOccurred())
		var created *CreateResponse[*privatev1.Tenant]
		Expect(result).To(Receive(&created))
		creation := created.GetObject().GetMetadata().GetCreationTimestamp().AsTime()
		Expect(creation).To(BeTemporally(">", *old.finalized))
		Expect(creation).To(BeTemporally(">", transactionNow))
		if futureFloor {
			Expect(creation).To(BeTemporally("==", old.finalized.Add(time.Microsecond)))
		}
		var stored time.Time
		Expect(creator.QueryRow(createCtx, `select creation_timestamp from tenants where name = 'reusable'`).Scan(&stored)).To(Succeed())
		Expect(stored).To(BeTemporally("==", creation))
		Expect(readIdentity(createCtx, creator, "reusable")).To(Equal(old))
		Expect(creator.End(createCtx)).To(Succeed())
		createEnded = true
		Expect(inTx("second", func(c context.Context, tx database.Tx) error {
			_, err := tenants.Delete().SetId("reusable").Do(c)
			if err != nil {
				return err
			}
			second := readIdentity(c, tx, "reusable")
			Expect(second.created).To(Equal(creation))
			Expect(second.actor).To(Equal(new("second")))
			Expect(second.deleted).To(BeTemporally(">=", creation))
			Expect(second.finalized).NotTo(BeNil())
			if futureFloor {
				Expect(second.deleted).To(BeTemporally("==", creation))
				Expect(*second.finalized).To(BeTemporally("==", second.deleted))
			}
			_, err = create(c, object("reusable"))
			return err
		})).To(Succeed())
	}, Entry("immediate Delete", false, false), Entry("last-finalizer Update", true, false),
		Entry("immediate Delete with future/tied boundaries", false, true),
		Entry("last-finalizer Update with future/tied boundaries", true, true))

	DescribeTable("takes the tenant identity key before each supported row-lock surface", func(operation string) {
		Expect(inTx("", func(c context.Context, _ database.Tx) error {
			_, err := create(c, object("ordered", "controller"))
			return err
		})).To(Succeed())
		waitCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		DeferCleanup(cancel)
		gate, err := tm.Begin(waitCtx)
		Expect(err).NotTo(HaveOccurred())
		gateCtx := database.TxIntoContext(waitCtx, gate)
		gateEnded := false
		defer func() {
			if !gateEnded {
				Expect(gate.End(context.Background())).To(Succeed())
			}
		}()
		key := "ordered"
		if operation == "bulk including missing" {
			key = "a-missing"
		}
		Expect(database.LockTenantIdentity(gateCtx, key)).To(Succeed())
		var gatePID int32
		Expect(gate.QueryRow(gateCtx, `select pg_backend_pid()`).Scan(&gatePID)).To(Succeed())
		waiter, err := tm.Begin(waitCtx)
		Expect(err).NotTo(HaveOccurred())
		waiterCtx := database.TxIntoContext(waitCtx, waiter)
		defer func() { Expect(waiter.End(context.Background())).To(Succeed()) }()
		var waiterPID int32
		Expect(waiter.QueryRow(waiterCtx, `select pg_backend_pid()`).Scan(&waiterPID)).To(Succeed())
		result := make(chan error, 1)
		go func() {
			var err error
			switch operation {
			case "Lock":
				_, err = tenants.Lock().AddId("ordered").Do(waiterCtx)
			case "bulk including missing":
				_, err = tenants.Lock().AddIds("ordered", "a-missing", "ordered").Do(waiterCtx)
			case "Get update":
				_, err = tenants.Get().SetId("ordered").SetLock(true).Do(waiterCtx)
			case "Get share":
				_, err = tenants.Get().SetId("ordered").SetShareLock(true).Do(waiterCtx)
			case "Update":
				_, err = tenants.Update().SetObject(object("ordered", "controller")).Do(waiterCtx)
			case "Delete":
				_, err = tenants.Delete().SetId("ordered").Do(waiterCtx)
			case "Signal":
				_, err = tenants.Signal().SetId("ordered").Do(waiterCtx)
			}
			result <- err
		}()
		Eventually(func() (bool, error) {
			var blocked bool
			err := pool.QueryRow(waitCtx, `select $1 = any(pg_blocking_pids($2))`, gatePID, waiterPID).Scan(&blocked)
			return blocked, err
		}, 5*time.Second, 10*time.Millisecond).Should(BeTrue())
		// The waiter has not taken even a SHARE lock on a tenant before asking for its key.
		probe, err := pool.Begin(waitCtx)
		Expect(err).NotTo(HaveOccurred())
		_, err = probe.Exec(waitCtx, `select id from tenants where id = 'ordered' for update nowait`)
		Expect(err).NotTo(HaveOccurred())
		Expect(probe.Rollback(waitCtx)).To(Succeed())
		Expect(gate.End(gateCtx)).To(Succeed())
		gateEnded = true
		var actual error
		Eventually(result, 5*time.Second, 10*time.Millisecond).Should(Receive(&actual))
		if operation == "bulk including missing" {
			var missing *ErrNotFound
			Expect(errors.As(actual, &missing)).To(BeTrue())
			Expect(missing.IDs).To(Equal([]string{"a-missing"}))
		} else {
			Expect(actual).NotTo(HaveOccurred())
		}
	}, Entry("Lock", "Lock"), Entry("complete sorted bulk set", "bulk including missing"),
		Entry("Get FOR UPDATE", "Get update"), Entry("Get FOR SHARE", "Get share"),
		Entry("direct Update", "Update"), Entry("direct Delete", "Delete"), Entry("Signal", "Signal"))

	It("keeps plain missing Get key-free", func() {
		gate, err := tm.Begin(ctx)
		Expect(err).NotTo(HaveOccurred())
		defer func() { Expect(gate.End(ctx)).To(Succeed()) }()
		Expect(database.LockTenantIdentity(database.TxIntoContext(ctx, gate), "never-known")).To(Succeed())
		err = inTx("", func(c context.Context, _ database.Tx) error {
			readCtx, cancel := context.WithTimeout(c, 5*time.Second)
			defer cancel()
			_, err := tenants.Get().SetId("never-known").Do(readCtx)
			return err
		})
		var missing *ErrNotFound
		Expect(errors.As(err, &missing)).To(BeTrue())
	})

	DescribeTable("records finalization after the actual PostgreSQL physical-delete boundary", func(finalizer bool) {
		Expect(inTx("", func(c context.Context, tx database.Tx) error {
			input := object("post-delete")
			if finalizer {
				input.GetMetadata().SetFinalizers([]string{"controller"})
			}
			if _, err := create(c, input); err != nil {
				return err
			}
			_, err := tx.Exec(c, `create table deletion_boundary (at timestamptz not null);
				create function record_delete_boundary() returns trigger language plpgsql as $$
				begin insert into deletion_boundary values (clock_timestamp()); return old; end $$;
				create trigger record_delete_boundary after delete on tenants
				for each row execute function record_delete_boundary()`)
			return err
		})).To(Succeed())
		Expect(inTx("deleter", func(c context.Context, tx database.Tx) error {
			if _, err := tenants.Delete().SetId("post-delete").Do(c); err != nil {
				return err
			}
			if finalizer {
				current, err := tenants.Get().SetId("post-delete").Do(c)
				if err != nil {
					return err
				}
				current.GetObject().GetMetadata().SetFinalizers(nil)
				if _, err = tenants.Update().SetObject(current.GetObject()).Do(c); err != nil {
					return err
				}
			}
			marker := readIdentity(c, tx, "post-delete")
			var physicallyDeletedAt time.Time
			Expect(tx.QueryRow(c, `select at from deletion_boundary`).Scan(&physicallyDeletedAt)).To(Succeed())
			Expect(marker.finalized).NotTo(BeNil())
			Expect(*marker.finalized).To(BeTemporally(">=", physicallyDeletedAt))
			return nil
		})).To(Succeed())
	}, Entry("immediate Delete", false), Entry("last-finalizer Update", true))

	It("cancels an identity-key waiter without accepting deletion or leaving a marker", func() {
		Expect(inTx("", func(c context.Context, _ database.Tx) error {
			_, err := create(c, object("cancelled"))
			return err
		})).To(Succeed())
		waitCtx, timeout := context.WithTimeout(ctx, 15*time.Second)
		defer timeout()
		gate, err := tm.Begin(waitCtx)
		Expect(err).NotTo(HaveOccurred())
		gateCtx := database.TxIntoContext(waitCtx, gate)
		defer func() { Expect(gate.End(context.Background())).To(Succeed()) }()
		Expect(database.LockTenantIdentity(gateCtx, "cancelled")).To(Succeed())
		var gatePID int32
		Expect(gate.QueryRow(gateCtx, `select pg_backend_pid()`).Scan(&gatePID)).To(Succeed())
		waiter, err := tm.Begin(waitCtx)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = waiter.End(context.Background()) }()
		waiterCtx, cancel := context.WithCancel(database.TxIntoContext(waitCtx, waiter))
		defer cancel()
		var waiterPID int32
		Expect(waiter.QueryRow(waiterCtx, `select pg_backend_pid()`).Scan(&waiterPID)).To(Succeed())
		result := make(chan error, 1)
		go func() {
			_, err := tenants.Delete().SetId("cancelled").Do(waiterCtx)
			result <- err
		}()
		Eventually(func() (bool, error) {
			var blocked bool
			err := pool.QueryRow(waitCtx, `select $1 = any(pg_blocking_pids($2))`, gatePID, waiterPID).Scan(&blocked)
			return blocked, err
		}, 5*time.Second, 10*time.Millisecond).Should(BeTrue())
		cancel()
		var actual error
		Eventually(result, 5*time.Second, 10*time.Millisecond).Should(Receive(&actual))
		Expect(errors.Is(actual, context.Canceled)).To(BeTrue())
		var markers int
		var deleted bool
		Expect(pool.QueryRow(waitCtx, `select count(*) from deleted_tenant_identities where tenant_name = 'cancelled'`).Scan(&markers)).To(Succeed())
		Expect(pool.QueryRow(waitCtx, `select deletion_timestamp <> 'epoch' from tenants where name = 'cancelled'`).Scan(&deleted)).To(Succeed())
		Expect(markers).To(BeZero())
		Expect(deleted).To(BeFalse())
	})

	It("rolls back physical archival and capture together when the outer caller fails", func() {
		Expect(inTx("", func(c context.Context, _ database.Tx) error {
			_, err := create(c, object("rollback"))
			return err
		})).To(Succeed())
		injected := errors.New("caller failed after archive")
		Expect(inTx("deleter", func(c context.Context, _ database.Tx) error {
			_, err := tenants.Delete().SetId("rollback").Do(c)
			if err != nil {
				return err
			}
			return injected
		})).To(MatchError(injected))
		Expect(inTx("", func(c context.Context, tx database.Tx) error {
			read, err := tenants.Get().SetId("rollback").Do(c)
			if err != nil {
				return err
			}
			Expect(read.GetObject().GetMetadata().HasDeletionTimestamp()).To(BeFalse())
			var markers, archives int
			Expect(tx.QueryRow(c, `select count(*) from deleted_tenant_identities where tenant_name = 'rollback'`).Scan(&markers)).To(Succeed())
			Expect(tx.QueryRow(c, `select count(*) from archived_tenants where name = 'rollback'`).Scan(&archives)).To(Succeed())
			Expect(markers).To(BeZero())
			Expect(archives).To(BeZero())
			return nil
		})).To(Succeed())
	})

	DescribeTable("preserves ordinary duplicate-create precedence for existing live and pending tenants", func(deleting, billed bool) {
		Expect(inTx("original", func(c context.Context, _ database.Tx) error {
			if _, err := create(c, object("duplicate", "controller")); err != nil {
				return err
			}
			if billed {
				if err := seedBilling(c, "duplicate", time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
					return err
				}
			}
			if deleting {
				_, err := tenants.Delete().SetId("duplicate").Do(c)
				return err
			}
			return nil
		})).To(Succeed())
		err := inTx("", func(c context.Context, _ database.Tx) error {
			_, err := tenants.Create().SetObject(object("duplicate")).Do(c)
			return err
		})
		var duplicate *ErrAlreadyExists
		Expect(errors.As(err, &duplicate)).To(BeTrue())
		Expect(errors.Is(err, database.ErrTenantIdentityRetained)).To(BeFalse())
	}, Entry("live unbilled", false, false), Entry("live billed", false, true),
		Entry("pending unbilled marker", true, false), Entry("pending billed marker", true, true))

	DescribeTable("rejects invalid direct tenant identities without normalizing or inventing them", func(id, name, tenant string) {
		err := inTx("", func(c context.Context, _ database.Tx) error {
			_, err := tenants.Create().SetObject(privatev1.Tenant_builder{
				Id: id, Metadata: privatev1.Metadata_builder{Name: name, Tenant: tenant}.Build(),
			}.Build()).Do(c)
			return err
		})
		Expect(err).To(MatchError("tenant id, name and self-tenant must be nonempty and equal"))
		var live int
		Expect(pool.QueryRow(ctx, `select count(*) from tenants where name in ('canonical', 'different')`).Scan(&live)).To(Succeed())
		Expect(live).To(BeZero())
	}, Entry("missing id", "", "canonical", "canonical"), Entry("missing name", "canonical", "", "canonical"),
		Entry("mismatched id", "different", "canonical", "canonical"), Entry("mismatched self-tenant", "canonical", "canonical", "different"))

	DescribeTable("fails actual DAO recreation for pending or overflowing absent identity evidence", func(finalized *time.Time) {
		Expect(inTx("", func(c context.Context, tx database.Tx) error {
			_, err := tx.Exec(c, `insert into deleted_tenant_identities values
				('audit-stop', '2020-01-01Z', '2020-01-02Z', NULL, $1, false)`, finalized)
			return err
		})).To(Succeed())
		err := inTx("", func(c context.Context, _ database.Tx) error {
			_, err := tenants.Create().SetObject(object("audit-stop")).Do(c)
			return err
		})
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, database.ErrTenantIdentityRetained)).To(BeFalse(), "audit failures are not permanent name policy")
		var live int
		Expect(pool.QueryRow(ctx, `select count(*) from tenants where name = 'audit-stop'`).Scan(&live)).To(Succeed())
		Expect(live).To(BeZero())
	}, Entry("unfinalized", nil), Entry("protobuf successor overflow", new(time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC))))

	DescribeTable("rolls back tenant, billing, identity and archive after an injected lifecycle failure", func(stage string, finalizer bool) {
		var beforeTenant, beforeHistory, beforeMarker string
		snapshot := func(c context.Context, tx database.Tx) (string, string, string) {
			GinkgoHelper()
			var tenant, history, marker string
			Expect(tx.QueryRow(c, `select to_jsonb(t)::text from tenants t where name = 'failure'`).Scan(&tenant)).To(Succeed())
			Expect(tx.QueryRow(c, `select jsonb_agg(to_jsonb(h) order by id)::text from tenant_billing_assignments h
				where tenant_id = 'failure'`).Scan(&history)).To(Succeed())
			Expect(tx.QueryRow(c, `select coalesce((select to_jsonb(d)::text from deleted_tenant_identities d
				where tenant_name = 'failure'), '')`).Scan(&marker)).To(Succeed())
			return tenant, history, marker
		}
		Expect(inTx("", func(c context.Context, tx database.Tx) error {
			input := object("failure")
			if finalizer {
				input.GetMetadata().SetFinalizers([]string{"controller"})
			}
			if _, err := create(c, input); err != nil {
				return err
			}
			if err := seedBilling(c, "failure", time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
				return err
			}
			if finalizer {
				if _, err := tenants.Delete().SetId("failure").Do(c); err != nil {
					return err
				}
			}
			beforeTenant, beforeHistory, beforeMarker = snapshot(c, tx)
			var injection string
			switch stage {
			case "acceptance capture":
				injection = `create function injected_failure() returns trigger language plpgsql as $$
					begin raise exception 'injected capture failure'; end $$;
					create trigger injected_failure before insert on deleted_tenant_identities
					for each row execute function injected_failure()`
			case "archive insert":
				injection = `create function injected_failure() returns trigger language plpgsql as $$
					begin raise exception 'injected archive failure'; end $$;
					create trigger injected_failure before insert on archived_tenants
					for each row execute function injected_failure()`
			case "zero-row delete":
				injection = `create function injected_failure() returns trigger language plpgsql as $$
					begin return null; end $$;
					create trigger injected_failure before delete on tenants
					for each row execute function injected_failure()`
			case "post-delete finalization":
				injection = `create function injected_failure() returns trigger language plpgsql as $$
					begin if new.finalized_at is not null then raise exception 'injected finalization failure';
					end if; return new; end $$;
					create trigger injected_failure before update on deleted_tenant_identities
					for each row execute function injected_failure()`
			case "outer commit":
				injection = `create function injected_failure() returns trigger language plpgsql as $$
					begin raise exception 'injected commit failure'; end $$;
					create constraint trigger injected_failure after insert or update on deleted_tenant_identities
					deferrable initially deferred for each row execute function injected_failure()`
			}
			_, err := tx.Exec(c, injection)
			return err
		})).To(Succeed())
		err := inTx("request-actor", func(c context.Context, _ database.Tx) error {
			if finalizer {
				current, err := tenants.Get().SetId("failure").Do(c)
				if err != nil {
					return err
				}
				current.GetObject().GetMetadata().SetFinalizers(nil)
				_, err = tenants.Update().SetObject(current.GetObject()).Do(c)
				return err
			}
			_, err := tenants.Delete().SetId("failure").Do(c)
			return err
		})
		Expect(err).To(HaveOccurred())
		Expect(inTx("", func(c context.Context, tx database.Tx) error {
			tenant, history, marker := snapshot(c, tx)
			Expect(tenant).To(Equal(beforeTenant))
			Expect(history).To(Equal(beforeHistory))
			Expect(marker).To(Equal(beforeMarker))
			var archived int
			Expect(tx.QueryRow(c, `select count(*) from archived_tenants where name = 'failure'`).Scan(&archived)).To(Succeed())
			Expect(archived).To(BeZero())
			return nil
		})).To(Succeed())
	}, Entry("Delete capture", "acceptance capture", false), Entry("Delete archive insert", "archive insert", false),
		Entry("Delete zero affected physical delete", "zero-row delete", false), Entry("Delete finalization", "post-delete finalization", false),
		Entry("Delete outer commit", "outer commit", false),
		Entry("Update capture", "acceptance capture", true), Entry("Update archive insert", "archive insert", true),
		Entry("Update zero affected physical delete", "zero-row delete", true), Entry("Update finalization", "post-delete finalization", true),
		Entry("Update outer commit", "outer commit", true))
})
