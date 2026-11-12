/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package tenant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	vaultapi "github.com/hashicorp/vault/api"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers/defaultnetworking"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/idp"
	"github.com/osac-project/osac/fulfillment-service/internal/masks"
	"github.com/osac-project/osac/fulfillment-service/internal/vault"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// Fixture credentials for tenant reconciler tests; test* prefix marks them non-production.
const (
	testPreGeneratedPassword = "pre-generated-password"
	testInlinePassword       = "inline-password"
	testSecretPassword       = "from-secret"
)

type mockSecretsClient struct {
	getFunc    func(ctx context.Context, req *privatev1.SecretsGetRequest) (*privatev1.SecretsGetResponse, error)
	createFunc func(ctx context.Context, req *privatev1.SecretsCreateRequest) (*privatev1.SecretsCreateResponse, error)
	listFunc   func(ctx context.Context, req *privatev1.SecretsListRequest) (*privatev1.SecretsListResponse, error)
	deleteFunc func(ctx context.Context, req *privatev1.SecretsDeleteRequest) (*privatev1.SecretsDeleteResponse, error)
}

func (m *mockSecretsClient) Get(ctx context.Context, req *privatev1.SecretsGetRequest, _ ...grpc.CallOption) (*privatev1.SecretsGetResponse, error) {
	if m.getFunc != nil {
		return m.getFunc(ctx, req)
	}
	return nil, fmt.Errorf("unexpected Get")
}

func (m *mockSecretsClient) List(ctx context.Context, req *privatev1.SecretsListRequest, _ ...grpc.CallOption) (*privatev1.SecretsListResponse, error) {
	if m.listFunc != nil {
		return m.listFunc(ctx, req)
	}
	return nil, fmt.Errorf("unexpected List")
}

func (m *mockSecretsClient) Create(ctx context.Context, req *privatev1.SecretsCreateRequest, _ ...grpc.CallOption) (*privatev1.SecretsCreateResponse, error) {
	if m.createFunc != nil {
		return m.createFunc(ctx, req)
	}
	return nil, fmt.Errorf("unexpected Create")
}

func (m *mockSecretsClient) Update(context.Context, *privatev1.SecretsUpdateRequest, ...grpc.CallOption) (*privatev1.SecretsUpdateResponse, error) {
	return nil, fmt.Errorf("unexpected Update")
}

func (m *mockSecretsClient) Delete(ctx context.Context, req *privatev1.SecretsDeleteRequest, _ ...grpc.CallOption) (*privatev1.SecretsDeleteResponse, error) {
	if m.deleteFunc != nil {
		return m.deleteFunc(ctx, req)
	}
	return nil, fmt.Errorf("unexpected Delete")
}

func (m *mockSecretsClient) Signal(context.Context, *privatev1.SecretsSignalRequest, ...grpc.CallOption) (*privatev1.SecretsSignalResponse, error) {
	return nil, fmt.Errorf("unexpected Signal")
}

var _ = Describe("Tenant Validation", func() {
	It("should succeed with a tenant assigned", func() {
		tenant := privatev1.Tenant_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: "tenant-1",
			}.Build(),
		}.Build()

		task := &task{
			tenant: tenant,
		}

		err := task.validateTenant()
		Expect(err).ToNot(HaveOccurred())
	})

	It("should fail with empty tenant", func() {
		tenant := privatev1.Tenant_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: "",
			}.Build(),
		}.Build()

		task := &task{
			tenant: tenant,
		}

		err := task.validateTenant()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("tenant"))
	})

	It("should fail with missing metadata", func() {
		tenant := privatev1.Tenant_builder{}.Build()

		task := &task{
			tenant: tenant,
		}

		err := task.validateTenant()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("tenant"))
	})
})

var _ = Describe("Finalizer Management", func() {
	It("should add finalizer on first call", func() {
		tenant := privatev1.Tenant_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{},
			}.Build(),
		}.Build()

		task := &task{
			tenant: tenant,
		}

		added := task.addFinalizer()
		Expect(added).To(BeTrue())
		Expect(tenant.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.TenantLifecycle))
	})

	It("should not add finalizer if already present", func() {
		tenant := privatev1.Tenant_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
			}.Build(),
		}.Build()

		task := &task{
			tenant: tenant,
		}

		added := task.addFinalizer()
		Expect(added).To(BeFalse())
		Expect(tenant.GetMetadata().GetFinalizers()).To(HaveLen(2))
	})

	It("should return immediately after adding finalizer", func() {
		tenant := privatev1.Tenant_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: "tenant-1",
			}.Build(),
		}.Build()

		r := &function{
			maskCalculator: masks.NewCalculator().Build(),
			tenantsClient: finalizerClient{update: func(req *privatev1.TenantsUpdateRequest) (*privatev1.TenantsUpdateResponse, error) {
				Expect(req.GetLock()).To(BeTrue())
				return privatev1.TenantsUpdateResponse_builder{Object: req.GetObject()}.Build(), nil
			}},
		}

		err := r.Run(context.Background(), tenant)
		Expect(err).ToNot(HaveOccurred())

		Expect(tenant.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.TenantLifecycle))
		Expect(tenant.HasStatus()).To(BeFalse())
	})
})

var _ = Describe("Default Values", func() {
	It("should set default status if missing", func() {
		tenant := privatev1.Tenant_builder{}.Build()

		task := &task{
			tenant: tenant,
		}

		task.setDefaults()
		Expect(tenant.HasStatus()).To(BeTrue())
		Expect(tenant.GetStatus().GetState()).To(Equal(privatev1.TenantState_TENANT_STATE_PENDING))
	})

	It("should set default state if unspecified", func() {
		tenant := privatev1.Tenant_builder{
			Status: privatev1.TenantStatus_builder{}.Build(),
		}.Build()

		task := &task{
			tenant: tenant,
		}

		task.setDefaults()
		Expect(tenant.GetStatus().GetState()).To(Equal(privatev1.TenantState_TENANT_STATE_PENDING))
	})

	It("should not override existing state", func() {
		tenant := privatev1.Tenant_builder{
			Status: privatev1.TenantStatus_builder{
				State: privatev1.TenantState_TENANT_STATE_SYNCED,
			}.Build(),
		}.Build()

		task := &task{
			tenant: tenant,
		}

		task.setDefaults()
		Expect(tenant.GetStatus().GetState()).To(Equal(privatev1.TenantState_TENANT_STATE_SYNCED))
	})
})

var _ = Describe("IDP Sync", func() {
	var (
		ctx        context.Context
		ctrl       *gomock.Controller
		mockClient *idp.MockClientInterface
		idpManager *idp.TenantManager
		reconciler *function
	)

	BeforeEach(func() {
		var err error
		ctx = context.Background()
		ctrl = gomock.NewController(GinkgoT())
		mockClient = idp.NewMockClientInterface(ctrl)

		idpManager, err = idp.NewTenantManager().
			SetLogger(logger).
			SetClient(mockClient).
			Build()
		Expect(err).ToNot(HaveOccurred())

		reconciler = &function{
			logger:     logger,
			idpManager: idpManager,
		}
	})

	It("should sync tenant to IDP successfully", func() {
		tenant := privatev1.Tenant_builder{
			Id: "org-123",
			Metadata: privatev1.Metadata_builder{
				Name:       "test-org",
				Finalizers: []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
				Tenant:     "tenant-1",
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				BreakGlassCredentials: privatev1.BreakGlassCredentials_builder{
					Username: "test-org-osac-break-glass",
					Password: testPreGeneratedPassword,
				}.Build(),
			}.Build(),
		}.Build()

		mockClient.EXPECT().
			CreateTenant(gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, org *idp.Tenant) (*idp.Tenant, error) {
				Expect(org.Name).To(Equal("test-org"))
				Expect(org.Enabled).To(BeTrue())
				return &idp.Tenant{
					Name:    "test-org",
					Enabled: true,
				}, nil
			}).
			Times(1)

		mockClient.EXPECT().
			CreateUser(gomock.Any(), "test-org", gomock.Any()).
			DoAndReturn(func(ctx context.Context, orgName string, user *idp.User) (*idp.User, error) {
				Expect(user.Username).To(Equal("test-org-osac-break-glass"))
				Expect(user.Email).To(Equal("break-glass@test-org.osac.local"))
				Expect(user.Credentials).To(HaveLen(1))
				Expect(user.Credentials[0].Value).To(Equal(testPreGeneratedPassword))
				user.ID = "user-123"
				return user, nil
			}).
			Times(1)

		mockClient.EXPECT().
			AssignIdpManagerPermissions(gomock.Any(), "user-123").
			Return(nil).
			Times(1)

		task := &task{
			r:      reconciler,
			tenant: tenant,
		}

		err := task.update(ctx)
		Expect(err).ToNot(HaveOccurred())

		Expect(tenant.GetStatus().GetState()).To(Equal(privatev1.TenantState_TENANT_STATE_SYNCED))
		Expect(tenant.GetStatus().GetIdpTenantName()).To(Equal("test-org"))
		Expect(tenant.GetStatus().GetBreakGlassUserId()).To(Equal("user-123"))
		Expect(tenant.GetStatus().HasBreakGlassCredentials()).To(BeFalse())
	})

	It("should set state to PENDING before sync", func() {
		tenant := privatev1.Tenant_builder{
			Metadata: privatev1.Metadata_builder{
				Name:       "test-org",
				Finalizers: []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
				Tenant:     "tenant-1",
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				BreakGlassCredentials: privatev1.BreakGlassCredentials_builder{
					Username: "test-org-osac-break-glass",
					Password: testPreGeneratedPassword,
				}.Build(),
			}.Build(),
		}.Build()

		mockClient.EXPECT().
			CreateTenant(gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, org *idp.Tenant) (*idp.Tenant, error) {
				Expect(tenant.GetStatus().GetState()).To(Equal(privatev1.TenantState_TENANT_STATE_PENDING))
				return org, nil
			}).
			Times(1)

		mockClient.EXPECT().
			CreateUser(gomock.Any(), "test-org", gomock.Any()).
			Return(&idp.User{ID: "user-123"}, nil).
			Times(1)

		mockClient.EXPECT().
			AssignIdpManagerPermissions(gomock.Any(), "user-123").
			Return(nil).
			Times(1)

		task := &task{
			r:      reconciler,
			tenant: tenant,
		}

		err := task.update(ctx)
		Expect(err).ToNot(HaveOccurred())
	})

	It("should set FAILED state on IDP error", func() {
		tenant := privatev1.Tenant_builder{
			Metadata: privatev1.Metadata_builder{
				Name:       "test-org",
				Finalizers: []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
				Tenant:     "tenant-1",
			}.Build(),
		}.Build()

		mockClient.EXPECT().
			CreateTenant(gomock.Any(), gomock.Any()).
			Return(nil, fmt.Errorf("IDP connection timeout")).
			Times(1)

		task := &task{
			r:      reconciler,
			tenant: tenant,
		}

		err := task.update(ctx)
		Expect(err).ToNot(HaveOccurred())

		Expect(tenant.GetStatus().GetState()).To(Equal(privatev1.TenantState_TENANT_STATE_FAILED))
		Expect(tenant.GetStatus().GetMessage()).To(ContainSubstring("Tenant creation in IDP failed"))
		Expect(tenant.GetStatus().GetMessage()).To(ContainSubstring("IDP connection timeout"))
		Expect(tenant.GetStatus().GetIdpTenantName()).To(BeEmpty())
		Expect(tenant.GetStatus().GetBreakGlassUserId()).To(BeEmpty())
	})

	It("should not return error on IDP failure", func() {
		tenant := privatev1.Tenant_builder{
			Metadata: privatev1.Metadata_builder{
				Name:       "test-org",
				Finalizers: []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
				Tenant:     "tenant-1",
			}.Build(),
		}.Build()

		mockClient.EXPECT().
			CreateTenant(gomock.Any(), gomock.Any()).
			Return(nil, fmt.Errorf("tenant already exists")).
			Times(1)

		task := &task{
			r:      reconciler,
			tenant: tenant,
		}

		err := task.update(ctx)
		Expect(err).ToNot(HaveOccurred())
	})

	It("should create builtin tenants as disabled", func() {
		tenant := privatev1.Tenant_builder{
			Id: "org-shared",
			Metadata: privatev1.Metadata_builder{
				Name:       auth.SharedTenant,
				Finalizers: []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
				Tenant:     "tenant-1",
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				BreakGlassCredentials: privatev1.BreakGlassCredentials_builder{
					Username: auth.SharedTenant + "-osac-break-glass",
					Password: testPreGeneratedPassword,
				}.Build(),
			}.Build(),
		}.Build()

		mockClient.EXPECT().
			CreateTenant(gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, org *idp.Tenant) (*idp.Tenant, error) {
				Expect(org.Name).To(Equal(auth.SharedTenant))
				Expect(org.Enabled).To(BeFalse())
				return &idp.Tenant{
					Name:    auth.SharedTenant,
					Enabled: false,
				}, nil
			}).
			Times(1)

		mockClient.EXPECT().
			CreateUser(gomock.Any(), auth.SharedTenant, gomock.Any()).
			Return(&idp.User{ID: "user-shared"}, nil).
			Times(1)

		mockClient.EXPECT().
			AssignIdpManagerPermissions(gomock.Any(), "user-shared").
			Return(nil).
			Times(1)

		task := &task{
			r:      reconciler,
			tenant: tenant,
		}

		err := task.update(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(tenant.GetStatus().GetState()).To(Equal(privatev1.TenantState_TENANT_STATE_SYNCED))
	})

	It("should create system tenant as disabled", func() {
		tenant := privatev1.Tenant_builder{
			Id: "org-system",
			Metadata: privatev1.Metadata_builder{
				Name:       auth.SystemTenant,
				Finalizers: []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
				Tenant:     "tenant-1",
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				BreakGlassCredentials: privatev1.BreakGlassCredentials_builder{
					Username: auth.SystemTenant + "-osac-break-glass",
					Password: testPreGeneratedPassword,
				}.Build(),
			}.Build(),
		}.Build()

		mockClient.EXPECT().
			CreateTenant(gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, org *idp.Tenant) (*idp.Tenant, error) {
				Expect(org.Name).To(Equal(auth.SystemTenant))
				Expect(org.Enabled).To(BeFalse())
				return &idp.Tenant{
					Name:    auth.SystemTenant,
					Enabled: false,
				}, nil
			}).
			Times(1)

		mockClient.EXPECT().
			CreateUser(gomock.Any(), auth.SystemTenant, gomock.Any()).
			Return(&idp.User{ID: "user-system"}, nil).
			Times(1)

		mockClient.EXPECT().
			AssignIdpManagerPermissions(gomock.Any(), "user-system").
			Return(nil).
			Times(1)

		task := &task{
			r:      reconciler,
			tenant: tenant,
		}

		err := task.update(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(tenant.GetStatus().GetState()).To(Equal(privatev1.TenantState_TENANT_STATE_SYNCED))
	})

	It("should restore SYNCED without re-creating when IDP tenant already exists", func() {
		tenant := privatev1.Tenant_builder{
			Id: "org-123",
			Metadata: privatev1.Metadata_builder{
				Name:       "test-org",
				Finalizers: []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
				Tenant:     "tenant-1",
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				State:            privatev1.TenantState_TENANT_STATE_PENDING,
				IdpTenantName:    "test-org",
				BreakGlassUserId: "user-123",
				Message:          new("previous hub sync failure"),
			}.Build(),
		}.Build()

		task := &task{
			r:      reconciler,
			tenant: tenant,
		}

		err := task.update(ctx)
		Expect(err).ToNot(HaveOccurred())

		Expect(tenant.GetStatus().GetState()).To(Equal(privatev1.TenantState_TENANT_STATE_SYNCED))
		Expect(tenant.GetStatus().HasMessage()).To(BeFalse())
		Expect(tenant.GetStatus().GetIdpTenantName()).To(Equal("test-org"))
		Expect(tenant.GetStatus().GetBreakGlassUserId()).To(Equal("user-123"))
	})

	It("should pass domains to IDP during initial sync", func() {
		tenant := privatev1.Tenant_builder{
			Id: "org-domains",
			Metadata: privatev1.Metadata_builder{
				Name: "domain-org",
				Finalizers: []string{
					finalizers.TenantLifecycle,
					finalizers.TenantOnboarding,
				},
				Tenant: "tenant-1",
			}.Build(),
			Spec: privatev1.TenantSpec_builder{
				Domains: []string{
					"example.com",
					"corp.example.org",
				},
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				BreakGlassCredentials: privatev1.BreakGlassCredentials_builder{
					Username: "domain-org-osac-break-glass",
					Password: testPreGeneratedPassword,
				}.Build(),
			}.Build(),
		}.Build()

		mockClient.EXPECT().
			CreateTenant(gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, org *idp.Tenant) (*idp.Tenant, error) {
				Expect(org.Domains).To(ConsistOf(
					"example.com",
					"corp.example.org",
				))
				return &idp.Tenant{
					Name:    "domain-org",
					Enabled: true,
					Domains: org.Domains,
				}, nil
			}).
			Times(1)

		mockClient.EXPECT().
			CreateUser(gomock.Any(), "domain-org", gomock.Any()).
			Return(&idp.User{ID: "user-domains"}, nil).
			Times(1)

		mockClient.EXPECT().
			AssignIdpManagerPermissions(gomock.Any(), "user-domains").
			Return(nil).
			Times(1)

		task := &task{
			r:      reconciler,
			tenant: tenant,
		}

		err := task.update(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(tenant.GetStatus().GetState()).To(
			Equal(privatev1.TenantState_TENANT_STATE_SYNCED),
		)
	})

	It("should update domains in IDP for synced tenant", func() {
		tenant := privatev1.Tenant_builder{
			Id: "org-update",
			Metadata: privatev1.Metadata_builder{
				Name: "update-org",
				Finalizers: []string{
					finalizers.TenantLifecycle,
					finalizers.TenantOnboarding,
				},
				Tenant: "tenant-1",
			}.Build(),
			Spec: privatev1.TenantSpec_builder{
				Domains: []string{
					"new.example.com",
					"new.corp.example.org",
				},
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				State:            privatev1.TenantState_TENANT_STATE_SYNCED,
				IdpTenantName:    "update-org",
				BreakGlassUserId: "user-update",
			}.Build(),
		}.Build()

		mockClient.EXPECT().
			GetTenant(gomock.Any(), "update-org").
			Return(&idp.Tenant{
				Name:    "update-org",
				Enabled: true,
				Domains: []string{
					"old.example.com",
				},
			}, nil).
			Times(1)

		mockClient.EXPECT().
			UpdateTenant(gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, org *idp.Tenant) (*idp.Tenant, error) {
				Expect(org.Domains).To(ConsistOf(
					"new.example.com",
					"new.corp.example.org",
				))
				return org, nil
			}).
			Times(1)

		task := &task{
			r:      reconciler,
			tenant: tenant,
		}

		err := task.update(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(tenant.GetStatus().GetState()).To(
			Equal(privatev1.TenantState_TENANT_STATE_SYNCED),
		)
	})
})

var _ = Describe("Break-glass credentials secret resolution", func() {
	var (
		ctx        context.Context
		ctrl       *gomock.Controller
		mockClient *idp.MockClientInterface
		idpManager *idp.TenantManager
		reconciler *function
	)

	BeforeEach(func() {
		var err error
		ctx = context.Background()
		ctrl = gomock.NewController(GinkgoT())
		mockClient = idp.NewMockClientInterface(ctrl)

		idpManager, err = idp.NewTenantManager().
			SetLogger(logger).
			SetClient(mockClient).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	It("should resolve the password from the referenced Secret", func() {
		secrets := &mockSecretsClient{
			getFunc: func(_ context.Context, req *privatev1.SecretsGetRequest) (*privatev1.SecretsGetResponse, error) {
				Expect(req.GetId()).To(Equal("secret-id"))
				return privatev1.SecretsGetResponse_builder{
					Object: privatev1.Secret_builder{
						Id: "secret-id",
						Data: map[string][]byte{
							"username": []byte("test-org-osac-break-glass"),
							"password": []byte(testSecretPassword),
						},
					}.Build(),
				}.Build(), nil
			},
		}
		reconciler = &function{
			logger:        logger,
			idpManager:    idpManager,
			secretsClient: secrets,
		}

		tenant := privatev1.Tenant_builder{
			Id: "org-123",
			Metadata: privatev1.Metadata_builder{
				Name:       "test-org",
				Finalizers: []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
				Tenant:     "tenant-1",
			}.Build(),
			Spec: privatev1.TenantSpec_builder{
				BreakGlassCredentialsSecret: privatev1.SecretLocalReference_builder{
					Id:   "secret-id",
					Name: "break-glass-credentials",
				}.Build(),
			}.Build(),
		}.Build()

		mockClient.EXPECT().
			CreateTenant(gomock.Any(), gomock.Any()).
			Return(&idp.Tenant{Name: "test-org", Enabled: true}, nil)
		mockClient.EXPECT().
			CreateUser(gomock.Any(), "test-org", gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, user *idp.User) (*idp.User, error) {
				Expect(user.Credentials).To(HaveLen(1))
				Expect(user.Credentials[0].Value).To(Equal(testSecretPassword))
				user.ID = "user-123"
				return user, nil
			})
		mockClient.EXPECT().
			AssignIdpManagerPermissions(gomock.Any(), "user-123").
			Return(nil)

		task := &task{r: reconciler, tenant: tenant}
		Expect(task.update(ctx)).To(Succeed())
		Expect(tenant.GetStatus().GetBreakGlassUserId()).To(Equal("user-123"))
	})

	It("should prefer inline status credentials over the Secret reference", func() {
		reconciler = &function{
			logger:     logger,
			idpManager: idpManager,
			secretsClient: &mockSecretsClient{
				getFunc: func(context.Context, *privatev1.SecretsGetRequest) (*privatev1.SecretsGetResponse, error) {
					Fail("Secrets.Get should not be called when status credentials are present")
					return nil, nil
				},
			},
		}

		tenant := privatev1.Tenant_builder{
			Id: "org-123",
			Metadata: privatev1.Metadata_builder{
				Name:       "test-org",
				Finalizers: []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
				Tenant:     "tenant-1",
			}.Build(),
			Spec: privatev1.TenantSpec_builder{
				BreakGlassCredentialsSecret: privatev1.SecretLocalReference_builder{
					Id: "secret-id",
				}.Build(),
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				BreakGlassCredentials: privatev1.BreakGlassCredentials_builder{
					Username: "test-org-osac-break-glass",
					Password: testInlinePassword,
				}.Build(),
			}.Build(),
		}.Build()

		mockClient.EXPECT().
			CreateTenant(gomock.Any(), gomock.Any()).
			Return(&idp.Tenant{Name: "test-org", Enabled: true}, nil)
		mockClient.EXPECT().
			CreateUser(gomock.Any(), "test-org", gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, user *idp.User) (*idp.User, error) {
				Expect(user.Credentials[0].Value).To(Equal(testInlinePassword))
				user.ID = "user-123"
				return user, nil
			})
		mockClient.EXPECT().
			AssignIdpManagerPermissions(gomock.Any(), "user-123").
			Return(nil)

		task := &task{r: reconciler, tenant: tenant}
		Expect(task.update(ctx)).To(Succeed())
	})

	It("should return an error when the Secret is missing data[\"password\"]", func() {
		reconciler = &function{
			logger:     logger,
			idpManager: idpManager,
			secretsClient: &mockSecretsClient{
				getFunc: func(context.Context, *privatev1.SecretsGetRequest) (*privatev1.SecretsGetResponse, error) {
					return privatev1.SecretsGetResponse_builder{
						Object: privatev1.Secret_builder{
							Id:   "secret-id",
							Data: map[string][]byte{"username": []byte("only-user")},
						}.Build(),
					}.Build(), nil
				},
			},
		}

		tenant := privatev1.Tenant_builder{
			Metadata: privatev1.Metadata_builder{
				Name:       "test-org",
				Finalizers: []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
				Tenant:     "tenant-1",
			}.Build(),
			Spec: privatev1.TenantSpec_builder{
				BreakGlassCredentialsSecret: privatev1.SecretLocalReference_builder{
					Id: "secret-id",
				}.Build(),
			}.Build(),
		}.Build()

		task := &task{r: reconciler, tenant: tenant}
		err := task.update(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(`missing data["password"]`))
	})

	It("should store the break-glass username as a Secret after IDP sync", func() {
		secrets := &mockSecretsClient{
			createFunc: func(_ context.Context, req *privatev1.SecretsCreateRequest) (*privatev1.SecretsCreateResponse, error) {
				secret := req.GetObject()
				Expect(secret.GetMetadata().GetName()).To(Equal("break-glass-credentials"))
				Expect(secret.GetMetadata().GetTenant()).To(Equal("test-org"))
				Expect(secret.GetType()).To(Equal(privatev1.SecretType_SECRET_TYPE_OPAQUE))
				Expect(secret.GetData()).To(HaveKeyWithValue("username", []byte("test-org-osac-break-glass")))
				Expect(secret.GetData()).ToNot(HaveKey("password"))
				secret.SetId("created-secret-id")
				return privatev1.SecretsCreateResponse_builder{Object: secret}.Build(), nil
			},
		}
		reconciler = &function{
			logger:        logger,
			idpManager:    idpManager,
			secretsClient: secrets,
		}

		tenant := privatev1.Tenant_builder{
			Id: "org-123",
			Metadata: privatev1.Metadata_builder{
				Name:       "test-org",
				Finalizers: []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
				Tenant:     "tenant-1",
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				BreakGlassCredentials: privatev1.BreakGlassCredentials_builder{
					Username: "test-org-osac-break-glass",
					Password: testPreGeneratedPassword,
				}.Build(),
			}.Build(),
		}.Build()

		mockClient.EXPECT().
			CreateTenant(gomock.Any(), gomock.Any()).
			Return(&idp.Tenant{Name: "test-org", Enabled: true}, nil)
		mockClient.EXPECT().
			CreateUser(gomock.Any(), "test-org", gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, user *idp.User) (*idp.User, error) {
				user.ID = "user-123"
				return user, nil
			})
		mockClient.EXPECT().
			AssignIdpManagerPermissions(gomock.Any(), "user-123").
			Return(nil)

		task := &task{r: reconciler, tenant: tenant}
		Expect(task.update(ctx)).To(Succeed())
		ref := tenant.GetSpec().GetBreakGlassCredentialsSecret()
		Expect(ref).ToNot(BeNil())
		Expect(ref.GetId()).To(Equal("created-secret-id"))
		Expect(ref.GetName()).To(Equal("break-glass-credentials"))
		Expect(tenant.GetStatus().HasBreakGlassCredentials()).To(BeFalse())
	})

	It("should adopt an existing Secret when Create returns AlreadyExists", func() {
		secrets := &mockSecretsClient{
			createFunc: func(context.Context, *privatev1.SecretsCreateRequest) (*privatev1.SecretsCreateResponse, error) {
				return nil, grpcstatus.Error(grpccodes.AlreadyExists,
					"secret with name 'break-glass-credentials' already exists")
			},
			listFunc: func(_ context.Context, req *privatev1.SecretsListRequest) (*privatev1.SecretsListResponse, error) {
				Expect(req.GetFilter()).To(ContainSubstring("break-glass-credentials"))
				Expect(req.GetFilter()).To(ContainSubstring("test-org"))
				return privatev1.SecretsListResponse_builder{
					Items: []*privatev1.Secret{
						privatev1.Secret_builder{
							Id: "existing-secret-id",
							Metadata: privatev1.Metadata_builder{
								Name:   "break-glass-credentials",
								Tenant: "test-org",
							}.Build(),
						}.Build(),
					},
				}.Build(), nil
			},
		}
		reconciler = &function{
			logger:        logger,
			idpManager:    idpManager,
			secretsClient: secrets,
		}

		tenant := privatev1.Tenant_builder{
			Id: "org-123",
			Metadata: privatev1.Metadata_builder{
				Name:       "test-org",
				Finalizers: []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
				Tenant:     "tenant-1",
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				BreakGlassCredentials: privatev1.BreakGlassCredentials_builder{
					Username: "test-org-osac-break-glass",
					Password: testPreGeneratedPassword,
				}.Build(),
			}.Build(),
		}.Build()

		mockClient.EXPECT().
			CreateTenant(gomock.Any(), gomock.Any()).
			Return(&idp.Tenant{Name: "test-org", Enabled: true}, nil)
		mockClient.EXPECT().
			CreateUser(gomock.Any(), "test-org", gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, user *idp.User) (*idp.User, error) {
				user.ID = "user-123"
				return user, nil
			})
		mockClient.EXPECT().
			AssignIdpManagerPermissions(gomock.Any(), "user-123").
			Return(nil)

		task := &task{r: reconciler, tenant: tenant}
		Expect(task.update(ctx)).To(Succeed())
		ref := tenant.GetSpec().GetBreakGlassCredentialsSecret()
		Expect(ref).ToNot(BeNil())
		Expect(ref.GetId()).To(Equal("existing-secret-id"))
		Expect(ref.GetName()).To(Equal("break-glass-credentials"))
		Expect(tenant.GetStatus().GetIdpTenantName()).To(Equal("test-org"))
		Expect(tenant.GetStatus().GetState()).To(Equal(privatev1.TenantState_TENANT_STATE_SYNCED))
	})

	It("should keep idpTenantName when persist fails so CreateTenant is not retried", func() {
		secrets := &mockSecretsClient{
			createFunc: func(context.Context, *privatev1.SecretsCreateRequest) (*privatev1.SecretsCreateResponse, error) {
				return nil, fmt.Errorf("secrets service unavailable")
			},
		}
		reconciler = &function{
			logger:        logger,
			idpManager:    idpManager,
			secretsClient: secrets,
		}

		tenant := privatev1.Tenant_builder{
			Id: "org-123",
			Metadata: privatev1.Metadata_builder{
				Name:       "test-org",
				Finalizers: []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
				Tenant:     "tenant-1",
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				BreakGlassCredentials: privatev1.BreakGlassCredentials_builder{
					Username: "test-org-osac-break-glass",
					Password: testPreGeneratedPassword,
				}.Build(),
			}.Build(),
		}.Build()

		mockClient.EXPECT().
			CreateTenant(gomock.Any(), gomock.Any()).
			Return(&idp.Tenant{Name: "test-org", Enabled: true}, nil)
		mockClient.EXPECT().
			CreateUser(gomock.Any(), "test-org", gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, user *idp.User) (*idp.User, error) {
				user.ID = "user-123"
				return user, nil
			})
		mockClient.EXPECT().
			AssignIdpManagerPermissions(gomock.Any(), "user-123").
			Return(nil)

		task := &task{r: reconciler, tenant: tenant}
		Expect(task.update(ctx)).To(Succeed())
		Expect(tenant.GetStatus().GetIdpTenantName()).To(Equal("test-org"))
		Expect(tenant.GetStatus().GetBreakGlassUserId()).To(Equal("user-123"))
		Expect(tenant.GetStatus().GetState()).To(Equal(privatev1.TenantState_TENANT_STATE_PENDING))
		Expect(tenant.GetSpec().GetBreakGlassCredentialsSecret()).To(BeNil())
	})
})

var _ = Describe("Builtin Tenant Detection", func() {
	It("should return true for the shared tenant", func() {
		tenant := privatev1.Tenant_builder{
			Metadata: privatev1.Metadata_builder{
				Name: auth.SharedTenant,
			}.Build(),
		}.Build()

		task := &task{tenant: tenant}
		Expect(task.isBuiltin()).To(BeTrue())
	})

	It("should return true for the system tenant", func() {
		tenant := privatev1.Tenant_builder{
			Metadata: privatev1.Metadata_builder{
				Name: auth.SystemTenant,
			}.Build(),
		}.Build()

		task := &task{tenant: tenant}
		Expect(task.isBuiltin()).To(BeTrue())
	})

	It("should return false for a regular tenant", func() {
		tenant := privatev1.Tenant_builder{
			Metadata: privatev1.Metadata_builder{
				Name: "my-org",
			}.Build(),
		}.Build()

		task := &task{tenant: tenant}
		Expect(task.isBuiltin()).To(BeFalse())
	})
})

var _ = Describe("Deletion", func() {
	var (
		ctx                context.Context
		ctrl               *gomock.Controller
		mockClient         *idp.MockClientInterface
		mockProjectsClient *MockProjectsClient
		idpManager         *idp.TenantManager
		reconciler         *function
	)

	BeforeEach(func() {
		var err error
		ctx = context.Background()
		ctrl = gomock.NewController(GinkgoT())
		mockClient = idp.NewMockClientInterface(ctrl)
		mockProjectsClient = NewMockProjectsClient(ctrl)

		idpManager, err = idp.NewTenantManager().
			SetLogger(logger).
			SetClient(mockClient).
			Build()
		Expect(err).ToNot(HaveOccurred())

		reconciler = &function{
			logger:         logger,
			projectsClient: mockProjectsClient,
			idpManager:     idpManager,
		}
	})

	It("should delete tenant from IDP and remove finalizer", func() {
		deletionTimestamp := timestamppb.New(time.Now())
		tenant := privatev1.Tenant_builder{
			Id: "org-123",
			Metadata: privatev1.Metadata_builder{
				Name:              "test-org",
				Finalizers:        []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
				DeletionTimestamp: deletionTimestamp,
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				State:            privatev1.TenantState_TENANT_STATE_SYNCED,
				IdpTenantName:    "test-org",
				BreakGlassUserId: "user-123",
			}.Build(),
		}.Build()

		mockProjectsClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(privatev1.ProjectsListResponse_builder{Total: 0}.Build(), nil).
			Times(2)

		mockClient.EXPECT().
			DeleteTenant(gomock.Any(), "test-org").
			Return(nil).
			Times(1)

		task := &task{
			r:      reconciler,
			tenant: tenant,
		}

		err := task.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(tenant.GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.TenantLifecycle))
	})

	It("deletes default networking before waiting for tenant projects", func() {
		deletionTimestamp := timestamppb.New(time.Now())
		tenant := privatev1.Tenant_builder{
			Id: "org-123",
			Metadata: privatev1.Metadata_builder{
				Name:              "test-org",
				Finalizers:        []string{finalizers.TenantLifecycle},
				DeletionTimestamp: deletionTimestamp,
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				State:         privatev1.TenantState_TENANT_STATE_SYNCED,
				IdpTenantName: "test-org",
			}.Build(),
		}.Build()

		defaultNetworkingDeleted := false
		reconciler.defaultNetwork = testDefaultNetworkingLifecycleManager{
			deleteFunc: func(_ context.Context, tenantName string) error {
				Expect(tenantName).To(Equal("test-org"))
				defaultNetworkingDeleted = true
				return nil
			},
		}

		projectList := func() *gomock.Call {
			return mockProjectsClient.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(
				func(context.Context, *privatev1.ProjectsListRequest, ...grpc.CallOption) (*privatev1.ProjectsListResponse, error) {
					Expect(defaultNetworkingDeleted).To(BeTrue())
					return privatev1.ProjectsListResponse_builder{Total: 0}.Build(), nil
				})
		}
		gomock.InOrder(projectList(), projectList())
		mockClient.EXPECT().DeleteTenant(gomock.Any(), "test-org").Return(nil)

		task := &task{r: reconciler, tenant: tenant}
		Expect(task.delete(ctx)).To(Succeed())
		Expect(defaultNetworkingDeleted).To(BeTrue())
		Expect(tenant.GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.TenantLifecycle))
	})

	It("deletes tenant users before requesting project deletion", func() {
		deletionTimestamp := timestamppb.New(time.Now())
		tenant := privatev1.Tenant_builder{
			Id: "org-123",
			Metadata: privatev1.Metadata_builder{
				Name:              "test-org",
				Finalizers:        []string{finalizers.TenantLifecycle},
				DeletionTimestamp: deletionTimestamp,
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				State:         privatev1.TenantState_TENANT_STATE_SYNCED,
				IdpTenantName: "test-org",
			}.Build(),
		}.Build()
		user := privatev1.User_builder{
			Id: "user-456",
			Metadata: privatev1.Metadata_builder{
				Name:   "test-user",
				Tenant: "test-org",
			}.Build(),
		}.Build()
		deletingUser := privatev1.User_builder{
			Id: "user-789",
			Metadata: privatev1.Metadata_builder{
				Name:              "deleting-user",
				Tenant:            "test-org",
				DeletionTimestamp: timestamppb.New(time.Now()),
			}.Build(),
		}.Build()
		mockUsersClient := NewMockUsersClient(ctrl)
		reconciler.usersClient = mockUsersClient

		userList := mockUsersClient.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, req *privatev1.UsersListRequest, _ ...grpc.CallOption) (*privatev1.UsersListResponse, error) {
				Expect(req.GetFilter()).To(Equal(`this.metadata.tenant == "test-org"`))
				return privatev1.UsersListResponse_builder{
					Size:  2,
					Total: 2,
					Items: []*privatev1.User{user, deletingUser},
				}.Build(), nil
			})
		userDelete := mockUsersClient.EXPECT().Delete(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, req *privatev1.UsersDeleteRequest, _ ...grpc.CallOption) (*privatev1.UsersDeleteResponse, error) {
				Expect(req.GetId()).To(Equal("user-456"))
				return privatev1.UsersDeleteResponse_builder{}.Build(), nil
			})
		userSignal := mockUsersClient.EXPECT().Signal(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, req *privatev1.UsersSignalRequest, _ ...grpc.CallOption) (*privatev1.UsersSignalResponse, error) {
				Expect(req.GetId()).To(Equal("user-789"))
				return privatev1.UsersSignalResponse_builder{}.Build(), nil
			})
		projectList := func() *gomock.Call {
			return mockProjectsClient.EXPECT().
				List(gomock.Any(), gomock.Any()).
				Return(privatev1.ProjectsListResponse_builder{Total: 0}.Build(), nil)
		}
		firstProjectList := projectList()
		remainingProjectList := projectList()
		deleteIDPTenant := mockClient.EXPECT().DeleteTenant(gomock.Any(), "test-org").Return(nil)
		gomock.InOrder(userList, userDelete, userSignal, firstProjectList,
			remainingProjectList, deleteIDPTenant)

		task := &task{r: reconciler, tenant: tenant}
		Expect(task.delete(ctx)).To(Succeed())
		Expect(tenant.GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.TenantLifecycle))
	})

	It("should delete IDP tenant by metadata name when not synced", func() {
		deletionTimestamp := timestamppb.New(time.Now())
		tenant := privatev1.Tenant_builder{
			Id: "org-123",
			Metadata: privatev1.Metadata_builder{
				Name:              "test-org",
				Finalizers:        []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
				DeletionTimestamp: deletionTimestamp,
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				State: privatev1.TenantState_TENANT_STATE_PENDING,
			}.Build(),
		}.Build()

		mockProjectsClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(privatev1.ProjectsListResponse_builder{Total: 0}.Build(), nil).
			Times(2)

		mockClient.EXPECT().
			DeleteTenant(gomock.Any(), "test-org").
			Return(nil).
			Times(1)

		task := &task{
			r:      reconciler,
			tenant: tenant,
		}

		err := task.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(tenant.GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.TenantLifecycle))
	})

	It("should delete IDP tenant by metadata name when idp_tenant_name is empty", func() {
		deletionTimestamp := timestamppb.New(time.Now())
		tenant := privatev1.Tenant_builder{
			Id: "org-123",
			Metadata: privatev1.Metadata_builder{
				Name:              "test-org",
				Finalizers:        []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
				DeletionTimestamp: deletionTimestamp,
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				State:         privatev1.TenantState_TENANT_STATE_SYNCED,
				IdpTenantName: "",
			}.Build(),
		}.Build()

		mockProjectsClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(privatev1.ProjectsListResponse_builder{Total: 0}.Build(), nil).
			Times(2)

		mockClient.EXPECT().
			DeleteTenant(gomock.Any(), "test-org").
			Return(nil).
			Times(1)

		task := &task{
			r:      reconciler,
			tenant: tenant,
		}

		err := task.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(tenant.GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.TenantLifecycle))
	})

	It("should delete the break-glass secret before waiting for remaining projects", func() {
		deletionTimestamp := timestamppb.New(time.Now())
		tenant := privatev1.Tenant_builder{
			Id: "org-123",
			Metadata: privatev1.Metadata_builder{
				Name:              "test-org",
				Finalizers:        []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
				DeletionTimestamp: deletionTimestamp,
			}.Build(),
			Spec: privatev1.TenantSpec_builder{
				BreakGlassCredentialsSecret: privatev1.SecretLocalReference_builder{
					Id:   "bg-secret-id",
					Name: breakGlassSecretName,
				}.Build(),
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				State:            privatev1.TenantState_TENANT_STATE_SYNCED,
				IdpTenantName:    "test-org",
				BreakGlassUserId: "user-123",
			}.Build(),
		}.Build()

		deleted := false
		secrets := &mockSecretsClient{
			deleteFunc: func(_ context.Context, req *privatev1.SecretsDeleteRequest) (*privatev1.SecretsDeleteResponse, error) {
				Expect(req.GetId()).To(Equal("bg-secret-id"))
				deleted = true
				return &privatev1.SecretsDeleteResponse{}, nil
			},
		}
		reconciler.secretsClient = secrets

		// Tenant project listing (no projects found).
		mockProjectsClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(privatev1.ProjectsListResponse_builder{}.Build(), nil).
			Times(1)
		// countRemainingProjects List call
		mockProjectsClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(privatev1.ProjectsListResponse_builder{
				Total: 1,
			}.Build(), nil).
			Times(1)

		task := &task{
			r:      reconciler,
			tenant: tenant,
		}

		err := task.delete(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("project(s) pending deletion"))
		Expect(deleted).To(BeTrue())
		Expect(tenant.GetSpec().GetBreakGlassCredentialsSecret()).To(BeNil())
		Expect(tenant.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.TenantLifecycle))
	})

	It("should clear the break-glass secret ref so finalizer removal can persist", func() {
		deletionTimestamp := timestamppb.New(time.Now())
		tenant := privatev1.Tenant_builder{
			Id: "org-123",
			Metadata: privatev1.Metadata_builder{
				Name:              "test-org",
				Finalizers:        []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
				DeletionTimestamp: deletionTimestamp,
			}.Build(),
			Spec: privatev1.TenantSpec_builder{
				BreakGlassCredentialsSecret: privatev1.SecretLocalReference_builder{
					Id:   "bg-secret-id",
					Name: breakGlassSecretName,
				}.Build(),
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				State:            privatev1.TenantState_TENANT_STATE_SYNCED,
				IdpTenantName:    "test-org",
				BreakGlassUserId: "user-123",
			}.Build(),
		}.Build()

		secrets := &mockSecretsClient{
			deleteFunc: func(_ context.Context, req *privatev1.SecretsDeleteRequest) (*privatev1.SecretsDeleteResponse, error) {
				Expect(req.GetId()).To(Equal("bg-secret-id"))
				return &privatev1.SecretsDeleteResponse{}, nil
			},
		}
		reconciler.secretsClient = secrets

		// Tenant project listing (no projects found).
		mockProjectsClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(privatev1.ProjectsListResponse_builder{}.Build(), nil).
			Times(1)
		// countRemainingProjects List call
		mockProjectsClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(privatev1.ProjectsListResponse_builder{Total: 0}.Build(), nil).
			Times(1)

		mockClient.EXPECT().
			DeleteTenant(gomock.Any(), "test-org").
			Return(nil).
			Times(1)

		task := &task{
			r:      reconciler,
			tenant: tenant,
		}

		err := task.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(tenant.GetSpec().GetBreakGlassCredentialsSecret()).To(BeNil())
		Expect(tenant.GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.TenantLifecycle))
	})

	It("should return error on IDP deletion failure and keep finalizer", func() {
		deletionTimestamp := timestamppb.New(time.Now())
		tenant := privatev1.Tenant_builder{
			Id: "org-123",
			Metadata: privatev1.Metadata_builder{
				Name:              "test-org",
				Finalizers:        []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
				DeletionTimestamp: deletionTimestamp,
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				State:            privatev1.TenantState_TENANT_STATE_SYNCED,
				IdpTenantName:    "test-org",
				BreakGlassUserId: "user-123",
			}.Build(),
		}.Build()

		mockProjectsClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(privatev1.ProjectsListResponse_builder{Total: 0}.Build(), nil).
			Times(2)

		mockClient.EXPECT().
			DeleteTenant(gomock.Any(), "test-org").
			Return(fmt.Errorf("IDP connection timeout")).
			Times(1)

		task := &task{
			r:      reconciler,
			tenant: tenant,
		}

		err := task.delete(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to delete IDP tenant"))
		Expect(err.Error()).To(ContainSubstring("IDP connection timeout"))
		Expect(tenant.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.TenantLifecycle))
	})

	It("should block deletion when projects remain", func() {
		deletionTimestamp := timestamppb.New(time.Now())
		tenant := privatev1.Tenant_builder{
			Id: "org-123",
			Metadata: privatev1.Metadata_builder{
				Name:              "test-org",
				Finalizers:        []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
				DeletionTimestamp: deletionTimestamp,
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				State:            privatev1.TenantState_TENANT_STATE_SYNCED,
				IdpTenantName:    "test-org",
				BreakGlassUserId: "user-123",
			}.Build(),
		}.Build()

		gomock.InOrder(
			mockProjectsClient.EXPECT().
				List(gomock.Any(), gomock.Any()).
				Return(privatev1.ProjectsListResponse_builder{}.Build(), nil),
			mockProjectsClient.EXPECT().
				List(gomock.Any(), gomock.Any()).
				Return(privatev1.ProjectsListResponse_builder{
					Total: 2,
				}.Build(), nil),
		)

		task := &task{
			r:      reconciler,
			tenant: tenant,
		}

		err := task.delete(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("project(s) pending deletion"))
		retryable, ok := err.(interface{ RequeueAfter() time.Duration })
		Expect(ok).To(BeTrue(), "pending project deletion must trigger a tenant retry")
		Expect(retryable.RequeueAfter()).To(Equal(time.Second))
		Expect(tenant.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.TenantLifecycle))
	})

	It("should return error when project query fails during deletion", func() {
		deletionTimestamp := timestamppb.New(time.Now())
		tenant := privatev1.Tenant_builder{
			Id: "org-123",
			Metadata: privatev1.Metadata_builder{
				Name:              "test-org",
				Finalizers:        []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
				DeletionTimestamp: deletionTimestamp,
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				State:            privatev1.TenantState_TENANT_STATE_SYNCED,
				IdpTenantName:    "test-org",
				BreakGlassUserId: "user-123",
			}.Build(),
		}.Build()

		gomock.InOrder(
			mockProjectsClient.EXPECT().
				List(gomock.Any(), gomock.Any()).
				Return(privatev1.ProjectsListResponse_builder{}.Build(), nil),
			mockProjectsClient.EXPECT().
				List(gomock.Any(), gomock.Any()).
				Return(nil, fmt.Errorf("connection refused")),
		)

		task := &task{
			r:      reconciler,
			tenant: tenant,
		}

		err := task.delete(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to query remaining projects"))
		Expect(tenant.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.TenantLifecycle))
	})

	It("should remove finalizer when called", func() {
		tenant := privatev1.Tenant_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.TenantLifecycle, "other-finalizer"},
			}.Build(),
		}.Build()

		task := &task{
			tenant: tenant,
		}

		task.removeFinalizer()
		Expect(tenant.GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.TenantLifecycle))
		Expect(tenant.GetMetadata().GetFinalizers()).To(ContainElement("other-finalizer"))
	})

	It("should handle removal when finalizer not present", func() {
		tenant := privatev1.Tenant_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{"other-finalizer"},
			}.Build(),
		}.Build()

		task := &task{
			tenant: tenant,
		}

		task.removeFinalizer()
		Expect(tenant.GetMetadata().GetFinalizers()).To(HaveLen(1))
		Expect(tenant.GetMetadata().GetFinalizers()).To(ContainElement("other-finalizer"))
	})
})

var _ = Describe("Project deletion during tenant deletion", func() {
	var (
		ctx                context.Context
		ctrl               *gomock.Controller
		mockClient         *idp.MockClientInterface
		mockProjectsClient *MockProjectsClient
		idpManager         *idp.TenantManager
		reconciler         *function
	)

	BeforeEach(func() {
		var err error
		ctx = context.Background()
		ctrl = gomock.NewController(GinkgoT())
		mockClient = idp.NewMockClientInterface(ctrl)
		mockProjectsClient = NewMockProjectsClient(ctrl)

		idpManager, err = idp.NewTenantManager().
			SetLogger(logger).
			SetClient(mockClient).
			Build()
		Expect(err).ToNot(HaveOccurred())

		reconciler = &function{
			logger:         logger,
			projectsClient: mockProjectsClient,
			idpManager:     idpManager,
		}
	})

	It("should request deletion of every tenant project", func() {
		tenant := privatev1.Tenant_builder{
			Id: "org-root-proj",
			Metadata: privatev1.Metadata_builder{
				Name:              "test-org",
				Finalizers:        []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
				DeletionTimestamp: timestamppb.Now(),
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				State:         privatev1.TenantState_TENANT_STATE_SYNCED,
				IdpTenantName: "test-org",
			}.Build(),
		}.Build()

		rootProject := privatev1.Project_builder{
			Id: "root-proj-id",
			Metadata: privatev1.Metadata_builder{
				Name:   "",
				Tenant: "test-org",
			}.Build(),
		}.Build()
		teamProject := privatev1.Project_builder{
			Id: "team-proj-id",
			Metadata: privatev1.Metadata_builder{
				Name:   "team-a",
				Tenant: "test-org",
			}.Build(),
		}.Build()

		gomock.InOrder(
			mockProjectsClient.EXPECT().
				List(gomock.Any(), gomock.Any()).
				Return(privatev1.ProjectsListResponse_builder{
					Items: []*privatev1.Project{rootProject, teamProject},
					Size:  2,
					Total: 2,
				}.Build(), nil),
			mockProjectsClient.EXPECT().
				Delete(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, request *privatev1.ProjectsDeleteRequest,
					_ ...grpc.CallOption) (*privatev1.ProjectsDeleteResponse, error) {
					Expect(request.GetId()).To(Equal("root-proj-id"))
					return privatev1.ProjectsDeleteResponse_builder{}.Build(), nil
				}),
			mockProjectsClient.EXPECT().
				Delete(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, request *privatev1.ProjectsDeleteRequest,
					_ ...grpc.CallOption) (*privatev1.ProjectsDeleteResponse, error) {
					Expect(request.GetId()).To(Equal("team-proj-id"))
					return privatev1.ProjectsDeleteResponse_builder{}.Build(), nil
				}),
			mockProjectsClient.EXPECT().
				List(gomock.Any(), gomock.Any()).
				Return(privatev1.ProjectsListResponse_builder{Total: 0}.Build(), nil),
		)

		mockClient.EXPECT().
			DeleteTenant(gomock.Any(), "test-org").
			Return(nil)

		t := &task{r: reconciler, tenant: tenant}
		err := t.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(tenant.GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.TenantLifecycle))
	})

	It("should signal a project that already has a deletion timestamp", func() {
		tenant := privatev1.Tenant_builder{
			Id: "org-root-deleting",
			Metadata: privatev1.Metadata_builder{
				Name:              "test-org",
				Finalizers:        []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
				DeletionTimestamp: timestamppb.Now(),
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				State:         privatev1.TenantState_TENANT_STATE_SYNCED,
				IdpTenantName: "test-org",
			}.Build(),
		}.Build()

		rootProject := privatev1.Project_builder{
			Id: "root-proj-id",
			Metadata: privatev1.Metadata_builder{
				Name:              "",
				Tenant:            "test-org",
				DeletionTimestamp: timestamppb.Now(),
			}.Build(),
		}.Build()

		gomock.InOrder(
			mockProjectsClient.EXPECT().
				List(gomock.Any(), gomock.Any()).
				Return(privatev1.ProjectsListResponse_builder{
					Items: []*privatev1.Project{rootProject},
					Size:  1,
					Total: 1,
				}.Build(), nil),
			mockProjectsClient.EXPECT().
				Signal(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, request *privatev1.ProjectsSignalRequest,
					_ ...grpc.CallOption) (*privatev1.ProjectsSignalResponse, error) {
					Expect(request.GetId()).To(Equal("root-proj-id"))
					return nil, grpcstatus.Error(grpccodes.NotFound, "project not found")
				}),
			mockProjectsClient.EXPECT().
				List(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, request *privatev1.ProjectsListRequest, _ ...grpc.CallOption) (*privatev1.ProjectsListResponse, error) {
					Expect(request.GetFilter()).To(Equal(`this.metadata.tenant == "test-org"`))
					return privatev1.ProjectsListResponse_builder{Total: 1}.Build(), nil
				}),
		)

		t := &task{r: reconciler, tenant: tenant}
		err := t.delete(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("project(s) pending deletion"))
	})

	It("should attempt all projects and join pending deletion errors", func() {
		tenant := privatev1.Tenant_builder{
			Id: "org-root-signal-error",
			Metadata: privatev1.Metadata_builder{
				Name:              "test-org",
				Finalizers:        []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
				DeletionTimestamp: timestamppb.Now(),
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				State:         privatev1.TenantState_TENANT_STATE_SYNCED,
				IdpTenantName: "test-org",
			}.Build(),
		}.Build()
		rootProject := privatev1.Project_builder{
			Id: "root-proj-id",
			Metadata: privatev1.Metadata_builder{
				Name:              "",
				Tenant:            "test-org",
				DeletionTimestamp: timestamppb.Now(),
			}.Build(),
		}.Build()
		teamProject := privatev1.Project_builder{
			Id: "team-proj-id",
			Metadata: privatev1.Metadata_builder{
				Name:   "team-a",
				Tenant: "test-org",
			}.Build(),
		}.Build()

		gomock.InOrder(
			mockProjectsClient.EXPECT().
				List(gomock.Any(), gomock.Any()).
				Return(privatev1.ProjectsListResponse_builder{
					Items: []*privatev1.Project{rootProject, teamProject},
					Size:  2,
					Total: 2,
				}.Build(), nil),
			mockProjectsClient.EXPECT().
				Signal(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, request *privatev1.ProjectsSignalRequest,
					_ ...grpc.CallOption) (*privatev1.ProjectsSignalResponse, error) {
					Expect(request.GetId()).To(Equal("root-proj-id"))
					return nil, grpcstatus.Error(grpccodes.Unavailable, "controller unavailable")
				}),
			mockProjectsClient.EXPECT().
				Delete(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, request *privatev1.ProjectsDeleteRequest,
					_ ...grpc.CallOption) (*privatev1.ProjectsDeleteResponse, error) {
					Expect(request.GetId()).To(Equal("team-proj-id"))
					return nil, grpcstatus.Error(grpccodes.PermissionDenied, "delete denied")
				}),
		)

		t := &task{r: reconciler, tenant: tenant}
		err := t.delete(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to signal project root-proj-id for tenant deletion"))
		Expect(err.Error()).To(ContainSubstring("controller unavailable"))
		Expect(err.Error()).To(ContainSubstring("failed to delete project team-proj-id"))
		Expect(err.Error()).To(ContainSubstring("delete denied"))
	})

	It("should return error when tenant project listing fails", func() {
		tenant := privatev1.Tenant_builder{
			Id: "org-root-err",
			Metadata: privatev1.Metadata_builder{
				Name:              "test-org",
				Finalizers:        []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
				DeletionTimestamp: timestamppb.Now(),
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				State:         privatev1.TenantState_TENANT_STATE_SYNCED,
				IdpTenantName: "test-org",
			}.Build(),
		}.Build()

		mockProjectsClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(nil, fmt.Errorf("connection refused"))

		t := &task{r: reconciler, tenant: tenant}
		err := t.delete(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to list projects for tenant deletion"))
	})

	It("should return error when tenant project deletion fails", func() {
		tenant := privatev1.Tenant_builder{
			Id: "org-root-del-err",
			Metadata: privatev1.Metadata_builder{
				Name:              "test-org",
				Finalizers:        []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
				DeletionTimestamp: timestamppb.Now(),
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				State:         privatev1.TenantState_TENANT_STATE_SYNCED,
				IdpTenantName: "test-org",
			}.Build(),
		}.Build()

		rootProject := privatev1.Project_builder{
			Id: "root-proj-id",
			Metadata: privatev1.Metadata_builder{
				Name:   "",
				Tenant: "test-org",
			}.Build(),
		}.Build()
		teamProject := privatev1.Project_builder{
			Id: "team-proj-id",
			Metadata: privatev1.Metadata_builder{
				Name:   "team-a",
				Tenant: "test-org",
			}.Build(),
		}.Build()

		gomock.InOrder(
			mockProjectsClient.EXPECT().
				List(gomock.Any(), gomock.Any()).
				Return(privatev1.ProjectsListResponse_builder{
					Items: []*privatev1.Project{rootProject, teamProject},
					Size:  2,
					Total: 2,
				}.Build(), nil),
			mockProjectsClient.EXPECT().
				Delete(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, request *privatev1.ProjectsDeleteRequest,
					_ ...grpc.CallOption) (*privatev1.ProjectsDeleteResponse, error) {
					Expect(request.GetId()).To(Equal("root-proj-id"))
					return nil, fmt.Errorf("permission denied")
				}),
			mockProjectsClient.EXPECT().
				Delete(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, request *privatev1.ProjectsDeleteRequest,
					_ ...grpc.CallOption) (*privatev1.ProjectsDeleteResponse, error) {
					Expect(request.GetId()).To(Equal("team-proj-id"))
					return privatev1.ProjectsDeleteResponse_builder{}.Build(), nil
				}),
		)

		t := &task{r: reconciler, tenant: tenant}
		err := t.delete(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to delete project root-proj-id"))
	})

	It("should ignore NotFound when requesting tenant project deletion", func() {
		tenant := privatev1.Tenant_builder{
			Id: "org-root-notfound",
			Metadata: privatev1.Metadata_builder{
				Name:              "test-org",
				Finalizers:        []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
				DeletionTimestamp: timestamppb.Now(),
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				State:         privatev1.TenantState_TENANT_STATE_SYNCED,
				IdpTenantName: "test-org",
			}.Build(),
		}.Build()

		rootProject := privatev1.Project_builder{
			Id: "root-proj-id",
			Metadata: privatev1.Metadata_builder{
				Name:   "",
				Tenant: "test-org",
			}.Build(),
		}.Build()

		gomock.InOrder(
			mockProjectsClient.EXPECT().
				List(gomock.Any(), gomock.Any()).
				Return(privatev1.ProjectsListResponse_builder{
					Items: []*privatev1.Project{rootProject},
					Size:  1,
					Total: 1,
				}.Build(), nil),
			mockProjectsClient.EXPECT().
				Delete(gomock.Any(), gomock.Any()).
				Return(nil, grpcstatus.Error(grpccodes.NotFound, "project not found")),
			mockProjectsClient.EXPECT().
				List(gomock.Any(), gomock.Any()).
				Return(privatev1.ProjectsListResponse_builder{Total: 0}.Build(), nil),
		)

		mockClient.EXPECT().
			DeleteTenant(gomock.Any(), "test-org").
			Return(nil)

		t := &task{r: reconciler, tenant: tenant}
		err := t.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(tenant.GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.TenantLifecycle))
	})
})

var _ = Describe("Skip Reconciliation", func() {
	It("should call updateIDP for synced tenants", func() {
		ctrl := gomock.NewController(GinkgoT())
		mockClient := idp.NewMockClientInterface(ctrl)

		idpManager, err := idp.NewTenantManager().
			SetLogger(logger).
			SetClient(mockClient).
			Build()
		Expect(err).ToNot(HaveOccurred())

		reconciler := &function{
			logger:     logger,
			idpManager: idpManager,
		}

		tenant := privatev1.Tenant_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
				Tenant:     "tenant-1",
			}.Build(),
			Spec: privatev1.TenantSpec_builder{
				Domains: []string{"example.com"},
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				State:            privatev1.TenantState_TENANT_STATE_SYNCED,
				IdpTenantName:    "test-org",
				BreakGlassUserId: "user-123",
			}.Build(),
		}.Build()

		mockClient.EXPECT().
			GetTenant(gomock.Any(), "test-org").
			Return(&idp.Tenant{Name: "test-org", Enabled: true}, nil).
			Times(1)

		mockClient.EXPECT().
			UpdateTenant(gomock.Any(), gomock.Any()).
			Return(&idp.Tenant{Name: "test-org", Enabled: true}, nil).
			Times(1)

		task := &task{
			r:      reconciler,
			tenant: tenant,
		}

		err = task.update(context.Background())
		Expect(err).ToNot(HaveOccurred())
	})

	It("should skip reconciliation for failed tenants", func() {
		msg := "Previous sync failed"
		tenant := privatev1.Tenant_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
				Tenant:     "tenant-1",
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				State:   privatev1.TenantState_TENANT_STATE_FAILED,
				Message: &msg,
			}.Build(),
		}.Build()

		task := &task{
			tenant: tenant,
		}

		err := task.update(context.Background())
		Expect(err).ToNot(HaveOccurred())
	})
})

type testDefaultNetworkingLifecycleManager struct {
	deleteFunc func(context.Context, string) error
}

func (testDefaultNetworkingLifecycleManager) Ensure(context.Context, string) (*privatev1.NetworkDefaults, error) {
	return nil, nil
}

func (m testDefaultNetworkingLifecycleManager) Delete(ctx context.Context, tenantName string) error {
	return m.deleteFunc(ctx, tenantName)
}

type readinessNetworkClasses struct {
	privatev1.NetworkClassesClient
	items []*privatev1.NetworkClass
}

func (f *readinessNetworkClasses) List(context.Context, *privatev1.NetworkClassesListRequest,
	...grpc.CallOption) (*privatev1.NetworkClassesListResponse, error) {
	return privatev1.NetworkClassesListResponse_builder{Items: f.items}.Build(), nil
}

type readinessVirtualNetworks struct {
	privatev1.VirtualNetworksClient
	items []*privatev1.VirtualNetwork
}

func (f *readinessVirtualNetworks) List(context.Context, *privatev1.VirtualNetworksListRequest,
	...grpc.CallOption) (*privatev1.VirtualNetworksListResponse, error) {
	return privatev1.VirtualNetworksListResponse_builder{Items: f.items}.Build(), nil
}

type readinessSubnets struct {
	privatev1.SubnetsClient
	items []*privatev1.Subnet
}

func (f *readinessSubnets) List(context.Context, *privatev1.SubnetsListRequest,
	...grpc.CallOption) (*privatev1.SubnetsListResponse, error) {
	return privatev1.SubnetsListResponse_builder{Items: f.items}.Build(), nil
}

type readinessSecurityGroups struct {
	privatev1.SecurityGroupsClient
	items []*privatev1.SecurityGroup
}

func (f *readinessSecurityGroups) List(context.Context, *privatev1.SecurityGroupsListRequest,
	...grpc.CallOption) (*privatev1.SecurityGroupsListResponse, error) {
	return privatev1.SecurityGroupsListResponse_builder{Items: f.items}.Build(), nil
}

type readinessExternalIPs struct {
	privatev1.ExternalIPsClient
	items []*privatev1.ExternalIP
}

func (f *readinessExternalIPs) List(context.Context, *privatev1.ExternalIPsListRequest,
	...grpc.CallOption) (*privatev1.ExternalIPsListResponse, error) {
	return privatev1.ExternalIPsListResponse_builder{Items: f.items}.Build(), nil
}

type readinessNATGateways struct {
	privatev1.NATGatewaysClient
	items []*privatev1.NATGateway
}

func (f *readinessNATGateways) List(context.Context, *privatev1.NATGatewaysListRequest,
	...grpc.CallOption) (*privatev1.NATGatewaysListResponse, error) {
	return privatev1.NATGatewaysListResponse_builder{Items: f.items}.Build(), nil
}

var _ = Describe("Default networking readiness", func() {
	var (
		ctx            context.Context
		networkClasses *readinessNetworkClasses
		vns            *readinessVirtualNetworks
		subnets        *readinessSubnets
		groups         *readinessSecurityGroups
		eips           *readinessExternalIPs
		gateways       *readinessNATGateways
		reconciler     *function
	)

	condType := privatev1.TenantConditionType_TENANT_CONDITION_TYPE_DEFAULT_NETWORKING_READY

	findCondition := func(tenant *privatev1.Tenant) *privatev1.TenantCondition {
		for _, c := range tenant.GetStatus().GetConditions() {
			if c.GetType() == condType {
				return c
			}
		}
		return nil
	}

	newSyncedTenant := func(name string) *privatev1.Tenant {
		return privatev1.Tenant_builder{
			Id: name,
			Metadata: privatev1.Metadata_builder{
				Name:       name,
				Tenant:     name,
				Finalizers: []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				State:         privatev1.TenantState_TENANT_STATE_SYNCED,
				IdpTenantName: name,
			}.Build(),
		}.Build()
	}

	check := func(tenant *privatev1.Tenant, defaultNetworkErr error) (*privatev1.TenantCondition, error) {
		t := &task{r: reconciler, tenant: tenant, defaultNetworkErr: defaultNetworkErr}
		t.setDefaults()
		t.setConditionDefaults()
		err := t.checkDefaultNetworkingReadiness(ctx)
		return findCondition(tenant), err
	}

	setAllReady := func() {
		vns.items = []*privatev1.VirtualNetwork{privatev1.VirtualNetwork_builder{
			Status: privatev1.VirtualNetworkStatus_builder{
				State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY,
			}.Build(),
		}.Build()}
		subnets.items = []*privatev1.Subnet{privatev1.Subnet_builder{
			Status: privatev1.SubnetStatus_builder{State: privatev1.SubnetState_SUBNET_STATE_READY}.Build(),
		}.Build()}
		groups.items = []*privatev1.SecurityGroup{privatev1.SecurityGroup_builder{
			Status: privatev1.SecurityGroupStatus_builder{
				State: privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY,
			}.Build(),
		}.Build()}
		eips.items = []*privatev1.ExternalIP{privatev1.ExternalIP_builder{
			Status: privatev1.ExternalIPStatus_builder{
				State: privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED,
			}.Build(),
		}.Build()}
		gateways.items = []*privatev1.NATGateway{privatev1.NATGateway_builder{
			Status: privatev1.NATGatewayStatus_builder{State: privatev1.NATGatewayState_NAT_GATEWAY_STATE_READY}.Build(),
		}.Build()}
	}

	BeforeEach(func() {
		ctx = context.Background()
		networkClasses = &readinessNetworkClasses{items: []*privatev1.NetworkClass{
			privatev1.NetworkClass_builder{
				Id: "nc-1",
				Status: privatev1.NetworkClassStatus_builder{
					State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
				}.Build(),
			}.Build(),
		}}
		vns = &readinessVirtualNetworks{}
		subnets = &readinessSubnets{}
		groups = &readinessSecurityGroups{}
		eips = &readinessExternalIPs{}
		gateways = &readinessNATGateways{}
		reconciler = &function{
			provisioningEnabled:   true,
			logger:                logger,
			networkClassesClient:  networkClasses,
			virtualNetworksClient: vns,
			subnetsClient:         subnets,
			securityGroupsClient:  groups,
			externalIPsClient:     eips,
			natGatewaysClient:     gateways,
		}
	})

	It("initializes the condition as FALSE for a non-SYNCED tenant", func() {
		tenant := newSyncedTenant("pending-tenant")
		tenant.GetStatus().SetState(privatev1.TenantState_TENANT_STATE_PENDING)
		t := &task{r: reconciler, tenant: tenant}
		t.setDefaults()
		t.setConditionDefaults()
		cond := findCondition(tenant)
		Expect(cond).ToNot(BeNil())
		Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))
	})

	DescribeTable("reports reserved tenants as ready",
		func(name string) {
			cond, err := check(newSyncedTenant(name), nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
			Expect(cond.GetReason()).To(Equal("ReservedTenant"))
		},
		Entry("system", auth.SystemTenant),
		Entry("shared", auth.SharedTenant),
	)

	It("waits when no NetworkClass is configured", func() {
		networkClasses.items = nil
		cond, err := check(newSyncedTenant("tenant-a"), nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))
		Expect(cond.GetReason()).To(Equal("ResourcesPending"))
		Expect(cond.GetMessage()).To(Equal("no NetworkClass is configured"))
	})

	It("waits while the NetworkClass isn't READY", func() {
		networkClasses.items[0].GetStatus().SetState(privatev1.NetworkClassState_NETWORK_CLASS_STATE_PENDING)
		cond, err := check(newSyncedTenant("tenant-a"), nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(cond.GetReason()).To(Equal("ResourcesPending"))
		Expect(cond.GetMessage()).To(ContainSubstring("the NetworkClass is not READY"))
	})

	It("lists the missing and pending default resources", func() {
		vns.items = []*privatev1.VirtualNetwork{privatev1.VirtualNetwork_builder{
			Status: privatev1.VirtualNetworkStatus_builder{
				State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING,
			}.Build(),
		}.Build()}
		cond, err := check(newSyncedTenant("tenant-a"), nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))
		Expect(cond.GetReason()).To(Equal("ResourcesPending"))
		Expect(cond.GetMessage()).To(Equal("Default networking resources pending: VirtualNetwork/default, " +
			"Subnet/default-ipv4, SecurityGroup/default, ExternalIP/default-nat, NATGateway/default"))
	})

	It("rechecks every 30 seconds while there is no READY pool", func() {
		cond, err := check(newSyncedTenant("tenant-a"), defaultnetworking.ErrNoReadyPool)
		Expect(controllers.IsRequeueable(err)).To(BeTrue())
		Expect(cond.GetReason()).To(Equal("ResourcesPending"))
		Expect(cond.GetMessage()).To(HaveSuffix("; no READY IPv4 ExternalIPPool"))
	})

	It("fails when every READY pool is full", func() {
		cond, err := check(newSyncedTenant("tenant-a"), defaultnetworking.ErrNoPoolCapacity)
		Expect(err).ToNot(HaveOccurred())
		Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))
		Expect(cond.GetReason()).To(Equal("ResourceFailed"))
		Expect(cond.GetMessage()).To(ContainSubstring("capacity"))
	})

	It("fails when a default resource is FAILED", func() {
		setAllReady()
		subnets.items[0].GetStatus().SetState(privatev1.SubnetState_SUBNET_STATE_FAILED)
		cond, err := check(newSyncedTenant("tenant-a"), nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(cond.GetReason()).To(Equal("ResourceFailed"))
		Expect(cond.GetMessage()).To(ContainSubstring("Subnet/default-ipv4"))
	})

	It("is ready when all five default resources are ready", func() {
		setAllReady()
		cond, err := check(newSyncedTenant("tenant-a"), nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
		Expect(cond.GetReason()).To(Equal("AllResourcesReady"))
	})

	It("needs only the VirtualNetwork, Subnet and SecurityGroup when provider networking is disabled", func() {
		reconciler.provisioningEnabled = false
		setAllReady()
		eips.items = nil
		gateways.items = nil
		cond, err := check(newSyncedTenant("tenant-a"), nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
		Expect(cond.GetReason()).To(Equal("ProvisioningDisabled"))
	})
})

var _ = Describe("Vault namespace provisioning", func() {
	var (
		ctx             context.Context
		ctrl            *gomock.Controller
		mockIDPClient   *idp.MockClientInterface
		mockVaultClient *vault.MockLifecycleClient
		idpManager      *idp.TenantManager
	)

	condType := privatev1.TenantConditionType_TENANT_CONDITION_TYPE_VAULT_READY

	findCondition := func(tenant *privatev1.Tenant) *privatev1.TenantCondition {
		for _, c := range tenant.GetStatus().GetConditions() {
			if c.GetType() == condType {
				return c
			}
		}
		return nil
	}

	BeforeEach(func() {
		var err error
		ctx = context.Background()
		ctrl = gomock.NewController(GinkgoT())
		mockIDPClient = idp.NewMockClientInterface(ctrl)
		mockVaultClient = vault.NewMockLifecycleClient(ctrl)

		idpManager, err = idp.NewTenantManager().
			SetLogger(logger).
			SetClient(mockIDPClient).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	DescribeTable("re-ensures vault provisioning when already ready", func(tenantName string) {
		reconciler := &function{
			logger:         logger,
			idpManager:     idpManager,
			vaultLifecycle: mockVaultClient,
		}

		tenant := privatev1.Tenant_builder{
			Id: "org-already",
			Metadata: privatev1.Metadata_builder{
				Name:       tenantName,
				Finalizers: []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
				Tenant:     "tenant-1",
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				State:         privatev1.TenantState_TENANT_STATE_SYNCED,
				IdpTenantName: tenantName,
				Conditions: []*privatev1.TenantCondition{
					privatev1.TenantCondition_builder{
						Type:   condType,
						Status: privatev1.ConditionStatus_CONDITION_STATUS_TRUE,
						Reason: new("NamespaceReady"),
					}.Build(),
				},
			}.Build(),
		}.Build()

		mockIDPClient.EXPECT().
			GetTenant(gomock.Any(), tenantName).
			Return(&idp.Tenant{Name: tenantName}, nil).Times(2)
		mockVaultClient.EXPECT().
			EnsureTenantNamespace(gomock.Any(), tenantName).
			Return(nil).Times(2)

		t := &task{r: reconciler, tenant: tenant}
		var readyCondition *privatev1.TenantCondition
		for range 2 {
			Expect(t.update(ctx)).To(Succeed())
			cond := findCondition(t.tenant)
			Expect(cond).ToNot(BeNil())
			Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
			Expect(cond.GetReason()).To(Equal("NamespaceReady"))
			if readyCondition == nil {
				readyCondition = proto.Clone(cond).(*privatev1.TenantCondition)
			} else {
				Expect(proto.Equal(cond, readyCondition)).To(BeTrue())
			}
			Expect(t.tenant.GetStatus().GetState()).To(Equal(privatev1.TenantState_TENANT_STATE_SYNCED))
		}
	}, Entry("ordinary tenant", "already-org"), Entry("system tenant", auth.SystemTenant))

	It("skips vault provisioning when vault lifecycle is nil", func() {
		reconciler := &function{
			logger:     logger,
			idpManager: idpManager,
		}

		tenant := privatev1.Tenant_builder{
			Id: "org-no-vault",
			Metadata: privatev1.Metadata_builder{
				Name:       "no-vault-org",
				Finalizers: []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
				Tenant:     "tenant-1",
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				State:         privatev1.TenantState_TENANT_STATE_SYNCED,
				IdpTenantName: "no-vault-org",
			}.Build(),
		}.Build()

		mockIDPClient.EXPECT().
			GetTenant(gomock.Any(), "no-vault-org").
			Return(&idp.Tenant{Name: "no-vault-org"}, nil)

		t := &task{r: reconciler, tenant: tenant}
		err := t.update(ctx)
		Expect(err).ToNot(HaveOccurred())
		cond := findCondition(tenant)
		Expect(cond).ToNot(BeNil())
		Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))
	})

	It("provisions vault namespace when condition is not yet true", func() {
		reconciler := &function{
			logger:         logger,
			idpManager:     idpManager,
			vaultLifecycle: mockVaultClient,
		}

		tenant := privatev1.Tenant_builder{
			Id: "org-retry",
			Metadata: privatev1.Metadata_builder{
				Name:       "retry-org",
				Finalizers: []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
				Tenant:     "tenant-1",
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				State:         privatev1.TenantState_TENANT_STATE_SYNCED,
				IdpTenantName: "retry-org",
			}.Build(),
		}.Build()

		mockIDPClient.EXPECT().
			GetTenant(gomock.Any(), "retry-org").
			Return(&idp.Tenant{Name: "retry-org"}, nil)

		mockVaultClient.EXPECT().
			EnsureTenantNamespace(gomock.Any(), "retry-org").
			Return(nil)

		t := &task{r: reconciler, tenant: tenant}
		err := t.update(ctx)
		Expect(err).ToNot(HaveOccurred())
		cond := findCondition(tenant)
		Expect(cond).ToNot(BeNil())
		Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
		Expect(cond.GetReason()).To(Equal("NamespaceReady"))
	})

	It("skips vault provisioning for failed tenants", func() {
		reconciler := &function{
			logger:         logger,
			idpManager:     idpManager,
			vaultLifecycle: mockVaultClient,
		}

		msg := "Previous sync failed"
		tenant := privatev1.Tenant_builder{
			Metadata: privatev1.Metadata_builder{
				Name:       "failed-org",
				Finalizers: []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
				Tenant:     "tenant-1",
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				State:   privatev1.TenantState_TENANT_STATE_FAILED,
				Message: &msg,
			}.Build(),
		}.Build()

		t := &task{r: reconciler, tenant: tenant}
		err := t.update(ctx)
		Expect(err).ToNot(HaveOccurred())
		cond := findCondition(tenant)
		Expect(cond).ToNot(BeNil())
		Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))
	})

	DescribeTable("provisions a vault namespace for built-in tenants", func(tenantName string) {
		reconciler := &function{
			logger:         logger,
			idpManager:     idpManager,
			vaultLifecycle: mockVaultClient,
		}

		tenant := privatev1.Tenant_builder{
			Id: "org-shared",
			Metadata: privatev1.Metadata_builder{
				Name:       tenantName,
				Finalizers: []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
				Tenant:     "tenant-1",
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				State:         privatev1.TenantState_TENANT_STATE_SYNCED,
				IdpTenantName: tenantName,
			}.Build(),
		}.Build()

		mockIDPClient.EXPECT().
			GetTenant(gomock.Any(), tenantName).
			Return(&idp.Tenant{Name: tenantName}, nil)
		mockVaultClient.EXPECT().
			EnsureTenantNamespace(gomock.Any(), tenantName).
			Return(nil)

		t := &task{r: reconciler, tenant: tenant}
		err := t.update(ctx)
		Expect(err).ToNot(HaveOccurred())
		cond := findCondition(tenant)
		Expect(cond).ToNot(BeNil())
		Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
	}, Entry("shared", auth.SharedTenant), Entry("system", auth.SystemTenant))

	DescribeTable("safely reports and retries vault provisioning after an already-ready tenant fails", func(tenantName string, transportFailure bool, responseStatus int, expectedCode grpccodes.Code) {
		const testHost = "synthetic-vault.example.invalid"
		const testBody = "synthetic-backend-body-marker"
		const testToken = "synthetic-token-marker"
		backendErr := fmt.Errorf("vault login returned status 403: %s %s %s", testHost, testBody, testToken)
		if transportFailure {
			backendErr = fmt.Errorf("failed to login to vault: %w", &url.Error{
				Op: "Post", URL: "https://" + testHost + "/v1/auth/jwt/login",
				Err: errors.New(testBody + " " + testToken),
			})
		} else if responseStatus != 0 {
			backendErr = fmt.Errorf("failed to login to vault: %w", &vaultapi.ResponseError{
				HTTPMethod: http.MethodPost, URL: "https://" + testHost + "/v1/auth/jwt/login",
				StatusCode: responseStatus, Errors: []string{testBody + " " + testToken},
			})
		}
		var logs bytes.Buffer
		var updates []*privatev1.TenantsUpdateRequest
		reconciler := &function{
			logger: slog.New(slog.NewJSONHandler(&logs, nil)), idpManager: idpManager, vaultLifecycle: mockVaultClient,
			maskCalculator: masks.NewCalculator().Build(),
			tenantsClient: finalizerClient{update: func(req *privatev1.TenantsUpdateRequest) (*privatev1.TenantsUpdateResponse, error) {
				updates = append(updates, proto.Clone(req).(*privatev1.TenantsUpdateRequest))
				return privatev1.TenantsUpdateResponse_builder{Object: req.GetObject()}.Build(), nil
			}},
		}
		tenant := privatev1.Tenant_builder{
			Id: "synthetic-tenant-id",
			Metadata: privatev1.Metadata_builder{
				Name: tenantName, Finalizers: []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding}, Tenant: "tenant-1",
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				State: privatev1.TenantState_TENANT_STATE_SYNCED, IdpTenantName: tenantName,
				Conditions: []*privatev1.TenantCondition{
					privatev1.TenantCondition_builder{
						Type:   condType,
						Status: privatev1.ConditionStatus_CONDITION_STATUS_TRUE,
						Reason: new("NamespaceReady"),
					}.Build(),
				},
			}.Build(),
		}.Build()
		mockIDPClient.EXPECT().GetTenant(gomock.Any(), tenantName).
			Return(&idp.Tenant{Name: tenantName}, nil).Times(2)
		gomock.InOrder(
			mockVaultClient.EXPECT().EnsureTenantNamespace(gomock.Any(), tenantName).
				Return(backendErr),
			mockVaultClient.EXPECT().EnsureTenantNamespace(gomock.Any(), tenantName).Return(nil),
		)
		Expect(reconciler.Run(ctx, tenant)).To(Succeed())
		Expect(updates).To(HaveLen(1))
		Expect(updates[0].GetLock()).To(BeTrue())
		Expect(updates[0].GetUpdateMask().GetPaths()).To(ContainElement("status.conditions"))
		persisted := updates[0].GetObject()
		Expect(findCondition(persisted).GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))
		Expect(findCondition(persisted).GetReason()).To(Equal("ProvisionFailed"))
		Expect(persisted.GetStatus().GetState()).To(Equal(privatev1.TenantState_TENANT_STATE_SYNCED))
		for _, sentinel := range []string{testHost, testBody, testToken} {
			Expect(logs.String()).ToNot(ContainSubstring(sentinel))
			Expect(updates[0].String()).ToNot(ContainSubstring(sentinel))
		}
		var record map[string]any
		Expect(json.Unmarshal(logs.Bytes(), &record)).To(Succeed())
		Expect(record).To(HaveKeyWithValue("msg", "Failed to provision vault namespace for tenant"))
		Expect(record).To(HaveKeyWithValue("tenant_id", "synthetic-tenant-id"))
		Expect(record).To(HaveKeyWithValue("tenant_name", tenantName))
		Expect(record).To(HaveKeyWithValue("error_code", expectedCode.String()))
		Expect(record).ToNot(HaveKey("error"))
		expectedMessage := "Failed to provision vault namespace (" + expectedCode.String() + ")"
		Expect(findCondition(persisted).GetMessage()).To(Equal(expectedMessage))
		Expect(reconciler.Run(ctx, proto.Clone(persisted).(*privatev1.Tenant))).To(Succeed())
		Expect(updates).To(HaveLen(2))
		Expect(findCondition(persisted).GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))
		Expect(findCondition(persisted).GetReason()).To(Equal("ProvisionFailed"))
		Expect(findCondition(persisted).GetMessage()).To(Equal(expectedMessage))
		Expect(persisted.GetStatus().GetState()).To(Equal(privatev1.TenantState_TENANT_STATE_SYNCED))
		Expect(findCondition(updates[1].GetObject()).GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
		Expect(findCondition(updates[1].GetObject()).GetReason()).To(Equal("NamespaceReady"))
		Expect(updates[1].GetObject().GetStatus().GetState()).To(Equal(privatev1.TenantState_TENANT_STATE_SYNCED))
	},
		Entry("ordinary tenant transport failure", "retry-transit", true, 0, grpccodes.Unavailable),
		Entry("system tenant transport failure", auth.SystemTenant, true, 0, grpccodes.Unavailable),
		Entry("ordinary tenant unrecognized failure", "retry-transit", false, 0, grpccodes.Internal),
		Entry("system tenant unrecognized failure", auth.SystemTenant, false, 0, grpccodes.Internal),
		Entry("wrapped forbidden Vault response", "retry-transit", false, http.StatusForbidden, grpccodes.PermissionDenied),
	)
	It("stays PENDING with condition FALSE when vault provisioning fails during initial sync", func() {
		reconciler := &function{
			logger:         logger,
			idpManager:     idpManager,
			vaultLifecycle: mockVaultClient,
		}

		tenant := privatev1.Tenant_builder{
			Id: "org-vault-fail",
			Metadata: privatev1.Metadata_builder{
				Name:       "vault-fail-org",
				Finalizers: []string{finalizers.Controller},
				Tenant:     "tenant-1",
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				BreakGlassCredentials: privatev1.BreakGlassCredentials_builder{
					Username: "vault-fail-org-osac-break-glass",
					Password: testPreGeneratedPassword,
				}.Build(),
			}.Build(),
		}.Build()

		mockIDPClient.EXPECT().
			CreateTenant(gomock.Any(), gomock.Any()).
			Return(&idp.Tenant{Name: "vault-fail-org", Enabled: true}, nil)
		mockIDPClient.EXPECT().
			CreateUser(gomock.Any(), "vault-fail-org", gomock.Any()).
			Return(&idp.User{ID: "user-fail"}, nil)
		mockIDPClient.EXPECT().
			AssignIdpManagerPermissions(gomock.Any(), "user-fail").
			Return(nil)

		mockVaultClient.EXPECT().
			EnsureTenantNamespace(gomock.Any(), "vault-fail-org").
			Return(fmt.Errorf("vault connection refused"))

		t := &task{r: reconciler, tenant: tenant}
		err := t.update(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(tenant.GetStatus().GetState()).To(Equal(privatev1.TenantState_TENANT_STATE_PENDING))
		Expect(tenant.GetStatus().GetIdpTenantName()).To(Equal("vault-fail-org"))
		cond := findCondition(tenant)
		Expect(cond).ToNot(BeNil())
		Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))
		Expect(cond.GetReason()).To(Equal("ProvisionFailed"))
		Expect(cond.GetMessage()).To(Equal("Failed to provision vault namespace (Internal)"))
	})

	It("stays SYNCED with condition FALSE when vault provisioning fails for synced tenant", func() {
		reconciler := &function{
			logger:         logger,
			idpManager:     idpManager,
			vaultLifecycle: mockVaultClient,
		}

		tenant := privatev1.Tenant_builder{
			Id: "org-synced-vault-fail",
			Metadata: privatev1.Metadata_builder{
				Name:       "synced-vault-fail-org",
				Finalizers: []string{finalizers.Controller},
				Tenant:     "tenant-1",
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				State:         privatev1.TenantState_TENANT_STATE_SYNCED,
				IdpTenantName: "synced-vault-fail-org",
			}.Build(),
		}.Build()

		mockIDPClient.EXPECT().
			GetTenant(gomock.Any(), "synced-vault-fail-org").
			Return(&idp.Tenant{Name: "synced-vault-fail-org"}, nil)

		mockVaultClient.EXPECT().
			EnsureTenantNamespace(gomock.Any(), "synced-vault-fail-org").
			Return(fmt.Errorf("dial tcp: no such host"))

		t := &task{r: reconciler, tenant: tenant}
		err := t.update(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(tenant.GetStatus().GetState()).To(Equal(privatev1.TenantState_TENANT_STATE_SYNCED))
		cond := findCondition(tenant)
		Expect(cond).ToNot(BeNil())
		Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))
		Expect(cond.GetReason()).To(Equal("ProvisionFailed"))
		Expect(cond.GetMessage()).To(Equal("Failed to provision vault namespace (Internal)"))
	})

	It("provisions a vault namespace for the system tenant", func() {
		reconciler := &function{
			logger:         logger,
			idpManager:     idpManager,
			vaultLifecycle: mockVaultClient,
		}

		tenant := privatev1.Tenant_builder{
			Id: "org-system",
			Metadata: privatev1.Metadata_builder{
				Name:       auth.SystemTenant,
				Finalizers: []string{finalizers.Controller},
				Tenant:     auth.SystemTenant,
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				State:         privatev1.TenantState_TENANT_STATE_SYNCED,
				IdpTenantName: auth.SystemTenant,
			}.Build(),
		}.Build()

		mockIDPClient.EXPECT().
			GetTenant(gomock.Any(), auth.SystemTenant).
			Return(&idp.Tenant{Name: auth.SystemTenant}, nil)
		mockVaultClient.EXPECT().
			EnsureTenantNamespace(gomock.Any(), auth.SystemTenant).
			Return(nil)

		t := &task{r: reconciler, tenant: tenant}
		err := t.update(ctx)
		Expect(err).ToNot(HaveOccurred())
		cond := findCondition(tenant)
		Expect(cond).ToNot(BeNil())
		Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
	})
})

var _ = Describe("Vault namespace cleanup during deletion", func() {
	var (
		ctx                context.Context
		ctrl               *gomock.Controller
		mockIDPClient      *idp.MockClientInterface
		mockVaultClient    *vault.MockLifecycleClient
		mockProjectsClient *MockProjectsClient
		idpManager         *idp.TenantManager
	)

	BeforeEach(func() {
		var err error
		ctx = context.Background()
		ctrl = gomock.NewController(GinkgoT())
		mockIDPClient = idp.NewMockClientInterface(ctrl)
		mockVaultClient = vault.NewMockLifecycleClient(ctrl)
		mockProjectsClient = NewMockProjectsClient(ctrl)

		idpManager, err = idp.NewTenantManager().
			SetLogger(logger).
			SetClient(mockIDPClient).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	It("deletes vault namespace during tenant deletion", func() {
		reconciler := &function{
			logger:         logger,
			projectsClient: mockProjectsClient,
			idpManager:     idpManager,
			vaultLifecycle: mockVaultClient,
		}

		tenant := privatev1.Tenant_builder{
			Id: "org-vault-del",
			Metadata: privatev1.Metadata_builder{
				Name:              "vault-org",
				Finalizers:        []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
				DeletionTimestamp: timestamppb.Now(),
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				State:         privatev1.TenantState_TENANT_STATE_SYNCED,
				IdpTenantName: "vault-org",
			}.Build(),
		}.Build()

		mockProjectsClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(privatev1.ProjectsListResponse_builder{Total: 0}.Build(), nil).
			Times(2)

		mockIDPClient.EXPECT().
			DeleteTenant(gomock.Any(), "vault-org").
			Return(nil)

		mockVaultClient.EXPECT().
			DeleteTenantNamespace(gomock.Any(), "vault-org").
			Return(nil)

		t := &task{r: reconciler, tenant: tenant}
		err := t.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(tenant.GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.TenantLifecycle))
	})

	It("blocks deletion when vault namespace deletion fails", func() {
		reconciler := &function{
			logger:         logger,
			projectsClient: mockProjectsClient,
			idpManager:     idpManager,
			vaultLifecycle: mockVaultClient,
		}

		tenant := privatev1.Tenant_builder{
			Id: "org-vault-fail",
			Metadata: privatev1.Metadata_builder{
				Name:              "vault-fail-org",
				Finalizers:        []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
				DeletionTimestamp: timestamppb.Now(),
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				State:         privatev1.TenantState_TENANT_STATE_SYNCED,
				IdpTenantName: "vault-fail-org",
			}.Build(),
		}.Build()

		mockProjectsClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(privatev1.ProjectsListResponse_builder{Total: 0}.Build(), nil).
			Times(2)

		mockVaultClient.EXPECT().
			DeleteTenantNamespace(gomock.Any(), "vault-fail-org").
			Return(fmt.Errorf("vault unavailable"))

		t := &task{r: reconciler, tenant: tenant}
		err := t.delete(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to delete vault namespace"))
		Expect(tenant.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.TenantLifecycle))
	})

	It("performs idempotent vault deletion even when namespace was never provisioned", func() {
		reconciler := &function{
			logger:         logger,
			projectsClient: mockProjectsClient,
			idpManager:     idpManager,
			vaultLifecycle: mockVaultClient,
		}

		tenant := privatev1.Tenant_builder{
			Id: "org-no-vault-ns",
			Metadata: privatev1.Metadata_builder{
				Name:              "no-vault-ns-org",
				Finalizers:        []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
				DeletionTimestamp: timestamppb.Now(),
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				State:         privatev1.TenantState_TENANT_STATE_SYNCED,
				IdpTenantName: "no-vault-ns-org",
			}.Build(),
		}.Build()

		mockProjectsClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(privatev1.ProjectsListResponse_builder{Total: 0}.Build(), nil).
			Times(2)

		mockIDPClient.EXPECT().
			DeleteTenant(gomock.Any(), "no-vault-ns-org").
			Return(nil)

		mockVaultClient.EXPECT().
			DeleteTenantNamespace(gomock.Any(), "no-vault-ns-org").
			Return(nil)

		t := &task{r: reconciler, tenant: tenant}
		err := t.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(tenant.GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.TenantLifecycle))
	})

	It("skips vault deletion when vault lifecycle is nil", func() {
		reconciler := &function{
			logger:         logger,
			projectsClient: mockProjectsClient,
			idpManager:     idpManager,
		}

		tenant := privatev1.Tenant_builder{
			Id: "org-nil-vault",
			Metadata: privatev1.Metadata_builder{
				Name:              "nil-vault-org",
				Finalizers:        []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
				DeletionTimestamp: timestamppb.Now(),
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				State:         privatev1.TenantState_TENANT_STATE_SYNCED,
				IdpTenantName: "nil-vault-org",
			}.Build(),
		}.Build()

		mockProjectsClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(privatev1.ProjectsListResponse_builder{Total: 0}.Build(), nil).
			Times(2)

		mockIDPClient.EXPECT().
			DeleteTenant(gomock.Any(), "nil-vault-org").
			Return(nil)

		t := &task{r: reconciler, tenant: tenant}
		err := t.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(tenant.GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.TenantLifecycle))
	})

	It("deletes vault namespace when idp_tenant_name is empty", func() {
		reconciler := &function{
			logger:         logger,
			projectsClient: mockProjectsClient,
			idpManager:     idpManager,
			vaultLifecycle: mockVaultClient,
		}

		tenant := privatev1.Tenant_builder{
			Id: "org-vault-only",
			Metadata: privatev1.Metadata_builder{
				Name:              "vault-only-org",
				Finalizers:        []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
				DeletionTimestamp: timestamppb.Now(),
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				State: privatev1.TenantState_TENANT_STATE_SYNCED,
			}.Build(),
		}.Build()

		mockProjectsClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(privatev1.ProjectsListResponse_builder{Total: 0}.Build(), nil).
			Times(2)

		mockIDPClient.EXPECT().
			DeleteTenant(gomock.Any(), "vault-only-org").
			Return(nil)

		mockVaultClient.EXPECT().
			DeleteTenantNamespace(gomock.Any(), "vault-only-org").
			Return(nil)

		t := &task{r: reconciler, tenant: tenant}
		err := t.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(tenant.GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.TenantLifecycle))
	})

	It("cleans up vault namespace when deleting non-SYNCED tenant", func() {
		reconciler := &function{
			logger:         logger,
			projectsClient: mockProjectsClient,
			idpManager:     idpManager,
			vaultLifecycle: mockVaultClient,
		}

		tenant := privatev1.Tenant_builder{
			Id: "org-failed-vault",
			Metadata: privatev1.Metadata_builder{
				Name:              "failed-org",
				Finalizers:        []string{finalizers.TenantLifecycle, finalizers.TenantOnboarding},
				DeletionTimestamp: timestamppb.Now(),
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				State: privatev1.TenantState_TENANT_STATE_FAILED,
			}.Build(),
		}.Build()

		mockProjectsClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(privatev1.ProjectsListResponse_builder{Total: 0}.Build(), nil).
			Times(2)

		mockIDPClient.EXPECT().
			DeleteTenant(gomock.Any(), "failed-org").
			Return(nil)

		mockVaultClient.EXPECT().
			DeleteTenantNamespace(gomock.Any(), "failed-org").
			Return(nil)

		t := &task{r: reconciler, tenant: tenant}
		err := t.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(tenant.GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.TenantLifecycle))
	})
})
