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
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// PrivateSelfSubjectAccessReviewsServerBuilder contains the data and logic needed to create a private self subject access reviews server.
type PrivateSelfSubjectAccessReviewsServerBuilder struct {
	logger    *slog.Logger
	evaluator auth.AuthorizationEvaluator
}

var _ privatev1.SelfSubjectAccessReviewsServer = (*PrivateSelfSubjectAccessReviewsServer)(nil)

type PrivateSelfSubjectAccessReviewsServer struct {
	privatev1.UnimplementedSelfSubjectAccessReviewsServer

	logger    *slog.Logger
	evaluator auth.AuthorizationEvaluator
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

func (b *PrivateSelfSubjectAccessReviewsServerBuilder) Build() (*PrivateSelfSubjectAccessReviewsServer, error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.evaluator == nil {
		return nil, errors.New("evaluator is mandatory")
	}

	result := &PrivateSelfSubjectAccessReviewsServer{
		logger:    b.logger,
		evaluator: b.evaluator,
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
	if review == nil {
		return nil, status.Error(codes.InvalidArgument, "object is required")
	}

	spec := review.GetSpec()
	if spec == nil {
		return nil, status.Error(codes.InvalidArgument, "spec is required")
	}

	// Build the gRPC method path and validate service/method exist
	methodPath, err := buildGRPCMethodPath(spec.GetService(), spec.GetMethod())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Extract AuthContext from the JWT token
	authContext, err := auth.ExtractAuthContext(token)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to extract auth context", slog.Any("error", err))
		return nil, status.Error(codes.Internal, "failed to process authentication")
	}

	// Build context extensions from the request metadata (tenant scope)
	if metadata := review.GetMetadata(); metadata != nil {
		authContext.Tenant = metadata.GetAnnotations()[("osac.openshift.io/tenant")]
	}

	// Evaluate authorization using the shared evaluator
	decision, err := s.evaluator.Evaluate(ctx, authContext, methodPath)
	if err != nil {
		s.logger.ErrorContext(ctx, "Failed to evaluate authorization", slog.Any("error", err))
		return nil, status.Error(codes.Internal, "authorization evaluation failed")
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
func buildGRPCMethodPath(service, method string) (string, error) {
	if service == "" {
		return "", fmt.Errorf("service is required")
	}
	if method == "" {
		return "", fmt.Errorf("method is required")
	}

	// Find the service descriptor using protobuf reflection
	var serviceDesc protoreflect.ServiceDescriptor
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		// Only check osac.public.v1 services
		if fd.Package() != "osac.public.v1" {
			return true
		}

		services := fd.Services()
		for i := 0; i < services.Len(); i++ {
			sd := services.Get(i)
			fullName := fmt.Sprintf("%s.%s", fd.Package(), sd.Name())
			if fullName == service {
				serviceDesc = sd
				return false // Stop iteration
			}
		}
		return true
	})

	if serviceDesc == nil {
		return "", fmt.Errorf("unknown service: %s", service)
	}

	// Check if this is the SelfSubjectAccessReviews service (prevent recursive checks)
	if string(serviceDesc.Name()) == "SelfSubjectAccessReviews" {
		return "", fmt.Errorf("cannot check permissions on SelfSubjectAccessReviews service")
	}

	// Validate that the service supports the requested method
	methods := serviceDesc.Methods()
	var found bool
	for i := 0; i < methods.Len(); i++ {
		if string(methods.Get(i).Name()) == method {
			found = true
			break
		}
	}

	if !found {
		return "", fmt.Errorf("method %s not supported for service %s", method, service)
	}

	// Construct the gRPC method path
	return fmt.Sprintf("/%s/%s", service, method), nil
}
