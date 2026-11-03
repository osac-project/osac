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
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/testing/billingfake"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Billing accounts server", func() {
	build := func(p *billing.Provider) *BillingAccountsServer {
		private, err := NewPrivateBillingAccountsServer().SetLogger(logger).SetProvider(p).Build()
		Expect(err).ToNot(HaveOccurred())
		server, err := NewBillingAccountsServer().SetLogger(logger).SetDelegate(private).Build()
		Expect(err).ToNot(HaveOccurred())
		return server
	}

	It("requires a logger and a delegate", func() {
		_, err := NewBillingAccountsServer().Build()
		Expect(err).To(MatchError("logger is mandatory"))
		_, err = NewBillingAccountsServer().SetLogger(logger).Build()
		Expect(err).To(MatchError("delegate is mandatory"))
	})

	It("never exposes provider_account_reference: the public message has no such field", func() {
		_, hasField := reflect.TypeOf((*publicv1.BillingAccount)(nil)).Elem().FieldByName("ProviderAccountReference")
		Expect(hasField).To(BeFalse())
	})

	It("maps a discovered account end to end, with no provider account number, IsMaster or BillingDay field",
		func() {
			fake := billingfake.New(billingfake.Scenario{Accounts: []billingfake.AccountSpec{
				{ID: "acct-1", Name: "Acme", ExternalID: "ext-1", State: billing.AccountActive},
			}})
			response, err := build(fake.Provider()).List(ctx, &publicv1.BillingAccountsListRequest{})
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetSize()).To(Equal(int32(1)))
			Expect(response.GetTotal()).To(Equal(int32(1)))
			item := response.GetItems()[0]
			Expect(item.GetProviderKey()).To(Equal("fake"))
			Expect(item.GetDisplayName()).To(Equal("Acme"))
			Expect(item.GetCorrelationId()).To(Equal("ext-1"))
			Expect(item.GetState()).To(Equal(publicv1.BillingAccountState_BILLING_ACCOUNT_STATE_ACTIVE))
			Expect(item.GetBaseCurrency()).To(BeEmpty())
			typ := reflect.TypeOf(item).Elem()
			for _, name := range []string{"ProviderAccountReference", "Number", "IsMaster", "BillingDay"} {
				_, ok := typ.FieldByName(name)
				Expect(ok).To(BeFalse(), "public BillingAccount must not carry %s", name)
			}
		})

	It("passes the shared list params (filter, order, limit, offset) through to the private request", func() {
		fake := billingfake.New(billingfake.Scenario{Accounts: []billingfake.AccountSpec{
			{ID: "a1", Name: "Beta", State: billing.AccountActive},
			{ID: "a2", Name: "Alpha", State: billing.AccountInactive},
		}})
		server := build(fake.Provider())

		filtered, err := server.List(ctx, publicv1.BillingAccountsListRequest_builder{
			Filter: proto.String(`this.state == "INACTIVE"`),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(filtered.GetTotal()).To(Equal(int32(1)))
		Expect(filtered.GetItems()[0].GetDisplayName()).To(Equal("Alpha"))

		ordered, err := server.List(ctx, publicv1.BillingAccountsListRequest_builder{
			Order: proto.String("display_name"),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(ordered.GetItems()[0].GetDisplayName()).To(Equal("Alpha"))

		paged, err := server.List(ctx, publicv1.BillingAccountsListRequest_builder{
			Order: proto.String("display_name"), Offset: proto.Int32(1), Limit: proto.Int32(1),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(paged.GetSize()).To(Equal(int32(1)))
		Expect(paged.GetItems()[0].GetDisplayName()).To(Equal("Beta"))
	})

	It("propagates FAILED_PRECONDITION when billing is disabled", func() {
		fake := billingfake.New(billingfake.Scenario{})
		_, err := build(billing.NewProvider("", fake, nil)).List(ctx, &publicv1.BillingAccountsListRequest{})
		st, ok := status.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.FailedPrecondition))
	})

	It("propagates UNAVAILABLE when the provider cannot be reached", func() {
		fake := billingfake.New(billingfake.Scenario{
			Failures: map[string]billingfake.Failure{"ListAccounts": {Reason: billing.ReasonUnavailable}},
		})
		_, err := build(fake.Provider()).List(ctx, &publicv1.BillingAccountsListRequest{})
		st, ok := status.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.Unavailable))
	})

	It("returns an empty items list, not an error, when no accounts are discovered", func() {
		fake := billingfake.New(billingfake.Scenario{})
		response, err := build(fake.Provider()).List(ctx, &publicv1.BillingAccountsListRequest{})
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetItems()).To(BeEmpty())
		Expect(response.GetTotal()).To(BeZero())
	})

	// SECURITY: BillingAccount is excluded from the reflection-driven filter-oracle suite in
	// internal/cmd/service/start/grpcserver/register_servers_test.go because it is registered standalone in
	// start_grpc_server_cmd.go, not through RegisterResourceServers, and uses its own allowlist-based filter
	// translator (internal/database/billing_account.go) instead of dao.FilterTranslator. This is the only
	// regression coverage confirming the public BillingAccountsServer's private delegate still rejects a filter
	// referencing provider_account_reference, the private-only field (private billing_account_type.proto field 8,
	// absent from the public BillingAccount message).
	It("rejects a filter referencing the private-only provider_account_reference field", func() {
		fake := billingfake.New(billingfake.Scenario{Accounts: []billingfake.AccountSpec{{ID: "acct-1"}}})
		_, err := build(fake.Provider()).List(ctx, publicv1.BillingAccountsListRequest_builder{
			// Syntactically valid under this table's own filter grammar (this.<field> == "<value>"); the field
			// name itself must still be rejected because it is not in the public allowlist.
			Filter: proto.String(`this.provider_account_reference == "acct-1"`),
		}.Build())
		Expect(err).To(HaveOccurred())
		st, ok := status.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.InvalidArgument))
	})

	It("accepts a filter referencing only the public provider_key field, as a positive control", func() {
		fake := billingfake.New(billingfake.Scenario{Accounts: []billingfake.AccountSpec{{ID: "acct-1"}}})
		response, err := build(fake.Provider()).List(ctx, publicv1.BillingAccountsListRequest_builder{
			Filter: proto.String(`this.provider_key == "fake"`),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetTotal()).To(Equal(int32(1)))
	})
})
