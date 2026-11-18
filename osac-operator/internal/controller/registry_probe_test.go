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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

var _ = Describe("CaaS management registry probe", func() {
	ctx := context.Background()

	newOrder := func() *v1alpha1.ClusterOrder {
		return &v1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{
				Name: "registry-probe-order", Namespace: "orders", UID: types.UID("order-uid"),
				Annotations: map[string]string{
					tenantAnnotation: "tenant-a",
				},
				Labels: map[string]string{osacClusterOrderIDLabel: "cluster-id"},
			},
			Spec: v1alpha1.ClusterOrderSpec{
				SupportsAutomaticCaaSEndpoints: true,
				ReleaseImage:                   "registry.example.test/ocp/release:4.22.0",
				PullSecret:                     `{"auths":{"registry.example.test":{"auth":"fake-secret"}}}`,
			},
		}
	}

	newReconciler := func(order *v1alpha1.ClusterOrder) (*ClusterOrderReconciler, client.Client) {
		c := clientfake.NewClientBuilder().
			WithScheme(k8sClient.Scheme()).
			WithStatusSubresource(order, &corev1.Pod{}).
			WithObjects(order).
			Build()
		return &ClusterOrderReconciler{
			Client: c, Scheme: c.Scheme(), NetworkingProvisioningEnabled: true,
		}, c
	}

	It("waits for the management kubelet pull, records its image, and removes probe credentials", func() {
		order := newOrder()
		reconciler, c := newReconciler(order)

		ready, result, err := reconciler.reconcileManagementRegistryProbe(ctx, order)
		Expect(err).NotTo(HaveOccurred())
		Expect(ready).To(BeFalse())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))

		pod := &corev1.Pod{}
		podKey := client.ObjectKey{Namespace: order.Namespace, Name: managementRegistryProbePodName(order)}
		Expect(c.Get(ctx, podKey, pod)).To(Succeed())
		Expect(pod.Spec.Containers).To(HaveLen(1))
		Expect(pod.Spec.Containers[0].Image).To(Equal(order.Spec.ReleaseImage))
		Expect(pod.Spec.Containers[0].ImagePullPolicy).To(Equal(corev1.PullAlways))
		Expect(pod.Spec.Containers[0].Command).To(BeEmpty())
		Expect(pod.Spec.Containers[0].Args).To(BeEmpty())
		Expect(pod.Spec.ImagePullSecrets).To(ConsistOf(corev1.LocalObjectReference{
			Name: managementRegistryProbePullSecretName(order),
		}))

		pullSecret := &corev1.Secret{}
		secretKey := client.ObjectKey{Namespace: order.Namespace, Name: managementRegistryProbePullSecretName(order)}
		Expect(c.Get(ctx, secretKey, pullSecret)).To(Succeed())
		Expect(pullSecret.Data[corev1.DockerConfigJsonKey]).To(Equal([]byte(order.Spec.PullSecret)))
		Expect(pullSecret.Annotations[tenantAnnotation]).To(Equal("tenant-a"))

		pod.Status.ContainerStatuses = []corev1.ContainerStatus{{
			Name: "registry-image-pull", ImageID: "docker-pullable://registry.example.test/ocp/release@sha256:abc",
		}}
		Expect(c.Status().Update(ctx, pod)).To(Succeed())

		ready, result, err = reconciler.reconcileManagementRegistryProbe(ctx, order)
		Expect(err).NotTo(HaveOccurred())
		Expect(ready).To(BeFalse(), "the readiness evidence must be persisted before core provisioning")
		Expect(result.RequeueAfter).To(Equal(time.Second))
		Expect(apimeta.IsStatusConditionTrue(order.Status.Conditions, registryAccessReadyCondition)).To(BeTrue())
		condition := apimeta.FindStatusCondition(order.Status.Conditions, registryAccessReadyCondition)
		Expect(condition.Message).To(ContainSubstring("Management image source"))
		Expect(condition.Message).To(ContainSubstring(order.Spec.ReleaseImage))
		Expect(condition.Message).NotTo(ContainSubstring("fake-secret"))
		Expect(apierrors.IsNotFound(c.Get(ctx, podKey, &corev1.Pod{}))).To(BeTrue())
		Expect(apierrors.IsNotFound(c.Get(ctx, secretKey, &corev1.Secret{}))).To(BeTrue())

		ready, _, err = reconciler.reconcileManagementRegistryProbe(ctx, order)
		Expect(err).NotTo(HaveOccurred())
		Expect(ready).To(BeTrue())
	})

	It("keeps provisioning blocked and exposes an actionable pull failure", func() {
		order := newOrder()
		reconciler, c := newReconciler(order)
		_, _, err := reconciler.reconcileManagementRegistryProbe(ctx, order)
		Expect(err).NotTo(HaveOccurred())

		pod := &corev1.Pod{}
		key := client.ObjectKey{Namespace: order.Namespace, Name: managementRegistryProbePodName(order)}
		Expect(c.Get(ctx, key, pod)).To(Succeed())
		pod.Status.ContainerStatuses = []corev1.ContainerStatus{{
			Name: "registry-image-pull",
			State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{
				Reason: "ImagePullBackOff", Message: "unauthorized: authentication required",
			}},
		}}
		Expect(c.Status().Update(ctx, pod)).To(Succeed())

		ready, result, err := reconciler.reconcileManagementRegistryProbe(ctx, order)
		Expect(err).NotTo(HaveOccurred())
		Expect(ready).To(BeFalse())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		condition := apimeta.FindStatusCondition(order.Status.Conditions, registryAccessReadyCondition)
		Expect(condition).NotTo(BeNil())
		Expect(condition.Status).To(Equal(metav1.ConditionFalse))
		Expect(condition.Reason).To(Equal("ManagementImagePullFailed"))
		Expect(condition.Message).To(ContainSubstring(order.Spec.ReleaseImage))
		Expect(condition.Message).To(ContainSubstring("unauthorized"))
		Expect(condition.Message).NotTo(ContainSubstring("fake-secret"))
	})

	It("preserves provider-networking-disabled behavior", func() {
		order := newOrder()
		reconciler, c := newReconciler(order)
		reconciler.NetworkingProvisioningEnabled = false

		ready, result, err := reconciler.reconcileManagementRegistryProbe(ctx, order)
		Expect(err).NotTo(HaveOccurred())
		Expect(ready).To(BeTrue())
		Expect(result).To(BeZero())
		Expect(apimeta.FindStatusCondition(order.Status.Conditions, registryAccessReadyCondition)).To(BeNil())
		pods := &corev1.PodList{}
		Expect(c.List(ctx, pods, client.InNamespace(order.Namespace))).To(Succeed())
		Expect(pods.Items).To(BeEmpty())
	})
})
