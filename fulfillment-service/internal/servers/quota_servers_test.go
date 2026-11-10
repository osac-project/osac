/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package servers

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/collections"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Quota servers", func() {
	var (
		limitsServer     *QuotaLimitsServer
		thresholdsServer *QuotaThresholdsServer
		requestsServer   *QuotaLimitRequestsServer
		usageServer      *QuotaUsageServer
		limitsDao        *dao.GenericDAO[*privatev1.QuotaLimit]
	)

	BeforeEach(func() {
		var err error
		limitsServer, err = NewQuotaLimitsServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		thresholdsServer, err = NewQuotaThresholdsServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		requestsServer, err = NewQuotaLimitRequestsServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		usageServer, err = NewQuotaUsageServer().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		limitsDao, err = dao.NewGenericDAO[*privatev1.QuotaLimit]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		projectsDao, err := dao.NewGenericDAO[*privatev1.Project]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		_, err = projectsDao.Create().
			SetObject(privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   "team",
					Tenant: testTenant,
				}.Build(),
			}.Build()).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
	})

	createLimit := func(project string, dimension string, value int64) *publicv1.QuotaLimit {
		response, err := limitsServer.Create(ctx, publicv1.QuotaLimitsCreateRequest_builder{
			Object: publicv1.QuotaLimit_builder{
				Metadata: publicv1.Metadata_builder{
					Tenant:  testTenant,
					Project: project,
				}.Build(),
				Spec: publicv1.QuotaLimitSpec_builder{
					Dimension: dimension,
					Value:     value,
				}.Build(),
			}.Build(),
		}.Build())
		ExpectWithOffset(1, err).ToNot(HaveOccurred())
		return response.GetObject()
	}

	Describe("Quota limits", func() {
		It("creates project limits and tenant overrides, deriving the name from the dimension", func() {
			override := createLimit("", "compute.vcpus", 10)
			Expect(override.GetMetadata().GetName()).To(Equal("compute-vcpus-" +
				quotaNameFromDimension("compute.vcpus")[len("compute-vcpus-"):]))
			Expect(override.GetMetadata().GetTenant()).To(Equal(testTenant))
			Expect(override.GetSpec().GetValue()).To(Equal(int64(10)))

			project := createLimit("team", "compute.vcpus", 4)
			Expect(project.GetMetadata().GetProject()).To(Equal("team"))
		})

		It("rejects a second limit of the same dimension in the same scope", func() {
			createLimit("", "compute.vcpus", 10)
			_, err := limitsServer.Create(ctx, publicv1.QuotaLimitsCreateRequest_builder{
				Object: publicv1.QuotaLimit_builder{
					Metadata: publicv1.Metadata_builder{Tenant: testTenant}.Build(),
					Spec:     publicv1.QuotaLimitSpec_builder{Dimension: "compute.vcpus", Value: 20}.Build(),
				}.Build(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.AlreadyExists))
		})

		It("rejects a limit without a dimension", func() {
			_, err := limitsServer.Create(ctx, publicv1.QuotaLimitsCreateRequest_builder{
				Object: publicv1.QuotaLimit_builder{
					Metadata: publicv1.Metadata_builder{Tenant: testTenant}.Build(),
					Spec:     publicv1.QuotaLimitSpec_builder{Value: 10}.Build(),
				}.Build(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		})

		It("derives a name within the metadata.name bound from a long dimension", func() {
			limit := createLimit("", "bare_metal.instances."+strings.Repeat("h", 63), 1)
			Expect(limit.GetMetadata().GetName()).
				To(MatchRegexp(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`))
		})

		It("updates only the value", func() {
			limit := createLimit("", "compute.vcpus", 10)
			_, err := limitsServer.Update(ctx, publicv1.QuotaLimitsUpdateRequest_builder{
				Object: publicv1.QuotaLimit_builder{
					Id:       limit.GetId(),
					Metadata: limit.GetMetadata(),
					Spec:     publicv1.QuotaLimitSpec_builder{Dimension: "compute.memory_gib", Value: 20}.Build(),
				}.Build(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))

			updated, err := limitsServer.Update(ctx, publicv1.QuotaLimitsUpdateRequest_builder{
				Object: publicv1.QuotaLimit_builder{
					Id:       limit.GetId(),
					Metadata: limit.GetMetadata(),
					Spec:     publicv1.QuotaLimitSpec_builder{Dimension: "compute.vcpus", Value: 20}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updated.GetObject().GetSpec().GetValue()).To(Equal(int64(20)))
		})

		It("deletes a limit", func() {
			limit := createLimit("", "compute.vcpus", 10)
			_, err := limitsServer.Delete(ctx, publicv1.QuotaLimitsDeleteRequest_builder{
				Id: limit.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			_, err = limitsServer.Get(ctx, publicv1.QuotaLimitsGetRequest_builder{Id: limit.GetId()}.Build())
			Expect(status.Code(err)).To(Equal(codes.NotFound))
		})

		It("lists the limits of a dimension", func() {
			createLimit("", "compute.vcpus", 10)
			createLimit("team", "compute.vcpus", 4)
			createLimit("team", "compute.memory_gib", 8)
			response, err := limitsServer.List(ctx, publicv1.QuotaLimitsListRequest_builder{
				Filter: new(`this.spec.dimension == "compute.vcpus"`),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetTotal()).To(Equal(int32(2)))
		})

		It("refuses a write to a tenant override without total visibility", func() {
			visibility, err := auth.NewVisibility().
				AddVisibleTenants(testTenant).
				SetTotalProjectVisibility(testTenant).
				Build()
			Expect(err).ToNot(HaveOccurred())
			restricted := auth.NewMockTenancyLogic(ctrl)
			restricted.EXPECT().DetermineAssignableTenants(gomock.Any()).
				Return(collections.NewSet(testTenant), nil).
				AnyTimes()
			restricted.EXPECT().DetermineDefaultTenant(gomock.Any()).
				Return(testTenant, nil).
				AnyTimes()
			restricted.EXPECT().DetermineVisibility(gomock.Any()).
				Return(visibility, nil).
				AnyTimes()
			server, err := NewQuotaLimitsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(restricted).
				Build()
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Create(ctx, publicv1.QuotaLimitsCreateRequest_builder{
				Object: publicv1.QuotaLimit_builder{
					Metadata: publicv1.Metadata_builder{Tenant: testTenant}.Build(),
					Spec:     publicv1.QuotaLimitSpec_builder{Dimension: "compute.vcpus", Value: 100}.Build(),
				}.Build(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.PermissionDenied))

			// A project limit is the tenant admin's to set:
			_, err = server.Create(ctx, publicv1.QuotaLimitsCreateRequest_builder{
				Object: publicv1.QuotaLimit_builder{
					Metadata: publicv1.Metadata_builder{Tenant: testTenant, Project: "team"}.Build(),
					Spec:     publicv1.QuotaLimitSpec_builder{Dimension: "compute.vcpus", Value: 4}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Another tenant's limits stay invisible:
			_, err = limitsDao.Create().
				SetObject(privatev1.QuotaLimit_builder{
					Metadata: privatev1.Metadata_builder{Name: "other", Tenant: "shared"}.Build(),
					Spec:     privatev1.QuotaLimitSpec_builder{Dimension: "images.count", Value: 9}.Build(),
				}.Build()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			response, err := server.List(ctx, publicv1.QuotaLimitsListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			for _, item := range response.GetItems() {
				Expect(item.GetMetadata().GetTenant()).To(Equal(testTenant))
			}
		})
	})

	Describe("Default limits", func() {
		setDefault := func(dimension string, value int64) *publicv1.QuotaDefaultLimit {
			response, err := limitsServer.SetDefault(ctx, publicv1.QuotaLimitsSetDefaultRequest_builder{
				Object: publicv1.QuotaDefaultLimit_builder{
					Spec: publicv1.QuotaDefaultLimitSpec_builder{
						Dimension: dimension,
						Value:     value,
					}.Build(),
				}.Build(),
			}.Build())
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			return response.GetObject()
		}

		It("sets, lists, gets and clears defaults, replacing the value", func() {
			first := setDefault("compute.vcpus", 10)
			Expect(first.GetMetadata().GetName()).ToNot(BeEmpty())
			Expect(first.GetSpec().GetValue()).To(Equal(int64(10)))

			second := setDefault("compute.vcpus", 20)
			Expect(second.GetId()).To(Equal(first.GetId()))
			Expect(second.GetSpec().GetValue()).To(Equal(int64(20)))
			Expect(second.GetMetadata().GetVersion()).To(Equal(first.GetMetadata().GetVersion() + 1))

			setDefault("network.virtual_networks", 2)
			response, err := limitsServer.ListDefaults(ctx, publicv1.QuotaLimitsListDefaultsRequest_builder{
				Filter: new(`this.spec.dimension == "compute.vcpus"`),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetTotal()).To(Equal(int32(1)))

			getResponse, err := limitsServer.GetDefault(ctx, publicv1.QuotaLimitsGetDefaultRequest_builder{
				Id: first.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetSpec().GetValue()).To(Equal(int64(20)))

			_, err = limitsServer.ClearDefault(ctx, publicv1.QuotaLimitsClearDefaultRequest_builder{
				Id: first.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			_, err = limitsServer.GetDefault(ctx, publicv1.QuotaLimitsGetDefaultRequest_builder{
				Id: first.GetId(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.NotFound))
		})

		It("rejects a default without a dimension", func() {
			_, err := limitsServer.SetDefault(ctx, publicv1.QuotaLimitsSetDefaultRequest_builder{
				Object: publicv1.QuotaDefaultLimit_builder{
					Spec: publicv1.QuotaDefaultLimitSpec_builder{Value: 10}.Build(),
				}.Build(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		})
	})

	Describe("Quota thresholds", func() {
		It("refuses a threshold on a project with no limit of the dimension", func() {
			_, err := thresholdsServer.Create(ctx, publicv1.QuotaThresholdsCreateRequest_builder{
				Object: publicv1.QuotaThreshold_builder{
					Metadata: publicv1.Metadata_builder{Tenant: testTenant, Project: "team"}.Build(),
					Spec:     publicv1.QuotaThresholdSpec_builder{Dimension: "compute.vcpus", Value: 8}.Build(),
				}.Build(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
		})

		It("refuses a threshold without a project", func() {
			createLimit("", "compute.vcpus", 10)
			_, err := thresholdsServer.Create(ctx, publicv1.QuotaThresholdsCreateRequest_builder{
				Object: publicv1.QuotaThreshold_builder{
					Metadata: publicv1.Metadata_builder{Tenant: testTenant}.Build(),
					Spec:     publicv1.QuotaThresholdSpec_builder{Dimension: "compute.vcpus", Value: 8}.Build(),
				}.Build(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		})

		It("creates a threshold for a dimension the project limits", func() {
			createLimit("team", "compute.vcpus", 4)
			response, err := thresholdsServer.Create(ctx, publicv1.QuotaThresholdsCreateRequest_builder{
				Object: publicv1.QuotaThreshold_builder{
					Metadata: publicv1.Metadata_builder{Tenant: testTenant, Project: "team"}.Build(),
					Spec:     publicv1.QuotaThresholdSpec_builder{Dimension: "compute.vcpus", Value: 3}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetSpec().GetValue()).To(Equal(int64(3)))

			// One threshold per dimension per project:
			_, err = thresholdsServer.Create(ctx, publicv1.QuotaThresholdsCreateRequest_builder{
				Object: publicv1.QuotaThreshold_builder{
					Metadata: publicv1.Metadata_builder{Tenant: testTenant, Project: "team"}.Build(),
					Spec:     publicv1.QuotaThresholdSpec_builder{Dimension: "compute.vcpus", Value: 2}.Build(),
				}.Build(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.AlreadyExists))
		})
	})

	Describe("Quota limit requests", func() {
		createRequest := func(dimension string, value int64) *publicv1.QuotaLimitRequest {
			response, err := requestsServer.Create(ctx, publicv1.QuotaLimitRequestsCreateRequest_builder{
				Object: publicv1.QuotaLimitRequest_builder{
					Metadata: publicv1.Metadata_builder{Tenant: testTenant}.Build(),
					Spec: publicv1.QuotaLimitRequestSpec_builder{
						Dimension:      dimension,
						RequestedValue: value,
					}.Build(),
				}.Build(),
			}.Build())
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			return response.GetObject()
		}

		listLimits := func(dimension string) []*privatev1.QuotaLimit {
			response, err := limitsDao.List().
				SetFilter(`this.spec.dimension == "` + dimension + `" && this.metadata.tenant == "` + testTenant + `"`).
				Do(ctx)
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			return response.GetItems()
		}

		It("holds a request as pending and rejects a client-supplied status", func() {
			request := createRequest("compute.vcpus", 20)
			Expect(request.GetStatus().GetState()).To(Equal(publicv1.QuotaLimitRequestState_QUOTA_LIMIT_REQUEST_STATE_PENDING))

			_, err := requestsServer.Create(ctx, publicv1.QuotaLimitRequestsCreateRequest_builder{
				Object: publicv1.QuotaLimitRequest_builder{
					Metadata: publicv1.Metadata_builder{Tenant: testTenant}.Build(),
					Spec: publicv1.QuotaLimitRequestSpec_builder{
						Dimension:      "compute.memory_gib",
						RequestedValue: 64,
					}.Build(),
					Status: publicv1.QuotaLimitRequestStatus_builder{
						State: publicv1.QuotaLimitRequestState_QUOTA_LIMIT_REQUEST_STATE_APPROVED,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		})

		It("updates a pending request that omits the status, and rejects an explicit state", func() {
			privateServer, err := NewPrivateQuotaLimitRequestsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			created, err := privateServer.Create(ctx, privatev1.QuotaLimitRequestsCreateRequest_builder{
				Object: privatev1.QuotaLimitRequest_builder{
					Metadata: privatev1.Metadata_builder{Tenant: testTenant}.Build(),
					Spec: privatev1.QuotaLimitRequestSpec_builder{
						Dimension:      "compute.vcpus",
						RequestedValue: 20,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			request := created.GetObject()

			updated, err := privateServer.Update(ctx, privatev1.QuotaLimitRequestsUpdateRequest_builder{
				Object: privatev1.QuotaLimitRequest_builder{
					Id:       request.GetId(),
					Metadata: request.GetMetadata(),
					Spec: privatev1.QuotaLimitRequestSpec_builder{
						Dimension:      request.GetSpec().GetDimension(),
						RequestedValue: 30,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updated.GetObject().GetStatus().GetState()).
				To(Equal(privatev1.QuotaLimitRequestState_QUOTA_LIMIT_REQUEST_STATE_PENDING))
			Expect(updated.GetObject().GetSpec().GetRequestedValue()).To(Equal(int64(30)))

			_, err = privateServer.Update(ctx, privatev1.QuotaLimitRequestsUpdateRequest_builder{
				Object: privatev1.QuotaLimitRequest_builder{
					Id:       request.GetId(),
					Metadata: request.GetMetadata(),
					Spec:     request.GetSpec(),
					Status: privatev1.QuotaLimitRequestStatus_builder{
						State: privatev1.QuotaLimitRequestState_QUOTA_LIMIT_REQUEST_STATE_APPROVED,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))

			// The omitted status left the request pending, so a review can still act on it:
			_, err = requestsServer.Approve(ctx, publicv1.QuotaLimitRequestsApproveRequest_builder{
				Id: request.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("rejects a second pending request of the same dimension", func() {
			createRequest("compute.vcpus", 20)
			_, err := requestsServer.Create(ctx, publicv1.QuotaLimitRequestsCreateRequest_builder{
				Object: publicv1.QuotaLimitRequest_builder{
					Metadata: publicv1.Metadata_builder{Tenant: testTenant}.Build(),
					Spec: publicv1.QuotaLimitRequestSpec_builder{
						Dimension:      "compute.vcpus",
						RequestedValue: 30,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.AlreadyExists))
		})

		It("approves a request and sets the limit", func() {
			request := createRequest("compute.vcpus", 20)
			_, err := requestsServer.Approve(ctx, publicv1.QuotaLimitRequestsApproveRequest_builder{
				Id: request.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			limits := listLimits("compute.vcpus")
			Expect(limits).To(HaveLen(1))
			Expect(limits[0].GetSpec().GetValue()).To(Equal(int64(20)))
			Expect(limits[0].GetMetadata().GetProject()).To(BeEmpty())

			// The approved request is terminal:
			_, err = requestsServer.Approve(ctx, publicv1.QuotaLimitRequestsApproveRequest_builder{
				Id: request.GetId(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
			_, err = requestsServer.Update(ctx, publicv1.QuotaLimitRequestsUpdateRequest_builder{
				Object: publicv1.QuotaLimitRequest_builder{
					Id:       request.GetId(),
					Metadata: request.GetMetadata(),
					Spec:     request.GetSpec(),
				}.Build(),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
		})

		It("approving a request replaces an existing override", func() {
			createLimit("", "compute.vcpus", 10)
			request := createRequest("compute.vcpus", 20)
			_, err := requestsServer.Approve(ctx, publicv1.QuotaLimitRequestsApproveRequest_builder{
				Id: request.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			limits := listLimits("compute.vcpus")
			Expect(limits).To(HaveLen(1))
			Expect(limits[0].GetSpec().GetValue()).To(Equal(int64(20)))
		})

		It("denies a request and changes no limit", func() {
			request := createRequest("compute.vcpus", 20)
			_, err := requestsServer.Deny(ctx, publicv1.QuotaLimitRequestsDenyRequest_builder{
				Id: request.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(listLimits("compute.vcpus")).To(BeEmpty())

			// A denied request doesn't block a new one:
			createRequest("compute.vcpus", 30)
		})
	})

	Describe("Quota usage", func() {
		BeforeEach(func() {
			_, err := limitsServer.SetDefault(ctx, publicv1.QuotaLimitsSetDefaultRequest_builder{
				Object: publicv1.QuotaDefaultLimit_builder{
					Spec: publicv1.QuotaDefaultLimitSpec_builder{
						Dimension: "compute.vcpus",
						Value:     10,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			_, err = limitsServer.SetDefault(ctx, publicv1.QuotaLimitsSetDefaultRequest_builder{
				Object: publicv1.QuotaDefaultLimit_builder{
					Spec: publicv1.QuotaDefaultLimitSpec_builder{
						Dimension: "network.virtual_networks",
						Value:     2,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			createLimit("", "compute.vcpus", 5)
			createLimit("team", "network.virtual_networks", 3)

			// One VM of a type with 6 vCPUs, and one NAT gateway in the team project:
			instanceTypesDao, err := dao.NewGenericDAO[*privatev1.InstanceType]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = instanceTypesDao.Create().
				SetObject(privatev1.InstanceType_builder{
					Metadata: privatev1.Metadata_builder{Name: "large", Tenant: "shared"}.Build(),
					Spec:     privatev1.InstanceTypeSpec_builder{Vcpus: 6, MemoryGib: 24}.Build(),
				}.Build()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			computeInstancesDao, err := dao.NewGenericDAO[*privatev1.ComputeInstance]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = computeInstancesDao.Create().
				SetObject(privatev1.ComputeInstance_builder{
					Metadata: privatev1.Metadata_builder{Name: "vm-1", Tenant: testTenant}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						InstanceType: privatev1.InstanceTypeReference_builder{Name: "large"}.Build(),
					}.Build(),
				}.Build()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			virtualNetworksDao, err := dao.NewGenericDAO[*privatev1.VirtualNetwork]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = virtualNetworksDao.Create().
				SetObject(privatev1.VirtualNetwork_builder{
					Metadata: privatev1.Metadata_builder{Name: "vn-1", Tenant: testTenant, Project: "team"}.Build(),
				}.Build()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
		})

		findDimension := func(
			dimensions []*publicv1.QuotaUsageDimension, name string,
		) *publicv1.QuotaUsageDimension {
			for _, dimension := range dimensions {
				if dimension.GetDimension() == name {
					return dimension
				}
			}
			return nil
		}

		It("resolves the limits, counts the usage, and marks the over-limit dimensions", func() {
			response, err := usageServer.Get(ctx, publicv1.QuotaUsageGetRequest_builder{
				Tenant: testTenant,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			report := response.GetReport()
			Expect(report.GetTenant()).To(Equal(testTenant))

			vcpus := findDimension(report.GetDimensions(), "compute.vcpus")
			Expect(vcpus).ToNot(BeNil())
			// The override replaces the default of 10:
			Expect(vcpus.GetLimit()).To(Equal(int64(5)))
			Expect(vcpus.GetUsage()).To(Equal(int64(6)))
			Expect(vcpus.GetRoom()).To(Equal(int64(-1)))
			Expect(vcpus.GetOverLimit()).To(BeTrue())

			gateways := findDimension(report.GetDimensions(), "network.virtual_networks")
			Expect(gateways).ToNot(BeNil())
			Expect(gateways.GetLimit()).To(Equal(int64(2)))
			Expect(gateways.GetUsage()).To(Equal(int64(1)))
			Expect(gateways.GetOverLimit()).To(BeFalse())

			Expect(report.GetProjects()).To(HaveLen(2))
			var team *publicv1.QuotaUsageProject
			for _, project := range report.GetProjects() {
				if project.GetProject() == "team" {
					team = project
				}
			}
			Expect(team).ToNot(BeNil())
			teamGateways := findDimension(team.GetDimensions(), "network.virtual_networks")
			Expect(teamGateways).ToNot(BeNil())
			// The project limit replaces the inherited tenant limit:
			Expect(teamGateways.GetLimit()).To(Equal(int64(3)))
			Expect(teamGateways.GetUsage()).To(Equal(int64(1)))
			Expect(teamGateways.GetRoom()).To(Equal(int64(2)))
		})

		It("refuses the built-in tenants and invisible tenants", func() {
			_, err := usageServer.Get(ctx, publicv1.QuotaUsageGetRequest_builder{
				Tenant: "shared",
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.NotFound))

			visibility, err := auth.NewVisibility().
				AddVisibleTenants(testTenant).
				SetTotalProjectVisibility(testTenant).
				Build()
			Expect(err).ToNot(HaveOccurred())
			restricted := auth.NewMockTenancyLogic(ctrl)
			restricted.EXPECT().DetermineVisibility(gomock.Any()).
				Return(visibility, nil).
				AnyTimes()
			server, err := NewQuotaUsageServer().
				SetLogger(logger).
				SetTenancyLogic(restricted).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = server.Get(ctx, publicv1.QuotaUsageGetRequest_builder{
				Tenant: "another",
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.PermissionDenied))
		})
	})
})
