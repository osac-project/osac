// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

// Package metal3 supplies omitted BMO observations for persistent test inventory.
// BMF and AWX own allocation, provisioning inputs, Secrets and power requests.
package metal3

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	metal3v1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	"k8s.io/apimachinery/pkg/api/equality"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

const rebootAnnotation = metal3v1.RebootAnnotationPrefix

// HostReconciler reports provisioning and power within the configured inventory namespace.
type HostReconciler struct {
	Client    client.Client
	APIReader client.Reader
	Namespace string
}

// Reconcile observes image deployment and separate power-off, acknowledgement and
// power-on steps. Suffixed reboot requests remain until their requesting client clears them.
func (r *HostReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if req.Namespace != r.Namespace {
		return ctrl.Result{}, nil
	}

	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	host := &metal3v1.BareMetalHost{}
	if err := r.APIReader.Get(ctx, req.NamespacedName, host); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if host.DeletionTimestamp != nil {
		return ctrl.Result{}, nil
	}

	if _, paused := host.Annotations[metal3v1.PausedAnnotation]; paused {
		return ctrl.Result{}, nil
	}

	next := host.DeepCopy()
	if !detachedStatus(host, next) {
		releasing := host.Status.Provisioning.Image.URL != "" &&
			(host.Spec.Image == nil || host.Spec.Image.URL != host.Status.Provisioning.Image.URL)
		if releasing || !host.Status.PoweredOn || host.Spec.DisablePowerOff {
			delete(next.Annotations, rebootAnnotation)
		}

		if !equality.Semantic.DeepEqual(host.Annotations, next.Annotations) {
			return ctrl.Result{}, r.Client.Patch(
				ctx,
				next,
				client.MergeFromWithOptions(host, client.MergeFromWithOptimisticLock{}),
			)
		}

		provisioningStatus(host, next)
		powerStatus(host, next)
	}

	if equality.Semantic.DeepEqual(host.Status, next.Status) {
		return ctrl.Result{}, nil
	}

	// Publish the complete status, including the zero-valued fields required by the API.
	return ctrl.Result{}, r.Client.Status().Update(ctx, next)
}

// detachedStatus observes provisioner detachment before power or image changes.
// Removing the annotation restores management on the following reconciliation.
func detachedStatus(host, next *metal3v1.BareMetalHost) bool {
	value, requested := host.Annotations[metal3v1.DetachedAnnotation]
	state := host.Status.Provisioning.State

	eligible := state == metal3v1.StateProvisioned || state == metal3v1.StateExternallyProvisioned ||
		state == metal3v1.StateReady || state == metal3v1.StateAvailable
	if requested && !eligible {
		var arguments metal3v1.DetachedAnnotationArguments

		if err := json.Unmarshal([]byte(value), &arguments); err == nil {
			eligible = arguments.Force && state != metal3v1.StateDeleting
		}
	}

	switch {
	case requested && eligible:
		next.Status.OperationalStatus = metal3v1.OperationalStatusDetached
	case requested:
		return false
	case host.Status.OperationalStatus == metal3v1.OperationalStatusDetached:
		next.Status.OperationalStatus = metal3v1.OperationalStatusOK
		if host.Status.ErrorType != "" && host.Status.ErrorType != metal3v1.DetachError {
			next.Status.OperationalStatus = metal3v1.OperationalStatusError
		}
	case host.Status.ErrorType != metal3v1.DetachError:
		return false
	}

	if host.Status.ErrorType == metal3v1.DetachError {
		next.Status.ErrorType, next.Status.ErrorMessage, next.Status.ErrorCount = "", "", 0
		if !requested {
			next.Status.OperationalStatus = metal3v1.OperationalStatusOK
		}
	}

	return true
}

// powerStatus observes the requested power state and native reboot arguments.
// The hardware backend completes either soft or hard power-off immediately.
func powerStatus(host, next *metal3v1.BareMetalHost) {
	next.Status.PoweredOn = host.Spec.Online
	provisioned := next.Status.Provisioning.State == metal3v1.StateProvisioned ||
		next.Status.Provisioning.State == metal3v1.StateExternallyProvisioned

	for key, value := range host.Annotations {
		if key != rebootAnnotation && !strings.HasPrefix(key, rebootAnnotation+"/") {
			continue
		}

		var arguments metal3v1.RebootAnnotationArguments

		// Invalid values use BMO's default soft reboot with force disabled.
		if err := json.Unmarshal([]byte(value), &arguments); err != nil {
			arguments = metal3v1.RebootAnnotationArguments{}
		}

		if provisioned || arguments.Force {
			next.Status.PoweredOn = false
		}
	}

	if host.Spec.DisablePowerOff && host.Status.PoweredOn {
		next.Status.PoweredOn = true
	}
}

// provisioningStatus uses the deployed URL to decide release and ignores checksum
// edits on a deployed image. Release retains hardware RAID and clears installation settings.
func provisioningStatus(host, next *metal3v1.BareMetalHost) {
	deployed := host.Status.Provisioning.Image.URL
	requested := ""
	if host.Spec.Image != nil {
		requested = host.Spec.Image.URL
	}

	switch {
	case deployed != "" && deployed != requested:
		next.Status.Provisioning.Image = metal3v1.Image{}
		next.Status.Provisioning.CustomDeploy = nil

		next.Status.Provisioning.RootDeviceHints = nil
		next.Status.Provisioning.Firmware = nil
		if next.Status.Provisioning.RAID != nil {
			next.Status.Provisioning.RAID.SoftwareRAIDVolumes = nil
		}

		next.Status.Provisioning.State = metal3v1.StateAvailable
	case deployed == "" && requested != "" && host.Spec.Online:
		next.Status.Provisioning.Image = *host.Spec.Image.DeepCopy()
		next.Status.Provisioning.RootDeviceHints = host.Spec.RootDeviceHints.DeepCopy()
		//nolint:staticcheck // Match firmware preparation in the pinned BMO controller.
		next.Status.Provisioning.Firmware = host.Spec.Firmware.DeepCopy()
		next.Status.Provisioning.RAID = host.Spec.RAID.DeepCopy()
		next.Status.Provisioning.State = metal3v1.StateProvisioned
	case host.Status.Provisioning.State == metal3v1.StateNone && requested == "":
		next.Status.Provisioning.State = metal3v1.StateAvailable
	default:
		return
	}

	next.Status.OperationalStatus = metal3v1.OperationalStatusOK
}

// SetupWithManager watches BMH changes in the manager's inventory-namespace cache.
func (r *HostReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).Named("simulated-metal3-host").
		For(&metal3v1.BareMetalHost{}).WithOptions(kube.ControllerOptions()).Complete(r)
}
