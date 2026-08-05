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
	"fmt"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	osacv1alpha1 "github.com/osac-project/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac-operator/pkg/provisioning"
)

// controllableProvider is a mock provider that allows test control over job progression
type controllableProvider struct {
	mu sync.Mutex

	provisionJobID      string
	provisionJobState   osacv1alpha1.JobState
	provisionJobMsg     string
	provisionTriggerErr error
	provisionCallCount  int

	deprovisionJobID      string
	deprovisionJobState   osacv1alpha1.JobState
	deprovisionJobMsg     string
	deprovisionTriggerErr error
}

func newControllableProvider() *controllableProvider {
	return &controllableProvider{
		provisionJobState:   osacv1alpha1.JobStatePending,
		deprovisionJobState: osacv1alpha1.JobStatePending,
	}
}

func (p *controllableProvider) TriggerProvision(ctx context.Context, resource client.Object) (*provisioning.ProvisionResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.provisionTriggerErr != nil {
		return nil, p.provisionTriggerErr
	}

	p.provisionCallCount++
	p.provisionJobID = fmt.Sprintf("prov-job-%s-%d", resource.GetName(), p.provisionCallCount)
	p.provisionJobState = osacv1alpha1.JobStatePending
	p.provisionJobMsg = "Job triggered"
	return &provisioning.ProvisionResult{
		JobID:        p.provisionJobID,
		InitialState: osacv1alpha1.JobStatePending,
		Message:      "Job triggered",
	}, nil
}

func (p *controllableProvider) GetProvisionStatus(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	return provisioning.ProvisionStatus{
		JobID:   jobID,
		State:   p.provisionJobState,
		Message: p.provisionJobMsg,
	}, nil
}

func (p *controllableProvider) TriggerDeprovision(ctx context.Context, resource client.Object, provisionJobs []osacv1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.deprovisionTriggerErr != nil {
		return nil, p.deprovisionTriggerErr
	}

	latestProvisionJob := provisioning.FindLatestJobByType(provisionJobs, osacv1alpha1.JobTypeProvision)
	if latestProvisionJob != nil {
		if !p.provisionJobState.IsTerminal() {
			// Cancel the provision job
			p.provisionJobState = osacv1alpha1.JobStateCanceled
			p.provisionJobMsg = "Job canceled"
			// Return waiting - need to poll again
			return &provisioning.DeprovisionResult{
				Action:                 provisioning.DeprovisionWaiting,
				BlockDeletionOnFailure: true,
			}, nil
		}
	}

	p.deprovisionJobID = "deprov-job-" + resource.GetName()
	p.deprovisionJobState = osacv1alpha1.JobStatePending
	p.deprovisionJobMsg = "Deprovision job triggered"
	return &provisioning.DeprovisionResult{
		Action:                 provisioning.DeprovisionTriggered,
		JobID:                  p.deprovisionJobID,
		BlockDeletionOnFailure: true,
	}, nil
}

func (p *controllableProvider) GetDeprovisionStatus(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	return provisioning.ProvisionStatus{
		JobID:   jobID,
		State:   p.deprovisionJobState,
		Message: p.deprovisionJobMsg,
	}, nil
}

func (p *controllableProvider) Name() string {
	return "aap"
}

// setProvisionJobState updates the provision job state (thread-safe)
func (p *controllableProvider) setProvisionJobState(state osacv1alpha1.JobState, message string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.provisionJobState = state
	p.provisionJobMsg = message
}

// setDeprovisionJobState updates the deprovision job state (thread-safe)
func (p *controllableProvider) setDeprovisionJobState(state osacv1alpha1.JobState, message string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.deprovisionJobState = state
	p.deprovisionJobMsg = message
}

var _ = Describe("ComputeInstance Integration Tests", func() {
	const (
		testNamespace            = "default"
		testDesiredConfigVersion = "initial"
		timeout                  = 10 * time.Second
		interval                 = 250 * time.Millisecond
	)

	var (
		reconciler *ComputeInstanceReconciler
		provider   *controllableProvider
	)

	BeforeEach(func() {
		provider = newControllableProvider()
		reconciler = NewComputeInstanceReconciler(testMcManager, testNamespace, testNamespace, "", provider, 100*time.Millisecond, provisioning.DefaultMaxJobHistory, mcmanager.LocalCluster)
	})

	Context("Provisioning workflow", func() {
		It("should provision a ComputeInstance successfully", func() {
			instanceName := "test-provision-success"
			instance := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      instanceName,
					Namespace: testNamespace,
					Annotations: map[string]string{
						osacTenantKey: "test-tenant",
					},
				},
				Spec: newTestComputeInstanceSpec("test_template"),
			}

			Expect(k8sClient.Create(ctx, instance)).To(Succeed())
			DeferCleanup(k8sClient.Delete, ctx, instance)

			// Simulate handleDesiredConfigVersion having run
			instance.Status.DesiredConfigVersion = testDesiredConfigVersion
			Expect(k8sClient.Status().Update(ctx, instance)).To(Succeed())

			// First call should trigger the job
			result, err := reconciler.handleProvisioning(ctx, instance)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(100 * time.Millisecond))

			// Re-fetch: RunProvisioningLifecycle flushes status after trigger
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: instanceName, Namespace: testNamespace}, instance)).To(Succeed())
			latestProvisionJob := provisioning.FindLatestJobByType(instance.Status.ProvisioningJobs, osacv1alpha1.JobTypeProvision)
			Expect(latestProvisionJob).NotTo(BeNil())
			Expect(latestProvisionJob.JobID).To(HavePrefix("prov-job-" + instanceName))
			Expect(latestProvisionJob.State).To(Equal(osacv1alpha1.JobStatePending))

			// Simulate job transitioning to running
			provider.setProvisionJobState(osacv1alpha1.JobStateRunning, "Job is running")

			result, err = reconciler.handleProvisioning(ctx, instance)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(100 * time.Millisecond))

			// Update status
			Expect(k8sClient.Status().Update(ctx, instance)).To(Succeed())

			// Verify status updated to running
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: instanceName, Namespace: testNamespace}, instance)).To(Succeed())
			latestProvisionJob = provisioning.FindLatestJobByType(instance.Status.ProvisioningJobs, osacv1alpha1.JobTypeProvision)
			Expect(latestProvisionJob).NotTo(BeNil())
			Expect(latestProvisionJob.State).To(Equal(osacv1alpha1.JobStateRunning))

			// Simulate job completing successfully
			provider.setProvisionJobState(osacv1alpha1.JobStateSucceeded, "Job completed")

			result, err = reconciler.handleProvisioning(ctx, instance)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			// Update status
			Expect(k8sClient.Status().Update(ctx, instance)).To(Succeed())

			// Verify final status
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: instanceName, Namespace: testNamespace}, instance)).To(Succeed())
			latestProvisionJob = provisioning.FindLatestJobByType(instance.Status.ProvisioningJobs, osacv1alpha1.JobTypeProvision)
			Expect(latestProvisionJob).NotTo(BeNil())
			Expect(latestProvisionJob.State).To(Equal(osacv1alpha1.JobStateSucceeded))
		})

		It("should handle provision job failure", func() {
			instanceName := "test-provision-failure"
			instance := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      instanceName,
					Namespace: testNamespace,
					Annotations: map[string]string{
						osacTenantKey: "test-tenant",
					},
				},
				Spec: newTestComputeInstanceSpec("test_template"),
			}

			Expect(k8sClient.Create(ctx, instance)).To(Succeed())
			DeferCleanup(k8sClient.Delete, ctx, instance)

			// Simulate handleDesiredConfigVersion having run
			instance.Status.DesiredConfigVersion = testDesiredConfigVersion
			Expect(k8sClient.Status().Update(ctx, instance)).To(Succeed())

			// Trigger the job
			_, err := reconciler.handleProvisioning(ctx, instance)
			Expect(err).NotTo(HaveOccurred())

			// Re-fetch: RunProvisioningLifecycle flushes status after trigger
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: instanceName, Namespace: testNamespace}, instance)).To(Succeed())

			// Simulate job failing
			provider.setProvisionJobState(osacv1alpha1.JobStateFailed, "Provisioning failed")

			_, err = reconciler.handleProvisioning(ctx, instance)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Status().Update(ctx, instance)).To(Succeed())

			// Verify status shows failure
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: instanceName, Namespace: testNamespace}, instance)).To(Succeed())
			latestProvisionJob := provisioning.FindLatestJobByType(instance.Status.ProvisioningJobs, osacv1alpha1.JobTypeProvision)
			Expect(latestProvisionJob).NotTo(BeNil())
			Expect(latestProvisionJob.State).To(Equal(osacv1alpha1.JobStateFailed))
			Expect(instance.Status.Phase).To(Equal(osacv1alpha1.ComputeInstancePhaseFailed))
		})
	})

	Context("Deprovisioning workflow", func() {
		It("should deprovision a ComputeInstance successfully (AAP Direct)", func() {
			instanceName := "test-deprovision-success"
			instance := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      instanceName,
					Namespace: testNamespace,
					// AAP Direct uses base finalizer, not AAP-specific finalizer
					Finalizers: []string{osacComputeInstanceFinalizer},
					Annotations: map[string]string{
						osacTenantKey: "test-tenant",
					},
				},
				Spec: newTestComputeInstanceSpec("test_template"),
			}

			Expect(k8sClient.Create(ctx, instance)).To(Succeed())

			// Trigger deprovisioning
			result, err := reconciler.handleDeprovisioning(ctx, instance)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(100 * time.Millisecond))

			// Verify deprovision job was triggered (in-memory state)
			latestDeprovisionJob := provisioning.FindLatestJobByType(instance.Status.ProvisioningJobs, osacv1alpha1.JobTypeDeprovision)
			Expect(latestDeprovisionJob).NotTo(BeNil())
			Expect(latestDeprovisionJob.JobID).To(Equal("deprov-job-" + instanceName))
			Expect(latestDeprovisionJob.State).To(Equal(osacv1alpha1.JobStatePending))

			// Simulate job completing
			provider.setDeprovisionJobState(osacv1alpha1.JobStateSucceeded, "Deprovision completed")

			result, err = reconciler.handleDeprovisioning(ctx, instance)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			// For AAP Direct: handleDeprovisioning() doesn't remove finalizers
			// Finalizer removal is handled by handleDelete()
			// Verify job succeeded
			latestDeprovisionJob = provisioning.FindLatestJobByType(instance.Status.ProvisioningJobs, osacv1alpha1.JobTypeDeprovision)
			Expect(latestDeprovisionJob).NotTo(BeNil())
			Expect(latestDeprovisionJob.State).To(Equal(osacv1alpha1.JobStateSucceeded))
		})

		It("should block deletion when deprovision job fails (AAP Direct)", func() {
			instanceName := "test-deprovision-failure"
			instance := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      instanceName,
					Namespace: testNamespace,
					Annotations: map[string]string{
						osacTenantKey: "test-tenant",
					},
				},
				Spec: newTestComputeInstanceSpec("test_template"),
			}

			Expect(k8sClient.Create(ctx, instance)).To(Succeed())

			// Trigger deprovisioning
			result, err := reconciler.handleDeprovisioning(ctx, instance)
			Expect(err).NotTo(HaveOccurred())
			// Should requeue to poll job status
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))

			// Simulate job failing
			provider.setDeprovisionJobState(osacv1alpha1.JobStateFailed, "Resource not found")

			// Call again with failed job
			result, err = reconciler.handleDeprovisioning(ctx, instance)
			Expect(err).NotTo(HaveOccurred())

			// For AAP Direct: Should requeue (block deletion) when deprovision fails
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))

			// Verify deletion is blocked - status shows failed deprovision
			latestDeprovisionJob := provisioning.FindLatestJobByType(instance.Status.ProvisioningJobs, osacv1alpha1.JobTypeDeprovision)
			Expect(latestDeprovisionJob).NotTo(BeNil())
			Expect(latestDeprovisionJob.State).To(Equal(osacv1alpha1.JobStateFailed))
		})
	})

	Context("Long-running job polling", func() {
		It("should poll for job status until completion", func() {
			instanceName := "test-long-running"
			instance := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      instanceName,
					Namespace: testNamespace,
					Annotations: map[string]string{
						osacTenantKey: "test-tenant",
					},
				},
				Spec: newTestComputeInstanceSpec("test_template"),
			}

			Expect(k8sClient.Create(ctx, instance)).To(Succeed())

			// Simulate handleDesiredConfigVersion having run
			instance.Status.DesiredConfigVersion = testDesiredConfigVersion
			Expect(k8sClient.Status().Update(ctx, instance)).To(Succeed())
			DeferCleanup(k8sClient.Delete, ctx, instance)

			// Trigger job
			_, err := reconciler.handleProvisioning(ctx, instance)
			Expect(err).NotTo(HaveOccurred())

			// Verify job is pending (in-memory)
			latestProvisionJob := provisioning.FindLatestJobByType(instance.Status.ProvisioningJobs, osacv1alpha1.JobTypeProvision)
			Expect(latestProvisionJob).NotTo(BeNil())
			Expect(latestProvisionJob.State).To(Equal(osacv1alpha1.JobStatePending))

			// Set to running and poll multiple times
			provider.setProvisionJobState(osacv1alpha1.JobStateRunning, "Job running - step 1")
			for range 3 {
				result, err := reconciler.handleProvisioning(ctx, instance)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.RequeueAfter).To(Equal(100 * time.Millisecond))

				latestProvisionJob = provisioning.FindLatestJobByType(instance.Status.ProvisioningJobs, osacv1alpha1.JobTypeProvision)
				Expect(latestProvisionJob).NotTo(BeNil())
				Expect(latestProvisionJob.State).To(Equal(osacv1alpha1.JobStateRunning))
			}

			// Finally complete
			provider.setProvisionJobState(osacv1alpha1.JobStateSucceeded, "Job completed")
			result, err := reconciler.handleProvisioning(ctx, instance)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			latestProvisionJob = provisioning.FindLatestJobByType(instance.Status.ProvisioningJobs, osacv1alpha1.JobTypeProvision)
			Expect(latestProvisionJob).NotTo(BeNil())
			Expect(latestProvisionJob.State).To(Equal(osacv1alpha1.JobStateSucceeded))
		})
	})
})
