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

// Package networkattachment contains helpers for managing
// NetworkAttachment resources.
package networkattachment

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	v1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

// DeleteOwned requests deletion and removes ownerFinalizer from a terminating
// attachment only when it is the sole remaining finalizer. It returns true
// when a read confirms that the attachment is absent.
func DeleteOwned(
	ctx context.Context,
	reader client.Reader,
	writer client.Writer,
	key client.ObjectKey,
	ownerFinalizer string,
) (bool, error) {
	attachment := &v1alpha1.NetworkAttachment{}
	if err := reader.Get(ctx, key, attachment); err != nil {
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	}

	if attachment.DeletionTimestamp.IsZero() {
		if err := writer.Delete(ctx, attachment); err != nil && !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("delete NetworkAttachment %s: %w", key.Name, err)
		}
		return false, nil
	}

	if len(attachment.Finalizers) == 1 && attachment.Finalizers[0] == ownerFinalizer {
		base := attachment.DeepCopy()
		controllerutil.RemoveFinalizer(attachment, ownerFinalizer)
		patch := client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})
		if err := writer.Patch(ctx, attachment, patch); err != nil && !apierrors.IsNotFound(err) {
			return false, err
		}
	}

	return false, nil
}
