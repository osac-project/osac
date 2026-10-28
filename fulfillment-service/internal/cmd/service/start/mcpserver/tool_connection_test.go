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

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/version"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

type mockSelfSubjectAccessReviewsClient struct {
	publicv1.SelfSubjectAccessReviewsClient
	createFunc func(context.Context, *publicv1.SelfSubjectAccessReviewsCreateRequest, ...grpc.CallOption) (*publicv1.SelfSubjectAccessReviewsCreateResponse, error)
}

type mockTenantsClient struct {
	publicv1.TenantsClient
	listFunc func(context.Context, *publicv1.TenantsListRequest, ...grpc.CallOption) (*publicv1.TenantsListResponse, error)
}

func (m *mockTenantsClient) List(
	ctx context.Context, request *publicv1.TenantsListRequest, options ...grpc.CallOption,
) (*publicv1.TenantsListResponse, error) {
	return m.listFunc(ctx, request, options...)
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

func tenantListResponse(total int32, names ...string) *publicv1.TenantsListResponse {
	items := make([]*publicv1.Tenant, 0, len(names))
	for _, name := range names {
		items = append(items, publicv1.Tenant_builder{
			Metadata: publicv1.Metadata_builder{Name: name}.Build(),
		}.Build())
	}
	return publicv1.TenantsListResponse_builder{Total: total, Items: items}.Build()
}

func checkConnectionToolRegistry(names ...string) typedToolRegistry {
	registered := make(map[string]struct{}, len(names))
	for _, name := range names {
		registered[name] = struct{}{}
	}
	return typedToolRegistry{registered: registered}
}

var _ = Describe("handleCheckConnection", func() {
	It("Returns a safe caller, capability, and permission summary without mutating a resource", func() {
		var capturedToken string
		var capturedServices []string
		var capturedMethods []string
		var capturedTenants []string
		var capturedNames []string
		var tenantListToken string
		var tenantListLimit int32
		tenantClient := &mockTenantsClient{
			listFunc: func(
				ctx context.Context, request *publicv1.TenantsListRequest, _ ...grpc.CallOption,
			) (*publicv1.TenantsListResponse, error) {
				tenantListToken = forwardedToken(ctx)
				tenantListLimit = request.GetLimit()
				return tenantListResponse(2, "shared", "tenant-1"), nil
			},
		}
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

		handler := handleCheckConnection(
			"https://mcp.example.com/",
			newResourceRegistry(ServerDeps{}),
			checkConnectionToolRegistry("zeta_tool", "alpha_tool"),
			tenantClient,
			reviews,
		)
		_, output, err := handler(context.Background(), requestWithCaller("raw-bearer-value", safeCallerContext{
			Subject: "user-1", Username: "alice", Tenants: []string{"token-claim-tenant"},
		}), CheckConnectionInput{})
		Expect(err).ToNot(HaveOccurred())
		Expect(output.Endpoint).To(Equal("https://mcp.example.com/"))
		Expect(output.Server).To(Equal(CheckConnectionServer{Name: "osac-deployment-mcp", Version: version.Get()}))
		Expect(output.Caller).To(Equal(safeCallerContext{
			Subject: "user-1", Username: "alice", Tenants: []string{"tenant-1"},
		}))
		Expect(tenantListToken).To(Equal("Bearer api-for-raw-bearer-value"))
		Expect(tenantListLimit).To(Equal(int32(maxCheckConnectionTenants + 1)))
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
			ResourceTypeProject,
			ResourceTypeSecretReference,
		))
		Expect(output.Tools).To(Equal([]string{"alpha_tool", "zeta_tool"}))
		Expect(output.PermissionsTruncated).To(BeFalse())
		Expect(output.Permissions).To(Equal([]CheckConnectionPermission{
			{Tenant: "tenant-1", Service: "osac.public.v1.ComputeInstances", Method: "List", Allowed: true},
			{Tenant: "tenant-1", Service: "osac.public.v1.ComputeInstances", Method: "Create", Allowed: false},
		}))
		Expect(capturedToken).To(Equal("Bearer api-for-raw-bearer-value"))
		Expect(tenantListToken).To(Equal(capturedToken))
		Expect(capturedServices).To(Equal([]string{"osac.public.v1.ComputeInstances", "osac.public.v1.ComputeInstances"}))
		Expect(capturedMethods).To(Equal([]string{"List", "Create"}))
		Expect(capturedTenants).To(Equal([]string{"tenant-1", "tenant-1"}))
		Expect(capturedNames).To(Equal([]string{"mcp-check-connection", "mcp-check-connection"}))
	})

	It("Uses an unscoped permission review for a caller without a tenant", func() {
		var capturedTenant string
		tenantClient := &mockTenantsClient{
			listFunc: func(
				context.Context, *publicv1.TenantsListRequest, ...grpc.CallOption,
			) (*publicv1.TenantsListResponse, error) {
				return tenantListResponse(0), nil
			},
		}
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

		handler := handleCheckConnection(
			"https://mcp.example.com/", newResourceRegistry(ServerDeps{}), checkConnectionToolRegistry(), tenantClient, reviews,
		)
		_, output, err := handler(context.Background(), requestWithCaller("raw-bearer-value", safeCallerContext{
			Subject: "user-1", Tenants: []string{"untrusted-claim-tenant"},
		}), CheckConnectionInput{})
		Expect(err).ToNot(HaveOccurred())
		Expect(output.Caller.Tenants).To(BeEmpty())
		Expect(capturedTenant).To(BeEmpty())
		Expect(output.Permissions).To(Equal([]CheckConnectionPermission{
			{Service: "osac.public.v1.ComputeInstances", Method: "List", Allowed: true},
			{Service: "osac.public.v1.ComputeInstances", Method: "Create", Allowed: true},
		}))
		Expect(output.PermissionsTruncated).To(BeFalse())
	})

	It("Preserves a retryable token exchange error", func() {
		request := requestWithCaller("raw-bearer-value", safeCallerContext{Subject: "user-1"})
		request.Extra.TokenInfo.Extra[exchangeTokenExtraKey] = apiTokenSource(func(context.Context) (string, error) {
			return "", errOAuthTokenExchangeUnavailable
		})
		handler := handleCheckConnection(
			"https://mcp.example.com/", newResourceRegistry(ServerDeps{}), checkConnectionToolRegistry(),
			&mockTenantsClient{}, &mockSelfSubjectAccessReviewsClient{},
		)
		_, _, err := handler(context.Background(), request, CheckConnectionInput{})
		Expect(err).To(HaveOccurred())
		envelope := decodeToolError(err)
		Expect(envelope.Category).To(Equal("service_unavailable"))
		Expect(envelope.GRPCCode).To(Equal("Unavailable"))
		Expect(envelope.Retryable).To(BeTrue())
	})

	It("Returns a stable safe error envelope for a service failure", func() {
		tenantClient := &mockTenantsClient{
			listFunc: func(
				context.Context, *publicv1.TenantsListRequest, ...grpc.CallOption,
			) (*publicv1.TenantsListResponse, error) {
				return tenantListResponse(0), nil
			},
		}
		reviews := &mockSelfSubjectAccessReviewsClient{
			createFunc: func(
				context.Context, *publicv1.SelfSubjectAccessReviewsCreateRequest, ...grpc.CallOption,
			) (*publicv1.SelfSubjectAccessReviewsCreateResponse, error) {
				return nil, status.Error(codes.Unavailable, "https://internal-fulfillment.example.com and secret-token")
			},
		}

		handler := handleCheckConnection(
			"https://mcp.example.com/", newResourceRegistry(ServerDeps{}), checkConnectionToolRegistry(), tenantClient, reviews,
		)
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

	It("Does not count the reserved shared tenant toward the summary limit", func() {
		tenantClient := &mockTenantsClient{
			listFunc: func(
				context.Context, *publicv1.TenantsListRequest, ...grpc.CallOption,
			) (*publicv1.TenantsListResponse, error) {
				return tenantListResponse(maxCheckConnectionTenants+1,
					"shared", "tenant-00", "tenant-01", "tenant-02", "tenant-03", "tenant-04",
					"tenant-05", "tenant-06", "tenant-07", "tenant-08", "tenant-09",
				), nil
			},
		}
		handler := handleCheckConnection(
			"https://mcp.example.com/",
			newResourceRegistry(ServerDeps{}),
			checkConnectionToolRegistry(),
			tenantClient,
			&mockSelfSubjectAccessReviewsClient{
				createFunc: func(
					_ context.Context, _ *publicv1.SelfSubjectAccessReviewsCreateRequest, _ ...grpc.CallOption,
				) (*publicv1.SelfSubjectAccessReviewsCreateResponse, error) {
					return publicv1.SelfSubjectAccessReviewsCreateResponse_builder{}.Build(), nil
				},
			},
		)
		_, output, err := handler(context.Background(), requestWithCaller("raw-bearer-value", safeCallerContext{
			Subject: "user-1",
		}), CheckConnectionInput{})
		Expect(err).ToNot(HaveOccurred())
		Expect(output.PermissionsTruncated).To(BeFalse())
		Expect(output.Caller.Tenants).To(HaveLen(maxCheckConnectionTenants))
		Expect(output.Caller.Tenants).ToNot(ContainElement(auth.SharedTenant))
		Expect(output.Permissions).To(HaveLen(maxCheckConnectionTenants * 2))
	})

	It("Marks a summary truncated when the total exceeds returned tenants", func() {
		tenantClient := &mockTenantsClient{
			listFunc: func(
				context.Context, *publicv1.TenantsListRequest, ...grpc.CallOption,
			) (*publicv1.TenantsListResponse, error) {
				return tenantListResponse(25,
					"shared", "tenant-00", "tenant-01", "tenant-02", "tenant-03", "tenant-04",
					"tenant-05", "tenant-06", "tenant-07", "tenant-08", "tenant-09",
				), nil
			},
		}
		handler := handleCheckConnection(
			"https://mcp.example.com/",
			newResourceRegistry(ServerDeps{}),
			checkConnectionToolRegistry(),
			tenantClient,
			&mockSelfSubjectAccessReviewsClient{
				createFunc: func(
					_ context.Context, _ *publicv1.SelfSubjectAccessReviewsCreateRequest, _ ...grpc.CallOption,
				) (*publicv1.SelfSubjectAccessReviewsCreateResponse, error) {
					return publicv1.SelfSubjectAccessReviewsCreateResponse_builder{}.Build(), nil
				},
			},
		)
		_, output, err := handler(context.Background(), requestWithCaller("raw-bearer-value", safeCallerContext{
			Subject: "user-1",
		}), CheckConnectionInput{})
		Expect(err).ToNot(HaveOccurred())
		Expect(output.PermissionsTruncated).To(BeTrue())
		Expect(output.Caller.Tenants).To(HaveLen(maxCheckConnectionTenants))
		Expect(output.Caller.Tenants).ToNot(ContainElement(auth.SharedTenant))
		Expect(output.Permissions).To(HaveLen(maxCheckConnectionTenants * 2))
	})

	It("Uses only the bounded public tenant list for permission checks", func() {
		names := []string{
			"tenant-10", "tenant-09", "tenant-08", "tenant-07", "tenant-06", "tenant-05",
			"tenant-04", "tenant-03", "tenant-02", "tenant-01", "tenant-00",
		}
		tenantClient := &mockTenantsClient{
			listFunc: func(
				context.Context, *publicv1.TenantsListRequest, ...grpc.CallOption,
			) (*publicv1.TenantsListResponse, error) {
				return tenantListResponse(13, names...), nil
			},
		}
		var reviewedTenants []string
		reviews := &mockSelfSubjectAccessReviewsClient{
			createFunc: func(
				_ context.Context, request *publicv1.SelfSubjectAccessReviewsCreateRequest, _ ...grpc.CallOption,
			) (*publicv1.SelfSubjectAccessReviewsCreateResponse, error) {
				reviewedTenants = append(reviewedTenants, request.GetObject().GetMetadata().GetTenant())
				return publicv1.SelfSubjectAccessReviewsCreateResponse_builder{
					Object: publicv1.SelfSubjectAccessReview_builder{}.Build(),
				}.Build(), nil
			},
		}
		handler := handleCheckConnection(
			"https://mcp.example.com/", newResourceRegistry(ServerDeps{}), checkConnectionToolRegistry(), tenantClient, reviews,
		)
		_, output, err := handler(context.Background(), requestWithCaller("raw-bearer-value", safeCallerContext{
			Subject: "user-1",
		}), CheckConnectionInput{})
		Expect(err).ToNot(HaveOccurred())
		Expect(output.PermissionsTruncated).To(BeTrue())
		encodedOutput, err := json.Marshal(output)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(encodedOutput)).To(ContainSubstring(`"permission_summary_truncated":true`))
		Expect(output.Caller.Tenants).To(HaveLen(maxCheckConnectionTenants))
		Expect(output.Caller.Tenants).To(Equal([]string{
			"tenant-00", "tenant-01", "tenant-02", "tenant-03", "tenant-04",
			"tenant-05", "tenant-06", "tenant-07", "tenant-08", "tenant-09",
		}))
		Expect(output.Permissions).To(HaveLen(maxCheckConnectionTenants * 2))
		Expect(reviewedTenants).To(HaveLen(maxCheckConnectionTenants * 2))
		Expect(reviewedTenants).ToNot(ContainElement("tenant-10"))
	})

	It("Returns a safe error when the public tenant list fails", func() {
		tenantClient := &mockTenantsClient{
			listFunc: func(
				context.Context, *publicv1.TenantsListRequest, ...grpc.CallOption,
			) (*publicv1.TenantsListResponse, error) {
				return nil, status.Error(codes.PermissionDenied, "private tenant policy detail")
			},
		}
		handler := handleCheckConnection(
			"https://mcp.example.com/", newResourceRegistry(ServerDeps{}), checkConnectionToolRegistry(), tenantClient,
			&mockSelfSubjectAccessReviewsClient{},
		)
		_, _, err := handler(context.Background(), requestWithCaller("raw-bearer-value", safeCallerContext{Subject: "user-1"}), CheckConnectionInput{})
		Expect(err).To(HaveOccurred())
		envelope := decodeToolError(err)
		Expect(envelope.Category).To(Equal("authorization"))
		Expect(err.Error()).ToNot(ContainSubstring("private tenant policy"))
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
		Entry("already exists", codes.AlreadyExists, "conflict", "AlreadyExists", false),
		Entry("failed precondition", codes.FailedPrecondition, "protocol_incompatible", "FailedPrecondition", false),
		Entry("canceled", codes.Canceled, "service_unavailable", "Canceled", false),
		Entry("deadline exceeded", codes.DeadlineExceeded, "service_unavailable", "DeadlineExceeded", true),
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

	It("Reports wrapped context deadline errors as DeadlineExceeded", func() {
		err := safePublicAPIError(errors.Join(context.DeadlineExceeded, errors.New("local timeout")))
		envelope := decodeToolError(err)
		Expect(envelope.Category).To(Equal("service_unavailable"))
		Expect(envelope.GRPCCode).To(Equal("DeadlineExceeded"))
		Expect(envelope.Retryable).To(BeTrue())
	})
})
