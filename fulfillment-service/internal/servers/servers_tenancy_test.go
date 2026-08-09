/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/collections"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
)

var _ = Describe("Tenancy logic", func() {
	BeforeEach(func() {
		var err error

		// Create the tenants used in the tests:
		tenantsDao, err := dao.NewGenericDAO[*privatev1.Tenant]().
			SetLogger(logger).
			SetTableName("tenants").
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		createTenant := func(name string) {
			_, err = tenantsDao.Create().
				SetObject(privatev1.Tenant_builder{
					Id: name,
					Metadata: privatev1.Metadata_builder{
						Name:   name,
						Tenant: name,
					}.Build(),
				}.Build()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
		}
		createTenant("my-tenant")
		createTenant("your-tenant")

		// Create a default cluster version for version resolution:
		seedClusterVersion(ctx, privatev1.ClusterVersion_builder{
			Id: "cv-default",
			Metadata: privatev1.Metadata_builder{
				Name:   "4-17-0",
				Tenant: auth.SharedTenant,
			}.Build(),
			Spec: privatev1.ClusterVersionSpec_builder{
				Image:     "quay.io/openshift-release-dev/ocp-release:4.17.0-multi",
				Enabled:   proto.Bool(true),
				IsDefault: proto.Bool(true),
				Version:   "4.17.0",
				State:     privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_ACTIVE,
			}.Build(),
		}.Build())
	})

	It("Returns tenant in metadata when object is created", func() {
		// Create a mock tenancy logic that returns a specific tenant:
		assignable := collections.NewSet("my-tenant")
		tenancy := auth.NewMockTenancyLogic(ctrl)
		tenancy.EXPECT().DetermineAssignableTenants(gomock.Any()).
			Return(assignable, nil).
			AnyTimes()
		tenancy.EXPECT().DetermineDefaultTenant(gomock.Any()).
			Return("my-tenant", nil).
			AnyTimes()
		tenancy.EXPECT().DetermineVisibleTenants(gomock.Any()).
			Return(auth.SharedTenants.Union(assignable), nil).
			AnyTimes()

		// Create the template using the DAO directly (this is setup for the test):
		templatesDao, err := dao.NewGenericDAO[*privatev1.ClusterTemplate]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		_, err = templatesDao.Create().
			SetObject(
				privatev1.ClusterTemplate_builder{
					Id:          "my-template",
					Title:       "My template",
					Description: "My template",
					Metadata: privatev1.Metadata_builder{
						Name:   "test-template",
						Tenant: "my-tenant",
					}.Build(),
				}.Build(),
			).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		// Create the public clusters server that uses the tenancy logic:
		clustersServer, err := NewClustersServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			SetScheme(testScheme).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Create a cluster using the public server to verify tenant assignment:
		response, err := clustersServer.Create(
			ctx,
			publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "test-cluster",
					}.Build(),
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: "my-template"}.Build(),
					}.Build(),
				}.Build(),
			}.Build(),
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())

		// Verify that the cluster metadata contains the expected tenant:
		cluster := response.GetObject()
		Expect(cluster).ToNot(BeNil())
		metadata := cluster.GetMetadata()
		Expect(metadata).ToNot(BeNil())
		tenant := metadata.GetTenant()
		Expect(tenant).To(Equal("my-tenant"))
	})

	It("Rejects object creation when assigned tenants are empty", func() {
		// Create the template using the DAO with setup tenancy:
		templatesDao, err := dao.NewGenericDAO[*privatev1.ClusterTemplate]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		_, err = templatesDao.Create().
			SetObject(privatev1.ClusterTemplate_builder{
				Id:          "my-template",
				Title:       "My template",
				Description: "My template",
				Metadata: privatev1.Metadata_builder{
					Name:   "test-template",
					Tenant: "my-tenant",
				}.Build(),
			}.Build(),
			).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		// Create a tenancy logic that doesn't return assignable tenants:
		tenancy := auth.NewMockTenancyLogic(ctrl)
		tenancy.EXPECT().DetermineAssignableTenants(gomock.Any()).
			Return(collections.NewSet[string](), nil).
			AnyTimes()
		tenancy.EXPECT().DetermineDefaultTenant(gomock.Any()).
			Return("my-tenant", nil).
			AnyTimes()
		tenancy.EXPECT().DetermineVisibleTenants(gomock.Any()).
			Return(auth.SharedTenants.Union(collections.NewSet("my-tenant")), nil).
			AnyTimes()

		// Create the clusters server with the empty tenancy logic:
		clustersServer, err := NewClustersServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			SetScheme(testScheme).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Attempt to create a cluster and verify it fails:
		response, err := clustersServer.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					Name: "test-cluster",
				}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: "my-template"}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(response).To(BeNil())
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.PermissionDenied))
		Expect(status.Message()).To(Equal("there are no assignable tenants"))
	})

	It("Uses default tenant when tenant is explicitly empty", func() {
		// Create a tenancy logic that returns a valid tenant:
		assignable := collections.NewSet("my-tenant")
		tenancy := auth.NewMockTenancyLogic(ctrl)
		tenancy.EXPECT().DetermineAssignableTenants(gomock.Any()).
			Return(assignable, nil).
			AnyTimes()
		tenancy.EXPECT().DetermineDefaultTenant(gomock.Any()).
			Return("my-tenant", nil).
			AnyTimes()
		tenancy.EXPECT().DetermineVisibleTenants(gomock.Any()).
			Return(auth.SharedTenants.Union(assignable), nil).
			AnyTimes()

		// Create the template using the DAO:
		templatesDao, err := dao.NewGenericDAO[*privatev1.ClusterTemplate]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		_, err = templatesDao.Create().
			SetObject(
				privatev1.ClusterTemplate_builder{
					Id:          "my-template",
					Title:       "My template",
					Description: "My template",
					Metadata: privatev1.Metadata_builder{
						Name:   "test-template",
						Tenant: "my-tenant",
					}.Build(),
				}.Build(),
			).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		// Create the clusters server:
		clustersServer, err := NewClustersServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			SetScheme(testScheme).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Attempt to create a cluster with explicitly empty tenant and verify it uses the default:
		response, err := clustersServer.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					Name:   "test-cluster",
					Tenant: "",
				}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: "my-template"}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// Verify that the cluster metadata contains the expected tenant:
		cluster := response.GetObject()
		Expect(cluster).ToNot(BeNil())
		tenant := cluster.GetMetadata().GetTenant()
		Expect(tenant).To(Equal("my-tenant"))
	})

	It("Respects explicitly specified tenant over default", func() {
		// Create a tenancy logic where the default tenant differs from the one the user will specify:
		assignable := collections.NewSet("my-tenant", "your-tenant")
		tenancy := auth.NewMockTenancyLogic(ctrl)
		tenancy.EXPECT().DetermineAssignableTenants(gomock.Any()).
			Return(assignable, nil).
			AnyTimes()
		tenancy.EXPECT().DetermineDefaultTenant(gomock.Any()).
			Return("your-tenant", nil).
			AnyTimes()
		tenancy.EXPECT().DetermineVisibleTenants(gomock.Any()).
			Return(auth.SharedTenants.Union(assignable), nil).
			AnyTimes()

		// Create the template using the DAO:
		templatesDao, err := dao.NewGenericDAO[*privatev1.ClusterTemplate]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		_, err = templatesDao.Create().
			SetObject(privatev1.ClusterTemplate_builder{
				Id:          "my-template",
				Title:       "My template",
				Description: "My template",
				Metadata: privatev1.Metadata_builder{
					Name:   "test-template",
					Tenant: "my-tenant",
				}.Build(),
			}.Build()).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		// Create the clusters server:
		clustersServer, err := NewClustersServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			SetScheme(testScheme).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Create a cluster explicitly specifying 'my-tenant', while the default is 'your-tenant':
		response, err := clustersServer.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					Name:   "test-cluster",
					Tenant: "my-tenant",
				}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: "my-template"}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// Verify that the cluster uses the explicitly specified tenant, not the default:
		cluster := response.GetObject()
		Expect(cluster).ToNot(BeNil())
		tenant := cluster.GetMetadata().GetTenant()
		Expect(tenant).To(Equal("my-tenant"))
	})

	It("Rejects changing tenant on update", func() {
		assignable := collections.NewSet("my-tenant", "your-tenant")
		tenancy := auth.NewMockTenancyLogic(ctrl)
		tenancy.EXPECT().DetermineAssignableTenants(gomock.Any()).
			Return(assignable, nil).
			AnyTimes()
		tenancy.EXPECT().DetermineDefaultTenant(gomock.Any()).
			Return("my-tenant", nil).
			AnyTimes()
		tenancy.EXPECT().DetermineVisibleTenants(gomock.Any()).
			Return(auth.SharedTenants.Union(assignable), nil).
			AnyTimes()

		templatesDao, err := dao.NewGenericDAO[*privatev1.ClusterTemplate]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		_, err = templatesDao.Create().
			SetObject(privatev1.ClusterTemplate_builder{
				Id:          "my-template",
				Title:       "My template",
				Description: "My template",
				Metadata: privatev1.Metadata_builder{
					Name:   "test-template",
					Tenant: "my-tenant",
				}.Build(),
			}.Build()).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		clustersServer, err := NewClustersServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			SetScheme(testScheme).
			Build()
		Expect(err).ToNot(HaveOccurred())

		createResponse, err := clustersServer.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					Name: "test-cluster",
				}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: "my-template"}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		object := createResponse.GetObject()

		_, err = clustersServer.Update(ctx, publicv1.ClustersUpdateRequest_builder{
			Object: publicv1.Cluster_builder{
				Id: object.GetId(),
				Metadata: publicv1.Metadata_builder{
					Name:   "test-cluster",
					Tenant: "your-tenant",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		Expect(status.Message()).To(ContainSubstring("immutable"))
	})

	It("Preserves tenant when update does not specify it", func() {
		assignable := collections.NewSet("my-tenant")
		tenancy := auth.NewMockTenancyLogic(ctrl)
		tenancy.EXPECT().DetermineAssignableTenants(gomock.Any()).
			Return(assignable, nil).
			AnyTimes()
		tenancy.EXPECT().DetermineDefaultTenant(gomock.Any()).
			Return("my-tenant", nil).
			AnyTimes()
		tenancy.EXPECT().DetermineVisibleTenants(gomock.Any()).
			Return(auth.SharedTenants.Union(assignable), nil).
			AnyTimes()

		templatesDao, err := dao.NewGenericDAO[*privatev1.ClusterTemplate]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		_, err = templatesDao.Create().
			SetObject(privatev1.ClusterTemplate_builder{
				Id:          "my-template",
				Title:       "My template",
				Description: "My template",
				Metadata: privatev1.Metadata_builder{
					Name:   "test-template",
					Tenant: "my-tenant",
				}.Build(),
			}.Build()).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		clustersServer, err := NewClustersServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			SetScheme(testScheme).
			Build()
		Expect(err).ToNot(HaveOccurred())

		createResponse, err := clustersServer.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					Name: "test-cluster",
				}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: "my-template"}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		object := createResponse.GetObject()

		updateResponse, err := clustersServer.Update(ctx, publicv1.ClustersUpdateRequest_builder{
			Object: publicv1.Cluster_builder{
				Id: object.GetId(),
				Spec: publicv1.ClusterSpec_builder{
					NodeSets: map[string]*publicv1.ClusterNodeSet{
						"compute": publicv1.ClusterNodeSet_builder{
							HostType: publicv1.HostTypeReference_builder{Id: "acme_1tib"}.Build(),
							Size:     4,
						}.Build(),
					},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(updateResponse.GetObject().GetMetadata().GetTenant()).To(Equal("my-tenant"))
	})

	It("Rejects object creation when assigned tenant is invisible to the user", func() {
		// Create a tenancy logic that returns visible tenants:
		assignable := collections.NewSet("my-tenant")
		tenancy := auth.NewMockTenancyLogic(ctrl)
		tenancy.EXPECT().DetermineAssignableTenants(gomock.Any()).
			Return(assignable, nil).
			AnyTimes()
		tenancy.EXPECT().DetermineDefaultTenant(gomock.Any()).
			Return("my-tenant", nil).
			AnyTimes()
		tenancy.EXPECT().DetermineVisibleTenants(gomock.Any()).
			Return(auth.SharedTenants.Union(assignable), nil).
			AnyTimes()

		// Create the template:
		templatesDao, err := dao.NewGenericDAO[*privatev1.ClusterTemplate]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		_, err = templatesDao.Create().
			SetObject(privatev1.ClusterTemplate_builder{
				Id: "our-template",
				Metadata: privatev1.Metadata_builder{
					Name:   "test-template",
					Tenant: "my-tenant",
				}.Build(),
			}.Build(),
			).Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		// Create the clusters server:
		clustersServer, err := NewClustersServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			SetScheme(testScheme).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Attempt to create an object with a tenant that is invisible to the user and verify that it fails:
		response, err := clustersServer.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					Name:   "test-cluster",
					Tenant: "your-tenant",
				}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: "our-template"}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(response).To(BeNil())
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.PermissionDenied))
		Expect(status.Message()).To(Equal("tenant 'your-tenant' doesn't exist"))
	})

	It("Rejects object creation when tenant is visible to the user, but doesn't exist in the database", func() {
		// Create a tenancy logic that returns visible tenants:
		tenancy := auth.NewMockTenancyLogic(ctrl)
		tenancy.EXPECT().DetermineAssignableTenants(gomock.Any()).
			Return(auth.AllTenants, nil).
			AnyTimes()
		tenancy.EXPECT().DetermineDefaultTenant(gomock.Any()).
			Return(auth.SharedTenant, nil).
			AnyTimes()
		tenancy.EXPECT().DetermineVisibleTenants(gomock.Any()).
			Return(auth.AllTenants, nil).
			AnyTimes()

		// Create the server:
		server, err := NewClustersServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			SetScheme(testScheme).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Create the template using the DAO:
		templatesDao, err := dao.NewGenericDAO[*privatev1.ClusterTemplate]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		_, err = templatesDao.Create().
			SetObject(
				privatev1.ClusterTemplate_builder{
					Id:          "my-template",
					Title:       "My template",
					Description: "My template",
					Metadata: privatev1.Metadata_builder{
						Name:   "test-template",
						Tenant: "my-tenant",
					}.Build(),
				}.Build(),
			).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		// Attempt to create an object and verify that it fails:
		response, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					Name:   "my-cluster",
					Tenant: "does-not-exist",
				}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: "my-template"}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(response).To(BeNil())
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		Expect(status.Message()).To(Equal("tenant 'does-not-exist' doesn't exist"))
	})
})
