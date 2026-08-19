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

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

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
						TemplateID: "test",
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

		It("should update conditions but not set Phase to Ready when HostedCluster is ready", func() {
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
						{Type: "Available", Status: metav1.ConditionTrue, LastTransitionTime: metav1.Now(), Reason: "Ready"},
						{Type: "Degraded", Status: metav1.ConditionFalse, LastTransitionTime: metav1.Now(), Reason: "Ready"},
						{Type: "ClusterVersionSucceeding", Status: metav1.ConditionTrue, LastTransitionTime: metav1.Now(), Reason: "Ready"},
					},
				},
			}

			err := reconciler.handleHostedCluster(ctx, instance, hc)
			Expect(err).NotTo(HaveOccurred())

			Expect(instance.Status.Phase).To(Equal(v1alpha1.ClusterOrderPhaseProgressing),
				"handleHostedCluster must not set Phase to Ready — Phase is controlled by provisioning callbacks")

			Expect(instance.IsStatusConditionTrue(v1alpha1.ConditionControlPlaneAvailable)).To(BeTrue())
			Expect(instance.IsStatusConditionTrue(v1alpha1.ConditionClusterAvailable)).To(BeTrue())
		})

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
						{Type: "Available", Status: metav1.ConditionFalse, LastTransitionTime: metav1.Now(), Reason: "NotReady"},
						{Type: "Degraded", Status: metav1.ConditionFalse, LastTransitionTime: metav1.Now(), Reason: "Ready"},
					},
				},
			}

			err := reconciler.handleHostedCluster(ctx, instance, hc)
			Expect(err).NotTo(HaveOccurred())

			Expect(instance.Status.Phase).To(Equal(v1alpha1.ClusterOrderPhaseProgressing))
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

		It("should skip when spec has more than one node request", func() {
			instance := &v1alpha1.ClusterOrder{
				Spec: v1alpha1.ClusterOrderSpec{
					NodeRequests: []v1alpha1.NodeRequest{
						{ResourceClass: "m1.large", NumberOfNodes: 3},
						{ResourceClass: "m1.small", NumberOfNodes: 1},
					},
				},
			}

			nodePool := &hypershiftv1beta1.NodePool{
				Status: hypershiftv1beta1.NodePoolStatus{
					Replicas: 5,
				},
			}

			err := reconciler.handleNodePool(ctx, instance, nodePool)
			Expect(err).NotTo(HaveOccurred())

			Expect(instance.Status.NodeRequests).To(BeEmpty(),
				"status.nodeRequests should not be touched when spec has multiple node requests")
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
							LastTransitionTime: metav1.Now(),
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

		It("should set Phase=Ready and Progressing=False on OnSuccess", func() {
			instance := &v1alpha1.ClusterOrder{
				Status: v1alpha1.ClusterOrderStatus{
					Phase: v1alpha1.ClusterOrderPhaseProgressing,
				},
			}

			reconciler := &ClusterOrderReconciler{}
			callbacks := reconciler.provisioningCallbacks(instance)
			callbacks.OnSuccess(provisioning.ProvisionStatus{})

			Expect(instance.Status.Phase).To(Equal(v1alpha1.ClusterOrderPhaseReady))
			cond := apimeta.FindStatusCondition(instance.Status.Conditions, v1alpha1.ConditionProgressing)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(v1alpha1.ReasonAsExpected))
		})

		It("should clear stale Progressing=False condition on provisioning recovery", func() {
			instance := &v1alpha1.ClusterOrder{
				Status: v1alpha1.ClusterOrderStatus{
					Phase: v1alpha1.ClusterOrderPhaseProgressing,
					Conditions: []metav1.Condition{
						{
							Type:               v1alpha1.ConditionProgressing,
							Status:             metav1.ConditionFalse,
							Reason:             v1alpha1.ReasonProvisioningFailed,
							Message:            "previous failure",
							LastTransitionTime: metav1.Now(),
						},
					},
				},
			}

			reconciler := &ClusterOrderReconciler{}
			callbacks := reconciler.provisioningCallbacks(instance)
			callbacks.OnSuccess(provisioning.ProvisionStatus{})

			Expect(instance.Status.Phase).To(Equal(v1alpha1.ClusterOrderPhaseReady))
			cond := apimeta.FindStatusCondition(instance.Status.Conditions, v1alpha1.ConditionProgressing)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(v1alpha1.ReasonAsExpected))
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

		It("should skip when spec has more than one node request", func() {
			instance := &v1alpha1.ClusterOrder{
				Spec: v1alpha1.ClusterOrderSpec{
					NodeRequests: []v1alpha1.NodeRequest{
						{ResourceClass: "m1.large", NumberOfNodes: 3},
						{ResourceClass: "m1.small", NumberOfNodes: 1},
					},
				},
			}

			nodePool := &hypershiftv1beta1.NodePool{
				Status: hypershiftv1beta1.NodePoolStatus{
					Replicas: 5,
				},
			}

			err := reconciler.handleNodePool(ctx, instance, nodePool)
			Expect(err).NotTo(HaveOccurred())

			Expect(instance.Status.NodeRequests).To(BeEmpty(),
				"status.nodeRequests should not be touched when spec has multiple node requests")
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

	Context("Agent selection and cleanup", func() {
		const agentNS = "test-agents"

		var reconciler *ClusterOrderReconciler

		BeforeEach(func() {
			reconciler = &ClusterOrderReconciler{
				Client:         k8sClient,
				apiReader:      k8sClient,
				Scheme:         k8sClient.Scheme(),
				AgentNamespace: agentNS,
			}
			// Create agent namespace
			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: agentNS}}
			_ = k8sClient.Create(ctx, ns)
		})

		createAgent := func(name, resourceClass, serverName string) {
			agent := &unstructured.Unstructured{}
			agent.SetGroupVersionKind(agentGVK)
			agent.SetName(name)
			agent.SetNamespace(agentNS)
			agent.SetLabels(map[string]string{
				agentResourceClassLabel: resourceClass,
				agentServerNameLabel:    serverName,
			})
			Expect(k8sClient.Create(ctx, agent)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, agent)
			})
		}

		It("selects available agents and labels them", func() {
			createAgent("agent-1", "fc430", "server-01")
			createAgent("agent-2", "fc430", "server-02")
			createAgent("agent-3", "fc430", "server-03")

			instance := &v1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cluster"},
				Spec: v1alpha1.ClusterOrderSpec{
					NodeRequests: []v1alpha1.NodeRequest{
						{ResourceClass: "fc430", NumberOfNodes: 2},
					},
				},
			}

			result, err := reconciler.reconcileAgentSelection(ctx, instance)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			Expect(instance.Status.NodeSets).To(HaveLen(1))
			Expect(instance.Status.NodeSets[0].Name).To(Equal("fc430"))
			Expect(instance.Status.NodeSets[0].Agents).To(HaveLen(2))

			// Verify labels were set
			for _, agentStatus := range instance.Status.NodeSets[0].Agents {
				agent := &unstructured.Unstructured{}
				agent.SetGroupVersionKind(agentGVK)
				Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name: agentStatus.AgentName, Namespace: agentNS,
				}, agent)).To(Succeed())
				Expect(agent.GetLabels()[agentClusterOrderLabel]).To(Equal("test-cluster"))
			}
		})

		It("requeues when not enough agents available", func() {
			createAgent("agent-1", "fc430", "server-01")

			instance := &v1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cluster"},
				Spec: v1alpha1.ClusterOrderSpec{
					NodeRequests: []v1alpha1.NodeRequest{
						{ResourceClass: "fc430", NumberOfNodes: 3},
					},
				},
			}

			result, err := reconciler.reconcileAgentSelection(ctx, instance)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))
			Expect(instance.Status.NodeSets).To(BeEmpty())
		})

		It("is idempotent when agents already selected", func() {
			instance := &v1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cluster"},
				Spec: v1alpha1.ClusterOrderSpec{
					NodeRequests: []v1alpha1.NodeRequest{
						{ResourceClass: "fc430", NumberOfNodes: 1},
					},
				},
				Status: v1alpha1.ClusterOrderStatus{
					NodeSets: []v1alpha1.NodeSetStatus{
						{Name: "fc430", Agents: []v1alpha1.AgentStatus{
							{AgentName: "agent-1", HostName: "server-01"},
						}},
					},
				},
			}

			result, err := reconciler.reconcileAgentSelection(ctx, instance)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())
			Expect(instance.Status.NodeSets).To(HaveLen(1))
		})

		It("skips when no node requests", func() {
			instance := &v1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cluster"},
			}

			result, err := reconciler.reconcileAgentSelection(ctx, instance)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())
		})

		It("cleans up agent labels on deletion", func() {
			createAgent("agent-1", "fc430", "server-01")
			createAgent("agent-2", "fc430", "server-02")

			// Label agents as allocated to this cluster
			for _, name := range []string{"agent-1", "agent-2"} {
				agent := &unstructured.Unstructured{}
				agent.SetGroupVersionKind(agentGVK)
				Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name: name, Namespace: agentNS,
				}, agent)).To(Succeed())
				labels := agent.GetLabels()
				labels[agentClusterOrderLabel] = "test-cluster"
				agent.SetLabels(labels)
				Expect(k8sClient.Update(ctx, agent)).To(Succeed())
			}

			instance := &v1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cluster"},
			}

			err := reconciler.reconcileAgentCleanup(ctx, instance)
			Expect(err).NotTo(HaveOccurred())

			// Verify labels were removed
			for _, name := range []string{"agent-1", "agent-2"} {
				agent := &unstructured.Unstructured{}
				agent.SetGroupVersionKind(agentGVK)
				Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name: name, Namespace: agentNS,
				}, agent)).To(Succeed())
				Expect(agent.GetLabels()).NotTo(HaveKey(agentClusterOrderLabel))
			}
		})
	})

})
