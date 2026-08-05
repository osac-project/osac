/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

// Package feedback provides shared reconciliation infrastructure for feedback controllers.
package feedback

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	ctrl "sigs.k8s.io/controller-runtime"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
)

// Bridge implements the shared feedback reconciliation policy for all feedback
// controllers. Each resource supplies type-specific callbacks (Fetch, Save,
// Signal, SyncUpdate, SyncDelete); the Bridge owns the finalizer lifecycle,
// remote-NotFound handling, clone-compare-save, and last-finalizer Signal.
//
// O is the Kubernetes CR type (e.g. *v1alpha1.Subnet).
// R is the remote proto type (e.g. *privatev1.Subnet).
type Bridge[O clnt.Object, R proto.Message] struct {
	Client    clnt.Client
	Finalizer string
	IDLabel   string

	// Kind and IDKey are used only for log messages (e.g. "Subnet", "subnetID").
	Kind  string
	IDKey string

	NewObject func() O

	// Fetch retrieves the remote record by ID. The implementation should
	// initialise empty Spec/Status sub-messages if needed.
	Fetch func(ctx context.Context, id string) (R, error)

	// Save persists an updated remote record. Called only when the record
	// has changed (the Bridge does the proto.Equal comparison).
	Save func(ctx context.Context, remote R) error

	// Signal notifies the fulfillment service after the last finalizer is
	// removed. Errors are logged but do not fail the reconcile.
	Signal func(ctx context.Context, id string) error

	// SyncUpdate maps CR state to the remote record on the non-delete path.
	// The Bridge adds the finalizer before calling this.
	SyncUpdate func(ctx context.Context, obj O, remote R) error

	// SyncDelete maps CR state to the remote record on the delete path.
	SyncDelete func(ctx context.Context, obj O, remote R) error

	// PostSaveOnDelete is an optional callback invoked after Save completes
	// on the delete path, before finalizer removal. Use it for cross-resource
	// side effects that must happen after this resource's state is persisted
	// (e.g. clearing a parent resource's "attached" flag). The remote record
	// is provided for callbacks that need to reference it (e.g. reading a
	// parent resource ID from the proto spec).
	// If nil, this step is skipped.
	PostSaveOnDelete func(ctx context.Context, obj O, remote R) error

	// IsNotFound determines whether a Fetch error means the remote record
	// does not exist. If nil, defaults to gRPC codes.NotFound.
	IsNotFound func(error) bool
}

func (b *Bridge[O, R]) isNotFound(err error) bool {
	if b.IsNotFound != nil {
		return b.IsNotFound(err)
	}
	return status.Code(err) == codes.NotFound
}

// Reconcile implements the 6-step feedback reconciliation policy.
func (b *Bridge[O, R]) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)
	result := ctrl.Result{}

	// Fetch the CR. Nothing to do if it no longer exists.
	object := b.NewObject()
	if err := b.Client.Get(ctx, req.NamespacedName, object); err != nil {
		return result, clnt.IgnoreNotFound(err)
	}

	// Get the resource ID from labels. Without it the CR was not
	// created by the fulfillment service. Remove our finalizer if the CR
	// is being deleted; otherwise ignore.
	id, ok := object.GetLabels()[b.IDLabel]
	if !ok {
		if !object.GetDeletionTimestamp().IsZero() && controllerutil.ContainsFinalizer(object, b.Finalizer) {
			log.Info("CR without ID label is being deleted, removing feedback finalizer",
				"kind", b.Kind)
			if controllerutil.RemoveFinalizer(object, b.Finalizer) {
				return result, b.Client.Update(ctx, object)
			}
		}
		log.Info("No label containing the resource identifier, ignoring",
			"kind", b.Kind, "label", b.IDLabel)
		return result, nil
	}

	// Fetch the remote record from the fulfillment service.
	remote, err := b.Fetch(ctx, id)
	if err != nil {
		if !object.GetDeletionTimestamp().IsZero() && b.isNotFound(err) {
			log.Info("Remote record not found during deletion, removing feedback finalizer",
				"kind", b.Kind, b.IDKey, id)
			if controllerutil.RemoveFinalizer(object, b.Finalizer) {
				return result, b.Client.Update(ctx, object)
			}
			return result, nil
		}
		return result, err
	}

	// Clone the remote record so we can detect changes after sync.
	before := proto.Clone(remote).(R)

	// Sync CR state to the remote record.
	if object.GetDeletionTimestamp().IsZero() {
		if controllerutil.AddFinalizer(object, b.Finalizer) {
			if err := b.Client.Update(ctx, object); err != nil {
				return result, err
			}
		}
		if err := b.SyncUpdate(ctx, object, remote); err != nil {
			return result, err
		}
	} else {
		if err := b.SyncDelete(ctx, object, remote); err != nil {
			return result, err
		}
	}

	// Persist changes only if the remote record was modified.
	if !proto.Equal(before, remote) {
		log.Info("Updating remote record", "kind", b.Kind, b.IDKey, id)
		if err := b.Save(ctx, remote); err != nil {
			return result, err
		}
	}

	// Run post-save side effects on the delete path (e.g. cross-resource cleanup).
	if !object.GetDeletionTimestamp().IsZero() && b.PostSaveOnDelete != nil {
		if err := b.PostSaveOnDelete(ctx, object, remote); err != nil {
			return result, err
		}
	}

	// Handle finalizer removal and Signal for deletions.
	if !object.GetDeletionTimestamp().IsZero() && controllerutil.ContainsFinalizer(object, b.Finalizer) {
		if len(object.GetFinalizers()) == 1 {
			log.Info("Feedback finalizer is last remaining, removing finalizer and signaling",
				"kind", b.Kind, b.IDKey, id)
			if controllerutil.RemoveFinalizer(object, b.Finalizer) {
				if err := b.Client.Update(ctx, object); err != nil {
					return result, err
				}
			}
			if signalErr := b.Signal(ctx, id); signalErr != nil {
				log.Error(signalErr,
					"Failed to signal fulfillment service, periodic sync will handle cleanup",
					b.IDKey, id)
			}
		} else {
			log.Info("Other finalizers still present, waiting",
				"finalizers", object.GetFinalizers())
		}
	}

	return result, nil
}
