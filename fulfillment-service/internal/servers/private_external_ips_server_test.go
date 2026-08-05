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
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/database/dao"
)

var _ = Describe("Private external IPs server", func() {
	var (
		externalIPPoolDao *dao.GenericDAO[*privatev1.ExternalIPPool]
		externalIPDao     *dao.GenericDAO[*privatev1.ExternalIP]
	)

	BeforeEach(func() {
		var err error

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
	})

	createReadyExternalIPPool := func(ctx context.Context, total int64, allocated int64) string {
		resp, err := externalIPPoolDao.Create().SetObject(
			privatev1.ExternalIPPool_builder{
				Metadata: privatev1.Metadata_builder{
					Tenant: auth.SharedTenant,
				}.Build(),
				Spec: privatev1.ExternalIPPoolSpec_builder{
					Cidrs: []string{"10.0.0.0/24"},
				}.Build(),
				Status: privatev1.ExternalIPPoolStatus_builder{
					State:     privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY,
					Total:     total,
					Allocated: allocated,
					Available: total - allocated,
				}.Build(),
			}.Build(),
		).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		return resp.GetObject().GetId()
	}

	createExternalIPInState := func(
		server *PrivateExternalIPsServer,
		state privatev1.ExternalIPState,
	) *privatev1.ExternalIP {
		poolID := createReadyExternalIPPool(ctx, 100, 0)
		resp, err := server.Create(ctx, privatev1.ExternalIPsCreateRequest_builder{
			Object: privatev1.ExternalIP_builder{
				Metadata: privatev1.Metadata_builder{
					Tenant: auth.SharedTenant,
				}.Build(),
				Spec: privatev1.ExternalIPSpec_builder{
					Pool: poolID,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		object := resp.GetObject()

		object.SetStatus(privatev1.ExternalIPStatus_builder{
			State: state,
		}.Build())
		daoResp, err := externalIPDao.Update().SetObject(object).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		return daoResp.GetObject()
	}

	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			server, err := NewPrivateExternalIPsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewPrivateExternalIPsServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewPrivateExternalIPsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if attribution logic is not set", func() {
			server, err := NewPrivateExternalIPsServer().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("attribution logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("CRUD operations", func() {
		var (
			externalIPsServer *PrivateExternalIPsServer
			poolID            string
		)

		BeforeEach(func() {
			var err error
			poolID = createReadyExternalIPPool(ctx, 100, 0)
			externalIPsServer, err = NewPrivateExternalIPsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("creates ExternalIP with PENDING initial state", func() {
			response, err := externalIPsServer.Create(ctx, privatev1.ExternalIPsCreateRequest_builder{
				Object: privatev1.ExternalIP_builder{
					Metadata: privatev1.Metadata_builder{Tenant: auth.SharedTenant}.Build(),
					Spec:     privatev1.ExternalIPSpec_builder{Pool: poolID}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetStatus().GetState()).To(
				Equal(privatev1.ExternalIPState_EXTERNAL_IP_STATE_PENDING))
		})

		It("retrieves ExternalIP by ID", func() {
			createResponse, err := externalIPsServer.Create(ctx, privatev1.ExternalIPsCreateRequest_builder{
				Object: privatev1.ExternalIP_builder{
					Metadata: privatev1.Metadata_builder{Tenant: auth.SharedTenant}.Build(),
					Spec:     privatev1.ExternalIPSpec_builder{Pool: poolID}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			getResponse, err := externalIPsServer.Get(ctx, privatev1.ExternalIPsGetRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(proto.Equal(createResponse.GetObject(), getResponse.GetObject())).To(BeTrue())
		})

		It("lists ExternalIPs", func() {
			const count = 3
			for range count {
				_, err := externalIPsServer.Create(ctx, privatev1.ExternalIPsCreateRequest_builder{
					Object: privatev1.ExternalIP_builder{
						Metadata: privatev1.Metadata_builder{Tenant: auth.SharedTenant}.Build(),
						Spec:     privatev1.ExternalIPSpec_builder{Pool: poolID}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			response, err := externalIPsServer.List(ctx, privatev1.ExternalIPsListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(count))
		})

		It("soft deletes ExternalIP in ALLOCATED state", func() {
			createResponse, err := externalIPsServer.Create(ctx, privatev1.ExternalIPsCreateRequest_builder{
				Object: privatev1.ExternalIP_builder{
					Metadata: privatev1.Metadata_builder{
						Finalizers: []string{"test-finalizer"},
						Tenant:     auth.SharedTenant,
					}.Build(),
					Spec: privatev1.ExternalIPSpec_builder{Pool: poolID}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			object.GetStatus().SetState(privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED)
			updateResponse, err := externalIPsServer.Update(ctx, privatev1.ExternalIPsUpdateRequest_builder{
				Object: object,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object = updateResponse.GetObject()

			_, err = externalIPsServer.Delete(ctx, privatev1.ExternalIPsDeleteRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			getResponse, err := externalIPsServer.Get(ctx, privatev1.ExternalIPsGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetMetadata().GetDeletionTimestamp()).ToNot(BeNil())
		})
	})

	Describe("Pool validation on Create", func() {
		var externalIPsServer *PrivateExternalIPsServer

		BeforeEach(func() {
			var err error
			externalIPsServer, err = NewPrivateExternalIPsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("rejects Create when pool does not exist", func() {
			_, err := externalIPsServer.Create(ctx, privatev1.ExternalIPsCreateRequest_builder{
				Object: privatev1.ExternalIP_builder{
					Metadata: privatev1.Metadata_builder{Tenant: auth.SharedTenant}.Build(),
					Spec:     privatev1.ExternalIPSpec_builder{Pool: "nonexistent-pool-id"}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("does not exist"))
		})

		It("rejects Create when pool is not READY", func() {
			resp, err := externalIPPoolDao.Create().SetObject(
				privatev1.ExternalIPPool_builder{
					Metadata: privatev1.Metadata_builder{Tenant: auth.SharedTenant}.Build(),
					Spec:     privatev1.ExternalIPPoolSpec_builder{Cidrs: []string{"10.0.0.0/24"}}.Build(),
					Status: privatev1.ExternalIPPoolStatus_builder{
						State: privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_PENDING,
						Total: 10, Allocated: 0, Available: 10,
					}.Build(),
				}.Build(),
			).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			_, err = externalIPsServer.Create(ctx, privatev1.ExternalIPsCreateRequest_builder{
				Object: privatev1.ExternalIP_builder{
					Metadata: privatev1.Metadata_builder{Tenant: auth.SharedTenant}.Build(),
					Spec:     privatev1.ExternalIPSpec_builder{Pool: resp.GetObject().GetId()}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
			Expect(err.Error()).To(ContainSubstring("not in READY state"))
		})

		It("rejects Create when pool has no available capacity", func() {
			exhaustedPoolID := createReadyExternalIPPool(ctx, 10, 10)

			_, err := externalIPsServer.Create(ctx, privatev1.ExternalIPsCreateRequest_builder{
				Object: privatev1.ExternalIP_builder{
					Metadata: privatev1.Metadata_builder{Tenant: auth.SharedTenant}.Build(),
					Spec:     privatev1.ExternalIPSpec_builder{Pool: exhaustedPoolID}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
			Expect(err.Error()).To(ContainSubstring("no available capacity"))
		})

		It("rejects nil object on Create", func() {
			_, err := externalIPsServer.Create(ctx, privatev1.ExternalIPsCreateRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("external IP is mandatory"))
		})

		It("rejects empty pool on Create", func() {
			_, err := externalIPsServer.Create(ctx, privatev1.ExternalIPsCreateRequest_builder{
				Object: privatev1.ExternalIP_builder{
					Metadata: privatev1.Metadata_builder{Tenant: auth.SharedTenant}.Build(),
					Spec:     privatev1.ExternalIPSpec_builder{}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("spec.pool"))
		})
	})

	Describe("Capacity tracking", func() {
		var externalIPsServer *PrivateExternalIPsServer

		BeforeEach(func() {
			var err error
			externalIPsServer, err = NewPrivateExternalIPsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("decrements pool available on Create", func() {
			poolID := createReadyExternalIPPool(ctx, 10, 2)

			_, err := externalIPsServer.Create(ctx, privatev1.ExternalIPsCreateRequest_builder{
				Object: privatev1.ExternalIP_builder{
					Metadata: privatev1.Metadata_builder{Tenant: auth.SharedTenant}.Build(),
					Spec:     privatev1.ExternalIPSpec_builder{Pool: poolID}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			poolResp, err := externalIPPoolDao.Get().SetId(poolID).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			pool := poolResp.GetObject()
			Expect(pool.GetStatus().GetAllocated()).To(Equal(int64(3)))
			Expect(pool.GetStatus().GetAvailable()).To(Equal(int64(7)))
		})

		It("restores pool capacity on Delete", func() {
			poolID := createReadyExternalIPPool(ctx, 10, 0)

			createResponse, err := externalIPsServer.Create(ctx, privatev1.ExternalIPsCreateRequest_builder{
				Object: privatev1.ExternalIP_builder{
					Metadata: privatev1.Metadata_builder{Tenant: auth.SharedTenant}.Build(),
					Spec:     privatev1.ExternalIPSpec_builder{Pool: poolID}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			object.GetStatus().SetState(privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED)
			_, err = externalIPsServer.Update(ctx, privatev1.ExternalIPsUpdateRequest_builder{
				Object: object,
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = externalIPsServer.Delete(ctx, privatev1.ExternalIPsDeleteRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			poolResp, err := externalIPPoolDao.Get().SetId(poolID).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			pool := poolResp.GetObject()
			Expect(pool.GetStatus().GetAllocated()).To(Equal(int64(0)))
			Expect(pool.GetStatus().GetAvailable()).To(Equal(int64(10)))
		})
	})

	Describe("State machine enforcement", func() {
		var externalIPsServer *PrivateExternalIPsServer

		BeforeEach(func() {
			var err error
			externalIPsServer, err = NewPrivateExternalIPsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		setStateOnObject := func(object *privatev1.ExternalIP, state privatev1.ExternalIPState) {
			if object.GetStatus() == nil {
				object.SetStatus(privatev1.ExternalIPStatus_builder{State: state}.Build())
			} else {
				object.GetStatus().SetState(state)
			}
		}

		transitionTo := func(object *privatev1.ExternalIP, state privatev1.ExternalIPState) *privatev1.ExternalIP {
			setStateOnObject(object, state)
			resp, err := externalIPsServer.Update(ctx, privatev1.ExternalIPsUpdateRequest_builder{
				Object: object,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			return resp.GetObject()
		}

		It("accepts PENDING to ALLOCATED transition", func() {
			object := createExternalIPInState(externalIPsServer, privatev1.ExternalIPState_EXTERNAL_IP_STATE_PENDING)
			updated := transitionTo(object, privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED)
			Expect(updated.GetStatus().GetState()).To(Equal(privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED))
		})

		It("accepts PENDING to FAILED transition", func() {
			object := createExternalIPInState(externalIPsServer, privatev1.ExternalIPState_EXTERNAL_IP_STATE_PENDING)
			updated := transitionTo(object, privatev1.ExternalIPState_EXTERNAL_IP_STATE_FAILED)
			Expect(updated.GetStatus().GetState()).To(Equal(privatev1.ExternalIPState_EXTERNAL_IP_STATE_FAILED))
		})

		It("accepts ALLOCATED to FAILED transition", func() {
			object := createExternalIPInState(externalIPsServer, privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED)
			updated := transitionTo(object, privatev1.ExternalIPState_EXTERNAL_IP_STATE_FAILED)
			Expect(updated.GetStatus().GetState()).To(Equal(privatev1.ExternalIPState_EXTERNAL_IP_STATE_FAILED))
		})

		It("accepts ALLOCATED to DELETING transition", func() {
			object := createExternalIPInState(externalIPsServer, privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED)
			updated := transitionTo(object, privatev1.ExternalIPState_EXTERNAL_IP_STATE_DELETING)
			Expect(updated.GetStatus().GetState()).To(Equal(privatev1.ExternalIPState_EXTERNAL_IP_STATE_DELETING))
		})

		It("accepts FAILED to ALLOCATED transition", func() {
			object := createExternalIPInState(externalIPsServer, privatev1.ExternalIPState_EXTERNAL_IP_STATE_FAILED)
			updated := transitionTo(object, privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED)
			Expect(updated.GetStatus().GetState()).To(Equal(privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED))
		})

		It("rejects FAILED to PENDING transition", func() {
			object := createExternalIPInState(externalIPsServer, privatev1.ExternalIPState_EXTERNAL_IP_STATE_FAILED)
			setStateOnObject(object, privatev1.ExternalIPState_EXTERNAL_IP_STATE_PENDING)
			_, err := externalIPsServer.Update(ctx, privatev1.ExternalIPsUpdateRequest_builder{
				Object: object,
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
			Expect(err.Error()).To(ContainSubstring("invalid state transition"))
		})

		It("rejects PENDING to DELETING transition", func() {
			object := createExternalIPInState(externalIPsServer, privatev1.ExternalIPState_EXTERNAL_IP_STATE_PENDING)
			setStateOnObject(object, privatev1.ExternalIPState_EXTERNAL_IP_STATE_DELETING)
			_, err := externalIPsServer.Update(ctx, privatev1.ExternalIPsUpdateRequest_builder{
				Object: object,
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
		})

		It("skips state validation when UpdateMask excludes status.state", func() {
			object := createExternalIPInState(externalIPsServer, privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED)

			object.GetMetadata().Name = "updated-name"
			resp, err := externalIPsServer.Update(ctx, privatev1.ExternalIPsUpdateRequest_builder{
				Object: object,
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"metadata.name"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetObject().GetMetadata().GetName()).To(Equal("updated-name"))
		})
	})

	Describe("Delete constraint", func() {
		var externalIPsServer *PrivateExternalIPsServer

		BeforeEach(func() {
			var err error
			externalIPsServer, err = NewPrivateExternalIPsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("allows Delete when state is ALLOCATED", func() {
			object := createExternalIPInState(externalIPsServer, privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED)
			_, err := externalIPsServer.Delete(ctx, privatev1.ExternalIPsDeleteRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("rejects Delete when state is PENDING", func() {
			object := createExternalIPInState(externalIPsServer, privatev1.ExternalIPState_EXTERNAL_IP_STATE_PENDING)
			_, err := externalIPsServer.Delete(ctx, privatev1.ExternalIPsDeleteRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
			Expect(err.Error()).To(ContainSubstring("must be in ALLOCATED state"))
		})

		It("blocks deletion of default-labeled ExternalIP", func() {
			object := createExternalIPInState(externalIPsServer, privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED)
			object.GetMetadata().SetLabels(map[string]string{
				"osac.openshift.io/default": "true",
			})
			eipDao, err := dao.NewGenericDAO[*privatev1.ExternalIP]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = eipDao.Update().SetObject(object).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			_, err = externalIPsServer.Delete(ctx, privatev1.ExternalIPsDeleteRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
			Expect(err.Error()).To(ContainSubstring("default"))
			Expect(err.Error()).To(ContainSubstring("system-managed"))
		})
	})

	Describe("Pool immutability on Update", func() {
		var externalIPsServer *PrivateExternalIPsServer

		BeforeEach(func() {
			var err error
			externalIPsServer, err = NewPrivateExternalIPsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("rejects Update that changes pool", func() {
			poolA := createReadyExternalIPPool(ctx, 100, 0)
			poolB := createReadyExternalIPPool(ctx, 100, 0)

			createResp, err := externalIPsServer.Create(ctx, privatev1.ExternalIPsCreateRequest_builder{
				Object: privatev1.ExternalIP_builder{
					Metadata: privatev1.Metadata_builder{Tenant: auth.SharedTenant}.Build(),
					Spec:     privatev1.ExternalIPSpec_builder{Pool: poolA}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResp.GetObject()

			object.GetSpec().SetPool(poolB)
			_, err = externalIPsServer.Update(ctx, privatev1.ExternalIPsUpdateRequest_builder{
				Object: object,
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("spec.pool' is immutable"))
		})
	})
})
