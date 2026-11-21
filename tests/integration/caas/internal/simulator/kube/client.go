// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package kube

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Delete requests deletion of the observed UID and accepts an already absent resource.
// Existing finalizers still control when deletion finishes.
func Delete(ctx context.Context, c client.Client, o client.Object) error {
	uid := o.GetUID()
	if uid == "" {
		return fmt.Errorf("%T %s/%s has no observed UID", o, o.GetNamespace(), o.GetName())
	}

	err := client.IgnoreNotFound(c.Delete(ctx, o, client.Preconditions{UID: &uid}))
	if err == nil {
		log.FromContext(ctx).V(4).Info("native deletion requested or resource absent",
			"kind", o.GetObjectKind().GroupVersionKind().Kind,
			"namespace", o.GetNamespace(),
			"name", o.GetName(),
			"uid", o.GetUID(),
		)
	}

	return err
}

// AddFinalizer adds name with an optimistic patch and preserves other finalizers.
func AddFinalizer(ctx context.Context, c client.Client, o client.Object, name string) error {
	before := o.DeepCopyObject().(client.Object)
	if !controllerutil.AddFinalizer(o, name) {
		return nil
	}

	return patchFinalizer(ctx, c, before, o, name)
}

// RemoveFinalizer removes name with an optimistic patch and preserves other finalizers.
func RemoveFinalizer(ctx context.Context, c client.Client, o client.Object, name string) error {
	before := o.DeepCopyObject().(client.Object)
	if !controllerutil.RemoveFinalizer(o, name) {
		return nil
	}

	return patchFinalizer(ctx, c, before, o, name)
}

// patchFinalizer saves a changed finalizer list against its observed resource version.
func patchFinalizer(ctx context.Context, c client.Client, before, after client.Object, name string) error {
	err := c.Patch(ctx, after, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
	if err == nil {
		log.FromContext(ctx).V(4).Info("updated native finalizer",
			"kind", after.GetObjectKind().GroupVersionKind().Kind,
			"namespace", after.GetNamespace(),
			"name", after.GetName(),
			"uid", after.GetUID(),
			"finalizer", name,
			"present", controllerutil.ContainsFinalizer(after, name),
		)
	}

	return err
}
