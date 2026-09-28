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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
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
		ctx          context.Context
		k8sClient    client.Client
		reconciler   *FabricDomainReconciler
		mockProvider *mockVirtualNetworkProvider
		domain       *v1alpha1.FabricDomain
		vnet         *v1alpha1.VirtualNetwork
		triggerCount int
		lastPayload  map[string]any
	)

	BeforeEach(func() {
		ctx = context.Background()
		triggerCount = 0
		lastPayload = nil
		scheme := runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		k8sClient = fake.NewClientBuilder().WithScheme(scheme).
			WithStatusSubresource(&v1alpha1.FabricDomain{}, &v1alpha1.VirtualNetwork{}).Build()

		mockProvider = &mockVirtualNetworkProvider{
			triggerProvisionFunc: func(ctx context.Context, resource client.Object) (*provisioning.ProvisionResult, error) {
				persisted := &v1alpha1.FabricDomain{}
				if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(resource), persisted); err != nil {
					return nil, err
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

		networkClass := privatev1.NetworkClass_builder{
			Id: "nc-1",
			Spec: privatev1.NetworkClassSpec_builder{
				EastWestConfig: privatev1.EastWestConfig_builder{
					EthernetEw: privatev1.EthernetEastWestConfig_builder{TemplateId: "42"}.Build(),
				}.Build(),
			}.Build(),
		}.Build()
		networkClassesClient := &stubNetworkClassesClient{
			getFunc: func(_ context.Context, req *privatev1.NetworkClassesGetRequest, _ ...grpc.CallOption) (*privatev1.NetworkClassesGetResponse, error) {
				Expect(req.GetId()).To(Equal("nc-1"))
				return privatev1.NetworkClassesGetResponse_builder{Object: networkClass}.Build(), nil
			},
		}

		reconciler = &FabricDomainReconciler{
			Client:                     k8sClient,
			APIReader:                  k8sClient,
			NetworkingNamespace:        namespace,
			ProvisioningProvider:       mockProvider,
			NetworkClassesClient:       networkClassesClient,
			StatusPollInterval:         time.Second,
			MaxJobHistory:              10,
			NetworkProvisioningEnabled: true,
		}
		vnet = &v1alpha1.VirtualNetwork{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "tenant-vnet",
				Namespace: namespace,
				Labels:    map[string]string{osacVirtualNetworkIDLabel: vnetID},
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

	It("resolves the NetworkClass and VPC, provisions, and records AAP outputs", func() {
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
	})

	It("rejects a ServerCluster reported in another VPC", func() {
		domain.Status.VPCID = "7"
		domain.Status.ProvisioningJobs = []v1alpha1.JobStatus{{
			JobID: "create-1", Type: v1alpha1.JobTypeProvision, State: v1alpha1.JobStateSucceeded,
		}}
		fabricDomainPollCallbacks(domain, "desired").OnSuccess(provisioning.ProvisionStatus{
			Outputs: map[string]any{"server_cluster_id": "42", "server_cluster_vpc_id": "8"},
		})
		Expect(domain.Status.Phase).To(Equal(v1alpha1.FabricDomainPhaseFailed))
		Expect(domain.Status.BackendID).To(Equal("42"), "retain the exact ID for safe cleanup")
		Expect(domain.Status.VPCID).To(Equal("7"))
		Expect(domain.Status.ProvisioningJobs[0].State).To(Equal(v1alpha1.JobStateFailed))
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
		Expect(condition.Message).To(ContainSubstring("Netris rejected"))
		Expect(updated.Status.Members).To(ConsistOf(
			v1alpha1.FabricDomainMemberStatus{Server: "server-a", State: v1alpha1.FabricDomainMemberStateFailed, Message: "Netris rejected the request"},
			v1alpha1.FabricDomainMemberStatus{Server: "server-b", State: v1alpha1.FabricDomainMemberStateFailed, Message: "Netris rejected the request"},
		))
	})
})
