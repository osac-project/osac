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
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	osacv1alpha1 "github.com/osac-project/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac-operator/pkg/provisioning"
)

var _ = Describe("SubnetReconciler", func() {
	var (
		reconciler   *SubnetReconciler
		mockProvider *mockSubnetProvider
		ctx          context.Context
		subnet       *osacv1alpha1.Subnet
		vnet         *osacv1alpha1.VirtualNetwork
	)

	BeforeEach(func() {
		ctx = context.TODO()
		mockProvider = &mockSubnetProvider{}
		reconciler = &SubnetReconciler{
			Client:               k8sClient,
			APIReader:            k8sClient,
			Scheme:               k8sClient.Scheme(),
			NetworkingNamespace:  "default",
			ProvisioningProvider: mockProvider,
			StatusPollInterval:   1 * time.Second,
			MaxJobHistory:        10,
		}

		// Create VirtualNetwork fixture with ImplementationStrategy set
		vnet = &osacv1alpha1.VirtualNetwork{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-vnet",
				Namespace: "default",
				Labels: map[string]string{
					osacVirtualNetworkIDLabel: "test-vnet-uuid",
				},
			},
			Spec: osacv1alpha1.VirtualNetworkSpec{
				Region:                 "us-west-1",
				IPv4CIDR:               "10.0.0.0/16",
				NetworkClass:           "cudn-net",
				ImplementationStrategy: "cudn-net",
			},
		}
		Expect(k8sClient.Create(ctx, vnet)).To(Succeed())

		// Create Subnet fixture
		subnet = &osacv1alpha1.Subnet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-subnet",
				Namespace: "default",
			},
			Spec: osacv1alpha1.SubnetSpec{
				VirtualNetwork: "test-vnet-uuid",
				IPv4CIDR:       "10.0.1.0/24",
			},
		}
	})

	AfterEach(func() {
		// Cleanup VirtualNetwork
		vnetKey := types.NamespacedName{Name: vnet.Name, Namespace: vnet.Namespace}
		existingVnet := &osacv1alpha1.VirtualNetwork{}
		if err := k8sClient.Get(ctx, vnetKey, existingVnet); err == nil {
			existingVnet.Finalizers = nil
			_ = k8sClient.Update(ctx, existingVnet)
			_ = k8sClient.Delete(ctx, existingVnet)
		}

		// Cleanup Subnet if it exists
		subnetKey := types.NamespacedName{Name: subnet.Name, Namespace: subnet.Namespace}
		existingSubnet := &osacv1alpha1.Subnet{}
		if err := k8sClient.Get(ctx, subnetKey, existingSubnet); err == nil {
			existingSubnet.Finalizers = nil
			_ = k8sClient.Update(ctx, existingSubnet)
			_ = k8sClient.Delete(ctx, existingSubnet)
		}
	})

	Context("Reconcile", func() {
		It("should add finalizer on first reconcile", func() {
			Expect(k8sClient.Create(ctx, subnet)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      subnet.Name,
					Namespace: subnet.Namespace,
				},
			}})
			Expect(err).NotTo(HaveOccurred())

			// Fetch updated Subnet
			updatedSubnet := &osacv1alpha1.Subnet{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: subnet.Name, Namespace: subnet.Namespace}, updatedSubnet)).To(Succeed())
			Expect(updatedSubnet.Finalizers).To(ContainElement(osacSubnetFinalizer))
		})

		It("should set phase to Progressing on first reconcile", func() {
			Expect(k8sClient.Create(ctx, subnet)).To(Succeed())

			req := mcreconcile.Request{Request: reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      subnet.Name,
					Namespace: subnet.Namespace,
				},
			}}

			// First reconcile sets annotation and requeues
			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			// Second reconcile persists the Progressing phase
			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Fetch updated Subnet
			updatedSubnet := &osacv1alpha1.Subnet{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: subnet.Name, Namespace: subnet.Namespace}, updatedSubnet)).To(Succeed())
			Expect(updatedSubnet.Status.Phase).To(Equal(osacv1alpha1.SubnetPhaseProgressing))
		})

		It("should persist job status even when resource is concurrently modified", func() {
			Expect(k8sClient.Create(ctx, subnet)).To(Succeed())

			req := mcreconcile.Request{Request: reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      subnet.Name,
					Namespace: subnet.Namespace,
				},
			}}

			// First reconcile: adds finalizer + sets annotation, returns early
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Simulate feedback controller: during TriggerProvision, modify
			// the resource's metadata (add feedback finalizer) so the
			// resourceVersion changes before the status flush runs.
			mockProvider.triggerProvisionFunc = func(ctx context.Context, resource client.Object) (*provisioning.ProvisionResult, error) {
				fresh := &osacv1alpha1.Subnet{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: subnet.Name, Namespace: subnet.Namespace}, fresh)).To(Succeed())
				fresh.Finalizers = append(fresh.Finalizers, "osac.openshift.io/subnet-feedback")
				Expect(k8sClient.Update(ctx, fresh)).To(Succeed())

				return &provisioning.ProvisionResult{
					JobID:        "concurrent-job-123",
					InitialState: osacv1alpha1.JobStatePending,
					Message:      "Provisioning triggered",
				}, nil
			}

			// Second reconcile: triggers job — the concurrent modification
			// must not prevent the job from being recorded in status.
			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Verify the job was persisted to the API server
			updatedSubnet := &osacv1alpha1.Subnet{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: subnet.Name, Namespace: subnet.Namespace}, updatedSubnet)).To(Succeed())
			latestJob := provisioning.FindLatestJobByType(updatedSubnet.Status.ProvisioningJobs, osacv1alpha1.JobTypeProvision)
			Expect(latestJob).NotTo(BeNil())
			Expect(latestJob.JobID).To(Equal("concurrent-job-123"))
		})

		It("should requeue when parent VirtualNetwork not found", func() {
			// Create subnet with non-existent parent
			subnetNoParent := &osacv1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "subnet-no-parent",
					Namespace: "default",
				},
				Spec: osacv1alpha1.SubnetSpec{
					VirtualNetwork: "missing-vnet",
					IPv4CIDR:       "10.0.2.0/24",
				},
			}
			Expect(k8sClient.Create(ctx, subnetNoParent)).To(Succeed())

			result, err := reconciler.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      subnetNoParent.Name,
					Namespace: subnetNoParent.Namespace,
				},
			}})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(defaultPreconditionRequeueInterval))

			// Cleanup
			_ = k8sClient.Delete(ctx, subnetNoParent)
		})

		It("should requeue when parent VirtualNetwork has no ImplementationStrategy", func() {
			// Create VirtualNetwork without ImplementationStrategy
			vnetNoStrategy := &osacv1alpha1.VirtualNetwork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "vnet-no-strategy",
					Namespace: "default",
					Labels: map[string]string{
						osacVirtualNetworkIDLabel: "vnet-no-strategy-uuid",
					},
				},
				Spec: osacv1alpha1.VirtualNetworkSpec{
					Region:       "us-west-1",
					IPv4CIDR:     "10.0.0.0/16",
					NetworkClass: "some-class",
					// ImplementationStrategy intentionally not set
				},
			}
			Expect(k8sClient.Create(ctx, vnetNoStrategy)).To(Succeed())

			subnetNoStrategy := &osacv1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "subnet-no-strategy",
					Namespace: "default",
				},
				Spec: osacv1alpha1.SubnetSpec{
					VirtualNetwork: "vnet-no-strategy-uuid",
					IPv4CIDR:       "10.0.3.0/24",
				},
			}
			Expect(k8sClient.Create(ctx, subnetNoStrategy)).To(Succeed())

			result, err := reconciler.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      subnetNoStrategy.Name,
					Namespace: subnetNoStrategy.Namespace,
				},
			}})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(defaultPreconditionRequeueInterval))

			// Cleanup
			_ = k8sClient.Delete(ctx, subnetNoStrategy)
			_ = k8sClient.Delete(ctx, vnetNoStrategy)
		})

		It("should ignore subnet with unmanaged annotation", func() {
			unmanagedSubnet := &osacv1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "unmanaged-subnet",
					Namespace: "default",
					Annotations: map[string]string{
						osacManagementStateAnnotation: ManagementStateUnmanaged,
					},
				},
				Spec: osacv1alpha1.SubnetSpec{
					VirtualNetwork: "test-vnet",
					IPv4CIDR:       "10.0.4.0/24",
				},
			}
			Expect(k8sClient.Create(ctx, unmanagedSubnet)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      unmanagedSubnet.Name,
					Namespace: unmanagedSubnet.Namespace,
				},
			}})
			Expect(err).NotTo(HaveOccurred())

			// Verify status was not updated
			updatedSubnet := &osacv1alpha1.Subnet{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: unmanagedSubnet.Name, Namespace: unmanagedSubnet.Namespace}, updatedSubnet)).To(Succeed())
			Expect(updatedSubnet.Status.Phase).To(BeEmpty())

			// Cleanup
			_ = k8sClient.Delete(ctx, unmanagedSubnet)
		})

		It("should still handle delete for unmanaged subnet with finalizer", func() {
			managedThenUnmanaged := &osacv1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "managed-then-unmanaged",
					Namespace: "default",
					Annotations: map[string]string{
						osacManagementStateAnnotation: ManagementStateUnmanaged,
					},
					Finalizers: []string{osacSubnetFinalizer},
				},
				Spec: osacv1alpha1.SubnetSpec{
					VirtualNetwork: "test-vnet-uuid",
					IPv4CIDR:       "10.0.5.0/24",
				},
			}
			Expect(k8sClient.Create(ctx, managedThenUnmanaged)).To(Succeed())

			key := types.NamespacedName{Name: managedThenUnmanaged.Name, Namespace: managedThenUnmanaged.Namespace}

			mockProvider.triggerDeprovisionFunc = func(
				ctx context.Context, resource client.Object, _ []osacv1alpha1.JobStatus,
			) (*provisioning.DeprovisionResult, error) {
				return &provisioning.DeprovisionResult{
					Action: provisioning.DeprovisionSkipped,
				}, nil
			}

			Expect(k8sClient.Delete(ctx, managedThenUnmanaged)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{
				NamespacedName: key,
			}})
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() bool {
				return errors.IsNotFound(k8sClient.Get(ctx, key, &osacv1alpha1.Subnet{}))
			}, 5*time.Second, 100*time.Millisecond).Should(BeTrue())
		})
	})

	Context("handleProvisioning", func() {
		BeforeEach(func() {
			subnet.Status.Phase = osacv1alpha1.SubnetPhaseProgressing
		})

		It("should trigger provisioning when no job exists", func() {
			mockProvider.triggerProvisionFunc = func(ctx context.Context, resource client.Object) (*provisioning.ProvisionResult, error) {
				return &provisioning.ProvisionResult{
					JobID:        "test-job-123",
					InitialState: osacv1alpha1.JobStatePending,
					Message:      "Provisioning triggered",
				}, nil
			}

			result, err := reconciler.handleProvisioning(ctx, subnet)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(1 * time.Second))

			latestJob := provisioning.FindLatestJobByType(subnet.Status.ProvisioningJobs, osacv1alpha1.JobTypeProvision)
			Expect(latestJob).NotTo(BeNil())
			Expect(latestJob.JobID).To(Equal("test-job-123"))
			Expect(latestJob.State).To(Equal(osacv1alpha1.JobStatePending))
		})

		It("should trigger new job when previous job failed", func() {
			subnet.Status.DesiredConfigVersion = testConfigVersionNew
			subnet.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
				{
					JobID:         "old-failed-job",
					Type:          osacv1alpha1.JobTypeProvision,
					Timestamp:     metav1.NewTime(time.Now().UTC()),
					State:         osacv1alpha1.JobStateFailed,
					Message:       "Previous job failed",
					ConfigVersion: testConfigVersionOld,
				},
			}

			mockProvider.triggerProvisionFunc = func(ctx context.Context, resource client.Object) (*provisioning.ProvisionResult, error) {
				return &provisioning.ProvisionResult{
					JobID:        "new-job-456",
					InitialState: osacv1alpha1.JobStatePending,
					Message:      "Retry provisioning",
				}, nil
			}

			result, err := reconciler.handleProvisioning(ctx, subnet)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(1 * time.Second))

			latestJob := provisioning.FindLatestJobByType(subnet.Status.ProvisioningJobs, osacv1alpha1.JobTypeProvision)
			Expect(latestJob).NotTo(BeNil())
			Expect(latestJob.JobID).To(Equal("new-job-456"))
			Expect(latestJob.State).To(Equal(osacv1alpha1.JobStatePending))
		})

		It("should poll job status when job exists", func() {
			subnet.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
				{
					JobID:     "existing-job-789",
					Type:      osacv1alpha1.JobTypeProvision,
					Timestamp: metav1.NewTime(time.Now().UTC()),
					State:     osacv1alpha1.JobStateRunning,
					Message:   "Job running",
				},
			}

			mockProvider.getProvisionStatusFunc = func(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID:   jobID,
					State:   osacv1alpha1.JobStateRunning,
					Message: "Still running",
				}, nil
			}

			result, err := reconciler.handleProvisioning(ctx, subnet)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(1 * time.Second))

			latestJob := provisioning.FindLatestJobByType(subnet.Status.ProvisioningJobs, osacv1alpha1.JobTypeProvision)
			Expect(latestJob.State).To(Equal(osacv1alpha1.JobStateRunning))
		})

		It("should set phase to Ready when job succeeds", func() {
			subnet.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
				{
					JobID:     "success-job-101",
					Type:      osacv1alpha1.JobTypeProvision,
					Timestamp: metav1.NewTime(time.Now().UTC()),
					State:     osacv1alpha1.JobStateRunning,
					Message:   "Job running",
				},
			}

			mockProvider.getProvisionStatusFunc = func(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID:   jobID,
					State:   osacv1alpha1.JobStateSucceeded,
					Message: "Job succeeded",
				}, nil
			}

			result, err := reconciler.handleProvisioning(ctx, subnet)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(0 * time.Second))
			Expect(subnet.Status.Phase).To(Equal(osacv1alpha1.SubnetPhaseReady))
		})

		It("should set phase to Failed when job fails", func() {
			subnet.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
				{
					JobID:     "failed-job-202",
					Type:      osacv1alpha1.JobTypeProvision,
					Timestamp: metav1.NewTime(time.Now().UTC()),
					State:     osacv1alpha1.JobStateRunning,
					Message:   "Job running",
				},
			}

			mockProvider.getProvisionStatusFunc = func(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID:   jobID,
					State:   osacv1alpha1.JobStateFailed,
					Message: "Job failed",
				}, nil
			}

			result, err := reconciler.handleProvisioning(ctx, subnet)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(0 * time.Second))
			Expect(subnet.Status.Phase).To(Equal(osacv1alpha1.SubnetPhaseFailed))
		})

		It("should set Ready=False condition with error message when job fails", func() {
			subnet.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
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

			_, err := reconciler.handleProvisioning(ctx, subnet)
			Expect(err).NotTo(HaveOccurred())

			cond := apimeta.FindStatusCondition(subnet.Status.Conditions, osacv1alpha1.ConditionReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(osacv1alpha1.SubnetProvisioningFailed))
			Expect(cond.Message).To(ContainSubstring("Ansible traceback"))
		})

		It("should set Ready=True condition when job succeeds", func() {
			subnet.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
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

			_, err := reconciler.handleProvisioning(ctx, subnet)
			Expect(err).NotTo(HaveOccurred())

			cond := apimeta.FindStatusCondition(subnet.Status.Conditions, osacv1alpha1.ConditionReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(osacv1alpha1.ReasonAsExpected))
		})

		It("should clear stale Ready=False condition on provisioning recovery", func() {
			subnet.Status.Conditions = []metav1.Condition{
				{
					Type:               osacv1alpha1.ConditionReady,
					Status:             metav1.ConditionFalse,
					Reason:             osacv1alpha1.SubnetProvisioningFailed,
					Message:            "previous failure",
					LastTransitionTime: metav1.Now(),
				},
			}
			subnet.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
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

			_, err := reconciler.handleProvisioning(ctx, subnet)
			Expect(err).NotTo(HaveOccurred())

			Expect(subnet.Status.Phase).To(Equal(osacv1alpha1.SubnetPhaseReady))
			cond := apimeta.FindStatusCondition(subnet.Status.Conditions, osacv1alpha1.ConditionReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(osacv1alpha1.ReasonAsExpected))
			Expect(cond.Message).To(BeEmpty())
		})
	})

	Context("backoff on failure", func() {
		It("should backoff when latest job failed with matching ConfigVersion", func() {
			subnet.Status.DesiredConfigVersion = testConfigVersion
			subnet.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
				{
					JobID:         "failed-job",
					Type:          osacv1alpha1.JobTypeProvision,
					Timestamp:     metav1.NewTime(time.Now().UTC()),
					State:         osacv1alpha1.JobStateFailed,
					Message:       "provision failed",
					ConfigVersion: testConfigVersion,
				},
			}

			result, err := reconciler.handleProvisioning(ctx, subnet)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))
			Expect(result.RequeueAfter).To(BeNumerically("<=", provisioning.BackoffMaxDelay))
		})

		It("should skip when config already applied", func() {
			subnet.Status.DesiredConfigVersion = testConfigVersion
			subnet.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
				{
					JobID:         "succeeded-job",
					Type:          osacv1alpha1.JobTypeProvision,
					Timestamp:     metav1.NewTime(time.Now().UTC()),
					State:         osacv1alpha1.JobStateSucceeded,
					ConfigVersion: testConfigVersion,
				},
			}

			result, err := reconciler.handleProvisioning(ctx, subnet)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(time.Duration(0)))
		})
	})

	Context("Job history management", func() {
		It("should limit job history to MaxJobHistory", func() {
			reconciler.MaxJobHistory = 3

			// Add 5 jobs
			for i := 1; i <= 5; i++ {
				newJob := osacv1alpha1.JobStatus{
					JobID:     "job-" + string(rune('0'+i)),
					Type:      osacv1alpha1.JobTypeProvision,
					Timestamp: metav1.NewTime(time.Now().UTC().Add(time.Duration(i) * time.Second)),
					State:     osacv1alpha1.JobStatePending,
					Message:   "Job triggered",
				}
				subnet.Status.ProvisioningJobs = provisioning.AppendJob(subnet.Status.ProvisioningJobs, newJob, reconciler.MaxJobHistory)
			}

			// Should only have last 3 jobs
			Expect(subnet.Status.ProvisioningJobs).To(HaveLen(3))
			Expect(subnet.Status.ProvisioningJobs[0].JobID).To(Equal("job-3"))
			Expect(subnet.Status.ProvisioningJobs[1].JobID).To(Equal("job-4"))
			Expect(subnet.Status.ProvisioningJobs[2].JobID).To(Equal("job-5"))
		})
	})

	Context("handleDeprovisioning", func() {
		It("should trigger deprovisioning when no deprovision job exists", func() {
			mockProvider.triggerDeprovisionFunc = func(ctx context.Context, resource client.Object, _ []osacv1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
				return &provisioning.DeprovisionResult{
					Action:                 provisioning.DeprovisionTriggered,
					JobID:                  "deprovision-job-303",
					BlockDeletionOnFailure: true,
				}, nil
			}

			result, err := reconciler.handleDeprovisioning(ctx, subnet)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(1 * time.Second))

			latestJob := provisioning.FindLatestJobByType(subnet.Status.ProvisioningJobs, osacv1alpha1.JobTypeDeprovision)
			Expect(latestJob).NotTo(BeNil())
			Expect(latestJob.JobID).To(Equal("deprovision-job-303"))
			Expect(latestJob.BlockDeletionOnFailure).To(BeTrue())
		})

		It("should skip deprovisioning when provider returns DeprovisionSkipped", func() {
			mockProvider.triggerDeprovisionFunc = func(ctx context.Context, resource client.Object, _ []osacv1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
				return &provisioning.DeprovisionResult{
					Action: provisioning.DeprovisionSkipped,
				}, nil
			}

			result, err := reconciler.handleDeprovisioning(ctx, subnet)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(0 * time.Second))
		})

		It("should poll deprovision job status", func() {
			subnet.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
				{
					JobID:     "deprovision-running-404",
					Type:      osacv1alpha1.JobTypeDeprovision,
					Timestamp: metav1.NewTime(time.Now().UTC()),
					State:     osacv1alpha1.JobStateRunning,
					Message:   "Deprovisioning in progress",
				},
			}

			mockProvider.getDeprovisionStatusFunc = func(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID:   jobID,
					State:   osacv1alpha1.JobStateSucceeded,
					Message: "Deprovision succeeded",
				}, nil
			}

			result, err := reconciler.handleDeprovisioning(ctx, subnet)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(0 * time.Second))
		})

		It("should wait for backoff when deprovision fails with BlockDeletionOnFailure", func() {
			subnet.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
				{
					JobID:                  "deprovision-failed-505",
					Type:                   osacv1alpha1.JobTypeDeprovision,
					Timestamp:              metav1.NewTime(time.Now().UTC()),
					State:                  osacv1alpha1.JobStateRunning,
					Message:                "Deprovisioning in progress",
					BlockDeletionOnFailure: true,
				},
			}

			mockProvider.getDeprovisionStatusFunc = func(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID:   jobID,
					State:   osacv1alpha1.JobStateFailed,
					Message: "Deprovision failed",
				}, nil
			}

			result, err := reconciler.handleDeprovisioning(ctx, subnet)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically("<=", provisioning.BackoffBaseDelay))
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		})
	})
})

// mockSubnetProvider implements the ProvisioningProvider interface for Subnet testing
type mockSubnetProvider struct {
	triggerProvisionFunc     func(ctx context.Context, resource client.Object) (*provisioning.ProvisionResult, error)
	getProvisionStatusFunc   func(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error)
	triggerDeprovisionFunc   func(ctx context.Context, resource client.Object, provisionJobs []osacv1alpha1.JobStatus) (*provisioning.DeprovisionResult, error)
	getDeprovisionStatusFunc func(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error)
}

func (m *mockSubnetProvider) TriggerProvision(ctx context.Context, resource client.Object) (*provisioning.ProvisionResult, error) {
	if m.triggerProvisionFunc != nil {
		return m.triggerProvisionFunc(ctx, resource)
	}
	return &provisioning.ProvisionResult{
		JobID:        "mock-job-id",
		InitialState: osacv1alpha1.JobStatePending,
		Message:      "Provisioning job triggered",
	}, nil
}

func (m *mockSubnetProvider) GetProvisionStatus(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error) {
	if m.getProvisionStatusFunc != nil {
		return m.getProvisionStatusFunc(ctx, resource, jobID)
	}
	return provisioning.ProvisionStatus{
		JobID:   jobID,
		State:   osacv1alpha1.JobStateSucceeded,
		Message: "Job completed successfully",
	}, nil
}

func (m *mockSubnetProvider) TriggerDeprovision(ctx context.Context, resource client.Object, provisionJobs []osacv1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
	if m.triggerDeprovisionFunc != nil {
		return m.triggerDeprovisionFunc(ctx, resource, provisionJobs)
	}
	return &provisioning.DeprovisionResult{
		Action:                 provisioning.DeprovisionTriggered,
		JobID:                  "mock-deprovision-job-id",
		BlockDeletionOnFailure: true,
	}, nil
}

func (m *mockSubnetProvider) GetDeprovisionStatus(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error) {
	if m.getDeprovisionStatusFunc != nil {
		return m.getDeprovisionStatusFunc(ctx, resource, jobID)
	}
	return provisioning.ProvisionStatus{
		JobID:   jobID,
		State:   osacv1alpha1.JobStateSucceeded,
		Message: "Deprovision completed successfully",
	}, nil
}

func (m *mockSubnetProvider) Name() string {
	return "mock-subnet-provider"
}
