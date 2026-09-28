// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package baremetalworker

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type bmiGetResult struct {
	bmi *privatev1.BareMetalInstance
	err error
}

// workerObservation lives for one invocation only. List omission is not evidence
// of deletion, and unknown fallback Get results are remembered just like success.
// The indexes are built during observation and are never repaired after a
// mutation: a create/delete/bind ends the invocation and a fresh observation
// supplies the next one.
type workerObservation struct {
	byID   map[string]*privatev1.BareMetalInstance
	byName map[string][]*privatev1.BareMetalInstance
	gets   map[string]bmiGetResult
	agents *unstructured.UnstructuredList
}

func indexWorkerBMIs(bmis []*privatev1.BareMetalInstance) *workerObservation {
	o := &workerObservation{
		byID:   make(map[string]*privatev1.BareMetalInstance),
		byName: make(map[string][]*privatev1.BareMetalInstance),
		gets:   make(map[string]bmiGetResult),
	}
	for _, bmi := range bmis {
		id, name := bmi.GetId(), bmi.GetMetadata().GetName()
		o.byID[id] = bmi
		o.byName[name] = append(o.byName[name], bmi)
	}
	return o
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

func (r *Reconciler) observeWorkerResources(ctx context.Context, co *v1alpha1.ClusterOrder) (*workerObservation, ctrl.Result, error) {
	filter := fmt.Sprintf(`this.metadata.labels["%s"] == "%s"`, clusterOrderLabel, co.Name)
	bmis, err := r.fulfillment.ListBareMetalInstances(ctx, filter)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("observing worker BMIs: %w", err)
	}
	o := indexWorkerBMIs(bmis)
	o.agents, err = r.listAgents(ctx, co)
	return o, ctrl.Result{}, err
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
// The returned slice is a complete replacement calculated from co's authoritative
// invocation snapshot; it is never merged into a newer status object.
func (r *Reconciler) observeExistingWorkers(ctx context.Context, co *v1alpha1.ClusterOrder, tenant string, o *workerObservation) ([]v1alpha1.WorkerStatus, error) {
	if err := validateWorkerBMIReferences(co); err != nil {
		return nil, err
	}
	now := time.Now()
	workers := append([]v1alpha1.WorkerStatus(nil), co.Status.Workers...)
	for i := 0; i < len(workers); i++ {
		w := workers[i]
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
			workers = append(workers[:i], workers[i+1:]...)
			i--
			continue
		}
		// Legacy optional-field migration: a pre-existing attempt gets one durable
		// origin, preferably from the backing BMI's own creation timestamp. It is
		// persisted before any side effect and never refreshed, so grace is granted
		// exactly once and parent age is never used as the registration clock.
		if candidate.AttemptStartedAt == nil && eligibleForAgentObservation(*candidate) {
			origin, fromBMI := attemptOrigin(state.bmi, now)
			candidate.AttemptStartedAt = &origin
			if !fromBMI {
				ctrllog.FromContext(ctx).Info("backfilled attempt start for legacy worker",
					"worker", candidate.Name, "reason", "no usable BMI creation timestamp")
			}
		}
		workers[i] = *candidate
	}
	// The pure helper is the single Agent-phase projection owner; this stage only
	// supplies the snapshot and never re-derives phases elsewhere.
	if o.agents != nil {
		var err error
		workers, err = projectAgentWorkerPhases(co, workers, o.agents)
		if err != nil {
			return nil, err
		}
	}
	projectReadySince(workers, now)
	// Readiness telemetry belongs to this single projection boundary. It is
	// emitted only for an actual Binding -> Ready observation; the caller
	// persists the projection afterwards or returns a conflict without
	// pretending the transition happened.
	r.observeAgentReadiness(ctx, co, co.Status.Workers, workers)
	return workers, nil
}

// attemptOrigin returns the durable registration origin to backfill for a
// pre-existing worker. The backing BMI's creation timestamp is used when it is
// plausible; otherwise the supplied observation time is used and the boolean
// reports that a compatibility fallback happened.
func attemptOrigin(bmi *privatev1.BareMetalInstance, now time.Time) (metav1.Time, bool) {
	if ts := bmi.GetMetadata().GetCreationTimestamp(); ts != nil && ts.GetSeconds() > 0 && ts.CheckValid() == nil {
		return metav1.NewTime(ts.AsTime()), true
	}
	return metav1.NewTime(now), false
}

// observeDeletionWorkers recovers names in every lifecycle state, verifies all
// recorded IDs, and changes only identity. It never resets history, projects
// Agent phases, reserves capacity or provisions resources.
func (r *Reconciler) observeDeletionWorkers(ctx context.Context, co *v1alpha1.ClusterOrder, tenant string, o *workerObservation) ([]v1alpha1.WorkerStatus, error) {
	if err := validateWorkerBMIReferences(co); err != nil {
		return nil, err
	}
	workers := append([]v1alpha1.WorkerStatus(nil), co.Status.Workers...)
	for i := range workers {
		if workers[i].Kind != workerKindBMI {
			continue
		}
		state, err := r.verifyRecordedBMI(ctx, co, tenant, workers[i], o)
		if err != nil {
			return nil, err
		}
		if workers[i].BareMetalInstance.ID == "" && state.bmi != nil {
			workers[i].BareMetalInstance.ID = state.bmi.GetId()
		}
	}
	return workers, nil
}
