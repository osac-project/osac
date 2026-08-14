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

	"k8s.io/apimachinery/pkg/api/equality"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	controllerutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

// VendorProvisioner abstracts vendor storage array operations. Unlike other
// OSAC resources that provision through AAP (RunProvisioningLifecycle), volumes
// are provisioned by calling the vendor CSI controller directly. This interface
// decouples the controller from the vendor implementation, allowing a mock for
// testing and development while the real vendor CSI client is wired in PR #3.
type VendorProvisioner interface {
	CreateVolume(ctx context.Context, req VendorCreateVolumeRequest) (VendorCreateVolumeResponse, error)
	DeleteVolume(ctx context.Context, req VendorDeleteVolumeRequest) error
}

// VendorCreateVolumeRequest carries the parameters the vendor needs to
// provision a volume. Backend is resolved by the fulfillment-service tier
// resolution (OSAC-3277) before the Volume CR is created; the operator
// passes it through to the vendor without re-resolving.
type VendorCreateVolumeRequest struct {
	Name       string
	Backend    string
	SizeGiB    int64
	AccessMode v1alpha1.VolumeAccessMode
}

// VendorCreateVolumeResponse carries the vendor-assigned identifiers that the
// feedback controller syncs back to the fulfillment-service inventory.
type VendorCreateVolumeResponse struct {
	VendorVolumeID string
	Backend        string
	Protocol       string
}

// VendorDeleteVolumeRequest identifies the vendor volume to deprovision.
type VendorDeleteVolumeRequest struct {
	VendorVolumeID string
	Backend        string
}

// VolumeReconciler reconciles Volume CRs created by the fulfillment-service
// reconciler. It calls the VendorProvisioner to create volumes on the backend
// storage array and updates the CR status with vendor-assigned identifiers.
// The feedback controller then syncs that status back to fulfillment-service.
//
// Unlike networking and compute controllers that use AAP for provisioning,
// this controller calls the vendor CSI directly because storage provisioning
// is a synchronous gRPC call, not an asynchronous job.
type VolumeReconciler struct {
	client.Client
	Scheme            *runtime.Scheme
	mgr               mcmanager.Manager
	VolumeNamespace   string
	VendorProvisioner VendorProvisioner
}

// NewVolumeReconciler creates a new reconciler for Volume resources. The
// volumeNamespace controls which namespace the controller watches; in
// production this is set via OSAC_VOLUME_NAMESPACE (same as the Helm release
// namespace), defaulting to "osac-volume" for local development.
func NewVolumeReconciler(
	mgr mcmanager.Manager,
	volumeNamespace string,
	vendorProvisioner VendorProvisioner,
) *VolumeReconciler {
	if mgr == nil {
		panic("mgr must not be nil")
	}
	if volumeNamespace == "" {
		volumeNamespace = defaultVolumeNamespace
	}
	return &VolumeReconciler{
		Client:            mgr.GetLocalManager().GetClient(),
		Scheme:            mgr.GetLocalManager().GetScheme(),
		mgr:               mgr,
		VolumeNamespace:   volumeNamespace,
		VendorProvisioner: vendorProvisioner,
	}
}

// +kubebuilder:rbac:groups=osac.openshift.io,resources=volumes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=osac.openshift.io,resources=volumes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=osac.openshift.io,resources=volumes/finalizers,verbs=update

// Reconcile is part of the main Kubernetes reconciliation loop. It drives
// Volume CRs through the provisioning lifecycle: Progressing -> Ready (on
// success) or Failed (on vendor error), and handles deletion by calling
// the vendor to deprovision before removing the finalizer.
func (r *VolumeReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	vol := &v1alpha1.Volume{}
	if err := r.Get(ctx, req.NamespacedName, vol); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	log.Info("start reconcile")

	oldstatus := vol.Status.DeepCopy()

	var res ctrl.Result
	var err error
	if vol.ObjectMeta.DeletionTimestamp.IsZero() {
		res, err = r.handleUpdate(ctx, vol)
	} else {
		res, err = r.handleDelete(ctx, vol)
	}

	if !equality.Semantic.DeepEqual(vol.Status, *oldstatus) {
		log.Info("status requires update")
		if err := r.Status().Update(ctx, vol); err != nil {
			return res, err
		}
	}

	log.Info("end reconcile")
	return res, err
}

// handleUpdate runs on every non-deleted reconcile. It ensures the finalizer
// is present, sets the initial phase to Progressing, and delegates to
// handleProvisioning if the volume has not yet reached Ready.
func (r *VolumeReconciler) handleUpdate(ctx context.Context, vol *v1alpha1.Volume) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	if controllerutil.AddFinalizer(vol, osacVolumeFinalizer) {
		if err := r.Update(ctx, vol); err != nil {
			return ctrl.Result{}, err
		}
	}

	if vol.Status.Phase == "" {
		vol.Status.Phase = v1alpha1.VolumePhaseProgressing
	}

	if r.VendorProvisioner == nil {
		// Temporary: VendorProvisioner is nil until the real vendor CSI client
		// is wired in the CSI driver integration PR. Remove this guard once a
		// concrete implementation is always passed to NewVolumeReconciler.
		log.Info("no vendor provisioner configured, skipping provisioning")
		return ctrl.Result{}, nil
	}

	// Already provisioned; nothing to do until spec changes (future: resize).
	if vol.Status.Phase == v1alpha1.VolumePhaseReady {
		return ctrl.Result{}, nil
	}

	// Failed volumes are not auto-retried to avoid spamming the vendor API
	// with a persistent configuration error. The admin fixes the issue, then
	// a Signal or periodic sync triggers re-reconciliation. At that point the
	// phase is reset to Progressing below so provisioning is attempted again.
	if vol.Status.Phase == v1alpha1.VolumePhaseFailed {
		return ctrl.Result{}, nil
	}

	return r.handleProvisioning(ctx, vol)
}

// handleProvisioning calls the vendor CSI to create the volume on the backend
// array. On success it transitions the phase to Ready and sets the
// VendorProvisioned condition. On failure it transitions to Failed and returns
// nil (no retry) so the error is visible in the condition; the feedback
// controller will sync this state to the fulfillment-service.
func (r *VolumeReconciler) handleProvisioning(ctx context.Context, vol *v1alpha1.Volume) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	resp, err := r.VendorProvisioner.CreateVolume(ctx, VendorCreateVolumeRequest{
		Name:       vol.Name,
		Backend:    vol.Status.Backend,
		SizeGiB:    vol.Spec.SizeGiB,
		AccessMode: vol.Spec.AccessMode,
	})
	if err != nil {
		log.Error(err, "vendor provisioning failed")
		vol.Status.Phase = v1alpha1.VolumePhaseFailed
		setVendorProvisionedCondition(&vol.Status.Conditions, metav1.ConditionFalse, "ProvisioningFailed", err.Error())
		return ctrl.Result{}, nil
	}

	vol.Status.VendorVolumeID = resp.VendorVolumeID
	vol.Status.Backend = resp.Backend
	vol.Status.Protocol = v1alpha1.VolumeProtocol(resp.Protocol)
	vol.Status.Phase = v1alpha1.VolumePhaseReady
	setVendorProvisionedCondition(&vol.Status.Conditions, metav1.ConditionTrue, "Provisioned", "Volume provisioned on vendor storage array")

	log.Info("vendor provisioning succeeded",
		"vendorVolumeID", resp.VendorVolumeID,
		"backend", resp.Backend,
		"protocol", resp.Protocol,
	)

	return ctrl.Result{}, nil
}

// handleDelete runs when the Volume CR has a deletion timestamp. It calls the
// vendor to deprovision the volume from the backend array, then removes the
// resource controller's finalizer. If deprovisioning fails the error is
// returned so the reconciler retries on the next cycle.
func (r *VolumeReconciler) handleDelete(ctx context.Context, vol *v1alpha1.Volume) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)
	log.Info("deleting volume")

	vol.Status.Phase = v1alpha1.VolumePhaseDeleting

	if !controllerutil.ContainsFinalizer(vol, osacVolumeFinalizer) {
		return ctrl.Result{}, nil
	}

	// Only call vendor if there is a volume to delete; volumes that failed
	// before vendor provisioning have no VendorVolumeID.
	if r.VendorProvisioner != nil && vol.Status.VendorVolumeID != "" {
		err := r.VendorProvisioner.DeleteVolume(ctx, VendorDeleteVolumeRequest{
			VendorVolumeID: vol.Status.VendorVolumeID,
			Backend:        vol.Status.Backend,
		})
		if err != nil {
			log.Error(err, "vendor deprovisioning failed")
			return ctrl.Result{}, err
		}
		log.Info("vendor deprovisioning succeeded", "vendorVolumeID", vol.Status.VendorVolumeID)
	}

	if controllerutil.RemoveFinalizer(vol, osacVolumeFinalizer) {
		if err := r.Update(ctx, vol); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// VolumeNamespacePredicate filters events to only those in the configured
// volume namespace, preventing the controller from reacting to Volume CRs
// in other namespaces.
func VolumeNamespacePredicate(namespace string) predicate.Predicate {
	return predicate.NewPredicateFuncs(
		func(obj client.Object) bool {
			return obj.GetNamespace() == namespace
		},
	)
}

// SetupWithManager registers the Volume controller with the manager. It
// watches Volume CRs in the configured namespace on the local (hub) cluster.
func (r *VolumeReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	return mcbuilder.ControllerManagedBy(mgr).
		For(&v1alpha1.Volume{},
			mcbuilder.WithPredicates(VolumeNamespacePredicate(r.VolumeNamespace)),
			mcbuilder.WithEngageWithLocalCluster(true),
			mcbuilder.WithEngageWithProviderClusters(false)).
		Complete(r)
}

// setVendorProvisionedCondition upserts the VendorProvisioned condition.
// Uses apimeta.SetStatusCondition which preserves LastTransitionTime when
// the status hasn't changed, but always updates Reason and Message so
// repeated failures reflect the latest error.
func setVendorProvisionedCondition(conditions *[]metav1.Condition, status metav1.ConditionStatus, reason, message string) {
	apimeta.SetStatusCondition(conditions, metav1.Condition{
		Type:    string(v1alpha1.VolumeConditionVendorProvisioned),
		Status:  status,
		Reason:  reason,
		Message: message,
	})
}
