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

	"buf.build/go/protovalidate"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

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

// The SQL column is `external_billing_account_id`; the flat tag-7 API field keeps its wire layout.
// The nested envelope below changes the wire contract, not just the JSON field names.
var _ = Describe("Billing account final API contract", func() {
	wire := []byte{0x22, 0x07, 0x32, 0x05, 'e', 'x', 't', '-', '1'}
	field := func(message protoreflect.MessageDescriptor, name protoreflect.Name,
		number protoreflect.FieldNumber, kind protoreflect.Kind) protoreflect.FieldDescriptor {
		result := message.Fields().ByName(name)
		ExpectWithOffset(1, result).ToNot(BeNil(), "%s.%s", message.FullName(), name)
		ExpectWithOffset(1, result.Number()).To(Equal(number))
		ExpectWithOffset(1, result.Kind()).To(Equal(kind))
		ExpectWithOffset(1, result.Cardinality()).To(Equal(protoreflect.Optional))
		return result
	}
	outputOnly := func(f protoreflect.FieldDescriptor) {
		ExpectWithOffset(1, proto.GetExtension(f.Options(), annotations.E_FieldBehavior)).To(
			ContainElement(annotations.FieldBehavior_OUTPUT_ONLY))
	}
	for _, layer := range []struct {
		name        string
		newAccount  func() proto.Message
		assignment  proto.Message
		services    protoreflect.FileDescriptor
		accounts    protoreflect.FileDescriptor
		accountRPC  *grpc.ServiceDesc
		assignRPC   *grpc.ServiceDesc
		accountStub any
		assignStub  any
	}{
		{"private", func() proto.Message { return &privatev1.BillingAccount{} }, &privatev1.TenantBillingAssignment{},
			privatev1.File_osac_private_v1_tenant_billing_assignments_service_proto,
			privatev1.File_osac_private_v1_billing_accounts_service_proto,
			&privatev1.BillingAccounts_ServiceDesc, &privatev1.TenantBillingAssignments_ServiceDesc,
			&privatev1.UnimplementedBillingAccountsServer{}, &privatev1.UnimplementedTenantBillingAssignmentsServer{}},
	} {
		Describe(layer.name, func() {
			var account proto.Message
			BeforeEach(func() {
				account = layer.newAccount()
			})

			It("has exactly the standard envelope and empty spec, reserving old flat fields", func() {
				d := account.ProtoReflect().Descriptor()
				Expect(d.Fields().Len()).To(Equal(4))
				field(d, "id", 1, protoreflect.StringKind)
				Expect(field(d, "metadata", 2, protoreflect.MessageKind).Message().Name()).To(Equal(protoreflect.Name("Metadata")))
				spec := field(d, "spec", 3, protoreflect.MessageKind).Message()
				Expect(spec.Name()).To(Equal(protoreflect.Name("BillingAccountSpec")))
				Expect(spec.Fields().Len()).To(BeZero())
				outputOnly(field(d, "status", 4, protoreflect.MessageKind))
				for n := protoreflect.FieldNumber(5); n <= 8; n++ {
					Expect(d.ReservedRanges().Has(n)).To(BeTrue())
				}
				for _, name := range []protoreflect.Name{"provider_key", "display_name", "base_currency", "state",
					"status_observed_at", "correlation_id", "external_billing_account_id", "provider_account_reference"} {
					Expect(d.Fields().ByName(name)).To(BeNil())
					Expect(d.ReservedNames().Has(name)).To(BeTrue(), "%s", name)
				}
			})

			It("has exact public status tags, optional token presence and private-only native identity", func() {
				d := field(account.ProtoReflect().Descriptor(), "status", 4, protoreflect.MessageKind).Message()
				for i, name := range []protoreflect.Name{"provider_key", "display_name", "base_currency"} {
					field(d, name, protoreflect.FieldNumber(i+1), protoreflect.StringKind)
				}
				state := field(d, "state", 4, protoreflect.EnumKind).Enum()
				Expect(state.Values().Len()).To(Equal(4))
				for i, name := range []protoreflect.Name{"BILLING_ACCOUNT_STATE_UNSPECIFIED", "BILLING_ACCOUNT_STATE_ACTIVE",
					"BILLING_ACCOUNT_STATE_INACTIVE", "BILLING_ACCOUNT_STATE_UNKNOWN"} {
					Expect(state.Values().ByNumber(protoreflect.EnumNumber(i)).Name()).To(Equal(name))
				}
				Expect(field(d, "state_observation_timestamp", 5, protoreflect.MessageKind).Message().FullName()).To(
					Equal(protoreflect.FullName("google.protobuf.Timestamp")))
				token := field(d, "external_billing_account_id", 6, protoreflect.StringKind)
				Expect(token.HasOptionalKeyword()).To(BeTrue())
				Expect(token.HasPresence()).To(BeTrue())
				Expect(token.JSONName()).To(Equal("externalBillingAccountId"))
				observed := field(d, "provider_external_id", 8, protoreflect.StringKind)
				Expect(observed.HasOptionalKeyword()).To(BeTrue())
				Expect(observed.HasPresence()).To(BeTrue())
				if layer.name == "private" {
					Expect(d.Fields().Len()).To(Equal(8))
					field(d, "provider_account_reference", 7, protoreflect.StringKind)
				} else {
					Expect(d.Fields().Len()).To(Equal(7))
					Expect(d.Fields().ByName("provider_account_reference")).To(BeNil())
				}
			})

			tokenField := func(message proto.Message) (protoreflect.Message, protoreflect.FieldDescriptor) {
				m := message.ProtoReflect()
				s := field(m.Descriptor(), "status", 4, protoreflect.MessageKind)
				m = m.Mutable(s).Message()
				return m, field(m.Descriptor(), "external_billing_account_id", 6, protoreflect.StringKind)
			}

			It("decodes and re-encodes the new status/tag-6 wire example", func() {
				Expect(proto.Unmarshal(wire, account)).To(Succeed())
				message, token := tokenField(account)
				Expect(message.Get(token).String()).To(Equal("ext-1"))
				encoded, err := proto.Marshal(account)
				Expect(err).ToNot(HaveOccurred())
				Expect(encoded).To(Equal(wire))
			})

			It("distinguishes absent, present-empty and cleared tokens on wire and JSON", func() {
				message, token := tokenField(account)
				Expect(message.Has(token)).To(BeFalse())
				encoded, err := billingAccountContractMarshaler().Marshal(account)
				Expect(err).ToNot(HaveOccurred())
				Expect(encoded).To(MatchJSON(`{"status":{}}`))
				message.Set(token, protoreflect.ValueOfString(""))
				Expect(message.Has(token)).To(BeTrue())
				encoded, err = proto.Marshal(account)
				Expect(err).ToNot(HaveOccurred())
				Expect(encoded).To(Equal([]byte{0x22, 0x02, 0x32, 0x00}))
				decoded := layer.newAccount()
				Expect(proto.Unmarshal(encoded, decoded)).To(Succeed())
				decodedStatus, decodedToken := tokenField(decoded)
				Expect(decodedStatus.Has(decodedToken)).To(BeTrue())
				encoded, err = billingAccountContractMarshaler().Marshal(decoded)
				Expect(err).ToNot(HaveOccurred())
				Expect(encoded).To(MatchJSON(`{"status":{"external_billing_account_id":""}}`))
				message.Clear(token)
				Expect(message.Has(token)).To(BeFalse())
			})

			It("does not reinterpret the former flat tag-7 token as the new nested token", func() {
				oldWire := []byte{0x3a, 0x05, 'e', 'x', 't', '-', '1'}
				Expect(proto.Unmarshal(oldWire, account)).To(Succeed())
				s := field(account.ProtoReflect().Descriptor(), "status", 4, protoreflect.MessageKind)
				Expect(account.ProtoReflect().Has(s)).To(BeFalse())
				Expect(account.ProtoReflect().GetUnknown()).To(Equal(protoreflect.RawFields(oldWire)))
				encoded, err := proto.Marshal(account)
				Expect(err).ToNot(HaveOccurred())
				Expect(encoded).To(Equal(oldWire), "unknown bytes survive; this is not semantic compatibility")
			})

			DescribeTable("retains the exact raw string on the wire", func(raw string) {
				message, token := tokenField(account)
				message.Set(token, protoreflect.ValueOfString(raw))
				encoded, err := proto.Marshal(account)
				Expect(err).ToNot(HaveOccurred())
				decoded := layer.newAccount()
				Expect(proto.Unmarshal(encoded, decoded)).To(Succeed())
				message, token = tokenField(decoded)
				Expect(message.Has(token)).To(BeTrue())
				Expect(message.Get(token).String()).To(Equal(raw))
			},
				Entry("present empty token", ""),
				Entry("padded token", " ext-1 "),
				Entry("whitespace-only token", " \t "),
				Entry("quotes and backslash", `ext-"1\raw`),
			)

			It("emits the canonical camel-case name with default protoJSON", func() {
				Expect(proto.Unmarshal(wire, account)).To(Succeed())
				encoded, err := protojson.Marshal(account)
				Expect(err).ToNot(HaveOccurred())
				Expect(encoded).To(MatchJSON(`{"status":{"externalBillingAccountId":"ext-1"}}`))
			})

			It("emits only the canonical proto-name key with the REST marshaler", func() {
				Expect(proto.Unmarshal(wire, account)).To(Succeed())
				encoded, err := billingAccountContractMarshaler().Marshal(account)
				Expect(err).ToNot(HaveOccurred())
				Expect(encoded).To(MatchJSON(`{"status":{"external_billing_account_id":"ext-1"}}`))
			})

			DescribeTable("accepts the new JSON spelling through the REST decoder", func(key string) {
				body := fmt.Sprintf(`{"status":{"%s":" ext-1 "}}`, key)
				Expect(billingAccountContractMarshaler().NewDecoder(strings.NewReader(body)).Decode(account)).To(Succeed())
				message, token := tokenField(account)
				Expect(message.Get(token).String()).To(Equal(" ext-1 "))
			},
				Entry("proto name", "external_billing_account_id"),
				Entry("JSON name", "externalBillingAccountId"),
			)

			DescribeTable("rejects retired flat and nested JSON spellings through the strict REST decoder", func(body string) {
				err := billingAccountContractMarshaler().NewDecoder(strings.NewReader(body)).Decode(account)
				Expect(err).To(MatchError(ContainSubstring("unknown field")))
			},
				Entry("old flat proto name", `{"correlation_id":"ext-1"}`),
				Entry("old flat JSON name", `{"correlationId":"ext-1"}`),
				Entry("retired new flat proto name", `{"external_billing_account_id":"ext-1"}`),
				Entry("retired new flat JSON name", `{"externalBillingAccountId":"ext-1"}`),
				Entry("old nested proto name", `{"status":{"correlation_id":"ext-1"}}`),
				Entry("old nested JSON name", `{"status":{"correlationId":"ext-1"}}`),
			)

			It("keeps provider key and label public and native identity private", func() {
				Expect(protojson.Unmarshal([]byte(`{"status":{"provider_key":"m360","display_name":"Provider label"}}`), account)).To(Succeed())
				encoded, err := billingAccountContractMarshaler().Marshal(account)
				Expect(err).ToNot(HaveOccurred())
				Expect(encoded).To(MatchJSON(`{"status":{"provider_key":"m360","display_name":"Provider label"}}`))
				err = protojson.Unmarshal([]byte(`{"status":{"provider_account_reference":"native-1"}}`), account)
				if layer.name == "private" {
					Expect(err).ToNot(HaveOccurred())
				} else {
					Expect(err).To(MatchError(ContainSubstring("unknown field")))
				}
			})

			It("preserves the assignment envelope and exact output-only interval status", func() {
				d := layer.assignment.ProtoReflect().Descriptor()
				Expect(d.Fields().Len()).To(Equal(4))
				field(d, "id", 1, protoreflect.StringKind)
				field(d, "metadata", 2, protoreflect.MessageKind)
				spec := field(d, "spec", 3, protoreflect.MessageKind).Message()
				Expect(spec.Fields().Len()).To(Equal(1))
				billingAccount := field(spec, "billing_account", 2, protoreflect.MessageKind)
				Expect(proto.GetExtension(billingAccount.Options(), annotations.E_FieldBehavior)).
					To(ContainElement(annotations.FieldBehavior_REQUIRED))
				for _, name := range []protoreflect.Name{"tenant", "tenant_id", "billing_account_id"} {
					Expect(spec.ReservedNames().Has(name)).To(BeTrue())
				}
				s := field(d, "status", 4, protoreflect.MessageKind)
				outputOnly(s)
				statusType := s.Message()
				Expect(statusType.Fields().Len()).To(Equal(4))
				state := field(statusType, "connection_state", 1, protoreflect.EnumKind)
				outputOnly(state)
				Expect(state.Enum().Values().Len()).To(Equal(4))
				for i, name := range []protoreflect.Name{"TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_UNSPECIFIED",
					"TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_PENDING", "TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED",
					"TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_FAILED"} {
					Expect(state.Enum().Values().ByNumber(protoreflect.EnumNumber(i)).Name()).To(Equal(name))
				}
				for i, name := range []protoreflect.Name{"effective_from_timestamp", "effective_to_timestamp"} {
					f := field(statusType, name, protoreflect.FieldNumber(i+2), protoreflect.MessageKind)
					Expect(f.Message().FullName()).To(Equal(protoreflect.FullName("google.protobuf.Timestamp")))
					outputOnly(f)
				}
				outputOnly(field(statusType, "message", 4, protoreflect.StringKind))
				Expect(statusType.ReservedNames().Has("effective_from")).To(BeTrue())
				Expect(statusType.ReservedNames().Has("effective_to")).To(BeTrue())
			})

			Describe("BillingAccountReference", func() {
				reference := func() protoreflect.MessageDescriptor {
					spec := field(layer.assignment.ProtoReflect().Descriptor(), "spec", 3, protoreflect.MessageKind).Message()
					return field(spec, "billing_account", 2, protoreflect.MessageKind).Message()
				}
				It("has only id/name fields in its target type file, without scope selectors", func() {
					d := reference()
					Expect(d.Name()).To(Equal(protoreflect.Name("BillingAccountReference")))
					Expect(d.ParentFile().Path()).To(HaveSuffix("/billing_account_type.proto"))
					Expect(d.Fields().Len()).To(Equal(2))
					field(d, "id", 1, protoreflect.StringKind)
					field(d, "name", 2, protoreflect.StringKind)
				})
				DescribeTable("validates supplied names and requires at least one identity", func(id, name string, valid bool) {
					d := reference()
					m := dynamicpb.NewMessage(d)
					m.Set(d.Fields().ByName("id"), protoreflect.ValueOfString(id))
					m.Set(d.Fields().ByName("name"), protoreflect.ValueOfString(name))
					err := protovalidate.Validate(m)
					if valid {
						Expect(err).ToNot(HaveOccurred())
					} else {
						Expect(err).To(HaveOccurred())
					}
				},
					Entry("neither", "", "", false),
					Entry("opaque id only", "opaque/id", "", true),
					Entry("name only", "", "a", true),
					Entry("both syntactically valid", "opaque/id", "account-a", true),
					Entry("63-character name", "", strings.Repeat("a", 63), true),
					Entry("uppercase", "id", "Account", false),
					Entry("leading hyphen", "id", "-account", false),
					Entry("trailing hyphen", "id", "account-", false),
					Entry("dotted", "id", "account.example", false),
					Entry("too long", "id", strings.Repeat("a", 64), false),
				)
			})

			It("requires the billing account reference", func() {
				spec := field(layer.assignment.ProtoReflect().Descriptor(), "spec", 3, protoreflect.MessageKind).Message()
				Expect(protovalidate.Validate(dynamicpb.NewMessage(spec))).To(HaveOccurred())
				account := spec.Fields().ByName("billing_account")
				Expect(account).ToNot(BeNil())
				Expect(account.Kind()).To(Equal(protoreflect.MessageKind))
				m := dynamicpb.NewMessage(spec)
				r := m.Mutable(account).Message()
				r.Set(r.Descriptor().Fields().ByName("id"), protoreflect.ValueOfString("id"))
				Expect(protovalidate.Validate(m)).To(Succeed())
			})

			It("exposes only the approved methods and keeps Signal private", func() {
				accounts := layer.accounts.Services().ByName("BillingAccounts")
				Expect(accounts.Methods().Len()).To(Equal(5))
				for _, name := range []protoreflect.Name{"List", "Get", "Create", "Update", "Delete"} {
					Expect(accounts.Methods().ByName(name)).ToNot(BeNil())
				}
				assignments := layer.services.Services().ByName("TenantBillingAssignments")
				count := 5
				if layer.name == "private" {
					count++
					Expect(assignments.Methods().ByName("Signal")).ToNot(BeNil())
				} else {
					Expect(assignments.Methods().ByName("Signal")).To(BeNil())
				}
				Expect(assignments.Methods().Len()).To(Equal(count))
				for _, name := range []protoreflect.Name{"List", "Get", "Create", "Update", "Delete"} {
					Expect(assignments.Methods().ByName(name)).ToNot(BeNil())
				}
			})

			DescribeTable("declares standard unsupported request/response and REST shapes", func(serviceName, methodName string) {
				file := layer.accounts
				resource, object := "accounts", "BillingAccount"
				if serviceName == "TenantBillingAssignments" {
					file, resource, object = layer.services, "tenant_billing_assignments", "TenantBillingAssignment"
				}
				method := file.Services().ByName(protoreflect.Name(serviceName)).Methods().ByName(protoreflect.Name(methodName))
				Expect(method).ToNot(BeNil())
				Expect(method.IsStreamingClient()).To(BeFalse())
				Expect(method.IsStreamingServer()).To(BeFalse())
				Expect(string(method.Input().Name())).To(Equal(serviceName + methodName + "Request"))
				Expect(string(method.Output().Name())).To(Equal(serviceName + methodName + "Response"))
				Expect(method.Input().Fields().Len()).To(Equal(1))
				if methodName == "Create" {
					Expect(string(field(method.Input(), "object", 1, protoreflect.MessageKind).Message().Name())).To(Equal(object))
				} else {
					field(method.Input(), "id", 1, protoreflect.StringKind)
				}
				if methodName == "Delete" {
					Expect(method.Output().Fields().Len()).To(BeZero())
				} else {
					Expect(method.Output().Fields().Len()).To(Equal(1))
					Expect(string(field(method.Output(), "object", 1, protoreflect.MessageKind).Message().Name())).To(Equal(object))
				}
				route := proto.GetExtension(method.Options(), annotations.E_Http).(*annotations.HttpRule)
				prefix := "/api/private/v1/billing/"
				switch methodName {
				case "Get":
					Expect(route.GetGet()).To(Equal(prefix + resource + "/{id}"))
					Expect(route.GetResponseBody()).To(Equal("object"))
				case "Create":
					Expect(route.GetPost()).To(Equal(prefix + resource))
					Expect(route.GetBody()).To(Equal("object"))
					Expect(route.GetResponseBody()).To(Equal("object"))
				case "Delete":
					Expect(route.GetDelete()).To(Equal(prefix + resource + "/{id}"))
					Expect(route.GetBody()).To(BeEmpty())
					Expect(route.GetResponseBody()).To(BeEmpty())
				}
			},
				Entry("account Get", "BillingAccounts", "Get"),
				Entry("account Create", "BillingAccounts", "Create"),
				Entry("account Delete", "BillingAccounts", "Delete"),
				Entry("assignment Delete", "TenantBillingAssignments", "Delete"),
			)

			DescribeTable("leaves descriptor-only operations Unimplemented in embedded stubs", func(assignment bool, methodName string) {
				description, stub := layer.accountRPC, layer.accountStub
				if assignment {
					description, stub = layer.assignRPC, layer.assignStub
				}
				for _, method := range description.Methods {
					if method.MethodName == methodName {
						_, err := method.Handler(stub, context.Background(), func(any) error { return nil }, nil)
						Expect(status.Code(err)).To(Equal(codes.Unimplemented))
						return
					}
				}
				Fail("missing generated method " + methodName)
			},
				Entry("account Get", false, "Get"),
				Entry("account Create", false, "Create"),
				Entry("account Delete", false, "Delete"),
				Entry("assignment Delete", true, "Delete"),
			)
		})
	}

	DescribeTable("serves only the canonical proto-name key through the private generated List route", func(layer string) {
		ctx := context.Background()
		mux := runtime.NewServeMux(runtime.WithMarshalerOption(runtime.MIMEWildcard, billingAccountContractMarshaler()))
		private := &privatev1.BillingAccount{}
		Expect(proto.Unmarshal(wire, private)).To(Succeed())
		Expect(privatev1.RegisterBillingAccountsHandlerClient(ctx, mux,
			privateBillingAccountsClient{account: private})).To(Succeed())
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/"+layer+"/v1/billing/accounts", nil))
		Expect(response.Code).To(Equal(http.StatusOK))
		Expect(response.Body.String()).To(MatchJSON(`{
			"items":[{"status":{"external_billing_account_id":"ext-1"}}],"size":1,"total":1
		}`))
	},
		Entry("private", "private"),
	)
})
