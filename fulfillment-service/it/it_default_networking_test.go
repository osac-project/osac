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
		DeferCleanup(func() {
			_, _ = networkClassesClient.Delete(ctx, privatev1.NetworkClassesDeleteRequest_builder{Id: networkClassID}.Build())
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
		_, _, vnID := createTenantAndDefaultVirtualNetwork(ctx, virtualNetworksClient)
		expectVirtualNetworkWithoutHub(ctx, virtualNetworksClient, vnID)
	})

	It("retries a pending tenant resource when the canonical Hub becomes available", func(ctx context.Context) {
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
		_, _, vnID := createTenantAndDefaultVirtualNetwork(ctx, virtualNetworksClient)
		expectVirtualNetworkWithoutHub(ctx, virtualNetworksClient, vnID)

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
			response, getErr := virtualNetworksClient.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: vnID}.Build())
			g.Expect(getErr).ToNot(HaveOccurred())
			g.Expect(response.GetObject().GetStatus().GetHub()).To(Equal(hubId))
		}, time.Minute, time.Second).Should(Succeed())
	})

	It("keeps a tenant resource pending when the canonical reference is invalid", func(ctx context.Context) {
		setNetworkClassCanonicalHub(ctx, networkClassesClient, networkClassID, "missing-canonical-hub")

		expectNetworkClassStatus(
			ctx,
			networkClassesClient,
			networkClassID,
			privatev1.NetworkClassState_NETWORK_CLASS_STATE_FAILED,
			"missing-canonical-hub",
			`canonical networking hub "missing-canonical-hub" is not registered`,
		)
		_, _, vnID := createTenantAndDefaultVirtualNetwork(ctx, virtualNetworksClient)
		expectVirtualNetworkWithoutHub(ctx, virtualNetworksClient, vnID)
	})

	It("keeps a tenant resource pending when the canonical Hub is unavailable", func(ctx context.Context) {
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
		_, _, vnID := createTenantAndDefaultVirtualNetwork(ctx, virtualNetworksClient)
		expectVirtualNetworkWithoutHub(ctx, virtualNetworksClient, vnID)
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

func expectVirtualNetworkWithoutHub(ctx context.Context, client privatev1.VirtualNetworksClient, id string) {
	Eventually(func(g Gomega) {
		response, err := client.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: id}.Build())
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(response.GetObject().GetStatus().GetHub()).To(BeEmpty())
	}, time.Minute, time.Second).Should(Succeed())
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
	tenantsClient := privatev1.NewTenantsClient(tool.InternalView().AdminConn())
	tenantName := fmt.Sprintf("test-canonical-tenant-%s", uuid.New())
	tenantID := createTenant(ctx, tenantsClient, tenantName)
	waitForTenantSynced(ctx, tenantsClient, tenantID)
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
