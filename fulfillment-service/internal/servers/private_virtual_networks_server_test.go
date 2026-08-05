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
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/database"
	"github.com/osac-project/fulfillment-service/internal/database/dao"
)

var _ = Describe("Private virtual networks server", func() {
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
	})

	// Helper function to create a NetworkClass for validation tests
	createNetworkClass := func(ctx context.Context, state privatev1.NetworkClassState) *privatev1.NetworkClass {
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
				State: state,
			}.Build(),
		}.Build()

		response, err := ncDao.Create().
			SetObject(nc).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		return response.GetObject()
	}

	// createDefaultNetworkClassViaDAO creates a NetworkClass with is_default=true via the DAO.
	createDefaultNetworkClassViaDAO := func(ctx context.Context, state privatev1.NetworkClassState) *privatev1.NetworkClass {
		ncDao, err := dao.NewGenericDAO[*privatev1.NetworkClass]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		nc := privatev1.NetworkClass_builder{
			ImplementationStrategy: "test-strategy",
			IsDefault:              new(true),
			Metadata: privatev1.Metadata_builder{
				Tenant: auth.SharedTenant,
			}.Build(),
			Capabilities: privatev1.NetworkClassCapabilities_builder{
				SupportsIpv4:      true,
				SupportsIpv6:      true,
				SupportsDualStack: true,
			}.Build(),
			Status: privatev1.NetworkClassStatus_builder{
				State: state,
			}.Build(),
		}.Build()

		response, err := ncDao.Create().
			SetObject(nc).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		return response.GetObject()
	}

	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			server, err := NewPrivateVirtualNetworksServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewPrivateVirtualNetworksServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewPrivateVirtualNetworksServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Validation tests", func() {
		var server *PrivateVirtualNetworksServer

		BeforeEach(func() {
			var err error

			// Create the server:
			server, err = NewPrivateVirtualNetworksServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		Context("VN-VAL-01: IPv4 CIDR validation", func() {
			It("accepts valid IPv4 CIDR", func() {
				nc := createNetworkClass(ctx, privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY)

				vn := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Ipv4Cidr:     new("192.168.0.0/16"),
						NetworkClass: nc.GetId(),
						Region:       "us-west-1",
					}.Build(),
				}.Build()

				_, err := server.validateVirtualNetwork(ctx, vn, nil)
				Expect(err).ToNot(HaveOccurred())
			})

			It("rejects invalid IPv4 CIDR format", func() {
				vn := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Ipv4Cidr: new("not-a-cidr"),
						Region:   "us-west-1",
					}.Build(),
				}.Build()

				_, err := server.validateVirtualNetwork(ctx, vn, nil)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("invalid IPv4 CIDR"))
			})

			It("rejects IPv4 with invalid mask", func() {
				vn := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Ipv4Cidr: new("192.168.0.0/33"),
						Region:   "us-west-1",
					}.Build(),
				}.Build()

				_, err := server.validateVirtualNetwork(ctx, vn, nil)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("invalid IPv4 CIDR"))
			})

			It("rejects IPv6 address in IPv4 field", func() {
				vn := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Ipv4Cidr: new("2001:db8::/32"),
						Region:   "us-west-1",
					}.Build(),
				}.Build()

				_, err := server.validateVirtualNetwork(ctx, vn, nil)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("contains IPv6 address"))
			})
		})

		Context("VN-VAL-02: IPv6 CIDR validation", func() {
			It("accepts valid IPv6 CIDR", func() {
				nc := createNetworkClass(ctx, privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY)

				vn := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Ipv6Cidr:     new("2001:db8::/32"),
						NetworkClass: nc.GetId(),
						Region:       "us-west-1",
					}.Build(),
				}.Build()

				_, err := server.validateVirtualNetwork(ctx, vn, nil)
				Expect(err).ToNot(HaveOccurred())
			})

			It("rejects invalid IPv6 CIDR format", func() {
				vn := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Ipv6Cidr: new("not-ipv6"),
						Region:   "us-west-1",
					}.Build(),
				}.Build()

				_, err := server.validateVirtualNetwork(ctx, vn, nil)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("invalid IPv6 CIDR"))
			})

			It("rejects IPv6 with invalid mask", func() {
				vn := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Ipv6Cidr: new("2001:db8::/129"),
						Region:   "us-west-1",
					}.Build(),
				}.Build()

				_, err := server.validateVirtualNetwork(ctx, vn, nil)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("invalid IPv6 CIDR"))
			})

			It("rejects IPv4 address in IPv6 field", func() {
				vn := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Ipv6Cidr: new("192.168.0.0/16"),
						Region:   "us-west-1",
					}.Build(),
				}.Build()

				_, err := server.validateVirtualNetwork(ctx, vn, nil)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("contains IPv4 address"))
			})
		})

		Context("CIDR canonicalization", func() {
			It("canonicalizes non-canonical IPv4 and IPv6 CIDRs on Create", func() {
				nc := createNetworkClass(ctx, privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY)

				ipv4VN := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Ipv4Cidr:     new("10.0.1.5/24"),
						NetworkClass: nc.GetId(),
						Region:       "us-west-1",
					}.Build(),
				}.Build()
				_, err := server.validateVirtualNetwork(ctx, ipv4VN, nil)
				Expect(err).ToNot(HaveOccurred())
				Expect(ipv4VN.GetSpec().GetIpv4Cidr()).To(Equal("10.0.1.0/24"))

				ipv6VN := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Ipv6Cidr:     new("2001:db8::1/32"),
						NetworkClass: nc.GetId(),
						Region:       "us-west-1",
					}.Build(),
				}.Build()
				_, err = server.validateVirtualNetwork(ctx, ipv6VN, nil)
				Expect(err).ToNot(HaveOccurred())
				Expect(ipv6VN.GetSpec().GetIpv6Cidr()).To(Equal("2001:db8::/32"))
			})

			It("stores canonical IPv4 CIDR on Create round-trip", func() {
				nc := createNetworkClass(ctx, privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY)

				createResponse, err := server.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
					Object: privatev1.VirtualNetwork_builder{
						Metadata: privatev1.Metadata_builder{
							Tenant: auth.SharedTenant,
						}.Build(),
						Spec: privatev1.VirtualNetworkSpec_builder{
							Ipv4Cidr:     new("10.0.1.5/24"),
							NetworkClass: nc.GetId(),
							Region:       "us-west-1",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(createResponse.GetObject().GetSpec().GetIpv4Cidr()).To(Equal("10.0.1.0/24"))
			})

			It("canonicalizes on Update including preserved legacy CIDR when omitted", func() {
				nc := createNetworkClass(ctx, privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY)

				existing := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Region:       "us-west-1",
						NetworkClass: nc.GetId(),
						Ipv4Cidr:     new("10.0.1.5/24"),
					}.Build(),
				}.Build()

				equivalentRewrite := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Region:       "us-west-1",
						NetworkClass: nc.GetId(),
						Ipv4Cidr:     new("10.0.1.0/24"),
					}.Build(),
				}.Build()
				_, err := server.validateVirtualNetwork(ctx, equivalentRewrite, existing)
				Expect(err).ToNot(HaveOccurred())
				Expect(equivalentRewrite.GetSpec().GetIpv4Cidr()).To(Equal("10.0.1.0/24"))

				omittedField := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Region:       "us-west-1",
						NetworkClass: nc.GetId(),
					}.Build(),
				}.Build()
				_, err = server.validateVirtualNetwork(ctx, omittedField, existing)
				Expect(err).ToNot(HaveOccurred())
				Expect(omittedField.GetSpec().GetIpv4Cidr()).To(Equal("10.0.1.0/24"))
			})
		})

		Context("VN-VAL-03: At least one CIDR required", func() {
			It("rejects empty IPv4 and IPv6 CIDRs", func() {
				vn := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Region: "us-west-1",
					}.Build(),
				}.Build()

				_, err := server.validateVirtualNetwork(ctx, vn, nil)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("at least one"))
			})

			It("accepts IPv4-only configuration", func() {
				nc := createNetworkClass(ctx, privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY)

				vn := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Ipv4Cidr:     new("10.0.0.0/16"),
						NetworkClass: nc.GetId(),
						Region:       "us-west-1",
					}.Build(),
				}.Build()

				_, err := server.validateVirtualNetwork(ctx, vn, nil)
				Expect(err).ToNot(HaveOccurred())
			})

			It("accepts IPv6-only configuration", func() {
				nc := createNetworkClass(ctx, privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY)

				vn := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Ipv6Cidr:     new("2001:db8::/32"),
						NetworkClass: nc.GetId(),
						Region:       "us-west-1",
					}.Build(),
				}.Build()

				_, err := server.validateVirtualNetwork(ctx, vn, nil)
				Expect(err).ToNot(HaveOccurred())
			})

			It("accepts dual-stack configuration", func() {
				nc := createNetworkClass(ctx, privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY)

				vn := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Ipv4Cidr:     new("10.0.0.0/16"),
						Ipv6Cidr:     new("2001:db8::/32"),
						NetworkClass: nc.GetId(),
						Region:       "us-west-1",
					}.Build(),
				}.Build()

				_, err := server.validateVirtualNetwork(ctx, vn, nil)
				Expect(err).ToNot(HaveOccurred())
			})
		})

		Context("VN-VAL-04: NetworkClass reference validation", func() {
			It("accepts valid NetworkClass reference", func() {
				nc := createNetworkClass(ctx, privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY)

				vn := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Ipv4Cidr:     new("10.0.0.0/16"),
						NetworkClass: nc.GetId(),
						Region:       "us-west-1",
					}.Build(),
				}.Build()

				_, err := server.validateVirtualNetwork(ctx, vn, nil)
				Expect(err).ToNot(HaveOccurred())
			})

			It("rejects non-existent NetworkClass", func() {
				vn := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Ipv4Cidr:     new("10.0.0.0/16"),
						NetworkClass: "non-existent-class",
						Region:       "us-west-1",
					}.Build(),
				}.Build()

				_, err := server.validateVirtualNetwork(ctx, vn, nil)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("does not exist"))
			})

			It("rejects empty NetworkClass when no default exists", func() {
				vn := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Ipv4Cidr: new("10.0.0.0/16"),
						Region:   "us-west-1",
					}.Build(),
				}.Build()

				_, err := server.validateVirtualNetwork(ctx, vn, nil)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("no default NetworkClass is configured"))
			})
		})

		Context("VN-VAL-05: NetworkClass READY state validation", func() {
			It("accepts NetworkClass in READY state", func() {
				nc := createNetworkClass(ctx, privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY)

				vn := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Ipv4Cidr:     new("10.0.0.0/16"),
						NetworkClass: nc.GetId(),
						Region:       "us-west-1",
					}.Build(),
				}.Build()

				_, err := server.validateVirtualNetwork(ctx, vn, nil)
				Expect(err).ToNot(HaveOccurred())
			})

			It("rejects NetworkClass in PENDING state", func() {
				nc := createNetworkClass(ctx, privatev1.NetworkClassState_NETWORK_CLASS_STATE_PENDING)

				vn := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Ipv4Cidr:     new("10.0.0.0/16"),
						NetworkClass: nc.GetId(),
						Region:       "us-west-1",
					}.Build(),
				}.Build()

				_, err := server.validateVirtualNetwork(ctx, vn, nil)
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
				Expect(err.Error()).To(ContainSubstring("not in READY state"))
			})

			It("rejects NetworkClass in FAILED state", func() {
				nc := createNetworkClass(ctx, privatev1.NetworkClassState_NETWORK_CLASS_STATE_FAILED)

				vn := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Ipv4Cidr:     new("10.0.0.0/16"),
						NetworkClass: nc.GetId(),
						Region:       "us-west-1",
					}.Build(),
				}.Build()

				_, err := server.validateVirtualNetwork(ctx, vn, nil)
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
				Expect(err.Error()).To(ContainSubstring("not in READY state"))
			})
		})

		Context("VN-VAL-06: Capabilities matching", func() {
			It("accepts matching IPv4 capability", func() {
				nc := createNetworkClass(ctx, privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY)

				vn := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Ipv4Cidr:     new("10.0.0.0/16"),
						NetworkClass: nc.GetId(),
						Region:       "us-west-1",
						Capabilities: privatev1.VirtualNetworkCapabilities_builder{
							EnableIpv4: true,
						}.Build(),
					}.Build(),
				}.Build()

				_, err := server.validateVirtualNetwork(ctx, vn, nil)
				Expect(err).ToNot(HaveOccurred())
			})

			It("rejects IPv4 when NetworkClass doesn't support it", func() {
				// Create NetworkClass without IPv4 support
				ncDao, err := dao.NewGenericDAO[*privatev1.NetworkClass]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())

				nc := privatev1.NetworkClass_builder{
					ImplementationStrategy: "no-ipv4-class",
					Metadata: privatev1.Metadata_builder{
						Tenant: auth.SharedTenant,
					}.Build(),
					Capabilities: privatev1.NetworkClassCapabilities_builder{
						SupportsIpv4: false,
						SupportsIpv6: true,
					}.Build(),
					Status: privatev1.NetworkClassStatus_builder{
						State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
					}.Build(),
				}.Build()
				response, err := ncDao.Create().
					SetObject(nc).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				nc = response.GetObject()

				vn := privatev1.VirtualNetwork_builder{
					Metadata: privatev1.Metadata_builder{
						Tenant: auth.SharedTenant,
					}.Build(),
					Spec: privatev1.VirtualNetworkSpec_builder{
						Ipv4Cidr:     new("10.0.0.0/16"),
						NetworkClass: nc.GetId(),
						Region:       "us-west-1",
						Capabilities: privatev1.VirtualNetworkCapabilities_builder{
							EnableIpv4: true,
						}.Build(),
					}.Build(),
				}.Build()

				_, err = server.validateVirtualNetwork(ctx, vn, nil)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("does not support IPv4"))
			})
		})

		Context("VN-VAL-08: Region field required", func() {
			It("rejects empty region", func() {
				vn := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Ipv4Cidr: new("10.0.0.0/16"),
					}.Build(),
				}.Build()

				_, err := server.validateVirtualNetwork(ctx, vn, nil)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("region"))
				Expect(err.Error()).To(ContainSubstring("required"))
			})

			It("accepts non-empty region", func() {
				nc := createNetworkClass(ctx, privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY)

				vn := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Ipv4Cidr:     new("10.0.0.0/16"),
						NetworkClass: nc.GetId(),
						Region:       "us-west-1",
					}.Build(),
				}.Build()

				_, err := server.validateVirtualNetwork(ctx, vn, nil)
				Expect(err).ToNot(HaveOccurred())
			})
		})

		Context("VN-VAL-09: Region immutability on Update", func() {
			It("prevents region modification", func() {
				existing := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Region:       "us-west-1",
						Ipv4Cidr:     new("10.0.0.0/16"),
						NetworkClass: "test-class",
					}.Build(),
				}.Build()

				updated := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Region:       "us-east-1",
						Ipv4Cidr:     new("10.0.0.0/16"),
						NetworkClass: "test-class",
					}.Build(),
				}.Build()

				_, err := server.validateVirtualNetwork(ctx, updated, existing)
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(err.Error()).To(ContainSubstring("region"))
				Expect(err.Error()).To(ContainSubstring("immutable"))
			})

			It("allows region to stay same", func() {
				nc := createNetworkClass(ctx, privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY)

				existing := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Region:       "us-west-1",
						Ipv4Cidr:     new("10.0.0.0/16"),
						NetworkClass: nc.GetId(),
					}.Build(),
				}.Build()

				updated := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Region:       "us-west-1",
						Ipv4Cidr:     new("10.0.0.0/16"),
						NetworkClass: nc.GetId(),
					}.Build(),
				}.Build()

				_, err := server.validateVirtualNetwork(ctx, updated, existing)
				Expect(err).ToNot(HaveOccurred())
			})
		})

		Context("VN-VAL-10: NetworkClass immutability on Update", func() {
			It("prevents network_class modification", func() {
				existing := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Region:       "us-west-1",
						Ipv4Cidr:     new("10.0.0.0/16"),
						NetworkClass: "class-1",
					}.Build(),
				}.Build()

				updated := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Region:       "us-west-1",
						Ipv4Cidr:     new("10.0.0.0/16"),
						NetworkClass: "class-2",
					}.Build(),
				}.Build()

				_, err := server.validateVirtualNetwork(ctx, updated, existing)
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(err.Error()).To(ContainSubstring("network_class"))
				Expect(err.Error()).To(ContainSubstring("immutable"))
			})

			It("allows network_class to stay same", func() {
				nc := createNetworkClass(ctx, privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY)

				existing := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Region:       "us-west-1",
						Ipv4Cidr:     new("10.0.0.0/16"),
						NetworkClass: nc.GetId(),
					}.Build(),
				}.Build()

				updated := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Region:       "us-west-1",
						Ipv4Cidr:     new("10.0.0.0/16"),
						NetworkClass: nc.GetId(),
					}.Build(),
				}.Build()

				_, err := server.validateVirtualNetwork(ctx, updated, existing)
				Expect(err).ToNot(HaveOccurred())
			})
		})

		Context("implementation_strategy immutability on Update", func() {
			It("rejects changing implementation_strategy", func() {
				existing := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Region:                 "us-west-1",
						NetworkClass:           "test-class",
						Ipv4Cidr:               new("10.0.0.0/16"),
						ImplementationStrategy: "strategy-a",
					}.Build(),
				}.Build()

				updated := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Region:                 "us-west-1",
						NetworkClass:           "test-class",
						Ipv4Cidr:               new("10.0.0.0/16"),
						ImplementationStrategy: "strategy-b",
					}.Build(),
				}.Build()

				_, err := server.validateVirtualNetwork(ctx, updated, existing)
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(err.Error()).To(ContainSubstring("implementation_strategy"))
				Expect(err.Error()).To(ContainSubstring("immutable"))
			})

			It("allows same implementation_strategy on Update", func() {
				nc := createNetworkClass(ctx, privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY)

				existing := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Region:                 "us-west-1",
						NetworkClass:           nc.GetId(),
						Ipv4Cidr:               new("10.0.0.0/16"),
						ImplementationStrategy: "test-strategy",
					}.Build(),
				}.Build()

				updated := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Region:                 "us-west-1",
						NetworkClass:           nc.GetId(),
						Ipv4Cidr:               new("10.0.0.0/16"),
						ImplementationStrategy: "test-strategy",
					}.Build(),
				}.Build()

				_, err := server.validateVirtualNetwork(ctx, updated, existing)
				Expect(err).ToNot(HaveOccurred())
			})

			It("preserves implementation_strategy when omitted in Update (round-trip)", func() {
				nc := createNetworkClass(ctx, privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY)

				createResponse, err := server.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
					Object: privatev1.VirtualNetwork_builder{
						Metadata: privatev1.Metadata_builder{
							Tenant: auth.SharedTenant,
						}.Build(),
						Spec: privatev1.VirtualNetworkSpec_builder{
							Ipv4Cidr:     new("10.0.0.0/16"),
							NetworkClass: nc.GetId(),
							Region:       "us-west-1",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				created := createResponse.GetObject()
				Expect(created.GetSpec().GetImplementationStrategy()).To(Equal("test-strategy"))

				updateResponse, err := server.Update(ctx, privatev1.VirtualNetworksUpdateRequest_builder{
					Object: privatev1.VirtualNetwork_builder{
						Id: created.GetId(),
						Metadata: privatev1.Metadata_builder{
							Name: "renamed-vn",
						}.Build(),
						Spec: privatev1.VirtualNetworkSpec_builder{
							NetworkClass: nc.GetId(),
							Region:       "us-west-1",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(updateResponse.GetObject().GetSpec().GetImplementationStrategy()).To(Equal("test-strategy"))
			})
		})

		Context("VN-VAL-11: IPv4 CIDR immutability on Update", func() {
			It("prevents ipv4_cidr field modification", func() {
				// Fake NetworkClass: immutability check for NC fires before the NC reference lookup
				// guard, so a fake NC ID is fine here. Region and NC stay identical so those
				// immutability checks do not fire.
				existing := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Region:       "us-west-1",
						NetworkClass: "test-class",
						Ipv4Cidr:     new("10.0.0.0/16"),
					}.Build(),
				}.Build()

				updated := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Region:       "us-west-1",
						NetworkClass: "test-class",
						Ipv4Cidr:     new("192.168.0.0/16"),
					}.Build(),
				}.Build()

				_, err := server.validateVirtualNetwork(ctx, updated, existing)
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(err.Error()).To(ContainSubstring("ipv4_cidr"))
				Expect(err.Error()).To(ContainSubstring("immutable"))
			})

			It("allows ipv4_cidr to stay same on Update", func() {
				// NC reference lookup guard: skips DB lookup when NC is unchanged.
				// Using a real NC so the guard's equality check works cleanly.
				nc := createNetworkClass(ctx, privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY)

				existing := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Region:       "us-west-1",
						NetworkClass: nc.GetId(),
						Ipv4Cidr:     new("10.0.0.0/16"),
					}.Build(),
				}.Build()

				updated := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Region:       "us-west-1",
						NetworkClass: nc.GetId(),
						Ipv4Cidr:     new("10.0.0.0/16"),
					}.Build(),
				}.Build()

				_, err := server.validateVirtualNetwork(ctx, updated, existing)
				Expect(err).ToNot(HaveOccurred())
			})

			It("prevents adding ipv4_cidr to an IPv6-only VirtualNetwork", func() {
				// existing: IPv6-only (no ipv4_cidr set).
				// updated: explicitly sets ipv4_cidr — HasIpv4Cidr() returns true,
				// existing.GetIpv4Cidr() == "" != new value → reject.
				existing := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Region:       "us-west-1",
						NetworkClass: "test-class",
						Ipv6Cidr:     new("2001:db8::/32"),
					}.Build(),
				}.Build()

				updated := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Region:       "us-west-1",
						NetworkClass: "test-class",
						Ipv4Cidr:     new("10.0.0.0/16"),
						Ipv6Cidr:     new("2001:db8::/32"),
					}.Build(),
				}.Build()

				_, err := server.validateVirtualNetwork(ctx, updated, existing)
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
				existing := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Region:       "us-west-1",
						NetworkClass: "test-class",
						Ipv4Cidr:     new("10.0.0.0/16"),
					}.Build(),
				}.Build()

				updated := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Region:       "us-west-1",
						NetworkClass: "test-class",
						Ipv4Cidr:     new(""),
						Ipv6Cidr:     new("2001:db8::/32"),
					}.Build(),
				}.Build()

				_, err := server.validateVirtualNetwork(ctx, updated, existing)
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(err.Error()).To(ContainSubstring("ipv4_cidr"))
				Expect(err.Error()).To(ContainSubstring("immutable"))
			})
		})

		Context("VN-VAL-12: IPv6 CIDR immutability on Update", func() {
			It("prevents ipv6_cidr field modification", func() {
				existing := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Region:       "us-west-1",
						NetworkClass: "test-class",
						Ipv6Cidr:     new("2001:db8::/32"),
					}.Build(),
				}.Build()

				updated := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Region:       "us-west-1",
						NetworkClass: "test-class",
						Ipv6Cidr:     new("fd00:1234::/32"),
					}.Build(),
				}.Build()

				_, err := server.validateVirtualNetwork(ctx, updated, existing)
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(err.Error()).To(ContainSubstring("ipv6_cidr"))
				Expect(err.Error()).To(ContainSubstring("immutable"))
			})

			It("allows ipv6_cidr to stay same on Update", func() {
				nc := createNetworkClass(ctx, privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY)

				existing := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Region:       "us-west-1",
						NetworkClass: nc.GetId(),
						Ipv6Cidr:     new("2001:db8::/32"),
					}.Build(),
				}.Build()

				updated := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Region:       "us-west-1",
						NetworkClass: nc.GetId(),
						Ipv6Cidr:     new("2001:db8::/32"),
					}.Build(),
				}.Build()

				_, err := server.validateVirtualNetwork(ctx, updated, existing)
				Expect(err).ToNot(HaveOccurred())
			})

			It("prevents adding ipv6_cidr to an IPv4-only VirtualNetwork", func() {
				// existing: IPv4-only (no ipv6_cidr set).
				// updated: explicitly sets ipv6_cidr — HasIpv6Cidr() returns true,
				// existing.GetIpv6Cidr() == "" != new value → reject.
				// ipv4_cidr is identical so the ipv4 check does not fire first.
				existing := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Region:       "us-west-1",
						NetworkClass: "test-class",
						Ipv4Cidr:     new("10.0.0.0/16"),
					}.Build(),
				}.Build()

				updated := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Region:       "us-west-1",
						NetworkClass: "test-class",
						Ipv4Cidr:     new("10.0.0.0/16"),
						Ipv6Cidr:     new("2001:db8::/32"),
					}.Build(),
				}.Build()

				_, err := server.validateVirtualNetwork(ctx, updated, existing)
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
				// (so VN-VAL-11 doesn't fire), sets ipv6_cidr to "" explicitly.
				existing := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Region:       "us-west-1",
						NetworkClass: "test-class",
						Ipv4Cidr:     new("10.0.0.0/16"),
						Ipv6Cidr:     new("2001:db8::/32"),
					}.Build(),
				}.Build()

				updated := privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Region:       "us-west-1",
						NetworkClass: "test-class",
						Ipv4Cidr:     new("10.0.0.0/16"),
						Ipv6Cidr:     new(""),
					}.Build(),
				}.Build()

				_, err := server.validateVirtualNetwork(ctx, updated, existing)
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(err.Error()).To(ContainSubstring("ipv6_cidr"))
				Expect(err.Error()).To(ContainSubstring("immutable"))
			})
		})

		Context("Round-trip: server.Update preserves CIDRs", func() {
			It("preserves CIDRs when Update omits CIDR fields (sec-1 regression)", func() {
				nc := createNetworkClass(ctx, privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY)

				// Create a VN with IPv4 CIDR via the server:
				createResponse, err := server.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
					Object: privatev1.VirtualNetwork_builder{
						Metadata: privatev1.Metadata_builder{
							Tenant: auth.SharedTenant,
						}.Build(),
						Spec: privatev1.VirtualNetworkSpec_builder{
							Ipv4Cidr:     new("10.0.0.0/16"),
							NetworkClass: nc.GetId(),
							Region:       "us-west-1",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				created := createResponse.GetObject()

				// Update with only the name changed; no CIDR fields in the request:
				updateResponse, err := server.Update(ctx, privatev1.VirtualNetworksUpdateRequest_builder{
					Object: privatev1.VirtualNetwork_builder{
						Id: created.GetId(),
						Metadata: privatev1.Metadata_builder{
							Name: "renamed-vn",
						}.Build(),
						Spec: privatev1.VirtualNetworkSpec_builder{
							NetworkClass: nc.GetId(),
							Region:       "us-west-1",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				updated := updateResponse.GetObject()

				// CIDRs must be preserved from the existing record:
				Expect(updated.GetSpec().GetIpv4Cidr()).To(Equal("10.0.0.0/16"))
			})
		})
	})

	Describe("GenericDAO VirtualNetwork operations", func() {
		var generic *dao.GenericDAO[*privatev1.VirtualNetwork]

		BeforeEach(func() {
			var err error

			// Create DAO
			generic, err = dao.NewGenericDAO[*privatev1.VirtualNetwork]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("creates VirtualNetwork and generates ID", func() {
			vn := privatev1.VirtualNetwork_builder{
				Metadata: privatev1.Metadata_builder{
					Tenant: auth.SharedTenant,
				}.Build(),
				Spec: privatev1.VirtualNetworkSpec_builder{
					Ipv4Cidr:     new("10.0.0.0/16"),
					Region:       "us-west-1",
					NetworkClass: "class-id",
				}.Build(),
			}.Build()

			response, err := generic.Create().
				SetObject(vn).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			vn = response.GetObject()
			Expect(vn.GetId()).ToNot(BeEmpty())
			Expect(vn.GetMetadata()).ToNot(BeNil())
			Expect(vn.GetMetadata().GetCreationTimestamp()).ToNot(BeNil())
		})

		It("retrieves VirtualNetwork by ID", func() {
			vn := privatev1.VirtualNetwork_builder{
				Metadata: privatev1.Metadata_builder{
					Tenant: auth.SharedTenant,
				}.Build(),
				Spec: privatev1.VirtualNetworkSpec_builder{
					Ipv4Cidr:     new("10.0.0.0/16"),
					Region:       "us-west-1",
					NetworkClass: "class-id",
				}.Build(),
			}.Build()

			createResponse, err := generic.Create().
				SetObject(vn).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			vn = createResponse.GetObject()

			getResponse, err := generic.Get().
				SetId(vn.GetId()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			retrieved := getResponse.GetObject()
			Expect(proto.Equal(vn, retrieved)).To(BeTrue())
		})

		It("lists VirtualNetworks with pagination", func() {
			// Create multiple VirtualNetworks
			const count = 10
			for i := range count {
				vn := privatev1.VirtualNetwork_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   fmt.Sprintf("vn-%d", i),
						Tenant: auth.SharedTenant,
					}.Build(),
					Spec: privatev1.VirtualNetworkSpec_builder{
						Ipv4Cidr:     new(fmt.Sprintf("10.%d.0.0/16", i)),
						Region:       "us-west-1",
						NetworkClass: "class-id",
					}.Build(),
				}.Build()

				_, err := generic.Create().
					SetObject(vn).
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

		It("lists with SQL filtering", func() {
			// Create test data
			for i := range 5 {
				vn := privatev1.VirtualNetwork_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   fmt.Sprintf("vn-%d", i),
						Tenant: auth.SharedTenant,
					}.Build(),
					Spec: privatev1.VirtualNetworkSpec_builder{
						Ipv4Cidr:     new(fmt.Sprintf("10.%d.0.0/16", i)),
						Region:       "us-west-1",
						NetworkClass: "class-id",
					}.Build(),
				}.Build()

				_, err := generic.Create().
					SetObject(vn).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
			}

			// List with filter
			response, err := generic.List().
				SetFilter("this.metadata.name == 'vn-2'").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(1))
			Expect(response.GetItems()[0].GetMetadata().GetName()).To(Equal("vn-2"))
		})

		It("updates VirtualNetwork", func() {
			vn := privatev1.VirtualNetwork_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   "original-name",
					Tenant: auth.SharedTenant,
				}.Build(),
				Spec: privatev1.VirtualNetworkSpec_builder{
					Ipv4Cidr:     new("10.0.0.0/16"),
					Region:       "us-west-1",
					NetworkClass: "class-id",
				}.Build(),
			}.Build()

			createResponse, err := generic.Create().
				SetObject(vn).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			vn = createResponse.GetObject()

			// Update name
			vn.GetMetadata().Name = "updated-name"
			updateResponse, err := generic.Update().
				SetObject(vn).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			vn = updateResponse.GetObject()

			// Verify update
			getResponse, err := generic.Get().
				SetId(vn.GetId()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			retrieved := getResponse.GetObject()
			Expect(retrieved.GetMetadata().GetName()).To(Equal("updated-name"))
		})

		It("soft deletes VirtualNetwork", func() {
			vn := privatev1.VirtualNetwork_builder{
				Metadata: privatev1.Metadata_builder{
					Finalizers: []string{"test-finalizer"},
					Tenant:     auth.SharedTenant,
				}.Build(),
				Spec: privatev1.VirtualNetworkSpec_builder{
					Ipv4Cidr:     new("10.0.0.0/16"),
					Region:       "us-west-1",
					NetworkClass: "class-id",
				}.Build(),
			}.Build()

			createResponse, err := generic.Create().
				SetObject(vn).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			vn = createResponse.GetObject()

			// Delete
			_, err = generic.Delete().
				SetId(vn.GetId()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Verify soft delete (deletion_timestamp set, object still retrievable)
			getResponse, err := generic.Get().
				SetId(vn.GetId()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			retrieved := getResponse.GetObject()
			Expect(retrieved.GetMetadata().GetDeletionTimestamp()).ToNot(BeNil())
		})

		It("enforces tenant isolation", func() {
			// Create VirtualNetwork with tenant-a
			vn1 := privatev1.VirtualNetwork_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   "vn-tenant-a",
					Tenant: "tenant-a",
				}.Build(),
				Spec: privatev1.VirtualNetworkSpec_builder{
					Ipv4Cidr:     new("10.1.0.0/16"),
					Region:       "us-west-1",
					NetworkClass: "class-id",
				}.Build(),
			}.Build()

			_, err := generic.Create().
				SetObject(vn1).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Create VirtualNetwork with tenant-b
			vn2 := privatev1.VirtualNetwork_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   "vn-tenant-b",
					Tenant: "tenant-b",
				}.Build(),
				Spec: privatev1.VirtualNetworkSpec_builder{
					Ipv4Cidr:     new("10.2.0.0/16"),
					Region:       "us-west-1",
					NetworkClass: "class-id",
				}.Build(),
			}.Build()

			_, err = generic.Create().
				SetObject(vn2).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// List should return both (tenancy logic allows all in test context)
			response, err := generic.List().Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(2))
		})
	})

	Describe("Default NetworkClass auto-population", func() {
		var vnServer *PrivateVirtualNetworksServer

		BeforeEach(func() {
			var err error
			vnServer, err = NewPrivateVirtualNetworksServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Create VN without network_class auto-populates from default NC", func() {
			// Create a default NC in READY state:
			defaultNC := createDefaultNetworkClassViaDAO(ctx, privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY)

			// Create VN without network_class:
			createResponse, err := vnServer.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
				Object: privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Ipv4Cidr: new("10.0.0.0/16"),
						Region:   "us-west-1",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(createResponse.GetObject().GetSpec().GetNetworkClass()).To(Equal(defaultNC.GetId()))
		})

		It("Create VN without network_class when no default exists returns error", func() {
			// Create NC without is_default=true:
			createNetworkClass(ctx, privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY)

			// Create VN without network_class (no default configured):
			_, err := vnServer.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
				Object: privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Ipv4Cidr: new("10.0.0.0/16"),
						Region:   "us-west-1",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no default NetworkClass is configured"))
		})

		It("Explicit network_class ignores default", func() {
			// Create NC-A as default:
			_ = createDefaultNetworkClassViaDAO(ctx, privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY)

			// Create NC-B as non-default:
			ncB := createNetworkClass(ctx, privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY)

			// Create VN with explicit network_class=NC-B:
			createResponse, err := vnServer.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
				Object: privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Ipv4Cidr:     new("10.0.0.0/16"),
						Region:       "us-west-1",
						NetworkClass: ncB.GetId(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(createResponse.GetObject().GetSpec().GetNetworkClass()).To(Equal(ncB.GetId()))
		})

		It("Default NC must be READY: PENDING default returns FailedPrecondition", func() {
			// Create a default NC in PENDING state:
			createDefaultNetworkClassViaDAO(ctx, privatev1.NetworkClassState_NETWORK_CLASS_STATE_PENDING)

			// Create VN without network_class (default is not READY):
			_, err := vnServer.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
				Object: privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Ipv4Cidr: new("10.0.0.0/16"),
						Region:   "us-west-1",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
			Expect(err.Error()).To(ContainSubstring("not in READY state"))
		})

		It("Default NC capability mismatch is rejected", func() {
			// Create a default NC that supports only IPv4 (not IPv6):
			ncDao, err := dao.NewGenericDAO[*privatev1.NetworkClass]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			nc := privatev1.NetworkClass_builder{
				ImplementationStrategy: "test-strategy",
				IsDefault:              new(true),
				Metadata: privatev1.Metadata_builder{
					Tenant: auth.SharedTenant,
				}.Build(),
				Capabilities: privatev1.NetworkClassCapabilities_builder{
					SupportsIpv4: true,
					SupportsIpv6: false,
				}.Build(),
				Status: privatev1.NetworkClassStatus_builder{
					State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
				}.Build(),
			}.Build()
			_, err = ncDao.Create().SetObject(nc).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Create VN without network_class but requesting IPv6 capability:
			// Must set both capabilities AND ipv6_cidr to trigger VN-VAL-06 check.
			_, err = vnServer.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
				Object: privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Ipv6Cidr: new("2001:db8::/32"),
						Region:   "us-west-1",
						Capabilities: privatev1.VirtualNetworkCapabilities_builder{
							EnableIpv6: true,
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("does not support IPv6"))
		})

		It("Create VN without network_class after default NC is deleted", func() {
			// Create a default NC in READY state, then delete it via DAO:
			defaultNC := createDefaultNetworkClassViaDAO(ctx, privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY)
			ncDao, ncErr := dao.NewGenericDAO[*privatev1.NetworkClass]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(ncErr).ToNot(HaveOccurred())
			_, ncErr = ncDao.Delete().SetId(defaultNC.GetId()).Do(ctx)
			Expect(ncErr).ToNot(HaveOccurred())

			// Attempt to create VN without network_class (no default exists now):
			_, err := vnServer.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
				Object: privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Ipv4Cidr: new("10.0.0.0/16"),
						Region:   "us-west-1",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no default NetworkClass is configured"))
		})

		It("Create VN without network_class rejects soft-deleted default NC", func() {
			// Create a default NC in READY state:
			defaultNC := createDefaultNetworkClassViaDAO(ctx, privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY)

			// Soft-delete the default NC by setting deletion_timestamp via SQL:
			tx, err := database.TxFromContext(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, sqlErr := tx.Exec(ctx,
				"UPDATE network_classes SET deletion_timestamp = now() WHERE id = $1",
				defaultNC.GetId(),
			)
			Expect(sqlErr).ToNot(HaveOccurred())

			// Create VN without network_class. findDefaultNetworkClass excludes
			// soft-deleted rows, so no active default is found and creation fails.
			_, err = vnServer.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
				Object: privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Ipv4Cidr: new("10.0.0.0/16"),
						Region:   "us-west-1",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no default NetworkClass is configured"))
		})

		It("Auto-populated network_class is immutable on Update", func() {
			// Create a default NC in READY state:
			defaultNC := createDefaultNetworkClassViaDAO(ctx, privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY)

			// Create VN without network_class (auto-populated):
			createResponse, err := vnServer.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
				Object: privatev1.VirtualNetwork_builder{
					Spec: privatev1.VirtualNetworkSpec_builder{
						Ipv4Cidr: new("10.0.0.0/16"),
						Region:   "us-west-1",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			vn := createResponse.GetObject()
			Expect(vn.GetSpec().GetNetworkClass()).To(Equal(defaultNC.GetId()))

			// Create a second NC to attempt switching to:
			ncB := createNetworkClass(ctx, privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY)

			// Attempt Update changing network_class:
			_, err = vnServer.Update(ctx, privatev1.VirtualNetworksUpdateRequest_builder{
				Object: privatev1.VirtualNetwork_builder{
					Id: vn.GetId(),
					Spec: privatev1.VirtualNetworkSpec_builder{
						Ipv4Cidr:     new("10.0.0.0/16"),
						Region:       "us-west-1",
						NetworkClass: ncB.GetId(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("network_class"))
			Expect(err.Error()).To(ContainSubstring("immutable"))
		})
	})

	Describe("Deletion validation", func() {
		var (
			vnServer  *PrivateVirtualNetworksServer
			subnetDao *dao.GenericDAO[*privatev1.Subnet]
			sgDao     *dao.GenericDAO[*privatev1.SecurityGroup]
		)

		BeforeEach(func() {
			var err error
			vnServer, err = NewPrivateVirtualNetworksServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			subnetDao, err = dao.NewGenericDAO[*privatev1.Subnet]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			sgDao, err = dao.NewGenericDAO[*privatev1.SecurityGroup]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		createVirtualNetwork := func() *privatev1.VirtualNetwork {
			nc := createNetworkClass(ctx, privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY)
			createResp, err := vnServer.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
				Object: privatev1.VirtualNetwork_builder{
					Metadata: privatev1.Metadata_builder{
						Tenant: auth.SharedTenant,
					}.Build(),
					Spec: privatev1.VirtualNetworkSpec_builder{
						Ipv4Cidr:     proto.String("10.0.0.0/16"),
						NetworkClass: nc.GetId(),
						Region:       "us-west-1",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			return createResp.GetObject()
		}

		setVirtualNetworkReady := func(vn *privatev1.VirtualNetwork) {
			vnDao, err := dao.NewGenericDAO[*privatev1.VirtualNetwork]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			vn.SetStatus(privatev1.VirtualNetworkStatus_builder{
				State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY,
			}.Build())
			_, err = vnDao.Update().SetObject(vn).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
		}

		It("allows deletion when no child resources exist", func() {
			vn := createVirtualNetwork()

			_, err := vnServer.Delete(ctx, privatev1.VirtualNetworksDeleteRequest_builder{
				Id: vn.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("blocks deletion when a Subnet references the VirtualNetwork", func() {
			vn := createVirtualNetwork()
			setVirtualNetworkReady(vn)

			subnet := privatev1.Subnet_builder{
				Metadata: privatev1.Metadata_builder{
					Tenant: auth.SharedTenant,
				}.Build(),
				Spec: privatev1.SubnetSpec_builder{
					VirtualNetwork: vn.GetId(),
					Ipv4Cidr:       proto.String("10.0.1.0/24"),
				}.Build(),
			}.Build()
			_, err := subnetDao.Create().SetObject(subnet).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			_, err = vnServer.Delete(ctx, privatev1.VirtualNetworksDeleteRequest_builder{
				Id: vn.GetId(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
			Expect(err.Error()).To(ContainSubstring("Subnet"))
			Expect(err.Error()).To(ContainSubstring(vn.GetId()))
		})

		It("blocks deletion when a SecurityGroup references the VirtualNetwork", func() {
			vn := createVirtualNetwork()

			sg := privatev1.SecurityGroup_builder{
				Metadata: privatev1.Metadata_builder{
					Tenant: auth.SharedTenant,
				}.Build(),
				Spec: privatev1.SecurityGroupSpec_builder{
					VirtualNetwork: vn.GetId(),
				}.Build(),
			}.Build()
			_, err := sgDao.Create().SetObject(sg).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			_, err = vnServer.Delete(ctx, privatev1.VirtualNetworksDeleteRequest_builder{
				Id: vn.GetId(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
			Expect(err.Error()).To(ContainSubstring("SecurityGroup"))
			Expect(err.Error()).To(ContainSubstring(vn.GetId()))
		})

		It("blocks deletion when both Subnets and SecurityGroups reference the VirtualNetwork", func() {
			vn := createVirtualNetwork()
			setVirtualNetworkReady(vn)

			subnet := privatev1.Subnet_builder{
				Metadata: privatev1.Metadata_builder{
					Tenant: auth.SharedTenant,
				}.Build(),
				Spec: privatev1.SubnetSpec_builder{
					VirtualNetwork: vn.GetId(),
					Ipv4Cidr:       proto.String("10.0.1.0/24"),
				}.Build(),
			}.Build()
			_, err := subnetDao.Create().SetObject(subnet).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			sg := privatev1.SecurityGroup_builder{
				Metadata: privatev1.Metadata_builder{
					Tenant: auth.SharedTenant,
				}.Build(),
				Spec: privatev1.SecurityGroupSpec_builder{
					VirtualNetwork: vn.GetId(),
				}.Build(),
			}.Build()
			_, err = sgDao.Create().SetObject(sg).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			_, err = vnServer.Delete(ctx, privatev1.VirtualNetworksDeleteRequest_builder{
				Id: vn.GetId(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
			Expect(err.Error()).To(ContainSubstring("Subnet"))
		})

		It("allows deletion after all child resources are removed", func() {
			vn := createVirtualNetwork()

			sg := privatev1.SecurityGroup_builder{
				Metadata: privatev1.Metadata_builder{
					Tenant: auth.SharedTenant,
				}.Build(),
				Spec: privatev1.SecurityGroupSpec_builder{
					VirtualNetwork: vn.GetId(),
				}.Build(),
			}.Build()
			sgResp, err := sgDao.Create().SetObject(sg).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			_, err = sgDao.Delete().SetId(sgResp.GetObject().GetId()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			_, err = vnServer.Delete(ctx, privatev1.VirtualNetworksDeleteRequest_builder{
				Id: vn.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("reports the correct count of referencing Subnets", func() {
			vn := createVirtualNetwork()
			setVirtualNetworkReady(vn)

			for i := range 3 {
				subnet := privatev1.Subnet_builder{
					Metadata: privatev1.Metadata_builder{
						Tenant: auth.SharedTenant,
					}.Build(),
					Spec: privatev1.SubnetSpec_builder{
						VirtualNetwork: vn.GetId(),
						Ipv4Cidr:       proto.String(fmt.Sprintf("10.0.%d.0/24", i)),
					}.Build(),
				}.Build()
				_, err := subnetDao.Create().SetObject(subnet).Do(ctx)
				Expect(err).ToNot(HaveOccurred())
			}

			_, err := vnServer.Delete(ctx, privatev1.VirtualNetworksDeleteRequest_builder{
				Id: vn.GetId(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
			Expect(err.Error()).To(ContainSubstring("3 Subnet"))
		})

		It("blocks deletion of default-labeled VirtualNetwork", func() {
			nc := createNetworkClass(ctx, privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY)
			createResp, err := vnServer.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
				Object: privatev1.VirtualNetwork_builder{
					Metadata: privatev1.Metadata_builder{
						Tenant: auth.SharedTenant,
						Labels: map[string]string{
							"osac.openshift.io/default": "true",
						},
					}.Build(),
					Spec: privatev1.VirtualNetworkSpec_builder{
						Ipv4Cidr:     proto.String("10.0.0.0/16"),
						NetworkClass: nc.GetId(),
						Region:       "us-west-1",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = vnServer.Delete(ctx, privatev1.VirtualNetworksDeleteRequest_builder{
				Id: createResp.GetObject().GetId(),
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
