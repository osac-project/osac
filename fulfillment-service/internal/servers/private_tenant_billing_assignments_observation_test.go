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
	"testing"
	"testing/synctest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/testing/billingfake"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func TestAssignmentObservationCapturePrecision(t *testing.T) {
	// Use the fake clock only for provider reads; the database test below exercises
	// ordering and durable outcomes without running real network I/O in a bubble.
	synctest.Test(t, func(t *testing.T) {
		g := NewWithT(t)
		const id, token = "precision-account", "precision-token"
		ref := billing.NewAccountRef(id)
		adapter := billingfake.New(billingfake.Scenario{Accounts: []billingfake.AccountSpec{{
			ID: id, ExternalID: token, State: billing.AccountActive,
		}}})
		start := time.Now().UTC()
		time.Sleep(251 * time.Nanosecond)
		s := &PrivateTenantBillingAssignmentsServer{}
		selected, observed, err := s.observeSelectedAccount(t.Context(), adapter, ref)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(observed.GetStateObservationTimestamp().AsTime()).To(BeTemporally("==", start.Add(251*time.Nanosecond)))

		time.Sleep(500 * time.Nanosecond)
		resolution, carrier, err := billing.ResolveObservedAccountCorrelationID(t.Context(), billing.AccountCorrelationInput{
			Adapter: adapter, AccountID: id, Ref: ref, LocalValue: token,
		}, selected)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(resolution.ProviderMatches).To(Equal(1))
		g.Expect(carrier).NotTo(BeNil())
		g.Expect(carrier.At).To(BeTemporally("==", start.Add(751*time.Nanosecond)))
	})
}

var _ = DescribeTable("Assignment state precedence within the same microsecond", func(storedOffset time.Duration, assignmentWins bool) {
	Expect(suiteTx.End(ctx)).To(Succeed())
	suiteTx = nil
	const id, token = "precision-account", "precision-token"
	name, err := billingAccountName(id)
	Expect(err).NotTo(HaveOccurred())
	s, err := NewPrivateTenantBillingAssignmentsServer().SetLogger(logger).
		SetProvider(billingfake.New(billingfake.Scenario{}).Provider()).Build()
	Expect(err).NotTo(HaveOccurred())
	admin := auth.ContextWithSubject(database.TxManagerIntoContext(context.Background(), tm),
		&auth.Subject{User: "admin", Tenants: auth.AllTenants})
	mirrorAt := time.Date(2026, 1, 1, 0, 0, 0, 123456000, time.UTC)
	storedAt := mirrorAt.Add(storedOffset)
	assignmentAt := mirrorAt.Add(751 * time.Nanosecond)
	_, err = testPool.Exec(ctx, `insert into billing_accounts (id, name, tenant, creator, data)
		values ($1, $2, 'system', 'system', jsonb_build_object('status', jsonb_build_object(
			'provider_key', $3::text, 'provider_account_reference', $1::text,
			'state', 'BILLING_ACCOUNT_STATE_ACTIVE', 'state_observation_timestamp', $4::text)))`,
		id, name, billingfake.Key, mirrorAt.Add(-time.Second).Format(time.RFC3339Nano))
	Expect(err).NotTo(HaveOccurred())

	var opened *privatev1.TenantBillingAssignment
	err = tm.Run(admin, func(tx database.Tx) error {
		txCtx := database.TxIntoContext(admin, tx)
		object, err := s.canonicalAssignment(txCtx, privatev1.TenantBillingAssignment_builder{
			Metadata: privatev1.Metadata_builder{Name: "precision-assignment", Tenant: testTenant}.Build(),
			Spec: privatev1.TenantBillingAssignmentSpec_builder{
				BillingAccount: privatev1.BillingAccountReference_builder{Id: id}.Build(),
			}.Build(),
		}.Build())
		if err != nil {
			return err
		}
		in := tenantBillingAssignmentAssignInput{
			Name: object.GetMetadata().GetName(), TenantID: testTenant, Creator: "admin", Object: object,
		}
		snapshot, err := s.assignmentSnapshot(txCtx, id, "", in, nil)
		if err != nil {
			return err
		}
		// Commit a competing inactive observation after the unlocked read. Its
		// observation time, not its commit order or the SQL mirror, decides.
		_, err = testPool.Exec(ctx, `update billing_accounts set data = jsonb_set(
			jsonb_set(data, '{status,state}', '"BILLING_ACCOUNT_STATE_INACTIVE"'),
			'{status,state_observation_timestamp}', to_jsonb($2::text)) where id = $1`,
			id, storedAt.Format(time.RFC3339Nano))
		if err != nil {
			return err
		}
		observed := privatev1.BillingAccountStatus_builder{
			State: mapBillingAccountState(string(billing.AccountActive)), StateObservationTimestamp: timestamppb.New(assignmentAt),
		}.Build()
		opened, err = s.finalizeAssignment(txCtx, snapshot, in, observed, billing.Resolution{
			Value: token, ProviderMatches: 1,
		}, tbaStateConnected, "")
		return err
	})

	wantAt := storedAt
	wantState, wantToken, wantIntervals := "BILLING_ACCOUNT_STATE_INACTIVE", "", 0
	if assignmentWins {
		Expect(err).NotTo(HaveOccurred())
		Expect(opened.GetStatus().GetConnectionState()).To(Equal(mapTenantBillingAssignmentConnectionState(tbaStateConnected)))
		var effectiveFrom time.Time
		Expect(testPool.QueryRow(ctx, `select (data->'status'->>'effective_from_timestamp')::timestamptz from tenant_billing_assignments
			where id = $1 and data->'status'->>'effective_to_timestamp' is null`, opened.GetId()).Scan(&effectiveFrom)).To(Succeed())
		Expect(effectiveFrom).To(BeTemporally("==", opened.GetStatus().GetEffectiveFromTimestamp().AsTime()))
		wantAt, wantState, wantToken, wantIntervals = assignmentAt, "BILLING_ACCOUNT_STATE_ACTIVE", token, 1
	} else {
		Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
		Expect(status.Convert(err).Message()).To(ContainSubstring("is inactive"))
		Expect(opened).To(BeNil())
	}
	var jsonAt, state, storedToken string
	Expect(testPool.QueryRow(ctx, `select data->'status'->>'state_observation_timestamp', data->'status'->>'state',
		coalesce(data->'status'->>'external_billing_account_id', '') from billing_accounts where id = $1`, id).
		Scan(&jsonAt, &state, &storedToken)).To(Succeed())
	Expect(jsonAt).To(Equal(wantAt.Format(time.RFC3339Nano)))
	Expect(state).To(Equal(wantState))
	Expect(storedToken).To(Equal(wantToken))
	var intervals int
	Expect(testPool.QueryRow(ctx, `select count(*) from tenant_billing_assignments`).Scan(&intervals)).To(Succeed())
	Expect(intervals).To(Equal(wantIntervals))
},
	Entry("a newer assignment observation adopts the token and opens the interval", 251*time.Nanosecond, true),
	Entry("a newer durable observation rejects token adoption and interval creation", 951*time.Nanosecond, false),
)
