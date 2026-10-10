// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package baremetalworker

import (
	"context"
	"fmt"
	"reflect"
	"time"

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

// reconcileWorkerLifecycle performs the existing-worker lifecycle work that
// needs no creation inputs: retirement intent and failed incarnation cleanup. A
// durable status write ends the invocation at its own boundary. Pending
// provider cleanup is not a global gate: it is reported through the final
// scheduling decision so teardown, Agent binding and NodePool replicas still
// converge.
func (r *Reconciler) reconcileWorkerLifecycle(
	ctx context.Context, co *v1alpha1.ClusterOrder, tenant string,
) (ctrl.Result, error) {
	// The observation belongs to the original snapshot and must not absorb new
	// slots. First reject changes to the worker plan, then refresh the object
	// itself so status-only patches made earlier in this invocation (for example,
	// the InfraEnv UID annotation) do not leave the next optimistic patch stale.
	expected := co.DeepCopy()
	if err := r.checkCurrentCapacityPlan(ctx, expected); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.readAuthoritativeOrder(ctx, co); err != nil {
		return ctrl.Result{}, err
	}
	if !sameCapacityPlan(expected, co) {
		return ctrl.Result{}, errWorkerObservationChanged
	}
	if err := r.checkCurrentCapacityPlan(ctx, co); err != nil {
		return ctrl.Result{}, err
	}
	// Persist retirement before retry cleanup, so failed excess slots cannot
	// schedule replacements and every destructive action has durable intent.
	plan := planWorkerSlots(co)
	retiring := r.handleScaleDown(ctx, co, plan.selected, plan.excess)
	if !workerStatusesEqual(co.Status.Workers, retiring) {
		if err := r.updateWorkerStatus(ctx, co, retiring); err != nil {
			return ctrl.Result{}, err
		}
		return workerBoundaryRequeue(), nil
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
	if hasFailedIncarnations(failed.Status.Workers) {
		// A pending cleanup action is a wait, not a global gate. Publish protected
		// summaries; the caller schedules the bounded cleanup recheck.
		if err := r.updateWorkerStatusWithAgent(ctx, co, co.Status.Workers); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

// workerCreationInputs holds the external inputs a BMI create needs. They are
// resolved only after a due slot is selected, so a stable order never fetches
// discovery ignition or resolves disk images on an unrelated event.
type workerCreationInputs struct {
	image    *privatev1.DiskImageReference
	ignition []byte
}

// resolveWorkerCreationInputs resolves the cluster-level inputs for a create from
// the invocation's single InfraEnv observation. A non-zero result is a bounded
// prerequisite wait, for example an InfraEnv whose discovery ignition URL is not
// published yet. Held observation evidence is reported as-is so the caller keeps
// converging prerequisite-free work.
func (r *Reconciler) resolveWorkerCreationInputs(
	ctx context.Context, co *v1alpha1.ClusterOrder, infra infraEnvEvidence,
) (workerCreationInputs, ctrl.Result, error) {
	if infra.err != nil {
		return workerCreationInputs{}, ctrl.Result{}, infra.err
	}
	if infra.object == nil {
		// Authoritative absence was observed and its creation is a separate
		// boundary; this invocation must not resolve inputs against nothing.
		return workerCreationInputs{}, ctrl.Result{RequeueAfter: infraEnvRequeueInterval}, nil
	}
	ignition, res, err := r.fetchDiscoveryIgnition(ctx, co, infra.object)
	if err != nil || !res.IsZero() {
		return workerCreationInputs{}, res, err
	}
	image, res, err := r.resolveDiskImage(ctx, co)
	if err != nil || !res.IsZero() {
		return workerCreationInputs{}, res, err
	}
	return workerCreationInputs{image: image, ignition: ignition}, ctrl.Result{}, nil
}

// reconcileWorkerCreation performs the durable reservation and the single
// create/retry for the selected due slot. created reports that a durable
// identity write was persisted, which always ends the invocation. Creation
// inputs are resolved only when a reservation or create is actually due, so a
// stable order never fetches discovery ignition or resolves disk images on an
// unrelated event. A prerequisite wait/error is reported without created so the
// caller can still converge teardown, Agents and NodePool replicas.
func (r *Reconciler) reconcileWorkerCreation(
	ctx context.Context, co *v1alpha1.ClusterOrder, tenant string, observed *workerObservation, infra infraEnvEvidence,
) (bool, ctrl.Result, error) {
	if _, prev := selectDueSlot(co, time.Now()); prev == nil && !workerCreationDue(co) {
		res, err := r.refreshWorkerReadiness(ctx, co, infra)
		if err != nil || !res.IsZero() {
			return false, res, err
		}
		res, err = r.reconcileDueWorkerCreation(ctx, co, tenant, workerCreationInputs{}, observed)
		return false, res, err
	}
	expected := co.DeepCopy()
	inputs, res, err := r.resolveWorkerCreationInputs(ctx, co, infra)
	// Input resolution can persist prerequisite conditions; refresh before any
	// later optimistic patch, including on the deferred path.
	if refreshErr := r.readAuthoritativeOrder(ctx, co); refreshErr != nil {
		return false, ctrl.Result{}, refreshErr
	}
	if err != nil || !res.IsZero() {
		return false, res, err
	}
	if !sameCapacityPlan(expected, co) {
		return false, ctrl.Result{}, errWorkerObservationChanged
	}
	return r.reconcileDueWorkerCapacity(ctx, co, tenant, inputs, observed)
}

// refreshWorkerReadiness reports current discovery-ignition evidence for an
// existing worker set when no create needs the bytes, so a published artifact is
// still observed as Ready. It reads the object the invocation already observed
// (never a second lookup) and does not re-fetch a claim that is already True: a
// stable order performs no ignition request for status alone.
func (r *Reconciler) refreshWorkerReadiness(
	ctx context.Context, co *v1alpha1.ClusterOrder, infra infraEnvEvidence,
) (ctrl.Result, error) {
	if infra.err != nil || infra.object == nil {
		return ctrl.Result{}, nil
	}
	if apimeta.IsStatusConditionTrue(co.Status.Conditions, v1alpha1.ConditionInfraEnvReady) {
		return ctrl.Result{}, nil
	}
	if _, res, err := r.fetchDiscoveryIgnition(ctx, co, infra.object); err != nil || !res.IsZero() {
		return res, err
	}
	return ctrl.Result{}, nil
}

// workerCreationDue reports whether the current worker plan still needs a
// durable reservation. It is the reservation counterpart of selectDueSlot: a
// missing slot has no worker yet, so the reservation itself is the due action.
func workerCreationDue(co *v1alpha1.ClusterOrder) bool {
	plan := planWorkerSlots(co)
	for _, missing := range plan.missingByNodeSet {
		if missing > 0 {
			return true
		}
	}
	return false
}

// reconcileDueWorkerCapacity reserves missing slots and then creates at most one
// selected slot using already resolved inputs. A durable reservation is itself a
// status mutation, so it ends the invocation before any external create.
func (r *Reconciler) reconcileDueWorkerCapacity(
	ctx context.Context, co *v1alpha1.ClusterOrder, tenant string,
	inputs workerCreationInputs, observed *workerObservation,
) (bool, ctrl.Result, error) {
	added, err := r.reserveWorkerSlots(ctx, co)
	if err != nil {
		return false, ctrl.Result{}, err
	}
	if added {
		return true, workerBoundaryRequeue(), nil
	}
	nr, prev := selectDueSlot(co, time.Now())
	if prev == nil {
		res, err := r.reconcileDueWorkerCreation(ctx, co, tenant, inputs, observed)
		return false, res, err
	}
	res, err := r.createSelectedWorker(ctx, co, tenant, nr, prev, inputs, observed)
	return true, res, err
}

// reconcileDueWorkerCreation selects the due slot and creates it with already
// resolved inputs. Without a due slot it only revalidates the capacity plan and
// clears a recovered fulfillment-service condition.
func (r *Reconciler) reconcileDueWorkerCreation(
	ctx context.Context, co *v1alpha1.ClusterOrder, tenant string,
	inputs workerCreationInputs, observed *workerObservation,
) (ctrl.Result, error) {
	nr, prev := selectDueSlot(co, time.Now())
	if prev == nil {
		if err := r.checkCurrentCapacityPlan(ctx, co); err != nil {
			return ctrl.Result{}, err
		}
		if apimeta.IsStatusConditionTrue(co.Status.Conditions, v1alpha1.ConditionFulfillmentServiceUnavailable) {
			if err := r.setFulfillmentServiceUnavailable(ctx, co, metav1.ConditionFalse,
				reasonFulfillmentAvailable, "fulfillment service recovered"); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}
	return r.createSelectedWorker(ctx, co, tenant, nr, prev, inputs, observed)
}

// selectDueSlot returns the first slot that may create or retry a BMI in
// spec/retention order. Slots waiting for retry and slots that already hold an
// identity are not due. It returns a nil worker when no create is due.
func selectDueSlot(co *v1alpha1.ClusterOrder, now time.Time) (*v1alpha1.NodeRequest, *v1alpha1.WorkerStatus) {
	plan := planWorkerSlots(co)
	for i := range co.Spec.NodeRequests {
		nr := &co.Spec.NodeRequests[i]
		if !nr.IsBareMetal() {
			continue
		}
		for _, prev := range plan.selected {
			if prev.NodeSet != nr.NodeSet || prev.BareMetalInstance.ID != "" ||
				(prev.Phase == workerPhaseFailed && !isRetryDue(prev, now)) {
				continue
			}
			candidate := prev
			return nr, &candidate
		}
	}
	return nil, nil
}

// createSelectedWorker performs the single durable create/retry for an already
// selected slot and persists its identity before returning. It is the only stage
// that consumes image and ignition bytes.
func (r *Reconciler) createSelectedWorker(
	ctx context.Context, co *v1alpha1.ClusterOrder, tenant string,
	nr *v1alpha1.NodeRequest, prev *v1alpha1.WorkerStatus,
	inputs workerCreationInputs, observed *workerObservation,
) (ctrl.Result, error) {
	instanceType, res, err := r.resolveNodeSetInstanceType(ctx, co, nr.BareMetal.InstanceType)
	if err != nil || !res.IsZero() {
		return res, err
	}
	var fabricInterface string
	if co.Spec.NetworkAttachment != nil && co.Spec.NetworkAttachment.SubnetRef != "" {
		fabricInterface, err = resolveFabricInterface(instanceType)
		if err != nil {
			return ctrl.Result{}, err
		}
	}
	filter := fmt.Sprintf(`this.metadata.labels["%s"] == "%s"`, clusterOrderLabel, co.Name)
	if err := r.checkCurrentCapacityPlan(ctx, co); err != nil {
		return ctrl.Result{}, err
	}
	// Persist the durable attempt origin before the first external Create for this
	// attempt. A lost acknowledgement or lost identity write then recovers the same
	// clock instead of restarting it, and no provider call happens inside this
	// status write.
	if prev.AttemptStartedAt == nil {
		origin := metav1.NewTime(time.Now())
		prev.AttemptStartedAt = &origin
		if err := r.writeSelectedWorker(ctx, co, prev); err != nil {
			return ctrl.Result{}, err
		}
		return workerBoundaryRequeue(), nil
	}
	if err := r.recordBMICreateAttempt(ctx, co, prev); err != nil {
		return ctrl.Result{}, err
	}
	if prev.Phase == workerPhaseFailed {
		res, err = r.retryFailedWorker(ctx, co, tenant, nr, prev, inputs.image, string(inputs.ignition), filter, fabricInterface)
	} else {
		var ws v1alpha1.WorkerStatus
		ws, res, err = r.ensureWorkerBMI(ctx, co, tenant, nr, prev.BareMetalInstance.Name, observed,
			inputs.image, string(inputs.ignition), filter, fabricInterface)
		if err == nil && res.IsZero() {
			workerCreated(prev, ws.BareMetalInstance.Name, ws.BareMetalInstance.ID)
		}
	}
	if err != nil || !res.IsZero() {
		return res, err
	}
	if err := r.writeSelectedWorker(ctx, co, prev); err != nil {
		return ctrl.Result{}, err
	}
	// Observe the external action next time, even if its response was a no-op.
	return workerBoundaryRequeue(), nil
}

// recordBMICreateAttempt fences Create against cancellation on the same
// resourceVersion. If cancellation/retirement wins, this invocation must not
// issue Create. If intent wins, cleanup retains the possibly accepted BMI.
// A crash after the write but before the API call remains an unknown outcome;
// absence cannot be inferred locally, even when a later List is empty.
func (r *Reconciler) recordBMICreateAttempt(ctx context.Context, co *v1alpha1.ClusterOrder, worker *v1alpha1.WorkerStatus) error {
	if worker.BMICreateState == v1alpha1.WorkerBMICreateStateAttempted {
		return nil
	}
	next := co.DeepCopy()
	recorded := workerByName(next.Status.Workers, worker.Name)
	if recorded == nil || !reflect.DeepEqual(*recorded, *worker) {
		return errWorkerObservationChanged
	}
	recorded.BMICreateState = v1alpha1.WorkerBMICreateStateAttempted
	if err := r.patchStatusFromBase(ctx, co, next); err != nil {
		return err
	}
	// Require the API response to retain intent: an older CRD may prune the
	// field. A successful HTTP status alone cannot authorize external Create.
	persisted := workerByName(next.Status.Workers, worker.Name)
	if persisted == nil || persisted.BMICreateState != v1alpha1.WorkerBMICreateStateAttempted {
		return fmt.Errorf("BMI Create intent was not persisted for worker %s", worker.Name)
	}
	// Use the patch response, not a new snapshot that could hide retirement.
	*co = *next
	*worker = *persisted
	return nil
}

// writeSelectedWorker persists one selected slot's status from the invocation's
// authoritative order. It is used for durable identity and attempt-boundary
// writes and keeps optimistic-lock protection.
func (r *Reconciler) writeSelectedWorker(ctx context.Context, co *v1alpha1.ClusterOrder, worker *v1alpha1.WorkerStatus) error {
	next := co.DeepCopy()
	for i := range next.Status.Workers {
		if next.Status.Workers[i].Name == worker.Name {
			next.Status.Workers[i] = *worker
			break
		}
	}
	return r.patchStatusFromBase(ctx, co, next)
}

// reserveWorkerSlots allocates opaque names in authoritative status before any
// external create. added reports actual additions in the successful patch, not
// whether a no-op helper was called or another invocation's slots were refreshed.
func (r *Reconciler) reserveWorkerSlots(ctx context.Context, co *v1alpha1.ClusterOrder) (bool, error) {
	if err := r.checkCurrentCapacityPlan(ctx, co); err != nil {
		return false, err
	}
	if err := validateBareMetalNodeSets(co); err != nil {
		return false, err
	}
	next := co.DeepCopy()
	before := len(next.Status.Workers)
	if err := allocateMissingWorkerSlots(next); err != nil {
		return false, err
	}
	if len(next.Status.Workers) == before {
		return false, nil
	}
	if err := r.patchStatusFromBase(ctx, co, next); err != nil {
		return false, err
	}
	return true, nil
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
			// newWorkerStatus seeds the attempt origin, so the reservation write
			// records the registration clock before any external Create.
			worker := newWorkerStatus(nr.NodeSet, nr.BareMetal.InstanceType, name, "", workerPhaseProvisioning)
			worker.BMICreateState = v1alpha1.WorkerBMICreateStateReserved
			co.Status.Workers = append(co.Status.Workers, worker)
		}
	}
	return nil
}

// ensureWorkerBMI resumes the persisted name, including after a lost Create
// acknowledgement. Ownership and ambiguity checks use the canonical observation
// index instead of duplicating a second name view.
func (r *Reconciler) ensureWorkerBMI(
	ctx context.Context, co *v1alpha1.ClusterOrder, tenant string,
	nr *v1alpha1.NodeRequest, workerName string,
	observed *workerObservation,
	image *privatev1.DiskImageReference, ignitionRaw, filter, fabricInterface string,
) (v1alpha1.WorkerStatus, ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)
	bmi, nameErr := observed.exactOwnedName(co, tenant, workerName)
	if nameErr != nil {
		return v1alpha1.WorkerStatus{}, ctrl.Result{}, r.rejectWorkerIdentity(co, nameErr.Error())
	}
	if bmi != nil {
		if err := checkBMIRecoveryCandidate(bmi); err != nil {
			return v1alpha1.WorkerStatus{}, ctrl.Result{}, err
		}
		log.Info("worker BMI already exists, skipping create", "name", workerName)
		return newWorkerStatus(nr.NodeSet, nr.BareMetal.InstanceType, workerName, bmi.GetId(), workerPhaseWaitingForAgent), ctrl.Result{}, nil
	}
	bmi, res, err := r.ensureBMI(ctx, co, tenant, *nr, workerName, image, ignitionRaw, filter, fabricInterface)
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
