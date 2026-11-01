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
	"net/netip"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Private virtual networks server", func() {
	var server *PrivateVirtualNetworksServer

	newServer := func(settings NetworkingSettings) *PrivateVirtualNetworksServer {
		result, err := NewPrivateVirtualNetworksServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			SetNetworkingSettings(settings).
			Build()
		Expect(err).ToNot(HaveOccurred())
		return result
	}

	create := func(cidr string, labels map[string]string) (*privatev1.VirtualNetwork, error) {
		response, err := server.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
			Object: privatev1.VirtualNetwork_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   "vn",
					Tenant: "tenant-a",
					Labels: labels,
				}.Build(),
				Spec: privatev1.VirtualNetworkSpec_builder{
					Ipv4Cidr: new(cidr),
				}.Build(),
			}.Build(),
		}.Build())
		return response.GetObject(), err
	}

	BeforeEach(func() {
		createTenant("tenant-a")
		server = newServer(DefaultNetworkingSettings())
	})

	Describe("Building", func() {
		It("fails without a logger", func() {
			_, err := NewPrivateVirtualNetworksServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
		})

		It("fails without tenancy logic", func() {
			_, err := NewPrivateVirtualNetworksServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
		})
	})

	Describe("Create", func() {
		It("fails when no NetworkClass is configured", func() {
			_, err := create("10.1.0.0/16", nil)
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
			Expect(err.Error()).To(ContainSubstring("no NetworkClass is configured"))
		})

		It("fails while the NetworkClass isn't READY", func() {
			seedNetworkClass(privatev1.NetworkClassState_NETWORK_CLASS_STATE_PENDING)
			_, err := create("10.1.0.0/16", nil)
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
			Expect(err.Error()).To(ContainSubstring("the NetworkClass is not READY"))
		})

		Context("with a READY NetworkClass", func() {
			BeforeEach(func() {
				seedNetworkClass(privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY)
			})

			It("creates the VirtualNetwork in PENDING state", func() {
				object, err := create("10.1.0.0/16", nil)
				Expect(err).ToNot(HaveOccurred())
				Expect(object.GetStatus().GetState()).To(Equal(
					privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING))
			})

			It("rejects status on create", func() {
				_, err := server.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
					Object: privatev1.VirtualNetwork_builder{
						Metadata: privatev1.Metadata_builder{Tenant: "tenant-a"}.Build(),
						Spec:     privatev1.VirtualNetworkSpec_builder{Ipv4Cidr: new("10.1.0.0/16")}.Build(),
						Status: privatev1.VirtualNetworkStatus_builder{
							State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY,
						}.Build(),
					}.Build(),
				}.Build())
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			})

			DescribeTable("rejects invalid CIDRs",
				func(cidr, message string) {
					_, err := create(cidr, nil)
					Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
					Expect(err.Error()).To(ContainSubstring(message))
				},
				Entry("missing", "", "spec.ipv4_cidr"),
				Entry("host bits", "10.1.0.1/16", "canonical"),
				Entry("IPv6", "2001:db8::/32", "IPv6"),
			)

			It("accepts a prefix at the address plan prefix limit", func() {
				_, err := create("10.1.0.0/25", nil)
				Expect(err).ToNot(HaveOccurred())
			})

			It("rejects a prefix beyond the address plan prefix limit", func() {
				_, err := create("10.1.0.0/26", nil)
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				Expect(err.Error()).To(ContainSubstring("prefix /26 exceeds the address plan prefix limit /25"))
			})

			It("rejects a range that overlaps an excluded range", func() {
				server = newServer(NetworkingSettings{
					ProvisioningEnabled: true,
					ExcludedIPv4CIDRs:   []netip.Prefix{netip.MustParsePrefix("10.1.128.0/17")},
				})
				_, err := create("10.1.0.0/16", nil)
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				Expect(err.Error()).To(ContainSubstring("overlaps the excluded range 10.1.128.0/17"))
			})

			It("rejects a range that overlaps an ExternalIPPool and names the pool", func() {
				pool := seedExternalIPPool("10.1.2.0/24", 256)
				_, err := create("10.1.0.0/16", nil)
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				Expect(err.Error()).To(ContainSubstring(pool.GetId()))
			})

			It("rejects the default label from a tenant", func() {
				_, err := create("10.1.0.0/16", defaultLabels())
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				Expect(err.Error()).To(ContainSubstring("is set only by the system"))
			})

			It("rejects creates in a tenant that is being deleted", func() {
				markTenantDeleting("tenant-a")
				_, err := create("10.1.0.0/16", nil)
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
				Expect(err.Error()).To(ContainSubstring("tenant 'tenant-a' is being deleted"))
			})
		})
	})

	Describe("Update and Delete", func() {
		var vn *privatev1.VirtualNetwork

		BeforeEach(func() {
			seedNetworkClass(privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY)
			vn = seedVirtualNetwork("tenant-a", "10.1.0.0/16",
				privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY, nil)
		})

		It("rejects a spec change", func() {
			object := vn
			object.GetSpec().SetIpv4Cidr("10.2.0.0/16")
			_, err := server.Update(ctx, privatev1.VirtualNetworksUpdateRequest_builder{
				Object:     object,
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.ipv4_cidr"}},
			}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("spec is immutable; delete and recreate the VirtualNetwork"))
		})

		It("accepts a status write", func() {
			object := vn
			object.GetStatus().SetMessage("ready")
			_, err := server.Update(ctx, privatev1.VirtualNetworksUpdateRequest_builder{
				Object:     object,
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.message"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("marks the VirtualNetwork DELETING, keeps it DELETING and accepts a second delete", func() {
			_, err := server.Delete(ctx, privatev1.VirtualNetworksDeleteRequest_builder{Id: vn.GetId()}.Build())
			Expect(err).ToNot(HaveOccurred())
			got, err := server.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: vn.GetId()}.Build())
			if err == nil {
				Expect(got.GetObject().GetStatus().GetState()).To(Equal(
					privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_DELETING))
				object := got.GetObject()
				object.GetStatus().SetState(privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY)
				_, err = server.Update(ctx, privatev1.VirtualNetworksUpdateRequest_builder{
					Object:     object,
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
				}.Build())
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				Expect(err.Error()).To(ContainSubstring("cannot change after delete was accepted"))
			}
			_, err = server.Delete(ctx, privatev1.VirtualNetworksDeleteRequest_builder{Id: vn.GetId()}.Build())
			Expect(grpcstatus.Code(err)).To(BeElementOf(grpccodes.OK, grpccodes.NotFound))
		})

		It("rejects the delete while a Subnet exists and names it", func() {
			subnet := seedSubnet(vn, "", "10.1.0.0/24", privatev1.SubnetState_SUBNET_STATE_READY, nil)
			_, err := server.Delete(ctx, privatev1.VirtualNetworksDeleteRequest_builder{Id: vn.GetId()}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
			Expect(err.Error()).To(ContainSubstring(subnet.GetId()))
		})

		It("rejects a tenant delete of the default VirtualNetwork", func() {
			defaultVN := seedVirtualNetwork("tenant-a", "10.0.0.0/16",
				privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY, defaultLabels())
			_, err := server.Delete(ctx, privatev1.VirtualNetworksDeleteRequest_builder{Id: defaultVN.GetId()}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
			Expect(err.Error()).To(ContainSubstring("cannot delete default virtual network"))
		})
	})
})
