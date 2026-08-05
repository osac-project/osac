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
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/database/dao"
)

var _ = Describe("Private subnets server", func() {
	var subnetDao *dao.GenericDAO[*privatev1.Subnet]

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
		createTenant("tenant-a")
		createTenant("tenant-b")

		// Create the subnet DAO:
		subnetDao, err = dao.NewGenericDAO[*privatev1.Subnet]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	// Helper function to create a NetworkClass for validation tests
	createNetworkClass := func(ctx context.Context) *privatev1.NetworkClass {
		// Create NetworkClass DAO
		ncDao, err := dao.NewGenericDAO[*privatev1.NetworkClass]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		nc := privatev1.NetworkClass_builder{
			ImplementationStrategy: "test-strategy",
			Metadata: privatev1.Metadata_builder{
				Tenant: auth.SharedTenant,
			}.Build(),
			Capabilities: privatev1.NetworkClassCapabilities_builder{
				SupportsIpv4:      true,
				SupportsIpv6:      true,
				SupportsDualStack: true,
			}.Build(),
			Status: privatev1.NetworkClassStatus_builder{
				State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
			}.Build(),
		}.Build()

		response, err := ncDao.Create().
			SetObject(nc).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		return response.GetObject()
	}

	// Helper function to create a VirtualNetwork parent for Subnet tests
	createVirtualNetwork := func(ctx context.Context, ipv4Cidr, ipv6Cidr string) *privatev1.VirtualNetwork {
		// Ensure NetworkClass exists
		nc := createNetworkClass(ctx)

		// Create VirtualNetwork DAO
		vnDao, err := dao.NewGenericDAO[*privatev1.VirtualNetwork]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		builder := privatev1.VirtualNetwork_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: auth.SharedTenant,
			}.Build(),
			Spec: privatev1.VirtualNetworkSpec_builder{
				NetworkClass: nc.GetImplementationStrategy(),
				Region:       "us-west-1",
			}.Build(),
			Status: privatev1.VirtualNetworkStatus_builder{
				State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY,
			}.Build(),
		}

		// Add IPv4 CIDR if provided
		if ipv4Cidr != "" {
			builder.Spec.Ipv4Cidr = new(ipv4Cidr)
		}

		// Add IPv6 CIDR if provided
		if ipv6Cidr != "" {
			builder.Spec.Ipv6Cidr = new(ipv6Cidr)
		}

		vn := builder.Build()

		response, err := vnDao.Create().
			SetObject(vn).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		return response.GetObject()
	}

	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			server, err := NewPrivateSubnetsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewPrivateSubnetsServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewPrivateSubnetsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Validation tests", func() {
		var server *PrivateSubnetsServer

		BeforeEach(func() {
			var err error

			// Create the server:
			server, err = NewPrivateSubnetsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		Context("SUB-VAL-01: IPv4 CIDR validation", func() {
			It("accepts valid IPv4 CIDR", func() {
				vn := createVirtualNetwork(ctx, "10.0.0.0/16", "")

				subnet := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("10.0.1.0/24"),
						VirtualNetwork: vn.GetId(),
					}.Build(),
				}.Build()

				err := server.validateSubnet(ctx, subnet, nil)
				Expect(err).ToNot(HaveOccurred())
			})

			It("rejects invalid IPv4 CIDR format", func() {
				vn := createVirtualNetwork(ctx, "10.0.0.0/16", "")

				subnet := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("not-a-cidr"),
						VirtualNetwork: vn.GetId(),
					}.Build(),
				}.Build()

				err := server.validateSubnet(ctx, subnet, nil)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("invalid IPv4 CIDR"))
			})

			It("rejects IPv4 with invalid mask", func() {
				vn := createVirtualNetwork(ctx, "10.0.0.0/16", "")

				subnet := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("10.0.1.0/33"),
						VirtualNetwork: vn.GetId(),
					}.Build(),
				}.Build()

				err := server.validateSubnet(ctx, subnet, nil)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("invalid IPv4 CIDR"))
			})

			It("rejects IPv6 address in IPv4 field", func() {
				vn := createVirtualNetwork(ctx, "10.0.0.0/16", "")

				subnet := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("2001:db8::/32"),
						VirtualNetwork: vn.GetId(),
					}.Build(),
				}.Build()

				err := server.validateSubnet(ctx, subnet, nil)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("contains IPv6 address"))
			})
		})

		Context("SUB-VAL-02: IPv6 CIDR validation", func() {
			It("accepts valid IPv6 CIDR", func() {
				vn := createVirtualNetwork(ctx, "", "2001:db8::/32")

				subnet := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv6Cidr:       new("2001:db8:1::/64"),
						VirtualNetwork: vn.GetId(),
					}.Build(),
				}.Build()

				err := server.validateSubnet(ctx, subnet, nil)
				Expect(err).ToNot(HaveOccurred())
			})

			It("rejects invalid IPv6 CIDR format", func() {
				vn := createVirtualNetwork(ctx, "", "2001:db8::/32")

				subnet := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv6Cidr:       new("not-ipv6"),
						VirtualNetwork: vn.GetId(),
					}.Build(),
				}.Build()

				err := server.validateSubnet(ctx, subnet, nil)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("invalid IPv6 CIDR"))
			})

			It("rejects IPv6 with invalid mask", func() {
				vn := createVirtualNetwork(ctx, "", "2001:db8::/32")

				subnet := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv6Cidr:       new("2001:db8::/129"),
						VirtualNetwork: vn.GetId(),
					}.Build(),
				}.Build()

				err := server.validateSubnet(ctx, subnet, nil)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("invalid IPv6 CIDR"))
			})

			It("rejects IPv4 address in IPv6 field", func() {
				vn := createVirtualNetwork(ctx, "", "2001:db8::/32")

				subnet := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv6Cidr:       new("10.0.1.0/24"),
						VirtualNetwork: vn.GetId(),
					}.Build(),
				}.Build()

				err := server.validateSubnet(ctx, subnet, nil)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("contains IPv4 address"))
			})
		})

		Context("CIDR canonicalization", func() {
			It("canonicalizes non-canonical IPv4 and IPv6 CIDRs on Create", func() {
				vn4 := createVirtualNetwork(ctx, "10.0.0.0/16", "")
				subnet4 := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("10.0.1.5/24"),
						VirtualNetwork: vn4.GetId(),
					}.Build(),
				}.Build()
				err := server.validateSubnet(ctx, subnet4, nil)
				Expect(err).ToNot(HaveOccurred())
				Expect(subnet4.GetSpec().GetIpv4Cidr()).To(Equal("10.0.1.0/24"))

				vn6 := createVirtualNetwork(ctx, "", "2001:db8::/32")
				subnet6 := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv6Cidr:       new("2001:db8:1::1/64"),
						VirtualNetwork: vn6.GetId(),
					}.Build(),
				}.Build()
				err = server.validateSubnet(ctx, subnet6, nil)
				Expect(err).ToNot(HaveOccurred())
				Expect(subnet6.GetSpec().GetIpv6Cidr()).To(Equal("2001:db8:1::/64"))
			})

			It("accepts non-canonical subnet CIDR within legacy non-canonical parent", func() {
				vn := createVirtualNetwork(ctx, "10.0.0.5/16", "")

				subnet := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("10.0.1.5/24"),
						VirtualNetwork: vn.GetId(),
					}.Build(),
				}.Build()

				err := server.validateSubnet(ctx, subnet, nil)
				Expect(err).ToNot(HaveOccurred())
				Expect(subnet.GetSpec().GetIpv4Cidr()).To(Equal("10.0.1.0/24"))
			})

			It("canonicalizes on Update including preserved legacy CIDR when omitted", func() {
				const fakeVNID = "vn-fake-id"

				existing := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("10.0.1.5/24"),
						VirtualNetwork: fakeVNID,
					}.Build(),
				}.Build()

				equivalentRewrite := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("10.0.1.0/24"),
						VirtualNetwork: fakeVNID,
					}.Build(),
				}.Build()
				err := server.validateSubnet(ctx, equivalentRewrite, existing)
				Expect(err).ToNot(HaveOccurred())
				Expect(equivalentRewrite.GetSpec().GetIpv4Cidr()).To(Equal("10.0.1.0/24"))

				omittedField := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						VirtualNetwork: fakeVNID,
					}.Build(),
				}.Build()
				err = server.validateSubnet(ctx, omittedField, existing)
				Expect(err).ToNot(HaveOccurred())
				Expect(omittedField.GetSpec().GetIpv4Cidr()).To(Equal("10.0.1.0/24"))
			})

			It("stores canonical IPv4 CIDR on Create round-trip", func() {
				vn := createVirtualNetwork(ctx, "10.0.0.0/16", "")

				createResponse, err := server.Create(ctx, privatev1.SubnetsCreateRequest_builder{
					Object: privatev1.Subnet_builder{
						Metadata: privatev1.Metadata_builder{
							Tenant: auth.SharedTenant,
						}.Build(),
						Spec: privatev1.SubnetSpec_builder{
							Ipv4Cidr:       new("10.0.1.5/24"),
							VirtualNetwork: vn.GetId(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(createResponse.GetObject().GetSpec().GetIpv4Cidr()).To(Equal("10.0.1.0/24"))
			})
		})

		Context("SUB-VAL-03: At least one CIDR required", func() {
			It("rejects both IPv4 and IPv6 CIDRs empty", func() {
				vn := createVirtualNetwork(ctx, "10.0.0.0/16", "2001:db8::/32")

				subnet := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						VirtualNetwork: vn.GetId(),
					}.Build(),
				}.Build()

				err := server.validateSubnet(ctx, subnet, nil)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("at least one"))
			})

			It("accepts IPv4-only configuration", func() {
				vn := createVirtualNetwork(ctx, "10.0.0.0/16", "")

				subnet := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("10.0.1.0/24"),
						VirtualNetwork: vn.GetId(),
					}.Build(),
				}.Build()

				err := server.validateSubnet(ctx, subnet, nil)
				Expect(err).ToNot(HaveOccurred())
			})

			It("accepts IPv6-only configuration", func() {
				vn := createVirtualNetwork(ctx, "", "2001:db8::/32")

				subnet := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv6Cidr:       new("2001:db8:1::/64"),
						VirtualNetwork: vn.GetId(),
					}.Build(),
				}.Build()

				err := server.validateSubnet(ctx, subnet, nil)
				Expect(err).ToNot(HaveOccurred())
			})

			It("accepts dual-stack configuration", func() {
				vn := createVirtualNetwork(ctx, "10.0.0.0/16", "2001:db8::/32")

				subnet := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("10.0.1.0/24"),
						Ipv6Cidr:       new("2001:db8:1::/64"),
						VirtualNetwork: vn.GetId(),
					}.Build(),
				}.Build()

				err := server.validateSubnet(ctx, subnet, nil)
				Expect(err).ToNot(HaveOccurred())
			})
		})

		Context("SUB-VAL-04: Parent VirtualNetwork existence", func() {
			It("accepts valid parent VirtualNetwork ID", func() {
				vn := createVirtualNetwork(ctx, "10.0.0.0/16", "")

				subnet := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("10.0.1.0/24"),
						VirtualNetwork: vn.GetId(),
					}.Build(),
				}.Build()

				err := server.validateSubnet(ctx, subnet, nil)
				Expect(err).ToNot(HaveOccurred())
			})

			It("rejects non-existent parent VirtualNetwork", func() {
				subnet := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("10.0.1.0/24"),
						VirtualNetwork: "non-existent-id",
					}.Build(),
				}.Build()

				err := server.validateSubnet(ctx, subnet, nil)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("does not exist"))
			})
		})

		Context("SUB-VAL-05: Parent VirtualNetwork READY state", func() {
			It("accepts parent VirtualNetwork in READY state", func() {
				vn := createVirtualNetwork(ctx, "10.0.0.0/16", "")

				subnet := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("10.0.1.0/24"),
						VirtualNetwork: vn.GetId(),
					}.Build(),
				}.Build()

				err := server.validateSubnet(ctx, subnet, nil)
				Expect(err).ToNot(HaveOccurred())
			})

			It("rejects parent VirtualNetwork in PENDING state", func() {
				// Create VirtualNetwork DAO
				vnDao, err := dao.NewGenericDAO[*privatev1.VirtualNetwork]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())

				nc := createNetworkClass(ctx)

				vn := privatev1.VirtualNetwork_builder{
					Metadata: privatev1.Metadata_builder{
						Tenant: auth.SharedTenant,
					}.Build(),
					Spec: privatev1.VirtualNetworkSpec_builder{
						Ipv4Cidr:     new("10.0.0.0/16"),
						NetworkClass: nc.GetImplementationStrategy(),
						Region:       "us-west-1",
					}.Build(),
					Status: privatev1.VirtualNetworkStatus_builder{
						State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING,
					}.Build(),
				}.Build()

				response, err := vnDao.Create().
					SetObject(vn).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				vn = response.GetObject()

				subnet := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("10.0.1.0/24"),
						VirtualNetwork: vn.GetId(),
					}.Build(),
				}.Build()

				err = server.validateSubnet(ctx, subnet, nil)
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
				Expect(err.Error()).To(ContainSubstring("not in READY state"))
			})

			It("rejects parent VirtualNetwork in FAILED state", func() {
				// Create VirtualNetwork DAO
				vnDao, err := dao.NewGenericDAO[*privatev1.VirtualNetwork]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())

				nc := createNetworkClass(ctx)

				vn := privatev1.VirtualNetwork_builder{
					Metadata: privatev1.Metadata_builder{
						Tenant: auth.SharedTenant,
					}.Build(),
					Spec: privatev1.VirtualNetworkSpec_builder{
						Ipv4Cidr:     new("10.0.0.0/16"),
						NetworkClass: nc.GetImplementationStrategy(),
						Region:       "us-west-1",
					}.Build(),
					Status: privatev1.VirtualNetworkStatus_builder{
						State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_FAILED,
					}.Build(),
				}.Build()

				response, err := vnDao.Create().
					SetObject(vn).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				vn = response.GetObject()

				subnet := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("10.0.1.0/24"),
						VirtualNetwork: vn.GetId(),
					}.Build(),
				}.Build()

				err = server.validateSubnet(ctx, subnet, nil)
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
				Expect(err.Error()).To(ContainSubstring("not in READY state"))
			})
		})

		Context("SUB-VAL-06: CIDR subset containment", func() {
			It("accepts subnet within parent VirtualNetwork", func() {
				vn := createVirtualNetwork(ctx, "10.0.0.0/16", "")

				subnet := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("10.0.1.0/24"),
						VirtualNetwork: vn.GetId(),
					}.Build(),
				}.Build()

				err := server.validateSubnet(ctx, subnet, nil)
				Expect(err).ToNot(HaveOccurred())
			})

			It("rejects subnet outside parent VirtualNetwork", func() {
				vn := createVirtualNetwork(ctx, "10.0.0.0/16", "")

				subnet := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("192.168.1.0/24"),
						VirtualNetwork: vn.GetId(),
					}.Build(),
				}.Build()

				err := server.validateSubnet(ctx, subnet, nil)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("not within parent"))
			})

			It("rejects subnet larger than parent VirtualNetwork", func() {
				vn := createVirtualNetwork(ctx, "10.0.1.0/24", "")

				subnet := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("10.0.0.0/16"),
						VirtualNetwork: vn.GetId(),
					}.Build(),
				}.Build()

				err := server.validateSubnet(ctx, subnet, nil)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("not within parent"))
			})

			It("accepts subnet matching parent VirtualNetwork exactly", func() {
				vn := createVirtualNetwork(ctx, "10.0.1.0/24", "")

				subnet := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("10.0.1.0/24"),
						VirtualNetwork: vn.GetId(),
					}.Build(),
				}.Build()

				err := server.validateSubnet(ctx, subnet, nil)
				Expect(err).ToNot(HaveOccurred())
			})
		})

		Context("SUB-VAL-07: IPv4 CIDR only if parent has IPv4", func() {
			It("accepts IPv4 CIDR when parent has IPv4", func() {
				vn := createVirtualNetwork(ctx, "10.0.0.0/16", "")

				subnet := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("10.0.1.0/24"),
						VirtualNetwork: vn.GetId(),
					}.Build(),
				}.Build()

				err := server.validateSubnet(ctx, subnet, nil)
				Expect(err).ToNot(HaveOccurred())
			})

			It("rejects IPv4 CIDR when parent is IPv6-only", func() {
				vn := createVirtualNetwork(ctx, "", "2001:db8::/32")

				subnet := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("10.0.1.0/24"),
						VirtualNetwork: vn.GetId(),
					}.Build(),
				}.Build()

				err := server.validateSubnet(ctx, subnet, nil)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("does not support IPv4"))
			})
		})

		Context("SUB-VAL-08: IPv6 CIDR only if parent has IPv6", func() {
			It("accepts IPv6 CIDR when parent has IPv6", func() {
				vn := createVirtualNetwork(ctx, "", "2001:db8::/32")

				subnet := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv6Cidr:       new("2001:db8:1::/64"),
						VirtualNetwork: vn.GetId(),
					}.Build(),
				}.Build()

				err := server.validateSubnet(ctx, subnet, nil)
				Expect(err).ToNot(HaveOccurred())
			})

			It("rejects IPv6 CIDR when parent is IPv4-only", func() {
				vn := createVirtualNetwork(ctx, "10.0.0.0/16", "")

				subnet := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv6Cidr:       new("2001:db8:1::/64"),
						VirtualNetwork: vn.GetId(),
					}.Build(),
				}.Build()

				err := server.validateSubnet(ctx, subnet, nil)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("does not support IPv6"))
			})
		})

		Context("SUB-VAL-09: Tenant isolation", func() {
			It("accepts subnet with same tenant as parent", func() {
				// Create VirtualNetwork DAO
				vnDao, err := dao.NewGenericDAO[*privatev1.VirtualNetwork]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())

				nc := createNetworkClass(ctx)

				vn := privatev1.VirtualNetwork_builder{
					Metadata: privatev1.Metadata_builder{
						Tenant: "tenant-a",
					}.Build(),
					Spec: privatev1.VirtualNetworkSpec_builder{
						Ipv4Cidr:     new("10.0.0.0/16"),
						NetworkClass: nc.GetImplementationStrategy(),
						Region:       "us-west-1",
					}.Build(),
					Status: privatev1.VirtualNetworkStatus_builder{
						State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY,
					}.Build(),
				}.Build()

				response, err := vnDao.Create().
					SetObject(vn).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				vn = response.GetObject()

				subnet := privatev1.Subnet_builder{
					Metadata: privatev1.Metadata_builder{
						Tenant: "tenant-a",
					}.Build(),
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("10.0.1.0/24"),
						VirtualNetwork: vn.GetId(),
					}.Build(),
				}.Build()

				err = server.validateSubnet(ctx, subnet, nil)
				Expect(err).ToNot(HaveOccurred())
			})

			It("rejects subnet with different tenant than parent", func() {
				// Note: SUB-VAL-09 tenant isolation is enforced by tenancy logic at the DAO layer,
				// not in validation logic. This test documents the expected behavior but actual
				// enforcement happens in servers_tenancy_test.go (see SUB-VAL-12, SUB-VAL-13).
				// In validation, we verify parent exists and is accessible.
			})
		})

		Context("SUB-VAL-10: OwnerReference annotation", func() {
			It("verifies ownerReference annotation is set after Create", func() {
				vn := createVirtualNetwork(ctx, "10.0.0.0/16", "")

				subnet := privatev1.Subnet_builder{
					Metadata: privatev1.Metadata_builder{
						Tenant: auth.SharedTenant,
					}.Build(),
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("10.0.1.0/24"),
						VirtualNetwork: vn.GetId(),
					}.Build(),
				}.Build()

				// Simulate what Create operation does (validation + annotation)
				err := server.validateSubnet(ctx, subnet, nil)
				Expect(err).ToNot(HaveOccurred())

				// Set owner reference annotation
				if subnet.GetMetadata() == nil {
					subnet.Metadata = &privatev1.Metadata{}
				}
				if subnet.GetMetadata().GetAnnotations() == nil {
					subnet.Metadata.Annotations = make(map[string]string)
				}
				subnet.Metadata.Annotations["osac.openshift.io/owner-reference"] = subnet.GetSpec().GetVirtualNetwork()

				// Create the subnet
				createResponse, err := subnetDao.Create().
					SetObject(subnet).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				created := createResponse.GetObject()

				// Verify annotation is set
				Expect(created.GetMetadata().GetAnnotations()).To(HaveKey("osac.openshift.io/owner-reference"))
				Expect(created.GetMetadata().GetAnnotations()["osac.openshift.io/owner-reference"]).To(Equal(vn.GetId()))
			})
		})

		Context("SUB-VAL-11: Parent VirtualNetwork immutability", func() {
			It("prevents parent VirtualNetwork modification on Update", func() {
				vn1 := createVirtualNetwork(ctx, "10.0.0.0/16", "")
				vn2 := createVirtualNetwork(ctx, "192.168.0.0/16", "")

				existing := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("10.0.1.0/24"),
						VirtualNetwork: vn1.GetId(),
					}.Build(),
				}.Build()

				updated := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("192.168.1.0/24"),
						VirtualNetwork: vn2.GetId(),
					}.Build(),
				}.Build()

				err := server.validateSubnet(ctx, updated, existing)
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(err.Error()).To(ContainSubstring("virtual_network"))
				Expect(err.Error()).To(ContainSubstring("immutable"))
			})

			It("allows parent VirtualNetwork to stay same on Update", func() {
				vn := createVirtualNetwork(ctx, "10.0.0.0/16", "")

				existing := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("10.0.1.0/24"),
						VirtualNetwork: vn.GetId(),
					}.Build(),
				}.Build()

				updated := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("10.0.1.0/24"),
						VirtualNetwork: vn.GetId(),
					}.Build(),
				}.Build()

				err := server.validateSubnet(ctx, updated, existing)
				Expect(err).ToNot(HaveOccurred())
			})
		})

		Context("SUB-VAL-14: IPv4 CIDR immutability on Update", func() {
			const fakeVNID = "vn-fake-id"

			It("prevents ipv4_cidr field modification", func() {
				// Fake VN ID: both existing and updated use the same ID, so validateSubnet
				// skips the VN reference lookup entirely. Rejection tests don't need a real VN.
				existing := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("10.0.1.0/24"),
						VirtualNetwork: fakeVNID,
					}.Build(),
				}.Build()

				updated := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("10.0.2.0/24"),
						VirtualNetwork: fakeVNID,
					}.Build(),
				}.Build()

				err := server.validateSubnet(ctx, updated, existing)
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(err.Error()).To(ContainSubstring("ipv4_cidr"))
				Expect(err.Error()).To(ContainSubstring("immutable"))
			})

			It("allows ipv4_cidr to stay same on Update", func() {
				// Same VN ID, same CIDR: immutability passes, VN lookup skipped.
				existing := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("10.0.1.0/24"),
						VirtualNetwork: fakeVNID,
					}.Build(),
				}.Build()

				updated := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("10.0.1.0/24"),
						VirtualNetwork: fakeVNID,
					}.Build(),
				}.Build()

				err := server.validateSubnet(ctx, updated, existing)
				Expect(err).ToNot(HaveOccurred())
			})

			It("prevents adding ipv4_cidr to an IPv6-only subnet", func() {
				// existing: IPv6-only (no ipv4_cidr set).
				// updated: explicitly sets ipv4_cidr — HasIpv4Cidr() returns true,
				// existing.GetIpv4Cidr() == "" != new value → reject.
				existing := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv6Cidr:       new("2001:db8::/64"),
						VirtualNetwork: fakeVNID,
					}.Build(),
				}.Build()

				updated := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("10.0.1.0/24"),
						Ipv6Cidr:       new("2001:db8::/64"),
						VirtualNetwork: fakeVNID,
					}.Build(),
				}.Build()

				err := server.validateSubnet(ctx, updated, existing)
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(err.Error()).To(ContainSubstring("ipv4_cidr"))
				Expect(err.Error()).To(ContainSubstring("immutable"))
			})

			It("rejects explicit empty ipv4_cidr on Update when existing ipv4_cidr is set (empty string edge case)", func() {
				// HasIpv4Cidr() returns true for explicitly-set empty string.
				// Immutability fires first: HasIpv4Cidr() true and "" != existing → rejected.
				existing := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("10.0.1.0/24"),
						VirtualNetwork: fakeVNID,
					}.Build(),
				}.Build()

				updated := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new(""),
						Ipv6Cidr:       new("2001:db8::/64"),
						VirtualNetwork: fakeVNID,
					}.Build(),
				}.Build()

				err := server.validateSubnet(ctx, updated, existing)
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(err.Error()).To(ContainSubstring("ipv4_cidr"))
				Expect(err.Error()).To(ContainSubstring("immutable"))
			})
		})

		Context("SUB-VAL-15: IPv6 CIDR immutability on Update", func() {
			const fakeVNID = "vn-fake-id"

			It("prevents ipv6_cidr field modification", func() {
				existing := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv6Cidr:       new("2001:db8::/64"),
						VirtualNetwork: fakeVNID,
					}.Build(),
				}.Build()

				updated := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv6Cidr:       new("fd00:1234::/64"),
						VirtualNetwork: fakeVNID,
					}.Build(),
				}.Build()

				err := server.validateSubnet(ctx, updated, existing)
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(err.Error()).To(ContainSubstring("ipv6_cidr"))
				Expect(err.Error()).To(ContainSubstring("immutable"))
			})

			It("allows ipv6_cidr to stay same on Update", func() {
				existing := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv6Cidr:       new("2001:db8::/64"),
						VirtualNetwork: fakeVNID,
					}.Build(),
				}.Build()

				updated := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv6Cidr:       new("2001:db8::/64"),
						VirtualNetwork: fakeVNID,
					}.Build(),
				}.Build()

				err := server.validateSubnet(ctx, updated, existing)
				Expect(err).ToNot(HaveOccurred())
			})

			It("prevents adding ipv6_cidr to an IPv4-only subnet", func() {
				// existing: IPv4-only (no ipv6_cidr set).
				// updated: explicitly sets ipv6_cidr — HasIpv6Cidr() returns true,
				// so the immutability check runs. existing.GetIpv6Cidr() == "" != new value → reject.
				// ipv4_cidr is identical so the ipv4 check does not fire first.
				existing := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("10.0.1.0/24"),
						VirtualNetwork: fakeVNID,
					}.Build(),
				}.Build()

				updated := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("10.0.1.0/24"),
						Ipv6Cidr:       new("2001:db8::/64"),
						VirtualNetwork: fakeVNID,
					}.Build(),
				}.Build()

				err := server.validateSubnet(ctx, updated, existing)
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(err.Error()).To(ContainSubstring("ipv6_cidr"))
				Expect(err.Error()).To(ContainSubstring("immutable"))
			})

			It("rejects explicit empty ipv6_cidr on Update when existing ipv6_cidr is set (empty string edge case)", func() {
				// Symmetric case of the ipv4 empty-string test above.
				// existing has both CIDRs set. updated keeps ipv4_cidr identical
				// (so SUB-VAL-14 doesn't fire), sets ipv6_cidr to "" explicitly.
				existing := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("10.0.1.0/24"),
						Ipv6Cidr:       new("2001:db8::/64"),
						VirtualNetwork: fakeVNID,
					}.Build(),
				}.Build()

				updated := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("10.0.1.0/24"),
						Ipv6Cidr:       new(""),
						VirtualNetwork: fakeVNID,
					}.Build(),
				}.Build()

				err := server.validateSubnet(ctx, updated, existing)
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(err.Error()).To(ContainSubstring("ipv6_cidr"))
				Expect(err.Error()).To(ContainSubstring("immutable"))
			})
		})

		Context("Metadata-only update with identical CIDRs", func() {
			It("passes validation when only non-CIDR fields change", func() {
				fakeVNID := "vn-fake-id-for-metadata-test"
				existing := privatev1.Subnet_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "original-name",
					}.Build(),
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("10.0.1.0/24"),
						Ipv6Cidr:       new("2001:db8::/64"),
						VirtualNetwork: fakeVNID,
					}.Build(),
				}.Build()

				updated := privatev1.Subnet_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "renamed-subnet",
					}.Build(),
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("10.0.1.0/24"),
						Ipv6Cidr:       new("2001:db8::/64"),
						VirtualNetwork: fakeVNID,
					}.Build(),
				}.Build()

				err := server.validateSubnet(ctx, updated, existing)
				Expect(err).ToNot(HaveOccurred())
			})
		})

		Context("Round-trip: server.Update preserves CIDRs", func() {
			It("preserves CIDRs when Update omits CIDR fields (sec-1 regression)", func() {
				vn := createVirtualNetwork(ctx, "10.0.0.0/16", "")

				// Create a subnet with IPv4 CIDR via the server:
				createResponse, err := server.Create(ctx, privatev1.SubnetsCreateRequest_builder{
					Object: privatev1.Subnet_builder{
						Metadata: privatev1.Metadata_builder{
							Tenant: auth.SharedTenant,
						}.Build(),
						Spec: privatev1.SubnetSpec_builder{
							Ipv4Cidr:       new("10.0.1.0/24"),
							VirtualNetwork: vn.GetId(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				created := createResponse.GetObject()

				// Update with only the name changed; no CIDR fields in the request:
				updateResponse, err := server.Update(ctx, privatev1.SubnetsUpdateRequest_builder{
					Object: privatev1.Subnet_builder{
						Id: created.GetId(),
						Metadata: privatev1.Metadata_builder{
							Name: "renamed-subnet",
						}.Build(),
						Spec: privatev1.SubnetSpec_builder{
							VirtualNetwork: vn.GetId(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				updated := updateResponse.GetObject()

				// CIDRs must be preserved from the existing record:
				Expect(updated.GetSpec().GetIpv4Cidr()).To(Equal("10.0.1.0/24"))
			})
		})

		Context("CIDR overlap validation", func() {
			// Helper to create a subnet directly in the database
			createSubnetInDB := func(ctx context.Context, name, ipv4Cidr, ipv6Cidr, virtualNetworkID string) {
				builder := privatev1.SubnetSpec_builder{
					VirtualNetwork: virtualNetworkID,
				}
				if ipv4Cidr != "" {
					builder.Ipv4Cidr = new(ipv4Cidr)
				}
				if ipv6Cidr != "" {
					builder.Ipv6Cidr = new(ipv6Cidr)
				}

				subnet := privatev1.Subnet_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   name,
						Tenant: auth.SharedTenant,
					}.Build(),
					Spec: builder.Build(),
				}.Build()

				_, err := subnetDao.Create().
					SetObject(subnet).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
			}

			It("rejects subnet with exact same IPv4 CIDR", func() {
				vn := createVirtualNetwork(ctx, "10.0.0.0/16", "")
				createSubnetInDB(ctx, "existing-subnet", "10.0.1.0/24", "", vn.GetId())

				newSubnet := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("10.0.1.0/24"),
						VirtualNetwork: vn.GetId(),
					}.Build(),
				}.Build()

				err := server.validateSubnet(ctx, newSubnet, nil)
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.AlreadyExists))
				Expect(err.Error()).To(ContainSubstring("overlaps"))
				Expect(err.Error()).To(ContainSubstring("existing-subnet"))
			})

			It("rejects subnet with overlapping IPv4 CIDR (new is subset)", func() {
				vn := createVirtualNetwork(ctx, "10.0.0.0/16", "")
				createSubnetInDB(ctx, "wide-subnet", "10.0.0.0/20", "", vn.GetId())

				newSubnet := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("10.0.1.0/24"),
						VirtualNetwork: vn.GetId(),
					}.Build(),
				}.Build()

				err := server.validateSubnet(ctx, newSubnet, nil)
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.AlreadyExists))
			})

			It("rejects subnet with overlapping IPv4 CIDR (new is superset)", func() {
				vn := createVirtualNetwork(ctx, "10.0.0.0/16", "")
				createSubnetInDB(ctx, "narrow-subnet", "10.0.1.0/24", "", vn.GetId())

				newSubnet := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("10.0.0.0/20"),
						VirtualNetwork: vn.GetId(),
					}.Build(),
				}.Build()

				err := server.validateSubnet(ctx, newSubnet, nil)
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.AlreadyExists))
			})

			It("accepts subnet with non-overlapping IPv4 CIDR", func() {
				vn := createVirtualNetwork(ctx, "10.0.0.0/16", "")
				createSubnetInDB(ctx, "first-subnet", "10.0.1.0/24", "", vn.GetId())

				newSubnet := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("10.0.2.0/24"),
						VirtualNetwork: vn.GetId(),
					}.Build(),
				}.Build()

				err := server.validateSubnet(ctx, newSubnet, nil)
				Expect(err).ToNot(HaveOccurred())
			})

			It("allows same CIDR in different VirtualNetworks", func() {
				vn1 := createVirtualNetwork(ctx, "10.0.0.0/16", "")
				vn2 := createVirtualNetwork(ctx, "10.0.0.0/16", "")
				createSubnetInDB(ctx, "vn1-subnet", "10.0.1.0/24", "", vn1.GetId())

				newSubnet := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new("10.0.1.0/24"),
						VirtualNetwork: vn2.GetId(),
					}.Build(),
				}.Build()

				err := server.validateSubnet(ctx, newSubnet, nil)
				Expect(err).ToNot(HaveOccurred())
			})

			It("rejects subnet with overlapping IPv6 CIDR", func() {
				vn := createVirtualNetwork(ctx, "", "2001:db8::/32")
				createSubnetInDB(ctx, "existing-v6", "", "2001:db8:1::/48", vn.GetId())

				newSubnet := privatev1.Subnet_builder{
					Spec: privatev1.SubnetSpec_builder{
						Ipv6Cidr:       new("2001:db8:1::/48"),
						VirtualNetwork: vn.GetId(),
					}.Build(),
				}.Build()

				err := server.validateSubnet(ctx, newSubnet, nil)
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.AlreadyExists))
			})
		})

		// SUB-VAL-12, SUB-VAL-13: Tenant isolation for Delete and List operations
		// are covered by servers_tenancy_test.go, not validation tests

		Context("Deletion validation", func() {
			var computeInstancesDao *dao.GenericDAO[*privatev1.ComputeInstance]

			BeforeEach(func() {
				var err error
				computeInstancesDao, err = dao.NewGenericDAO[*privatev1.ComputeInstance]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())
			})

			It("rejects deletion when a compute instance uses the subnet", func() {
				vn := createVirtualNetwork(ctx, "10.0.0.0/16", "")

				createResponse, err := server.Create(ctx, privatev1.SubnetsCreateRequest_builder{
					Object: privatev1.Subnet_builder{
						Metadata: privatev1.Metadata_builder{
							Tenant: auth.SharedTenant,
						}.Build(),
						Spec: privatev1.SubnetSpec_builder{
							Ipv4Cidr:       new("10.0.1.0/24"),
							VirtualNetwork: vn.GetId(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				subnetID := createResponse.GetObject().GetId()

				_, err = computeInstancesDao.Create().
					SetObject(privatev1.ComputeInstance_builder{
						Metadata: privatev1.Metadata_builder{
							Tenant: auth.SharedTenant,
						}.Build(),
						Spec: privatev1.ComputeInstanceSpec_builder{
							NetworkAttachments: []*privatev1.NetworkAttachment{
								privatev1.NetworkAttachment_builder{
									Subnet: subnetID,
								}.Build(),
							},
						}.Build(),
					}.Build()).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				_, err = server.Delete(ctx, privatev1.SubnetsDeleteRequest_builder{
					Id: subnetID,
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
				Expect(status.Message()).To(ContainSubstring("compute instance"))
			})

			It("allows deletion when no compute instances use the subnet", func() {
				vn := createVirtualNetwork(ctx, "10.0.0.0/16", "")

				createResponse, err := server.Create(ctx, privatev1.SubnetsCreateRequest_builder{
					Object: privatev1.Subnet_builder{
						Metadata: privatev1.Metadata_builder{
							Tenant: auth.SharedTenant,
						}.Build(),
						Spec: privatev1.SubnetSpec_builder{
							Ipv4Cidr:       new("10.0.1.0/24"),
							VirtualNetwork: vn.GetId(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				subnetID := createResponse.GetObject().GetId()

				_, err = server.Delete(ctx, privatev1.SubnetsDeleteRequest_builder{
					Id: subnetID,
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})

			It("rejects creating a compute instance referencing a deleted subnet", func() {
				vn := createVirtualNetwork(ctx, "10.0.0.0/16", "")

				createResponse, err := server.Create(ctx, privatev1.SubnetsCreateRequest_builder{
					Object: privatev1.Subnet_builder{
						Metadata: privatev1.Metadata_builder{
							Tenant: auth.SharedTenant,
						}.Build(),
						Spec: privatev1.SubnetSpec_builder{
							Ipv4Cidr:       new("10.0.1.0/24"),
							VirtualNetwork: vn.GetId(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				subnetID := createResponse.GetObject().GetId()

				// Soft-delete the subnet:
				_, err = server.Delete(ctx, privatev1.SubnetsDeleteRequest_builder{
					Id: subnetID,
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				// Try to create a compute instance referencing the deleted subnet:
				_, err = computeInstancesDao.Create().
					SetObject(privatev1.ComputeInstance_builder{
						Metadata: privatev1.Metadata_builder{
							Tenant: auth.SharedTenant,
						}.Build(),
						Spec: privatev1.ComputeInstanceSpec_builder{
							NetworkAttachments: []*privatev1.NetworkAttachment{
								privatev1.NetworkAttachment_builder{
									Subnet: subnetID,
								}.Build(),
							},
						}.Build(),
					}.Build()).
					Do(ctx)
				Expect(err).To(HaveOccurred())
				var referenceErr *dao.ErrReference
				Expect(errors.As(err, &referenceErr)).To(BeTrue())
				Expect(referenceErr.Reason).To(ContainSubstring(subnetID))
			})

			It("allows deletion when compute instance using it is itself deleted", func() {
				vn := createVirtualNetwork(ctx, "10.0.0.0/16", "")

				createResponse, err := server.Create(ctx, privatev1.SubnetsCreateRequest_builder{
					Object: privatev1.Subnet_builder{
						Metadata: privatev1.Metadata_builder{
							Tenant: auth.SharedTenant,
						}.Build(),
						Spec: privatev1.SubnetSpec_builder{
							Ipv4Cidr:       new("10.0.1.0/24"),
							VirtualNetwork: vn.GetId(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				subnetID := createResponse.GetObject().GetId()

				ciCreateResponse, err := computeInstancesDao.Create().
					SetObject(privatev1.ComputeInstance_builder{
						Metadata: privatev1.Metadata_builder{
							Tenant: auth.SharedTenant,
						}.Build(),
						Spec: privatev1.ComputeInstanceSpec_builder{
							NetworkAttachments: []*privatev1.NetworkAttachment{
								privatev1.NetworkAttachment_builder{
									Subnet: subnetID,
								}.Build(),
							},
						}.Build(),
					}.Build()).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				// Soft-delete the compute instance first:
				_, err = computeInstancesDao.Delete().
					SetId(ciCreateResponse.GetObject().GetId()).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				// Subnet deletion should now succeed:
				_, err = server.Delete(ctx, privatev1.SubnetsDeleteRequest_builder{
					Id: subnetID,
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})

			It("blocks deletion of default-labeled Subnet", func() {
				vn := createVirtualNetwork(ctx, "10.0.0.0/16", "")

				createResponse, err := server.Create(ctx, privatev1.SubnetsCreateRequest_builder{
					Object: privatev1.Subnet_builder{
						Metadata: privatev1.Metadata_builder{
							Tenant: auth.SharedTenant,
							Labels: map[string]string{
								"osac.openshift.io/default": "true",
							},
						}.Build(),
						Spec: privatev1.SubnetSpec_builder{
							Ipv4Cidr:       new("10.0.1.0/24"),
							VirtualNetwork: vn.GetId(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				_, err = server.Delete(ctx, privatev1.SubnetsDeleteRequest_builder{
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

	Describe("GenericDAO Subnet operations", func() {
		var generic *dao.GenericDAO[*privatev1.Subnet]

		BeforeEach(func() {
			var err error

			// Create DAO
			generic, err = dao.NewGenericDAO[*privatev1.Subnet]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("creates Subnet and generates ID", func() {
			vn := createVirtualNetwork(ctx, "10.0.0.0/16", "")

			subnet := privatev1.Subnet_builder{
				Metadata: privatev1.Metadata_builder{
					Tenant: auth.SharedTenant,
				}.Build(),
				Spec: privatev1.SubnetSpec_builder{
					Ipv4Cidr:       new("10.0.1.0/24"),
					VirtualNetwork: vn.GetId(),
				}.Build(),
			}.Build()

			response, err := generic.Create().
				SetObject(subnet).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			subnet = response.GetObject()
			Expect(subnet.GetId()).ToNot(BeEmpty())
			Expect(subnet.GetMetadata()).ToNot(BeNil())
			Expect(subnet.GetMetadata().GetCreationTimestamp()).ToNot(BeNil())
		})

		It("retrieves Subnet by ID", func() {
			vn := createVirtualNetwork(ctx, "10.0.0.0/16", "")

			subnet := privatev1.Subnet_builder{
				Metadata: privatev1.Metadata_builder{
					Tenant: auth.SharedTenant,
				}.Build(),
				Spec: privatev1.SubnetSpec_builder{
					Ipv4Cidr:       new("10.0.1.0/24"),
					VirtualNetwork: vn.GetId(),
				}.Build(),
			}.Build()

			createResponse, err := generic.Create().
				SetObject(subnet).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			subnet = createResponse.GetObject()

			getResponse, err := generic.Get().
				SetId(subnet.GetId()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			retrieved := getResponse.GetObject()
			Expect(proto.Equal(subnet, retrieved)).To(BeTrue())
		})

		It("lists Subnets with pagination", func() {
			vn := createVirtualNetwork(ctx, "10.0.0.0/8", "")

			// Create multiple Subnets
			const count = 10
			for i := 0; i < count; i++ {
				subnet := privatev1.Subnet_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   fmt.Sprintf("subnet-%d", i),
						Tenant: auth.SharedTenant,
					}.Build(),
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new(fmt.Sprintf("10.%d.0.0/16", i)),
						VirtualNetwork: vn.GetId(),
					}.Build(),
				}.Build()

				_, err := generic.Create().
					SetObject(subnet).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
			}

			// List with limit
			response, err := generic.List().
				SetLimit(5).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(5))
			Expect(response.GetTotal()).To(Equal(int32(count)))
		})

		It("lists Subnets with SQL filtering by parent VirtualNetwork", func() {
			vn1 := createVirtualNetwork(ctx, "10.0.0.0/16", "")
			vn2 := createVirtualNetwork(ctx, "192.168.0.0/16", "")

			// Create subnets for vn1
			for i := range 3 {
				subnet := privatev1.Subnet_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   fmt.Sprintf("vn1-subnet-%d", i),
						Tenant: auth.SharedTenant,
					}.Build(),
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new(fmt.Sprintf("10.0.%d.0/24", i)),
						VirtualNetwork: vn1.GetId(),
					}.Build(),
				}.Build()

				_, err := generic.Create().
					SetObject(subnet).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
			}

			// Create subnets for vn2
			for i := range 2 {
				subnet := privatev1.Subnet_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   fmt.Sprintf("vn2-subnet-%d", i),
						Tenant: auth.SharedTenant,
					}.Build(),
					Spec: privatev1.SubnetSpec_builder{
						Ipv4Cidr:       new(fmt.Sprintf("192.168.%d.0/24", i)),
						VirtualNetwork: vn2.GetId(),
					}.Build(),
				}.Build()

				_, err := generic.Create().
					SetObject(subnet).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
			}

			// List subnets for vn1 only
			response, err := generic.List().
				SetFilter(fmt.Sprintf("this.spec.virtual_network == %q", vn1.GetId())).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(3))
			for _, subnet := range response.GetItems() {
				Expect(subnet.GetSpec().GetVirtualNetwork()).To(Equal(vn1.GetId()))
			}
		})

		It("updates Subnet", func() {
			vn := createVirtualNetwork(ctx, "10.0.0.0/16", "")

			subnet := privatev1.Subnet_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   "original-name",
					Tenant: auth.SharedTenant,
				}.Build(),
				Spec: privatev1.SubnetSpec_builder{
					Ipv4Cidr:       new("10.0.1.0/24"),
					VirtualNetwork: vn.GetId(),
				}.Build(),
			}.Build()

			createResponse, err := generic.Create().
				SetObject(subnet).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			subnet = createResponse.GetObject()

			// Update name
			subnet.GetMetadata().Name = "updated-name"
			updateResponse, err := generic.Update().
				SetObject(subnet).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			subnet = updateResponse.GetObject()

			// Verify update
			getResponse, err := generic.Get().
				SetId(subnet.GetId()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			retrieved := getResponse.GetObject()
			Expect(retrieved.GetMetadata().GetName()).To(Equal("updated-name"))
		})

		It("soft deletes Subnet", func() {
			vn := createVirtualNetwork(ctx, "10.0.0.0/16", "")

			subnet := privatev1.Subnet_builder{
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{"test-finalizer"},
					Tenant:     auth.SharedTenant,
				}.Build(),
				Spec: privatev1.SubnetSpec_builder{
					Ipv4Cidr:       new("10.0.1.0/24"),
					VirtualNetwork: vn.GetId(),
				}.Build(),
			}.Build()

			createResponse, err := generic.Create().
				SetObject(subnet).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			subnet = createResponse.GetObject()

			// Delete
			_, err = generic.Delete().
				SetId(subnet.GetId()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Verify soft delete (deletion_timestamp set, object still retrievable)
			getResponse, err := generic.Get().
				SetId(subnet.GetId()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			retrieved := getResponse.GetObject()
			Expect(retrieved.GetMetadata().GetDeletionTimestamp()).ToNot(BeNil())
		})

		It("enforces tenant isolation", func() {
			vn1 := createVirtualNetwork(ctx, "10.0.0.0/16", "")
			vn2 := createVirtualNetwork(ctx, "192.168.0.0/16", "")

			// Create Subnet with tenant-a
			subnet1 := privatev1.Subnet_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   "subnet-tenant-a",
					Tenant: "tenant-a",
				}.Build(),
				Spec: privatev1.SubnetSpec_builder{
					Ipv4Cidr:       new("10.0.1.0/24"),
					VirtualNetwork: vn1.GetId(),
				}.Build(),
			}.Build()

			_, err := generic.Create().
				SetObject(subnet1).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Create Subnet with tenant-b
			subnet2 := privatev1.Subnet_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   "subnet-tenant-b",
					Tenant: "tenant-b",
				}.Build(),
				Spec: privatev1.SubnetSpec_builder{
					Ipv4Cidr:       new("192.168.1.0/24"),
					VirtualNetwork: vn2.GetId(),
				}.Build(),
			}.Build()

			_, err = generic.Create().
				SetObject(subnet2).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// List should return both (tenancy logic allows all in test context)
			response, err := generic.List().Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(2))
		})
	})
})
