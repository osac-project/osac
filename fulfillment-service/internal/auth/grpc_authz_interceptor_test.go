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
	"fmt"
	"maps"
	"time"

	"github.com/golang-jwt/jwt/v5"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/testing"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
)

var _ = Describe("Rego authorization interceptor", func() {
	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			interceptor, err := NewGrpcAuthzInterceptor().
				SetLogger(logger).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(interceptor).ToNot(BeNil())
		})

		It("Can't be built without a logger", func() {
			interceptor, err := NewGrpcAuthzInterceptor().
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("logger is mandatory"))
			Expect(interceptor).To(BeNil())
		})

		It("Rejects empty emergency service account name", func() {
			_, err := NewGrpcAuthzInterceptor().
				SetLogger(logger).
				AddEmergencyServiceAccounts("").
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not a valid Kubernetes service account name"))
		})

		It("Rejects whitespace-only emergency service account name", func() {
			_, err := NewGrpcAuthzInterceptor().
				SetLogger(logger).
				AddEmergencyServiceAccounts("  ").
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not a valid Kubernetes service account name"))
		})

		It("Rejects emergency service account name with colon", func() {
			_, err := NewGrpcAuthzInterceptor().
				SetLogger(logger).
				AddEmergencyServiceAccounts("system:admin").
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not a valid Kubernetes service account name"))
		})

		It("Rejects emergency service account name with uppercase", func() {
			_, err := NewGrpcAuthzInterceptor().
				SetLogger(logger).
				AddEmergencyServiceAccounts("Admin").
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not a valid Kubernetes service account name"))
		})

		It("Rejects emergency service account name starting with hyphen", func() {
			_, err := NewGrpcAuthzInterceptor().
				SetLogger(logger).
				AddEmergencyServiceAccounts("-admin").
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not a valid Kubernetes service account name"))
		})
	})

	Describe("Permission checks", func() {
		var interceptor *GrpcAuthzInterceptor

		// createKubernetesToken creates a token resembling the ones issued by the Kubernetes service account
		// token issuer.
		createKubernetesToken := func(namespace, name string, overrides jwt.MapClaims) *jwt.Token {
			now := time.Now()
			claims := jwt.MapClaims{
				"aud": []any{
					"https://kubernetes.default.svc.cluster.local",
				},
				"exp": now.Add(time.Hour).Unix(),
				"iat": now.Unix(),
				"iss": "https://kubernetes.default.svc.cluster.local",
				"jti": uuid.New(),
				"nbf": now.Unix(),
				"sub": fmt.Sprintf("system:serviceaccount:%s:%s", namespace, name),
				"kubernetes.io": map[string]any{
					"namespace": namespace,
					"serviceaccount": map[string]any{
						"name": name,
						"uid":  uuid.New(),
					},
				},
			}
			maps.Copy(claims, overrides)
			return testing.MakeTokenObject(nil, claims)
		}

		// createKeycloakServiceAccountToken creates a token resembling the ones issued by the Keycloak for
		// service accounts.
		createKeycloakServiceAccountToken := func(name string, overrides jwt.MapClaims) *jwt.Token {
			now := time.Now()
			claims := jwt.MapClaims{
				"acr":            "1",
				"azp":            name,
				"email_verified": true,
				"exp":            now.Add(time.Hour).Unix(),
				"iat":            now.Unix(),
				"iss":            "https://keycloak.keycloak.svc.cluster.local:8000/realms/osac",
				"jti":            uuid.New(),
				"scope":          "openid email profile",
				"sub":            uuid.New(),
				"typ":            "Bearer",
				"username":       fmt.Sprintf("service-account-%s", name),
			}
			maps.Copy(claims, overrides)
			return testing.MakeTokenObject(nil, claims)
		}

		// createKeycloakUserToken creates a token resembling the ones issued by the Keycloak for regular users.
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

			// Create the interceptor:
			interceptor, err = NewGrpcAuthzInterceptor().
				SetLogger(logger).
				AddAnonymousMethodRegex(`^/grpc\.health\.v1\.Health/.*$`).
				AddAnonymousMethodRegex(`^/grpc\.reflection\.v1\.ServerReflection/.*$`).
				AddAnonymousMethodRegex(`^/osac\.public\.v1\.Capabilities/.*$`).
				AddEmergencyServiceAccounts(
					"admin",
					"osac-operator",
					"osac-operator-controller-manager",
					"template-publisher",
				).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Allows guest on the anonymous part of the public API", func(ctx context.Context) {
			handled := false
			_, err := interceptor.UnaryServer(
				ctx,
				nil,
				&grpc.UnaryServerInfo{
					FullMethod: "/osac.public.v1.Capabilities/Get",
				},
				func(ctx context.Context, req any) (any, error) {
					subject := SubjectFromContext(ctx)
					Expect(subject).To(Equal(Guest))
					handled = true
					return nil, nil
				},
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(handled).To(BeTrue())
		})

		It("Allows guest on the reflection API", func(ctx context.Context) {
			handled := false
			_, err := interceptor.UnaryServer(
				ctx,
				nil,
				&grpc.UnaryServerInfo{
					FullMethod: "/grpc.reflection.v1.ServerReflection/ServerReflectionInfo",
				},
				func(ctx context.Context, req any) (any, error) {
					subject := SubjectFromContext(ctx)
					Expect(subject).To(Equal(Guest))
					handled = true
					return nil, nil
				},
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(handled).To(BeTrue())
		})

		It("Allows guest on the health check API", func(ctx context.Context) {
			handled := false
			_, err := interceptor.UnaryServer(
				ctx,
				nil,
				&grpc.UnaryServerInfo{
					FullMethod: "/grpc.health.v1.Health/Check",
				},
				func(ctx context.Context, req any) (any, error) {
					subject := SubjectFromContext(ctx)
					Expect(subject).To(Equal(Guest))
					handled = true
					return nil, nil
				},
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(handled).To(BeTrue())
		})

		It("Denies guest on the private API", func(ctx context.Context) {
			handled := false
			_, err := interceptor.UnaryServer(
				ctx,
				nil,
				&grpc.UnaryServerInfo{
					FullMethod: "/osac.private.v1.Hubs/Create",
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
			Expect(status.Message()).To(Equal(
				"method '/osac.private.v1.Hubs/Create' requires authentication",
			))
			Expect(handled).To(BeFalse())
		})

		DescribeTable(
			"Allows selected Kubernetes service accounts on the private API",
			func(ctx context.Context, namespace, name string) {
				token := createKubernetesToken(namespace, name, nil)
				ctx = ContextWithToken(ctx, token)
				handled := false
				_, err := interceptor.UnaryServer(
					ctx,
					nil,
					&grpc.UnaryServerInfo{
						FullMethod: "/osac.private.v1.Hubs/Create",
					},
					func(ctx context.Context, req any) (any, error) {
						subject := SubjectFromContext(ctx)
						Expect(subject.User).To(Equal(name))
						Expect(subject.Tenants.Universal()).To(BeTrue())
						handled = true
						return nil, nil
					},
				)
				Expect(err).ToNot(HaveOccurred())
				Expect(handled).To(BeTrue())
			},
			Entry(
				"Administrator",
				"osac", "admin",
			),
			Entry(
				"Template publisher",
				"osac", "template-publisher",
			),
			Entry(
				"Controller manager",
				"osac", "osac-operator",
			),
			Entry(
				"Alternative controller manager",
				"osac", "osac-operator-controller-manager",
			),
		)

		DescribeTable(
			"Allows selected Kubernetes service accounts on the public API",
			func(ctx context.Context, namespace, name string) {
				token := createKubernetesToken(namespace, name, nil)
				ctx = ContextWithToken(ctx, token)
				handled := false
				_, err := interceptor.UnaryServer(
					ctx,
					nil,
					&grpc.UnaryServerInfo{
						FullMethod: "/osac.public.v1.Clusters/Create",
					},
					func(ctx context.Context, req any) (any, error) {
						subject := SubjectFromContext(ctx)
						Expect(subject.User).To(Equal(name))
						Expect(subject.Tenants.Universal()).To(BeTrue())
						handled = true
						return nil, nil
					},
				)
				Expect(err).ToNot(HaveOccurred())
				Expect(handled).To(BeTrue())
			},
			Entry(
				"Administrator",
				"osac", "admin",
			),
			Entry(
				"Template publisher",
				"osac", "template-publisher",
			),
			Entry(
				"Controller manager",
				"osac", "osac-operator-controller-manager",
			),
		)

		DescribeTable(
			"Denies other Kubernetes service accounts on the private API",
			func(ctx context.Context, namespace, name string) {
				token := createKubernetesToken(namespace, name, nil)
				ctx = ContextWithToken(ctx, token)
				handled := false
				_, err := interceptor.UnaryServer(
					ctx,
					nil,
					&grpc.UnaryServerInfo{
						FullMethod: "/osac.private.v1.Hubs/Create",
					},
					func(ctx context.Context, req any) (any, error) {
						handled = true
						return nil, nil
					},
				)
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.PermissionDenied))
				Expect(status.Message()).To(Equal("permission denied"))
				Expect(handled).To(BeFalse())
			},
			Entry(
				"Administrator, but wrong namespace",
				"junk", "admin",
			),
			Entry(
				"Template publisher, but wrong name",
				"junk", "template-publisher",
			),
			Entry(
				"Controller manager, but wrong namespace",
				"junk", "osac-operator-controller-manager",
			),
			Entry(
				"Right namespace, but wrong name",
				"osac", "junk",
			),
		)

		DescribeTable(
			"Allows other Kubernetes service accounts on the public API",
			func(ctx context.Context, namespace, name string) {
				token := createKubernetesToken(namespace, name, nil)
				ctx = ContextWithToken(ctx, token)
				handled := false
				_, err := interceptor.UnaryServer(
					ctx,
					nil,
					&grpc.UnaryServerInfo{
						FullMethod: "/osac.public.v1.Clusters/Create",
					},
					func(ctx context.Context, req any) (any, error) {
						subject := SubjectFromContext(ctx)
						Expect(subject.User).To(Equal(name))
						Expect(subject.Tenants.Finite()).To(BeTrue())
						Expect(subject.Tenants.Inclusions()).To(ConsistOf(namespace))
						handled = true
						return nil, nil
					},
				)
				Expect(err).ToNot(HaveOccurred())
				Expect(handled).To(BeTrue())
			},
			Entry(
				"Service account in a different namesspace",
				"my-tenant", "my-sa",
			),
		)

		DescribeTable(
			"Allows selected Keycloak service accounts on the private API",
			func(ctx context.Context, name string) {
				token := createKeycloakServiceAccountToken(name, nil)
				ctx = ContextWithToken(ctx, token)
				handled := false
				_, err := interceptor.UnaryServer(
					ctx,
					nil,
					&grpc.UnaryServerInfo{
						FullMethod: "/osac.private.v1.Hubs/Create",
					},
					func(ctx context.Context, req any) (any, error) {
						subject := SubjectFromContext(ctx)
						Expect(subject.User).To(Equal(fmt.Sprintf("service-account-%s", name)))
						Expect(subject.Tenants.Universal()).To(BeTrue())
						handled = true
						return nil, nil
					},
				)
				Expect(err).ToNot(HaveOccurred())
				Expect(handled).To(BeTrue())
			},
			Entry(
				"Administrator",
				"osac-admin",
			),
			Entry(
				"Controller",
				"osac-controller",
			),
		)

		DescribeTable(
			"Allows selected Keycloak service accounts on the public API",
			func(ctx context.Context, name string) {
				token := createKeycloakServiceAccountToken(name, nil)
				ctx = ContextWithToken(ctx, token)
				handled := false
				_, err := interceptor.UnaryServer(
					ctx,
					nil,
					&grpc.UnaryServerInfo{
						FullMethod: "/osac.public.v1.Clusters/Create",
					},
					func(ctx context.Context, req any) (any, error) {
						subject := SubjectFromContext(ctx)
						Expect(subject.User).To(Equal(fmt.Sprintf("service-account-%s", name)))
						Expect(subject.Tenants.Universal()).To(BeTrue())
						handled = true
						return nil, nil
					},
				)
				Expect(err).ToNot(HaveOccurred())
				Expect(handled).To(BeTrue())
			},
			Entry(
				"Administrator",
				"osac-admin",
			),
			Entry(
				"Controller",
				"osac-controller",
			),
		)

		DescribeTable(
			"Allows other Keycloak service accounts on the public API",
			func(ctx context.Context, name string) {
				token := createKeycloakServiceAccountToken(name, nil)
				ctx = ContextWithToken(ctx, token)
				handled := false
				_, err := interceptor.UnaryServer(
					ctx,
					nil,
					&grpc.UnaryServerInfo{
						FullMethod: "/osac.public.v1.Clusters/Create",
					},
					func(ctx context.Context, req any) (any, error) {
						subject := SubjectFromContext(ctx)
						Expect(subject.User).To(Equal(fmt.Sprintf("service-account-%s", name)))
						Expect(subject.Tenants.Empty()).To(BeTrue())
						handled = true
						return nil, nil
					},
				)
				Expect(err).ToNot(HaveOccurred())
				Expect(handled).To(BeTrue())
			},
			Entry(
				"Random service account",
				"my-sa",
			),
		)

		DescribeTable(
			"Denies other Keycloak service accounts on the private API",
			func(ctx context.Context, name string) {
				token := createKeycloakServiceAccountToken(name, nil)
				ctx = ContextWithToken(ctx, token)
				handled := false
				_, err := interceptor.UnaryServer(
					ctx,
					nil,
					&grpc.UnaryServerInfo{
						FullMethod: "/osac.private.v1.Hubs/Create",
					},
					func(ctx context.Context, req any) (any, error) {
						handled = true
						return nil, nil
					},
				)
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.PermissionDenied))
				Expect(status.Message()).To(Equal("permission denied"))
				Expect(handled).To(BeFalse())
			},
			Entry(
				"Random service account",
				"my-sa",
			),
		)

		DescribeTable(
			"Allows Keycloak users on the public API",
			func(ctx context.Context, tenant, name string) {
				token := createKeycloakUserToken(tenant, name, nil)
				ctx = ContextWithToken(ctx, token)
				handled := false
				_, err := interceptor.UnaryServer(
					ctx,
					nil,
					&grpc.UnaryServerInfo{
						FullMethod: "/osac.public.v1.Clusters/Create",
					},
					func(ctx context.Context, req any) (any, error) {
						subject := SubjectFromContext(ctx)
						Expect(subject.User).To(Equal(name))
						Expect(subject.Tenants.Finite()).To(BeTrue())
						Expect(subject.Tenants.Inclusions()).To(ConsistOf("my-tenant"))
						handled = true
						return nil, nil
					},
				)
				Expect(err).ToNot(HaveOccurred())
				Expect(handled).To(BeTrue())
			},
			Entry(
				"Random user",
				"my-tenant", "my-user",
			),
		)

		DescribeTable(
			"Denies Keycloak users on the private API",
			func(ctx context.Context, tenant, name string) {
				token := createKeycloakUserToken(tenant, name, nil)
				ctx = ContextWithToken(ctx, token)
				handled := false
				_, err := interceptor.UnaryServer(
					ctx,
					nil,
					&grpc.UnaryServerInfo{
						FullMethod: "/osac.private.v1.Hubs/Create",
					},
					func(ctx context.Context, req any) (any, error) {
						handled = true
						return nil, nil
					},
				)
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.PermissionDenied))
				Expect(status.Message()).To(Equal("permission denied"))
				Expect(handled).To(BeFalse())
			},
			Entry(
				"Random user",
				"my-tenant", "my-user",
			),
		)

		DescribeTable(
			"Allows Keycloak users on the public Secrets API",
			func(ctx context.Context, method string) {
				token := createKeycloakUserToken("my-tenant", "my-user", nil)
				ctx = ContextWithToken(ctx, token)
				handled := false
				_, err := interceptor.UnaryServer(
					ctx,
					nil,
					&grpc.UnaryServerInfo{
						FullMethod: method,
					},
					func(ctx context.Context, req any) (any, error) {
						subject := SubjectFromContext(ctx)
						Expect(subject.User).To(Equal("my-user"))
						Expect(subject.Tenants.Finite()).To(BeTrue())
						Expect(subject.Tenants.Inclusions()).To(ConsistOf("my-tenant"))
						handled = true
						return nil, nil
					},
				)
				Expect(err).ToNot(HaveOccurred())
				Expect(handled).To(BeTrue())
			},
			Entry("List", "/osac.public.v1.Secrets/List"),
			Entry("Get", "/osac.public.v1.Secrets/Get"),
			Entry("Create", "/osac.public.v1.Secrets/Create"),
			Entry("Update", "/osac.public.v1.Secrets/Update"),
			Entry("Delete", "/osac.public.v1.Secrets/Delete"),
		)

		DescribeTable(
			"Denies Keycloak users on the private Secrets API",
			func(ctx context.Context, method string) {
				token := createKeycloakUserToken("my-tenant", "my-user", nil)
				ctx = ContextWithToken(ctx, token)
				handled := false
				_, err := interceptor.UnaryServer(
					ctx,
					nil,
					&grpc.UnaryServerInfo{
						FullMethod: method,
					},
					func(ctx context.Context, req any) (any, error) {
						handled = true
						return nil, nil
					},
				)
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.PermissionDenied))
				Expect(status.Message()).To(Equal("permission denied"))
				Expect(handled).To(BeFalse())
			},
			Entry("List", "/osac.private.v1.Secrets/List"),
			Entry("Get", "/osac.private.v1.Secrets/Get"),
			Entry("Create", "/osac.private.v1.Secrets/Create"),
			Entry("Update", "/osac.private.v1.Secrets/Update"),
			Entry("Delete", "/osac.private.v1.Secrets/Delete"),
			Entry("Signal", "/osac.private.v1.Secrets/Signal"),
		)

		It("Allows tenant admin to manage users", func(ctx context.Context) {
			token := createKeycloakUserToken("my-tenant", "my-user", jwt.MapClaims{
				"realm_access": map[string]any{
					"roles": []any{
						"tenant-admin",
					},
				},
			})
			ctx = ContextWithToken(ctx, token)
			handled := false
			_, err := interceptor.UnaryServer(
				ctx,
				nil,
				&grpc.UnaryServerInfo{
					FullMethod: "/osac.public.v1.Users/Create",
				},
				func(ctx context.Context, req any) (any, error) {
					subject := SubjectFromContext(ctx)
					Expect(subject.User).To(Equal("my-user"))
					Expect(subject.Tenants.Finite()).To(BeTrue())
					Expect(subject.Tenants.Inclusions()).To(ConsistOf("my-tenant"))
					handled = true
					return nil, nil
				},
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(handled).To(BeTrue())
		})

		It("Denies regular user from managing users", func(ctx context.Context) {
			token := createKeycloakUserToken("my-tenant", "my-user", jwt.MapClaims{
				"realm_access": map[string]any{
					"roles": []any{},
				},
			})
			ctx = ContextWithToken(ctx, token)
			handled := false
			_, err := interceptor.UnaryServer(
				ctx,
				nil,
				&grpc.UnaryServerInfo{
					FullMethod: "/osac.public.v1.Users/Create",
				},
				func(ctx context.Context, req any) (any, error) {
					handled = true
					return nil, nil
				},
			)
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.PermissionDenied))
			Expect(status.Message()).To(Equal("permission denied"))
			Expect(handled).To(BeFalse())
		})

		It("Grants admin privileges when groups include the admins group", func(ctx context.Context) {
			token := createKeycloakUserToken("", "my-user", jwt.MapClaims{
				"organization": nil,
				"groups": []any{
					"admins",
				},
			})
			ctx = ContextWithToken(ctx, token)
			handled := false
			_, err := interceptor.UnaryServer(
				ctx,
				nil,
				&grpc.UnaryServerInfo{
					FullMethod: "/osac.public.v1.Users/Create",
				},
				func(ctx context.Context, req any) (any, error) {
					subject := SubjectFromContext(ctx)
					Expect(subject.User).To(Equal("my-user"))
					Expect(subject.Tenants.Universal()).To(BeTrue())
					handled = true
					return nil, nil
				},
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(handled).To(BeTrue())
		})

		It("Allows tenant admin to manage project memberships", func(ctx context.Context) {
			token := createKeycloakUserToken("my-tenant", "my-user", jwt.MapClaims{
				"realm_access": map[string]any{
					"roles": []any{
						"tenant-admin",
					},
				},
			})
			ctx = ContextWithToken(ctx, token)
			handled := false
			_, err := interceptor.UnaryServer(
				ctx,
				nil,
				&grpc.UnaryServerInfo{
					FullMethod: "/osac.public.v1.ProjectMemberships/Create",
				},
				func(ctx context.Context, req any) (any, error) {
					subject := SubjectFromContext(ctx)
					Expect(subject.User).To(Equal("my-user"))
					Expect(subject.Tenants.Finite()).To(BeTrue())
					Expect(subject.Tenants.Inclusions()).To(ConsistOf("my-tenant"))
					handled = true
					return nil, nil
				},
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(handled).To(BeTrue())
		})

		It("Denies regular user from creating project memberships", func(ctx context.Context) {
			token := createKeycloakUserToken("my-tenant", "my-user", jwt.MapClaims{
				"realm_access": map[string]any{
					"roles": []any{},
				},
			})
			ctx = ContextWithToken(ctx, token)
			handled := false
			_, err := interceptor.UnaryServer(
				ctx,
				nil,
				&grpc.UnaryServerInfo{
					FullMethod: "/osac.public.v1.ProjectMemberships/Create",
				},
				func(ctx context.Context, req any) (any, error) {
					handled = true
					return nil, nil
				},
			)
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.PermissionDenied))
			Expect(handled).To(BeFalse())
		})

		It("Allows regular user to list project memberships", func(ctx context.Context) {
			token := createKeycloakUserToken("my-tenant", "my-user", jwt.MapClaims{})
			ctx = ContextWithToken(ctx, token)
			handled := false
			_, err := interceptor.UnaryServer(
				ctx,
				nil,
				&grpc.UnaryServerInfo{
					FullMethod: "/osac.public.v1.ProjectMemberships/List",
				},
				func(ctx context.Context, req any) (any, error) {
					handled = true
					return nil, nil
				},
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(handled).To(BeTrue())
		})

		It("Allows project manager to create project memberships", func(ctx context.Context) {
			token := createKeycloakUserToken("", "my-user", jwt.MapClaims{
				"organization": map[string]any{
					"my-tenant": map[string]any{
						"groups": []any{
							"/my-project/system:managers",
						},
					},
				},
			})
			ctx = ContextWithToken(ctx, token)
			handled := false
			_, err := interceptor.UnaryServer(
				ctx,
				publicv1.ProjectMembershipsCreateRequest_builder{
					Object: publicv1.ProjectMembership_builder{
						Metadata: publicv1.Metadata_builder{
							Project: "my-project",
						}.Build(),
					}.Build(),
				}.Build(),
				&grpc.UnaryServerInfo{
					FullMethod: "/osac.public.v1.ProjectMemberships/Create",
				},
				func(ctx context.Context, req any) (any, error) {
					handled = true
					return nil, nil
				},
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(handled).To(BeTrue())
		})

		It("Allows project manager to get project memberships", func(ctx context.Context) {
			pmInterceptor, err := NewGrpcAuthzInterceptor().
				SetLogger(logger).
				SetProjectMembershipMetadataFetcher(func(ctx context.Context, id string) *ObjectMetadata {
					Expect(id).To(Equal("pm-100"))
					return &ObjectMetadata{Tenant: "my-tenant", Project: "my-project"}
				}).
				Build()
			Expect(err).ToNot(HaveOccurred())

			token := createKeycloakUserToken("", "my-user", jwt.MapClaims{
				"organization": map[string]any{
					"my-tenant": map[string]any{
						"groups": []any{
							"/my-project/system:managers",
						},
					},
				},
			})
			ctx = ContextWithToken(ctx, token)
			handled := false
			_, err = pmInterceptor.UnaryServer(
				ctx,
				publicv1.ProjectMembershipsGetRequest_builder{
					Id: "pm-100",
				}.Build(),
				&grpc.UnaryServerInfo{
					FullMethod: "/osac.public.v1.ProjectMemberships/Get",
				},
				func(ctx context.Context, req any) (any, error) {
					handled = true
					return nil, nil
				},
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(handled).To(BeTrue())
		})

		It("Allows project manager to update project memberships", func(ctx context.Context) {
			pmInterceptor, err := NewGrpcAuthzInterceptor().
				SetLogger(logger).
				SetProjectMembershipMetadataFetcher(func(ctx context.Context, id string) *ObjectMetadata {
					Expect(id).To(Equal("pm-200"))
					return &ObjectMetadata{Tenant: "my-tenant", Project: "my-project"}
				}).
				Build()
			Expect(err).ToNot(HaveOccurred())

			token := createKeycloakUserToken("", "my-user", jwt.MapClaims{
				"organization": map[string]any{
					"my-tenant": map[string]any{
						"groups": []any{
							"/my-project/system:managers",
						},
					},
				},
			})
			ctx = ContextWithToken(ctx, token)
			handled := false
			_, err = pmInterceptor.UnaryServer(
				ctx,
				publicv1.ProjectMembershipsUpdateRequest_builder{
					Object: publicv1.ProjectMembership_builder{
						Id: "pm-200",
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{},
				}.Build(),
				&grpc.UnaryServerInfo{
					FullMethod: "/osac.public.v1.ProjectMemberships/Update",
				},
				func(ctx context.Context, req any) (any, error) {
					handled = true
					return nil, nil
				},
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(handled).To(BeTrue())
		})

		It("Allows project manager to delete project memberships", func(ctx context.Context) {
			pmInterceptor, err := NewGrpcAuthzInterceptor().
				SetLogger(logger).
				SetProjectMembershipMetadataFetcher(func(ctx context.Context, id string) *ObjectMetadata {
					Expect(id).To(Equal("pm-300"))
					return &ObjectMetadata{Tenant: "my-tenant", Project: "my-project"}
				}).
				Build()
			Expect(err).ToNot(HaveOccurred())

			token := createKeycloakUserToken("", "my-user", jwt.MapClaims{
				"organization": map[string]any{
					"my-tenant": map[string]any{
						"groups": []any{
							"/my-project/system:managers",
						},
					},
				},
			})
			ctx = ContextWithToken(ctx, token)
			handled := false
			_, err = pmInterceptor.UnaryServer(
				ctx,
				publicv1.ProjectMembershipsDeleteRequest_builder{
					Id: "pm-300",
				}.Build(),
				&grpc.UnaryServerInfo{
					FullMethod: "/osac.public.v1.ProjectMemberships/Delete",
				},
				func(ctx context.Context, req any) (any, error) {
					handled = true
					return nil, nil
				},
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(handled).To(BeTrue())
		})

		DescribeTable(
			"Allows tenant admin to manage catalog items",
			func(ctx context.Context, method string) {
				token := createKeycloakUserToken("my-tenant", "my-user", jwt.MapClaims{
					"realm_access": map[string]any{
						"roles": []any{
							"tenant-admin",
						},
					},
				})
				ctx = ContextWithToken(ctx, token)
				handled := false
				_, err := interceptor.UnaryServer(
					ctx,
					nil,
					&grpc.UnaryServerInfo{
						FullMethod: method,
					},
					func(ctx context.Context, req any) (any, error) {
						subject := SubjectFromContext(ctx)
						Expect(subject.User).To(Equal("my-user"))
						Expect(subject.Tenants.Finite()).To(BeTrue())
						Expect(subject.Tenants.Inclusions()).To(ConsistOf("my-tenant"))
						handled = true
						return nil, nil
					},
				)
				Expect(err).ToNot(HaveOccurred())
				Expect(handled).To(BeTrue())
			},
			Entry("BareMetalInstanceCatalogItems/Create",
				"/osac.public.v1.BareMetalInstanceCatalogItems/Create"),
			Entry("BareMetalInstanceCatalogItems/Update",
				"/osac.public.v1.BareMetalInstanceCatalogItems/Update"),
			Entry("BareMetalInstanceCatalogItems/Delete",
				"/osac.public.v1.BareMetalInstanceCatalogItems/Delete"),
			Entry("ClusterCatalogItems/Create",
				"/osac.public.v1.ClusterCatalogItems/Create"),
			Entry("ClusterCatalogItems/Update",
				"/osac.public.v1.ClusterCatalogItems/Update"),
			Entry("ClusterCatalogItems/Delete",
				"/osac.public.v1.ClusterCatalogItems/Delete"),
			Entry("ComputeInstanceCatalogItems/Create",
				"/osac.public.v1.ComputeInstanceCatalogItems/Create"),
			Entry("ComputeInstanceCatalogItems/Update",
				"/osac.public.v1.ComputeInstanceCatalogItems/Update"),
			Entry("ComputeInstanceCatalogItems/Delete",
				"/osac.public.v1.ComputeInstanceCatalogItems/Delete"),
		)

		DescribeTable(
			"Denies regular user from managing catalog items",
			func(ctx context.Context, method string) {
				token := createKeycloakUserToken("my-tenant", "my-user", jwt.MapClaims{
					"realm_access": map[string]any{
						"roles": []any{},
					},
				})
				ctx = ContextWithToken(ctx, token)
				handled := false
				_, err := interceptor.UnaryServer(
					ctx,
					nil,
					&grpc.UnaryServerInfo{
						FullMethod: method,
					},
					func(ctx context.Context, req any) (any, error) {
						handled = true
						return nil, nil
					},
				)
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.PermissionDenied))
				Expect(status.Message()).To(Equal("permission denied"))
				Expect(handled).To(BeFalse())
			},
			Entry("BareMetalInstanceCatalogItems/Create",
				"/osac.public.v1.BareMetalInstanceCatalogItems/Create"),
			Entry("BareMetalInstanceCatalogItems/Update",
				"/osac.public.v1.BareMetalInstanceCatalogItems/Update"),
			Entry("BareMetalInstanceCatalogItems/Delete",
				"/osac.public.v1.BareMetalInstanceCatalogItems/Delete"),
			Entry("ClusterCatalogItems/Create",
				"/osac.public.v1.ClusterCatalogItems/Create"),
			Entry("ClusterCatalogItems/Update",
				"/osac.public.v1.ClusterCatalogItems/Update"),
			Entry("ClusterCatalogItems/Delete",
				"/osac.public.v1.ClusterCatalogItems/Delete"),
			Entry("ComputeInstanceCatalogItems/Create",
				"/osac.public.v1.ComputeInstanceCatalogItems/Create"),
			Entry("ComputeInstanceCatalogItems/Update",
				"/osac.public.v1.ComputeInstanceCatalogItems/Update"),
			Entry("ComputeInstanceCatalogItems/Delete",
				"/osac.public.v1.ComputeInstanceCatalogItems/Delete"),
		)

		It("Grants admin via groups access to private API", func(ctx context.Context) {
			token := createKeycloakUserToken("", "my-user", jwt.MapClaims{
				"organization": nil,
				"groups": []any{
					"admins",
				},
			})
			ctx = ContextWithToken(ctx, token)
			handled := false
			_, err := interceptor.UnaryServer(
				ctx,
				nil,
				&grpc.UnaryServerInfo{
					FullMethod: "/osac.private.v1.Hubs/Create",
				},
				func(ctx context.Context, req any) (any, error) {
					subject := SubjectFromContext(ctx)
					Expect(subject.Tenants.Universal()).To(BeTrue())
					handled = true
					return nil, nil
				},
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(handled).To(BeTrue())
		})

		It("Uses organization claim when both organization and organizations are present", func(ctx context.Context) {
			token := createKeycloakUserToken("org-tenant", "my-user", jwt.MapClaims{
				"organizations": []any{
					"orgs-tenant",
				},
			})
			ctx = ContextWithToken(ctx, token)
			handled := false
			_, err := interceptor.UnaryServer(
				ctx,
				nil,
				&grpc.UnaryServerInfo{
					FullMethod: "/osac.public.v1.Clusters/Create",
				},
				func(ctx context.Context, req any) (any, error) {
					subject := SubjectFromContext(ctx)
					Expect(subject.User).To(Equal("my-user"))
					Expect(subject.Tenants.Finite()).To(BeTrue())
					Expect(subject.Tenants.Inclusions()).To(ConsistOf("org-tenant"))
					Expect(subject.Tenants.Inclusions()).ToNot(ContainElement("orgs-tenant"))
					handled = true
					return nil, nil
				},
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(handled).To(BeTrue())
		})
	})
})
