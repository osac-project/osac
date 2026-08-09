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

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/osac-operator/internal/api/osac/private/v1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"
)

// mockStorageBackendsClient is a test double for StorageBackendsClient.
// By default List returns an empty response (total=0, no backends registered).
// Set listFunc/getFunc to override the response for specific test scenarios.
type mockStorageBackendsClient struct {
	listFunc     func(ctx context.Context, in *privatev1.StorageBackendsListRequest, opts ...grpc.CallOption) (*privatev1.StorageBackendsListResponse, error)
	getFunc      func(ctx context.Context, in *privatev1.StorageBackendsGetRequest, opts ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error)
	getCallCount int
}

func (m *mockStorageBackendsClient) List(ctx context.Context, in *privatev1.StorageBackendsListRequest, opts ...grpc.CallOption) (*privatev1.StorageBackendsListResponse, error) {
	if m.listFunc != nil {
		return m.listFunc(ctx, in, opts...)
	}
	return &privatev1.StorageBackendsListResponse{}, nil
}

func (m *mockStorageBackendsClient) Get(ctx context.Context, in *privatev1.StorageBackendsGetRequest, opts ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error) {
	m.getCallCount++
	return m.getFunc(ctx, in, opts...)
}

// registeredBackendsClient returns a mockStorageBackendsClient whose List response
// reports the given total number of registered backends.
func registeredBackendsClient(total int32) *mockStorageBackendsClient {
	return &mockStorageBackendsClient{
		listFunc: func(_ context.Context, _ *privatev1.StorageBackendsListRequest, _ ...grpc.CallOption) (*privatev1.StorageBackendsListResponse, error) {
			resp := &privatev1.StorageBackendsListResponse{}
			resp.SetTotal(total)
			return resp, nil
		},
	}
}

func storageReconcileRequest(nn types.NamespacedName) mcreconcile.Request {
	return mcreconcile.Request{Request: reconcile.Request{NamespacedName: nn}}
}

func createReadyTenantForStorage(ctx context.Context, name, namespace string) {
	tenant := &v1alpha1.Tenant{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

	Eventually(func() error {
		return k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, tenant)
	}, 5*time.Second, 10*time.Millisecond).Should(Succeed())

	tenant.Status.Phase = v1alpha1.TenantPhaseReady
	tenant.Status.Namespace = name
	Expect(k8sClient.Status().Update(ctx, tenant)).To(Succeed())

	Eventually(func(g Gomega) {
		t := &v1alpha1.Tenant{}
		g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, t)).To(Succeed())
		g.Expect(t.Status.Phase).To(Equal(v1alpha1.TenantPhaseReady))
	}, 5*time.Second, 10*time.Millisecond).Should(Succeed())
}

func createHubSecret(ctx context.Context, tenantName, namespace string) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("vast-tenant-config-%s", tenantName),
			Namespace: namespace,
			Labels: map[string]string{
				osacTenantKey: tenantName,
			},
		},
		Data: map[string][]byte{
			"vast_tenant_id": []byte("123"),
		},
	}
	Expect(k8sClient.Create(ctx, secret)).To(Succeed())
}

func createLabeledStorageClass(ctx context.Context, name, tenant, tier string) {
	sc := &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				osacTenantKey:        tenant,
				osacStorageTierLabel: tier,
			},
		},
		Provisioner: "kubernetes.io/no-provisioner",
	}
	Expect(k8sClient.Create(ctx, sc)).To(Succeed())
	DeferCleanup(func() {
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, sc))).To(Succeed())
	})
}

type mockStorageTiersLister struct {
	listFunc      func(ctx context.Context, in *privatev1.StorageTiersListRequest, opts ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error)
	listCallCount int
}

func (m *mockStorageTiersLister) List(ctx context.Context, in *privatev1.StorageTiersListRequest, opts ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
	m.listCallCount++
	return m.listFunc(ctx, in, opts...)
}

// newTestStorageTier builds a StorageTier fixture with one BackendAssociation per
// backendID given. Passing no backendIDs produces a tier with zero associations.
func newTestStorageTier(name string, backendIDs ...string) *privatev1.StorageTier {
	backends := make([]*privatev1.BackendAssociation, len(backendIDs))
	for i, id := range backendIDs {
		backends[i] = privatev1.BackendAssociation_builder{
			BackendId:            id,
			Protocol:             privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS,
			MaxReadBandwidthMbs:  100,
			MaxWriteBandwidthMbs: 100,
			QuotaGib:             500,
		}.Build()
	}
	return privatev1.StorageTier_builder{
		Metadata: privatev1.Metadata_builder{Name: name}.Build(),
		Spec:     privatev1.StorageTierSpec_builder{Backends: backends}.Build(),
	}.Build()
}

// testBackendUsername and testBackendPassword are fixture values, not real
// credentials — used to verify connection details round-trip through
// resolveTierDefinitions and the AAP extra_vars conversion unmodified.
const (
	testBackendUsername = "test-backend-user"
	testBackendPassword = "test-backend-password"
)

// newTestStorageBackendGetResponse builds a StorageBackendsGetResponse fixture for
// the given provider, with a fixed endpoint/credentials pair.
func newTestStorageBackendGetResponse(provider string) *privatev1.StorageBackendsGetResponse {
	return privatev1.StorageBackendsGetResponse_builder{
		Object: privatev1.StorageBackend_builder{
			Spec: privatev1.StorageBackendSpec_builder{
				Provider: provider,
				Endpoint: "https://" + provider + ".example.com",
				Credentials: privatev1.StorageBackendCredentials_builder{
					Username: testBackendUsername,
					Password: testBackendPassword,
				}.Build(),
			}.Build(),
		}.Build(),
	}.Build()
}

func newClusterOrder(name, namespace string, annotations map[string]string) *v1alpha1.ClusterOrder {
	return &v1alpha1.ClusterOrder{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			Annotations: annotations,
		},
		Spec: v1alpha1.ClusterOrderSpec{TemplateID: "test_template"},
	}
}

var _ = Describe("Storage Controller", func() {
	const (
		testNamespace    = "default"
		secretsNamespace = "osac-system"
		pollInterval     = 1 * time.Second
	)

	ctx := context.Background()

	BeforeEach(func() {
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: secretsNamespace},
		}
		if err := k8sClient.Create(ctx, ns); err != nil {
			Expect(client.IgnoreAlreadyExists(err)).To(Succeed())
		}
	})

	Context("Stage 1: Backend provisioning", func() {
		It("should skip reconciliation when Tenant is not Ready", func() {
			name := "storage-test-not-ready"
			tenant := &v1alpha1.Tenant{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: testNamespace,
				},
			}
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				&mockProvisioningProvider{}, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			Eventually(func() error {
				return r.Client.Get(ctx, nn, &v1alpha1.Tenant{})
			}, 5*time.Second, 10*time.Millisecond).Should(Succeed())

			result, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			Expect(tenant.Status.ProvisioningJobs).To(BeEmpty())
			cond := tenant.GetStatusCondition(v1alpha1.TenantConditionStorageBackendReady)
			Expect(cond).To(BeNil())
		})

		It("should trigger backend provisioning when hub Secret is missing", func() {
			name := "storage-test-no-secret"
			createReadyTenantForStorage(ctx, name, testNamespace)

			provider := &mockProvisioningProvider{}
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				provider, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.BackendsClient = registeredBackendsClient(1)

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			result, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(pollInterval))

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			Expect(tenant.Status.StorageBackendJobs).To(HaveLen(1))
			Expect(tenant.Status.StorageBackendJobs[0].Type).To(Equal(v1alpha1.JobTypeProvision))
			Expect(tenant.Status.StorageBackendJobs[0].JobID).To(Equal("mock-job-id"))

			cond := tenant.GetStatusCondition(v1alpha1.TenantConditionStorageBackendReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		})

		It("should set StorageBackendReady=True when hub Secret exists", func() {
			name := "storage-test-secret-exists"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createHubSecret(ctx, name, secretsNamespace)

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())

			cond := tenant.GetStatusCondition(v1alpha1.TenantConditionStorageBackendReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(v1alpha1.TenantReasonFound))
		})

		It("should set StorageBackendReady=False with NoProvider and ClusterStorageReady=False when no tenant SCs exist", func() {
			name := "storage-test-no-provider"
			createReadyTenantForStorage(ctx, name, testNamespace)

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())

			backendCond := tenant.GetStatusCondition(v1alpha1.TenantConditionStorageBackendReady)
			Expect(backendCond).NotTo(BeNil())
			Expect(backendCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(backendCond.Reason).To(Equal(v1alpha1.TenantReasonNoProvider))

			clusterCond := tenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady)
			Expect(clusterCond).NotTo(BeNil())
			Expect(clusterCond.Status).To(Equal(metav1.ConditionFalse))

			Expect(tenant.Status.StorageClasses).To(BeNil())
		})

		It("should set ClusterStorageReady=False when no provider and no labeled SCs", func() {
			name := "storage-test-no-provider-no-sc"
			createReadyTenantForStorage(ctx, name, testNamespace)

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())

			backendCond := tenant.GetStatusCondition(v1alpha1.TenantConditionStorageBackendReady)
			Expect(backendCond).NotTo(BeNil())
			Expect(backendCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(backendCond.Reason).To(Equal(v1alpha1.TenantReasonNoProvider))

			clusterCond := tenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady)
			Expect(clusterCond).NotTo(BeNil())
			Expect(clusterCond.Status).To(Equal(metav1.ConditionFalse))

			Expect(tenant.Status.StorageClasses).To(BeNil())
		})

		It("should preserve tenant controller fields when patching storage status", func() {
			name := "storage-test-patch-preserves-phase"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createLabeledStorageClass(ctx, name+"-tenant-sc", name, "default")

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())

			Expect(tenant.Status.Phase).To(Equal(v1alpha1.TenantPhaseReady))
			Expect(tenant.Status.Namespace).To(Equal(name))

			Expect(tenant.Status.StorageClasses).To(HaveLen(1))
			Expect(tenant.Status.StorageClasses[0].Name).To(Equal(name + "-tenant-sc"))
		})

		It("should propagate trigger error without creating fake job", func() {
			name := "storage-test-prov-fail"
			createReadyTenantForStorage(ctx, name, testNamespace)

			provider := &mockProvisioningProvider{
				triggerProvisionFunc: func(_ context.Context, _ client.Object) (*provisioning.ProvisionResult, error) {
					return nil, fmt.Errorf("AAP unreachable")
				},
			}
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				provider, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.BackendsClient = registeredBackendsClient(1)

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("AAP unreachable"))

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			Expect(tenant.Status.StorageBackendJobs).To(BeEmpty())
		})
	})

	Context("Stage 2: Cluster storage provisioning", func() {
		It("should trigger cluster storage provisioning when Stage 1 complete but no SCs", func() {
			name := "storage-test-no-sc"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createHubSecret(ctx, name, secretsNamespace)

			clusterProvider := &mockProvisioningProvider{name: "cluster-storage-mock"}
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, clusterProvider, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			result, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(pollInterval))

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())

			backendCond := tenant.GetStatusCondition(v1alpha1.TenantConditionStorageBackendReady)
			Expect(backendCond).NotTo(BeNil())
			Expect(backendCond.Status).To(Equal(metav1.ConditionTrue))

			Expect(tenant.Status.ClusterStorageJobs).To(HaveLen(1))
			Expect(tenant.Status.ClusterStorageJobs[0].Type).To(Equal(v1alpha1.JobTypeProvision))
		})

		It("should set ClusterStorageReady=True when SCs are discovered", func() {
			name := "storage-test-sc-found"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createHubSecret(ctx, name, secretsNamespace)
			createLabeledStorageClass(ctx, name+"-default-sc", name, "default")

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())

			clusterCond := tenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady)
			Expect(clusterCond).NotTo(BeNil())
			Expect(clusterCond.Status).To(Equal(metav1.ConditionTrue))

			Expect(tenant.Status.StorageClasses).To(HaveLen(1))
			Expect(tenant.Status.StorageClasses[0].Name).To(Equal(name + "-default-sc"))
			Expect(tenant.Status.StorageClasses[0].Tier).To(Equal("default"))
		})

		It("should set ClusterStorageReady=False and trigger provisioning when no tenant SCs exist and provider is configured", func() {
			name := "storage-test-default-only-with-provider"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createHubSecret(ctx, name, secretsNamespace)

			clusterProvider := &mockProvisioningProvider{name: "cluster-storage-mock"}
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, clusterProvider, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())

			clusterCond := tenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady)
			Expect(clusterCond).NotTo(BeNil())
			Expect(clusterCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(clusterCond.Reason).To(Equal(v1alpha1.TenantReasonNotFound))

			Expect(tenant.Status.StorageClasses).To(BeNil())
			Expect(tenant.Status.ClusterStorageJobs).To(HaveLen(1))
		})

		It("should set ClusterStorageReady=False and trigger provisioning when provider configured but no tenant SCs", func() {
			name := "storage-test-no-tenant-sc-with-provider"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createHubSecret(ctx, name, secretsNamespace)

			clusterProvider := &mockProvisioningProvider{name: "cluster-storage-mock"}
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, clusterProvider, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())

			clusterCond := tenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady)
			Expect(clusterCond).NotTo(BeNil())
			Expect(clusterCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(clusterCond.Reason).To(Equal(v1alpha1.TenantReasonNotFound))

			Expect(tenant.Status.StorageClasses).To(BeNil())
			Expect(tenant.Status.ClusterStorageJobs).To(HaveLen(1))
		})

		It("should set ClusterStorageReady=False and trigger provisioning when no SCs at all and provider is configured", func() {
			name := "storage-test-no-sc-with-provider"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createHubSecret(ctx, name, secretsNamespace)

			clusterProvider := &mockProvisioningProvider{name: "cluster-storage-mock"}
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, clusterProvider, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())

			clusterCond := tenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady)
			Expect(clusterCond).NotTo(BeNil())
			Expect(clusterCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(clusterCond.Reason).To(Equal(v1alpha1.TenantReasonNotFound))

			Expect(tenant.Status.StorageClasses).To(BeNil())
			Expect(tenant.Status.ClusterStorageJobs).To(HaveLen(1))
		})

		It("should not trigger provisioning when tenant-specific SC exists and provider is configured", func() {
			name := "storage-test-tenant-sc-with-provider"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createHubSecret(ctx, name, secretsNamespace)
			createLabeledStorageClass(ctx, name+"-vast-sc", name, "default")

			clusterProvider := &mockProvisioningProvider{name: "cluster-storage-mock"}
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, clusterProvider, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())

			clusterCond := tenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady)
			Expect(clusterCond).NotTo(BeNil())
			Expect(clusterCond.Status).To(Equal(metav1.ConditionTrue))

			Expect(tenant.Status.StorageClasses).To(HaveLen(1))
			Expect(tenant.Status.StorageClasses[0].Name).To(Equal(name + "-vast-sc"))

			Expect(tenant.Status.ClusterStorageJobs).To(BeEmpty())
		})

		It("should surface duplicate warnings in condition when some tiers resolve and others have duplicates", func() {
			name := "storage-test-mixed-tiers-with-provider"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createHubSecret(ctx, name, secretsNamespace)
			createLabeledStorageClass(ctx, name+"-default-sc", name, "default")
			createLabeledStorageClass(ctx, name+"-premium-sc-1", name, "premium")
			createLabeledStorageClass(ctx, name+"-premium-sc-2", name, "premium")

			clusterProvider := &mockProvisioningProvider{name: "cluster-storage-mock"}
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, clusterProvider, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())

			clusterCond := tenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady)
			Expect(clusterCond).NotTo(BeNil())
			Expect(clusterCond.Status).To(Equal(metav1.ConditionTrue))
			Expect(clusterCond.Message).To(ContainSubstring("default"))
			Expect(clusterCond.Message).To(ContainSubstring("premium"))
			Expect(clusterCond.Message).To(ContainSubstring("multiple tenant StorageClasses"))

			Expect(tenant.Status.StorageClasses).To(HaveLen(1))
			Expect(tenant.Status.StorageClasses[0].Tier).To(Equal("default"))
		})
	})

	Context("Tier resolution", func() {
		It("should resolve no StorageClasses when only tenant-labeled SCs are absent (no Default fallback)", func() {
			name := "storage-test-no-tenant-sc"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createHubSecret(ctx, name, secretsNamespace)
			createLabeledStorageClass(ctx, name+"-default-sc", "Default", "default")

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())

			Expect(tenant.Status.StorageClasses).To(BeNil())
		})

		It("should resolve only tenant-specific SCs (unlabeled Default SCs ignored)", func() {
			name := "storage-test-tenant-priority"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createHubSecret(ctx, name, secretsNamespace)
			createLabeledStorageClass(ctx, name+"-default-sc", "Default", "default")
			createLabeledStorageClass(ctx, name+"-tenant-sc", name, "default")

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())

			Expect(tenant.Status.StorageClasses).To(HaveLen(1))
			Expect(tenant.Status.StorageClasses[0].Name).To(Equal(name + "-tenant-sc"))
		})
	})

	Context("Tier definition validation", func() {
		It("should resolve provider and tier fields from the Tier and Backend APIs", func() {
			tiersClient := &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return privatev1.StorageTiersListResponse_builder{
						Items: []*privatev1.StorageTier{newTestStorageTier("fast", "backend-1")},
					}.Build(), nil
				},
			}
			backendsClient := &mockStorageBackendsClient{
				getFunc: func(_ context.Context, in *privatev1.StorageBackendsGetRequest, _ ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error) {
					Expect(in.GetId()).To(Equal("backend-1"))
					return newTestStorageBackendGetResponse("vast"), nil
				},
			}

			defs, conns, err := resolveTierDefinitions(ctx, tiersClient, backendsClient)
			Expect(err).NotTo(HaveOccurred())
			Expect(defs).To(HaveLen(1))
			Expect(defs[0].Name).To(Equal("fast"))
			Expect(defs[0].Protocol).To(Equal("nfs"))
			Expect(defs[0].Provider).To(Equal("vast"))
			Expect(defs[0].BackendID).To(Equal("backend-1"))
			Expect(defs[0].QuotaGiB).To(Equal(int64(500)))
			Expect(defs[0].QosLimits.MaxReadBandwidthMBs).To(Equal(int32(100)))
			Expect(defs[0].QosLimits.MaxWriteBandwidthMBs).To(Equal(int32(100)))
			Expect(conns).To(HaveKeyWithValue("backend-1", provisioning.BackendConnection{
				Endpoint: "https://vast.example.com",
				Username: testBackendUsername,
				Password: testBackendPassword,
			}))
		})

		It("should skip a tier with no backend association without failing", func() {
			tiersClient := &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return privatev1.StorageTiersListResponse_builder{
						Items: []*privatev1.StorageTier{
							newTestStorageTier("orphan"),
							newTestStorageTier("fast", "backend-1"),
						},
					}.Build(), nil
				},
			}
			backendsClient := &mockStorageBackendsClient{
				getFunc: func(context.Context, *privatev1.StorageBackendsGetRequest, ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error) {
					return newTestStorageBackendGetResponse("vast"), nil
				},
			}

			defs, _, err := resolveTierDefinitions(ctx, tiersClient, backendsClient)
			Expect(err).NotTo(HaveOccurred())
			Expect(defs).To(HaveLen(1))
			Expect(defs[0].Name).To(Equal("fast"))
			Expect(backendsClient.getCallCount).To(Equal(1))
		})

		It("should call the backend getter once per unique backend_id, not once per tier", func() {
			tiersClient := &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return privatev1.StorageTiersListResponse_builder{
						Items: []*privatev1.StorageTier{
							newTestStorageTier("fast", "backend-1"),
							newTestStorageTier("standard", "backend-1"),
						},
					}.Build(), nil
				},
			}
			backendsClient := &mockStorageBackendsClient{
				getFunc: func(context.Context, *privatev1.StorageBackendsGetRequest, ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error) {
					return newTestStorageBackendGetResponse("vast"), nil
				},
			}

			defs, conns, err := resolveTierDefinitions(ctx, tiersClient, backendsClient)
			Expect(err).NotTo(HaveOccurred())
			Expect(defs).To(HaveLen(2))
			Expect(backendsClient.getCallCount).To(Equal(1))
			Expect(conns).To(HaveLen(1))
			Expect(conns).To(HaveKey("backend-1"))
		})

		It("should skip only tiers referencing a NotFound backend, not the whole resolution", func() {
			tiersClient := &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return privatev1.StorageTiersListResponse_builder{
						Items: []*privatev1.StorageTier{
							newTestStorageTier("stale", "backend-gone"),
							newTestStorageTier("also-stale", "backend-gone"),
							newTestStorageTier("fast", "backend-1"),
						},
					}.Build(), nil
				},
			}
			backendsClient := &mockStorageBackendsClient{
				getFunc: func(_ context.Context, in *privatev1.StorageBackendsGetRequest, _ ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error) {
					if in.GetId() == "backend-gone" {
						return nil, status.Error(codes.NotFound, "storage backend not found")
					}
					return newTestStorageBackendGetResponse("vast"), nil
				},
			}

			defs, conns, err := resolveTierDefinitions(ctx, tiersClient, backendsClient)
			Expect(err).NotTo(HaveOccurred())
			Expect(defs).To(HaveLen(1))
			Expect(defs[0].Name).To(Equal("fast"))
			Expect(conns).NotTo(HaveKey("backend-gone"))
			Expect(conns).To(HaveKey("backend-1"))
			Expect(backendsClient.getCallCount).To(Equal(2), "backend-gone should be fetched once (cached NotFound), backend-1 once")
		})

		It("should propagate a List error without swallowing it", func() {
			tiersClient := &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return nil, errors.New("list rpc failed")
				},
			}
			backendsClient := &mockStorageBackendsClient{
				getFunc: func(context.Context, *privatev1.StorageBackendsGetRequest, ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error) {
					return newTestStorageBackendGetResponse("vast"), nil
				},
			}

			_, _, err := resolveTierDefinitions(ctx, tiersClient, backendsClient)
			Expect(err).To(HaveOccurred())
		})

		It("should propagate a non-NotFound Get error without swallowing it", func() {
			tiersClient := &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return privatev1.StorageTiersListResponse_builder{
						Items: []*privatev1.StorageTier{newTestStorageTier("fast", "backend-1")},
					}.Build(), nil
				},
			}
			backendsClient := &mockStorageBackendsClient{
				getFunc: func(context.Context, *privatev1.StorageBackendsGetRequest, ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error) {
					return nil, status.Error(codes.Unavailable, "backend service down")
				},
			}

			_, _, err := resolveTierDefinitions(ctx, tiersClient, backendsClient)
			Expect(err).To(HaveOccurred())
		})

		It("should report a missing tier via Warning Event and the ClusterStorageReady condition", func() {
			name := "storage-test-missing-tier"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createLabeledStorageClass(ctx, name+"-default-sc", name, "default")

			fakeRecorder := events.NewFakeRecorder(100)
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.Recorder = fakeRecorder
			r.TiersClient = &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return privatev1.StorageTiersListResponse_builder{
						Items: []*privatev1.StorageTier{
							newTestStorageTier("default", "backend-1"),
							newTestStorageTier("archive", "backend-1"),
						},
					}.Build(), nil
				},
			}
			r.BackendsClient = &mockStorageBackendsClient{
				getFunc: func(context.Context, *privatev1.StorageBackendsGetRequest, ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error) {
					return newTestStorageBackendGetResponse("vast"), nil
				},
			}

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			Eventually(fakeRecorder.Events).Should(Receive(And(
				ContainSubstring("Warning"),
				ContainSubstring(eventReasonMissingStorageTier),
				ContainSubstring(`tier "archive" has no StorageClass`),
			)))

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			cond := tenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Message).To(ContainSubstring(`tier "archive" has no StorageClass`))
		})

		It("should not report a missing tier when every defined tier has a matching StorageClass", func() {
			name := "storage-test-no-missing-tier"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createLabeledStorageClass(ctx, name+"-default-sc", name, "default")

			fakeRecorder := events.NewFakeRecorder(100)
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.Recorder = fakeRecorder
			r.TiersClient = &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return privatev1.StorageTiersListResponse_builder{
						Items: []*privatev1.StorageTier{newTestStorageTier("default", "backend-1")},
					}.Build(), nil
				},
			}
			r.BackendsClient = &mockStorageBackendsClient{
				getFunc: func(context.Context, *privatev1.StorageBackendsGetRequest, ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error) {
					return newTestStorageBackendGetResponse("vast"), nil
				},
			}

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			Consistently(fakeRecorder.Events, 200*time.Millisecond).ShouldNot(Receive(
				ContainSubstring(eventReasonMissingStorageTier),
			))

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			cond := tenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Message).NotTo(ContainSubstring("has no StorageClass"))
		})

		It("should not report a missing tier when the Tier API name's case differs from the StorageClass label", func() {
			name := "storage-test-tier-case-mismatch"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createLabeledStorageClass(ctx, name+"-default-sc", name, "default")

			fakeRecorder := events.NewFakeRecorder(100)
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.Recorder = fakeRecorder
			r.TiersClient = &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return privatev1.StorageTiersListResponse_builder{
						Items: []*privatev1.StorageTier{newTestStorageTier("Default", "backend-1")},
					}.Build(), nil
				},
			}
			r.BackendsClient = &mockStorageBackendsClient{
				getFunc: func(context.Context, *privatev1.StorageBackendsGetRequest, ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error) {
					return newTestStorageBackendGetResponse("vast"), nil
				},
			}

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			Consistently(fakeRecorder.Events, 200*time.Millisecond).ShouldNot(Receive(
				ContainSubstring(eventReasonMissingStorageTier),
			))

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			cond := tenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Message).NotTo(ContainSubstring("has no StorageClass"))
		})

		It("should skip tier validation entirely when TiersClient is nil", func() {
			name := "storage-test-no-tiers-client"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createLabeledStorageClass(ctx, name+"-default-sc", name, "default")

			fakeRecorder := events.NewFakeRecorder(100)
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.Recorder = fakeRecorder

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			Consistently(fakeRecorder.Events, 200*time.Millisecond).ShouldNot(Receive(
				ContainSubstring(eventReasonMissingStorageTier),
			))
		})

		It("should skip tier validation without panicking when BackendsClient is nil but TiersClient is set", func() {
			name := "storage-test-no-backends-getter"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createLabeledStorageClass(ctx, name+"-default-sc", name, "default")

			fakeRecorder := events.NewFakeRecorder(100)
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.Recorder = fakeRecorder
			r.TiersClient = &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return privatev1.StorageTiersListResponse_builder{
						Items: []*privatev1.StorageTier{newTestStorageTier("default", "backend-1")},
					}.Build(), nil
				},
			}

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			Expect(func() {
				_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
				Expect(err).NotTo(HaveOccurred())
			}).NotTo(Panic())

			Consistently(fakeRecorder.Events, 200*time.Millisecond).ShouldNot(Receive(
				ContainSubstring(eventReasonMissingStorageTier),
			))
		})

		It("should complete StorageClass resolution when tier resolution fails", func() {
			name := "storage-test-tier-resolution-failure"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createLabeledStorageClass(ctx, name+"-default-sc", name, "default")

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.Recorder = events.NewFakeRecorder(100)
			r.TiersClient = &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return nil, status.Error(codes.Unavailable, "fulfillment service unreachable")
				},
			}

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred(), "a Tier API failure must not abort StorageClass resolution, which has no dependency on it")

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			cond := tenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		})

		It("should call resolveTierDefinitions exactly once per handleUpdate invocation", func() {
			name := "storage-test-resolve-once"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createLabeledStorageClass(ctx, name+"-default-sc", name, "default")

			tiersClient := &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return privatev1.StorageTiersListResponse_builder{
						Items: []*privatev1.StorageTier{newTestStorageTier("default", "backend-1")},
					}.Build(), nil
				},
			}
			backendsClient := &mockStorageBackendsClient{
				getFunc: func(context.Context, *privatev1.StorageBackendsGetRequest, ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error) {
					return newTestStorageBackendGetResponse("vast"), nil
				},
			}

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.TiersClient = tiersClient
			r.BackendsClient = backendsClient

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			Expect(tiersClient.listCallCount).To(Equal(1))
			Expect(backendsClient.getCallCount).To(Equal(1))
		})

		It("should not flag an ambiguous (duplicate StorageClass) tier as missing", func() {
			name := "storage-test-ambiguous-not-missing"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createLabeledStorageClass(ctx, name+"-fast-sc-1", name, "fast")
			createLabeledStorageClass(ctx, name+"-fast-sc-2", name, "fast")

			fakeRecorder := events.NewFakeRecorder(100)
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.Recorder = fakeRecorder
			r.TiersClient = &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return privatev1.StorageTiersListResponse_builder{
						Items: []*privatev1.StorageTier{
							newTestStorageTier("fast", "backend-1"),
							newTestStorageTier("archive", "backend-1"),
						},
					}.Build(), nil
				},
			}
			r.BackendsClient = &mockStorageBackendsClient{
				getFunc: func(context.Context, *privatev1.StorageBackendsGetRequest, ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error) {
					return newTestStorageBackendGetResponse("vast"), nil
				},
			}

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			Eventually(fakeRecorder.Events).Should(Receive(And(
				ContainSubstring("Warning"),
				ContainSubstring(eventReasonMissingStorageTier),
				ContainSubstring(`tier "archive" has no StorageClass`),
			)))
			Consistently(fakeRecorder.Events, 200*time.Millisecond).ShouldNot(Receive(
				ContainSubstring(`tier "fast" has no StorageClass`),
			))

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			cond := tenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Message).To(ContainSubstring(`tier "archive" has no StorageClass`))
			Expect(cond.Message).NotTo(ContainSubstring(`tier "fast" has no StorageClass`))
		})

		It("should map each StorageProtocol enum value to its AAP-schema string", func() {
			Expect(storageProtocolToString(privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS)).To(Equal("nfs"))
			Expect(storageProtocolToString(privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK)).To(Equal("block"))
			Expect(storageProtocolToString(privatev1.StorageProtocol_STORAGE_PROTOCOL_UNSPECIFIED)).To(BeEmpty())
		})

		It("should join a missing-tier message onto an existing condition message without a leading separator when condMsg is empty", func() {
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.Recorder = events.NewFakeRecorder(100)
			instance := &v1alpha1.Tenant{ObjectMeta: metav1.ObjectMeta{Name: "storage-test-empty-condmsg", Namespace: testNamespace}}

			result := r.appendMissingTierWarnings(
				instance,
				[]provisioning.TierDefinition{{Name: "archive"}},
				nil, nil, "",
			)

			Expect(result).To(Equal(`tier "archive" has no StorageClass`))
		})
	})

	Context("Tier definitions in AAP extra_vars context", func() {
		tierDefsTiersClient := func() *mockStorageTiersLister {
			return &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return privatev1.StorageTiersListResponse_builder{
						Items: []*privatev1.StorageTier{newTestStorageTier("fast", "backend-1")},
					}.Build(), nil
				},
			}
		}
		tierDefsBackendsClient := func() *mockStorageBackendsClient {
			return &mockStorageBackendsClient{
				getFunc: func(context.Context, *privatev1.StorageBackendsGetRequest, ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error) {
					return newTestStorageBackendGetResponse("vast"), nil
				},
			}
		}

		It("should inject storage_tier_definitions into context before handleBackendProvisioning (Stage 1)", func() {
			name := "storage-test-tier-ctx-backend-provisioning"
			createReadyTenantForStorage(ctx, name, testNamespace)

			var gotTiers []provisioning.TierDefinition
			var sawTiers bool
			provider := &mockProvisioningProvider{
				triggerProvisionFunc: func(ctx context.Context, resource client.Object) (*provisioning.ProvisionResult, error) {
					gotTiers = provisioning.StorageTierDefinitionsFromContext(ctx)
					sawTiers = true
					return &provisioning.ProvisionResult{JobID: "mock-job-id", InitialState: v1alpha1.JobStatePending}, nil
				},
			}
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				provider, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.TiersClient = tierDefsTiersClient()
			backendsClient := tierDefsBackendsClient()
			backendsClient.listFunc = registeredBackendsClient(1).listFunc
			r.BackendsClient = backendsClient

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			Expect(sawTiers).To(BeTrue())
			Expect(gotTiers).To(HaveLen(1))
			Expect(gotTiers[0].Name).To(Equal("fast"))
		})

		It("should inject storage_tier_definitions into context before handleClusterStorageProvisioning (Stage 2)", func() {
			name := "storage-test-tier-ctx-cluster-provisioning"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createHubSecret(ctx, name, secretsNamespace)

			var gotTiers []provisioning.TierDefinition
			var sawTiers bool
			clusterProvider := &mockProvisioningProvider{
				name: "cluster-storage-mock",
				triggerProvisionFunc: func(ctx context.Context, resource client.Object) (*provisioning.ProvisionResult, error) {
					gotTiers = provisioning.StorageTierDefinitionsFromContext(ctx)
					sawTiers = true
					return &provisioning.ProvisionResult{JobID: "mock-job-id", InitialState: v1alpha1.JobStatePending}, nil
				},
			}
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, clusterProvider, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.TiersClient = tierDefsTiersClient()
			r.BackendsClient = tierDefsBackendsClient()

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			Expect(sawTiers).To(BeTrue())
			Expect(gotTiers).To(HaveLen(1))
			Expect(gotTiers[0].Name).To(Equal("fast"))
		})

		It("should inject storage_backend_connections into context alongside storage_tier_definitions", func() {
			name := "storage-test-backend-conn-ctx"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createHubSecret(ctx, name, secretsNamespace)

			var gotConns map[string]provisioning.BackendConnection
			var sawConns bool
			clusterProvider := &mockProvisioningProvider{
				name: "cluster-storage-mock",
				triggerProvisionFunc: func(ctx context.Context, resource client.Object) (*provisioning.ProvisionResult, error) {
					gotConns = provisioning.StorageBackendConnectionsFromContext(ctx)
					sawConns = true
					return &provisioning.ProvisionResult{JobID: "mock-job-id", InitialState: v1alpha1.JobStatePending}, nil
				},
			}
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, clusterProvider, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.TiersClient = tierDefsTiersClient()
			r.BackendsClient = tierDefsBackendsClient()

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			Expect(sawConns).To(BeTrue())
			Expect(gotConns).To(HaveKeyWithValue("backend-1", provisioning.BackendConnection{
				Endpoint: "https://vast.example.com",
				Username: testBackendUsername,
				Password: testBackendPassword,
			}))
		})

		It("should inject storage_tier_definitions into context before handleBackendDeprovisioning (delete path)", func() {
			name := "storage-test-tier-ctx-backend-deprovisioning"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createHubSecret(ctx, name, secretsNamespace)

			var gotTiers []provisioning.TierDefinition
			var sawTiers bool
			provider := &mockProvisioningProvider{
				name: "backend-mock",
				triggerDeprovisionFunc: func(ctx context.Context, resource client.Object, provisionJobs []v1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
					gotTiers = provisioning.StorageTierDefinitionsFromContext(ctx)
					sawTiers = true
					return &provisioning.DeprovisionResult{Action: provisioning.DeprovisionTriggered, JobID: "mock-deprovision-job-id", BlockDeletionOnFailure: true}, nil
				},
			}
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				provider, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.TiersClient = tierDefsTiersClient()
			r.BackendsClient = tierDefsBackendsClient()

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			Expect(k8sClient.Delete(ctx, tenant)).To(Succeed())

			Eventually(func(g Gomega) {
				_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(sawTiers).To(BeTrue())
			}).Should(Succeed())

			Expect(gotTiers).To(HaveLen(1))
			Expect(gotTiers[0].Name).To(Equal("fast"))
		})

		It("should inject storage_tier_definitions into context before handleClusterStorageDeprovisioning (delete path)", func() {
			name := "storage-test-tier-ctx-cluster-deprovisioning"
			createReadyTenantForStorage(ctx, name, testNamespace)

			var gotTiers []provisioning.TierDefinition
			var sawTiers bool
			clusterProvider := &mockProvisioningProvider{
				name: "cluster-storage-mock",
				triggerDeprovisionFunc: func(ctx context.Context, resource client.Object, provisionJobs []v1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
					gotTiers = provisioning.StorageTierDefinitionsFromContext(ctx)
					sawTiers = true
					return &provisioning.DeprovisionResult{Action: provisioning.DeprovisionTriggered, JobID: "mock-deprovision-job-id", BlockDeletionOnFailure: true}, nil
				},
			}
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, clusterProvider, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.TiersClient = tierDefsTiersClient()
			r.BackendsClient = tierDefsBackendsClient()

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			Expect(k8sClient.Delete(ctx, tenant)).To(Succeed())

			Eventually(func(g Gomega) {
				_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(sawTiers).To(BeTrue())
			}).Should(Succeed())

			Expect(gotTiers).To(HaveLen(1))
			Expect(gotTiers[0].Name).To(Equal("fast"))
		})
	})

	Context("Finalizer and deletion", func() {
		It("should add storage finalizer on first reconcile", func() {
			name := "storage-test-finalizer"
			createReadyTenantForStorage(ctx, name, testNamespace)

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			Expect(tenant.Finalizers).To(ContainElement(storageFinalizer))
		})

		It("should run deletion even when class provider is nil", func() {
			name := "storage-test-delete-no-class"
			createReadyTenantForStorage(ctx, name, testNamespace)

			backendProvider := &mockProvisioningProvider{name: "backend-mock"}
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				backendProvider, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			Expect(tenant.Finalizers).To(ContainElement(storageFinalizer))

			Expect(k8sClient.Delete(ctx, tenant)).To(Succeed())

			Eventually(func(g Gomega) {
				_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
				g.Expect(err).NotTo(HaveOccurred())

				t := &v1alpha1.Tenant{}
				err = k8sClient.Get(ctx, nn, t)
				g.Expect(client.IgnoreNotFound(err)).To(Succeed())
				if err == nil {
					g.Expect(t.Finalizers).NotTo(ContainElement(storageFinalizer))
				}
			}).Should(Succeed())
		})

		It("should remove the finalizer during deletion even when tier resolution fails", func() {
			name := "storage-test-delete-tier-resolution-failure"
			createReadyTenantForStorage(ctx, name, testNamespace)

			backendProvider := &mockProvisioningProvider{name: "backend-mock"}
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				backendProvider, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.TiersClient = &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return nil, status.Error(codes.Unavailable, "fulfillment service unreachable")
				},
			}

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			Expect(tenant.Finalizers).To(ContainElement(storageFinalizer))

			Expect(k8sClient.Delete(ctx, tenant)).To(Succeed())

			Eventually(func(g Gomega) {
				_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
				g.Expect(err).NotTo(HaveOccurred(), "a Tier API failure must not block finalizer removal during deletion")

				t := &v1alpha1.Tenant{}
				err = k8sClient.Get(ctx, nn, t)
				g.Expect(client.IgnoreNotFound(err)).To(Succeed())
				if err == nil {
					g.Expect(t.Finalizers).NotTo(ContainElement(storageFinalizer))
				}
			}).Should(Succeed())
		})
	})

	Context("Management state", func() {
		It("should skip reconciliation when Unmanaged", func() {
			name := "storage-test-unmanaged"
			tenant := &v1alpha1.Tenant{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: testNamespace,
					Annotations: map[string]string{
						osacManagementStateAnnotation: ManagementStateUnmanaged,
					},
				},
			}
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

			Eventually(func() error {
				t := &v1alpha1.Tenant{}
				return k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: testNamespace}, t)
			}, 5*time.Second, 10*time.Millisecond).Should(Succeed())

			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: testNamespace}, tenant)).To(Succeed())
			tenant.Status.Phase = v1alpha1.TenantPhaseReady
			Expect(k8sClient.Status().Update(ctx, tenant)).To(Succeed())

			provider := &mockProvisioningProvider{}
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				provider, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			result, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			Expect(tenant.Status.ProvisioningJobs).To(BeEmpty())
			Expect(tenant.Status.StorageBackendJobs).To(BeEmpty())
			Expect(tenant.Status.ClusterStorageJobs).To(BeEmpty())
			Expect(tenant.Finalizers).NotTo(ContainElement(storageFinalizer))
		})
	})

	Context("Job array isolation", func() {
		It("should place backend jobs in StorageBackendJobs array", func() {
			name := "storage-test-backend-array"
			createReadyTenantForStorage(ctx, name, testNamespace)

			provider := &mockProvisioningProvider{}
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				provider, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.BackendsClient = registeredBackendsClient(1)

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			Expect(tenant.Status.StorageBackendJobs).To(HaveLen(1))
			Expect(tenant.Status.StorageBackendJobs[0].Type).To(Equal(v1alpha1.JobTypeProvision))
			Expect(tenant.Status.ProvisioningJobs).To(BeEmpty())
			Expect(tenant.Status.ClusterStorageJobs).To(BeEmpty())
		})

		It("should place cluster storage jobs in ClusterStorageJobs array", func() {
			name := "storage-test-cs-array"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createHubSecret(ctx, name, secretsNamespace)

			clusterProvider := &mockProvisioningProvider{name: "cluster-storage-mock"}
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, clusterProvider, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			Expect(tenant.Status.ClusterStorageJobs).To(HaveLen(1))
			Expect(tenant.Status.ClusterStorageJobs[0].Type).To(Equal(v1alpha1.JobTypeProvision))
			Expect(tenant.Status.ProvisioningJobs).To(BeEmpty())
			Expect(tenant.Status.StorageBackendJobs).To(BeEmpty())
		})
	})

	Context("Cluster storage failure", func() {
		It("should record failed cluster storage provisioning job", func() {
			name := "storage-test-cs-fail"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createHubSecret(ctx, name, secretsNamespace)

			clusterProvider := &mockProvisioningProvider{
				name: "cluster-storage-mock",
				triggerProvisionFunc: func(_ context.Context, _ client.Object) (*provisioning.ProvisionResult, error) {
					return nil, fmt.Errorf("cluster storage template not found")
				},
			}
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, clusterProvider, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("cluster storage template not found"))

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			Expect(tenant.Status.ClusterStorageJobs).To(BeEmpty())
		})
	})

	Context("Backend condition transition", func() {
		It("should transition StorageBackendReady from True to False when hub Secret disappears", func() {
			name := "storage-test-backend-transition"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createHubSecret(ctx, name, secretsNamespace)

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			cond := tenant.GetStatusCondition(v1alpha1.TenantConditionStorageBackendReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))

			secret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      fmt.Sprintf("vast-tenant-config-%s", name),
				Namespace: secretsNamespace,
			}, secret)).To(Succeed())
			Expect(k8sClient.Delete(ctx, secret)).To(Succeed())

			provider := &mockProvisioningProvider{}
			r2 := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				provider, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)

			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
				_, err := r2.Reconcile(ctx, storageReconcileRequest(nn))
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
				cond := tenant.GetStatusCondition(v1alpha1.TenantConditionStorageBackendReady)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			}, 5*time.Second, 100*time.Millisecond).Should(Succeed())
		})
	})

	Context("Deprovisioning ordering", func() {
		It("should clean up cluster storage before backend teardown", func() {
			name := "storage-test-deprov-order"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createHubSecret(ctx, name, secretsNamespace)

			var deprovisionOrder []string
			clusterProvider := &mockProvisioningProvider{
				name: "cluster-mock",
				triggerDeprovisionFunc: func(_ context.Context, _ client.Object, _ []v1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
					deprovisionOrder = append(deprovisionOrder, "cluster-storage")
					return &provisioning.DeprovisionResult{
						Action: provisioning.DeprovisionTriggered,
						JobID:  "deprov-cs-1",
					}, nil
				},
				getDeprovisionStatusFunc: func(_ context.Context, _ client.Object, _ string) (provisioning.ProvisionStatus, error) {
					return provisioning.ProvisionStatus{State: v1alpha1.JobStateSucceeded, Message: "done"}, nil
				},
			}
			backendProvider := &mockProvisioningProvider{
				name: "backend-mock",
				triggerDeprovisionFunc: func(_ context.Context, _ client.Object, _ []v1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
					deprovisionOrder = append(deprovisionOrder, "backend")
					return &provisioning.DeprovisionResult{
						Action: provisioning.DeprovisionTriggered,
						JobID:  "deprov-be-1",
					}, nil
				},
				getDeprovisionStatusFunc: func(_ context.Context, _ client.Object, _ string) (provisioning.ProvisionStatus, error) {
					return provisioning.ProvisionStatus{State: v1alpha1.JobStateSucceeded, Message: "done"}, nil
				},
			}

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				backendProvider, clusterProvider, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			Expect(tenant.Finalizers).To(ContainElement(storageFinalizer))

			Expect(k8sClient.Delete(ctx, tenant)).To(Succeed())

			Eventually(func(g Gomega) {
				_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(deprovisionOrder).To(HaveLen(2))
			}, 5*time.Second, 100*time.Millisecond).Should(Succeed())

			Expect(deprovisionOrder[0]).To(Equal("cluster-storage"))
			Expect(deprovisionOrder[1]).To(Equal("backend"))
		})
	})

	Context("Duplicate StorageClass detection", func() {
		It("should set ClusterStorageReady=False with MultipleFound when duplicate SCs exist for same tier", func() {
			name := "storage-test-dup-sc"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createHubSecret(ctx, name, secretsNamespace)
			createLabeledStorageClass(ctx, name+"-sc-1", name, "default")
			createLabeledStorageClass(ctx, name+"-sc-2", name, "default")

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())

			cond := tenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(v1alpha1.TenantReasonMultipleFound))
			Expect(tenant.Status.StorageClasses).To(BeEmpty())
		})
	})

	Context("CaaS: mapClusterOrderToTenant", func() {
		It("should return reconcile request for the owning Tenant", func() {
			tenantName := "caas-map-valid"
			createReadyTenantForStorage(ctx, tenantName, testNamespace)

			co := newClusterOrder("caas-map-co-1", testNamespace, map[string]string{
				osacTenantKey: tenantName,
			})
			Expect(k8sClient.Create(ctx, co)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, co) })

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval, provisioning.DefaultMaxJobHistory,
			)

			requests := r.mapClusterOrderToTenant(ctx, co)
			Expect(requests).To(HaveLen(1))
			Expect(requests[0].Name).To(Equal(tenantName))
			Expect(requests[0].Namespace).To(Equal(testNamespace))
		})

		It("should return nil when tenant annotation is missing", func() {
			co := newClusterOrder("caas-map-no-annotation", testNamespace, nil)
			Expect(k8sClient.Create(ctx, co)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, co) })

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval, provisioning.DefaultMaxJobHistory,
			)

			requests := r.mapClusterOrderToTenant(ctx, co)
			Expect(requests).To(BeNil())
		})

		It("should return nil when tenant annotation is empty", func() {
			co := newClusterOrder("caas-map-empty-annotation", testNamespace, map[string]string{
				osacTenantKey: "",
			})
			Expect(k8sClient.Create(ctx, co)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, co) })

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval, provisioning.DefaultMaxJobHistory,
			)

			requests := r.mapClusterOrderToTenant(ctx, co)
			Expect(requests).To(BeNil())
		})

		It("should return nil when referenced Tenant does not exist", func() {
			co := newClusterOrder("caas-map-no-tenant", testNamespace, map[string]string{
				osacTenantKey: "nonexistent-tenant",
			})
			Expect(k8sClient.Create(ctx, co)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, co) })

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval, provisioning.DefaultMaxJobHistory,
			)

			requests := r.mapClusterOrderToTenant(ctx, co)
			Expect(requests).To(BeNil())
		})
	})

	Context("Backend API routing (OSAC-1957)", func() {
		It("should fall through to SC resolution when BackendsClient is nil", func() {
			name := "storage-test-nil-client"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createLabeledStorageClass(ctx, name+"-sc", name, "default")

			// BackendProvider set but BackendsClient nil: should behave as no backend registered
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				&mockProvisioningProvider{}, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())

			backendCond := tenant.GetStatusCondition(v1alpha1.TenantConditionStorageBackendReady)
			Expect(backendCond).NotTo(BeNil())
			Expect(backendCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(backendCond.Reason).To(Equal(v1alpha1.TenantReasonNoProvider))
			Expect(backendCond.Message).To(ContainSubstring("No fulfillment service connection configured"))
			// Falls through to Stage 2: tenant-specific SC resolved, no AAP job triggered
			Expect(tenant.Status.StorageClasses).NotTo(BeEmpty())
			Expect(tenant.Status.StorageBackendJobs).To(BeEmpty())
		})

		It("should fall through to SC resolution when BackendsClient reports no backends (total=0)", func() {
			name := "storage-test-zero-backends"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createLabeledStorageClass(ctx, name+"-sc", name, "default")

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				&mockProvisioningProvider{}, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.BackendsClient = &mockStorageBackendsClient{} // default: total=0

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())

			backendCond := tenant.GetStatusCondition(v1alpha1.TenantConditionStorageBackendReady)
			Expect(backendCond).NotTo(BeNil())
			Expect(backendCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(backendCond.Reason).To(Equal(v1alpha1.TenantReasonNoProvider))
			Expect(backendCond.Message).To(ContainSubstring("No storage backend registered"))
			Expect(tenant.Status.StorageClasses).NotTo(BeEmpty())
			Expect(tenant.Status.StorageBackendJobs).To(BeEmpty())
		})

		It("should set BackendConfiguredNoProvider when backend is registered but no AAP configured", func() {
			name := "storage-test-backend-no-aap"
			createReadyTenantForStorage(ctx, name, testNamespace)

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.BackendsClient = registeredBackendsClient(1)

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			result, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())

			cond := tenant.GetStatusCondition(v1alpha1.TenantConditionStorageBackendReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(v1alpha1.TenantReasonBackendConfiguredNoProvider))
			Expect(tenant.Status.StorageBackendJobs).To(BeEmpty())
		})

		It("should requeue with StatusPollInterval when cluster storage job failed and hub Secret exists", func() {
			name := "storage-test-cs-retry"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createHubSecret(ctx, name, secretsNamespace)

			// Mock returns an immediately-failed job so the second reconcile hits the retry path.
			clusterProvider := &mockProvisioningProvider{
				name: "cluster-mock",
				triggerProvisionFunc: func(_ context.Context, _ client.Object) (*provisioning.ProvisionResult, error) {
					return &provisioning.ProvisionResult{
						JobID:        "cs-job-retry",
						InitialState: v1alpha1.JobStateFailed,
						Message:      "cluster storage provisioning failed",
					}, nil
				},
			}
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, clusterProvider, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			// First reconcile: triggers provisioning; mock returns immediately-failed job.
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile: failed job found, hub Secret exists → requeue with backoff.
			result, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(pollInterval))
		})

		It("should propagate error when BackendsClient.List returns a gRPC error", func() {
			name := "storage-test-backends-err"
			createReadyTenantForStorage(ctx, name, testNamespace)

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				&mockProvisioningProvider{}, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.BackendsClient = &mockStorageBackendsClient{
				listFunc: func(_ context.Context, _ *privatev1.StorageBackendsListRequest, _ ...grpc.CallOption) (*privatev1.StorageBackendsListResponse, error) {
					return nil, fmt.Errorf("connection refused")
				},
			}

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("connection refused"))
		})

		It("should not requeue when cluster storage job failed and no hub Secret exists", func() {
			name := "storage-test-cs-no-retry"
			createReadyTenantForStorage(ctx, name, testNamespace)
			// No hub secret: hubSecretReady=false → wait for external trigger on failure.

			clusterProvider := &mockProvisioningProvider{
				name: "cluster-mock",
				triggerProvisionFunc: func(_ context.Context, _ client.Object) (*provisioning.ProvisionResult, error) {
					return &provisioning.ProvisionResult{
						JobID:        "cs-job-no-retry",
						InitialState: v1alpha1.JobStateFailed,
						Message:      "cluster storage provisioning failed",
					}, nil
				},
			}
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, clusterProvider, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			// First reconcile: triggers provisioning; mock returns immediately-failed job.
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile: failed job found, no hub Secret → no requeue (wait for external trigger).
			result, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())
		})
	})

	Context("CaaS: getClusterKubeconfig", func() {
		// HyperShift convention: HCP namespace = {HC-namespace}-{HC-name}
		const hcNamespace = "clusters-caas-test"
		const hcName = "test-hcp"
		const hcpNamespace = hcNamespace + "-" + hcName

		BeforeEach(func() {
			for _, nsName := range []string{hcNamespace, hcpNamespace} {
				ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: nsName}}
				if err := k8sClient.Create(ctx, ns); err != nil {
					Expect(client.IgnoreAlreadyExists(err)).To(Succeed())
				}
			}
		})

		It("should return kubeconfig from HostedControlPlane Secret reference", func() {
			kubeconfigData := []byte("apiVersion: v1\nclusters: []\n")

			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "admin-kubeconfig",
					Namespace: hcpNamespace,
				},
				Data: map[string][]byte{
					"kubeconfig": kubeconfigData,
				},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, secret) })

			hcp := &hypershiftv1beta1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      hcName,
					Namespace: hcpNamespace,
				},
			}
			Expect(k8sClient.Create(ctx, hcp)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, hcp) })

			hcp.Status.KubeConfig = &hypershiftv1beta1.KubeconfigSecretRef{
				Name: "admin-kubeconfig",
				Key:  "kubeconfig",
			}
			Expect(k8sClient.Status().Update(ctx, hcp)).To(Succeed())

			co := newClusterOrder("caas-kc-valid", testNamespace, nil)
			Expect(k8sClient.Create(ctx, co)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, co) })
			co.Status.ClusterReference = &v1alpha1.ClusterOrderClusterReferenceType{
				Namespace:         hcNamespace,
				HostedClusterName: hcName,
			}
			Expect(k8sClient.Status().Update(ctx, co)).To(Succeed())

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval, provisioning.DefaultMaxJobHistory,
			)

			result, err := r.getClusterKubeconfig(ctx, co)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(kubeconfigData))
		})

		It("should return nil when ClusterReference is nil", func() {
			co := newClusterOrder("caas-kc-no-ref", testNamespace, nil)
			Expect(k8sClient.Create(ctx, co)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, co) })

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval, provisioning.DefaultMaxJobHistory,
			)

			result, err := r.getClusterKubeconfig(ctx, co)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeNil())
		})

		It("should return nil when HostedControlPlane does not exist", func() {
			co := newClusterOrder("caas-kc-no-hcp", testNamespace, nil)
			Expect(k8sClient.Create(ctx, co)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, co) })
			co.Status.ClusterReference = &v1alpha1.ClusterOrderClusterReferenceType{
				Namespace:         hcNamespace,
				HostedClusterName: "nonexistent-hcp",
			}
			Expect(k8sClient.Status().Update(ctx, co)).To(Succeed())

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval, provisioning.DefaultMaxJobHistory,
			)

			result, err := r.getClusterKubeconfig(ctx, co)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeNil())
		})

		It("should return nil when kubeconfig Secret does not exist", func() {
			hcpNsNoSecret := hcNamespace + "-test-hcp-no-secret"
			nsNoSecret := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: hcpNsNoSecret}}
			if err := k8sClient.Create(ctx, nsNoSecret); err != nil {
				Expect(client.IgnoreAlreadyExists(err)).To(Succeed())
			}

			hcp := &hypershiftv1beta1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp-no-secret",
					Namespace: hcpNsNoSecret,
				},
			}
			Expect(k8sClient.Create(ctx, hcp)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, hcp) })

			hcp.Status.KubeConfig = &hypershiftv1beta1.KubeconfigSecretRef{
				Name: "nonexistent-secret",
				Key:  "kubeconfig",
			}
			Expect(k8sClient.Status().Update(ctx, hcp)).To(Succeed())

			co := newClusterOrder("caas-kc-no-secret", testNamespace, nil)
			Expect(k8sClient.Create(ctx, co)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, co) })
			co.Status.ClusterReference = &v1alpha1.ClusterOrderClusterReferenceType{
				Namespace:         hcNamespace,
				HostedClusterName: "test-hcp-no-secret",
			}
			Expect(k8sClient.Status().Update(ctx, co)).To(Succeed())

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval, provisioning.DefaultMaxJobHistory,
			)

			result, err := r.getClusterKubeconfig(ctx, co)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeNil())
		})
	})

	Context("CaaS: provisioning trigger", func() {
		It("should trigger CaaS provisioning for Ready ClusterOrder with backend ready", func() {
			tenantName := "caas-prov-trigger"
			createReadyTenantForStorage(ctx, tenantName, testNamespace)
			createHubSecret(ctx, tenantName, secretsNamespace)
			// VMaaS StorageClasses must already exist from tenant onboarding
			// before CaaS provisioning runs, because the VMaaS resolution in
			// handleUpdate returns early if no SCs are found.
			createLabeledStorageClass(ctx, tenantName+"-vmaas-sc", tenantName, "default")

			co := newClusterOrder("caas-prov-co-1", testNamespace, map[string]string{
				osacTenantKey: tenantName,
			})
			Expect(k8sClient.Create(ctx, co)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, co) })

			co.Status.Phase = v1alpha1.ClusterOrderPhaseReady
			Expect(k8sClient.Status().Update(ctx, co)).To(Succeed())

			// ClusterStorageProvider triggers provisioning; the kubeconfig
			// retrieval returns nil (no HCP) so the controller sets
			// KubeConfigNotAvailable and continues.
			clusterProvider := &mockProvisioningProvider{name: "caas-mock"}
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, clusterProvider, pollInterval, provisioning.DefaultMaxJobHistory,
			)

			nn := types.NamespacedName{Name: tenantName, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			// ClusterOrder should have the storage finalizer and
			// KubeConfigNotAvailable condition
			updatedCO := &v1alpha1.ClusterOrder{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(co), updatedCO)).To(Succeed())
			Expect(updatedCO.Finalizers).To(ContainElement(clusterStorageFinalizer))
			Expect(updatedCO.IsStatusConditionFalse(
				string(v1alpha1.ClusterOrderConditionClusterStorageReady))).To(BeTrue())
		})

		It("should skip CaaS provisioning for non-Ready ClusterOrders", func() {
			tenantName := "caas-prov-skip-notready"
			createReadyTenantForStorage(ctx, tenantName, testNamespace)
			createHubSecret(ctx, tenantName, secretsNamespace)

			co := newClusterOrder("caas-prov-co-notready", testNamespace, map[string]string{
				osacTenantKey: tenantName,
			})
			Expect(k8sClient.Create(ctx, co)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, co) })

			co.Status.Phase = v1alpha1.ClusterOrderPhaseProgressing
			Expect(k8sClient.Status().Update(ctx, co)).To(Succeed())

			clusterProvider := &mockProvisioningProvider{name: "caas-mock"}
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, clusterProvider, pollInterval, provisioning.DefaultMaxJobHistory,
			)

			nn := types.NamespacedName{Name: tenantName, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			updatedCO := &v1alpha1.ClusterOrder{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(co), updatedCO)).To(Succeed())
			Expect(updatedCO.Finalizers).NotTo(ContainElement(clusterStorageFinalizer))
		})
	})

	Context("CaaS: VMaaS regression", func() {
		It("should not affect VMaaS provisioning when CaaS ClusterOrders exist", func() {
			tenantName := "caas-vmaas-regression"
			createReadyTenantForStorage(ctx, tenantName, testNamespace)
			createHubSecret(ctx, tenantName, secretsNamespace)
			createLabeledStorageClass(ctx, tenantName+"-vmaas-sc", tenantName, "default")

			// Create a CaaS ClusterOrder for the same tenant
			co := newClusterOrder("caas-regression-co", testNamespace, map[string]string{
				osacTenantKey: tenantName,
			})
			Expect(k8sClient.Create(ctx, co)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, co) })

			co.Status.Phase = v1alpha1.ClusterOrderPhaseReady
			Expect(k8sClient.Status().Update(ctx, co)).To(Succeed())

			// No cluster provider: VMaaS-only setup with CaaS ClusterOrder present
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval, provisioning.DefaultMaxJobHistory,
			)

			nn := types.NamespacedName{Name: tenantName, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			// VMaaS StorageClass should still be resolved on the Tenant
			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			Expect(tenant.Status.StorageClasses).To(HaveLen(1))
			Expect(tenant.Status.StorageClasses[0].Name).To(Equal(tenantName + "-vmaas-sc"))

			clusterCond := tenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady)
			Expect(clusterCond).NotTo(BeNil())
			Expect(clusterCond.Status).To(Equal(metav1.ConditionTrue))
		})
	})
})
