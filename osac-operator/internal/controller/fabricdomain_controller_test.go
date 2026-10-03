/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("FabricDomainReconciler", func() {
	const (
		namespace = "networking"
		vnetID    = "virtual-network-uuid"
	)

	var (
		ctx                       context.Context
		k8sClient                 client.Client
		reconciler                *FabricDomainReconciler
		mockProvider              *mockVirtualNetworkProvider
		domain                    *v1alpha1.FabricDomain
		vnet                      *v1alpha1.VirtualNetwork
		triggerCount              int
		lastPayload               map[string]any
		networkClassGetCalls      int
		instanceTypeListCalls     int
		lastInstanceTypeFilter    string
		instanceTypeOffsets       []int32
		instanceTypeFilterCounts  []int
		instanceTypePageCap       int
		instanceTypes             map[string]*privatev1.BareMetalInstanceType
		networkClass              *privatev1.NetworkClass
		authoritativeDomainTenant string
		authoritativeVNetTenant   string
		authoritativeDomainVNet   string
	)

	BeforeEach(func() {
		ctx = context.Background()
		triggerCount = 0
		lastPayload = nil
		networkClassGetCalls = 0
		instanceTypeListCalls = 0
		lastInstanceTypeFilter = ""
		instanceTypeOffsets = nil
		instanceTypeFilterCounts = nil
		instanceTypePageCap = int(fabricDomainCatalogPageSize)
		authoritativeDomainTenant = "tenant-a"
		authoritativeVNetTenant = "tenant-a"
		authoritativeDomainVNet = vnetID
		scheme := runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		k8sClient = fake.NewClientBuilder().WithScheme(scheme).
			WithStatusSubresource(&v1alpha1.FabricDomain{}, &v1alpha1.VirtualNetwork{}).Build()

		mockProvider = &mockVirtualNetworkProvider{
			triggerProvisionFunc: func(ctx context.Context, resource client.Object) (*provisioning.ProvisionResult, error) {
				persisted := &v1alpha1.FabricDomain{}
				if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(resource), persisted); err != nil {
					return nil, err
				}
				if persisted.Status.ProvisioningConfig == nil {
					return nil, fmt.Errorf("hardware binding was not persisted before launching AAP")
				}
				if !persisted.Status.ProvisioningIntent {
					return nil, fmt.Errorf("FabricDomain provisioning intent was not persisted before launching AAP")
				}
				triggerCount++
				extraVars := provisioning.AAPExtraVarsFromContext(ctx)
				eda := extraVars["ansible_eda"].(map[string]any)
				event := eda["event"].(map[string]any)
				lastPayload = event["payload"].(map[string]any)
				return &provisioning.ProvisionResult{
					JobID:        fmt.Sprintf("create-%d", triggerCount),
					InitialState: v1alpha1.JobStatePending,
					Message:      "create started",
				}, nil
			},
			getProvisionStatusFunc: func(_ context.Context, _ client.Object, jobID string) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID:   jobID,
					State:   v1alpha1.JobStateSucceeded,
					Message: "create completed",
					Outputs: map[string]any{"server_cluster_id": "42", "server_cluster_vpc_id": "7"},
				}, nil
			},
		}

		networkClass = privatev1.NetworkClass_builder{
			Id:            "nc-1",
			FabricManager: new("netris"),
			Capabilities:  privatev1.NetworkClassCapabilities_builder{SupportsEastWestEthernet: true}.Build(),
		}.Build()
		instanceTypes = map[string]*privatev1.BareMetalInstanceType{
			"gpu-type": fabricDomainTestInstanceType("gpu-type", "nc-1", "42"),
		}
		Expect(k8sClient.Create(ctx, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: fabricDomainInventoryName, Namespace: namespace},
			Data:       map[string]string{"server-a": "gpu-type", "server-b": "gpu-type", "server-c": "gpu-type"},
		})).To(Succeed())
		networkClassesClient := &stubNetworkClassesClient{
			getFunc: func(_ context.Context, req *privatev1.NetworkClassesGetRequest, _ ...grpc.CallOption) (*privatev1.NetworkClassesGetResponse, error) {
				networkClassGetCalls++
				Expect(req.GetId()).To(Equal("nc-1"))
				return privatev1.NetworkClassesGetResponse_builder{Object: networkClass}.Build(), nil
			},
		}
		fabricDomainsClient := &stubFabricDomainIdentityClient{
			getFunc: func(_ context.Context, request *privatev1.FabricDomainsGetRequest, _ ...grpc.CallOption) (*privatev1.FabricDomainsGetResponse, error) {
				Expect(request.GetId()).To(Equal("fabric-domain-uuid"))
				return privatev1.FabricDomainsGetResponse_builder{Object: privatev1.FabricDomain_builder{
					Id: "fabric-domain-uuid", Metadata: privatev1.Metadata_builder{Tenant: authoritativeDomainTenant}.Build(),
					Spec: privatev1.FabricDomainSpec_builder{VirtualNetwork: authoritativeDomainVNet}.Build(),
				}.Build()}.Build(), nil
			},
		}
		virtualNetworksClient := &stubFabricDomainVirtualNetworkIdentityClient{
			getFunc: func(_ context.Context, request *privatev1.VirtualNetworksGetRequest, _ ...grpc.CallOption) (*privatev1.VirtualNetworksGetResponse, error) {
				Expect(request.GetId()).To(Equal(vnetID))
				return privatev1.VirtualNetworksGetResponse_builder{Object: privatev1.VirtualNetwork_builder{
					Id: vnetID, Metadata: privatev1.Metadata_builder{Tenant: authoritativeVNetTenant}.Build(),
				}.Build()}.Build(), nil
			},
		}

		reconciler = &FabricDomainReconciler{
			Client:                k8sClient,
			APIReader:             k8sClient,
			NetworkingNamespace:   namespace,
			ProvisioningProvider:  mockProvider,
			NetworkClassesClient:  networkClassesClient,
			FabricDomainsClient:   fabricDomainsClient,
			VirtualNetworksClient: virtualNetworksClient,
			BareMetalInstanceTypesClient: &stubFabricDomainInstanceTypesClient{
				listFunc: func(_ context.Context, request *privatev1.BareMetalInstanceTypesListRequest, _ ...grpc.CallOption) (*privatev1.BareMetalInstanceTypesListResponse, error) {
					instanceTypeListCalls++
					lastInstanceTypeFilter = request.GetFilter()
					instanceTypeOffsets = append(instanceTypeOffsets, request.GetOffset())
					instanceTypeFilterCounts = append(instanceTypeFilterCounts, strings.Count(request.GetFilter(), `"type-`))
					Expect(request.GetLimit()).To(Equal(fabricDomainCatalogPageSize))
					Expect(request.GetOffset()).To(BeZero())
					items := make([]*privatev1.BareMetalInstanceType, 0, len(instanceTypes))
					ids := make([]string, 0, len(instanceTypes))
					for id := range instanceTypes {
						ids = append(ids, id)
					}
					sort.Strings(ids)
					for _, id := range ids {
						if strings.Contains(request.GetFilter(), strconv.Quote(id)) {
							items = append(items, instanceTypes[id])
						}
					}
					page := items[:min(len(items), instanceTypePageCap)]
					return privatev1.BareMetalInstanceTypesListResponse_builder{
						Items: page, Size: int32(len(page)), Total: int32(len(items)),
					}.Build(), nil
				},
			},
			StatusPollInterval:         time.Second,
			MaxJobHistory:              10,
			NetworkProvisioningEnabled: true,
		}
		vnet = &v1alpha1.VirtualNetwork{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "tenant-vnet",
				Namespace:   namespace,
				Labels:      map[string]string{osacVirtualNetworkIDLabel: vnetID},
				Annotations: map[string]string{osacTenantKey: "tenant-a"},
			},
			Spec: v1alpha1.VirtualNetworkSpec{NetworkClass: "nc-1", Region: "region-a"},
			Status: v1alpha1.VirtualNetworkStatus{
				Phase:            v1alpha1.VirtualNetworkPhaseReady,
				BackendNetworkID: "7",
			},
		}
		domain = &v1alpha1.FabricDomain{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "tenant-fabric",
				Namespace: namespace,
				Labels: map[string]string{
					osacFabricDomainIDLabel: "fabric-domain-uuid",
				},
				Annotations: map[string]string{
					"osac.openshift.io/tenant":          "tenant-a",
					"osac.openshift.io/owner-reference": "owner-1",
				},
			},
			Spec: v1alpha1.FabricDomainSpec{
				Type:           v1alpha1.FabricDomainTypeEthernetEW,
				Servers:        []string{"server-a", "server-b"},
				VirtualNetwork: vnetID,
			},
		}
		Expect(k8sClient.Create(ctx, vnet)).To(Succeed())
		Expect(k8sClient.Create(ctx, domain)).To(Succeed())
	})

	request := func() mcreconcile.Request {
		return mcreconcile.Request{Request: reconcile.Request{NamespacedName: types.NamespacedName{Name: domain.Name, Namespace: domain.Namespace}}}
	}

	reconcileTimes := func(count int) *v1alpha1.FabricDomain {
		for i := 0; i < count; i++ {
			_, err := reconciler.Reconcile(ctx, request())
			Expect(err).NotTo(HaveOccurred())
		}
		updated := &v1alpha1.FabricDomain{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(domain), updated)).To(Succeed())
		return updated
	}

	It("fails closed for an unknown inventory hostname", func() {
		domain.Spec.Servers = []string{"not-onboarded"}
		Expect(k8sClient.Update(ctx, domain)).To(Succeed())
		updated := reconcileTimes(3)
		Expect(triggerCount).To(BeZero())
		Expect(updated.Status.ProvisioningIntent).To(BeFalse())
		condition := apimeta.FindStatusCondition(updated.Status.Conditions, v1alpha1.ConditionReady)
		Expect(condition.Reason).To(Equal("InvalidHardwareBinding"))
		Expect(condition.Message).NotTo(ContainSubstring("not-onboarded"))
		Expect(condition.Message).To(ContainSubstring("consult administrator logs"))
	})

	It("does not infer a hardware type when the inventory ConfigMap is absent", func() {
		Expect(k8sClient.Delete(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: fabricDomainInventoryName, Namespace: namespace}})).To(Succeed())
		updated := reconcileTimes(3)
		Expect(triggerCount).To(BeZero())
		Expect(updated.Status.Phase).To(Equal(v1alpha1.FabricDomainPhaseFailed))
	})

	DescribeTable("rejects invalid instance type bindings without launching AAP",
		func(classID, templateID string) {
			instanceTypes["gpu-type"] = fabricDomainTestInstanceType("gpu-type", classID, templateID)
			updated := reconcileTimes(3)
			Expect(triggerCount).To(BeZero())
			Expect(apimeta.FindStatusCondition(updated.Status.Conditions, v1alpha1.ConditionReady).Reason).To(Equal("InvalidHardwareBinding"))
		},
		Entry("another NetworkClass", "another-class", "42"),
		Entry("no NetworkClass", "", "42"),
		Entry("zero template", "nc-1", "0"),
		Entry("non-numeric template", "nc-1", "template-42"),
		Entry("non-canonical template", "nc-1", "042"),
		Entry("missing template", "nc-1", ""),
	)

	It("accepts different hardware types using the same scoped template", func() {
		inventory := &corev1.ConfigMap{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: fabricDomainInventoryName}, inventory)).To(Succeed())
		inventory.Data["server-b"] = "other-gpu-type"
		Expect(k8sClient.Update(ctx, inventory)).To(Succeed())
		instanceTypes["other-gpu-type"] = fabricDomainTestInstanceType("other-gpu-type", "nc-1", "42")
		updated := reconcileTimes(4)
		Expect(triggerCount).To(Equal(1))
		Expect(updated.Status.Phase).To(Equal(v1alpha1.FabricDomainPhaseReady))
	})

	It("resolves all distinct member types with one filtered catalog request", func() {
		inventory := &corev1.ConfigMap{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: fabricDomainInventoryName}, inventory)).To(Succeed())
		inventory.Data["server-b"] = "other-gpu-type"
		Expect(k8sClient.Update(ctx, inventory)).To(Succeed())
		instanceTypes["other-gpu-type"] = fabricDomainTestInstanceType("other-gpu-type", "nc-1", "42")

		templateID, err := reconciler.resolveFabricDomainHardware(ctx, domain, "nc-1")
		Expect(err).NotTo(HaveOccurred())
		Expect(templateID).To(Equal("42"))
		Expect(instanceTypeListCalls).To(Equal(1))
		Expect(lastInstanceTypeFilter).To(ContainSubstring(`"gpu-type"`))
		Expect(lastInstanceTypeFilter).To(ContainSubstring(`"other-gpu-type"`))
	})

	It("rejects mixed templates rather than selecting the first member", func() {
		inventory := &corev1.ConfigMap{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: fabricDomainInventoryName}, inventory)).To(Succeed())
		inventory.Data["server-b"] = "other-gpu-type"
		Expect(k8sClient.Update(ctx, inventory)).To(Succeed())
		instanceTypes["other-gpu-type"] = fabricDomainTestInstanceType("other-gpu-type", "nc-1", "43")
		updated := reconcileTimes(3)
		Expect(triggerCount).To(BeZero())
		condition := apimeta.FindStatusCondition(updated.Status.Conditions, v1alpha1.ConditionReady)
		Expect(condition.Message).NotTo(ContainSubstring("incompatible Ethernet templates"))
		Expect(condition.Message).To(ContainSubstring("consult administrator logs"))
	})

	It("pins the backend binding and recovers when an incompatible catalog edit is reverted", func() {
		updated := reconcileTimes(4)
		Expect(updated.Status.ProvisioningConfig).To(Equal(&v1alpha1.FabricDomainProvisioningConfig{
			NetworkClass: "nc-1", TemplateID: "42", VPCID: "7", Region: "region-a",
		}))
		instanceTypes["gpu-type"] = fabricDomainTestInstanceType("gpu-type", "nc-1", "43")
		updated = reconcileTimes(1)
		Expect(triggerCount).To(Equal(1))
		Expect(apimeta.FindStatusCondition(updated.Status.Conditions, v1alpha1.ConditionReady).Reason).To(Equal("BackendBindingChanged"))
		Expect(updated.Status.ProvisioningConfig.TemplateID).To(Equal("42"))
		instanceTypes["gpu-type"] = fabricDomainTestInstanceType("gpu-type", "nc-1", "42")
		updated = reconcileTimes(1)
		Expect(updated.Status.Phase).To(Equal(v1alpha1.FabricDomainPhaseReady))
		Expect(triggerCount).To(Equal(1))
	})

	It("does not mark newly requested members active when an earlier job completes", func() {
		updated := reconcileTimes(3)
		updated.Spec.Servers = []string{"server-a", "server-c"}
		Expect(k8sClient.Update(ctx, updated)).To(Succeed())
		updated = reconcileTimes(1)
		Expect(updated.Status.Phase).To(Equal(v1alpha1.FabricDomainPhaseProgressing))
		Expect(updated.Status.Members).To(ConsistOf(
			v1alpha1.FabricDomainMemberStatus{Server: "server-a", State: v1alpha1.FabricDomainMemberStatePending},
			v1alpha1.FabricDomainMemberStatus{Server: "server-c", State: v1alpha1.FabricDomainMemberStatePending},
		))
		reconcileTimes(1)
		Expect(triggerCount).To(Equal(2))
	})

	It("reuses the persisted backend binding while an AAP provisioning job is active", func() {
		mockProvider.getProvisionStatusFunc = func(_ context.Context, _ client.Object, jobID string) (provisioning.ProvisionStatus, error) {
			return provisioning.ProvisionStatus{JobID: jobID, State: v1alpha1.JobStateRunning, Message: "still provisioning"}, nil
		}
		updated := reconcileTimes(3)
		Expect(updated.Status.ProvisioningConfig).NotTo(BeNil())
		Expect(instanceTypeListCalls).To(Equal(1))
		Expect(networkClassGetCalls).To(Equal(1))

		_, err := reconciler.Reconcile(ctx, request())
		Expect(err).NotTo(HaveOccurred())
		Expect(triggerCount).To(Equal(1))
		Expect(instanceTypeListCalls).To(Equal(1), "the in-flight job uses its persisted type/template binding")
		Expect(networkClassGetCalls).To(Equal(1), "the in-flight job uses its persisted NetworkClass binding")
	})

	It("does not use tenant annotations as authority for VirtualNetwork resolution", func() {
		vnet.Annotations[osacTenantKey] = "other-tenant"
		Expect(k8sClient.Update(ctx, vnet)).To(Succeed())
		updated := reconcileTimes(4)
		Expect(triggerCount).To(Equal(1))
		Expect(apimeta.FindStatusCondition(updated.Status.Conditions, v1alpha1.ConditionReady).Reason).NotTo(Equal("TenantMismatch"))
	})

	It("rejects cross-tenant VirtualNetworks using Fulfillment metadata", func() {
		authoritativeVNetTenant = "tenant-b"
		updated := reconcileTimes(4)
		Expect(triggerCount).To(BeZero())
		Expect(apimeta.FindStatusCondition(updated.Status.Conditions, v1alpha1.ConditionReady).Reason).To(Equal("TenantMismatch"))
		Expect(updated.Status.Phase).To(Equal(v1alpha1.FabricDomainPhaseFailed))

		protectedVNet := &v1alpha1.VirtualNetwork{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vnet), protectedVNet)).To(Succeed())
		Expect(protectedVNet.Finalizers).NotTo(ContainElement(osacFabricDomainProtectionFinalizer))
	})

	It("rejects a hub FabricDomain whose VirtualNetwork differs from Fulfillment", func() {
		authoritativeDomainVNet = "different-virtual-network-uuid"
		updated := reconcileTimes(4)
		Expect(triggerCount).To(BeZero())
		Expect(apimeta.FindStatusCondition(updated.Status.Conditions, v1alpha1.ConditionReady).Reason).To(Equal("TenantMismatch"))
	})

	It("paginates the filtered instance-type catalog when resolving large domains", func() {
		inventory := &corev1.ConfigMap{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: fabricDomainInventoryName}, inventory)).To(Succeed())
		inventory.Data = make(map[string]string, 101)
		domain.Spec.Servers = make([]string, 101)
		instanceTypes = make(map[string]*privatev1.BareMetalInstanceType, 101)
		instanceTypePageCap = 50
		for i := range 101 {
			serverName := fmt.Sprintf("server-%03d", i)
			typeID := fmt.Sprintf("type-%03d", i)
			domain.Spec.Servers[i] = serverName
			inventory.Data[serverName] = typeID
			instanceTypes[typeID] = fabricDomainTestInstanceType(typeID, "nc-1", "42")
		}
		Expect(k8sClient.Update(ctx, inventory)).To(Succeed())

		templateID, err := reconciler.resolveFabricDomainHardware(ctx, domain, "nc-1")
		Expect(err).NotTo(HaveOccurred())
		Expect(templateID).To(Equal("42"))
		Expect(instanceTypeListCalls).To(Equal(4))
		Expect(instanceTypeOffsets).To(Equal([]int32{0, 0, 0, 0}))
		Expect(instanceTypeFilterCounts).To(Equal([]int{100, 50, 50, 1}))
	})

	It("wakes domains when their administrator inventory changes", func() {
		inventory := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: fabricDomainInventoryName, Namespace: namespace}}
		Expect(reconciler.mapInventoryToFabricDomains(ctx, inventory)).To(ConsistOf(reconcile.Request{NamespacedName: client.ObjectKeyFromObject(domain)}))
		inventory.Name = "unrelated-config"
		Expect(reconciler.mapInventoryToFabricDomains(ctx, inventory)).To(BeEmpty())
	})

	It("resolves inventory hardware bindings and VPC, provisions, and records AAP outputs", func() {
		for i := 0; i < 3; i++ {
			_, err := reconciler.Reconcile(ctx, request())
			Expect(err).NotTo(HaveOccurred())
		}
		Expect(triggerCount).To(Equal(1))
		Expect(lastPayload["kind"]).To(Equal("ServerCluster"))
		metadata := lastPayload["metadata"].(map[string]any)
		annotations := metadata["annotations"].(map[string]string)
		Expect(annotations).To(HaveKeyWithValue("osac.openshift.io/tenant", "tenant-a"))
		Expect(annotations).To(HaveKeyWithValue("osac.openshift.io/owner-reference", "owner-1"))
		spec := lastPayload["spec"].(map[string]any)
		Expect(spec["templateId"]).To(Equal("42"))
		Expect(spec["vpcId"]).To(Equal("7"))
		Expect(spec["region"]).To(Equal("region-a"))
		Expect(spec["servers"]).To(Equal([]string{"server-a", "server-b"}))

		_, err := reconciler.Reconcile(ctx, request())
		Expect(err).NotTo(HaveOccurred())
		updated := &v1alpha1.FabricDomain{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(domain), updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(v1alpha1.FabricDomainPhaseReady))
		Expect(updated.Status.BackendID).To(Equal("42"))
		Expect(updated.Status.VPCID).To(Equal("7"))
		Expect(updated.Status.Members).To(ConsistOf(
			v1alpha1.FabricDomainMemberStatus{Server: "server-a", State: v1alpha1.FabricDomainMemberStateActive},
			v1alpha1.FabricDomainMemberStatus{Server: "server-b", State: v1alpha1.FabricDomainMemberStateActive},
		))

		updated.Spec.Servers = []string{"server-a", "server-c"}
		Expect(k8sClient.Update(ctx, updated)).To(Succeed())
		_, err = reconciler.Reconcile(ctx, request())
		Expect(err).NotTo(HaveOccurred())
		Expect(triggerCount).To(Equal(2), "a server-list change should launch an idempotent update job")
		updatedServers := lastPayload["spec"].(map[string]any)["servers"]
		Expect(updatedServers).To(Equal([]string{"server-a", "server-c"}))

		updatedVNet := &v1alpha1.VirtualNetwork{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vnet), updatedVNet)).To(Succeed())
		Expect(updatedVNet.Finalizers).To(ContainElement(osacFabricDomainProtectionFinalizer))
	})

	It("deprovisions the ServerCluster before releasing VirtualNetwork protection", func() {
		protectedVNet := &v1alpha1.VirtualNetwork{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vnet), protectedVNet)).To(Succeed())
		protectedVNet.Finalizers = append(protectedVNet.Finalizers, osacFabricDomainProtectionFinalizer)
		Expect(k8sClient.Update(ctx, protectedVNet)).To(Succeed())

		deletingDomain := &v1alpha1.FabricDomain{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(domain), deletingDomain)).To(Succeed())
		deletingDomain.Finalizers = append(deletingDomain.Finalizers, osacFabricDomainFinalizer)
		Expect(k8sClient.Update(ctx, deletingDomain)).To(Succeed())
		deletingDomain.Status.BackendID = "42"
		deletingDomain.Status.VPCID = "7"
		deletingDomain.Status.ProvisioningJobs = []v1alpha1.JobStatus{{
			JobID: "create-1", Type: v1alpha1.JobTypeProvision, State: v1alpha1.JobStateSucceeded,
		}}
		Expect(k8sClient.Status().Update(ctx, deletingDomain)).To(Succeed())

		var deprovisionCalls int
		var deletePayload map[string]any
		mockProvider.triggerDeprovisionFunc = func(ctx context.Context, _ client.Object, _ []v1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
			deprovisionCalls++
			extraVars := provisioning.AAPExtraVarsFromContext(ctx)
			eda := extraVars["ansible_eda"].(map[string]any)
			event := eda["event"].(map[string]any)
			deletePayload = event["payload"].(map[string]any)
			return &provisioning.DeprovisionResult{
				Action:                 provisioning.DeprovisionTriggered,
				JobID:                  "delete-1",
				BlockDeletionOnFailure: true,
			}, nil
		}
		mockProvider.getDeprovisionStatusFunc = func(_ context.Context, _ client.Object, jobID string) (provisioning.ProvisionStatus, error) {
			return provisioning.ProvisionStatus{JobID: jobID, State: v1alpha1.JobStateSucceeded}, nil
		}

		result, err := reconciler.handleDelete(ctx, deletingDomain)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(time.Second))
		Expect(deprovisionCalls).To(Equal(1))
		Expect(deletingDomain.Status.Phase).To(Equal(v1alpha1.FabricDomainPhaseDeleting))
		Expect(deletePayload["kind"]).To(Equal("ServerCluster"))
		deleteSpec := deletePayload["spec"].(map[string]any)
		Expect(deleteSpec["backendId"]).To(Equal("42"))
		Expect(deleteSpec["vpcId"]).To(Equal("7"))

		_, err = reconciler.handleDelete(ctx, deletingDomain)
		Expect(err).NotTo(HaveOccurred())
		Expect(deprovisionCalls).To(Equal(1))
		Expect(deletingDomain.Finalizers).NotTo(ContainElement(osacFabricDomainFinalizer))

		updatedVNet := &v1alpha1.VirtualNetwork{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vnet), updatedVNet)).To(Succeed())
		Expect(updatedVNet.Finalizers).NotTo(ContainElement(osacFabricDomainProtectionFinalizer))
	})

	It("keeps VirtualNetwork protection while another FabricDomain references it", func() {
		protectedVNet := &v1alpha1.VirtualNetwork{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vnet), protectedVNet)).To(Succeed())
		protectedVNet.Finalizers = append(protectedVNet.Finalizers, osacFabricDomainProtectionFinalizer)
		Expect(k8sClient.Update(ctx, protectedVNet)).To(Succeed())

		otherDomain := &v1alpha1.FabricDomain{
			ObjectMeta: metav1.ObjectMeta{Name: "other-fabric-domain", Namespace: namespace},
			Spec:       domain.Spec,
		}
		Expect(k8sClient.Create(ctx, otherDomain)).To(Succeed())

		Expect(reconciler.releaseVirtualNetworkProtection(ctx, domain)).To(Succeed())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vnet), protectedVNet)).To(Succeed())
		Expect(protectedVNet.Finalizers).To(ContainElement(osacFabricDomainProtectionFinalizer))
	})

	It("retains the finalizer when provisioning intent has no saved job or backend ID", func() {
		deletingDomain := &v1alpha1.FabricDomain{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(domain), deletingDomain)).To(Succeed())
		deletingDomain.Finalizers = append(deletingDomain.Finalizers, osacFabricDomainFinalizer)
		Expect(k8sClient.Update(ctx, deletingDomain)).To(Succeed())
		deletingDomain.Status.ProvisioningIntent = true
		Expect(k8sClient.Status().Update(ctx, deletingDomain)).To(Succeed())

		var deprovisionCalls int
		mockProvider.triggerDeprovisionFunc = func(_ context.Context, _ client.Object, _ []v1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
			deprovisionCalls++
			return &provisioning.DeprovisionResult{
				Action:                 provisioning.DeprovisionTriggered,
				JobID:                  "delete-intent",
				BlockDeletionOnFailure: true,
			}, nil
		}

		_, err := reconciler.handleDelete(ctx, deletingDomain)
		Expect(err).To(MatchError(ContainSubstring("cannot safely delete FabricDomain")))
		Expect(deprovisionCalls).To(Equal(0))
		Expect(deletingDomain.Finalizers).To(ContainElement(osacFabricDomainFinalizer))
		Expect(deletingDomain.Status.Phase).To(Equal(v1alpha1.FabricDomainPhaseFailed))
	})

	It("retains the finalizer while an unverified backend artifact remains", func() {
		deletingDomain := &v1alpha1.FabricDomain{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(domain), deletingDomain)).To(Succeed())
		deletingDomain.Finalizers = append(deletingDomain.Finalizers, osacFabricDomainFinalizer)
		Expect(k8sClient.Update(ctx, deletingDomain)).To(Succeed())
		deletingDomain.Status.ProvisioningIntent = true
		deletingDomain.Status.VPCID = "7"
		deletingDomain.Status.UnverifiedBackendArtifact = true
		deletingDomain.Status.UnverifiedBackendID = "42"
		deletingDomain.Status.ProvisioningJobs = []v1alpha1.JobStatus{{
			JobID: "create-1", Type: v1alpha1.JobTypeProvision, State: v1alpha1.JobStateFailed,
		}}
		deletingDomain.Status.Conditions = []metav1.Condition{{
			Type: v1alpha1.ConditionReady, Status: metav1.ConditionFalse,
			Reason: fabricDomainUnverifiedBackendReason, Message: "AAP reported another VPC",
		}}
		Expect(k8sClient.Status().Update(ctx, deletingDomain)).To(Succeed())

		var deprovisionCalls int
		mockProvider.triggerDeprovisionFunc = func(_ context.Context, _ client.Object, _ []v1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
			deprovisionCalls++
			return &provisioning.DeprovisionResult{Action: provisioning.DeprovisionTriggered, JobID: "delete-1"}, nil
		}

		_, err := reconciler.handleDelete(ctx, deletingDomain)
		Expect(err).To(MatchError(ContainSubstring("status.unverifiedBackendId")))
		Expect(deprovisionCalls).To(BeZero())
		Expect(deletingDomain.Finalizers).To(ContainElement(osacFabricDomainFinalizer))
		Expect(deletingDomain.Status.Phase).To(Equal(v1alpha1.FabricDomainPhaseFailed))
	})

	It("blocks retry and deletion when AAP succeeds without a backend ID", func() {
		domain.Finalizers = append(domain.Finalizers, osacFabricDomainFinalizer)
		Expect(k8sClient.Update(ctx, domain)).To(Succeed())
		domain.Status.ProvisioningIntent = true
		domain.Status.VPCID = "7"
		domain.Status.ProvisioningJobs = []v1alpha1.JobStatus{{
			JobID: "create-no-id", Type: v1alpha1.JobTypeProvision, State: v1alpha1.JobStateSucceeded,
		}}
		fabricDomainPollCallbacks(ctx, domain, "desired").OnSuccess(provisioning.ProvisionStatus{
			Outputs: map[string]any{"server_cluster_vpc_id": "7"},
		})
		Expect(domain.Status.UnverifiedBackendArtifact).To(BeTrue())
		Expect(domain.Status.UnverifiedBackendID).To(BeEmpty())
		Expect(domain.Status.BackendID).To(BeEmpty())
		Expect(domain.Status.Members).To(HaveLen(len(domain.Spec.Servers)))
		Expect(domain.Status.Members).To(HaveEach(HaveField("State", Equal(v1alpha1.FabricDomainMemberStateFailed))))

		result, err := reconciler.handleUpdate(ctx, domain)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", time.Duration(0)))
		Expect(triggerCount).To(BeZero(), "do not retry while the artifact identity is unknown")

		_, err = reconciler.handleDelete(ctx, domain)
		Expect(err).To(MatchError(ContainSubstring("status.unverifiedBackendArtifact")))
		Expect(domain.Finalizers).To(ContainElement(osacFabricDomainFinalizer))
	})

	It("uses the VirtualNetwork region for scoped cleanup after a recorded provision job", func() {
		deletingDomain := &v1alpha1.FabricDomain{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(domain), deletingDomain)).To(Succeed())
		deletingDomain.Finalizers = append(deletingDomain.Finalizers, osacFabricDomainFinalizer)
		Expect(k8sClient.Update(ctx, deletingDomain)).To(Succeed())
		deletingDomain.Status.ProvisioningIntent = true
		deletingDomain.Status.VPCID = "7"
		deletingDomain.Status.ProvisioningJobs = []v1alpha1.JobStatus{{
			JobID: "create-1", Type: v1alpha1.JobTypeProvision, State: v1alpha1.JobStateSucceeded,
		}}
		Expect(k8sClient.Status().Update(ctx, deletingDomain)).To(Succeed())

		mockProvider.triggerDeprovisionFunc = func(ctx context.Context, _ client.Object, _ []v1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
			extraVars := provisioning.AAPExtraVarsFromContext(ctx)
			payload := extraVars["ansible_eda"].(map[string]any)["event"].(map[string]any)["payload"].(map[string]any)
			spec := payload["spec"].(map[string]any)
			Expect(spec["backendId"]).To(BeEmpty())
			Expect(spec["vpcId"]).To(Equal("7"))
			Expect(spec["region"]).To(Equal("region-a"))
			return &provisioning.DeprovisionResult{Action: provisioning.DeprovisionTriggered, JobID: "delete-1", BlockDeletionOnFailure: true}, nil
		}
		result, err := reconciler.handleDelete(ctx, deletingDomain)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(time.Second))
		Expect(deletingDomain.Finalizers).To(ContainElement(osacFabricDomainFinalizer))
	})

	It("does not launch AAP with an invalid Netris VPC ID", func() {
		updatedVNet := &v1alpha1.VirtualNetwork{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vnet), updatedVNet)).To(Succeed())
		updatedVNet.Status.BackendNetworkID = "vpc-7"
		Expect(k8sClient.Status().Update(ctx, updatedVNet)).To(Succeed())
		for i := 0; i < 3; i++ {
			_, err := reconciler.Reconcile(ctx, request())
			Expect(err).NotTo(HaveOccurred())
		}
		Expect(triggerCount).To(BeZero())
		updated := &v1alpha1.FabricDomain{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(domain), updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(v1alpha1.FabricDomainPhaseFailed))
		condition := apimeta.FindStatusCondition(updated.Status.Conditions, v1alpha1.ConditionReady)
		Expect(condition.Message).NotTo(ContainSubstring("vpc-7"))
	})

	It("does not expose a Netris template identifier in hardware binding conditions", func() {
		instanceTypes["gpu-type"] = fabricDomainTestInstanceType("gpu-type", "nc-1", "template-secret")
		for i := 0; i < 3; i++ {
			_, err := reconciler.Reconcile(ctx, request())
			Expect(err).NotTo(HaveOccurred())
		}
		updated := &v1alpha1.FabricDomain{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(domain), updated)).To(Succeed())
		condition := apimeta.FindStatusCondition(updated.Status.Conditions, v1alpha1.ConditionReady)
		Expect(condition.Reason).To(Equal("InvalidHardwareBinding"))
		Expect(condition.Message).NotTo(ContainSubstring("template-secret"))
		Expect(condition.Message).To(ContainSubstring("consult administrator logs"))
	})

	It("rejects a ServerCluster reported in another VPC", func() {
		domain.Status.VPCID = "7"
		domain.Status.ProvisioningJobs = []v1alpha1.JobStatus{{
			JobID: "create-1", Type: v1alpha1.JobTypeProvision, State: v1alpha1.JobStateSucceeded,
		}}
		fabricDomainPollCallbacks(ctx, domain, "desired").OnSuccess(provisioning.ProvisionStatus{
			Outputs: map[string]any{"server_cluster_id": "42", "server_cluster_vpc_id": "8"},
		})
		Expect(domain.Status.Phase).To(Equal(v1alpha1.FabricDomainPhaseFailed))
		Expect(domain.Status.BackendID).To(BeEmpty(), "do not persist an ID from a mismatched VPC artifact")
		Expect(domain.Status.UnverifiedBackendArtifact).To(BeTrue())
		Expect(domain.Status.UnverifiedBackendID).To(Equal("42"))
		Expect(domain.Status.VPCID).To(Equal("7"))
		Expect(domain.Status.ProvisioningJobs[0].State).To(Equal(v1alpha1.JobStateFailed))
		Expect(domain.Status.ProvisioningJobs[0].Message).NotTo(ContainSubstring("8"))
		condition := apimeta.FindStatusCondition(domain.Status.Conditions, v1alpha1.ConditionReady)
		Expect(condition.Message).NotTo(ContainSubstring("8"))
	})

	It("marks the artifact unverified when AAP omits the ServerCluster VPC ID", func() {
		domain.Status.VPCID = "7"
		domain.Status.ProvisioningJobs = []v1alpha1.JobStatus{{
			JobID: "create-1", Type: v1alpha1.JobTypeProvision, State: v1alpha1.JobStateSucceeded,
		}}
		fabricDomainPollCallbacks(ctx, domain, "desired").OnSuccess(provisioning.ProvisionStatus{
			Outputs: map[string]any{"server_cluster_id": "42"},
		})

		Expect(domain.Status.Phase).To(Equal(v1alpha1.FabricDomainPhaseFailed))
		Expect(domain.Status.BackendID).To(BeEmpty())
		Expect(domain.Status.UnverifiedBackendArtifact).To(BeTrue())
		Expect(domain.Status.UnverifiedBackendID).To(Equal("42"))
		condition := apimeta.FindStatusCondition(domain.Status.Conditions, v1alpha1.ConditionReady)
		Expect(condition.Message).To(ContainSubstring("did not confirm"))
	})

	It("preserves a previously verified ServerCluster ID after a mismatched VPC update", func() {
		domain.Status.BackendID = "41"
		domain.Status.VPCID = "7"
		domain.Status.ProvisioningJobs = []v1alpha1.JobStatus{{
			JobID: "update-1", Type: v1alpha1.JobTypeProvision, State: v1alpha1.JobStateSucceeded,
		}}
		fabricDomainPollCallbacks(ctx, domain, "desired").OnSuccess(provisioning.ProvisionStatus{
			Outputs: map[string]any{"server_cluster_id": "42", "server_cluster_vpc_id": "8"},
		})
		Expect(domain.Status.Phase).To(Equal(v1alpha1.FabricDomainPhaseFailed))
		Expect(domain.Status.BackendID).To(Equal("41"))
	})

	It("keeps an unresolved artifact blocked across a successful retry", func() {
		domain.Finalizers = append(domain.Finalizers, osacFabricDomainFinalizer)
		domain.Status.VPCID = "7"
		domain.Status.ProvisioningJobs = []v1alpha1.JobStatus{{
			JobID: "create-1", Type: v1alpha1.JobTypeProvision, State: v1alpha1.JobStateSucceeded,
		}}
		fabricDomainPollCallbacks(ctx, domain, "desired").OnSuccess(provisioning.ProvisionStatus{
			Outputs: map[string]any{"server_cluster_id": "42", "server_cluster_vpc_id": "8"},
		})
		Expect(domain.Status.UnverifiedBackendArtifact).To(BeTrue())
		Expect(domain.Status.UnverifiedBackendID).To(Equal("42"))

		result, err := reconciler.handleUpdate(ctx, domain)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", time.Duration(0)))
		Expect(triggerCount).To(BeZero(), "do not retry while the mismatched artifact is unresolved")

		domain.Status.ProvisioningJobs = []v1alpha1.JobStatus{{
			JobID: "retry-1", Type: v1alpha1.JobTypeProvision, State: v1alpha1.JobStateSucceeded,
		}}
		fabricDomainPollCallbacks(ctx, domain, "retry-version").OnSuccess(provisioning.ProvisionStatus{
			Outputs: map[string]any{"server_cluster_id": "43", "server_cluster_vpc_id": "7"},
		})
		Expect(domain.Status.BackendID).To(Equal("43"))
		Expect(domain.Status.UnverifiedBackendArtifact).To(BeTrue())
		Expect(domain.Status.UnverifiedBackendID).To(Equal("42"))

		_, err = reconciler.handleDelete(ctx, domain)
		Expect(err).To(MatchError(ContainSubstring("status.unverifiedBackendId")))
		Expect(domain.Finalizers).To(ContainElement(osacFabricDomainFinalizer))
	})

	It("wakes only FabricDomains that reference a changed VirtualNetwork", func() {
		other := &v1alpha1.FabricDomain{
			ObjectMeta: metav1.ObjectMeta{Name: "other-domain", Namespace: namespace},
			Spec:       v1alpha1.FabricDomainSpec{VirtualNetwork: "another-network"},
		}
		Expect(k8sClient.Create(ctx, other)).To(Succeed())
		requests := reconciler.mapVirtualNetworkToFabricDomains(ctx, vnet)
		Expect(requests).To(ConsistOf(reconcile.Request{NamespacedName: client.ObjectKeyFromObject(domain)}))
	})

	It("retains the finalizer when provisioning state exists but the provider is unavailable", func() {
		deletingDomain := &v1alpha1.FabricDomain{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(domain), deletingDomain)).To(Succeed())
		deletingDomain.Finalizers = append(deletingDomain.Finalizers, osacFabricDomainFinalizer)
		Expect(k8sClient.Update(ctx, deletingDomain)).To(Succeed())
		deletingDomain.Status.ProvisioningIntent = true
		Expect(k8sClient.Status().Update(ctx, deletingDomain)).To(Succeed())
		reconciler.ProvisioningProvider = nil

		_, err := reconciler.handleDelete(ctx, deletingDomain)
		Expect(err).To(MatchError(ContainSubstring("AAP provisioning provider is unavailable")))
		Expect(deletingDomain.Finalizers).To(ContainElement(osacFabricDomainFinalizer))

		persisted := &v1alpha1.FabricDomain{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(domain), persisted)).To(Succeed())
		Expect(persisted.Finalizers).To(ContainElement(osacFabricDomainFinalizer))
	})

	It("reports AAP failures as failed Ready conditions and failed members", func() {
		mockProvider.getProvisionStatusFunc = func(_ context.Context, _ client.Object, jobID string) (provisioning.ProvisionStatus, error) {
			return provisioning.ProvisionStatus{JobID: jobID, State: v1alpha1.JobStateFailed, Message: "Netris rejected the request"}, nil
		}
		for i := 0; i < 4; i++ {
			_, err := reconciler.Reconcile(ctx, request())
			Expect(err).NotTo(HaveOccurred())
		}
		updated := &v1alpha1.FabricDomain{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(domain), updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(v1alpha1.FabricDomainPhaseFailed))
		condition := apimeta.FindStatusCondition(updated.Status.Conditions, v1alpha1.ConditionReady)
		Expect(condition).NotTo(BeNil())
		Expect(condition.Status).To(Equal(metav1.ConditionFalse))
		Expect(condition.Message).NotTo(ContainSubstring("Netris rejected"))
		Expect(condition.Message).To(ContainSubstring("consult administrator logs"))
		Expect(updated.Status.Members).To(ConsistOf(
			v1alpha1.FabricDomainMemberStatus{Server: "server-a", State: v1alpha1.FabricDomainMemberStateFailed, Message: "provisioning failed; consult administrator logs for details"},
			v1alpha1.FabricDomainMemberStatus{Server: "server-b", State: v1alpha1.FabricDomainMemberStateFailed, Message: "provisioning failed; consult administrator logs for details"},
		))
		provisionJob := provisioning.FindLatestJobByType(updated.Status.ProvisioningJobs, v1alpha1.JobTypeProvision)
		Expect(provisionJob.Message).To(Equal("provisioning failed; consult administrator logs for details"))
	})
})

// Only List is used by the resolver; embedding the generated interface keeps the stub focused.
type stubFabricDomainInstanceTypesClient struct {
	privatev1.BareMetalInstanceTypesClient
	listFunc func(context.Context, *privatev1.BareMetalInstanceTypesListRequest, ...grpc.CallOption) (*privatev1.BareMetalInstanceTypesListResponse, error)
}

type stubFabricDomainIdentityClient struct {
	privatev1.FabricDomainsClient
	getFunc func(context.Context, *privatev1.FabricDomainsGetRequest, ...grpc.CallOption) (*privatev1.FabricDomainsGetResponse, error)
}

func (s *stubFabricDomainIdentityClient) Get(ctx context.Context, request *privatev1.FabricDomainsGetRequest, options ...grpc.CallOption) (*privatev1.FabricDomainsGetResponse, error) {
	return s.getFunc(ctx, request, options...)
}

type stubFabricDomainVirtualNetworkIdentityClient struct {
	privatev1.VirtualNetworksClient
	getFunc func(context.Context, *privatev1.VirtualNetworksGetRequest, ...grpc.CallOption) (*privatev1.VirtualNetworksGetResponse, error)
}

func (s *stubFabricDomainVirtualNetworkIdentityClient) Get(ctx context.Context, request *privatev1.VirtualNetworksGetRequest, options ...grpc.CallOption) (*privatev1.VirtualNetworksGetResponse, error) {
	return s.getFunc(ctx, request, options...)
}

func (s *stubFabricDomainInstanceTypesClient) List(ctx context.Context, request *privatev1.BareMetalInstanceTypesListRequest, options ...grpc.CallOption) (*privatev1.BareMetalInstanceTypesListResponse, error) {
	return s.listFunc(ctx, request, options...)
}

func fabricDomainTestInstanceType(id, networkClass, templateID string) *privatev1.BareMetalInstanceType {
	return privatev1.BareMetalInstanceType_builder{
		Id:       id,
		Metadata: privatev1.Metadata_builder{Tenant: "shared"}.Build(),
		Spec: privatev1.BareMetalInstanceTypeSpec_builder{
			FabricBindings: privatev1.BareMetalFabricBindings_builder{
				EthernetEw: privatev1.BareMetalEthernetFabricBinding_builder{
					Netris: privatev1.BareMetalNetrisFabricBinding_builder{NetworkClass: networkClass, TemplateId: templateID}.Build(),
				}.Build(),
			}.Build(),
		}.Build(),
	}.Build()
}
