/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package mcpserver

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/version"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

type mockSelfSubjectAccessReviewsClient struct {
	publicv1.SelfSubjectAccessReviewsClient
	createFunc func(context.Context, *publicv1.SelfSubjectAccessReviewsCreateRequest, ...grpc.CallOption) (*publicv1.SelfSubjectAccessReviewsCreateResponse, error)
}

func (m *mockSelfSubjectAccessReviewsClient) Create(
	ctx context.Context, request *publicv1.SelfSubjectAccessReviewsCreateRequest, options ...grpc.CallOption,
) (*publicv1.SelfSubjectAccessReviewsCreateResponse, error) {
	return m.createFunc(ctx, request, options...)
}

func requestWithCaller(rawToken string, caller safeCallerContext) *mcp.CallToolRequest {
	request := requestWithToken(rawToken)
	request.Extra.TokenInfo.Extra[callerContextExtraKey] = caller
	return request
}

func decodeToolError(err error) toolError {
	var envelope toolError
	ExpectWithOffset(1, json.Unmarshal([]byte(err.Error()), &envelope)).To(Succeed())
	return envelope
}

var _ = Describe("handleCheckConnection", func() {
	It("Returns a safe caller, capability, and permission summary without mutating a resource", func() {
		var capturedToken string
		var capturedServices []string
		var capturedMethods []string
		var capturedTenants []string
		var capturedNames []string
		reviews := &mockSelfSubjectAccessReviewsClient{
			createFunc: func(
				ctx context.Context, request *publicv1.SelfSubjectAccessReviewsCreateRequest, _ ...grpc.CallOption,
			) (*publicv1.SelfSubjectAccessReviewsCreateResponse, error) {
				capturedToken = forwardedToken(ctx)
				capturedServices = append(capturedServices, request.GetObject().GetSpec().GetService())
				capturedMethods = append(capturedMethods, request.GetObject().GetSpec().GetMethod())
				capturedTenants = append(capturedTenants, request.GetObject().GetMetadata().GetTenant())
				capturedNames = append(capturedNames, request.GetObject().GetMetadata().GetName())
				return publicv1.SelfSubjectAccessReviewsCreateResponse_builder{
					Object: publicv1.SelfSubjectAccessReview_builder{
						Status: publicv1.SelfSubjectAccessReviewStatus_builder{
							Allowed: request.GetObject().GetSpec().GetMethod() == "List",
						}.Build(),
					}.Build(),
				}.Build(), nil
			},
		}

		handler := handleCheckConnection("https://mcp.example.com/", newResourceRegistry(ServerDeps{}), reviews)
		_, output, err := handler(context.Background(), requestWithCaller("raw-bearer-value", safeCallerContext{
			Subject: "user-1", Username: "alice", Tenants: []string{"tenant-1"},
		}), CheckConnectionInput{})
		Expect(err).ToNot(HaveOccurred())
		Expect(output.Endpoint).To(Equal("https://mcp.example.com/"))
		Expect(output.Server).To(Equal(CheckConnectionServer{Name: "osac-deployment-mcp", Version: version.Get()}))
		Expect(output.Caller).To(Equal(safeCallerContext{
			Subject: "user-1", Username: "alice", Tenants: []string{"tenant-1"},
		}))
		Expect(output.ResourceFamilies).To(ConsistOf(
			ResourceTypeComputeInstanceCatalogItem,
			ResourceTypeComputeInstanceTemplate,
			ResourceTypeInstanceType,
			ResourceTypeDiskImage,
			ResourceTypeStorageTier,
			ResourceTypeVirtualNetwork,
			ResourceTypeSubnet,
			ResourceTypeSecurityGroup,
			ResourceTypeComputeInstance,
		))
		Expect(output.Tools).To(ConsistOf(
			"check_connection", "list_resources", "get_resource", "create_compute_instance", "delete_compute_instance",
		))
		Expect(output.Permissions).To(Equal([]CheckConnectionPermission{
			{Tenant: "tenant-1", Service: "osac.public.v1.ComputeInstances", Method: "List", Allowed: true},
			{Tenant: "tenant-1", Service: "osac.public.v1.ComputeInstances", Method: "Create", Allowed: false},
		}))
		Expect(capturedToken).To(Equal("Bearer api-for-raw-bearer-value"))
		Expect(capturedServices).To(Equal([]string{"osac.public.v1.ComputeInstances", "osac.public.v1.ComputeInstances"}))
		Expect(capturedMethods).To(Equal([]string{"List", "Create"}))
		Expect(capturedTenants).To(Equal([]string{"tenant-1", "tenant-1"}))
		Expect(capturedNames).To(Equal([]string{"mcp-check-connection", "mcp-check-connection"}))
	})

	It("Uses an unscoped permission review for a caller without a tenant", func() {
		var capturedTenant string
		reviews := &mockSelfSubjectAccessReviewsClient{
			createFunc: func(
				_ context.Context, request *publicv1.SelfSubjectAccessReviewsCreateRequest, _ ...grpc.CallOption,
			) (*publicv1.SelfSubjectAccessReviewsCreateResponse, error) {
				capturedTenant = request.GetObject().GetMetadata().GetTenant()
				return publicv1.SelfSubjectAccessReviewsCreateResponse_builder{
					Object: publicv1.SelfSubjectAccessReview_builder{
						Status: publicv1.SelfSubjectAccessReviewStatus_builder{Allowed: true}.Build(),
					}.Build(),
				}.Build(), nil
			},
		}

		handler := handleCheckConnection("https://mcp.example.com/", newResourceRegistry(ServerDeps{}), reviews)
		_, output, err := handler(context.Background(), requestWithCaller("raw-bearer-value", safeCallerContext{
			Subject: "user-1",
		}), CheckConnectionInput{})
		Expect(err).ToNot(HaveOccurred())
		Expect(capturedTenant).To(BeEmpty())
		Expect(output.Permissions).To(Equal([]CheckConnectionPermission{
			{Service: "osac.public.v1.ComputeInstances", Method: "List", Allowed: true},
			{Service: "osac.public.v1.ComputeInstances", Method: "Create", Allowed: true},
		}))
	})

	It("Returns a stable safe error envelope for a service failure", func() {
		reviews := &mockSelfSubjectAccessReviewsClient{
			createFunc: func(
				context.Context, *publicv1.SelfSubjectAccessReviewsCreateRequest, ...grpc.CallOption,
			) (*publicv1.SelfSubjectAccessReviewsCreateResponse, error) {
				return nil, status.Error(codes.Unavailable, "https://internal-fulfillment.example.com and secret-token")
			},
		}

		handler := handleCheckConnection("https://mcp.example.com/", newResourceRegistry(ServerDeps{}), reviews)
		_, _, err := handler(context.Background(), requestWithCaller("raw-bearer-value", safeCallerContext{
			Subject: "user-1",
		}), CheckConnectionInput{})
		Expect(err).To(HaveOccurred())
		var envelope toolError
		Expect(json.Unmarshal([]byte(err.Error()), &envelope)).To(Succeed())
		Expect(envelope.Category).To(Equal("service_unavailable"))
		Expect(envelope.GRPCCode).To(Equal("Unavailable"))
		Expect(envelope.Message).To(Equal("The OSAC service is currently unavailable. Retry after checking service status."))
		Expect(envelope.Retryable).To(BeTrue())
		Expect(envelope.Details).To(Equal([]string{}))
		Expect(uuid.Validate(envelope.RequestID)).To(Succeed())
		Expect(err.Error()).ToNot(ContainSubstring("internal-fulfillment"))
		Expect(err.Error()).ToNot(ContainSubstring("secret-token"))
	})
})

var _ = Describe("safePublicAPIError", func() {
	DescribeTable("Maps gRPC codes to the stable client-facing contract",
		func(code codes.Code, category, grpcCode string, retryable bool) {
			err := safePublicAPIError(status.Error(code, "private SQL host and customer data"))
			var envelope toolError
			Expect(json.Unmarshal([]byte(err.Error()), &envelope)).To(Succeed())
			Expect(envelope.Category).To(Equal(category))
			Expect(envelope.GRPCCode).To(Equal(grpcCode))
			Expect(envelope.Retryable).To(Equal(retryable))
			Expect(envelope.Message).ToNot(BeEmpty())
			Expect(envelope.Details).To(Equal([]string{}))
			Expect(uuid.Validate(envelope.RequestID)).To(Succeed())
			Expect(err.Error()).ToNot(ContainSubstring("private SQL host"))
		},
		Entry("invalid argument", codes.InvalidArgument, "invalid_request", "InvalidArgument", false),
		Entry("unauthenticated", codes.Unauthenticated, "authentication", "Unauthenticated", false),
		Entry("permission denied", codes.PermissionDenied, "authorization", "PermissionDenied", false),
		Entry("not found", codes.NotFound, "not_found", "NotFound", false),
		Entry("already exists", codes.AlreadyExists, "conflict", "Aborted", false),
		Entry("resource exhausted", codes.ResourceExhausted, "rate_limited", "ResourceExhausted", true),
		Entry("unavailable", codes.Unavailable, "service_unavailable", "Unavailable", true),
		Entry("unknown", codes.Unknown, "upstream_failure", "Unknown", false),
	)

	It("Gives each error its own request ID", func() {
		first := safePublicAPIError(errors.New("first failure"))
		second := safePublicAPIError(errors.New("second failure"))
		var firstEnvelope, secondEnvelope toolError
		Expect(json.Unmarshal([]byte(first.Error()), &firstEnvelope)).To(Succeed())
		Expect(json.Unmarshal([]byte(second.Error()), &secondEnvelope)).To(Succeed())
		Expect(firstEnvelope.RequestID).ToNot(Equal(secondEnvelope.RequestID))
	})
})
