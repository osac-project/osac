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
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/servers"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// assignmentCreateDryRun reports the dry-run metadata the gateway derives from the X-Dry-Run header.
func assignmentCreateDryRun(ctx context.Context) bool {
	values := metadata.ValueFromIncomingContext(ctx, servers.DryRunMetadataKey)
	for _, value := range values {
		if strings.EqualFold(value, "true") {
			return true
		}
	}
	return false
}

type publicAssignmentCreateStub struct {
	publicv1.UnimplementedTenantBillingAssignmentsServer
}

func (publicAssignmentCreateStub) Create(ctx context.Context, req *publicv1.TenantBillingAssignmentsCreateRequest) (
	*publicv1.TenantBillingAssignmentsCreateResponse, error) {
	if assignmentCreateDryRun(ctx) {
		return nil, status.Error(codes.Unimplemented,
			"dry-run is not supported for tenant billing assignment creation")
	}
	obj := req.GetObject()
	spec := obj.GetSpec()
	if spec.GetTenant() == nil || spec.GetBillingAccount() == nil {
		return nil, status.Error(codes.InvalidArgument, "expected both assignment references")
	}
	// Echo the caller's selectors and fill in the complementary identity fields.
	spec.GetTenant().SetName("tenant-a")
	spec.GetBillingAccount().SetId("account-id")
	obj.SetId("interval-id")
	obj.SetStatus(publicv1.TenantBillingAssignmentStatus_builder{
		ConnectionState:        publicv1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED,
		EffectiveFromTimestamp: assignmentCreateStart(),
	}.Build())
	return publicv1.TenantBillingAssignmentsCreateResponse_builder{Object: obj}.Build(), nil
}

type privateAssignmentCreateStub struct {
	privatev1.UnimplementedTenantBillingAssignmentsServer
}

func (privateAssignmentCreateStub) Create(ctx context.Context, req *privatev1.TenantBillingAssignmentsCreateRequest) (
	*privatev1.TenantBillingAssignmentsCreateResponse, error) {
	if assignmentCreateDryRun(ctx) {
		return nil, status.Error(codes.Unimplemented,
			"dry-run is not supported for tenant billing assignment creation")
	}
	obj := req.GetObject()
	spec := obj.GetSpec()
	if spec.GetTenant() == nil || spec.GetBillingAccount() == nil {
		return nil, status.Error(codes.InvalidArgument, "expected both assignment references")
	}
	spec.GetTenant().SetName("tenant-a")
	spec.GetBillingAccount().SetId("account-id")
	obj.SetId("interval-id")
	obj.SetStatus(privatev1.TenantBillingAssignmentStatus_builder{
		ConnectionState:        privatev1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED,
		EffectiveFromTimestamp: assignmentCreateStart(),
	}.Build())
	return privatev1.TenantBillingAssignmentsCreateResponse_builder{Object: obj}.Build(), nil
}

func assignmentCreateStart() *timestamppb.Timestamp {
	return timestamppb.New(time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC))
}

var _ = Describe("Tenant billing assignment REST Create contract", func() {
	var (
		gateway http.Handler
		calls   atomic.Int32
	)

	// Use production transcoding and generated RPC bindings, with only the
	// assignment backend replaced. No provider or database is involved.
	BeforeEach(func(ctx SpecContext) {
		calls.Store(0)
		listener := bufconn.Listen(1024 * 1024)
		server := grpc.NewServer(grpc.UnaryInterceptor(func(ctx context.Context, req any,
			_ *grpc.UnaryServerInfo, handle grpc.UnaryHandler) (any, error) {
			calls.Add(1)
			return handle(ctx, req)
		}))
		publicv1.RegisterTenantBillingAssignmentsServer(server, publicAssignmentCreateStub{})
		privatev1.RegisterTenantBillingAssignmentsServer(server, privateAssignmentCreateStub{})
		serveErr := make(chan error, 1)
		DeferCleanup(func() {
			server.Stop()
			Expect(<-serveErr).To(Or(Succeed(), MatchError(grpc.ErrServerStopped)),
				"the gRPC server must stop cleanly")
		})
		go func() {
			serveErr <- server.Serve(listener)
		}()

		conn, err := grpc.NewClient("passthrough:///assignment-create", grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return listener.DialContext(ctx)
			}))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(conn.Close)
		mux := newGatewayMux()
		runner := &runnerContext{grpcClient: conn}
		Expect(runner.registerHandlers(ctx, mux)).To(Succeed())
		gateway = mux
	})

	for _, scope := range []string{"fulfillment", "private"} {
		Describe("/api/"+scope+"/v1", func() {
			post := func(ctx context.Context, body string) *httptest.ResponseRecorder {
				// Bound each fixture request so a wedged handler cannot stall the suite.
				requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()
				request := httptest.NewRequestWithContext(requestCtx, http.MethodPost,
					"/api/"+scope+"/v1/billing/tenant_billing_assignments", strings.NewReader(body))
				request.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				gateway.ServeHTTP(response, request)
				return response
			}

			It("transcodes nested selectors and returns the current interval under status", func(ctx SpecContext) {
				response := post(ctx, `{"spec":{"tenant":{"id":"tenant-id"},"billing_account":{"name":"account-a"}}}`)
				Expect(response.Code).To(Equal(http.StatusOK), response.Body.String())
				Expect(calls.Load()).To(Equal(int32(1)))
				Expect(response.Body.String()).To(MatchJSON(`{
					"id":"interval-id",
					"spec":{"tenant":{"id":"tenant-id","name":"tenant-a"},"billing_account":{"id":"account-id","name":"account-a"}},
					"status":{
						"connection_state":"TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED",
						"effective_from_timestamp":"2026-01-01T12:00:00Z"
					}
				}`))
			}, SpecTimeout(5*time.Second))

			It("forwards X-Dry-Run to Create and returns Unimplemented", func(ctx SpecContext) {
				request := httptest.NewRequestWithContext(ctx, http.MethodPost,
					"/api/"+scope+"/v1/billing/tenant_billing_assignments",
					strings.NewReader(`{"spec":{"tenant":{"id":"tenant-id"},"billing_account":{"id":"account-id"}}}`))
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set(servers.DryRunHTTPHeader, "true")
				response := httptest.NewRecorder()
				gateway.ServeHTTP(response, request)
				Expect(response.Code).To(Equal(http.StatusNotImplemented), response.Body.String())
				Expect(calls.Load()).To(Equal(int32(1)), "the gateway must deliver the request to Create")
				Expect(response.Body.String()).To(ContainSubstring("dry-run is not supported"))
			}, SpecTimeout(5*time.Second))

			DescribeTable("rejects legacy scalar fields before calling Create", func(ctx SpecContext, body, field string) {
				response := post(ctx, body)
				Expect(response.Code).To(Equal(http.StatusBadRequest), response.Body.String())
				Expect(response.Body.String()).To(And(ContainSubstring("unknown field"), ContainSubstring(field)))
				Expect(calls.Load()).To(BeZero())
			},
				Entry("flat tenant_id", `{"tenant_id":"tenant-id"}`, "tenant_id"),
				Entry("flat billing_account_id", `{"billing_account_id":"account-id"}`, "billing_account_id"),
				Entry("spec.tenant_id", `{"spec":{"tenant_id":"tenant-id"}}`, "tenant_id"),
				Entry("spec.billing_account_id", `{"spec":{"billing_account_id":"account-id"}}`, "billing_account_id"),
				Entry("status.effective_from", `{"status":{"effective_from":"2026-01-01T12:00:00Z"}}`, "effective_from"),
				Entry("status.effective_to", `{"status":{"effective_to":"2026-01-02T12:00:00Z"}}`, "effective_to"),
			)
		})
	}
})
