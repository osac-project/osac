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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/testing/billingfake"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Tenant billing assignments server", func() {
	build := func(p *billing.Provider) *TenantBillingAssignmentsServer {
		private, err := NewPrivateTenantBillingAssignmentsServer().SetLogger(logger).SetProvider(p).Build()
		Expect(err).ToNot(HaveOccurred())
		server, err := NewTenantBillingAssignmentsServer().SetLogger(logger).SetDelegate(private).Build()
		Expect(err).ToNot(HaveOccurred())
		return server
	}
	It("requires a logger and a delegate", func() {
		_, err := NewTenantBillingAssignmentsServer().Build()
		Expect(err).To(MatchError("logger is mandatory"))
		_, err = NewTenantBillingAssignmentsServer().SetLogger(logger).Build()
		Expect(err).To(MatchError("delegate is mandatory"))
	})

	It("propagates FAILED_PRECONDITION when billing is disabled", func() {
		fake := billingfake.New(billingfake.Scenario{})
		adminCtx := auth.ContextWithSubject(ctx, &auth.Subject{User: "admin", Tenants: auth.AllTenants})
		_, err := build(billing.NewProvider("", fake, nil)).List(adminCtx, &publicv1.TenantBillingAssignmentsListRequest{})
		st, ok := status.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.FailedPrecondition))
	})

	It("creates, gets, and lists an assignment end to end through the public API", func() {
		id := uuid.New()
		fake := billingfake.New(billingfake.Scenario{
			CorrelationWrite: true,
			Accounts:         []billingfake.AccountSpec{{ID: id, State: billing.AccountActive}},
		})
		server := build(fake.Provider())
		adminCtx := auth.ContextWithSubject(ctx, &auth.Subject{User: "admin", Tenants: auth.AllTenants})

		tx, err := database.TxFromContext(ctx)
		Expect(err).ToNot(HaveOccurred())
		_, err = database.UpsertBillingAccount(ctx, tx, database.BillingAccountUpsert{
			ID: id, ProviderKey: billingfake.Key, ProviderAccountReference: id, State: "ACTIVE",
		})
		Expect(err).ToNot(HaveOccurred())

		created, err := server.Create(adminCtx, publicv1.TenantBillingAssignmentsCreateRequest_builder{
			Object: publicv1.TenantBillingAssignment_builder{
				Metadata: publicv1.Metadata_builder{Name: "asg-1"}.Build(),
				Spec: publicv1.TenantBillingAssignmentSpec_builder{
					TenantId: testTenant, BillingAccountId: id,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(created.GetObject().GetStatus().GetConnectionState()).To(Equal(
			publicv1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED))

		got, err := server.Get(adminCtx, publicv1.TenantBillingAssignmentsGetRequest_builder{
			Id: created.GetObject().GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(got.GetObject().GetSpec().GetTenantId()).To(Equal(testTenant))

		listed, err := server.List(adminCtx, &publicv1.TenantBillingAssignmentsListRequest{})
		Expect(err).ToNot(HaveOccurred())
		Expect(listed.GetTotal()).To(Equal(int32(1)))
	})

	It("updates an assignment to another account through the public API", func() {
		id1, id2 := uuid.New(), uuid.New()
		fake := billingfake.New(billingfake.Scenario{
			CorrelationWrite: true,
			Accounts: []billingfake.AccountSpec{
				{ID: id1, State: billing.AccountActive}, {ID: id2, State: billing.AccountActive},
			},
		})
		server := build(fake.Provider())
		adminCtx := auth.ContextWithSubject(ctx, &auth.Subject{User: "admin", Tenants: auth.AllTenants})

		tx, err := database.TxFromContext(ctx)
		Expect(err).ToNot(HaveOccurred())
		for _, id := range []string{id1, id2} {
			_, err = database.UpsertBillingAccount(ctx, tx, database.BillingAccountUpsert{
				ID: id, ProviderKey: billingfake.Key, ProviderAccountReference: id, State: "ACTIVE",
			})
			Expect(err).ToNot(HaveOccurred())
		}

		created, err := server.Create(adminCtx, publicv1.TenantBillingAssignmentsCreateRequest_builder{
			Object: publicv1.TenantBillingAssignment_builder{
				Metadata: publicv1.Metadata_builder{Name: "asg-1"}.Build(),
				Spec:     publicv1.TenantBillingAssignmentSpec_builder{TenantId: testTenant, BillingAccountId: id1}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// The public Update converts the object, passes the update mask through, and converts the response
		// back: reassigning to a second account must close the first interval and open a new one.
		updated, err := server.Update(adminCtx, publicv1.TenantBillingAssignmentsUpdateRequest_builder{
			Object: publicv1.TenantBillingAssignment_builder{
				Id:   created.GetObject().GetId(),
				Spec: publicv1.TenantBillingAssignmentSpec_builder{TenantId: testTenant, BillingAccountId: id2}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.billing_account_id"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(updated.GetObject().GetSpec().GetBillingAccountId()).To(Equal(id2))
		Expect(updated.GetObject().GetStatus().GetConnectionState()).To(Equal(
			publicv1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED))

		listed, err := server.List(adminCtx, &publicv1.TenantBillingAssignmentsListRequest{})
		Expect(err).ToNot(HaveOccurred())
		Expect(listed.GetTotal()).To(Equal(int32(2)), "the closed interval and the new one are both listed")
		current := 0
		for _, item := range listed.GetItems() {
			if item.GetStatus().GetEffectiveTo() == nil {
				current++
				Expect(item.GetSpec().GetBillingAccountId()).To(Equal(id2))
			}
		}
		Expect(current).To(Equal(1), "exactly one current interval remains after the reassignment")
	})
})
