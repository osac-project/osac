/*
Copyright (c) 2026 Red Hat Inc.

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
	"maps"
	"time"

	"github.com/golang-jwt/jwt/v5"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"

	"github.com/osac-project/fulfillment-service/internal/collections"
	"github.com/osac-project/fulfillment-service/internal/testing"
	"github.com/osac-project/fulfillment-service/internal/uuid"
)

// mockUserProvisioner is a simple test double for UserProvisioner.
type mockUserProvisioner struct {
	provisionFunc func(ctx context.Context, username, tenant string, claims jwt.MapClaims) error
	callCount     int
	lastUsername  string
	lastTenant    string
	lastClaims    jwt.MapClaims
}

func newMockUserProvisioner() *mockUserProvisioner {
	return &mockUserProvisioner{
		provisionFunc: func(ctx context.Context, username, tenant string, claims jwt.MapClaims) error {
			return nil
		},
	}
}

func (m *mockUserProvisioner) Provision(ctx context.Context, username, tenant string, claims jwt.MapClaims) error {
	m.callCount++
	m.lastUsername = username
	m.lastTenant = tenant
	m.lastClaims = claims
	return m.provisionFunc(ctx, username, tenant, claims)
}

var _ UserProvisioner = (*mockUserProvisioner)(nil)

// mockServerStream is a simple test double for grpc.ServerStream.
type mockServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (m *mockServerStream) Context() context.Context {
	return m.ctx
}

var _ = Describe("JIT Provisioning Interceptor", func() {
	var (
		ctx             context.Context
		interceptor     *GrpcJitProvisioningInterceptor
		mockProvisioner *mockUserProvisioner
		handler         grpc.UnaryHandler
		handlerCalled   bool
	)

	// createKeycloakUserToken creates a token resembling the ones issued by Keycloak for regular users.
	createKeycloakUserToken := func(tenant, name string, overrides jwt.MapClaims) *jwt.Token {
		now := time.Now()
		claims := jwt.MapClaims{
			"auth_time": now.Unix(),
			"azp":       "osac-cli",
			"exp":       now.Add(time.Hour).Unix(),
			"organization": []any{
				tenant,
			},
			"iat":      now.Unix(),
			"iss":      "https://keycloak.keycloak.svc.cluster.local:8000/realms/osac",
			"jti":      uuid.New(),
			"scope":    "openid organization",
			"sid":      uuid.New(),
			"sub":      uuid.New(),
			"typ":      "Bearer",
			"username": name,
		}
		maps.Copy(claims, overrides)
		return testing.MakeTokenObject(nil, claims)
	}

	BeforeEach(func() {
		var err error

		// Create a context:
		ctx = context.Background()

		// Reset handler tracking:
		handlerCalled = false

		// Create a mock user provisioner:
		mockProvisioner = newMockUserProvisioner()

		// Create a mock handler:
		handler = func(ctx context.Context, req any) (any, error) {
			handlerCalled = true
			return nil, nil
		}

		// Create the interceptor with JIT provisioning enabled:
		interceptor, err = NewGrpcJitProvisioningInterceptor().
			SetLogger(logger).
			SetProvisioner(mockProvisioner).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("Builder validation", func() {
		It("Requires a logger", func() {
			_, err := NewGrpcJitProvisioningInterceptor().
				SetProvisioner(mockProvisioner).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("logger is mandatory"))
		})

		It("Allows nil provisioner", func() {
			interceptor, err := NewGrpcJitProvisioningInterceptor().
				SetLogger(logger).
				SetProvisioner(nil).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(interceptor).ToNot(BeNil())
		})
	})

	Describe("User provisioning", func() {
		It("Provisions a new user on first authentication", func() {
			token := createKeycloakUserToken("tenant1", "alice", jwt.MapClaims{
				"email":          "alice@example.com",
				"email_verified": true,
				"name":           "Alice Smith",
			})
			ctx = ContextWithToken(ctx, token)

			// Create subject with single tenant (provisioning should happen)
			subject := &Subject{
				User:    "alice",
				Tenants: collections.NewSet("tenant1"),
			}
			ctx = ContextWithSubject(ctx, subject)

			_, err := interceptor.UnaryServer(
				ctx,
				nil,
				&grpc.UnaryServerInfo{
					FullMethod: "/osac.public.v1.Clusters/List",
				},
				handler,
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(handlerCalled).To(BeTrue())

			// Verify provisioner was called:
			Expect(mockProvisioner.callCount).To(Equal(1))
			Expect(mockProvisioner.lastUsername).To(Equal("alice"))
			Expect(mockProvisioner.lastTenant).To(Equal("tenant1"))
			Expect(mockProvisioner.lastClaims["email"]).To(Equal("alice@example.com"))
			Expect(mockProvisioner.lastClaims["name"]).To(Equal("Alice Smith"))
		})

		It("Does not provision when no provisioner is configured", func() {
			var err error
			interceptor, err = NewGrpcJitProvisioningInterceptor().
				SetLogger(logger).
				Build()
			Expect(err).ToNot(HaveOccurred())

			token := createKeycloakUserToken("tenant1", "bob", nil)
			ctx = ContextWithToken(ctx, token)
			subject := &Subject{
				User:    "bob",
				Tenants: collections.NewSet("tenant1"),
			}
			ctx = ContextWithSubject(ctx, subject)

			_, err = interceptor.UnaryServer(
				ctx,
				nil,
				&grpc.UnaryServerInfo{
					FullMethod: "/osac.public.v1.Clusters/List",
				},
				handler,
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(handlerCalled).To(BeTrue())
		})

		It("Does not provision when subject has no tenants", func() {
			token := createKeycloakUserToken("tenant1", "david", nil)
			ctx = ContextWithToken(ctx, token)
			subject := &Subject{
				User:    "david",
				Tenants: collections.NewSet[string](), // Empty tenant set
			}
			ctx = ContextWithSubject(ctx, subject)

			_, err := interceptor.UnaryServer(
				ctx,
				nil,
				&grpc.UnaryServerInfo{
					FullMethod: "/osac.public.v1.Clusters/List",
				},
				handler,
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(handlerCalled).To(BeTrue())
			Expect(mockProvisioner.callCount).To(Equal(0))
		})

		It("Does not provision when subject has multiple tenants", func() {
			token := createKeycloakUserToken("tenant1", "eve", jwt.MapClaims{
				"organizations": []any{"tenant2", "tenant3"},
			})
			ctx = ContextWithToken(ctx, token)
			subject := &Subject{
				User:    "eve",
				Tenants: collections.NewSet("tenant1", "tenant2", "tenant3"),
			}
			ctx = ContextWithSubject(ctx, subject)

			_, err := interceptor.UnaryServer(
				ctx,
				nil,
				&grpc.UnaryServerInfo{
					FullMethod: "/osac.public.v1.Clusters/List",
				},
				handler,
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(handlerCalled).To(BeTrue())
			Expect(mockProvisioner.callCount).To(Equal(0))
		})

		It("Does not provision when subject has infinite tenants", func() {
			token := createKeycloakUserToken("tenant1", "frank", nil)
			ctx = ContextWithToken(ctx, token)
			subject := &Subject{
				User:    "frank",
				Tenants: AllTenants, // Infinite tenant set
			}
			ctx = ContextWithSubject(ctx, subject)

			_, err := interceptor.UnaryServer(
				ctx,
				nil,
				&grpc.UnaryServerInfo{
					FullMethod: "/osac.public.v1.Clusters/List",
				},
				handler,
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(handlerCalled).To(BeTrue())
			Expect(mockProvisioner.callCount).To(Equal(0))
		})

		It("Does not provision users with system tenant", func() {
			token := createKeycloakUserToken(SystemTenant, "admin", nil)
			ctx = ContextWithToken(ctx, token)
			subject := &Subject{
				User:    "admin",
				Tenants: collections.NewSet(SystemTenant),
			}
			ctx = ContextWithSubject(ctx, subject)

			_, err := interceptor.UnaryServer(
				ctx,
				nil,
				&grpc.UnaryServerInfo{
					FullMethod: "/osac.public.v1.Clusters/List",
				},
				handler,
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(handlerCalled).To(BeTrue())
			Expect(mockProvisioner.callCount).To(Equal(0))
		})

		It("Does not provision users with shared tenant", func() {
			token := createKeycloakUserToken(SharedTenant, "shared-user", nil)
			ctx = ContextWithToken(ctx, token)
			subject := &Subject{
				User:    "shared-user",
				Tenants: collections.NewSet(SharedTenant),
			}
			ctx = ContextWithSubject(ctx, subject)

			_, err := interceptor.UnaryServer(
				ctx,
				nil,
				&grpc.UnaryServerInfo{
					FullMethod: "/osac.public.v1.Clusters/List",
				},
				handler,
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(handlerCalled).To(BeTrue())
			Expect(mockProvisioner.callCount).To(Equal(0))
		})

		It("Does not provision when token is not in context", func() {
			// No token in context
			subject := &Subject{
				User:    "george",
				Tenants: collections.NewSet("tenant1"),
			}
			ctx = ContextWithSubject(ctx, subject)

			_, err := interceptor.UnaryServer(
				ctx,
				nil,
				&grpc.UnaryServerInfo{
					FullMethod: "/osac.public.v1.Clusters/List",
				},
				handler,
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(handlerCalled).To(BeTrue())
			Expect(mockProvisioner.callCount).To(Equal(0))
		})

		It("Does not provision when token has non-MapClaims", func() {
			// Create a token with non-MapClaims type
			token := &jwt.Token{
				Claims: jwt.RegisteredClaims{
					Subject: "harry",
				},
			}
			ctx = ContextWithToken(ctx, token)
			subject := &Subject{
				User:    "harry",
				Tenants: collections.NewSet("tenant1"),
			}
			ctx = ContextWithSubject(ctx, subject)

			_, err := interceptor.UnaryServer(
				ctx,
				nil,
				&grpc.UnaryServerInfo{
					FullMethod: "/osac.public.v1.Clusters/List",
				},
				handler,
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(handlerCalled).To(BeTrue())
			Expect(mockProvisioner.callCount).To(Equal(0))
		})

		It("Fails request when provisioner fails", func() {
			token := createKeycloakUserToken("tenant1", "iris", nil)
			ctx = ContextWithToken(ctx, token)
			subject := &Subject{
				User:    "iris",
				Tenants: collections.NewSet("tenant1"),
			}
			ctx = ContextWithSubject(ctx, subject)

			// Configure provisioner to return error:
			mockProvisioner.provisionFunc = func(ctx context.Context, username, tenant string, claims jwt.MapClaims) error {
				return errors.New("database connection failed")
			}

			_, err := interceptor.UnaryServer(
				ctx,
				nil,
				&grpc.UnaryServerInfo{
					FullMethod: "/osac.public.v1.Clusters/List",
				},
				handler,
			)
			// Provisioning failure should block the request
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("user provisioning failed"))
			Expect(err.Error()).To(ContainSubstring("database connection failed"))
			Expect(handlerCalled).To(BeFalse())
			// Verify provisioner was called
			Expect(mockProvisioner.callCount).To(Equal(1))
		})

		It("Passes correct claims to provisioner", func() {
			token := createKeycloakUserToken("tenant1", "judy", jwt.MapClaims{
				"email":          "judy@example.com",
				"email_verified": true,
				"name":           "Judy Lee",
				"custom_field":   "custom_value",
			})
			ctx = ContextWithToken(ctx, token)
			subject := &Subject{
				User:    "judy",
				Tenants: collections.NewSet("tenant1"),
			}
			ctx = ContextWithSubject(ctx, subject)

			_, err := interceptor.UnaryServer(
				ctx,
				nil,
				&grpc.UnaryServerInfo{
					FullMethod: "/osac.public.v1.Clusters/List",
				},
				handler,
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(handlerCalled).To(BeTrue())

			// Verify all claims were passed:
			Expect(mockProvisioner.callCount).To(Equal(1))
			Expect(mockProvisioner.lastUsername).To(Equal("judy"))
			Expect(mockProvisioner.lastTenant).To(Equal("tenant1"))
			Expect(mockProvisioner.lastClaims["email"]).To(Equal("judy@example.com"))
			Expect(mockProvisioner.lastClaims["email_verified"]).To(BeTrue())
			Expect(mockProvisioner.lastClaims["name"]).To(Equal("Judy Lee"))
			Expect(mockProvisioner.lastClaims["custom_field"]).To(Equal("custom_value"))
			Expect(mockProvisioner.lastClaims["username"]).To(Equal("judy"))
		})

		It("Invokes handler even when provisioning is skipped", func() {
			token := createKeycloakUserToken("tenant1", "karl", nil)
			ctx = ContextWithToken(ctx, token)
			subject := &Subject{
				User:    "karl",
				Tenants: collections.NewSet("tenant1", "tenant2"), // Multiple tenants
			}
			ctx = ContextWithSubject(ctx, subject)

			_, err := interceptor.UnaryServer(
				ctx,
				nil,
				&grpc.UnaryServerInfo{
					FullMethod: "/osac.public.v1.Clusters/List",
				},
				handler,
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(handlerCalled).To(BeTrue())
			Expect(mockProvisioner.callCount).To(Equal(0))
		})

		Describe("Stream provisioning", func() {
			var (
				streamHandler       grpc.StreamHandler
				streamHandlerCalled bool
			)

			BeforeEach(func() {
				streamHandlerCalled = false
				streamHandler = func(srv any, stream grpc.ServerStream) error {
					streamHandlerCalled = true
					return nil
				}
			})

			It("Provisions a new user on first stream authentication", func() {
				token := createKeycloakUserToken("tenant1", "alice-stream", jwt.MapClaims{
					"email":          "alice-stream@example.com",
					"email_verified": true,
					"name":           "Alice Stream",
				})
				ctx = ContextWithToken(ctx, token)

				subject := &Subject{
					User:    "alice-stream",
					Tenants: collections.NewSet("tenant1"),
				}
				ctx = ContextWithSubject(ctx, subject)

				stream := &mockServerStream{ctx: ctx}
				err := interceptor.StreamServer(
					nil,
					stream,
					&grpc.StreamServerInfo{
						FullMethod: "/osac.public.v1.Clusters/Watch",
					},
					streamHandler,
				)
				Expect(err).ToNot(HaveOccurred())
				Expect(streamHandlerCalled).To(BeTrue())

				// Verify provisioner was called:
				Expect(mockProvisioner.callCount).To(Equal(1))
				Expect(mockProvisioner.lastUsername).To(Equal("alice-stream"))
				Expect(mockProvisioner.lastTenant).To(Equal("tenant1"))
			})

			It("Fails stream request when provisioner fails", func() {
				token := createKeycloakUserToken("tenant1", "bob-stream", nil)
				ctx = ContextWithToken(ctx, token)
				subject := &Subject{
					User:    "bob-stream",
					Tenants: collections.NewSet("tenant1"),
				}
				ctx = ContextWithSubject(ctx, subject)

				// Configure provisioner to return error:
				mockProvisioner.provisionFunc = func(ctx context.Context, username, tenant string, claims jwt.MapClaims) error {
					return errors.New("database unavailable")
				}

				stream := &mockServerStream{ctx: ctx}
				err := interceptor.StreamServer(
					nil,
					stream,
					&grpc.StreamServerInfo{
						FullMethod: "/osac.public.v1.Clusters/Watch",
					},
					streamHandler,
				)
				// Provisioning failure should block the stream
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("user provisioning failed"))
				Expect(err.Error()).To(ContainSubstring("database unavailable"))
				Expect(streamHandlerCalled).To(BeFalse())
				Expect(mockProvisioner.callCount).To(Equal(1))
			})

			It("Does not provision when subject has multiple tenants", func() {
				token := createKeycloakUserToken("tenant1", "charlie-stream", nil)
				ctx = ContextWithToken(ctx, token)
				subject := &Subject{
					User:    "charlie-stream",
					Tenants: collections.NewSet("tenant1", "tenant2"),
				}
				ctx = ContextWithSubject(ctx, subject)

				stream := &mockServerStream{ctx: ctx}
				err := interceptor.StreamServer(
					nil,
					stream,
					&grpc.StreamServerInfo{
						FullMethod: "/osac.public.v1.Clusters/Watch",
					},
					streamHandler,
				)
				Expect(err).ToNot(HaveOccurred())
				Expect(streamHandlerCalled).To(BeTrue())
				Expect(mockProvisioner.callCount).To(Equal(0))
			})
		})
	})
})
