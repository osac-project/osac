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
	"time"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	bmfov1alpha1 "github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("BareMetalInstance cleanup automatic external access", func() {
	newNamespaces := func() (string, string) {
		suffix := uuid.NewString()[:8]
		bmiNamespace := "bmi-cleanup-" + suffix
		networkNamespace := "network-cleanup-" + suffix
		for _, name := range []string{bmiNamespace, networkNamespace} {
			namespaceName := name
			Expect(k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespaceName}})).To(Succeed())
			DeferCleanup(func() {
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespaceName}}))).To(Succeed())
			})
		}
		return bmiNamespace, networkNamespace
	}

	newReadyBMI := func(namespace string, runStrategy bmfov1alpha1.BareMetalInstanceRunStrategy) *bmfov1alpha1.BareMetalInstance {
		bmi := &bmfov1alpha1.BareMetalInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "instance",
				Namespace: namespace,
				Labels:    map[string]string{osacBareMetalInstanceIDLabel: "bmi-1"},
			},
			Spec: bmfov1alpha1.BareMetalInstanceSpec{
				TemplateID:  "test_template",
				Selector:    bmfov1alpha1.HostSelectorSpec{HostSelector: map[string]string{"pool": "test"}},
				RunStrategy: runStrategy,
			},
		}
		Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
		bmi.Status = bmfov1alpha1.BareMetalInstanceStatus{
			Phase:       bmfov1alpha1.BareMetalInstancePhaseReady,
			RunStrategy: runStrategy,
			NetworkAttachmentStatuses: []bmfov1alpha1.BareMetalNetworkAttachmentStatus{{
				Primary: true, IPAddress: "192.0.2.12",
			}},
		}
		Expect(k8sClient.Status().Update(ctx, bmi)).To(Succeed())
		Eventually(func(g Gomega) {
			persisted := &bmfov1alpha1.BareMetalInstance{}
			g.Expect(indexedClient.Get(ctx, client.ObjectKeyFromObject(bmi), persisted)).To(Succeed())
			g.Expect(persisted.Status.Phase).To(Equal(bmfov1alpha1.BareMetalInstancePhaseReady))
			g.Expect(persisted.Status.RunStrategy).To(Equal(runStrategy))
		}, 5*time.Second, 100*time.Millisecond).Should(Succeed())
		DeferCleanup(func() {
			current := &bmfov1alpha1.BareMetalInstance{}
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), current)
			if apierrors.IsNotFound(err) {
				return
			}
			Expect(err).ToNot(HaveOccurred())
			current.Finalizers = nil
			Expect(k8sClient.Update(ctx, current)).To(Succeed())
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, current))).To(Succeed())
		})
		return bmi
	}

	It("creates one attachment for an allocated EIP after persisted Ready and Always status", func() {
		bmiNamespace, networkNamespace := newNamespaces()
		bmi := newReadyBMI(bmiNamespace, bmfov1alpha1.RunStrategyAlways)
		address := "192.0.2.10"
		eip := &osacv1alpha1.ExternalIP{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "auto-eip",
				Namespace: networkNamespace,
				Labels: map[string]string{
					autoCreatedLabel:     labelValueTrue,
					autoCreatedForLabel:  "bmi-1",
					autoCreatedKindLabel: autoExternalIPKindLabel(bareMetalInstanceOwner),
					externalIPUUIDLabel:  "eip-1",
				},
				Annotations: map[string]string{tenantAnnotation: "tenant-1"},
			},
			Spec: osacv1alpha1.ExternalIPSpec{Pool: "pool-1"},
		}
		Expect(k8sClient.Create(ctx, eip)).To(Succeed())
		eip.Status = osacv1alpha1.ExternalIPStatus{Address: address, Conditions: allocatedConditions(address)}
		Expect(k8sClient.Status().Update(ctx, eip)).To(Succeed())
		eventuallyIndexedExternalIP(eip)

		autoAttach := true
		fulfillmentBMI := privatev1.BareMetalInstance_builder{
			Metadata: privatev1.Metadata_builder{Tenant: "tenant-1"}.Build(),
			Spec:     privatev1.BareMetalInstanceSpec_builder{AutoExternalIpAttachment: &autoAttach}.Build(),
		}.Build()
		instancesClient := &testBareMetalInstancesGetter{
			response: privatev1.BareMetalInstancesGetResponse_builder{Object: fulfillmentBMI}.Build(),
		}
		attachmentsClient := &testExternalIPAttachmentsClient{}
		reconciler := NewBareMetalInstanceCleanupReconciler(indexedClient, bmiNamespace, networkNamespace)
		reconciler.BareMetalInstancesClient = instancesClient
		reconciler.ExternalIPAttachmentsClient = attachmentsClient
		request := ctrl.Request{NamespacedName: types.NamespacedName{Name: bmi.Name, Namespace: bmi.Namespace}}

		_, err := reconciler.Reconcile(ctx, request)
		Expect(err).ToNot(HaveOccurred())
		eventuallyCleanupFinalizer(bmi)
		_, err = reconciler.Reconcile(ctx, request)
		Expect(err).ToNot(HaveOccurred())
		Expect(instancesClient.getCalls).To(Equal(1))
		Expect(attachmentsClient.created).To(HaveLen(1))
		Expect(attachmentsClient.created[0].GetMetadata().GetName()).To(Equal("auto-eipa-eip-1"))

		_, err = reconciler.Reconcile(ctx, request)
		Expect(err).ToNot(HaveOccurred())
		Expect(attachmentsClient.created).To(HaveLen(1), "reconciliation must remain idempotent")
	})

	It("does not request an attachment after deletion starts", func() {
		bmiNamespace, networkNamespace := newNamespaces()
		bmi := newReadyBMI(bmiNamespace, bmfov1alpha1.RunStrategyAlways)
		autoAttach := true
		fulfillmentBMI := privatev1.BareMetalInstance_builder{
			Metadata: privatev1.Metadata_builder{Tenant: "tenant-1"}.Build(),
			Spec:     privatev1.BareMetalInstanceSpec_builder{AutoExternalIpAttachment: &autoAttach}.Build(),
		}.Build()
		instancesClient := &testBareMetalInstancesGetter{
			response: privatev1.BareMetalInstancesGetResponse_builder{Object: fulfillmentBMI}.Build(),
		}
		attachmentsClient := &testExternalIPAttachmentsClient{}
		reconciler := NewBareMetalInstanceCleanupReconciler(indexedClient, bmiNamespace, networkNamespace)
		reconciler.BareMetalInstancesClient = instancesClient
		reconciler.ExternalIPAttachmentsClient = attachmentsClient
		request := ctrl.Request{NamespacedName: types.NamespacedName{Name: bmi.Name, Namespace: bmi.Namespace}}

		_, err := reconciler.Reconcile(ctx, request)
		Expect(err).ToNot(HaveOccurred(), "first reconcile installs the cleanup finalizer")
		current := &bmfov1alpha1.BareMetalInstance{}
		eventuallyCleanupFinalizer(bmi)
		Expect(indexedClient.Get(ctx, request.NamespacedName, current)).To(Succeed())
		Expect(k8sClient.Delete(ctx, current)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(indexedClient.Get(ctx, request.NamespacedName, current)).To(Succeed())
			g.Expect(current.DeletionTimestamp.IsZero()).To(BeFalse())
		}, 5*time.Second, 100*time.Millisecond).Should(Succeed())

		_, err = reconciler.Reconcile(ctx, request)
		Expect(err).ToNot(HaveOccurred())
		Expect(instancesClient.getCalls).To(Equal(0))
		Expect(attachmentsClient.listCalls).To(Equal(0))
		Expect(attachmentsClient.created).To(BeEmpty())
		Eventually(func() error {
			return k8sClient.Get(ctx, request.NamespacedName, &bmfov1alpha1.BareMetalInstance{})
		}).Should(Satisfy(apierrors.IsNotFound))
	})

	DescribeTable("waits for a ready Always endpoint with a primary address before requesting an attachment",
		func(phase bmfov1alpha1.BareMetalInstancePhaseType, runStrategy bmfov1alpha1.BareMetalInstanceRunStrategy,
			primary bool, address string) {
			bmiNamespace, networkNamespace := newNamespaces()
			bmi := newReadyBMI(bmiNamespace, bmfov1alpha1.RunStrategyAlways)
			persisted := &bmfov1alpha1.BareMetalInstance{}
			Expect(indexedClient.Get(ctx, client.ObjectKeyFromObject(bmi), persisted)).To(Succeed())
			persisted.Status.Phase = phase
			persisted.Status.RunStrategy = runStrategy
			persisted.Status.NetworkAttachmentStatuses = []bmfov1alpha1.BareMetalNetworkAttachmentStatus{{
				Primary: primary, IPAddress: address,
			}}
			expectedAttachments := append(
				[]bmfov1alpha1.BareMetalNetworkAttachmentStatus(nil),
				persisted.Status.NetworkAttachmentStatuses...,
			)
			Expect(k8sClient.Status().Update(ctx, persisted)).To(Succeed())
			Eventually(func(g Gomega) {
				current := &bmfov1alpha1.BareMetalInstance{}
				g.Expect(indexedClient.Get(ctx, client.ObjectKeyFromObject(bmi), current)).To(Succeed())
				g.Expect(current.Status.Phase).To(Equal(phase))
				g.Expect(current.Status.RunStrategy).To(Equal(runStrategy))
				g.Expect(current.Status.NetworkAttachmentStatuses).To(Equal(expectedAttachments))
			}, 5*time.Second, 100*time.Millisecond).Should(Succeed())

			ip := &osacv1alpha1.ExternalIP{
				ObjectMeta: metav1.ObjectMeta{
					Name: "auto-eip", Namespace: networkNamespace,
					Labels: map[string]string{
						autoCreatedLabel: labelValueTrue, autoCreatedForLabel: "bmi-1",
						autoCreatedKindLabel: autoExternalIPKindLabel(bareMetalInstanceOwner), externalIPUUIDLabel: "eip-1",
					},
					Annotations: map[string]string{tenantAnnotation: "tenant-1"},
				},
				Spec: osacv1alpha1.ExternalIPSpec{Pool: "pool-1"},
			}
			Expect(k8sClient.Create(ctx, ip)).To(Succeed())
			ip.Status = osacv1alpha1.ExternalIPStatus{Address: "192.0.2.10", Conditions: allocatedConditions("192.0.2.10")}
			Expect(k8sClient.Status().Update(ctx, ip)).To(Succeed())
			eventuallyIndexedExternalIP(ip)

			autoAttach := true
			fulfillmentBMI := privatev1.BareMetalInstance_builder{
				Metadata: privatev1.Metadata_builder{Tenant: "tenant-1"}.Build(),
				Spec:     privatev1.BareMetalInstanceSpec_builder{AutoExternalIpAttachment: &autoAttach}.Build(),
			}.Build()
			instancesClient := &testBareMetalInstancesGetter{
				response: privatev1.BareMetalInstancesGetResponse_builder{Object: fulfillmentBMI}.Build(),
			}
			attachmentsClient := &testExternalIPAttachmentsClient{}
			reconciler := NewBareMetalInstanceCleanupReconciler(indexedClient, bmiNamespace, networkNamespace)
			reconciler.BareMetalInstancesClient = instancesClient
			reconciler.ExternalIPAttachmentsClient = attachmentsClient
			request := ctrl.Request{NamespacedName: types.NamespacedName{Name: bmi.Name, Namespace: bmi.Namespace}}

			_, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			eventuallyCleanupFinalizer(bmi)
			result, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(automaticExternalAccessRecheck))
			Expect(attachmentsClient.created).To(BeEmpty())
		},
		Entry("progressing", bmfov1alpha1.BareMetalInstancePhaseProgressing, bmfov1alpha1.RunStrategyAlways, true, "192.0.2.12"),
		Entry("halted", bmfov1alpha1.BareMetalInstancePhaseReady, bmfov1alpha1.RunStrategyHalted, true, "192.0.2.12"),
		Entry("without an address", bmfov1alpha1.BareMetalInstancePhaseReady, bmfov1alpha1.RunStrategyAlways, true, ""),
		Entry("without a primary address", bmfov1alpha1.BareMetalInstancePhaseReady, bmfov1alpha1.RunStrategyAlways, false, "192.0.2.12"),
	)

	It("rechecks a Fulfillment readiness race after thirty seconds", func() {
		bmiNamespace, networkNamespace := newNamespaces()
		bmi := newReadyBMI(bmiNamespace, bmfov1alpha1.RunStrategyAlways)
		address := "192.0.2.10"
		eip := &osacv1alpha1.ExternalIP{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "auto-eip",
				Namespace: networkNamespace,
				Labels: map[string]string{
					autoCreatedLabel:     labelValueTrue,
					autoCreatedForLabel:  "bmi-1",
					autoCreatedKindLabel: autoExternalIPKindLabel(bareMetalInstanceOwner),
					externalIPUUIDLabel:  "eip-1",
				},
				Annotations: map[string]string{tenantAnnotation: "tenant-1"},
			},
			Spec: osacv1alpha1.ExternalIPSpec{Pool: "pool-1"},
		}
		Expect(k8sClient.Create(ctx, eip)).To(Succeed())
		eip.Status = osacv1alpha1.ExternalIPStatus{Address: address, Conditions: allocatedConditions(address)}
		Expect(k8sClient.Status().Update(ctx, eip)).To(Succeed())
		eventuallyIndexedExternalIP(eip)

		autoAttach := true
		fulfillmentBMI := privatev1.BareMetalInstance_builder{
			Metadata: privatev1.Metadata_builder{Tenant: "tenant-1"}.Build(),
			Spec:     privatev1.BareMetalInstanceSpec_builder{AutoExternalIpAttachment: &autoAttach}.Build(),
		}.Build()
		instancesClient := &testBareMetalInstancesGetter{
			response: privatev1.BareMetalInstancesGetResponse_builder{Object: fulfillmentBMI}.Build(),
		}
		attachmentsClient := &testExternalIPAttachmentsClient{
			createErr: status.Error(codes.FailedPrecondition, "target is not ready yet"),
		}
		reconciler := NewBareMetalInstanceCleanupReconciler(indexedClient, bmiNamespace, networkNamespace)
		reconciler.BareMetalInstancesClient = instancesClient
		reconciler.ExternalIPAttachmentsClient = attachmentsClient
		request := ctrl.Request{NamespacedName: types.NamespacedName{Name: bmi.Name, Namespace: bmi.Namespace}}

		_, err := reconciler.Reconcile(ctx, request)
		Expect(err).ToNot(HaveOccurred())
		eventuallyCleanupFinalizer(bmi)
		result, err := reconciler.Reconcile(ctx, request)
		Expect(err).ToNot(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(automaticExternalAccessRecheck))
		Expect(instancesClient.getCalls).To(Equal(1))
	})
})

func eventuallyIndexedExternalIP(eip *osacv1alpha1.ExternalIP) {
	Eventually(func(g Gomega) {
		persisted := &osacv1alpha1.ExternalIP{}
		g.Expect(indexedClient.Get(ctx, client.ObjectKeyFromObject(eip), persisted)).To(Succeed())
		g.Expect(persisted.Status.Address).To(Equal(eip.Status.Address))
	}, 5*time.Second, 100*time.Millisecond).Should(Succeed())
}

func eventuallyCleanupFinalizer(bmi *bmfov1alpha1.BareMetalInstance) {
	Eventually(func(g Gomega) {
		persisted := &bmfov1alpha1.BareMetalInstance{}
		g.Expect(indexedClient.Get(ctx, client.ObjectKeyFromObject(bmi), persisted)).To(Succeed())
		g.Expect(persisted.Finalizers).To(ContainElement(bareMetalInstanceCleanupFinalizer))
	}, 5*time.Second, 100*time.Millisecond).Should(Succeed())
}
