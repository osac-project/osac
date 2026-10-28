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
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
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
		g.Expect(indexedClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, t)).To(Succeed())
		g.Expect(t.Status.Phase).To(Equal(v1alpha1.TenantPhaseReady))
	}, 5*time.Second, 10*time.Millisecond).Should(Succeed())
}

func createHubSecret(ctx context.Context, tenantName, namespace string) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("lvms-tenant-config-%s", tenantName),
			Namespace: namespace,
			Labels: map[string]string{
				osacTenantKey:            tenantName,
				osacStorageProviderLabel: "lvms",
			},
		},
		StringData: map[string]string{
			"provider": "lvms",
		},
	}
	Expect(k8sClient.Create(ctx, secret)).To(Succeed())
	waitForIndexedClientObject(secret)
}

// createLabeledStorageClass creates a StorageClass labeled with the given
// tenant and storage tier and registers a DeferCleanup hook to delete it.
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
	waitForIndexedClientObject(sc)
	DeferCleanup(func() {
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, sc))).To(Succeed())
	})
}

// waitForStorageClassInCache polls the local manager's cached client until the
// named StorageClass is visible to controller reads.
func waitForStorageClassInCache(scName string) {
	Eventually(func() error {
		return testMcManager.GetLocalManager().GetClient().Get(context.Background(),
			client.ObjectKey{Name: scName}, &storagev1.StorageClass{})
	}, 5*time.Second, 10*time.Millisecond).Should(Succeed())
}

type mockStorageTiersLister struct {
	listFunc      func(ctx context.Context, in *privatev1.StorageTiersListRequest, opts ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error)
	listCallCount int
}

func (m *mockStorageTiersLister) List(ctx context.Context, in *privatev1.StorageTiersListRequest, opts ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
	m.listCallCount++
	return m.listFunc(ctx, in, opts...)
}

// mockSecretsClient is a test double for SecretsClient. Set getFunc to override the
// response for password_secret resolution scenarios.
type mockSecretsClient struct {
	getFunc      func(ctx context.Context, in *privatev1.SecretsGetRequest, opts ...grpc.CallOption) (*privatev1.SecretsGetResponse, error)
	getCallCount int
}

func (m *mockSecretsClient) Get(ctx context.Context, in *privatev1.SecretsGetRequest, opts ...grpc.CallOption) (*privatev1.SecretsGetResponse, error) {
	m.getCallCount++
	return m.getFunc(ctx, in, opts...)
}

// newTestStorageTier builds a StorageTier fixture with one BackendAssociation per
// backendID given. Passing no backendIDs produces a tier with zero associations.
func newTestStorageTier(name string, backendIDs ...string) *privatev1.StorageTier {
	backends := make([]*privatev1.BackendAssociation, len(backendIDs))
	for i, id := range backendIDs {
		backends[i] = privatev1.BackendAssociation_builder{
			BackendId:            id,
			MaxReadBandwidthMbs:  100,
			MaxWriteBandwidthMbs: 100,
		}.Build()
	}
	return privatev1.StorageTier_builder{
		Metadata: privatev1.Metadata_builder{Name: name}.Build(),
		Spec: privatev1.StorageTierSpec_builder{
			Backends: backends,
			Protocol: privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS,
		}.Build(),
	}.Build()
}

// testBackendUsername and testBackendPassword are fixture values, not real
// credentials — used to verify connection details round-trip through
// resolveTierDefinitions and the AAP extra_vars conversion unmodified.
const (
	testBackendUsername = "test-backend-user"
	testBackendPassword = "test-backend-password"
	// testSecretPassword is the fixture password returned from a referenced Secret
	// when a backend uses password_secret instead of an inline password.
	testSecretPassword = "test-secret-password"
)

// newTestStorageBackendGetResponseWithSecret builds a StorageBackendsGetResponse whose
// credentials carry a password_secret reference (id secretID) and no inline password —
// the shape a backend created with password_secret has.
func newTestStorageBackendGetResponseWithSecret(provider, secretID string) *privatev1.StorageBackendsGetResponse {
	return privatev1.StorageBackendsGetResponse_builder{
		Object: privatev1.StorageBackend_builder{
			Spec: privatev1.StorageBackendSpec_builder{
				Provider: provider,
				Endpoint: "https://" + provider + ".example.com",
				Credentials: privatev1.StorageBackendCredentials_builder{
					Username:       testBackendUsername,
					PasswordSecret: privatev1.SecretLocalReference_builder{Id: secretID}.Build(),
				}.Build(),
			}.Build(),
		}.Build(),
	}.Build()
}

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

// newTestCSPManagedBackendGetResponse builds a StorageBackendsGetResponse fixture
// for a CSP-managed backend (CspManaged: true, no Endpoint or Credentials).
func newTestCSPManagedBackendGetResponse(provider string) *privatev1.StorageBackendsGetResponse {
	return privatev1.StorageBackendsGetResponse_builder{
		Object: privatev1.StorageBackend_builder{
			Spec: privatev1.StorageBackendSpec_builder{
				Provider:   provider,
				CspManaged: true,
			}.Build(),
		}.Build(),
	}.Build()
}

// configureTwoProviderReadinessClients wires tier and backend API responses for
// readiness tests that exercise two registered storage providers.
func configureTwoProviderReadinessClients(r *StorageReconciler, registeredBackendCount int32) {
	r.TiersClient = &mockStorageTiersLister{
		listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
			return privatev1.StorageTiersListResponse_builder{
				Items: []*privatev1.StorageTier{
					newTestStorageTier("local", "backend-lvms"),
					newTestStorageTier("block", "backend-vast"),
				},
			}.Build(), nil
		},
	}
	r.BackendsClient = &mockStorageBackendsClient{
		listFunc: registeredBackendsClient(registeredBackendCount).listFunc,
		getFunc: func(_ context.Context, in *privatev1.StorageBackendsGetRequest, _ ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error) {
			switch in.GetId() {
			case "backend-lvms":
				return newTestStorageBackendGetResponse("lvms"), nil
			case "backend-vast":
				return newTestStorageBackendGetResponse("vast"), nil
			default:
				return nil, status.Error(codes.NotFound, "not found")
			}
		},
	}
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

			defs, conns, err := resolveTierDefinitions(ctx, tiersClient, backendsClient, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(defs).To(HaveLen(1))
			Expect(defs[0].Name).To(Equal("fast"))
			Expect(defs[0].Protocol).To(Equal("nfs"))
			Expect(defs[0].Provider).To(Equal("vast"))
			Expect(defs[0].BackendID).To(Equal("backend-1"))
			Expect(defs[0].QosLimits.MaxReadBandwidthMBs).To(Equal(int32(100)))
			Expect(defs[0].QosLimits.MaxWriteBandwidthMBs).To(Equal(int32(100)))
			Expect(conns).To(HaveKeyWithValue("backend-1", provisioning.BackendConnection{
				Endpoint: "https://vast.example.com",
				Username: testBackendUsername,
				Password: testBackendPassword,
			}))
		})

		It("should resolve the backend password from the referenced Secret when the inline password is empty", func() {
			tiersClient := &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return privatev1.StorageTiersListResponse_builder{
						Items: []*privatev1.StorageTier{newTestStorageTier("fast", "backend-1")},
					}.Build(), nil
				},
			}
			backendsClient := &mockStorageBackendsClient{
				getFunc: func(context.Context, *privatev1.StorageBackendsGetRequest, ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error) {
					return newTestStorageBackendGetResponseWithSecret("vast", "secret-1"), nil
				},
			}
			secretsClient := &mockSecretsClient{
				getFunc: func(_ context.Context, in *privatev1.SecretsGetRequest, _ ...grpc.CallOption) (*privatev1.SecretsGetResponse, error) {
					Expect(in.GetId()).To(Equal("secret-1"))
					return privatev1.SecretsGetResponse_builder{
						Object: privatev1.Secret_builder{
							Type: privatev1.SecretType_SECRET_TYPE_VALUE,
							Data: map[string][]byte{"value": []byte(testSecretPassword)},
						}.Build(),
					}.Build(), nil
				},
			}

			defs, conns, err := resolveTierDefinitions(ctx, tiersClient, backendsClient, secretsClient)
			Expect(err).NotTo(HaveOccurred())
			Expect(defs).To(HaveLen(1))
			Expect(secretsClient.getCallCount).To(Equal(1))
			Expect(conns).To(HaveKeyWithValue("backend-1", provisioning.BackendConnection{
				Endpoint: "https://vast.example.com",
				Username: testBackendUsername,
				Password: testSecretPassword,
			}))
		})

		It("should skip a backend whose password_secret is missing data[\"value\"], not provision an empty password", func() {
			tiersClient := &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return privatev1.StorageTiersListResponse_builder{
						Items: []*privatev1.StorageTier{newTestStorageTier("fast", "backend-1")},
					}.Build(), nil
				},
			}
			backendsClient := &mockStorageBackendsClient{
				getFunc: func(context.Context, *privatev1.StorageBackendsGetRequest, ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error) {
					return newTestStorageBackendGetResponseWithSecret("vast", "secret-1"), nil
				},
			}
			secretsClient := &mockSecretsClient{
				getFunc: func(context.Context, *privatev1.SecretsGetRequest, ...grpc.CallOption) (*privatev1.SecretsGetResponse, error) {
					return privatev1.SecretsGetResponse_builder{
						Object: privatev1.Secret_builder{
							Type: privatev1.SecretType_SECRET_TYPE_VALUE,
							Data: map[string][]byte{},
						}.Build(),
					}.Build(), nil
				},
			}

			defs, conns, err := resolveTierDefinitions(ctx, tiersClient, backendsClient, secretsClient)
			Expect(err).NotTo(HaveOccurred())
			Expect(defs).To(BeEmpty())
			Expect(conns).NotTo(HaveKey("backend-1"))
		})

		It("should skip a backend using password_secret when no SecretsClient is configured", func() {
			tiersClient := &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return privatev1.StorageTiersListResponse_builder{
						Items: []*privatev1.StorageTier{newTestStorageTier("fast", "backend-1")},
					}.Build(), nil
				},
			}
			backendsClient := &mockStorageBackendsClient{
				getFunc: func(context.Context, *privatev1.StorageBackendsGetRequest, ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error) {
					return newTestStorageBackendGetResponseWithSecret("vast", "secret-1"), nil
				},
			}

			defs, conns, err := resolveTierDefinitions(ctx, tiersClient, backendsClient, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(defs).To(BeEmpty())
			Expect(conns).NotTo(HaveKey("backend-1"))
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

			defs, _, err := resolveTierDefinitions(ctx, tiersClient, backendsClient, nil)
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

			defs, conns, err := resolveTierDefinitions(ctx, tiersClient, backendsClient, nil)
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

			defs, conns, err := resolveTierDefinitions(ctx, tiersClient, backendsClient, nil)
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

			_, _, err := resolveTierDefinitions(ctx, tiersClient, backendsClient, nil)
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

			_, _, err := resolveTierDefinitions(ctx, tiersClient, backendsClient, nil)
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

		It("should defer reconciliation and preserve existing storage status when tier resolution fails even without AAP providers", func() {
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
			r.BackendsClient = registeredBackendsClient(0)

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			res, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			Expect(res.RequeueAfter).To(Equal(pollInterval))

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			backendCond := tenant.GetStatusCondition(v1alpha1.TenantConditionStorageBackendReady)
			Expect(backendCond).NotTo(BeNil())
			Expect(backendCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(backendCond.Reason).To(Equal("TierResolutionFailed"))
			Expect(backendCond.Message).To(ContainSubstring("fulfillment service unreachable"))
			Expect(tenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady)).To(BeNil())
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

		It("should resolve a cspManaged backend into cspDefinitions, skip SecretsClient, and omit it from connections", func() {
			tiersClient := &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return privatev1.StorageTiersListResponse_builder{
						Items: []*privatev1.StorageTier{
							newTestStorageTier("gold", "backend-csp"),
							newTestStorageTier("silver", "backend-csp"),
						},
					}.Build(), nil
				},
			}
			backendsClient := &mockStorageBackendsClient{
				getFunc: func(_ context.Context, in *privatev1.StorageBackendsGetRequest, _ ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error) {
					Expect(in.GetId()).To(Equal("backend-csp"))
					return newTestCSPManagedBackendGetResponse("ceph"), nil
				},
			}
			secretsClient := &mockSecretsClient{
				getFunc: func(context.Context, *privatev1.SecretsGetRequest, ...grpc.CallOption) (*privatev1.SecretsGetResponse, error) {
					Fail("SecretsClient.Get must not be called for a cspManaged backend")
					return nil, nil
				},
			}

			osacDefs, cspDefs, conns, skipped, err := resolveTierDefinitionsWithStatus(ctx, tiersClient, backendsClient, secretsClient)
			Expect(err).NotTo(HaveOccurred())
			Expect(skipped).To(BeFalse())
			Expect(backendsClient.getCallCount).To(Equal(1))
			Expect(secretsClient.getCallCount).To(Equal(0))
			Expect(osacDefs).To(BeEmpty())
			Expect(cspDefs).To(HaveLen(2))
			Expect(cspDefs[0].Name).To(Equal("gold"))
			Expect(cspDefs[0].Provider).To(Equal("ceph"))
			Expect(cspDefs[0].BackendID).To(Equal("backend-csp"))
			Expect(cspDefs[1].Name).To(Equal("silver"))
			Expect(conns).To(BeEmpty())
		})

		It("should resolve STORAGE_PROTOCOL_UNSPECIFIED to an empty protocol for both cspManaged and OSAC-managed tiers", func() {
			cspTier := newTestStorageTier("csp-unspecified", "backend-csp")
			cspTier.GetSpec().SetProtocol(privatev1.StorageProtocol_STORAGE_PROTOCOL_UNSPECIFIED)
			osacTier := newTestStorageTier("osac-unspecified", "backend-vast")
			osacTier.GetSpec().SetProtocol(privatev1.StorageProtocol_STORAGE_PROTOCOL_UNSPECIFIED)

			tiersClient := &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return privatev1.StorageTiersListResponse_builder{
						Items: []*privatev1.StorageTier{cspTier, osacTier},
					}.Build(), nil
				},
			}
			backendsClient := &mockStorageBackendsClient{
				getFunc: func(_ context.Context, in *privatev1.StorageBackendsGetRequest, _ ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error) {
					switch in.GetId() {
					case "backend-csp":
						return newTestCSPManagedBackendGetResponse("ceph"), nil
					case "backend-vast":
						return newTestStorageBackendGetResponse("vast"), nil
					default:
						return nil, status.Error(codes.NotFound, "not found")
					}
				},
			}

			osacDefs, cspDefs, _, skipped, err := resolveTierDefinitionsWithStatus(ctx, tiersClient, backendsClient, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(skipped).To(BeFalse())
			Expect(cspDefs).To(HaveLen(1))
			Expect(cspDefs[0].Name).To(Equal("csp-unspecified"))
			Expect(cspDefs[0].Protocol).To(Equal(""))
			Expect(osacDefs).To(HaveLen(1))
			Expect(osacDefs[0].Name).To(Equal("osac-unspecified"))
			Expect(osacDefs[0].Protocol).To(Equal(""))
		})

		It("should inject only non-CSP tiers and backends into AAP context when comingled tiers are resolved", func() {
			tiersClient := &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return privatev1.StorageTiersListResponse_builder{
						Items: []*privatev1.StorageTier{
							newTestStorageTier("csp-gold", "backend-csp"),
							newTestStorageTier("osac-fast", "backend-vast"),
						},
					}.Build(), nil
				},
			}
			backendsClient := &mockStorageBackendsClient{
				getFunc: func(_ context.Context, in *privatev1.StorageBackendsGetRequest, _ ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error) {
					switch in.GetId() {
					case "backend-csp":
						return newTestCSPManagedBackendGetResponse("ceph"), nil
					case "backend-vast":
						return newTestStorageBackendGetResponse("vast"), nil
					default:
						return nil, status.Error(codes.NotFound, "not found")
					}
				},
			}
			secretsClient := &mockSecretsClient{
				getFunc: func(context.Context, *privatev1.SecretsGetRequest, ...grpc.CallOption) (*privatev1.SecretsGetResponse, error) {
					Fail("SecretsClient.Get must not be called when inline password is set or backend is cspManaged")
					return nil, nil
				},
			}

			injectedCtx, osacTiers, cspTiers, skipped, err := resolveAndPartitionTierContext(ctx, tiersClient, backendsClient, secretsClient, "tenant", "test-tenant")
			Expect(err).NotTo(HaveOccurred())
			Expect(skipped).To(BeFalse())
			Expect(osacTiers).To(HaveLen(1))
			Expect(osacTiers[0].Name).To(Equal("osac-fast"))
			Expect(cspTiers).To(HaveLen(1))
			Expect(cspTiers[0].Name).To(Equal("csp-gold"))

			ctxTiers := provisioning.StorageTierDefinitionsFromContext(injectedCtx)
			Expect(ctxTiers).To(HaveLen(1))
			Expect(ctxTiers[0].Name).To(Equal("osac-fast"))

			ctxConns := provisioning.StorageBackendConnectionsFromContext(injectedCtx)
			Expect(ctxConns).To(HaveLen(1))
			Expect(ctxConns).To(HaveKeyWithValue("backend-vast", provisioning.BackendConnection{
				Endpoint: "https://vast.example.com",
				Username: testBackendUsername,
				Password: testBackendPassword,
			}))
			Expect(ctxConns).NotTo(HaveKey("backend-csp"))
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

	Context("Tier resolution failure guard", func() {
		// These tests verify that when TiersClient and BackendsClient are both
		// configured but tier resolution fails (e.g. fulfillment service down),
		// the storage reconciler does NOT proceed to call AAP provisioning
		// providers — matching the guard already present in handleCaaSDelete
		// (OSAC-4855) but extending it to the provisioning (update) paths.

		It("should not call ClusterStorageProvider when tier resolution fails", func() {
			name := "storage-test-guard-cluster-provider"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createHubSecret(ctx, name, secretsNamespace)

			triggerCount := 0
			clusterProvider := &mockProvisioningProvider{
				name: "cluster-storage-mock",
				triggerProvisionFunc: func(_ context.Context, _ client.Object) (*provisioning.ProvisionResult, error) {
					triggerCount++
					return &provisioning.ProvisionResult{JobID: "should-not-run", InitialState: v1alpha1.JobStatePending}, nil
				},
			}

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, clusterProvider, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.TiersClient = &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return nil, status.Error(codes.Unavailable, "fulfillment service unreachable")
				},
			}
			r.BackendsClient = registeredBackendsClient(0)

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			result, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(pollInterval),
				"should requeue so the controller retries when the fulfillment service recovers")
			Expect(triggerCount).To(Equal(0),
				"AAP must not be called when tier resolution failed and a provider is configured")

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			backendCond := tenant.GetStatusCondition(v1alpha1.TenantConditionStorageBackendReady)
			Expect(backendCond).NotTo(BeNil())
			Expect(backendCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(backendCond.Reason).To(Equal("TierResolutionFailed"))
			Expect(backendCond.Message).To(ContainSubstring("fulfillment service unreachable"),
				"condition message should carry the actual resolution error")
		})

		It("should not call BackendProvider when tier resolution fails", func() {
			name := "storage-test-guard-backend-provider"
			createReadyTenantForStorage(ctx, name, testNamespace)

			triggerCount := 0
			backendProvider := &mockProvisioningProvider{
				name: "backend-mock",
				triggerProvisionFunc: func(_ context.Context, _ client.Object) (*provisioning.ProvisionResult, error) {
					triggerCount++
					return &provisioning.ProvisionResult{JobID: "should-not-run", InitialState: v1alpha1.JobStatePending}, nil
				},
			}

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				backendProvider, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.TiersClient = &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return nil, status.Error(codes.Unauthenticated, "token expired")
				},
			}
			r.BackendsClient = registeredBackendsClient(1)

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			result, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(pollInterval))
			Expect(triggerCount).To(Equal(0),
				"BackendProvider AAP must not be called when tier resolution failed")

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			backendCond := tenant.GetStatusCondition(v1alpha1.TenantConditionStorageBackendReady)
			Expect(backendCond).NotTo(BeNil())
			Expect(backendCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(backendCond.Reason).To(Equal("TierResolutionFailed"))
			Expect(backendCond.Message).To(ContainSubstring("token expired"),
				"condition message should carry the actual resolution error")
		})

		It("should proceed normally when tier clients are nil (backward compat)", func() {
			name := "storage-test-guard-nil-clients"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createLabeledStorageClass(ctx, name+"-default-sc", name, "default")

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			// TiersClient and BackendsClient are nil — pre-tier-API environment

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			clusterCond := tenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady)
			Expect(clusterCond).NotTo(BeNil())
			Expect(clusterCond.Status).To(Equal(metav1.ConditionTrue),
				"backward compatibility: nil tier clients must not block StorageClass resolution")
		})

		It("should not block deletion when tier resolution fails", func() {
			name := "storage-test-guard-delete-unblocked"
			createReadyTenantForStorage(ctx, name, testNamespace)

			// Use no providers so deletion proceeds without AAP job polling,
			// matching the existing "should run deletion even when class
			// provider is nil" pattern. handleDelete skips deprovisioning
			// and removes the finalizer despite the tier resolution error.
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.TiersClient = &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return nil, status.Error(codes.Unavailable, "fulfillment service unreachable")
				},
			}
			r.BackendsClient = registeredBackendsClient(0)

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			// First reconcile adds finalizer; no providers configured so no
			// guard triggers and the reconcile completes normally.
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			Expect(tenant.Finalizers).To(ContainElement(storageFinalizer))

			Expect(k8sClient.Delete(ctx, tenant)).To(Succeed())

			Eventually(func(g Gomega) {
				_, reconcileErr := r.Reconcile(ctx, storageReconcileRequest(nn))
				g.Expect(reconcileErr).NotTo(HaveOccurred(),
					"tier resolution failure must not block finalizer removal during deletion")

				t := &v1alpha1.Tenant{}
				getErr := k8sClient.Get(ctx, nn, t)
				g.Expect(client.IgnoreNotFound(getErr)).To(Succeed())
				if getErr == nil {
					g.Expect(t.Finalizers).NotTo(ContainElement(storageFinalizer))
				}
			}).Should(Succeed())
		})

		It("should retain finalizer and requeue when tier resolution fails during deletion with providers configured", func() {
			name := "storage-test-guard-delete-retain-finalizer"
			createReadyTenantForStorage(ctx, name, testNamespace)

			deprovTriggers := 0
			clusterProvider := &mockProvisioningProvider{
				name: "cluster-storage-mock",
				triggerDeprovisionFunc: func(_ context.Context, _ client.Object, _ []v1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
					deprovTriggers++
					return &provisioning.DeprovisionResult{
						Action:                 provisioning.DeprovisionTriggered,
						JobID:                  "should-not-run-deprov",
						BlockDeletionOnFailure: true,
					}, nil
				},
			}

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, clusterProvider, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.TiersClient = &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return nil, status.Error(codes.Unavailable, "fulfillment service unreachable")
				},
			}
			r.BackendsClient = registeredBackendsClient(0)

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			// First reconcile: handleUpdate guard triggers (tier err + provider),
			// but the finalizer is added before the guard.
			result, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(pollInterval))

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			Expect(tenant.Finalizers).To(ContainElement(storageFinalizer))

			Expect(k8sClient.Delete(ctx, tenant)).To(Succeed())

			// Reconcile with DeletionTimestamp set: tier resolution still fails,
			// so handleDelete retains the finalizer and requeues instead of
			// launching AAP deprovisioning without backend connections.
			Eventually(func(g Gomega) {
				deleteResult, reconcileErr := r.Reconcile(ctx, storageReconcileRequest(nn))
				g.Expect(reconcileErr).NotTo(HaveOccurred())
				g.Expect(deleteResult.RequeueAfter).To(Equal(pollInterval),
					"should requeue at poll interval so the controller retries when the fulfillment service recovers")

				t := &v1alpha1.Tenant{}
				g.Expect(k8sClient.Get(ctx, nn, t)).To(Succeed())
				g.Expect(t.Finalizers).To(ContainElement(storageFinalizer),
					"finalizer must be retained — deprovisioning deferred until tier resolution recovers")
			}).Should(Succeed())

			Expect(deprovTriggers).To(Equal(0),
				"deprovisioning AAP must not be called when tier resolution failed during deletion")
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

		It("should remove the finalizer during deletion when only TiersClient is set (partial-client backward compat)", func() {
			name := "storage-test-delete-partial-client"
			createReadyTenantForStorage(ctx, name, testNamespace)

			backendProvider := &mockProvisioningProvider{name: "backend-mock"}
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				backendProvider, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			// TiersClient is set but BackendsClient is nil — resolveAndInjectTierContext
			// requires both to attempt resolution, so it skips and returns nil error.
			// This covers the partial-client misconfiguration path: no tier resolution
			// is attempted, no error is raised, and deletion proceeds normally.
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
				g.Expect(err).NotTo(HaveOccurred(),
					"partial client config (TiersClient only) must not block finalizer removal during deletion")

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
				Name:      fmt.Sprintf("lvms-tenant-config-%s", name),
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

	Context("hubSecretExists provider filtering", func() {
		It("should filter by storage-provider label when provider is non-empty", func() {
			name := "storage-test-provider-filter"
			// Create a secret with the lvms storage-provider label
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("lvms-tenant-config-%s", name),
					Namespace: secretsNamespace,
					Labels: map[string]string{
						osacTenantKey:            name,
						osacStorageProviderLabel: "lvms",
					},
				},
				StringData: map[string]string{
					"provider": "lvms",
				},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())
			waitForIndexedClientObject(secret)
			DeferCleanup(func() {
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, secret))).To(Succeed())
			})

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)

			// provider="lvms" → should find the secret
			found, err := r.hubSecretExists(ctx, name, "lvms")
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeTrue(), "expected hub secret to be found when filtering by provider=lvms")

			// provider="vast" → should NOT find the secret (wrong provider)
			found, err = r.hubSecretExists(ctx, name, "vast")
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeFalse(), "expected hub secret NOT to be found when filtering by provider=vast")

			// provider="" → should find the secret (backward compat, no provider filter)
			found, err = r.hubSecretExists(ctx, name, "")
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeTrue(), "expected hub secret to be found when provider is empty (backward compat)")
		})
	})

	Context("Multi-provider hub secret readiness (OSAC-4855)", func() {
		It("should not set StorageBackendReady=True when hub Secrets exist for only some dispatched providers", func() {
			name := "storage-test-partial-provider-secrets"
			createReadyTenantForStorage(ctx, name, testNamespace)
			// createHubSecret creates only an lvms-provider Secret
			createHubSecret(ctx, name, secretsNamespace)

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			configureTwoProviderReadinessClients(r, 0)

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())

			backendCond := tenant.GetStatusCondition(v1alpha1.TenantConditionStorageBackendReady)
			Expect(backendCond).NotTo(BeNil())
			Expect(backendCond.Status).To(Equal(metav1.ConditionFalse),
				"should not be True when only some providers have hub Secrets")
			Expect(backendCond.Reason).To(Equal(v1alpha1.TenantReasonNoProvider))
			Expect(backendCond.Message).To(Equal("Hub Secret readiness by backend: ready (1): backend-lvms: lvms; not ready (1): backend-vast: vast; no storage backend registered"))
			Expect(tenant.Status.StorageBackends).To(ConsistOf(
				v1alpha1.StorageBackendStatus{
					Name:     "backend-lvms",
					Provider: "lvms",
					Ready:    true,
					Message:  fmt.Sprintf("Hub Secret for tenant %q exists", name),
				},
				v1alpha1.StorageBackendStatus{
					Name:     "backend-vast",
					Provider: "vast",
					Ready:    false,
					Message:  fmt.Sprintf("Hub Secret for tenant %q not found", name),
				},
			))
		})

		It("should explicitly report when no backend Hub Secrets are ready", func() {
			name := "storage-test-no-provider-secrets"
			createReadyTenantForStorage(ctx, name, testNamespace)

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			configureTwoProviderReadinessClients(r, 0)

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())

			backendCond := tenant.GetStatusCondition(v1alpha1.TenantConditionStorageBackendReady)
			Expect(backendCond).NotTo(BeNil())
			Expect(backendCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(backendCond.Reason).To(Equal(v1alpha1.TenantReasonNoProvider))
			Expect(backendCond.Message).To(Equal("Hub Secret readiness by backend: ready (0): none; not ready (2): backend-lvms: lvms, backend-vast: vast; no backend Hub Secrets are ready; no storage backend registered"))
			Expect(tenant.Status.StorageBackends).To(ConsistOf(
				v1alpha1.StorageBackendStatus{
					Name:     "backend-lvms",
					Provider: "lvms",
					Ready:    false,
					Message:  fmt.Sprintf("Hub Secret for tenant %q not found", name),
				},
				v1alpha1.StorageBackendStatus{
					Name:     "backend-vast",
					Provider: "vast",
					Ready:    false,
					Message:  fmt.Sprintf("Hub Secret for tenant %q not found", name),
				},
			))
		})

		It("should set StorageBackendReady=True when hub Secrets exist for all dispatched providers", func() {
			name := "storage-test-all-provider-secrets"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createHubSecret(ctx, name, secretsNamespace) // lvms secret

			// Create vast secret too
			vastSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("vast-tenant-config-%s", name),
					Namespace: secretsNamespace,
					Labels: map[string]string{
						osacTenantKey:            name,
						osacStorageProviderLabel: "vast",
					},
				},
				StringData: map[string]string{"provider": "vast"},
			}
			Expect(k8sClient.Create(ctx, vastSecret)).To(Succeed())
			waitForIndexedClientObject(vastSecret)
			DeferCleanup(func() {
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, vastSecret))).To(Succeed())
			})

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			configureTwoProviderReadinessClients(r, 0)

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())

			backendCond := tenant.GetStatusCondition(v1alpha1.TenantConditionStorageBackendReady)
			Expect(backendCond).NotTo(BeNil())
			Expect(backendCond.Status).To(Equal(metav1.ConditionTrue),
				"should be True when all providers have hub Secrets")
			Expect(backendCond.Reason).To(Equal(v1alpha1.TenantReasonFound))
			Expect(backendCond.Message).To(Equal("Hub Secret readiness by backend: ready (2): backend-lvms: lvms, backend-vast: vast; not ready (0): none"))
			Expect(tenant.Status.StorageBackends).To(ConsistOf(
				v1alpha1.StorageBackendStatus{
					Name:     "backend-lvms",
					Provider: "lvms",
					Ready:    true,
					Message:  fmt.Sprintf("Hub Secret for tenant %q exists", name),
				},
				v1alpha1.StorageBackendStatus{
					Name:     "backend-vast",
					Provider: "vast",
					Ready:    true,
					Message:  fmt.Sprintf("Hub Secret for tenant %q exists", name),
				},
			))
		})

		It("should fall back to any-secret check when no tier definitions are available", func() {
			name := "storage-test-no-tiers-fallback"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createHubSecret(ctx, name, secretsNamespace)

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			// No TiersClient/BackendsClient: tierDefinitions will be nil

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())

			backendCond := tenant.GetStatusCondition(v1alpha1.TenantConditionStorageBackendReady)
			Expect(backendCond).NotTo(BeNil())
			Expect(backendCond.Status).To(Equal(metav1.ConditionTrue),
				"should be True when any hub Secret exists and no tier definitions are available (backward compat)")
			Expect(backendCond.Reason).To(Equal(v1alpha1.TenantReasonFound))
			Expect(backendCond.Message).To(Equal("No storage backend entries are available to evaluate (ready: 0; not ready: 0); legacy tenant-wide Hub Secret check: found a Secret"))
		})
	})

	Context("Multi-tier retry (OSAC-4855)", func() {
		It("should trigger retry when some tiers resolve but others are missing and provider is configured", func() {
			name := "storage-test-partial-tier-retry"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createHubSecret(ctx, name, secretsNamespace)
			createLabeledStorageClass(ctx, name+"-local-sc", name, "local")
			// No SC for "block" tier

			clusterProvider := &mockProvisioningProvider{name: "cluster-storage-mock"}
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, clusterProvider, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.TiersClient = &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return privatev1.StorageTiersListResponse_builder{
						Items: []*privatev1.StorageTier{
							newTestStorageTier("local", "backend-lvms"),
							newTestStorageTier("block", "backend-vast"),
						},
					}.Build(), nil
				},
			}
			r.BackendsClient = &mockStorageBackendsClient{
				getFunc: func(_ context.Context, in *privatev1.StorageBackendsGetRequest, _ ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error) {
					switch in.GetId() {
					case "backend-lvms":
						return newTestStorageBackendGetResponse("lvms"), nil
					case "backend-vast":
						return newTestStorageBackendGetResponse("vast"), nil
					}
					return nil, status.Error(codes.NotFound, "not found")
				},
			}

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			result, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(pollInterval), "should requeue for provisioning retry")

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())

			clusterCond := tenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady)
			Expect(clusterCond).NotTo(BeNil())
			Expect(clusterCond.Status).To(Equal(metav1.ConditionFalse),
				"should be False when tiers are missing; handleCaaSDelete runs before this check so finalizer removal is safe (OSAC-4855)")
			Expect(clusterCond.Message).To(ContainSubstring(`tier "block" has no StorageClass`))

			// Resolved tier should still be present in status
			Expect(tenant.Status.StorageClasses).To(HaveLen(1))
			Expect(tenant.Status.StorageClasses[0].Tier).To(Equal("local"))

			Expect(tenant.Status.ClusterStorage).To(HaveLen(1))
			Expect(tenant.Status.ClusterStorage[0].ClusterName).To(Equal(string(mcmanager.LocalCluster)))
			Expect(tenant.Status.ClusterStorage[0].Ready).To(BeFalse())
			Expect(tenant.Status.ClusterStorage[0].Reason).To(Equal(v1alpha1.TenantReasonNotFound))

			// Provisioning job should be triggered for missing tiers
			Expect(tenant.Status.ClusterStorageJobs).To(HaveLen(1))

			By("triggering a new ClusterStorageProvider job via DesiredConfigVersion when a new OSAC tier is added after an initial job succeeded")
			r.Client = k8sClient
			tenant.Status.ClusterStorageJobs[0].State = v1alpha1.JobStateSucceeded
			Expect(k8sClient.Status().Update(ctx, tenant)).To(Succeed())

			createLabeledStorageClass(ctx, name+"-block-sc", name, "block")
			waitForStorageClassInCache(name + "-block-sc")
			r.TiersClient = &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return privatev1.StorageTiersListResponse_builder{
						Items: []*privatev1.StorageTier{
							newTestStorageTier("local", "backend-lvms"),
							newTestStorageTier("block", "backend-vast"),
							newTestStorageTier("archive", "backend-vast"),
						},
					}.Build(), nil
				},
			}

			result, err = r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(pollInterval))
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			Expect(tenant.Status.ClusterStorageJobs).To(HaveLen(2),
				"DesiredConfigVersion change from new tier must trigger a fresh provision job instead of skipping")
		})

		It("should requeue without creating new jobs when failed job exists and hub Secret appears", func() {
			name := "storage-test-failed-tier-retry"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createHubSecret(ctx, name, secretsNamespace)
			createLabeledStorageClass(ctx, name+"-local-sc", name, "local")
			waitForStorageClassInCache(name + "-local-sc")

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			tenant.Status.ClusterStorageJobs = []v1alpha1.JobStatus{{
				JobID:     "failed-cluster-storage",
				Type:      v1alpha1.JobTypeProvision,
				State:     v1alpha1.JobStateFailed,
				Timestamp: metav1.Now(),
			}}
			Expect(k8sClient.Status().Update(ctx, tenant)).To(Succeed())

			triggers := 0
			clusterProvider := &mockProvisioningProvider{
				name: "cluster-storage-mock",
				triggerProvisionFunc: func(context.Context, client.Object) (*provisioning.ProvisionResult, error) {
					triggers++
					return &provisioning.ProvisionResult{
						JobID:        "replacement-cluster-storage",
						InitialState: v1alpha1.JobStateRunning,
					}, nil
				},
				getProvisionStatusFunc: func(_ context.Context, _ client.Object, jobID string) (provisioning.ProvisionStatus, error) {
					return provisioning.ProvisionStatus{JobID: jobID, State: v1alpha1.JobStateRunning}, nil
				},
			}
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, clusterProvider, pollInterval, provisioning.DefaultMaxJobHistory,
			)
			// Use direct reads to make successive reconciles independent of cache lag.
			r.Client = k8sClient
			r.TiersClient = &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return privatev1.StorageTiersListResponse_builder{
						Items: []*privatev1.StorageTier{
							newTestStorageTier("local", "backend-lvms"),
							newTestStorageTier("block", "backend-vast"),
						},
					}.Build(), nil
				},
			}
			r.BackendsClient = &mockStorageBackendsClient{
				getFunc: func(_ context.Context, in *privatev1.StorageBackendsGetRequest, _ ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error) {
					switch in.GetId() {
					case "backend-lvms":
						return newTestStorageBackendGetResponse("lvms"), nil
					case "backend-vast":
						return newTestStorageBackendGetResponse("vast"), nil
					}
					return nil, status.Error(codes.NotFound, "not found")
				},
			}

			// The failed job cannot be retried while the vast backend is absent.
			result, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())
			Expect(triggers).To(BeZero())

			vastSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
				Name:      name + "-vast",
				Namespace: secretsNamespace,
				Labels:    map[string]string{osacTenantKey: name, osacStorageProviderLabel: "vast"},
			}}
			Expect(k8sClient.Create(ctx, vastSecret)).To(Succeed())
			waitForIndexedClientObject(vastSecret)
			DeferCleanup(func() {
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, vastSecret))).To(Succeed())
			})

			// Backend readiness enables retries: failed job + hubSecretReady →
			// requeue at StatusPollInterval without creating a new job.
			result, err = r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(pollInterval),
				"should requeue at StatusPollInterval, not spawn a new job")
			Expect(triggers).To(BeZero(), "no new provisioning jobs should be triggered")
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			Expect(tenant.GetStatusCondition(v1alpha1.TenantConditionStorageBackendReady).Status).To(Equal(metav1.ConditionTrue))
			Expect(tenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady).Status).To(Equal(metav1.ConditionFalse),
				"should be False when tiers are still missing")

			// Once the missing StorageClass appears, the condition flips to True
			// without needing a new provisioning job.
			createLabeledStorageClass(ctx, name+"-block-sc", name, "block")
			waitForStorageClassInCache(name + "-block-sc")
			_, err = r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			Expect(tenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady).Status).To(Equal(metav1.ConditionTrue))
			Expect(tenant.Status.StorageClasses).To(HaveLen(2))
			Expect(triggers).To(BeZero(), "no provisioning jobs should have been triggered at any point")
		})

		It("should set ClusterStorageReady=True when all defined tiers have StorageClasses and provider is configured", func() {
			name := "storage-test-all-tiers-resolved"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createHubSecret(ctx, name, secretsNamespace)
			createLabeledStorageClass(ctx, name+"-local-sc", name, "local")
			createLabeledStorageClass(ctx, name+"-block-sc", name, "block")

			clusterProvider := &mockProvisioningProvider{name: "cluster-storage-mock"}
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, clusterProvider, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.TiersClient = &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return privatev1.StorageTiersListResponse_builder{
						Items: []*privatev1.StorageTier{
							newTestStorageTier("local", "backend-lvms"),
							newTestStorageTier("block", "backend-vast"),
						},
					}.Build(), nil
				},
			}
			r.BackendsClient = &mockStorageBackendsClient{
				getFunc: func(_ context.Context, in *privatev1.StorageBackendsGetRequest, _ ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error) {
					switch in.GetId() {
					case "backend-lvms":
						return newTestStorageBackendGetResponse("lvms"), nil
					case "backend-vast":
						return newTestStorageBackendGetResponse("vast"), nil
					}
					return nil, status.Error(codes.NotFound, "not found")
				},
			}

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())

			clusterCond := tenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady)
			Expect(clusterCond).NotTo(BeNil())
			Expect(clusterCond.Status).To(Equal(metav1.ConditionTrue),
				"should be True when all defined tiers have StorageClasses")

			Expect(tenant.Status.StorageClasses).To(HaveLen(2))
			Expect(tenant.Status.ClusterStorageJobs).To(BeEmpty(),
				"no provisioning job should be triggered when all tiers are resolved")
		})

		It("should not trigger retry when no tier definitions are available and some SCs exist", func() {
			name := "storage-test-no-tiers-no-retry"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createHubSecret(ctx, name, secretsNamespace)
			createLabeledStorageClass(ctx, name+"-default-sc", name, "default")

			clusterProvider := &mockProvisioningProvider{name: "cluster-storage-mock"}
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, clusterProvider, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			// No TiersClient/BackendsClient: tierDefinitions will be nil (backward compat)

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())

			clusterCond := tenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady)
			Expect(clusterCond).NotTo(BeNil())
			Expect(clusterCond.Status).To(Equal(metav1.ConditionTrue),
				"should be True when SCs exist and no tier definitions (backward compat)")

			Expect(tenant.Status.ClusterStorageJobs).To(BeEmpty())
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
			Expect(backendCond.Message).To(Equal("No storage backend entries are available to evaluate (ready: 0; not ready: 0); legacy tenant-wide Hub Secret check: no Secret found; no fulfillment service connection configured; storage backend status unknown"))
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
			Expect(backendCond.Message).To(Equal("No storage backend entries are available to evaluate (ready: 0; not ready: 0); legacy tenant-wide Hub Secret check: no Secret found; no storage backend registered"))
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
			Expect(cond.Message).To(Equal("No storage backend entries are available to evaluate (ready: 0; not ready: 0); legacy tenant-wide Hub Secret check: no Secret found; storage backend is registered but no AAP is configured to provision the hub Secret for tenant \"storage-test-backend-no-aap\""))
			Expect(tenant.Status.StorageBackendJobs).To(BeEmpty())
		})

		It("should requeue at poll interval when cluster storage job failed and hub Secret exists", func() {
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

			// Second reconcile: failed job found, hub Secret exists → requeue at
			// StatusPollInterval. The job stays terminal; no new job is created.
			result, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(pollInterval),
				"should requeue at StatusPollInterval without spawning a new job")
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

			tenant := &v1alpha1.Tenant{}
			nn := types.NamespacedName{Name: tenantName, Namespace: testNamespace}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			tenant.Status.ClusterStorage = []v1alpha1.ClusterStorageStatus{{
				ClusterName: co.Name,
				Ready:       true,
				Reason:      v1alpha1.TenantReasonFound,
			}}
			Expect(k8sClient.Status().Update(ctx, tenant)).To(Succeed())

			co.Status.Phase = v1alpha1.ClusterOrderPhaseReady
			Expect(k8sClient.Status().Update(ctx, co)).To(Succeed())

			// No cluster provider: VMaaS-only setup with CaaS ClusterOrder present
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval, provisioning.DefaultMaxJobHistory,
			)

			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			// VMaaS StorageClass should still be resolved on the Tenant
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			Expect(tenant.Status.StorageClasses).To(HaveLen(1))
			Expect(tenant.Status.StorageClasses[0].Name).To(Equal(tenantName + "-vmaas-sc"))
			Expect(tenant.Status.ClusterStorage).To(ConsistOf(
				v1alpha1.ClusterStorageStatus{
					ClusterName: co.Name,
					Ready:       true,
					Reason:      v1alpha1.TenantReasonFound,
				},
				v1alpha1.ClusterStorageStatus{
					ClusterName: string(mcmanager.LocalCluster),
					Ready:       true,
					Reason:      v1alpha1.TenantReasonFound,
				},
			))

			clusterCond := tenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady)
			Expect(clusterCond).NotTo(BeNil())
			Expect(clusterCond.Status).To(Equal(metav1.ConditionTrue))
		})
	})

	Context("CaaS: cluster deletion without storage provider (OSAC-4340)", func() {
		// These tests verify that CaaS cluster deletion (finalizer removal)
		// works independently of:
		//   1. ClusterStorageProvider being configured
		//   2. Stage 1 (handleBackendReadiness) returning stop=true
		//
		// The watch engagement options (WithEngageWithLocalCluster/
		// WithEngageWithProviderClusters) on the ClusterOrder Watches call
		// are verified by source inspection — envtest runs against the
		// local cluster only, so it cannot exercise multicluster watch
		// delivery. The functional tests below verify the behavioral fix:
		// once a reconcile is triggered, CaaS deletion runs regardless of
		// provider or Stage 1 state.

		It("should remove the cluster-storage finalizer from a deleting ClusterOrder when no ClusterStorageProvider is configured", func() {
			tenantName := "caas-delete-no-provider"
			createReadyTenantForStorage(ctx, tenantName, testNamespace)

			// No backends, no providers: the environment has no storage infrastructure configured.
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval, provisioning.DefaultMaxJobHistory,
			)

			// Create a ClusterOrder with the cluster-storage finalizer already present
			// (as if a previous reconcile with a provider had added it).
			co := newClusterOrder("caas-del-no-prov-co", testNamespace, map[string]string{
				osacTenantKey: tenantName,
			})
			controllerutil.AddFinalizer(co, clusterStorageFinalizer)
			Expect(k8sClient.Create(ctx, co)).To(Succeed())
			DeferCleanup(func() {
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, co))).To(Succeed())
			})

			// Verify the finalizer is present
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(co), co)).To(Succeed())
			Expect(co.Finalizers).To(ContainElement(clusterStorageFinalizer))

			// Delete the ClusterOrder — the finalizer should block deletion
			Expect(k8sClient.Delete(ctx, co)).To(Succeed())

			// Reconcile the Tenant: handleCaaSDelete should process the
			// deleting ClusterOrder even without a ClusterStorageProvider.
			// The HCP does not exist (kubeconfig==nil), so cleanup is skipped
			// and the finalizer is removed immediately.
			nn := types.NamespacedName{Name: tenantName, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
				g.Expect(err).NotTo(HaveOccurred())

				updatedCO := &v1alpha1.ClusterOrder{}
				err = k8sClient.Get(ctx, client.ObjectKeyFromObject(co), updatedCO)
				g.Expect(client.IgnoreNotFound(err)).To(Succeed())
				if err == nil {
					g.Expect(updatedCO.Finalizers).NotTo(ContainElement(clusterStorageFinalizer),
						"cluster-storage finalizer should be removed even without a ClusterStorageProvider")
				}
			}).Should(Succeed())
		})

		It("should remove the cluster-storage finalizer during Tenant deletion when no ClusterStorageProvider is configured", func() {
			tenantName := "caas-tdel-no-provider"
			createReadyTenantForStorage(ctx, tenantName, testNamespace)

			// No backends, no providers.
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval, provisioning.DefaultMaxJobHistory,
			)

			// First reconcile: adds the storage finalizer to the Tenant
			nn := types.NamespacedName{Name: tenantName, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			// Create a ClusterOrder with the cluster-storage finalizer
			co := newClusterOrder("caas-tdel-co", testNamespace, map[string]string{
				osacTenantKey: tenantName,
			})
			controllerutil.AddFinalizer(co, clusterStorageFinalizer)
			Expect(k8sClient.Create(ctx, co)).To(Succeed())
			DeferCleanup(func() {
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, co))).To(Succeed())
			})

			// Delete the Tenant — handleDelete should clean up CaaS finalizers
			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			Expect(k8sClient.Delete(ctx, tenant)).To(Succeed())

			Eventually(func(g Gomega) {
				_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
				g.Expect(err).NotTo(HaveOccurred())

				// ClusterOrder's cluster-storage finalizer should be removed
				updatedCO := &v1alpha1.ClusterOrder{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(co), updatedCO)).To(Succeed())
				g.Expect(updatedCO.Finalizers).NotTo(ContainElement(clusterStorageFinalizer),
					"cluster-storage finalizer should be removed from ClusterOrder during Tenant deletion")
			}).Should(Succeed())
		})

		It("should remove the cluster-storage finalizer even when Stage 1 returns stop=true (backend registered, no AAP)", func() {
			tenantName := "caas-del-stage1-stop"
			createReadyTenantForStorage(ctx, tenantName, testNamespace)

			// Backend registered but no AAP provider: Stage 1 (handleBackendReadiness)
			// returns stop=true at the "backend registered but no AAP" branch.
			// Before the OSAC-4340 fix, handleCaaSDelete was called AFTER Stage 1,
			// so it was never reached. Now it runs before Stage 1.
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval, provisioning.DefaultMaxJobHistory,
			)
			r.BackendsClient = registeredBackendsClient(1)

			// Create a ClusterOrder with the cluster-storage finalizer
			co := newClusterOrder("caas-del-s1stop-co", testNamespace, map[string]string{
				osacTenantKey: tenantName,
			})
			controllerutil.AddFinalizer(co, clusterStorageFinalizer)
			Expect(k8sClient.Create(ctx, co)).To(Succeed())
			DeferCleanup(func() {
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, co))).To(Succeed())
			})

			// Verify the finalizer is present
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(co), co)).To(Succeed())
			Expect(co.Finalizers).To(ContainElement(clusterStorageFinalizer))

			// Delete the ClusterOrder
			Expect(k8sClient.Delete(ctx, co)).To(Succeed())

			// Reconcile: Stage 1 will return stop=true (backend registered,
			// no AAP), but handleCaaSDelete runs before Stage 1 so the
			// finalizer is removed regardless.
			nn := types.NamespacedName{Name: tenantName, Namespace: testNamespace}
			Eventually(func(g Gomega) {
				_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
				g.Expect(err).NotTo(HaveOccurred())

				updatedCO := &v1alpha1.ClusterOrder{}
				err = k8sClient.Get(ctx, client.ObjectKeyFromObject(co), updatedCO)
				g.Expect(client.IgnoreNotFound(err)).To(Succeed())
				if err == nil {
					g.Expect(updatedCO.Finalizers).NotTo(ContainElement(clusterStorageFinalizer),
						"cluster-storage finalizer should be removed even when Stage 1 returns stop=true")
				}
			}).Should(Succeed())
		})
	})

	Context("CSP-managed storage (pure cspManaged flow)", func() {
		It("should set StorageReady=True, clear stale OSAC status/conditions, and trigger zero AAP jobs when all csp-managed StorageClasses exist", func() {
			name := "csp-pure-all-exist"
			createReadyTenantForStorage(ctx, name, testNamespace)

			// Pre-populate stale OSAC-managed conditions and status fields to verify
			// pure-CSP reconciliation removes them while preserving NamespaceReady.
			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			tenant.SetStatusCondition(v1alpha1.TenantConditionNamespaceReady,
				metav1.ConditionTrue, v1alpha1.TenantReasonFound, "namespace ready")
			tenant.SetStatusCondition(v1alpha1.TenantConditionStorageBackendReady,
				metav1.ConditionFalse, "TierResolutionFailed", "stale backend condition")
			tenant.SetStatusCondition(v1alpha1.TenantConditionClusterStorageReady,
				metav1.ConditionFalse, v1alpha1.TenantReasonNotFound, "stale cluster condition")
			tenant.Status.StorageBackends = []v1alpha1.StorageBackendStatus{{
				Name: "stale-backend", Provider: "vast", Ready: false,
			}}
			tenant.Status.ClusterStorage = []v1alpha1.ClusterStorageStatus{{
				ClusterName: "", Ready: false, Reason: v1alpha1.TenantReasonNotFound,
			}}
			Expect(k8sClient.Status().Update(ctx, tenant)).To(Succeed())

			createLabeledStorageClass(ctx, "csp-pure-gold-sc", cspManagedTenantValue, "csp-pure-gold")
			createLabeledStorageClass(ctx, "csp-pure-silver-sc", cspManagedTenantValue, "csp-pure-silver")
			waitForStorageClassInCache("csp-pure-gold-sc")
			waitForStorageClassInCache("csp-pure-silver-sc")

			backendTriggers := 0
			clusterTriggers := 0
			backendProvider := &mockProvisioningProvider{
				name: "backend-mock",
				triggerProvisionFunc: func(context.Context, client.Object) (*provisioning.ProvisionResult, error) {
					backendTriggers++
					return &provisioning.ProvisionResult{JobID: "unexpected-be", InitialState: v1alpha1.JobStateRunning}, nil
				},
			}
			clusterProvider := &mockProvisioningProvider{
				name: "cluster-mock",
				triggerProvisionFunc: func(context.Context, client.Object) (*provisioning.ProvisionResult, error) {
					clusterTriggers++
					return &provisioning.ProvisionResult{JobID: "unexpected-cs", InitialState: v1alpha1.JobStateRunning}, nil
				},
			}

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				backendProvider, clusterProvider, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.Client = k8sClient
			r.TiersClient = &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return privatev1.StorageTiersListResponse_builder{
						Items: []*privatev1.StorageTier{
							newTestStorageTier("csp-pure-silver", "backend-csp"),
							newTestStorageTier("csp-pure-gold", "backend-csp"),
						},
					}.Build(), nil
				},
			}
			r.BackendsClient = &mockStorageBackendsClient{
				listFunc: func(context.Context, *privatev1.StorageBackendsListRequest, ...grpc.CallOption) (*privatev1.StorageBackendsListResponse, error) {
					return privatev1.StorageBackendsListResponse_builder{
						Total: 1,
						Items: []*privatev1.StorageBackend{
							privatev1.StorageBackend_builder{
								Spec: privatev1.StorageBackendSpec_builder{
									Provider:   "aws-ebs",
									CspManaged: true,
								}.Build(),
							}.Build(),
						},
					}.Build(), nil
				},
				getFunc: func(_ context.Context, in *privatev1.StorageBackendsGetRequest, _ ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error) {
					if in.GetId() == "backend-csp" {
						return newTestCSPManagedBackendGetResponse("aws-ebs"), nil
					}
					return nil, status.Error(codes.NotFound, "not found")
				},
			}

			res, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			Expect(res.RequeueAfter).To(BeZero())

			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())

			cspCond := tenant.GetStatusCondition(v1alpha1.TenantConditionStorageReady)
			Expect(cspCond).NotTo(BeNil())
			Expect(cspCond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cspCond.Reason).To(Equal(v1alpha1.TenantReasonFound))
			Expect(cspCond.Message).To(ContainSubstring(`tier "csp-pure-gold": StorageClass "csp-pure-gold-sc" (csp-managed)`))
			Expect(cspCond.Message).To(ContainSubstring(`tier "csp-pure-silver": StorageClass "csp-pure-silver-sc" (csp-managed)`))

			Expect(tenant.GetStatusCondition(v1alpha1.TenantConditionStorageBackendReady)).To(BeNil())
			Expect(tenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady)).To(BeNil())
			Expect(tenant.GetStatusCondition(v1alpha1.TenantConditionNamespaceReady)).NotTo(BeNil(),
				"non-storage conditions must be preserved")
			Expect(tenant.Status.StorageBackends).To(BeEmpty())
			Expect(tenant.Status.ClusterStorage).To(BeEmpty())

			Expect(tenant.Status.StorageClasses).To(Equal([]v1alpha1.ResolvedStorageClass{
				{Name: "csp-pure-gold-sc", Tier: "csp-pure-gold"},
				{Name: "csp-pure-silver-sc", Tier: "csp-pure-silver"},
			}))
			Expect(backendTriggers).To(BeZero())
			Expect(clusterTriggers).To(BeZero())

			By("keeping StorageReady=True (one-time transition) while refreshing status.storageClasses if a tier lacks a StorageClass after initial onboarding")
			r.TiersClient = &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return privatev1.StorageTiersListResponse_builder{
						Items: []*privatev1.StorageTier{
							newTestStorageTier("csp-pure-gold", "backend-csp"),
							newTestStorageTier("csp-pure-unprovisioned", "backend-csp"),
						},
					}.Build(), nil
				},
			}
			res, err = r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			Expect(res.RequeueAfter).To(BeZero())
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			cspCondAfter := tenant.GetStatusCondition(v1alpha1.TenantConditionStorageReady)
			Expect(cspCondAfter).NotTo(BeNil())
			Expect(cspCondAfter.Status).To(Equal(metav1.ConditionTrue),
				"StorageReady is a one-time transition and must remain True once completed")
			Expect(tenant.Status.StorageClasses).To(Equal([]v1alpha1.ResolvedStorageClass{
				{Name: "csp-pure-gold-sc", Tier: "csp-pure-gold"},
			}), "status.storageClasses must refresh to reflect currently resolved CSP tiers")
		})

		It("should set StorageReady=False with NotFound and emit MissingStorageTier events when 1-of-2 or 0-of-2 csp-managed StorageClasses are missing", func() {
			By("0-of-2 missing case")
			nameZero := "csp-pure-missing-all"
			createReadyTenantForStorage(ctx, nameZero, testNamespace)

			fakeRecorder := events.NewFakeRecorder(100)
			backendTriggers := 0
			clusterTriggers := 0
			backendProvider := &mockProvisioningProvider{
				name: "backend-mock",
				triggerProvisionFunc: func(context.Context, client.Object) (*provisioning.ProvisionResult, error) {
					backendTriggers++
					return &provisioning.ProvisionResult{JobID: "unexpected", InitialState: v1alpha1.JobStateRunning}, nil
				},
			}
			clusterProvider := &mockProvisioningProvider{
				name: "cluster-mock",
				triggerProvisionFunc: func(context.Context, client.Object) (*provisioning.ProvisionResult, error) {
					clusterTriggers++
					return &provisioning.ProvisionResult{JobID: "unexpected", InitialState: v1alpha1.JobStateRunning}, nil
				},
			}

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				backendProvider, clusterProvider, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.Client = k8sClient
			r.Recorder = fakeRecorder
			r.TiersClient = &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return privatev1.StorageTiersListResponse_builder{
						Items: []*privatev1.StorageTier{
							newTestStorageTier("csp-miss-gold", "backend-csp"),
							newTestStorageTier("csp-miss-silver", "backend-csp"),
						},
					}.Build(), nil
				},
			}
			r.BackendsClient = &mockStorageBackendsClient{
				getFunc: func(_ context.Context, _ *privatev1.StorageBackendsGetRequest, _ ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error) {
					return newTestCSPManagedBackendGetResponse("aws-ebs"), nil
				},
			}

			nnZero := types.NamespacedName{Name: nameZero, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nnZero))
			Expect(err).NotTo(HaveOccurred())

			tenantZero := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nnZero, tenantZero)).To(Succeed())
			cspCond := tenantZero.GetStatusCondition(v1alpha1.TenantConditionStorageReady)
			Expect(cspCond).NotTo(BeNil())
			Expect(cspCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cspCond.Reason).To(Equal(v1alpha1.TenantReasonNotFound))
			Expect(cspCond.Message).To(ContainSubstring(`tier "csp-miss-gold" has no StorageClass`))
			Expect(cspCond.Message).To(ContainSubstring(`tier "csp-miss-silver" has no StorageClass`))
			Expect(tenantZero.Status.StorageClasses).To(BeNil())
			Expect(tenantZero.GetStatusCondition(v1alpha1.TenantConditionStorageBackendReady)).To(BeNil())
			Expect(tenantZero.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady)).To(BeNil())

			var zeroEvents []string
			for len(fakeRecorder.Events) > 0 {
				zeroEvents = append(zeroEvents, <-fakeRecorder.Events)
			}
			Expect(zeroEvents).To(HaveLen(2))
			Expect(zeroEvents[0]).To(ContainSubstring(eventReasonMissingStorageTier))
			Expect(zeroEvents[0]).To(ContainSubstring(`tier "csp-miss-gold" has no StorageClass`))
			Expect(zeroEvents[1]).To(ContainSubstring(eventReasonMissingStorageTier))
			Expect(zeroEvents[1]).To(ContainSubstring(`tier "csp-miss-silver" has no StorageClass`))

			By("1-of-2 partial missing case")
			createLabeledStorageClass(ctx, "csp-miss-gold-sc", cspManagedTenantValue, "csp-miss-gold")
			waitForStorageClassInCache("csp-miss-gold-sc")

			namePartial := "csp-pure-missing-one"
			createReadyTenantForStorage(ctx, namePartial, testNamespace)
			nnPartial := types.NamespacedName{Name: namePartial, Namespace: testNamespace}

			_, err = r.Reconcile(ctx, storageReconcileRequest(nnPartial))
			Expect(err).NotTo(HaveOccurred())

			tenantPartial := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nnPartial, tenantPartial)).To(Succeed())
			cspCondPartial := tenantPartial.GetStatusCondition(v1alpha1.TenantConditionStorageReady)
			Expect(cspCondPartial).NotTo(BeNil())
			Expect(cspCondPartial.Status).To(Equal(metav1.ConditionFalse))
			Expect(cspCondPartial.Reason).To(Equal(v1alpha1.TenantReasonNotFound))
			Expect(cspCondPartial.Message).To(ContainSubstring(`tier "csp-miss-gold": StorageClass "csp-miss-gold-sc" (csp-managed)`))
			Expect(cspCondPartial.Message).To(ContainSubstring(`tier "csp-miss-silver" has no StorageClass`))
			Expect(tenantPartial.Status.StorageClasses).To(Equal([]v1alpha1.ResolvedStorageClass{
				{Name: "csp-miss-gold-sc", Tier: "csp-miss-gold"},
			}))
			Expect(tenantPartial.GetStatusCondition(v1alpha1.TenantConditionStorageBackendReady)).To(BeNil())
			Expect(tenantPartial.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady)).To(BeNil())
			Expect(backendTriggers).To(BeZero())
			Expect(clusterTriggers).To(BeZero())

			var partialEvents []string
			for len(fakeRecorder.Events) > 0 {
				partialEvents = append(partialEvents, <-fakeRecorder.Events)
			}
			Expect(partialEvents).To(HaveLen(1))
			Expect(partialEvents[0]).To(ContainSubstring(eventReasonMissingStorageTier))
			Expect(partialEvents[0]).To(ContainSubstring(`tier "csp-miss-silver" has no StorageClass`))
		})

		It("should set StorageReady=False with MultipleFound and emit DuplicateStorageClass event without MissingStorageTier when duplicate csp-managed SCs exist", func() {
			name := "csp-pure-duplicate-sc"
			createReadyTenantForStorage(ctx, name, testNamespace)

			createLabeledStorageClass(ctx, "csp-dup-gold-sc-1", cspManagedTenantValue, "csp-dup-gold")
			createLabeledStorageClass(ctx, "csp-dup-gold-sc-2", cspManagedTenantValue, "csp-dup-gold")
			waitForStorageClassInCache("csp-dup-gold-sc-1")
			waitForStorageClassInCache("csp-dup-gold-sc-2")

			fakeRecorder := events.NewFakeRecorder(100)
			backendTriggers := 0
			clusterTriggers := 0
			backendProvider := &mockProvisioningProvider{
				name: "backend-mock",
				triggerProvisionFunc: func(context.Context, client.Object) (*provisioning.ProvisionResult, error) {
					backendTriggers++
					return &provisioning.ProvisionResult{JobID: "unexpected", InitialState: v1alpha1.JobStateRunning}, nil
				},
			}
			clusterProvider := &mockProvisioningProvider{
				name: "cluster-mock",
				triggerProvisionFunc: func(context.Context, client.Object) (*provisioning.ProvisionResult, error) {
					clusterTriggers++
					return &provisioning.ProvisionResult{JobID: "unexpected", InitialState: v1alpha1.JobStateRunning}, nil
				},
			}

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				backendProvider, clusterProvider, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.Client = k8sClient
			r.Recorder = fakeRecorder
			r.TiersClient = &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return privatev1.StorageTiersListResponse_builder{
						Items: []*privatev1.StorageTier{
							newTestStorageTier("csp-dup-gold", "backend-csp"),
						},
					}.Build(), nil
				},
			}
			r.BackendsClient = &mockStorageBackendsClient{
				getFunc: func(_ context.Context, _ *privatev1.StorageBackendsGetRequest, _ ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error) {
					return newTestCSPManagedBackendGetResponse("aws-ebs"), nil
				},
			}

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())

			cspCond := tenant.GetStatusCondition(v1alpha1.TenantConditionStorageReady)
			Expect(cspCond).NotTo(BeNil())
			Expect(cspCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cspCond.Reason).To(Equal(v1alpha1.TenantReasonMultipleFound))
			Expect(cspCond.Message).To(ContainSubstring(`tier "csp-dup-gold": multiple csp-managed StorageClasses`))
			Expect(tenant.Status.StorageClasses).To(BeNil())
			Expect(backendTriggers).To(BeZero())
			Expect(clusterTriggers).To(BeZero())

			var recordedEvents []string
			for len(fakeRecorder.Events) > 0 {
				recordedEvents = append(recordedEvents, <-fakeRecorder.Events)
			}
			Expect(recordedEvents).To(HaveLen(1))
			Expect(recordedEvents[0]).To(ContainSubstring(eventReasonDuplicateStorageClass))
			Expect(recordedEvents[0]).NotTo(ContainSubstring(eventReasonMissingStorageTier))
		})

		It("should set StorageReady=False with NotFound when one csp-managed tier has duplicate StorageClasses and another is missing", func() {
			name := "csp-pure-dup-and-missing"
			createReadyTenantForStorage(ctx, name, testNamespace)

			createLabeledStorageClass(ctx, "csp-combo-dup-sc-1", cspManagedTenantValue, "csp-combo-dup")
			createLabeledStorageClass(ctx, "csp-combo-dup-sc-2", cspManagedTenantValue, "csp-combo-dup")
			waitForStorageClassInCache("csp-combo-dup-sc-1")
			waitForStorageClassInCache("csp-combo-dup-sc-2")

			fakeRecorder := events.NewFakeRecorder(100)
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.Client = k8sClient
			r.Recorder = fakeRecorder
			r.TiersClient = &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return privatev1.StorageTiersListResponse_builder{
						Items: []*privatev1.StorageTier{
							newTestStorageTier("csp-combo-dup", "backend-csp"),
							newTestStorageTier("csp-combo-missing", "backend-csp"),
						},
					}.Build(), nil
				},
			}
			r.BackendsClient = &mockStorageBackendsClient{
				getFunc: func(_ context.Context, _ *privatev1.StorageBackendsGetRequest, _ ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error) {
					return newTestCSPManagedBackendGetResponse("aws-ebs"), nil
				},
			}

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())

			cspCond := tenant.GetStatusCondition(v1alpha1.TenantConditionStorageReady)
			Expect(cspCond).NotTo(BeNil())
			Expect(cspCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cspCond.Reason).To(Equal(v1alpha1.TenantReasonNotFound),
				"MultipleFound applies only when len(missing) == 0; combined duplicate + missing must report NotFound")
			Expect(cspCond.Message).To(ContainSubstring(`tier "csp-combo-dup": multiple csp-managed StorageClasses`))
			Expect(cspCond.Message).To(ContainSubstring(`tier "csp-combo-missing" has no StorageClass`))
			Expect(cspCond.Message).NotTo(ContainSubstring(`tier "csp-combo-dup" has no StorageClass`))

			var recordedEvents []string
			for len(fakeRecorder.Events) > 0 {
				recordedEvents = append(recordedEvents, <-fakeRecorder.Events)
			}
			Expect(recordedEvents).To(HaveLen(2))
			Expect(recordedEvents[0]).To(ContainSubstring(eventReasonDuplicateStorageClass))
			Expect(recordedEvents[0]).To(ContainSubstring(`tier "csp-combo-dup": multiple csp-managed StorageClasses [csp-combo-dup-sc-1, csp-combo-dup-sc-2]`))
			Expect(recordedEvents[1]).To(ContainSubstring(eventReasonMissingStorageTier))
			Expect(recordedEvents[1]).To(ContainSubstring(`tier "csp-combo-missing" has no StorageClass`))
		})

		It("should ignore csp-managed StorageClasses whose tier is not in the configured cspTiers list and give cspResolved precedence in mergeResolvedStorageClasses on tier collision", func() {
			name := "csp-pure-ignore-unconfigured-tier"
			createReadyTenantForStorage(ctx, name, testNamespace)

			createLabeledStorageClass(ctx, "csp-cfg-gold-sc", cspManagedTenantValue, "csp-cfg-gold")
			createLabeledStorageClass(ctx, "csp-uncfg-extra-sc", cspManagedTenantValue, "csp-uncfg-extra")
			waitForStorageClassInCache("csp-cfg-gold-sc")
			waitForStorageClassInCache("csp-uncfg-extra-sc")

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.Client = k8sClient
			r.TiersClient = &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return privatev1.StorageTiersListResponse_builder{
						Items: []*privatev1.StorageTier{
							newTestStorageTier("csp-cfg-gold", "backend-csp"),
						},
					}.Build(), nil
				},
			}
			r.BackendsClient = &mockStorageBackendsClient{
				getFunc: func(_ context.Context, _ *privatev1.StorageBackendsGetRequest, _ ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error) {
					return newTestCSPManagedBackendGetResponse("aws-ebs"), nil
				},
			}

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			cspCond := tenant.GetStatusCondition(v1alpha1.TenantConditionStorageReady)
			Expect(cspCond).NotTo(BeNil())
			Expect(cspCond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cspCond.Message).NotTo(ContainSubstring("csp-uncfg-extra"))
			Expect(tenant.Status.StorageClasses).To(Equal([]v1alpha1.ResolvedStorageClass{
				{Name: "csp-cfg-gold-sc", Tier: "csp-cfg-gold"},
			}))

			// Verify mergeResolvedStorageClasses deduplicates overlapping Tier names with cspResolved taking precedence.
			merged := mergeResolvedStorageClasses(
				[]v1alpha1.ResolvedStorageClass{
					{Name: "csp-shared-sc", Tier: "shared"},
					{Name: "csp-gold-sc", Tier: "gold"},
				},
				[]v1alpha1.ResolvedStorageClass{
					{Name: "osac-shared-sc", Tier: "shared"},
					{Name: "osac-fast-sc", Tier: "fast"},
				},
			)
			Expect(merged).To(Equal([]v1alpha1.ResolvedStorageClass{
				{Name: "osac-fast-sc", Tier: "fast"},
				{Name: "csp-gold-sc", Tier: "gold"},
				{Name: "csp-shared-sc", Tier: "shared"},
			}))
			Expect(mergeResolvedStorageClasses(nil, nil)).To(BeNil())
		})

		It("should fan out mapStorageClassToTenant across all Tenants in r.tenantNamespace for osac.openshift.io/tenant=csp-managed", func() {
			fanoutNS := "csp-fanout-ns"
			otherNS := "csp-other-ns"
			for _, nsName := range []string{fanoutNS, otherNS} {
				ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: nsName}}
				if err := k8sClient.Create(ctx, ns); err != nil {
					Expect(client.IgnoreAlreadyExists(err)).To(Succeed())
				}
			}

			createReadyTenantForStorage(ctx, "fanout-tenant-1", fanoutNS)
			createReadyTenantForStorage(ctx, "fanout-tenant-2", fanoutNS)
			createReadyTenantForStorage(ctx, "other-ns-tenant", otherNS)

			r := NewStorageReconciler(
				testMcManager, fanoutNS, mcmanager.LocalCluster,
				nil, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.Client = k8sClient

			sc := &storagev1.StorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "csp-fanout-sc",
					Labels: map[string]string{
						osacTenantKey:        cspManagedTenantValue,
						osacStorageTierLabel: "csp-fanout-tier",
					},
				},
				Provisioner: "kubernetes.io/no-provisioner",
			}

			requests := r.mapStorageClassToTenant(ctx, sc)
			Expect(requests).To(ConsistOf(
				reconcile.Request{NamespacedName: types.NamespacedName{Name: "fanout-tenant-1", Namespace: fanoutNS}},
				reconcile.Request{NamespacedName: types.NamespacedName{Name: "fanout-tenant-2", Namespace: fanoutNS}},
			))
		})

		It("should remove storage finalizer immediately on handleDelete without triggering deprovision jobs for pure-CSP and deleted-CSP-tier tenants", func() {
			deprovTriggers := 0
			backendProvider := &mockProvisioningProvider{
				name: "backend-mock",
				triggerDeprovisionFunc: func(context.Context, client.Object, []v1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
					deprovTriggers++
					return &provisioning.DeprovisionResult{Action: provisioning.DeprovisionTriggered, JobID: "unexpected-be-del"}, nil
				},
			}
			clusterProvider := &mockProvisioningProvider{
				name: "cluster-mock",
				triggerDeprovisionFunc: func(context.Context, client.Object, []v1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
					deprovTriggers++
					return &provisioning.DeprovisionResult{Action: provisioning.DeprovisionTriggered, JobID: "unexpected-cs-del"}, nil
				},
			}

			By("pure cspManaged tenant deletion")
			namePure := "csp-delete-pure"
			createReadyTenantForStorage(ctx, namePure, testNamespace)
			createLabeledStorageClass(ctx, "csp-del-gold-sc", cspManagedTenantValue, "csp-del-gold")
			waitForStorageClassInCache("csp-del-gold-sc")

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				backendProvider, clusterProvider, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.Client = k8sClient
			r.TiersClient = &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return privatev1.StorageTiersListResponse_builder{
						Items: []*privatev1.StorageTier{
							newTestStorageTier("csp-del-gold", "backend-csp"),
						},
					}.Build(), nil
				},
			}
			r.BackendsClient = &mockStorageBackendsClient{
				getFunc: func(_ context.Context, _ *privatev1.StorageBackendsGetRequest, _ ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error) {
					return newTestCSPManagedBackendGetResponse("aws-ebs"), nil
				},
			}

			nnPure := types.NamespacedName{Name: namePure, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nnPure))
			Expect(err).NotTo(HaveOccurred())

			tenantPure := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nnPure, tenantPure)).To(Succeed())
			Expect(tenantPure.Finalizers).To(ContainElement(storageFinalizer))

			Expect(k8sClient.Delete(ctx, tenantPure)).To(Succeed())
			_, err = r.Reconcile(ctx, storageReconcileRequest(nnPure))
			Expect(err).NotTo(HaveOccurred())
			Expect(deprovTriggers).To(BeZero())

			err = k8sClient.Get(ctx, nnPure, tenantPure)
			if err == nil {
				Expect(tenantPure.Finalizers).NotTo(ContainElement(storageFinalizer))
			} else {
				Expect(client.IgnoreNotFound(err)).To(Succeed())
			}

			By("tenant deletion after cspManaged tiers were already deleted (0 tiers returned)")
			nameZeroTiers := "csp-delete-zero-tiers"
			createReadyTenantForStorage(ctx, nameZeroTiers, testNamespace)
			nnZeroTiers := types.NamespacedName{Name: nameZeroTiers, Namespace: testNamespace}
			_, err = r.Reconcile(ctx, storageReconcileRequest(nnZeroTiers))
			Expect(err).NotTo(HaveOccurred())

			// Now simulate all cspManaged tiers having been deleted before Tenant deletion.
			r.TiersClient = &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return &privatev1.StorageTiersListResponse{}, nil
				},
			}
			tenantZeroTiers := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nnZeroTiers, tenantZeroTiers)).To(Succeed())
			Expect(k8sClient.Delete(ctx, tenantZeroTiers)).To(Succeed())

			_, err = r.Reconcile(ctx, storageReconcileRequest(nnZeroTiers))
			Expect(err).NotTo(HaveOccurred())
			Expect(deprovTriggers).To(BeZero())

			err = k8sClient.Get(ctx, nnZeroTiers, tenantZeroTiers)
			if err == nil {
				Expect(tenantZeroTiers.Finalizers).NotTo(ContainElement(storageFinalizer))
			} else {
				Expect(client.IgnoreNotFound(err)).To(Succeed())
			}

			By("not skipping deprovisioning when an OSAC-managed backend is still registered or a tier was skipped")
			nameOSACBackendRemains := "csp-delete-osac-backend-remains"
			createReadyTenantForStorage(ctx, nameOSACBackendRemains, testNamespace)
			nnRemains := types.NamespacedName{Name: nameOSACBackendRemains, Namespace: testNamespace}
			_, err = r.Reconcile(ctx, storageReconcileRequest(nnRemains))
			Expect(err).NotTo(HaveOccurred())

			// Simulate 0 tiers remaining, but an OSAC-managed backend (cspManaged: false) is still registered.
			r.BackendsClient = &mockStorageBackendsClient{
				listFunc: func(context.Context, *privatev1.StorageBackendsListRequest, ...grpc.CallOption) (*privatev1.StorageBackendsListResponse, error) {
					return privatev1.StorageBackendsListResponse_builder{
						Total: 1,
						Items: []*privatev1.StorageBackend{
							privatev1.StorageBackend_builder{
								Spec: privatev1.StorageBackendSpec_builder{
									Provider:   "vast",
									CspManaged: false,
								}.Build(),
							}.Build(),
						},
					}.Build(), nil
				},
			}
			tenantRemains := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nnRemains, tenantRemains)).To(Succeed())
			Expect(k8sClient.Delete(ctx, tenantRemains)).To(Succeed())

			_, err = r.Reconcile(ctx, storageReconcileRequest(nnRemains))
			Expect(err).NotTo(HaveOccurred())
			Expect(deprovTriggers).To(BeNumerically(">", 0),
				"must not skip deprovisioning when an OSAC-managed backend is still registered")

			By("not skipping deprovisioning when a tier was skipped by resolveTierDefinitions due to a missing backend secret")
			deprovTriggers = 0
			nameSkippedTier := "csp-delete-skipped-tier"
			createReadyTenantForStorage(ctx, nameSkippedTier, testNamespace)
			nnSkipped := types.NamespacedName{Name: nameSkippedTier, Namespace: testNamespace}
			r.BackendsClient = &mockStorageBackendsClient{
				getFunc: func(_ context.Context, _ *privatev1.StorageBackendsGetRequest, _ ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error) {
					return newTestCSPManagedBackendGetResponse("aws-ebs"), nil
				},
			}
			_, err = r.Reconcile(ctx, storageReconcileRequest(nnSkipped))
			Expect(err).NotTo(HaveOccurred())

			// Now return a tier whose backend is NotFound (skipped during resolution -> 0 osacTiers, skippedTiers=true).
			r.TiersClient = &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return privatev1.StorageTiersListResponse_builder{
						Items: []*privatev1.StorageTier{
							newTestStorageTier("osac-skipped-tier", "backend-missing"),
						},
					}.Build(), nil
				},
			}
			r.BackendsClient = &mockStorageBackendsClient{
				getFunc: func(_ context.Context, _ *privatev1.StorageBackendsGetRequest, _ ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error) {
					return nil, status.Error(codes.NotFound, "backend not found")
				},
			}
			tenantSkipped := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nnSkipped, tenantSkipped)).To(Succeed())
			Expect(k8sClient.Delete(ctx, tenantSkipped)).To(Succeed())

			_, err = r.Reconcile(ctx, storageReconcileRequest(nnSkipped))
			Expect(err).NotTo(HaveOccurred())
			Expect(deprovTriggers).To(BeNumerically(">", 0),
				"must not skip deprovisioning when a tier was skipped during resolution")
		})

		It("should fail closed without querying local StorageClasses when r.targetCluster is remote and not registered", func() {
			name := "csp-remote-fail-closed"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createLabeledStorageClass(ctx, "csp-remote-local-sc", cspManagedTenantValue, "csp-remote-gold")
			waitForStorageClassInCache("csp-remote-local-sc")

			r := NewStorageReconciler(
				testMcManager, testNamespace, "remote",
				nil, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.Client = k8sClient
			r.TiersClient = &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return privatev1.StorageTiersListResponse_builder{
						Items: []*privatev1.StorageTier{
							newTestStorageTier("csp-remote-gold", "backend-csp"),
						},
					}.Build(), nil
				},
			}
			r.BackendsClient = &mockStorageBackendsClient{
				getFunc: func(_ context.Context, _ *privatev1.StorageBackendsGetRequest, _ ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error) {
					return newTestCSPManagedBackendGetResponse("aws-ebs"), nil
				},
			}

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).To(HaveOccurred())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			Expect(tenant.GetStatusCondition(v1alpha1.TenantConditionStorageReady)).To(BeNil())
			Expect(tenant.Status.StorageClasses).To(BeEmpty())
		})

		It("should return false from backendRegistered and not trigger BackendProvider when only cspManaged backends are registered and no tiers exist yet", func() {
			name := "csp-pure-backend-only-no-tiers"
			createReadyTenantForStorage(ctx, name, testNamespace)

			backendTriggers := 0
			backendProvider := &mockProvisioningProvider{
				name: "backend-mock",
				triggerProvisionFunc: func(context.Context, client.Object) (*provisioning.ProvisionResult, error) {
					backendTriggers++
					return &provisioning.ProvisionResult{JobID: "unexpected-be", InitialState: v1alpha1.JobStateRunning}, nil
				},
			}

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				backendProvider, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.Client = k8sClient
			r.TiersClient = &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return &privatev1.StorageTiersListResponse{}, nil
				},
			}
			r.BackendsClient = &mockStorageBackendsClient{
				listFunc: func(context.Context, *privatev1.StorageBackendsListRequest, ...grpc.CallOption) (*privatev1.StorageBackendsListResponse, error) {
					return privatev1.StorageBackendsListResponse_builder{
						Total: 1,
						Items: []*privatev1.StorageBackend{
							privatev1.StorageBackend_builder{
								Spec: privatev1.StorageBackendSpec_builder{
									Provider:   "aws-ebs",
									CspManaged: true,
								}.Build(),
							}.Build(),
						},
					}.Build(), nil
				},
			}

			registered, err := r.backendRegistered(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(registered).To(BeFalse())

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err = r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			Expect(backendTriggers).To(BeZero())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			backendCond := tenant.GetStatusCondition(v1alpha1.TenantConditionStorageBackendReady)
			Expect(backendCond).NotTo(BeNil())
			Expect(backendCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(backendCond.Reason).To(Equal(v1alpha1.TenantReasonNoProvider))
			Expect(backendCond.Message).To(ContainSubstring("no storage backend registered"))
		})

		It("should preserve status.storageClasses and StorageReady=True when tier resolution fails on an already-onboarded pure-CSP tenant", func() {
			name := "csp-pure-tier-err-preserves-status"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createLabeledStorageClass(ctx, "csp-preserve-gold-sc", cspManagedTenantValue, "csp-preserve-gold")
			waitForStorageClassInCache("csp-preserve-gold-sc")

			failTiers := false
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.Client = k8sClient
			r.TiersClient = &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					if failTiers {
						return nil, status.Error(codes.Unavailable, "fulfillment service unreachable")
					}
					return privatev1.StorageTiersListResponse_builder{
						Items: []*privatev1.StorageTier{
							newTestStorageTier("csp-preserve-gold", "backend-csp"),
						},
					}.Build(), nil
				},
			}
			r.BackendsClient = &mockStorageBackendsClient{
				getFunc: func(_ context.Context, _ *privatev1.StorageBackendsGetRequest, _ ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error) {
					return newTestCSPManagedBackendGetResponse("aws-ebs"), nil
				},
			}

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			Expect(tenant.GetStatusCondition(v1alpha1.TenantConditionStorageReady)).NotTo(BeNil())
			Expect(tenant.GetStatusCondition(v1alpha1.TenantConditionStorageReady).Status).To(Equal(metav1.ConditionTrue))
			Expect(tenant.Status.StorageClasses).To(Equal([]v1alpha1.ResolvedStorageClass{
				{Name: "csp-preserve-gold-sc", Tier: "csp-preserve-gold"},
			}))

			// Simulate transient Tier API outage on subsequent reconcile.
			failTiers = true
			res, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			Expect(res.RequeueAfter).To(Equal(pollInterval))

			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			cspCond := tenant.GetStatusCondition(v1alpha1.TenantConditionStorageReady)
			Expect(cspCond).NotTo(BeNil(), "existing StorageReady condition must not be wiped on transient tier error")
			Expect(cspCond.Status).To(Equal(metav1.ConditionTrue))
			Expect(tenant.Status.StorageClasses).To(Equal([]v1alpha1.ResolvedStorageClass{
				{Name: "csp-preserve-gold-sc", Tier: "csp-preserve-gold"},
			}), "existing status.storageClasses must not be wiped on transient tier error")
			backendCond := tenant.GetStatusCondition(v1alpha1.TenantConditionStorageBackendReady)
			Expect(backendCond).NotTo(BeNil())
			Expect(backendCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(backendCond.Reason).To(Equal("TierResolutionFailed"))
		})
	})

	Context("Comingled storage (cspManaged + OSAC-managed tiers)", func() {
		configureComingledClients := func(r *StorageReconciler, cspTierName, osacTierName string) {
			r.TiersClient = &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return privatev1.StorageTiersListResponse_builder{
						Items: []*privatev1.StorageTier{
							newTestStorageTier(cspTierName, "backend-csp"),
							newTestStorageTier(osacTierName, "backend-vast"),
						},
					}.Build(), nil
				},
			}
			r.BackendsClient = &mockStorageBackendsClient{
				listFunc: func(context.Context, *privatev1.StorageBackendsListRequest, ...grpc.CallOption) (*privatev1.StorageBackendsListResponse, error) {
					return privatev1.StorageBackendsListResponse_builder{
						Total: 2,
						Items: []*privatev1.StorageBackend{
							privatev1.StorageBackend_builder{
								Spec: privatev1.StorageBackendSpec_builder{
									Provider:   "aws-ebs",
									CspManaged: true,
								}.Build(),
							}.Build(),
							privatev1.StorageBackend_builder{
								Spec: privatev1.StorageBackendSpec_builder{
									Provider:   "vast",
									CspManaged: false,
								}.Build(),
							}.Build(),
						},
					}.Build(), nil
				},
				getFunc: func(_ context.Context, in *privatev1.StorageBackendsGetRequest, _ ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error) {
					switch in.GetId() {
					case "backend-csp":
						return newTestCSPManagedBackendGetResponse("aws-ebs"), nil
					case "backend-vast":
						return newTestStorageBackendGetResponse("vast"), nil
					default:
						return nil, status.Error(codes.NotFound, "not found")
					}
				},
			}
		}

		It("should complete cspManaged onboarding even when Stage 1 stops early to provision OSAC hub Secret", func() {
			name := "comingled-s1-early-stop"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createLabeledStorageClass(ctx, "csp-gold-s1-sc", cspManagedTenantValue, "csp-gold-s1")
			waitForStorageClassInCache("csp-gold-s1-sc")

			var capturedTiers []provisioning.TierDefinition
			var capturedConns map[string]provisioning.BackendConnection
			backendTriggers := 0
			backendProvider := &mockProvisioningProvider{
				name: "backend-mock",
				triggerProvisionFunc: func(pCtx context.Context, _ client.Object) (*provisioning.ProvisionResult, error) {
					backendTriggers++
					capturedTiers = provisioning.StorageTierDefinitionsFromContext(pCtx)
					capturedConns = provisioning.StorageBackendConnectionsFromContext(pCtx)
					return &provisioning.ProvisionResult{
						JobID:        "be-job-s1",
						InitialState: v1alpha1.JobStateRunning,
					}, nil
				},
			}

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				backendProvider, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.Client = k8sClient
			configureComingledClients(r, "csp-gold-s1", "osac-fast")

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			res, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			Expect(res.RequeueAfter).To(Equal(pollInterval))

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())

			cspCond := tenant.GetStatusCondition(v1alpha1.TenantConditionStorageReady)
			Expect(cspCond).NotTo(BeNil())
			Expect(cspCond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cspCond.Reason).To(Equal(v1alpha1.TenantReasonFound))

			backendCond := tenant.GetStatusCondition(v1alpha1.TenantConditionStorageBackendReady)
			Expect(backendCond).NotTo(BeNil())
			Expect(backendCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(backendCond.Reason).To(Equal(v1alpha1.TenantReasonNotFound))

			Expect(tenant.Status.StorageClasses).To(Equal([]v1alpha1.ResolvedStorageClass{
				{Name: "csp-gold-s1-sc", Tier: "csp-gold-s1"},
			}))

			Expect(backendTriggers).To(Equal(1))
			Expect(capturedTiers).To(HaveLen(1))
			Expect(capturedTiers[0].Name).To(Equal("osac-fast"))
			Expect(capturedConns).To(HaveKey("backend-vast"))
			Expect(capturedConns).NotTo(HaveKey("backend-csp"))
		})

		It("should preserve previously resolved OSAC StorageClasses when Stage 1 stops early on an already-onboarded comingled Tenant", func() {
			name := "comingled-s1-preserve-osac"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createLabeledStorageClass(ctx, "csp-gold-pres-sc", cspManagedTenantValue, "csp-gold-pres")
			waitForStorageClassInCache("csp-gold-pres-sc")

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			tenant.Status.StorageClasses = []v1alpha1.ResolvedStorageClass{
				{Name: "csp-gold-pres-sc", Tier: "csp-gold-pres"},
				{Name: name + "-osac-fast-sc", Tier: "osac-fast"},
			}
			Expect(k8sClient.Status().Update(ctx, tenant)).To(Succeed())

			backendProvider := &mockProvisioningProvider{
				name: "backend-mock",
				triggerProvisionFunc: func(context.Context, client.Object) (*provisioning.ProvisionResult, error) {
					return &provisioning.ProvisionResult{
						JobID:        "be-job-preserve",
						InitialState: v1alpha1.JobStateRunning,
					}, nil
				},
			}

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				backendProvider, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.Client = k8sClient
			configureComingledClients(r, "csp-gold-pres", "osac-fast")

			res, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			Expect(res.RequeueAfter).To(Equal(pollInterval))

			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			Expect(tenant.Status.StorageClasses).To(Equal([]v1alpha1.ResolvedStorageClass{
				{Name: "csp-gold-pres-sc", Tier: "csp-gold-pres"},
				{Name: name + "-osac-fast-sc", Tier: "osac-fast"},
			}), "Stage 1 early stop must not clobber existing OSAC-managed StorageClasses on a comingled Tenant")
		})

		It("should preserve cspResolved in status.storageClasses and isolate ClusterStorageProvider ctx when OSAC StorageClass is missing in Stage 2", func() {
			name := "comingled-s2-missing-osac"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createLabeledStorageClass(ctx, "csp-gold-s2-sc", cspManagedTenantValue, "csp-gold-s2")
			waitForStorageClassInCache("csp-gold-s2-sc")

			vastSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("vast-tenant-config-%s", name),
					Namespace: secretsNamespace,
					Labels: map[string]string{
						osacTenantKey:            name,
						osacStorageProviderLabel: "vast",
					},
				},
				StringData: map[string]string{"provider": "vast"},
			}
			Expect(k8sClient.Create(ctx, vastSecret)).To(Succeed())
			waitForIndexedClientObject(vastSecret)
			DeferCleanup(func() {
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, vastSecret))).To(Succeed())
			})

			var capturedTiers []provisioning.TierDefinition
			var capturedConns map[string]provisioning.BackendConnection
			clusterTriggers := 0
			clusterProvider := &mockProvisioningProvider{
				name: "cluster-mock",
				triggerProvisionFunc: func(pCtx context.Context, _ client.Object) (*provisioning.ProvisionResult, error) {
					clusterTriggers++
					capturedTiers = provisioning.StorageTierDefinitionsFromContext(pCtx)
					capturedConns = provisioning.StorageBackendConnectionsFromContext(pCtx)
					return &provisioning.ProvisionResult{
						JobID:        "cs-job-s2",
						InitialState: v1alpha1.JobStateRunning,
					}, nil
				},
			}

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, clusterProvider, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.Client = k8sClient
			configureComingledClients(r, "csp-gold-s2", "osac-fast")

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			res, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			Expect(res.RequeueAfter).To(Equal(pollInterval))

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())

			cspCond := tenant.GetStatusCondition(v1alpha1.TenantConditionStorageReady)
			Expect(cspCond).NotTo(BeNil())
			Expect(cspCond.Status).To(Equal(metav1.ConditionTrue))

			backendCond := tenant.GetStatusCondition(v1alpha1.TenantConditionStorageBackendReady)
			Expect(backendCond).NotTo(BeNil())
			Expect(backendCond.Status).To(Equal(metav1.ConditionTrue))
			Expect(tenant.Status.StorageBackends).To(Equal([]v1alpha1.StorageBackendStatus{{
				Name:     "backend-vast",
				Provider: "vast",
				Ready:    true,
				Message:  fmt.Sprintf("Hub Secret for tenant %q exists", name),
			}}))

			clusterCond := tenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady)
			Expect(clusterCond).NotTo(BeNil())
			Expect(clusterCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(clusterCond.Reason).To(Equal(v1alpha1.TenantReasonNotFound))
			Expect(clusterCond.Message).To(ContainSubstring(`tier "osac-fast" has no StorageClass`))
			Expect(clusterCond.Message).NotTo(ContainSubstring("csp-gold-s2"))

			Expect(tenant.Status.StorageClasses).To(Equal([]v1alpha1.ResolvedStorageClass{
				{Name: "csp-gold-s2-sc", Tier: "csp-gold-s2"},
			}))

			Expect(clusterTriggers).To(Equal(1))
			Expect(capturedTiers).To(HaveLen(1))
			Expect(capturedTiers[0].Name).To(Equal("osac-fast"))
			Expect(capturedConns).To(HaveKey("backend-vast"))
			Expect(capturedConns).NotTo(HaveKey("backend-csp"))
		})

		It("should set all three conditions True and merge StorageClasses sorted by Tier when both cspManaged and OSAC StorageClasses exist", func() {
			name := "comingled-both-exist"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createLabeledStorageClass(ctx, "csp-gold-s3-sc", cspManagedTenantValue, "csp-gold-s3")
			createLabeledStorageClass(ctx, name+"-osac-fast-sc", name, "osac-fast")
			waitForStorageClassInCache("csp-gold-s3-sc")
			waitForStorageClassInCache(name + "-osac-fast-sc")

			vastSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("vast-tenant-config-%s", name),
					Namespace: secretsNamespace,
					Labels: map[string]string{
						osacTenantKey:            name,
						osacStorageProviderLabel: "vast",
					},
				},
				StringData: map[string]string{"provider": "vast"},
			}
			Expect(k8sClient.Create(ctx, vastSecret)).To(Succeed())
			waitForIndexedClientObject(vastSecret)
			DeferCleanup(func() {
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, vastSecret))).To(Succeed())
			})

			clusterProvider := &mockProvisioningProvider{name: "cluster-mock"}
			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, clusterProvider, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.Client = k8sClient
			configureComingledClients(r, "csp-gold-s3", "osac-fast")

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())

			Expect(tenant.GetStatusCondition(v1alpha1.TenantConditionStorageReady).Status).To(Equal(metav1.ConditionTrue))
			Expect(tenant.GetStatusCondition(v1alpha1.TenantConditionStorageBackendReady).Status).To(Equal(metav1.ConditionTrue))
			Expect(tenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady).Status).To(Equal(metav1.ConditionTrue))

			Expect(tenant.Status.StorageBackends).To(HaveLen(1))
			Expect(tenant.Status.StorageBackends[0].Name).To(Equal("backend-vast"))

			Expect(tenant.Status.StorageClasses).To(Equal([]v1alpha1.ResolvedStorageClass{
				{Name: "csp-gold-s3-sc", Tier: "csp-gold-s3"},
				{Name: name + "-osac-fast-sc", Tier: "osac-fast"},
			}))
		})

		It("should preserve cspResolved when pre-provisioned OSAC StorageClass is missing and merge both when present (ClusterStorageProvider == nil)", func() {
			name := "comingled-preprovisioned"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createLabeledStorageClass(ctx, "csp-gold-preprov-sc", cspManagedTenantValue, "csp-gold-preprov")
			waitForStorageClassInCache("csp-gold-preprov-sc")

			vastSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("vast-tenant-config-%s", name),
					Namespace: secretsNamespace,
					Labels: map[string]string{
						osacTenantKey:            name,
						osacStorageProviderLabel: "vast",
					},
				},
				StringData: map[string]string{"provider": "vast"},
			}
			Expect(k8sClient.Create(ctx, vastSecret)).To(Succeed())
			waitForIndexedClientObject(vastSecret)
			DeferCleanup(func() {
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, vastSecret))).To(Succeed())
			})

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.Client = k8sClient
			r.Recorder = events.NewFakeRecorder(100)
			configureComingledClients(r, "csp-gold-preprov", "osac-fast")

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}

			By("reconciling when pre-provisioned OSAC StorageClass is missing (len(result.resolved) == 0)")
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			Expect(tenant.GetStatusCondition(v1alpha1.TenantConditionStorageReady).Status).To(Equal(metav1.ConditionTrue))
			Expect(tenant.GetStatusCondition(v1alpha1.TenantConditionStorageBackendReady).Status).To(Equal(metav1.ConditionTrue))

			clusterCond := tenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady)
			Expect(clusterCond).NotTo(BeNil())
			Expect(clusterCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(clusterCond.Reason).To(Equal(v1alpha1.TenantReasonNotFound))
			Expect(clusterCond.Message).To(ContainSubstring(`tier "osac-fast" has no StorageClass`))
			Expect(clusterCond.Message).NotTo(ContainSubstring("csp-gold-preprov"))
			Expect(tenant.Status.StorageClasses).To(Equal([]v1alpha1.ResolvedStorageClass{
				{Name: "csp-gold-preprov-sc", Tier: "csp-gold-preprov"},
			}))

			By("reconciling after pre-provisioned OSAC StorageClass is created (len(result.resolved) > 0)")
			createLabeledStorageClass(ctx, name+"-osac-fast-sc", name, "osac-fast")
			waitForStorageClassInCache(name + "-osac-fast-sc")

			_, err = r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			clusterCond = tenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady)
			Expect(clusterCond).NotTo(BeNil())
			Expect(clusterCond.Status).To(Equal(metav1.ConditionTrue))
			Expect(clusterCond.Reason).To(Equal(v1alpha1.TenantReasonFound))
			Expect(tenant.Status.StorageClasses).To(Equal([]v1alpha1.ResolvedStorageClass{
				{Name: "csp-gold-preprov-sc", Tier: "csp-gold-preprov"},
				{Name: name + "-osac-fast-sc", Tier: "osac-fast"},
			}))
		})

		It("should deprovision only OSAC tiers and backends on handleDelete for a comingled Tenant", func() {
			name := "comingled-delete"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createLabeledStorageClass(ctx, "csp-gold-del-sc", cspManagedTenantValue, "csp-gold-del")
			createLabeledStorageClass(ctx, name+"-osac-fast-sc", name, "osac-fast")
			waitForStorageClassInCache("csp-gold-del-sc")
			waitForStorageClassInCache(name + "-osac-fast-sc")

			vastSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("vast-tenant-config-%s", name),
					Namespace: secretsNamespace,
					Labels: map[string]string{
						osacTenantKey:            name,
						osacStorageProviderLabel: "vast",
					},
				},
				StringData: map[string]string{"provider": "vast"},
			}
			Expect(k8sClient.Create(ctx, vastSecret)).To(Succeed())
			waitForIndexedClientObject(vastSecret)
			DeferCleanup(func() {
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, vastSecret))).To(Succeed())
			})

			var clusterDeprovTiers, backendDeprovTiers []provisioning.TierDefinition
			var clusterDeprovConns, backendDeprovConns map[string]provisioning.BackendConnection

			clusterProvider := &mockProvisioningProvider{
				name: "cluster-mock",
				triggerDeprovisionFunc: func(dCtx context.Context, _ client.Object, _ []v1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
					clusterDeprovTiers = provisioning.StorageTierDefinitionsFromContext(dCtx)
					clusterDeprovConns = provisioning.StorageBackendConnectionsFromContext(dCtx)
					return &provisioning.DeprovisionResult{
						Action: provisioning.DeprovisionTriggered,
						JobID:  "deprov-cs-comingled",
					}, nil
				},
				getDeprovisionStatusFunc: func(context.Context, client.Object, string) (provisioning.ProvisionStatus, error) {
					return provisioning.ProvisionStatus{State: v1alpha1.JobStateSucceeded, Message: "done"}, nil
				},
			}
			backendProvider := &mockProvisioningProvider{
				name: "backend-mock",
				triggerDeprovisionFunc: func(dCtx context.Context, _ client.Object, _ []v1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
					backendDeprovTiers = provisioning.StorageTierDefinitionsFromContext(dCtx)
					backendDeprovConns = provisioning.StorageBackendConnectionsFromContext(dCtx)
					return &provisioning.DeprovisionResult{
						Action: provisioning.DeprovisionTriggered,
						JobID:  "deprov-be-comingled",
					}, nil
				},
				getDeprovisionStatusFunc: func(context.Context, client.Object, string) (provisioning.ProvisionStatus, error) {
					return provisioning.ProvisionStatus{State: v1alpha1.JobStateSucceeded, Message: "done"}, nil
				},
			}

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				backendProvider, clusterProvider, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.Client = k8sClient
			configureComingledClients(r, "csp-gold-del", "osac-fast")

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			Expect(k8sClient.Delete(ctx, tenant)).To(Succeed())

			Eventually(func(g Gomega) {
				_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(clusterDeprovTiers).To(HaveLen(1))
				g.Expect(backendDeprovTiers).To(HaveLen(1))
				err = k8sClient.Get(ctx, nn, tenant)
				if err == nil {
					g.Expect(tenant.Finalizers).NotTo(ContainElement(storageFinalizer))
				} else {
					g.Expect(client.IgnoreNotFound(err)).To(Succeed())
				}
			}, 5*time.Second, 100*time.Millisecond).Should(Succeed())

			Expect(clusterDeprovTiers[0].Name).To(Equal("osac-fast"))
			Expect(clusterDeprovConns).To(HaveKey("backend-vast"))
			Expect(clusterDeprovConns).NotTo(HaveKey("backend-csp"))
			Expect(backendDeprovTiers[0].Name).To(Equal("osac-fast"))
			Expect(backendDeprovConns).To(HaveKey("backend-vast"))
			Expect(backendDeprovConns).NotTo(HaveKey("backend-csp"))
		})

		It("should leave StorageReady nil (and remove stale condition) in pure OSAC-managed mode", func() {
			name := "pure-osac-removes-stale-csp-cond"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createHubSecret(ctx, name, secretsNamespace)
			createLabeledStorageClass(ctx, name+"-lvms-sc", name, "local")
			waitForStorageClassInCache(name + "-lvms-sc")

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			tenant.SetStatusCondition(v1alpha1.TenantConditionStorageReady,
				metav1.ConditionTrue, v1alpha1.TenantReasonFound, "stale csp onboarding condition")
			Expect(k8sClient.Status().Update(ctx, tenant)).To(Succeed())

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.Client = k8sClient
			r.TiersClient = &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return privatev1.StorageTiersListResponse_builder{
						Items: []*privatev1.StorageTier{
							newTestStorageTier("local", "backend-lvms"),
						},
					}.Build(), nil
				},
			}
			r.BackendsClient = &mockStorageBackendsClient{
				getFunc: func(_ context.Context, _ *privatev1.StorageBackendsGetRequest, _ ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error) {
					return newTestStorageBackendGetResponse("lvms"), nil
				},
			}

			_, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())

			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			Expect(tenant.GetStatusCondition(v1alpha1.TenantConditionStorageReady)).To(BeNil(),
				"pure OSAC-managed mode must not set StorageReady and must remove stale condition")
			Expect(tenant.GetStatusCondition(v1alpha1.TenantConditionStorageBackendReady).Status).To(Equal(metav1.ConditionTrue))
			Expect(tenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady).Status).To(Equal(metav1.ConditionTrue))
		})

		It("should not take the pure-CSP branch when an OSAC tier is skipped during resolution and should keep periodic CSP requeue on Stage 1 stop", func() {
			name := "comingled-skipped-osac-tier"
			createReadyTenantForStorage(ctx, name, testNamespace)
			createLabeledStorageClass(ctx, "csp-gold-skip-sc", cspManagedTenantValue, "csp-gold-skip")
			createLabeledStorageClass(ctx, name+"-osac-fast-sc", name, "osac-fast")
			waitForStorageClassInCache("csp-gold-skip-sc")
			waitForStorageClassInCache(name + "-osac-fast-sc")

			vastSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("vast-tenant-config-%s", name),
					Namespace: secretsNamespace,
					Labels: map[string]string{
						osacTenantKey:            name,
						osacStorageProviderLabel: "vast",
					},
				},
				StringData: map[string]string{"provider": "vast"},
			}
			Expect(k8sClient.Create(ctx, vastSecret)).To(Succeed())
			waitForIndexedClientObject(vastSecret)
			DeferCleanup(func() {
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, vastSecret))).To(Succeed())
			})

			r := NewStorageReconciler(
				testMcManager, testNamespace, mcmanager.LocalCluster,
				nil, nil, pollInterval,
				provisioning.DefaultMaxJobHistory,
			)
			r.Client = k8sClient
			r.Recorder = events.NewFakeRecorder(100)
			configureComingledClients(r, "csp-gold-skip", "osac-fast")

			nn := types.NamespacedName{Name: name, Namespace: testNamespace}
			res, err := r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			Expect(res.RequeueAfter).To(BeZero())

			By("simulating a skipped OSAC tier (backend NotFound) while Stage 1 stops early (hub secret removed and BackendProvider == nil)")
			r.TiersClient = &mockStorageTiersLister{
				listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
					return privatev1.StorageTiersListResponse_builder{
						Items: []*privatev1.StorageTier{
							newTestStorageTier("csp-gold-skip", "backend-csp"),
							newTestStorageTier("osac-fast", "backend-missing"),
						},
					}.Build(), nil
				},
			}
			Expect(k8sClient.Delete(ctx, vastSecret)).To(Succeed())
			waitForIndexedClientObjectGone(vastSecret)

			res, err = r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			Expect(res.RequeueAfter).To(BeZero())

			tenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, nn, tenant)).To(Succeed())
			Expect(tenant.Status.StorageClasses).To(Equal([]v1alpha1.ResolvedStorageClass{
				{Name: "csp-gold-skip-sc", Tier: "csp-gold-skip"},
				{Name: name + "-osac-fast-sc", Tier: "osac-fast"},
			}), "skipped OSAC tier must not trigger pure-CSP branch or drop existing OSAC StorageClasses")
			backendCond := tenant.GetStatusCondition(v1alpha1.TenantConditionStorageBackendReady)
			Expect(backendCond).NotTo(BeNil())
			Expect(backendCond.Reason).To(Equal(v1alpha1.TenantReasonBackendConfiguredNoProvider))

			By("preventing BackendProvider and ClusterStorageProvider from launching with empty OSAC tier definitions when all OSAC tiers are skipped")
			provTriggers := 0
			noopProv := &mockProvisioningProvider{
				name: "noop-prov",
				triggerProvisionFunc: func(context.Context, client.Object) (*provisioning.ProvisionResult, error) {
					provTriggers++
					return &provisioning.ProvisionResult{JobID: "unexpected-empty-osac-job", InitialState: v1alpha1.JobStateRunning}, nil
				},
			}
			r.BackendProvider = noopProv
			r.ClusterStorageProvider = noopProv
			res, err = r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			Expect(res.RequeueAfter).To(BeZero())
			Expect(provTriggers).To(BeZero(),
				"AAP provisioning must not run when all OSAC tiers were skipped during resolution")

			By("staying on the pure-CSP path when a global tier is skipped on a pure-CSP Tenant with no OSAC backend or state")
			pureCSPName := "pure-csp-with-global-skipped-tier"
			createReadyTenantForStorage(ctx, pureCSPName, testNamespace)
			tiersClient := &mockStorageTiersLister{
				listFunc: r.TiersClient.List,
			}
			backendsClient := &mockStorageBackendsClient{
				listFunc: func(context.Context, *privatev1.StorageBackendsListRequest, ...grpc.CallOption) (*privatev1.StorageBackendsListResponse, error) {
					return privatev1.StorageBackendsListResponse_builder{
						Total: 1,
						Items: []*privatev1.StorageBackend{
							privatev1.StorageBackend_builder{
								Spec: privatev1.StorageBackendSpec_builder{
									Provider:   "aws-ebs",
									CspManaged: true,
								}.Build(),
							}.Build(),
						},
					}.Build(), nil
				},
				getFunc: func(_ context.Context, in *privatev1.StorageBackendsGetRequest, _ ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error) {
					if in.GetId() == "backend-csp" {
						return newTestCSPManagedBackendGetResponse("aws-ebs"), nil
					}
					return nil, status.Error(codes.NotFound, "not found")
				},
			}
			r.TiersClient = tiersClient
			r.BackendsClient = backendsClient
			pureNN := types.NamespacedName{Name: pureCSPName, Namespace: testNamespace}
			res, err = r.Reconcile(ctx, storageReconcileRequest(pureNN))
			Expect(err).NotTo(HaveOccurred())
			Expect(res.RequeueAfter).To(BeZero())
			Expect(provTriggers).To(BeZero(), "pure-CSP Tenant must not trigger AAP provisioning when an unrelated tier is skipped")

			pureTenant := &v1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, pureNN, pureTenant)).To(Succeed())
			Expect(pureTenant.GetStatusCondition(v1alpha1.TenantConditionStorageReady)).NotTo(BeNil())
			Expect(pureTenant.GetStatusCondition(v1alpha1.TenantConditionStorageReady).Status).To(Equal(metav1.ConditionTrue))
			Expect(pureTenant.GetStatusCondition(v1alpha1.TenantConditionStorageBackendReady)).To(BeNil())
			Expect(pureTenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady)).To(BeNil())

			By("batching catalog lookups across multiple Tenants via syncCatalog and emitting a catalogEvents notification only when the catalog changes")
			tiersClient.listCallCount = 0
			backendsClient.getCallCount = 0
			changed, err := r.syncCatalog(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(changed).To(BeTrue(), "initial syncCatalog call must report catalog changed")
			Expect(tiersClient.listCallCount).To(Equal(1))
			Expect(backendsClient.getCallCount).To(Equal(2))
			Eventually(r.catalogEvents).Should(Receive())

			// Subsequent syncCatalog with identical catalog state reports changed=false and emits no event.
			changed, err = r.syncCatalog(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(changed).To(BeFalse(), "unchanged catalog must not re-enqueue Tenants")
			Consistently(r.catalogEvents, 50*time.Millisecond).ShouldNot(Receive())

			// Reconciling Tenants while cachedCatalog is populated makes 0 additional gRPC calls.
			tiersBefore := tiersClient.listCallCount
			backendsBefore := backendsClient.getCallCount
			_, err = r.Reconcile(ctx, storageReconcileRequest(pureNN))
			Expect(err).NotTo(HaveOccurred())
			_, err = r.Reconcile(ctx, storageReconcileRequest(nn))
			Expect(err).NotTo(HaveOccurred())
			Expect(tiersClient.listCallCount).To(Equal(tiersBefore),
				"Tenant reconciles must read from the shared catalog snapshot without per-Tenant TiersClient.List calls")
			Expect(backendsClient.getCallCount).To(Equal(backendsBefore),
				"Tenant reconciles must read from the shared catalog snapshot without per-Tenant BackendsClient.Get calls")

			reqs := r.mapCatalogEventToTenants(ctx, nil)
			Expect(reqs).NotTo(BeEmpty())
		})
	})
})
