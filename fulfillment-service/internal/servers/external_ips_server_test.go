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
	"fmt"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/references"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Public external IPs server", func() {
	Describe("Builder", func() {
		It("Builds successfully with required parameters", func() {
			s, err := NewExternalIPsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(s).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			s, err := NewExternalIPsServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(s).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			s, err := NewExternalIPsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(s).To(BeNil())
		})

		It("Fails if attribution logic is not set", func() {
			s, err := NewExternalIPsServer().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("attribution logic is mandatory"))
			Expect(s).To(BeNil())
		})
	})

	Describe("CRUD operations", func() {
		var (
			publicServer      *ExternalIPsServer
			externalIPPoolDao *dao.GenericDAO[*privatev1.ExternalIPPool]
		)

		BeforeEach(func() {
			var err error

			publicServer, err = NewExternalIPsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			externalIPPoolDao, err = dao.NewGenericDAO[*privatev1.ExternalIPPool]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Create a READY pool for CRUD tests:
			_, err = externalIPPoolDao.Create().SetObject(
				privatev1.ExternalIPPool_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   "test-pool",
						Tenant: auth.SharedTenant,
					}.Build(),
					Spec: privatev1.ExternalIPPoolSpec_builder{
						Cidrs: []string{"10.0.0.0/24"},
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
		})

		// getPoolID returns the ID of the test pool created in BeforeEach.
		getPoolID := func() string {
			externalIPPoolDao, err := dao.NewGenericDAO[*privatev1.ExternalIPPool]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			resp, err := externalIPPoolDao.List().Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetItems()).ToNot(BeEmpty())
			return resp.GetItems()[0].GetId()
		}

		It("creates and retrieves an ExternalIP", func() {
			poolID := getPoolID()
			createResp, err := publicServer.Create(ctx, publicv1.ExternalIPsCreateRequest_builder{
				Object: publicv1.ExternalIP_builder{
					Metadata: publicv1.Metadata_builder{Name: "test-eip", Tenant: testTenant}.Build(),
					Spec:     publicv1.ExternalIPSpec_builder{Pool: publicv1.ExternalIPPoolReference_builder{Id: poolID}.Build()}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(createResp.GetObject().GetId()).ToNot(BeEmpty())

			getResp, err := publicServer.Get(ctx, publicv1.ExternalIPsGetRequest_builder{
				Id: createResp.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResp.GetObject().GetId()).To(Equal(createResp.GetObject().GetId()))
		})

		It("lists ExternalIPs", func() {
			poolID := getPoolID()
			for range 3 {
				_, err := publicServer.Create(ctx, publicv1.ExternalIPsCreateRequest_builder{
					Object: publicv1.ExternalIP_builder{
						Metadata: publicv1.Metadata_builder{Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]), Tenant: testTenant}.Build(),
						Spec:     publicv1.ExternalIPSpec_builder{Pool: publicv1.ExternalIPPoolReference_builder{Id: poolID}.Build()}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			listResp, err := publicServer.List(ctx, publicv1.ExternalIPsListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(listResp.GetItems()).To(HaveLen(3))
		})

		It("deletes an ExternalIP", func() {
			poolID := getPoolID()
			createResp, err := publicServer.Create(ctx, publicv1.ExternalIPsCreateRequest_builder{
				Object: publicv1.ExternalIP_builder{
					Metadata: publicv1.Metadata_builder{Name: "test-eip", Tenant: testTenant}.Build(),
					Spec:     publicv1.ExternalIPSpec_builder{Pool: publicv1.ExternalIPPoolReference_builder{Id: poolID}.Build()}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Transition to ALLOCATED via private server (public API doesn't expose state changes):
			privateServer, err := NewPrivateExternalIPsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			getResp, err := privateServer.Get(ctx, privatev1.ExternalIPsGetRequest_builder{
				Id: createResp.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := getResp.GetObject()
			object.GetStatus().SetState(privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED)
			object.GetStatus().SetStateTransitionTime(timestamppb.Now())
			_, err = privateServer.Update(ctx, privatev1.ExternalIPsUpdateRequest_builder{
				Object:     object,
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state", "status.state_transition_time"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = publicServer.Delete(ctx, publicv1.ExternalIPsDeleteRequest_builder{
				Id: createResp.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("rejects caller-supplied output status on Create", func() {
			_, err := publicServer.Create(ctx, publicv1.ExternalIPsCreateRequest_builder{
				Object: publicv1.ExternalIP_builder{
					Metadata: publicv1.Metadata_builder{Name: "output-on-create", Tenant: testTenant}.Build(),
					Spec:     publicv1.ExternalIPSpec_builder{Pool: publicv1.ExternalIPPoolReference_builder{Id: getPoolID()}.Build()}.Build(),
					Status: publicv1.ExternalIPStatus_builder{
						Attached: true,
						Address:  "198.51.100.12",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(codes.InvalidArgument))
		})

		It("creates an ExternalIP from a shared pool given by name and returns its name and ID", func() {
			validator, err := references.NewReferenceValidator().SetLogger(logger).Build()
			Expect(err).ToNot(HaveOccurred())
			references.RegisterDAOLookup(validator, "osac.public.v1.ExternalIPPoolReference", externalIPPoolDao)
			request := publicv1.ExternalIPsCreateRequest_builder{
				Object: publicv1.ExternalIP_builder{
					Metadata: publicv1.Metadata_builder{Name: "by-name-eip"}.Build(),
					Spec: publicv1.ExternalIPSpec_builder{
						Pool: publicv1.ExternalIPPoolReference_builder{Name: "test-pool"}.Build(),
					}.Build(),
				}.Build(),
			}.Build()

			response, err := validator.UnaryServer(ctx, request,
				&grpc.UnaryServerInfo{FullMethod: publicv1.ExternalIPs_Create_FullMethodName},
				func(callCtx context.Context, message any) (any, error) {
					return publicServer.Create(callCtx, message.(*publicv1.ExternalIPsCreateRequest))
				})
			Expect(err).ToNot(HaveOccurred())

			pool := response.(*publicv1.ExternalIPsCreateResponse).GetObject().GetSpec().GetPool()
			Expect(pool.GetName()).To(Equal("test-pool"))
			Expect(pool.GetId()).To(Equal(getPoolID()))
		})

		It("walks a small pool through allocation, exhaustion, release and deletion", func() {
			poolsServer, err := NewPrivateExternalIPPoolsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			privateServer, err := NewPrivateExternalIPsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			_, err = reuseNetworkClassDAO.Create().SetObject(privatev1.NetworkClass_builder{
				Metadata: privatev1.Metadata_builder{Name: "test-network-class", Tenant: testTenant}.Build(),
				Status: privatev1.NetworkClassStatus_builder{
					State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
					Hub:   "network-hub-a",
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			created, err := poolsServer.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
				Object: privatev1.ExternalIPPool_builder{
					Metadata: privatev1.Metadata_builder{Name: "small-pool"}.Build(),
					Spec: privatev1.ExternalIPPoolSpec_builder{
						Cidrs:    []string{"10.1.0.0/30"},
						IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			poolID := created.GetObject().GetId()
			_, err = poolsServer.Update(ctx, privatev1.ExternalIPPoolsUpdateRequest_builder{
				Object: privatev1.ExternalIPPool_builder{
					Id: poolID,
					Status: privatev1.ExternalIPPoolStatus_builder{
						State: privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY,
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			expectCapacity := func(total, allocated, available int64) {
				GinkgoHelper()
				response, err := poolsServer.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{Id: poolID}.Build())
				Expect(err).ToNot(HaveOccurred())
				status := response.GetObject().GetStatus()
				Expect(status.GetTotal()).To(Equal(total))
				Expect(status.GetAllocated()).To(Equal(allocated))
				Expect(status.GetAvailable()).To(Equal(available))
			}
			create := func(name string) (*publicv1.ExternalIPsCreateResponse, error) {
				return publicServer.Create(ctx, publicv1.ExternalIPsCreateRequest_builder{
					Object: publicv1.ExternalIP_builder{
						Metadata: publicv1.Metadata_builder{Name: name}.Build(),
						Spec: publicv1.ExternalIPSpec_builder{
							Pool: publicv1.ExternalIPPoolReference_builder{Id: poolID}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
			}
			allocate := func(name string) string {
				GinkgoHelper()
				response, err := create(name)
				Expect(err).ToNot(HaveOccurred())
				id := response.GetObject().GetId()
				stored, err := privateServer.Get(ctx, privatev1.ExternalIPsGetRequest_builder{Id: id}.Build())
				Expect(err).ToNot(HaveOccurred())
				object := stored.GetObject()
				object.GetStatus().SetState(privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED)
				object.GetStatus().SetStateTransitionTime(timestamppb.Now())
				_, err = privateServer.Update(ctx, privatev1.ExternalIPsUpdateRequest_builder{
					Object:     object,
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state", "status.state_transition_time"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				return id
			}
			release := func(id string) {
				GinkgoHelper()
				_, err := publicServer.Delete(ctx, publicv1.ExternalIPsDeleteRequest_builder{Id: id}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			By("allocating the first address")
			expectCapacity(2, 0, 2)
			first := allocate("first")
			expectCapacity(2, 1, 1)

			By("rejecting a create once the pool is full")
			second := allocate("second")
			expectCapacity(2, 2, 0)
			_, err = create("over-capacity")
			Expect(grpcstatus.Code(err)).To(Equal(codes.FailedPrecondition))
			expectCapacity(2, 2, 0)

			By("restoring capacity when an address is released, then filling the pool again")
			release(first)
			expectCapacity(2, 1, 1)
			third := allocate("third")
			expectCapacity(2, 2, 0)

			By("blocking the pool delete while addresses remain")
			_, err = poolsServer.Delete(ctx, privatev1.ExternalIPPoolsDeleteRequest_builder{Id: poolID}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(codes.FailedPrecondition))

			By("deleting the pool once every address is released")
			release(second)
			release(third)
			expectCapacity(2, 0, 2)
			_, err = poolsServer.Delete(ctx, privatev1.ExternalIPPoolsDeleteRequest_builder{Id: poolID}.Build())
			Expect(err).ToNot(HaveOccurred())
			_, err = poolsServer.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{Id: poolID}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(codes.NotFound))
		})
	})
})
