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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
)

var _ = Describe("Private external IP pools server", func() {
	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			server, err := NewPrivateExternalIPPoolsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewPrivateExternalIPPoolsServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewPrivateExternalIPPoolsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		var (
			poolsServer *PrivateExternalIPPoolsServer
			ipsServer   *PrivateExternalIPsServer
		)

		BeforeEach(func() {
			var err error

			poolsServer, err = NewPrivateExternalIPPoolsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			ipsServer, err = NewPrivateExternalIPsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Creates a pool", func() {
			response, err := poolsServer.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
				Object: privatev1.ExternalIPPool_builder{
					Spec: privatev1.ExternalIPPoolSpec_builder{
						Cidrs:    []string{"192.168.1.0/24"},
						IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			object := response.GetObject()
			Expect(object.GetId()).ToNot(BeEmpty())
			Expect(object.GetSpec().GetCidrs()).To(ConsistOf("192.168.1.0/24"))
			Expect(object.GetSpec().GetIpFamily()).To(Equal(privatev1.IPFamily_IP_FAMILY_IPV4))
		})

		It("Lists pools", func() {
			const count = 5
			for i := range count {
				_, err := poolsServer.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
					Object: privatev1.ExternalIPPool_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("pool-%d", i),
						}.Build(),
						Spec: privatev1.ExternalIPPoolSpec_builder{
							Cidrs:    []string{fmt.Sprintf("10.%d.0.0/24", i)},
							IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			response, err := poolsServer.List(ctx, privatev1.ExternalIPPoolsListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(count))
		})

		It("Gets a pool", func() {
			createResponse, err := poolsServer.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
				Object: privatev1.ExternalIPPool_builder{
					Spec: privatev1.ExternalIPPoolSpec_builder{
						Cidrs:    []string{"192.168.1.0/24"},
						IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			getResponse, err := poolsServer.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(proto.Equal(createResponse.GetObject(), getResponse.GetObject())).To(BeTrue())
		})

		It("Updates a pool", func() {
			createResponse, err := poolsServer.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
				Object: privatev1.ExternalIPPool_builder{
					Spec: privatev1.ExternalIPPoolSpec_builder{
						Cidrs:    []string{"192.168.1.0/24"},
						IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			updateResponse, err := poolsServer.Update(ctx, privatev1.ExternalIPPoolsUpdateRequest_builder{
				Object: privatev1.ExternalIPPool_builder{
					Id: object.GetId(),
					Metadata: privatev1.Metadata_builder{
						Name: "my-pool",
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"metadata.name"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetMetadata().GetName()).To(Equal("my-pool"))
		})

		It("Deletes a pool", func() {
			createResponse, err := poolsServer.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
				Object: privatev1.ExternalIPPool_builder{
					Metadata: privatev1.Metadata_builder{
						Finalizers: []string{"test"},
					}.Build(),
					Spec: privatev1.ExternalIPPoolSpec_builder{
						Cidrs:    []string{"192.168.1.0/24"},
						IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			_, err = poolsServer.Delete(ctx, privatev1.ExternalIPPoolsDeleteRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			getResponse, err := poolsServer.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetMetadata().GetDeletionTimestamp()).ToNot(BeNil())
		})

		It("Rejects deletion when allocated ExternalIPs reference the pool", func() {
			createPoolResponse, err := poolsServer.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
				Object: privatev1.ExternalIPPool_builder{
					Spec: privatev1.ExternalIPPoolSpec_builder{
						Cidrs:    []string{"10.0.0.0/24"},
						IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			poolID := createPoolResponse.GetObject().GetId()

			_, err = poolsServer.Update(ctx, privatev1.ExternalIPPoolsUpdateRequest_builder{
				Object: privatev1.ExternalIPPool_builder{
					Id: poolID,
					Status: privatev1.ExternalIPPoolStatus_builder{
						State:     privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY,
						Total:     10,
						Allocated: 0,
						Available: 10,
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"status"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = ipsServer.Create(ctx, privatev1.ExternalIPsCreateRequest_builder{
				Object: privatev1.ExternalIP_builder{
					Spec: privatev1.ExternalIPSpec_builder{
						Pool: poolID,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = poolsServer.Delete(ctx, privatev1.ExternalIPPoolsDeleteRequest_builder{
				Id: poolID,
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("external IP(s) are still allocated"))
		})

		It("Allows deletion when no ExternalIPs reference the pool", func() {
			createPoolResponse, err := poolsServer.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
				Object: privatev1.ExternalIPPool_builder{
					Spec: privatev1.ExternalIPPoolSpec_builder{
						Cidrs:    []string{"10.1.0.0/24"},
						IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			poolID := createPoolResponse.GetObject().GetId()

			_, err = poolsServer.Delete(ctx, privatev1.ExternalIPPoolsDeleteRequest_builder{
				Id: poolID,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Describe("Validation tests", func() {
		var poolsServer *PrivateExternalIPPoolsServer

		BeforeEach(func() {
			var err error

			poolsServer, err = NewPrivateExternalIPPoolsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		Context("ip_family validation", func() {
			It("rejects pool with unspecified ip_family", func() {
				pool := privatev1.ExternalIPPool_builder{
					Spec: privatev1.ExternalIPPoolSpec_builder{
						Cidrs: []string{"192.168.1.0/24"},
					}.Build(),
				}.Build()

				err := poolsServer.validateCreate(ctx, pool)
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(err.Error()).To(ContainSubstring("ip_family"))
			})
		})

		Context("CIDRs required", func() {
			It("rejects pool with no CIDRs", func() {
				pool := privatev1.ExternalIPPool_builder{
					Spec: privatev1.ExternalIPPoolSpec_builder{
						IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
					}.Build(),
				}.Build()

				err := poolsServer.validateCreate(ctx, pool)
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(err.Error()).To(ContainSubstring("cidrs"))
			})
		})

		Context("CIDR format validation", func() {
			It("rejects malformed CIDR", func() {
				pool := privatev1.ExternalIPPool_builder{
					Spec: privatev1.ExternalIPPoolSpec_builder{
						Cidrs:    []string{"not-a-cidr"},
						IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
					}.Build(),
				}.Build()

				err := poolsServer.validateCreate(ctx, pool)
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(err.Error()).To(ContainSubstring("invalid CIDR format"))
			})
		})

		Context("IP family consistency", func() {
			It("rejects IPv6 CIDR in an IPv4 pool", func() {
				pool := privatev1.ExternalIPPool_builder{
					Spec: privatev1.ExternalIPPoolSpec_builder{
						Cidrs:    []string{"2001:db8::/64"},
						IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
					}.Build(),
				}.Build()

				err := poolsServer.validateCreate(ctx, pool)
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(err.Error()).To(ContainSubstring("IPv6"))
			})

			It("canonicalizes non-canonical CIDRs on Create", func() {
				pool := privatev1.ExternalIPPool_builder{
					Spec: privatev1.ExternalIPPoolSpec_builder{
						Cidrs:    []string{"10.0.1.5/24"},
						IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
					}.Build(),
				}.Build()

				err := poolsServer.validateCreate(ctx, pool)
				Expect(err).ToNot(HaveOccurred())
				Expect(pool.GetSpec().GetCidrs()).To(Equal([]string{"10.0.1.0/24"}))
			})
		})

		Context("Cross-pool CIDR overlap detection", func() {
			It("rejects a pool whose CIDR exactly matches an existing pool", func() {
				_, err := poolsServer.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
					Object: privatev1.ExternalIPPool_builder{
						Metadata: privatev1.Metadata_builder{Name: "pool-a"}.Build(),
						Spec: privatev1.ExternalIPPoolSpec_builder{
							Cidrs:    []string{"10.0.0.0/24"},
							IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				_, err = poolsServer.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
					Object: privatev1.ExternalIPPool_builder{
						Metadata: privatev1.Metadata_builder{Name: "pool-b"}.Build(),
						Spec: privatev1.ExternalIPPoolSpec_builder{
							Cidrs:    []string{"10.0.0.0/24"},
							IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.AlreadyExists))
				Expect(err.Error()).To(ContainSubstring("overlaps"))
				Expect(err.Error()).To(ContainSubstring("pool-a"))
			})

			It("accepts a pool with a non-overlapping CIDR", func() {
				_, err := poolsServer.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
					Object: privatev1.ExternalIPPool_builder{
						Spec: privatev1.ExternalIPPoolSpec_builder{
							Cidrs:    []string{"10.0.0.0/24"},
							IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				_, err = poolsServer.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
					Object: privatev1.ExternalIPPool_builder{
						Spec: privatev1.ExternalIPPoolSpec_builder{
							Cidrs:    []string{"10.0.1.0/24"},
							IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})
		})

		Context("Intra-pool CIDR overlap detection", func() {
			It("rejects a pool whose second CIDR is a subset of the first", func() {
				pool := privatev1.ExternalIPPool_builder{
					Spec: privatev1.ExternalIPPoolSpec_builder{
						Cidrs:    []string{"10.0.0.0/24", "10.0.0.0/25"},
						IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
					}.Build(),
				}.Build()

				err := poolsServer.validateCreate(ctx, pool)
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(err.Error()).To(ContainSubstring("overlaps"))
			})
		})

		Context("Capacity calculation on Create", func() {
			It("sets status.total to 254 for a /24 IPv4 CIDR", func() {
				response, err := poolsServer.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
					Object: privatev1.ExternalIPPool_builder{
						Spec: privatev1.ExternalIPPoolSpec_builder{
							Cidrs:    []string{"192.168.1.0/24"},
							IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetObject().GetStatus().GetTotal()).To(BeNumerically("==", 254))
			})

			It("sets status.available equal to status.total on a new pool", func() {
				response, err := poolsServer.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
					Object: privatev1.ExternalIPPool_builder{
						Spec: privatev1.ExternalIPPoolSpec_builder{
							Cidrs:    []string{"10.0.0.0/24"},
							IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				status := response.GetObject().GetStatus()
				Expect(status.GetAvailable()).To(Equal(status.GetTotal()))
				Expect(status.GetAllocated()).To(BeNumerically("==", 0))
			})
		})

		Context("Immutability on Update", func() {
			It("prevents changing spec.cidrs after creation", func() {
				createResponse, err := poolsServer.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
					Object: privatev1.ExternalIPPool_builder{
						Spec: privatev1.ExternalIPPoolSpec_builder{
							Cidrs:    []string{"10.0.0.0/24"},
							IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				id := createResponse.GetObject().GetId()

				_, err = poolsServer.Update(ctx, privatev1.ExternalIPPoolsUpdateRequest_builder{
					Object: privatev1.ExternalIPPool_builder{
						Id: id,
						Spec: privatev1.ExternalIPPoolSpec_builder{
							Cidrs:    []string{"10.0.1.0/24"},
							IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(err.Error()).To(ContainSubstring("cidrs"))
				Expect(err.Error()).To(ContainSubstring("immutable"))
			})

			It("prevents changing spec.ip_family after creation", func() {
				createResponse, err := poolsServer.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
					Object: privatev1.ExternalIPPool_builder{
						Spec: privatev1.ExternalIPPoolSpec_builder{
							Cidrs:    []string{"10.0.0.0/24"},
							IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				id := createResponse.GetObject().GetId()

				_, err = poolsServer.Update(ctx, privatev1.ExternalIPPoolsUpdateRequest_builder{
					Object: privatev1.ExternalIPPool_builder{
						Id: id,
						Spec: privatev1.ExternalIPPoolSpec_builder{
							IpFamily: privatev1.IPFamily_IP_FAMILY_IPV6,
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(err.Error()).To(ContainSubstring("ip_family"))
				Expect(err.Error()).To(ContainSubstring("immutable"))
			})

			It("allows metadata-only Update (spec fields omitted)", func() {
				createResponse, err := poolsServer.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
					Object: privatev1.ExternalIPPool_builder{
						Spec: privatev1.ExternalIPPoolSpec_builder{
							Cidrs:    []string{"10.0.0.0/24"},
							IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				id := createResponse.GetObject().GetId()

				updateResponse, err := poolsServer.Update(ctx, privatev1.ExternalIPPoolsUpdateRequest_builder{
					Object: privatev1.ExternalIPPool_builder{
						Id: id,
						Metadata: privatev1.Metadata_builder{
							Name: "my-pool",
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"metadata.name"},
					},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(updateResponse.GetObject().GetMetadata().GetName()).To(Equal("my-pool"))
				Expect(updateResponse.GetObject().GetSpec().GetCidrs()).To(ConsistOf("10.0.0.0/24"))
			})
		})
	})
})
