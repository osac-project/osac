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
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

// Tests for the ExternalIPPool provisioning controller.
//
// Key concepts for understanding these tests:
//
//   - Unlike ExternalIP (which inherits strategy from its parent pool), ExternalIPPool reads
//     the implementation strategy from its own spec.implementationStrategy field.
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
var _ = Describe("ExternalIPPoolReconciler", func() {
	var (
		reconciler   *ExternalIPPoolReconciler
		mockProvider *mockProvisioningProvider
		fakeClient   client.Client
		testCtx      context.Context
		pool         *osacv1alpha1.ExternalIPPool
		testScheme   *runtime.Scheme
	)

	BeforeEach(func() {
		testCtx = context.TODO()
		testScheme = runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(testScheme)).To(Succeed())
		Expect(scheme.AddToScheme(testScheme)).To(Succeed())

		pool = &osacv1alpha1.ExternalIPPool{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pool",
				Namespace: "test-namespace",
			},
			Spec: osacv1alpha1.ExternalIPPoolSpec{
				CIDRs:                  []string{"192.168.1.0/24"},
				IPFamily:               "IPv4",
				ImplementationStrategy: "metallb-l2",
			},
		}

		fakeClient = fake.NewClientBuilder().
			WithScheme(testScheme).
			WithObjects(pool).
			WithStatusSubresource(&osacv1alpha1.ExternalIPPool{}).
			Build()

		mockProvider = &mockProvisioningProvider{name: "mock-aap"}

		reconciler = &ExternalIPPoolReconciler{
			Client:                     fakeClient,
			APIReader:                  fakeClient,
			Scheme:                     testScheme,
			NetworkingNamespace:        "test-namespace",
			ProvisioningProvider:       mockProvider,
			StatusPollInterval:         1 * time.Second,
			MaxJobHistory:              10,
			NetworkProvisioningEnabled: true,
		}
	})

	Context("Reconcile", func() {
		It("should add finalizer on first reconcile", func() {
			key := types.NamespacedName{Name: pool.Name, Namespace: pool.Namespace}
			result, err := reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())

			updated := &osacv1alpha1.ExternalIPPool{}
			Expect(fakeClient.Get(testCtx, key, updated)).To(Succeed())

			Expect(updated.Finalizers).To(ContainElement(osacExternalIPPoolFinalizer))
		})

		It("should set phase to Progressing initially", func() {
			key := types.NamespacedName{Name: pool.Name, Namespace: pool.Namespace}

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

			// Pass 2: reads strategy from spec, triggers provisioning, sets Progressing
			_, err = reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())

			updated := &osacv1alpha1.ExternalIPPool{}
			Expect(fakeClient.Get(testCtx, key, updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(osacv1alpha1.ExternalIPPoolPhaseProgressing))
		})

		It("should persist job status even when resource is concurrently modified", func() {
			key := types.NamespacedName{Name: pool.Name, Namespace: pool.Namespace}

			// Simulate feedback controller: during TriggerProvision, modify
			// the resource's metadata (add feedback finalizer) so the
			// resourceVersion changes before the status flush runs.
			provisionCalled := false
			mockProvider.triggerProvisionFunc = func(ctx context.Context, resource client.Object) (*provisioning.ProvisionResult, error) {
				provisionCalled = true
				fresh := &osacv1alpha1.ExternalIPPool{}
				Expect(fakeClient.Get(ctx, key, fresh)).To(Succeed())
				fresh.Finalizers = append(fresh.Finalizers, "osac.openshift.io/externalippool-feedback")
				Expect(fakeClient.Update(ctx, fresh)).To(Succeed())

				return &provisioning.ProvisionResult{
					JobID:        "concurrent-job-123",
					InitialState: osacv1alpha1.JobStatePending,
					Message:      "Provisioning triggered",
				}, nil
			}

			// Pass 1: adds finalizer and stamps implementation-strategy annotation,
			// returns early before triggering provisioning.
			_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())
			Expect(provisionCalled).To(BeFalse(), "AAP provisioning must not be triggered before annotation is stamped")

			// Pass 2: annotation already set, triggers provisioning.
			// The concurrent modification must not prevent the job from
			// being recorded in status.
			_, err = reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())
			Expect(provisionCalled).To(BeTrue(), "AAP provisioning must be triggered after annotation is stamped")

			// Verify the job was persisted
			updated := &osacv1alpha1.ExternalIPPool{}
			Expect(fakeClient.Get(testCtx, key, updated)).To(Succeed())
			latestJob := provisioning.FindLatestJobByType(updated.Status.ProvisioningJobs, osacv1alpha1.JobTypeProvision)
			Expect(latestJob).NotTo(BeNil())
			Expect(latestJob.JobID).To(Equal("concurrent-job-123"))
		})

		It("should stamp implementation-strategy annotation before triggering AAP provisioning", func() {
			key := types.NamespacedName{Name: pool.Name, Namespace: pool.Namespace}

			provisionCalled := false
			mockProvider.triggerProvisionFunc = func(_ context.Context, _ client.Object) (*provisioning.ProvisionResult, error) {
				provisionCalled = true
				return &provisioning.ProvisionResult{
					JobID:        "aap-job-1",
					InitialState: osacv1alpha1.JobStatePending,
					Message:      "Provisioning triggered",
				}, nil
			}

			// Pass 1: adds finalizer and stamps the annotation, returns early.
			_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())
			Expect(provisionCalled).To(BeFalse(), "AAP provisioning must not be triggered before annotation is stamped")

			annotated := &osacv1alpha1.ExternalIPPool{}
			Expect(fakeClient.Get(testCtx, key, annotated)).To(Succeed())
			Expect(annotated.Annotations).To(HaveKeyWithValue(
				osacImplementationStrategyAnnotation, "metallb-l2",
			))

			// Pass 2: annotation is already set, provisioning is triggered.
			_, err = reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())
			Expect(provisionCalled).To(BeTrue(), "AAP provisioning must be triggered after annotation is stamped")
		})

		It("should stamp default metallb-l2 annotation when spec.implementationStrategy is empty", func() {
			// Create a pool that omits ImplementationStrategy (relies on the default).
			poolNoStrategy := &osacv1alpha1.ExternalIPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pool-no-strategy",
					Namespace: "test-namespace",
				},
				Spec: osacv1alpha1.ExternalIPPoolSpec{
					CIDRs:    []string{"10.10.0.0/24"},
					IPFamily: "IPv4",
					// ImplementationStrategy intentionally omitted
				},
			}
			Expect(fakeClient.Create(testCtx, poolNoStrategy)).To(Succeed())

			key := types.NamespacedName{Name: poolNoStrategy.Name, Namespace: poolNoStrategy.Namespace}

			// Pass 1: finalizer + annotation with defaultExternalIPPoolImplementationStrategy.
			_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())

			annotated := &osacv1alpha1.ExternalIPPool{}
			Expect(fakeClient.Get(testCtx, key, annotated)).To(Succeed())
			Expect(annotated.Annotations).To(HaveKeyWithValue(
				osacImplementationStrategyAnnotation, defaultExternalIPPoolImplementationStrategy,
			))
		})

		It("should set ConfigurationApplied condition to True", func() {
			key := types.NamespacedName{Name: pool.Name, Namespace: pool.Namespace}

			// Pass 1: finalizer, Pass 2: process spec and set condition
			_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())
			_, err = reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())

			updated := &osacv1alpha1.ExternalIPPool{}
			Expect(fakeClient.Get(testCtx, key, updated)).To(Succeed())

			condition := osacv1alpha1.GetExternalIPPoolStatusCondition(
				updated, osacv1alpha1.ExternalIPPoolConditionConfigurationApplied,
			)
			Expect(condition).NotTo(BeNil())
			Expect(condition.Status).To(Equal(metav1.ConditionTrue))
			Expect(condition.Reason).To(Equal("ConfigurationApplied"))
		})

		It("should set phase to Ready on successful provision", func() {
			key := types.NamespacedName{Name: pool.Name, Namespace: pool.Namespace}

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

			updated := &osacv1alpha1.ExternalIPPool{}
			Expect(fakeClient.Get(testCtx, key, updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(osacv1alpha1.ExternalIPPoolPhaseReady))
		})

		It("should set phase to Failed on provision failure", func() {
			key := types.NamespacedName{Name: pool.Name, Namespace: pool.Namespace}

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

			updated := &osacv1alpha1.ExternalIPPool{}
			Expect(fakeClient.Get(testCtx, key, updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(osacv1alpha1.ExternalIPPoolPhaseFailed))
		})

		// Deletion tests call handleDelete directly because the fake client does not
		// support setting DeletionTimestamp via Update. We add the finalizer via a
		// normal Reconcile, then set DeletionTimestamp in memory and call handleDelete.

		It("should wait for child ExternalIP before deprovisioning", func() {
			// Add UUID label to pool so the guard can match children
			const poolUUID = "test-pool-uuid"
			pool.Labels = map[string]string{osacExternalIPPoolIDLabel: poolUUID}
			Expect(fakeClient.Update(testCtx, pool)).To(Succeed())

			childEIP := &osacv1alpha1.ExternalIP{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "child-eip",
					Namespace: pool.Namespace,
				},
				Spec: osacv1alpha1.ExternalIPSpec{
					Pool: poolUUID,
				},
			}
			Expect(fakeClient.Create(testCtx, childEIP)).To(Succeed())

			key := types.NamespacedName{Name: pool.Name, Namespace: pool.Namespace}
			// Add finalizer via normal reconcile
			_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())

			toDelete := &osacv1alpha1.ExternalIPPool{}
			Expect(fakeClient.Get(testCtx, key, toDelete)).To(Succeed())
			now := metav1.Now()
			toDelete.DeletionTimestamp = &now

			result, err := reconciler.handleDelete(testCtx, toDelete)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(defaultPreconditionRequeueInterval))

			// Clean up child, verify deprovision proceeds
			Expect(fakeClient.Delete(testCtx, childEIP)).To(Succeed())

			deprovisionCalled := false
			mockProvider.triggerDeprovisionFunc = func(
				ctx context.Context, resource client.Object, _ []osacv1alpha1.JobStatus,
			) (*provisioning.DeprovisionResult, error) {
				deprovisionCalled = true
				return &provisioning.DeprovisionResult{
					Action:                 provisioning.DeprovisionTriggered,
					JobID:                  "deprovision-after-child-removed",
					BlockDeletionOnFailure: true,
				}, nil
			}

			_, err = reconciler.handleDelete(testCtx, toDelete)
			Expect(err).NotTo(HaveOccurred())
			Expect(deprovisionCalled).To(BeTrue())
		})

		It("should trigger deprovision on delete", func() {
			key := types.NamespacedName{Name: pool.Name, Namespace: pool.Namespace}

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
			toDelete := &osacv1alpha1.ExternalIPPool{}
			Expect(fakeClient.Get(testCtx, key, toDelete)).To(Succeed())
			now := metav1.Now()
			toDelete.DeletionTimestamp = &now

			_, err = reconciler.handleDelete(testCtx, toDelete)
			Expect(err).NotTo(HaveOccurred())

			Expect(deprovisionCalled).To(BeTrue())

			latestJob := provisioning.FindLatestJobByType(toDelete.Status.ProvisioningJobs, osacv1alpha1.JobTypeDeprovision)
			Expect(latestJob).NotTo(BeNil())
			Expect(latestJob.JobID).To(Equal("deprovision-job-123"))
		})

		It("should remove finalizer after successful deprovision", func() {
			key := types.NamespacedName{Name: pool.Name, Namespace: pool.Namespace}

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

			// Add finalizer first
			_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())

			toDelete := &osacv1alpha1.ExternalIPPool{}
			Expect(fakeClient.Get(testCtx, key, toDelete)).To(Succeed())

			now := metav1.Now()
			toDelete.DeletionTimestamp = &now

			// First handleDelete triggers deprovision job
			_, err = reconciler.handleDelete(testCtx, toDelete)
			Expect(err).NotTo(HaveOccurred())

			// Second handleDelete polls status and removes finalizer
			_, _ = reconciler.handleDelete(testCtx, toDelete)

			// Verify finalizer was removed from in-memory object
			Expect(toDelete.Finalizers).NotTo(ContainElement(osacExternalIPPoolFinalizer))
		})

		It("should still handle delete for unmanaged pool with finalizer", func() {
			// Edge case: a pool was managed (has finalizer), then an admin marked
			// it unmanaged. The management-state guard in Reconcile skips processing
			// for non-deleted resources, but deletion must still proceed to clean up
			// the AAP-provisioned resources and remove the finalizer.
			managedThenUnmanaged := &osacv1alpha1.ExternalIPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "managed-then-unmanaged",
					Namespace: "test-namespace",
					Annotations: map[string]string{
						osacManagementStateAnnotation: ManagementStateUnmanaged,
					},
					Finalizers: []string{osacExternalIPPoolFinalizer},
				},
				Spec: osacv1alpha1.ExternalIPPoolSpec{

					CIDRs:                  []string{"10.0.0.0/24"},
					IPFamily:               "IPv4",
					ImplementationStrategy: "metallb-l2",
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

			// Fetch from the fake client, then set DeletionTimestamp in memory
			// and call handleDelete directly (fake client does not support
			// DeletionTimestamp via Update, so we test the in-memory behavior).
			fetched := &osacv1alpha1.ExternalIPPool{}
			Expect(fakeClient.Get(testCtx, key, fetched)).To(Succeed())

			now := metav1.Now()
			fetched.DeletionTimestamp = &now

			// Verify the Reconcile guard: with DeletionTimestamp set, the
			// unmanaged annotation must NOT cause an early return.
			// DeletionTimestamp.IsZero() is false, so the guard is skipped
			// and we reach the delete branch.
			Expect(fetched.ObjectMeta.DeletionTimestamp.IsZero()).To(BeFalse())

			// Call handleDelete directly. The DeprovisionSkipped path removes
			// the finalizer in memory. The subsequent r.Update will fail on
			// the fake client (DeletionTimestamp is immutable), but the
			// important assertion is that deprovision ran and the finalizer
			// was removed from the in-memory object.
			_, _ = reconciler.handleDelete(testCtx, fetched)

			Expect(deprovisionCalled).To(BeTrue())
			Expect(fetched.Finalizers).NotTo(ContainElement(osacExternalIPPoolFinalizer))
		})

		It("should ignore pool with management-state unmanaged annotation", func() {
			// When a pool has the unmanaged annotation and is NOT being deleted,
			// the controller should skip it entirely: no finalizer, no phase change.
			unmanagedPool := &osacv1alpha1.ExternalIPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "unmanaged-pool",
					Namespace: "test-namespace",
					Annotations: map[string]string{
						osacManagementStateAnnotation: ManagementStateUnmanaged,
					},
				},
				Spec: osacv1alpha1.ExternalIPPoolSpec{

					CIDRs:                  []string{"10.0.0.0/24"},
					IPFamily:               "IPv4",
					ImplementationStrategy: "metallb-l2",
				},
			}
			Expect(fakeClient.Create(testCtx, unmanagedPool)).To(Succeed())

			key := types.NamespacedName{Name: unmanagedPool.Name, Namespace: unmanagedPool.Namespace}
			_, err := reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())

			updated := &osacv1alpha1.ExternalIPPool{}
			Expect(fakeClient.Get(testCtx, key, updated)).To(Succeed())

			// Verify no finalizer was added (controller skipped it)
			Expect(updated.Finalizers).To(BeEmpty())
			Expect(updated.Status.Phase).To(BeEmpty())
		})
	})

	Context("provisioning condition updates", func() {
		It("should set Ready=False condition with error message when job fails", func() {
			pool.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
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

			_, err := reconciler.handleProvisioning(ctx, pool)
			Expect(err).NotTo(HaveOccurred())

			cond := apimeta.FindStatusCondition(pool.Status.Conditions, osacv1alpha1.ConditionReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(osacv1alpha1.ReasonProvisioningFailed))
			Expect(cond.Message).To(ContainSubstring("Ansible traceback"))
		})

		It("should set Ready=True condition when job succeeds", func() {
			pool.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
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

			_, err := reconciler.handleProvisioning(ctx, pool)
			Expect(err).NotTo(HaveOccurred())

			cond := apimeta.FindStatusCondition(pool.Status.Conditions, osacv1alpha1.ConditionReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(osacv1alpha1.ReasonAsExpected))
		})

		It("should clear stale Ready=False condition on provisioning recovery", func() {
			pool.Status.Conditions = []metav1.Condition{
				{
					Type:               osacv1alpha1.ConditionReady,
					Status:             metav1.ConditionFalse,
					Reason:             osacv1alpha1.ReasonProvisioningFailed,
					Message:            "previous failure",
					LastTransitionTime: metav1.Now(),
				},
			}
			pool.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
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

			_, err := reconciler.handleProvisioning(ctx, pool)
			Expect(err).NotTo(HaveOccurred())

			Expect(pool.Status.Phase).To(Equal(osacv1alpha1.ExternalIPPoolPhaseReady))
			cond := apimeta.FindStatusCondition(pool.Status.Conditions, osacv1alpha1.ConditionReady)
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
			pool.Status.DesiredConfigVersion = testConfigVersion
			pool.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
				{
					JobID:         "failed-job",
					Type:          osacv1alpha1.JobTypeProvision,
					Timestamp:     metav1.NewTime(time.Now().UTC()),
					State:         osacv1alpha1.JobStateFailed,
					Message:       "provision failed",
					ConfigVersion: testConfigVersion,
				},
			}

			result, err := reconciler.handleProvisioning(testCtx, pool)
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

			pool.Status.DesiredConfigVersion = testConfigVersionUpdated
			pool.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
				{
					JobID:         "failed-job",
					Type:          osacv1alpha1.JobTypeProvision,
					Timestamp:     metav1.NewTime(time.Now().UTC().Add(-2 * time.Second)),
					State:         osacv1alpha1.JobStateFailed,
					Message:       "provision failed",
					ConfigVersion: testConfigVersion,
				},
			}

			result, err := reconciler.handleProvisioning(testCtx, pool)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(1 * time.Second))

			latestJob := provisioning.FindLatestJobByType(pool.Status.ProvisioningJobs, osacv1alpha1.JobTypeProvision)
			Expect(latestJob).NotTo(BeNil())
			Expect(latestJob.JobID).To(Equal("retry-job"))
		})
	})
})
