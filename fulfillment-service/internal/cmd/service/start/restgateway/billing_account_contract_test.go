/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package restgateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

type publicBillingAccountsClient struct {
	publicv1.BillingAccountsClient
	account *publicv1.BillingAccount
}

func (c publicBillingAccountsClient) List(context.Context, *publicv1.BillingAccountsListRequest,
	...grpc.CallOption) (*publicv1.BillingAccountsListResponse, error) {
	return publicv1.BillingAccountsListResponse_builder{
		Items: []*publicv1.BillingAccount{c.account}, Size: 1, Total: 1,
	}.Build(), nil
}

type privateBillingAccountsClient struct {
	privatev1.BillingAccountsClient
	account *privatev1.BillingAccount
}

func (c privateBillingAccountsClient) List(context.Context, *privatev1.BillingAccountsListRequest,
	...grpc.CallOption) (*privatev1.BillingAccountsListResponse, error) {
	return privatev1.BillingAccountsListResponse_builder{
		Items: []*privatev1.BillingAccount{c.account}, Size: 1, Total: 1,
	}.Build(), nil
}

// Match the JSONPb options and HTTPBody wrapper in start_rest_gateway_cmd.go.
// Client doubles below omit authorization, persistence and provider discovery.
func billingAccountContractMarshaler() *runtime.HTTPBodyMarshaler {
	return &runtime.HTTPBodyMarshaler{Marshaler: &runtime.JSONPb{
		MarshalOptions: protojson.MarshalOptions{UseProtoNames: true},
	}}
}

// This is the isolated, flat P1 rename contract, not compatibility evidence for
// the later status envelope (which deliberately has a different tag/presence).
var _ = Describe("Billing account identifier contract", func() {
	wire := []byte{0x3a, 0x05, 'e', 'x', 't', '-', '1'} // 3a056578742d31
	for _, layer := range []struct {
		name       string
		newAccount func() proto.Message
	}{
		{"public", func() proto.Message { return &publicv1.BillingAccount{} }},
		{"private", func() proto.Message { return &privatev1.BillingAccount{} }},
	} {
		Describe(layer.name, func() {
			var account proto.Message
			BeforeEach(func() {
				account = layer.newAccount()
			})

			It("names string field 7 canonically without retaining the old descriptor name", func() {
				fields := account.ProtoReflect().Descriptor().Fields()
				field := fields.ByNumber(7)
				Expect(field).ToNot(BeNil())
				Expect(field.Kind()).To(Equal(protoreflect.StringKind))
				Expect(field.Cardinality()).To(Equal(protoreflect.Optional))
				Expect(field.Name()).To(Equal(protoreflect.Name("external_billing_account_id")))
				Expect(field.JSONName()).To(Equal("externalBillingAccountId"))
				Expect(fields.ByName("external_billing_account_id")).To(Equal(field))
				Expect(fields.ByName("correlation_id")).To(BeNil())
			})

			It("decodes and re-encodes the original tag-7 wire example unchanged", func() {
				Expect(proto.Unmarshal(wire, account)).To(Succeed())
				field := account.ProtoReflect().Descriptor().Fields().ByNumber(7)
				Expect(field).ToNot(BeNil())
				Expect(account.ProtoReflect().Get(field).String()).To(Equal("ext-1"))
				encoded, err := proto.Marshal(account)
				Expect(err).ToNot(HaveOccurred())
				Expect(encoded).To(Equal(wire))
			})

			It("retains implicit string presence and omits unset and explicitly empty values", func() {
				message := account.ProtoReflect()
				field := message.Descriptor().Fields().ByNumber(7)
				Expect(field).ToNot(BeNil())
				Expect(field.HasPresence()).To(BeFalse(), "P1 does not introduce proto3 optional")
				Expect(message.Has(field)).To(BeFalse())
				message.Set(field, protoreflect.ValueOfString(""))
				Expect(message.Has(field)).To(BeFalse())
				encoded, err := proto.Marshal(account)
				Expect(err).ToNot(HaveOccurred())
				Expect(encoded).To(BeEmpty())
				encoded, err = billingAccountContractMarshaler().Marshal(account)
				Expect(err).ToNot(HaveOccurred())
				Expect(encoded).To(MatchJSON(`{}`))
			})

			DescribeTable("retains the exact raw string on the wire", func(raw string) {
				field := account.ProtoReflect().Descriptor().Fields().ByNumber(7)
				Expect(field).ToNot(BeNil())
				account.ProtoReflect().Set(field, protoreflect.ValueOfString(raw))
				encoded, err := proto.Marshal(account)
				Expect(err).ToNot(HaveOccurred())
				decoded := layer.newAccount()
				Expect(proto.Unmarshal(encoded, decoded)).To(Succeed())
				Expect(decoded.ProtoReflect().Get(field).String()).To(Equal(raw))
			},
				Entry("padded token", " ext-1 "),
				Entry("whitespace-only token", " \t "),
				Entry("quotes and backslash", `ext-"1\raw`),
			)

			It("emits the canonical camel-case name with default protoJSON", func() {
				Expect(proto.Unmarshal(wire, account)).To(Succeed())
				encoded, err := protojson.Marshal(account)
				Expect(err).ToNot(HaveOccurred())
				Expect(encoded).To(MatchJSON(`{"externalBillingAccountId":"ext-1"}`))
			})

			It("emits only the canonical proto-name key with the REST marshaler", func() {
				Expect(proto.Unmarshal(wire, account)).To(Succeed())
				encoded, err := billingAccountContractMarshaler().Marshal(account)
				Expect(err).ToNot(HaveOccurred())
				Expect(encoded).To(MatchJSON(`{"external_billing_account_id":"ext-1"}`))
			})

			DescribeTable("accepts the new JSON spelling through the REST decoder", func(key string) {
				body := fmt.Sprintf(`{"%s":" ext-1 "}`, key)
				Expect(billingAccountContractMarshaler().NewDecoder(strings.NewReader(body)).Decode(account)).To(Succeed())
				field := account.ProtoReflect().Descriptor().Fields().ByNumber(7)
				Expect(field).ToNot(BeNil())
				Expect(account.ProtoReflect().Get(field).String()).To(Equal(" ext-1 "))
			},
				Entry("proto name", "external_billing_account_id"),
				Entry("JSON name", "externalBillingAccountId"),
			)

			DescribeTable("rejects the former JSON spelling through the strict REST decoder", func(key string) {
				body := fmt.Sprintf(`{"%s":"ext-1"}`, key)
				err := billingAccountContractMarshaler().NewDecoder(strings.NewReader(body)).Decode(account)
				Expect(err).To(MatchError(ContainSubstring("unknown field")))
			},
				Entry("proto name", "correlation_id"),
				Entry("JSON name", "correlationId"),
			)
		})
	}

	DescribeTable("serves only the canonical proto-name key through the generated List route", func(layer string) {
		ctx := context.Background()
		mux := runtime.NewServeMux(runtime.WithMarshalerOption(runtime.MIMEWildcard, billingAccountContractMarshaler()))
		public := &publicv1.BillingAccount{}
		private := &privatev1.BillingAccount{}
		Expect(proto.Unmarshal(wire, public)).To(Succeed())
		Expect(proto.Unmarshal(wire, private)).To(Succeed())
		Expect(publicv1.RegisterBillingAccountsHandlerClient(ctx, mux,
			publicBillingAccountsClient{account: public})).To(Succeed())
		Expect(privatev1.RegisterBillingAccountsHandlerClient(ctx, mux,
			privateBillingAccountsClient{account: private})).To(Succeed())
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/"+layer+"/v1/billing/accounts", nil))
		Expect(response.Code).To(Equal(http.StatusOK))
		Expect(response.Body.String()).To(MatchJSON(`{
			"items":[{"external_billing_account_id":"ext-1"}],"size":1,"total":1
		}`))
	},
		Entry("public", "fulfillment"),
		Entry("private", "private"),
	)
})
