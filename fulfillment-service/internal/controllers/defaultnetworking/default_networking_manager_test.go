/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package defaultnetworking

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func TestDefaultNetworkingManager(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Default networking manager")
}

type fakeNetworkClasses struct {
	items []*privatev1.NetworkClass
	err   error
}

func (f *fakeNetworkClasses) List(context.Context, *privatev1.NetworkClassesListRequest,
	...grpc.CallOption) (*privatev1.NetworkClassesListResponse, error) {
	return privatev1.NetworkClassesListResponse_builder{Items: f.items}.Build(), f.err
}

type fakeVirtualNetworks struct {
	items   []*privatev1.VirtualNetwork
	creates []*privatev1.VirtualNetwork
	deletes []string
}

func (f *fakeVirtualNetworks) List(context.Context, *privatev1.VirtualNetworksListRequest,
	...grpc.CallOption) (*privatev1.VirtualNetworksListResponse, error) {
	return privatev1.VirtualNetworksListResponse_builder{Items: f.items}.Build(), nil
}

func (f *fakeVirtualNetworks) Create(_ context.Context, request *privatev1.VirtualNetworksCreateRequest,
	options ...grpc.CallOption) (*privatev1.VirtualNetworksCreateResponse, error) {
	object := request.GetObject()
	object.SetId("vn-default")
	f.creates = append(f.creates, object)
	f.items = append(f.items, object)
	return privatev1.VirtualNetworksCreateResponse_builder{Object: object}.Build(), nil
}

func (f *fakeVirtualNetworks) Delete(_ context.Context, request *privatev1.VirtualNetworksDeleteRequest,
	options ...grpc.CallOption) (*privatev1.VirtualNetworksDeleteResponse, error) {
	f.deletes = append(f.deletes, request.GetId())
	f.items = nil
	return privatev1.VirtualNetworksDeleteResponse_builder{}.Build(), nil
}

type fakeSubnets struct {
	items         []*privatev1.Subnet
	creates       []*privatev1.Subnet
	alreadyExists bool
}

func (f *fakeSubnets) List(_ context.Context, request *privatev1.SubnetsListRequest,
	_ ...grpc.CallOption) (*privatev1.SubnetsListResponse, error) {
	var items []*privatev1.Subnet
	for _, item := range f.items {
		if request.GetFilter() == resourceFilter(item.GetMetadata().GetTenant(), item.GetMetadata().GetName()) ||
			request.GetFilter() == childResourceFilter(item.GetMetadata().GetTenant(), item.GetSpec().GetVirtualNetwork().GetId()) {
			items = append(items, item)
		}
	}
	return privatev1.SubnetsListResponse_builder{Items: items}.Build(), nil
}

func (f *fakeSubnets) Create(_ context.Context, request *privatev1.SubnetsCreateRequest,
	options ...grpc.CallOption) (*privatev1.SubnetsCreateResponse, error) {
	object := request.GetObject()
	object.SetId("subnet-" + object.GetMetadata().GetName())
	f.creates = append(f.creates, object)
	f.items = append(f.items, object)
	if f.alreadyExists {
		object.SetStatus(privatev1.SubnetStatus_builder{State: privatev1.SubnetState_SUBNET_STATE_READY}.Build())
		return nil, status.Error(codes.AlreadyExists, "concurrent creation")
	}
	return privatev1.SubnetsCreateResponse_builder{Object: object}.Build(), nil
}

func (f *fakeSubnets) Delete(context.Context, *privatev1.SubnetsDeleteRequest,
	...grpc.CallOption) (*privatev1.SubnetsDeleteResponse, error) {
	return privatev1.SubnetsDeleteResponse_builder{}.Build(), nil
}

type fakeSecurityGroups struct {
	items         []*privatev1.SecurityGroup
	creates       []*privatev1.SecurityGroup
	alreadyExists bool
}

func (f *fakeSecurityGroups) List(context.Context, *privatev1.SecurityGroupsListRequest,
	...grpc.CallOption) (*privatev1.SecurityGroupsListResponse, error) {
	return privatev1.SecurityGroupsListResponse_builder{Items: f.items}.Build(), nil
}

func (f *fakeSecurityGroups) Create(_ context.Context, request *privatev1.SecurityGroupsCreateRequest,
	options ...grpc.CallOption) (*privatev1.SecurityGroupsCreateResponse, error) {
	object := request.GetObject()
	object.SetId("sg-default")
	f.creates = append(f.creates, object)
	f.items = append(f.items, object)
	if f.alreadyExists {
		object.SetStatus(privatev1.SecurityGroupStatus_builder{State: privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY}.Build())
		return nil, status.Error(codes.AlreadyExists, "concurrent creation")
	}
	return privatev1.SecurityGroupsCreateResponse_builder{Object: object}.Build(), nil
}

func (f *fakeSecurityGroups) Delete(context.Context, *privatev1.SecurityGroupsDeleteRequest,
	...grpc.CallOption) (*privatev1.SecurityGroupsDeleteResponse, error) {
	return privatev1.SecurityGroupsDeleteResponse_builder{}.Build(), nil
}

type fakeNATGateways struct {
	items   []*privatev1.NATGateway
	creates []*privatev1.NATGateway
}

type fakeExternalIPs struct {
	items         []*privatev1.ExternalIP
	deletes       []string
	alreadyExists bool
}

type fakeExternalIPPools struct {
	items []*privatev1.ExternalIPPool
}

func (f *fakeExternalIPPools) List(context.Context, *privatev1.ExternalIPPoolsListRequest,
	...grpc.CallOption) (*privatev1.ExternalIPPoolsListResponse, error) {
	return privatev1.ExternalIPPoolsListResponse_builder{Items: f.items}.Build(), nil
}

func (f *fakeExternalIPs) List(context.Context, *privatev1.ExternalIPsListRequest,
	...grpc.CallOption) (*privatev1.ExternalIPsListResponse, error) {
	return privatev1.ExternalIPsListResponse_builder{Items: f.items}.Build(), nil
}

func (f *fakeExternalIPs) Create(_ context.Context, request *privatev1.ExternalIPsCreateRequest,
	options ...grpc.CallOption) (*privatev1.ExternalIPsCreateResponse, error) {
	object := request.GetObject()
	object.SetId("eip-default")
	f.items = append(f.items, object)
	if f.alreadyExists {
		return nil, status.Error(codes.AlreadyExists, "concurrent creation")
	}
	return privatev1.ExternalIPsCreateResponse_builder{Object: object}.Build(), nil
}

func ensureManager(m Manager, ctx context.Context, tenantName string) error {
	_, err := m.Ensure(ctx, tenantName)
	return err
}

func (f *fakeExternalIPs) Delete(_ context.Context, request *privatev1.ExternalIPsDeleteRequest,
	options ...grpc.CallOption) (*privatev1.ExternalIPsDeleteResponse, error) {
	f.deletes = append(f.deletes, request.GetId())
	f.items = nil
	return privatev1.ExternalIPsDeleteResponse_builder{}.Build(), nil
}

func (f *fakeNATGateways) List(context.Context, *privatev1.NATGatewaysListRequest,
	...grpc.CallOption) (*privatev1.NATGatewaysListResponse, error) {
	return privatev1.NATGatewaysListResponse_builder{Items: f.items}.Build(), nil
}

func (f *fakeNATGateways) Create(_ context.Context, request *privatev1.NATGatewaysCreateRequest,
	options ...grpc.CallOption) (*privatev1.NATGatewaysCreateResponse, error) {
	object := request.GetObject()
	object.SetId("nat-default")
	f.creates = append(f.creates, object)
	f.items = append(f.items, object)
	return privatev1.NATGatewaysCreateResponse_builder{Object: object}.Build(), nil
}

func (f *fakeNATGateways) Delete(context.Context, *privatev1.NATGatewaysDeleteRequest,
	...grpc.CallOption) (*privatev1.NATGatewaysDeleteResponse, error) {
	return privatev1.NATGatewaysDeleteResponse_builder{}.Build(), nil
}

var _ = Describe("default networking manager", func() {
	var (
		ctx context.Context
		m   *manager
	)

	BeforeEach(func() {
		ctx = context.Background()
		m = &manager{
			logger:          slog.Default(),
			networkClasses:  &fakeNetworkClasses{},
			virtualNetworks: &fakeVirtualNetworks{},
			subnets:         &fakeSubnets{},
			securityGroups:  &fakeSecurityGroups{},
			natGateways:     &fakeNATGateways{},
			externalIPs:     &fakeExternalIPs{},
		}
	})

	It("returns configured defaults for tenant readiness evaluation", func() {
		defaults := privatev1.NetworkDefaults_builder{
			SubnetIpv4Cidr: "10.0.1.0/24", EnableNatGateway: true,
		}.Build()
		m.networkClasses = &fakeNetworkClasses{items: []*privatev1.NetworkClass{
			privatev1.NetworkClass_builder{
				Id:   "nc-1",
				Spec: privatev1.NetworkClassSpec_builder{Defaults: defaults}.Build(),
			}.Build(),
		}}

		got, err := m.Ensure(ctx, "tenant-a")
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal(defaults))
	})

	It("waits for NetworkClass readiness before creating default resources", func() {
		m.networkClasses = &fakeNetworkClasses{items: []*privatev1.NetworkClass{
			privatev1.NetworkClass_builder{
				Id: "nc-1",
				Spec: privatev1.NetworkClassSpec_builder{
					Defaults: privatev1.NetworkDefaults_builder{
						VirtualNetworkIpv4Cidr: "10.0.0.0/16",
					}.Build(),
				}.Build(),
				Status: privatev1.NetworkClassStatus_builder{
					State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_PENDING,
				}.Build(),
			}.Build(),
		}}

		Expect(ensureManager(m, ctx, "tenant-a")).To(Succeed())
		Expect(m.virtualNetworks.(*fakeVirtualNetworks).creates).To(BeEmpty())

		m.networkClasses.(*fakeNetworkClasses).items[0].SetStatus(privatev1.NetworkClassStatus_builder{
			State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
		}.Build())
		Expect(ensureManager(m, ctx, "tenant-a")).To(Succeed())
		Expect(m.virtualNetworks.(*fakeVirtualNetworks).creates).To(HaveLen(1))
	})

	It("waits for each default resource dependency across repeated Ensure calls", func() {
		m.networkClasses = &fakeNetworkClasses{items: []*privatev1.NetworkClass{
			privatev1.NetworkClass_builder{
				Id: "nc-1",
				Spec: privatev1.NetworkClassSpec_builder{Defaults: privatev1.NetworkDefaults_builder{
					SubnetIpv4Cidr: "10.0.1.0/24", SubnetIpv6Cidr: "fd00::/64", EnableNatGateway: true,
				}.Build()}.Build(),
				Status: privatev1.NetworkClassStatus_builder{
					State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
				}.Build(),
			}.Build(),
		}}
		m.externalIPPools = &fakeExternalIPPools{items: []*privatev1.ExternalIPPool{
			privatev1.ExternalIPPool_builder{
				Id: "pool-a", Status: privatev1.ExternalIPPoolStatus_builder{
					State: privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY, Available: 1,
				}.Build(),
			}.Build(),
		}}
		vns := m.virtualNetworks.(*fakeVirtualNetworks)
		subnets := m.subnets.(*fakeSubnets)
		groups := m.securityGroups.(*fakeSecurityGroups)
		ips := m.externalIPs.(*fakeExternalIPs)
		gateways := m.natGateways.(*fakeNATGateways)

		Expect(ensureManager(m, ctx, "tenant-a")).To(Succeed())
		Expect(vns.creates).To(HaveLen(1))
		Expect(subnets.creates).To(BeEmpty())
		Expect(groups.creates).To(BeEmpty())
		vns.items[0].SetStatus(privatev1.VirtualNetworkStatus_builder{
			State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_FAILED,
		}.Build())
		Expect(ensureManager(m, ctx, "tenant-a")).To(Succeed())
		Expect(subnets.creates).To(BeEmpty())
		vns.items[0].SetStatus(privatev1.VirtualNetworkStatus_builder{
			State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY,
		}.Build())

		Expect(ensureManager(m, ctx, "tenant-a")).To(Succeed())
		Expect(subnets.creates).To(HaveLen(2))
		Expect(groups.creates).To(HaveLen(1))
		Expect(ips.items).To(BeEmpty())
		Expect(subnets.creates[0].GetMetadata().GetTenant()).To(Equal("tenant-a"))
		Expect(subnets.creates[0].GetMetadata().GetLabels()).To(HaveKeyWithValue(defaultLabel, "true"))
		Expect(subnets.creates[0].GetMetadata().GetAnnotations()).To(HaveKeyWithValue(ownerReferenceAnnotation, "vn-default"))

		subnets.items[0].SetStatus(privatev1.SubnetStatus_builder{
			State: privatev1.SubnetState_SUBNET_STATE_READY,
		}.Build())
		Expect(ensureManager(m, ctx, "tenant-a")).To(Succeed())
		Expect(ips.items).To(BeEmpty())

		subnets.items[1].SetStatus(privatev1.SubnetStatus_builder{
			State: privatev1.SubnetState_SUBNET_STATE_FAILED,
		}.Build())
		Expect(ensureManager(m, ctx, "tenant-a")).To(Succeed())
		Expect(ips.items).To(BeEmpty())

		subnets.items[1].SetStatus(privatev1.SubnetStatus_builder{
			State: privatev1.SubnetState_SUBNET_STATE_READY,
		}.Build())
		groups.items[0].SetStatus(privatev1.SecurityGroupStatus_builder{
			State: privatev1.SecurityGroupState_SECURITY_GROUP_STATE_FAILED,
		}.Build())
		Expect(ensureManager(m, ctx, "tenant-a")).To(Succeed())
		Expect(ips.items).To(BeEmpty())

		groups.items[0].SetStatus(privatev1.SecurityGroupStatus_builder{
			State: privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY,
		}.Build())
		Expect(ensureManager(m, ctx, "tenant-a")).To(Succeed())
		Expect(ips.items).To(HaveLen(1))
		Expect(gateways.creates).To(BeEmpty())

		ips.items[0].SetStatus(privatev1.ExternalIPStatus_builder{
			State: privatev1.ExternalIPState_EXTERNAL_IP_STATE_FAILED,
		}.Build())
		Expect(ensureManager(m, ctx, "tenant-a")).To(Succeed())
		Expect(gateways.creates).To(BeEmpty())

		ips.items[0].SetStatus(privatev1.ExternalIPStatus_builder{
			State: privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED,
		}.Build())
		Expect(ensureManager(m, ctx, "tenant-a")).To(Succeed())
		Expect(ensureManager(m, ctx, "tenant-a")).To(Succeed())
		Expect(vns.creates).To(HaveLen(1))
		Expect(subnets.creates).To(HaveLen(2))
		Expect(groups.creates).To(HaveLen(1))
		Expect(ips.items).To(HaveLen(1))
		Expect(gateways.creates).To(HaveLen(1))
		Expect(gateways.creates[0].GetSpec().GetExternalIp().GetId()).To(Equal("eip-default"))
	})

	It("creates the default resources asynchronously and idempotently", func() {
		m.networkClasses = &fakeNetworkClasses{items: []*privatev1.NetworkClass{
			privatev1.NetworkClass_builder{
				Id: "nc-1",
				Spec: privatev1.NetworkClassSpec_builder{
					Defaults: privatev1.NetworkDefaults_builder{
						VirtualNetworkIpv4Cidr: "10.0.0.0/16",
						SubnetIpv4Cidr:         "10.0.1.0/24",
					}.Build(),
				}.Build(),
				Status: privatev1.NetworkClassStatus_builder{
					State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
					Hub:   "hub-a",
				}.Build(),
			}.Build(),
		}}

		vns := m.virtualNetworks.(*fakeVirtualNetworks)
		subnets := m.subnets.(*fakeSubnets)
		securityGroups := m.securityGroups.(*fakeSecurityGroups)
		Expect(ensureManager(m, ctx, "tenant-a")).To(Succeed())
		vns.items[0].SetStatus(privatev1.VirtualNetworkStatus_builder{
			State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY,
		}.Build())
		Expect(ensureManager(m, ctx, "tenant-a")).To(Succeed())
		Expect(ensureManager(m, ctx, "tenant-a")).To(Succeed())

		Expect(vns.creates).To(HaveLen(1))
		Expect(vns.creates[0].GetMetadata().GetLabels()).To(HaveKeyWithValue(defaultLabel, "true"))
		Expect(vns.creates[0].GetSpec().GetNetworkClass().GetId()).To(Equal("nc-1"))
		Expect(subnets.creates).To(HaveLen(1))
		Expect(subnets.creates[0].GetMetadata().GetAnnotations()).To(HaveKeyWithValue(ownerReferenceAnnotation, "vn-default"))
		Expect(securityGroups.creates).To(HaveLen(1))
	})

	It("re-reads default resources after concurrent creation", func() {
		m.networkClasses = &fakeNetworkClasses{items: []*privatev1.NetworkClass{
			privatev1.NetworkClass_builder{
				Id: "nc-1",
				Spec: privatev1.NetworkClassSpec_builder{Defaults: privatev1.NetworkDefaults_builder{
					SubnetIpv4Cidr: "10.0.1.0/24", EnableNatGateway: true,
				}.Build()}.Build(),
				Status: privatev1.NetworkClassStatus_builder{
					State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
				}.Build(),
			}.Build(),
		}}
		m.virtualNetworks = &fakeVirtualNetworks{items: []*privatev1.VirtualNetwork{
			privatev1.VirtualNetwork_builder{
				Id: "vn-default",
				Status: privatev1.VirtualNetworkStatus_builder{
					State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY,
				}.Build(),
			}.Build(),
		}}
		m.subnets = &fakeSubnets{alreadyExists: true}
		m.securityGroups = &fakeSecurityGroups{alreadyExists: true}
		m.externalIPs = &fakeExternalIPs{alreadyExists: true}
		m.externalIPPools = &fakeExternalIPPools{items: []*privatev1.ExternalIPPool{
			privatev1.ExternalIPPool_builder{
				Id: "pool-a", Status: privatev1.ExternalIPPoolStatus_builder{
					State: privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY, Available: 1,
				}.Build(),
			}.Build(),
		}}

		Expect(ensureManager(m, ctx, "tenant-a")).To(Succeed())
		Expect(m.externalIPs.(*fakeExternalIPs).items).To(HaveLen(1))
		Expect(m.natGateways.(*fakeNATGateways).creates).To(BeEmpty())
	})

	It("allocates a default NAT ExternalIP from the first ready pool with capacity", func() {
		m.networkClasses = &fakeNetworkClasses{items: []*privatev1.NetworkClass{
			privatev1.NetworkClass_builder{
				Id: "nc-1",
				Spec: privatev1.NetworkClassSpec_builder{
					Defaults: privatev1.NetworkDefaults_builder{EnableNatGateway: true}.Build(),
				}.Build(),
				Status: privatev1.NetworkClassStatus_builder{
					State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
					Hub:   "hub-a",
				}.Build(),
			}.Build(),
		}}
		m.externalIPPools = &fakeExternalIPPools{items: []*privatev1.ExternalIPPool{
			privatev1.ExternalIPPool_builder{
				Id: "pool-b",
				Status: privatev1.ExternalIPPoolStatus_builder{
					State:     privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY,
					Available: 10,
				}.Build(),
			}.Build(),
			privatev1.ExternalIPPool_builder{
				Id: "pool-a",
				Status: privatev1.ExternalIPPoolStatus_builder{
					State:     privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY,
					Available: 1,
				}.Build(),
			}.Build(),
		}}

		externalIPs := m.externalIPs.(*fakeExternalIPs)
		natGateways := m.natGateways.(*fakeNATGateways)
		Expect(ensureManager(m, ctx, "tenant-a")).To(Succeed())
		m.virtualNetworks.(*fakeVirtualNetworks).items[0].SetStatus(privatev1.VirtualNetworkStatus_builder{
			State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY,
		}.Build())
		Expect(ensureManager(m, ctx, "tenant-a")).To(Succeed())
		m.securityGroups.(*fakeSecurityGroups).items[0].SetStatus(privatev1.SecurityGroupStatus_builder{
			State: privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY,
		}.Build())
		Expect(ensureManager(m, ctx, "tenant-a")).To(Succeed())
		Expect(natGateways.creates).To(BeEmpty())
		externalIPs.items[0].SetStatus(privatev1.ExternalIPStatus_builder{
			State: privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED,
		}.Build())
		Expect(ensureManager(m, ctx, "tenant-a")).To(Succeed())
		Expect(externalIPs.items).To(HaveLen(1))
		Expect(externalIPs.items[0].GetSpec().GetPool().GetId()).To(Equal("pool-a"))
		Expect(natGateways.creates).To(HaveLen(1))
		Expect(natGateways.creates[0].GetSpec().GetExternalIp().GetId()).To(Equal("eip-default"))
	})

	It("does not create resources for reserved tenants", func() {
		Expect(ensureManager(m, ctx, "system")).To(Succeed())
		Expect(ensureManager(m, ctx, "shared")).To(Succeed())
		Expect(m.networkClasses.(*fakeNetworkClasses).items).To(BeEmpty())
	})

	It("deletes default resources through the controller lifecycle", func() {
		vns := m.virtualNetworks.(*fakeVirtualNetworks)
		vns.items = []*privatev1.VirtualNetwork{privatev1.VirtualNetwork_builder{
			Id: "vn-default",
			Metadata: privatev1.Metadata_builder{
				Name: "default", Tenant: "tenant-a",
				Labels: map[string]string{defaultLabel: "true"},
			}.Build(),
		}.Build()}

		Expect(m.Delete(ctx, "tenant-a")).To(MatchError(ErrResourcesDeleting))
		Expect(vns.deletes).To(ConsistOf("vn-default"))
		Expect(m.Delete(ctx, "tenant-a")).To(Succeed())
	})

	It("reports multiple active NetworkClasses instead of guessing", func() {
		m.networkClasses = &fakeNetworkClasses{items: []*privatev1.NetworkClass{
			privatev1.NetworkClass_builder{Id: "nc-a"}.Build(),
			privatev1.NetworkClass_builder{Id: "nc-b"}.Build(),
		}}
		Expect(ensureManager(m, ctx, "tenant-a")).To(MatchError(errors.New("multiple active NetworkClasses are configured")))
	})
})
