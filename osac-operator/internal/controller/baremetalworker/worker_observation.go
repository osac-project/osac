// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package baremetalworker

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type bmiGetResult struct {
	bmi *privatev1.BareMetalInstance
	err error
}

// workerObservation lives for one invocation only. List omission is not evidence
// of deletion, and unknown fallback Get results are remembered just like success.
type workerObservation struct {
	byID              map[string]*privatev1.BareMetalInstance
	byName            map[string][]*privatev1.BareMetalInstance
	gets              map[string]bmiGetResult
	agents            *unstructured.UnstructuredList
	agentsInvalidated bool
	projected         map[string]v1alpha1.WorkerStatus
}

func indexWorkerBMIs(bmis []*privatev1.BareMetalInstance) *workerObservation {
	o := &workerObservation{byID: make(map[string]*privatev1.BareMetalInstance), byName: make(map[string][]*privatev1.BareMetalInstance), gets: make(map[string]bmiGetResult), projected: make(map[string]v1alpha1.WorkerStatus)}
	for _, bmi := range bmis {
		o.recordBMI(bmi)
	}
	return o
}

func (o *workerObservation) recordBMI(bmi *privatev1.BareMetalInstance) {
	id, name := bmi.GetId(), bmi.GetMetadata().GetName()
	// Replacing the same ID updates its indexes rather than inventing ambiguity.
	if old := o.byID[id]; old != nil {
		o.invalidateBMI(id)
	}
	o.byID[id] = bmi
	o.byName[name] = append(o.byName[name], bmi)
	delete(o.gets, id)
}

func (o *workerObservation) invalidateBMI(id string) {
	old := o.byID[id]
	if old != nil {
		name := old.GetMetadata().GetName()
		kept := o.byName[name][:0]
		for _, bmi := range o.byName[name] {
			if bmi.GetId() != id {
				kept = append(kept, bmi)
			}
		}
		o.byName[name] = kept
	}
	delete(o.byID, id)
	// A Delete request is not a completed deletion; never cache synthetic NotFound.
	delete(o.gets, id)
}

func (o *workerObservation) getBMI(ctx context.Context, f FulfillmentClient, id string) (*privatev1.BareMetalInstance, error) {
	if bmi, ok := o.byID[id]; ok {
		return bmi, nil
	}
	if got, ok := o.gets[id]; ok {
		return got.bmi, got.err
	}
	bmi, err := f.GetBareMetalInstance(ctx, id)
	o.gets[id] = bmiGetResult{bmi: bmi, err: err}
	return bmi, err
}

func (o *workerObservation) exactOwnedName(co *v1alpha1.ClusterOrder, tenant, name string) (*privatev1.BareMetalInstance, error) {
	candidates := o.byName[name]
	if len(candidates) > 1 {
		return nil, fmt.Errorf("ambiguous BMI name %q", name)
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	bmi := candidates[0]
	if err := checkWorkerBMI(co, tenant, name, bmi); err != nil {
		return nil, err
	}
	return bmi, nil
}

func (o *workerObservation) uniqueBMIsByName() (map[string]*privatev1.BareMetalInstance, error) {
	result := make(map[string]*privatev1.BareMetalInstance)
	for name, candidates := range o.byName {
		if len(candidates) > 1 {
			return nil, fmt.Errorf("ambiguous BMI name %q", name)
		}
		if len(candidates) == 1 {
			result[name] = candidates[0]
		}
	}
	return result, nil
}

func (r *Reconciler) observeWorkerResources(ctx context.Context, co *v1alpha1.ClusterOrder) (*workerObservation, ctrl.Result, error) {
	filter := fmt.Sprintf(`this.metadata.labels["%s"] == "%s"`, clusterOrderLabel, co.Name)
	bmis, err := r.fulfillment.ListBareMetalInstances(ctx, filter)
	if err != nil {
		res, err := r.handleFulfillmentError(ctx, co, fmt.Errorf("observing worker BMIs: %w", err))
		return nil, res, err
	}
	o := indexWorkerBMIs(bmis)
	o.agents, err = r.listAgents(ctx, co)
	return o, ctrl.Result{}, err
}

// workerCapacityObservation reuses the reconcile-local observation when supplied;
// standalone capacity reconciliation only needs to list and index BMIs.
func (r *Reconciler) workerCapacityObservation(
	ctx context.Context, co *v1alpha1.ClusterOrder, filter string, observations ...*workerObservation,
) (*workerObservation, ctrl.Result, error) {
	if len(observations) > 0 && observations[0] != nil {
		return observations[0], ctrl.Result{}, nil
	}
	existing, err := r.fulfillment.ListBareMetalInstances(ctx, filter)
	if err != nil {
		res, err := r.handleFulfillmentError(ctx, co, fmt.Errorf("listing BMIs for %s: %w", co.Name, err))
		return nil, res, err
	}
	return indexWorkerBMIs(existing), ctrl.Result{}, nil
}

func (r *Reconciler) workerMACResolver(o *workerObservation) MACResolver {
	if r.macResolver != nil {
		return r.macResolver
	}
	if o == nil {
		return r.resolveHostMACs
	}
	return func(ctx context.Context, id string) []string {
		bmi, err := o.getBMI(ctx, r.fulfillment, id)
		if err != nil {
			return nil
		}
		return nicMACs(bmi)
	}
}

// observeExistingWorkers verifies all recorded IDs, including protected lifecycle
// states, then combines identity repair and quiet early Agent projection in one pass.
func (r *Reconciler) observeExistingWorkers(ctx context.Context, co *v1alpha1.ClusterOrder, tenant string, o *workerObservation) ([]workerChange, error) {
	if err := validateWorkerBMIReferences(co); err != nil {
		return nil, err
	}
	macs := r.workerMACResolver(o)
	var changes []workerChange
	for _, w := range co.Status.Workers {
		if w.Kind != workerKindBMI {
			continue
		}
		var state bmiState
		if w.BareMetalInstance.ID != "" || eligibleForBMIRecovery(w) {
			var err error
			state, err = r.verifyRecordedBMI(ctx, co, tenant, w, o)
			if err != nil {
				return nil, err
			}
		}
		candidate := reconcileBMI(w, state)
		if candidate == nil {
			changes = append(changes, workerChange{observed: w})
			continue
		}
		next := *candidate
		if o.agents != nil && eligibleForAgentObservation(next) {
			next.Phase = deriveWorkerPhase(findAgentForWorker(ctx, o.agents, next.BareMetalInstance.ID, next.Name, macs), next.Name)
			if next.Phase == workerPhaseReady && next.ReadySince == nil {
				projected := []v1alpha1.WorkerStatus{next}
				initializeReadySince(projected)
				next = projected[0]
			}
		}
		o.projected[w.Name] = next
		changes = appendWorkerDifference(changes, w, next)
	}
	return changes, nil
}

// observeDeletionWorkers recovers names in every lifecycle state, verifies all
// recorded IDs, and changes only identity. It never resets history, projects
// Agent phases, reserves capacity or provisions resources.
func (r *Reconciler) observeDeletionWorkers(ctx context.Context, co *v1alpha1.ClusterOrder, tenant string, o *workerObservation) ([]workerChange, error) {
	if err := validateWorkerBMIReferences(co); err != nil {
		return nil, err
	}
	var changes []workerChange
	for _, w := range co.Status.Workers {
		if w.Kind != workerKindBMI {
			continue
		}
		state, err := r.verifyRecordedBMI(ctx, co, tenant, w, o)
		if err != nil {
			return nil, err
		}
		next := w
		if next.BareMetalInstance.ID == "" && state.bmi != nil {
			next.BareMetalInstance.ID = state.bmi.GetId()
		}
		changes = appendWorkerDifference(changes, w, next)
	}
	return changes, nil
}
