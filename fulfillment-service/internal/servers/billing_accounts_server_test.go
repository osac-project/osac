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
	"encoding/json"
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/collections"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/testing/billingfake"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Billing accounts server", func() {
	BeforeEach(func() {
		Expect(suiteTx.End(ctx)).To(Succeed())
		suiteTx = nil
		ctx = database.TxManagerIntoContext(context.Background(), tm)
	})
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

	It("rolls discovery and freshness back on strict public mapping failure, then retries the walk", func() {
		fake := billingfake.New(billingfake.Scenario{Accounts: []billingfake.AccountSpec{{ID: "private-native", State: billing.AccountActive}}})
		server := build(fake.Provider())
		server.outMapper.strict = true
		_, err := billingRPC(ctx, server.List, &publicv1.BillingAccountsListRequest{})
		Expect(status.Code(err)).To(Equal(codes.Internal))
		var count int
		Expect(testPool.QueryRow(ctx, `select count(*) from billing_accounts`).Scan(&count)).To(Succeed())
		Expect(count).To(BeZero())
		Expect(server.private.lastDiscovery).To(BeZero())
		Expect(server.private.discoverInFlight).To(BeFalse())
		server.outMapper.strict = false
		response, err := billingRPC(ctx, server.List, &publicv1.BillingAccountsListRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(response.GetTotal()).To(Equal(int32(1)))
		Expect(fake.Count("ListAccounts")).To(Equal(2))
		Expect(server.private.lastDiscovery).NotTo(BeZero())
	})

	It("keeps account Get, Create and Delete as side-effect-free descriptor stubs", func() {
		fake := billingfake.New(billingfake.Scenario{})
		server := build(fake.Provider())
		_, err := server.Get(ctx, publicv1.BillingAccountsGetRequest_builder{Id: "id"}.Build())
		Expect(status.Code(err)).To(Equal(codes.Unimplemented))
		_, err = server.Create(ctx, publicv1.BillingAccountsCreateRequest_builder{Object: &publicv1.BillingAccount{}}.Build())
		Expect(status.Code(err)).To(Equal(codes.Unimplemented))
		_, err = server.Delete(ctx, publicv1.BillingAccountsDeleteRequest_builder{Id: "id"}.Build())
		Expect(status.Code(err)).To(Equal(codes.Unimplemented))
		Expect(fake.Calls()).To(BeEmpty())
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
			response, err := billingRPC(ctx, build(fake.Provider()).List, &publicv1.BillingAccountsListRequest{})
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetSize()).To(Equal(int32(1)))
			Expect(response.GetTotal()).To(Equal(int32(1)))
			item := response.GetItems()[0]
			Expect(item.GetStatus().GetProviderKey()).To(Equal("fake"))
			Expect(item.GetStatus().GetDisplayName()).To(Equal("Acme"))
			Expect(item.GetStatus().GetExternalBillingAccountId()).To(Equal("ext-1"))
			Expect(item.GetStatus().GetState()).To(Equal(publicv1.BillingAccountState_BILLING_ACCOUNT_STATE_ACTIVE))
			Expect(item.GetStatus().GetBaseCurrency()).To(BeEmpty())
			encoded, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(item)
			Expect(err).ToNot(HaveOccurred())
			var body map[string]any
			Expect(json.Unmarshal(encoded, &body)).To(Succeed())
			Expect(body["status"]).To(HaveKeyWithValue("provider_key", "fake"))
			Expect(body["status"]).To(HaveKeyWithValue("external_billing_account_id", "ext-1"))
			Expect(body["status"]).ToNot(HaveKey("provider_account_reference"))
			Expect(body).To(HaveKey("metadata"))
			Expect(body).ToNot(HaveKey("provider_key"))
			Expect(string(encoded)).ToNot(ContainSubstring("acct-1"))
			Expect(item.GetMetadata().GetName()).To(HavePrefix("ba-"))
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

		filtered, err := billingRPC(ctx, server.List, publicv1.BillingAccountsListRequest_builder{
			Filter: proto.String(`this.status.state == 2`),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(filtered.GetTotal()).To(Equal(int32(1)))
		Expect(filtered.GetItems()[0].GetStatus().GetDisplayName()).To(Equal("Alpha"))

		ordered, err := billingRPC(ctx, server.List, publicv1.BillingAccountsListRequest_builder{
			Order: proto.String("status.display_name"),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(ordered.GetItems()[0].GetStatus().GetDisplayName()).To(Equal("Alpha"))

		paged, err := billingRPC(ctx, server.List, publicv1.BillingAccountsListRequest_builder{
			Order: proto.String("status.display_name"), Offset: proto.Int32(1), Limit: proto.Int32(1),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(paged.GetSize()).To(Equal(int32(1)))
		Expect(paged.GetItems()[0].GetStatus().GetDisplayName()).To(Equal("Beta"))
	})

	It("propagates FAILED_PRECONDITION when billing is disabled", func() {
		fake := billingfake.New(billingfake.Scenario{})
		_, err := billingRPC(ctx, build(billing.NewProvider("", fake, nil)).List, &publicv1.BillingAccountsListRequest{})
		st, ok := status.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.FailedPrecondition))
	})

	It("propagates UNAVAILABLE when the provider cannot be reached", func() {
		fake := billingfake.New(billingfake.Scenario{
			Failures: map[string]billingfake.Failure{"ListAccounts": {Reason: billing.ReasonUnavailable}},
		})
		_, err := billingRPC(ctx, build(fake.Provider()).List, &publicv1.BillingAccountsListRequest{})
		st, ok := status.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.Unavailable))
	})

	It("returns an empty items list, not an error, when no accounts are discovered", func() {
		fake := billingfake.New(billingfake.Scenario{})
		response, err := billingRPC(ctx, build(fake.Provider()).List, &publicv1.BillingAccountsListRequest{})
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetItems()).To(BeEmpty())
		Expect(response.GetTotal()).To(BeZero())
	})

	// SECURITY: BillingAccount is excluded from the reflection-driven filter-oracle suite because it is registered
	// standalone and uses its own allowlist-based translator. This is the only regression coverage confirming the
	// public server's private delegate still rejects a filter referencing provider_account_reference, the
	// private-only field.
	It("rejects a filter referencing the private-only provider_account_reference field", func() {
		fake := billingfake.New(billingfake.Scenario{Accounts: []billingfake.AccountSpec{{ID: "acct-1"}}})
		_, err := billingRPC(ctx, build(fake.Provider()).List, publicv1.BillingAccountsListRequest_builder{
			// Syntactically valid under this table's own filter grammar (this.<field> == "<value>"); the field
			// name itself must still be rejected because it is not in the public allowlist.
			Filter: proto.String(`this.status.provider_account_reference == "acct-1"`),
		}.Build())
		Expect(err).To(HaveOccurred())
		st, ok := status.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.InvalidArgument))
	})

	It("accepts a filter referencing only the public provider_key field, as a positive control", func() {
		fake := billingfake.New(billingfake.Scenario{Accounts: []billingfake.AccountSpec{{ID: "acct-1"}}})
		response, err := billingRPC(ctx, build(fake.Provider()).List, publicv1.BillingAccountsListRequest_builder{
			Filter: proto.String(`this.status.provider_key == "fake"`),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetTotal()).To(Equal(int32(1)))
	})

	It("serves rows durably committed by an earlier request to a fresh server instance", func() {
		fake := billingfake.New(billingfake.Scenario{Accounts: []billingfake.AccountSpec{
			{ID: "acct-1", State: billing.AccountActive},
		}})
		first := build(fake.Provider())
		response, err := billingRPC(ctx, first.List, &publicv1.BillingAccountsListRequest{})
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetTotal()).To(Equal(int32(1)))

		// A second server instance over the same storage, a restarted process or another pod, reads the rows the
		// first request committed. The freshness window remains per-process: a second pod still re-walks the
		// provider inside the first pod's window, and cross-pod freshness sharing is not covered here.
		second := build(fake.Provider())
		response, err = billingRPC(ctx, second.List, &publicv1.BillingAccountsListRequest{})
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetTotal()).To(Equal(int32(1)))
		Expect(fake.Count("ListAccounts")).To(Equal(2), "a fresh instance has no freshness window yet")
	})

	DescribeTable("rejects unsupported public order paths",
		func(order string) {
			fake := billingfake.New(billingfake.Scenario{})
			_, err := billingRPC(ctx, build(fake.Provider()).List, publicv1.BillingAccountsListRequest_builder{
				Order: proto.String(order),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		},
		Entry("private native identity", "status.provider_account_reference"),
		Entry("legacy alias", "display_name"),
		Entry("unsupported metadata scalar", "metadata.creator"),
		Entry("finalizers", "metadata.finalizers"),
		Entry("timestamp submember", "status.state_observation_timestamp.seconds"),
		Entry("SQL", "id; select 1"),
	)

	It("keeps count independent of paging and uses the billing explicit-zero default", func() {
		fake := billingfake.New(billingfake.Scenario{Accounts: []billingfake.AccountSpec{
			{ID: "a", Name: "Same", State: billing.AccountActive},
			{ID: "b", Name: "Same", State: billing.AccountActive},
			{ID: "c", Name: "Other", State: billing.AccountInactive},
		}})
		server := build(fake.Provider())
		request := publicv1.BillingAccountsListRequest_builder{
			Filter: proto.String(`this.status.state == 1`), Order: proto.String("status.display_name desc"),
			Limit: proto.Int32(0),
		}.Build()
		all, err := billingRPC(ctx, server.List, request)
		Expect(err).ToNot(HaveOccurred())
		Expect(all.GetTotal()).To(Equal(int32(2)))
		Expect(all.GetItems()).To(HaveLen(2))
		Expect(all.GetItems()[0].GetId() < all.GetItems()[1].GetId()).To(BeTrue())
		request.SetLimit(1)
		request.SetOffset(1)
		page, err := billingRPC(ctx, server.List, request)
		Expect(err).ToNot(HaveOccurred())
		Expect(page.GetTotal()).To(Equal(int32(2)))
		Expect(page.GetItems()).To(HaveLen(1))
		Expect(page.GetItems()[0].GetId()).To(Equal(all.GetItems()[1].GetId()))
		request.SetOffset(-1)
		_, err = billingRPC(ctx, server.List, request)
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
	})

	DescribeTable("uses platform account scope independently of caller-selected customer tenants",
		func(tenants collections.Set[string]) {
			fake := billingfake.New(billingfake.Scenario{Accounts: []billingfake.AccountSpec{{ID: "a", Name: "Platform"}}})
			server := build(fake.Provider())
			caller := auth.ContextWithSubject(ctx, &auth.Subject{User: "authorized-platform-admin", Tenants: tenants})
			response, err := billingRPC(caller, server.List, publicv1.BillingAccountsListRequest_builder{
				Filter: proto.String(`this.metadata.tenant == "system"`), Limit: proto.Int32(1),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(1))
			Expect(response.GetTotal()).To(Equal(int32(1)))
			Expect(response.GetItems()[0].GetStatus().GetDisplayName()).To(Equal("Platform"))
		},
		Entry("finite customer", collections.NewSet("customer")),
		Entry("empty", collections.NewSet[string]()),
		Entry("universal", auth.AllTenants),
		Entry("system excluded", auth.AllTenants.Difference(auth.SystemTenants)),
	)

	DescribeTable("rejects private, reserved and legacy filter paths through the public descriptor",
		func(filter string) {
			server := build(billingfake.New(billingfake.Scenario{}).Provider())
			_, err := billingRPC(ctx, server.List, publicv1.BillingAccountsListRequest_builder{Filter: proto.String(filter)}.Build())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		},
		Entry("private native identity", `this.status.provider_account_reference == "native"`),
		Entry("finalizer presence", `has(this.metadata.finalizers)`),
		Entry("legacy flat state", `this.state == 1`),
		Entry("legacy token", `this.status.correlation_id == "token"`),
	)
})
