/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package it

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Default networking provisioning", func() {
	var (
		ctx context.Context

		tenantsClient         privatev1.TenantsClient
		networkClassesClient  privatev1.NetworkClassesClient
		virtualNetworksClient privatev1.VirtualNetworksClient
		subnetsClient         privatev1.SubnetsClient
		securityGroupsClient  privatev1.SecurityGroupsClient

		networkClassId string
	)

	BeforeEach(func() {
		ctx = context.Background()

		tenantsClient = privatev1.NewTenantsClient(tool.InternalView().AdminConn())
		networkClassesClient = privatev1.NewNetworkClassesClient(tool.InternalView().AdminConn())
		virtualNetworksClient = privatev1.NewVirtualNetworksClient(tool.InternalView().AdminConn())
		subnetsClient = privatev1.NewSubnetsClient(tool.InternalView().AdminConn())
		securityGroupsClient = privatev1.NewSecurityGroupsClient(tool.InternalView().AdminConn())

		// Create the deployment singleton NetworkClass. The tenant controller
		// asynchronously consumes it after its Hub status becomes READY.
		networkClassId = createDefaultNetworkClass(
			ctx,
			networkClassesClient,
			"test-default-nc",
			"Test Default Network Class",
			"10.200.0.0/16",
			"10.200.0.0/20",
		)
		DeferCleanup(func(cleanupCtx context.Context) {
			if networkClassId == "" {
				return
			}
			deleteAndWaitForComputeInstanceFixtureResource(cleanupCtx,
				func(deleteCtx context.Context) error {
					_, err := networkClassesClient.Delete(deleteCtx, privatev1.NetworkClassesDeleteRequest_builder{
						Id: networkClassId,
					}.Build())
					return err
				},
				func(getCtx context.Context) error {
					_, err := networkClassesClient.Get(getCtx, privatev1.NetworkClassesGetRequest_builder{
						Id: networkClassId,
					}.Build())
					return err
				})
		})
	})

	It("creates K8s CRs for default VN/Subnet/SG and transitions DefaultNetworkingReady to True", func(ctx context.Context) {
		By("Creating tenant and waiting for the default VirtualNetwork")
		tenantId, tenantName, vnId := createTenantAndDefaultVirtualNetwork(ctx, virtualNetworksClient)

		defaultLabelFilter := fmt.Sprintf(
			"this.metadata.labels['osac.openshift.io/default'] == 'true' && this.metadata.tenant == %q",
			tenantName,
		)

		// logVNState logs the current VN state from the FS DB for tracing reconciler progress.
		logVNState := func() {
			if resp, getErr := virtualNetworksClient.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: vnId}.Build()); getErr == nil {
				vn := resp.GetObject()
				GinkgoWriter.Printf("[vn-state] state=%v hub=%q finalizers=%v message=%q\n",
					vn.GetStatus().GetState(), vn.GetStatus().GetHub(),
					vn.GetMetadata().GetFinalizers(), vn.GetStatus().GetMessage())
			}
		}

		By("Waiting for VN finalizer set in DB (pass 1: addFinalizer + Update done)")
		Eventually(func(g Gomega) {
			logVNState()
			resp, err := virtualNetworksClient.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: vnId}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(resp.GetObject().GetMetadata().GetFinalizers()).ToNot(BeEmpty())
		}, time.Minute, time.Second).Should(Succeed())

		By("Waiting for VN hub set in DB (pass 2: selectHub + Update done)")
		Eventually(func(g Gomega) {
			logVNState()
			resp, err := virtualNetworksClient.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: vnId}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(resp.GetObject().GetStatus().GetHub()).ToNot(BeEmpty())
		}, time.Minute, time.Second).Should(Succeed())

		By("Waiting for the NetworkClass canonical Hub to be persisted")
		Eventually(func(g Gomega) {
			resp, err := networkClassesClient.Get(ctx, privatev1.NetworkClassesGetRequest_builder{Id: networkClassId}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(resp.GetObject().GetStatus().GetHub()).To(Equal(hubId))
			g.Expect(resp.GetObject().GetStatus().GetState()).To(
				Equal(privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY))
		}, time.Minute, time.Second).Should(Succeed())

		By("Waiting for VN K8s CR to appear (pass 3: hubClient.Create done)")
		kubeClient := tool.KubeClient()
		vnList := &osacv1alpha1.VirtualNetworkList{}
		Eventually(func(g Gomega) {
			logVNState()
			err := kubeClient.List(ctx, vnList, crclient.MatchingLabels{
				labels.VirtualNetworkUuid: vnId,
			})
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(vnList.Items).To(HaveLen(1))
		}, time.Minute, time.Second).Should(Succeed())

		By("Setting default VirtualNetwork to READY (no osac-operator in IT)")
		vnResp, err := virtualNetworksClient.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: vnId}.Build())
		Expect(err).ToNot(HaveOccurred())
		vnObj := vnResp.GetObject()
		vnObj.SetStatus(privatev1.VirtualNetworkStatus_builder{
			State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY,
		}.Build())
		_, err = virtualNetworksClient.Update(ctx, privatev1.VirtualNetworksUpdateRequest_builder{
			Object:     vnObj,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		By("Waiting for default Subnet to appear in FS DB")
		var subnetId string
		Eventually(func(g Gomega) {
			resp, err := subnetsClient.List(ctx, privatev1.SubnetsListRequest_builder{
				Filter: &defaultLabelFilter,
			}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(resp.GetItems()).ToNot(BeEmpty())
			subnetId = resp.GetItems()[0].GetId()
		}, time.Minute, time.Second).Should(Succeed())

		By("Verifying default Subnet K8s CR is created")
		subnetList := &osacv1alpha1.SubnetList{}
		Eventually(func(g Gomega) {
			err := kubeClient.List(ctx, subnetList, crclient.MatchingLabels{
				labels.SubnetUuid: subnetId,
			})
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(subnetList.Items).To(HaveLen(1))
		}, time.Minute, time.Second).Should(Succeed())

		By("Waiting for default Subnet to reach PENDING state before overriding")
		Eventually(func(g Gomega) {
			resp, err := subnetsClient.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: subnetId}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(resp.GetObject().GetStatus().GetState()).To(
				Equal(privatev1.SubnetState_SUBNET_STATE_PENDING))
		}, time.Minute, time.Second).Should(Succeed())

		By("Setting default Subnet to READY")
		subResp, err := subnetsClient.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: subnetId}.Build())
		Expect(err).ToNot(HaveOccurred())
		subObj := subResp.GetObject()
		subObj.SetStatus(privatev1.SubnetStatus_builder{
			State: privatev1.SubnetState_SUBNET_STATE_READY,
		}.Build())
		_, err = subnetsClient.Update(ctx, privatev1.SubnetsUpdateRequest_builder{
			Object:     subObj,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		By("Waiting for default SecurityGroup to appear in FS DB")
		var sgId string
		Eventually(func(g Gomega) {
			resp, err := securityGroupsClient.List(ctx, privatev1.SecurityGroupsListRequest_builder{
				Filter: &defaultLabelFilter,
			}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(resp.GetItems()).ToNot(BeEmpty())
			sgId = resp.GetItems()[0].GetId()
		}, time.Minute, time.Second).Should(Succeed())

		By("Verifying default SecurityGroup K8s CR is created")
		sgList := &osacv1alpha1.SecurityGroupList{}
		Eventually(func(g Gomega) {
			err := kubeClient.List(ctx, sgList, crclient.MatchingLabels{
				labels.SecurityGroupUuid: sgId,
			})
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(sgList.Items).To(HaveLen(1))
		}, time.Minute, time.Second).Should(Succeed())

		By("Waiting for default SecurityGroup to reach PENDING state before overriding")
		Eventually(func(g Gomega) {
			resp, err := securityGroupsClient.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: sgId}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(resp.GetObject().GetStatus().GetState()).To(
				Equal(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_PENDING))
		}, time.Minute, time.Second).Should(Succeed())

		By("Setting default SecurityGroup to READY")
		sgResp, err := securityGroupsClient.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: sgId}.Build())
		Expect(err).ToNot(HaveOccurred())
		sgObj := sgResp.GetObject()
		sgObj.SetStatus(privatev1.SecurityGroupStatus_builder{
			State: privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY,
		}.Build())
		_, err = securityGroupsClient.Update(ctx, privatev1.SecurityGroupsUpdateRequest_builder{
			Object:     sgObj,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		By("Waiting for DefaultNetworkingReady=True/AllResourcesReady")
		Eventually(func(g Gomega) {
			resp, err := tenantsClient.Get(ctx, privatev1.TenantsGetRequest_builder{Id: tenantId}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			cond := findTenantCondition(resp.GetObject().GetStatus().GetConditions(),
				privatev1.TenantConditionType_TENANT_CONDITION_TYPE_DEFAULT_NETWORKING_READY)
			g.Expect(cond).ToNot(BeNil())
			g.Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
			g.Expect(cond.HasReason()).To(BeTrue())
			g.Expect(cond.GetReason()).To(Equal("AllResourcesReady"))
		}, time.Minute, time.Second).Should(Succeed())
	})

	It("waits for every default networking dependency before creating NAT and reporting ready", func(ctx context.Context) {
		By("Enabling NAT on the singleton NetworkClass")
		ncResponse, err := networkClassesClient.Get(ctx, privatev1.NetworkClassesGetRequest_builder{Id: networkClassId}.Build())
		Expect(err).ToNot(HaveOccurred())
		nc := ncResponse.GetObject()
		nc.GetSpec().GetDefaults().SetEnableNatGateway(true)
		_, err = networkClassesClient.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
			Object: nc, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.defaults"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		waitForNetworkClassReady(ctx, networkClassesClient, networkClassId)

		poolsClient := privatev1.NewExternalIPPoolsClient(tool.InternalView().AdminConn())
		externalIPsClient := privatev1.NewExternalIPsClient(tool.InternalView().AdminConn())
		natGatewaysClient := privatev1.NewNATGatewaysClient(tool.InternalView().AdminConn())
		poolID := fmt.Sprintf("test-default-nat-pool-%s", uuid.New())
		var tenantID string
		_, err = poolsClient.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
			Object: privatev1.ExternalIPPool_builder{
				Id: poolID,
				Metadata: privatev1.Metadata_builder{
					Name: fmt.Sprintf("test-default-nat-pool-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: privatev1.ExternalIPPoolSpec_builder{
					Cidrs: []string{uniqueCIDR()}, IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		// Registered before tenant creation: DeferCleanup removes tenant/default
		// resources first, then this pool, then the outer NetworkClass.
		DeferCleanup(func(cleanupCtx context.Context) {
			if tenantID != "" {
				Eventually(func(g Gomega) {
					_, getErr := tenantsClient.Get(cleanupCtx, privatev1.TenantsGetRequest_builder{Id: tenantID}.Build())
					g.Expect(grpcstatus.Code(getErr)).To(Equal(grpccodes.NotFound))
				}, 2*time.Minute, time.Second).Should(Succeed())
			}
			deleteAndWaitForComputeInstanceFixtureResource(cleanupCtx,
				func(deleteCtx context.Context) error {
					_, deleteErr := poolsClient.Delete(deleteCtx, privatev1.ExternalIPPoolsDeleteRequest_builder{Id: poolID}.Build())
					return deleteErr
				},
				func(getCtx context.Context) error {
					_, getErr := poolsClient.Get(getCtx, privatev1.ExternalIPPoolsGetRequest_builder{Id: poolID}.Build())
					return getErr
				})
		})
		Eventually(func(g Gomega) {
			response, getErr := poolsClient.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{Id: poolID}.Build())
			g.Expect(getErr).ToNot(HaveOccurred())
			g.Expect(response.GetObject().GetStatus().GetState()).To(Equal(privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_PENDING))
		}, time.Minute, time.Second).Should(Succeed())
		poolResponse, err := poolsClient.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{Id: poolID}.Build())
		Expect(err).ToNot(HaveOccurred())
		pool := poolResponse.GetObject()
		pool.SetStatus(privatev1.ExternalIPPoolStatus_builder{
			State: privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY,
			Total: pool.GetStatus().GetTotal(), Available: pool.GetStatus().GetAvailable(),
			Allocated: pool.GetStatus().GetAllocated(),
		}.Build())
		_, err = poolsClient.Update(ctx, privatev1.ExternalIPPoolsUpdateRequest_builder{
			Object: pool, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		var tenantName, vnID string
		tenantID, tenantName, vnID = createTenantAndDefaultVirtualNetwork(ctx, virtualNetworksClient)
		filter := fmt.Sprintf("this.metadata.labels['osac.openshift.io/default'] == 'true' && this.metadata.tenant == %q", tenantName)
		listSubnets := func(g Gomega) []*privatev1.Subnet {
			response, listErr := subnetsClient.List(ctx, privatev1.SubnetsListRequest_builder{Filter: &filter}.Build())
			g.Expect(listErr).ToNot(HaveOccurred())
			return response.GetItems()
		}
		listGroups := func(g Gomega) []*privatev1.SecurityGroup {
			response, listErr := securityGroupsClient.List(ctx, privatev1.SecurityGroupsListRequest_builder{Filter: &filter}.Build())
			g.Expect(listErr).ToNot(HaveOccurred())
			return response.GetItems()
		}
		listIPs := func(g Gomega) []*privatev1.ExternalIP {
			response, listErr := externalIPsClient.List(ctx, privatev1.ExternalIPsListRequest_builder{Filter: &filter}.Build())
			g.Expect(listErr).ToNot(HaveOccurred())
			return response.GetItems()
		}
		listGateways := func(g Gomega) []*privatev1.NATGateway {
			response, listErr := natGatewaysClient.List(ctx, privatev1.NATGatewaysListRequest_builder{Filter: &filter}.Build())
			g.Expect(listErr).ToNot(HaveOccurred())
			return response.GetItems()
		}
		expectNotReady := func(g Gomega) {
			response, getErr := tenantsClient.Get(ctx, privatev1.TenantsGetRequest_builder{Id: tenantID}.Build())
			g.Expect(getErr).ToNot(HaveOccurred())
			condition := findTenantCondition(response.GetObject().GetStatus().GetConditions(),
				privatev1.TenantConditionType_TENANT_CONDITION_TYPE_DEFAULT_NETWORKING_READY)
			g.Expect(condition).ToNot(BeNil())
			g.Expect(condition.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))
		}

		By("Waiting for the default VN CR while Subnets and SecurityGroup remain absent")
		Eventually(func(g Gomega) {
			response, getErr := virtualNetworksClient.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: vnID}.Build())
			g.Expect(getErr).ToNot(HaveOccurred())
			g.Expect(response.GetObject().GetStatus().GetState()).To(Equal(privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING))
			vnList := &osacv1alpha1.VirtualNetworkList{}
			g.Expect(tool.KubeClient().List(ctx, vnList, crclient.MatchingLabels{labels.VirtualNetworkUuid: vnID})).To(Succeed())
			g.Expect(vnList.Items).To(HaveLen(1))
		}, time.Minute, time.Second).Should(Succeed())
		Eventually(expectNotReady, time.Minute, time.Second).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(listSubnets(g)).To(BeEmpty())
			g.Expect(listGroups(g)).To(BeEmpty())
			g.Expect(listIPs(g)).To(BeEmpty())
			g.Expect(listGateways(g)).To(BeEmpty())
			expectNotReady(g)
		}, 5*time.Second, time.Second).Should(Succeed())

		vnResponse, err := virtualNetworksClient.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: vnID}.Build())
		Expect(err).ToNot(HaveOccurred())
		vn := vnResponse.GetObject()
		vn.SetStatus(privatev1.VirtualNetworkStatus_builder{State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY}.Build())
		_, err = virtualNetworksClient.Update(ctx, privatev1.VirtualNetworksUpdateRequest_builder{
			Object: vn, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		By("Waiting for the default Subnet and SecurityGroup, with no ExternalIP yet")
		var subnetIDs map[string]string
		var groupID string
		Eventually(func(g Gomega) {
			subnets := listSubnets(g)
			g.Expect(subnets).To(HaveLen(1))
			subnetIDs = make(map[string]string, 1)
			for _, subnet := range subnets {
				g.Expect(subnet.GetStatus().GetState()).To(Equal(privatev1.SubnetState_SUBNET_STATE_PENDING))
				subnetIDs[subnet.GetMetadata().GetName()] = subnet.GetId()
			}
			g.Expect(subnetIDs).To(HaveKey("default-ipv4"))
			groups := listGroups(g)
			g.Expect(groups).To(HaveLen(1))
			g.Expect(groups[0].GetStatus().GetState()).To(Equal(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_PENDING))
			groupID = groups[0].GetId()
		}, time.Minute, time.Second).Should(Succeed())
		for _, subnetID := range subnetIDs {
			Eventually(func(g Gomega) {
				crs := &osacv1alpha1.SubnetList{}
				g.Expect(tool.KubeClient().List(ctx, crs, crclient.MatchingLabels{labels.SubnetUuid: subnetID})).To(Succeed())
				g.Expect(crs.Items).To(HaveLen(1))
			}, time.Minute, time.Second).Should(Succeed())
		}
		Eventually(func(g Gomega) {
			crs := &osacv1alpha1.SecurityGroupList{}
			g.Expect(tool.KubeClient().List(ctx, crs, crclient.MatchingLabels{labels.SecurityGroupUuid: groupID})).To(Succeed())
			g.Expect(crs.Items).To(HaveLen(1))
		}, time.Minute, time.Second).Should(Succeed())
		setSubnetReady := func(id string) {
			response, getErr := subnetsClient.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: id}.Build())
			Expect(getErr).ToNot(HaveOccurred())
			object := response.GetObject()
			object.SetStatus(privatev1.SubnetStatus_builder{State: privatev1.SubnetState_SUBNET_STATE_READY}.Build())
			_, updateErr := subnetsClient.Update(ctx, privatev1.SubnetsUpdateRequest_builder{
				Object: object, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
			}.Build())
			Expect(updateErr).ToNot(HaveOccurred())
		}
		setSubnetReady(subnetIDs["default-ipv4"])
		Consistently(func(g Gomega) {
			g.Expect(listSubnets(g)).To(HaveLen(1))
			g.Expect(listGroups(g)).To(HaveLen(1))
			g.Expect(listIPs(g)).To(BeEmpty())
			g.Expect(listGateways(g)).To(BeEmpty())
			expectNotReady(g)
		}, 5*time.Second, time.Second).Should(Succeed())
		groupResponse, err := securityGroupsClient.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: groupID}.Build())
		Expect(err).ToNot(HaveOccurred())
		group := groupResponse.GetObject()
		group.SetStatus(privatev1.SecurityGroupStatus_builder{State: privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY}.Build())
		_, err = securityGroupsClient.Update(ctx, privatev1.SecurityGroupsUpdateRequest_builder{
			Object: group, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		By("Waiting for one ExternalIP while NATGateway remains absent")
		var externalIPID string
		Eventually(func(g Gomega) {
			ips := listIPs(g)
			g.Expect(ips).To(HaveLen(1))
			g.Expect(ips[0].GetStatus().GetState()).To(Equal(privatev1.ExternalIPState_EXTERNAL_IP_STATE_PENDING))
			g.Expect(ips[0].GetSpec().GetPool().GetId()).To(Equal(poolID))
			externalIPID = ips[0].GetId()
			g.Expect(listGateways(g)).To(BeEmpty())
			expectNotReady(g)
		}, time.Minute, time.Second).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(listIPs(g)).To(HaveLen(1))
			g.Expect(listGateways(g)).To(BeEmpty())
			expectNotReady(g)
		}, 5*time.Second, time.Second).Should(Succeed())
		ipResponse, err := externalIPsClient.Get(ctx, privatev1.ExternalIPsGetRequest_builder{Id: externalIPID}.Build())
		Expect(err).ToNot(HaveOccurred())
		ip := ipResponse.GetObject()
		ip.SetStatus(privatev1.ExternalIPStatus_builder{State: privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED}.Build())
		_, err = externalIPsClient.Update(ctx, privatev1.ExternalIPsUpdateRequest_builder{
			Object: ip, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		By("Waiting for NATGateway and keeping readiness false until it is READY")
		var gatewayID string
		Eventually(func(g Gomega) {
			gateways := listGateways(g)
			g.Expect(gateways).To(HaveLen(1))
			g.Expect(gateways[0].GetStatus().GetState()).To(Equal(privatev1.NATGatewayState_NAT_GATEWAY_STATE_PENDING))
			g.Expect(gateways[0].GetSpec().GetExternalIp().GetId()).To(Equal(externalIPID))
			gatewayID = gateways[0].GetId()
			expectNotReady(g)
		}, time.Minute, time.Second).Should(Succeed())

		By("Signaling another tenant reconciliation while NATGateway remains PENDING")
		tenantResponse, err := tenantsClient.Get(ctx, privatev1.TenantsGetRequest_builder{Id: tenantID}.Build())
		Expect(err).ToNot(HaveOccurred())
		tenant := tenantResponse.GetObject()
		condition := findTenantCondition(tenant.GetStatus().GetConditions(),
			privatev1.TenantConditionType_TENANT_CONDITION_TYPE_DEFAULT_NETWORKING_READY)
		Expect(condition).ToNot(BeNil())
		condition.SetReason("ReconciliationRequested")
		_, err = tenantsClient.Update(ctx, privatev1.TenantsUpdateRequest_builder{
			Object: tenant, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.conditions"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		_, err = tenantsClient.Signal(ctx, privatev1.TenantsSignalRequest_builder{Id: tenantID}.Build())
		Expect(err).ToNot(HaveOccurred())
		expectPendingResources := func(g Gomega) {
			response, getErr := tenantsClient.Get(ctx, privatev1.TenantsGetRequest_builder{Id: tenantID}.Build())
			g.Expect(getErr).ToNot(HaveOccurred())
			condition := findTenantCondition(response.GetObject().GetStatus().GetConditions(),
				privatev1.TenantConditionType_TENANT_CONDITION_TYPE_DEFAULT_NETWORKING_READY)
			g.Expect(condition).ToNot(BeNil())
			g.Expect(condition.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))
			g.Expect(condition.GetReason()).To(Equal("ResourcesPending"))
			vns, listErr := virtualNetworksClient.List(ctx, privatev1.VirtualNetworksListRequest_builder{Filter: &filter}.Build())
			g.Expect(listErr).ToNot(HaveOccurred())
			g.Expect(vns.GetItems()).To(HaveLen(1))
			g.Expect(vns.GetItems()[0].GetId()).To(Equal(vnID))
			subnets := listSubnets(g)
			g.Expect(subnets).To(HaveLen(1))
			for _, subnet := range subnets {
				g.Expect(subnetIDs).To(HaveKeyWithValue(subnet.GetMetadata().GetName(), subnet.GetId()))
			}
			groups := listGroups(g)
			g.Expect(groups).To(HaveLen(1))
			g.Expect(groups[0].GetId()).To(Equal(groupID))
			ips := listIPs(g)
			g.Expect(ips).To(HaveLen(1))
			g.Expect(ips[0].GetId()).To(Equal(externalIPID))
			gateways := listGateways(g)
			g.Expect(gateways).To(HaveLen(1))
			g.Expect(gateways[0].GetId()).To(Equal(gatewayID))
			g.Expect(gateways[0].GetStatus().GetState()).To(Equal(privatev1.NATGatewayState_NAT_GATEWAY_STATE_PENDING))
		}
		Eventually(expectPendingResources, time.Minute, time.Second).Should(Succeed())
		Consistently(expectPendingResources, 5*time.Second, time.Second).Should(Succeed())

		gatewayResponse, err := natGatewaysClient.Get(ctx, privatev1.NATGatewaysGetRequest_builder{Id: gatewayID}.Build())
		Expect(err).ToNot(HaveOccurred())
		gateway := gatewayResponse.GetObject()
		gateway.SetStatus(privatev1.NATGatewayStatus_builder{State: privatev1.NATGatewayState_NAT_GATEWAY_STATE_READY}.Build())
		_, err = natGatewaysClient.Update(ctx, privatev1.NATGatewaysUpdateRequest_builder{
			Object: gateway, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Eventually(func(g Gomega) {
			response, getErr := tenantsClient.Get(ctx, privatev1.TenantsGetRequest_builder{Id: tenantID}.Build())
			g.Expect(getErr).ToNot(HaveOccurred())
			condition := findTenantCondition(response.GetObject().GetStatus().GetConditions(),
				privatev1.TenantConditionType_TENANT_CONDITION_TYPE_DEFAULT_NETWORKING_READY)
			g.Expect(condition).ToNot(BeNil())
			g.Expect(condition.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
			g.Expect(condition.GetReason()).To(Equal("AllResourcesReady"))
		}, time.Minute, time.Second).Should(Succeed())
		Consistently(func(g Gomega) {
			response, listErr := virtualNetworksClient.List(ctx, privatev1.VirtualNetworksListRequest_builder{Filter: &filter}.Build())
			g.Expect(listErr).ToNot(HaveOccurred())
			g.Expect(response.GetItems()).To(HaveLen(1))
			g.Expect(listSubnets(g)).To(HaveLen(1))
			g.Expect(listGroups(g)).To(HaveLen(1))
			g.Expect(listIPs(g)).To(HaveLen(1))
			g.Expect(listGateways(g)).To(HaveLen(1))
		}, 5*time.Second, time.Second).Should(Succeed())
	})

	It("sets DefaultNetworkingReady=True/NoDefaultNetworking when no default NetworkClass has defaults", func(ctx context.Context) {
		_, err := networkClassesClient.Delete(ctx, privatev1.NetworkClassesDeleteRequest_builder{
			Id: networkClassId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		networkClassId = ""

		tenantName := fmt.Sprintf("test-nodefnet-%s", uuid.New())

		By("Creating tenant and waiting for SYNCED")
		tenantId := createTenant(ctx, tenantsClient, tenantName)
		waitForTenantSynced(ctx, tenantsClient, tenantId)

		By("Waiting for DefaultNetworkingReady=True/NoDefaultNetworking")
		Eventually(func(g Gomega) {
			resp, err := tenantsClient.Get(ctx, privatev1.TenantsGetRequest_builder{Id: tenantId}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			cond := findTenantCondition(resp.GetObject().GetStatus().GetConditions(),
				privatev1.TenantConditionType_TENANT_CONDITION_TYPE_DEFAULT_NETWORKING_READY)
			g.Expect(cond).ToNot(BeNil())
			g.Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
			g.Expect(cond.HasReason()).To(BeTrue())
			g.Expect(cond.GetReason()).To(Equal("NoDefaultNetworking"))
		}, time.Minute, time.Second).Should(Succeed())
	})

	It("rejects Delete and default-label removal for system-managed VN/Subnet/SG", func(ctx context.Context) {
		tenantName := fmt.Sprintf("test-defnet-protect-%s", uuid.New())

		By("Creating tenant and waiting for SYNCED")
		tenantId := createTenant(ctx, tenantsClient, tenantName)
		waitForTenantSynced(ctx, tenantsClient, tenantId)

		defaultLabelFilter := fmt.Sprintf(
			"this.metadata.labels['osac.openshift.io/default'] == 'true' && this.metadata.tenant == %q",
			tenantName,
		)

		// Subnet/SG Create requires the parent VN to be READY. IT has no
		// osac-operator, so force READY before waiting for child defaults —
		// same pattern as the provisioning happy-path spec above.
		By("Waiting for default VirtualNetwork")
		var vnId string
		Eventually(func(g Gomega) {
			vnResp, err := virtualNetworksClient.List(ctx, privatev1.VirtualNetworksListRequest_builder{
				Filter: &defaultLabelFilter,
			}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(vnResp.GetItems()).ToNot(BeEmpty())
			vnId = vnResp.GetItems()[0].GetId()
		}, time.Minute, time.Second).Should(Succeed())

		By("Setting default VirtualNetwork to READY (no osac-operator in IT)")
		vnResp, err := virtualNetworksClient.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: vnId}.Build())
		Expect(err).ToNot(HaveOccurred())
		vnObj := vnResp.GetObject()
		vnObj.SetStatus(privatev1.VirtualNetworkStatus_builder{
			State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY,
		}.Build())
		_, err = virtualNetworksClient.Update(ctx, privatev1.VirtualNetworksUpdateRequest_builder{
			Object:     vnObj,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		By("Waiting for default Subnet and SecurityGroup")
		var subnetId, sgId string
		Eventually(func(g Gomega) {
			subnetResp, err := subnetsClient.List(ctx, privatev1.SubnetsListRequest_builder{
				Filter: &defaultLabelFilter,
			}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(subnetResp.GetItems()).ToNot(BeEmpty())
			subnetId = subnetResp.GetItems()[0].GetId()

			sgResp, err := securityGroupsClient.List(ctx, privatev1.SecurityGroupsListRequest_builder{
				Filter: &defaultLabelFilter,
			}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(sgResp.GetItems()).ToNot(BeEmpty())
			sgId = sgResp.GetItems()[0].GetId()
		}, time.Minute, time.Second).Should(Succeed())

		vnBefore, err := virtualNetworksClient.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: vnId}.Build())
		Expect(err).ToNot(HaveOccurred())
		subnetBefore, err := subnetsClient.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: subnetId}.Build())
		Expect(err).ToNot(HaveOccurred())
		sgBefore, err := securityGroupsClient.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: sgId}.Build())
		Expect(err).ToNot(HaveOccurred())

		By("Rejecting Delete of default VirtualNetwork without entering PENDING/deletion")
		_, err = virtualNetworksClient.Delete(ctx, privatev1.VirtualNetworksDeleteRequest_builder{Id: vnId}.Build())
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
		Expect(err.Error()).To(And(ContainSubstring("default"), ContainSubstring("system-managed")))
		vnAfter, err := virtualNetworksClient.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: vnId}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(vnAfter.GetObject().GetMetadata().GetDeletionTimestamp()).To(BeNil())
		Expect(vnAfter.GetObject().GetStatus().GetState()).To(Equal(vnBefore.GetObject().GetStatus().GetState()))

		By("Rejecting Delete of default Subnet without entering PENDING/deletion")
		_, err = subnetsClient.Delete(ctx, privatev1.SubnetsDeleteRequest_builder{Id: subnetId}.Build())
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
		Expect(err.Error()).To(And(ContainSubstring("default"), ContainSubstring("system-managed")))
		subnetAfter, err := subnetsClient.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: subnetId}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(subnetAfter.GetObject().GetMetadata().GetDeletionTimestamp()).To(BeNil())
		Expect(subnetAfter.GetObject().GetStatus().GetState()).To(Equal(subnetBefore.GetObject().GetStatus().GetState()))

		By("Rejecting Delete of default SecurityGroup without entering PENDING/deletion")
		_, err = securityGroupsClient.Delete(ctx, privatev1.SecurityGroupsDeleteRequest_builder{Id: sgId}.Build())
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
		Expect(err.Error()).To(And(ContainSubstring("default"), ContainSubstring("system-managed")))
		sgAfter, err := securityGroupsClient.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: sgId}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(sgAfter.GetObject().GetMetadata().GetDeletionTimestamp()).To(BeNil())
		Expect(sgAfter.GetObject().GetStatus().GetState()).To(Equal(sgBefore.GetObject().GetStatus().GetState()))

		By("Rejecting Update that strips the default label from VirtualNetwork")
		vnObj = vnAfter.GetObject()
		vnObj.GetMetadata().SetLabels(map[string]string{"env": "test"})
		_, err = virtualNetworksClient.Update(ctx, privatev1.VirtualNetworksUpdateRequest_builder{
			Object:     vnObj,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.labels"}},
		}.Build())
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
		Expect(err.Error()).To(And(ContainSubstring("default"), ContainSubstring("system-managed")))

		vnFinal, err := virtualNetworksClient.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: vnId}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(vnFinal.GetObject().GetMetadata().GetLabels()).To(HaveKeyWithValue("osac.openshift.io/default", "true"))
		Expect(vnFinal.GetObject().GetMetadata().GetDeletionTimestamp()).To(BeNil())
	})
})

func findTenantCondition(conditions []*privatev1.TenantCondition, condType privatev1.TenantConditionType) *privatev1.TenantCondition {
	for _, c := range conditions {
		if c.GetType() == condType {
			return c
		}
	}
	return nil
}

func createDefaultNetworkClass(
	ctx context.Context,
	client privatev1.NetworkClassesClient,
	namePrefix string,
	title string,
	virtualNetworkCIDR string,
	subnetCIDR string,
) string {
	response, err := client.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
		Object: privatev1.NetworkClass_builder{
			Metadata:      privatev1.Metadata_builder{Name: fmt.Sprintf("%s-%s", namePrefix, uuid.New())}.Build(),
			Title:         title,
			FabricManager: new("cudn_net"),
			Spec: privatev1.NetworkClassSpec_builder{
				Defaults: privatev1.NetworkDefaults_builder{
					VirtualNetworkIpv4Cidr: virtualNetworkCIDR,
					SubnetIpv4Cidr:         subnetCIDR,
				}.Build(),
			}.Build(),
		}.Build(),
	}.Build())
	Expect(err).ToNot(HaveOccurred())
	return response.GetObject().GetId()
}

var _ = Describe("Canonical networking Hub resolution", func() {
	var (
		ctx                   context.Context
		networkClassesClient  privatev1.NetworkClassesClient
		virtualNetworksClient privatev1.VirtualNetworksClient
		hubsClient            privatev1.HubsClient
		networkClassID        string
	)

	BeforeEach(func() {
		ctx = context.Background()
		networkClassesClient = privatev1.NewNetworkClassesClient(tool.InternalView().AdminConn())
		virtualNetworksClient = privatev1.NewVirtualNetworksClient(tool.InternalView().AdminConn())
		hubsClient = privatev1.NewHubsClient(tool.InternalView().AdminConn())

		networkClassID = createDefaultNetworkClass(
			ctx,
			networkClassesClient,
			"test-canonical-nc",
			"Test Canonical Network Class",
			"10.220.0.0/16",
			"10.220.0.0/20",
		)
		DeferCleanup(func(cleanupCtx context.Context) {
			Expect(cleanupNetworkClassFixture(cleanupCtx, networkClassesClient, networkClassID)).To(Succeed())
		})
	})

	It("keeps a multiple-Hub deployment pending without selecting a Hub", func(ctx context.Context) {
		createTestHub(ctx, hubsClient, fmt.Sprintf("additional-hub-%s", uuid.New()))

		expectNetworkClassStatus(
			ctx,
			networkClassesClient,
			networkClassID,
			privatev1.NetworkClassState_NETWORK_CLASS_STATE_PENDING,
			"",
			"expected exactly one active networking hub, found multiple",
		)
		tenantID, tenantName := createTenantForDefaultNetworking(ctx)
		expectTenantDefaultNetworkingBlocked(ctx, tenantID, tenantName, virtualNetworksClient)
	})

	It("retries default networking when the canonical Hub becomes available", func(ctx context.Context) {
		additionalHubID := fmt.Sprintf("additional-hub-%s", uuid.New())
		createTestHub(ctx, hubsClient, additionalHubID)

		expectNetworkClassStatus(
			ctx,
			networkClassesClient,
			networkClassID,
			privatev1.NetworkClassState_NETWORK_CLASS_STATE_PENDING,
			"",
			"expected exactly one active networking hub, found multiple",
		)
		tenantID, tenantName := createTenantForDefaultNetworking(ctx)
		expectTenantDefaultNetworkingBlocked(ctx, tenantID, tenantName, virtualNetworksClient)

		By("Removing the extra Hub and waiting for NetworkClass reconciliation")
		_, err := hubsClient.Delete(ctx, privatev1.HubsDeleteRequest_builder{Id: additionalHubID}.Build())
		Expect(err).ToNot(HaveOccurred())
		Eventually(func(g Gomega) {
			filter := "!has(this.metadata.deletion_timestamp)"
			response, listErr := hubsClient.List(ctx, privatev1.HubsListRequest_builder{
				Filter: &filter,
				Limit:  new(int32(2)),
			}.Build())
			g.Expect(listErr).ToNot(HaveOccurred())
			g.Expect(response.GetItems()).To(HaveLen(1))
			g.Expect(response.GetItems()[0].GetId()).To(Equal(hubId))
		}, time.Minute, time.Second).Should(Succeed())

		expectNetworkClassStatus(
			ctx,
			networkClassesClient,
			networkClassID,
			privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
			hubId,
			"",
		)
		Eventually(func(g Gomega) {
			filter := fmt.Sprintf("this.metadata.tenant == %q", tenantName)
			response, listErr := virtualNetworksClient.List(ctx, privatev1.VirtualNetworksListRequest_builder{Filter: &filter}.Build())
			g.Expect(listErr).ToNot(HaveOccurred())
			g.Expect(response.GetItems()).To(HaveLen(1))
			g.Expect(response.GetItems()[0].GetStatus().GetHub()).To(Equal(hubId))
		}, time.Minute, time.Second).Should(Succeed())
	})

	It("keeps default networking blocked when the canonical reference is invalid", func(ctx context.Context) {
		setNetworkClassCanonicalHub(ctx, networkClassesClient, networkClassID, "missing-canonical-hub")

		expectNetworkClassStatus(
			ctx,
			networkClassesClient,
			networkClassID,
			privatev1.NetworkClassState_NETWORK_CLASS_STATE_FAILED,
			"missing-canonical-hub",
			`canonical networking hub "missing-canonical-hub" is not registered`,
		)
		tenantID, tenantName := createTenantForDefaultNetworking(ctx)
		expectTenantDefaultNetworkingBlocked(ctx, tenantID, tenantName, virtualNetworksClient)
	})

	It("keeps default networking blocked when the canonical Hub is unavailable", func(ctx context.Context) {
		unavailableHubID := fmt.Sprintf("unavailable-hub-%s", uuid.New())
		createTestHub(ctx, hubsClient, unavailableHubID)

		setNetworkClassCanonicalHub(ctx, networkClassesClient, networkClassID, unavailableHubID)

		expectNetworkClassStatus(
			ctx,
			networkClassesClient,
			networkClassID,
			privatev1.NetworkClassState_NETWORK_CLASS_STATE_PENDING,
			unavailableHubID,
			fmt.Sprintf(`canonical networking hub %q is unavailable`, unavailableHubID),
		)
		tenantID, tenantName := createTenantForDefaultNetworking(ctx)
		expectTenantDefaultNetworkingBlocked(ctx, tenantID, tenantName, virtualNetworksClient)
	})
})

func expectNetworkClassStatus(
	ctx context.Context,
	client privatev1.NetworkClassesClient,
	id string,
	state privatev1.NetworkClassState,
	hubID string,
	message string,
) {
	Eventually(func(g Gomega) {
		response, err := client.Get(ctx, privatev1.NetworkClassesGetRequest_builder{Id: id}.Build())
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(response.GetObject().GetStatus().GetHub()).To(Equal(hubID))
		g.Expect(response.GetObject().GetStatus().GetState()).To(Equal(state))
		g.Expect(response.GetObject().GetStatus().GetMessage()).To(Equal(message))
	}, time.Minute, time.Second).Should(Succeed())
}

func expectTenantDefaultNetworkingBlocked(
	ctx context.Context,
	tenantID, tenantName string,
	virtualNetworksClient privatev1.VirtualNetworksClient,
) {
	tenantsClient := privatev1.NewTenantsClient(tool.InternalView().AdminConn())
	Eventually(func(g Gomega) {
		response, err := tenantsClient.Get(ctx, privatev1.TenantsGetRequest_builder{Id: tenantID}.Build())
		g.Expect(err).ToNot(HaveOccurred())
		condition := findTenantCondition(response.GetObject().GetStatus().GetConditions(),
			privatev1.TenantConditionType_TENANT_CONDITION_TYPE_DEFAULT_NETWORKING_READY)
		g.Expect(condition).ToNot(BeNil())
		g.Expect(condition.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))
	}, time.Minute, time.Second).Should(Succeed())
	filter := fmt.Sprintf("this.metadata.tenant == %q", tenantName)
	Consistently(func(g Gomega) {
		response, err := virtualNetworksClient.List(ctx, privatev1.VirtualNetworksListRequest_builder{Filter: &filter}.Build())
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(response.GetItems()).To(BeEmpty())
	}, 3*time.Second, time.Second).Should(Succeed())
}

func createTestHub(ctx context.Context, hubsClient privatev1.HubsClient, id string) {
	_, err := hubsClient.Create(ctx, privatev1.HubsCreateRequest_builder{
		Object: privatev1.Hub_builder{
			Id:       id,
			Metadata: privatev1.Metadata_builder{Name: id}.Build(),
			Spec: privatev1.HubSpec_builder{
				Kubeconfig: []byte("not-a-kubeconfig"),
				Namespace:  hubNamespace,
			}.Build(),
		}.Build(),
	}.Build())
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func(cleanupCtx context.Context) {
		_, _ = hubsClient.Delete(cleanupCtx, privatev1.HubsDeleteRequest_builder{Id: id}.Build())
	})
}

func setNetworkClassCanonicalHub(ctx context.Context, client privatev1.NetworkClassesClient, id, hubID string) {
	Eventually(func(g Gomega) {
		response, err := client.Get(ctx, privatev1.NetworkClassesGetRequest_builder{Id: id}.Build())
		g.Expect(err).ToNot(HaveOccurred())
		networkClass := response.GetObject()
		if !networkClass.HasStatus() {
			networkClass.SetStatus(&privatev1.NetworkClassStatus{})
		}
		networkClass.GetStatus().SetHub(hubID)
		_, err = client.Update(ctx, privatev1.NetworkClassesUpdateRequest_builder{
			Object: networkClass,
			UpdateMask: &fieldmaskpb.FieldMask{
				Paths: []string{"status.hub"},
			},
			Lock: true,
		}.Build())
		g.Expect(err).ToNot(HaveOccurred())
	}, time.Minute, time.Second).Should(Succeed())
}

func createTenantAndDefaultVirtualNetwork(
	ctx context.Context,
	virtualNetworksClient privatev1.VirtualNetworksClient,
) (string, string, string) {
	tenantID, tenantName := createTenantForDefaultNetworking(ctx)
	filter := fmt.Sprintf("this.metadata.tenant == %q", tenantName)
	var virtualNetworkID string
	Eventually(func(g Gomega) {
		response, err := virtualNetworksClient.List(ctx, privatev1.VirtualNetworksListRequest_builder{Filter: &filter}.Build())
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(response.GetItems()).ToNot(BeEmpty())
		virtualNetworkID = response.GetItems()[0].GetId()
	}, time.Minute, time.Second).Should(Succeed())
	return tenantID, tenantName, virtualNetworkID
}

func createTenantForDefaultNetworking(ctx context.Context) (string, string) {
	tenantsClient := privatev1.NewTenantsClient(tool.InternalView().AdminConn())
	tenantName := fmt.Sprintf("test-canonical-tenant-%s", uuid.New())
	tenantID := createTenant(ctx, tenantsClient, tenantName)
	projectsClient := privatev1.NewProjectsClient(tool.InternalView().AdminConn())
	DeferCleanup(func(cleanupCtx context.Context) {
		deleteTenant(cleanupCtx, tenantsClient, projectsClient, tenantID, tenantName)
	})
	waitForTenantSynced(ctx, tenantsClient, tenantID)
	return tenantID, tenantName
}
