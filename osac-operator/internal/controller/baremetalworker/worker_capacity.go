// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package baremetalworker

import (
	"context"
	"fmt"
	"reflect"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const eventReasonWorkerCreated = "WorkerCreated"

// reconcileWorkerCapacity persists reservations before creating infrastructure.
// Each invocation ends at its first durable allocation/lifecycle boundary.
func (r *Reconciler) reconcileWorkerCapacity(
	ctx context.Context, co *v1alpha1.ClusterOrder, tenant string, image *privatev1.DiskImageReference, ignition []byte,
	observations ...*workerObservation,
) (ctrl.Result, error) {
	// Standalone callers also plan from authoritative status. A supplied resource
	// observation belongs to the original snapshot and must not absorb new slots.
	if len(observations) > 0 && observations[0] != nil {
		if err := r.checkCurrentCapacityPlan(ctx, co); err != nil {
			return ctrl.Result{}, err
		}
	} else if err := r.refreshWorkerOrder(ctx, co); err != nil {
		return ctrl.Result{}, err
	}
	filter := fmt.Sprintf(`this.metadata.labels["%s"] == "%s"`, clusterOrderLabel, co.Name)
	observed, res, err := r.workerCapacityObservation(ctx, co, filter, observations...)
	if err != nil || !res.IsZero() {
		return res, err
	}
	existingByName, err := observed.uniqueBMIsByName()
	if err != nil {
		return ctrl.Result{}, r.rejectWorkerIdentity(co, err.Error())
	}
	prepared := co.DeepCopy()
	added, err := r.reserveWorkerSlots(ctx, co)
	if err != nil {
		return ctrl.Result{}, err
	}
	if added {
		return workerBoundaryRequeue(), nil
	}
	if !sameWorkerOrder(prepared, co) || prepared.Generation != co.Generation || !reflect.DeepEqual(prepared.Spec, co.Spec) || !reflect.DeepEqual(prepared.Status.Workers, co.Status.Workers) {
		return ctrl.Result{}, errWorkerObservationChanged
	}
	if err := r.checkCurrentCapacityPlan(ctx, co); err != nil {
		return ctrl.Result{}, err
	}
	failed := co.DeepCopy()
	if err := r.handleFailedWorkers(ctx, failed); err != nil {
		// Publish failure summaries without advancing to another external action.
		if statusErr := r.updateWorkerStatusWithAgent(ctx, co, co.Status.Workers); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{}, err
	}
	if !workerSlicesEqual(co.Status.Workers, failed.Status.Workers) {
		if err := r.updateWorkerStatus(ctx, co, failed.Status.Workers); err != nil {
			return ctrl.Result{}, err
		}
		return workerBoundaryRequeue(), nil
	}

	workers, res, err := r.reconcileNodeSets(ctx, co, tenant, existingByName, image, string(ignition), filter, observed)
	if err != nil || !res.IsZero() {
		return res, err
	}
	if len(workerDifferences(co.Status.Workers, workers)) > 0 {
		if err := r.updateWorkerStatus(ctx, co, workers); err != nil {
			return ctrl.Result{}, err
		}
		return workerBoundaryRequeue(), nil
	}
	if apimeta.IsStatusConditionTrue(co.Status.Conditions, v1alpha1.ConditionFulfillmentServiceUnavailable) {
		if err := r.setFulfillmentServiceUnavailable(ctx, co, metav1.ConditionFalse,
			reasonFulfillmentAvailable, "fulfillment service recovered"); err != nil {
			return ctrl.Result{}, err
		}
	}
	return r.earliestRetryRequeue(workers), nil
}

// reconcileNodeSets selects at most one actionable slot in spec/retention order.
// Slots waiting for retry do not monopolize creation of other reserved workers.
func (r *Reconciler) reconcileNodeSets(
	ctx context.Context, co *v1alpha1.ClusterOrder, tenant string,
	existingByName map[string]*privatev1.BareMetalInstance,
	image *privatev1.DiskImageReference, ignitionRaw, filter string, observations ...*workerObservation,
) ([]v1alpha1.WorkerStatus, ctrl.Result, error) {
	plan := planWorkerSlots(co)
	for i := range co.Spec.NodeRequests {
		nr := &co.Spec.NodeRequests[i]
		if !nr.IsBareMetal() {
			continue
		}
		for _, prev := range plan.selected {
			if prev.NodeSet != nr.NodeSet || prev.BareMetalInstance.ID != "" || (prev.Phase == workerPhaseFailed && !isRetryDue(prev)) {
				continue
			}
			instanceType, res, err := r.resolveNodeSetInstanceType(ctx, co, nr.BareMetal.InstanceType)
			if err != nil || !res.IsZero() {
				return nil, res, err
			}
			var fabricInterface string
			if co.Spec.NetworkAttachment != nil && co.Spec.NetworkAttachment.SubnetRef != "" {
				fabricInterface, err = resolveFabricInterface(instanceType)
				if err != nil {
					return nil, ctrl.Result{}, err
				}
			}
			if err := r.checkCurrentCapacityPlan(ctx, co); err != nil {
				return nil, ctrl.Result{}, err
			}
			before := prev
			if prev.Phase == workerPhaseFailed {
				res, err = r.retryFailedWorker(ctx, co, tenant, nr, &prev, image, ignitionRaw, filter, fabricInterface, observations...)
			} else {
				var ws v1alpha1.WorkerStatus
				ws, res, err = r.ensureWorkerBMI(ctx, co, tenant, nr, prev.BareMetalInstance.Name, existingByName, image, ignitionRaw, filter, fabricInterface, observations...)
				if err == nil && res.IsZero() {
					workerCreated(&prev, ws.BareMetalInstance.Name, ws.BareMetalInstance.ID)
				}
			}
			if err != nil || !res.IsZero() {
				return nil, res, err
			}
			if res, err := r.persistWorkerChangesAndRefresh(ctx, co, appendWorkerDifference(nil, before, prev)); err != nil || !res.IsZero() {
				return nil, res, err
			}
			// Observe the external action next time, even if its response was a no-op.
			return nil, workerBoundaryRequeue(), nil
		}
	}
	if err := r.checkCurrentCapacityPlan(ctx, co); err != nil {
		return nil, ctrl.Result{}, err
	}
	workers := plan.selected
	if len(plan.excess) > 0 {
		workers = r.handleScaleDown(ctx, co, workers, plan.excess, observations...)
	}
	return workers, ctrl.Result{}, nil
}

// reserveWorkerSlots allocates opaque names in authoritative status before any
// external create. added reports actual additions in the successful patch, not
// whether a no-op helper was called or another invocation's slots were refreshed.
func (r *Reconciler) reserveWorkerSlots(ctx context.Context, co *v1alpha1.ClusterOrder) (bool, error) {
	var referenceErr error
	added := false
	if err := r.patchStatusWithRetry(ctx, co, func(latest *v1alpha1.ClusterOrder) {
		referenceErr = nil
		added = false
		if err := validateBareMetalNodeSets(latest); err != nil {
			referenceErr = err
			return
		}
		before := len(latest.Status.Workers)
		referenceErr = allocateMissingWorkerSlots(latest)
		added = len(latest.Status.Workers) > before
	}); err != nil {
		return false, err
	}
	if referenceErr != nil {
		return false, referenceErr
	}
	return added, r.refreshWorkerOrder(ctx, co)
}

// allocateMissingWorkerSlots fills gaps in each NodeSet's desired capacity in status.
// The caller must persist these reservations before creating any external resources.
func allocateMissingWorkerSlots(co *v1alpha1.ClusterOrder) error {
	if err := validateWorkerBMIReferences(co); err != nil {
		return err
	}
	plan := planWorkerSlots(co)
	for _, nr := range co.Spec.NodeRequests {
		if !nr.IsBareMetal() {
			continue
		}
		for range plan.missingByNodeSet[nr.NodeSet] {
			name := uuid.NewString()
			w := newWorkerStatus(nr.NodeSet, nr.BareMetal.InstanceType, name, "", workerPhaseProvisioning)
			co.Status.Workers = append(co.Status.Workers, w)
		}
	}
	return nil
}

// ensureWorkerBMI resumes the persisted name, including after a lost Create
// acknowledgement. Ownership and ambiguity checks apply before adoption.
func (r *Reconciler) ensureWorkerBMI(
	ctx context.Context, co *v1alpha1.ClusterOrder, tenant string,
	nr *v1alpha1.NodeRequest, workerName string,
	existingByName map[string]*privatev1.BareMetalInstance,
	image *privatev1.DiskImageReference, ignitionRaw, filter, fabricInterface string, observations ...*workerObservation,
) (v1alpha1.WorkerStatus, ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)
	if bmi, ok := existingByName[workerName]; ok {
		if err := checkWorkerBMI(co, tenant, workerName, bmi); err != nil {
			return v1alpha1.WorkerStatus{}, ctrl.Result{}, r.rejectWorkerIdentity(co, err.Error())
		}
		if err := checkBMIRecoveryCandidate(bmi); err != nil {
			return v1alpha1.WorkerStatus{}, ctrl.Result{}, err
		}
		log.Info("worker BMI already exists, skipping create", "name", workerName)
		return newWorkerStatus(nr.NodeSet, nr.BareMetal.InstanceType, workerName, bmi.GetId(), workerPhaseWaitingForAgent), ctrl.Result{}, nil
	}
	bmi, res, err := r.ensureBMI(ctx, co, tenant, *nr, workerName, image, ignitionRaw, filter, fabricInterface, observations...)
	if err != nil || !res.IsZero() {
		return v1alpha1.WorkerStatus{}, res, err
	}
	log.Info("created BMI", "name", workerName, "id", bmi.GetId())
	r.recorder.Eventf(co, nil, corev1.EventTypeNormal, eventReasonWorkerCreated, "CreateWorker",
		"worker %s: BMI %s created", workerName, bmi.GetId())
	return newWorkerStatus(nr.NodeSet, nr.BareMetal.InstanceType, workerName, bmi.GetId(), workerPhaseWaitingForAgent), ctrl.Result{}, nil
}

func validateBareMetalNodeSets(co *v1alpha1.ClusterOrder) error {
	seen := make(map[string]bool)
	for i, request := range co.Spec.NodeRequests {
		if !request.IsBareMetal() {
			continue
		}
		if request.BareMetal.InstanceType == "" {
			return fmt.Errorf("spec.nodeRequests[%d].bareMetal.instanceType is required", i)
		}
		if request.NodeSet == "" || seen[request.NodeSet] {
			return fmt.Errorf("spec.nodeRequests[%d].nodeSet must be nonempty and unique", i)
		}
		seen[request.NodeSet] = true
	}
	return nil
}
