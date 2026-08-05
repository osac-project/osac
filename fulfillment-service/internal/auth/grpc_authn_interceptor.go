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
	"strings"

	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	grpcstatus "google.golang.org/grpc/status"
)

// GrpcAuthnInterceptorBuilder contains the data and logic needed to build an interceptor that validates JWT tokens
// using a JwtValidator. Don't create instances of this type directly, use the NewGrpcAuthnInterceptor function
// instead.
type GrpcAuthnInterceptorBuilder struct {
	logger           *slog.Logger
	jwtValidator     JwtValidator
	anonymousMethods []string
}

// GrpcAuthnInterceptor is a gRPC interceptor that validates JWT tokens. It extracts the bearer token from the
// authorization header, validates it using the configured JwtValidator, and stores the validated token in the context
// for downstream interceptors and handlers.
type GrpcAuthnInterceptor struct {
	logger           *slog.Logger
	jwtValidator     JwtValidator
	anonymousMethods []*regexp.Regexp
}

// NewGrpcAuthnInterceptor creates a builder that can then be used to configure and create a new authentication
// interceptor.
func NewGrpcAuthnInterceptor() *GrpcAuthnInterceptorBuilder {
	return &GrpcAuthnInterceptorBuilder{}
}

// SetLogger sets the logger that will be used to write to the log. This is mandatory.
func (b *GrpcAuthnInterceptorBuilder) SetLogger(value *slog.Logger) *GrpcAuthnInterceptorBuilder {
	b.logger = value
	return b
}

// SetJwtValidator sets the JWT validator that will be used to validate bearer tokens. This is mandatory.
func (b *GrpcAuthnInterceptorBuilder) SetJwtValidator(value JwtValidator) *GrpcAuthnInterceptorBuilder {
	b.jwtValidator = value
	return b
}

// AddAnonymousMethodRegex adds a regular expression that describes a set of methods that are considered anonymous, and
// therefore require no authentication. The regular expression will be matched against the full gRPC method name,
// including the leading slash. For example, to consider anonymous all the methods of the 'example.v1.Products' service
// the regular expression could be '^/example\.v1\.Products/.*$'.
//
// This method may be called multiple times to add multiple regular expressions. A method will be considered anonymous
// if it matches at least one of them.
func (b *GrpcAuthnInterceptorBuilder) AddAnonymousMethodRegex(value string) *GrpcAuthnInterceptorBuilder {
	b.anonymousMethods = append(b.anonymousMethods, value)
	return b
}

// Build uses the data stored in the builder to create and configure a new interceptor.
func (b *GrpcAuthnInterceptorBuilder) Build() (result *GrpcAuthnInterceptor, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.jwtValidator == nil {
		err = errors.New("JWT validator is mandatory")
		return
	}

	// Compile anonymous method regexes:
	anonymousMethods := make([]*regexp.Regexp, len(b.anonymousMethods))
	for i, expr := range b.anonymousMethods {
		anonymousMethods[i], err = regexp.Compile(expr)
		if err != nil {
			err = fmt.Errorf("failed to compile anonymous method regex '%s': %w", expr, err)
			return
		}
	}

	// Create the interceptor:
	result = &GrpcAuthnInterceptor{
		logger:           b.logger,
		jwtValidator:     b.jwtValidator,
		anonymousMethods: anonymousMethods,
	}
	return
}

// UnaryServer is the unary server interceptor function.
func (i *GrpcAuthnInterceptor) UnaryServer(ctx context.Context, request any, info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler) (response any, err error) {
	ctx, err = i.authenticate(ctx, info.FullMethod)
	if err != nil {
		return
	}
	return handler(ctx, request)
}

// StreamServer is the stream server interceptor function.
func (i *GrpcAuthnInterceptor) StreamServer(server any, stream grpc.ServerStream, info *grpc.StreamServerInfo,
	handler grpc.StreamHandler) error {
	ctx, err := i.authenticate(stream.Context(), info.FullMethod)
	if err != nil {
		return err
	}
	stream = &grpcAuthnStream{
		context: ctx,
		stream:  stream,
	}
	return handler(server, stream)
}

// grpcAuthnStream wraps a gRPC server stream with a modified context.
type grpcAuthnStream struct {
	context context.Context
	stream  grpc.ServerStream
}

func (s *grpcAuthnStream) Context() context.Context {
	return s.context
}

func (s *grpcAuthnStream) RecvMsg(message any) error {
	return s.stream.RecvMsg(message)
}

func (s *grpcAuthnStream) SendHeader(md metadata.MD) error {
	return s.stream.SendHeader(md)
}

func (s *grpcAuthnStream) SendMsg(message any) error {
	return s.stream.SendMsg(message)
}

func (s *grpcAuthnStream) SetHeader(md metadata.MD) error {
	return s.stream.SetHeader(md)
}

func (s *grpcAuthnStream) SetTrailer(md metadata.MD) {
	s.stream.SetTrailer(md)
}

// authenticate extracts and validates the bearer token from the gRPC metadata, then stores the parsed token in the
// context. Anonymous methods skip authentication entirely, regardless of whether an authorization header is present.
// This matches the behavior of the previous external auth interceptor and is required because some clients (e.g.
// grpcurl) send non-JWT bearer tokens (such as JWE console tickets) on all requests, including anonymous methods
// like reflection.
func (i *GrpcAuthnInterceptor) authenticate(ctx context.Context, method string) (result context.Context, err error) {
	if i.isAnonymous(method) {
		result = ctx
		return
	}
	values := metadata.ValueFromIncomingContext(ctx, Authorization)
	length := len(values)
	switch length {
	case 0:
		err = grpcstatus.Errorf(grpccodes.Unauthenticated, "method '%s' requires authentication", method)
	case 1:
		header := values[0]
		result, err = i.withHeader(ctx, header)
	default:
		err = grpcstatus.Errorf(
			grpccodes.Unauthenticated,
			"expected at most one 'authorization' header, but received %d",
			length,
		)
	}
	return
}

func (i *GrpcAuthnInterceptor) withHeader(ctx context.Context, header string) (result context.Context, err error) {
	bearer, err := i.extractBearer(header)
	if err != nil {
		return
	}
	token, err := i.jwtValidator.Validate(ctx, bearer)
	if err != nil {
		err = grpcstatus.Errorf(grpccodes.Unauthenticated, "%s", err.Error())
		return
	}
	result = ContextWithToken(ctx, token)
	return
}

// extractBearer extracts the bearer token from the authorization header value.
func (i *GrpcAuthnInterceptor) extractBearer(auth string) (result string, err error) {
	matches := authnBearerRE.FindStringSubmatch(auth)
	if len(matches) != 3 {
		err = grpcstatus.Error(
			grpccodes.Unauthenticated,
			"authorization header value should be 'Bearer TOKEN'",
		)
		return
	}
	scheme := matches[1]
	if !strings.EqualFold(scheme, "Bearer") {
		err = grpcstatus.Errorf(
			grpccodes.Unauthenticated,
			"authentication scheme '%s' is not supported",
			scheme,
		)
		return
	}
	result = matches[2]
	return
}

// isAnonymous checks if the given method is anonymous by matching it against the configured anonymous method
// regular expressions.
func (i *GrpcAuthnInterceptor) isAnonymous(method string) bool {
	for _, anonymousMethod := range i.anonymousMethods {
		if anonymousMethod.MatchString(method) {
			return true
		}
	}
	return false
}

// authnBearerRE is the regular expression used to extract the bearer token from the authorization header.
var authnBearerRE = regexp.MustCompile(`^([a-zA-Z0-9]+)\s+(.*)$`)
