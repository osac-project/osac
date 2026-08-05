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
)

var _ = Describe("Default networking provisioner", func() {
	var (
		provisioner *DefaultNetworkingProvisioner
	)

	createNetworkClass := func(defaults *privatev1.NetworkDefaults) *privatev1.NetworkClass {
		ncDao := provisioner.networkClassDao
		nc := privatev1.NetworkClass_builder{
			Metadata: privatev1.Metadata_builder{
				Name:   "test-network-class",
				Tenant: "system",
			}.Build(),
			IsDefault:              new(true),
			FabricManager:          "netris",
			ImplementationStrategy: "netris",
			Spec: privatev1.NetworkClassSpec_builder{
				Defaults: defaults,
			}.Build(),
			Status: privatev1.NetworkClassStatus_builder{
				State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
			}.Build(),
		}.Build()
		resp, err := ncDao.Create().SetObject(nc).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		return resp.GetObject()
	}

	createTenant := func(name string) {
		tenantDao := provisioner.tenantDao
		tenant := privatev1.Tenant_builder{
			Metadata: privatev1.Metadata_builder{
				Name:   name,
				Tenant: name,
			}.Build(),
		}.Build()
		tenant.SetId(name)
		_, err := tenantDao.Create().SetObject(tenant).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
	}

	createExternalIPPool := func(name, tenant string, available int64) *privatev1.ExternalIPPool {
		pool := privatev1.ExternalIPPool_builder{
			Metadata: privatev1.Metadata_builder{
				Name:   name,
				Tenant: tenant,
			}.Build(),
			Status: privatev1.ExternalIPPoolStatus_builder{
				State:     privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY,
				Total:     available,
				Available: available,
				Allocated: 0,
			}.Build(),
		}.Build()
		resp, err := provisioner.externalIPPoolDao.Create().SetObject(pool).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		return resp.GetObject()
	}

	BeforeEach(func() {
		var err error
		provisioner, err = NewDefaultNetworkingProvisioner().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	Context("when no default NetworkClass exists", func() {
		It("returns nil without creating any resources", func() {
			createTenant("test-tenant")
			err := provisioner.Provision(ctx, "test-tenant")
			Expect(err).ToNot(HaveOccurred())

			vnList, err := provisioner.virtualNetworkDao.List().
				SetFilter("this.metadata.tenant == 'test-tenant'").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(vnList.GetItems()).To(BeEmpty())
		})
	})

	Context("when default NetworkClass exists with defaults", func() {
		BeforeEach(func() {
			createTenant("test-tenant")
			createNetworkClass(privatev1.NetworkDefaults_builder{
				VirtualNetworkIpv4Cidr: "10.0.0.0/16",
				VirtualNetworkIpv6Cidr: "fd00::/48",
				SubnetIpv4Cidr:         "10.0.1.0/24",
				SubnetIpv6Cidr:         "fd00:0:0:1::/64",
				IngressRules: []*privatev1.SecurityRule{
					privatev1.SecurityRule_builder{
						Protocol: privatev1.Protocol_PROTOCOL_TCP,
						PortFrom: new(int32(22)),
						PortTo:   new(int32(22)),
						Ipv4Cidr: new("0.0.0.0/0"),
					}.Build(),
				},
				EgressRules: []*privatev1.SecurityRule{
					privatev1.SecurityRule_builder{
						Protocol: privatev1.Protocol_PROTOCOL_TCP,
						PortFrom: new(int32(443)),
						PortTo:   new(int32(443)),
						Ipv4Cidr: new("0.0.0.0/0"),
					}.Build(),
				},
			}.Build())
		})

		It("creates default VirtualNetwork with correct CIDR and labels", func() {
			err := provisioner.Provision(ctx, "test-tenant")
			Expect(err).ToNot(HaveOccurred())

			vnList, err := provisioner.virtualNetworkDao.List().
				SetFilter("this.metadata.tenant == 'test-tenant'").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(vnList.GetItems()).To(HaveLen(1))

			vn := vnList.GetItems()[0]
			Expect(vn.GetMetadata().GetName()).To(Equal("default"))
			Expect(vn.GetMetadata().GetTenant()).To(Equal("test-tenant"))
			Expect(vn.GetMetadata().GetLabels()).To(HaveKeyWithValue("osac.openshift.io/default", "true"))
			Expect(vn.GetSpec().GetIpv4Cidr()).To(Equal("10.0.0.0/16"))
			Expect(vn.GetSpec().GetIpv6Cidr()).To(Equal("fd00::/48"))
			Expect(vn.GetSpec().GetNetworkClass()).ToNot(BeEmpty())
			Expect(vn.GetSpec().GetImplementationStrategy()).ToNot(BeEmpty())
			Expect(vn.GetStatus().GetState()).To(Equal(
				privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING))
		})

		It("creates default IPv4 Subnet with correct CIDR and owner-reference", func() {
			err := provisioner.Provision(ctx, "test-tenant")
			Expect(err).ToNot(HaveOccurred())

			vnList, err := provisioner.virtualNetworkDao.List().
				SetFilter("this.metadata.tenant == 'test-tenant'").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			vnID := vnList.GetItems()[0].GetId()

			subnetList, err := provisioner.subnetDao.List().
				SetFilter("this.metadata.tenant == 'test-tenant'").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			var ipv4Subnet *privatev1.Subnet
			for _, s := range subnetList.GetItems() {
				if s.GetMetadata().GetName() == "default-ipv4" {
					ipv4Subnet = s
					break
				}
			}
			Expect(ipv4Subnet).ToNot(BeNil())
			Expect(ipv4Subnet.GetMetadata().GetTenant()).To(Equal("test-tenant"))
			Expect(ipv4Subnet.GetMetadata().GetLabels()).To(HaveKeyWithValue("osac.openshift.io/default", "true"))
			Expect(ipv4Subnet.GetMetadata().GetAnnotations()).To(HaveKeyWithValue("osac.openshift.io/owner-reference", vnID))
			Expect(ipv4Subnet.GetSpec().GetIpv4Cidr()).To(Equal("10.0.1.0/24"))
			Expect(ipv4Subnet.GetSpec().GetVirtualNetwork()).To(Equal(vnID))
			Expect(ipv4Subnet.GetStatus().GetState()).To(Equal(
				privatev1.SubnetState_SUBNET_STATE_PENDING))
		})

		It("creates default IPv6 Subnet with correct CIDR and owner-reference", func() {
			err := provisioner.Provision(ctx, "test-tenant")
			Expect(err).ToNot(HaveOccurred())

			vnList, err := provisioner.virtualNetworkDao.List().
				SetFilter("this.metadata.tenant == 'test-tenant'").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			vnID := vnList.GetItems()[0].GetId()

			subnetList, err := provisioner.subnetDao.List().
				SetFilter("this.metadata.tenant == 'test-tenant'").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			var ipv6Subnet *privatev1.Subnet
			for _, s := range subnetList.GetItems() {
				if s.GetMetadata().GetName() == "default-ipv6" {
					ipv6Subnet = s
					break
				}
			}
			Expect(ipv6Subnet).ToNot(BeNil())
			Expect(ipv6Subnet.GetMetadata().GetTenant()).To(Equal("test-tenant"))
			Expect(ipv6Subnet.GetMetadata().GetLabels()).To(HaveKeyWithValue("osac.openshift.io/default", "true"))
			Expect(ipv6Subnet.GetMetadata().GetAnnotations()).To(HaveKeyWithValue("osac.openshift.io/owner-reference", vnID))
			Expect(ipv6Subnet.GetSpec().GetIpv6Cidr()).To(Equal("fd00:0:0:1::/64"))
			Expect(ipv6Subnet.GetSpec().GetVirtualNetwork()).To(Equal(vnID))
			Expect(ipv6Subnet.GetStatus().GetState()).To(Equal(
				privatev1.SubnetState_SUBNET_STATE_PENDING))
		})

		It("creates default SecurityGroup with rules and owner-reference", func() {
			err := provisioner.Provision(ctx, "test-tenant")
			Expect(err).ToNot(HaveOccurred())

			vnList, err := provisioner.virtualNetworkDao.List().
				SetFilter("this.metadata.tenant == 'test-tenant'").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			vnID := vnList.GetItems()[0].GetId()

			sgList, err := provisioner.securityGroupDao.List().
				SetFilter("this.metadata.tenant == 'test-tenant'").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(sgList.GetItems()).To(HaveLen(1))

			sg := sgList.GetItems()[0]
			Expect(sg.GetMetadata().GetName()).To(Equal("default"))
			Expect(sg.GetMetadata().GetTenant()).To(Equal("test-tenant"))
			Expect(sg.GetMetadata().GetLabels()).To(HaveKeyWithValue("osac.openshift.io/default", "true"))
			Expect(sg.GetMetadata().GetAnnotations()).To(HaveKeyWithValue("osac.openshift.io/owner-reference", vnID))
			Expect(sg.GetSpec().GetVirtualNetwork()).To(Equal(vnID))
			Expect(sg.GetSpec().GetIngress()).To(HaveLen(1))
			Expect(sg.GetSpec().GetIngress()[0].GetProtocol()).To(Equal(privatev1.Protocol_PROTOCOL_TCP))
			Expect(sg.GetSpec().GetIngress()[0].GetPortFrom()).To(Equal(int32(22)))
			Expect(sg.GetSpec().GetEgress()).To(HaveLen(1))
			Expect(sg.GetSpec().GetEgress()[0].GetProtocol()).To(Equal(privatev1.Protocol_PROTOCOL_TCP))
			Expect(sg.GetStatus().GetState()).To(Equal(
				privatev1.SecurityGroupState_SECURITY_GROUP_STATE_PENDING))
		})

		It("does not create NATGateway when enable_nat_gateway is false", func() {
			err := provisioner.Provision(ctx, "test-tenant")
			Expect(err).ToNot(HaveOccurred())

			ngList, err := provisioner.natGatewayDao.List().
				SetFilter("this.metadata.tenant == 'test-tenant'").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(ngList.GetItems()).To(BeEmpty())
		})

		It("sets implementation_strategy on VN from NetworkClass", func() {
			err := provisioner.Provision(ctx, "test-tenant")
			Expect(err).ToNot(HaveOccurred())

			vnList, err := provisioner.virtualNetworkDao.List().
				SetFilter("this.metadata.tenant == 'test-tenant'").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(vnList.GetItems()).To(HaveLen(1))
			Expect(vnList.GetItems()[0].GetSpec().GetImplementationStrategy()).To(Equal("netris"))
		})

		It("creates all four resources in a single Provision call", func() {
			err := provisioner.Provision(ctx, "test-tenant")
			Expect(err).ToNot(HaveOccurred())

			vnList, err := provisioner.virtualNetworkDao.List().
				SetFilter("this.metadata.tenant == 'test-tenant'").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(vnList.GetItems()).To(HaveLen(1))

			subnetList, err := provisioner.subnetDao.List().
				SetFilter("this.metadata.tenant == 'test-tenant'").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(subnetList.GetItems()).To(HaveLen(2))

			sgList, err := provisioner.securityGroupDao.List().
				SetFilter("this.metadata.tenant == 'test-tenant'").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(sgList.GetItems()).To(HaveLen(1))
		})
	})

	Context("when enable_nat_gateway is true", func() {
		It("creates ExternalIP and NATGateway when pool has capacity", func() {
			createTenant("nat-tenant")
			createNetworkClass(privatev1.NetworkDefaults_builder{
				VirtualNetworkIpv4Cidr: "10.0.0.0/16",
				SubnetIpv4Cidr:         "10.0.1.0/24",
				EnableNatGateway:       true,
			}.Build())
			pool := createExternalIPPool("test-pool", "system", 10)

			err := provisioner.Provision(ctx, "nat-tenant")
			Expect(err).ToNot(HaveOccurred())

			eipList, err := provisioner.externalIPDao.List().
				SetFilter("this.metadata.tenant == 'nat-tenant'").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(eipList.GetItems()).To(HaveLen(1))
			eip := eipList.GetItems()[0]
			Expect(eip.GetMetadata().GetLabels()).To(HaveKeyWithValue("osac.openshift.io/default", "true"))
			Expect(eip.GetSpec().GetPool()).To(Equal(pool.GetId()))
			Expect(eip.GetStatus().GetState()).To(Equal(privatev1.ExternalIPState_EXTERNAL_IP_STATE_PENDING))
			Expect(eip.GetStatus().GetAttached()).To(BeTrue())

			ngList, err := provisioner.natGatewayDao.List().
				SetFilter("this.metadata.tenant == 'nat-tenant'").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(ngList.GetItems()).To(HaveLen(1))
			vnList, err := provisioner.virtualNetworkDao.List().
				SetFilter("this.metadata.tenant == 'nat-tenant'").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			vnID := vnList.GetItems()[0].GetId()

			ng := ngList.GetItems()[0]
			Expect(ng.GetMetadata().GetLabels()).To(HaveKeyWithValue("osac.openshift.io/default", "true"))
			Expect(ng.GetMetadata().GetAnnotations()).To(HaveKeyWithValue("osac.openshift.io/owner-reference", vnID))
			Expect(ng.GetSpec().GetExternalIp()).To(Equal(eip.GetId()))
			Expect(ng.GetStatus().GetState()).To(Equal(privatev1.NATGatewayState_NAT_GATEWAY_STATE_PENDING))

			poolResp, err := provisioner.externalIPPoolDao.Get().
				SetId(pool.GetId()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(poolResp.GetObject().GetStatus().GetAllocated()).To(Equal(int64(1)))
			Expect(poolResp.GetObject().GetStatus().GetAvailable()).To(Equal(int64(9)))
		})

		It("returns error when no pool has capacity", func() {
			createTenant("nat-tenant")
			createNetworkClass(privatev1.NetworkDefaults_builder{
				VirtualNetworkIpv4Cidr: "10.0.0.0/16",
				SubnetIpv4Cidr:         "10.0.1.0/24",
				EnableNatGateway:       true,
			}.Build())

			err := provisioner.Provision(ctx, "nat-tenant")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no ExternalIP pool with available capacity"))
		})
	})

	Context("when NetworkClass defaults are partially populated", func() {
		It("creates only IPv4 subnet when IPv6 CIDRs are empty", func() {
			createTenant("test-tenant")
			createNetworkClass(privatev1.NetworkDefaults_builder{
				VirtualNetworkIpv4Cidr: "10.0.0.0/16",
				SubnetIpv4Cidr:         "10.0.1.0/24",
			}.Build())

			err := provisioner.Provision(ctx, "test-tenant")
			Expect(err).ToNot(HaveOccurred())

			vnList, err := provisioner.virtualNetworkDao.List().
				SetFilter("this.metadata.tenant == 'test-tenant'").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(vnList.GetItems()).To(HaveLen(1))
			Expect(vnList.GetItems()[0].GetSpec().GetIpv4Cidr()).To(Equal("10.0.0.0/16"))

			subnetList, err := provisioner.subnetDao.List().
				SetFilter("this.metadata.tenant == 'test-tenant'").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(subnetList.GetItems()).To(HaveLen(1))
			Expect(subnetList.GetItems()[0].GetMetadata().GetName()).To(Equal("default-ipv4"))
		})
	})

	Context("when NetworkClass has no defaults", func() {
		It("returns nil without creating any resources", func() {
			createTenant("test-tenant")
			createNetworkClass(nil)

			err := provisioner.Provision(ctx, "test-tenant")
			Expect(err).ToNot(HaveOccurred())

			vnList, err := provisioner.virtualNetworkDao.List().
				SetFilter("this.metadata.tenant == 'test-tenant'").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(vnList.GetItems()).To(BeEmpty())
		})
	})
})
