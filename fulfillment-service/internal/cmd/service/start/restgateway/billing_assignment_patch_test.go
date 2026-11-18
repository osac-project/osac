/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package restgateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/testing"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("TenantBillingAssignments REST PATCH", func() {
	// This is a transport contract: both generated HTTP routes and the outbound
	// gRPC connection are real, while the backend captures the request in place
	// of the database and provider workflow.
	const atomicPath = "spec.billing_account"
	const byID = `{"spec":{"billing_account":{"id":"account-id"}}}`
	for _, layer := range []string{"fulfillment", "private"} {
		Context(layer, func() {
			type captured struct {
				method, id, accountID, accountName string
				mask                               *fieldmaskpb.FieldMask
			}
			var (
				mux      http.Handler
				conn     *grpc.ClientConn
				requests chan captured
			)
			BeforeEach(func(ctx SpecContext) {
				requests = make(chan captured, 1)
				server := testing.NewServer(grpc.UnaryInterceptor(func(_ context.Context, req any, info *grpc.UnaryServerInfo, _ grpc.UnaryHandler) (any, error) {
					var got captured
					var reply any
					switch req := req.(type) {
					case *publicv1.TenantBillingAssignmentsUpdateRequest:
						obj := req.GetObject()
						ref := obj.GetSpec().GetBillingAccount()
						got = captured{info.FullMethod, obj.GetId(), ref.GetId(), ref.GetName(), req.GetUpdateMask()}
						reply = publicv1.TenantBillingAssignmentsUpdateResponse_builder{Object: obj}.Build()
					case *privatev1.TenantBillingAssignmentsUpdateRequest:
						obj := req.GetObject()
						ref := obj.GetSpec().GetBillingAccount()
						got = captured{info.FullMethod, obj.GetId(), ref.GetId(), ref.GetName(), req.GetUpdateMask()}
						reply = privatev1.TenantBillingAssignmentsUpdateResponse_builder{Object: obj}.Build()
					default:
						return nil, status.Errorf(codes.Unimplemented, "unexpected request %T", req)
					}
					requests <- got
					for _, path := range got.mask.GetPaths() {
						if path != atomicPath {
							return nil, status.Error(codes.InvalidArgument, "only the atomic billing account reference is writable")
						}
					}
					return reply, nil
				}))
				DeferCleanup(server.Stop)
				publicv1.RegisterTenantBillingAssignmentsServer(server.Registrar(), &publicv1.UnimplementedTenantBillingAssignmentsServer{})
				privatev1.RegisterTenantBillingAssignmentsServer(server.Registrar(), &privatev1.UnimplementedTenantBillingAssignmentsServer{})
				server.Start()
				var err error
				conn, err = grpc.NewClient(server.Address(), grpc.WithTransportCredentials(insecure.NewCredentials()),
					grpc.WithUnaryInterceptor(normalizeBillingAssignmentPatchMask))
				Expect(err).NotTo(HaveOccurred())
				DeferCleanup(conn.Close)
				gateway := newGatewayMux()
				runner := &runnerContext{grpcClient: conn}
				Expect(runner.registerHandlers(ctx, gateway)).To(Succeed())
				mux = gateway
			})

			DescribeTable("forwards the expected mask and reference on the wire",
				func(specCtx SpecContext, body, query, accountID, accountName string, paths []string, code int) {
					ctx, cancel := context.WithTimeout(specCtx, 5*time.Second)
					defer cancel()
					req := httptest.NewRequestWithContext(ctx, http.MethodPatch,
						"/api/"+layer+"/v1/billing/tenant_billing_assignments/assignment-id"+query, strings.NewReader(body))
					req.Header.Set("Content-Type", "application/json")
					response := httptest.NewRecorder()
					mux.ServeHTTP(response, req)
					Expect(response.Code).To(Equal(code), response.Body.String())
					var got captured
					Expect(requests).To(Receive(&got))
					method := publicv1.TenantBillingAssignments_Update_FullMethodName
					if layer == "private" {
						method = privatev1.TenantBillingAssignments_Update_FullMethodName
					}
					Expect(got.method).To(Equal(method))
					Expect(got.id).To(Equal("assignment-id"))
					Expect(got.accountID).To(Equal(accountID))
					Expect(got.accountName).To(Equal(accountName))
					Expect(got.mask).NotTo(BeNil())
					Expect(got.mask.GetPaths()).To(Equal(paths))
				},
				Entry("infers an atomic mask for ID", byID, "", "account-id", "", []string{atomicPath}, http.StatusOK),
				Entry("infers an atomic mask for name with JSON casing", `{"spec":{"billingAccount":{"name":"account-name"}}}`,
					"", "", "account-name", []string{atomicPath}, http.StatusOK),
				Entry("coalesces both selectors for a canonical same-account resubmission",
					`{"spec":{"billing_account":{"id":"account-id","name":"account-name"}}}`, "",
					"account-id", "account-name", []string{atomicPath}, http.StatusOK),
				Entry("keeps unrelated inferred paths for rejection",
					`{"metadata":{"name":"assignment-name"},"spec":{"billing_account":{"id":"account-id"},"tenant":{"id":"t"}}}`, "",
					"account-id", "", []string{"metadata.name", atomicPath, "spec.tenant.id"}, http.StatusBadRequest),
				Entry("keeps an explicit atomic path", byID, "?update_mask="+atomicPath,
					"account-id", "", []string{atomicPath}, http.StatusOK),
				Entry("keeps explicitly requested leaf and unrelated paths", byID, "?updateMask="+atomicPath+".id,status.message",
					"account-id", "", []string{atomicPath + ".id", "status.message"}, http.StatusBadRequest),
				Entry("keeps explicit repeated paths using the existing gateway parser", byID, "?update_mask.paths="+atomicPath+".name",
					"account-id", "", []string{atomicPath + ".id", atomicPath + ".name"}, http.StatusBadRequest),
				Entry("keeps an inferred empty mask present for an absent body", "", "", "", "", []string(nil), http.StatusOK),
				Entry("does not special-case an empty JSON object", `{}`, "", "", "", []string{""}, http.StatusBadRequest),
				Entry("does not interpret an empty query value as an empty mask", byID, "?update_mask=",
					"account-id", "", []string{""}, http.StatusBadRequest),
			)

			DescribeTable("rejects competing explicit mask aliases without forwarding an RPC",
				func(specCtx SpecContext, query string) {
					ctx, cancel := context.WithTimeout(specCtx, 5*time.Second)
					defer cancel()
					req := httptest.NewRequestWithContext(ctx, http.MethodPatch,
						"/api/"+layer+"/v1/billing/tenant_billing_assignments/assignment-id"+query, strings.NewReader(byID))
					req.Header.Set("Content-Type", "application/json")
					response := httptest.NewRecorder()
					mux.ServeHTTP(response, req)
					Expect(response.Code).To(Equal(http.StatusBadRequest), response.Body.String())
					Expect(response.Body.String()).To(ContainSubstring("query must not specify both update_mask and updateMask"))
					Expect(requests).NotTo(Receive())
				},
				Entry("proto name first", "?update_mask="+atomicPath+"&updateMask="+atomicPath+".id"),
				Entry("JSON name first", "?updateMask="+atomicPath+".id&update_mask="+atomicPath),
				Entry("both names with identical paths", "?update_mask="+atomicPath+"&updateMask="+atomicPath),
				Entry("proto name and JSON repeated paths", "?update_mask="+atomicPath+"&updateMask.paths="+atomicPath+".id"),
			)

			DescribeTable("does not rewrite direct gRPC masks", func(specCtx SpecContext, mask *fieldmaskpb.FieldMask, code codes.Code) {
				ctx, cancel := context.WithTimeout(specCtx, 5*time.Second)
				defer cancel()
				var err error
				if layer == "private" {
					_, err = privatev1.NewTenantBillingAssignmentsClient(conn).Update(ctx,
						privatev1.TenantBillingAssignmentsUpdateRequest_builder{UpdateMask: mask}.Build())
				} else {
					_, err = publicv1.NewTenantBillingAssignmentsClient(conn).Update(ctx,
						publicv1.TenantBillingAssignmentsUpdateRequest_builder{UpdateMask: mask}.Build())
				}
				Expect(status.Code(err)).To(Equal(code))
				var got captured
				Expect(requests).To(Receive(&got))
				Expect(proto.Equal(got.mask, mask)).To(BeTrue())
			},
				Entry("omitted", (*fieldmaskpb.FieldMask)(nil), codes.OK),
				Entry("present empty", &fieldmaskpb.FieldMask{}, codes.OK),
				Entry("explicit child path", &fieldmaskpb.FieldMask{Paths: []string{atomicPath + ".id"}}, codes.InvalidArgument),
			)
		})
	}
})
