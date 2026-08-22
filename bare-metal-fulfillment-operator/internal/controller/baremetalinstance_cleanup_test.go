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
						HostType:       "test-host",
						ExternalHostID: "host-789",
						TemplateID:     "noop",
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
						HostType:       "test-host",
						ExternalHostID: "host-789",
						TemplateID:     "noop",
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
	})
})
