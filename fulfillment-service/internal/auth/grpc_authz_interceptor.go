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
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/open-policy-agent/opa/v1/rego"
	"github.com/open-policy-agent/opa/v1/storage/inmem"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/osac-project/fulfillment-service/internal/collections"
	k8sfiles "github.com/osac-project/fulfillment-service/internal/kubernetes/files"
)

//go:embed policies/authz.rego
var authzPolicy string

// ObjectMetadata contains the metadata fields fetched from the database for authorization decisions.
type ObjectMetadata struct {
	Tenant  string
	Name    string
	Project string
}

// MetadataFetcher fetches object metadata for authorization. Returns nil if the object is not found or on error.
type MetadataFetcher func(ctx context.Context, id string) *ObjectMetadata

// ContextExtensions holds the typed fields passed to OPA as input.context.context_extensions.
type ContextExtensions struct {
	ID      string
	Tenant  string
	Name    string
	Project string
}

// ToMap converts the extensions to the untyped map OPA expects, omitting empty fields.
func (e *ContextExtensions) ToMap() map[string]any {
	m := map[string]any{}
	if e.ID != "" {
		m["id"] = e.ID
	}
	if e.Tenant != "" {
		m["tenant"] = e.Tenant
	}
	if e.Name != "" {
		m["name"] = e.Name
	}
	if e.Project != "" {
		m["project"] = e.Project
	}
	return m
}

// GrpcAuthzInterceptorBuilder contains the data and logic needed to build an interceptor that checks authorization
// using an embedded Rego policy evaluated with the OPA library. Don't create instances of this type directly, use the
// NewGrpcAuthzInterceptor function instead.
type GrpcAuthzInterceptorBuilder struct {
	logger                           *slog.Logger
	anonymousMethods                 []string
	inputCallback                    func(ctx context.Context, input map[string]any) error
	metadataFetcher                  MetadataFetcher
	projectMembershipMetadataFetcher MetadataFetcher
	emergencyServiceAccounts         []string
}

// GrpcAuthzInterceptor is a gRPC interceptor that evaluates an embedded Rego policy for authorization. It reads the
// validated JWT token from the context (placed there by the authentication interceptor), constructs an input and
// evaluates the policy to determine if the request is allowed. On success it constructs a Subject containing the user
// and tenants and stores it in the context.
type GrpcAuthzInterceptor struct {
	logger                           *slog.Logger
	anonymousMethods                 []*regexp.Regexp
	inputCallback                    func(ctx context.Context, input map[string]any) error
	query                            rego.PreparedEvalQuery
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

// AddEmergencyServiceAccounts adds Kubernetes service account names that are allowed to access the private API with
// administrator permissions. These are intended only for emergency situations, for example when the regular
// authentication mechanisms are not working.
func (b *GrpcAuthzInterceptorBuilder) AddEmergencyServiceAccounts(
	values ...string) *GrpcAuthzInterceptorBuilder {
	b.emergencyServiceAccounts = append(b.emergencyServiceAccounts, values...)
	return b
}

// Build uses the data stored in the builder to create and configure a new interceptor.
func (b *GrpcAuthzInterceptorBuilder) Build() (result *GrpcAuthzInterceptor, err error) {
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}

	// If we are running in a Kubernetes pod we want to add a 'nsName' to the external data, so that it can be
	// used by the policy to construct the full names of Kubernetes service accounts.
	var nsName string
	nsBytes, err := os.ReadFile(k8sfiles.ServiceAccountNamespace)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			b.logger.Warn(
				"Kubernetes namespace file not found, will use the default",
				slog.String("file", k8sfiles.ServiceAccountNamespace),
				slog.String("default", grpcAuthzDefaultNamespace),
			)
			nsName = grpcAuthzDefaultNamespace
		} else {
			err = fmt.Errorf(
				"failed to read Kubernetes namespace file '%s': %w",
				k8sfiles.ServiceAccountNamespace, err,
			)
			return
		}
	} else {
		nsName = strings.TrimSpace(string(nsBytes))
		if nsName == "" {
			b.logger.Warn(
				"Kubernetes namespace file is empty",
				slog.String("file", k8sfiles.ServiceAccountNamespace),
			)
			nsName = grpcAuthzDefaultNamespace
		}
	}

	// Validate and build the full Kubernetes service account names for the emergency accounts:
	emergencyServiceAccounts := make([]any, 0, len(b.emergencyServiceAccounts))
	for _, emergencyServiceAccount := range b.emergencyServiceAccounts {
		emergencyServiceAccount = strings.TrimSpace(emergencyServiceAccount)
		errs := validation.IsDNS1123Subdomain(emergencyServiceAccount)
		if len(errs) > 0 {
			err = fmt.Errorf(
				"emergency service account name '%s' is not a valid Kubernetes service account name",
				emergencyServiceAccount,
			)
			return
		}

		emergencyServiceAccountName := fmt.Sprintf(
			"system:serviceaccount:%s:%s",
			nsName, emergencyServiceAccount,
		)
		emergencyServiceAccounts = append(emergencyServiceAccounts, emergencyServiceAccountName)
	}

	// Build external data for the policy:
	policyData := inmem.NewFromObject(map[string]any{
		"emergency_service_accounts": emergencyServiceAccounts,
	})

	// Prepare the Rego query by compiling the embedded policy once. The query evaluates all exported variables in
	// the authz package so we can read 'allow', 'subject_user', 'subject_tenant_result' and 'is_admin' from the
	// results.
	query, err := rego.New(
		rego.Query("data.authz"),
		rego.Module("authz.rego", authzPolicy),
		rego.Store(policyData),
	).PrepareForEval(context.Background())
	if err != nil {
		err = fmt.Errorf("failed to compile authorization policy: %w", err)
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
		query:                            query,
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
	// Build the input for the Rego policy:
	input, err := i.buildInput(ctx, method, request, token)
	if err != nil {
		i.logger.ErrorContext(
			ctx,
			"Failed to build authorization policy input",
			slog.Any("error", err),
		)
		err = grpcstatus.Error(grpccodes.Internal, "failed to process authorization")
		return
	}

	// If there is an input callback, call it:
	if i.inputCallback != nil {
		err = i.inputCallback(ctx, input)
		if err != nil {
			i.logger.ErrorContext(
				ctx,
				"Failed to call input callback",
				slog.Any("error", err),
			)
			err = grpcstatus.Error(grpccodes.Internal, "internal error")
			return
		}
	}

	// Evaluate th e policy:
	logger := i.logger.With(slog.String("method", method))
	results, err := i.query.Eval(ctx, rego.EvalInput(input))
	if err != nil {
		logger.ErrorContext(
			ctx,
			"Failed to evaluate authorization policy",
			slog.Any("error", err),
		)
		err = grpcstatus.Error(grpccodes.Internal, "failed to evaluate authorization policy")
		return
	}
	if len(results) == 0 {
		logger.DebugContext(ctx, "Authorization policy returned no results")
		err = grpcstatus.Error(grpccodes.PermissionDenied, "permission denied")
		return
	}

	// The query is 'data.authz' so 'results[0].Expressions[0].Value' is a map of all exported variables:
	authzData, ok := results[0].Expressions[0].Value.(map[string]any)
	if !ok {
		logger.ErrorContext(ctx, "Authorization policy returned unexpected result type")
		err = grpcstatus.Error(grpccodes.Internal, "failed to evaluate authorization policy")
		return
	}

	// Check if the request is allowed:
	allow, _ := authzData["allow"].(bool)
	if !allow {
		logger.DebugContext(ctx, "Permission denied by authorization policy")
		err = grpcstatus.Error(grpccodes.PermissionDenied, "permission denied")
		return
	}

	// Build the subject from the policy output:
	subject, err := i.buildSubject(authzData)
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

// buildInput constructs the OPA input map from the validated JWT token.
func (i *GrpcAuthzInterceptor) buildInput(ctx context.Context, method string, request any,
	token *jwt.Token) (result map[string]any, err error) {
	// Get the claims from the token:
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		err = fmt.Errorf("unexpected claims type")
		return
	}

	// Check if this the token corresponds to a Kubernetes service account:
	_, kube := claims["kubernetes.io"]

	// Build the context extensions that the OPA policy reads from input.context.context_extensions.
	var ext ContextExtensions
	if request != nil {
		ext.ID = i.extractId(request)

		// For project Get/Delete/Update operations, fetch the authoritative tenant and name from the database
		// to prevent clients from spoofing these values for authorization bypass.
		if i.shouldFetchProjectMetadata(method, ext.ID) && i.metadataFetcher != nil {
			if meta := i.metadataFetcher(ctx, ext.ID); meta != nil {
				ext.Tenant = meta.Tenant
				ext.Name = meta.Name
			}
		}

		// For project membership Get/Delete/Update, fetch the membership's tenant and project name
		// from the database for authorization.
		if i.shouldFetchProjectMembershipMetadata(method, ext.ID) && i.projectMembershipMetadataFetcher != nil {
			if meta := i.projectMembershipMetadataFetcher(ctx, ext.ID); meta != nil {
				ext.Tenant = meta.Tenant
				ext.Project = meta.Project
			}
		}

		// For project membership Create, extract the project name from the request body.
		// The OPA policy iterates over the user's tenants to check manager group membership.
		if method == "/osac.public.v1.ProjectMemberships/Create" {
			ext.Project = i.extractProjectFromRequest(request)
		}
	}

	// Build the identity document:
	identity := map[string]any{}
	if kube {
		identity["authnMethod"] = "serviceaccount"
		sub, _ := claims["sub"].(string)
		identity["user"] = map[string]any{
			"username": sub,
			"groups":   claimAsAnySlice(claims, "groups"),
		}
	} else {
		identity["authnMethod"] = "jwt"

		username, _ := claims["preferred_username"].(string)
		if username == "" {
			username, _ = claims["username"].(string)
		}
		identity["username"] = username

		if groups := claimAsAnySlice(claims, "groups"); groups != nil {
			identity["groups"] = groups
		}
		// Handle organization claim - can be array or object
		if orgValue := claims["organization"]; orgValue != nil {
			if orgArray := claimAsAnySlice(claims, "organization"); orgArray != nil {
				identity["organization"] = orgArray
			} else if orgObj, ok := orgValue.(map[string]any); ok {
				identity["organization"] = orgObj
			}
		}
		if orgs := claimAsAnySlice(claims, "organizations"); orgs != nil {
			identity["organizations"] = orgs
		}
		if realmAccess, ok := claims["realm_access"].(map[string]any); ok {
			identity["realm_access"] = realmAccess
		}
	}

	// Build the input document:
	result = map[string]any{
		"context": map[string]any{
			"request": map[string]any{
				"http": map[string]any{
					"path": method,
				},
			},
			"context_extensions": ext.ToMap(),
		},
		"auth": map[string]any{
			"identity": identity,
		},
	}
	return
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
	reflect := message.ProtoReflect()

	objectField := reflect.Descriptor().Fields().ByName("object")
	if objectField == nil {
		return ""
	}
	if !reflect.Has(objectField) {
		return ""
	}
	objectMsg := reflect.Get(objectField).Message()

	metadataField := objectMsg.Descriptor().Fields().ByName("metadata")
	if metadataField == nil {
		return ""
	}
	if !objectMsg.Has(metadataField) {
		return ""
	}
	metadataMsg := objectMsg.Get(metadataField).Message()

	projectField := metadataMsg.Descriptor().Fields().ByName("project")
	if projectField == nil {
		return ""
	}
	return metadataMsg.Get(projectField).String()
}

// claimAsAnySlice extracts a claim value and returns it as []any, which is the type that JSON-decoded arrays produce
// and what OPA expects.
func claimAsAnySlice(claims jwt.MapClaims, name string) []any {
	value, ok := claims[name]
	if !ok || value == nil {
		return nil
	}
	switch v := value.(type) {
	case []any:
		return v
	case []string:
		result := make([]any, len(v))
		for i, s := range v {
			result[i] = s
		}
		return result
	default:
		return nil
	}
}

// buildSubject constructs a Subject from the Rego policy evaluation output.
func (i *GrpcAuthzInterceptor) buildSubject(authzData map[string]any) (result *Subject, err error) {
	user, _ := authzData["subject_user"].(string)
	if user == "" {
		err = fmt.Errorf("policy did not produce a subject_user")
		return
	}

	tenantValues, _ := authzData["subject_tenant_result"].([]any)
	var tenantNames []string
	for _, v := range tenantValues {
		if s, ok := v.(string); ok {
			if s == "*" {
				result = &Subject{
					User:    user,
					Tenants: AllTenants,
				}
				return
			}
			tenantNames = append(tenantNames, s)
		}
	}

	result = &Subject{
		User:    user,
		Tenants: collections.NewSet(tenantNames...),
	}
	return
}

// k8sTokenFile is the value of the 'namespace' external data item passed to the Rego policy when we aren't running
// inside a Kubernetes pod.
const grpcAuthzDefaultNamespace = "osac"
