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
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
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

		// Create a default NetworkClass with defaults so ensureDefaultNetworking fires.
		ncResp, err := networkClassesClient.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
			Object: privatev1.NetworkClass_builder{
				Metadata:               privatev1.Metadata_builder{Name: fmt.Sprintf("test-default-nc-%s", uuid.New())}.Build(),
				Title:                  "Test Default Network Class",
				ImplementationStrategy: "cudn_net",
				FabricManager:          new("cudn_net"),
				IsDefault:              new(true),
				Spec: privatev1.NetworkClassSpec_builder{
					Defaults: privatev1.NetworkDefaults_builder{
						VirtualNetworkIpv4Cidr: "10.200.0.0/16",
						SubnetIpv4Cidr:         "10.200.0.0/20",
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		networkClassId = ncResp.GetObject().GetId()
		DeferCleanup(func() {
			_, _ = networkClassesClient.Delete(ctx, privatev1.NetworkClassesDeleteRequest_builder{
				Id: networkClassId,
			}.Build())
		})
	})

	It("creates K8s CRs for default VN/Subnet/SG and transitions DefaultNetworkingReady to True", func(ctx context.Context) {
		tenantName := fmt.Sprintf("test-defnet-%s", uuid.New())

		By("Creating tenant and waiting for SYNCED")
		tenantId := createTenant(ctx, tenantsClient, tenantName)
		waitForTenantSynced(ctx, tenantsClient, tenantId)

		defaultLabelFilter := fmt.Sprintf(
			"this.metadata.labels['osac.openshift.io/default'] == 'true' && this.metadata.tenant == %q",
			tenantName,
		)

		By("Waiting for DefaultNetworkingReady=False/ResourcesPending (ensureDefaultNetworking ran)")
		Eventually(func(g Gomega) {
			resp, err := tenantsClient.Get(ctx, privatev1.TenantsGetRequest_builder{Id: tenantId}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			cond := findTenantCondition(resp.GetObject().GetStatus().GetConditions(),
				privatev1.TenantConditionType_TENANT_CONDITION_TYPE_DEFAULT_NETWORKING_READY)
			g.Expect(cond).ToNot(BeNil())
			g.Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))
			g.Expect(cond.HasReason()).To(BeTrue())
			g.Expect(cond.GetReason()).To(Equal("ResourcesPending"))
		}, time.Minute, time.Second).Should(Succeed())

		By("Waiting for default VirtualNetwork to appear in FS DB")
		var vnId string
		Eventually(func(g Gomega) {
			resp, err := virtualNetworksClient.List(ctx, privatev1.VirtualNetworksListRequest_builder{
				Filter: &defaultLabelFilter,
			}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(resp.GetItems()).ToNot(BeEmpty())
			vnId = resp.GetItems()[0].GetId()
		}, time.Minute, time.Second).Should(Succeed())

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
})

func findTenantCondition(conditions []*privatev1.TenantCondition, condType privatev1.TenantConditionType) *privatev1.TenantCondition {
	for _, c := range conditions {
		if c.GetType() == condType {
			return c
		}
	}
	return nil
}
