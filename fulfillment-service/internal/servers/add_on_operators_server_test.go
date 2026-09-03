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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
)

var _ = Describe("Add-on operators server", func() {
	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			server, err := NewAddOnOperatorsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewAddOnOperatorsServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewAddOnOperatorsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Published filtering", func() {
		var publicServer *AddOnOperatorsServer
		var privateServer *PrivateAddOnOperatorsServer

		BeforeEach(func() {
			var err error

			privateServer, err = NewPrivateAddOnOperatorsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			publicServer, err = NewAddOnOperatorsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("List returns only published operators", func() {
			// Create a published operator:
			_, err := privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("published-%s", uuid.New()[24:32]),
					}.Build(),
					Title:     "Published Operator",
					Published: new(true),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Create an unpublished operator:
			_, err = privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("unpublished-%s", uuid.New()[24:32]),
					}.Build(),
					Title: "Unpublished Operator",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// List via public server — should only see published:
			response, err := publicServer.List(ctx, publicv1.AddOnOperatorsListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			titles := make([]string, len(response.GetItems()))
			for i, item := range response.GetItems() {
				Expect(item.GetPublished()).To(BeTrue())
				titles[i] = item.GetTitle()
			}
			Expect(titles).To(ContainElement("Published Operator"))
			Expect(titles).ToNot(ContainElement("Unpublished Operator"))
		})

		It("Get returns published operator", func() {
			createResponse, err := privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("published-%s", uuid.New()[24:32]),
					}.Build(),
					Title:     "Published Operator",
					Published: new(true),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			getResponse, err := publicServer.Get(ctx, publicv1.AddOnOperatorsGetRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetTitle()).To(Equal("Published Operator"))
		})

		It("Get returns NotFound for unpublished operator", func() {
			createResponse, err := privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("unpublished-%s", uuid.New()[24:32]),
					}.Build(),
					Title: "Unpublished Operator",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = publicServer.Get(ctx, publicv1.AddOnOperatorsGetRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.NotFound))
		})

		It("List with filter composes correctly with published filter", func() {
			// Create a published operator with a specific title:
			_, err := privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("gpu-%s", uuid.New()[24:32]),
					}.Build(),
					Title:     "GPU Operator",
					Published: new(true),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// List with a title filter:
			response, err := publicServer.List(ctx, publicv1.AddOnOperatorsListRequest_builder{
				Filter: new("this.title == 'GPU Operator'"),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).ToNot(BeEmpty())
			for _, item := range response.GetItems() {
				Expect(item.GetTitle()).To(Equal("GPU Operator"))
				Expect(item.GetPublished()).To(BeTrue())
			}
		})
	})

	Describe("Tenant scope filtering", func() {
		var privateServer *PrivateAddOnOperatorsServer

		BeforeEach(func() {
			var err error
			privateServer, err = NewPrivateAddOnOperatorsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		// makeTenancyForTenants creates a mock tenancy logic with visibility restricted to the given
		// tenants plus the shared tenant and the test-tenant (the ownership tenant for all objects
		// created in this suite). This lets the delegate's GenericServer see the objects while the
		// public server's tenant scope filter exercises the top-level tenant field.
		makeTenancyForTenants := func(tenants ...string) *auth.MockTenancyLogic {
			builder := auth.NewVisibility()
			builder.AddVisibleTenants(auth.SharedTenant)
			builder.AddVisibleTenants(testTenant)
			for _, t := range tenants {
				builder.AddVisibleTenants(t)
			}
			visibility, visErr := builder.Build()
			Expect(visErr).ToNot(HaveOccurred())
			mock := auth.NewMockTenancyLogic(ctrl)
			mock.EXPECT().DetermineAssignableTenants(gomock.Any()).
				Return(auth.AllTenants, nil).
				AnyTimes()
			mock.EXPECT().DetermineDefaultTenant(gomock.Any()).
				Return(testTenant, nil).
				AnyTimes()
			mock.EXPECT().DetermineVisibility(gomock.Any()).
				Return(visibility, nil).
				AnyTimes()
			return mock
		}

		It("List returns global operators to any tenant", func() {
			_, err := privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("global-%s", uuid.New()[24:32]),
					}.Build(),
					Title:     "Global Operator",
					Published: new(true),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			restrictedTenancy := makeTenancyForTenants("other-tenant")
			publicServer, err := NewAddOnOperatorsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(restrictedTenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			response, err := publicServer.List(ctx, publicv1.AddOnOperatorsListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			titles := make([]string, len(response.GetItems()))
			for i, item := range response.GetItems() {
				titles[i] = item.GetTitle()
			}
			Expect(titles).To(ContainElement("Global Operator"))
		})

		It("List returns tenant-scoped operators to matching tenant", func() {
			_, err := privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("scoped-%s", uuid.New()[24:32]),
					}.Build(),
					Title:     "Scoped Operator",
					Published: new(true),
					Tenant:    "tenant-a",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			matchingTenancy := makeTenancyForTenants("tenant-a")
			publicServer, err := NewAddOnOperatorsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(matchingTenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			response, err := publicServer.List(ctx, publicv1.AddOnOperatorsListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			titles := make([]string, len(response.GetItems()))
			for i, item := range response.GetItems() {
				titles[i] = item.GetTitle()
			}
			Expect(titles).To(ContainElement("Scoped Operator"))
		})

		It("List hides tenant-scoped operators from non-matching tenant", func() {
			_, err := privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("hidden-%s", uuid.New()[24:32]),
					}.Build(),
					Title:     "Hidden Operator",
					Published: new(true),
					Tenant:    "tenant-a",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			otherTenancy := makeTenancyForTenants("tenant-b")
			publicServer, err := NewAddOnOperatorsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(otherTenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			response, err := publicServer.List(ctx, publicv1.AddOnOperatorsListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			for _, item := range response.GetItems() {
				Expect(item.GetTitle()).ToNot(Equal("Hidden Operator"))
			}
		})

		It("Get returns tenant-scoped operator to matching tenant", func() {
			createResponse, err := privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("scoped-%s", uuid.New()[24:32]),
					}.Build(),
					Title:     "Scoped Operator",
					Published: new(true),
					Tenant:    "tenant-a",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			matchingTenancy := makeTenancyForTenants("tenant-a")
			publicServer, err := NewAddOnOperatorsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(matchingTenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			getResponse, err := publicServer.Get(ctx, publicv1.AddOnOperatorsGetRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetTitle()).To(Equal("Scoped Operator"))
		})

		It("List total excludes hidden tenant-scoped operators", func() {
			// Create a global published operator:
			_, err := privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("global-%s", uuid.New()[24:32]),
					}.Build(),
					Title:     "Visible Global",
					Published: new(true),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Create a tenant-scoped published operator hidden from the caller:
			_, err = privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("hidden-%s", uuid.New()[24:32]),
					}.Build(),
					Title:     "Hidden Scoped",
					Published: new(true),
					Tenant:    "tenant-a",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			otherTenancy := makeTenancyForTenants("tenant-b")
			publicServer, err := NewAddOnOperatorsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(otherTenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			response, err := publicServer.List(ctx, publicv1.AddOnOperatorsListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetTotal()).To(Equal(response.GetSize()))
			for _, item := range response.GetItems() {
				Expect(item.GetTitle()).ToNot(Equal("Hidden Scoped"))
			}
		})

		It("List applies pagination after tenant visibility filtering", func() {
			_, err := privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata: privatev1.Metadata_builder{Name: fmt.Sprintf("hidden-first-%s", uuid.New()[24:32])}.Build(),
					Title:    "Hidden First", Published: new(true), Tenant: "tenant-a",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			_, err = privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata: privatev1.Metadata_builder{Name: fmt.Sprintf("visible-second-%s", uuid.New()[24:32])}.Build(),
					Title:    "Visible Second", Published: new(true),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			publicServer, err := NewAddOnOperatorsServer().
				SetLogger(logger).SetAttributionLogic(attribution).
				SetTenancyLogic(makeTenancyForTenants("tenant-b")).Build()
			Expect(err).ToNot(HaveOccurred())

			response, err := publicServer.List(ctx, publicv1.AddOnOperatorsListRequest_builder{
				Filter: new("this.title == 'Visible Second'"),
				Limit:  new(int32(1)),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetTotal()).To(Equal(int32(1)))
			Expect(response.GetSize()).To(Equal(int32(1)))
			Expect(response.GetItems()[0].GetTitle()).To(Equal("Visible Second"))
		})

		It("Get returns NotFound for tenant-scoped operator from non-matching tenant", func() {
			createResponse, err := privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("hidden-%s", uuid.New()[24:32]),
					}.Build(),
					Title:     "Hidden Operator",
					Published: new(true),
					Tenant:    "tenant-a",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			otherTenancy := makeTenancyForTenants("tenant-b")
			publicServer, err := NewAddOnOperatorsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(otherTenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			_, err = publicServer.Get(ctx, publicv1.AddOnOperatorsGetRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.NotFound))
		})
	})
})
