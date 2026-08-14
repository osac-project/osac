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
	"errors"
	"fmt"

	"google.golang.org/grpc"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/osac-operator/internal/api/osac/private/v1"
	"github.com/osac-project/osac/osac-operator/internal/controller/feedback"
)

// VolumeFeedbackReconciler syncs Volume CR status from the hub cluster back
// to the fulfillment-service via the private Volumes gRPC API. It maps CRD
// phases to proto states and copies vendor-assigned fields (vendorVolumeID,
// backend, protocol) so the fulfillment-service inventory stays current.
type VolumeFeedbackReconciler struct {
	bridge          *feedback.Bridge[*v1alpha1.Volume, *privatev1.Volume]
	volumeNamespace string
}

// NewVolumeFeedbackReconciler creates a feedback reconciler that syncs Volume
// CR status to the fulfillment-service. The volumeNamespace controls which
// namespace this controller watches, matching the resource controller's scope.
func NewVolumeFeedbackReconciler(hubClient clnt.Client, grpcConn *grpc.ClientConn, volumeNamespace string) *VolumeFeedbackReconciler {
	if volumeNamespace == "" {
		volumeNamespace = defaultVolumeNamespace
	}
	volClient := privatev1.NewVolumesClient(grpcConn)
	r := &VolumeFeedbackReconciler{volumeNamespace: volumeNamespace}
	r.bridge = &feedback.Bridge[*v1alpha1.Volume, *privatev1.Volume]{
		Client:    hubClient,
		Finalizer: osacVolumeFeedbackFinalizer,
		IDLabel:   osacVolumeIDLabel,
		Kind:      "Volume",
		IDKey:     "volumeID",
		NewObject: func() *v1alpha1.Volume { return &v1alpha1.Volume{} },
		Fetch: func(ctx context.Context, id string) (*privatev1.Volume, error) {
			response, err := volClient.Get(ctx, privatev1.VolumesGetRequest_builder{Id: id}.Build())
			if err != nil {
				return nil, err
			}
			vol := response.GetObject()
			if vol == nil {
				return nil, errors.New("volume response contained nil object")
			}
			if !vol.HasSpec() {
				vol.SetSpec(&privatev1.VolumeSpec{})
			}
			if !vol.HasStatus() {
				vol.SetStatus(&privatev1.VolumeStatus{})
			}
			return vol, nil
		},
		Save: func(ctx context.Context, remote *privatev1.Volume) error {
			_, err := volClient.Update(ctx, privatev1.VolumesUpdateRequest_builder{
				Object: remote,
			}.Build())
			return err
		},
		Signal: func(ctx context.Context, id string) error {
			_, err := volClient.Signal(ctx, privatev1.VolumesSignalRequest_builder{
				Id: id,
			}.Build())
			return err
		},
		SyncUpdate: syncVolumeUpdate,
		SyncDelete: syncVolumeDelete,
	}
	return r
}

// SetupWithManager registers the feedback controller with the manager. It
// watches Volume CRs in the configured namespace on the local (hub) cluster.
func (r *VolumeFeedbackReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	localMgr := mgr.GetLocalManager()
	if localMgr == nil {
		return fmt.Errorf("local manager is nil")
	}

	return ctrl.NewControllerManagedBy(localMgr).
		Named("volume-feedback").
		For(&v1alpha1.Volume{}, builder.WithPredicates(VolumeNamespacePredicate(r.volumeNamespace))).
		Complete(r)
}

// Reconcile delegates to the shared feedback Bridge which handles the
// finalizer lifecycle, clone-compare-save, and last-finalizer Signal.
func (r *VolumeFeedbackReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	return r.bridge.Reconcile(ctx, request)
}

// syncVolumeUpdate maps Volume CR status to the fulfillment-service proto on
// the non-delete path. It syncs the phase, vendor-assigned identifiers, and
// the PVC/PV references that the operator populates after provisioning.
func syncVolumeUpdate(ctx context.Context, obj *v1alpha1.Volume, remote *privatev1.Volume) error {
	syncVolumePhase(ctx, obj, remote)
	syncVolumeVendorFields(obj, remote)
	return nil
}

// syncVolumeDelete maps Volume CR status during deletion. Failed volumes
// report FAILED; all other deletion states report DELETING.
func syncVolumeDelete(_ context.Context, obj *v1alpha1.Volume, remote *privatev1.Volume) error {
	if obj.Status.Phase == v1alpha1.VolumePhaseFailed {
		remote.GetStatus().SetState(privatev1.VolumeState_VOLUME_STATE_FAILED)
		return nil
	}
	remote.GetStatus().SetState(privatev1.VolumeState_VOLUME_STATE_DELETING)
	return nil
}

// syncVolumePhase converts the CRD phase to the proto state enum.
//
// CRD Phase      -> Proto State
// Progressing    -> CREATING   (volume is being provisioned on vendor array)
// Ready          -> AVAILABLE  (vendor provisioned, ready for use)
// Failed         -> FAILED     (vendor provisioning failed)
// Deleting       -> DELETING   (volume is being deprovisioned)
func syncVolumePhase(ctx context.Context, obj *v1alpha1.Volume, remote *privatev1.Volume) {
	switch obj.Status.Phase {
	case v1alpha1.VolumePhaseProgressing:
		remote.GetStatus().SetState(privatev1.VolumeState_VOLUME_STATE_CREATING)
	case v1alpha1.VolumePhaseReady:
		remote.GetStatus().SetState(privatev1.VolumeState_VOLUME_STATE_AVAILABLE)
	case v1alpha1.VolumePhaseFailed:
		remote.GetStatus().SetState(privatev1.VolumeState_VOLUME_STATE_FAILED)
	case v1alpha1.VolumePhaseDeleting:
		remote.GetStatus().SetState(privatev1.VolumeState_VOLUME_STATE_DELETING)
	default:
		log := ctrllog.FromContext(ctx)
		log.Info("Unknown phase, will ignore it", "phase", obj.Status.Phase)
	}
}

// syncVolumeVendorFields copies the vendor-assigned identifiers from the CR
// status to the proto status so the fulfillment-service inventory reflects
// the actual storage array state.
func syncVolumeVendorFields(obj *v1alpha1.Volume, remote *privatev1.Volume) {
	if obj.Status.VendorVolumeID != "" {
		remote.GetStatus().SetVendorVolumeId(obj.Status.VendorVolumeID)
	}
	if obj.Status.Backend != "" {
		remote.GetStatus().SetBackend(obj.Status.Backend)
	}
	if obj.Status.Protocol != "" {
		remote.GetStatus().SetProtocol(crdProtocolToProto(obj.Status.Protocol))
	}
}

// crdProtocolToProto converts the CRD VolumeProtocol typed string (e.g.
// "Block", "NFS") to the proto StorageProtocol enum. A direct map lookup
// would fail because the proto keys are "STORAGE_PROTOCOL_BLOCK", not "Block".
func crdProtocolToProto(protocol v1alpha1.VolumeProtocol) privatev1.StorageProtocol {
	switch protocol {
	case v1alpha1.VolumeProtocolBlock:
		return privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK
	case v1alpha1.VolumeProtocolNFS:
		return privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS
	default:
		return privatev1.StorageProtocol_STORAGE_PROTOCOL_UNSPECIFIED
	}
}
