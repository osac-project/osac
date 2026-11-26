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

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/collections"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/testing/billingfake"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = DescribeTable("Cross-tenant billing account reuse after clock rollback", func(replace bool) {
	Expect(suiteTx.End(ctx)).To(Succeed())
	suiteTx = nil
	const otherTenant = "receiving-tenant"
	_, err := testPool.Exec(ctx, `insert into tenants (id, name, tenant, creator, data) values ($1, $1, $1, 'system', '{}')`, otherTenant)
	Expect(err).NotTo(HaveOccurred())

	provider := billingfake.New(billingfake.Scenario{Accounts: []billingfake.AccountSpec{
		{ID: "reused-account", State: billing.AccountActive},
		{ID: "current-account", State: billing.AccountActive},
	}}).Provider()
	accounts, err := NewPrivateBillingAccountsServer().SetLogger(logger).SetProvider(provider).Build()
	Expect(err).NotTo(HaveOccurred())
	assignments, err := NewPrivateTenantBillingAssignmentsServer().SetLogger(logger).SetProvider(provider).Build()
	Expect(err).NotTo(HaveOccurred())
	rpcCtx := database.TxManagerIntoContext(context.Background(), tm)
	admin := auth.ContextWithSubject(rpcCtx, &auth.Subject{User: "admin", Tenants: auth.AllTenants})
	recipient := auth.ContextWithSubject(rpcCtx, &auth.Subject{User: "recipient", Tenants: collections.NewSet(otherTenant)})
	_, err = billingRPC(admin, accounts.List, &privatev1.BillingAccountsListRequest{})
	Expect(err).NotTo(HaveOccurred())
	var reusedAccount, currentAccount string
	Expect(testPool.QueryRow(ctx, `select id from billing_accounts where data->'status'->>'provider_account_reference' = 'reused-account'`).Scan(&reusedAccount)).To(Succeed())
	Expect(testPool.QueryRow(ctx, `select id from billing_accounts where data->'status'->>'provider_account_reference' = 'current-account'`).Scan(&currentAccount)).To(Succeed())

	object := func(tenantID, accountID string) *privatev1.TenantBillingAssignment {
		return privatev1.TenantBillingAssignment_builder{
			Metadata: privatev1.Metadata_builder{Name: "assignment", Tenant: tenantID}.Build(),
			Spec: privatev1.TenantBillingAssignmentSpec_builder{
				BillingAccount: privatev1.BillingAccountReference_builder{Id: accountID}.Build(),
			}.Build(),
		}.Build()
	}
	prior, err := billingRPC(admin, assignments.Create, privatev1.TenantBillingAssignmentsCreateRequest_builder{Object: object(testTenant, reusedAccount)}.Build())
	Expect(err).NotTo(HaveOccurred())
	// Simulate rollback without changing the server clock: persist a close ahead of the time
	// PostgreSQL will sample for the receiving tenant. Leave the original start untouched.
	floor := prior.GetObject().GetStatus().GetEffectiveFromTimestamp().AsTime().Add(time.Hour)
	_, err = testPool.Exec(ctx, `update tenant_billing_assignments
		set data = jsonb_set(data, '{status,effective_to_timestamp}', to_jsonb($2::text))
		where id = $1`, prior.GetObject().GetId(), floor.Format("2006-01-02T15:04:05.000000Z"))
	Expect(err).NotTo(HaveOccurred())

	var opened *privatev1.TenantBillingAssignment
	if replace {
		current, err := billingRPC(recipient, assignments.Create, privatev1.TenantBillingAssignmentsCreateRequest_builder{Object: object(otherTenant, currentAccount)}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(current.GetObject().GetStatus().GetEffectiveFromTimestamp().AsTime()).To(BeTemporally("<", floor))
		replacement := object(otherTenant, reusedAccount)
		replacement.SetId(current.GetObject().GetId())
		updated, err := billingRPC(recipient, assignments.Update, privatev1.TenantBillingAssignmentsUpdateRequest_builder{Object: replacement}.Build())
		Expect(err).NotTo(HaveOccurred())
		opened = updated.GetObject()
		var oldEnd time.Time
		Expect(testPool.QueryRow(ctx, `select (data->'status'->>'effective_to_timestamp')::timestamptz from tenant_billing_assignments where id = $1`, current.GetObject().GetId()).Scan(&oldEnd)).To(Succeed())
		Expect(oldEnd).To(BeTemporally("==", opened.GetStatus().GetEffectiveFromTimestamp().AsTime()))
	} else {
		created, err := billingRPC(recipient, assignments.Create, privatev1.TenantBillingAssignmentsCreateRequest_builder{Object: object(otherTenant, reusedAccount)}.Build())
		Expect(err).NotTo(HaveOccurred())
		opened = created.GetObject()
	}
	var priorEnd, newStart time.Time
	Expect(testPool.QueryRow(ctx, `select (data->'status'->>'effective_to_timestamp')::timestamptz from tenant_billing_assignments where id = $1`, prior.GetObject().GetId()).Scan(&priorEnd)).To(Succeed())
	Expect(testPool.QueryRow(ctx, `select (data->'status'->>'effective_from_timestamp')::timestamptz from tenant_billing_assignments where id = $1`, opened.GetId()).Scan(&newStart)).To(Succeed())
	Expect(priorEnd).To(BeTemporally("==", floor))
	Expect(newStart).To(BeTemporally("==", priorEnd), "the reused account must not overlap the previous tenant's interval")
}, Entry("creates", false), Entry("replaces", true))
