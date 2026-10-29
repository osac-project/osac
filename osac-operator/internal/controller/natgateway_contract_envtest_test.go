package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	v1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/networkmanager"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("NATGateway v1 Envtest", func() {
	const (
		natGatewayID     = "11111111-1111-4111-8111-111111111111"
		virtualNetworkID = "22222222-2222-4222-8222-222222222222"
		externalIPID     = "33333333-3333-4333-8333-333333333333"
	)

	var (
		namespaceName  string
		namespaceID    int
		reconciler     *NATGatewayReconciler
		provider       *mockProvisioningProvider
		virtualNetwork *v1alpha1.VirtualNetwork
		externalIP     *v1alpha1.ExternalIP
		natGateway     *v1alpha1.NATGateway
		newReconciler  func() *NATGatewayReconciler
	)

	BeforeEach(func() {
		namespaceID++
		namespaceName = fmt.Sprintf("natgateway-contract-test-%d", namespaceID)
		Expect(k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespaceName}})).To(Succeed())

		managerConfig := newFabricManagerConfigMap("agentless-net", namespaceName, "agentless_net")
		managerConfig.Data[networkmanager.DataKeyNATGatewayContractVersion] = networkmanager.NATGatewayContractVersionV1
		Expect(k8sClient.Create(ctx, managerConfig)).To(Succeed())

		virtualNetwork = &v1alpha1.VirtualNetwork{
			ObjectMeta: metav1.ObjectMeta{
				Name: "network", Namespace: namespaceName,
				Labels:      map[string]string{osacVirtualNetworkIDLabel: virtualNetworkID},
				Annotations: map[string]string{osacTenantKey: "tenant-a"},
			},
			Spec: v1alpha1.VirtualNetworkSpec{
				Region: "region-a", NetworkClass: "network-class", IPv4CIDR: "10.20.0.0/16",
			},
		}
		Expect(k8sClient.Create(ctx, virtualNetwork)).To(Succeed())
		virtualNetwork.Status.Phase = v1alpha1.VirtualNetworkPhaseReady
		Expect(k8sClient.Status().Update(ctx, virtualNetwork)).To(Succeed())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(virtualNetwork), virtualNetwork)).To(Succeed())

		externalIP = &v1alpha1.ExternalIP{
			ObjectMeta: metav1.ObjectMeta{
				Name: "external-ip", Namespace: namespaceName,
				Labels:      map[string]string{osacExternalIPIDLabel: externalIPID},
				Annotations: map[string]string{osacTenantKey: "tenant-a"},
			},
			Spec: v1alpha1.ExternalIPSpec{Pool: "pool"},
		}
		Expect(k8sClient.Create(ctx, externalIP)).To(Succeed())
		externalIP.Status = v1alpha1.ExternalIPStatus{
			Phase:   v1alpha1.ExternalIPPhaseReady,
			State:   v1alpha1.ExternalIPStateAllocated,
			Address: "198.51.100.10",
		}
		Expect(k8sClient.Status().Update(ctx, externalIP)).To(Succeed())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(externalIP), externalIP)).To(Succeed())

		resolver, _ := wireExternalIPDispatcher(k8sClient, namespaceName, []*privatev1.NetworkClass{{
			Id: "network-class", FabricManager: ptr.To("agentless_net"),
		}})
		provider = &mockProvisioningProvider{name: "mock-aap"}
		newReconciler = func() *NATGatewayReconciler {
			return &NATGatewayReconciler{
				Client: k8sClient, APIReader: k8sClient, Scheme: k8sClient.Scheme(),
				NetworkingNamespace: namespaceName, ProvisioningProvider: provider,
				StatusPollInterval: time.Millisecond, MaxJobHistory: 10,
				NetworkProvisioningEnabled: true, Resolver: resolver,
			}
		}
		reconciler = newReconciler()
	})

	AfterEach(func() {
		if natGateway != nil {
			current := &v1alpha1.NATGateway{}
			if k8sClient.Get(ctx, client.ObjectKeyFromObject(natGateway), current) == nil {
				current.Finalizers = nil
				_ = k8sClient.Update(ctx, current)
				_ = k8sClient.Delete(ctx, current)
			}
		}
		if externalIP != nil {
			_ = k8sClient.Delete(ctx, externalIP)
		}
		if virtualNetwork != nil {
			_ = k8sClient.Delete(ctx, virtualNetwork)
		}
		_ = k8sClient.Delete(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "agentless-net", Namespace: namespaceName}})
		_ = k8sClient.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespaceName}})
	})

	createNATGateway := func() {
		natGateway = &v1alpha1.NATGateway{
			ObjectMeta: metav1.ObjectMeta{
				Name: "nat-gateway", Namespace: namespaceName,
				Labels:      map[string]string{osacNATGatewayIDLabel: natGatewayID},
				Annotations: map[string]string{osacTenantKey: "tenant-a"},
			},
			Spec: v1alpha1.NATGatewaySpec{
				VirtualNetwork: virtualNetworkID,
				ExternalIP:     externalIPID,
			},
		}
		Expect(k8sClient.Create(ctx, natGateway)).To(Succeed())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(natGateway), natGateway)).To(Succeed())
	}

	contextFor := func(gateway *v1alpha1.NATGateway) provisioning.NATGatewayContext {
		return provisioning.NATGatewayContext{
			ExternalIP: provisioning.NATGatewayExternalIPContext{
				Name: externalIP.Name, Namespace: externalIP.Namespace,
				FulfillmentID: externalIP.Labels[osacExternalIPIDLabel],
				KubernetesUID: string(externalIP.UID), TenantID: "tenant-a",
				Address: externalIP.Status.Address,
				State:   string(externalIP.Status.State), Phase: string(externalIP.Status.Phase),
			},
			VirtualNetwork: provisioning.NATGatewayVirtualNetworkContext{
				Name: virtualNetwork.Name, Namespace: virtualNetwork.Namespace,
				FulfillmentID: virtualNetwork.Labels[osacVirtualNetworkIDLabel],
				KubernetesUID: string(virtualNetwork.UID), TenantID: "tenant-a",
				IPv4CIDR: virtualNetwork.Spec.IPv4CIDR,
				Phase:    string(virtualNetwork.Status.Phase),
			},
		}
	}

	natGatewayInputs := func(resource *v1alpha1.NATGateway, snapshot *provisioning.NATGatewayContext) map[string]any {
		resourceJSON, err := json.Marshal(resource)
		Expect(err).NotTo(HaveOccurred())
		var resourceMap map[string]any
		Expect(json.Unmarshal(resourceJSON, &resourceMap)).To(Succeed())
		jobVars := map[string]any{"resource": resourceMap}
		if snapshot != nil {
			snapshotJSON, err := json.Marshal(snapshot)
			Expect(err).NotTo(HaveOccurred())
			var snapshotMap map[string]any
			Expect(json.Unmarshal(snapshotJSON, &snapshotMap)).To(Succeed())
			jobVars["nat_gateway_context"] = snapshotMap
		}
		return map[string]any{"osac_job_vars": jobVars}
	}

	reconcileUntil := func(request mcreconcile.Request, condition func(*v1alpha1.NATGateway) bool) *v1alpha1.NATGateway {
		for range 8 {
			_, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			current := &v1alpha1.NATGateway{}
			if getErr := k8sClient.Get(ctx, request.NamespacedName, current); getErr == nil && condition(current) {
				return current
			}
		}
		current := &v1alpha1.NATGateway{}
		Expect(k8sClient.Get(ctx, request.NamespacedName, current)).To(Succeed())
		return current
	}

	It("marks Ready only after validating the same-job ensure artifact", func() {
		createNATGateway()
		provider.getProvisionStatusWithExtraVarsFunc = func(_ context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatusWithExtraVars, error) {
			gateway := resource.(*v1alpha1.NATGateway)
			return provisioning.ProvisionStatusWithExtraVars{
				ProvisionStatus: provisioning.ProvisionStatus{JobID: jobID, State: v1alpha1.JobStateSucceeded},
				InputExtraVars:  natGatewayInputs(gateway, ptr.To(contextFor(gateway))),
				ExtraVars: map[string]any{"osac_result": map[string]any{
					"schemaVersion": "v1", "operation": provisioning.NATGatewayEnsureOperation,
					"resourceUID": string(gateway.UID), "observedGeneration": gateway.Generation,
					"data": map[string]any{},
				}},
			}, nil
		}

		request := mcreconcile.Request{Request: ctrl.Request{NamespacedName: client.ObjectKeyFromObject(natGateway)}}
		for range 3 {
			_, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
		}
		triggered := &v1alpha1.NATGateway{}
		Expect(k8sClient.Get(ctx, request.NamespacedName, triggered)).To(Succeed())
		latestProvision := provisioning.FindLatestJobByType(triggered.Status.ProvisioningJobs, v1alpha1.JobTypeProvision)
		Expect(latestProvision).NotTo(BeNil())
		Expect(latestProvision.JobID).To(Equal("mock-job-id"))

		// A replacement reconciler must recover the pending job from persisted status.
		reconciler = newReconciler()
		current := reconcileUntil(request, func(gateway *v1alpha1.NATGateway) bool {
			return gateway.Status.Phase == v1alpha1.NATGatewayPhaseReady
		})
		Expect(current.Status.Phase).To(Equal(v1alpha1.NATGatewayPhaseReady))
		Expect(current.Annotations[osacNATGatewayContractVersionAnnotation]).To(Equal(networkmanager.NATGatewayContractVersionV1))
	})

	It("keeps the desired gateway configuration independent of Subnet lifecycle", func() {
		createNATGateway()
		provider.getProvisionStatusWithExtraVarsFunc = func(_ context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatusWithExtraVars, error) {
			gateway := resource.(*v1alpha1.NATGateway)
			return provisioning.ProvisionStatusWithExtraVars{
				ProvisionStatus: provisioning.ProvisionStatus{JobID: jobID, State: v1alpha1.JobStateSucceeded},
				InputExtraVars:  natGatewayInputs(gateway, ptr.To(contextFor(gateway))),
				ExtraVars: map[string]any{"osac_result": map[string]any{
					"schemaVersion": "v1", "operation": provisioning.NATGatewayEnsureOperation,
					"resourceUID": string(gateway.UID), "observedGeneration": gateway.Generation,
					"data": map[string]any{},
				}},
			}, nil
		}

		request := mcreconcile.Request{Request: ctrl.Request{NamespacedName: client.ObjectKeyFromObject(natGateway)}}
		current := reconcileUntil(request, func(gateway *v1alpha1.NATGateway) bool {
			return gateway.Status.Phase == v1alpha1.NATGatewayPhaseReady
		})
		desiredConfigVersion := current.Status.DesiredConfigVersion
		provisioningJobs := append([]v1alpha1.JobStatus(nil), current.Status.ProvisioningJobs...)

		subnet := &v1alpha1.Subnet{
			ObjectMeta: metav1.ObjectMeta{
				Name: "subnet", Namespace: namespaceName,
				Labels:      map[string]string{osacSubnetIDLabel: "44444444-4444-4444-8444-444444444444"},
				Annotations: map[string]string{osacTenantKey: "tenant-a"},
			},
			Spec: v1alpha1.SubnetSpec{VirtualNetwork: virtualNetworkID, IPv4CIDR: "10.20.1.0/24"},
		}
		Expect(k8sClient.Create(ctx, subnet)).To(Succeed())
		_, err := reconciler.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())

		unchanged := &v1alpha1.NATGateway{}
		Expect(k8sClient.Get(ctx, request.NamespacedName, unchanged)).To(Succeed())
		Expect(unchanged.Status.Phase).To(Equal(v1alpha1.NATGatewayPhaseReady))
		Expect(unchanged.Status.DesiredConfigVersion).To(Equal(desiredConfigVersion))
		Expect(unchanged.Status.ProvisioningJobs).To(Equal(provisioningJobs))

		Expect(k8sClient.Delete(ctx, subnet)).To(Succeed())
		_, err = reconciler.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Get(ctx, request.NamespacedName, unchanged)).To(Succeed())
		Expect(unchanged.Status.Phase).To(Equal(v1alpha1.NATGatewayPhaseReady))
		Expect(unchanged.Status.DesiredConfigVersion).To(Equal(desiredConfigVersion))
		Expect(unchanged.Status.ProvisioningJobs).To(Equal(provisioningJobs))
	})

	It("rejects a successful ensure when the ExternalIP snapshot changed during the job", func() {
		createNATGateway()
		provider.getProvisionStatusWithExtraVarsFunc = func(_ context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatusWithExtraVars, error) {
			gateway := resource.(*v1alpha1.NATGateway)
			snapshot := contextFor(gateway)
			currentExternalIP := &v1alpha1.ExternalIP{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(externalIP), currentExternalIP)).To(Succeed())
			currentExternalIP.Status.Address = "198.51.100.11"
			Expect(k8sClient.Status().Update(ctx, currentExternalIP)).To(Succeed())
			return provisioning.ProvisionStatusWithExtraVars{
				ProvisionStatus: provisioning.ProvisionStatus{JobID: jobID, State: v1alpha1.JobStateSucceeded},
				InputExtraVars:  natGatewayInputs(gateway, &snapshot),
				ExtraVars: map[string]any{"osac_result": map[string]any{
					"schemaVersion": "v1", "operation": provisioning.NATGatewayEnsureOperation,
					"resourceUID": string(gateway.UID), "observedGeneration": gateway.Generation,
					"data": map[string]any{},
				}},
			}, nil
		}

		request := mcreconcile.Request{Request: ctrl.Request{NamespacedName: client.ObjectKeyFromObject(natGateway)}}
		current := reconcileUntil(request, func(gateway *v1alpha1.NATGateway) bool {
			return gateway.Status.Phase == v1alpha1.NATGatewayPhaseFailed
		})
		Expect(current.Status.Phase).To(Equal(v1alpha1.NATGatewayPhaseFailed))
		Expect(provisioning.IsConfigApplied(&current.Status.ProvisioningJobs, current.Status.DesiredConfigVersion)).To(BeFalse())
	})

	It("retains the delete finalizer when the same-job cleanup result is stale", func() {
		createNATGateway()
		provider.getProvisionStatusWithExtraVarsFunc = func(_ context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatusWithExtraVars, error) {
			gateway := resource.(*v1alpha1.NATGateway)
			return provisioning.ProvisionStatusWithExtraVars{
				ProvisionStatus: provisioning.ProvisionStatus{JobID: jobID, State: v1alpha1.JobStateSucceeded},
				InputExtraVars:  natGatewayInputs(gateway, ptr.To(contextFor(gateway))),
				ExtraVars: map[string]any{"osac_result": map[string]any{
					"schemaVersion": "v1", "operation": provisioning.NATGatewayEnsureOperation,
					"resourceUID": string(gateway.UID), "observedGeneration": gateway.Generation,
					"data": map[string]any{},
				}},
			}, nil
		}
		request := mcreconcile.Request{Request: ctrl.Request{NamespacedName: client.ObjectKeyFromObject(natGateway)}}
		current := reconcileUntil(request, func(gateway *v1alpha1.NATGateway) bool {
			return gateway.Status.Phase == v1alpha1.NATGatewayPhaseReady
		})
		Expect(current.Status.Phase).To(Equal(v1alpha1.NATGatewayPhaseReady))

		provider.getDeprovisionStatusWithExtraVarsFunc = func(_ context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatusWithExtraVars, error) {
			gateway := resource.(*v1alpha1.NATGateway)
			inputs := natGatewayInputs(gateway, nil)
			return provisioning.ProvisionStatusWithExtraVars{
				ProvisionStatus: provisioning.ProvisionStatus{JobID: jobID, State: v1alpha1.JobStateSucceeded},
				InputExtraVars:  inputs,
				ExtraVars: map[string]any{"osac_result": map[string]any{
					"schemaVersion": "v1", "operation": provisioning.NATGatewayDeleteOperation,
					"resourceUID": string(gateway.UID), "observedGeneration": gateway.Generation + 1,
					"data": map[string]any{},
				}},
			}, nil
		}
		Expect(k8sClient.Delete(ctx, current)).To(Succeed())
		for range 4 {
			_, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
		}
		retained := &v1alpha1.NATGateway{}
		Expect(k8sClient.Get(ctx, request.NamespacedName, retained)).To(Succeed())
		Expect(retained.Finalizers).To(ContainElement(osacNATGatewayFinalizer))
		latestDelete := provisioning.FindLatestJobByType(retained.Status.ProvisioningJobs, v1alpha1.JobTypeDeprovision)
		Expect(latestDelete).NotTo(BeNil())
		Expect(latestDelete.State).To(Equal(v1alpha1.JobStateFailed))
		Expect(latestDelete.BlockDeletionOnFailure).To(BeTrue())
	})
})
