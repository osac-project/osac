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
	"github.com/jackc/pgx/v5/pgxpool"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// These are component tests of the production DAO and PostgreSQL triggers, not RPC authorization or provider tests.
var _ = Describe("Billing GenericDAO persistence", func() {
	var (
		ctx            context.Context
		tx             database.Tx
		tm             database.TxManager
		pool           *pgxpool.Pool
		visibility     *auth.Visibility
		accounts       *GenericDAO[*privatev1.BillingAccount]
		assignments    *GenericDAO[*privatev1.TenantBillingAssignment]
		publicAssigned *GenericDAO[*privatev1.TenantBillingAssignment]
		tenants        *GenericDAO[*privatev1.Tenant]
	)
	const ownerAnnotation = "osac.openshift.io/tenant"
	at := time.Date(2026, 11, 17, 12, 0, 0, 123456000, time.UTC)

	// Fixture identity follows the approved UTF-8 SHA256 contract; it does not call or test the server naming helper.
	accountObject := func(id string, token *string) *privatev1.BillingAccount {
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte(id)))
		return privatev1.BillingAccount_builder{
			Id: id,
			Metadata: privatev1.Metadata_builder{
				Name: "ba-" + hash[:60], Tenant: auth.SystemTenant, Creator: "system",
				Labels: map[string]string{"fixture": "billing"}, Annotations: map[string]string{"note": "retained"},
			}.Build(),
			Spec: privatev1.BillingAccountSpec_builder{}.Build(),
			Status: privatev1.BillingAccountStatus_builder{
				ProviderKey: "fixture", ProviderAccountReference: "native/" + id, DisplayName: "Display " + id,
				BaseCurrency: "USD", State: privatev1.BillingAccountState_BILLING_ACCOUNT_STATE_ACTIVE,
				StateObservationTimestamp: timestamppb.New(at), ExternalBillingAccountId: token,
			}.Build(),
		}.Build()
	}
	assignmentObject := func(id, tenant string, account *privatev1.BillingAccount) *privatev1.TenantBillingAssignment {
		return privatev1.TenantBillingAssignment_builder{
			Id: id,
			Metadata: privatev1.Metadata_builder{
				Name: "billing-history", Tenant: tenant, Creator: "request-principal",
				Annotations: map[string]string{ownerAnnotation: tenant}, Labels: map[string]string{"fixture": "assignment"},
			}.Build(),
			Spec: privatev1.TenantBillingAssignmentSpec_builder{
				BillingAccount: privatev1.BillingAccountReference_builder{
					Id: account.GetId(), Name: account.GetMetadata().GetName(),
				}.Build(),
			}.Build(),
			Status: privatev1.TenantBillingAssignmentStatus_builder{
				ConnectionState:        privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_PENDING,
				EffectiveFromTimestamp: timestamppb.New(at), Message: "manual remediation",
			}.Build(),
		}.Build()
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
		tx, err = tm.Begin(ctx)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			if tx != nil {
				Expect(tx.End(context.Background())).To(Succeed())
			}
		})
		ctx = database.TxIntoContext(ctx, tx)
		visibility = auth.TotalVisibility()
		tenancy := auth.NewMockTenancyLogic(gomock.NewController(GinkgoT()))
		tenancy.EXPECT().DetermineVisibility(gomock.Any()).DoAndReturn(func(context.Context) (*auth.Visibility, error) {
			return visibility, nil
		}).AnyTimes()
		accounts, err = NewGenericDAO[*privatev1.BillingAccount]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
		Expect(err).NotTo(HaveOccurred())
		assignments, err = NewGenericDAO[*privatev1.TenantBillingAssignment]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
		Expect(err).NotTo(HaveOccurred())
		publicAssigned, err = NewGenericDAO[*privatev1.TenantBillingAssignment]().SetLogger(logger).SetTenancyLogic(tenancy).
			SetFilterDesc((*privatev1.TenantBillingAssignment)(nil).ProtoReflect().Descriptor()).Build()
		Expect(err).NotTo(HaveOccurred())
		tenants, err = NewGenericDAO[*privatev1.Tenant]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
		Expect(err).NotTo(HaveOccurred())
		// The migrated native template already owns the system tenant; ordinary tenants bootstrap themselves.
		for _, tenant := range []string{"billing-a", "billing-b", "billing-c"} {
			_, err = tenants.Create().SetObject(privatev1.Tenant_builder{
				Id: tenant, Metadata: privatev1.Metadata_builder{Name: tenant, Tenant: tenant, Creator: "system"}.Build(),
			}.Build()).Do(ctx)
			Expect(err).NotTo(HaveOccurred())
		}
	})

	It("round-trips the typed account body and DB-owned metadata independently of observation time", func() {
		input := accountObject("opaque/東京", nil)
		input.GetMetadata().SetCreationTimestamp(timestamppb.New(time.Unix(1, 0)))
		input.GetMetadata().SetVersion(77)
		created, err := accounts.Create().SetObject(input).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		object := created.GetObject()
		Expect(object.GetMetadata().GetCreationTimestamp().AsTime()).NotTo(Equal(time.Unix(1, 0)))
		Expect(object.GetMetadata().GetVersion()).To(BeZero())
		Expect(object.GetMetadata().HasDeletionTimestamp()).To(BeFalse())
		var creation time.Time
		var version int32
		var data []byte
		Expect(tx.QueryRow(ctx, `select creation_timestamp, version, data from billing_accounts where id=$1`, object.GetId()).
			Scan(&creation, &version, &data)).To(Succeed())
		Expect(creation).To(BeTemporally("==", object.GetMetadata().GetCreationTimestamp().AsTime()))
		Expect(version).To(Equal(object.GetMetadata().GetVersion()))
		Expect(data).To(MatchJSON(`{"spec":{},"status":{"provider_key":"fixture","provider_account_reference":"native/opaque/東京",
			"display_name":"Display opaque/東京","base_currency":"USD","state":"BILLING_ACCOUNT_STATE_ACTIVE",
			"state_observation_timestamp":"2026-11-17T12:00:00.123456Z"}}`))
		stored, err := accounts.Get().SetId(object.GetId()).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(proto.Equal(stored.GetObject(), object)).To(BeTrue())
		before := proto.Clone(object.GetMetadata()).(*privatev1.Metadata)
		object.GetStatus().SetDisplayName("renamed provider label")
		object.GetStatus().SetState(privatev1.BillingAccountState_BILLING_ACCOUNT_STATE_INACTIVE)
		object.GetStatus().SetStateObservationTimestamp(timestamppb.New(at.Add(time.Second)))
		object.GetMetadata().SetCreator("forged")
		object.GetMetadata().SetCreationTimestamp(timestamppb.New(time.Unix(2, 0)))
		updated, err := accounts.Update().SetObject(object).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		before.SetVersion(1)
		Expect(proto.Equal(updated.GetObject().GetMetadata(), before)).To(BeTrue())
		var key, native, label, state string
		var observed time.Time
		Expect(tx.QueryRow(ctx, `select data->'status'->>'provider_key', data->'status'->>'provider_account_reference',
			data->'status'->>'display_name', data->'status'->>'state',
			(data->'status'->>'state_observation_timestamp')::timestamptz
			from billing_accounts where id=$1`, object.GetId()).Scan(&key, &native, &label, &state, &observed)).To(Succeed())
		Expect([]string{key, native, label, state}).To(Equal([]string{"fixture", "native/opaque/東京", "renamed provider label",
			"BILLING_ACCOUNT_STATE_INACTIVE"}))
		Expect(observed).To(BeTemporally("==", at.Add(time.Second)))
		stored, err = accounts.Get().SetId(object.GetId()).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(proto.Equal(stored.GetObject(), updated.GetObject())).To(BeTrue())
	})

	DescribeTable("preserves optional token presence and raw uniqueness", func(token *string) {
		created, err := accounts.Create().SetObject(accountObject("first", token)).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		stored, err := accounts.Get().SetId("first").Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(proto.Equal(stored.GetObject(), created.GetObject())).To(BeTrue())
		Expect(stored.GetObject().GetStatus().HasExternalBillingAccountId()).To(Equal(token != nil))
		var projection *string
		var present bool
		Expect(tx.QueryRow(ctx, `select data->'status'->>'external_billing_account_id',
			(data->'status') ? 'external_billing_account_id'
			from billing_accounts where id='first'`).Scan(&projection, &present)).To(Succeed())
		Expect(projection).To(Equal(token))
		Expect(present).To(Equal(token != nil))
		err = tx.Savepoint(ctx, func(spCtx context.Context) error {
			_, err := accounts.Create().SetObject(accountObject("second", token)).Do(spCtx)
			return err
		})
		if token == nil {
			Expect(err).NotTo(HaveOccurred(), "multiple SQL NULL tokens are allowed")
		} else {
			var duplicate *ErrAlreadyExists
			Expect(errors.As(err, &duplicate)).To(BeTrue())
			Expect(duplicate.ConstraintName).To(Equal("billing_accounts_unique_external_billing_account_id"))
			other := *token + " "
			_, err = accounts.Create().SetObject(accountObject("raw-distinct", &other)).Do(ctx)
			Expect(err).NotTo(HaveOccurred(), "uniqueness uses bytes, not trimmed equality")
		}
	}, Entry("absent is SQL NULL", (*string)(nil)), Entry("present empty", proto.String("")),
		Entry("whitespace", proto.String(" \t\n")), Entry("padded", proto.String("  attribution  ")),
		Entry("verbatim", proto.String("Raw/東京'\\ID")))

	It("reports the actual native-key constraint and recovers the create savepoint", func() {
		_, err := accounts.Create().SetObject(accountObject("first", nil)).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		duplicate := accountObject("second", nil)
		duplicate.GetStatus().SetProviderAccountReference("native/first")
		err = tx.Savepoint(ctx, func(spCtx context.Context) error {
			_, err := accounts.Create().SetObject(duplicate).Do(spCtx)
			return err
		})
		var conflict *ErrAlreadyExists
		Expect(errors.As(err, &conflict)).To(BeTrue())
		Expect(conflict.ConstraintName).To(Equal("billing_accounts_unique_provider_account"))
		list, err := accounts.List().SetFilter(`this.id == 'second'`).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(list.GetTotal()).To(BeZero())
		_, err = accounts.Create().SetObject(accountObject("after-conflict", nil)).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
	})

	It("retains Update's real raw-token ConstraintName, recovers, and commits unrelated work", func() {
		_, err := accounts.Create().SetObject(accountObject("carrier", proto.String("shared-raw"))).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		candidate, err := accounts.Create().SetObject(accountObject("candidate", nil)).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		candidate.GetObject().GetStatus().SetExternalBillingAccountId("shared-raw")
		err = tx.Savepoint(ctx, func(spCtx context.Context) error {
			_, err := accounts.Update().SetObject(candidate.GetObject()).Do(spCtx)
			return err
		})
		var conflict *ErrAlreadyExists
		Expect(errors.As(err, &conflict)).To(BeTrue())
		stored, err := accounts.Get().SetId("candidate").Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(stored.GetObject().GetStatus().HasExternalBillingAccountId()).To(BeFalse())
		Expect(stored.GetObject().GetMetadata().GetVersion()).To(BeZero())
		stored.GetObject().GetStatus().SetDisplayName("valid observation retained without token")
		_, err = accounts.Update().SetObject(stored.GetObject()).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		_, err = tenants.Create().SetObject(privatev1.Tenant_builder{Id: "after-token-conflict",
			Metadata: privatev1.Metadata_builder{Name: "after-token-conflict", Tenant: "after-token-conflict"}.Build(),
		}.Build()).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		var one int
		Expect(tx.QueryRow(ctx, `select 1`).Scan(&one)).To(Succeed())
		Expect(one).To(Equal(1))
		Expect(tx.End(ctx)).To(Succeed())
		tx = nil
		Expect(tm.Run(context.Background(), func(readCtx context.Context) error {
			read, err := accounts.Get().SetId("candidate").Do(readCtx)
			Expect(err).NotTo(HaveOccurred())
			Expect(read.GetObject().GetMetadata().GetVersion()).To(Equal(int32(1)))
			Expect(read.GetObject().GetStatus().HasExternalBillingAccountId()).To(BeFalse())
			control, err := tenants.List().SetFilter(`this.id == 'after-token-conflict'`).Do(readCtx)
			Expect(err).NotTo(HaveOccurred())
			Expect(control.GetTotal()).To(Equal(int32(1)))
			return nil
		})).To(Succeed())
	})

	It("rejects native identity changes and established token rotation without poisoning the transaction", func() {
		created, err := accounts.Create().SetObject(accountObject("immutable", proto.String(" exact "))).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		for _, mutate := range []func(*privatev1.BillingAccount){
			func(o *privatev1.BillingAccount) { o.GetStatus().SetProviderKey("another") },
			func(o *privatev1.BillingAccount) { o.GetStatus().SetProviderAccountReference("another") },
			func(o *privatev1.BillingAccount) { o.GetStatus().SetExternalBillingAccountId("exact") },
			func(o *privatev1.BillingAccount) { o.GetStatus().ClearExternalBillingAccountId() },
		} {
			object := proto.Clone(created.GetObject()).(*privatev1.BillingAccount)
			mutate(object)
			err = tx.Savepoint(ctx, func(spCtx context.Context) error {
				_, err := accounts.Update().SetObject(object).Do(spCtx)
				return err
			})
			var check *pgconn.PgError
			Expect(errors.As(err, &check)).To(BeTrue())
			Expect(check.Code).To(Equal("23514"))
		}
		stored, err := accounts.Get().SetId("immutable").Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(proto.Equal(stored.GetObject(), created.GetObject())).To(BeTrue())
	})

	It("round-trips canonical references and revalidation with stable interval identity and metadata", func() {
		account, err := accounts.Create().SetObject(accountObject("account", nil)).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		created, err := assignments.Create().SetObject(assignmentObject("interval", "billing-a", account.GetObject())).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		stored, err := assignments.Get().SetId("interval").Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(proto.Equal(stored.GetObject(), created.GetObject())).To(BeTrue())
		metadata := proto.Clone(stored.GetObject().GetMetadata()).(*privatev1.Metadata)
		stored.GetObject().GetStatus().SetConnectionState(privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED)
		stored.GetObject().GetStatus().SetMessage("confirmed")
		updated, err := assignments.Update().SetObject(stored.GetObject()).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		metadata.SetVersion(1)
		Expect(proto.Equal(updated.GetObject().GetMetadata(), metadata)).To(BeTrue())
		var tenant, accountID, state, message string
		var from time.Time
		var to *time.Time
		var canonical bool
		Expect(tx.QueryRow(ctx, `select tenant, data->'spec'->'billing_account'->>'id',
			data->'status'->>'connection_state', data->'status'->>'message',
			(data->'status'->>'effective_from_timestamp')::timestamptz,
			(data->'status'->>'effective_to_timestamp')::timestamptz,
			not (data ? 'id' or data ? 'metadata') and data#>>'{spec,billing_account,id}'='account'
			from tenant_billing_assignments where id='interval'`).Scan(&tenant, &accountID, &state, &message, &from, &to, &canonical)).To(Succeed())
		Expect([]string{tenant, accountID, state, message}).To(Equal([]string{"billing-a", "account",
			"TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED", "confirmed"}))
		Expect(from).To(BeTemporally("==", at))
		Expect(to).To(BeNil())
		Expect(canonical).To(BeTrue())
		stored, err = assignments.Get().SetId("interval").Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(proto.Equal(stored.GetObject(), updated.GetObject())).To(BeTrue())
	})

	DescribeTable("enforces the independent current-interval unique indexes", func(secondTenant, secondAccount, constraint string) {
		for _, id := range []string{"first-account", "second-account"} {
			_, err := accounts.Create().SetObject(accountObject(id, nil)).Do(ctx)
			Expect(err).NotTo(HaveOccurred())
		}
		_, err := assignments.Create().SetObject(assignmentObject("first", "billing-a", accountObject("first-account", nil))).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		err = tx.Savepoint(ctx, func(spCtx context.Context) error {
			_, err := assignments.Create().SetObject(assignmentObject("second", secondTenant, accountObject(secondAccount, nil))).Do(spCtx)
			return err
		})
		var conflict *ErrAlreadyExists
		Expect(errors.As(err, &conflict)).To(BeTrue(), "this must reach uniqueness, not fail an FK or projection guard")
		Expect(conflict.ConstraintName).To(Equal(constraint))
		list, err := assignments.List().Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(list.GetTotal()).To(Equal(int32(1)))
	}, Entry("one current account per tenant", "billing-a", "second-account", "tenant_billing_assignments_unique_active_tenant"),
		Entry("one current tenant per account", "billing-b", "first-account", "tenant_billing_assignments_unique_active_account"))

	It("rejects an actually missing account via the trigger reference check", func() {
		err := tx.Savepoint(ctx, func(spCtx context.Context) error {
			_, err := assignments.Create().SetObject(assignmentObject("orphan", "billing-a", accountObject("missing", nil))).Do(spCtx)
			return err
		})
		var reference *ErrReference
		Expect(errors.As(err, &reference)).To(BeTrue())
		Expect(reference.Reason).To(Equal("billing account 'missing' does not exist"))
		err = tx.Savepoint(ctx, func(spCtx context.Context) error {
			_, err := tx.Exec(spCtx, `insert into tenant_billing_assignments (id, name, tenant, data)
				values ($1, $2, $3, $4::jsonb)`, "orphan-trigger", "billing-history", "billing-a",
				`{"spec":{"billing_account":{"id":"missing","name":"ba-missing"}},
				"status":{"effective_from_timestamp":"2026-11-17T12:00:00Z"}}`)
			return err
		})
		var triggerError *pgconn.PgError
		Expect(errors.As(err, &triggerError)).To(BeTrue())
		Expect(triggerError.Code).To(Equal("Z0002"))
		Expect(triggerError.Message).To(Equal("billing account 'missing' does not exist"))
		list, err := assignments.List().Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(list.GetTotal()).To(BeZero())
	})

	It("queries both assignment descriptors with typed references, numeric enums and Timestamp filters", func() {
		for i, id := range []string{"older", "newer"} {
			_, err := accounts.Create().SetObject(accountObject(id, nil)).Do(ctx)
			Expect(err).NotTo(HaveOccurred())
			object := assignmentObject(id, "billing-a", accountObject(id, nil))
			object.GetStatus().SetEffectiveFromTimestamp(timestamppb.New(at.Add(time.Duration(i) * time.Second)))
			object.GetStatus().SetEffectiveToTimestamp(timestamppb.New(at.Add(time.Minute)))
			_, err = assignments.Create().SetObject(object).Do(ctx)
			Expect(err).NotTo(HaveOccurred())
		}
		for _, d := range []*GenericDAO[*privatev1.TenantBillingAssignment]{assignments, publicAssigned} {
			page, err := d.List().SetFilter(`this.metadata.tenant in ['billing-a'] && this.spec.billing_account.name != ''
				&& this.status.connection_state == 1 && has(this.status.effective_to_timestamp)`).SetLimit(1).Do(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(page.GetTotal()).To(Equal(int32(2)))
			Expect(page.GetItems()).To(HaveLen(1))
			Expect([]string{"older", "newer"}).To(ContainElement(page.GetItems()[0].GetId()))
		}
	})

	DescribeTable("intersects actual DAO tenancy visibility before count and pages", func(mode string, total int32, first string) {
		for i, tenant := range []string{"billing-a", "billing-a", "billing-b"} {
			id := fmt.Sprintf("interval-%d", i)
			_, err := accounts.Create().SetObject(accountObject(id, nil)).Do(ctx)
			Expect(err).NotTo(HaveOccurred())
			object := assignmentObject(id, tenant, accountObject(id, nil))
			object.GetStatus().SetEffectiveToTimestamp(timestamppb.New(at))
			_, err = assignments.Create().SetObject(object).Do(ctx)
			Expect(err).NotTo(HaveOccurred())
		}
		switch mode {
		case "finite":
			var err error
			visibility, err = auth.NewVisibility().AddVisibleTenant("billing-a").Build()
			Expect(err).NotTo(HaveOccurred())
		case "empty":
			visibility = auth.ZeroVisibility()
		}
		filter := `(this.metadata.tenant == 'billing-b' || this.status.connection_state == 1)`
		page, err := publicAssigned.List().SetFilter(filter).SetOffset(1).SetLimit(1).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(page.GetTotal()).To(Equal(total))
		if total == 0 {
			Expect(page.GetItems()).To(BeEmpty())
		} else {
			Expect(page.GetItems()).To(HaveLen(1))
			Expect(page.GetItems()[0].GetId()).To(Equal(first))
		}
		count, err := publicAssigned.List().SetFilter(filter).SetLimit(0).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(count.GetTotal()).To(Equal(total))
		Expect(count.GetItems()).To(BeEmpty())
	}, Entry("finite", "finite", int32(2), "interval-1"), Entry("universal", "total", int32(3), "interval-1"),
		Entry("empty", "empty", int32(0), ""))

	It("applies an explicitly composed exclusion predicate before count and pages, without claiming Subject policy", func() {
		for _, tenant := range []string{"billing-a", "billing-b", "billing-c"} {
			_, err := accounts.Create().SetObject(accountObject(tenant, nil)).Do(ctx)
			Expect(err).NotTo(HaveOccurred())
			_, err = assignments.Create().SetObject(assignmentObject(tenant, tenant, accountObject(tenant, nil))).Do(ctx)
			Expect(err).NotTo(HaveOccurred())
		}
		// GenericDAO has finite/total Visibility, not Subject.Tenants exclusion sets. The server owns that conversion.
		trusted := fmt.Sprintf(`!(this.metadata.tenant in [%q])`, "billing-b")
		caller := `this.metadata.tenant == 'billing-b' || this.status.connection_state == 1`
		page, err := publicAssigned.List().SetFilter("(" + trusted + ") && (" + caller + ")").
			SetOffset(1).SetLimit(1).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(page.GetTotal()).To(Equal(int32(2)))
		Expect(page.GetItems()).To(HaveLen(1))
		Expect(page.GetItems()[0].GetMetadata().GetTenant()).To(Equal("billing-c"))
	})

	It("commits billing create/update/close/replacement and publishes each write while Tenant INSERT/UPDATE enqueue", func() {
		// Hold a separate physical connection so post-commit observations cannot accidentally reuse the writer session.
		reader, err := pool.Acquire(ctx)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(reader.Release)
		var writerPID int32
		Expect(tx.QueryRow(ctx, `select pg_backend_pid()`).Scan(&writerPID)).To(Succeed())
		Expect(reader.Conn().PgConn().PID()).NotTo(Equal(uint32(writerPID)))
		var triggerCount int
		Expect(tx.QueryRow(ctx, `select count(*) from pg_trigger t join pg_proc p on p.oid=t.tgfoid
			where not t.tgisinternal and t.tgrelid in ('billing_accounts'::regclass,'tenant_billing_assignments'::regclass)
			and (t.tgname='enqueue_change' or p.proname='enqueue_change')`).Scan(&triggerCount)).To(Succeed())
		Expect(triggerCount).To(Equal(2), "both billing tables publish their changes")
		account, err := accounts.Create().SetObject(accountObject("published-never", nil)).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		account.GetObject().GetStatus().SetExternalBillingAccountId("canonical-token")
		account.GetObject().GetStatus().SetStateObservationTimestamp(timestamppb.New(at.Add(time.Second)))
		updated, err := accounts.Update().SetObject(account.GetObject()).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		old, err := assignments.Create().SetObject(assignmentObject("old-interval", "billing-a", updated.GetObject())).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		boundary := at.Add(time.Minute)
		old.GetObject().GetStatus().SetEffectiveToTimestamp(timestamppb.New(boundary))
		closed, err := assignments.Update().SetObject(old.GetObject()).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		replacement := assignmentObject("replacement", "billing-a", updated.GetObject())
		replacement.GetStatus().SetEffectiveFromTimestamp(timestamppb.New(boundary))
		next, err := assignments.Create().SetObject(replacement).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(next.GetObject().GetMetadata().GetName()).To(Equal(closed.GetObject().GetMetadata().GetName()))
		var end, start time.Time
		Expect(tx.QueryRow(ctx, `select (a.data->'status'->>'effective_to_timestamp')::timestamptz,
			(b.data->'status'->>'effective_from_timestamp')::timestamptz from tenant_billing_assignments a,
			tenant_billing_assignments b where a.id='old-interval' and b.id='replacement'`).Scan(&end, &start)).To(Succeed())
		Expect(end).To(BeTemporally("==", boundary))
		Expect(start).To(BeTemporally("==", end))
		// Already-closed history cannot be rewritten by GenericDAO, even with an unchanged payload/version increment.
		err = tx.Savepoint(ctx, func(spCtx context.Context) error {
			_, err := assignments.Update().SetObject(closed.GetObject()).Do(spCtx)
			return err
		})
		var history *pgconn.PgError
		Expect(errors.As(err, &history)).To(BeTrue())
		Expect(history.Code).To(Equal("23514"))
		control, err := tenants.Get().SetId("billing-a").Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		control.GetObject().GetMetadata().SetLabels(map[string]string{"positive": "updated"})
		_, err = tenants.Update().SetObject(control.GetObject()).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		var count int
		Expect(tx.QueryRow(ctx, `select count(*) from changes where "table" in ('billing_accounts','tenant_billing_assignments')`).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(5), "each billing write publishes a change")
		Expect(tx.End(ctx)).To(Succeed())
		tx = nil
		Expect(reader.QueryRow(ctx, `select count(*) from changes where "table" in ('billing_accounts','tenant_billing_assignments')`).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(5), "committed billing writes are published")
		var ops []string
		Expect(reader.QueryRow(ctx, `select array_agg(op order by op) from changes
			where "table"='tenants' and data->>'id'='billing-a'`).Scan(&ops)).To(Succeed())
		Expect(ops).To(Equal([]string{"INSERT", "UPDATE"}))
		Expect(tm.Run(context.Background(), func(readCtx context.Context) error {
			storedAccount, err := accounts.Get().SetId("published-never").Do(readCtx)
			Expect(err).NotTo(HaveOccurred())
			Expect(proto.Equal(storedAccount.GetObject(), updated.GetObject())).To(BeTrue())
			for _, expected := range []*privatev1.TenantBillingAssignment{closed.GetObject(), next.GetObject()} {
				stored, err := assignments.Get().SetId(expected.GetId()).Do(readCtx)
				Expect(err).NotTo(HaveOccurred())
				Expect(proto.Equal(stored.GetObject(), expected)).To(BeTrue())
			}
			return nil
		})).To(Succeed())
	})

	It("rolls back both canonical billing bodies and the proven nonbilling outbox row", func() {
		account, err := accounts.Create().SetObject(accountObject("rollback-account", nil)).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		_, err = assignments.Create().SetObject(assignmentObject("rollback-interval", "billing-a", account.GetObject())).Do(ctx)
		Expect(err).NotTo(HaveOccurred())
		var count int
		Expect(tx.QueryRow(ctx, `select count(*) from changes where "table"='tenants' and data->>'id'='billing-a'`).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(1), "positive enqueue must exist before rollback")
		rollback := errors.New("intentional ambient rollback")
		tx.ReportError(&rollback)
		Expect(tx.End(ctx)).To(Succeed())
		tx = nil
		Expect(tm.Run(context.Background(), func(readCtx context.Context) error {
			accountList, err := accounts.List().SetFilter(`this.id == 'rollback-account'`).Do(readCtx)
			Expect(err).NotTo(HaveOccurred())
			Expect(accountList.GetTotal()).To(BeZero())
			intervalList, err := assignments.List().SetFilter(`this.id == 'rollback-interval'`).Do(readCtx)
			Expect(err).NotTo(HaveOccurred())
			Expect(intervalList.GetTotal()).To(BeZero())
			controlList, err := tenants.List().SetFilter(`this.id == 'billing-a'`).Do(readCtx)
			Expect(err).NotTo(HaveOccurred())
			Expect(controlList.GetTotal()).To(BeZero())
			return nil
		})).To(Succeed())
		Expect(pool.QueryRow(ctx, `select count(*) from changes where "table"='tenants' and data->>'id'='billing-a'`).Scan(&count)).To(Succeed())
		Expect(count).To(BeZero())
	})
})
