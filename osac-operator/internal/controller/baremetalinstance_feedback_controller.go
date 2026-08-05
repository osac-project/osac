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

	"google.golang.org/grpc"
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

	bmfov1alpha1 "github.com/osac-project/bare-metal-fulfillment-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac-operator/internal/api/osac/private/v1"
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
func NewBareMetalInstanceFeedbackReconciler(hubClient clnt.Client, grpcConn *grpc.ClientConn, bareMetalInstanceNamespace string) *BareMetalInstanceFeedbackReconciler {
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
		if controllerutil.AddFinalizer(object, osacBareMetalInstanceFeedbackFinalizer) {
			if err = r.hubClient.Update(ctx, object); err != nil {
				return result, err
			}
		}
	} else {
		if !controllerutil.ContainsFinalizer(object, osacBareMetalInstanceFeedbackFinalizer) {
			return result, nil
		}
		if len(object.GetFinalizers()) > 1 {
			log.Info("Other finalizers still present, waiting", "finalizers", object.GetFinalizers())
			return result, nil
		}
		log.Info("Feedback finalizer is last remaining, removing finalizer and signaling", "bmiID", bmiID)
		if controllerutil.RemoveFinalizer(object, osacBareMetalInstanceFeedbackFinalizer) {
			if err = r.hubClient.Update(ctx, object); err != nil {
				return result, err
			}
		}
	}

	_, signalErr := r.bareMetalInstancesClient.Signal(ctx, privatev1.BareMetalInstancesSignalRequest_builder{
		Id: bmiID,
	}.Build())
	if signalErr != nil {
		log.Error(signalErr, "Failed to signal fulfillment service", "bmiID", bmiID)
		if object.DeletionTimestamp.IsZero() {
			return result, signalErr
		}
	}

	return result, nil
}
