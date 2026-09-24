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
	stderrors "errors"
	"time"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

func readyClusterOrderNodePool(resourceClass string, replicas int32) hypershiftv1beta1.NodePool {
	return hypershiftv1beta1.NodePool{
		ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{agentResourceClassLabel: resourceClass}},
		Status: hypershiftv1beta1.NodePoolStatus{
			Replicas: replicas,
			Conditions: []hypershiftv1beta1.NodePoolCondition{
				{Type: hypershiftv1beta1.NodePoolAllMachinesReadyConditionType, Status: corev1.ConditionTrue},
				{Type: hypershiftv1beta1.NodePoolReadyConditionType, Status: corev1.ConditionTrue},
			},
		},
	}
}

type hostedClusterListErrorClient struct {
	client.Client
	err error
}

func (c hostedClusterListErrorClient) List(
	ctx context.Context, list client.ObjectList, opts ...client.ListOption,
) error {
	if _, ok := list.(*hypershiftv1beta1.HostedClusterList); ok {
		return c.err
	}
	return c.Client.List(ctx, list, opts...)
}

var _ = Describe("ClusterOrder Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default", // TODO(user):Modify as needed
		}
		clusterorder := &v1alpha1.ClusterOrder{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind ClusterOrder")
			err := k8sClient.Get(ctx, typeNamespacedName, clusterorder)
			if err != nil && errors.IsNotFound(err) {
				resource := &v1alpha1.ClusterOrder{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: v1alpha1.ClusterOrderSpec{
						TemplateID:     "test",
						AddOnOperators: []string{"operator-one"},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			// TODO(user): Cleanup logic after each test, like removing the resource instance.
			resource := &v1alpha1.ClusterOrder{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance ClusterOrder")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &ClusterOrderReconciler{
				Client:               k8sClient,
				apiReader:            k8sClient,
				Scheme:               k8sClient.Scheme(),
				ProvisioningProvider: noopProvisioningProvider{},
				MaxJobHistory:        provisioning.DefaultMaxJobHistory,
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			stored := &v1alpha1.ClusterOrder{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, stored)).To(Succeed())
			Expect(stored.Spec.AddOnOperators).To(Equal([]string{"operator-one"}))
		})
	})

	Context("When adding the controller finalizer", func() {
		It("should preserve a concurrent storage finalizer", func() {
			ctx := context.Background()
			initial := &v1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "concurrent-finalizer",
					Namespace: "default",
				},
				Spec: v1alpha1.ClusterOrderSpec{
					TemplateID:     "test",
					AddOnOperators: []string{"operator-one"},
				},
			}
			conflictClient := fake.NewClientBuilder().
				WithScheme(k8sClient.Scheme()).
				WithObjects(initial).
				WithInterceptorFuncs(interceptor.Funcs{
					Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
						latest := &v1alpha1.ClusterOrder{}
						Expect(c.Get(ctx, client.ObjectKeyFromObject(obj), latest)).To(Succeed())
						latest.Finalizers = append(latest.Finalizers, clusterStorageFinalizer)
						Expect(c.Update(ctx, latest)).To(Succeed())
						return c.Patch(ctx, obj, patch, opts...)
					},
				}).
				Build()

			instance := &v1alpha1.ClusterOrder{}
			key := client.ObjectKeyFromObject(initial)
			Expect(conflictClient.Get(ctx, key, instance)).To(Succeed())

			reconciler := &ClusterOrderReconciler{
				Client:               conflictClient,
				apiReader:            conflictClient,
				Scheme:               k8sClient.Scheme(),
				ProvisioningProvider: noopProvisioningProvider{},
				MaxJobHistory:        provisioning.DefaultMaxJobHistory,
			}
			_, err := reconciler.handleUpdate(ctx, reconcile.Request{NamespacedName: key}, instance)
			Expect(errors.IsConflict(err)).To(BeTrue())

			stored := &v1alpha1.ClusterOrder{}
			Expect(conflictClient.Get(ctx, key, stored)).To(Succeed())
			Expect(stored.Finalizers).To(ConsistOf(clusterStorageFinalizer))
		})
	})

	Context("EvaluateAction (formerly shouldTriggerProvision)", func() {
		ctx := context.Background()

		evaluateAction := func(instance *v1alpha1.ClusterOrder) (provisioning.Action, *v1alpha1.JobStatus) {
			provState := &provisioning.State{
				Jobs:                 &instance.Status.ProvisioningJobs,
				DesiredConfigVersion: instance.Status.DesiredConfigVersion,
			}
			return provisioning.EvaluateAction(provState, func() bool {
				return provisioning.CheckAPIServerForNonTerminalProvisionJob(ctx, k8sClient, client.ObjectKeyFromObject(instance), &v1alpha1.ClusterOrder{}, func(obj client.Object) []v1alpha1.JobStatus {
					return obj.(*v1alpha1.ClusterOrder).Status.ProvisioningJobs
				})
			})
		}

		It("should trigger when no job exists and config versions differ", func() {
			instance := &v1alpha1.ClusterOrder{
				Status: v1alpha1.ClusterOrderStatus{
					DesiredConfigVersion: "abc123",
				},
			}
			action, job := evaluateAction(instance)
			Expect(action).To(Equal(provisioning.Trigger))
			Expect(job).To(BeNil())
		})

		It("should trigger when job has empty ID and config versions differ", func() {
			instance := &v1alpha1.ClusterOrder{
				Status: v1alpha1.ClusterOrderStatus{
					DesiredConfigVersion: "abc123",
					ProvisioningJobs:     []v1alpha1.JobStatus{{Type: v1alpha1.JobTypeProvision, JobID: ""}},
				},
			}
			action, _ := evaluateAction(instance)
			Expect(action).To(Equal(provisioning.Trigger))
		})

		It("should trigger when no job exists", func() {
			instance := &v1alpha1.ClusterOrder{
				Status: v1alpha1.ClusterOrderStatus{
					DesiredConfigVersion: "abc123",
				},
			}
			action, job := evaluateAction(instance)
			Expect(action).To(Equal(provisioning.Trigger))
			Expect(job).To(BeNil())
		})

		It("should poll when job is still running", func() {
			instance := &v1alpha1.ClusterOrder{
				Status: v1alpha1.ClusterOrderStatus{
					ProvisioningJobs: []v1alpha1.JobStatus{{Type: v1alpha1.JobTypeProvision, JobID: "job-1", State: v1alpha1.JobStateRunning}},
				},
			}
			action, job := evaluateAction(instance)
			Expect(action).To(Equal(provisioning.Poll))
			Expect(job).NotTo(BeNil())
			Expect(job.JobID).To(Equal("job-1"))
		})

		It("should poll when job is pending", func() {
			instance := &v1alpha1.ClusterOrder{
				Status: v1alpha1.ClusterOrderStatus{
					ProvisioningJobs: []v1alpha1.JobStatus{{Type: v1alpha1.JobTypeProvision, JobID: "job-1", State: v1alpha1.JobStatePending}},
				},
			}
			action, job := evaluateAction(instance)
			Expect(action).To(Equal(provisioning.Poll))
			Expect(job).NotTo(BeNil())
		})

		It("should skip when job succeeded with matching ConfigVersion", func() {
			instance := &v1alpha1.ClusterOrder{
				Status: v1alpha1.ClusterOrderStatus{
					DesiredConfigVersion: "abc123",
					ProvisioningJobs:     []v1alpha1.JobStatus{{Type: v1alpha1.JobTypeProvision, JobID: "job-1", State: v1alpha1.JobStateSucceeded, ConfigVersion: "abc123"}},
				},
			}
			action, job := evaluateAction(instance)
			Expect(action).To(Equal(provisioning.Skip))
			Expect(job).NotTo(BeNil())
		})

		It("should trigger when job succeeded but ConfigVersion differs", func() {
			instance := &v1alpha1.ClusterOrder{
				Status: v1alpha1.ClusterOrderStatus{
					DesiredConfigVersion: "new-version",
					ProvisioningJobs:     []v1alpha1.JobStatus{{Type: v1alpha1.JobTypeProvision, JobID: "job-1", State: v1alpha1.JobStateSucceeded, ConfigVersion: "old-version"}},
				},
			}
			action, job := evaluateAction(instance)
			Expect(action).To(Equal(provisioning.Trigger))
			Expect(job).NotTo(BeNil())
		})

		It("should trigger when job failed with different ConfigVersion", func() {
			instance := &v1alpha1.ClusterOrder{
				Status: v1alpha1.ClusterOrderStatus{
					DesiredConfigVersion: "new-version",
					ProvisioningJobs:     []v1alpha1.JobStatus{{Type: v1alpha1.JobTypeProvision, JobID: "job-1", State: v1alpha1.JobStateFailed, ConfigVersion: "old-version"}},
				},
			}
			action, job := evaluateAction(instance)
			Expect(action).To(Equal(provisioning.Trigger))
			Expect(job).NotTo(BeNil())
		})

		It("should skip when latest job succeeded with matching ConfigVersion", func() {
			instance := &v1alpha1.ClusterOrder{
				Status: v1alpha1.ClusterOrderStatus{
					DesiredConfigVersion: "abc123",
					ProvisioningJobs: []v1alpha1.JobStatus{{
						Type:          v1alpha1.JobTypeProvision,
						JobID:         "job-1",
						State:         v1alpha1.JobStateSucceeded,
						ConfigVersion: "abc123",
					}},
				},
			}
			action, job := evaluateAction(instance)
			Expect(action).To(Equal(provisioning.Skip))
			Expect(job).NotTo(BeNil())
		})

		It("should backoff when latest job failed with matching ConfigVersion", func() {
			instance := &v1alpha1.ClusterOrder{
				Status: v1alpha1.ClusterOrderStatus{
					DesiredConfigVersion: "abc123",
					ProvisioningJobs: []v1alpha1.JobStatus{{
						Type:          v1alpha1.JobTypeProvision,
						JobID:         "job-1",
						State:         v1alpha1.JobStateFailed,
						ConfigVersion: "abc123",
					}},
				},
			}
			action, job := evaluateAction(instance)
			Expect(action).To(Equal(provisioning.Backoff))
			Expect(job).NotTo(BeNil())
		})

		It("should trigger when latest job failed with different ConfigVersion", func() {
			instance := &v1alpha1.ClusterOrder{
				Status: v1alpha1.ClusterOrderStatus{
					DesiredConfigVersion: "new-version",
					ProvisioningJobs: []v1alpha1.JobStatus{{
						Type:          v1alpha1.JobTypeProvision,
						JobID:         "job-1",
						State:         v1alpha1.JobStateFailed,
						ConfigVersion: "old-version",
					}},
				},
			}
			action, job := evaluateAction(instance)
			Expect(action).To(Equal(provisioning.Trigger))
			Expect(job).NotTo(BeNil())
		})

		It("should trigger when latest job succeeded with different ConfigVersion", func() {
			instance := &v1alpha1.ClusterOrder{
				Status: v1alpha1.ClusterOrderStatus{
					DesiredConfigVersion: "new-version",
					ProvisioningJobs: []v1alpha1.JobStatus{{
						Type:          v1alpha1.JobTypeProvision,
						JobID:         "job-1",
						State:         v1alpha1.JobStateSucceeded,
						ConfigVersion: "old-version",
					}},
				},
			}
			action, job := evaluateAction(instance)
			Expect(action).To(Equal(provisioning.Trigger))
			Expect(job).NotTo(BeNil())
		})

		It("should requeue when API server has non-terminal job but cache shows none", func() {
			instanceName := "test-co-api-server-check"
			apiInstance := &v1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      instanceName,
					Namespace: "default",
				},
				Spec: v1alpha1.ClusterOrderSpec{
					TemplateID: "test",
				},
			}
			Expect(k8sClient.Create(ctx, apiInstance)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, apiInstance)
			})

			jobTimestamp := metav1.NewTime(time.Now().UTC())
			apiInstance.Status.DesiredConfigVersion = "v1"
			apiInstance.Status.ProvisioningJobs = []v1alpha1.JobStatus{
				{Type: v1alpha1.JobTypeProvision, JobID: "running-job", State: v1alpha1.JobStateRunning, Timestamp: jobTimestamp},
			}
			Expect(k8sClient.Status().Update(ctx, apiInstance)).To(Succeed())

			staleInstance := &v1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      instanceName,
					Namespace: "default",
				},
				Status: v1alpha1.ClusterOrderStatus{
					DesiredConfigVersion: "v1",
				},
			}

			action, job := evaluateAction(staleInstance)
			Expect(action).To(Equal(provisioning.Requeue))
			Expect(job).To(BeNil())
		})
	})

	Context("handleProvisioning", func() {
		var reconciler *ClusterOrderReconciler

		BeforeEach(func() {
			reconciler = &ClusterOrderReconciler{
				Client:               k8sClient,
				apiReader:            k8sClient,
				Scheme:               k8sClient.Scheme(),
				ProvisioningProvider: &mockProvisioningProvider{},
			}
		})

		ctx := context.Background()

		It("should skip provisioning when ManagementStateManual annotation is set", func() {
			instance := &v1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						osacManagementStateAnnotation: ManagementStateManual,
					},
				},
				Status: v1alpha1.ClusterOrderStatus{DesiredConfigVersion: "v1"},
			}

			result, err := reconciler.handleProvisioning(ctx, instance)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())
			latestProvisionJob := provisioning.FindLatestJobByType(instance.Status.ProvisioningJobs, v1alpha1.JobTypeProvision)
			Expect(latestProvisionJob).To(BeNil())
		})
	})

	Context("handleDeprovisioning", func() {
		var reconciler *ClusterOrderReconciler

		BeforeEach(func() {
			reconciler = &ClusterOrderReconciler{
				Client:               k8sClient,
				apiReader:            k8sClient,
				Scheme:               k8sClient.Scheme(),
				ProvisioningProvider: &mockProvisioningProvider{},
			}
		})

		ctx := context.Background()

		It("should skip deprovisioning when ManagementStateManual annotation is set", func() {
			instance := &v1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						osacManagementStateAnnotation: ManagementStateManual,
					},
				},
			}

			result, err := reconciler.handleDeprovisioning(ctx, instance)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())
			latestDeprovisionJob := provisioning.FindLatestJobByType(instance.Status.ProvisioningJobs, v1alpha1.JobTypeDeprovision)
			Expect(latestDeprovisionJob).To(BeNil())
		})
	})

	Context("handleDesiredConfigVersion", func() {
		It("should produce consistent hash for same spec", func() {
			reconciler := &ClusterOrderReconciler{}
			instance := &v1alpha1.ClusterOrder{
				Spec: v1alpha1.ClusterOrderSpec{
					TemplateID: "test-template",
				},
			}
			err := reconciler.handleDesiredConfigVersion(instance)
			Expect(err).NotTo(HaveOccurred())
			Expect(instance.Status.DesiredConfigVersion).NotTo(BeEmpty())

			firstHash := instance.Status.DesiredConfigVersion
			err = reconciler.handleDesiredConfigVersion(instance)
			Expect(err).NotTo(HaveOccurred())
			Expect(instance.Status.DesiredConfigVersion).To(Equal(firstHash))
		})

		It("should produce different hash for different spec", func() {
			reconciler := &ClusterOrderReconciler{}
			instance1 := &v1alpha1.ClusterOrder{
				Spec: v1alpha1.ClusterOrderSpec{TemplateID: "template-a"},
			}
			instance2 := &v1alpha1.ClusterOrder{
				Spec: v1alpha1.ClusterOrderSpec{TemplateID: "template-b"},
			}
			Expect(reconciler.handleDesiredConfigVersion(instance1)).To(Succeed())
			Expect(reconciler.handleDesiredConfigVersion(instance2)).To(Succeed())
			Expect(instance1.Status.DesiredConfigVersion).NotTo(Equal(instance2.Status.DesiredConfigVersion))
		})
	})

	Context("management-state unmanaged with deletion", func() {
		ctx := context.Background()

		It("should still handle delete for unmanaged ClusterOrder with finalizer", func() {
			managedThenUnmanaged := &v1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "managed-then-unmanaged",
					Namespace: "default",
					Annotations: map[string]string{
						osacManagementStateAnnotation: ManagementStateUnmanaged,
					},
					Finalizers: []string{osacFinalizer},
				},
				Spec: v1alpha1.ClusterOrderSpec{
					TemplateID: "test",
				},
			}
			Expect(k8sClient.Create(ctx, managedThenUnmanaged)).To(Succeed())

			key := types.NamespacedName{Name: managedThenUnmanaged.Name, Namespace: managedThenUnmanaged.Namespace}

			controllerReconciler := &ClusterOrderReconciler{
				Client:               k8sClient,
				apiReader:            k8sClient,
				Scheme:               k8sClient.Scheme(),
				ProvisioningProvider: noopProvisioningProvider{},
				MaxJobHistory:        provisioning.DefaultMaxJobHistory,
			}

			Expect(k8sClient.Delete(ctx, managedThenUnmanaged)).To(Succeed())

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: key,
			})
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() bool {
				return errors.IsNotFound(k8sClient.Get(ctx, key, &v1alpha1.ClusterOrder{}))
			}, 5*time.Second, 100*time.Millisecond).Should(BeTrue())
		})
	})

	Context("handleHostedCluster", func() {
		var reconciler *ClusterOrderReconciler

		BeforeEach(func() {
			reconciler = &ClusterOrderReconciler{
				Client:               k8sClient,
				apiReader:            k8sClient,
				Scheme:               k8sClient.Scheme(),
				ProvisioningProvider: noopProvisioningProvider{},
			}
		})

		ctx := context.Background()

		It("should set Phase to Ready when HostedCluster is ready", func() {
			instance := &v1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hc-no-phase",
					Namespace: "default",
				},
				Status: v1alpha1.ClusterOrderStatus{
					Phase: v1alpha1.ClusterOrderPhaseProgressing,
				},
			}

			hc := &hypershiftv1beta1.HostedCluster{
				Status: hypershiftv1beta1.HostedClusterStatus{
					Conditions: []metav1.Condition{
						{Type: string(hypershiftv1beta1.HostedClusterAvailable), Status: metav1.ConditionTrue, LastTransitionTime: metav1.NewTime(time.Now().UTC()), Reason: "Ready"},
						{Type: string(hypershiftv1beta1.HostedClusterDegraded), Status: metav1.ConditionFalse, LastTransitionTime: metav1.NewTime(time.Now().UTC()), Reason: "Ready"},
						{Type: string(hypershiftv1beta1.ClusterVersionSucceeding), Status: metav1.ConditionTrue, LastTransitionTime: metav1.NewTime(time.Now().UTC()), Reason: "Ready"},
					},
				},
			}
			err := reconciler.handleHostedCluster(ctx, instance, hc)
			Expect(err).NotTo(HaveOccurred())

			Expect(instance.Status.Phase).To(Equal(v1alpha1.ClusterOrderPhaseReady))

			Expect(instance.IsStatusConditionTrue(v1alpha1.ConditionControlPlaneAvailable)).To(BeTrue())
			Expect(instance.IsStatusConditionTrue(v1alpha1.ConditionClusterAvailable)).To(BeTrue())
		})

		It("should finalize Ready after provisioning and live worker readiness", func() {
			instance := &v1alpha1.ClusterOrder{
				Status: v1alpha1.ClusterOrderStatus{
					Phase: v1alpha1.ClusterOrderPhaseProgressing,
					ProvisioningJobs: []v1alpha1.JobStatus{{
						Type:  v1alpha1.JobTypeProvision,
						State: v1alpha1.JobStateSucceeded,
					}},
				},
				Spec: v1alpha1.ClusterOrderSpec{
					NodeRequests: []v1alpha1.NodeRequest{{ResourceClass: "worker", NumberOfNodes: 1}},
				},
			}
			hc := &hypershiftv1beta1.HostedCluster{Status: hypershiftv1beta1.HostedClusterStatus{Conditions: []metav1.Condition{
				{Type: string(hypershiftv1beta1.KubeAPIServerAvailable), Status: metav1.ConditionTrue},
				{Type: string(hypershiftv1beta1.HostedClusterAvailable), Status: metav1.ConditionTrue},
				{Type: string(hypershiftv1beta1.HostedClusterDegraded), Status: metav1.ConditionFalse},
				{Type: string(hypershiftv1beta1.ClusterVersionSucceeding), Status: metav1.ConditionTrue},
			}}}
			nodePools := []hypershiftv1beta1.NodePool{readyClusterOrderNodePool("worker", 1)}

			Expect(finalizeReadyIfProvisioned(logr.Discard(), instance, hc, nodePools)).To(BeTrue())
			Expect(instance.Status.Phase).To(Equal(v1alpha1.ClusterOrderPhaseReady))
			progressing := apimeta.FindStatusCondition(instance.Status.Conditions, v1alpha1.ConditionProgressing)
			Expect(progressing).NotTo(BeNil())
			Expect(progressing.Status).To(Equal(metav1.ConditionFalse))
			Expect(progressing.Reason).To(Equal(v1alpha1.ReasonAsExpected))
		})

		It("should finalize Ready through handleHostedCluster", func() {
			instance := &v1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{Name: "test-hc-finalize-ready", Namespace: "default"},
				Status: v1alpha1.ClusterOrderStatus{
					Phase: v1alpha1.ClusterOrderPhaseProgressing,
					ProvisioningJobs: []v1alpha1.JobStatus{{
						Type:  v1alpha1.JobTypeProvision,
						State: v1alpha1.JobStateSucceeded,
					}},
				},
				Spec: v1alpha1.ClusterOrderSpec{
					NodeRequests: []v1alpha1.NodeRequest{{ResourceClass: "worker", NumberOfNodes: 1}},
				},
			}
			hc := &hypershiftv1beta1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test-hc-finalize-ready", Namespace: "default"},
				Status: hypershiftv1beta1.HostedClusterStatus{Conditions: []metav1.Condition{
					{Type: string(hypershiftv1beta1.KubeAPIServerAvailable), Status: metav1.ConditionTrue},
					{Type: string(hypershiftv1beta1.HostedClusterAvailable), Status: metav1.ConditionTrue},
					{Type: string(hypershiftv1beta1.HostedClusterDegraded), Status: metav1.ConditionFalse},
					{Type: string(hypershiftv1beta1.ClusterVersionSucceeding), Status: metav1.ConditionTrue},
				}},
			}
			nodePool := readyClusterOrderNodePool("worker", 1)
			nodePool.ObjectMeta = metav1.ObjectMeta{
				Name:      "test-hc-finalize-ready-worker",
				Namespace: "default",
				Labels: map[string]string{
					osacClusterOrderNameLabel: instance.Name,
					agentResourceClassLabel:   "worker",
				},
			}
			reconciler.Client = fake.NewClientBuilder().
				WithScheme(k8sClient.Scheme()).
				WithObjects(&nodePool).
				Build()

			Expect(reconciler.handleHostedCluster(ctx, instance, hc)).To(Succeed())
			Expect(instance.Status.Phase).To(Equal(v1alpha1.ClusterOrderPhaseReady))
			progressing := apimeta.FindStatusCondition(instance.Status.Conditions, v1alpha1.ConditionProgressing)
			Expect(progressing).NotTo(BeNil())
			Expect(progressing.Status).To(Equal(metav1.ConditionFalse))
		})

		DescribeTable("should require every NodePool to match its requested capacity",
			func(requests []v1alpha1.NodeRequest, nodePools []hypershiftv1beta1.NodePool, expected bool) {
				Expect(nodePoolsMatchRequests(requests, nodePools)).To(Equal(expected))
			},
			Entry("all pools match", []v1alpha1.NodeRequest{
				{ResourceClass: "gpu", NumberOfNodes: 2},
				{ResourceClass: "worker", NumberOfNodes: 3},
			}, []hypershiftv1beta1.NodePool{
				readyClusterOrderNodePool("gpu", 2), readyClusterOrderNodePool("worker", 3),
			}, true),
			Entry("one pool is under capacity", []v1alpha1.NodeRequest{
				{ResourceClass: "gpu", NumberOfNodes: 2},
				{ResourceClass: "worker", NumberOfNodes: 3},
			}, []hypershiftv1beta1.NodePool{
				readyClusterOrderNodePool("gpu", 1), readyClusterOrderNodePool("worker", 3),
			}, false),
			Entry("one pool is over capacity", []v1alpha1.NodeRequest{
				{ResourceClass: "gpu", NumberOfNodes: 2},
				{ResourceClass: "worker", NumberOfNodes: 3},
			}, []hypershiftv1beta1.NodePool{
				readyClusterOrderNodePool("gpu", 3), readyClusterOrderNodePool("worker", 3),
			}, false),
			Entry("duplicate resource classes do not collapse", []v1alpha1.NodeRequest{
				{ResourceClass: "worker", NumberOfNodes: 1},
				{ResourceClass: "worker", NumberOfNodes: 5},
			}, []hypershiftv1beta1.NodePool{
				readyClusterOrderNodePool("worker", 5),
			}, false),
			Entry("duplicate node pool resource classes do not match", []v1alpha1.NodeRequest{
				{ResourceClass: "worker", NumberOfNodes: 1},
			}, []hypershiftv1beta1.NodePool{
				readyClusterOrderNodePool("worker", 1), readyClusterOrderNodePool("worker", 1),
			}, false),
			Entry("empty requests and pools do not match", []v1alpha1.NodeRequest{}, []hypershiftv1beta1.NodePool{}, false),
		)

		It("should not modify Phase when HostedCluster is not yet available", func() {
			instance := &v1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hc-not-available",
					Namespace: "default",
				},
				Status: v1alpha1.ClusterOrderStatus{
					Phase: v1alpha1.ClusterOrderPhaseProgressing,
				},
			}

			hc := &hypershiftv1beta1.HostedCluster{
				Status: hypershiftv1beta1.HostedClusterStatus{
					Conditions: []metav1.Condition{
						{Type: string(hypershiftv1beta1.HostedClusterAvailable), Status: metav1.ConditionFalse, LastTransitionTime: metav1.NewTime(time.Now().UTC()), Reason: "NotReady"},
						{Type: string(hypershiftv1beta1.HostedClusterDegraded), Status: metav1.ConditionFalse, LastTransitionTime: metav1.NewTime(time.Now().UTC()), Reason: "Ready"},
					},
				},
			}

			err := reconciler.handleHostedCluster(ctx, instance, hc)
			Expect(err).NotTo(HaveOccurred())

			Expect(instance.Status.Phase).To(Equal(v1alpha1.ClusterOrderPhaseProgressing))
		})

		It("should fail closed when a non-terminal order has no HostedCluster", func() {
			name := "test-hc-absent"
			instance := &v1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Name:        name,
					Namespace:   "default",
					Annotations: map[string]string{osacManagementStateAnnotation: ManagementStateManual},
					Finalizers:  []string{osacFinalizer},
				},
				Status: v1alpha1.ClusterOrderStatus{
					Phase: v1alpha1.ClusterOrderPhaseReady,
					Conditions: []metav1.Condition{
						{Type: v1alpha1.ConditionClusterAvailable, Status: metav1.ConditionTrue, Reason: v1alpha1.ReasonAsExpected},
						{Type: v1alpha1.ConditionProgressing, Status: metav1.ConditionFalse, Reason: v1alpha1.ReasonAsExpected},
					},
				},
			}
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: generateNamespaceName(instance)}})
			})

			_, err := reconciler.handleUpdate(ctx, reconcile.Request{}, instance)
			Expect(err).NotTo(HaveOccurred())

			Expect(instance.Status.Phase).To(Equal(v1alpha1.ClusterOrderPhaseProgressing))
			progressing := apimeta.FindStatusCondition(instance.Status.Conditions, v1alpha1.ConditionProgressing)
			Expect(progressing).NotTo(BeNil())
			Expect(progressing.Status).To(Equal(metav1.ConditionTrue))
			Expect(progressing.Reason).To(Equal(v1alpha1.ReasonControlPlaneStarting))
			Expect(instance.IsStatusConditionTrue(v1alpha1.ConditionClusterAvailable)).To(BeTrue())
		})

		It("should return HostedCluster lookup errors", func() {
			lookupErr := stderrors.New("hosted cluster list failed")
			name := "test-hc-lookup-error"
			instance := &v1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Name:        name,
					Namespace:   "default",
					Annotations: map[string]string{osacManagementStateAnnotation: ManagementStateManual},
					Finalizers:  []string{osacFinalizer},
				},
			}
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: generateNamespaceName(instance)}})
			})

			reconciler.Client = hostedClusterListErrorClient{Client: k8sClient, err: lookupErr}
			_, err := reconciler.handleUpdate(ctx, reconcile.Request{}, instance)
			Expect(err).To(MatchError(lookupErr))
		})

		It("should set Progressing reason to StageUnknown when HC has no conditions", func() {
			instance := &v1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hc-no-conditions",
					Namespace: "default",
				},
				Status: v1alpha1.ClusterOrderStatus{
					Phase: v1alpha1.ClusterOrderPhaseProgressing,
				},
			}
			instance.SetStatusCondition(v1alpha1.ConditionProgressing, metav1.ConditionTrue,
				"", v1alpha1.ReasonProgressing)

			hc := &hypershiftv1beta1.HostedCluster{
				Status: hypershiftv1beta1.HostedClusterStatus{},
			}

			err := reconciler.handleHostedCluster(ctx, instance, hc)
			Expect(err).NotTo(HaveOccurred())

			progressing := apimeta.FindStatusCondition(instance.Status.Conditions, v1alpha1.ConditionProgressing)
			Expect(progressing).NotTo(BeNil())
			Expect(progressing.Reason).To(Equal(v1alpha1.ReasonStageUnknown))
		})

		It("should set Progressing reason to PreparingInfrastructure when InfrastructureReady is absent", func() {
			instance := &v1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hc-no-infra",
					Namespace: "default",
				},
				Status: v1alpha1.ClusterOrderStatus{
					Phase: v1alpha1.ClusterOrderPhaseProgressing,
				},
			}
			instance.SetStatusCondition(v1alpha1.ConditionProgressing, metav1.ConditionTrue,
				"", v1alpha1.ReasonProgressing)

			hc := &hypershiftv1beta1.HostedCluster{
				Status: hypershiftv1beta1.HostedClusterStatus{
					Conditions: []metav1.Condition{
						{Type: string(hypershiftv1beta1.HostedClusterAvailable), Status: metav1.ConditionFalse, LastTransitionTime: metav1.NewTime(time.Now().UTC()), Reason: "NotReady"},
						{Type: string(hypershiftv1beta1.HostedClusterDegraded), Status: metav1.ConditionFalse, LastTransitionTime: metav1.NewTime(time.Now().UTC()), Reason: "Ready"},
					},
				},
			}

			err := reconciler.handleHostedCluster(ctx, instance, hc)
			Expect(err).NotTo(HaveOccurred())

			progressing := apimeta.FindStatusCondition(instance.Status.Conditions, v1alpha1.ConditionProgressing)
			Expect(progressing).NotTo(BeNil())
			Expect(progressing.Reason).To(Equal(v1alpha1.ReasonPreparingInfrastructure))
		})

		It("should set Progressing reason to PreparingInfrastructure when InfrastructureReady is False", func() {
			instance := &v1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hc-infra-false",
					Namespace: "default",
				},
				Status: v1alpha1.ClusterOrderStatus{
					Phase: v1alpha1.ClusterOrderPhaseProgressing,
				},
			}
			instance.SetStatusCondition(v1alpha1.ConditionProgressing, metav1.ConditionTrue,
				"", v1alpha1.ReasonProgressing)

			hc := &hypershiftv1beta1.HostedCluster{
				Status: hypershiftv1beta1.HostedClusterStatus{
					Conditions: []metav1.Condition{
						{Type: string(hypershiftv1beta1.InfrastructureReady), Status: metav1.ConditionFalse, LastTransitionTime: metav1.NewTime(time.Now().UTC()), Reason: "NotReady"},
						{Type: string(hypershiftv1beta1.HostedClusterAvailable), Status: metav1.ConditionFalse, LastTransitionTime: metav1.NewTime(time.Now().UTC()), Reason: "NotReady"},
						{Type: string(hypershiftv1beta1.HostedClusterDegraded), Status: metav1.ConditionFalse, LastTransitionTime: metav1.NewTime(time.Now().UTC()), Reason: "Ready"},
					},
				},
			}

			err := reconciler.handleHostedCluster(ctx, instance, hc)
			Expect(err).NotTo(HaveOccurred())

			progressing := apimeta.FindStatusCondition(instance.Status.Conditions, v1alpha1.ConditionProgressing)
			Expect(progressing).NotTo(BeNil())
			Expect(progressing.Reason).To(Equal(v1alpha1.ReasonPreparingInfrastructure))
		})

		It("should set Progressing reason to ControlPlaneStarting when InfrastructureReady is True", func() {
			instance := &v1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hc-infra-true",
					Namespace: "default",
				},
				Status: v1alpha1.ClusterOrderStatus{
					Phase: v1alpha1.ClusterOrderPhaseProgressing,
				},
			}
			instance.SetStatusCondition(v1alpha1.ConditionProgressing, metav1.ConditionTrue,
				"", v1alpha1.ReasonProgressing)

			hc := &hypershiftv1beta1.HostedCluster{
				Status: hypershiftv1beta1.HostedClusterStatus{
					Conditions: []metav1.Condition{
						{Type: string(hypershiftv1beta1.InfrastructureReady), Status: metav1.ConditionTrue, LastTransitionTime: metav1.NewTime(time.Now().UTC()), Reason: "Ready"},
						{Type: string(hypershiftv1beta1.HostedClusterAvailable), Status: metav1.ConditionFalse, LastTransitionTime: metav1.NewTime(time.Now().UTC()), Reason: "NotReady"},
						{Type: string(hypershiftv1beta1.HostedClusterDegraded), Status: metav1.ConditionFalse, LastTransitionTime: metav1.NewTime(time.Now().UTC()), Reason: "Ready"},
					},
				},
			}

			err := reconciler.handleHostedCluster(ctx, instance, hc)
			Expect(err).NotTo(HaveOccurred())

			progressing := apimeta.FindStatusCondition(instance.Status.Conditions, v1alpha1.ConditionProgressing)
			Expect(progressing).NotTo(BeNil())
			Expect(progressing.Reason).To(Equal(v1alpha1.ReasonControlPlaneStarting))
		})

		It("should set Progressing reason to ControlPlaneStarting when KubeAPIServerAvailable is True but Available is False", func() {
			instance := &v1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hc-kube-true-avail-false",
					Namespace: "default",
				},
				Status: v1alpha1.ClusterOrderStatus{
					Phase: v1alpha1.ClusterOrderPhaseProgressing,
				},
			}
			instance.SetStatusCondition(v1alpha1.ConditionProgressing, metav1.ConditionTrue,
				"", v1alpha1.ReasonProgressing)

			hc := &hypershiftv1beta1.HostedCluster{
				Status: hypershiftv1beta1.HostedClusterStatus{
					Conditions: []metav1.Condition{
						{Type: string(hypershiftv1beta1.InfrastructureReady), Status: metav1.ConditionTrue, LastTransitionTime: metav1.NewTime(time.Now().UTC()), Reason: "Ready"},
						{Type: string(hypershiftv1beta1.KubeAPIServerAvailable), Status: metav1.ConditionTrue, LastTransitionTime: metav1.NewTime(time.Now().UTC()), Reason: "Ready"},
						{Type: string(hypershiftv1beta1.HostedClusterAvailable), Status: metav1.ConditionFalse, LastTransitionTime: metav1.NewTime(time.Now().UTC()), Reason: "NotReady"},
						{Type: string(hypershiftv1beta1.HostedClusterDegraded), Status: metav1.ConditionFalse, LastTransitionTime: metav1.NewTime(time.Now().UTC()), Reason: "Ready"},
					},
				},
			}

			err := reconciler.handleHostedCluster(ctx, instance, hc)
			Expect(err).NotTo(HaveOccurred())

			progressing := apimeta.FindStatusCondition(instance.Status.Conditions, v1alpha1.ConditionProgressing)
			Expect(progressing).NotTo(BeNil())
			Expect(progressing.Reason).To(Equal(v1alpha1.ReasonControlPlaneStarting))
		})

		It("should set Progressing reason to ControlPlaneStarting when Available is True but KubeAPIServerAvailable is False", func() {
			instance := &v1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hc-avail-true-kube-false",
					Namespace: "default",
				},
				Status: v1alpha1.ClusterOrderStatus{
					Phase: v1alpha1.ClusterOrderPhaseProgressing,
				},
			}
			instance.SetStatusCondition(v1alpha1.ConditionProgressing, metav1.ConditionTrue,
				"", v1alpha1.ReasonProgressing)

			hc := &hypershiftv1beta1.HostedCluster{
				Status: hypershiftv1beta1.HostedClusterStatus{
					Conditions: []metav1.Condition{
						{Type: string(hypershiftv1beta1.InfrastructureReady), Status: metav1.ConditionTrue, LastTransitionTime: metav1.NewTime(time.Now().UTC()), Reason: "Ready"},
						{Type: string(hypershiftv1beta1.KubeAPIServerAvailable), Status: metav1.ConditionFalse, LastTransitionTime: metav1.NewTime(time.Now().UTC()), Reason: "NotReady"},
						{Type: string(hypershiftv1beta1.HostedClusterAvailable), Status: metav1.ConditionTrue, LastTransitionTime: metav1.NewTime(time.Now().UTC()), Reason: "Ready"},
						{Type: string(hypershiftv1beta1.HostedClusterDegraded), Status: metav1.ConditionFalse, LastTransitionTime: metav1.NewTime(time.Now().UTC()), Reason: "Ready"},
					},
				},
			}

			err := reconciler.handleHostedCluster(ctx, instance, hc)
			Expect(err).NotTo(HaveOccurred())

			progressing := apimeta.FindStatusCondition(instance.Status.Conditions, v1alpha1.ConditionProgressing)
			Expect(progressing).NotTo(BeNil())
			Expect(progressing.Reason).To(Equal(v1alpha1.ReasonControlPlaneStarting))
		})

		It("should set Progressing reason to WorkersJoining when KubeAPIServerAvailable and Available are True", func() {
			instance := &v1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hc-workers-joining",
					Namespace: "default",
				},
				Status: v1alpha1.ClusterOrderStatus{
					Phase: v1alpha1.ClusterOrderPhaseProgressing,
				},
			}
			instance.SetStatusCondition(v1alpha1.ConditionProgressing, metav1.ConditionTrue,
				"", v1alpha1.ReasonProgressing)

			hc := &hypershiftv1beta1.HostedCluster{
				Status: hypershiftv1beta1.HostedClusterStatus{
					Conditions: []metav1.Condition{
						{Type: string(hypershiftv1beta1.InfrastructureReady), Status: metav1.ConditionTrue, LastTransitionTime: metav1.NewTime(time.Now().UTC()), Reason: "Ready"},
						{Type: string(hypershiftv1beta1.KubeAPIServerAvailable), Status: metav1.ConditionTrue, LastTransitionTime: metav1.NewTime(time.Now().UTC()), Reason: "Ready"},
						{Type: string(hypershiftv1beta1.HostedClusterAvailable), Status: metav1.ConditionTrue, LastTransitionTime: metav1.NewTime(time.Now().UTC()), Reason: "Ready"},
						{Type: string(hypershiftv1beta1.HostedClusterDegraded), Status: metav1.ConditionFalse, LastTransitionTime: metav1.NewTime(time.Now().UTC()), Reason: "Ready"},
					},
				},
			}

			err := reconciler.handleHostedCluster(ctx, instance, hc)
			Expect(err).NotTo(HaveOccurred())

			progressing := apimeta.FindStatusCondition(instance.Status.Conditions, v1alpha1.ConditionProgressing)
			Expect(progressing).NotTo(BeNil())
			Expect(progressing.Reason).To(Equal(v1alpha1.ReasonWorkersJoining))
		})

		It("should keep a BMaaS order progressing when the HostedCluster is ready but workers are pending", func() {
			instance := &v1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hc-workers-pending",
					Namespace: "default",
				},
				Spec: v1alpha1.ClusterOrderSpec{
					NodeRequests: []v1alpha1.NodeRequest{{
						NumberOfNodes: 1,
						BareMetal:     &v1alpha1.BareMetalNodeSpec{InstanceType: "bm.large"},
					}},
				},
				Status: v1alpha1.ClusterOrderStatus{
					Phase:          v1alpha1.ClusterOrderPhaseProgressing,
					DesiredWorkers: p32(1),
					CurrentWorkers: p32(1),
					ReadyWorkers:   p32(0),
				},
			}

			hc := &hypershiftv1beta1.HostedCluster{
				Status: hypershiftv1beta1.HostedClusterStatus{
					Conditions: []metav1.Condition{
						{Type: string(hypershiftv1beta1.InfrastructureReady), Status: metav1.ConditionTrue, LastTransitionTime: metav1.Now(), Reason: "Ready"},
						{Type: string(hypershiftv1beta1.KubeAPIServerAvailable), Status: metav1.ConditionTrue, LastTransitionTime: metav1.Now(), Reason: "Ready"},
						{Type: string(hypershiftv1beta1.HostedClusterAvailable), Status: metav1.ConditionTrue, LastTransitionTime: metav1.Now(), Reason: "Ready"},
						{Type: string(hypershiftv1beta1.ClusterVersionSucceeding), Status: metav1.ConditionTrue, LastTransitionTime: metav1.Now(), Reason: "Ready"},
						{Type: string(hypershiftv1beta1.HostedClusterDegraded), Status: metav1.ConditionFalse, LastTransitionTime: metav1.Now(), Reason: "Ready"},
					},
				},
			}

			Expect(reconciler.handleHostedCluster(ctx, instance, hc)).To(Succeed())
			reconciler.provisioningCallbacks(instance).OnSuccess(provisioning.ProvisionStatus{})

			Expect(instance.IsStatusConditionTrue(v1alpha1.ConditionClusterAvailable)).To(BeTrue())
			Expect(instance.Status.Phase).To(Equal(v1alpha1.ClusterOrderPhaseProgressing))
			progressing := apimeta.FindStatusCondition(instance.Status.Conditions, v1alpha1.ConditionProgressing)
			Expect(progressing).NotTo(BeNil())
			Expect(progressing.Status).To(Equal(metav1.ConditionTrue))
			Expect(progressing.Reason).To(Equal(v1alpha1.ReasonWorkersJoining))
		})

		It("should transition a BMaaS order to Ready when all workers are ready", func() {
			instance := &v1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hc-workers-ready",
					Namespace: "default",
				},
				Spec: v1alpha1.ClusterOrderSpec{
					NodeRequests: []v1alpha1.NodeRequest{{
						NumberOfNodes: 1,
						BareMetal:     &v1alpha1.BareMetalNodeSpec{InstanceType: "bm.large"},
					}},
				},
				Status: v1alpha1.ClusterOrderStatus{
					Phase:          v1alpha1.ClusterOrderPhaseProgressing,
					DesiredWorkers: p32(1),
					CurrentWorkers: p32(1),
					ReadyWorkers:   p32(1),
					Workers: []v1alpha1.WorkerStatus{{
						NodeSet: "compute",
						Name:    "worker-0",
						Kind:    "BareMetalInstance",
						Phase:   "Ready",
					}},
				},
			}

			hc := &hypershiftv1beta1.HostedCluster{
				Status: hypershiftv1beta1.HostedClusterStatus{
					Conditions: []metav1.Condition{
						{Type: string(hypershiftv1beta1.InfrastructureReady), Status: metav1.ConditionTrue, LastTransitionTime: metav1.Now(), Reason: "Ready"},
						{Type: string(hypershiftv1beta1.KubeAPIServerAvailable), Status: metav1.ConditionTrue, LastTransitionTime: metav1.Now(), Reason: "Ready"},
						{Type: string(hypershiftv1beta1.HostedClusterAvailable), Status: metav1.ConditionTrue, LastTransitionTime: metav1.Now(), Reason: "Ready"},
						{Type: string(hypershiftv1beta1.ClusterVersionSucceeding), Status: metav1.ConditionTrue, LastTransitionTime: metav1.Now(), Reason: "Ready"},
						{Type: string(hypershiftv1beta1.HostedClusterDegraded), Status: metav1.ConditionFalse, LastTransitionTime: metav1.Now(), Reason: "Ready"},
					},
				},
			}

			Expect(reconciler.handleHostedCluster(ctx, instance, hc)).To(Succeed())
			reconciler.provisioningCallbacks(instance).OnSuccess(provisioning.ProvisionStatus{})

			Expect(instance.Status.Phase).To(Equal(v1alpha1.ClusterOrderPhaseReady))
			progressing := apimeta.FindStatusCondition(instance.Status.Conditions, v1alpha1.ConditionProgressing)
			Expect(progressing).NotTo(BeNil())
			Expect(progressing.Status).To(Equal(metav1.ConditionFalse))
			Expect(progressing.Reason).To(Equal(v1alpha1.ReasonAsExpected))
		})

		It("should keep a CaaS order progressing until the HostedCluster is available", func() {
			instance := &v1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hc-caas-gate",
					Namespace: "default",
				},
				Status: v1alpha1.ClusterOrderStatus{
					Phase: v1alpha1.ClusterOrderPhaseProgressing,
				},
			}

			hc := &hypershiftv1beta1.HostedCluster{
				Status: hypershiftv1beta1.HostedClusterStatus{
					Conditions: []metav1.Condition{
						{Type: string(hypershiftv1beta1.InfrastructureReady), Status: metav1.ConditionTrue, LastTransitionTime: metav1.Now(), Reason: "Ready"},
						{Type: string(hypershiftv1beta1.KubeAPIServerAvailable), Status: metav1.ConditionFalse, LastTransitionTime: metav1.Now(), Reason: "NotReady"},
						{Type: string(hypershiftv1beta1.HostedClusterAvailable), Status: metav1.ConditionFalse, LastTransitionTime: metav1.Now(), Reason: "NotReady"},
						{Type: string(hypershiftv1beta1.ClusterVersionSucceeding), Status: metav1.ConditionFalse, LastTransitionTime: metav1.Now(), Reason: "NotReady"},
						{Type: string(hypershiftv1beta1.HostedClusterDegraded), Status: metav1.ConditionFalse, LastTransitionTime: metav1.Now(), Reason: "Ready"},
					},
				},
			}
			Expect(reconciler.handleHostedCluster(ctx, instance, hc)).To(Succeed())
			callbacks := reconciler.provisioningCallbacks(instance)
			callbacks.OnSuccess(provisioning.ProvisionStatus{})
			Expect(instance.Status.Phase).To(Equal(v1alpha1.ClusterOrderPhaseProgressing))

			hc.Status.Conditions = []metav1.Condition{
				{Type: string(hypershiftv1beta1.InfrastructureReady), Status: metav1.ConditionTrue, LastTransitionTime: metav1.Now(), Reason: "Ready"},
				{Type: string(hypershiftv1beta1.KubeAPIServerAvailable), Status: metav1.ConditionTrue, LastTransitionTime: metav1.Now(), Reason: "Ready"},
				{Type: string(hypershiftv1beta1.HostedClusterAvailable), Status: metav1.ConditionTrue, LastTransitionTime: metav1.Now(), Reason: "Ready"},
				{Type: string(hypershiftv1beta1.ClusterVersionSucceeding), Status: metav1.ConditionTrue, LastTransitionTime: metav1.Now(), Reason: "Ready"},
				{Type: string(hypershiftv1beta1.HostedClusterDegraded), Status: metav1.ConditionFalse, LastTransitionTime: metav1.Now(), Reason: "Ready"},
			}
			Expect(reconciler.handleHostedCluster(ctx, instance, hc)).To(Succeed())
			callbacks.OnSuccess(provisioning.ProvisionStatus{})

			Expect(instance.Status.Phase).To(Equal(v1alpha1.ClusterOrderPhaseReady))
		})

		It("should restore a progressing stage when a Ready order becomes unavailable", func() {
			instance := &v1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hc-ready-regression",
					Namespace: "default",
				},
				Status: v1alpha1.ClusterOrderStatus{
					Phase: v1alpha1.ClusterOrderPhaseReady,
					Conditions: []metav1.Condition{
						{Type: v1alpha1.ConditionControlPlaneAvailable, Status: metav1.ConditionTrue, Reason: v1alpha1.ReasonAsExpected},
						{Type: v1alpha1.ConditionClusterAvailable, Status: metav1.ConditionTrue, Reason: v1alpha1.ReasonAsExpected},
						{Type: v1alpha1.ConditionProgressing, Status: metav1.ConditionFalse, Reason: v1alpha1.ReasonAsExpected},
						{Type: v1alpha1.ConditionWorkersFailed, Status: metav1.ConditionTrue, Reason: "WorkersRetrying"},
					},
				},
			}

			hc := &hypershiftv1beta1.HostedCluster{
				Status: hypershiftv1beta1.HostedClusterStatus{
					Conditions: []metav1.Condition{
						{Type: string(hypershiftv1beta1.InfrastructureReady), Status: metav1.ConditionTrue, Reason: "Ready"},
						{Type: string(hypershiftv1beta1.KubeAPIServerAvailable), Status: metav1.ConditionTrue, Reason: "Ready"},
						{Type: string(hypershiftv1beta1.HostedClusterAvailable), Status: metav1.ConditionFalse, Reason: "NotReady"},
						{Type: string(hypershiftv1beta1.HostedClusterDegraded), Status: metav1.ConditionTrue, Reason: "Degraded"},
					},
				},
			}

			Expect(reconciler.handleHostedCluster(ctx, instance, hc)).To(Succeed())

			Expect(instance.Status.Phase).To(Equal(v1alpha1.ClusterOrderPhaseProgressing))
			progressing := apimeta.FindStatusCondition(instance.Status.Conditions, v1alpha1.ConditionProgressing)
			Expect(progressing).NotTo(BeNil())
			Expect(progressing.Status).To(Equal(metav1.ConditionTrue))
			Expect(progressing.Reason).To(Equal(v1alpha1.ReasonControlPlaneStarting))
			Expect(instance.IsStatusConditionTrue(v1alpha1.ConditionControlPlaneAvailable)).To(BeTrue())
			Expect(instance.IsStatusConditionTrue(v1alpha1.ConditionClusterAvailable)).To(BeTrue())
			Expect(instance.IsStatusConditionTrue(v1alpha1.ConditionWorkersFailed)).To(BeTrue())
		})

		It("should not revive a Failed order when HostedCluster and workers are ready", func() {
			instance := &v1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hc-failed-terminal",
					Namespace: "default",
				},
				Spec: v1alpha1.ClusterOrderSpec{
					NodeRequests: []v1alpha1.NodeRequest{{
						NumberOfNodes: 1,
						BareMetal:     &v1alpha1.BareMetalNodeSpec{InstanceType: "bm.large"},
					}},
				},
				Status: v1alpha1.ClusterOrderStatus{
					Phase:          v1alpha1.ClusterOrderPhaseFailed,
					DesiredWorkers: p32(1),
					CurrentWorkers: p32(1),
					ReadyWorkers:   p32(1),
					Conditions: []metav1.Condition{
						{Type: v1alpha1.ConditionProgressing, Status: metav1.ConditionFalse, Reason: v1alpha1.ReasonProvisioningFailed},
					},
				},
			}

			hc := &hypershiftv1beta1.HostedCluster{
				Status: hypershiftv1beta1.HostedClusterStatus{
					Conditions: []metav1.Condition{
						{Type: string(hypershiftv1beta1.InfrastructureReady), Status: metav1.ConditionTrue, Reason: "Ready"},
						{Type: string(hypershiftv1beta1.KubeAPIServerAvailable), Status: metav1.ConditionTrue, Reason: "Ready"},
						{Type: string(hypershiftv1beta1.HostedClusterAvailable), Status: metav1.ConditionTrue, Reason: "Ready"},
						{Type: string(hypershiftv1beta1.ClusterVersionSucceeding), Status: metav1.ConditionTrue, Reason: "Ready"},
						{Type: string(hypershiftv1beta1.HostedClusterDegraded), Status: metav1.ConditionFalse, Reason: "Ready"},
					},
				},
			}

			Expect(reconciler.handleHostedCluster(ctx, instance, hc)).To(Succeed())

			Expect(instance.Status.Phase).To(Equal(v1alpha1.ClusterOrderPhaseFailed))
			progressing := apimeta.FindStatusCondition(instance.Status.Conditions, v1alpha1.ConditionProgressing)
			Expect(progressing).NotTo(BeNil())
			Expect(progressing.Status).To(Equal(metav1.ConditionFalse))
			Expect(progressing.Reason).To(Equal(v1alpha1.ReasonProvisioningFailed))
		})

		It("should allow sub-stage reason to regress when HC conditions transiently disappear", func() {
			instance := &v1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hc-reason-regression",
					Namespace: "default",
				},
				Status: v1alpha1.ClusterOrderStatus{
					Phase: v1alpha1.ClusterOrderPhaseProgressing,
				},
			}
			instance.SetStatusCondition(v1alpha1.ConditionProgressing, metav1.ConditionTrue,
				"Workers Joining", v1alpha1.ReasonWorkersJoining)

			hc := &hypershiftv1beta1.HostedCluster{
				Status: hypershiftv1beta1.HostedClusterStatus{
					Conditions: []metav1.Condition{},
				},
			}

			err := reconciler.handleHostedCluster(ctx, instance, hc)
			Expect(err).NotTo(HaveOccurred())

			progressing := apimeta.FindStatusCondition(instance.Status.Conditions, v1alpha1.ConditionProgressing)
			Expect(progressing).NotTo(BeNil())
			Expect(progressing.Reason).To(Equal(v1alpha1.ReasonStageUnknown))
		})

		It("should keep stage conditions with ReasonAsExpected unchanged", func() {
			instance := &v1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hc-stage-reasons",
					Namespace: "default",
				},
				Status: v1alpha1.ClusterOrderStatus{
					Phase: v1alpha1.ClusterOrderPhaseProgressing,
				},
			}
			instance.SetStatusCondition(v1alpha1.ConditionProgressing, metav1.ConditionTrue,
				"", v1alpha1.ReasonProgressing)

			hc := &hypershiftv1beta1.HostedCluster{
				Status: hypershiftv1beta1.HostedClusterStatus{
					Conditions: []metav1.Condition{
						{Type: string(hypershiftv1beta1.InfrastructureReady), Status: metav1.ConditionTrue, LastTransitionTime: metav1.NewTime(time.Now().UTC()), Reason: "Ready"},
						{Type: string(hypershiftv1beta1.KubeAPIServerAvailable), Status: metav1.ConditionTrue, LastTransitionTime: metav1.NewTime(time.Now().UTC()), Reason: "Ready"},
						{Type: string(hypershiftv1beta1.HostedClusterAvailable), Status: metav1.ConditionTrue, LastTransitionTime: metav1.NewTime(time.Now().UTC()), Reason: "Ready"},
						{Type: string(hypershiftv1beta1.HostedClusterDegraded), Status: metav1.ConditionFalse, LastTransitionTime: metav1.NewTime(time.Now().UTC()), Reason: "Ready"},
						{Type: string(hypershiftv1beta1.ClusterVersionSucceeding), Status: metav1.ConditionTrue, LastTransitionTime: metav1.NewTime(time.Now().UTC()), Reason: "Ready"},
					},
				},
			}

			err := reconciler.handleHostedCluster(ctx, instance, hc)
			Expect(err).NotTo(HaveOccurred())

			controlPlaneCreated := apimeta.FindStatusCondition(instance.Status.Conditions, v1alpha1.ConditionControlPlaneCreated)
			Expect(controlPlaneCreated).NotTo(BeNil())
			Expect(controlPlaneCreated.Reason).To(Equal(v1alpha1.ReasonAsExpected))

			controlPlaneAvailable := apimeta.FindStatusCondition(instance.Status.Conditions, v1alpha1.ConditionControlPlaneAvailable)
			Expect(controlPlaneAvailable).NotTo(BeNil())
			Expect(controlPlaneAvailable.Reason).To(Equal(v1alpha1.ReasonAsExpected))
		})

		It("should not overwrite Progressing=False when Phase is Ready", func() {
			instance := &v1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hc-phase-ready",
					Namespace: "default",
				},
				Status: v1alpha1.ClusterOrderStatus{
					Phase: v1alpha1.ClusterOrderPhaseReady,
				},
			}
			instance.SetStatusCondition(v1alpha1.ConditionProgressing, metav1.ConditionFalse,
				"", v1alpha1.ReasonAsExpected)

			hc := &hypershiftv1beta1.HostedCluster{
				Status: hypershiftv1beta1.HostedClusterStatus{
					Conditions: []metav1.Condition{
						{Type: string(hypershiftv1beta1.InfrastructureReady), Status: metav1.ConditionTrue, LastTransitionTime: metav1.NewTime(time.Now().UTC()), Reason: "Ready"},
						{Type: string(hypershiftv1beta1.KubeAPIServerAvailable), Status: metav1.ConditionTrue, LastTransitionTime: metav1.NewTime(time.Now().UTC()), Reason: "Ready"},
						{Type: string(hypershiftv1beta1.HostedClusterAvailable), Status: metav1.ConditionTrue, LastTransitionTime: metav1.NewTime(time.Now().UTC()), Reason: "Ready"},
						{Type: string(hypershiftv1beta1.HostedClusterDegraded), Status: metav1.ConditionFalse, LastTransitionTime: metav1.NewTime(time.Now().UTC()), Reason: "Ready"},
						{Type: string(hypershiftv1beta1.ClusterVersionSucceeding), Status: metav1.ConditionTrue, LastTransitionTime: metav1.NewTime(time.Now().UTC()), Reason: "Ready"},
					},
				},
			}

			err := reconciler.handleHostedCluster(ctx, instance, hc)
			Expect(err).NotTo(HaveOccurred())

			progressing := apimeta.FindStatusCondition(instance.Status.Conditions, v1alpha1.ConditionProgressing)
			Expect(progressing).NotTo(BeNil())
			Expect(progressing.Status).To(Equal(metav1.ConditionFalse))
			Expect(progressing.Reason).To(Equal(v1alpha1.ReasonAsExpected))
		})
	})

	Context("bareMetalWorkersReady", func() {
		newBMOrder := func() *v1alpha1.ClusterOrder {
			return &v1alpha1.ClusterOrder{
				Spec: v1alpha1.ClusterOrderSpec{
					NodeRequests: []v1alpha1.NodeRequest{{
						NumberOfNodes: 1,
						BareMetal:     &v1alpha1.BareMetalNodeSpec{InstanceType: "bm.large"},
					}},
				},
			}
		}

		It("should reject bare-metal workers when aggregate counts are missing", func() {
			Expect(bareMetalWorkersReady(newBMOrder())).To(BeFalse())
		})

		It("should reject bare-metal workers when all aggregate counts are zero", func() {
			instance := newBMOrder()
			instance.Status.DesiredWorkers = p32(0)
			instance.Status.CurrentWorkers = p32(0)
			instance.Status.ReadyWorkers = p32(0)

			Expect(bareMetalWorkersReady(instance)).To(BeFalse())
		})

		It("should reject bare-metal workers when current workers are below desired", func() {
			instance := newBMOrder()
			instance.Status.DesiredWorkers = p32(2)
			instance.Status.CurrentWorkers = p32(1)
			instance.Status.ReadyWorkers = p32(1)

			Expect(bareMetalWorkersReady(instance)).To(BeFalse())
		})

		It("should reject bare-metal workers when WorkersFailed is true", func() {
			instance := newBMOrder()
			instance.Status.DesiredWorkers = p32(1)
			instance.Status.CurrentWorkers = p32(1)
			instance.Status.ReadyWorkers = p32(1)
			instance.SetStatusCondition(v1alpha1.ConditionWorkersFailed, metav1.ConditionTrue, "worker failed", "WorkersRetrying")

			Expect(bareMetalWorkersReady(instance)).To(BeFalse())
		})
	})

	Context("handleNodePool", func() {
		var reconciler *ClusterOrderReconciler

		BeforeEach(func() {
			reconciler = &ClusterOrderReconciler{
				Client:               k8sClient,
				apiReader:            k8sClient,
				Scheme:               k8sClient.Scheme(),
				ProvisioningProvider: noopProvisioningProvider{},
			}
		})

		ctx := context.Background()

		It("should write observed node count to status, not spec", func() {
			instance := &v1alpha1.ClusterOrder{
				Spec: v1alpha1.ClusterOrderSpec{
					NodeRequests: []v1alpha1.NodeRequest{
						{ResourceClass: "m1.large", NumberOfNodes: 3},
					},
				},
			}

			nodePool := &hypershiftv1beta1.NodePool{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{agentResourceClassLabel: "m1.large"}},
				Status: hypershiftv1beta1.NodePoolStatus{
					Replicas: 5,
				},
			}

			err := reconciler.handleNodePool(ctx, instance, nodePool)
			Expect(err).NotTo(HaveOccurred())

			Expect(instance.Spec.NodeRequests).To(HaveLen(1),
				"spec.nodeRequests must not be mutated by observed state")
			Expect(instance.Spec.NodeRequests[0].NumberOfNodes).To(Equal(3),
				"spec.nodeRequests[0].numberOfNodes must remain unchanged")

			Expect(instance.Status.NodeRequests).To(HaveLen(1),
				"status.nodeRequests should have exactly one entry")
			Expect(instance.Status.NodeRequests[0].ResourceClass).To(Equal("m1.large"))
			Expect(instance.Status.NodeRequests[0].NumberOfNodes).To(Equal(5),
				"status.nodeRequests[0].numberOfNodes should reflect observed replicas")
		})

		It("should update existing status entry instead of appending", func() {
			instance := &v1alpha1.ClusterOrder{
				Spec: v1alpha1.ClusterOrderSpec{
					NodeRequests: []v1alpha1.NodeRequest{
						{ResourceClass: "m1.large", NumberOfNodes: 3},
					},
				},
				Status: v1alpha1.ClusterOrderStatus{
					NodeRequests: []v1alpha1.NodeRequest{
						{ResourceClass: "m1.large", NumberOfNodes: 2},
					},
				},
			}

			nodePool := &hypershiftv1beta1.NodePool{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{agentResourceClassLabel: "m1.large"}},
				Status: hypershiftv1beta1.NodePoolStatus{
					Replicas: 5,
				},
			}

			err := reconciler.handleNodePool(ctx, instance, nodePool)
			Expect(err).NotTo(HaveOccurred())

			Expect(instance.Status.NodeRequests).To(HaveLen(1))
			Expect(instance.Status.NodeRequests[0].NumberOfNodes).To(Equal(5))
			Expect(instance.Spec.NodeRequests).To(HaveLen(1),
				"spec must not be modified")
		})

		It("should update the status entry matching the node pool resource class", func() {
			instance := &v1alpha1.ClusterOrder{
				Spec: v1alpha1.ClusterOrderSpec{
					NodeRequests: []v1alpha1.NodeRequest{
						{ResourceClass: "m1.large", NumberOfNodes: 3},
						{ResourceClass: "m1.small", NumberOfNodes: 1},
					},
				},
			}

			nodePools := &hypershiftv1beta1.NodePoolList{Items: []hypershiftv1beta1.NodePool{
				{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{agentResourceClassLabel: "m1.small"}},
					Status: hypershiftv1beta1.NodePoolStatus{
						Replicas: 2,
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{agentResourceClassLabel: "m1.large"}},
					Status: hypershiftv1beta1.NodePoolStatus{
						Replicas: 4,
					},
				},
			}}
			status := []v1alpha1.NodeRequest{
				{ResourceClass: "m1.large", NumberOfNodes: 0},
				{ResourceClass: "m1.small", NumberOfNodes: 0},
			}
			instance.Status.NodeRequests = status

			err := reconciler.handleNodePools(ctx, instance, nodePools)
			Expect(err).NotTo(HaveOccurred())

			Expect(instance.Status.NodeRequests).To(ConsistOf(
				v1alpha1.NodeRequest{ResourceClass: "m1.large", NumberOfNodes: 4},
				v1alpha1.NodeRequest{ResourceClass: "m1.small", NumberOfNodes: 2},
			))
		})

		It("should not modify status when replicas already match", func() {
			instance := &v1alpha1.ClusterOrder{
				Spec: v1alpha1.ClusterOrderSpec{
					NodeRequests: []v1alpha1.NodeRequest{
						{ResourceClass: "m1.large", NumberOfNodes: 3},
					},
				},
				Status: v1alpha1.ClusterOrderStatus{
					NodeRequests: []v1alpha1.NodeRequest{
						{ResourceClass: "m1.large", NumberOfNodes: 5},
					},
				},
			}

			nodePool := &hypershiftv1beta1.NodePool{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{agentResourceClassLabel: "m1.large"}},
				Status: hypershiftv1beta1.NodePoolStatus{
					Replicas: 5,
				},
			}

			err := reconciler.handleNodePool(ctx, instance, nodePool)
			Expect(err).NotTo(HaveOccurred())

			Expect(instance.Status.NodeRequests).To(HaveLen(1))
			Expect(instance.Status.NodeRequests[0].NumberOfNodes).To(Equal(5))
		})
	})

	Context("provisioning callbacks", func() {
		DescribeTable("OnFailed phase and condition based on ClusterReference state",
			func(setup func(*v1alpha1.ClusterOrder), expectedPhase v1alpha1.ClusterOrderPhaseType) {
				instance := &v1alpha1.ClusterOrder{
					Status: v1alpha1.ClusterOrderStatus{Phase: v1alpha1.ClusterOrderPhaseProgressing},
				}
				setup(instance)
				callbacks := (&ClusterOrderReconciler{}).provisioningCallbacks(instance)
				callbacks.OnFailed("job failed")

				Expect(instance.Status.Phase).To(Equal(expectedPhase))
				cond := apimeta.FindStatusCondition(instance.Status.Conditions, v1alpha1.ConditionProgressing)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				Expect(cond.Reason).To(Equal(v1alpha1.ReasonProvisioningFailed))
				Expect(cond.Message).To(ContainSubstring("job failed"))
			},
			Entry("nil ClusterReference -> Failed with condition",
				func(_ *v1alpha1.ClusterOrder) {}, v1alpha1.ClusterOrderPhaseFailed),
			Entry("empty HostedClusterName -> Failed with condition",
				func(i *v1alpha1.ClusterOrder) {
					i.Status.ClusterReference = &v1alpha1.ClusterOrderClusterReferenceType{}
				},
				v1alpha1.ClusterOrderPhaseFailed),
		)

		It("should preserve Progressing condition when OnFailed guard is active (OSAC-2024)", func() {
			instance := &v1alpha1.ClusterOrder{
				Status: v1alpha1.ClusterOrderStatus{
					Phase: v1alpha1.ClusterOrderPhaseProgressing,
					Conditions: []metav1.Condition{
						{
							Type:               v1alpha1.ConditionProgressing,
							Status:             metav1.ConditionTrue,
							Reason:             v1alpha1.ReasonProgressing,
							Message:            "provisioning in progress",
							LastTransitionTime: metav1.NewTime(time.Now().UTC()),
						},
					},
				},
			}
			instance.SetClusterReferenceHostedClusterName("my-cluster")

			callbacks := (&ClusterOrderReconciler{}).provisioningCallbacks(instance)
			callbacks.OnFailed("ancillary task failed")

			Expect(instance.Status.Phase).To(Equal(v1alpha1.ClusterOrderPhaseProgressing))
			cond := apimeta.FindStatusCondition(instance.Status.Conditions, v1alpha1.ConditionProgressing)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(v1alpha1.ReasonProgressing))
			Expect(cond.Message).To(Equal("provisioning in progress"))
		})

		It("should keep BMaaS Phase=Progressing and set WorkersJoining on OnSuccess while workers are pending", func() {
			instance := &v1alpha1.ClusterOrder{
				Spec: v1alpha1.ClusterOrderSpec{
					NodeRequests: []v1alpha1.NodeRequest{{
						NumberOfNodes: 1,
						BareMetal:     &v1alpha1.BareMetalNodeSpec{InstanceType: "bm.large"},
					}},
				},
				Status: v1alpha1.ClusterOrderStatus{
					Phase:          v1alpha1.ClusterOrderPhaseProgressing,
					DesiredWorkers: p32(1),
					CurrentWorkers: p32(1),
					ReadyWorkers:   p32(0),
				},
			}

			reconciler := &ClusterOrderReconciler{}
			callbacks := reconciler.provisioningCallbacks(instance)
			callbacks.OnSuccess(provisioning.ProvisionStatus{})

			Expect(instance.Status.Phase).To(Equal(v1alpha1.ClusterOrderPhaseProgressing))
			cond := apimeta.FindStatusCondition(instance.Status.Conditions, v1alpha1.ConditionProgressing)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(v1alpha1.ReasonWorkersJoining))
		})

		It("should keep CaaS orders progressing after provisioning recovery until HostedCluster readiness", func() {
			instance := &v1alpha1.ClusterOrder{
				Status: v1alpha1.ClusterOrderStatus{
					Phase: v1alpha1.ClusterOrderPhaseProgressing,
					Conditions: []metav1.Condition{
						{
							Type:               v1alpha1.ConditionProgressing,
							Status:             metav1.ConditionFalse,
							Reason:             v1alpha1.ReasonProvisioningFailed,
							Message:            "previous failure",
							LastTransitionTime: metav1.NewTime(time.Now().UTC()),
						},
					},
				},
			}

			reconciler := &ClusterOrderReconciler{}
			callbacks := reconciler.provisioningCallbacks(instance)
			callbacks.OnSuccess(provisioning.ProvisionStatus{})

			Expect(instance.Status.Phase).To(Equal(v1alpha1.ClusterOrderPhaseProgressing))
			cond := apimeta.FindStatusCondition(instance.Status.Conditions, v1alpha1.ConditionProgressing)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(v1alpha1.ReasonProgressing))
			Expect(cond.Message).To(BeEmpty())
		})
	})

	Context("handleNodePool", func() {
		var reconciler *ClusterOrderReconciler

		BeforeEach(func() {
			reconciler = &ClusterOrderReconciler{
				Client:               k8sClient,
				apiReader:            k8sClient,
				Scheme:               k8sClient.Scheme(),
				ProvisioningProvider: noopProvisioningProvider{},
			}
		})

		ctx := context.Background()

		It("should write observed node count to status, not spec", func() {
			instance := &v1alpha1.ClusterOrder{
				Spec: v1alpha1.ClusterOrderSpec{
					NodeRequests: []v1alpha1.NodeRequest{
						{ResourceClass: "m1.large", NumberOfNodes: 3},
					},
				},
			}

			nodePool := &hypershiftv1beta1.NodePool{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{agentResourceClassLabel: "m1.large"}},
				Status: hypershiftv1beta1.NodePoolStatus{
					Replicas: 5,
				},
			}

			err := reconciler.handleNodePool(ctx, instance, nodePool)
			Expect(err).NotTo(HaveOccurred())

			Expect(instance.Spec.NodeRequests).To(HaveLen(1),
				"spec.nodeRequests must not be mutated by observed state")
			Expect(instance.Spec.NodeRequests[0].NumberOfNodes).To(Equal(3),
				"spec.nodeRequests[0].numberOfNodes must remain unchanged")

			Expect(instance.Status.NodeRequests).To(HaveLen(1),
				"status.nodeRequests should have exactly one entry")
			Expect(instance.Status.NodeRequests[0].ResourceClass).To(Equal("m1.large"))
			Expect(instance.Status.NodeRequests[0].NumberOfNodes).To(Equal(5),
				"status.nodeRequests[0].numberOfNodes should reflect observed replicas")
		})

		It("should update existing status entry instead of appending", func() {
			instance := &v1alpha1.ClusterOrder{
				Spec: v1alpha1.ClusterOrderSpec{
					NodeRequests: []v1alpha1.NodeRequest{
						{ResourceClass: "m1.large", NumberOfNodes: 3},
					},
				},
				Status: v1alpha1.ClusterOrderStatus{
					NodeRequests: []v1alpha1.NodeRequest{
						{ResourceClass: "m1.large", NumberOfNodes: 2},
					},
				},
			}

			nodePool := &hypershiftv1beta1.NodePool{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{agentResourceClassLabel: "m1.large"}},
				Status: hypershiftv1beta1.NodePoolStatus{
					Replicas: 5,
				},
			}

			err := reconciler.handleNodePool(ctx, instance, nodePool)
			Expect(err).NotTo(HaveOccurred())

			Expect(instance.Status.NodeRequests).To(HaveLen(1))
			Expect(instance.Status.NodeRequests[0].NumberOfNodes).To(Equal(5))
			Expect(instance.Spec.NodeRequests).To(HaveLen(1),
				"spec must not be modified")
		})

		It("should update the status entry matching the node pool resource class", func() {
			instance := &v1alpha1.ClusterOrder{
				Spec: v1alpha1.ClusterOrderSpec{
					NodeRequests: []v1alpha1.NodeRequest{
						{ResourceClass: "m1.large", NumberOfNodes: 3},
						{ResourceClass: "m1.small", NumberOfNodes: 1},
					},
				},
			}

			nodePool := &hypershiftv1beta1.NodePool{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{agentResourceClassLabel: "m1.small"}},
				Status: hypershiftv1beta1.NodePoolStatus{
					Replicas: 2,
				},
			}
			instance.Status.NodeRequests = []v1alpha1.NodeRequest{
				{ResourceClass: "m1.large", NumberOfNodes: 0},
				{ResourceClass: "m1.small", NumberOfNodes: 0},
			}

			err := reconciler.handleNodePool(ctx, instance, nodePool)
			Expect(err).NotTo(HaveOccurred())

			Expect(instance.Status.NodeRequests).To(ConsistOf(
				v1alpha1.NodeRequest{ResourceClass: "m1.large", NumberOfNodes: 0},
				v1alpha1.NodeRequest{ResourceClass: "m1.small", NumberOfNodes: 2},
			))
		})

		It("should not modify status when replicas already match", func() {
			instance := &v1alpha1.ClusterOrder{
				Spec: v1alpha1.ClusterOrderSpec{
					NodeRequests: []v1alpha1.NodeRequest{
						{ResourceClass: "m1.large", NumberOfNodes: 3},
					},
				},
				Status: v1alpha1.ClusterOrderStatus{
					NodeRequests: []v1alpha1.NodeRequest{
						{ResourceClass: "m1.large", NumberOfNodes: 5},
					},
				},
			}

			nodePool := &hypershiftv1beta1.NodePool{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{agentResourceClassLabel: "m1.large"}},
				Status: hypershiftv1beta1.NodePoolStatus{
					Replicas: 5,
				},
			}

			err := reconciler.handleNodePool(ctx, instance, nodePool)
			Expect(err).NotTo(HaveOccurred())

			Expect(instance.Status.NodeRequests).To(HaveLen(1))
			Expect(instance.Status.NodeRequests[0].NumberOfNodes).To(Equal(5))
		})
	})

	Context("VIP endpoint reconciliation", func() {
		It("copies VIP annotations to status", func() {
			instance := &v1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-vip",
					Namespace: "default",
					Annotations: map[string]string{
						"osac.openshift.io/api-endpoint":     "10.0.1.240",
						"osac.openshift.io/ingress-endpoint": "10.0.1.241",
					},
				},
			}

			reconcileVIPEndpoints(instance)
			Expect(instance.Status.ApiEndpoint).To(Equal("10.0.1.240"))
			Expect(instance.Status.IngressEndpoint).To(Equal("10.0.1.241"))
		})

		It("does nothing when annotations are absent", func() {
			instance := &v1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-no-vip",
					Namespace: "default",
				},
			}

			reconcileVIPEndpoints(instance)
			Expect(instance.Status.ApiEndpoint).To(BeEmpty())
			Expect(instance.Status.IngressEndpoint).To(BeEmpty())
		})
	})

})
