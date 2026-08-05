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
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	runtimecluster "sigs.k8s.io/controller-runtime/pkg/cluster"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mc "sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	osacv1alpha1 "github.com/osac-project/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac-operator/pkg/provisioning"
)

// mockExternalIPMulticlusterManager is a minimal implementation for testing address population.
// Embeds the full Manager interface; only GetCluster is overridden.
type mockExternalIPMulticlusterManager struct {
	mcmanager.Manager
	targetClient client.Client
}

func (m *mockExternalIPMulticlusterManager) GetCluster(_ context.Context, _ mc.ClusterName) (runtimecluster.Cluster, error) {
	return &mockExternalIPCluster{client: m.targetClient}, nil
}

// mockExternalIPCluster satisfies cluster.Cluster; only GetClient is overridden.
type mockExternalIPCluster struct {
	runtimecluster.Cluster
	client client.Client
}

func (c *mockExternalIPCluster) GetClient() client.Client {
	return c.client
}

// Tests for the ExternalIP provisioning controller.
//
// Key concepts for understanding these tests:
//
//   - A ExternalIP belongs to a parent ExternalIPPool. The relationship uses fulfillment-service
//     UUIDs, not K8s object names: publicIP.spec.pool contains a UUID, and the parent
//     ExternalIPPool CR carries that UUID in its osac.openshift.io/externalippool-uuid label.
//     The controller resolves the parent by listing pools with a matching label.
//
//   - The reconcile loop requires multiple passes because each pass does one thing and
//     returns: first pass adds the finalizer (metadata write), second pass processes the
//     spec and triggers provisioning, third pass polls the provisioning job status.
//
//   - Deletion tests call handleDelete directly because the fake client does not support
//     setting DeletionTimestamp via Update. We set it in memory and invoke the handler.
//
//   - The mock provisioning provider (defined in computeinstance_provisioning_test.go)
//     simulates AAP job triggers and status polls. By default it returns success; tests
//     override specific funcs to simulate failures or running states.
var _ = Describe("ExternalIPReconciler", func() {
	var (
		reconciler   *ExternalIPReconciler
		mockProvider *mockProvisioningProvider
		fakeClient   client.Client
		testCtx      context.Context
		publicIP     *osacv1alpha1.ExternalIP
		parentPool   *osacv1alpha1.ExternalIPPool
		testScheme   *runtime.Scheme
	)

	const (
		testNamespace            = "test-namespace"
		testPoolUUID             = "pool-uuid-123"
		testConfigVersion        = "version-1-abc123"
		testConfigVersionUpdated = "version-2-def456"
	)

	BeforeEach(func() {
		testCtx = context.TODO()
		testScheme = runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(testScheme)).To(Succeed())
		Expect(scheme.AddToScheme(testScheme)).To(Succeed())

		// The parent pool has a K8s name ("pool-k8s-name") that differs from the UUID
		// ("pool-uuid-123") to verify the controller uses label-based lookup, not name-based.
		parentPool = &osacv1alpha1.ExternalIPPool{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pool-k8s-name",
				Namespace: testNamespace,
				Labels: map[string]string{
					osacExternalIPPoolIDLabel: testPoolUUID,
				},
			},
			Spec: osacv1alpha1.ExternalIPPoolSpec{
				CIDRs:                  []string{"192.168.1.0/24"},
				IPFamily:               "IPv4",
				ImplementationStrategy: "metallb-l2",
			},
		}

		// The ExternalIP references the parent pool by UUID, not by K8s name.
		publicIP = &osacv1alpha1.ExternalIP{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-externalip",
				Namespace: testNamespace,
			},
			Spec: osacv1alpha1.ExternalIPSpec{
				Pool: testPoolUUID,
			},
		}

		fakeClient = fake.NewClientBuilder().
			WithScheme(testScheme).
			WithObjects(publicIP, parentPool).
			WithStatusSubresource(&osacv1alpha1.ExternalIP{}).
			Build()

		mockProvider = &mockProvisioningProvider{name: "mock-aap"}

		// Default mock mgr provides an empty workload-cluster client so the
		// address-population guard in handleUpdate does not nil-panic. Tests
		// that need a Service on the workload cluster override reconciler.mgr.
		emptyTargetClient := fake.NewClientBuilder().WithScheme(testScheme).Build()

		reconciler = &ExternalIPReconciler{
			Client:               fakeClient,
			APIReader:            fakeClient,
			Scheme:               testScheme,
			mgr:                  &mockExternalIPMulticlusterManager{targetClient: emptyTargetClient},
			NetworkingNamespace:  testNamespace,
			ProvisioningProvider: mockProvider,
			StatusPollInterval:   1 * time.Second,
			MaxJobHistory:        10,
		}
	})

	Context("Reconcile", func() {
		It("should add finalizer on first reconcile", func() {
			key := types.NamespacedName{Name: publicIP.Name, Namespace: publicIP.Namespace}
			result, err := reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())

			updated := &osacv1alpha1.ExternalIP{}
			Expect(fakeClient.Get(testCtx, key, updated)).To(Succeed())
			Expect(updated.Finalizers).To(ContainElement(osacExternalIPFinalizer))
		})

		It("should set phase to Progressing initially", func() {
			key := types.NamespacedName{Name: publicIP.Name, Namespace: publicIP.Namespace}

			// Keep the job in Running state so the phase stays Progressing
			// (a Succeeded job would transition to Ready)
			mockProvider.getProvisionStatusFunc = func(
				ctx context.Context, resource client.Object, jobID string,
			) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID:   jobID,
					State:   osacv1alpha1.JobStateRunning,
					Message: "Job running",
				}, nil
			}

			// Pass 1: adds finalizer
			_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())

			// Pass 2: resolves parent pool, triggers provisioning, sets Progressing
			_, err = reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())

			updated := &osacv1alpha1.ExternalIP{}
			Expect(fakeClient.Get(testCtx, key, updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(osacv1alpha1.ExternalIPPhaseProgressing))
		})

		It("should set implementation-strategy annotation from parent pool", func() {
			key := types.NamespacedName{Name: publicIP.Name, Namespace: publicIP.Namespace}

			// Pass 1: adds finalizer
			_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())

			// Pass 2: resolves parent pool and copies its implementation strategy
			_, err = reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())

			updated := &osacv1alpha1.ExternalIP{}
			Expect(fakeClient.Get(testCtx, key, updated)).To(Succeed())
			Expect(updated.Annotations[osacImplementationStrategyAnnotation]).To(Equal("metallb-l2"))
			Expect(updated.Annotations[osacExternalIPPoolNameAnnotation]).To(Equal("pool-k8s-name"))
		})

		It("should requeue when parent ExternalIPPool is not found", func() {
			// This ExternalIP references a pool UUID that doesn't exist as a label
			// on any ExternalIPPool CR. The controller should requeue and wait for
			// the pool to appear (it may not have been reconciled to K8s yet).
			orphanIP := &osacv1alpha1.ExternalIP{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "orphan-externalip",
					Namespace: testNamespace,
				},
				Spec: osacv1alpha1.ExternalIPSpec{
					Pool: "nonexistent-pool-uuid",
				},
			}
			Expect(fakeClient.Create(testCtx, orphanIP)).To(Succeed())

			key := types.NamespacedName{Name: orphanIP.Name, Namespace: orphanIP.Namespace}

			// Pass 1: adds finalizer
			_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())

			// Pass 2: parent pool not found, requeues after precondition interval
			result, err := reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(defaultPreconditionRequeueInterval))
		})

		It("should use default implementation strategy when pool has none", func() {
			// A pool with no ImplementationStrategy in its spec should fall back
			// to defaultExternalIPPoolImplementationStrategy ("metallb-l2").
			poolNoStrategy := &osacv1alpha1.ExternalIPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pool-no-strategy",
					Namespace: testNamespace,
					Labels: map[string]string{
						osacExternalIPPoolIDLabel: "pool-no-strategy-uuid",
					},
				},
				Spec: osacv1alpha1.ExternalIPPoolSpec{
					CIDRs:    []string{"10.0.0.0/24"},
					IPFamily: "IPv4",
				},
			}
			Expect(fakeClient.Create(testCtx, poolNoStrategy)).To(Succeed())

			ipNoStrategy := &osacv1alpha1.ExternalIP{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ip-no-strategy",
					Namespace: testNamespace,
				},
				Spec: osacv1alpha1.ExternalIPSpec{
					Pool: "pool-no-strategy-uuid",
				},
			}
			Expect(fakeClient.Create(testCtx, ipNoStrategy)).To(Succeed())

			key := types.NamespacedName{Name: ipNoStrategy.Name, Namespace: ipNoStrategy.Namespace}

			// Pass 1: adds finalizer
			_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())

			// Pass 2: inherits default strategy since pool has none
			_, err = reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())

			updated := &osacv1alpha1.ExternalIP{}
			Expect(fakeClient.Get(testCtx, key, updated)).To(Succeed())
			Expect(updated.Annotations[osacImplementationStrategyAnnotation]).To(Equal(defaultExternalIPPoolImplementationStrategy))
			Expect(updated.Annotations[osacExternalIPPoolNameAnnotation]).To(Equal("pool-no-strategy"))
		})

		It("should set ConfigurationApplied condition to True", func() {
			key := types.NamespacedName{Name: publicIP.Name, Namespace: publicIP.Namespace}

			// Pass 1: finalizer, Pass 2: process spec and set condition
			_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())
			_, err = reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())

			updated := &osacv1alpha1.ExternalIP{}
			Expect(fakeClient.Get(testCtx, key, updated)).To(Succeed())

			condition := osacv1alpha1.GetExternalIPStatusCondition(
				updated, osacv1alpha1.ExternalIPConditionConfigurationApplied,
			)
			Expect(condition).NotTo(BeNil())
			Expect(condition.Status).To(Equal(metav1.ConditionTrue))
			Expect(condition.Reason).To(Equal("ConfigurationApplied"))
		})

		It("should set phase to Ready on successful provision when address is available", func() {
			key := types.NamespacedName{Name: publicIP.Name, Namespace: publicIP.Namespace}

			svc := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      externalIPServiceNamePrefix + publicIP.Name,
					Namespace: externalIPDefaultMetalLBNamespace,
				},
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{
						Ingress: []corev1.LoadBalancerIngress{{IP: "203.0.113.99"}},
					},
				},
			}
			targetClient := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(svc).Build()
			reconciler.mgr = &mockExternalIPMulticlusterManager{targetClient: targetClient}

			mockProvider.triggerProvisionFunc = func(
				ctx context.Context, resource client.Object,
			) (*provisioning.ProvisionResult, error) {
				return &provisioning.ProvisionResult{
					JobID:        "job-success",
					InitialState: osacv1alpha1.JobStatePending,
					Message:      "Job triggered",
				}, nil
			}

			mockProvider.getProvisionStatusFunc = func(
				ctx context.Context, resource client.Object, jobID string,
			) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID:   jobID,
					State:   osacv1alpha1.JobStateSucceeded,
					Message: "Provisioning completed",
				}, nil
			}

			// Pass 1: finalizer, Pass 2: trigger AAP job, Pass 3: poll job -> Succeeded -> Ready
			_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())
			_, err = reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())
			_, err = reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())

			updated := &osacv1alpha1.ExternalIP{}
			Expect(fakeClient.Get(testCtx, key, updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(osacv1alpha1.ExternalIPPhaseReady))
			Expect(updated.Status.Address).To(Equal("203.0.113.99"))
		})

		It("should set phase to Failed on provision failure", func() {
			key := types.NamespacedName{Name: publicIP.Name, Namespace: publicIP.Namespace}

			mockProvider.triggerProvisionFunc = func(
				ctx context.Context, resource client.Object,
			) (*provisioning.ProvisionResult, error) {
				return &provisioning.ProvisionResult{
					JobID:        "job-fail",
					InitialState: osacv1alpha1.JobStatePending,
					Message:      "Job triggered",
				}, nil
			}

			mockProvider.getProvisionStatusFunc = func(
				ctx context.Context, resource client.Object, jobID string,
			) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID:        jobID,
					State:        osacv1alpha1.JobStateFailed,
					Message:      "Provisioning failed",
					ErrorDetails: "MetalLB unreachable",
				}, nil
			}

			// Pass 1: finalizer, Pass 2: trigger, Pass 3: poll -> Failed
			_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())
			_, err = reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())
			_, err = reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())

			updated := &osacv1alpha1.ExternalIP{}
			Expect(fakeClient.Get(testCtx, key, updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(osacv1alpha1.ExternalIPPhaseFailed))
		})

		// Deletion tests call handleDelete directly because the fake client does not
		// support setting DeletionTimestamp via Update. We add the finalizer via a
		// normal Reconcile, then set DeletionTimestamp in memory and call handleDelete.

		It("should trigger deprovision on delete", func() {
			key := types.NamespacedName{Name: publicIP.Name, Namespace: publicIP.Namespace}

			deprovisionCalled := false
			mockProvider.triggerDeprovisionFunc = func(
				ctx context.Context, resource client.Object, _ []osacv1alpha1.JobStatus,
			) (*provisioning.DeprovisionResult, error) {
				deprovisionCalled = true
				return &provisioning.DeprovisionResult{
					Action:                 provisioning.DeprovisionTriggered,
					JobID:                  "deprovision-job-123",
					BlockDeletionOnFailure: true,
				}, nil
			}

			// Add finalizer via normal reconcile
			_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())

			// Simulate K8s delete by setting DeletionTimestamp in memory
			toDelete := &osacv1alpha1.ExternalIP{}
			Expect(fakeClient.Get(testCtx, key, toDelete)).To(Succeed())
			now := metav1.Now()
			toDelete.DeletionTimestamp = &now

			_, err = reconciler.handleDelete(testCtx, toDelete)
			Expect(err).NotTo(HaveOccurred())

			Expect(deprovisionCalled).To(BeTrue())
			Expect(toDelete.Status.Phase).To(Equal(osacv1alpha1.ExternalIPPhaseDeleting))

			latestJob := provisioning.FindLatestJobByType(toDelete.Status.ProvisioningJobs, osacv1alpha1.JobTypeDeprovision)
			Expect(latestJob).NotTo(BeNil())
			Expect(latestJob.JobID).To(Equal("deprovision-job-123"))
		})

		It("should remove finalizer after successful deprovision", func() {
			key := types.NamespacedName{Name: publicIP.Name, Namespace: publicIP.Namespace}

			mockProvider.triggerDeprovisionFunc = func(
				ctx context.Context, resource client.Object, _ []osacv1alpha1.JobStatus,
			) (*provisioning.DeprovisionResult, error) {
				return &provisioning.DeprovisionResult{
					Action:                 provisioning.DeprovisionTriggered,
					JobID:                  "deprovision-success",
					BlockDeletionOnFailure: true,
				}, nil
			}

			mockProvider.getDeprovisionStatusFunc = func(
				ctx context.Context, resource client.Object, jobID string,
			) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID:   jobID,
					State:   osacv1alpha1.JobStateSucceeded,
					Message: "Deprovision completed",
				}, nil
			}

			// Add finalizer via normal reconcile
			_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())

			toDelete := &osacv1alpha1.ExternalIP{}
			Expect(fakeClient.Get(testCtx, key, toDelete)).To(Succeed())
			now := metav1.Now()
			toDelete.DeletionTimestamp = &now

			// First call triggers the deprovision job
			_, err = reconciler.handleDelete(testCtx, toDelete)
			Expect(err).NotTo(HaveOccurred())

			// Second call polls status (Succeeded) and removes the finalizer
			_, _ = reconciler.handleDelete(testCtx, toDelete)

			Expect(toDelete.Finalizers).NotTo(ContainElement(osacExternalIPFinalizer))
		})

		It("should still handle delete for unmanaged ExternalIP with finalizer", func() {
			// Edge case: a ExternalIP was managed (has finalizer), then an admin marked
			// it unmanaged. The management-state guard in Reconcile skips processing
			// for non-deleted resources, but deletion must still proceed to clean up
			// the AAP-provisioned resources and remove the finalizer.
			managedThenUnmanaged := &osacv1alpha1.ExternalIP{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "managed-then-unmanaged",
					Namespace: testNamespace,
					Annotations: map[string]string{
						osacManagementStateAnnotation: ManagementStateUnmanaged,
					},
					Finalizers: []string{osacExternalIPFinalizer},
				},
				Spec: osacv1alpha1.ExternalIPSpec{
					Pool: testPoolUUID,
				},
			}
			Expect(fakeClient.Create(testCtx, managedThenUnmanaged)).To(Succeed())

			key := types.NamespacedName{Name: managedThenUnmanaged.Name, Namespace: managedThenUnmanaged.Namespace}

			deprovisionCalled := false
			mockProvider.triggerDeprovisionFunc = func(
				ctx context.Context, resource client.Object, _ []osacv1alpha1.JobStatus,
			) (*provisioning.DeprovisionResult, error) {
				deprovisionCalled = true
				return &provisioning.DeprovisionResult{
					Action: provisioning.DeprovisionSkipped,
				}, nil
			}

			fetched := &osacv1alpha1.ExternalIP{}
			Expect(fakeClient.Get(testCtx, key, fetched)).To(Succeed())
			now := metav1.Now()
			fetched.DeletionTimestamp = &now

			// Verify the guard logic: DeletionTimestamp is set, so the unmanaged
			// annotation is ignored and the delete branch runs.
			Expect(fetched.ObjectMeta.DeletionTimestamp.IsZero()).To(BeFalse())

			_, _ = reconciler.handleDelete(testCtx, fetched)

			Expect(deprovisionCalled).To(BeTrue())
			Expect(fetched.Finalizers).NotTo(ContainElement(osacExternalIPFinalizer))
		})

		It("should set state to Pending on initial provisioning", func() {
			key := types.NamespacedName{Name: publicIP.Name, Namespace: publicIP.Namespace}

			mockProvider.getProvisionStatusFunc = func(
				ctx context.Context, resource client.Object, jobID string,
			) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID:   jobID,
					State:   osacv1alpha1.JobStateRunning,
					Message: "Job running",
				}, nil
			}

			// Pass 1: finalizer, Pass 2: trigger provisioning -> Progressing + Pending
			_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())
			_, err = reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())

			updated := &osacv1alpha1.ExternalIP{}
			Expect(fakeClient.Get(testCtx, key, updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(osacv1alpha1.ExternalIPPhaseProgressing))
			Expect(updated.Status.State).To(Equal(osacv1alpha1.ExternalIPStatePending))
		})

		It("should stay Progressing when Allocated but address not yet available", func() {
			key := types.NamespacedName{Name: publicIP.Name, Namespace: publicIP.Namespace}

			mockProvider.triggerProvisionFunc = func(
				ctx context.Context, resource client.Object,
			) (*provisioning.ProvisionResult, error) {
				return &provisioning.ProvisionResult{
					JobID:        "job-allocated",
					InitialState: osacv1alpha1.JobStatePending,
					Message:      "Job triggered",
				}, nil
			}

			mockProvider.getProvisionStatusFunc = func(
				ctx context.Context, resource client.Object, jobID string,
			) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID:   jobID,
					State:   osacv1alpha1.JobStateSucceeded,
					Message: "Provisioning completed",
				}, nil
			}

			// Pass 1: finalizer, Pass 2: trigger, Pass 3: poll -> Allocated but no address
			_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())
			_, err = reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())
			result, err := reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())

			updated := &osacv1alpha1.ExternalIP{}
			Expect(fakeClient.Get(testCtx, key, updated)).To(Succeed())
			Expect(updated.Status.State).To(Equal(osacv1alpha1.ExternalIPStateAllocated))
			Expect(updated.Status.Phase).To(Equal(osacv1alpha1.ExternalIPPhaseProgressing),
				"phase should stay Progressing until address is populated")
			Expect(updated.Status.Address).To(BeEmpty())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0),
				"should requeue to retry address population")
		})

		It("should populate address in OnSuccess on initial allocation when Service already has an IP", func() {
			// This test covers the new code path added to the OnSuccess callback inside
			// handleProvisioning: when state transitions to Allocated and the parking
			// Service already has an ingress IP, the address must be set immediately
			// within the same reconcile pass -- no extra round-trip through handleUpdate.
			key := types.NamespacedName{Name: publicIP.Name, Namespace: publicIP.Namespace}

			svc := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      externalIPServiceNamePrefix + publicIP.Name,
					Namespace: externalIPDefaultMetalLBNamespace,
				},
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{
						Ingress: []corev1.LoadBalancerIngress{{IP: "203.0.113.10"}},
					},
				},
			}
			targetClient := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(svc).Build()
			reconciler.mgr = &mockExternalIPMulticlusterManager{targetClient: targetClient}

			mockProvider.triggerProvisionFunc = func(
				_ context.Context, _ client.Object,
			) (*provisioning.ProvisionResult, error) {
				return &provisioning.ProvisionResult{
					JobID:        "job-alloc-addr",
					InitialState: osacv1alpha1.JobStatePending,
					Message:      "Job triggered",
				}, nil
			}
			mockProvider.getProvisionStatusFunc = func(
				_ context.Context, _ client.Object, jobID string,
			) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID:   jobID,
					State:   osacv1alpha1.JobStateSucceeded,
					Message: "Provisioning completed",
				}, nil
			}

			// Pass 1: finalizer, Pass 2: trigger job, Pass 3: poll -> Succeeded -> OnSuccess
			for range 3 {
				_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
				Expect(err).NotTo(HaveOccurred())
			}

			updated := &osacv1alpha1.ExternalIP{}
			Expect(fakeClient.Get(testCtx, key, updated)).To(Succeed())
			Expect(updated.Status.State).To(Equal(osacv1alpha1.ExternalIPStateAllocated))
			Expect(updated.Status.Address).To(Equal("203.0.113.10"),
				"address should be populated immediately in OnSuccess, not deferred to the next reconcile")
		})

		It("should set state to Failed on provisioning failure", func() {
			key := types.NamespacedName{Name: publicIP.Name, Namespace: publicIP.Namespace}

			mockProvider.triggerProvisionFunc = func(
				ctx context.Context, resource client.Object,
			) (*provisioning.ProvisionResult, error) {
				return &provisioning.ProvisionResult{
					JobID:        "job-fail",
					InitialState: osacv1alpha1.JobStatePending,
					Message:      "Job triggered",
				}, nil
			}

			mockProvider.getProvisionStatusFunc = func(
				ctx context.Context, resource client.Object, jobID string,
			) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID:        jobID,
					State:        osacv1alpha1.JobStateFailed,
					Message:      "Provisioning failed",
					ErrorDetails: "MetalLB unreachable",
				}, nil
			}

			// Pass 1: finalizer, Pass 2: trigger, Pass 3: poll -> Failed state + phase
			_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())
			_, err = reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())
			_, err = reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())

			updated := &osacv1alpha1.ExternalIP{}
			Expect(fakeClient.Get(testCtx, key, updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(osacv1alpha1.ExternalIPPhaseFailed))
			Expect(updated.Status.State).To(Equal(osacv1alpha1.ExternalIPStateFailed))
		})

		It("should not change state on deletion", func() {
			key := types.NamespacedName{Name: publicIP.Name, Namespace: publicIP.Namespace}

			// Start with Allocated state: persist metadata first, then status.
			publicIP.Finalizers = []string{osacExternalIPFinalizer}
			Expect(fakeClient.Update(testCtx, publicIP)).To(Succeed())

			publicIP.Status.Phase = osacv1alpha1.ExternalIPPhaseReady
			publicIP.Status.State = osacv1alpha1.ExternalIPStateAllocated
			Expect(fakeClient.Status().Update(testCtx, publicIP)).To(Succeed())

			// Return a running deprovision job so handleDelete requeues before
			// reaching the finalizer-removal Update (which the envtest server
			// rejects when DeletionTimestamp is set in-memory).
			mockProvider.triggerDeprovisionFunc = func(
				_ context.Context, _ client.Object, _ []osacv1alpha1.JobStatus,
			) (*provisioning.DeprovisionResult, error) {
				return &provisioning.DeprovisionResult{
					Action: provisioning.DeprovisionTriggered,
					JobID:  "deprov-job",
				}, nil
			}

			toDelete := &osacv1alpha1.ExternalIP{}
			Expect(fakeClient.Get(testCtx, key, toDelete)).To(Succeed())
			now := metav1.Now()
			toDelete.DeletionTimestamp = &now

			_, err := reconciler.handleDelete(testCtx, toDelete)
			Expect(err).NotTo(HaveOccurred())

			// Phase transitions to Deleting but State remains unchanged
			Expect(toDelete.Status.Phase).To(Equal(osacv1alpha1.ExternalIPPhaseDeleting))
			Expect(toDelete.Status.State).To(Equal(osacv1alpha1.ExternalIPStateAllocated))
		})

		It("should ignore ExternalIP with management-state unmanaged annotation", func() {
			// When a ExternalIP has the unmanaged annotation and is NOT being deleted,
			// the controller should skip it entirely: no finalizer, no phase change.
			unmanagedIP := &osacv1alpha1.ExternalIP{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "unmanaged-ip",
					Namespace: testNamespace,
					Annotations: map[string]string{
						osacManagementStateAnnotation: ManagementStateUnmanaged,
					},
				},
				Spec: osacv1alpha1.ExternalIPSpec{
					Pool: testPoolUUID,
				},
			}
			Expect(fakeClient.Create(testCtx, unmanagedIP)).To(Succeed())

			key := types.NamespacedName{Name: unmanagedIP.Name, Namespace: unmanagedIP.Namespace}
			_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())

			updated := &osacv1alpha1.ExternalIP{}
			Expect(fakeClient.Get(testCtx, key, updated)).To(Succeed())

			Expect(updated.Finalizers).To(BeEmpty())
			Expect(updated.Status.Phase).To(BeEmpty())
		})
	})

	Context("provisioning condition updates", func() {
		It("should set Ready=False condition with error message when job fails", func() {
			publicIP.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
				{
					JobID:     "failed-job-cond",
					Type:      osacv1alpha1.JobTypeProvision,
					Timestamp: metav1.NewTime(time.Now().UTC()),
					State:     osacv1alpha1.JobStateRunning,
				},
			}

			mockProvider.getProvisionStatusFunc = func(_ context.Context, _ client.Object, jobID string) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID:   jobID,
					State:   osacv1alpha1.JobStateFailed,
					Message: "Ansible traceback: role xyz failed",
				}, nil
			}

			_, err := reconciler.handleProvisioning(ctx, publicIP)
			Expect(err).NotTo(HaveOccurred())

			cond := apimeta.FindStatusCondition(publicIP.Status.Conditions, osacv1alpha1.ConditionReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(osacv1alpha1.ReasonProvisioningFailed))
			Expect(cond.Message).To(ContainSubstring("Ansible traceback"))
		})

		It("should set Ready=True condition when job succeeds", func() {
			publicIP.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
				{
					JobID:     "success-job-cond",
					Type:      osacv1alpha1.JobTypeProvision,
					Timestamp: metav1.NewTime(time.Now().UTC()),
					State:     osacv1alpha1.JobStateRunning,
				},
			}

			mockProvider.getProvisionStatusFunc = func(_ context.Context, _ client.Object, jobID string) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID: jobID,
					State: osacv1alpha1.JobStateSucceeded,
				}, nil
			}

			_, err := reconciler.handleProvisioning(ctx, publicIP)
			Expect(err).NotTo(HaveOccurred())

			cond := apimeta.FindStatusCondition(publicIP.Status.Conditions, osacv1alpha1.ConditionReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(osacv1alpha1.ReasonAsExpected))
		})

		It("should clear stale Ready=False condition on provisioning recovery", func() {
			publicIP.Status.Conditions = []metav1.Condition{
				{
					Type:               osacv1alpha1.ConditionReady,
					Status:             metav1.ConditionFalse,
					Reason:             osacv1alpha1.ReasonProvisioningFailed,
					Message:            "previous failure",
					LastTransitionTime: metav1.Now(),
				},
			}
			publicIP.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
				{
					JobID:     "recovery-job",
					Type:      osacv1alpha1.JobTypeProvision,
					Timestamp: metav1.NewTime(time.Now().UTC()),
					State:     osacv1alpha1.JobStateRunning,
				},
			}

			mockProvider.getProvisionStatusFunc = func(_ context.Context, _ client.Object, jobID string) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID: jobID,
					State: osacv1alpha1.JobStateSucceeded,
				}, nil
			}

			_, err := reconciler.handleProvisioning(ctx, publicIP)
			Expect(err).NotTo(HaveOccurred())

			cond := apimeta.FindStatusCondition(publicIP.Status.Conditions, osacv1alpha1.ConditionReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(osacv1alpha1.ReasonAsExpected))
			Expect(cond.Message).To(BeEmpty())
		})
	})

	// The provisioning lifecycle uses a config version (hash of spec + strategy) to
	// detect spec changes. When provisioning fails, the controller backs off with
	// exponential delay. But if the spec changes (new config version), it retries
	// immediately instead of waiting for the backoff to expire.
	Context("backoff on failure", func() {
		It("should backoff when latest job failed with matching ConfigVersion", func() {
			publicIP.Status.DesiredConfigVersion = testConfigVersion
			publicIP.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
				{
					JobID:         "failed-job",
					Type:          osacv1alpha1.JobTypeProvision,
					Timestamp:     metav1.NewTime(time.Now().UTC()),
					State:         osacv1alpha1.JobStateFailed,
					Message:       "provision failed",
					ConfigVersion: testConfigVersion,
				},
			}

			result, err := reconciler.handleProvisioning(testCtx, publicIP)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))
			Expect(result.RequeueAfter).To(BeNumerically("<=", provisioning.BackoffMaxDelay))
		})

		It("should trigger immediately when spec changed after failure", func() {
			mockProvider.triggerProvisionFunc = func(
				ctx context.Context, resource client.Object,
			) (*provisioning.ProvisionResult, error) {
				return &provisioning.ProvisionResult{
					JobID:        "retry-job",
					InitialState: osacv1alpha1.JobStatePending,
				}, nil
			}

			// The desired version is "version-2" but the failed job was for "version-1",
			// meaning the spec changed. The controller should retry immediately.
			publicIP.Status.DesiredConfigVersion = testConfigVersionUpdated
			publicIP.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
				{
					JobID:         "failed-job",
					Type:          osacv1alpha1.JobTypeProvision,
					Timestamp:     metav1.NewTime(time.Now().UTC().Add(-2 * time.Second)),
					State:         osacv1alpha1.JobStateFailed,
					Message:       "provision failed",
					ConfigVersion: testConfigVersion,
				},
			}

			result, err := reconciler.handleProvisioning(testCtx, publicIP)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(1 * time.Second))

			latestJob := provisioning.FindLatestJobByType(publicIP.Status.ProvisioningJobs, osacv1alpha1.JobTypeProvision)
			Expect(latestJob).NotTo(BeNil())
			Expect(latestJob.JobID).To(Equal("retry-job"))
		})
	})

	Context("getExternalIPAddress", func() {
		It("should return IP from LoadBalancer Service ingress", func() {
			svc := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      externalIPServiceNamePrefix + "test-externalip",
					Namespace: externalIPDefaultMetalLBNamespace,
				},
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{
						Ingress: []corev1.LoadBalancerIngress{
							{IP: "203.0.113.42"},
						},
					},
				},
			}
			targetClient := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(svc).Build()

			ip := reconciler.getExternalIPAddress(testCtx, targetClient, "test-externalip")
			Expect(ip).To(Equal("203.0.113.42"))
		})

		It("should return empty string when Service not found", func() {
			targetClient := fake.NewClientBuilder().WithScheme(testScheme).Build()

			ip := reconciler.getExternalIPAddress(testCtx, targetClient, "nonexistent")
			Expect(ip).To(Equal(""))
		})

		It("should return empty string when ingress list is empty", func() {
			svc := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      externalIPServiceNamePrefix + "test-externalip",
					Namespace: externalIPDefaultMetalLBNamespace,
				},
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{
						Ingress: []corev1.LoadBalancerIngress{},
					},
				},
			}
			targetClient := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(svc).Build()

			ip := reconciler.getExternalIPAddress(testCtx, targetClient, "test-externalip")
			Expect(ip).To(Equal(""))
		})

		It("should return empty string when ingress IP is empty", func() {
			svc := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      externalIPServiceNamePrefix + "test-externalip",
					Namespace: externalIPDefaultMetalLBNamespace,
				},
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{
						Ingress: []corev1.LoadBalancerIngress{
							{IP: ""},
						},
					},
				},
			}
			targetClient := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(svc).Build()

			ip := reconciler.getExternalIPAddress(testCtx, targetClient, "test-externalip")
			Expect(ip).To(Equal(""))
		})

		It("should not populate address before provisioning succeeds (temporal ordering)", func() {
			key := types.NamespacedName{Name: publicIP.Name, Namespace: publicIP.Namespace}

			// A LoadBalancer Service with an assigned IP exists on the workload cluster.
			// The guard condition (state==Allocated && address=="") must prevent
			// address population until provisioning has completed.
			svc := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      externalIPServiceNamePrefix + "test-externalip",
					Namespace: externalIPDefaultMetalLBNamespace,
				},
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{
						Ingress: []corev1.LoadBalancerIngress{
							{IP: "203.0.113.42"},
						},
					},
				},
			}
			targetClient := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(svc).Build()
			reconciler.mgr = &mockExternalIPMulticlusterManager{targetClient: targetClient}

			// Keep jobs in Running state so OnSuccess does not fire and
			// override the state we set for each sub-test.
			mockProvider.getProvisionStatusFunc = func(
				_ context.Context, _ client.Object, jobID string,
			) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID:   jobID,
					State:   osacv1alpha1.JobStateRunning,
					Message: "Job running",
				}, nil
			}

			// Pending state: address must NOT be populated (pre-provisioning).
			// Call handleUpdate and verify the in-memory object.
			pendingIP := publicIP.DeepCopy()
			pendingIP.Status.Phase = osacv1alpha1.ExternalIPPhaseProgressing
			pendingIP.Status.State = osacv1alpha1.ExternalIPStatePending
			pendingIP.Status.Address = ""

			_, err := reconciler.handleUpdate(testCtx, pendingIP)
			Expect(err).NotTo(HaveOccurred())
			Expect(pendingIP.Status.Address).To(Equal(""), "address must not be populated in Pending state")

			// Allocated state: address SHOULD be populated (post-provisioning).
			// Re-read the object from the fake client to get the current
			// resourceVersion (handleUpdate modifies the object in the store).
			allocatedIP := &osacv1alpha1.ExternalIP{}
			Expect(fakeClient.Get(testCtx, key, allocatedIP)).To(Succeed())
			allocatedIP.Status.Phase = osacv1alpha1.ExternalIPPhaseProgressing
			allocatedIP.Status.State = osacv1alpha1.ExternalIPStateAllocated
			allocatedIP.Status.Address = ""

			_, err = reconciler.handleUpdate(testCtx, allocatedIP)
			Expect(err).NotTo(HaveOccurred())
			Expect(allocatedIP.Status.Address).To(Equal("203.0.113.42"), "address should be populated in Allocated state")
		})
	})
})
