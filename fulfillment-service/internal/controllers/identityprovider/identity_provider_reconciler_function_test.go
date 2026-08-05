/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package identityprovider

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/types/known/timestamppb"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/apiclient"
	"github.com/osac-project/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/fulfillment-service/internal/idp"
	"google.golang.org/grpc"
)

// mockIdentityProvidersClient is a minimal mock for testing the gRPC client path
type mockIdentityProvidersClient struct {
	getFunc func(ctx context.Context, req *privatev1.IdentityProvidersGetRequest) (*privatev1.IdentityProvidersGetResponse, error)
}

func (m *mockIdentityProvidersClient) Get(ctx context.Context, req *privatev1.IdentityProvidersGetRequest, _ ...grpc.CallOption) (*privatev1.IdentityProvidersGetResponse, error) {
	return m.getFunc(ctx, req)
}

func (m *mockIdentityProvidersClient) Create(ctx context.Context, req *privatev1.IdentityProvidersCreateRequest, _ ...grpc.CallOption) (*privatev1.IdentityProvidersCreateResponse, error) {
	return nil, nil
}

func (m *mockIdentityProvidersClient) List(ctx context.Context, req *privatev1.IdentityProvidersListRequest, _ ...grpc.CallOption) (*privatev1.IdentityProvidersListResponse, error) {
	return nil, nil
}

func (m *mockIdentityProvidersClient) Update(ctx context.Context, req *privatev1.IdentityProvidersUpdateRequest, _ ...grpc.CallOption) (*privatev1.IdentityProvidersUpdateResponse, error) {
	return nil, nil
}

func (m *mockIdentityProvidersClient) Delete(ctx context.Context, req *privatev1.IdentityProvidersDeleteRequest, _ ...grpc.CallOption) (*privatev1.IdentityProvidersDeleteResponse, error) {
	return nil, nil
}

func (m *mockIdentityProvidersClient) Signal(ctx context.Context, req *privatev1.IdentityProvidersSignalRequest, _ ...grpc.CallOption) (*privatev1.IdentityProvidersSignalResponse, error) {
	return nil, nil
}

var _ = Describe("Finalizer Management", func() {
	It("should add finalizer on first call", func() {
		identityProvider := privatev1.IdentityProvider_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{},
			}.Build(),
		}.Build()

		task := &task{
			identityProvider: identityProvider,
		}

		added := task.addFinalizer()
		Expect(added).To(BeTrue())
		Expect(identityProvider.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
	})

	It("should not add finalizer if already present", func() {
		identityProvider := privatev1.IdentityProvider_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
			}.Build(),
		}.Build()

		task := &task{
			identityProvider: identityProvider,
		}

		added := task.addFinalizer()
		Expect(added).To(BeFalse())
		Expect(identityProvider.GetMetadata().GetFinalizers()).To(HaveLen(1))
	})

	It("should return immediately after adding finalizer", func() {
		identityProvider := privatev1.IdentityProvider_builder{
			Metadata: privatev1.Metadata_builder{
				Name:   "test-idp",
				Tenant: "tenant-1",
			}.Build(),
		}.Build()

		task := &task{
			identityProvider: identityProvider,
		}

		err := task.update(context.Background())
		Expect(err).ToNot(HaveOccurred())

		Expect(identityProvider.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
		Expect(identityProvider.HasStatus()).To(BeFalse())
	})

	It("should initialize metadata when adding finalizer if not present", func() {
		identityProvider := privatev1.IdentityProvider_builder{}.Build()

		task := &task{
			identityProvider: identityProvider,
		}

		added := task.addFinalizer()
		Expect(added).To(BeTrue())
		Expect(identityProvider.HasMetadata()).To(BeTrue())
		Expect(identityProvider.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
	})
})

var _ = Describe("Default Values", func() {
	It("should set default status if missing", func() {
		identityProvider := privatev1.IdentityProvider_builder{}.Build()

		task := &task{
			identityProvider: identityProvider,
		}

		task.setDefaults()
		Expect(identityProvider.HasStatus()).To(BeTrue())
		Expect(identityProvider.GetStatus().GetPhase()).To(Equal(privatev1.IdentityProviderPhase_IDENTITY_PROVIDER_PHASE_UNKNOWN))
	})

	It("should set default phase if unspecified", func() {
		identityProvider := privatev1.IdentityProvider_builder{
			Status: privatev1.IdentityProviderStatus_builder{}.Build(),
		}.Build()

		task := &task{
			identityProvider: identityProvider,
		}

		task.setDefaults()
		Expect(identityProvider.GetStatus().GetPhase()).To(Equal(privatev1.IdentityProviderPhase_IDENTITY_PROVIDER_PHASE_UNKNOWN))
	})

	It("should not override existing phase", func() {
		identityProvider := privatev1.IdentityProvider_builder{
			Status: privatev1.IdentityProviderStatus_builder{
				Phase: privatev1.IdentityProviderPhase_IDENTITY_PROVIDER_PHASE_READY,
			}.Build(),
		}.Build()

		task := &task{
			identityProvider: identityProvider,
		}

		task.setDefaults()
		Expect(identityProvider.GetStatus().GetPhase()).To(Equal(privatev1.IdentityProviderPhase_IDENTITY_PROVIDER_PHASE_READY))
	})
})

var _ = Describe("Tenant Validation", func() {
	It("should succeed when tenant is set to a valid tenant", func() {
		identityProvider := privatev1.IdentityProvider_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: "my-org",
			}.Build(),
		}.Build()

		task := &task{
			identityProvider: identityProvider,
		}

		err := task.validateTenant()
		Expect(err).ToNot(HaveOccurred())
	})

	It("should fail when tenant is empty", func() {
		identityProvider := privatev1.IdentityProvider_builder{
			Metadata: privatev1.Metadata_builder{}.Build(),
		}.Build()

		task := &task{
			identityProvider: identityProvider,
		}

		err := task.validateTenant()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("must have a tenant assigned"))
	})

	It("should fail when metadata is missing", func() {
		identityProvider := privatev1.IdentityProvider_builder{}.Build()

		task := &task{
			identityProvider: identityProvider,
		}

		err := task.validateTenant()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("must have a tenant assigned"))
	})

	It("should fail when tenant is 'shared'", func() {
		identityProvider := privatev1.IdentityProvider_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: "shared",
			}.Build(),
		}.Build()

		task := &task{
			identityProvider: identityProvider,
		}

		err := task.validateTenant()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("cannot belong to 'shared' tenant"))
	})

	It("should fail when tenant is 'system'", func() {
		identityProvider := privatev1.IdentityProvider_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: "system",
			}.Build(),
		}.Build()

		task := &task{
			identityProvider: identityProvider,
		}

		err := task.validateTenant()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("cannot belong to 'system' tenant"))
	})
})

var _ = Describe("Provider Type Detection", func() {
	It("should return oidc for OIDC config", func() {
		identityProvider := privatev1.IdentityProvider_builder{
			Spec: privatev1.IdentityProviderSpec_builder{
				Oidc: privatev1.OidcConfig_builder{
					Issuer: "https://example.com",
				}.Build(),
			}.Build(),
		}.Build()

		task := &task{
			identityProvider: identityProvider,
		}

		providerType := task.determineProviderTypeFromIdp(identityProvider)
		Expect(providerType).To(Equal("oidc"))
	})

	It("should return empty string when no config is set", func() {
		identityProvider := privatev1.IdentityProvider_builder{
			Spec: privatev1.IdentityProviderSpec_builder{}.Build(),
		}.Build()

		task := &task{
			identityProvider: identityProvider,
		}

		providerType := task.determineProviderTypeFromIdp(identityProvider)
		Expect(providerType).To(BeEmpty())
	})
})

var _ = Describe("Config Building", func() {
	It("should build OIDC config correctly", func() {
		identityProvider := privatev1.IdentityProvider_builder{
			Spec: privatev1.IdentityProviderSpec_builder{
				Oidc: privatev1.OidcConfig_builder{
					AuthorizationUrl: "https://example.com/auth",
					TokenUrl:         "https://example.com/token",
					ClientId:         "client-123",
					ClientSecret:     "secret-456",
					Issuer:           "https://example.com",
				}.Build(),
			}.Build(),
		}.Build()

		task := &task{
			identityProvider: identityProvider,
		}

		config := task.buildConfigFromIdp(identityProvider)
		Expect(config).To(HaveKeyWithValue("authorizationUrl", "https://example.com/auth"))
		Expect(config).To(HaveKeyWithValue("tokenUrl", "https://example.com/token"))
		Expect(config).To(HaveKeyWithValue("clientId", "client-123"))
		Expect(config).To(HaveKeyWithValue("clientSecret", "secret-456"))
		Expect(config).To(HaveKeyWithValue("issuer", "https://example.com"))
	})

	It("should return empty config when no provider is configured", func() {
		identityProvider := privatev1.IdentityProvider_builder{
			Spec: privatev1.IdentityProviderSpec_builder{}.Build(),
		}.Build()

		task := &task{
			identityProvider: identityProvider,
		}

		config := task.buildConfigFromIdp(identityProvider)
		Expect(config).To(BeEmpty())
	})
})

var _ = Describe("IDP Sync", func() {
	var (
		ctrl       *gomock.Controller
		mockClient *idp.MockClientInterface
		reconciler *function
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		mockClient = idp.NewMockClientInterface(ctrl)

		reconciler = &function{
			logger:    logger,
			idpClient: mockClient,
		}
	})

	AfterEach(func() {
		ctrl.Finish()
	})

	It("should sync identity provider with OIDC config", func() {
		identityProvider := privatev1.IdentityProvider_builder{
			Id: "idp-456",
			Metadata: privatev1.Metadata_builder{
				Name:       "google-sso",
				Tenant:     "tenant-2",
				Finalizers: []string{finalizers.Controller},
			}.Build(),
			Spec: privatev1.IdentityProviderSpec_builder{
				Title:   "Google SSO",
				Enabled: true,
				Oidc: privatev1.OidcConfig_builder{
					AuthorizationUrl: "https://accounts.google.com/o/oauth2/auth",
					TokenUrl:         "https://oauth2.googleapis.com/token",
					ClientId:         "client-123",
					ClientSecret:     "secret-456",
					Issuer:           "https://accounts.google.com",
				}.Build(),
			}.Build(),
		}.Build()

		mockClient.EXPECT().
			CreateIdentityProvider(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, tenantName string, idpProvider *idp.IdentityProvider) (*idp.IdentityProvider, error) {
				Expect(tenantName).To(Equal("tenant-2"))
				Expect(idpProvider.Alias).To(Equal("tenant-2-google-sso"))
				Expect(idpProvider.DisplayName).To(Equal("Google SSO"))
				Expect(idpProvider.Type).To(Equal("oidc"))
				Expect(idpProvider.Enabled).To(BeTrue())
				Expect(idpProvider.Config).To(HaveKeyWithValue("issuer", "https://accounts.google.com"))
				return idpProvider, nil
			}).
			Times(1)

		task := &task{
			r:                reconciler,
			identityProvider: identityProvider,
		}

		err := task.update(ctx)
		Expect(err).ToNot(HaveOccurred())

		Expect(identityProvider.GetStatus().GetPhase()).To(Equal(privatev1.IdentityProviderPhase_IDENTITY_PROVIDER_PHASE_READY))
	})

	It("should fetch full object with secrets via identityProvidersClient", func() {
		// This test verifies the full-object fetch path in syncToIDP
		// when identityProvidersClient is set (real controller scenario)

		// Create event object with redacted secrets (simulating watch event)
		identityProviderEvent := privatev1.IdentityProvider_builder{
			Id: "idp-789",
			Metadata: privatev1.Metadata_builder{
				Name:       "oidc-provider",
				Tenant:     "tenant-3",
				Finalizers: []string{finalizers.Controller},
			}.Build(),
			Spec: privatev1.IdentityProviderSpec_builder{
				Title:   "OIDC Provider",
				Enabled: true,
				Oidc: privatev1.OidcConfig_builder{
					AuthorizationUrl: "https://auth.example.com/authorize",
					TokenUrl:         "https://auth.example.com/token",
					ClientId:         "client-xyz",
					ClientSecret:     "[REDACTED]", // Simulates watch event redaction
					Issuer:           "https://auth.example.com",
				}.Build(),
			}.Build(),
		}.Build()

		// Create full object with unredacted secrets (simulating Get response)
		fullIdentityProvider := privatev1.IdentityProvider_builder{
			Id: "idp-789",
			Metadata: privatev1.Metadata_builder{
				Name:       "oidc-provider",
				Tenant:     "tenant-3",
				Finalizers: []string{finalizers.Controller},
			}.Build(),
			Spec: privatev1.IdentityProviderSpec_builder{
				Title:   "OIDC Provider",
				Enabled: true,
				Oidc: privatev1.OidcConfig_builder{
					AuthorizationUrl: "https://auth.example.com/authorize",
					TokenUrl:         "https://auth.example.com/token",
					ClientId:         "client-xyz",
					ClientSecret:     "actual-secret-123", // Real secret from Get
					Issuer:           "https://auth.example.com",
				}.Build(),
			}.Build(),
		}.Build()

		// Mock the gRPC client Get call
		mockGrpcClient := &mockIdentityProvidersClient{
			getFunc: func(ctx context.Context, req *privatev1.IdentityProvidersGetRequest) (*privatev1.IdentityProvidersGetResponse, error) {
				Expect(req.GetId()).To(Equal("idp-789"))
				return privatev1.IdentityProvidersGetResponse_builder{
					Object: fullIdentityProvider,
				}.Build(), nil
			},
		}

		reconciler := &function{
			logger:                  logger,
			identityProvidersClient: mockGrpcClient,
			idpClient:               mockClient,
		}

		mockClient.EXPECT().
			CreateIdentityProvider(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, tenantName string, idpProvider *idp.IdentityProvider) (*idp.IdentityProvider, error) {
				// Verify the full object with unredacted secrets was used
				Expect(tenantName).To(Equal("tenant-3"))
				Expect(idpProvider.Config).To(HaveKeyWithValue("clientSecret", "actual-secret-123"))
				Expect(idpProvider.Config).To(HaveKeyWithValue("clientAuthMethod", "client_secret_post"))
				Expect(idpProvider.Config).To(HaveKeyWithValue("authorizationUrl", "https://auth.example.com/authorize"))
				return idpProvider, nil
			}).
			Times(1)

		task := &task{
			r:                reconciler,
			identityProvider: identityProviderEvent,
		}

		err := task.update(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(identityProviderEvent.GetStatus().GetPhase()).To(Equal(privatev1.IdentityProviderPhase_IDENTITY_PROVIDER_PHASE_READY))
	})

	It("should use tenant-prefixed alias", func() {
		identityProvider := privatev1.IdentityProvider_builder{
			Metadata: privatev1.Metadata_builder{
				Name:       "test-idp",
				Tenant:     "my-tenant",
				Finalizers: []string{finalizers.Controller},
			}.Build(),
			Spec: privatev1.IdentityProviderSpec_builder{
				Title:   "Test IDP",
				Enabled: true,
				Oidc: privatev1.OidcConfig_builder{
					Issuer: "https://example.com",
				}.Build(),
			}.Build(),
		}.Build()

		mockClient.EXPECT().
			CreateIdentityProvider(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, tenantName string, idpProvider *idp.IdentityProvider) (*idp.IdentityProvider, error) {
				Expect(tenantName).To(Equal("my-tenant"))
				Expect(idpProvider.Alias).To(Equal("my-tenant-test-idp"))
				return idpProvider, nil
			}).
			Times(1)

		task := &task{
			r:                reconciler,
			identityProvider: identityProvider,
		}

		err := task.update(ctx)
		Expect(err).ToNot(HaveOccurred())
	})

	It("should set ERROR state on IDP creation failure", func() {
		identityProvider := privatev1.IdentityProvider_builder{
			Metadata: privatev1.Metadata_builder{
				Name:       "test-oidc",
				Tenant:     "tenant-1",
				Finalizers: []string{finalizers.Controller},
			}.Build(),
			Spec: privatev1.IdentityProviderSpec_builder{
				Title: "Test OIDC",
				Oidc: privatev1.OidcConfig_builder{
					Issuer: "https://example.com",
				}.Build(),
			}.Build(),
		}.Build()

		mockClient.EXPECT().
			CreateIdentityProvider(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(nil, fmt.Errorf("IDP connection timeout")).
			Times(1)

		task := &task{
			r:                reconciler,
			identityProvider: identityProvider,
		}

		err := task.update(ctx)
		Expect(err).ToNot(HaveOccurred())

		Expect(identityProvider.GetStatus().GetPhase()).To(Equal(privatev1.IdentityProviderPhase_IDENTITY_PROVIDER_PHASE_ERROR))
		Expect(identityProvider.GetStatus().GetMessage()).To(ContainSubstring("Identity provider creation in IDP failed"))
		Expect(identityProvider.GetStatus().GetMessage()).To(ContainSubstring("IDP connection timeout"))
	})

	It("should not return error on IDP failure", func() {
		identityProvider := privatev1.IdentityProvider_builder{
			Metadata: privatev1.Metadata_builder{
				Name:       "test-idp",
				Tenant:     "tenant-1",
				Finalizers: []string{finalizers.Controller},
			}.Build(),
			Spec: privatev1.IdentityProviderSpec_builder{
				Oidc: privatev1.OidcConfig_builder{
					Issuer: "https://example.com",
				}.Build(),
			}.Build(),
		}.Build()

		mockClient.EXPECT().
			CreateIdentityProvider(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(nil, fmt.Errorf("identity provider already exists")).
			Times(1)

		task := &task{
			r:                reconciler,
			identityProvider: identityProvider,
		}

		err := task.update(ctx)
		Expect(err).ToNot(HaveOccurred())
	})

	It("should sync disabled identity provider", func() {
		identityProvider := privatev1.IdentityProvider_builder{
			Metadata: privatev1.Metadata_builder{
				Name:       "disabled-oidc",
				Tenant:     "tenant-1",
				Finalizers: []string{finalizers.Controller},
			}.Build(),
			Spec: privatev1.IdentityProviderSpec_builder{
				Title:   "Disabled OIDC",
				Enabled: false,
				Oidc: privatev1.OidcConfig_builder{
					Issuer: "https://example.com",
				}.Build(),
			}.Build(),
		}.Build()

		mockClient.EXPECT().
			CreateIdentityProvider(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, tenantName string, idpProvider *idp.IdentityProvider) (*idp.IdentityProvider, error) {
				Expect(tenantName).To(Equal("tenant-1"))
				Expect(idpProvider.Enabled).To(BeFalse())
				return idpProvider, nil
			}).
			Times(1)

		task := &task{
			r:                reconciler,
			identityProvider: identityProvider,
		}

		err := task.update(ctx)
		Expect(err).ToNot(HaveOccurred())

		Expect(identityProvider.GetStatus().GetPhase()).To(Equal(privatev1.IdentityProviderPhase_IDENTITY_PROVIDER_PHASE_READY))
	})
})

var _ = Describe("Skip Reconciliation", func() {
	It("should skip reconciliation for ready identity providers", func() {
		identityProvider := privatev1.IdentityProvider_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant:     "my-org",
				Finalizers: []string{finalizers.Controller},
			}.Build(),
			Status: privatev1.IdentityProviderStatus_builder{
				Phase: privatev1.IdentityProviderPhase_IDENTITY_PROVIDER_PHASE_READY,
			}.Build(),
		}.Build()

		task := &task{
			identityProvider: identityProvider,
		}

		err := task.update(context.Background())
		Expect(err).ToNot(HaveOccurred())
	})

	It("should skip reconciliation for error state identity providers", func() {
		identityProvider := privatev1.IdentityProvider_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant:     "my-org",
				Finalizers: []string{finalizers.Controller},
			}.Build(),
			Status: privatev1.IdentityProviderStatus_builder{
				Phase:   privatev1.IdentityProviderPhase_IDENTITY_PROVIDER_PHASE_ERROR,
				Message: "Previous sync failed",
			}.Build(),
		}.Build()

		task := &task{
			identityProvider: identityProvider,
		}

		err := task.update(context.Background())
		Expect(err).ToNot(HaveOccurred())
	})
})

var _ = Describe("Deletion", func() {
	var (
		ctrl       *gomock.Controller
		mockClient *idp.MockClientInterface
		reconciler *function
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		mockClient = idp.NewMockClientInterface(ctrl)

		reconciler = &function{
			logger:    logger,
			idpClient: mockClient,
		}
	})

	AfterEach(func() {
		ctrl.Finish()
	})

	It("should remove finalizer when identity provider not synced", func() {
		deletionTimestamp := timestamppb.New(time.Now())
		identityProvider := privatev1.IdentityProvider_builder{
			Metadata: privatev1.Metadata_builder{
				Name:              "test-idp",
				Finalizers:        []string{finalizers.Controller},
				DeletionTimestamp: deletionTimestamp,
			}.Build(),
			Status: privatev1.IdentityProviderStatus_builder{
				Phase: privatev1.IdentityProviderPhase_IDENTITY_PROVIDER_PHASE_UNKNOWN,
			}.Build(),
		}.Build()

		task := &task{
			r:                reconciler,
			identityProvider: identityProvider,
		}

		err := task.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(identityProvider.GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.Controller))
	})

	It("should remove finalizer when in ERROR state", func() {
		deletionTimestamp := timestamppb.New(time.Now())
		identityProvider := privatev1.IdentityProvider_builder{
			Metadata: privatev1.Metadata_builder{
				Name:              "test-idp",
				Finalizers:        []string{finalizers.Controller},
				DeletionTimestamp: deletionTimestamp,
			}.Build(),
			Status: privatev1.IdentityProviderStatus_builder{
				Phase: privatev1.IdentityProviderPhase_IDENTITY_PROVIDER_PHASE_ERROR,
			}.Build(),
		}.Build()

		task := &task{
			r:                reconciler,
			identityProvider: identityProvider,
		}

		err := task.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(identityProvider.GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.Controller))
	})

	It("should delete from IDP and remove finalizer when deleting READY identity provider", func() {
		deletionTimestamp := timestamppb.New(time.Now())
		identityProvider := privatev1.IdentityProvider_builder{
			Id: "idp-123",
			Metadata: privatev1.Metadata_builder{
				Name:              "test-idp",
				Tenant:            "tenant-1",
				Finalizers:        []string{finalizers.Controller},
				DeletionTimestamp: deletionTimestamp,
			}.Build(),
			Status: privatev1.IdentityProviderStatus_builder{
				Phase: privatev1.IdentityProviderPhase_IDENTITY_PROVIDER_PHASE_READY,
			}.Build(),
		}.Build()

		mockClient.EXPECT().
			DeleteIdentityProvider(gomock.Any(), "tenant-1", "tenant-1-test-idp").
			Return(nil).
			Times(1)

		task := &task{
			r:                reconciler,
			identityProvider: identityProvider,
		}

		err := task.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(identityProvider.GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.Controller))
	})

	It("should remove finalizer when IdP deletion returns 404 (already deleted)", func() {
		deletionTimestamp := timestamppb.New(time.Now())
		identityProvider := privatev1.IdentityProvider_builder{
			Id: "idp-404",
			Metadata: privatev1.Metadata_builder{
				Name:              "missing-idp",
				Tenant:            "tenant-1",
				Finalizers:        []string{finalizers.Controller},
				DeletionTimestamp: deletionTimestamp,
			}.Build(),
			Status: privatev1.IdentityProviderStatus_builder{
				Phase: privatev1.IdentityProviderPhase_IDENTITY_PROVIDER_PHASE_READY,
			}.Build(),
		}.Build()

		// Simulate 404 - IdP was already deleted
		mockClient.EXPECT().
			DeleteIdentityProvider(gomock.Any(), "tenant-1", "tenant-1-missing-idp").
			Return(&apiclient.APIError{
				StatusCode: 404,
				Body:       "Identity provider not found",
			}).
			Times(1)

		task := &task{
			r:                reconciler,
			identityProvider: identityProvider,
		}

		err := task.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(identityProvider.GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.Controller))
	})

	It("should keep finalizer and return error on non-404 delete failure", func() {
		deletionTimestamp := timestamppb.New(time.Now())
		identityProvider := privatev1.IdentityProvider_builder{
			Id: "idp-error",
			Metadata: privatev1.Metadata_builder{
				Name:              "failing-idp",
				Tenant:            "tenant-1",
				Finalizers:        []string{finalizers.Controller},
				DeletionTimestamp: deletionTimestamp,
			}.Build(),
			Status: privatev1.IdentityProviderStatus_builder{
				Phase: privatev1.IdentityProviderPhase_IDENTITY_PROVIDER_PHASE_READY,
			}.Build(),
		}.Build()

		// Simulate transient error (500)
		mockClient.EXPECT().
			DeleteIdentityProvider(gomock.Any(), "tenant-1", "tenant-1-failing-idp").
			Return(&apiclient.APIError{
				StatusCode: 500,
				Body:       "Internal server error",
			}).
			Times(1)

		task := &task{
			r:                reconciler,
			identityProvider: identityProvider,
		}

		err := task.delete(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to delete identity provider from IDP"))
		Expect(identityProvider.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
	})

	It("should remove finalizer when called", func() {
		identityProvider := privatev1.IdentityProvider_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller, "other-finalizer"},
			}.Build(),
		}.Build()

		task := &task{
			identityProvider: identityProvider,
		}

		task.removeFinalizer()
		Expect(identityProvider.GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.Controller))
		Expect(identityProvider.GetMetadata().GetFinalizers()).To(ContainElement("other-finalizer"))
	})

	It("should handle removal when finalizer not present", func() {
		identityProvider := privatev1.IdentityProvider_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{"other-finalizer"},
			}.Build(),
		}.Build()

		task := &task{
			identityProvider: identityProvider,
		}

		task.removeFinalizer()
		Expect(identityProvider.GetMetadata().GetFinalizers()).To(HaveLen(1))
		Expect(identityProvider.GetMetadata().GetFinalizers()).To(ContainElement("other-finalizer"))
	})

	It("should handle removal when metadata is not present", func() {
		identityProvider := privatev1.IdentityProvider_builder{}.Build()

		task := &task{
			identityProvider: identityProvider,
		}

		task.removeFinalizer()
		Expect(identityProvider.HasMetadata()).To(BeFalse())
	})
})
