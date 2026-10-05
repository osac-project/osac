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
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

var _ = Describe("AddOnOperatorReconciler envtest", func() {
	It("persists independent outcomes without changing the main phase", func() {
		const (
			name      = "cluster-order-addon-envtest"
			namespace = "default"
		)
		ctx := context.Background()
		provider := newAddOnOperatorProviderStub()
		reconciler := NewAddOnOperatorReconciler(k8sClient, k8sClient, namespace, provider, time.Minute)
		reconciler.getAdminKubeconfig = func(context.Context, *osacv1alpha1.ClusterOrder) ([]byte, error) {
			return []byte("test-kubeconfig"), nil
		}
		order := &osacv1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Spec: osacv1alpha1.ClusterOrderSpec{
				TemplateID:     "test.template",
				AddOnOperators: []string{"cert-manager", "gpu-operator"},
			},
			Status: osacv1alpha1.ClusterOrderStatus{
				Phase: osacv1alpha1.ClusterOrderPhaseReady,
				ProvisioningJobs: []osacv1alpha1.JobStatus{{
					JobID: "cluster-job", Type: osacv1alpha1.JobTypeProvision,
					State: osacv1alpha1.JobStateSucceeded, Timestamp: metav1.Now(),
				}},
				ClusterStorageJobs: []osacv1alpha1.JobStatus{{
					JobID: "storage-job", Type: osacv1alpha1.JobTypeProvision,
					State: osacv1alpha1.JobStateSucceeded, Timestamp: metav1.Now(),
				}},
				Conditions: []metav1.Condition{{
					Type: osacv1alpha1.ConditionProgressing, Status: metav1.ConditionFalse,
					Reason: "Provisioned", Message: "cluster provisioned", LastTransitionTime: metav1.Now(),
				}},
			},
		}
		Expect(k8sClient.Create(ctx, order)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, order) })
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, order)).To(Succeed())
		order.Status.Phase = osacv1alpha1.ClusterOrderPhaseReady
		order.Status.ProvisioningJobs = []osacv1alpha1.JobStatus{{
			JobID: "cluster-job", Type: osacv1alpha1.JobTypeProvision,
			State: osacv1alpha1.JobStateSucceeded, Timestamp: metav1.Now(),
		}}
		order.Status.ClusterStorageJobs = []osacv1alpha1.JobStatus{{
			JobID: "storage-job", Type: osacv1alpha1.JobTypeProvision,
			State: osacv1alpha1.JobStateSucceeded, Timestamp: metav1.Now(),
		}}
		order.Status.Conditions = []metav1.Condition{{
			Type: osacv1alpha1.ConditionProgressing, Status: metav1.ConditionFalse,
			Reason: "Provisioned", Message: "cluster provisioned", LastTransitionTime: metav1.Now(),
		}}
		Expect(k8sClient.Status().Update(ctx, order)).To(Succeed())
		request := ctrl.Request{NamespacedName: types.NamespacedName{Name: name, Namespace: namespace}}

		_, err := reconciler.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(provider.adminKubeconfig).To(Equal("test-kubeconfig"))
		stored := &osacv1alpha1.ClusterOrder{}
		Expect(k8sClient.Get(ctx, request.NamespacedName, stored)).To(Succeed())
		Expect(stored.Status.AddOnOperatorJobs).To(HaveLen(2))

		provider.setJobStatus(stored.Status.AddOnOperatorJobs[0].JobID, provisioning.ProvisionStatus{
			State: osacv1alpha1.JobStateFailed, Message: "failed", ErrorDetails: "private traceback",
		})
		provider.setJobStatus(stored.Status.AddOnOperatorJobs[1].JobID, provisioning.ProvisionStatus{
			State: osacv1alpha1.JobStateSucceeded, Message: "successful",
		})
		_, err = reconciler.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())

		Expect(k8sClient.Get(ctx, request.NamespacedName, stored)).To(Succeed())
		Expect(stored.Status.Phase).To(Equal(osacv1alpha1.ClusterOrderPhaseReady))
		Expect(stored.Status.AddOnOperatorJobs[0].State).To(Equal(osacv1alpha1.JobStateFailed))
		Expect(stored.Status.AddOnOperatorJobs[0].Message).NotTo(ContainSubstring("traceback"))
		Expect(findAddOnOperatorCondition(stored).Status).To(Equal(metav1.ConditionFalse))

		stored.Status.AddOnOperatorJobs[0].Timestamp = metav1.NewTime(time.Now().Add(-provisioning.BackoffMaxDelay))
		Expect(k8sClient.Status().Update(ctx, stored)).To(Succeed())
		_, err = reconciler.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(provider.triggeredOperators).To(Equal([]string{"cert-manager", "gpu-operator", "cert-manager"}))

		Expect(k8sClient.Get(ctx, request.NamespacedName, stored)).To(Succeed())
		Expect(findAddOnOperatorCondition(stored).Status).To(Equal(metav1.ConditionFalse))
		retryJobID := stored.Status.AddOnOperatorJobs[2].JobID
		provider.setJobStatus(retryJobID, provisioning.ProvisionStatus{State: osacv1alpha1.JobStateSucceeded})
		_, err = reconciler.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Get(ctx, request.NamespacedName, stored)).To(Succeed())
		Expect(findAddOnOperatorCondition(stored).Status).To(Equal(metav1.ConditionTrue))
		Expect(stored.Status.Phase).To(Equal(osacv1alpha1.ClusterOrderPhaseReady))
		Expect(stored.Status.ProvisioningJobs).To(HaveLen(1))
		Expect(stored.Status.ClusterStorageJobs).To(HaveLen(1))
		Expect(apimeta.FindStatusCondition(stored.Status.Conditions, osacv1alpha1.ConditionProgressing)).NotTo(BeNil())
	})

	It("registers the controller and reacts when a ClusterOrder transitions to Ready", func() {
		const namespace = "addon-operator-manager-test"
		provider := newAddOnOperatorProviderStub()
		localManager, err := ctrl.NewManager(cfg, ctrl.Options{
			Scheme:  k8sClient.Scheme(),
			Metrics: metricsserver.Options{BindAddress: "0"},
		})
		Expect(err).NotTo(HaveOccurred())
		manager, err := mcmanager.WithMultiCluster(localManager, nil)
		Expect(err).NotTo(HaveOccurred())
		reconciler := NewAddOnOperatorReconciler(k8sClient, k8sClient, namespace, provider, 10*time.Millisecond)
		reconciler.getAdminKubeconfig = func(context.Context, *osacv1alpha1.ClusterOrder) ([]byte, error) {
			return []byte("test-kubeconfig"), nil
		}
		Expect(reconciler.SetupWithManager(manager)).To(Succeed())

		startCtx, cancel := context.WithCancel(ctx)
		DeferCleanup(cancel)
		go func() { _ = localManager.Start(startCtx) }()
		Expect(localManager.GetCache().WaitForCacheSync(startCtx)).To(BeTrue())
		Expect(k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}) })

		order := &osacv1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{Name: "manager-watched-order", Namespace: namespace},
			Spec: osacv1alpha1.ClusterOrderSpec{
				TemplateID:     "test.template",
				AddOnOperators: []string{"cert-manager"},
			},
		}
		Expect(k8sClient.Create(ctx, order)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, order) })
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: order.Name, Namespace: namespace}, order)).To(Succeed())
		order.Status.Phase = osacv1alpha1.ClusterOrderPhaseProgressing
		Expect(k8sClient.Status().Update(ctx, order)).To(Succeed())
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: order.Name, Namespace: namespace}, order)).To(Succeed())
		order.Status.Phase = osacv1alpha1.ClusterOrderPhaseReady
		Expect(k8sClient.Status().Update(ctx, order)).To(Succeed())

		Eventually(func() int {
			current := &osacv1alpha1.ClusterOrder{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: order.Name, Namespace: namespace}, current); err != nil {
				return 0
			}
			return len(current.Status.AddOnOperatorJobs)
		}, 5*time.Second, 50*time.Millisecond).Should(Equal(1))
	})
})
