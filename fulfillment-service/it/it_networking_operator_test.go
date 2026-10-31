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

// Networking specs that need the deployed OSAC operator. The SUITE=fulfillment installer target runs them in their own
// phase (ginkgo --label-filter networking-operator), with global.networking.provisioningEnabled=false. The osac chart
// renders the Netris Fabric Manager and cudn_evpn Kubernetes Manager registrations of the kind/dev profile
// (global.networking.fabricManager=netris, k8sManager=cudn_evpn). Unlike the service-only specs, these never write
// networking status themselves: every state they check comes from the operator's reconcilers and feedback
// controllers. With provider networking disabled the operator reads no registration and runs no AAP job; the enabled
// path belongs to the operator suite and the E2E suites.

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// opTimeout bounds the waits for the operator: its reconcilers, the feedback controllers and the fulfillment
// controller (which runs without periodic synchronization in the installer target) each take a round.
const opTimeout = 5 * time.Minute

// opExpectProvisioningDisabled checks the Ready condition of a networking CR: True, reason ProvisioningDisabled,
// a message naming the skipped task, and no job.
func opExpectProvisioningDisabled(g Gomega, kind string, conditions []metav1.Condition, jobs int, task string) {
	ready := meta.FindStatusCondition(conditions, osacv1alpha1.NetworkConditionReady)
	g.Expect(ready).ToNot(BeNil(), "%s CR has no Ready condition yet: %+v", kind, conditions)
	g.Expect(ready.Status).To(Equal(metav1.ConditionTrue), "%s CR Ready: %+v", kind, ready)
	g.Expect(ready.Reason).To(Equal(osacv1alpha1.ReasonProvisioningDisabled), "%s CR Ready: %+v", kind, ready)
	g.Expect(ready.Message).To(ContainSubstring("provider networking is disabled"), "%s CR Ready: %+v", kind, ready)
	g.Expect(ready.Message).To(ContainSubstring(task), "%s CR Ready: %+v", kind, ready)
	g.Expect(jobs).To(BeZero(), "%s CR recorded a networking job although provider networking is disabled", kind)
}

var _ = Describe("Networking with operator", Label(networkingOperatorLabel), func() {
	var c ndClients

	BeforeEach(func(ctx context.Context) {
		ndRequireOperatorDeployed(ctx)
		c = newNDClients()
		Expect(ndProviderNetworkingEnabled(ctx)).To(BeFalse(),
			"the operator phase of the SUITE=fulfillment target deploys global.networking.provisioningEnabled=false")
	})

	It("TC-OP-01 makes the installer's NetworkClass READY without resolving the registrations", func(ctx context.Context) {
		By("checking the Fabric Manager registration that the osac chart renders for the profile")
		netris := &corev1.ConfigMap{}
		Expect(tool.KubeClient().Get(ctx, crclient.ObjectKey{
			Namespace: hubNamespace,
			Name:      "osac-network-fabric-manager-netris",
		}, netris)).To(Succeed(), "the osac chart renders it for global.networking.fabricManager=netris")
		fmt.Fprintf(GinkgoWriter, "Netris registration %s: %v\n", netris.Name, netris.Data)
		Expect(netris.Labels).To(HaveKeyWithValue("osac.openshift.io/network-fabric-manager", "true"))
		Expect(netris.Data["name"]).To(Equal("netris"))
		Expect(netris.Data["ansible_role"]).To(Equal("osac.templates.netris"))
		Expect(netris.Data["network_types"]).To(Equal("evpn"))
		Expect(netris.Data["capabilities"]).To(Equal("eastWestEthernet,security_group_icmp"))

		By("checking the Kubernetes Manager registration that the osac chart renders for the profile")
		cudn := &corev1.ConfigMap{}
		Expect(tool.KubeClient().Get(ctx, crclient.ObjectKey{
			Namespace: hubNamespace,
			Name:      "osac-network-k8s-manager-cudn-evpn",
		}, cudn)).To(Succeed(), "the osac chart renders it for global.networking.k8sManager=cudn_evpn")
		Expect(cudn.Labels).To(HaveKeyWithValue("osac.openshift.io/network-k8s-manager", "true"))
		Expect(cudn.Data["name"]).To(Equal("cudn_evpn"))
		Expect(cudn.Data).ToNot(HaveKey("capabilities"))

		nc := ndSingletonNetworkClass(ctx, c)
		By(fmt.Sprintf("waiting for the operator to make the NetworkClass CR of %s Ready", nc.GetId()))
		Eventually(func(g Gomega) {
			list := &osacv1alpha1.NetworkClassList{}
			g.Expect(tool.KubeClient().List(ctx, list, crclient.InNamespace(hubNamespace),
				crclient.MatchingLabels{labels.NetworkClassUuid: nc.GetId()})).To(Succeed())
			g.Expect(list.Items).To(HaveLen(1), "NetworkClass CR of %s", nc.GetId())
			status := list.Items[0].Status
			fmt.Fprintf(GinkgoWriter, "NetworkClass CR status: %+v\n", status)
			ready := meta.FindStatusCondition(status.Conditions, osacv1alpha1.NetworkConditionReady)
			g.Expect(ready).ToNot(BeNil())
			g.Expect(ready.Status).To(Equal(metav1.ConditionTrue), "Ready: %+v", ready)
			// Provider networking disabled: the operator does not read the registrations.
			g.Expect(ready.Reason).To(Equal(osacv1alpha1.ReasonProvisioningDisabled), "Ready: %+v", ready)
			g.Expect(status.NetworkManagers).To(BeNil())
			g.Expect(string(status.NetworkType)).To(BeEmpty())
		}, opTimeout, time.Second).Should(Succeed())

		By("waiting for the feedback controller to report READY in the fulfillment record")
		Eventually(func(g Gomega) {
			response, err := c.networkClasses.Get(ctx, privatev1.NetworkClassesGetRequest_builder{
				Id: nc.GetId(),
			}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			status := response.GetObject().GetStatus()
			g.Expect(status.GetState()).To(Equal(privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY),
				"NetworkClass: %s", ndText(response.GetObject()))
			g.Expect(status.GetNetworkManagers().GetFabric().GetName()).To(BeEmpty(),
				"NetworkClass: %s", ndText(response.GetObject()))
			g.Expect(status.GetNetworkType()).To(BeEmpty())
			// Disabled: the registrations are not read, so no capability is recorded.
			g.Expect(status.HasSecurityGroupIcmp()).To(BeFalse(), "NetworkClass: %s", ndText(response.GetObject()))
		}, opTimeout, time.Second).Should(Succeed())
		response, err := c.networkClasses.Get(ctx, privatev1.NetworkClassesGetRequest_builder{Id: nc.GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
		ndLog("Resolved NetworkClass", response.GetObject())
	})

	It("TC-OP-02 makes VirtualNetwork, Subnet and SecurityGroup READY without a job and deletes them",
		func(ctx context.Context) {
			cidr := ndVirtualNetworkCIDR()
			base := netip.MustParsePrefix(cidr).Addr()

			By("creating a VirtualNetwork through the public API")
			vnResponse, err := c.publicVirtualNetworks.Create(ctx, publicv1.VirtualNetworksCreateRequest_builder{
				Object: publicv1.VirtualNetwork_builder{
					Metadata: publicv1.Metadata_builder{Name: ndUniqueName("op-vn")}.Build(),
					Spec:     publicv1.VirtualNetworkSpec_builder{Ipv4Cidr: new(cidr)}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			vnID := vnResponse.GetObject().GetId()
			DeferCleanup(func(cleanupCtx context.Context) {
				Expect(cleanupVirtualNetworkFixture(cleanupCtx, c.virtualNetworks, vnID)).To(Succeed())
			})

			By("waiting for the operator to make the VirtualNetwork READY with reason ProvisioningDisabled")
			Eventually(func(g Gomega) {
				list := &osacv1alpha1.VirtualNetworkList{}
				g.Expect(tool.KubeClient().List(ctx, list, crclient.InNamespace(hubNamespace),
					crclient.MatchingLabels{labels.VirtualNetworkUuid: vnID})).To(Succeed())
				g.Expect(list.Items).To(HaveLen(1), "VirtualNetwork CR of %s", vnID)
				opExpectProvisioningDisabled(g, "VirtualNetwork", list.Items[0].Status.Conditions,
					len(list.Items[0].Status.Jobs), "create_virtual_network")
				response, err := c.virtualNetworks.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: vnID}.Build())
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(response.GetObject().GetStatus().GetState()).To(Equal(
					privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY), "VirtualNetwork: %s",
					ndText(response.GetObject()))
				g.Expect(response.GetObject().GetStatus().GetMessage()).To(ContainSubstring(
					"provider networking is disabled"))
			}, opTimeout, time.Second).Should(Succeed())

			By("creating a Subnet and a SecurityGroup through the public API")
			subnetResponse, err := c.publicSubnets.Create(ctx, publicv1.SubnetsCreateRequest_builder{
				Object: publicv1.Subnet_builder{
					Metadata: publicv1.Metadata_builder{Name: ndUniqueName("op-subnet")}.Build(),
					Spec: publicv1.SubnetSpec_builder{
						VirtualNetwork: publicv1.VirtualNetworkLocalReference_builder{Id: vnID}.Build(),
						Ipv4Cidr:       new(netip.PrefixFrom(base, 24).String()),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			subnetID := subnetResponse.GetObject().GetId()
			DeferCleanup(func(cleanupCtx context.Context) {
				Expect(cleanupSubnetFixture(cleanupCtx, c.subnets, subnetID)).To(Succeed())
			})
			groupResponse, err := c.publicSecurityGroups.Create(ctx, publicv1.SecurityGroupsCreateRequest_builder{
				Object: publicv1.SecurityGroup_builder{
					Metadata: publicv1.Metadata_builder{Name: ndUniqueName("op-sg")}.Build(),
					Spec: publicv1.SecurityGroupSpec_builder{
						VirtualNetwork: publicv1.VirtualNetworkLocalReference_builder{Id: vnID}.Build(),
						Ingress: []*publicv1.SecurityRule{
							ndPublicRule(publicv1.Protocol_PROTOCOL_TCP, "0.0.0.0/0", 22, 22),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			groupID := groupResponse.GetObject().GetId()
			DeferCleanup(func(cleanupCtx context.Context) {
				Expect(cleanupSecurityGroupFixture(cleanupCtx, c.securityGroups, groupID)).To(Succeed())
			})

			By("waiting for the operator to make the Subnet and the SecurityGroup READY")
			Eventually(func(g Gomega) {
				subnets := &osacv1alpha1.SubnetList{}
				g.Expect(tool.KubeClient().List(ctx, subnets, crclient.InNamespace(hubNamespace),
					crclient.MatchingLabels{labels.SubnetUuid: subnetID})).To(Succeed())
				g.Expect(subnets.Items).To(HaveLen(1), "Subnet CR of %s", subnetID)
				opExpectProvisioningDisabled(g, "Subnet", subnets.Items[0].Status.Conditions,
					len(subnets.Items[0].Status.Jobs), "create_subnet")
				response, err := c.subnets.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: subnetID}.Build())
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(response.GetObject().GetStatus().GetState()).To(Equal(
					privatev1.SubnetState_SUBNET_STATE_READY), "Subnet: %s", ndText(response.GetObject()))
			}, opTimeout, time.Second).Should(Succeed())
			Eventually(func(g Gomega) {
				groups := &osacv1alpha1.SecurityGroupList{}
				g.Expect(tool.KubeClient().List(ctx, groups, crclient.InNamespace(hubNamespace),
					crclient.MatchingLabels{labels.SecurityGroupUuid: groupID})).To(Succeed())
				g.Expect(groups.Items).To(HaveLen(1), "SecurityGroup CR of %s", groupID)
				opExpectProvisioningDisabled(g, "SecurityGroup", groups.Items[0].Status.Conditions,
					len(groups.Items[0].Status.Jobs), "create_security_group")
				response, err := c.securityGroups.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{
					Id: groupID,
				}.Build())
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(response.GetObject().GetStatus().GetState()).To(Equal(
					privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY), "SecurityGroup: %s",
					ndText(response.GetObject()))
			}, opTimeout, time.Second).Should(Succeed())

			By("updating the SecurityGroup rules: they reach the CR, the group stays READY/ProvisioningDisabled")
			_, err = c.publicSecurityGroups.Update(ctx, publicv1.SecurityGroupsUpdateRequest_builder{
				Object: publicv1.SecurityGroup_builder{
					Id: groupID,
					Spec: publicv1.SecurityGroupSpec_builder{
						Ingress: []*publicv1.SecurityRule{
							ndPublicRule(publicv1.Protocol_PROTOCOL_TCP, "0.0.0.0/0", 22, 22),
							ndPublicRule(publicv1.Protocol_PROTOCOL_TCP, "0.0.0.0/0", 443, 443),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Eventually(func(g Gomega) {
				groups := &osacv1alpha1.SecurityGroupList{}
				g.Expect(tool.KubeClient().List(ctx, groups, crclient.InNamespace(hubNamespace),
					crclient.MatchingLabels{labels.SecurityGroupUuid: groupID})).To(Succeed())
				g.Expect(groups.Items).To(HaveLen(1))
				g.Expect(groups.Items[0].Spec.IngressRules).To(HaveLen(2), "CR spec: %+v", groups.Items[0].Spec)
				opExpectProvisioningDisabled(g, "SecurityGroup", groups.Items[0].Status.Conditions,
					len(groups.Items[0].Status.Jobs), "create_security_group")
				response, err := c.securityGroups.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{
					Id: groupID,
				}.Build())
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(response.GetObject().GetStatus().GetState()).To(Equal(
					privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY))
				g.Expect(response.GetObject().GetStatus().GetMessage()).ToNot(Equal("Updating"),
					"no Updating with provider networking disabled: %s", ndText(response.GetObject()))
			}, opTimeout, time.Second).Should(Succeed())

			deleteAndWait := func(kind, id string, deleteFn func() error, getFn func() error,
				crCount func() (int, error)) {
				By(fmt.Sprintf("deleting the %s %s; it completes without a task", kind, id))
				Expect(deleteFn()).To(Succeed())
				Eventually(func(g Gomega) {
					err := getFn()
					g.Expect(grpcstatus.Code(err)).To(Equal(grpccodes.NotFound), "%s %s remains (%v)", kind, id, err)
					count, err := crCount()
					g.Expect(err).ToNot(HaveOccurred())
					g.Expect(count).To(BeZero(), "the %s CR of %s remains", kind, id)
				}, opTimeout, time.Second).Should(Succeed())
			}
			deleteAndWait("SecurityGroup", groupID,
				func() error {
					_, err := c.publicSecurityGroups.Delete(ctx, publicv1.SecurityGroupsDeleteRequest_builder{
						Id: groupID,
					}.Build())
					return err
				},
				func() error {
					_, err := c.securityGroups.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: groupID}.Build())
					return err
				},
				func() (int, error) {
					list := &osacv1alpha1.SecurityGroupList{}
					err := tool.KubeClient().List(ctx, list, crclient.InNamespace(hubNamespace),
						crclient.MatchingLabels{labels.SecurityGroupUuid: groupID})
					return len(list.Items), err
				})
			deleteAndWait("Subnet", subnetID,
				func() error {
					_, err := c.publicSubnets.Delete(ctx, publicv1.SubnetsDeleteRequest_builder{Id: subnetID}.Build())
					return err
				},
				func() error {
					_, err := c.subnets.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: subnetID}.Build())
					return err
				},
				func() (int, error) {
					list := &osacv1alpha1.SubnetList{}
					err := tool.KubeClient().List(ctx, list, crclient.InNamespace(hubNamespace),
						crclient.MatchingLabels{labels.SubnetUuid: subnetID})
					return len(list.Items), err
				})
			deleteAndWait("VirtualNetwork", vnID,
				func() error {
					_, err := c.publicVirtualNetworks.Delete(ctx, publicv1.VirtualNetworksDeleteRequest_builder{
						Id: vnID,
					}.Build())
					return err
				},
				func() error {
					_, err := c.virtualNetworks.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: vnID}.Build())
					return err
				},
				func() (int, error) {
					list := &osacv1alpha1.VirtualNetworkList{}
					err := tool.KubeClient().List(ctx, list, crclient.InNamespace(hubNamespace),
						crclient.MatchingLabels{labels.VirtualNetworkUuid: vnID})
					return len(list.Items), err
				})
		})

	It("TC-OP-03 makes an ExternalIPPool READY without a job, rejects ExternalIP creates and deletes the pool",
		func(ctx context.Context) {
			pool := ndCreatePool(ctx, c, ndPoolBase()+"/28")
			poolID := pool.GetId()

			By("waiting for the operator to make the pool READY with reason ProvisioningDisabled")
			Eventually(func(g Gomega) {
				list := &osacv1alpha1.ExternalIPPoolList{}
				g.Expect(tool.KubeClient().List(ctx, list, crclient.InNamespace(hubNamespace),
					crclient.MatchingLabels{labels.ExternalIPPoolUuid: poolID})).To(Succeed())
				g.Expect(list.Items).To(HaveLen(1), "ExternalIPPool CR of %s", poolID)
				opExpectProvisioningDisabled(g, "ExternalIPPool", list.Items[0].Status.Conditions,
					len(list.Items[0].Status.Jobs), "create_external_ip_pool")
				response, err := c.pools.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{Id: poolID}.Build())
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(response.GetObject().GetStatus().GetState()).To(Equal(
					privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY), "pool: %s",
					ndText(response.GetObject()))
				g.Expect(response.GetObject().GetStatus().GetTotal()).To(Equal(int64(14)))
				g.Expect(response.GetObject().GetStatus().GetAvailable()).To(Equal(int64(14)))
			}, opTimeout, time.Second).Should(Succeed())

			By("creating an ExternalIP from the READY pool: rejected as designed")
			_, err := c.publicExternalIPs.Create(ctx, publicv1.ExternalIPsCreateRequest_builder{
				Object: publicv1.ExternalIP_builder{
					Metadata: publicv1.Metadata_builder{Name: ndUniqueName("op-eip")}.Build(),
					Spec: publicv1.ExternalIPSpec_builder{
						Pool: publicv1.ExternalIPPoolReference_builder{Id: poolID}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			ndExpectCode(err, grpccodes.FailedPrecondition, "provider networking is disabled")

			By("deleting the pool; it completes without a task")
			_, err = c.pools.Delete(ctx, privatev1.ExternalIPPoolsDeleteRequest_builder{Id: poolID}.Build())
			Expect(err).ToNot(HaveOccurred())
			Eventually(func(g Gomega) {
				_, err := c.pools.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{Id: poolID}.Build())
				g.Expect(grpcstatus.Code(err)).To(Equal(grpccodes.NotFound), "pool %s remains (%v)", poolID, err)
				list := &osacv1alpha1.ExternalIPPoolList{}
				g.Expect(tool.KubeClient().List(ctx, list, crclient.InNamespace(hubNamespace),
					crclient.MatchingLabels{labels.ExternalIPPoolUuid: poolID})).To(Succeed())
				g.Expect(list.Items).To(BeEmpty(), "the ExternalIPPool CR of %s remains", poolID)
			}, opTimeout, time.Second).Should(Succeed())
		})

	It("TC-OP-04 provisions a new tenant's default networking to DefaultNetworkingReady=True/ProvisioningDisabled",
		func(ctx context.Context) {
			tenantID, tenantName := ndCreateTenant(ctx, c)

			By("waiting for the operator and the tenant controller to make every default READY")
			ndExpectDefaultNetworkingCondition(ctx, c, tenantID, privatev1.ConditionStatus_CONDITION_STATUS_TRUE,
				"ProvisioningDisabled")

			By("checking the defaults and that no default ExternalIP or NATGateway exists")
			defaults := fmt.Sprintf("this.metadata.tenant == %q && this.metadata.labels[%q] == 'true'",
				tenantName, ndDefaultLabel)
			vns, err := c.virtualNetworks.List(ctx, privatev1.VirtualNetworksListRequest_builder{
				Filter: &defaults,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(vns.GetItems()).To(HaveLen(1))
			ndLog("Default VirtualNetwork", vns.GetItems()[0])
			Expect(vns.GetItems()[0].GetStatus().GetState()).To(Equal(
				privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY))
			subnets, err := c.subnets.List(ctx, privatev1.SubnetsListRequest_builder{Filter: &defaults}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(subnets.GetItems()).To(HaveLen(1))
			ndLog("Default Subnet", subnets.GetItems()[0])
			Expect(subnets.GetItems()[0].GetStatus().GetState()).To(Equal(privatev1.SubnetState_SUBNET_STATE_READY))
			groups, err := c.securityGroups.List(ctx, privatev1.SecurityGroupsListRequest_builder{
				Filter: &defaults,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(groups.GetItems()).To(HaveLen(1))
			ndLog("Default SecurityGroup", groups.GetItems()[0])
			Expect(groups.GetItems()[0].GetStatus().GetState()).To(Equal(
				privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY))
			filter := fmt.Sprintf("this.metadata.tenant == %q", tenantName)
			ips, err := c.externalIPs.List(ctx, privatev1.ExternalIPsListRequest_builder{Filter: &filter}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(ips.GetItems()).To(BeEmpty())
			gateways, err := c.natGateways.List(ctx, privatev1.NATGatewaysListRequest_builder{
				Filter: &filter,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(gateways.GetItems()).To(BeEmpty())
		})
})
