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
	"time"

	"github.com/golang-jwt/jwt/v5"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	grpcstatus "google.golang.org/grpc/status"

	. "github.com/osac-project/fulfillment-service/internal/testing"
)

var _ = Describe("Authentication interceptor creation", func() {
	var ctrl *gomock.Controller

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
	})

	It("Rejects builder without logger", func() {
		jwtValidator := NewMockJwtValidator(ctrl)
		_, err := NewGrpcAuthnInterceptor().
			SetJwtValidator(jwtValidator).
			Build()
		Expect(err).To(MatchError("logger is mandatory"))
	})

	It("Rejects builder without JWT validator", func() {
		_, err := NewGrpcAuthnInterceptor().
			SetLogger(logger).
			Build()
		Expect(err).To(MatchError("JWT validator is mandatory"))
	})

	It("Can be built with valid parameters", func() {
		jwtValidator := NewMockJwtValidator(ctrl)
		interceptor, err := NewGrpcAuthnInterceptor().
			SetLogger(logger).
			SetJwtValidator(jwtValidator).
			Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(interceptor).ToNot(BeNil())
	})

	It("Rejects invalid anonymous method regex", func() {
		jwtValidator := NewMockJwtValidator(ctrl)
		_, err := NewGrpcAuthnInterceptor().
			SetLogger(logger).
			SetJwtValidator(jwtValidator).
			AddAnonymousMethodRegex("[invalid").
			Build()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to compile anonymous method regex"))
	})
})

var _ = Describe("Authentication interceptor behaviour", func() {
	var (
		ctrl      *gomock.Controller
		issuerUrl string
	)

	BeforeEach(func() {
		// Create the mock controller:
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)

		// Create the issuer URL:
		issuerUrl = "https://good-issuer.example.com"
	})

	It("Rejects request without authorization header for private method", func(ctx context.Context) {
		jwtValidator := NewMockJwtValidator(ctrl)
		interceptor, err := NewGrpcAuthnInterceptor().
			SetLogger(logger).
			SetJwtValidator(jwtValidator).
			Build()
		Expect(err).ToNot(HaveOccurred())
		_, err = interceptor.UnaryServer(
			ctx,
			nil,
			&grpc.UnaryServerInfo{
				FullMethod: "/my_package/MyMethod",
			},
			func(ctx context.Context, req any) (any, error) {
				return nil, nil
			},
		)
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.Unauthenticated))
		Expect(status.Message()).To(Equal("method '/my_package/MyMethod' requires authentication"))
	})

	It("Rejects bad authorization type", func(ctx context.Context) {
		jwtValidator := NewMockJwtValidator(ctrl)
		interceptor, err := NewGrpcAuthnInterceptor().
			SetLogger(logger).
			SetJwtValidator(jwtValidator).
			Build()
		Expect(err).ToNot(HaveOccurred())
		bearer := MakeTokenString(issuerUrl, "Bearer", time.Minute)
		ctx = metadata.NewIncomingContext(ctx, metadata.Pairs(Authorization, "Bad "+bearer))
		_, err = interceptor.UnaryServer(
			ctx,
			nil,
			&grpc.UnaryServerInfo{
				FullMethod: "/my_package/MyMethod",
			},
			func(ctx context.Context, req any) (any, error) {
				return nil, nil
			},
		)
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.Unauthenticated))
		Expect(status.Message()).To(Equal("authentication scheme 'Bad' is not supported"))
	})

	It("Returns token validator error as gRPC status", func(ctx context.Context) {
		jwtValidator := NewMockJwtValidator(ctrl)
		errValidation := errors.New("my reason")
		jwtValidator.EXPECT().
			Validate(gomock.Any(), gomock.Any()).
			Return(nil, errValidation)
		interceptor, err := NewGrpcAuthnInterceptor().
			SetLogger(logger).
			SetJwtValidator(jwtValidator).
			Build()
		Expect(err).ToNot(HaveOccurred())
		bearer := MakeTokenString(issuerUrl, "Bearer", time.Minute)
		ctx = metadata.NewIncomingContext(ctx, metadata.Pairs(Authorization, "Bearer "+bearer))
		handled := false
		_, err = interceptor.UnaryServer(
			ctx,
			nil,
			&grpc.UnaryServerInfo{
				FullMethod: "/my_package/MyMethod",
			},
			func(ctx context.Context, req any) (any, error) {
				handled = true
				return nil, nil
			},
		)
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.Unauthenticated))
		Expect(status.Message()).To(Equal("my reason"))
		Expect(handled).To(BeFalse())
	})

	It("Stores the validated token in the context", func(ctx context.Context) {
		token := MakeTokenObject(nil, jwt.MapClaims{
			"iss": issuerUrl,
		})
		bearer := token.Raw
		jwtValidator := NewMockJwtValidator(ctrl)
		jwtValidator.EXPECT().
			Validate(gomock.Any(), bearer).
			Return(token, nil)
		interceptor, err := NewGrpcAuthnInterceptor().
			SetLogger(logger).
			SetJwtValidator(jwtValidator).
			Build()
		Expect(err).ToNot(HaveOccurred())
		ctx = metadata.NewIncomingContext(ctx, metadata.Pairs(Authorization, "Bearer "+bearer))
		handled := false
		_, err = interceptor.UnaryServer(
			ctx,
			nil,
			&grpc.UnaryServerInfo{
				FullMethod: "/my_package/MyMethod",
			},
			func(ctx context.Context, req any) (any, error) {
				stored := TokenFromContext(ctx)
				Expect(stored).To(BeIdenticalTo(token))
				handled = true
				return nil, nil
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(handled).To(BeTrue())
	})

	It("Doesn't require authorization header for anonymous method", func(ctx context.Context) {
		jwtValidator := NewMockJwtValidator(ctrl)
		interceptor, err := NewGrpcAuthnInterceptor().
			SetLogger(logger).
			SetJwtValidator(jwtValidator).
			AddAnonymousMethodRegex(`^/my_package/.*$`).
			Build()
		Expect(err).ToNot(HaveOccurred())
		handled := false
		_, err = interceptor.UnaryServer(
			ctx,
			nil,
			&grpc.UnaryServerInfo{
				FullMethod: "/my_package/MyMethod",
			},
			func(ctx context.Context, req any) (any, error) {
				handled = true
				return nil, nil
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(handled).To(BeTrue())
	})

	It("Ignores invalid token for anonymous method", func(ctx context.Context) {
		jwtValidator := NewMockJwtValidator(ctrl)
		interceptor, err := NewGrpcAuthnInterceptor().
			SetLogger(logger).
			SetJwtValidator(jwtValidator).
			AddAnonymousMethodRegex(`^/my_package/.*$`).
			Build()
		Expect(err).ToNot(HaveOccurred())
		bearer := MakeTokenString(issuerUrl, "Bearer", -time.Minute)
		ctx = metadata.NewIncomingContext(ctx, metadata.Pairs(Authorization, "Bearer "+bearer))
		handled := false
		_, err = interceptor.UnaryServer(
			ctx,
			nil,
			&grpc.UnaryServerInfo{
				FullMethod: "/my_package/MyMethod",
			},
			func(ctx context.Context, req any) (any, error) {
				stored := TokenFromContext(ctx)
				Expect(stored).To(BeNil())
				handled = true
				return nil, nil
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(handled).To(BeTrue())
	})

	It("Ignores valid token for anonymous method", func(ctx context.Context) {
		jwtValidator := NewMockJwtValidator(ctrl)
		interceptor, err := NewGrpcAuthnInterceptor().
			SetLogger(logger).
			SetJwtValidator(jwtValidator).
			AddAnonymousMethodRegex(`^/my_package/.*$`).
			Build()
		Expect(err).ToNot(HaveOccurred())
		token := MakeTokenObject(nil, jwt.MapClaims{
			"iss": issuerUrl,
			"sub": "user-123",
		})
		ctx = metadata.NewIncomingContext(ctx, metadata.Pairs(Authorization, "Bearer "+token.Raw))
		handled := false
		_, err = interceptor.UnaryServer(
			ctx,
			nil,
			&grpc.UnaryServerInfo{
				FullMethod: "/my_package/MyMethod",
			},
			func(ctx context.Context, req any) (any, error) {
				stored := TokenFromContext(ctx)
				Expect(stored).To(BeNil())
				handled = true
				return nil, nil
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(handled).To(BeTrue())
	})

	It("Ignores non-JWT authorization header for anonymous method", func(ctx context.Context) {
		jwtValidator := NewMockJwtValidator(ctrl)
		interceptor, err := NewGrpcAuthnInterceptor().
			SetLogger(logger).
			SetJwtValidator(jwtValidator).
			AddAnonymousMethodRegex(`^/my_package/.*$`).
			Build()
		Expect(err).ToNot(HaveOccurred())
		// JWE tokens have 5 dot-separated segments, which the JWT parser rejects.
		// Anonymous methods must pass through regardless.
		jweTicket := "header.key.iv.ciphertext.tag"
		ctx = metadata.NewIncomingContext(ctx, metadata.Pairs(Authorization, "Bearer "+jweTicket))
		handled := false
		_, err = interceptor.UnaryServer(
			ctx,
			nil,
			&grpc.UnaryServerInfo{
				FullMethod: "/my_package/MyMethod",
			},
			func(ctx context.Context, req any) (any, error) {
				stored := TokenFromContext(ctx)
				Expect(stored).To(BeNil())
				handled = true
				return nil, nil
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(handled).To(BeTrue())
	})

	It("Combines multiple anonymous method patterns", func(ctx context.Context) {
		jwtValidator := NewMockJwtValidator(ctrl)
		interceptor, err := NewGrpcAuthnInterceptor().
			SetLogger(logger).
			SetJwtValidator(jwtValidator).
			AddAnonymousMethodRegex(`^/my_package/.*$`).
			AddAnonymousMethodRegex(`^/your_package/.*$`).
			Build()
		Expect(err).ToNot(HaveOccurred())
		handled := false
		_, err = interceptor.UnaryServer(
			ctx,
			nil,
			&grpc.UnaryServerInfo{
				FullMethod: "/my_package/MyMethod",
			},
			func(ctx context.Context, req any) (any, error) {
				handled = true
				return nil, nil
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(handled).To(BeTrue())
		handled = false
		_, err = interceptor.UnaryServer(
			ctx,
			nil,
			&grpc.UnaryServerInfo{
				FullMethod: "/your_package/YourMethod",
			},
			func(ctx context.Context, req any) (any, error) {
				handled = true
				return nil, nil
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(handled).To(BeTrue())
	})

	It("Rejects multiple authorization headers", func(ctx context.Context) {
		jwtValidator := NewMockJwtValidator(ctrl)
		interceptor, err := NewGrpcAuthnInterceptor().
			SetLogger(logger).
			SetJwtValidator(jwtValidator).
			Build()
		Expect(err).ToNot(HaveOccurred())
		ctx = metadata.NewIncomingContext(ctx, metadata.Pairs(
			Authorization, "Bearer token1",
			Authorization, "Bearer token2",
		))
		handled := false
		_, err = interceptor.UnaryServer(
			ctx,
			nil,
			&grpc.UnaryServerInfo{
				FullMethod: "/my_package/MyMethod",
			},
			func(ctx context.Context, req any) (any, error) {
				handled = true
				return nil, nil
			},
		)
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.Unauthenticated))
		Expect(status.Message()).To(ContainSubstring("at most one"))
		Expect(handled).To(BeFalse())
	})
})
