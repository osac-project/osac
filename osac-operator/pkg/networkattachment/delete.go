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

// Package networkattachment contains shared NetworkAttachment lifecycle helpers.
package networkattachment

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// DeleteOwned requests deletion of a NetworkAttachment and removes ownerFinalizer
// after the networking controller has removed its finalizer. It returns true
// only when the object is absent. reader should be an uncached API reader when
// one is available; writer performs deletion and finalizer patches.
func DeleteOwned(
	ctx context.Context,
	writer client.Client,
	reader client.Reader,
	key client.ObjectKey,
	object client.Object,
	ownerFinalizer string,
) (bool, error) {
	if writer == nil {
		return false, fmt.Errorf("NetworkAttachment writer is not configured")
	}
	if reader == nil {
		reader = writer
	}
	if err := reader.Get(ctx, key, object); err != nil {
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	}

	if object.GetDeletionTimestamp().IsZero() {
		if err := writer.Delete(ctx, object); err != nil && !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("delete NetworkAttachment %s: %w", key.Name, err)
		}
		return false, nil
	}

	finalizers := object.GetFinalizers()
	if len(finalizers) == 1 && finalizers[0] == ownerFinalizer {
		base := object.DeepCopyObject().(client.Object)
		controllerutil.RemoveFinalizer(object, ownerFinalizer)
		if err := writer.Patch(ctx, object,
			client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); err != nil &&
			!apierrors.IsNotFound(err) {
			return false, fmt.Errorf("remove finalizer %q from NetworkAttachment %s: %w",
				ownerFinalizer, key.Name, err)
		}
	}
	return false, nil
}
