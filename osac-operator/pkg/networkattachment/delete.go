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

// Package networkattachment holds shared hub NetworkAttachment helpers used by
// consumers (ComputeInstance, BareMetalInstance).
package networkattachment

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	opv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

// DeleteOwned deletes a hub NetworkAttachment and, once it is terminating with
// only ownerFinalizer left, strips that finalizer so the object can finish
// deletion. Reads via reader (typically an uncached APIReader). Returns true
// when the NetworkAttachment is gone.
func DeleteOwned(
	ctx context.Context,
	reader client.Reader,
	writer client.Writer,
	key client.ObjectKey,
	ownerFinalizer string,
) (bool, error) {
	na := &opv1alpha1.NetworkAttachment{}
	if err := reader.Get(ctx, key, na); err != nil {
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	}
	if na.DeletionTimestamp.IsZero() {
		if err := writer.Delete(ctx, na); err != nil && !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("delete NetworkAttachment %s: %w", key.Name, err)
		}
		return false, nil
	}
	if len(na.Finalizers) == 1 && na.Finalizers[0] == ownerFinalizer {
		base := na.DeepCopy()
		controllerutil.RemoveFinalizer(na, ownerFinalizer)
		if err := writer.Patch(ctx, na, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); err != nil && !apierrors.IsNotFound(err) {
			return false, err
		}
	}
	return false, nil
}
