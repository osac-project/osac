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

package controller

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	bmfov1alpha1 "github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
)

const bareMetalInstanceCleanupFinalizer = "osac.openshift.io/baremetalinstance-cleanup"

// BareMetalInstanceCleanupReconciler removes automatically created networking resources before a BareMetalInstance is deleted.
type BareMetalInstanceCleanupReconciler struct {
	client.Client
	bareMetalInstanceNamespace  string
	networkingNamespace         string
	pollInterval                time.Duration
	BareMetalInstancesClient    automaticBareMetalInstancesGetter
	ExternalIPAttachmentsClient automaticExternalIPAttachmentsClient
}

// NewBareMetalInstanceCleanupReconciler creates the networking cleanup controller.
func NewBareMetalInstanceCleanupReconciler(c client.Client, bareMetalInstanceNamespace, networkingNamespace string) *BareMetalInstanceCleanupReconciler {
	return &BareMetalInstanceCleanupReconciler{
		Client:                     c,
		bareMetalInstanceNamespace: bareMetalInstanceNamespace,
		networkingNamespace:        networkingNamespace,
		pollInterval:               5 * time.Second,
	}
}

// SetupWithManager registers the BareMetalInstance lifecycle watch.
func (r *BareMetalInstanceCleanupReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	localMgr := mgr.GetLocalManager()
	if localMgr == nil {
		return fmt.Errorf("local manager is nil")
	}
	return ctrl.NewControllerManagedBy(localMgr).
		Named("baremetalinstance-cleanup").
		For(&bmfov1alpha1.BareMetalInstance{}, builder.WithPredicates(BareMetalInstanceNamespacePredicate(r.bareMetalInstanceNamespace))).
		Complete(r)
}

// +kubebuilder:rbac:groups=osac.openshift.io,resources=baremetalinstances,verbs=get;list;watch;update
// +kubebuilder:rbac:groups=osac.openshift.io,resources=baremetalinstances/finalizers,verbs=update
// +kubebuilder:rbac:groups=osac.openshift.io,resources=externalips,verbs=get;list;delete
// +kubebuilder:rbac:groups=osac.openshift.io,resources=externalipattachments,verbs=get;list;delete

// Reconcile guards BareMetalInstance deletion until its automatically created networking resources are gone.
func (r *BareMetalInstanceCleanupReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	if request.Namespace != r.bareMetalInstanceNamespace {
		return ctrl.Result{}, nil
	}
	bmi := &bmfov1alpha1.BareMetalInstance{}
	if err := r.Get(ctx, request.NamespacedName, bmi); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if bmi.DeletionTimestamp.IsZero() && bmi.Annotations[osacManagementStateAnnotation] == ManagementStateUnmanaged {
		return ctrl.Result{}, nil
	}
	id := bmi.Labels[osacBareMetalInstanceIDLabel]
	if bmi.DeletionTimestamp.IsZero() {
		if id == "" || r.networkingNamespace == "" {
			return ctrl.Result{}, nil
		}
		if controllerutil.AddFinalizer(bmi, bareMetalInstanceCleanupFinalizer) {
			return ctrl.Result{}, r.Update(ctx, bmi)
		}
		return r.reconcileAutomaticExternalIPAttachments(ctx, bmi, id)
	}
	if !controllerutil.ContainsFinalizer(bmi, bareMetalInstanceCleanupFinalizer) {
		return ctrl.Result{}, nil
	}
	if bmi.Annotations[osacManagementStateAnnotation] == ManagementStateUnmanaged {
		controllerutil.RemoveFinalizer(bmi, bareMetalInstanceCleanupFinalizer)
		return ctrl.Result{}, r.Update(ctx, bmi)
	}
	done, result, err := reconcileAutoExternalIPCleanup(ctx, r.Client, r.networkingNamespace,
		autoExternalIPOwner{kind: bareMetalInstanceOwner, id: id}, r.pollInterval)
	if err != nil || !done {
		return result, err
	}
	controllerutil.RemoveFinalizer(bmi, bareMetalInstanceCleanupFinalizer)
	return ctrl.Result{}, r.Update(ctx, bmi)
}

func (r *BareMetalInstanceCleanupReconciler) reconcileAutomaticExternalIPAttachments(
	ctx context.Context,
	bmi *bmfov1alpha1.BareMetalInstance,
	bmiID string,
) (ctrl.Result, error) {
	if bmi.Status.Phase != bmfov1alpha1.BareMetalInstancePhaseReady || !hasPrimaryBareMetalInstanceAddress(bmi) ||
		r.BareMetalInstancesClient == nil || r.ExternalIPAttachmentsClient == nil {
		return ctrl.Result{}, nil
	}
	response, err := r.BareMetalInstancesClient.Get(ctx,
		privatev1.BareMetalInstancesGetRequest_builder{Id: bmiID}.Build())
	if status.Code(err) == codes.NotFound {
		return ctrl.Result{}, nil
	}
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("get Fulfillment BareMetalInstance %q for automatic ExternalIPAttachment: %w", bmiID, err)
	}
	instance := response.GetObject()
	if instance == nil || !instance.GetSpec().GetAutoExternalIpAttachment() {
		return ctrl.Result{}, nil
	}

	target := automaticExternalIPAttachmentTarget{
		owner:     autoExternalIPOwner{kind: bareMetalInstanceOwner, id: bmiID},
		kind:      bareMetalInstanceOwner,
		kindLabel: autoExternalIPKindLabel(bareMetalInstanceOwner),
		tenant:    instance.GetMetadata().GetTenant(),
		endpoints: []string{""},
		availableEndpoints: map[string]bool{
			"": true,
		},
	}
	return reconcileAutomaticExternalIPAttachments(ctx, r.Client, r.networkingNamespace,
		r.ExternalIPAttachmentsClient, r.pollInterval, target)
}

func hasPrimaryBareMetalInstanceAddress(bmi *bmfov1alpha1.BareMetalInstance) bool {
	for _, attachment := range bmi.Status.NetworkAttachmentStatuses {
		if attachment.Primary && attachment.IPAddress != "" {
			return true
		}
	}
	return false
}
