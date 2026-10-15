/*
Copyright 2025.

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
	"errors"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type preparationDeniedReader struct{ client.Reader }

func (r preparationDeniedReader) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if _, ok := list.(*corev1.SecretList); ok {
		return errors.New("fixture denied")
	}
	return r.Reader.List(ctx, list, opts...)
}

// Fulfillment enforces Tenant ID == resource name; Kubernetes UID is separate.
func prepareStorageTenant(ctx context.Context, name string) *v1alpha1.Tenant {
	tenant := &v1alpha1.Tenant{}
	Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: name}, tenant)).To(Succeed())
	tenant.Labels = map[string]string{osacTenantIDLabel: name}
	Expect(k8sClient.Update(ctx, tenant)).To(Succeed())
	return tenant
}

func createStoragePreparation(ctx context.Context, tenant *v1alpha1.Tenant, backendName, name string) *corev1.Secret {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "osac-storage"}}
	Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, ns))).To(Succeed())
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "osac-storage", Name: name, Labels: map[string]string{
			"osac.openshift.io/storage-backend": backendName,
			osacTenantKey:                       tenant.Name,
		}},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{"username": []byte("tenant-user"), "password": []byte("fixture-runtime-password"), "svm": []byte("admin_chosen_svm")},
	}
	Expect(k8sClient.Create(ctx, s)).To(Succeed())
	DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, s))).To(Succeed()) })
	return s
}

var _ = Describe("NetApp storage preparation", func() {
	ctx := context.Background()
	var r *StorageReconciler
	var tenant *v1alpha1.Tenant
	var provisions int

	BeforeEach(func() {
		name := fmt.Sprintf("preparation-%d", time.Now().UnixNano())
		createReadyTenantForStorage(ctx, name, "default")
		tenant = prepareStorageTenant(ctx, name)
		provisions = 0
		provider := &mockProvisioningProvider{triggerProvisionFunc: func(context.Context, client.Object) (*provisioning.ProvisionResult, error) {
			provisions++
			return &provisioning.ProvisionResult{JobID: fmt.Sprint(provisions), InitialState: v1alpha1.JobStatePending}, nil
		}}
		r = NewStorageReconciler(testMcManager, "default", mcmanager.LocalCluster, provider, provider, time.Second, provisioning.DefaultMaxJobHistory)
		r.TiersClient = &mockStorageTiersLister{listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
			return privatev1.StorageTiersListResponse_builder{Items: []*privatev1.StorageTier{
				newTestStorageTier("fast", "backend-1"), newTestStorageTier("standard", "backend-1"),
			}}.Build(), nil
		}}
		backends := registeredBackendsClient(1)
		backends.getFunc = func(_ context.Context, req *privatev1.StorageBackendsGetRequest, _ ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error) {
			resp := newTestStorageBackendGetResponse("ontap")
			if req.GetId() == "backend-2" {
				resp.GetObject().GetMetadata().SetName("ontap-secondary")
			}
			return resp, nil
		}
		r.BackendsClient = backends
	})

	reconcile := func() {
		GinkgoHelper()
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(tenant), tenant)).To(Succeed())
		Eventually(func(g Gomega) {
			cached := &v1alpha1.Tenant{}
			g.Expect(r.Get(ctx, client.ObjectKeyFromObject(tenant), cached)).To(Succeed())
			g.Expect(cached.ResourceVersion).To(Equal(tenant.ResourceVersion))
		}, 5*time.Second, 10*time.Millisecond).Should(Succeed())
		result, err := r.Reconcile(ctx, storageReconcileRequest(client.ObjectKeyFromObject(tenant)))
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(time.Second))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(tenant), tenant)).To(Succeed())
	}

	expectWaiting := func(reason string) {
		GinkgoHelper()
		reconcile()
		Expect(provisions).To(BeZero())
		for _, typ := range []v1alpha1.TenantConditionType{v1alpha1.TenantConditionStorageBackendReady, v1alpha1.TenantConditionClusterStorageReady} {
			c := tenant.GetStatusCondition(typ)
			Expect(c).NotTo(BeNil())
			Expect(c.Status).To(Equal(metav1.ConditionFalse))
			Expect(c.Reason).To(Equal(reason))
			Expect(c.ObservedGeneration).To(Equal(tenant.Generation))
			Expect(c.Message).NotTo(ContainSubstring("fixture-runtime-password"))
		}
	}

	It("waits without AAP until the admin creates a preparation, then starts once", func() {
		expectWaiting("PreparationMissing")
		s := createStoragePreparation(ctx, tenant, "ontap-primary", tenant.Name)
		reconcile()
		Expect(provisions).To(Equal(1))
		Expect(tenant.GetStatusCondition(v1alpha1.TenantConditionStorageBackendReady).Status).To(Equal(metav1.ConditionFalse))
		Expect(tenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady).Status).To(Equal(metav1.ConditionFalse))
		reconcile()
		Expect(provisions).To(Equal(1), "an active job must be polled instead of duplicated")
		persisted := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(s), persisted)).To(Succeed())
		Expect(persisted.Data).To(Equal(s.Data))
		Expect(persisted.Annotations).To(BeEmpty(), "the admin preparation remains read-only")
	})

	It("passes one runtime preparation for two tiers while preserving discovery credentials", func() {
		s := createStoragePreparation(ctx, tenant, "ontap-primary", tenant.Name)
		r.BackendProvider = &mockProvisioningProvider{triggerProvisionFunc: func(ctx context.Context, _ client.Object) (*provisioning.ProvisionResult, error) {
			provisions++
			connections := provisioning.StorageBackendConnectionsFromContext(ctx)
			Expect(connections).To(HaveLen(1))
			Expect(connections["backend-1"].Password).To(Equal(testBackendPassword))
			p := connections["backend-1"].TenantPreparation
			Expect(p).NotTo(BeNil())
			Expect(p.TenantID).To(Equal(tenant.Labels[osacTenantIDLabel]))
			Expect(p.UID).To(Equal(string(s.UID)))
			Expect(p.ProviderConfig).To(Equal(map[string]any{"username": "tenant-user", "password": "fixture-runtime-password", "svm": "admin_chosen_svm"}))
			Expect(provisioning.StorageTierDefinitionsFromContext(ctx)).To(HaveLen(2))
			return &provisioning.ProvisionResult{JobID: "1", InitialState: v1alpha1.JobStatePending}, nil
		}}
		reconcile()
		Expect(provisions).To(Equal(1))
	})

	It("waits for every backend preparation before dispatching", func() {
		createStoragePreparation(ctx, tenant, "ontap-primary", tenant.Name+"-a")
		r.TiersClient = &mockStorageTiersLister{listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
			return privatev1.StorageTiersListResponse_builder{Items: []*privatev1.StorageTier{
				newTestStorageTier("fast", "backend-1"), newTestStorageTier("standard", "backend-2"),
			}}.Build(), nil
		}}
		expectWaiting("PreparationMissing")
		createStoragePreparation(ctx, tenant, "ontap-secondary", tenant.Name+"-b")
		reconcile()
		Expect(provisions).To(Equal(1))
	})

	It("keeps readiness false until both jobs and all tier classes are present", func() {
		s := createStoragePreparation(ctx, tenant, "ontap-primary", tenant.Name)
		createLabeledStorageClass(ctx, tenant.Name+"-fast", tenant.Name, "fast")
		createLabeledStorageClass(ctx, tenant.Name+"-standard", tenant.Name, "standard")
		reconcile()
		Expect(tenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady).Status).To(Equal(metav1.ConditionFalse))
		Expect(tenant.Status.StorageBackends).To(ConsistOf(v1alpha1.StorageBackendStatus{
			Name: "backend-1", Provider: "ontap", Ready: false, Message: "Waiting for storage backend setup to complete",
		}))
		reconcile() // Observe backend setup success.
		reconcile() // Start native configuration.
		Expect(provisions).To(Equal(2))
		Expect(tenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady).Status).To(Equal(metav1.ConditionFalse))
		Expect(tenant.Status.StorageBackends).To(ConsistOf(v1alpha1.StorageBackendStatus{
			Name: "backend-1", Provider: "ontap", Ready: true, Message: "Storage backend setup completed",
		}))
		reconcile() // Observe native configuration success.
		reconcile()
		for _, typ := range []v1alpha1.TenantConditionType{v1alpha1.TenantConditionStorageBackendReady, v1alpha1.TenantConditionClusterStorageReady} {
			Expect(tenant.GetStatusCondition(typ).Status).To(Equal(metav1.ConditionTrue))
			Expect(tenant.GetStatusCondition(typ).ObservedGeneration).To(Equal(tenant.Generation))
		}
		Expect(tenant.Status.StorageClasses).To(HaveLen(2))
		Expect(provisions).To(Equal(2))
		Expect(k8sClient.Delete(ctx, s)).To(Succeed())
		reconcile()
		Expect(provisions).To(Equal(2), "missing preparation must not launch another job")
		Expect(tenant.GetStatusCondition(v1alpha1.TenantConditionStorageBackendReady).Reason).To(Equal("PreparationMissing"))
		Expect(tenant.Status.StorageBackends).To(HaveLen(1))
		Expect(tenant.Status.StorageBackends[0].Ready).To(BeFalse(), "backend details must not retain stale readiness")
	})

	It("reports unavailable source reads without launching a job", func() {
		r.APIReader = preparationDeniedReader{r.APIReader}
		expectWaiting("PreparationUnavailable")
	})

	It("waits with valid preparation when AAP is not configured", func() {
		createStoragePreparation(ctx, tenant, "ontap-primary", tenant.Name)
		r.BackendProvider = nil
		expectWaiting(v1alpha1.TenantReasonBackendConfiguredNoProvider)
	})

	It("does not reuse legacy successful jobs for newly configured NetApp storage", func() {
		createStoragePreparation(ctx, tenant, "ontap-primary", tenant.Name)
		createLabeledStorageClass(ctx, tenant.Name+"-fast", tenant.Name, "fast")
		createLabeledStorageClass(ctx, tenant.Name+"-standard", tenant.Name, "standard")
		legacy := v1alpha1.JobStatus{JobID: "legacy", Type: v1alpha1.JobTypeProvision, State: v1alpha1.JobStateSucceeded, Timestamp: metav1.Now()}
		tenant.Status.StorageBackendJobs = []v1alpha1.JobStatus{legacy}
		tenant.Status.ClusterStorageJobs = []v1alpha1.JobStatus{legacy}
		Expect(k8sClient.Status().Update(ctx, tenant)).To(Succeed())
		reconcile()
		Expect(provisions).To(Equal(1))
		Expect(tenant.GetStatusCondition(v1alpha1.TenantConditionStorageBackendReady).Status).To(Equal(metav1.ConditionFalse))
	})

	It("repairs missing classes after a successful native job", func() {
		createStoragePreparation(ctx, tenant, "ontap-primary", tenant.Name)
		for range 4 {
			reconcile()
		}
		Expect(provisions).To(Equal(2))
		reconcile()
		Expect(provisions).To(Equal(3))
		Expect(tenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady).Status).To(Equal(metav1.ConditionFalse))
	})

	It("revalidates a changed SVM while deferring password rotation", func() {
		s := createStoragePreparation(ctx, tenant, "ontap-primary", tenant.Name)
		createLabeledStorageClass(ctx, tenant.Name+"-fast", tenant.Name, "fast")
		createLabeledStorageClass(ctx, tenant.Name+"-standard", tenant.Name, "standard")
		for range 5 {
			reconcile()
		}
		Expect(provisions).To(Equal(2))
		s.Data["password"] = []byte("fixture-rotated-password")
		Expect(k8sClient.Update(ctx, s)).To(Succeed())
		reconcile()
		Expect(provisions).To(Equal(2))
		s.Data["svm"] = []byte("different_svm")
		Expect(k8sClient.Update(ctx, s)).To(Succeed())
		reconcile()
		Expect(provisions).To(Equal(3))
		Expect(tenant.GetStatusCondition(v1alpha1.TenantConditionStorageBackendReady).Status).To(Equal(metav1.ConditionFalse))
	})

	It("holds deletion after partial setup when its catalog cannot be recovered", func() {
		s := createStoragePreparation(ctx, tenant, "ontap-primary", tenant.Name)
		reconcile()
		Expect(k8sClient.Delete(ctx, tenant)).To(Succeed())
		r.TiersClient = &mockStorageTiersLister{listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
			return nil, errors.New("catalog unavailable")
		}}
		reconcile()
		Expect(tenant.Finalizers).To(ContainElement(storageFinalizer))
		Expect(tenant.Status.ClusterStorageJobs).To(BeEmpty())
		Expect(tenant.GetStatusCondition(v1alpha1.TenantConditionStorageBackendReady).Reason).To(Equal("ConfigurationUnavailable"))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(s), &corev1.Secret{})).To(Succeed())
	})

	It("uses the Tenant name without requiring a tenant-ID label", func() {
		tenant.Labels = nil
		Expect(k8sClient.Update(ctx, tenant)).To(Succeed())
		createStoragePreparation(ctx, tenant, "ontap-primary", "arbitrary-admin-name")
		r.BackendProvider = &mockProvisioningProvider{triggerProvisionFunc: func(ctx context.Context, _ client.Object) (*provisioning.ProvisionResult, error) {
			provisions++
			p := provisioning.StorageBackendConnectionsFromContext(ctx)["backend-1"].TenantPreparation
			Expect(p.TenantID).To(Equal(tenant.Name))
			Expect(p.Name).To(Equal("arbitrary-admin-name"))
			return &provisioning.ProvisionResult{JobID: "1", InitialState: v1alpha1.JobStatePending}, nil
		}}
		reconcile()
		Expect(provisions).To(Equal(1))
	})

	It("waits if the catalog omits the backend name", func() {
		r.BackendsClient.(*mockStorageBackendsClient).getFunc = func(context.Context, *privatev1.StorageBackendsGetRequest, ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error) {
			resp := newTestStorageBackendGetResponse("ontap")
			resp.GetObject().GetMetadata().SetName("")
			return resp, nil
		}
		expectWaiting("ConfigurationUnavailable")
	})

	It("ignores a matching preparation in the workload namespace", func() {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "trident"}}
		Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, ns))).To(Succeed())
		s := createStoragePreparation(ctx, tenant, "ontap-primary", tenant.Name)
		Expect(k8sClient.Delete(ctx, s)).To(Succeed())
		s.ResourceVersion, s.UID = "", ""
		s.Namespace = "trident"
		Expect(k8sClient.Create(ctx, s)).To(Succeed())
		expectWaiting("PreparationMissing")
	})

	It("rejects ambiguous preparations instead of selecting the first", func() {
		createStoragePreparation(ctx, tenant, "ontap-primary", tenant.Name+"-a")
		createStoragePreparation(ctx, tenant, "ontap-primary", tenant.Name+"-b")
		expectWaiting("PreparationAmbiguous")
	})

	It("rejects non-Opaque preparation Secrets", func() {
		s := createStoragePreparation(ctx, tenant, "ontap-primary", tenant.Name)
		Expect(k8sClient.Delete(ctx, s)).To(Succeed())
		s.ResourceVersion, s.UID = "", ""
		s.Type = corev1.SecretTypeBasicAuth
		Expect(k8sClient.Create(ctx, s)).To(Succeed())
		expectWaiting("PreparationInvalid")
	})

	DescribeTable("rejects invalid preparations", func(change func(*corev1.Secret)) {
		s := createStoragePreparation(ctx, tenant, "ontap-primary", tenant.Name)
		change(s)
		Expect(k8sClient.Update(ctx, s)).To(Succeed())
		expectWaiting("PreparationInvalid")
	},
		Entry("missing username", func(s *corev1.Secret) { delete(s.Data, "username") }),
		Entry("empty password", func(s *corev1.Secret) { s.Data["password"] = nil }),
		Entry("blank SVM", func(s *corev1.Secret) { s.Data["svm"] = []byte(" ") }),
		Entry("deleting Secret", func(s *corev1.Secret) {
			s.Finalizers = []string{"fixture/hold"}
			Expect(k8sClient.Update(ctx, s)).To(Succeed())
			Expect(k8sClient.Delete(ctx, s)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(s), s)).To(Succeed())
			DeferCleanup(func() { s.Finalizers = nil; Expect(k8sClient.Update(ctx, s)).To(Succeed()) })
		}),
	)

	DescribeTable("ignores records outside the authorized assignment", func(change func(*corev1.Secret)) {
		s := createStoragePreparation(ctx, tenant, "ontap-primary", tenant.Name)
		change(s)
		Expect(k8sClient.Update(ctx, s)).To(Succeed())
		expectWaiting("PreparationMissing")
	},
		Entry("another tenant", func(s *corev1.Secret) { s.Labels[osacTenantKey] = "unrelated" }),
		Entry("another backend", func(s *corev1.Secret) { s.Labels["osac.openshift.io/storage-backend"] = "unrelated" }),
		Entry("backend ID in place of name", func(s *corev1.Secret) { s.Labels["osac.openshift.io/storage-backend"] = "backend-1" }),
		Entry("missing tenant label", func(s *corev1.Secret) { delete(s.Labels, osacTenantKey) }),
		Entry("legacy ID and purpose labels only", func(s *corev1.Secret) {
			s.Labels = map[string]string{"osac.openshift.io/storage-backend-id": "backend-1", osacTenantIDLabel: tenant.Name, "osac.openshift.io/secret-type": "storage-tenant-preparation"}
		}),
	)
})
