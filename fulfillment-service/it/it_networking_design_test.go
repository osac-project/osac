/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package it

// Component-integration specs of the unified networking design (enhancement OSAC-1433, "Unified Networking": API
// operation constraint, Status, Error codes, NetworkClass, Subnet address plan and layout, SecurityGroup, Creation
// readiness gates, Deletion dependency guards and Provider networking control) against the deployed fulfillment
// service, its private and public gRPC APIs and PostgreSQL. They run in the service-only phase of the SUITE=fulfillment
// installer target, where no OSAC operator runs: the fixtures report networking status through the private API as the
// operator's feedback controllers would. The CI deployment has global.networking.provisioningEnabled=false.

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	appsv1 "k8s.io/api/apps/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// ndExcludedCIDR is the range that the dev profile excludes with global.networking.excludedIpv4Cidrs. It is the
// RFC 5737 TEST-NET-1 block, which no other spec of the suite uses as a VirtualNetwork or ExternalIPPool range.
const ndExcludedCIDR = "192.0.2.0/24"

var _ = Describe("Networking design: installation-created NetworkClass", Label("networking-design"), func() {
	var (
		c  ndClients
		nc *privatev1.NetworkClass
	)

	BeforeEach(func(ctx context.Context) {
		c = newNDClients()
		nc = ndReadyNetworkClass(ctx, c)
		ndLog("Deployment NetworkClass", nc)
	})

	It("TC-NC-01 has exactly one NetworkClass, created by the installer from global.networking", func(ctx context.Context) {
		By("listing every NetworkClass")
		response, err := c.networkClasses.List(ctx, privatev1.NetworkClassesListRequest_builder{}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetItems()).To(HaveLen(1), "a deployment has exactly one NetworkClass")

		By("checking the values the installer's NetworkClass step wrote (osac-installer values/dev)")
		Expect(nc.GetTitle()).To(Equal("OSAC NetworkClass"), "the title of the installer step: %s", ndText(nc))
		Expect(nc.GetFabricManager()).To(Equal("netris"), "global.networking.fabricManager: %s", ndText(nc))
		Expect(nc.GetK8SManager()).To(Equal("cudn_evpn"), "global.networking.k8sManager: %s", ndText(nc))
		defaults := nc.GetSpec().GetDefaults()
		Expect(defaults.GetVirtualNetworkIpv4Cidr()).To(Equal("10.200.0.0/16"), "chart default: %s", ndText(nc))
		Expect(defaults.GetSubnetIpv4Cidr()).To(Equal("10.200.0.0/20"), "chart default: %s", ndText(nc))
		Expect(defaults.GetEgressRules()).To(HaveLen(1), "chart default egress rules: %s", ndText(nc))
		Expect(defaults.GetEgressRules()[0].GetProtocol()).To(Equal(privatev1.Protocol_PROTOCOL_ALL))
		Expect(defaults.GetEgressRules()[0].GetIpv4Cidr()).To(Equal("0.0.0.0/0"))
		Expect(nc.GetSpec().GetEastWestCapabilities().GetSupportsEastWestEthernet()).To(BeFalse(),
			"the dev profile doesn't enable east-west Ethernet: %s", ndText(nc))

		By("checking the derived address plan against the plan validation rules")
		plan := nc.GetSpec().GetAddressPlan()
		Expect(plan).ToNot(BeNil(), "the installer always writes spec.address_plan: %s", ndText(nc))
		sixteenths := func(value float64) float64 { return value * 16 }
		for name, share := range map[string]float64{
			"overlay": plan.GetOverlay(), "fabric": plan.GetFabric(), "lb_vip": plan.GetLbVip(),
		} {
			Expect(sixteenths(share)).To(Equal(float64(int(sixteenths(share)))),
				"address_plan.%s %v is not a multiple of 0.0625", name, share)
		}
		Expect(plan.GetOverlay()+plan.GetFabric()+plan.GetLbVip()).To(Equal(float64(1)), "the shares sum to 1")
		Expect(plan.GetOverlay()).To(BeNumerically(">", 0), "k8sManager is set, so overlay > 0")
		Expect(plan.GetFabric()).To(BeNumerically(">", 0), "BMaaS and CaaS are enabled, so fabric > 0")
		Expect(plan.GetLbVip()).To(BeNumerically(">", 0), "CaaS is enabled, so lbVip > 0")
	})

	It("TC-NC-02 rejects a second NetworkClass with FAILED_PRECONDITION and stores nothing", func(ctx context.Context) {
		By("creating a second NetworkClass")
		_, err := c.networkClasses.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
			Object: privatev1.NetworkClass_builder{
				Title:         "Second NetworkClass",
				FabricManager: "netris",
				Spec: privatev1.NetworkClassSpec_builder{
					Defaults: privatev1.NetworkDefaults_builder{
						VirtualNetworkIpv4Cidr: "10.201.0.0/16",
						SubnetIpv4Cidr:         "10.201.0.0/24",
					}.Build(),
					AddressPlan: privatev1.AddressPlan_builder{Fabric: 0.75, LbVip: 0.25}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		ndExpectCode(err, grpccodes.FailedPrecondition, "only one NetworkClass per deployment", nc.GetId())

		By("checking that the deployment still has exactly the installer's NetworkClass")
		Expect(ndSingletonNetworkClass(ctx, c).GetId()).To(Equal(nc.GetId()))
	})

	It("TC-NC-03 keeps every field except status and spec.address_plan immutable on the private Update",
		func(ctx context.Context) {
			for _, mutation := range []struct {
				path   string
				mutate func(*privatev1.NetworkClass)
			}{
				{"fabric_manager", func(o *privatev1.NetworkClass) { o.SetFabricManager("other-fabric") }},
				{"k8s_manager", func(o *privatev1.NetworkClass) { o.SetK8SManager("other-k8s") }},
				{"title", func(o *privatev1.NetworkClass) { o.SetTitle("Changed title") }},
				{"spec.defaults", func(o *privatev1.NetworkClass) {
					o.GetSpec().GetDefaults().SetVirtualNetworkIpv4Cidr("10.201.0.0/16")
					o.GetSpec().GetDefaults().SetSubnetIpv4Cidr("10.201.0.0/20")
				}},
				{"spec.east_west_capabilities", func(o *privatev1.NetworkClass) {
					o.GetSpec().SetEastWestCapabilities(privatev1.NetworkClassEastWestCapabilities_builder{
						SupportsEastWestEthernet: true,
					}.Build())
				}},
				{"metadata.labels", func(o *privatev1.NetworkClass) {
					o.GetMetadata().SetLabels(map[string]string{"it.osac.openshift.io/changed": "true"})
				}},
			} {
				By(fmt.Sprintf("updating %s through the private API", mutation.path))
				object := proto.Clone(nc).(*privatev1.NetworkClass)
				mutation.mutate(object)
				_, err := c.networkClasses.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
					Object:     object,
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{mutation.path}},
				}.Build())
				ndExpectCode(err, grpccodes.InvalidArgument, "spec is immutable", "NetworkClass")
			}

			By("checking that nothing changed")
			current := ndSingletonNetworkClass(ctx, c)
			ndLog("NetworkClass after the rejected updates", current)
			Expect(current.GetFabricManager()).To(Equal(nc.GetFabricManager()))
			Expect(current.GetK8SManager()).To(Equal(nc.GetK8SManager()))
			Expect(current.GetTitle()).To(Equal(nc.GetTitle()))
			Expect(proto.Equal(current.GetSpec(), nc.GetSpec())).To(BeTrue(), "spec changed: %s", ndText(current))
			Expect(current.GetMetadata().GetLabels()).To(Equal(nc.GetMetadata().GetLabels()))
		})

	It("TC-NC-04 rejects any address_plan change: the NetworkClass spec is immutable",
		func(ctx context.Context) {
			By("making sure a VirtualNetwork exists")
			ndCreateVirtualNetwork(ctx, c, usersGroup, ndVirtualNetworkCIDR())

			writePlan := func(overlay, fabric, lbVip float64) error {
				object := proto.Clone(nc).(*privatev1.NetworkClass)
				object.GetSpec().SetAddressPlan(privatev1.AddressPlan_builder{
					Overlay: overlay, Fabric: fabric, LbVip: lbVip,
				}.Build())
				_, err := c.networkClasses.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
					Object:     object,
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.address_plan"}},
				}.Build())
				return err
			}
			stored := nc.GetSpec().GetAddressPlan()

			By("writing the stored plan again, which changes nothing and is accepted")
			Expect(writePlan(stored.GetOverlay(), stored.GetFabric(), stored.GetLbVip())).To(Succeed())

			By("writing a different valid plan")
			ndExpectCode(writePlan(0.5, 0.375, 0.125), grpccodes.InvalidArgument, "spec is immutable")

			By("writing an invalid plan")
			ndExpectCode(writePlan(0.3, 0.45, 0.25), grpccodes.InvalidArgument)

			By("checking that the stored plan didn't change")
			current := ndSingletonNetworkClass(ctx, c)
			Expect(proto.Equal(current.GetSpec().GetAddressPlan(), stored)).To(BeTrue(),
				"address plan changed: %s", ndText(current))
		})

	It("TC-NC-05 rejects the NetworkClass delete with FAILED_PRECONDITION while a VirtualNetwork exists",
		func(ctx context.Context) {
			By("making sure a VirtualNetwork exists")
			vn := ndCreateVirtualNetwork(ctx, c, usersGroup, ndVirtualNetworkCIDR())

			By("deleting the NetworkClass")
			_, err := c.networkClasses.Delete(ctx, privatev1.NetworkClassesDeleteRequest_builder{Id: nc.GetId()}.Build())
			ndExpectCode(err, grpccodes.FailedPrecondition, "cannot delete NetworkClass", "VirtualNetwork")
			fmt.Fprintf(GinkgoWriter, "VirtualNetwork %s exists while the delete is rejected\n", vn.GetId())

			By("checking that the NetworkClass is not being deleted")
			current := ndSingletonNetworkClass(ctx, c)
			Expect(current.GetMetadata().HasDeletionTimestamp()).To(BeFalse(), "NetworkClass: %s", ndText(current))
		})

	It("TC-NC-06 accepts status writes and gates VirtualNetwork and ExternalIPPool creates on READY",
		func(ctx context.Context) {
			DeferCleanup(func(cleanupCtx context.Context) {
				By("restoring the NetworkClass to READY")
				ndSetNetworkClassStatus(cleanupCtx, c, nc.GetId(),
					privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY, "")
			})

			By("reporting the NetworkClass PENDING with a message, as the operator does while it resolves it")
			ndSetNetworkClassStatus(ctx, c, nc.GetId(), privatev1.NetworkClassState_NETWORK_CLASS_STATE_PENDING,
				"it: waiting for the manager registrations")
			current := ndSingletonNetworkClass(ctx, c)
			ndLog("PENDING NetworkClass", current)
			Expect(current.GetStatus().GetState()).To(Equal(privatev1.NetworkClassState_NETWORK_CLASS_STATE_PENDING))
			Expect(current.GetStatus().GetMessage()).To(Equal("it: waiting for the manager registrations"))

			By("creating a VirtualNetwork while the NetworkClass is not READY")
			name := ndUniqueName("nd-vn-gated")
			_, err := c.publicVirtualNetworks.Create(ctx, publicv1.VirtualNetworksCreateRequest_builder{
				Object: publicv1.VirtualNetwork_builder{
					Metadata: publicv1.Metadata_builder{Name: name}.Build(),
					Spec:     publicv1.VirtualNetworkSpec_builder{Ipv4Cidr: new(ndVirtualNetworkCIDR())}.Build(),
				}.Build(),
			}.Build())
			ndExpectCode(err, grpccodes.FailedPrecondition, "the NetworkClass is not READY")
			expectNoVirtualNetworkNamed(ctx, c.virtualNetworks, name)

			By("creating an ExternalIPPool while the NetworkClass is not READY")
			_, err = c.pools.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
				Object: privatev1.ExternalIPPool_builder{
					Metadata: privatev1.Metadata_builder{Name: ndUniqueName("nd-pool-gated")}.Build(),
					Spec: privatev1.ExternalIPPoolSpec_builder{
						Cidrs:    []string{uniqueCIDR()},
						IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
					}.Build(),
				}.Build(),
			}.Build())
			ndExpectCode(err, grpccodes.FailedPrecondition, "the NetworkClass is not READY")

			By("reporting the NetworkClass READY again; the message is cleared")
			ndSetNetworkClassStatus(ctx, c, nc.GetId(), privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY, "")
			current = ndSingletonNetworkClass(ctx, c)
			Expect(current.GetStatus().GetState()).To(Equal(privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY))
			Expect(current.GetStatus().HasMessage()).To(BeFalse(), "NetworkClass: %s", ndText(current))

			By("creating the VirtualNetwork now that the NetworkClass is READY")
			ndCreateVirtualNetwork(ctx, c, usersGroup, ndVirtualNetworkCIDR())
		})
})

var _ = Describe("Networking design: creation gates and address rules", Label("networking-design"), func() {
	var c ndClients

	BeforeEach(func(ctx context.Context) {
		c = newNDClients()
		ndReadyNetworkClass(ctx, c)
	})

	It("TC-GATE-01 rejects Subnet and SecurityGroup creates while the parent VirtualNetwork is not READY",
		func(ctx context.Context) {
			cidr := ndVirtualNetworkCIDR()
			vn := ndCreateVirtualNetwork(ctx, c, usersGroup, cidr)
			ndWaitVirtualNetworkPlaced(ctx, c, vn.GetId())
			prefix := netip.MustParsePrefix(cidr)
			subnetCIDR := netip.PrefixFrom(prefix.Addr(), 24).String()

			By("creating a Subnet in the PENDING VirtualNetwork through the public API")
			subnetName := ndUniqueName("nd-subnet-gated")
			_, err := c.publicSubnets.Create(ctx, publicv1.SubnetsCreateRequest_builder{
				Object: publicv1.Subnet_builder{
					Metadata: publicv1.Metadata_builder{Name: subnetName}.Build(),
					Spec: publicv1.SubnetSpec_builder{
						VirtualNetwork: publicv1.VirtualNetworkLocalReference_builder{Id: vn.GetId()}.Build(),
						Ipv4Cidr:       new(subnetCIDR),
					}.Build(),
				}.Build(),
			}.Build())
			ndExpectCode(err, grpccodes.FailedPrecondition, vn.GetId(), "is not in READY state")
			expectNoSubnetNamed(ctx, c.subnets, subnetName)

			By("creating a SecurityGroup in the PENDING VirtualNetwork through the public API")
			groupName := ndUniqueName("nd-sg-gated")
			_, err = c.publicSecurityGroups.Create(ctx, publicv1.SecurityGroupsCreateRequest_builder{
				Object: publicv1.SecurityGroup_builder{
					Metadata: publicv1.Metadata_builder{Name: groupName}.Build(),
					Spec: publicv1.SecurityGroupSpec_builder{
						VirtualNetwork: publicv1.VirtualNetworkLocalReference_builder{Id: vn.GetId()}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			ndExpectCode(err, grpccodes.FailedPrecondition, vn.GetId(), "is not in READY state")
			expectNoSecurityGroupNamed(ctx, c.securityGroups, groupName)

			By("reporting the VirtualNetwork READY and creating both again")
			ndSetVirtualNetworkState(ctx, c, vn.GetId(), privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY)
			subnet := ndCreateSubnet(ctx, c, usersGroup, vn.GetId(), subnetCIDR)
			Expect(subnet.GetStatus().GetState()).To(Equal(privatev1.SubnetState_SUBNET_STATE_PENDING),
				"a new Subnet starts PENDING: %s", ndText(subnet))
			group := ndCreateSecurityGroup(ctx, c, usersGroup, vn.GetId(), nil, nil)
			Expect(group.GetStatus().GetState()).To(Equal(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_PENDING),
				"a new SecurityGroup starts PENDING: %s", ndText(group))
		})

	It("TC-IPAM-01 enforces canonical IPv4 CIDRs, the address plan prefix limit and Subnet containment",
		func(ctx context.Context) {
			nc := ndSingletonNetworkClass(ctx, c)
			plan := nc.GetSpec().GetAddressPlan()
			ndLog("Address plan", plan)
			// The prefix limit is 28 - log2(1/g), with g the largest power of two dividing every non-zero share.
			granule := 16
			for _, share := range []float64{plan.GetOverlay(), plan.GetFabric(), plan.GetLbVip()} {
				n := int(share * 16)
				for n != 0 && n%granule != 0 {
					granule /= 2
				}
			}
			limit := 28
			for g := 16 / granule; g > 1; g /= 2 {
				limit--
			}
			fmt.Fprintf(GinkgoWriter, "Address plan prefix limit: /%d\n", limit)

			createVN := func(cidr string) error {
				_, err := c.publicVirtualNetworks.Create(ctx, publicv1.VirtualNetworksCreateRequest_builder{
					Object: publicv1.VirtualNetwork_builder{
						Metadata: publicv1.Metadata_builder{Name: ndUniqueName("nd-vn-cidr")}.Build(),
						Spec:     publicv1.VirtualNetworkSpec_builder{Ipv4Cidr: new(cidr)}.Build(),
					}.Build(),
				}.Build())
				return err
			}

			By("rejecting a non-canonical VirtualNetwork CIDR")
			ndExpectCode(createVN("10.150.0.1/16"), grpccodes.InvalidArgument, "canonical")
			By("rejecting an IPv6 VirtualNetwork CIDR")
			ndExpectCode(createVN("fd00:10::/48"), grpccodes.InvalidArgument)
			By(fmt.Sprintf("rejecting a VirtualNetwork longer than the prefix limit /%d", limit))
			ndExpectCode(createVN(fmt.Sprintf("10.150.0.0/%d", limit+1)), grpccodes.InvalidArgument,
				fmt.Sprintf("prefix limit /%d", limit))

			By("creating a READY VirtualNetwork for the Subnet rules")
			cidr := ndVirtualNetworkCIDR()
			vn := ndReadyVirtualNetwork(ctx, c, usersGroup, cidr)
			base := netip.MustParsePrefix(cidr).Addr()
			createSubnet := func(subnetCIDR string) error {
				_, err := c.publicSubnets.Create(ctx, publicv1.SubnetsCreateRequest_builder{
					Object: publicv1.Subnet_builder{
						Metadata: publicv1.Metadata_builder{Name: ndUniqueName("nd-subnet-cidr")}.Build(),
						Spec: publicv1.SubnetSpec_builder{
							VirtualNetwork: publicv1.VirtualNetworkLocalReference_builder{Id: vn.GetId()}.Build(),
							Ipv4Cidr:       new(subnetCIDR),
						}.Build(),
					}.Build(),
				}.Build())
				return err
			}

			By("rejecting a non-canonical Subnet CIDR")
			ndExpectCode(createSubnet(netip.PrefixFrom(base.Next(), 24).String()), grpccodes.InvalidArgument,
				"canonical")
			By("rejecting an IPv6 Subnet CIDR")
			ndExpectCode(createSubnet("fd00:10::/64"), grpccodes.InvalidArgument)
			By("rejecting a Subnet outside the VirtualNetwork")
			ndExpectCode(createSubnet("10.249.0.0/24"), grpccodes.InvalidArgument, "is not within parent")
			By(fmt.Sprintf("rejecting a Subnet longer than the prefix limit /%d", limit))
			ndExpectCode(createSubnet(netip.PrefixFrom(base, limit+1).String()), grpccodes.InvalidArgument,
				fmt.Sprintf("prefix limit /%d", limit))

			By(fmt.Sprintf("accepting a Subnet at the prefix limit /%d", limit))
			atLimit := ndCreateSubnet(ctx, c, usersGroup, vn.GetId(), netip.PrefixFrom(base, limit).String())

			By("rejecting a Subnet that overlaps its sibling with ALREADY_EXISTS")
			ndExpectCode(createSubnet(netip.PrefixFrom(base, 24).String()), grpccodes.AlreadyExists,
				"overlaps with existing subnet", atLimit.GetId())

			By("accepting the same CIDR in another VirtualNetwork: VirtualNetworks may overlap")
			other := ndReadyVirtualNetwork(ctx, c, usersGroup, cidr)
			ndCreateSubnet(ctx, c, usersGroup, other.GetId(), netip.PrefixFrom(base, limit).String())
		})

	It("TC-IPAM-02 rejects overlaps between ExternalIPPools, VirtualNetworks and the NetworkClass default",
		func(ctx context.Context) {
			nc := ndSingletonNetworkClass(ctx, c)
			poolBase := ndPoolBase()
			poolCIDR := poolBase + "/28"
			pool := ndCreatePool(ctx, c, poolCIDR)

			By("rejecting a VirtualNetwork that overlaps the pool, naming only the pool ID")
			_, err := c.publicVirtualNetworks.Create(ctx, publicv1.VirtualNetworksCreateRequest_builder{
				Object: publicv1.VirtualNetwork_builder{
					Metadata: publicv1.Metadata_builder{Name: ndUniqueName("nd-vn-pool")}.Build(),
					Spec: publicv1.VirtualNetworkSpec_builder{
						Ipv4Cidr: new(netip.PrefixFrom(netip.MustParseAddr(poolBase), 24).Masked().String()),
					}.Build(),
				}.Build(),
			}.Build())
			ndExpectCode(err, grpccodes.InvalidArgument, "overlaps ExternalIPPool", pool.GetId())

			createPool := func(cidr string) error {
				_, err := c.pools.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
					Object: privatev1.ExternalIPPool_builder{
						Metadata: privatev1.Metadata_builder{Name: ndUniqueName("nd-pool-overlap")}.Build(),
						Spec: privatev1.ExternalIPPoolSpec_builder{
							Cidrs:    []string{cidr},
							IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
						}.Build(),
					}.Build(),
				}.Build())
				return err
			}

			By("rejecting a pool that overlaps another pool")
			ndExpectCode(createPool(poolBase+"/29"), grpccodes.InvalidArgument, "overlaps ExternalIPPool",
				pool.GetId())

			By("rejecting a pool inside the NetworkClass default VirtualNetwork CIDR")
			defaultPrefix := netip.MustParsePrefix(nc.GetSpec().GetDefaults().GetVirtualNetworkIpv4Cidr())
			ndExpectCode(createPool(netip.PrefixFrom(defaultPrefix.Addr(), 28).String()), grpccodes.InvalidArgument,
				"overlaps the default VirtualNetwork CIDR")

			By("rejecting a pool inside an existing VirtualNetwork")
			vnCIDR := ndVirtualNetworkCIDR()
			vn := ndCreateVirtualNetwork(ctx, c, usersGroup, vnCIDR)
			insideVN := netip.PrefixFrom(netip.MustParsePrefix(vnCIDR).Addr(), 28).String()
			err = createPool(insideVN)
			ndExpectCode(err, grpccodes.InvalidArgument, "overlaps")
			Expect(grpcstatus.Convert(err).Message()).To(SatisfyAny(ContainSubstring(vn.GetId()),
				ContainSubstring("VirtualNetwork")), "the error names the overlapping VirtualNetwork")

			By("rejecting an IPv6 pool")
			_, err = c.pools.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
				Object: privatev1.ExternalIPPool_builder{
					Metadata: privatev1.Metadata_builder{Name: ndUniqueName("nd-pool-v6")}.Build(),
					Spec: privatev1.ExternalIPPoolSpec_builder{
						Cidrs:    []string{"2001:db8::/120"},
						IpFamily: privatev1.IPFamily_IP_FAMILY_IPV6,
					}.Build(),
				}.Build(),
			}.Build())
			ndExpectCode(err, grpccodes.InvalidArgument, "object.spec.ip_family")
		})

	It("TC-IPAM-03 rejects VirtualNetworks and ExternalIPPools that overlap global.networking.excludedIpv4Cidrs",
		func(ctx context.Context) {
			// The dev profile sets global.networking.excludedIpv4Cidrs to ndExcludedCIDR
			// (osac-installer/values/dev/kind-instance.yaml). A Subnet always lies inside its VirtualNetwork, so the
			// VirtualNetwork check is what keeps a Subnet out of the excluded ranges.
			createVN := func(name, cidr string) error {
				_, err := c.publicVirtualNetworks.Create(ctx, publicv1.VirtualNetworksCreateRequest_builder{
					Object: publicv1.VirtualNetwork_builder{
						Metadata: publicv1.Metadata_builder{Name: name}.Build(),
						Spec:     publicv1.VirtualNetworkSpec_builder{Ipv4Cidr: new(cidr)}.Build(),
					}.Build(),
				}.Build())
				return err
			}
			createPool := func(name, cidr string) error {
				_, err := c.pools.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
					Object: privatev1.ExternalIPPool_builder{
						Metadata: privatev1.Metadata_builder{Name: name}.Build(),
						Spec: privatev1.ExternalIPPoolSpec_builder{
							Cidrs:    []string{cidr},
							IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
						}.Build(),
					}.Build(),
				}.Build())
				return err
			}

			for _, cidr := range []string{ndExcludedCIDR, "192.0.0.0/16"} {
				By(fmt.Sprintf("rejecting a VirtualNetwork %s that overlaps the excluded range", cidr))
				name := ndUniqueName("nd-vn-excluded")
				ndExpectCode(createVN(name, cidr), grpccodes.InvalidArgument, "overlaps the excluded range",
					ndExcludedCIDR)
				expectNoVirtualNetworkNamed(ctx, c.virtualNetworks, name)
			}

			By("rejecting an ExternalIPPool inside the excluded range")
			name := ndUniqueName("nd-pool-excluded")
			ndExpectCode(createPool(name, "192.0.2.0/28"), grpccodes.InvalidArgument,
				"overlaps the excluded range", ndExcludedCIDR)
			expectNoExternalIPPoolNamed(ctx, c.pools, name)

			By("accepting a VirtualNetwork next to the excluded range")
			neighbour := ndCreateVirtualNetwork(ctx, c, usersGroup, "192.0.3.0/24")
			Expect(neighbour.GetSpec().GetIpv4Cidr()).To(Equal("192.0.3.0/24"))

			By("accepting an ExternalIPPool next to the excluded range")
			pool := ndCreatePool(ctx, c, "192.0.4.0/28")
			Expect(pool.GetSpec().GetCidrs()).To(Equal([]string{"192.0.4.0/28"}))
		})

	It("TC-POOL-01 computes pool capacity, ignores written counters and shows tenants only READY pools",
		func(ctx context.Context) {
			for _, testCase := range []struct {
				bits  int
				total int64
			}{{28, 14}, {31, 2}, {32, 1}} {
				cidr := fmt.Sprintf("%s/%d", ndPoolBase(), testCase.bits)
				pool := ndCreatePool(ctx, c, cidr)
				By(fmt.Sprintf("checking the capacity of a /%d pool", testCase.bits))
				Expect(pool.GetStatus().GetState()).To(Equal(
					privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_PENDING), "pool: %s", ndText(pool))
				Expect(pool.GetStatus().GetTotal()).To(Equal(testCase.total), "pool: %s", ndText(pool))
				Expect(pool.GetStatus().GetAllocated()).To(BeZero(), "pool: %s", ndText(pool))
				Expect(pool.GetStatus().GetAvailable()).To(Equal(testCase.total), "pool: %s", ndText(pool))
			}

			cidr := ndPoolBase() + "/28"
			pool := ndCreatePool(ctx, c, cidr)

			By("checking that tenants don't see a PENDING pool")
			Consistently(func(g Gomega) {
				_, err := c.publicPools.Get(ctx, publicv1.ExternalIPPoolsGetRequest_builder{Id: pool.GetId()}.Build())
				g.Expect(grpcstatus.Code(err)).To(Equal(grpccodes.NotFound), "PENDING pool visible: %v", err)
			}, 3*time.Second, time.Second).Should(Succeed())

			By("reporting the pool READY with forged counters in the same write")
			ndStatusUpdate(ctx, "set the pool READY with forged counters", func() error {
				response, err := c.pools.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{Id: pool.GetId()}.Build())
				if err != nil {
					return err
				}
				object := response.GetObject()
				object.GetStatus().SetState(privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY)
				object.GetStatus().SetTotal(999)
				object.GetStatus().SetAllocated(500)
				object.GetStatus().SetAvailable(499)
				_, err = c.pools.Update(ctx, privatev1.ExternalIPPoolsUpdateRequest_builder{
					Object: object,
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{
						"status.state", "status.total", "status.allocated", "status.available",
					}},
					Lock: true,
				}.Build())
				return err
			})
			response, err := c.pools.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{Id: pool.GetId()}.Build())
			Expect(err).ToNot(HaveOccurred())
			ndLog("READY pool", response.GetObject())
			Expect(response.GetObject().GetStatus().GetState()).To(Equal(
				privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY))
			Expect(response.GetObject().GetStatus().GetTotal()).To(Equal(int64(14)), "fulfillment keeps the counters")
			Expect(response.GetObject().GetStatus().GetAllocated()).To(BeZero())
			Expect(response.GetObject().GetStatus().GetAvailable()).To(Equal(int64(14)))

			By("checking that tenants see the READY pool with only its available count")
			Eventually(func(g Gomega) {
				public, err := c.publicPools.Get(ctx, publicv1.ExternalIPPoolsGetRequest_builder{Id: pool.GetId()}.Build())
				g.Expect(err).ToNot(HaveOccurred())
				ndLog("Public pool", public.GetObject())
				g.Expect(public.GetObject().GetStatus().GetAvailable()).To(Equal(int64(14)))
				g.Expect(public.GetObject().GetSpec().GetIpFamily()).To(Equal(publicv1.IPFamily_IP_FAMILY_IPV4))
			}, ndTimeout, ndPoll).Should(Succeed())

			By("checking that the private spec is immutable")
			object := response.GetObject()
			object.GetSpec().SetCidrs([]string{ndPoolBase() + "/28"})
			_, err = c.pools.Update(ctx, privatev1.ExternalIPPoolsUpdateRequest_builder{
				Object:     object,
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.cidrs"}},
			}.Build())
			ndExpectCode(err, grpccodes.InvalidArgument)
		})
})

var _ = Describe("Networking design: deletion guards and DELETING", Label("networking-design"), func() {
	var c ndClients

	BeforeEach(func(ctx context.Context) {
		c = newNDClients()
		ndReadyNetworkClass(ctx, c)
	})

	It("TC-GUARD-01 rejects the VirtualNetwork delete while a Subnet or a SecurityGroup exists", func(ctx context.Context) {
		cidr := ndVirtualNetworkCIDR()
		vn := ndReadyVirtualNetwork(ctx, c, usersGroup, cidr)
		subnet := ndCreateSubnet(ctx, c, usersGroup, vn.GetId(),
			netip.PrefixFrom(netip.MustParsePrefix(cidr).Addr(), 24).String())
		group := ndCreateSecurityGroup(ctx, c, usersGroup, vn.GetId(), nil, nil)

		By("deleting the VirtualNetwork through the public API while the Subnet exists")
		_, err := c.publicVirtualNetworks.Delete(ctx, publicv1.VirtualNetworksDeleteRequest_builder{
			Id: vn.GetId(),
		}.Build())
		ndExpectCode(err, grpccodes.FailedPrecondition, "cannot delete VirtualNetwork", "1 active Subnet",
			subnet.GetId())

		By("deleting the Subnet; the SecurityGroup still blocks the delete")
		Expect(cleanupSubnetFixture(ctx, c.subnets, subnet.GetId())).To(Succeed())
		_, err = c.publicVirtualNetworks.Delete(ctx, publicv1.VirtualNetworksDeleteRequest_builder{
			Id: vn.GetId(),
		}.Build())
		ndExpectCode(err, grpccodes.FailedPrecondition, "cannot delete VirtualNetwork", "1 active SecurityGroup",
			group.GetId())

		By("deleting the SecurityGroup; the VirtualNetwork delete is accepted")
		Expect(cleanupSecurityGroupFixture(ctx, c.securityGroups, group.GetId())).To(Succeed())
		_, err = c.publicVirtualNetworks.Delete(ctx, publicv1.VirtualNetworksDeleteRequest_builder{
			Id: vn.GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Eventually(func(g Gomega) {
			_, err := c.virtualNetworks.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: vn.GetId()}.Build())
			g.Expect(grpcstatus.Code(err)).To(Equal(grpccodes.NotFound), "VirtualNetwork %s remains", vn.GetId())
		}, ndTimeout, ndPoll).Should(Succeed())
	})

	It("TC-GUARD-02 rejects the Tenant delete while a non-default networking resource of the tenant exists",
		func(ctx context.Context) {
			tenantID, tenantName := ndCreateTenant(ctx, c)
			vn := ndCreateVirtualNetwork(ctx, c, tenantName, ndVirtualNetworkCIDR())

			By("deleting the tenant while its VirtualNetwork exists")
			_, err := c.tenants.Delete(ctx, privatev1.TenantsDeleteRequest_builder{Id: tenantID}.Build())
			ndExpectCode(err, grpccodes.FailedPrecondition, "cannot delete tenant", "VirtualNetwork", vn.GetId())

			By("checking that the tenant is not being deleted")
			response, err := c.tenants.Get(ctx, privatev1.TenantsGetRequest_builder{Id: tenantID}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetMetadata().HasDeletionTimestamp()).To(BeFalse(),
				"tenant: %s", ndText(response.GetObject()))

			By("deleting the VirtualNetwork; the tenant delete is then accepted by the cleanup")
			Expect(cleanupVirtualNetworkFixture(ctx, c.virtualNetworks, vn.GetId())).To(Succeed())
		})

	It("TC-DELETING-01 keeps a VirtualNetwork DELETING once its delete is accepted; a second Delete returns OK",
		func(ctx context.Context) {
			vn := ndCreateVirtualNetwork(ctx, c, usersGroup, ndVirtualNetworkCIDR())
			ndWaitVirtualNetworkPlaced(ctx, c, vn.GetId())

			By("holding the record with a test finalizer, so the spec can inspect it while it is DELETING")
			ndMutateVirtualNetworkFinalizers(ctx, c, vn.GetId(), ndAddHold)
			DeferCleanup(func(cleanupCtx context.Context) {
				ndRemoveVirtualNetworkHoldIfPresent(cleanupCtx, c, vn.GetId())
			})

			By("deleting the VirtualNetwork through the public API")
			_, err := c.publicVirtualNetworks.Delete(ctx, publicv1.VirtualNetworksDeleteRequest_builder{
				Id: vn.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			response, err := c.virtualNetworks.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: vn.GetId()}.Build())
			Expect(err).ToNot(HaveOccurred())
			deleting := response.GetObject()
			ndLog("VirtualNetwork after the delete", deleting)
			Expect(deleting.GetStatus().GetState()).To(Equal(privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_DELETING))
			Expect(deleting.GetMetadata().HasDeletionTimestamp()).To(BeTrue())
			deletionTime := deleting.GetMetadata().GetDeletionTimestamp()

			By("deleting it again: OK, with the same deletion time")
			_, err = c.publicVirtualNetworks.Delete(ctx, publicv1.VirtualNetworksDeleteRequest_builder{
				Id: vn.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred(), "a second Delete of a DELETING record returns OK")
			response, err = c.virtualNetworks.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: vn.GetId()}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(proto.Equal(response.GetObject().GetMetadata().GetDeletionTimestamp(), deletionTime)).To(BeTrue(),
				"the deletion time changed: %s", ndText(response.GetObject()))
			Expect(response.GetObject().GetStatus().GetState()).To(Equal(
				privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_DELETING))

			By("trying to move the state away from DELETING through the private API")
			object := response.GetObject()
			object.GetStatus().SetState(privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY)
			_, err = c.virtualNetworks.Update(ctx, privatev1.VirtualNetworksUpdateRequest_builder{
				Object:     object,
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
			}.Build())
			ndExpectCode(err, grpccodes.InvalidArgument, "status.state cannot change after delete was accepted")

			By("writing the status message, which stays writable while DELETING")
			ndStatusUpdate(ctx, "write the DELETING message", func() error {
				response, err := c.virtualNetworks.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{
					Id: vn.GetId(),
				}.Build())
				if err != nil {
					return err
				}
				object := response.GetObject()
				object.GetStatus().SetMessage("it: delete task running")
				_, err = c.virtualNetworks.Update(ctx, privatev1.VirtualNetworksUpdateRequest_builder{
					Object:     object,
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.message"}},
					Lock:       true,
				}.Build())
				return err
			})

			By("creating a Subnet in the VirtualNetwork being deleted")
			_, err = c.publicSubnets.Create(ctx, publicv1.SubnetsCreateRequest_builder{
				Object: publicv1.Subnet_builder{
					Metadata: publicv1.Metadata_builder{Name: ndUniqueName("nd-subnet-deleting")}.Build(),
					Spec: publicv1.SubnetSpec_builder{
						VirtualNetwork: publicv1.VirtualNetworkLocalReference_builder{Id: vn.GetId()}.Build(),
						Ipv4Cidr: new(netip.PrefixFrom(netip.MustParsePrefix(
							vn.GetSpec().GetIpv4Cidr()).Addr(), 24).String()),
					}.Build(),
				}.Build(),
			}.Build())
			ndExpectCode(err, grpccodes.FailedPrecondition, "is being deleted")

			By("releasing the test finalizer; the record is removed")
			Eventually(func(g Gomega) {
				ndRemoveVirtualNetworkHoldIfPresent(ctx, c, vn.GetId())
				response, err := c.virtualNetworks.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{
					Id: vn.GetId(),
				}.Build())
				if err == nil {
					g.Expect(response.GetObject().GetStatus().GetState()).To(Equal(
						privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_DELETING),
						"the state left DELETING before the record was removed: %s", ndText(response.GetObject()))
				}
				g.Expect(grpcstatus.Code(err)).To(Equal(grpccodes.NotFound), "VirtualNetwork %s remains", vn.GetId())
			}, ndTimeout, ndPoll).Should(Succeed())
		})

	It("TC-DELETING-02 keeps a Subnet DELETING and returns OK for a second Delete", func(ctx context.Context) {
		cidr := ndVirtualNetworkCIDR()
		vn := ndReadyVirtualNetwork(ctx, c, usersGroup, cidr)
		subnet := ndReadySubnet(ctx, c, usersGroup, vn.GetId(),
			netip.PrefixFrom(netip.MustParsePrefix(cidr).Addr(), 24).String())

		ndMutateSubnetFinalizers(ctx, c, subnet.GetId(), ndAddHold)
		DeferCleanup(func(cleanupCtx context.Context) {
			ndRemoveSubnetHoldIfPresent(cleanupCtx, c, subnet.GetId())
		})

		By("deleting the READY Subnet twice through the public API")
		for attempt := 1; attempt <= 2; attempt++ {
			_, err := c.publicSubnets.Delete(ctx, publicv1.SubnetsDeleteRequest_builder{Id: subnet.GetId()}.Build())
			Expect(err).ToNot(HaveOccurred(), "Delete %d", attempt)
		}
		response, err := c.subnets.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: subnet.GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
		ndLog("Subnet after two deletes", response.GetObject())
		Expect(response.GetObject().GetStatus().GetState()).To(Equal(privatev1.SubnetState_SUBNET_STATE_DELETING))
		Expect(response.GetObject().GetStatus().HasMessage()).To(BeFalse(), "the delete clears the message")

		By("checking that the public API shows DELETING too")
		public, err := c.publicSubnets.Get(ctx, publicv1.SubnetsGetRequest_builder{Id: subnet.GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(public.GetObject().GetStatus().GetState()).To(Equal(publicv1.SubnetState_SUBNET_STATE_DELETING))

		By("checking that the DELETING Subnet still blocks its VirtualNetwork's delete")
		_, err = c.publicVirtualNetworks.Delete(ctx, publicv1.VirtualNetworksDeleteRequest_builder{
			Id: vn.GetId(),
		}.Build())
		ndExpectCode(err, grpccodes.FailedPrecondition, subnet.GetId())

		By("releasing the test finalizer; the record is removed")
		Eventually(func(g Gomega) {
			ndRemoveSubnetHoldIfPresent(ctx, c, subnet.GetId())
			_, err := c.subnets.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: subnet.GetId()}.Build())
			g.Expect(grpcstatus.Code(err)).To(Equal(grpccodes.NotFound), "Subnet %s remains", subnet.GetId())
		}, ndTimeout, ndPoll).Should(Succeed())
	})

	It("TC-STATUS-01 lets the private Update write status only and rejects a create that sets status",
		func(ctx context.Context) {
			cidr := ndVirtualNetworkCIDR()
			vn := ndReadyVirtualNetwork(ctx, c, usersGroup, cidr)
			subnet := ndReadySubnet(ctx, c, usersGroup, vn.GetId(),
				netip.PrefixFrom(netip.MustParsePrefix(cidr).Addr(), 24).String())
			group := ndReadySecurityGroup(ctx, c, usersGroup, vn.GetId(), nil, nil)

			By("changing VirtualNetwork spec, name and labels through the private API")
			for _, mutation := range []struct {
				path   string
				mutate func(*privatev1.VirtualNetwork)
			}{
				{"spec.ipv4_cidr", func(o *privatev1.VirtualNetwork) { o.GetSpec().SetIpv4Cidr("10.149.0.0/16") }},
				{"metadata.name", func(o *privatev1.VirtualNetwork) { o.GetMetadata().SetName(ndUniqueName("renamed")) }},
				{"metadata.labels", func(o *privatev1.VirtualNetwork) {
					o.GetMetadata().SetLabels(map[string]string{"it.osac.openshift.io/changed": "true"})
				}},
			} {
				object := proto.Clone(vn).(*privatev1.VirtualNetwork)
				mutation.mutate(object)
				_, err := c.virtualNetworks.Update(ctx, privatev1.VirtualNetworksUpdateRequest_builder{
					Object:     object,
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{mutation.path}},
				}.Build())
				if mutation.path == "spec.ipv4_cidr" {
					ndExpectCode(err, grpccodes.InvalidArgument, "spec is immutable", "VirtualNetwork")
				} else {
					ndExpectCode(err, grpccodes.InvalidArgument)
				}
			}

			By("changing the Subnet parent and CIDR through the private API")
			object := proto.Clone(subnet).(*privatev1.Subnet)
			object.GetSpec().SetIpv4Cidr(netip.PrefixFrom(netip.MustParsePrefix(cidr).Addr(), 25).String())
			_, err := c.subnets.Update(ctx, privatev1.SubnetsUpdateRequest_builder{
				Object:     object,
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.ipv4_cidr"}},
			}.Build())
			ndExpectCode(err, grpccodes.InvalidArgument, "spec is immutable", "Subnet")

			By("changing the SecurityGroup parent through the private API")
			other := ndReadyVirtualNetwork(ctx, c, usersGroup, ndVirtualNetworkCIDR())
			groupObject := proto.Clone(group).(*privatev1.SecurityGroup)
			groupObject.GetSpec().SetVirtualNetwork(privatev1.VirtualNetworkLocalReference_builder{
				Id: other.GetId(),
			}.Build())
			_, err = c.securityGroups.Update(ctx, privatev1.SecurityGroupsUpdateRequest_builder{
				Object:     groupObject,
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.virtual_network"}},
			}.Build())
			ndExpectCode(err, grpccodes.InvalidArgument, "spec is immutable", "SecurityGroup")

			By("checking that the stored specs didn't change")
			vnResponse, err := c.virtualNetworks.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: vn.GetId()}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(vnResponse.GetObject().GetSpec().GetIpv4Cidr()).To(Equal(cidr))
			Expect(vnResponse.GetObject().GetMetadata().GetName()).To(Equal(vn.GetMetadata().GetName()))
			subnetResponse, err := c.subnets.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: subnet.GetId()}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(subnetResponse.GetObject().GetSpec().GetIpv4Cidr()).To(Equal(subnet.GetSpec().GetIpv4Cidr()))
			groupResponse, err := c.securityGroups.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{
				Id: group.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(groupResponse.GetObject().GetSpec().GetVirtualNetwork().GetId()).To(Equal(vn.GetId()))

			By("writing the VirtualNetwork status: FAILED with a message, then READY")
			ndSetVirtualNetworkState(ctx, c, vn.GetId(), privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_FAILED)
			ndSetVirtualNetworkState(ctx, c, vn.GetId(), privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY)

			By("creating a VirtualNetwork that sets status")
			name := ndUniqueName("nd-vn-status")
			_, err = c.virtualNetworks.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
				Object: privatev1.VirtualNetwork_builder{
					Metadata: privatev1.Metadata_builder{Name: name, Tenant: usersGroup}.Build(),
					Spec:     privatev1.VirtualNetworkSpec_builder{Ipv4Cidr: new(ndVirtualNetworkCIDR())}.Build(),
					Status: privatev1.VirtualNetworkStatus_builder{
						State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY,
					}.Build(),
				}.Build(),
			}.Build())
			ndExpectCode(err, grpccodes.InvalidArgument, "status")
			expectNoVirtualNetworkNamed(ctx, c.virtualNetworks, name)

		})
})

var _ = Describe("Networking design: SecurityGroup rules", Label("networking-design"), func() {
	var (
		c  ndClients
		vn *privatev1.VirtualNetwork
	)

	BeforeEach(func(ctx context.Context) {
		c = newNDClients()
		ndReadyNetworkClass(ctx, c)
		// The specs create ICMP rules, so they state the capability instead of relying on the unset default.
		ndSetSecurityGroupICMP(ctx, c, new(true))
		vn = ndReadyVirtualNetwork(ctx, c, usersGroup, ndVirtualNetworkCIDR())
	})

	publicCreate := func(ctx context.Context, ingress, egress []*publicv1.SecurityRule) (*publicv1.SecurityGroup,
		error) {
		response, err := c.publicSecurityGroups.Create(ctx, publicv1.SecurityGroupsCreateRequest_builder{
			Object: publicv1.SecurityGroup_builder{
				Metadata: publicv1.Metadata_builder{Name: ndUniqueName("nd-sg-rules")}.Build(),
				Spec: publicv1.SecurityGroupSpec_builder{
					VirtualNetwork: publicv1.VirtualNetworkLocalReference_builder{Id: vn.GetId()}.Build(),
					Ingress:        ingress,
					Egress:         egress,
				}.Build(),
			}.Build(),
		}.Build())
		if err != nil {
			return nil, err
		}
		id := response.GetObject().GetId()
		DeferCleanup(func(cleanupCtx context.Context) {
			Expect(cleanupSecurityGroupFixture(cleanupCtx, c.securityGroups, id)).To(Succeed())
		})
		return response.GetObject(), nil
	}

	publicUpdate := func(ctx context.Context, id string, ingress, egress []*publicv1.SecurityRule) (
		*publicv1.SecurityGroup, error) {
		response, err := c.publicSecurityGroups.Update(ctx, publicv1.SecurityGroupsUpdateRequest_builder{
			Object: publicv1.SecurityGroup_builder{
				Id:   id,
				Spec: publicv1.SecurityGroupSpec_builder{Ingress: ingress, Egress: egress}.Build(),
			}.Build(),
		}.Build())
		if err != nil {
			return nil, err
		}
		return response.GetObject(), nil
	}

	// The fragment is empty: the rules are checked by the proto validation of the public API and again by the
	// server, with different messages; the design fixes only the code.
	invalidRules := []struct {
		name     string
		rule     *publicv1.SecurityRule
		fragment string
	}{
		{"tcp without ports", ndPublicRule(publicv1.Protocol_PROTOCOL_TCP, "0.0.0.0/0", 0, 0),
			""},
		{"udp with only port_from", publicv1.SecurityRule_builder{
			Protocol: publicv1.Protocol_PROTOCOL_UDP, PortFrom: new(int32(53)), Ipv4Cidr: new("0.0.0.0/0"),
		}.Build(), ""},
		{"tcp port 0", publicv1.SecurityRule_builder{
			Protocol: publicv1.Protocol_PROTOCOL_TCP, PortFrom: new(int32(0)), PortTo: new(int32(10)),
			Ipv4Cidr: new("0.0.0.0/0"),
		}.Build(), ""},
		{"tcp port above 65535", ndPublicRule(publicv1.Protocol_PROTOCOL_TCP, "0.0.0.0/0", 10, 70000), ""},
		{"tcp reversed range", ndPublicRule(publicv1.Protocol_PROTOCOL_TCP, "0.0.0.0/0", 100, 10),
			""},
		{"icmp with ports", ndPublicRule(publicv1.Protocol_PROTOCOL_ICMP, "0.0.0.0/0", 1, 1),
			""},
		{"all with ports", ndPublicRule(publicv1.Protocol_PROTOCOL_ALL, "0.0.0.0/0", 1, 1),
			""},
		{"unspecified protocol", ndPublicRule(publicv1.Protocol_PROTOCOL_UNSPECIFIED, "0.0.0.0/0", 0, 0), ""},
		{"non-canonical CIDR", ndPublicRule(publicv1.Protocol_PROTOCOL_ALL, "10.0.0.1/8", 0, 0), ""},
		{"IPv6 CIDR", ndPublicRule(publicv1.Protocol_PROTOCOL_ALL, "::/0", 0, 0), ""},
	}

	validIngress := func() []*publicv1.SecurityRule {
		return []*publicv1.SecurityRule{
			ndPublicRule(publicv1.Protocol_PROTOCOL_TCP, "0.0.0.0/0", 22, 22),
			ndPublicRule(publicv1.Protocol_PROTOCOL_UDP, "10.0.0.0/8", 53, 53),
			ndPublicRule(publicv1.Protocol_PROTOCOL_ICMP, "10.0.0.0/8", 0, 0),
			ndPublicRule(publicv1.Protocol_PROTOCOL_ALL, "192.168.0.0/16", 0, 0),
		}
	}

	It("TC-SG-01 validates each protocol's rule shape on create and on rule update", func(ctx context.Context) {
		By("creating a SecurityGroup with one valid rule of each protocol")
		group, err := publicCreate(ctx, validIngress(), []*publicv1.SecurityRule{
			ndPublicRule(publicv1.Protocol_PROTOCOL_ALL, "0.0.0.0/0", 0, 0),
		})
		Expect(err).ToNot(HaveOccurred())
		ndLog("SecurityGroup with valid rules", group)
		Expect(group.GetSpec().GetIngress()).To(HaveLen(4))
		ndSetSecurityGroupStatus(ctx, c, group.GetId(), privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY, "")

		for _, invalid := range invalidRules {
			By(fmt.Sprintf("creating a SecurityGroup with an invalid ingress rule: %s", invalid.name))
			_, err := publicCreate(ctx, []*publicv1.SecurityRule{invalid.rule}, nil)
			ndExpectCode(err, grpccodes.InvalidArgument, invalid.fragment)

			By(fmt.Sprintf("creating a SecurityGroup with an invalid egress rule: %s", invalid.name))
			_, err = publicCreate(ctx, nil, []*publicv1.SecurityRule{invalid.rule})
			ndExpectCode(err, grpccodes.InvalidArgument, invalid.fragment)

			By(fmt.Sprintf("updating the READY SecurityGroup with an invalid rule: %s", invalid.name))
			_, err = publicUpdate(ctx, group.GetId(), append(validIngress(), invalid.rule), nil)
			ndExpectCode(err, grpccodes.InvalidArgument, invalid.fragment)
		}

		By("checking that no rejected update changed the stored rules")
		response, err := c.securityGroups.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: group.GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetObject().GetSpec().GetIngress()).To(HaveLen(4), "SecurityGroup: %s",
			ndText(response.GetObject()))
		Expect(response.GetObject().GetSpec().GetEgress()).To(HaveLen(1))
	})

	It("TC-SG-ICMP-01 rejects icmp rules without the capability, on create and on rule update",
		func(ctx context.Context) {
			ndSetSecurityGroupICMP(ctx, c, new(false))
			icmp := ndPublicRule(publicv1.Protocol_PROTOCOL_ICMP, "10.0.0.0/8", 0, 0)
			all := ndPublicRule(publicv1.Protocol_PROTOCOL_ALL, "10.0.0.0/8", 0, 0)

			By("creating with an ingress icmp rule, then an egress one, through the public API")
			_, err := publicCreate(ctx, []*publicv1.SecurityRule{icmp}, nil)
			ndExpectCode(err, grpccodes.InvalidArgument, "protocol icmp is not supported", "use protocol all")
			_, err = publicCreate(ctx, nil, []*publicv1.SecurityRule{icmp})
			ndExpectCode(err, grpccodes.InvalidArgument, "protocol icmp is not supported", "use protocol all")

			By("creating with an icmp rule through the private API")
			_, err = c.securityGroups.Create(ctx, privatev1.SecurityGroupsCreateRequest_builder{
				Object: privatev1.SecurityGroup_builder{
					Metadata: privatev1.Metadata_builder{Name: ndUniqueName("nd-sg-icmp"), Tenant: usersGroup}.Build(),
					Spec: privatev1.SecurityGroupSpec_builder{
						VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: vn.GetId()}.Build(),
						Ingress: []*privatev1.SecurityRule{privatev1.SecurityRule_builder{
							Protocol: privatev1.Protocol_PROTOCOL_ICMP, Ipv4Cidr: new("10.0.0.0/8"),
						}.Build()},
					}.Build(),
				}.Build(),
			}.Build())
			ndExpectCode(err, grpccodes.InvalidArgument, "protocol icmp is not supported", "use protocol all")

			By("creating with protocol all, which is never gated")
			group, err := publicCreate(ctx, []*publicv1.SecurityRule{all}, nil)
			Expect(err).ToNot(HaveOccurred())
			ndSetSecurityGroupStatus(ctx, c, group.GetId(), privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY, "")

			By("updating the READY SecurityGroup to add an icmp rule")
			_, err = publicUpdate(ctx, group.GetId(), []*publicv1.SecurityRule{all, icmp}, nil)
			ndExpectCode(err, grpccodes.InvalidArgument, "protocol icmp is not supported", "use protocol all")
			response, err := c.securityGroups.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: group.GetId()}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetSpec().GetIngress()).To(HaveLen(1), "SecurityGroup: %s",
				ndText(response.GetObject()))
			Expect(response.GetObject().GetStatus().GetMessage()).ToNot(Equal("Updating"))
		})

	It("TC-SG-ICMP-02 keeps a stored icmp rule readable and replaceable after the capability goes away",
		func(ctx context.Context) {
			ndSetSecurityGroupICMP(ctx, c, new(true))
			icmp := ndPublicRule(publicv1.Protocol_PROTOCOL_ICMP, "10.0.0.0/8", 0, 0)
			group, err := publicCreate(ctx, []*publicv1.SecurityRule{icmp}, nil)
			Expect(err).ToNot(HaveOccurred())
			ndSetSecurityGroupStatus(ctx, c, group.GetId(), privatev1.SecurityGroupState_SECURITY_GROUP_STATE_FAILED,
				"icmp is not supported")
			ndSetSecurityGroupICMP(ctx, c, new(false))

			By("reading the stored icmp rule")
			response, err := c.securityGroups.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: group.GetId()}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetSpec().GetIngress()[0].GetProtocol()).
				To(Equal(privatev1.Protocol_PROTOCOL_ICMP))

			By("rejecting an update that keeps the icmp rule")
			_, err = publicUpdate(ctx, group.GetId(), []*publicv1.SecurityRule{icmp}, nil)
			ndExpectCode(err, grpccodes.InvalidArgument, "protocol icmp is not supported")
			response, err = c.securityGroups.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: group.GetId()}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetStatus().GetState()).
				To(Equal(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_FAILED))

			By("accepting an update that replaces it with protocol all")
			_, err = publicUpdate(ctx, group.GetId(), []*publicv1.SecurityRule{
				ndPublicRule(publicv1.Protocol_PROTOCOL_ALL, "10.0.0.0/8", 0, 0)}, nil)
			Expect(err).ToNot(HaveOccurred())
		})

	It("TC-SG-ICMP-03 accepts icmp with the capability", func(ctx context.Context) {
		ndSetSecurityGroupICMP(ctx, c, new(true))
		icmp := ndPublicRule(publicv1.Protocol_PROTOCOL_ICMP, "10.0.0.0/8", 0, 0)
		group, err := publicCreate(ctx, []*publicv1.SecurityRule{icmp}, nil)
		Expect(err).ToNot(HaveOccurred())
		ndSetSecurityGroupStatus(ctx, c, group.GetId(), privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY, "")
		_, err = publicUpdate(ctx, group.GetId(), nil, []*publicv1.SecurityRule{icmp})
		Expect(err).ToNot(HaveOccurred())
	})

	It("TC-SG-ICMP-04 accepts icmp when no capability is recorded and provider networking is disabled",
		func(ctx context.Context) {
			ndSkipIfProviderNetworkingEnabled(ctx, "storing icmp rules without a recorded capability")
			ndSetSecurityGroupICMP(ctx, c, nil)
			_, err := publicCreate(ctx, []*publicv1.SecurityRule{
				ndPublicRule(publicv1.Protocol_PROTOCOL_ICMP, "10.0.0.0/8", 0, 0)}, nil)
			Expect(err).ToNot(HaveOccurred())
		})

	It("TC-SG-02 replaces only the rules on a public Update and copies them to the hub CR", func(ctx context.Context) {
		disabled := !ndProviderNetworkingEnabled(ctx)
		group, err := publicCreate(ctx, validIngress(), nil)
		Expect(err).ToNot(HaveOccurred())
		ndSetSecurityGroupStatus(ctx, c, group.GetId(), privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY, "")

		By("updating the rules and, in the same request, the name and the parent VirtualNetwork")
		other := ndReadyVirtualNetwork(ctx, c, usersGroup, ndVirtualNetworkCIDR())
		updated, err := c.publicSecurityGroups.Update(ctx, publicv1.SecurityGroupsUpdateRequest_builder{
			Object: publicv1.SecurityGroup_builder{
				Id:       group.GetId(),
				Metadata: publicv1.Metadata_builder{Name: ndUniqueName("nd-sg-renamed")}.Build(),
				Spec: publicv1.SecurityGroupSpec_builder{
					VirtualNetwork: publicv1.VirtualNetworkLocalReference_builder{Id: other.GetId()}.Build(),
					Ingress: []*publicv1.SecurityRule{
						ndPublicRule(publicv1.Protocol_PROTOCOL_TCP, "0.0.0.0/0", 443, 443),
					},
					Egress: []*publicv1.SecurityRule{
						ndPublicRule(publicv1.Protocol_PROTOCOL_TCP, "10.0.0.0/8", 5432, 5432),
						ndPublicRule(publicv1.Protocol_PROTOCOL_ICMP, "10.0.0.0/8", 0, 0),
					},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		ndLog("Updated SecurityGroup", updated)

		By("checking that only the rules changed")
		response, err := c.securityGroups.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: group.GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
		stored := response.GetObject()
		ndLog("Stored SecurityGroup", stored)
		Expect(stored.GetMetadata().GetName()).To(Equal(group.GetMetadata().GetName()), "the name changed")
		Expect(stored.GetSpec().GetVirtualNetwork().GetId()).To(Equal(vn.GetId()), "the parent changed")
		Expect(stored.GetSpec().GetIngress()).To(HaveLen(1))
		Expect(stored.GetSpec().GetIngress()[0].GetPortFrom()).To(Equal(int32(443)))
		Expect(stored.GetSpec().GetEgress()).To(HaveLen(2))

		By("checking the status the Update wrote in its own transaction")
		Expect(stored.GetStatus().GetState()).To(Equal(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY))
		if disabled {
			Expect(stored.GetStatus().GetMessage()).To(BeEmpty(),
				"with provider networking disabled the group stays READY without Updating: %s", ndText(stored))
		} else {
			Expect(stored.GetStatus().GetMessage()).To(Equal("Updating"),
				"a READY group shows Updating from the Update's own transaction: %s", ndText(stored))
		}

		By("waiting for the fulfillment controller to copy the rules to the SecurityGroup CR")
		Eventually(func(g Gomega) {
			list := &osacv1alpha1.SecurityGroupList{}
			g.Expect(tool.KubeClient().List(ctx, list, crclient.InNamespace(hubNamespace),
				crclient.MatchingLabels{labels.SecurityGroupUuid: group.GetId()})).To(Succeed())
			g.Expect(list.Items).To(HaveLen(1), "SecurityGroup CR of %s", group.GetId())
			spec := list.Items[0].Spec
			fmt.Fprintf(GinkgoWriter, "SecurityGroup CR spec: %+v\n", spec)
			g.Expect(spec.IngressRules).To(HaveLen(1))
			g.Expect(spec.EgressRules).To(HaveLen(2))
			g.Expect(spec.VirtualNetwork).To(Equal(vn.GetId()))
		}, ndTimeout, ndPoll).Should(Succeed())
	})

	It("TC-SG-03 accepts rule updates only while READY or FAILED", func(ctx context.Context) {
		disabled := !ndProviderNetworkingEnabled(ctx)
		rules := []*publicv1.SecurityRule{ndPublicRule(publicv1.Protocol_PROTOCOL_TCP, "0.0.0.0/0", 8080, 8080)}

		By("updating a PENDING SecurityGroup")
		group, err := publicCreate(ctx, validIngress(), nil)
		Expect(err).ToNot(HaveOccurred())
		_, err = publicUpdate(ctx, group.GetId(), rules, nil)
		ndExpectCode(err, grpccodes.FailedPrecondition, group.GetId(),
			"is being created or updated; retry when it is READY or FAILED")

		By("updating a READY SecurityGroup whose previous update still runs (message Updating)")
		ndSetSecurityGroupStatus(ctx, c, group.GetId(), privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY,
			"Updating")
		_, err = publicUpdate(ctx, group.GetId(), rules, nil)
		ndExpectCode(err, grpccodes.FailedPrecondition, "is being created or updated")

		By("updating a FAILED SecurityGroup")
		ndSetSecurityGroupStatus(ctx, c, group.GetId(), privatev1.SecurityGroupState_SECURITY_GROUP_STATE_FAILED,
			"it: manager rejected the rules")
		_, err = publicUpdate(ctx, group.GetId(), rules, nil)
		Expect(err).ToNot(HaveOccurred(), "a FAILED group accepts a rule update")
		response, err := c.securityGroups.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: group.GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
		ndLog("FAILED SecurityGroup after the rule update", response.GetObject())
		Expect(response.GetObject().GetSpec().GetIngress()).To(HaveLen(1))
		if disabled {
			Expect(response.GetObject().GetStatus().GetState()).To(Equal(
				privatev1.SecurityGroupState_SECURITY_GROUP_STATE_FAILED),
				"with provider networking disabled the update only stores the rules")
		} else {
			Expect(response.GetObject().GetStatus().GetState()).To(Equal(
				privatev1.SecurityGroupState_SECURITY_GROUP_STATE_PENDING), "a FAILED group becomes PENDING")
		}

		By("updating a SecurityGroup whose delete was accepted")
		ndSetSecurityGroupStatus(ctx, c, group.GetId(), privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY, "")
		ndMutateSecurityGroupFinalizers(ctx, c, group.GetId(), ndAddHold)
		DeferCleanup(func(cleanupCtx context.Context) {
			ndRemoveSecurityGroupHoldIfPresent(cleanupCtx, c, group.GetId())
		})
		_, err = c.publicSecurityGroups.Delete(ctx, publicv1.SecurityGroupsDeleteRequest_builder{
			Id: group.GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		_, err = publicUpdate(ctx, group.GetId(), validIngress(), nil)
		ndExpectCode(err, grpccodes.FailedPrecondition, "is being deleted")
		Eventually(func(g Gomega) {
			ndRemoveSecurityGroupHoldIfPresent(ctx, c, group.GetId())
			_, err := c.securityGroups.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: group.GetId()}.Build())
			g.Expect(grpcstatus.Code(err)).To(Equal(grpccodes.NotFound), "SecurityGroup %s remains", group.GetId())
		}, ndTimeout, ndPoll).Should(Succeed())

		By("updating without an ID or without a spec")
		_, err = c.publicSecurityGroups.Update(ctx, publicv1.SecurityGroupsUpdateRequest_builder{
			Object: publicv1.SecurityGroup_builder{Spec: publicv1.SecurityGroupSpec_builder{}.Build()}.Build(),
		}.Build())
		ndExpectCode(err, grpccodes.InvalidArgument)
		_, err = c.publicSecurityGroups.Update(ctx, publicv1.SecurityGroupsUpdateRequest_builder{
			Object: publicv1.SecurityGroup_builder{Id: group.GetId()}.Build(),
		}.Build())
		ndExpectCode(err, grpccodes.InvalidArgument)
	})
})

var _ = Describe("Networking design: provider networking disabled", Label("networking-design"), func() {
	var c ndClients

	BeforeEach(func(ctx context.Context) {
		c = newNDClients()
		ndReadyNetworkClass(ctx, c)
	})

	It("TC-CAP-01 reports networking_provisioning_enabled as deployed, only in the private capabilities",
		func(ctx context.Context) {
			By("reading the --networking-provisioning-enabled flag of the deployed gRPC server")
			deployed := ""
			deployments := &appsv1.DeploymentList{}
			Expect(tool.KubeClient().List(ctx, deployments, crclient.InNamespace(hubNamespace))).To(Succeed())
			for _, deployment := range deployments.Items {
				for _, container := range deployment.Spec.Template.Spec.Containers {
					for _, arg := range append(slices.Clone(container.Command), container.Args...) {
						if value, ok := strings.CutPrefix(arg, "--networking-provisioning-enabled="); ok {
							fmt.Fprintf(GinkgoWriter, "Deployment %s container %s: %s\n", deployment.Name,
								container.Name, arg)
							deployed = value
						}
					}
				}
			}
			Expect(deployed).ToNot(BeEmpty(), "no fulfillment container sets --networking-provisioning-enabled")

			By("reading the private capabilities")
			response, err := c.capabilities.Get(ctx, privatev1.CapabilitiesGetRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			ndLog("Private capabilities", response)
			Expect(fmt.Sprint(response.GetNetworkingProvisioningEnabled())).To(Equal(deployed))

			By("checking that the public capabilities don't carry the setting")
			publicResponse, err := publicv1.NewCapabilitiesClient(tool.ExternalView().AnonymousConn()).Get(ctx,
				publicv1.CapabilitiesGetRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(publicResponse.ProtoReflect().Descriptor().Fields().ByName("networking_provisioning_enabled")).To(
				BeNil(), "networking_provisioning_enabled is a private field")
		})

	It("TC-DISABLED-01 rejects an ExternalIP create with FAILED_PRECONDITION and stores nothing",
		func(ctx context.Context) {
			ndSkipIfProviderNetworkingEnabled(ctx, "TC-DISABLED-01")
			pool := ndCreatePool(ctx, c, ndPoolBase()+"/28")
			ndSetPoolState(ctx, c, pool.GetId(), privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY)

			By("creating an ExternalIP from the READY pool through the public API")
			name := ndUniqueName("nd-eip")
			_, err := c.publicExternalIPs.Create(ctx, publicv1.ExternalIPsCreateRequest_builder{
				Object: publicv1.ExternalIP_builder{
					Metadata: publicv1.Metadata_builder{Name: name}.Build(),
					Spec: publicv1.ExternalIPSpec_builder{
						Pool: publicv1.ExternalIPPoolReference_builder{Id: pool.GetId()}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			ndExpectCode(err, grpccodes.FailedPrecondition, "provider networking is disabled")

			By("creating it through the private API as the administrator")
			_, err = c.externalIPs.Create(ctx, privatev1.ExternalIPsCreateRequest_builder{
				Object: privatev1.ExternalIP_builder{
					Metadata: privatev1.Metadata_builder{Name: name, Tenant: usersGroup}.Build(),
					Spec: privatev1.ExternalIPSpec_builder{
						Pool: privatev1.ExternalIPPoolReference_builder{Id: pool.GetId()}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			ndExpectCode(err, grpccodes.FailedPrecondition, "provider networking is disabled")

			By("checking that no ExternalIP was stored and the pool reserved nothing")
			filter := fmt.Sprintf("this.metadata.name == %q", name)
			list, err := c.externalIPs.List(ctx, privatev1.ExternalIPsListRequest_builder{Filter: &filter}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(list.GetItems()).To(BeEmpty())
			response, err := c.pools.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{Id: pool.GetId()}.Build())
			Expect(err).ToNot(HaveOccurred())
			ndLog("Pool after the rejected creates", response.GetObject())
			Expect(response.GetObject().GetStatus().GetAllocated()).To(BeZero())
			Expect(response.GetObject().GetStatus().GetAvailable()).To(Equal(int64(14)))
		})

	It("TC-DISABLED-02 rejects ExternalIPAttachment requests with unresolved references",
		func(ctx context.Context) {
			ndSkipIfProviderNetworkingEnabled(ctx, "TC-DISABLED-02")
			for _, target := range []struct {
				name  string
				build func(*publicv1.ExternalIPAttachmentSpec_builder)
			}{
				{"compute instance", func(b *publicv1.ExternalIPAttachmentSpec_builder) {
					b.ComputeInstance = publicv1.ComputeInstanceLocalReference_builder{Id: "nd-missing-ci"}.Build()
				}},
				{"cluster API endpoint", func(b *publicv1.ExternalIPAttachmentSpec_builder) {
					b.Cluster = publicv1.ClusterLocalReference_builder{Id: "nd-missing-cluster"}.Build()
					b.TargetEndpoint = publicv1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API
				}},
				{"bare-metal instance", func(b *publicv1.ExternalIPAttachmentSpec_builder) {
					b.BaremetalInstance = publicv1.BareMetalInstanceLocalReference_builder{Id: "nd-missing-bmi"}.Build()
				}},
			} {
				By(fmt.Sprintf("creating an ExternalIPAttachment for a %s", target.name))
				spec := publicv1.ExternalIPAttachmentSpec_builder{
					ExternalIp: publicv1.ExternalIPLocalReference_builder{Id: "nd-missing-eip"}.Build(),
				}
				target.build(&spec)
				_, err := c.publicAttachments.Create(ctx, publicv1.ExternalIPAttachmentsCreateRequest_builder{
					Object: publicv1.ExternalIPAttachment_builder{
						Metadata: publicv1.Metadata_builder{Name: ndUniqueName("nd-eipa")}.Build(),
						Spec:     spec.Build(),
					}.Build(),
				}.Build())
				ndExpectCode(err, grpccodes.InvalidArgument, "reference validation failed:")
			}
		})

	It("TC-NAT-01 rejects a tenant NATGateway on its default VirtualNetwork and one without an ALLOCATED ExternalIP",
		func(ctx context.Context) {
			_, tenantName := ndCreateTenant(ctx, c)
			var defaultVN *privatev1.VirtualNetwork
			By("waiting for the tenant's default VirtualNetwork and reporting it READY")
			Eventually(func(g Gomega) {
				filter := ndDefaultFilter(tenantName, "default")
				list, err := c.virtualNetworks.List(ctx, privatev1.VirtualNetworksListRequest_builder{
					Filter: &filter,
				}.Build())
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(list.GetItems()).To(HaveLen(1), "tenant %s has no default VirtualNetwork yet", tenantName)
				defaultVN = list.GetItems()[0]
			}, ndTimeout, ndPoll).Should(Succeed())
			ndWaitVirtualNetworkPlaced(ctx, c, defaultVN.GetId())
			ndSetVirtualNetworkState(ctx, c, defaultVN.GetId(),
				privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY)

			By("creating a NATGateway on the default VirtualNetwork as the administrator, not as the system")
			_, err := c.natGateways.Create(ctx, privatev1.NATGatewaysCreateRequest_builder{
				Object: privatev1.NATGateway_builder{
					Metadata: privatev1.Metadata_builder{Name: ndUniqueName("nd-nat"), Tenant: tenantName}.Build(),
					Spec: privatev1.NATGatewaySpec_builder{
						VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: defaultVN.GetId()}.Build(),
						ExternalIp:     privatev1.ExternalIPLocalReference_builder{Id: "nd-missing-eip"}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			ndExpectCode(err, grpccodes.FailedPrecondition, "is a tenant default; only the system creates its NATGateway")

			By("creating a NATGateway on a tenant VirtualNetwork with an ExternalIP that doesn't exist")
			vn := ndReadyVirtualNetwork(ctx, c, usersGroup, ndVirtualNetworkCIDR())
			_, err = c.publicNATGateways.Create(ctx, publicv1.NATGatewaysCreateRequest_builder{
				Object: publicv1.NATGateway_builder{
					Metadata: publicv1.Metadata_builder{Name: ndUniqueName("nd-nat")}.Build(),
					Spec: publicv1.NATGatewaySpec_builder{
						VirtualNetwork: publicv1.VirtualNetworkLocalReference_builder{Id: vn.GetId()}.Build(),
						ExternalIp:     publicv1.ExternalIPLocalReference_builder{Id: "nd-missing-eip"}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			ndExpectCode(err, grpccodes.InvalidArgument, "nd-missing-eip", "does not exist")

			By("checking that the tenant default reserves no ExternalIP and no NATGateway")
			if !ndProviderNetworkingEnabled(ctx) {
				filter := fmt.Sprintf("this.metadata.tenant == %q", tenantName)
				ips, err := c.externalIPs.List(ctx, privatev1.ExternalIPsListRequest_builder{Filter: &filter}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(ips.GetItems()).To(BeEmpty(), "no default ExternalIP with provider networking disabled")
				gateways, err := c.natGateways.List(ctx, privatev1.NATGatewaysListRequest_builder{
					Filter: &filter,
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(gateways.GetItems()).To(BeEmpty(), "no default NATGateway with provider networking disabled")
			}
		})
})

var _ = Describe("Networking design: default networking", Label("networking-design"), func() {
	var c ndClients

	BeforeEach(func(ctx context.Context) {
		c = newNDClients()
		ndReadyNetworkClass(ctx, c)
	})

	It("TC-DN-01 creates the defaults in order and reports DefaultNetworkingReady with provider networking disabled",
		func(ctx context.Context) {
			ndSkipIfProviderNetworkingEnabled(ctx, "TC-DN-01")
			nc := ndSingletonNetworkClass(ctx, c)
			tenantID, tenantName := ndCreateTenant(ctx, c)

			By("waiting for the default VirtualNetwork, created first")
			var vn *privatev1.VirtualNetwork
			Eventually(func(g Gomega) {
				filter := ndDefaultFilter(tenantName, "default")
				list, err := c.virtualNetworks.List(ctx, privatev1.VirtualNetworksListRequest_builder{
					Filter: &filter,
				}.Build())
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(list.GetItems()).To(HaveLen(1), "tenant %s has no default VirtualNetwork yet", tenantName)
				vn = list.GetItems()[0]
			}, ndTimeout, ndPoll).Should(Succeed())
			ndLog("Default VirtualNetwork", vn)
			Expect(vn.GetMetadata().GetCreator()).To(Equal("system"))
			Expect(vn.GetSpec().GetIpv4Cidr()).To(Equal(nc.GetSpec().GetDefaults().GetVirtualNetworkIpv4Cidr()))
			ndWaitVirtualNetworkPlaced(ctx, c, vn.GetId())

			By("checking DefaultNetworkingReady=False/ResourcesPending while the VirtualNetwork is PENDING")
			ndExpectDefaultNetworkingCondition(ctx, c, tenantID, privatev1.ConditionStatus_CONDITION_STATUS_FALSE,
				"ResourcesPending", "pending")

			By("checking that no Subnet or SecurityGroup is created before the VirtualNetwork is READY")
			tenantFilter := fmt.Sprintf("this.metadata.tenant == %q", tenantName)
			Consistently(func(g Gomega) {
				subnets, err := c.subnets.List(ctx, privatev1.SubnetsListRequest_builder{Filter: &tenantFilter}.Build())
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(subnets.GetItems()).To(BeEmpty(), "a default Subnet appeared before its VirtualNetwork "+
					"was READY")
				groups, err := c.securityGroups.List(ctx, privatev1.SecurityGroupsListRequest_builder{
					Filter: &tenantFilter,
				}.Build())
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(groups.GetItems()).To(BeEmpty(), "a default SecurityGroup appeared before its "+
					"VirtualNetwork was READY")
			}, 5*time.Second, time.Second).Should(Succeed())

			By("reporting the default VirtualNetwork READY")
			ndSetVirtualNetworkState(ctx, c, vn.GetId(), privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY)

			By("waiting for the default Subnet and SecurityGroup")
			var subnet *privatev1.Subnet
			var group *privatev1.SecurityGroup
			Eventually(func(g Gomega) {
				filter := ndDefaultFilter(tenantName, "default-ipv4")
				subnets, err := c.subnets.List(ctx, privatev1.SubnetsListRequest_builder{Filter: &filter}.Build())
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(subnets.GetItems()).To(HaveLen(1), "no default Subnet yet")
				subnet = subnets.GetItems()[0]
				filter = ndDefaultFilter(tenantName, "default")
				groups, err := c.securityGroups.List(ctx, privatev1.SecurityGroupsListRequest_builder{
					Filter: &filter,
				}.Build())
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(groups.GetItems()).To(HaveLen(1), "no default SecurityGroup yet")
				group = groups.GetItems()[0]
			}, ndTimeout, ndPoll).Should(Succeed())
			ndLog("Default Subnet", subnet)
			ndLog("Default SecurityGroup", group)
			Expect(subnet.GetSpec().GetIpv4Cidr()).To(Equal(nc.GetSpec().GetDefaults().GetSubnetIpv4Cidr()))
			Expect(subnet.GetSpec().GetVirtualNetwork().GetId()).To(Equal(vn.GetId()))
			Expect(group.GetSpec().GetVirtualNetwork().GetId()).To(Equal(vn.GetId()))
			Expect(group.GetSpec().GetEgress()).To(HaveLen(len(nc.GetSpec().GetDefaults().GetEgressRules())))
			Expect(group.GetSpec().GetIngress()).To(HaveLen(len(nc.GetSpec().GetDefaults().GetIngressRules())))

			By("checking that no default ExternalIP or NATGateway exists with provider networking disabled")
			ips, err := c.externalIPs.List(ctx, privatev1.ExternalIPsListRequest_builder{Filter: &tenantFilter}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(ips.GetItems()).To(BeEmpty())
			gateways, err := c.natGateways.List(ctx, privatev1.NATGatewaysListRequest_builder{
				Filter: &tenantFilter,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(gateways.GetItems()).To(BeEmpty())

			By("reporting the default Subnet and SecurityGroup READY")
			ndSetSubnetState(ctx, c, subnet.GetId(), privatev1.SubnetState_SUBNET_STATE_READY)
			ndSetSecurityGroupStatus(ctx, c, group.GetId(), privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY, "")

			By("checking DefaultNetworkingReady=True/ProvisioningDisabled")
			ndExpectDefaultNetworkingCondition(ctx, c, tenantID, privatev1.ConditionStatus_CONDITION_STATUS_TRUE,
				"ProvisioningDisabled", "provider networking is disabled")

			By("reporting the default SecurityGroup FAILED")
			ndSetSecurityGroupStatus(ctx, c, group.GetId(), privatev1.SecurityGroupState_SECURITY_GROUP_STATE_FAILED,
				"it: manager rejected the default rules")
			ndExpectDefaultNetworkingCondition(ctx, c, tenantID, privatev1.ConditionStatus_CONDITION_STATUS_FALSE,
				"ResourceFailed", "Default networking resources failed")

			By("reporting the default SecurityGroup READY again; the condition recovers without an onboarding retry")
			ndSetSecurityGroupStatus(ctx, c, group.GetId(), privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY, "")
			ndExpectDefaultNetworkingCondition(ctx, c, tenantID, privatev1.ConditionStatus_CONDITION_STATUS_TRUE,
				"ProvisioningDisabled")
		})

	It("TC-DN-02 rejects a second Subnet while the default Subnet is PENDING",
		func(ctx context.Context) {
			_, tenantName := ndCreateTenant(ctx, c)
			nc := ndSingletonNetworkClass(ctx, c)

			var vn *privatev1.VirtualNetwork
			Eventually(func(g Gomega) {
				filter := ndDefaultFilter(tenantName, "default")
				list, err := c.virtualNetworks.List(ctx, privatev1.VirtualNetworksListRequest_builder{
					Filter: &filter,
				}.Build())
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(list.GetItems()).To(HaveLen(1), "tenant %s has no default VirtualNetwork yet", tenantName)
				vn = list.GetItems()[0]
			}, ndTimeout, ndPoll).Should(Succeed())
			ndWaitVirtualNetworkPlaced(ctx, c, vn.GetId())
			ndSetVirtualNetworkState(ctx, c, vn.GetId(), privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY)

			var defaultSubnet *privatev1.Subnet
			Eventually(func(g Gomega) {
				filter := ndDefaultFilter(tenantName, "default-ipv4")
				subnets, err := c.subnets.List(ctx, privatev1.SubnetsListRequest_builder{Filter: &filter}.Build())
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(subnets.GetItems()).To(HaveLen(1), "no default Subnet yet")
				defaultSubnet = subnets.GetItems()[0]
			}, ndTimeout, ndPoll).Should(Succeed())

			// A /24 of the default VirtualNetwork outside the default Subnet.
			defaultVNPrefix := netip.MustParsePrefix(nc.GetSpec().GetDefaults().GetVirtualNetworkIpv4Cidr())
			defaultSubnetPrefix := netip.MustParsePrefix(defaultSubnet.GetSpec().GetIpv4Cidr())
			secondAddr := defaultVNPrefix.Addr().As4()
			secondAddr[2] = 0xf0
			second := netip.PrefixFrom(netip.AddrFrom4(secondAddr), 24)
			Expect(second.Overlaps(defaultSubnetPrefix)).To(BeFalse(), "test CIDR %s overlaps %s", second,
				defaultSubnetPrefix)

			By("creating a second Subnet while the default Subnet is PENDING")
			_, err := c.subnets.Create(ctx, privatev1.SubnetsCreateRequest_builder{
				Object: privatev1.Subnet_builder{
					Metadata: privatev1.Metadata_builder{Name: ndUniqueName("nd-second"), Tenant: tenantName}.Build(),
					Spec: privatev1.SubnetSpec_builder{
						VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: vn.GetId()}.Build(),
						Ipv4Cidr:       new(second.String()),
					}.Build(),
				}.Build(),
			}.Build())
			ndExpectCode(err, grpccodes.FailedPrecondition, "the default Subnet of this VirtualNetwork is not READY yet")

		})
})
