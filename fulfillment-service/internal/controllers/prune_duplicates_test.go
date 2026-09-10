/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package controllers

import (
	"context"
	"errors"
	"log/slog"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

var _ = Describe("PruneDuplicateCRs", func() {
	var (
		ctx    context.Context
		scheme *runtime.Scheme
	)

	BeforeEach(func() {
		ctx = context.Background()
		scheme = runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())
	})

	It("should delete all extra objects", func() {
		extra1 := &osacv1alpha1.Subnet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "subnet-dup-1",
				Namespace: "test-ns",
			},
		}
		extra2 := &osacv1alpha1.Subnet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "subnet-dup-2",
				Namespace: "test-ns",
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(extra1, extra2).
			Build()

		extras := []clnt.Object{extra1, extra2}
		PruneDuplicateCRs(ctx, slog.Default(), fakeClient, extras, "subnet", "test-id")

		// Verify both extras were deleted
		list := &osacv1alpha1.SubnetList{}
		err := fakeClient.List(ctx, list)
		Expect(err).ToNot(HaveOccurred())
		Expect(list.Items).To(BeEmpty())
	})

	It("should do nothing when extras slice is empty", func() {
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		extras := []clnt.Object{}
		// Should not panic or error
		PruneDuplicateCRs(ctx, slog.Default(), fakeClient, extras, "subnet", "test-id")
	})

	It("should continue deleting remaining extras when one delete fails", func() {
		extra1 := &osacv1alpha1.Subnet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "subnet-fail",
				Namespace: "test-ns",
			},
		}
		extra2 := &osacv1alpha1.Subnet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "subnet-ok",
				Namespace: "test-ns",
			},
		}

		deleteCount := 0
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(extra1, extra2).
			WithInterceptorFuncs(interceptor.Funcs{
				Delete: func(ctx context.Context, client clnt.WithWatch, obj clnt.Object, opts ...clnt.DeleteOption) error {
					deleteCount++
					if obj.GetName() == "subnet-fail" {
						return errors.New("delete failed")
					}
					return client.Delete(ctx, obj, opts...)
				},
			}).
			Build()

		extras := []clnt.Object{extra1, extra2}
		PruneDuplicateCRs(ctx, slog.Default(), fakeClient, extras, "subnet", "test-id")

		// Both deletes should have been attempted
		Expect(deleteCount).To(Equal(2))

		// The second object should have been deleted despite first failure
		list := &osacv1alpha1.SubnetList{}
		err := fakeClient.List(ctx, list)
		Expect(err).ToNot(HaveOccurred())
		// Only the failed one should remain
		Expect(list.Items).To(HaveLen(1))
		Expect(list.Items[0].Name).To(Equal("subnet-fail"))
	})

	It("should handle single extra object", func() {
		extra := &osacv1alpha1.Subnet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "subnet-single",
				Namespace: "test-ns",
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(extra).
			Build()

		extras := []clnt.Object{extra}
		PruneDuplicateCRs(ctx, slog.Default(), fakeClient, extras, "subnet", "test-id")

		list := &osacv1alpha1.SubnetList{}
		err := fakeClient.List(ctx, list)
		Expect(err).ToNot(HaveOccurred())
		Expect(list.Items).To(BeEmpty())
	})

	It("should log errors but not panic when all deletes fail", func() {
		extra1 := &osacv1alpha1.Subnet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "subnet-err-1",
				Namespace: "test-ns",
			},
		}
		extra2 := &osacv1alpha1.Subnet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "subnet-err-2",
				Namespace: "test-ns",
			},
		}

		deleteCount := 0
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(extra1, extra2).
			WithInterceptorFuncs(interceptor.Funcs{
				Delete: func(ctx context.Context, client clnt.WithWatch, obj clnt.Object, opts ...clnt.DeleteOption) error {
					deleteCount++
					return errors.New("delete failed")
				},
			}).
			Build()

		extras := []clnt.Object{extra1, extra2}
		// Should not panic
		PruneDuplicateCRs(ctx, slog.Default(), fakeClient, extras, "subnet", "test-id")

		// Both deletes should have been attempted
		Expect(deleteCount).To(Equal(2))
	})
})
