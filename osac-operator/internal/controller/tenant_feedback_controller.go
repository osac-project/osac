/*
Copyright (c) 2026 Red Hat Inc.

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

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// TenantFeedbackReconciler signals fulfillment-service when Tenant CR status changes.
type TenantFeedbackReconciler struct {
	hubClient       clnt.Client
	tenantsClient   privatev1.TenantsClient
	tenantNamespace string
}

// NewTenantFeedbackReconciler creates a Tenant feedback reconciler.
func NewTenantFeedbackReconciler(hubClient clnt.Client, grpcConn grpc.ClientConnInterface, tenantNamespace string) *TenantFeedbackReconciler {
	return &TenantFeedbackReconciler{
		hubClient:       hubClient,
		tenantsClient:   privatev1.NewTenantsClient(grpcConn),
		tenantNamespace: tenantNamespace,
	}
}

// tenantStatusChangedPredicate triggers feedback for status, deletion, or ID-label changes.
func tenantStatusChangedPredicate() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(update event.UpdateEvent) bool {
			oldObject, oldOK := update.ObjectOld.(*v1alpha1.Tenant)
			newObject, newOK := update.ObjectNew.(*v1alpha1.Tenant)
			if !oldOK || !newOK {
				return true
			}
			return !equality.Semantic.DeepEqual(oldObject.Status, newObject.Status) ||
				!equality.Semantic.DeepEqual(oldObject.DeletionTimestamp, newObject.DeletionTimestamp) ||
				oldObject.GetLabels()[osacTenantIDLabel] != newObject.GetLabels()[osacTenantIDLabel]
		},
	}
}

// SetupWithManager registers the Tenant status watch.
func (r *TenantFeedbackReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	localManager := mgr.GetLocalManager()
	if localManager == nil {
		return fmt.Errorf("local manager is nil")
	}

	return ctrl.NewControllerManagedBy(localManager).
		Named("tenant-feedback").
		For(&v1alpha1.Tenant{}, builder.WithPredicates(
			tenantNamespacePredicate(r.tenantNamespace),
			tenantStatusChangedPredicate(),
		)).
		Complete(r)
}

// Reconcile signals the fulfillment tenant associated with the changed CR.
func (r *TenantFeedbackReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	object := &v1alpha1.Tenant{}
	if err := r.hubClient.Get(ctx, request.NamespacedName, object); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	tenantID := object.GetLabels()[osacTenantIDLabel]
	if tenantID == "" {
		if !object.GetDeletionTimestamp().IsZero() && controllerutil.ContainsFinalizer(object, osacTenantFeedbackFinalizer) {
			controllerutil.RemoveFinalizer(object, osacTenantFeedbackFinalizer)
			return ctrl.Result{}, r.hubClient.Update(ctx, object)
		}
		ctrllog.FromContext(ctx).Info("Tenant CR has no fulfillment identifier", "label", osacTenantIDLabel)
		return ctrl.Result{}, nil
	}

	if object.GetDeletionTimestamp().IsZero() {
		if controllerutil.AddFinalizer(object, osacTenantFeedbackFinalizer) {
			if err := r.hubClient.Update(ctx, object); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		if _, err := r.tenantsClient.Signal(ctx, privatev1.TenantsSignalRequest_builder{Id: tenantID}.Build()); err != nil {
			return ctrl.Result{}, err
		}
		if controllerutil.RemoveFinalizer(object, osacTenantFeedbackFinalizer) {
			return ctrl.Result{}, r.hubClient.Update(ctx, object)
		}
		return ctrl.Result{}, nil
	}

	_, err := r.tenantsClient.Signal(ctx, privatev1.TenantsSignalRequest_builder{Id: tenantID}.Build())
	return ctrl.Result{}, err
}
