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
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/database/dao"
)

var _ = Describe("Private NAT gateways server", func() {
	var (
		vnDao             *dao.GenericDAO[*privatev1.VirtualNetwork]
		externalIPPoolDao *dao.GenericDAO[*privatev1.ExternalIPPool]
		externalIPDao     *dao.GenericDAO[*privatev1.ExternalIP]
		sharedPool        *privatev1.ExternalIPPool
	)

	BeforeEach(func() {
		var err error
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

	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			server, err := NewPrivateNATGatewaysServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewPrivateNATGatewaysServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewPrivateNATGatewaysServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if attribution logic is not set", func() {
			server, err := NewPrivateNATGatewaysServer().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("attribution logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("CRUD operations", func() {
		var (
			natGatewaysServer *PrivateNATGatewaysServer
			vnID              string
		)

		BeforeEach(func() {
			var err error
			vnID = createVirtualNetwork()
			natGatewaysServer, err = NewPrivateNATGatewaysServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("creates NATGateway with PENDING initial state", func() {
			eip := createAllocatedExternalIP()
			response, err := natGatewaysServer.Create(ctx, privatev1.NATGatewaysCreateRequest_builder{
				Object: privatev1.NATGateway_builder{
					Metadata: privatev1.Metadata_builder{Tenant: auth.SharedTenant}.Build(),
					Spec: privatev1.NATGatewaySpec_builder{
						VirtualNetwork: vnID,
						ExternalIp:     eip.GetId(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetStatus().GetState()).To(
				Equal(privatev1.NATGatewayState_NAT_GATEWAY_STATE_PENDING))
		})

		It("overrides client-provided state to PENDING on Create", func() {
			eip := createAllocatedExternalIP()
			response, err := natGatewaysServer.Create(ctx, privatev1.NATGatewaysCreateRequest_builder{
				Object: privatev1.NATGateway_builder{
					Metadata: privatev1.Metadata_builder{Tenant: auth.SharedTenant}.Build(),
					Spec: privatev1.NATGatewaySpec_builder{
						VirtualNetwork: vnID,
						ExternalIp:     eip.GetId(),
					}.Build(),
					Status: privatev1.NATGatewayStatus_builder{
						State: privatev1.NATGatewayState_NAT_GATEWAY_STATE_READY,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetStatus().GetState()).To(
				Equal(privatev1.NATGatewayState_NAT_GATEWAY_STATE_PENDING))
		})

		It("retrieves NATGateway by ID", func() {
			eip := createAllocatedExternalIP()
			createResponse, err := natGatewaysServer.Create(ctx, privatev1.NATGatewaysCreateRequest_builder{
				Object: privatev1.NATGateway_builder{
					Metadata: privatev1.Metadata_builder{Tenant: auth.SharedTenant}.Build(),
					Spec: privatev1.NATGatewaySpec_builder{
						VirtualNetwork: vnID,
						ExternalIp:     eip.GetId(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			getResponse, err := natGatewaysServer.Get(ctx, privatev1.NATGatewaysGetRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(proto.Equal(createResponse.GetObject(), getResponse.GetObject())).To(BeTrue())
		})

		It("lists NATGateways", func() {
			const count = 3
			for range count {
				vn := createVirtualNetwork()
				eip := createAllocatedExternalIP()
				_, err := natGatewaysServer.Create(ctx, privatev1.NATGatewaysCreateRequest_builder{
					Object: privatev1.NATGateway_builder{
						Metadata: privatev1.Metadata_builder{Tenant: auth.SharedTenant}.Build(),
						Spec: privatev1.NATGatewaySpec_builder{
							VirtualNetwork: vn,
							ExternalIp:     eip.GetId(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			response, err := natGatewaysServer.List(ctx, privatev1.NATGatewaysListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(count))
		})

		It("updates NATGateway metadata", func() {
			eip := createAllocatedExternalIP()
			createResponse, err := natGatewaysServer.Create(ctx, privatev1.NATGatewaysCreateRequest_builder{
				Object: privatev1.NATGateway_builder{
					Metadata: privatev1.Metadata_builder{Tenant: auth.SharedTenant}.Build(),
					Spec: privatev1.NATGatewaySpec_builder{
						VirtualNetwork: vnID,
						ExternalIp:     eip.GetId(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			object := createResponse.GetObject()
			object.GetMetadata().SetName("updated-name")
			updateResponse, err := natGatewaysServer.Update(ctx, privatev1.NATGatewaysUpdateRequest_builder{
				Object: object,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetMetadata().GetName()).To(Equal("updated-name"))
		})

		It("soft deletes NATGateway", func() {
			eip := createAllocatedExternalIP()
			createResponse, err := natGatewaysServer.Create(ctx, privatev1.NATGatewaysCreateRequest_builder{
				Object: privatev1.NATGateway_builder{
					Metadata: privatev1.Metadata_builder{
						Finalizers: []string{"test-finalizer"},
						Tenant:     auth.SharedTenant,
					}.Build(),
					Spec: privatev1.NATGatewaySpec_builder{
						VirtualNetwork: vnID,
						ExternalIp:     eip.GetId(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = natGatewaysServer.Delete(ctx, privatev1.NATGatewaysDeleteRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			getResponse, err := natGatewaysServer.Get(ctx, privatev1.NATGatewaysGetRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetMetadata().GetDeletionTimestamp()).ToNot(BeNil())
		})

		It("signals NATGateway", func() {
			eip := createAllocatedExternalIP()
			createResponse, err := natGatewaysServer.Create(ctx, privatev1.NATGatewaysCreateRequest_builder{
				Object: privatev1.NATGateway_builder{
					Metadata: privatev1.Metadata_builder{Tenant: auth.SharedTenant}.Build(),
					Spec: privatev1.NATGatewaySpec_builder{
						VirtualNetwork: vnID,
						ExternalIp:     eip.GetId(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = natGatewaysServer.Signal(ctx, privatev1.NATGatewaysSignalRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Describe("Validation", func() {
		var natGatewaysServer *PrivateNATGatewaysServer

		BeforeEach(func() {
			var err error
			natGatewaysServer, err = NewPrivateNATGatewaysServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("rejects Create with nil object", func() {
			_, err := natGatewaysServer.Create(ctx, privatev1.NATGatewaysCreateRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("NAT gateway is mandatory"))
		})

		It("rejects Create with nil spec", func() {
			_, err := natGatewaysServer.Create(ctx, privatev1.NATGatewaysCreateRequest_builder{
				Object: privatev1.NATGateway_builder{
					Metadata: privatev1.Metadata_builder{Tenant: auth.SharedTenant}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("spec is mandatory"))
		})

		It("rejects Create with empty virtual_network", func() {
			eip := createAllocatedExternalIP()
			_, err := natGatewaysServer.Create(ctx, privatev1.NATGatewaysCreateRequest_builder{
				Object: privatev1.NATGateway_builder{
					Metadata: privatev1.Metadata_builder{Tenant: auth.SharedTenant}.Build(),
					Spec: privatev1.NATGatewaySpec_builder{
						ExternalIp: eip.GetId(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("spec.virtual_network"))
		})

		It("rejects Create with empty external_ip", func() {
			vnID := createVirtualNetwork()
			_, err := natGatewaysServer.Create(ctx, privatev1.NATGatewaysCreateRequest_builder{
				Object: privatev1.NATGateway_builder{
					Metadata: privatev1.Metadata_builder{Tenant: auth.SharedTenant}.Build(),
					Spec: privatev1.NATGatewaySpec_builder{
						VirtualNetwork: vnID,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("spec.external_ip"))
		})
	})

	Describe("ExternalIP reference validation", func() {
		var natGatewaysServer *PrivateNATGatewaysServer

		BeforeEach(func() {
			var err error
			natGatewaysServer, err = NewPrivateNATGatewaysServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("rejects Create when ExternalIP does not exist", func() {
			vnID := createVirtualNetwork()
			_, err := natGatewaysServer.Create(ctx, privatev1.NATGatewaysCreateRequest_builder{
				Object: privatev1.NATGateway_builder{
					Metadata: privatev1.Metadata_builder{Tenant: auth.SharedTenant}.Build(),
					Spec: privatev1.NATGatewaySpec_builder{
						VirtualNetwork: vnID,
						ExternalIp:     "nonexistent-id",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("does not exist"))
		})

		It("rejects Create when ExternalIP is not in ALLOCATED state", func() {
			vnID := createVirtualNetwork()
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_PENDING, false)
			_, err := natGatewaysServer.Create(ctx, privatev1.NATGatewaysCreateRequest_builder{
				Object: privatev1.NATGateway_builder{
					Metadata: privatev1.Metadata_builder{Tenant: auth.SharedTenant}.Build(),
					Spec: privatev1.NATGatewaySpec_builder{
						VirtualNetwork: vnID,
						ExternalIp:     eip.GetId(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
			Expect(err.Error()).To(ContainSubstring("not in ALLOCATED state"))
		})

		It("rejects Create when ExternalIP is already attached", func() {
			vnID := createVirtualNetwork()
			eip := createExternalIPInState(ctx, externalIPDao, sharedPool.GetId(),
				privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED, true)
			_, err := natGatewaysServer.Create(ctx, privatev1.NATGatewaysCreateRequest_builder{
				Object: privatev1.NATGateway_builder{
					Metadata: privatev1.Metadata_builder{Tenant: auth.SharedTenant}.Build(),
					Spec: privatev1.NATGatewaySpec_builder{
						VirtualNetwork: vnID,
						ExternalIp:     eip.GetId(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
			Expect(err.Error()).To(ContainSubstring("already attached"))
		})
	})

	Describe("Immutable fields", func() {
		var natGatewaysServer *PrivateNATGatewaysServer

		BeforeEach(func() {
			var err error
			natGatewaysServer, err = NewPrivateNATGatewaysServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("rejects update of spec.virtual_network", func() {
			vnID := createVirtualNetwork()
			eip := createAllocatedExternalIP()
			createResponse, err := natGatewaysServer.Create(ctx, privatev1.NATGatewaysCreateRequest_builder{
				Object: privatev1.NATGateway_builder{
					Metadata: privatev1.Metadata_builder{Tenant: auth.SharedTenant}.Build(),
					Spec: privatev1.NATGatewaySpec_builder{
						VirtualNetwork: vnID,
						ExternalIp:     eip.GetId(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			object := createResponse.GetObject()
			newVN := createVirtualNetwork()
			object.GetSpec().SetVirtualNetwork(newVN)
			_, err = natGatewaysServer.Update(ctx, privatev1.NATGatewaysUpdateRequest_builder{
				Object: object,
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.virtual_network"},
				},
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("immutable"))
		})

		It("rejects update of spec.external_ip", func() {
			vnID := createVirtualNetwork()
			eip := createAllocatedExternalIP()
			createResponse, err := natGatewaysServer.Create(ctx, privatev1.NATGatewaysCreateRequest_builder{
				Object: privatev1.NATGateway_builder{
					Metadata: privatev1.Metadata_builder{Tenant: auth.SharedTenant}.Build(),
					Spec: privatev1.NATGatewaySpec_builder{
						VirtualNetwork: vnID,
						ExternalIp:     eip.GetId(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			object := createResponse.GetObject()
			newEIP := createAllocatedExternalIP()
			object.GetSpec().SetExternalIp(newEIP.GetId())
			_, err = natGatewaysServer.Update(ctx, privatev1.NATGatewaysUpdateRequest_builder{
				Object: object,
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.external_ip"},
				},
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("immutable"))
		})
	})

	Describe("ExternalIP attached flag", func() {
		var natGatewaysServer *PrivateNATGatewaysServer

		BeforeEach(func() {
			var err error
			natGatewaysServer, err = NewPrivateNATGatewaysServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("sets attached flag to true on Create", func() {
			vnID := createVirtualNetwork()
			eip := createAllocatedExternalIP()
			_, err := natGatewaysServer.Create(ctx, privatev1.NATGatewaysCreateRequest_builder{
				Object: privatev1.NATGateway_builder{
					Metadata: privatev1.Metadata_builder{Tenant: auth.SharedTenant}.Build(),
					Spec: privatev1.NATGatewaySpec_builder{
						VirtualNetwork: vnID,
						ExternalIp:     eip.GetId(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			getResp, err := externalIPDao.Get().SetId(eip.GetId()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(getResp.GetObject().GetStatus().GetAttached()).To(BeTrue())
		})

		It("clears attached flag on Delete", func() {
			vnID := createVirtualNetwork()
			eip := createAllocatedExternalIP()
			createResponse, err := natGatewaysServer.Create(ctx, privatev1.NATGatewaysCreateRequest_builder{
				Object: privatev1.NATGateway_builder{
					Metadata: privatev1.Metadata_builder{
						Finalizers: []string{"test-finalizer"},
						Tenant:     auth.SharedTenant,
					}.Build(),
					Spec: privatev1.NATGatewaySpec_builder{
						VirtualNetwork: vnID,
						ExternalIp:     eip.GetId(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = natGatewaysServer.Delete(ctx, privatev1.NATGatewaysDeleteRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			getResp, err := externalIPDao.Get().SetId(eip.GetId()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(getResp.GetObject().GetStatus().GetAttached()).To(BeFalse())
		})

		It("blocks deletion of default-labeled NATGateway", func() {
			vnID := createVirtualNetwork()
			eip := createAllocatedExternalIP()
			createResponse, err := natGatewaysServer.Create(ctx, privatev1.NATGatewaysCreateRequest_builder{
				Object: privatev1.NATGateway_builder{
					Metadata: privatev1.Metadata_builder{
						Finalizers: []string{"test-finalizer"},
						Tenant:     auth.SharedTenant,
						Labels: map[string]string{
							"osac.openshift.io/default": "true",
						},
					}.Build(),
					Spec: privatev1.NATGatewaySpec_builder{
						VirtualNetwork: vnID,
						ExternalIp:     eip.GetId(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = natGatewaysServer.Delete(ctx, privatev1.NATGatewaysDeleteRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
			Expect(err.Error()).To(ContainSubstring("default"))
			Expect(err.Error()).To(ContainSubstring("system-managed"))
		})
	})
})
