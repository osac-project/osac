/*
Copyright (c) 2025 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"

	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/collections"
	"github.com/osac-project/osac/fulfillment-service/internal/reflection"
)

// ObjectMetadata contains the metadata fields fetched from the database for authorization decisions.
type ObjectMetadata struct {
	Tenant  string
	Name    string
	Project string
}

// MetadataFetcher fetches object metadata for authorization. Returns nil if the object is not found or on error.
type MetadataFetcher func(ctx context.Context, id string) *ObjectMetadata

// GrpcAuthzInterceptorBuilder contains the data and logic needed to build an interceptor that checks authorization
// using an embedded Rego policy evaluated with the OPA library. Don't create instances of this type directly, use the
// NewGrpcAuthzInterceptor function instead.
type GrpcAuthzInterceptorBuilder struct {
	logger                           *slog.Logger
	anonymousMethods                 []string
	inputCallback                    func(ctx context.Context, input map[string]any) error
	metadataFetcher                  MetadataFetcher
	projectMembershipMetadataFetcher MetadataFetcher
	evaluator                        AuthorizationEvaluator
}

// GrpcAuthzInterceptor is a gRPC interceptor that evaluates an embedded Rego policy for authorization. It reads the
// validated JWT token from the context (placed there by the authentication interceptor), constructs an input and
// evaluates the policy to determine if the request is allowed. On success it constructs a Subject containing the user
// and tenants and stores it in the context.
type GrpcAuthzInterceptor struct {
	logger                           *slog.Logger
	anonymousMethods                 []*regexp.Regexp
	inputCallback                    func(ctx context.Context, input map[string]any) error
	evaluator                        AuthorizationEvaluator
	metadataFetcher                  MetadataFetcher
	projectMembershipMetadataFetcher MetadataFetcher
}

// NewGrpcAuthzInterceptor creates a builder that can then be used to configure and create a new authorization
// interceptor.
func NewGrpcAuthzInterceptor() *GrpcAuthzInterceptorBuilder {
	return &GrpcAuthzInterceptorBuilder{}
}

// SetLogger sets the logger that will be used to write to the log. This is mandatory.
func (b *GrpcAuthzInterceptorBuilder) SetLogger(value *slog.Logger) *GrpcAuthzInterceptorBuilder {
	b.logger = value
	return b
}

// AddAnonymousMethodRegex adds a regular expression that describes a set of methods that are allowed without
// authentication. The regular expression will be matched against the full gRPC method name, including the leading
// slash. For example, to allow anonymous access to all the methods of the 'example.v1.Products' service the regular
// expression could be '^/example\.v1\.Products/.*$'.
//
// This method may be called multiple times to add multiple regular expressions. A method will be considered anonymous
// if it matches at least one of them.
func (b *GrpcAuthzInterceptorBuilder) AddAnonymousMethodRegex(value string) *GrpcAuthzInterceptorBuilder {
	b.anonymousMethods = append(b.anonymousMethods, value)
	return b
}

// SetInputCallback sets the function used to inspect and potenttially modify the input before it is passed to the
// policy for evaluation.
func (b *GrpcAuthzInterceptorBuilder) SetInputCallback(value func(ctx context.Context,
	input map[string]any) error) *GrpcAuthzInterceptorBuilder {
	b.inputCallback = value
	return b
}

// SetMetadataFetcher sets the function used to retrieve object metadata (tenant and project name) for authorization.
// This is optional - if not set, metadata will not be fetched for authorization.
func (b *GrpcAuthzInterceptorBuilder) SetMetadataFetcher(value MetadataFetcher) *GrpcAuthzInterceptorBuilder {
	b.metadataFetcher = value
	return b
}

// SetProjectMembershipMetadataFetcher sets the function used to retrieve project membership metadata (tenant and
// project name) for authorization of project membership operations.
func (b *GrpcAuthzInterceptorBuilder) SetProjectMembershipMetadataFetcher(
	value MetadataFetcher) *GrpcAuthzInterceptorBuilder {
	b.projectMembershipMetadataFetcher = value
	return b
}

// SetEvaluator sets the AuthorizationEvaluator to use for policy evaluation. This is mandatory.
func (b *GrpcAuthzInterceptorBuilder) SetEvaluator(value AuthorizationEvaluator) *GrpcAuthzInterceptorBuilder {
	b.evaluator = value
	return b
}

// Build uses the data stored in the builder to create and configure a new interceptor.
func (b *GrpcAuthzInterceptorBuilder) Build() (result *GrpcAuthzInterceptor, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}

	if b.evaluator == nil {
		err = errors.New("evaluator is mandatory")
		return
	}

	// Compile anonymous method regexes:
	anonymousMethods := make([]*regexp.Regexp, len(b.anonymousMethods))
	for i, expr := range b.anonymousMethods {
		anonymousMethods[i], err = regexp.Compile(expr)
		if err != nil {
			err = fmt.Errorf("failed to compile public method regex '%s': %w", expr, err)
			return
		}
	}

	// Create the interceptor:
	result = &GrpcAuthzInterceptor{
		logger:                           b.logger,
		anonymousMethods:                 anonymousMethods,
		metadataFetcher:                  b.metadataFetcher,
		projectMembershipMetadataFetcher: b.projectMembershipMetadataFetcher,
		inputCallback:                    b.inputCallback,
		evaluator:                        b.evaluator,
	}
	return
}

// UnaryServer is the unary server interceptor function.
func (i *GrpcAuthzInterceptor) UnaryServer(ctx context.Context, request any,
	info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (response any, err error) {
	ctx, err = i.authorize(ctx, info.FullMethod, request)
	if err != nil {
		return
	}
	return handler(ctx, request)
}

// StreamServer is the stream server interceptor function.
func (i *GrpcAuthzInterceptor) StreamServer(server any, stream grpc.ServerStream,
	info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	ctx, err := i.authorize(stream.Context(), info.FullMethod, nil)
	if err != nil {
		return err
	}
	stream = &grpcAuthzStream{
		context: ctx,
		stream:  stream,
	}
	return handler(server, stream)
}

// grpcAuthzStream wraps a gRPC server stream with a modified context.
type grpcAuthzStream struct {
	context context.Context
	stream  grpc.ServerStream
}

func (s *grpcAuthzStream) Context() context.Context {
	return s.context
}

func (s *grpcAuthzStream) RecvMsg(message any) error {
	return s.stream.RecvMsg(message)
}

func (s *grpcAuthzStream) SendHeader(md metadata.MD) error {
	return s.stream.SendHeader(md)
}

func (s *grpcAuthzStream) SendMsg(message any) error {
	return s.stream.SendMsg(message)
}

func (s *grpcAuthzStream) SetHeader(md metadata.MD) error {
	return s.stream.SetHeader(md)
}

func (s *grpcAuthzStream) SetTrailer(md metadata.MD) {
	s.stream.SetTrailer(md)
}

// authorize evaluates the Rego policy for the given method and the token from the context. If the request is allowed it
// constructs a subject and stores it in the context.
func (i *GrpcAuthzInterceptor) authorize(ctx context.Context, method string, request any) (result context.Context,
	err error) {
	token := TokenFromContext(ctx)
	if token == nil {
		result, err = i.authorizeWithoutToken(ctx, method)
		return
	}
	result, err = i.authorizeWithToken(ctx, method, request, token)
	return
}

func (i *GrpcAuthzInterceptor) authorizeWithoutToken(ctx context.Context, method string) (result context.Context,
	err error) {
	if i.isAnonymousMethod(method) {
		result = ContextWithSubject(ctx, Guest)
		return
	}
	err = grpcstatus.Errorf(grpccodes.Unauthenticated, "method '%s' requires authentication", method)
	return
}

func (i *GrpcAuthzInterceptor) authorizeWithToken(ctx context.Context, method string, request any,
	token *jwt.Token) (result context.Context, err error) {
	logger := i.logger.With(slog.String("method", method))

	// Extract authentication context from the JWT token
	authContext, err := ExtractAuthContext(token)
	if err != nil {
		logger.ErrorContext(
			ctx,
			"Failed to extract authentication context",
			slog.Any("error", err),
		)
		err = grpcstatus.Error(grpccodes.Internal, "failed to process authorization")
		return
	}

	// Build context extensions from the request and method
	i.buildContextExtensions(ctx, authContext, method, request)

	// If there is an input callback, build the legacy input map and call it for backwards compatibility
	if i.inputCallback != nil {
		input := constructOPAInput(authContext, method)
		err = i.inputCallback(ctx, input)
		if err != nil {
			logger.ErrorContext(
				ctx,
				"Failed to call input callback",
				slog.Any("error", err),
			)
			err = grpcstatus.Error(grpccodes.Internal, "internal error")
			return
		}
	}

	// Evaluate the authorization policy using the shared evaluator
	decision, err := i.evaluator.Evaluate(ctx, authContext, method)
	if err != nil {
		logger.ErrorContext(
			ctx,
			"Failed to evaluate authorization policy",
			slog.Any("error", err),
		)
		err = grpcstatus.Error(grpccodes.Internal, "failed to evaluate authorization policy")
		return
	}

	// Check if the request is allowed
	if decision == nil || !decision.Allowed {
		logger.DebugContext(ctx, "Permission denied by authorization policy")
		err = grpcstatus.Error(grpccodes.PermissionDenied, "permission denied")
		return
	}

	// Build the subject from the decision
	subject, err := i.buildSubjectFromDecision(decision)
	if err != nil {
		logger.ErrorContext(
			ctx,
			"Failed to build subject from policy output",
			slog.Any("error", err),
		)
		err = grpcstatus.Error(grpccodes.Internal, "failed to process authorization")
		return
	}

	// Store subject in context
	result = ContextWithSubject(ctx, subject)

	logger.DebugContext(
		result,
		"Permission granted by authorization policy",
		slog.String("user", subject.User),
	)
	return
}

// extractId tries to extract the identifier of the object from the incoming request message. For get and delete
// requests, the identifier is directly available via the 'GetId' method. For update requests, the identifier is inside
// the 'object' field, which is accessed via protobuf reflection.
func (i *GrpcAuthzInterceptor) extractId(request any) string {
	// First try to get the identifier directly from the request. This works for any request message that has a
	// 'GetId' method, including get and delete requests.
	type idGetter interface {
		GetId() string
	}
	getter, ok := request.(idGetter)
	if ok {
		return getter.GetId()
	}

	// If the request doesn't have a direct identifier, try to extract it from the nested 'object' field using
	// protobuf reflection. This is necessary for update requests, for example, where the identifier is inside
	// the object.
	message, ok := request.(proto.Message)
	if !ok {
		return ""
	}
	reflect := message.ProtoReflect()
	field := reflect.Descriptor().Fields().ByName("object")
	if field == nil {
		return ""
	}
	if !reflect.Has(field) {
		return ""
	}
	value := reflect.Get(field)
	getter, ok = value.Message().Interface().(idGetter)
	if !ok {
		return ""
	}
	return getter.GetId()
}

// isAnonymousMethod checks if the given method is anonymous by matching it against the configured regular expressions.
func (i *GrpcAuthzInterceptor) isAnonymousMethod(method string) bool {
	for _, anonymousMethod := range i.anonymousMethods {
		if anonymousMethod.MatchString(method) {
			return true
		}
	}
	return false
}

// buildContextExtensions builds the ContextExtensions from the request and method.
// This includes extracting IDs, fetching metadata from the database for certain operations,
// and extracting project names from request bodies.
func (i *GrpcAuthzInterceptor) buildContextExtensions(ctx context.Context, authContext *AuthContext, method string, request any) {
	if request != nil {
		authContext.ID = i.extractId(request)

		// For project Get/Delete/Update operations, fetch the authoritative tenant and name from the database
		// to prevent clients from spoofing these values for authorization bypass.
		if i.shouldFetchProjectMetadata(method, authContext.ID) && i.metadataFetcher != nil {
			if meta := i.metadataFetcher(ctx, authContext.ID); meta != nil {
				authContext.Tenant = meta.Tenant
				authContext.Name = meta.Name
			}
		}

		// For project membership Get/Delete/Update, fetch the membership's tenant and project name
		// from the database for authorization.
		if i.shouldFetchProjectMembershipMetadata(method, authContext.ID) && i.projectMembershipMetadataFetcher != nil {
			if meta := i.projectMembershipMetadataFetcher(ctx, authContext.ID); meta != nil {
				authContext.Tenant = meta.Tenant
				authContext.Project = meta.Project
			}
		}

		// For project membership Create, extract the project name from the request body.
		// The OPA policy iterates over the user's tenants to check manager group membership.
		if method == "/osac.public.v1.ProjectMemberships/Create" {
			authContext.Project = i.extractProjectFromRequest(request)
		}
	}
}

// shouldFetchProjectMetadata determines if we should fetch project metadata from the database for authorization.
// This is needed for Get/Delete/Update operations to prevent clients from spoofing tenant/name values for
// authorization bypass.
func (i *GrpcAuthzInterceptor) shouldFetchProjectMetadata(method string, id string) bool {
	if id == "" {
		return false
	}
	// Fetch metadata for Projects Get, Delete, and Update operations
	return method == "/osac.public.v1.Projects/Get" ||
		method == "/osac.public.v1.Projects/Delete" ||
		method == "/osac.public.v1.Projects/Update"
}

// shouldFetchProjectMembershipMetadata determines if we should fetch project membership metadata for authorization.
func (i *GrpcAuthzInterceptor) shouldFetchProjectMembershipMetadata(method string, id string) bool {
	if id == "" {
		return false
	}
	return method == "/osac.public.v1.ProjectMemberships/Get" ||
		method == "/osac.public.v1.ProjectMemberships/Delete" ||
		method == "/osac.public.v1.ProjectMemberships/Update"
}

// extractProjectFromRequest extracts the project name from the request's embedded object metadata using proto
// reflection. This navigates object -> metadata -> project.
func (i *GrpcAuthzInterceptor) extractProjectFromRequest(request any) string {
	message, ok := request.(proto.Message)
	if !ok {
		return ""
	}
	return reflection.ResolveFieldPathOr(message, "object.metadata.project", "")
}

// claimAsAnySlice extracts a claim value and returns it as []any, which is the type that JSON-decoded arrays produce
// and what OPA expects.

// buildSubjectFromDecision constructs a Subject from an AuthzDecision.
func (i *GrpcAuthzInterceptor) buildSubjectFromDecision(decision *AuthzDecision) (result *Subject, err error) {
	if decision.SubjectUser == "" {
		err = fmt.Errorf("policy did not produce a subject_user")
		return
	}

	// Check for the universal tenant marker "*"
	for _, tenant := range decision.SubjectTenants {
		if tenant == "*" {
			result = &Subject{
				User:    decision.SubjectUser,
				Tenants: AllTenants,
			}
			return
		}
	}

	result = &Subject{
		User:    decision.SubjectUser,
		Tenants: collections.NewSet(decision.SubjectTenants...),
	}
	return
}
