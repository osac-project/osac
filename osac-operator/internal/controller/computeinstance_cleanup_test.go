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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	v1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

var _ = Describe("ComputeInstance automatic ExternalIP cleanup", func() {
	It("holds provider cleanup and the parent finalizer while Fulfillment also removes owned children", func() {
		const (
			namespace = "default"
			ownerID   = "test-compute-cleanup-uuid"
			interval  = 50 * time.Millisecond
		)
		ctx := context.Background()
		instance := &v1alpha1.ComputeInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-compute-cleanup",
				Namespace:  namespace,
				Finalizers: []string{osacComputeInstanceFinalizer},
				Labels: map[string]string{
					osacComputeInstanceIDLabel: ownerID,
				},
			},
			Spec: newTestComputeInstanceSpec("test_template"),
		}
		Expect(k8sClient.Create(ctx, instance)).To(Succeed())
		instance.Status.Phase = v1alpha1.ComputeInstancePhaseDeleting
		Expect(k8sClient.Status().Update(ctx, instance)).To(Succeed())
		DeferCleanup(func() {
			stored := &v1alpha1.ComputeInstance{}
			if k8sClient.Get(ctx, client.ObjectKeyFromObject(instance), stored) == nil {
				stored.Finalizers = nil
				Expect(k8sClient.Update(ctx, stored)).To(Succeed())
			}
		})

		target := ownerID
		attachment := &v1alpha1.ExternalIPAttachment{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-compute-cleanup-attachment",
				Namespace:  namespace,
				Finalizers: []string{"test.provider/detach"},
				Labels:     map[string]string{autoCreatedLabel: labelValueTrue},
			},
			Spec: v1alpha1.ExternalIPAttachmentSpec{
				ExternalIP:      "test-compute-cleanup-address",
				ComputeInstance: &target,
			},
		}
		Expect(k8sClient.Create(ctx, attachment)).To(Succeed())
		waitForIndexedClientObject(attachment)
		address := &v1alpha1.ExternalIP{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-compute-cleanup-address",
				Namespace:  namespace,
				Finalizers: []string{"test.provider/release"},
				Labels: map[string]string{
					autoCreatedLabel:    labelValueTrue,
					autoCreatedForLabel: ownerID,
				},
			},
			Spec: v1alpha1.ExternalIPSpec{Pool: "test-pool"},
		}
		Expect(k8sClient.Create(ctx, address)).To(Succeed())
		waitForIndexedClientObject(address)

		providerCalls := 0
		provider := &mockProvisioningProvider{
			name: "aap",
			triggerDeprovisionFunc: func(_ context.Context, _ client.Object, _ []v1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
				providerCalls++
				return &provisioning.DeprovisionResult{Action: provisioning.DeprovisionSkipped}, nil
			},
		}
		r := NewComputeInstanceReconciler(testMcManager, namespace, namespace, namespace, provider, interval, 0, mcmanager.LocalCluster)
		r.Client = autoExternalIPCleanupTestClient()
		key := types.NamespacedName{Name: instance.Name, Namespace: namespace}
		reconcileParent := func() time.Duration {
			result, err := r.Reconcile(ctx, mcreconcile.Request{Request: reconcile.Request{NamespacedName: key}})
			Expect(err).NotTo(HaveOccurred())
			return result.RequeueAfter
		}
		assertHeld := func() {
			stored := &v1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, key, stored)).To(Succeed())
			Expect(stored.Finalizers).To(ContainElement(osacComputeInstanceFinalizer))
			Expect(providerCalls).To(BeZero())
		}

		Expect(k8sClient.Delete(ctx, instance)).To(Succeed())
		Expect(reconcileParent()).To(Equal(interval))
		assertHeld()
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(attachment), attachment)).To(Succeed())
		Expect(attachment.DeletionTimestamp).NotTo(BeNil())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(address), address)).To(Succeed())
		Expect(address.DeletionTimestamp).To(BeNil())

		Expect(reconcileParent()).To(Equal(interval))
		assertHeld()

		attachment.Finalizers = nil
		Expect(k8sClient.Update(ctx, attachment)).To(Succeed())
		waitForIndexedClientObjectGone(attachment)
		Expect(k8sClient.Delete(ctx, address)).To(Succeed())
		Expect(reconcileParent()).To(Equal(interval))
		assertHeld()
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(address), address)).To(Succeed())
		Expect(address.DeletionTimestamp).NotTo(BeNil())

		address.Finalizers = nil
		Expect(k8sClient.Update(ctx, address)).To(Succeed())
		waitForIndexedClientObjectGone(address)
		Expect(reconcileParent()).To(BeZero())
		Expect(providerCalls).To(Equal(1))
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key, &v1alpha1.ComputeInstance{}))).To(BeTrue())
	})
})
