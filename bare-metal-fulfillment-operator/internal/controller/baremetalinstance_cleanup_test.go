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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
)

var _ = Describe("BareMetalInstance Auto-Cleanup", func() {
	var (
		ctx        context.Context
		reconciler *BareMetalInstanceReconciler
		bmi        *v1alpha1.BareMetalInstance
	)

	BeforeEach(func() {
		ctx = context.Background()
		reconciler = &BareMetalInstanceReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
		}
	})

	Describe("reconcileAutoCleanup", func() {
		Context("when cleanup finalizer is not present", func() {
			BeforeEach(func() {
				bmi = &v1alpha1.BareMetalInstance{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "test-bmi-cleanup-",
						Namespace:    "default",
					},
					Spec: v1alpha1.BareMetalInstanceSpec{
						ExternalHostID: "host-789",
						Selector: v1alpha1.HostSelectorSpec{
							HostSelector: map[string]string{"test": "value"},
						},
						TemplateID: "noop",
					},
				}
				Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
			})

			AfterEach(func() {
				_ = k8sClient.Delete(ctx, bmi)
			})

			It("should return done=true immediately", func() {
				_, done, err := reconciler.reconcileAutoCleanup(ctx, bmi)
				Expect(err).NotTo(HaveOccurred())
				Expect(done).To(BeTrue())
			})
		})

		Context("when cleanup finalizer is present with no auto-provisioned resources", func() {
			BeforeEach(func() {
				bmi = &v1alpha1.BareMetalInstance{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "test-bmi-cleanup-",
						Namespace:    "default",
					},
					Spec: v1alpha1.BareMetalInstanceSpec{
						ExternalHostID: "host-789",
						Selector: v1alpha1.HostSelectorSpec{
							HostSelector: map[string]string{"test": "value"},
						},
						TemplateID: "noop",
					},
				}
				controllerutil.AddFinalizer(bmi, BareMetalInstanceCleanupFinalizer)
				Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
			})

			AfterEach(func() {
				_ = k8sClient.Delete(ctx, bmi)
			})

			It("should remove finalizer and return done=true", func() {
				_, done, err := reconciler.reconcileAutoCleanup(ctx, bmi)
				Expect(err).NotTo(HaveOccurred())
				Expect(done).To(BeTrue())

				fresh := &v1alpha1.BareMetalInstance{}
				Expect(k8sClient.Get(ctx,
					client.ObjectKeyFromObject(bmi), fresh)).To(Succeed())
				Expect(controllerutil.ContainsFinalizer(fresh,
					BareMetalInstanceCleanupFinalizer)).To(BeFalse())
			})
		})

		Context("when auto-provisioned ExternalIPAttachments and ExternalIPs exist", func() {
			const bmiID = "bmi-cleanup-uuid"

			var (
				attachment *unstructured.Unstructured
				eip        *unstructured.Unstructured
			)

			newAutoCreatedObject := func(gvk schema.GroupVersionKind, name string, spec map[string]interface{}) *unstructured.Unstructured {
				object := &unstructured.Unstructured{Object: map[string]interface{}{
					"spec": spec,
				}}
				object.SetGroupVersionKind(gvk)
				object.SetNamespace("default")
				object.SetName(name)
				object.SetLabels(map[string]string{
					autoCreatedLabel:    "true",
					autoCreatedForLabel: bmiID,
				})
				return object
			}

			BeforeEach(func() {
				bmi = &v1alpha1.BareMetalInstance{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "test-bmi-cleanup-",
						Namespace:    "default",
						Labels:       map[string]string{bareMetalInstanceIDLabel: bmiID},
					},
					Spec: v1alpha1.BareMetalInstanceSpec{
						ExternalHostID: "host-789",
						Selector: v1alpha1.HostSelectorSpec{
							HostSelector: map[string]string{"test": "value"},
						},
						TemplateID: "noop",
					},
				}
				controllerutil.AddFinalizer(bmi, BareMetalInstanceCleanupFinalizer)
				Expect(k8sClient.Create(ctx, bmi)).To(Succeed())

				attachment = newAutoCreatedObject(externalIPAttachmentGVK, "cleanup-att", map[string]interface{}{
					"externalIP":        "cleanup-eip",
					"baremetalInstance": bmiID,
				})
				Expect(k8sClient.Create(ctx, attachment)).To(Succeed())
				eip = newAutoCreatedObject(externalIPGVK, "cleanup-eip", map[string]interface{}{
					"pool": "cleanup-pool",
				})
				Expect(k8sClient.Create(ctx, eip)).To(Succeed())
			})

			AfterEach(func() {
				for _, object := range []*unstructured.Unstructured{attachment, eip} {
					current := &unstructured.Unstructured{}
					current.SetGroupVersionKind(object.GroupVersionKind())
					err := k8sClient.Get(ctx, client.ObjectKeyFromObject(object), current)
					if err != nil {
						continue
					}
					current.SetFinalizers(nil)
					_ = k8sClient.Update(ctx, current)
					_ = k8sClient.Delete(ctx, current)
				}
				_ = k8sClient.Delete(ctx, bmi)
			})

			holdsObject := func(object *unstructured.Unstructured) func() bool {
				return func() bool {
					current := &unstructured.Unstructured{}
					current.SetGroupVersionKind(object.GroupVersionKind())
					return k8sClient.Get(ctx, client.ObjectKeyFromObject(object), current) == nil
				}
			}

			releaseObject := func(object *unstructured.Unstructured) {
				current := &unstructured.Unstructured{}
				current.SetGroupVersionKind(object.GroupVersionKind())
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(object), current)).To(Succeed())
				current.SetFinalizers(nil)
				Expect(k8sClient.Update(ctx, current)).To(Succeed())
			}

			It("deletes ExternalIPAttachments before ExternalIPs and removes the finalizer last", func() {
				By("triggering cleanup while a test finalizer holds each auto-created resource")
				testFinalizer := "osac.openshift.io/test-cleanup-finalizer"
				for _, object := range []*unstructured.Unstructured{attachment, eip} {
					current := &unstructured.Unstructured{}
					current.SetGroupVersionKind(object.GroupVersionKind())
					Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(object), current)).To(Succeed())
					current.SetFinalizers([]string{testFinalizer})
					Expect(k8sClient.Update(ctx, current)).To(Succeed())
				}

				By("deleting the ExternalIPAttachments and requeueing before touching ExternalIPs")
				_, done, err := reconciler.reconcileAutoCleanup(ctx, bmi)
				Expect(err).ToNot(HaveOccurred())
				Expect(done).To(BeFalse())
				Expect(holdsObject(attachment)()).To(BeTrue(), "attachment stays terminating")
				Expect(holdsObject(eip)()).To(BeTrue())
				storedEip := &unstructured.Unstructured{}
				storedEip.SetGroupVersionKind(externalIPGVK)
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(eip), storedEip)).To(Succeed())
				Expect(storedEip.GetDeletionTimestamp().IsZero()).To(BeTrue(),
					"ExternalIP must not be deleted while attachments remain")

				By("deleting the ExternalIPs once the attachments are gone")
				releaseObject(attachment)
				_, done, err = reconciler.reconcileAutoCleanup(ctx, bmi)
				Expect(err).ToNot(HaveOccurred())
				Expect(done).To(BeFalse())
				Eventually(holdsObject(attachment)).Should(BeFalse())
				Expect(holdsObject(eip)()).To(BeTrue(), "ExternalIP stays terminating")

				By("removing the cleanup finalizer only after both are gone")
				releaseObject(eip)
				_, done, err = reconciler.reconcileAutoCleanup(ctx, bmi)
				Expect(err).ToNot(HaveOccurred())
				Expect(done).To(BeTrue())
				fresh := &v1alpha1.BareMetalInstance{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bmi), fresh)).To(Succeed())
				Expect(controllerutil.ContainsFinalizer(fresh,
					BareMetalInstanceCleanupFinalizer)).To(BeFalse())
			})
		})
	})
})
