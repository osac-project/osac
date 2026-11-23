// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package kube

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

var _ = Describe("Conditional Kubernetes mutations", func() {
	DescribeTable(
		"changes only its finalizer and skips unchanged writes",
		func(
			ctx SpecContext,
			update func(context.Context, client.Client, client.Object, string) error,
			initial, want []string,
		) {
			object := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
				Namespace: "hub", Name: "output", UID: "output", Finalizers: initial,
			}}
			patches := 0
			c := fake.NewClientBuilder().WithObjects(object).WithInterceptorFuncs(interceptor.Funcs{
				Patch: func(
					ctx context.Context, c client.WithWatch, o client.Object, patch client.Patch, opts ...client.PatchOption,
				) error {
					patches++
					return c.Patch(ctx, o, patch, opts...)
				},
			}).Build()

			current := &corev1.Secret{}
			Expect(c.Get(ctx, client.ObjectKeyFromObject(object), current)).To(Succeed())
			Expect(update(ctx, c, current, "simulator.example/cleanup")).To(Succeed())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(object), current)).To(Succeed())
			Expect(current.Finalizers).To(Equal(want))
			Expect(update(ctx, c, current, "simulator.example/cleanup")).To(Succeed())
			Expect(patches).To(Equal(1), "repeating the operation must leave the stored object unchanged")
		},
		Entry("adding", AddFinalizer, []string{"foreign.example/cleanup"},
			[]string{"foreign.example/cleanup", "simulator.example/cleanup"}),
		Entry(
			"removing",
			RemoveFinalizer,
			[]string{"foreign.example/cleanup", "simulator.example/cleanup", "other.example/cleanup"},
			[]string{"foreign.example/cleanup", "other.example/cleanup"},
		),
	)

	DescribeTable("preserves concurrent finalizer changes", func(ctx SpecContext, operation string) {
		object := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "hub", Name: "output", UID: "output"}}
		if operation == "remove" {
			object.Finalizers = []string{"simulator.example/cleanup"}
		}

		intervened := false
		c := fake.NewClientBuilder().WithObjects(object).WithInterceptorFuncs(interceptor.Funcs{
			Patch: func(
				ctx context.Context, c client.WithWatch, o client.Object, patch client.Patch, opts ...client.PatchOption,
			) error {
				if !intervened {
					intervened = true

					current := &corev1.Secret{}
					if err := c.Get(ctx, client.ObjectKeyFromObject(o), current); err != nil {
						return err
					}

					current.Finalizers = append(current.Finalizers, "foreign.example/cleanup")
					if err := c.Update(ctx, current); err != nil {
						return err
					}
				}

				return c.Patch(ctx, o, patch, opts...)
			},
		}).Build()
		mutate := func() error {
			current := &corev1.Secret{}
			if err := c.Get(ctx, client.ObjectKeyFromObject(object), current); err != nil {
				return err
			}

			if operation == "add" {
				return AddFinalizer(ctx, c, current, "simulator.example/cleanup")
			}

			return RemoveFinalizer(ctx, c, current, "simulator.example/cleanup")
		}

		By("rejecting a mutation that raced with a foreign finalizer")

		err := mutate()
		Expect(apierrors.IsConflict(err)).To(BeTrue(), "stale finalizer mutation: %v", err)
		By("reading again and preserving the concurrent writer's finalizer")
		Expect(mutate()).To(Succeed())

		current := &corev1.Secret{}
		Expect(c.Get(ctx, client.ObjectKeyFromObject(object), current)).To(Succeed())
		Expect(current.Finalizers).To(ContainElement("foreign.example/cleanup"))

		if operation == "remove" {
			Expect(current.Finalizers).NotTo(ContainElement("simulator.example/cleanup"))
		} else {
			Expect(current.Finalizers).To(ContainElement("simulator.example/cleanup"))
		}
	},
		Entry("adding a finalizer", "add"),
		Entry("removing a finalizer", "remove"),
	)

	It("deletes only the observed UID when an object is replaced at the last moment", func(ctx SpecContext) {
		object := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "hub", Name: "output", UID: "original"}}
		// The fake API needs an explicit check of the UID delete precondition.
		c := fake.NewClientBuilder().WithObjects(object).WithInterceptorFuncs(interceptor.Funcs{
			Delete: func(ctx context.Context, c client.WithWatch, o client.Object, opts ...client.DeleteOption) error {
				current := &corev1.Secret{}
				if err := c.Get(ctx, client.ObjectKeyFromObject(o), current); err != nil {
					return err
				}

				if err := c.Delete(ctx, current); err != nil {
					return err
				}

				replacement := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
					Namespace: o.GetNamespace(), Name: o.GetName(), UID: "replacement",
				}}
				if err := c.Create(ctx, replacement); err != nil {
					return err
				}

				options := &client.DeleteOptions{}
				options.ApplyOptions(opts)

				if options.Preconditions != nil && options.Preconditions.UID != nil &&
					*options.Preconditions.UID != replacement.UID {
					return apierrors.NewConflict(corev1.SchemeGroupVersion.WithResource("secrets").GroupResource(),
						o.GetName(), fmt.Errorf("UID changed"))
				}

				return c.Delete(ctx, o, opts...)
			},
		}).Build()

		By("requesting deletion of the original observation")

		observed := &corev1.Secret{}
		Expect(c.Get(ctx, client.ObjectKeyFromObject(object), observed)).To(Succeed())
		err := Delete(ctx, c, observed)
		Expect(apierrors.IsConflict(err)).To(BeTrue(), "replacement deletion: %v", err)
		By("confirming the replacement survives")

		current := &corev1.Secret{}
		Expect(c.Get(ctx, client.ObjectKeyFromObject(object), current)).To(Succeed())
		Expect(current.UID).To(Equal(types.UID("replacement")))
	})

	It("requires an observed identity and accepts an already-absent incarnation", func(ctx SpecContext) {
		c := fake.NewClientBuilder().Build()
		object := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "hub", Name: "absent"}}

		By("rejecting name-only deletion")
		Expect(Delete(ctx, c, object)).NotTo(Succeed())
		By("accepting natural disappearance of the observed UID")

		object.UID = "already-gone"
		Expect(Delete(ctx, c, object)).To(Succeed())
	})
})
