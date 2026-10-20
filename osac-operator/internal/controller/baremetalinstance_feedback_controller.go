/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	bmfov1alpha1 "github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// BareMetalInstanceFeedbackReconciler watches BareMetalInstance CRs and signals
// the fulfillment-service when their status changes.
type BareMetalInstanceFeedbackReconciler struct {
	hubClient                  clnt.Client
	bareMetalInstancesClient   privatev1.BareMetalInstancesClient
	bareMetalInstanceNamespace string
}

// NewBareMetalInstanceFeedbackReconciler creates a reconciler that signals the
// fulfillment-service when BareMetalInstance CRs change.
func NewBareMetalInstanceFeedbackReconciler(hubClient clnt.Client, grpcConn grpc.ClientConnInterface, bareMetalInstanceNamespace string) *BareMetalInstanceFeedbackReconciler {
	return &BareMetalInstanceFeedbackReconciler{
		hubClient:                  hubClient,
		bareMetalInstancesClient:   privatev1.NewBareMetalInstancesClient(grpcConn),
		bareMetalInstanceNamespace: bareMetalInstanceNamespace,
	}
}

// BareMetalInstanceNamespacePredicate filters events to a specific namespace.
func BareMetalInstanceNamespacePredicate(namespace string) predicate.Predicate {
	return predicate.NewPredicateFuncs(
		func(obj clnt.Object) bool {
			return obj.GetNamespace() == namespace
		},
	)
}

// bareMetalInstanceStatusChangedPredicate filters Update events to only those
// where the BareMetalInstance status has changed, avoiding unnecessary Signal
// calls for metadata-only or spec-only changes. Create and Delete events are
// always passed through.
func bareMetalInstanceStatusChangedPredicate() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldObj, oldOk := e.ObjectOld.(*bmfov1alpha1.BareMetalInstance)
			newObj, newOk := e.ObjectNew.(*bmfov1alpha1.BareMetalInstance)
			if !oldOk || !newOk {
				return true
			}
			if !newObj.DeletionTimestamp.IsZero() {
				return true
			}
			return !equality.Semantic.DeepEqual(oldObj.Status, newObj.Status)
		},
	}
}

// SetupWithManager adds the reconciler to the controller manager.
func (r *BareMetalInstanceFeedbackReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	localMgr := mgr.GetLocalManager()
	if localMgr == nil {
		return fmt.Errorf("local manager is nil")
	}

	return ctrl.NewControllerManagedBy(localMgr).
		Named("baremetalinstance-feedback").
		For(&bmfov1alpha1.BareMetalInstance{}, builder.WithPredicates(
			BareMetalInstanceNamespacePredicate(r.bareMetalInstanceNamespace),
			bareMetalInstanceStatusChangedPredicate(),
		)).
		Complete(r)
}

// +kubebuilder:rbac:groups=osac.openshift.io,resources=baremetalinstances,verbs=get;list;watch;update
// +kubebuilder:rbac:groups=osac.openshift.io,resources=baremetalinstances/finalizers,verbs=update

// Reconcile watches BareMetalInstance CRs and calls Signal RPC on changes.
func (r *BareMetalInstanceFeedbackReconciler) Reconcile(ctx context.Context, request ctrl.Request) (result ctrl.Result, err error) {
	log := ctrllog.FromContext(ctx)

	object := &bmfov1alpha1.BareMetalInstance{}
	err = r.hubClient.Get(ctx, request.NamespacedName, object)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return result, err
		}
		// The CR is physically gone. This is the only point at which the
		// fulfillment delete path releases its record, because it keys off the
		// hub CR being absent (not merely a deletion timestamp). The identifier
		// label is unreadable now, so recover the BMI ID from the request name
		// ("bmi-<id>") and signal fulfillment so it reconciles the missing CR and
		// drops its record. The name prefix guard keeps unrelated objects from
		// producing a signal.
		if bmiID, ok := bareMetalInstanceIDFromName(request.Name); ok {
			if signalErr := r.signalUnlessArchived(ctx, bmiID); signalErr != nil {
				log.Error(signalErr, "Failed to signal fulfillment service for deleted CR", "bmiID", bmiID)
				return result, signalErr
			}
			log.Info("Signaled fulfillment service for deleted CR", "bmiID", bmiID)
			return result, nil
		}
		log.Info("CR not found, nothing to do")
		return result, nil
	}

	bmiID, ok := object.Labels[osacBareMetalInstanceIDLabel]
	if !ok {
		if !object.DeletionTimestamp.IsZero() && controllerutil.ContainsFinalizer(object, osacBareMetalInstanceFeedbackFinalizer) {
			log.Info("CR without BMI ID label is being deleted, removing feedback finalizer")
			if controllerutil.RemoveFinalizer(object, osacBareMetalInstanceFeedbackFinalizer) {
				err = r.hubClient.Update(ctx, object)
			}
			return result, err
		}
		log.Info(
			"There is no label containing the bare metal instance identifier, will ignore it",
			"label", osacBareMetalInstanceIDLabel,
		)
		return result, nil
	}

	if object.DeletionTimestamp.IsZero() {
		// Creation/update path: ensure the finalizer is present, then signal the
		// current state. A signal failure here is retried via the returned error.
		if controllerutil.AddFinalizer(object, osacBareMetalInstanceFeedbackFinalizer) {
			if err = r.hubClient.Update(ctx, object); err != nil {
				return result, err
			}
		}
		if signalErr := r.signal(ctx, bmiID); signalErr != nil {
			log.Error(signalErr, "Failed to signal fulfillment service", "bmiID", bmiID)
			return result, signalErr
		}
		return result, nil
	}

	// Deletion path.
	//
	// The fulfillment service releases its record only when the hub CR is
	// physically absent: its delete reconcile lists the CR by identifier label
	// and acts solely on nil, so a deletion timestamp while the CR still exists
	// just logs "waiting for K8s finalizers" and makes no progress. The decisive
	// signal is therefore the one sent AFTER the CR is gone, emitted from the
	// NotFound branch above once the last finalizer is released. Here we remove
	// the feedback finalizer before signaling (matching the generic feedback
	// bridge) and keep the removal update retryable so a conflict requeues with
	// the finalizer still pinning the CR rather than stranding the deletion.
	if !controllerutil.ContainsFinalizer(object, osacBareMetalInstanceFeedbackFinalizer) {
		// The feedback finalizer was never durably added (e.g. a create/delete
		// race whose AddFinalizer update kept conflicting). A foreign finalizer
		// may still pin the CR, so a signal now cannot clean up the record yet;
		// retry on a non-NotFound error and rely on the NotFound branch to
		// deliver the decisive signal once the CR is physically gone.
		if signalErr := r.signalUnlessArchived(ctx, bmiID); signalErr != nil {
			log.Error(signalErr, "Failed to signal fulfillment service for delete without feedback finalizer", "bmiID", bmiID)
			return result, signalErr
		}
		return result, nil
	}

	if len(object.GetFinalizers()) > 1 {
		log.Info("Other finalizers still present, waiting", "finalizers", object.GetFinalizers())
		return result, nil
	}

	log.Info("Feedback finalizer is last remaining, removing finalizer and signaling", "bmiID", bmiID)
	// Remove the finalizer first. Because it is the last one, the update
	// physically deletes the CR, after which fulfillment can observe the CR as
	// absent and release its record. Return update errors so a conflict requeues
	// before anything is lost.
	if controllerutil.RemoveFinalizer(object, osacBareMetalInstanceFeedbackFinalizer) {
		if err = r.hubClient.Update(ctx, object); err != nil {
			return result, err
		}
	}
	// Signal after the CR is gone. A gRPC NotFound means fulfillment already
	// archived the record; any other error is returned so the follow-up reconcile
	// (which hits the NotFound branch and re-signals) retries until it lands.
	if signalErr := r.signalUnlessArchived(ctx, bmiID); signalErr != nil {
		log.Error(signalErr, "Failed to signal fulfillment service on deletion", "bmiID", bmiID)
		return result, signalErr
	}

	return result, nil
}

// signal notifies the fulfillment service that the BareMetalInstance identified
// by bmiID has changed (or been deleted), prompting it to resync its record.
func (r *BareMetalInstanceFeedbackReconciler) signal(ctx context.Context, bmiID string) error {
	_, err := r.bareMetalInstancesClient.Signal(ctx, privatev1.BareMetalInstancesSignalRequest_builder{
		Id: bmiID,
	}.Build())
	return err
}

// signalUnlessArchived signals fulfillment for bmiID, treating a gRPC NotFound
// response as success because it means the record is already archived. Any other
// error is returned so the caller can requeue and retry.
func (r *BareMetalInstanceFeedbackReconciler) signalUnlessArchived(ctx context.Context, bmiID string) error {
	if err := r.signal(ctx, bmiID); err != nil && grpcstatus.Code(err) != grpccodes.NotFound {
		return err
	}
	return nil
}

// bareMetalInstanceIDFromName recovers the fulfillment BMI identifier from a hub
// CR name of the form "bmi-<id>". It returns false for any name without that
// prefix, or with an empty identifier, so unrelated reconcile requests never
// trigger a signal.
func bareMetalInstanceIDFromName(name string) (string, bool) {
	id, ok := strings.CutPrefix(name, bareMetalInstanceObjectNamePrefix)
	if !ok || id == "" {
		return "", false
	}
	return id, true
}
