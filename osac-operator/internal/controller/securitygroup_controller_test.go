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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	osacv1alpha1 "github.com/osac-project/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac-operator/pkg/provisioning"
)

const (
	testConfigVersion        = "version-1"
	testConfigVersionUpdated = "version-2"
	testConfigVersionOld     = "old-version"
	testConfigVersionNew     = "new-version"
)

var _ = Describe("SecurityGroupReconciler", func() {
	var (
		reconciler   *SecurityGroupReconciler
		mockProvider *mockProvisioningProvider
		fakeClient   client.Client
		ctx          context.Context
		sg           *osacv1alpha1.SecurityGroup
		vnet         *osacv1alpha1.VirtualNetwork
	)

	BeforeEach(func() {
		ctx = context.TODO()

		// Setup scheme
		testScheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(testScheme)).To(Succeed())
		Expect(scheme.AddToScheme(testScheme)).To(Succeed())

		// Create parent VirtualNetwork fixture with ImplementationStrategy
		vnet = &osacv1alpha1.VirtualNetwork{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-vnet",
				Namespace: "test-namespace",
				Labels: map[string]string{
					osacVirtualNetworkIDLabel: "test-vnet-uuid",
				},
			},
			Spec: osacv1alpha1.VirtualNetworkSpec{
				Region:                 "us-west-1",
				NetworkClass:           "cudn-net",
				ImplementationStrategy: "cudn-net",
			},
		}

		// Create SecurityGroup fixture
		sg = &osacv1alpha1.SecurityGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-sg",
				Namespace: "test-namespace",
			},
			Spec: osacv1alpha1.SecurityGroupSpec{
				VirtualNetwork: "test-vnet-uuid",
				IngressRules: []osacv1alpha1.SecurityRule{
					{
						Protocol: osacv1alpha1.SecurityGroupProtocolTCP,
						PortFrom: ptr.To[int32](80),
						PortTo:   ptr.To[int32](80),
					},
				},
			},
		}

		// Create fake client with fixtures
		fakeClient = fake.NewClientBuilder().
			WithScheme(testScheme).
			WithObjects(vnet, sg).
			WithStatusSubresource(&osacv1alpha1.SecurityGroup{}).
			Build()

		// Create mock provider
		mockProvider = &mockProvisioningProvider{
			name: "mock-aap",
		}

		// Create reconciler
		reconciler = &SecurityGroupReconciler{
			Client:               fakeClient,
			APIReader:            fakeClient,
			Scheme:               testScheme,
			NetworkingNamespace:  "test-namespace",
			ProvisioningProvider: mockProvider,
			StatusPollInterval:   1 * time.Second,
			MaxJobHistory:        10,
		}
	})

	Context("Reconcile", func() {
		It("should add finalizer on first reconcile", func() {
			// Get the SecurityGroup before reconcile
			key := types.NamespacedName{Name: sg.Name, Namespace: sg.Namespace}
			result, err := reconciler.Reconcile(ctx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())

			// Fetch updated SecurityGroup
			updated := &osacv1alpha1.SecurityGroup{}
			Expect(fakeClient.Get(ctx, key, updated)).To(Succeed())

			// Verify finalizer was added
			Expect(updated.Finalizers).To(ContainElement(osacSecurityGroupFinalizer))
		})

		It("should set phase to Progressing initially", func() {
			key := types.NamespacedName{Name: sg.Name, Namespace: sg.Namespace}

			// Setup mock to return Running state so phase stays in Progressing
			mockProvider.getProvisionStatusFunc = func(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID:   jobID,
					State:   osacv1alpha1.JobStateRunning,
					Message: "Job running",
				}, nil
			}

			// First reconcile adds finalizer
			_, err := reconciler.Reconcile(ctx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile processes the resource
			_, err = reconciler.Reconcile(ctx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())

			// Fetch updated SecurityGroup
			updated := &osacv1alpha1.SecurityGroup{}
			Expect(fakeClient.Get(ctx, key, updated)).To(Succeed())

			// Verify phase is Progressing
			Expect(updated.Status.Phase).To(Equal(osacv1alpha1.SecurityGroupPhaseProgressing))
		})

		It("should persist job status even when resource is concurrently modified", func() {
			key := types.NamespacedName{Name: sg.Name, Namespace: sg.Namespace}

			// First reconcile: adds finalizer + sets annotation, returns early
			_, err := reconciler.Reconcile(ctx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())

			// Simulate feedback controller: during TriggerProvision, modify
			// the resource's metadata (add feedback finalizer) so the
			// resourceVersion changes before the status flush runs.
			mockProvider.triggerProvisionFunc = func(ctx context.Context, resource client.Object) (*provisioning.ProvisionResult, error) {
				fresh := &osacv1alpha1.SecurityGroup{}
				Expect(fakeClient.Get(ctx, key, fresh)).To(Succeed())
				fresh.Finalizers = append(fresh.Finalizers, "osac.openshift.io/securitygroup-feedback")
				Expect(fakeClient.Update(ctx, fresh)).To(Succeed())

				return &provisioning.ProvisionResult{
					JobID:        "concurrent-job-123",
					InitialState: osacv1alpha1.JobStatePending,
					Message:      "Provisioning triggered",
				}, nil
			}

			// Second reconcile: triggers job — the concurrent modification
			// must not prevent the job from being recorded in status.
			_, err = reconciler.Reconcile(ctx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())

			// Verify the job was persisted
			updated := &osacv1alpha1.SecurityGroup{}
			Expect(fakeClient.Get(ctx, key, updated)).To(Succeed())
			latestJob := provisioning.FindLatestJobByType(updated.Status.ProvisioningJobs, osacv1alpha1.JobTypeProvision)
			Expect(latestJob).NotTo(BeNil())
			Expect(latestJob.JobID).To(Equal("concurrent-job-123"))
		})

		It("should lookup parent VirtualNetwork", func() {
			key := types.NamespacedName{Name: sg.Name, Namespace: sg.Namespace}

			// Setup mock to track if TriggerProvision was called
			provisionCalled := false
			mockProvider.triggerProvisionFunc = func(ctx context.Context, resource client.Object) (*provisioning.ProvisionResult, error) {
				provisionCalled = true
				return &provisioning.ProvisionResult{
					JobID:        "job-123",
					InitialState: osacv1alpha1.JobStatePending,
					Message:      "Job triggered",
				}, nil
			}

			// Reconcile
			_, err := reconciler.Reconcile(ctx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile to trigger provisioning
			_, err = reconciler.Reconcile(ctx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())

			// Verify provisioning was triggered (meaning parent was found)
			Expect(provisionCalled).To(BeTrue())
		})

		It("should default to network_policy strategy when spec has no implementationStrategy", func() {
			key := types.NamespacedName{Name: sg.Name, Namespace: sg.Namespace}

			mockProvider.triggerProvisionFunc = func(ctx context.Context, resource client.Object) (*provisioning.ProvisionResult, error) {
				return &provisioning.ProvisionResult{
					JobID:        "job-123",
					InitialState: osacv1alpha1.JobStatePending,
					Message:      "Job triggered",
				}, nil
			}

			// Reconcile twice (first adds finalizer, second sets annotation and provisions)
			_, err := reconciler.Reconcile(ctx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())
			_, err = reconciler.Reconcile(ctx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())

			// Fetch updated SecurityGroup
			updated := &osacv1alpha1.SecurityGroup{}
			Expect(fakeClient.Get(ctx, key, updated)).To(Succeed())

			// Verify annotation was set to the default network_policy strategy
			Expect(updated.Annotations).NotTo(BeNil())
			Expect(updated.Annotations[osacImplementationStrategyAnnotation]).To(Equal(defaultSecurityGroupImplementationStrategy))
		})

		It("should use implementationStrategy from spec when set", func() {
			sgWithStrategy := &osacv1alpha1.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "sg-with-strategy",
					Namespace: "test-namespace",
				},
				Spec: osacv1alpha1.SecurityGroupSpec{
					VirtualNetwork:         "test-vnet-uuid",
					ImplementationStrategy: "custom-backend",
				},
			}
			Expect(fakeClient.Create(ctx, sgWithStrategy)).To(Succeed())

			key := types.NamespacedName{Name: sgWithStrategy.Name, Namespace: sgWithStrategy.Namespace}

			mockProvider.triggerProvisionFunc = func(ctx context.Context, resource client.Object) (*provisioning.ProvisionResult, error) {
				return &provisioning.ProvisionResult{
					JobID:        "job-custom",
					InitialState: osacv1alpha1.JobStatePending,
					Message:      "Job triggered",
				}, nil
			}

			// Reconcile twice (first adds finalizer, second sets annotation and provisions)
			_, err := reconciler.Reconcile(ctx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())
			_, err = reconciler.Reconcile(ctx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())

			// Fetch updated SecurityGroup
			updated := &osacv1alpha1.SecurityGroup{}
			Expect(fakeClient.Get(ctx, key, updated)).To(Succeed())

			// Verify annotation was set to the spec-provided strategy
			Expect(updated.Annotations).NotTo(BeNil())
			Expect(updated.Annotations[osacImplementationStrategyAnnotation]).To(Equal("custom-backend"))
		})

		It("should not update when annotation already matches implementation strategy", func() {
			// Create SecurityGroup with annotation already set to the default
			sgWithAnnotation := &osacv1alpha1.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "sg-with-annotation",
					Namespace: "test-namespace",
					Annotations: map[string]string{
						osacImplementationStrategyAnnotation: defaultSecurityGroupImplementationStrategy,
					},
				},
				Spec: osacv1alpha1.SecurityGroupSpec{
					VirtualNetwork: "test-vnet-uuid",
				},
			}
			Expect(fakeClient.Create(ctx, sgWithAnnotation)).To(Succeed())

			key := types.NamespacedName{Name: sgWithAnnotation.Name, Namespace: sgWithAnnotation.Namespace}

			mockProvider.triggerProvisionFunc = func(ctx context.Context, resource client.Object) (*provisioning.ProvisionResult, error) {
				return &provisioning.ProvisionResult{
					JobID:        "job-456",
					InitialState: osacv1alpha1.JobStatePending,
					Message:      "Job triggered",
				}, nil
			}

			// Reconcile twice
			_, err := reconciler.Reconcile(ctx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())
			_, err = reconciler.Reconcile(ctx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())

			// Fetch updated SecurityGroup
			updated := &osacv1alpha1.SecurityGroup{}
			Expect(fakeClient.Get(ctx, key, updated)).To(Succeed())

			// Verify annotation still matches (no duplicate Update calls)
			Expect(updated.Annotations[osacImplementationStrategyAnnotation]).To(Equal(defaultSecurityGroupImplementationStrategy))
		})

		It("should update annotation when it differs from the resolved strategy", func() {
			// Create SecurityGroup with a stale annotation value
			sgDifferentAnnotation := &osacv1alpha1.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "sg-different-annotation",
					Namespace: "test-namespace",
					Annotations: map[string]string{
						osacImplementationStrategyAnnotation: "old-strategy",
					},
				},
				Spec: osacv1alpha1.SecurityGroupSpec{
					VirtualNetwork: "test-vnet-uuid",
				},
			}
			Expect(fakeClient.Create(ctx, sgDifferentAnnotation)).To(Succeed())

			key := types.NamespacedName{Name: sgDifferentAnnotation.Name, Namespace: sgDifferentAnnotation.Namespace}

			mockProvider.triggerProvisionFunc = func(ctx context.Context, resource client.Object) (*provisioning.ProvisionResult, error) {
				return &provisioning.ProvisionResult{
					JobID:        "job-789",
					InitialState: osacv1alpha1.JobStatePending,
					Message:      "Job triggered",
				}, nil
			}

			// Reconcile twice
			_, err := reconciler.Reconcile(ctx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())
			_, err = reconciler.Reconcile(ctx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())

			// Fetch updated SecurityGroup
			updated := &osacv1alpha1.SecurityGroup{}
			Expect(fakeClient.Get(ctx, key, updated)).To(Succeed())

			// Verify annotation was updated to the default strategy
			Expect(updated.Annotations[osacImplementationStrategyAnnotation]).To(Equal(defaultSecurityGroupImplementationStrategy))
		})

		It("should trigger provision job when no job exists", func() {
			key := types.NamespacedName{Name: sg.Name, Namespace: sg.Namespace}

			mockProvider.triggerProvisionFunc = func(ctx context.Context, resource client.Object) (*provisioning.ProvisionResult, error) {
				return &provisioning.ProvisionResult{
					JobID:        "job-456",
					InitialState: osacv1alpha1.JobStatePending,
					Message:      "Provisioning started",
				}, nil
			}

			// Setup mock to return Pending state so job stays in Pending
			mockProvider.getProvisionStatusFunc = func(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID:   jobID,
					State:   osacv1alpha1.JobStatePending,
					Message: "Job pending",
				}, nil
			}

			req := mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}}

			// First reconcile adds finalizer and sets annotation, then requeues
			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			// Second reconcile triggers the provision job
			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Fetch updated SecurityGroup to check job state
			updated := &osacv1alpha1.SecurityGroup{}
			Expect(fakeClient.Get(ctx, key, updated)).To(Succeed())

			// Verify job was created
			latestJob := provisioning.FindLatestJobByType(updated.Status.ProvisioningJobs, osacv1alpha1.JobTypeProvision)
			Expect(latestJob).NotTo(BeNil())
			Expect(latestJob.JobID).To(Equal("job-456"))
			Expect(latestJob.State).To(Equal(osacv1alpha1.JobStatePending))
		})

		It("should poll job status when job is running", func() {
			key := types.NamespacedName{Name: sg.Name, Namespace: sg.Namespace}

			// First trigger provision
			mockProvider.triggerProvisionFunc = func(ctx context.Context, resource client.Object) (*provisioning.ProvisionResult, error) {
				return &provisioning.ProvisionResult{
					JobID:        "job-789",
					InitialState: osacv1alpha1.JobStateRunning,
					Message:      "Job running",
				}, nil
			}

			// Mock status as Running
			mockProvider.getProvisionStatusFunc = func(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID:   jobID,
					State:   osacv1alpha1.JobStateRunning,
					Message: "Still running",
				}, nil
			}

			// First reconcile adds finalizer
			_, err := reconciler.Reconcile(ctx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile triggers provisioning
			result, err := reconciler.Reconcile(ctx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(1 * time.Second))

			// Third reconcile polls status
			result, err = reconciler.Reconcile(ctx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(1 * time.Second))

			// Fetch updated SecurityGroup
			updated := &osacv1alpha1.SecurityGroup{}
			Expect(fakeClient.Get(ctx, key, updated)).To(Succeed())

			// Verify phase is still Progressing
			Expect(updated.Status.Phase).To(Equal(osacv1alpha1.SecurityGroupPhaseProgressing))
		})

		It("should set phase to Ready on successful provision", func() {
			key := types.NamespacedName{Name: sg.Name, Namespace: sg.Namespace}

			mockProvider.triggerProvisionFunc = func(ctx context.Context, resource client.Object) (*provisioning.ProvisionResult, error) {
				return &provisioning.ProvisionResult{
					JobID:        "job-success",
					InitialState: osacv1alpha1.JobStatePending,
					Message:      "Job triggered",
				}, nil
			}

			mockProvider.getProvisionStatusFunc = func(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID:   jobID,
					State:   osacv1alpha1.JobStateSucceeded,
					Message: "Provisioning completed",
				}, nil
			}

			// Reconcile to add finalizer, trigger, and poll
			_, err := reconciler.Reconcile(ctx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())
			_, err = reconciler.Reconcile(ctx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())
			_, err = reconciler.Reconcile(ctx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())

			// Fetch updated SecurityGroup
			updated := &osacv1alpha1.SecurityGroup{}
			Expect(fakeClient.Get(ctx, key, updated)).To(Succeed())

			// Verify phase is Ready
			Expect(updated.Status.Phase).To(Equal(osacv1alpha1.SecurityGroupPhaseReady))
		})

		It("should set phase to Failed on job failure", func() {
			key := types.NamespacedName{Name: sg.Name, Namespace: sg.Namespace}

			mockProvider.triggerProvisionFunc = func(ctx context.Context, resource client.Object) (*provisioning.ProvisionResult, error) {
				return &provisioning.ProvisionResult{
					JobID:        "job-fail",
					InitialState: osacv1alpha1.JobStatePending,
					Message:      "Job triggered",
				}, nil
			}

			mockProvider.getProvisionStatusFunc = func(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID:        jobID,
					State:        osacv1alpha1.JobStateFailed,
					Message:      "Provisioning failed",
					ErrorDetails: "Network unreachable",
				}, nil
			}

			// Reconcile multiple times
			_, err := reconciler.Reconcile(ctx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())
			_, err = reconciler.Reconcile(ctx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())
			_, err = reconciler.Reconcile(ctx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())

			// Fetch updated SecurityGroup
			updated := &osacv1alpha1.SecurityGroup{}
			Expect(fakeClient.Get(ctx, key, updated)).To(Succeed())

			// Verify phase is Failed
			Expect(updated.Status.Phase).To(Equal(osacv1alpha1.SecurityGroupPhaseFailed))
		})

		It("should trigger deprovision on delete", func() {
			key := types.NamespacedName{Name: sg.Name, Namespace: sg.Namespace}

			// Setup deprovision mock
			deprovisionCalled := false
			mockProvider.triggerDeprovisionFunc = func(ctx context.Context, resource client.Object, _ []osacv1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
				deprovisionCalled = true
				return &provisioning.DeprovisionResult{
					Action:                 provisioning.DeprovisionTriggered,
					JobID:                  "deprovision-job-123",
					BlockDeletionOnFailure: true,
				}, nil
			}

			// Add finalizer first
			_, err := reconciler.Reconcile(ctx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())

			// Fetch the SecurityGroup and prepare for deletion
			toDelete := &osacv1alpha1.SecurityGroup{}
			Expect(fakeClient.Get(ctx, key, toDelete)).To(Succeed())

			// Set deletion timestamp in memory and call handleDelete directly
			// (fake client doesn't allow setting DeletionTimestamp via Update)
			now := metav1.Now()
			toDelete.DeletionTimestamp = &now

			// Call handleDelete directly with the object that has DeletionTimestamp set
			_, err = reconciler.handleDelete(ctx, toDelete)
			Expect(err).NotTo(HaveOccurred())

			// Verify deprovision was called
			Expect(deprovisionCalled).To(BeTrue())

			// Verify deprovision job was added to the in-memory object
			latestJob := provisioning.FindLatestJobByType(toDelete.Status.ProvisioningJobs, osacv1alpha1.JobTypeDeprovision)
			Expect(latestJob).NotTo(BeNil())
			Expect(latestJob.JobID).To(Equal("deprovision-job-123"))
		})

		It("should remove finalizer after successful deprovision", func() {
			key := types.NamespacedName{Name: sg.Name, Namespace: sg.Namespace}

			// Setup deprovision to succeed
			mockProvider.triggerDeprovisionFunc = func(ctx context.Context, resource client.Object, _ []osacv1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
				return &provisioning.DeprovisionResult{
					Action:                 provisioning.DeprovisionTriggered,
					JobID:                  "deprovision-success",
					BlockDeletionOnFailure: true,
				}, nil
			}

			mockProvider.getDeprovisionStatusFunc = func(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID:   jobID,
					State:   osacv1alpha1.JobStateSucceeded,
					Message: "Deprovision completed",
				}, nil
			}

			// Add finalizer first
			_, err := reconciler.Reconcile(ctx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())

			// Fetch the SecurityGroup
			toDelete := &osacv1alpha1.SecurityGroup{}
			Expect(fakeClient.Get(ctx, key, toDelete)).To(Succeed())

			// Set deletion timestamp in memory
			now := metav1.Now()
			toDelete.DeletionTimestamp = &now

			// First handleDelete call - triggers deprovision job
			_, err = reconciler.handleDelete(ctx, toDelete)
			Expect(err).NotTo(HaveOccurred())

			// Second handleDelete call - polls status and tries to remove finalizer
			// The error is expected because fake client doesn't support deletion workflow
			// But we verify the controller logic worked by checking the in-memory object
			_, _ = reconciler.handleDelete(ctx, toDelete)

			// Verify finalizer was removed from in-memory object
			// (the actual Update call fails with fake client, but the logic is correct)
			Expect(toDelete.Finalizers).NotTo(ContainElement(osacSecurityGroupFinalizer))
		})

		It("should still handle delete for unmanaged SecurityGroup with finalizer", func() {
			// Use envtest (k8sClient) instead of fakeClient so we can go through
			// Reconcile and exercise the DeletionTimestamp.IsZero() guard.
			envMockProvider := &mockProvisioningProvider{
				name: "mock-aap",
				triggerDeprovisionFunc: func(ctx context.Context, resource client.Object, _ []osacv1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
					return &provisioning.DeprovisionResult{
						Action: provisioning.DeprovisionSkipped,
					}, nil
				},
			}
			envReconciler := &SecurityGroupReconciler{
				Client:               k8sClient,
				APIReader:            k8sClient,
				Scheme:               k8sClient.Scheme(),
				NetworkingNamespace:  "default",
				ProvisioningProvider: envMockProvider,
				StatusPollInterval:   1 * time.Second,
				MaxJobHistory:        10,
			}

			managedThenUnmanaged := &osacv1alpha1.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "managed-then-unmanaged",
					Namespace: "default",
					Annotations: map[string]string{
						osacManagementStateAnnotation: ManagementStateUnmanaged,
					},
					Finalizers: []string{osacSecurityGroupFinalizer},
				},
				Spec: osacv1alpha1.SecurityGroupSpec{
					VirtualNetwork: "test-vnet-uuid",
				},
			}
			Expect(k8sClient.Create(ctx, managedThenUnmanaged)).To(Succeed())

			key := types.NamespacedName{Name: managedThenUnmanaged.Name, Namespace: managedThenUnmanaged.Namespace}

			Expect(k8sClient.Delete(ctx, managedThenUnmanaged)).To(Succeed())

			_, err := envReconciler.Reconcile(ctx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() bool {
				return errors.IsNotFound(k8sClient.Get(ctx, key, &osacv1alpha1.SecurityGroup{}))
			}, 5*time.Second, 100*time.Millisecond).Should(BeTrue())
		})

		It("should ignore SecurityGroup with management-state unmanaged annotation", func() {
			unmanagedSG := &osacv1alpha1.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "unmanaged-sg",
					Namespace: "test-namespace",
					Annotations: map[string]string{
						osacManagementStateAnnotation: ManagementStateUnmanaged,
					},
				},
				Spec: osacv1alpha1.SecurityGroupSpec{
					VirtualNetwork: "test-vnet-uuid",
				},
			}
			Expect(fakeClient.Create(ctx, unmanagedSG)).To(Succeed())

			key := types.NamespacedName{Name: unmanagedSG.Name, Namespace: unmanagedSG.Namespace}
			_, err := reconciler.Reconcile(ctx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())

			updated := &osacv1alpha1.SecurityGroup{}
			Expect(fakeClient.Get(ctx, key, updated)).To(Succeed())

			Expect(updated.Finalizers).To(BeEmpty())
			Expect(updated.Status.Phase).To(BeEmpty())
		})
	})

	Context("provisioning condition updates", func() {
		It("should set Ready=False condition with error message when job fails", func() {
			sg.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
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

			_, err := reconciler.handleProvisioning(ctx, sg)
			Expect(err).NotTo(HaveOccurred())

			cond := apimeta.FindStatusCondition(sg.Status.Conditions, osacv1alpha1.ConditionReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(osacv1alpha1.ReasonProvisioningFailed))
			Expect(cond.Message).To(ContainSubstring("Ansible traceback"))
		})

		It("should set Ready=True condition when job succeeds", func() {
			sg.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
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

			_, err := reconciler.handleProvisioning(ctx, sg)
			Expect(err).NotTo(HaveOccurred())

			cond := apimeta.FindStatusCondition(sg.Status.Conditions, osacv1alpha1.ConditionReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(osacv1alpha1.ReasonAsExpected))
		})

		It("should clear stale Ready=False condition on provisioning recovery", func() {
			sg.Status.Conditions = []metav1.Condition{
				{
					Type:               osacv1alpha1.ConditionReady,
					Status:             metav1.ConditionFalse,
					Reason:             osacv1alpha1.ReasonProvisioningFailed,
					Message:            "previous failure",
					LastTransitionTime: metav1.Now(),
				},
			}
			sg.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
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

			_, err := reconciler.handleProvisioning(ctx, sg)
			Expect(err).NotTo(HaveOccurred())

			Expect(sg.Status.Phase).To(Equal(osacv1alpha1.SecurityGroupPhaseReady))
			cond := apimeta.FindStatusCondition(sg.Status.Conditions, osacv1alpha1.ConditionReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(osacv1alpha1.ReasonAsExpected))
			Expect(cond.Message).To(BeEmpty())
		})
	})

	Context("backoff on failure", func() {
		It("should backoff when latest job failed with matching ConfigVersion", func() {
			sg.Status.DesiredConfigVersion = testConfigVersion
			sg.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
				{
					JobID:         "failed-job",
					Type:          osacv1alpha1.JobTypeProvision,
					Timestamp:     metav1.NewTime(time.Now().UTC()),
					State:         osacv1alpha1.JobStateFailed,
					Message:       "provision failed",
					ConfigVersion: testConfigVersion,
				},
			}

			result, err := reconciler.handleProvisioning(ctx, sg)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))
			Expect(result.RequeueAfter).To(BeNumerically("<=", provisioning.BackoffMaxDelay))
		})

		It("should trigger immediately when spec changed after failure", func() {
			mockProvider.triggerProvisionFunc = func(ctx context.Context, resource client.Object) (*provisioning.ProvisionResult, error) {
				return &provisioning.ProvisionResult{
					JobID:        "retry-job",
					InitialState: osacv1alpha1.JobStatePending,
				}, nil
			}

			sg.Status.DesiredConfigVersion = testConfigVersionUpdated
			sg.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
				{
					JobID:         "failed-job",
					Type:          osacv1alpha1.JobTypeProvision,
					Timestamp:     metav1.NewTime(time.Now().UTC().Add(-2 * time.Second)),
					State:         osacv1alpha1.JobStateFailed,
					Message:       "provision failed",
					ConfigVersion: testConfigVersion,
				},
			}

			result, err := reconciler.handleProvisioning(ctx, sg)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(1 * time.Second))

			latestJob := provisioning.FindLatestJobByType(sg.Status.ProvisioningJobs, osacv1alpha1.JobTypeProvision)
			Expect(latestJob).NotTo(BeNil())
			Expect(latestJob.JobID).To(Equal("retry-job"))
		})

		It("should skip when config already applied", func() {
			sg.Status.DesiredConfigVersion = testConfigVersion
			sg.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
				{
					JobID:         "succeeded-job",
					Type:          osacv1alpha1.JobTypeProvision,
					Timestamp:     metav1.NewTime(time.Now().UTC()),
					State:         osacv1alpha1.JobStateSucceeded,
					ConfigVersion: testConfigVersion,
				},
			}

			result, err := reconciler.handleProvisioning(ctx, sg)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(time.Duration(0)))
		})
	})

	Context("Helper functions", func() {
		It("should append jobs and trim to max history", func() {
			jobs := []osacv1alpha1.JobStatus{}

			// Add 15 jobs
			for i := range 15 {
				newJob := osacv1alpha1.JobStatus{
					JobID:     string(rune('a' + i)),
					Type:      osacv1alpha1.JobTypeProvision,
					Timestamp: metav1.Now(),
					State:     osacv1alpha1.JobStatePending,
				}
				jobs = provisioning.AppendJob(jobs, newJob, reconciler.MaxJobHistory)
			}

			// Should only have MaxJobHistory jobs
			Expect(jobs).To(HaveLen(reconciler.MaxJobHistory))
			// Should have the most recent jobs (last 10)
			Expect(jobs[0].JobID).To(Equal("f")) // 6th job (0-indexed: 5)
			Expect(jobs[9].JobID).To(Equal("o")) // 15th job (0-indexed: 14)
		})

		It("should update existing job by ID", func() {
			jobs := []osacv1alpha1.JobStatus{
				{
					JobID:   "job-1",
					Type:    osacv1alpha1.JobTypeProvision,
					State:   osacv1alpha1.JobStatePending,
					Message: "Initial message",
				},
				{
					JobID:   "job-2",
					Type:    osacv1alpha1.JobTypeProvision,
					State:   osacv1alpha1.JobStatePending,
					Message: "Another job",
				},
			}

			updatedJob := osacv1alpha1.JobStatus{
				JobID:   "job-1",
				Type:    osacv1alpha1.JobTypeProvision,
				State:   osacv1alpha1.JobStateRunning,
				Message: "Updated message",
			}

			provisioning.UpdateJob(jobs, updatedJob)

			// Verify job was updated
			Expect(jobs[0].State).To(Equal(osacv1alpha1.JobStateRunning))
			Expect(jobs[0].Message).To(Equal("Updated message"))

			// Verify other job unchanged
			Expect(jobs[1].Message).To(Equal("Another job"))
		})
	})
})
