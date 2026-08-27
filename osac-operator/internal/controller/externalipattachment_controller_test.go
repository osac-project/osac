/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package controller

import (
	"context"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	bmfov1alpha1 "github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

var _ = Describe("ExternalIPAttachmentReconciler", func() {
	const (
		testNetworkingNamespace        = "test-networking"
		testComputeInstanceNamespace   = "test-ci"
		testClusterOrderNamespace      = "test-orders"
		testBaremetalInstanceNamespace = "test-bmi"
		testPoolUUID                   = "pool-uuid-123"
		testExternalIPUUID             = "pip-uuid-789"
		testCIUUID                     = "ci-uuid-456"
		testCIName                     = "test-ci-1"
		testCOUUID                     = "co-uuid-789"
		testCOName                     = "test-cluster-1"
		testBMIUUID                    = "bmi-uuid-101"
		testBMIName                    = "test-bmi-1"
		testAPIEndpoint                = "10.0.0.100"
		testIngressEndpoint            = "10.0.0.200"
		testExternalIPName             = "test-pip"
		testAttachmentName             = "test-attachment"
		testVMNamespace                = "subnet-abc123"
	)

	var (
		reconciler   *ExternalIPAttachmentReconciler
		mockProvider *mockProvisioningProvider
		fakeClient   client.Client
		testCtx      context.Context
		testScheme   *runtime.Scheme
		attachment   *osacv1alpha1.ExternalIPAttachment
		publicIP     *osacv1alpha1.ExternalIP
		pool         *osacv1alpha1.ExternalIPPool
		ci           *osacv1alpha1.ComputeInstance
		key          types.NamespacedName
	)

	buildClient := func(objs ...client.Object) client.Client {
		return fake.NewClientBuilder().
			WithScheme(testScheme).
			WithObjects(objs...).
			WithStatusSubresource(
				&osacv1alpha1.ExternalIPAttachment{},
				&osacv1alpha1.ExternalIP{},
				&osacv1alpha1.ComputeInstance{},
				&osacv1alpha1.ClusterOrder{},
				&bmfov1alpha1.BareMetalInstance{},
			).
			Build()
	}

	BeforeEach(func() {
		testCtx = context.TODO()
		testScheme = runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(testScheme)).To(Succeed())
		Expect(bmfov1alpha1.AddToScheme(testScheme)).To(Succeed())
		Expect(scheme.AddToScheme(testScheme)).To(Succeed())

		pool = &osacv1alpha1.ExternalIPPool{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pool",
				Namespace: testNetworkingNamespace,
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

		publicIP = &osacv1alpha1.ExternalIP{
			ObjectMeta: metav1.ObjectMeta{
				Name:      testExternalIPName,
				Namespace: testNetworkingNamespace,
				Labels: map[string]string{
					osacExternalIPIDLabel: testExternalIPUUID,
				},
			},
			Spec: osacv1alpha1.ExternalIPSpec{
				Pool: testPoolUUID,
			},
			Status: osacv1alpha1.ExternalIPStatus{
				Address: "198.51.100.10",
			},
		}

		ci = &osacv1alpha1.ComputeInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      testCIName,
				Namespace: testComputeInstanceNamespace,
				Labels: map[string]string{
					osacComputeInstanceIDLabel: testCIUUID,
				},
			},
			Status: osacv1alpha1.ComputeInstanceStatus{
				VirtualMachineReference: &osacv1alpha1.VirtualMachineReferenceType{
					Namespace:                  testVMNamespace,
					KubeVirtVirtualMachineName: "test-vm",
				},
			},
		}

		attachment = &osacv1alpha1.ExternalIPAttachment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      testAttachmentName,
				Namespace: testNetworkingNamespace,
			},
			Spec: osacv1alpha1.ExternalIPAttachmentSpec{
				ExternalIP:      testExternalIPUUID,
				ComputeInstance: ptr.To(testCIUUID),
			},
		}

		key = types.NamespacedName{Name: testAttachmentName, Namespace: testNetworkingNamespace}

		mockProvider = &mockProvisioningProvider{name: "mock-aap"}
	})

	setupReconciler := func(c client.Client) {
		reconciler = &ExternalIPAttachmentReconciler{
			Client:                     c,
			APIReader:                  c,
			Scheme:                     testScheme,
			NetworkingNamespace:        testNetworkingNamespace,
			ComputeInstanceNamespace:   testComputeInstanceNamespace,
			ClusterOrderNamespace:      testClusterOrderNamespace,
			BaremetalInstanceNamespace: testBaremetalInstanceNamespace,
			ProvisioningProvider:       mockProvider,
			StatusPollInterval:         1 * time.Second,
			MaxJobHistory:              10,
			NetworkProvisioningEnabled: true,
		}
	}

	reconcileOnce := func() (ctrl.Result, error) {
		return reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: key}})
	}

	Context("Reconcile basics", func() {
		It("should add finalizer on first reconcile", func() {
			fakeClient = buildClient(attachment, publicIP, pool, ci)
			setupReconciler(fakeClient)

			_, err := reconcileOnce()
			Expect(err).NotTo(HaveOccurred())

			updated := &osacv1alpha1.ExternalIPAttachment{}
			Expect(fakeClient.Get(testCtx, key, updated)).To(Succeed())
			Expect(updated.Finalizers).To(ContainElement(osacExternalIPAttachmentFinalizer))
		})

		It("should set phase to Progressing initially", func() {
			fakeClient = buildClient(attachment, publicIP, pool, ci)
			setupReconciler(fakeClient)

			mockProvider.getProvisionStatusFunc = func(
				ctx context.Context, resource client.Object, jobID string,
			) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID: jobID, State: osacv1alpha1.JobStateRunning, Message: "running",
				}, nil
			}

			_, _ = reconcileOnce()
			_, _ = reconcileOnce()

			updated := &osacv1alpha1.ExternalIPAttachment{}
			Expect(fakeClient.Get(testCtx, key, updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(osacv1alpha1.ExternalIPAttachmentPhaseProgressing))
		})

		It("should ignore resource with management-state unmanaged", func() {
			attachment.Annotations = map[string]string{
				osacManagementStateAnnotation: ManagementStateUnmanaged,
			}
			fakeClient = buildClient(attachment, publicIP, pool, ci)
			setupReconciler(fakeClient)

			_, err := reconcileOnce()
			Expect(err).NotTo(HaveOccurred())

			updated := &osacv1alpha1.ExternalIPAttachment{}
			Expect(fakeClient.Get(testCtx, key, updated)).To(Succeed())
			Expect(updated.Finalizers).To(BeEmpty())
			Expect(updated.Status.Phase).To(BeEmpty())
		})
	})

	Context("Parent resolution", func() {
		It("should requeue when parent ExternalIP not found", func() {
			fakeClient = buildClient(attachment, pool, ci) // no publicIP
			setupReconciler(fakeClient)

			_, _ = reconcileOnce() // finalizer

			result, err := reconcileOnce()
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(defaultPreconditionRequeueInterval))
		})

		It("should requeue when parent ExternalIPPool not found", func() {
			poolless := publicIP.DeepCopy()
			poolless.Spec.Pool = "nonexistent-uuid"
			fakeClient = buildClient(attachment, poolless, ci) // pool UUID won't match
			setupReconciler(fakeClient)

			_, _ = reconcileOnce() // finalizer

			result, err := reconcileOnce()
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(defaultPreconditionRequeueInterval))
		})

		It("should auto-detach when ComputeInstance not found", func() {
			fakeClient = buildClient(attachment, publicIP, pool) // no CI
			setupReconciler(fakeClient)

			_, _ = reconcileOnce() // finalizer
			_, _ = reconcileOnce() // auto-detach: sets DeletionTimestamp
			_, _ = reconcileOnce() // handleDelete: removes finalizer

			fetched := &osacv1alpha1.ExternalIPAttachment{}
			err := fakeClient.Get(ctx, types.NamespacedName{
				Namespace: attachment.Namespace, Name: attachment.Name,
			}, fetched)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})

		It("should requeue when CI has no VirtualMachineReference", func() {
			ciNoVM := ci.DeepCopy()
			ciNoVM.Status.VirtualMachineReference = nil
			fakeClient = buildClient(attachment, publicIP, pool, ciNoVM)
			setupReconciler(fakeClient)

			_, _ = reconcileOnce() // finalizer

			result, err := reconcileOnce()
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(defaultPreconditionRequeueInterval))
		})
	})

	Context("Annotation sync", func() {
		It("should set implementation-strategy, pool-name, and target-namespace annotations", func() {
			fakeClient = buildClient(attachment, publicIP, pool, ci)
			setupReconciler(fakeClient)

			mockProvider.getProvisionStatusFunc = func(
				ctx context.Context, resource client.Object, jobID string,
			) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID: jobID, State: osacv1alpha1.JobStateRunning, Message: "running",
				}, nil
			}

			_, _ = reconcileOnce() // finalizer
			_, _ = reconcileOnce() // annotations + provisioning

			updated := &osacv1alpha1.ExternalIPAttachment{}
			Expect(fakeClient.Get(testCtx, key, updated)).To(Succeed())
			Expect(updated.Annotations[osacImplementationStrategyAnnotation]).To(Equal("metallb-l2"))
			Expect(updated.Annotations[osacExternalIPPoolNameAnnotation]).To(Equal("test-pool"))
			Expect(updated.Annotations[osacExternalIPNameAnnotation]).To(Equal(testExternalIPName))
			Expect(updated.Annotations[osacExternalIPTargetNamespaceAnnotation]).To(Equal(testVMNamespace))
		})

		It("should use default implementation strategy when pool has none", func() {
			pool.Spec.ImplementationStrategy = ""
			fakeClient = buildClient(attachment, publicIP, pool, ci)
			setupReconciler(fakeClient)

			mockProvider.getProvisionStatusFunc = func(
				ctx context.Context, resource client.Object, jobID string,
			) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID: jobID, State: osacv1alpha1.JobStateRunning, Message: "running",
				}, nil
			}

			_, _ = reconcileOnce()
			_, _ = reconcileOnce()

			updated := &osacv1alpha1.ExternalIPAttachment{}
			Expect(fakeClient.Get(testCtx, key, updated)).To(Succeed())
			Expect(updated.Annotations[osacImplementationStrategyAnnotation]).To(Equal(defaultExternalIPPoolImplementationStrategy))
		})
	})

	Context("Provisioning lifecycle", func() {
		It("should set phase to Ready on successful provision", func() {
			fakeClient = buildClient(attachment, publicIP, pool, ci)
			setupReconciler(fakeClient)

			// Set address on ExternalIP so onProvisionSuccess can propagate it
			pip := &osacv1alpha1.ExternalIP{}
			Expect(fakeClient.Get(testCtx, client.ObjectKeyFromObject(publicIP), pip)).To(Succeed())
			pip.Status.Address = "192.168.1.10"
			Expect(fakeClient.Status().Update(testCtx, pip)).To(Succeed())

			mockProvider.getProvisionStatusFunc = func(
				ctx context.Context, resource client.Object, jobID string,
			) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID: jobID, State: osacv1alpha1.JobStateSucceeded, Message: "done",
				}, nil
			}

			// Pass 1: finalizer, Pass 2: annotations + trigger, Pass 3: poll -> Ready
			_, _ = reconcileOnce()
			_, _ = reconcileOnce()
			_, _ = reconcileOnce()

			updated := &osacv1alpha1.ExternalIPAttachment{}
			Expect(fakeClient.Get(testCtx, key, updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(osacv1alpha1.ExternalIPAttachmentPhaseReady))
		})

		It("should set ExternalIP.status.attached on provision success", func() {
			fakeClient = buildClient(attachment, publicIP, pool, ci)
			setupReconciler(fakeClient)

			pip := &osacv1alpha1.ExternalIP{}
			Expect(fakeClient.Get(testCtx, client.ObjectKeyFromObject(publicIP), pip)).To(Succeed())
			pip.Status.Address = "192.168.1.10"
			Expect(fakeClient.Status().Update(testCtx, pip)).To(Succeed())

			mockProvider.getProvisionStatusFunc = func(
				ctx context.Context, resource client.Object, jobID string,
			) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID: jobID, State: osacv1alpha1.JobStateSucceeded, Message: "done",
				}, nil
			}

			_, _ = reconcileOnce()
			_, _ = reconcileOnce()
			_, _ = reconcileOnce()

			updatedPIP := &osacv1alpha1.ExternalIP{}
			Expect(fakeClient.Get(testCtx, client.ObjectKeyFromObject(publicIP), updatedPIP)).To(Succeed())
			Expect(updatedPIP.Status.Attached).To(BeTrue())
		})

		It("should set ComputeInstance.status.externalIPAddress on provision success", func() {
			fakeClient = buildClient(attachment, publicIP, pool, ci)
			setupReconciler(fakeClient)

			pip := &osacv1alpha1.ExternalIP{}
			Expect(fakeClient.Get(testCtx, client.ObjectKeyFromObject(publicIP), pip)).To(Succeed())
			pip.Status.Address = "192.168.1.10"
			Expect(fakeClient.Status().Update(testCtx, pip)).To(Succeed())

			mockProvider.getProvisionStatusFunc = func(
				ctx context.Context, resource client.Object, jobID string,
			) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID: jobID, State: osacv1alpha1.JobStateSucceeded, Message: "done",
				}, nil
			}

			_, _ = reconcileOnce()
			_, _ = reconcileOnce()
			_, _ = reconcileOnce()

			updatedCI := &osacv1alpha1.ComputeInstance{}
			Expect(fakeClient.Get(testCtx, client.ObjectKeyFromObject(ci), updatedCI)).To(Succeed())
			Expect(updatedCI.GetExternalIPAddress()).To(Equal("192.168.1.10"))
		})

		It("should set phase to Failed on provision failure", func() {
			fakeClient = buildClient(attachment, publicIP, pool, ci)
			setupReconciler(fakeClient)

			mockProvider.getProvisionStatusFunc = func(
				ctx context.Context, resource client.Object, jobID string,
			) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID: jobID, State: osacv1alpha1.JobStateFailed, Message: "MetalLB unreachable",
				}, nil
			}

			_, _ = reconcileOnce()
			_, _ = reconcileOnce()
			_, _ = reconcileOnce()

			updated := &osacv1alpha1.ExternalIPAttachment{}
			Expect(fakeClient.Get(testCtx, key, updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(osacv1alpha1.ExternalIPAttachmentPhaseFailed))
		})

		It("should set ConfigurationApplied condition", func() {
			fakeClient = buildClient(attachment, publicIP, pool, ci)
			setupReconciler(fakeClient)

			_, _ = reconcileOnce()
			_, _ = reconcileOnce()

			updated := &osacv1alpha1.ExternalIPAttachment{}
			Expect(fakeClient.Get(testCtx, key, updated)).To(Succeed())
			condition := osacv1alpha1.GetExternalIPAttachmentStatusCondition(
				updated, osacv1alpha1.ExternalIPAttachmentConditionConfigurationApplied,
			)
			Expect(condition).NotTo(BeNil())
			Expect(condition.Status).To(Equal(metav1.ConditionTrue))
		})
	})

	Context("provisioning condition updates", func() {
		BeforeEach(func() {
			fakeClient = buildClient(attachment, publicIP, pool, ci)
			setupReconciler(fakeClient)
		})

		It("should set Ready=False condition with error message when job fails", func() {
			attachment.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
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

			_, err := reconciler.handleProvisioning(testCtx, attachment, publicIP, ci)
			Expect(err).NotTo(HaveOccurred())

			cond := apimeta.FindStatusCondition(attachment.Status.Conditions, osacv1alpha1.ConditionReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(osacv1alpha1.ReasonProvisioningFailed))
			Expect(cond.Message).To(ContainSubstring("Ansible traceback"))
		})

		It("should set Ready=True condition when job succeeds", func() {
			attachment.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
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

			_, err := reconciler.handleProvisioning(testCtx, attachment, publicIP, ci)
			Expect(err).NotTo(HaveOccurred())

			cond := apimeta.FindStatusCondition(attachment.Status.Conditions, osacv1alpha1.ConditionReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(osacv1alpha1.ReasonAsExpected))
		})

		It("should clear stale Ready=False condition on provisioning recovery", func() {
			attachment.Status.Conditions = []metav1.Condition{
				{
					Type:               osacv1alpha1.ConditionReady,
					Status:             metav1.ConditionFalse,
					Reason:             osacv1alpha1.ReasonProvisioningFailed,
					Message:            "previous failure",
					LastTransitionTime: metav1.Now(),
				},
			}
			attachment.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{
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

			_, err := reconciler.handleProvisioning(testCtx, attachment, publicIP, ci)
			Expect(err).NotTo(HaveOccurred())

			Expect(attachment.Status.Phase).To(Equal(osacv1alpha1.ExternalIPAttachmentPhaseReady))
			cond := apimeta.FindStatusCondition(attachment.Status.Conditions, osacv1alpha1.ConditionReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(osacv1alpha1.ReasonAsExpected))
			Expect(cond.Message).To(BeEmpty())
		})
	})

	Context("Deprovisioning (delete)", func() {
		It("should set phase Deleting and trigger deprovision", func() {
			fakeClient = buildClient(attachment, publicIP, pool, ci)
			setupReconciler(fakeClient)

			deprovisionCalled := false
			mockProvider.triggerDeprovisionFunc = func(
				ctx context.Context, resource client.Object, _ []osacv1alpha1.JobStatus,
			) (*provisioning.DeprovisionResult, error) {
				deprovisionCalled = true
				return &provisioning.DeprovisionResult{
					Action: provisioning.DeprovisionTriggered,
					JobID:  "detach-job-1",
				}, nil
			}

			// Add finalizer
			_, _ = reconcileOnce()

			toDelete := &osacv1alpha1.ExternalIPAttachment{}
			Expect(fakeClient.Get(testCtx, key, toDelete)).To(Succeed())
			now := metav1.Now()
			toDelete.DeletionTimestamp = &now

			_, err := reconciler.handleDelete(testCtx, toDelete)
			Expect(err).NotTo(HaveOccurred())
			Expect(deprovisionCalled).To(BeTrue())
			Expect(toDelete.Status.Phase).To(Equal(osacv1alpha1.ExternalIPAttachmentPhaseDeleting))
		})

		It("should remove finalizer after successful deprovision", func() {
			fakeClient = buildClient(attachment, publicIP, pool, ci)
			setupReconciler(fakeClient)

			mockProvider.triggerDeprovisionFunc = func(
				ctx context.Context, resource client.Object, _ []osacv1alpha1.JobStatus,
			) (*provisioning.DeprovisionResult, error) {
				return &provisioning.DeprovisionResult{
					Action: provisioning.DeprovisionTriggered,
					JobID:  "detach-success",
				}, nil
			}
			mockProvider.getDeprovisionStatusFunc = func(
				ctx context.Context, resource client.Object, jobID string,
			) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID: jobID, State: osacv1alpha1.JobStateSucceeded, Message: "done",
				}, nil
			}

			_, _ = reconcileOnce() // finalizer

			toDelete := &osacv1alpha1.ExternalIPAttachment{}
			Expect(fakeClient.Get(testCtx, key, toDelete)).To(Succeed())
			now := metav1.Now()
			toDelete.DeletionTimestamp = &now

			// First: trigger deprovision job
			_, _ = reconciler.handleDelete(testCtx, toDelete)
			// Second: poll status -> success -> remove finalizer
			_, _ = reconciler.handleDelete(testCtx, toDelete)

			Expect(toDelete.Finalizers).NotTo(ContainElement(osacExternalIPAttachmentFinalizer))
		})

		It("should clear ExternalIP.status.attached on deprovision", func() {
			fakeClient = buildClient(attachment, publicIP, pool, ci)
			setupReconciler(fakeClient)

			// Set attached=true on ExternalIP
			pip := &osacv1alpha1.ExternalIP{}
			Expect(fakeClient.Get(testCtx, client.ObjectKeyFromObject(publicIP), pip)).To(Succeed())
			pip.Status.Attached = true
			Expect(fakeClient.Status().Update(testCtx, pip)).To(Succeed())

			mockProvider.triggerDeprovisionFunc = func(
				ctx context.Context, resource client.Object, _ []osacv1alpha1.JobStatus,
			) (*provisioning.DeprovisionResult, error) {
				return &provisioning.DeprovisionResult{
					Action: provisioning.DeprovisionSkipped,
				}, nil
			}

			_, _ = reconcileOnce() // finalizer

			toDelete := &osacv1alpha1.ExternalIPAttachment{}
			Expect(fakeClient.Get(testCtx, key, toDelete)).To(Succeed())
			now := metav1.Now()
			toDelete.DeletionTimestamp = &now

			_, _ = reconciler.handleDelete(testCtx, toDelete)

			updatedPIP := &osacv1alpha1.ExternalIP{}
			Expect(fakeClient.Get(testCtx, client.ObjectKeyFromObject(publicIP), updatedPIP)).To(Succeed())
			Expect(updatedPIP.Status.Attached).To(BeFalse())
		})

		It("should block deletion when deprovision fails with BlockDeletionOnFailure", func() {
			fakeClient = buildClient(attachment, publicIP, pool, ci)
			setupReconciler(fakeClient)

			mockProvider.triggerDeprovisionFunc = func(
				ctx context.Context, resource client.Object, _ []osacv1alpha1.JobStatus,
			) (*provisioning.DeprovisionResult, error) {
				return &provisioning.DeprovisionResult{
					Action:                 provisioning.DeprovisionTriggered,
					JobID:                  "detach-fail",
					BlockDeletionOnFailure: true,
				}, nil
			}
			mockProvider.getDeprovisionStatusFunc = func(
				ctx context.Context, resource client.Object, jobID string,
			) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID: jobID, State: osacv1alpha1.JobStateFailed, Message: "failed",
				}, nil
			}

			_, _ = reconcileOnce() // finalizer

			toDelete := &osacv1alpha1.ExternalIPAttachment{}
			Expect(fakeClient.Get(testCtx, key, toDelete)).To(Succeed())
			now := metav1.Now()
			toDelete.DeletionTimestamp = &now

			_, _ = reconciler.handleDelete(testCtx, toDelete)       // trigger
			result, _ := reconciler.handleDelete(testCtx, toDelete) // poll -> failed -> block

			Expect(result.RequeueAfter).To(BeNumerically(">", 0))
			Expect(toDelete.Finalizers).To(ContainElement(osacExternalIPAttachmentFinalizer))
		})
	})

	Context("CI detach finalizer management", func() {
		It("should add detach finalizer to CI during handleUpdate", func() {
			fakeClient = buildClient(attachment, publicIP, pool, ci)
			setupReconciler(fakeClient)

			mockProvider.getProvisionStatusFunc = func(
				ctx context.Context, resource client.Object, jobID string,
			) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID: jobID, State: osacv1alpha1.JobStateRunning, Message: "running",
				}, nil
			}

			_, _ = reconcileOnce() // finalizer
			_, _ = reconcileOnce() // parent resolve + CI finalizer

			updatedCI := &osacv1alpha1.ComputeInstance{}
			Expect(fakeClient.Get(testCtx, client.ObjectKeyFromObject(ci), updatedCI)).To(Succeed())
			Expect(updatedCI.Finalizers).To(ContainElement(osacExternalIPDetachFinalizer))
		})

		It("should remove detach finalizer when no other attachments reference the CI", func() {
			ci.Finalizers = []string{osacExternalIPDetachFinalizer}
			fakeClient = buildClient(ci)
			setupReconciler(fakeClient)

			err := reconciler.removeCIDetachFinalizerIfUnreferenced(testCtx, testCIUUID, "")
			Expect(err).NotTo(HaveOccurred())

			updatedCI := &osacv1alpha1.ComputeInstance{}
			Expect(fakeClient.Get(testCtx, client.ObjectKeyFromObject(ci), updatedCI)).To(Succeed())
			Expect(updatedCI.Finalizers).NotTo(ContainElement(osacExternalIPDetachFinalizer))
		})

		It("should keep detach finalizer when other attachments reference the CI", func() {
			otherAttachment := &osacv1alpha1.ExternalIPAttachment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "other-attachment",
					Namespace: testNetworkingNamespace,
				},
				Spec: osacv1alpha1.ExternalIPAttachmentSpec{
					ExternalIP:      "other-pip",
					ComputeInstance: ptr.To(testCIUUID),
				},
			}
			ci.Finalizers = []string{osacExternalIPDetachFinalizer}
			fakeClient = buildClient(ci, otherAttachment)
			setupReconciler(fakeClient)

			err := reconciler.removeCIDetachFinalizerIfUnreferenced(testCtx, testCIUUID, testAttachmentName)
			Expect(err).NotTo(HaveOccurred())

			updatedCI := &osacv1alpha1.ComputeInstance{}
			Expect(fakeClient.Get(testCtx, client.ObjectKeyFromObject(ci), updatedCI)).To(Succeed())
			Expect(updatedCI.Finalizers).To(ContainElement(osacExternalIPDetachFinalizer))
		})

		It("should retry finalizer removal on conflict error", func() {
			ci.Finalizers = []string{osacExternalIPDetachFinalizer}

			var updateCount atomic.Int32
			conflictClient := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(ci).
				WithStatusSubresource(
					&osacv1alpha1.ExternalIPAttachment{},
					&osacv1alpha1.ExternalIP{},
					&osacv1alpha1.ComputeInstance{},
				).
				WithInterceptorFuncs(interceptor.Funcs{
					Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
						if _, ok := obj.(*osacv1alpha1.ComputeInstance); ok {
							if updateCount.Add(1) == 1 {
								return apierrors.NewConflict(
									schema.GroupResource{Group: "osac.openshift.io", Resource: "computeinstances"},
									obj.GetName(),
									nil,
								)
							}
						}
						return c.Update(ctx, obj, opts...)
					},
				}).
				Build()
			setupReconciler(conflictClient)

			err := reconciler.removeCIDetachFinalizerIfUnreferenced(testCtx, testCIUUID, "")
			Expect(err).NotTo(HaveOccurred())
			Expect(updateCount.Load()).To(BeNumerically(">=", int32(2)))

			updatedCI := &osacv1alpha1.ComputeInstance{}
			Expect(conflictClient.Get(testCtx, client.ObjectKeyFromObject(ci), updatedCI)).To(Succeed())
			Expect(updatedCI.Finalizers).NotTo(ContainElement(osacExternalIPDetachFinalizer))
		})

		It("should keep detach finalizer when other ExternalIPAttachments still reference the CI", func() {
			excludedAttachment := &osacv1alpha1.ExternalIPAttachment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "excluded-attachment",
					Namespace: testNetworkingNamespace,
				},
				Spec: osacv1alpha1.ExternalIPAttachmentSpec{
					ExternalIP:      "excluded-pip",
					ComputeInstance: ptr.To(testCIUUID),
				},
			}
			remainingAttachment := &osacv1alpha1.ExternalIPAttachment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "remaining-attachment",
					Namespace: testNetworkingNamespace,
				},
				Spec: osacv1alpha1.ExternalIPAttachmentSpec{
					ExternalIP:      "remaining-pip",
					ComputeInstance: ptr.To(testCIUUID),
				},
			}
			ci.Finalizers = []string{osacExternalIPDetachFinalizer}
			fakeClient = buildClient(ci, excludedAttachment, remainingAttachment)
			setupReconciler(fakeClient)

			err := reconciler.removeCIDetachFinalizerIfUnreferenced(testCtx, testCIUUID, "excluded-attachment")
			Expect(err).NotTo(HaveOccurred())

			updatedCI := &osacv1alpha1.ComputeInstance{}
			Expect(fakeClient.Get(testCtx, client.ObjectKeyFromObject(ci), updatedCI)).To(Succeed())
			Expect(updatedCI.Finalizers).To(ContainElement(osacExternalIPDetachFinalizer))
		})
	})

	Context("Auto-detach (CI deletion)", func() {
		It("should delete ExternalIPAttachment when CI is being deleted", func() {
			// The fake client does not support setting DeletionTimestamp via Create,
			// so we test resolveComputeInstance directly with an in-memory CI that
			// has DeletionTimestamp set.
			deletingCI := ci.DeepCopy()
			now := metav1.Now()
			deletingCI.DeletionTimestamp = &now
			deletingCI.Finalizers = []string{osacExternalIPDetachFinalizer}

			attachment.Finalizers = []string{osacExternalIPAttachmentFinalizer}
			fakeClient = buildClient(attachment, publicIP, pool, ci)
			setupReconciler(fakeClient)

			// Patch the CI in the fake client to add the finalizer (so it can be
			// "deleted" with DeletionTimestamp), then call resolveComputeInstance
			// with an in-memory CI that has DeletionTimestamp set.
			fetchedCI := &osacv1alpha1.ComputeInstance{}
			Expect(fakeClient.Get(testCtx, client.ObjectKeyFromObject(ci), fetchedCI)).To(Succeed())
			fetchedCI.Finalizers = []string{osacExternalIPDetachFinalizer}
			Expect(fakeClient.Update(testCtx, fetchedCI)).To(Succeed())

			// Verify the map function returns the attachment for this CI
			requests := reconciler.mapComputeInstanceToExternalIPAttachments(testCtx, ci)
			Expect(requests).To(HaveLen(1))
		})
	})

	Context("CI watch mapping", func() {
		It("should map CI changes to attachment reconcile requests", func() {
			fakeClient = buildClient(attachment, publicIP, pool, ci)
			setupReconciler(fakeClient)

			requests := reconciler.mapComputeInstanceToExternalIPAttachments(testCtx, ci)
			Expect(requests).To(HaveLen(1))
			Expect(requests[0].NamespacedName).To(Equal(reconcile.Request{
				NamespacedName: key,
			}.NamespacedName))
		})

		It("should not map CI changes to unrelated attachments", func() {
			unrelatedAttachment := attachment.DeepCopy()
			unrelatedAttachment.Spec.ComputeInstance = new("other-ci")
			fakeClient = buildClient(unrelatedAttachment, publicIP, pool, ci)
			setupReconciler(fakeClient)

			requests := reconciler.mapComputeInstanceToExternalIPAttachments(testCtx, ci)
			Expect(requests).To(BeEmpty())
		})
	})

	// --- Cluster target tests ---

	Context("Cluster target resolution", func() {
		var (
			co                *osacv1alpha1.ClusterOrder
			clusterAttachment *osacv1alpha1.ExternalIPAttachment
			clusterKey        types.NamespacedName
		)

		BeforeEach(func() {
			co = &osacv1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testCOName,
					Namespace: testClusterOrderNamespace,
					Labels: map[string]string{
						osacClusterOrderIDLabel: testCOUUID,
					},
				},
				Status: osacv1alpha1.ClusterOrderStatus{
					ApiEndpoint:     testAPIEndpoint,
					IngressEndpoint: testIngressEndpoint,
				},
			}

			clusterAttachment = &osacv1alpha1.ExternalIPAttachment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-attachment",
					Namespace: testNetworkingNamespace,
				},
				Spec: osacv1alpha1.ExternalIPAttachmentSpec{
					ExternalIP:     testExternalIPUUID,
					Cluster:        ptr.To(testCOUUID),
					TargetEndpoint: ptr.To(osacv1alpha1.ExternalIPAttachmentTargetEndpointAPI),
				},
			}

			clusterKey = types.NamespacedName{Name: "cluster-attachment", Namespace: testNetworkingNamespace}
		})

		clusterReconcileOnce := func() (ctrl.Result, error) {
			return reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: clusterKey}})
		}

		It("should auto-detach when ClusterOrder not found", func() {
			fakeClient = buildClient(clusterAttachment, publicIP, pool)
			setupReconciler(fakeClient)

			_, _ = clusterReconcileOnce() // finalizer
			_, _ = clusterReconcileOnce() // auto-detach: sets DeletionTimestamp
			_, _ = clusterReconcileOnce() // handleDelete: removes finalizer

			fetched := &osacv1alpha1.ExternalIPAttachment{}
			err := fakeClient.Get(ctx, types.NamespacedName{
				Namespace: clusterAttachment.Namespace, Name: clusterAttachment.Name,
			}, fetched)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})

		It("should requeue when ClusterOrder has no API endpoint", func() {
			coNoEndpoint := co.DeepCopy()
			coNoEndpoint.Status.ApiEndpoint = ""
			fakeClient = buildClient(clusterAttachment, publicIP, pool, coNoEndpoint)
			setupReconciler(fakeClient)

			_, _ = clusterReconcileOnce() // finalizer

			result, err := clusterReconcileOnce()
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(defaultPreconditionRequeueInterval))
		})

		It("should requeue when ClusterOrder has no ingress endpoint for Ingress target", func() {
			clusterAttachment.Spec.TargetEndpoint = ptr.To(osacv1alpha1.ExternalIPAttachmentTargetEndpointIngress)
			coNoIngress := co.DeepCopy()
			coNoIngress.Status.IngressEndpoint = ""
			fakeClient = buildClient(clusterAttachment, publicIP, pool, coNoIngress)
			setupReconciler(fakeClient)

			_, _ = clusterReconcileOnce() // finalizer

			result, err := clusterReconcileOnce()
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(defaultPreconditionRequeueInterval))
		})

		It("should set target-ip annotation with resolved API endpoint", func() {
			fakeClient = buildClient(clusterAttachment, publicIP, pool, co)
			setupReconciler(fakeClient)

			mockProvider.getProvisionStatusFunc = func(
				ctx context.Context, resource client.Object, jobID string,
			) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID: jobID, State: osacv1alpha1.JobStateRunning, Message: "running",
				}, nil
			}

			_, _ = clusterReconcileOnce() // finalizer
			_, _ = clusterReconcileOnce() // annotations + provisioning

			updated := &osacv1alpha1.ExternalIPAttachment{}
			Expect(fakeClient.Get(testCtx, clusterKey, updated)).To(Succeed())
			Expect(updated.Annotations[osacExternalIPTargetIPAnnotation]).To(Equal(testAPIEndpoint))
			Expect(updated.Annotations[osacImplementationStrategyAnnotation]).To(Equal("metallb-l2"))
		})

		It("should set target-ip annotation with resolved ingress endpoint", func() {
			clusterAttachment.Spec.TargetEndpoint = ptr.To(osacv1alpha1.ExternalIPAttachmentTargetEndpointIngress)
			fakeClient = buildClient(clusterAttachment, publicIP, pool, co)
			setupReconciler(fakeClient)

			mockProvider.getProvisionStatusFunc = func(
				ctx context.Context, resource client.Object, jobID string,
			) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID: jobID, State: osacv1alpha1.JobStateRunning, Message: "running",
				}, nil
			}

			_, _ = clusterReconcileOnce() // finalizer
			_, _ = clusterReconcileOnce() // annotations

			updated := &osacv1alpha1.ExternalIPAttachment{}
			Expect(fakeClient.Get(testCtx, clusterKey, updated)).To(Succeed())
			Expect(updated.Annotations[osacExternalIPTargetIPAnnotation]).To(Equal(testIngressEndpoint))
		})

		It("should add detach finalizer to ClusterOrder", func() {
			fakeClient = buildClient(clusterAttachment, publicIP, pool, co)
			setupReconciler(fakeClient)

			mockProvider.getProvisionStatusFunc = func(
				ctx context.Context, resource client.Object, jobID string,
			) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID: jobID, State: osacv1alpha1.JobStateRunning, Message: "running",
				}, nil
			}

			_, _ = clusterReconcileOnce() // finalizer
			_, _ = clusterReconcileOnce() // resolve CO + add finalizer

			updatedCO := &osacv1alpha1.ClusterOrder{}
			Expect(fakeClient.Get(testCtx, client.ObjectKeyFromObject(co), updatedCO)).To(Succeed())
			Expect(updatedCO.Finalizers).To(ContainElement(osacExternalIPDetachFinalizer))
		})
	})

	Context("Cluster watch mapping", func() {
		var (
			co                *osacv1alpha1.ClusterOrder
			clusterAttachment *osacv1alpha1.ExternalIPAttachment
			clusterKey        types.NamespacedName
		)

		BeforeEach(func() {
			co = &osacv1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testCOName,
					Namespace: testClusterOrderNamespace,
					Labels: map[string]string{
						osacClusterOrderIDLabel: testCOUUID,
					},
				},
				Status: osacv1alpha1.ClusterOrderStatus{
					ApiEndpoint: testAPIEndpoint,
				},
			}

			clusterAttachment = &osacv1alpha1.ExternalIPAttachment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-attachment",
					Namespace: testNetworkingNamespace,
				},
				Spec: osacv1alpha1.ExternalIPAttachmentSpec{
					ExternalIP:     testExternalIPUUID,
					Cluster:        ptr.To(testCOUUID),
					TargetEndpoint: ptr.To(osacv1alpha1.ExternalIPAttachmentTargetEndpointAPI),
				},
			}

			clusterKey = types.NamespacedName{Name: "cluster-attachment", Namespace: testNetworkingNamespace}
			_ = clusterKey // used in other tests
		})

		It("should map ClusterOrder changes to attachment reconcile requests", func() {
			fakeClient = buildClient(clusterAttachment, publicIP, pool, co)
			setupReconciler(fakeClient)

			requests := reconciler.mapClusterOrderToExternalIPAttachments(testCtx, co)
			Expect(requests).To(HaveLen(1))
			Expect(requests[0].NamespacedName).To(Equal(reconcile.Request{
				NamespacedName: clusterKey,
			}.NamespacedName))
		})

		It("should not map ClusterOrder changes to unrelated attachments", func() {
			unrelatedAttachment := clusterAttachment.DeepCopy()
			unrelatedAttachment.Spec.Cluster = ptr.To("other-cluster-uuid")
			fakeClient = buildClient(unrelatedAttachment, publicIP, pool, co)
			setupReconciler(fakeClient)

			requests := reconciler.mapClusterOrderToExternalIPAttachments(testCtx, co)
			Expect(requests).To(BeEmpty())
		})
	})

	Context("Cluster detach finalizer management", func() {
		var co *osacv1alpha1.ClusterOrder

		BeforeEach(func() {
			co = &osacv1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testCOName,
					Namespace: testClusterOrderNamespace,
					Labels: map[string]string{
						osacClusterOrderIDLabel: testCOUUID,
					},
				},
			}
		})

		It("should remove detach finalizer when no other attachments reference the ClusterOrder", func() {
			co.Finalizers = []string{osacExternalIPDetachFinalizer}
			fakeClient = buildClient(co)
			setupReconciler(fakeClient)

			err := reconciler.maybeRemoveCODetachFinalizer(testCtx, testCOUUID, "")
			Expect(err).NotTo(HaveOccurred())

			updatedCO := &osacv1alpha1.ClusterOrder{}
			Expect(fakeClient.Get(testCtx, client.ObjectKeyFromObject(co), updatedCO)).To(Succeed())
			Expect(updatedCO.Finalizers).NotTo(ContainElement(osacExternalIPDetachFinalizer))
		})

		It("should keep detach finalizer when other attachments reference the ClusterOrder", func() {
			excludedAttachment := &osacv1alpha1.ExternalIPAttachment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "excluded-attachment",
					Namespace: testNetworkingNamespace,
				},
				Spec: osacv1alpha1.ExternalIPAttachmentSpec{
					ExternalIP:     "excluded-pip",
					Cluster:        ptr.To(testCOUUID),
					TargetEndpoint: ptr.To(osacv1alpha1.ExternalIPAttachmentTargetEndpointAPI),
				},
			}
			otherAttachment := &osacv1alpha1.ExternalIPAttachment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "other-cluster-attachment",
					Namespace: testNetworkingNamespace,
				},
				Spec: osacv1alpha1.ExternalIPAttachmentSpec{
					ExternalIP:     "other-pip",
					Cluster:        ptr.To(testCOUUID),
					TargetEndpoint: ptr.To(osacv1alpha1.ExternalIPAttachmentTargetEndpointAPI),
				},
			}
			co.Finalizers = []string{osacExternalIPDetachFinalizer}
			fakeClient = buildClient(co, excludedAttachment, otherAttachment)
			setupReconciler(fakeClient)

			err := reconciler.maybeRemoveCODetachFinalizer(testCtx, testCOUUID, "excluded-attachment")
			Expect(err).NotTo(HaveOccurred())

			updatedCO := &osacv1alpha1.ClusterOrder{}
			Expect(fakeClient.Get(testCtx, client.ObjectKeyFromObject(co), updatedCO)).To(Succeed())
			Expect(updatedCO.Finalizers).To(ContainElement(osacExternalIPDetachFinalizer))
		})
	})

	Context("Cluster provisioning lifecycle", func() {
		var (
			co                *osacv1alpha1.ClusterOrder
			clusterAttachment *osacv1alpha1.ExternalIPAttachment
			clusterKey        types.NamespacedName
		)

		BeforeEach(func() {
			co = &osacv1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testCOName,
					Namespace: testClusterOrderNamespace,
					Labels: map[string]string{
						osacClusterOrderIDLabel: testCOUUID,
					},
				},
				Status: osacv1alpha1.ClusterOrderStatus{
					ApiEndpoint: testAPIEndpoint,
				},
			}

			clusterAttachment = &osacv1alpha1.ExternalIPAttachment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-attachment",
					Namespace: testNetworkingNamespace,
				},
				Spec: osacv1alpha1.ExternalIPAttachmentSpec{
					ExternalIP:     testExternalIPUUID,
					Cluster:        ptr.To(testCOUUID),
					TargetEndpoint: ptr.To(osacv1alpha1.ExternalIPAttachmentTargetEndpointAPI),
				},
			}

			clusterKey = types.NamespacedName{Name: "cluster-attachment", Namespace: testNetworkingNamespace}
		})

		clusterReconcileOnce := func() (ctrl.Result, error) {
			return reconciler.Reconcile(testCtx, mcreconcile.Request{Request: ctrl.Request{NamespacedName: clusterKey}})
		}

		It("should set phase to Ready on successful provision with cluster target", func() {
			fakeClient = buildClient(clusterAttachment, publicIP, pool, co)
			setupReconciler(fakeClient)

			mockProvider.getProvisionStatusFunc = func(
				ctx context.Context, resource client.Object, jobID string,
			) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID: jobID, State: osacv1alpha1.JobStateSucceeded, Message: "done",
				}, nil
			}

			_, _ = clusterReconcileOnce() // finalizer
			_, _ = clusterReconcileOnce() // annotations + trigger
			_, _ = clusterReconcileOnce() // poll -> Ready

			updated := &osacv1alpha1.ExternalIPAttachment{}
			Expect(fakeClient.Get(testCtx, clusterKey, updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(osacv1alpha1.ExternalIPAttachmentPhaseReady))
		})

		It("should set ExternalIP.status.attached on provision success with cluster target", func() {
			fakeClient = buildClient(clusterAttachment, publicIP, pool, co)
			setupReconciler(fakeClient)

			pip := &osacv1alpha1.ExternalIP{}
			Expect(fakeClient.Get(testCtx, client.ObjectKeyFromObject(publicIP), pip)).To(Succeed())
			pip.Status.Address = "192.168.1.10"
			Expect(fakeClient.Status().Update(testCtx, pip)).To(Succeed())

			mockProvider.getProvisionStatusFunc = func(
				ctx context.Context, resource client.Object, jobID string,
			) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID: jobID, State: osacv1alpha1.JobStateSucceeded, Message: "done",
				}, nil
			}

			_, _ = clusterReconcileOnce()
			_, _ = clusterReconcileOnce()
			_, _ = clusterReconcileOnce()

			updatedPIP := &osacv1alpha1.ExternalIP{}
			Expect(fakeClient.Get(testCtx, client.ObjectKeyFromObject(publicIP), updatedPIP)).To(Succeed())
			Expect(updatedPIP.Status.Attached).To(BeTrue())
		})

		It("should clear ExternalIP.status.attached on deprovision with cluster target", func() {
			fakeClient = buildClient(clusterAttachment, publicIP, pool, co)
			setupReconciler(fakeClient)

			pip := &osacv1alpha1.ExternalIP{}
			Expect(fakeClient.Get(testCtx, client.ObjectKeyFromObject(publicIP), pip)).To(Succeed())
			pip.Status.Attached = true
			Expect(fakeClient.Status().Update(testCtx, pip)).To(Succeed())

			mockProvider.triggerDeprovisionFunc = func(
				ctx context.Context, resource client.Object, _ []osacv1alpha1.JobStatus,
			) (*provisioning.DeprovisionResult, error) {
				return &provisioning.DeprovisionResult{
					Action: provisioning.DeprovisionSkipped,
				}, nil
			}

			_, _ = clusterReconcileOnce() // finalizer

			toDelete := &osacv1alpha1.ExternalIPAttachment{}
			Expect(fakeClient.Get(testCtx, clusterKey, toDelete)).To(Succeed())
			now := metav1.Now()
			toDelete.DeletionTimestamp = &now

			_, _ = reconciler.handleDelete(testCtx, toDelete)

			updatedPIP := &osacv1alpha1.ExternalIP{}
			Expect(fakeClient.Get(testCtx, client.ObjectKeyFromObject(publicIP), updatedPIP)).To(Succeed())
			Expect(updatedPIP.Status.Attached).To(BeFalse())
		})
	})

	// --- BareMetalInstance target tests ---

	Context("BareMetalInstance target resolution", func() {
		var (
			bmi           *bmfov1alpha1.BareMetalInstance
			bmiAttachment *osacv1alpha1.ExternalIPAttachment
			bmiKey        types.NamespacedName
		)

		BeforeEach(func() {
			bmi = &bmfov1alpha1.BareMetalInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testBMIName,
					Namespace: testBaremetalInstanceNamespace,
					Labels: map[string]string{
						osacBareMetalInstanceIDLabel: testBMIUUID,
					},
				},
				Spec: bmfov1alpha1.BareMetalInstanceSpec{
					HostType:       "compute",
					ExternalHostID: "ext-host-1",
				},
			}

			bmiAttachment = &osacv1alpha1.ExternalIPAttachment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "bmi-attachment",
					Namespace: testNetworkingNamespace,
				},
				Spec: osacv1alpha1.ExternalIPAttachmentSpec{
					ExternalIP:        testExternalIPUUID,
					BaremetalInstance: ptr.To(testBMIUUID),
				},
			}

			bmiKey = types.NamespacedName{
				Name: "bmi-attachment", Namespace: testNetworkingNamespace,
			}
		})

		bmiReconcileOnce := func() (ctrl.Result, error) {
			return reconciler.Reconcile(testCtx, mcreconcile.Request{
				Request: ctrl.Request{NamespacedName: bmiKey},
			})
		}

		It("should auto-detach when BareMetalInstance not found", func() {
			fakeClient = buildClient(bmiAttachment, publicIP, pool)
			setupReconciler(fakeClient)

			_, _ = bmiReconcileOnce() // finalizer
			_, _ = bmiReconcileOnce() // auto-detach: sets DeletionTimestamp
			_, _ = bmiReconcileOnce() // handleDelete: removes finalizer

			fetched := &osacv1alpha1.ExternalIPAttachment{}
			err := fakeClient.Get(ctx, types.NamespacedName{
				Namespace: bmiAttachment.Namespace, Name: bmiAttachment.Name,
			}, fetched)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})

		It("should add detach finalizer to BareMetalInstance", func() {
			fakeClient = buildClient(bmiAttachment, publicIP, pool, bmi)
			setupReconciler(fakeClient)

			mockProvider.getProvisionStatusFunc = func(
				ctx context.Context, resource client.Object, jobID string,
			) (provisioning.ProvisionStatus, error) {
				return provisioning.ProvisionStatus{
					JobID: jobID, State: osacv1alpha1.JobStateRunning, Message: "running",
				}, nil
			}

			_, _ = bmiReconcileOnce() // finalizer
			_, _ = bmiReconcileOnce() // resolve + annotations

			updatedBMI := &bmfov1alpha1.BareMetalInstance{}
			Expect(fakeClient.Get(testCtx, client.ObjectKeyFromObject(bmi), updatedBMI)).To(Succeed())
			Expect(updatedBMI.Finalizers).To(ContainElement(osacExternalIPDetachFinalizer))
		})

		It("should trigger delete when BareMetalInstance is being deleted", func() {
			deletingBMI := bmi.DeepCopy()
			now := metav1.Now()
			deletingBMI.DeletionTimestamp = &now
			deletingBMI.Finalizers = []string{osacExternalIPDetachFinalizer}

			bmiAttachment.Finalizers = []string{osacExternalIPAttachmentFinalizer}
			fakeClient = buildClient(bmiAttachment, publicIP, pool, bmi)
			setupReconciler(fakeClient)

			fetchedBMI := &bmfov1alpha1.BareMetalInstance{}
			Expect(fakeClient.Get(testCtx, client.ObjectKeyFromObject(bmi), fetchedBMI)).To(Succeed())
			fetchedBMI.Finalizers = []string{osacExternalIPDetachFinalizer}
			Expect(fakeClient.Update(testCtx, fetchedBMI)).To(Succeed())

			requests := reconciler.mapBaremetalInstanceToExternalIPAttachments(testCtx, bmi)
			Expect(requests).To(HaveLen(1))
		})

		It("should map BareMetalInstance changes to attachment reconcile requests", func() {
			fakeClient = buildClient(bmiAttachment, publicIP, pool, bmi)
			setupReconciler(fakeClient)

			requests := reconciler.mapBaremetalInstanceToExternalIPAttachments(testCtx, bmi)
			Expect(requests).To(HaveLen(1))
			Expect(requests[0].NamespacedName).To(Equal(bmiKey))
		})

		It("should not map BareMetalInstance changes to unrelated attachments", func() {
			fakeClient = buildClient(attachment, publicIP, pool, bmi)
			setupReconciler(fakeClient)

			requests := reconciler.mapBaremetalInstanceToExternalIPAttachments(testCtx, bmi)
			Expect(requests).To(BeEmpty())
		})
	})

	Context("BareMetalInstance detach finalizer cleanup", func() {
		var bmi *bmfov1alpha1.BareMetalInstance

		BeforeEach(func() {
			bmi = &bmfov1alpha1.BareMetalInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testBMIName,
					Namespace: testBaremetalInstanceNamespace,
					Labels: map[string]string{
						osacBareMetalInstanceIDLabel: testBMIUUID,
					},
				},
				Spec: bmfov1alpha1.BareMetalInstanceSpec{
					HostType:       "compute",
					ExternalHostID: "ext-host-1",
				},
			}
		})

		It("should remove detach finalizer when no other attachments reference the BMI", func() {
			bmi.Finalizers = []string{osacExternalIPDetachFinalizer}
			excluded := &osacv1alpha1.ExternalIPAttachment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "excluded-attachment",
					Namespace: testNetworkingNamespace,
				},
				Spec: osacv1alpha1.ExternalIPAttachmentSpec{
					ExternalIP:        "excluded-pip",
					BaremetalInstance: ptr.To(testBMIUUID),
				},
			}
			fakeClient = buildClient(bmi, excluded)
			setupReconciler(fakeClient)

			err := reconciler.maybeRemoveBMIDetachFinalizer(testCtx, testBMIUUID, "excluded-attachment")
			Expect(err).NotTo(HaveOccurred())

			updatedBMI := &bmfov1alpha1.BareMetalInstance{}
			Expect(fakeClient.Get(testCtx, client.ObjectKeyFromObject(bmi), updatedBMI)).To(Succeed())
			Expect(updatedBMI.Finalizers).NotTo(ContainElement(osacExternalIPDetachFinalizer))
		})

		It("should keep detach finalizer when other attachments reference the BMI", func() {
			excluded := &osacv1alpha1.ExternalIPAttachment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "excluded-attachment",
					Namespace: testNetworkingNamespace,
				},
				Spec: osacv1alpha1.ExternalIPAttachmentSpec{
					ExternalIP:        "excluded-pip",
					BaremetalInstance: ptr.To(testBMIUUID),
				},
			}
			other := &osacv1alpha1.ExternalIPAttachment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "other-bmi-attachment",
					Namespace: testNetworkingNamespace,
				},
				Spec: osacv1alpha1.ExternalIPAttachmentSpec{
					ExternalIP:        "other-pip",
					BaremetalInstance: ptr.To(testBMIUUID),
				},
			}
			bmi.Finalizers = []string{osacExternalIPDetachFinalizer}
			fakeClient = buildClient(bmi, excluded, other)
			setupReconciler(fakeClient)

			err := reconciler.maybeRemoveBMIDetachFinalizer(testCtx, testBMIUUID, "excluded-attachment")
			Expect(err).NotTo(HaveOccurred())

			updatedBMI := &bmfov1alpha1.BareMetalInstance{}
			Expect(fakeClient.Get(testCtx, client.ObjectKeyFromObject(bmi), updatedBMI)).To(Succeed())
			Expect(updatedBMI.Finalizers).To(ContainElement(osacExternalIPDetachFinalizer))
		})
	})
})

var _ = Describe("ExternalIPAttachment tenant VPC resolution", func() {
	const (
		nsNet      = "test-networking"
		subnetUUID = "subnet-uuid-1"
		vnUUID     = "vn-uuid-1"
		vnName     = "virtualnetwork-gctnt"
	)
	var (
		vpcScheme *runtime.Scheme
		vpcCtx    context.Context
	)

	BeforeEach(func() {
		vpcCtx = context.TODO()
		vpcScheme = runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(vpcScheme)).To(Succeed())
		Expect(bmfov1alpha1.AddToScheme(vpcScheme)).To(Succeed())
	})

	newVPCReconciler := func(objs ...client.Object) *ExternalIPAttachmentReconciler {
		c := fake.NewClientBuilder().WithScheme(vpcScheme).WithObjects(objs...).Build()
		return &ExternalIPAttachmentReconciler{Client: c, NetworkingNamespace: nsNet}
	}
	subnetCR := func(name string) *osacv1alpha1.Subnet {
		return &osacv1alpha1.Subnet{
			ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: nsNet,
				Labels: map[string]string{osacSubnetIDLabel: subnetUUID},
			},
			Spec: osacv1alpha1.SubnetSpec{VirtualNetwork: vnUUID},
		}
	}
	vnCR := func() *osacv1alpha1.VirtualNetwork {
		return &osacv1alpha1.VirtualNetwork{
			ObjectMeta: metav1.ObjectMeta{
				Name: vnName, Namespace: nsNet,
				Labels: map[string]string{osacVirtualNetworkIDLabel: vnUUID},
			},
		}
	}

	It("resolves the VN name from a BMI primary subnet (looked up by UUID)", func() {
		bmi := &bmfov1alpha1.BareMetalInstance{
			Status: bmfov1alpha1.BareMetalInstanceStatus{
				NetworkAttachmentStatuses: []bmfov1alpha1.BareMetalNetworkAttachmentStatus{
					{Primary: true, IPAddress: "10.100.0.2", SubnetRef: subnetUUID},
				},
			},
		}
		r := newVPCReconciler(subnetCR("subnet-gxx2l"), vnCR())
		name, res, err := r.resolveTargetVirtualNetworkName(vpcCtx, nil, bmi)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(BeZero())
		Expect(name).To(Equal(vnName))
	})

	It("resolves the VN name from a ComputeInstance primary subnet (looked up by name)", func() {
		ci := &osacv1alpha1.ComputeInstance{
			Spec: osacv1alpha1.ComputeInstanceSpec{
				NetworkAttachments: []osacv1alpha1.ComputeNetworkAttachment{{SubnetRef: "subnet-cr-name"}},
			},
		}
		r := newVPCReconciler(subnetCR("subnet-cr-name"), vnCR())
		name, res, err := r.resolveTargetVirtualNetworkName(vpcCtx, ci, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(BeZero())
		Expect(name).To(Equal(vnName))
	})

	It("returns empty for a non-tenant (cluster) target", func() {
		r := newVPCReconciler()
		name, res, err := r.resolveTargetVirtualNetworkName(vpcCtx, nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(BeZero())
		Expect(name).To(BeEmpty())
	})

	It("requeues when the subnet CR is not present yet", func() {
		bmi := &bmfov1alpha1.BareMetalInstance{
			Status: bmfov1alpha1.BareMetalInstanceStatus{
				NetworkAttachmentStatuses: []bmfov1alpha1.BareMetalNetworkAttachmentStatus{
					{Primary: true, IPAddress: "10.100.0.2", SubnetRef: subnetUUID},
				},
			},
		}
		r := newVPCReconciler(vnCR()) // subnet not seeded
		name, res, err := r.resolveTargetVirtualNetworkName(vpcCtx, nil, bmi)
		Expect(err).NotTo(HaveOccurred())
		Expect(name).To(BeEmpty())
		Expect(res.RequeueAfter).To(BeNumerically(">", 0))
	})
})

var _ = Describe("primaryBMISubnetRef", func() {
	It("returns the primary attachment's subnet ref", func() {
		bmi := &bmfov1alpha1.BareMetalInstance{Status: bmfov1alpha1.BareMetalInstanceStatus{
			NetworkAttachmentStatuses: []bmfov1alpha1.BareMetalNetworkAttachmentStatus{
				{Primary: false, IPAddress: "10.0.0.5", SubnetRef: "s-secondary"},
				{Primary: true, IPAddress: "10.0.0.2", SubnetRef: "s-primary"},
			},
		}}
		Expect(primaryBMISubnetRef(bmi)).To(Equal("s-primary"))
	})
	It("treats a single attachment as implicitly primary", func() {
		bmi := &bmfov1alpha1.BareMetalInstance{Status: bmfov1alpha1.BareMetalInstanceStatus{
			NetworkAttachmentStatuses: []bmfov1alpha1.BareMetalNetworkAttachmentStatus{
				{SubnetRef: "only", IPAddress: "10.0.0.9"},
			},
		}}
		Expect(primaryBMISubnetRef(bmi)).To(Equal("only"))
	})
	It("returns empty when multiple attachments and none is primary", func() {
		bmi := &bmfov1alpha1.BareMetalInstance{Status: bmfov1alpha1.BareMetalInstanceStatus{
			NetworkAttachmentStatuses: []bmfov1alpha1.BareMetalNetworkAttachmentStatus{
				{SubnetRef: "a"}, {SubnetRef: "b"},
			},
		}}
		Expect(primaryBMISubnetRef(bmi)).To(BeEmpty())
	})
})
