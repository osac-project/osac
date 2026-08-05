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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/database/dao"
	"github.com/osac-project/fulfillment-service/internal/events"
)

var _ = Describe("Private identity providers server", func() {
	const (
		// Test fixture values for credentials (not real secrets)
		testOidcClientSecret = "test-client-secret-fixture"
	)

	BeforeEach(func() {
		// The default tenant mock returns 'system', which is invalid for identity providers, so we need to
		// create a valid tenant, and use it explicitly in the tests.
		tenantsDao, err := dao.NewGenericDAO[*privatev1.Tenant]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		_, err = tenantsDao.Create().
			SetObject(
				privatev1.Tenant_builder{
					Id: "my-tenant",
					Metadata: privatev1.Metadata_builder{
						Name:   "my-tenant",
						Tenant: "my-tenant",
					}.Build(),
				}.Build(),
			).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("Behaviour", func() {
		var server *PrivateIdentityProvidersServer

		BeforeEach(func() {
			var err error

			// Create server:
			server, err = NewPrivateIdentityProvidersServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Creates an identity provider", func() {
			// Create request:
			request := privatev1.IdentityProvidersCreateRequest_builder{
				Object: privatev1.IdentityProvider_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   "test-oidc",
						Tenant: "my-tenant",
					}.Build(),
					Spec: privatev1.IdentityProviderSpec_builder{
						Title:   "Test OIDC",
						Enabled: true,
						Oidc: privatev1.OidcConfig_builder{
							AuthorizationUrl: "https://example.com/auth",
							TokenUrl:         "https://example.com/token",
							ClientId:         "client-id",
							ClientSecret:     testOidcClientSecret,
							Issuer:           "https://example.com",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build()

			// Create identity provider:
			response, err := server.Create(ctx, request)
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			Expect(response.Object).ToNot(BeNil())
			Expect(response.Object.Id).ToNot(BeEmpty())
			Expect(response.Object.Metadata.Name).To(Equal("test-oidc"))
			Expect(response.Object.Spec.Title).To(Equal("Test OIDC"))
			Expect(response.Object.Spec.Enabled).To(BeTrue())
			Expect(response.Object.Spec.GetOidc()).ToNot(BeNil())
			Expect(response.Object.Spec.GetOidc().Issuer).To(Equal("https://example.com"))
		})

		It("Sets tenant from the request context for identity providers", func() {
			// Create request with explicit tenant (required for identity providers):
			request := privatev1.IdentityProvidersCreateRequest_builder{
				Object: privatev1.IdentityProvider_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   "test-oidc",
						Tenant: "my-tenant",
					}.Build(),
					Spec: privatev1.IdentityProviderSpec_builder{
						Title:   "Test OIDC",
						Enabled: true,
						Oidc: privatev1.OidcConfig_builder{
							AuthorizationUrl: "https://example.com/auth",
							TokenUrl:         "https://example.com/token",
							ClientId:         "client-id",
							ClientSecret:     testOidcClientSecret,
							Issuer:           "https://example.com",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build()

			// Create identity provider:
			response, err := server.Create(ctx, request)
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			Expect(response.Object).ToNot(BeNil())
			// Tenant should be preserved from the request
			Expect(response.Object.Metadata.Tenant).To(Equal("my-tenant"))
		})

		It("Lists identity providers", func() {
			// Create an identity provider first:
			createReq := privatev1.IdentityProvidersCreateRequest_builder{
				Object: privatev1.IdentityProvider_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   "test-oidc",
						Tenant: "my-tenant",
					}.Build(),
					Spec: privatev1.IdentityProviderSpec_builder{
						Title:   "Test OIDC",
						Enabled: true,
						Oidc: privatev1.OidcConfig_builder{
							AuthorizationUrl: "https://example.com/auth",
							TokenUrl:         "https://example.com/token",
							ClientId:         "client-id",
							ClientSecret:     testOidcClientSecret,
							Issuer:           "https://example.com",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build()
			_, err := server.Create(ctx, createReq)
			Expect(err).ToNot(HaveOccurred())

			// List identity providers:
			listResp, err := server.List(ctx, &privatev1.IdentityProvidersListRequest{
				Filter: new("this.metadata.name == 'test-oidc'"),
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(listResp.Size).To(Equal(int32(1)))
			Expect(listResp.Items).To(HaveLen(1))
			Expect(listResp.Items[0].Metadata.Name).To(Equal("test-oidc"))
		})

		It("Gets an identity provider by ID", func() {
			// Create an identity provider:
			createReq := privatev1.IdentityProvidersCreateRequest_builder{
				Object: privatev1.IdentityProvider_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   "test-oidc",
						Tenant: "my-tenant",
					}.Build(),
					Spec: privatev1.IdentityProviderSpec_builder{
						Title:   "Test OIDC",
						Enabled: true,
						Oidc: privatev1.OidcConfig_builder{
							AuthorizationUrl: "https://example.com/auth",
							TokenUrl:         "https://example.com/token",
							ClientId:         "client-id",
							ClientSecret:     testOidcClientSecret,
							Issuer:           "https://example.com",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build()
			createResp, err := server.Create(ctx, createReq)
			Expect(err).ToNot(HaveOccurred())

			// Get the identity provider:
			getResp, err := server.Get(ctx, privatev1.IdentityProvidersGetRequest_builder{
				Id: createResp.Object.Id,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResp.Object.Id).To(Equal(createResp.Object.Id))
			Expect(getResp.Object.Metadata.Name).To(Equal("test-oidc"))
		})

		It("Deletes an identity provider", func() {
			// Create an identity provider:
			createReq := privatev1.IdentityProvidersCreateRequest_builder{
				Object: privatev1.IdentityProvider_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   "test-oidc",
						Tenant: "my-tenant",
					}.Build(),
					Spec: privatev1.IdentityProviderSpec_builder{
						Title:   "Test OIDC",
						Enabled: true,
						Oidc: privatev1.OidcConfig_builder{
							AuthorizationUrl: "https://example.com/auth",
							TokenUrl:         "https://example.com/token",
							ClientId:         "client-id",
							ClientSecret:     testOidcClientSecret,
							Issuer:           "https://example.com",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build()
			createResp, err := server.Create(ctx, createReq)
			Expect(err).ToNot(HaveOccurred())

			// Delete the identity provider:
			_, err = server.Delete(ctx, privatev1.IdentityProvidersDeleteRequest_builder{
				Id: createResp.Object.Id,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Updates an identity provider", func() {
			// Create an identity provider:
			createReq := privatev1.IdentityProvidersCreateRequest_builder{
				Object: privatev1.IdentityProvider_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   "test-oidc",
						Tenant: "my-tenant",
					}.Build(),
					Spec: privatev1.IdentityProviderSpec_builder{
						Title:   "Test OIDC",
						Enabled: true,
						Oidc: privatev1.OidcConfig_builder{
							AuthorizationUrl: "https://example.com/auth",
							TokenUrl:         "https://example.com/token",
							ClientId:         "client-id",
							ClientSecret:     testOidcClientSecret,
							Issuer:           "https://example.com",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build()
			createResp, err := server.Create(ctx, createReq)
			Expect(err).ToNot(HaveOccurred())

			// Update the identity provider:
			updateReq := privatev1.IdentityProvidersUpdateRequest_builder{
				Object: privatev1.IdentityProvider_builder{
					Id: createResp.Object.Id,
					Spec: privatev1.IdentityProviderSpec_builder{
						Title: "Updated OIDC Title",
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{
						"spec.title",
					},
				},
			}.Build()
			updateResp, err := server.Update(ctx, updateReq)
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResp.Object.Spec.Title).To(Equal("Updated OIDC Title"))
		})

		It("Accepts creation of an identity provider without a name", func() {
			// Identity providers can have empty names (name is not mandatory)
			response, err := server.Create(ctx, privatev1.IdentityProvidersCreateRequest_builder{
				Object: privatev1.IdentityProvider_builder{
					Metadata: privatev1.Metadata_builder{
						Tenant: "my-tenant",
					}.Build(),
					Spec: privatev1.IdentityProviderSpec_builder{
						Title:   "Test OIDC",
						Enabled: true,
						Oidc: privatev1.OidcConfig_builder{
							AuthorizationUrl: "https://example.com/auth",
							TokenUrl:         "https://example.com/token",
							ClientId:         "client-id",
							ClientSecret:     testOidcClientSecret,
							Issuer:           "https://example.com",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			// Tenant should be preserved from the request
			Expect(response.Object.Metadata.Tenant).To(Equal("my-tenant"))
		})

		It("Rejects update of the name of an identity provider", func() {
			createResponse, err := server.Create(ctx, privatev1.IdentityProvidersCreateRequest_builder{
				Object: privatev1.IdentityProvider_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   "test-oidc",
						Tenant: "my-tenant",
					}.Build(),
					Spec: privatev1.IdentityProviderSpec_builder{
						Title:   "Test OIDC",
						Enabled: true,
						Oidc: privatev1.OidcConfig_builder{
							AuthorizationUrl: "https://example.com/auth",
							TokenUrl:         "https://example.com/token",
							ClientId:         "client-id",
							ClientSecret:     testOidcClientSecret,
							Issuer:           "https://example.com",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()
			id := object.GetId()
			updateResponse, err := server.Update(ctx, privatev1.IdentityProvidersUpdateRequest_builder{
				Object: privatev1.IdentityProvider_builder{
					Id: id,
					Metadata: privatev1.Metadata_builder{
						Name: "new-name",
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{
						"metadata.name",
					},
				},
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(updateResponse).To(BeNil())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(Equal(
				"field 'metadata.name' is immutable",
			))
		})

		It("Rejects update of the tenant of an identity provider", func() {
			createResponse, err := server.Create(ctx, privatev1.IdentityProvidersCreateRequest_builder{
				Object: privatev1.IdentityProvider_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   "test-oidc",
						Tenant: "my-tenant",
					}.Build(),
					Spec: privatev1.IdentityProviderSpec_builder{
						Title:   "Test OIDC",
						Enabled: true,
						Oidc: privatev1.OidcConfig_builder{
							AuthorizationUrl: "https://example.com/auth",
							TokenUrl:         "https://example.com/token",
							ClientId:         "client-id",
							ClientSecret:     testOidcClientSecret,
							Issuer:           "https://example.com",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()
			id := object.GetId()
			updateResponse, err := server.Update(ctx, privatev1.IdentityProvidersUpdateRequest_builder{
				Object: privatev1.IdentityProvider_builder{
					Id: id,
					Metadata: privatev1.Metadata_builder{
						Tenant: "other-tenant",
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{
						"metadata.tenant",
					},
				},
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(updateResponse).To(BeNil())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(Equal(
				"field 'metadata.tenant' is immutable",
			))
		})

		It("Creates an OIDC identity provider", func() {
			// Create request:
			request := privatev1.IdentityProvidersCreateRequest_builder{
				Object: privatev1.IdentityProvider_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   "test-oidc",
						Tenant: "my-tenant",
					}.Build(),
					Spec: privatev1.IdentityProviderSpec_builder{
						Title:   "Test OIDC",
						Enabled: true,
						Oidc: privatev1.OidcConfig_builder{
							AuthorizationUrl: "https://accounts.google.com/o/oauth2/v2/auth",
							TokenUrl:         "https://oauth2.googleapis.com/token",
							ClientId:         "my-client-id",
							ClientSecret:     testOidcClientSecret,
							Issuer:           "https://accounts.google.com",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build()

			// Create identity provider:
			response, err := server.Create(ctx, request)
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			Expect(response.Object).ToNot(BeNil())
			Expect(response.Object.Id).ToNot(BeEmpty())
			Expect(response.Object.Metadata.Name).To(Equal("test-oidc"))
			Expect(response.Object.Spec.GetOidc()).ToNot(BeNil())
			Expect(response.Object.Spec.GetOidc().Issuer).To(Equal("https://accounts.google.com"))
		})

		Describe("Tenant Validation", func() {
			It("Rejects creation when tenant is explicitly set to 'shared'", func() {
				request := privatev1.IdentityProvidersCreateRequest_builder{
					Object: privatev1.IdentityProvider_builder{
						Metadata: privatev1.Metadata_builder{
							Name:   "test-oidc",
							Tenant: "shared",
						}.Build(),
						Spec: privatev1.IdentityProviderSpec_builder{
							Title:   "Test OIDC",
							Enabled: true,
							Oidc: privatev1.OidcConfig_builder{
								AuthorizationUrl: "https://example.com/auth",
								TokenUrl:         "https://example.com/token",
								ClientId:         "client-id",
								ClientSecret:     testOidcClientSecret,
								Issuer:           "https://example.com",
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build()

				_, err := server.Create(ctx, request)
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("cannot belong to 'shared' tenant"))
			})

			It("Rejects creation when tenant is explicitly set to 'system'", func() {
				request := privatev1.IdentityProvidersCreateRequest_builder{
					Object: privatev1.IdentityProvider_builder{
						Metadata: privatev1.Metadata_builder{
							Name:   "test-oidc",
							Tenant: "system",
						}.Build(),
						Spec: privatev1.IdentityProviderSpec_builder{
							Title:   "Test OIDC",
							Enabled: true,
							Oidc: privatev1.OidcConfig_builder{
								AuthorizationUrl: "https://example.com/auth",
								TokenUrl:         "https://example.com/token",
								ClientId:         "client-id",
								ClientSecret:     testOidcClientSecret,
								Issuer:           "https://example.com",
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build()

				_, err := server.Create(ctx, request)
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("cannot belong to 'system' tenant"))
			})
		})
	})

	Describe("Redacts event payload", func() {
		var (
			event  *privatev1.Event
			server *PrivateIdentityProvidersServer
		)

		BeforeEach(func() {
			var err error

			// Create a mock notifier that captures the event:
			notifier := events.NewMockNotifier(ctrl)
			notifier.EXPECT().
				Notify(gomock.Any(), gomock.Any()).
				DoAndReturn(
					func(ctx context.Context, payload proto.Message) error {
						event = payload.(*privatev1.Event)
						return nil
					},
				)

			// Create the server configured with the mock notifier:
			server, err = NewPrivateIdentityProvidersServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				SetNotifier(notifier).
				Build()
			Expect(err).ToNot(HaveOccurred())

		})

		It("OIDC", func() {
			// Create the object:
			_, err := server.Create(
				ctx,
				privatev1.IdentityProvidersCreateRequest_builder{
					Object: privatev1.IdentityProvider_builder{
						Metadata: privatev1.Metadata_builder{
							Tenant: "my-tenant",
							Name:   "my-oidc",
						}.Build(),
						Spec: privatev1.IdentityProviderSpec_builder{
							Title:   "My OIDC",
							Enabled: true,
							Oidc: privatev1.OidcConfig_builder{
								AuthorizationUrl: "https://accounts.google.com/o/oauth2/v2/auth",
								TokenUrl:         "https://oauth2.googleapis.com/token",
								ClientId:         "my-client-id",
								ClientSecret:     testOidcClientSecret,
								Issuer:           "https://accounts.google.com",
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build(),
			)
			Expect(err).ToNot(HaveOccurred())

			// Verify the event:
			Expect(event).ToNot(BeNil())
			Expect(event.GetType()).To(Equal(privatev1.EventType_EVENT_TYPE_OBJECT_CREATED))
			object := event.GetIdentityProvider()
			Expect(object).ToNot(BeNil())
			Expect(object.GetSpec().GetOidc().GetClientSecret()).To(BeEmpty())
		})
	})
})
