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
	"errors"
	"fmt"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/packages"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// PrivateSelfSubjectAccessReviewsServerBuilder contains the data and logic needed to create a private self subject access reviews server.
type PrivateSelfSubjectAccessReviewsServerBuilder struct {
	logger       *slog.Logger
	evaluator    auth.AuthorizationEvaluator
	tenancyLogic auth.TenancyLogic
}

var _ privatev1.SelfSubjectAccessReviewsServer = (*PrivateSelfSubjectAccessReviewsServer)(nil)

type privateSelfSubjectAccessReviewsServerServiceInfo struct {
	Descriptor protoreflect.ServiceDescriptor
	Methods    map[string]bool
}

type PrivateSelfSubjectAccessReviewsServer struct {
	privatev1.UnimplementedSelfSubjectAccessReviewsServer

	logger       *slog.Logger
	evaluator    auth.AuthorizationEvaluator
	tenancyLogic auth.TenancyLogic
	services     map[string]*privateSelfSubjectAccessReviewsServerServiceInfo
}

// NewPrivateSelfSubjectAccessReviewsServer creates a new builder for the private self subject access reviews server.
func NewPrivateSelfSubjectAccessReviewsServer() *PrivateSelfSubjectAccessReviewsServerBuilder {
	return &PrivateSelfSubjectAccessReviewsServerBuilder{}
}

// SetLogger sets the logger. This is mandatory.
func (b *PrivateSelfSubjectAccessReviewsServerBuilder) SetLogger(value *slog.Logger) *PrivateSelfSubjectAccessReviewsServerBuilder {
	b.logger = value
	return b
}

// SetEvaluator sets the authorization evaluator. This is mandatory.
func (b *PrivateSelfSubjectAccessReviewsServerBuilder) SetEvaluator(value auth.AuthorizationEvaluator) *PrivateSelfSubjectAccessReviewsServerBuilder {
	b.evaluator = value
	return b
}

// SetTenancyLogic sets the tenancy logic. This is mandatory.
func (b *PrivateSelfSubjectAccessReviewsServerBuilder) SetTenancyLogic(value auth.TenancyLogic) *PrivateSelfSubjectAccessReviewsServerBuilder {
	b.tenancyLogic = value
	return b
}

func (b *PrivateSelfSubjectAccessReviewsServerBuilder) Build() (*PrivateSelfSubjectAccessReviewsServer, error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.evaluator == nil {
		return nil, errors.New("evaluator is mandatory")
	}
	if b.tenancyLogic == nil {
		return nil, errors.New("tenancy logic is mandatory")
	}

	// Pre-build service info map for fast lookups
	services := make(map[string]*privateSelfSubjectAccessReviewsServerServiceInfo)

	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		// Only index osac.public.v1 services
		if string(fd.Package()) != packages.PublicV1 {
			return true
		}

		svcDescs := fd.Services()
		for i := 0; i < svcDescs.Len(); i++ {
			sd := svcDescs.Get(i)
			fullName := fmt.Sprintf("%s.%s", fd.Package(), sd.Name())

			// Build the method set for this service
			methods := sd.Methods()
			methodSet := make(map[string]bool, methods.Len())
			for j := 0; j < methods.Len(); j++ {
				methodSet[string(methods.Get(j).Name())] = true
			}

			// Store service info
			services[fullName] = &privateSelfSubjectAccessReviewsServerServiceInfo{
				Descriptor: sd,
				Methods:    methodSet,
			}
		}
		return true
	})

	result := &PrivateSelfSubjectAccessReviewsServer{
		logger:       b.logger,
		evaluator:    b.evaluator,
		tenancyLogic: b.tenancyLogic,
		services:     services,
	}

	return result, nil
}

func (s *PrivateSelfSubjectAccessReviewsServer) Create(ctx context.Context, request *privatev1.SelfSubjectAccessReviewsCreateRequest) (*privatev1.SelfSubjectAccessReviewsCreateResponse, error) {
	// Extract the JWT token from context (set by authentication interceptor)
	token := auth.TokenFromContext(ctx)
	if token == nil {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}

	// Extract the review object from the request
	review := request.GetObject()
	if err := ValidateRequiredObject("object", review); err != nil {
		return nil, err
	}

	spec := review.GetSpec()
	if err := ValidateRequiredObject("spec", spec); err != nil {
		return nil, err
	}

	// Build the gRPC method path and validate service/method exist
	methodPath, err := s.buildGRPCMethodPath(spec.GetService(), spec.GetMethod())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Extract AuthContext from the JWT token
	authContext, err := auth.ExtractAuthContext(token)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to extract auth context", slog.Any("error", err))
		return nil, status.Error(codes.Internal, "failed to process authentication")
	}

	// Determine the tenant that would be assigned, using the same logic as GenericServer.
	// This ensures SelfSubjectAccessReview accurately reflects what would happen in a real operation.
	requestedTenant := ""
	if metadata := review.GetMetadata(); metadata != nil {
		requestedTenant = metadata.GetTenant()
	}

	// For Get, List, and Delete methods, use the requested tenant directly without validation
	// For other methods (Create, Update, etc.), use shared tenant determination logic
	method := spec.GetMethod()
	var determinedTenant string
	if method == "Get" || method == "List" || method == "Delete" {
		determinedTenant = requestedTenant
	} else {
		// Use shared tenant determination logic (same as GenericServer)
		// Note: currentTenant is "" because SelfSubjectAccessReview is always a hypothetical "create" check
		var err error
		determinedTenant, err = auth.DetermineTenantForOperation(ctx, s.tenancyLogic, requestedTenant, "")
		if err != nil {
			// Convert to gRPC error using shared mapper to ensure consistent error messages
			grpcErr := convertTenantErrorToGRPC(ctx, err, s.logger, requestedTenant)

			// For tenant denial errors (TenantInvisibleError and TenantUnassignableError),
			// return an Allowed: false response. For other errors, return the gRPC error.
			var tenantInvisibleErr *auth.TenantInvisibleError
			var tenantUnassignableErr *auth.TenantUnassignableError
			if errors.As(err, &tenantInvisibleErr) || errors.As(err, &tenantUnassignableErr) {
				return &privatev1.SelfSubjectAccessReviewsCreateResponse{
					Object: &privatev1.SelfSubjectAccessReview{
						Metadata: review.Metadata,
						Spec:     review.Spec,
						Status: &privatev1.SelfSubjectAccessReviewStatus{
							Allowed: false,
							Reason:  status.Convert(grpcErr).Message(),
						},
					},
				}, nil
			}
			// For other errors, return the gRPC error directly
			return nil, grpcErr
		}

		// For Create method, reject empty determinedTenant
		if method == "Create" && determinedTenant == "" {
			return &privatev1.SelfSubjectAccessReviewsCreateResponse{
				Object: &privatev1.SelfSubjectAccessReview{
					Metadata: review.Metadata,
					Spec:     review.Spec,
					Status: &privatev1.SelfSubjectAccessReviewStatus{
						Allowed: false,
						Reason:  "there is no default tenant",
					},
				},
			}, nil
		}
	}
	authContext.Tenant = determinedTenant

	// Evaluate authorization using the shared evaluator with the determined tenant
	decision, err := s.evaluator.Evaluate(ctx, authContext, methodPath)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to evaluate authorization", slog.Any("error", err))
		return nil, status.Error(codes.Internal, "authorization evaluation failed")
	}

	if decision == nil {
		decision = &auth.AuthzDecision{Allowed: false}
	}

	// Build the response with the authorization decision
	response := &privatev1.SelfSubjectAccessReviewsCreateResponse{
		Object: &privatev1.SelfSubjectAccessReview{
			Metadata: review.Metadata,
			Spec:     review.Spec,
			Status: &privatev1.SelfSubjectAccessReviewStatus{
				Allowed: decision.Allowed,
				Reason:  decision.Reason,
			},
		},
	}

	return response, nil
}

// buildGRPCMethodPath validates that the service exists and supports the requested method,
// then constructs the gRPC method path (e.g., "/osac.public.v1.Clusters/Create").
func (s *PrivateSelfSubjectAccessReviewsServer) buildGRPCMethodPath(service, method string) (string, error) {
	if service == "" {
		return "", fmt.Errorf("service is required")
	}
	if method == "" {
		return "", fmt.Errorf("method is required")
	}

	// Look up the service info from pre-built map
	serviceInfo, exists := s.services[service]
	if !exists {
		return "", fmt.Errorf("unknown service: %s", service)
	}

	// Check if this is the SelfSubjectAccessReviews service (prevent recursive checks)
	if string(serviceInfo.Descriptor.Name()) == "SelfSubjectAccessReviews" {
		return "", fmt.Errorf("cannot check permissions on SelfSubjectAccessReviews service")
	}

	// Validate that the service supports the requested method
	if !serviceInfo.Methods[method] {
		return "", fmt.Errorf("method %s not supported for service %s", method, service)
	}

	// Construct the gRPC method path
	return fmt.Sprintf("/%s/%s", service, method), nil
}
