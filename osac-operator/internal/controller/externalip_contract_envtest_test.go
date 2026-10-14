package controller

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	v1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/networkmanager"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("ExternalIP v1 Envtest", func() {
	const (
		poolID       = "22222222-2222-4222-8222-222222222222"
		externalIPID = "44444444-4444-4444-8444-444444444444"
	)

	var (
		namespaceName string
		namespaceID   int
		reconciler    *ExternalIPReconciler
		provider      *mockProvisioningProvider
		pool          *v1alpha1.ExternalIPPool
		externalIP    *v1alpha1.ExternalIP
	)

	BeforeEach(func() {
		namespaceID++
		namespaceName = fmt.Sprintf("externalip-contract-test-%d", namespaceID)
		Expect(k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespaceName}})).To(Succeed())
		manager := newFabricManagerConfigMap("agentless-net", namespaceName, "agentless_net")
		manager.Data[networkmanager.DataKeyExternalIPContractVersion] = networkmanager.ExternalIPContractVersionV1
		Expect(k8sClient.Create(ctx, manager)).To(Succeed())

		pool = &v1alpha1.ExternalIPPool{
			ObjectMeta: metav1.ObjectMeta{
				Name: "pool", Namespace: namespaceName,
				Labels:      map[string]string{osacExternalIPPoolIDLabel: poolID},
				Annotations: map[string]string{osacTenantKey: "tenant-a"},
			},
			Spec: v1alpha1.ExternalIPPoolSpec{IPFamily: "IPv4", CIDRs: []string{"198.51.100.0/24"}},
		}
		Expect(k8sClient.Create(ctx, pool)).To(Succeed())
		pool.Status = v1alpha1.ExternalIPPoolStatus{
			Phase: v1alpha1.ExternalIPPoolPhaseReady, Total: 254, Available: 254,
		}
		Expect(k8sClient.Status().Update(ctx, pool)).To(Succeed())
		latestPool := &v1alpha1.ExternalIPPool{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(pool), latestPool)).To(Succeed())
		Expect(latestPool.Status.Phase).To(Equal(v1alpha1.ExternalIPPoolPhaseReady))

		resolver, networkClasses := wireExternalIPDispatcher(k8sClient, namespaceName,
			[]*privatev1.NetworkClass{{Id: "network-class", FabricManager: ptr.To("agentless_net")}})
		provider = &mockProvisioningProvider{name: "mock-aap"}
		reconciler = &ExternalIPReconciler{
			Client: k8sClient, APIReader: k8sClient, Scheme: k8sClient.Scheme(),
			NetworkingNamespace: namespaceName, ProvisioningProvider: provider,
			StatusPollInterval: time.Second, MaxJobHistory: 10,
			NetworkProvisioningEnabled: true, Resolver: resolver,
			networkClassesClient: networkClasses,
		}
	})

	AfterEach(func() {
		if externalIP != nil {
			current := &v1alpha1.ExternalIP{}
			if k8sClient.Get(ctx, client.ObjectKeyFromObject(externalIP), current) == nil {
				current.Finalizers = nil
				_ = k8sClient.Update(ctx, current)
				_ = k8sClient.Delete(ctx, current)
			}
		}
		attachments := &v1alpha1.ExternalIPAttachmentList{}
		if k8sClient.List(ctx, attachments, client.InNamespace(namespaceName)) == nil {
			for i := range attachments.Items {
				item := &attachments.Items[i]
				item.Finalizers = nil
				_ = k8sClient.Update(ctx, item)
				_ = k8sClient.Delete(ctx, item)
			}
		}
		if pool != nil {
			_ = k8sClient.Delete(ctx, pool)
		}
		_ = k8sClient.Delete(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "agentless-net", Namespace: namespaceName}})
		_ = k8sClient.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespaceName}})
	})

	createExternalIP := func(status v1alpha1.ExternalIPStatus, finalizers []string) {
		externalIP = &v1alpha1.ExternalIP{
			ObjectMeta: metav1.ObjectMeta{
				Name: "external-ip", Namespace: namespaceName, Finalizers: finalizers,
				Labels:      map[string]string{osacExternalIPIDLabel: externalIPID},
				Annotations: map[string]string{osacTenantKey: "tenant-a"},
			},
			Spec: v1alpha1.ExternalIPSpec{Pool: poolID},
		}
		Expect(k8sClient.Create(ctx, externalIP)).To(Succeed())
		if status.State != "" || status.Phase != "" {
			externalIP.Status = status
			Expect(k8sClient.Status().Update(ctx, externalIP)).To(Succeed())
		}
	}

	It("publishes only the validated current allocation", func() {
		createExternalIP(v1alpha1.ExternalIPStatus{}, nil)
		provider.triggerProvisionFunc = func(_ context.Context, _ client.Object) (*provisioning.ProvisionResult, error) {
			return &provisioning.ProvisionResult{JobID: "allocate-job", InitialState: v1alpha1.JobStatePending}, nil
		}
		provider.getProvisionStatusWithExtraVarsFunc = func(_ context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatusWithExtraVars, error) {
			return provisioning.ProvisionStatusWithExtraVars{
				ProvisionStatus: provisioning.ProvisionStatus{JobID: jobID, State: v1alpha1.JobStateSucceeded},
				ExtraVars: map[string]any{"osac_result": map[string]any{
					"schemaVersion": "v1", "operation": "external_ip.allocate",
					"resourceUID": string(resource.GetUID()), "observedGeneration": resource.GetGeneration(),
					"data": map[string]any{"address": "198.51.100.1"},
				}},
			}, nil
		}

		request := mcreconcile.Request{Request: ctrl.Request{NamespacedName: client.ObjectKeyFromObject(externalIP)}}
		for range 5 {
			_, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
		}
		updated := &v1alpha1.ExternalIP{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(externalIP), updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(v1alpha1.ExternalIPPhaseReady), "status=%#v", updated.Status)
		Expect(updated.Status.Address).To(Equal("198.51.100.1"))
		Expect(updated.Status.State).To(Equal(v1alpha1.ExternalIPStateAllocated))
		Expect(updated.Status.Phase).To(Equal(v1alpha1.ExternalIPPhaseReady))
		Expect(updated.Annotations[osacExternalIPContractVersionAnnotation]).To(Equal("v1"))
		Expect(updated.Annotations).NotTo(HaveKey(osacExternalIPAllocatedAddressAnnotation))
	})

	It("waits for a terminating Attachment before provider release", func() {
		createExternalIP(v1alpha1.ExternalIPStatus{
			Phase:   v1alpha1.ExternalIPPhaseReady,
			State:   v1alpha1.ExternalIPStateAllocated,
			Address: "198.51.100.1",
			ProvisioningJobs: []v1alpha1.JobStatus{{
				JobID: "allocate-job", Type: v1alpha1.JobTypeProvision, Timestamp: metav1.Now(),
				State: v1alpha1.JobStateSucceeded, ConfigVersion: "allocated",
			}},
		}, []string{osacExternalIPFinalizer})
		computeInstance := "compute-instance"
		attachment := &v1alpha1.ExternalIPAttachment{
			ObjectMeta: metav1.ObjectMeta{Name: "attachment", Namespace: namespaceName, Finalizers: []string{"test-hold"}},
			Spec:       v1alpha1.ExternalIPAttachmentSpec{ExternalIP: externalIP.Name, ComputeInstance: &computeInstance},
		}
		Expect(k8sClient.Create(ctx, attachment)).To(Succeed())
		Expect(k8sClient.Delete(ctx, attachment)).To(Succeed())
		Expect(k8sClient.Delete(ctx, externalIP)).To(Succeed())
		terminatingExternalIP := &v1alpha1.ExternalIP{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(externalIP), terminatingExternalIP)).To(Succeed())
		terminatingAttachment := &v1alpha1.ExternalIPAttachment{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(attachment), terminatingAttachment)).To(Succeed())
		indexedClient := fake.NewClientBuilder().
			WithScheme(k8sClient.Scheme()).
			WithIndex(&v1alpha1.ExternalIPAttachment{}, externalIPReferenceIndexField, func(obj client.Object) []string {
				return []string{obj.(*v1alpha1.ExternalIPAttachment).Spec.ExternalIP}
			}).
			WithIndex(&v1alpha1.NATGateway{}, externalIPReferenceIndexField, func(obj client.Object) []string {
				return []string{obj.(*v1alpha1.NATGateway).Spec.ExternalIP}
			}).
			WithObjects(terminatingExternalIP, terminatingAttachment).
			WithStatusSubresource(&v1alpha1.ExternalIP{}).
			Build()
		reconciler.Client = indexedClient
		reconciler.APIReader = indexedClient
		deprovisionCalls := 0
		provider.triggerDeprovisionFunc = func(_ context.Context, _ client.Object, _ []v1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
			deprovisionCalls++
			return &provisioning.DeprovisionResult{Action: provisioning.DeprovisionTriggered, JobID: "release-job"}, nil
		}

		request := mcreconcile.Request{Request: ctrl.Request{NamespacedName: client.ObjectKeyFromObject(externalIP)}}
		result, err := reconciler.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(defaultPreconditionRequeueInterval))
		Expect(deprovisionCalls).To(BeZero())

		terminating := &v1alpha1.ExternalIPAttachment{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(attachment), terminating)).To(Succeed())
		Expect(terminating.DeletionTimestamp.IsZero()).To(BeFalse())
	})
})
