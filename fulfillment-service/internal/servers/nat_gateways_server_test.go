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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/database/dao"
)

var _ = Describe("Public NAT gateways server", func() {
	Describe("Builder", func() {
		It("Builds successfully with required parameters", func() {
			s, err := NewNATGatewaysServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(s).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			s, err := NewNATGatewaysServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(s).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			s, err := NewNATGatewaysServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(s).To(BeNil())
		})

		It("Fails if attribution logic is not set", func() {
			s, err := NewNATGatewaysServer().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("attribution logic is mandatory"))
			Expect(s).To(BeNil())
		})
	})

	Describe("CRUD operations", func() {
		var (
			publicServer      *NATGatewaysServer
			vnDao             *dao.GenericDAO[*privatev1.VirtualNetwork]
			externalIPPoolDao *dao.GenericDAO[*privatev1.ExternalIPPool]
			externalIPDao     *dao.GenericDAO[*privatev1.ExternalIP]
			sharedPool        *privatev1.ExternalIPPool
		)

		BeforeEach(func() {
			var err error

			publicServer, err = NewNATGatewaysServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			vnDao, err = dao.NewGenericDAO[*privatev1.VirtualNetwork]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			externalIPPoolDao, err = dao.NewGenericDAO[*privatev1.ExternalIPPool]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			externalIPDao, err = dao.NewGenericDAO[*privatev1.ExternalIP]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			poolResp, err := externalIPPoolDao.Create().SetObject(
				privatev1.ExternalIPPool_builder{
					Metadata: privatev1.Metadata_builder{
						Tenant: auth.SharedTenant,
					}.Build(),
					Spec: privatev1.ExternalIPPoolSpec_builder{
						Cidrs: []string{"203.0.113.0/24"},
					}.Build(),
					Status: privatev1.ExternalIPPoolStatus_builder{
						State:     privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY,
						Total:     100,
						Allocated: 0,
						Available: 100,
					}.Build(),
				}.Build(),
			).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			sharedPool = poolResp.GetObject()
		})

		createVirtualNetwork := func() string {
			resp, err := vnDao.Create().SetObject(
				privatev1.VirtualNetwork_builder{
					Metadata: privatev1.Metadata_builder{
						Tenant: auth.SharedTenant,
					}.Build(),
				}.Build(),
			).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			return resp.GetObject().GetId()
		}

		createAllocatedExternalIP := func() *privatev1.ExternalIP {
			return createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, false)
		}

		It("creates and retrieves a NATGateway", func() {
			vnID := createVirtualNetwork()
			eip := createAllocatedExternalIP()
			createResp, err := publicServer.Create(ctx, publicv1.NATGatewaysCreateRequest_builder{
				Object: publicv1.NATGateway_builder{
					Metadata: publicv1.Metadata_builder{Tenant: auth.SharedTenant}.Build(),
					Spec: publicv1.NATGatewaySpec_builder{
						VirtualNetwork: vnID,
						ExternalIp:     eip.GetId(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(createResp.GetObject().GetId()).ToNot(BeEmpty())

			getResp, err := publicServer.Get(ctx, publicv1.NATGatewaysGetRequest_builder{
				Id: createResp.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResp.GetObject().GetId()).To(Equal(createResp.GetObject().GetId()))
		})

		It("lists NATGateways", func() {
			for range 3 {
				vnID := createVirtualNetwork()
				eip := createAllocatedExternalIP()
				_, err := publicServer.Create(ctx, publicv1.NATGatewaysCreateRequest_builder{
					Object: publicv1.NATGateway_builder{
						Metadata: publicv1.Metadata_builder{Tenant: auth.SharedTenant}.Build(),
						Spec: publicv1.NATGatewaySpec_builder{
							VirtualNetwork: vnID,
							ExternalIp:     eip.GetId(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			listResp, err := publicServer.List(ctx, publicv1.NATGatewaysListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(listResp.GetItems()).To(HaveLen(3))
		})

		It("updates a NATGateway", func() {
			vnID := createVirtualNetwork()
			eip := createAllocatedExternalIP()
			createResp, err := publicServer.Create(ctx, publicv1.NATGatewaysCreateRequest_builder{
				Object: publicv1.NATGateway_builder{
					Metadata: publicv1.Metadata_builder{Tenant: auth.SharedTenant}.Build(),
					Spec: publicv1.NATGatewaySpec_builder{
						VirtualNetwork: vnID,
						ExternalIp:     eip.GetId(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			object := createResp.GetObject()
			object.GetMetadata().SetName("updated-name")
			updateResp, err := publicServer.Update(ctx, publicv1.NATGatewaysUpdateRequest_builder{
				Object: object,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResp.GetObject().GetMetadata().GetName()).To(Equal("updated-name"))
		})

		It("deletes a NATGateway", func() {
			vnID := createVirtualNetwork()
			eip := createAllocatedExternalIP()
			createResp, err := publicServer.Create(ctx, publicv1.NATGatewaysCreateRequest_builder{
				Object: publicv1.NATGateway_builder{
					Metadata: publicv1.Metadata_builder{
						Tenant: auth.SharedTenant,
					}.Build(),
					Spec: publicv1.NATGatewaySpec_builder{
						VirtualNetwork: vnID,
						ExternalIp:     eip.GetId(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = publicServer.Delete(ctx, publicv1.NATGatewaysDeleteRequest_builder{
				Id: createResp.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})
	})
})
