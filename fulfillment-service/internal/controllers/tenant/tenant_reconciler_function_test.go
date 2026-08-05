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
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/types/known/timestamppb"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/fulfillment-service/internal/idp"
)

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
		Expect(tenant.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
	})

	It("should not add finalizer if already present", func() {
		tenant := privatev1.Tenant_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
			}.Build(),
		}.Build()

		task := &task{
			tenant: tenant,
		}

		added := task.addFinalizer()
		Expect(added).To(BeFalse())
		Expect(tenant.GetMetadata().GetFinalizers()).To(HaveLen(1))
	})

	It("should return immediately after adding finalizer", func() {
		tenant := privatev1.Tenant_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: "tenant-1",
			}.Build(),
		}.Build()

		task := &task{
			tenant: tenant,
		}

		err := task.update(context.Background())
		Expect(err).ToNot(HaveOccurred())

		Expect(tenant.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
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
				Finalizers: []string{finalizers.Controller},
				Tenant:     "tenant-1",
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				BreakGlassCredentials: privatev1.BreakGlassCredentials_builder{
					Username: "test-org-osac-break-glass",
					Password: "pre-generated-password",
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
				Expect(user.Credentials[0].Value).To(Equal("pre-generated-password"))
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
				Finalizers: []string{finalizers.Controller},
				Tenant:     "tenant-1",
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				BreakGlassCredentials: privatev1.BreakGlassCredentials_builder{
					Username: "test-org-osac-break-glass",
					Password: "pre-generated-password",
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
				Finalizers: []string{finalizers.Controller},
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
				Finalizers: []string{finalizers.Controller},
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
				Finalizers: []string{finalizers.Controller},
				Tenant:     "tenant-1",
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				BreakGlassCredentials: privatev1.BreakGlassCredentials_builder{
					Username: auth.SharedTenant + "-osac-break-glass",
					Password: "pre-generated-password",
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
				Finalizers: []string{finalizers.Controller},
				Tenant:     "tenant-1",
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				BreakGlassCredentials: privatev1.BreakGlassCredentials_builder{
					Username: auth.SystemTenant + "-osac-break-glass",
					Password: "pre-generated-password",
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
				Finalizers: []string{finalizers.Controller},
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
					finalizers.Controller,
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
					Password: "pre-generated-password",
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
					finalizers.Controller,
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
				Finalizers:        []string{finalizers.Controller},
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
		Expect(tenant.GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.Controller))
	})

	It("should skip IDP deletion and remove finalizer when tenant not synced", func() {
		deletionTimestamp := timestamppb.New(time.Now())
		tenant := privatev1.Tenant_builder{
			Id: "org-123",
			Metadata: privatev1.Metadata_builder{
				Name:              "test-org",
				Finalizers:        []string{finalizers.Controller},
				DeletionTimestamp: deletionTimestamp,
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				State: privatev1.TenantState_TENANT_STATE_PENDING,
			}.Build(),
		}.Build()

		mockProjectsClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(privatev1.ProjectsListResponse_builder{Total: 0}.Build(), nil).
			Times(1)

		task := &task{
			r:      reconciler,
			tenant: tenant,
		}

		err := task.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(tenant.GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.Controller))
	})

	It("should skip IDP deletion and remove finalizer when idp_tenant_name is empty", func() {
		deletionTimestamp := timestamppb.New(time.Now())
		tenant := privatev1.Tenant_builder{
			Id: "org-123",
			Metadata: privatev1.Metadata_builder{
				Name:              "test-org",
				Finalizers:        []string{finalizers.Controller},
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
			Times(1)

		task := &task{
			r:      reconciler,
			tenant: tenant,
		}

		err := task.delete(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(tenant.GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.Controller))
	})

	It("should return error on IDP deletion failure and keep finalizer", func() {
		deletionTimestamp := timestamppb.New(time.Now())
		tenant := privatev1.Tenant_builder{
			Id: "org-123",
			Metadata: privatev1.Metadata_builder{
				Name:              "test-org",
				Finalizers:        []string{finalizers.Controller},
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
			Times(1)

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
		Expect(tenant.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
	})

	It("should block deletion when projects remain", func() {
		deletionTimestamp := timestamppb.New(time.Now())
		tenant := privatev1.Tenant_builder{
			Id: "org-123",
			Metadata: privatev1.Metadata_builder{
				Name:              "test-org",
				Finalizers:        []string{finalizers.Controller},
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
			Return(privatev1.ProjectsListResponse_builder{
				Total: 2,
			}.Build(), nil).
			Times(1)

		task := &task{
			r:      reconciler,
			tenant: tenant,
		}

		err := task.delete(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("project(s) pending deletion"))
		Expect(tenant.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
	})

	It("should return error when project query fails during deletion", func() {
		deletionTimestamp := timestamppb.New(time.Now())
		tenant := privatev1.Tenant_builder{
			Id: "org-123",
			Metadata: privatev1.Metadata_builder{
				Name:              "test-org",
				Finalizers:        []string{finalizers.Controller},
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
			Return(nil, fmt.Errorf("connection refused")).
			Times(1)

		task := &task{
			r:      reconciler,
			tenant: tenant,
		}

		err := task.delete(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to query remaining projects"))
		Expect(tenant.GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller))
	})

	It("should remove finalizer when called", func() {
		tenant := privatev1.Tenant_builder{
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller, "other-finalizer"},
			}.Build(),
		}.Build()

		task := &task{
			tenant: tenant,
		}

		task.removeFinalizer()
		Expect(tenant.GetMetadata().GetFinalizers()).ToNot(ContainElement(finalizers.Controller))
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
				Finalizers: []string{finalizers.Controller},
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
				Finalizers: []string{finalizers.Controller},
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

var _ = Describe("Default networking readiness", func() {
	var (
		ctx         context.Context
		ctrl        *gomock.Controller
		mockVNs     *MockVirtualNetworksClient
		mockSubnets *MockSubnetsClient
		mockSGs     *MockSecurityGroupsClient
		mockNGs     *MockNATGatewaysClient
		reconciler  *function
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
				Finalizers: []string{finalizers.Controller},
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				State:         privatev1.TenantState_TENANT_STATE_SYNCED,
				IdpTenantName: name,
			}.Build(),
		}.Build()
	}

	expectEmptyLists := func() {
		mockVNs.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			privatev1.VirtualNetworksListResponse_builder{}.Build(), nil)
		mockSubnets.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			privatev1.SubnetsListResponse_builder{}.Build(), nil)
		mockSGs.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			privatev1.SecurityGroupsListResponse_builder{}.Build(), nil)
		mockNGs.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			privatev1.NATGatewaysListResponse_builder{}.Build(), nil)
	}

	BeforeEach(func() {
		ctx = context.Background()
		ctrl = gomock.NewController(GinkgoT())
		mockVNs = NewMockVirtualNetworksClient(ctrl)
		mockSubnets = NewMockSubnetsClient(ctrl)
		mockSGs = NewMockSecurityGroupsClient(ctrl)
		mockNGs = NewMockNATGatewaysClient(ctrl)

		mockClient := idp.NewMockClientInterface(ctrl)
		idpManager, err := idp.NewTenantManager().
			SetLogger(logger).
			SetClient(mockClient).
			Build()
		Expect(err).ToNot(HaveOccurred())

		reconciler = &function{
			logger:                logger,
			idpManager:            idpManager,
			virtualNetworksClient: mockVNs,
			subnetsClient:         mockSubnets,
			securityGroupsClient:  mockSGs,
			natGatewaysClient:     mockNGs,
		}
	})

	It("initializes DefaultNetworkingReady condition as FALSE for non-SYNCED tenant", func() {
		tenant := privatev1.Tenant_builder{
			Id: "pending-tenant",
			Metadata: privatev1.Metadata_builder{
				Name:       "pending-tenant",
				Tenant:     "pending-tenant",
				Finalizers: []string{finalizers.Controller},
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				State: privatev1.TenantState_TENANT_STATE_PENDING,
			}.Build(),
		}.Build()

		t := &task{r: reconciler, tenant: tenant}
		t.setDefaults()
		t.setConditionDefaults()

		cond := findCondition(tenant)
		Expect(cond).ToNot(BeNil())
		Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))
	})

	It("sets condition TRUE when no default resources exist", func() {
		tenant := newSyncedTenant("no-net-tenant")
		expectEmptyLists()

		t := &task{r: reconciler, tenant: tenant}
		t.setDefaults()
		t.setConditionDefaults()
		err := t.checkDefaultNetworkingReadiness(ctx)
		Expect(err).ToNot(HaveOccurred())

		cond := findCondition(tenant)
		Expect(cond).ToNot(BeNil())
		Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
		Expect(cond.GetReason()).To(Equal("NoDefaultNetworking"))
	})

	It("sets condition TRUE when all default resources are READY", func() {
		tenant := newSyncedTenant("ready-tenant")

		mockVNs.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			privatev1.VirtualNetworksListResponse_builder{
				Items: []*privatev1.VirtualNetwork{
					privatev1.VirtualNetwork_builder{
						Metadata: privatev1.Metadata_builder{Name: "default"}.Build(),
						Status: privatev1.VirtualNetworkStatus_builder{
							State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY,
						}.Build(),
					}.Build(),
				},
			}.Build(), nil)
		mockSubnets.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			privatev1.SubnetsListResponse_builder{
				Items: []*privatev1.Subnet{
					privatev1.Subnet_builder{
						Metadata: privatev1.Metadata_builder{Name: "default-ipv4"}.Build(),
						Status: privatev1.SubnetStatus_builder{
							State: privatev1.SubnetState_SUBNET_STATE_READY,
						}.Build(),
					}.Build(),
				},
			}.Build(), nil)
		mockSGs.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			privatev1.SecurityGroupsListResponse_builder{
				Items: []*privatev1.SecurityGroup{
					privatev1.SecurityGroup_builder{
						Metadata: privatev1.Metadata_builder{Name: "default"}.Build(),
						Status: privatev1.SecurityGroupStatus_builder{
							State: privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY,
						}.Build(),
					}.Build(),
				},
			}.Build(), nil)
		mockNGs.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			privatev1.NATGatewaysListResponse_builder{}.Build(), nil)

		t := &task{r: reconciler, tenant: tenant}
		t.setDefaults()
		t.setConditionDefaults()
		err := t.checkDefaultNetworkingReadiness(ctx)
		Expect(err).ToNot(HaveOccurred())

		cond := findCondition(tenant)
		Expect(cond).ToNot(BeNil())
		Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
		Expect(cond.GetReason()).To(Equal("AllResourcesReady"))
	})

	It("sets condition FALSE when some resources are PENDING", func() {
		tenant := newSyncedTenant("pending-net-tenant")

		mockVNs.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			privatev1.VirtualNetworksListResponse_builder{
				Items: []*privatev1.VirtualNetwork{
					privatev1.VirtualNetwork_builder{
						Metadata: privatev1.Metadata_builder{Name: "default"}.Build(),
						Status: privatev1.VirtualNetworkStatus_builder{
							State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY,
						}.Build(),
					}.Build(),
				},
			}.Build(), nil)
		mockSubnets.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			privatev1.SubnetsListResponse_builder{
				Items: []*privatev1.Subnet{
					privatev1.Subnet_builder{
						Metadata: privatev1.Metadata_builder{Name: "default-ipv4"}.Build(),
						Status: privatev1.SubnetStatus_builder{
							State: privatev1.SubnetState_SUBNET_STATE_PENDING,
						}.Build(),
					}.Build(),
				},
			}.Build(), nil)
		mockSGs.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			privatev1.SecurityGroupsListResponse_builder{}.Build(), nil)
		mockNGs.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			privatev1.NATGatewaysListResponse_builder{}.Build(), nil)

		t := &task{r: reconciler, tenant: tenant}
		t.setDefaults()
		t.setConditionDefaults()
		err := t.checkDefaultNetworkingReadiness(ctx)
		Expect(err).ToNot(HaveOccurred())

		cond := findCondition(tenant)
		Expect(cond).ToNot(BeNil())
		Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))
		Expect(cond.GetReason()).To(Equal("ResourcesPending"))
		Expect(cond.GetMessage()).To(ContainSubstring("Subnet/default-ipv4"))
	})

	It("sets condition FALSE when some resources are FAILED", func() {
		tenant := newSyncedTenant("failed-net-tenant")

		mockVNs.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			privatev1.VirtualNetworksListResponse_builder{
				Items: []*privatev1.VirtualNetwork{
					privatev1.VirtualNetwork_builder{
						Metadata: privatev1.Metadata_builder{Name: "default"}.Build(),
						Status: privatev1.VirtualNetworkStatus_builder{
							State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_FAILED,
						}.Build(),
					}.Build(),
				},
			}.Build(), nil)
		mockSubnets.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			privatev1.SubnetsListResponse_builder{}.Build(), nil)
		mockSGs.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			privatev1.SecurityGroupsListResponse_builder{}.Build(), nil)
		mockNGs.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			privatev1.NATGatewaysListResponse_builder{}.Build(), nil)

		t := &task{r: reconciler, tenant: tenant}
		t.setDefaults()
		t.setConditionDefaults()
		err := t.checkDefaultNetworkingReadiness(ctx)
		Expect(err).ToNot(HaveOccurred())

		cond := findCondition(tenant)
		Expect(cond).ToNot(BeNil())
		Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))
		Expect(cond.GetReason()).To(Equal("ResourceFailed"))
		Expect(cond.GetMessage()).To(ContainSubstring("VirtualNetwork/default"))
	})

	It("reports FAILED over PENDING when both exist", func() {
		tenant := newSyncedTenant("mixed-tenant")

		mockVNs.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			privatev1.VirtualNetworksListResponse_builder{
				Items: []*privatev1.VirtualNetwork{
					privatev1.VirtualNetwork_builder{
						Metadata: privatev1.Metadata_builder{Name: "default"}.Build(),
						Status: privatev1.VirtualNetworkStatus_builder{
							State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_FAILED,
						}.Build(),
					}.Build(),
				},
			}.Build(), nil)
		mockSubnets.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			privatev1.SubnetsListResponse_builder{
				Items: []*privatev1.Subnet{
					privatev1.Subnet_builder{
						Metadata: privatev1.Metadata_builder{Name: "default-ipv4"}.Build(),
						Status: privatev1.SubnetStatus_builder{
							State: privatev1.SubnetState_SUBNET_STATE_PENDING,
						}.Build(),
					}.Build(),
				},
			}.Build(), nil)
		mockSGs.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			privatev1.SecurityGroupsListResponse_builder{}.Build(), nil)
		mockNGs.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			privatev1.NATGatewaysListResponse_builder{}.Build(), nil)

		t := &task{r: reconciler, tenant: tenant}
		t.setDefaults()
		t.setConditionDefaults()
		err := t.checkDefaultNetworkingReadiness(ctx)
		Expect(err).ToNot(HaveOccurred())

		cond := findCondition(tenant)
		Expect(cond).ToNot(BeNil())
		Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))
		Expect(cond.GetReason()).To(Equal("ResourceFailed"))
	})

	It("initializes DefaultNetworkingReady condition as FALSE for deleted tenant", func() {
		tenant := privatev1.Tenant_builder{
			Id: "deleting-tenant",
			Metadata: privatev1.Metadata_builder{
				Name:              "deleting-tenant",
				Tenant:            "deleting-tenant",
				Finalizers:        []string{finalizers.Controller},
				DeletionTimestamp: timestamppb.Now(),
			}.Build(),
			Status: privatev1.TenantStatus_builder{
				State:         privatev1.TenantState_TENANT_STATE_SYNCED,
				IdpTenantName: "deleting-tenant",
			}.Build(),
		}.Build()

		t := &task{r: reconciler, tenant: tenant}
		t.setDefaults()
		t.setConditionDefaults()

		cond := findCondition(tenant)
		Expect(cond).ToNot(BeNil())
		Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))
	})
})
