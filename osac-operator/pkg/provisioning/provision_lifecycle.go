/*
Copyright 2025.

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

package provisioning

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	v1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

// State points into the resource's status fields used by the provisioning lifecycle.
// Jobs is a pointer so shared functions can modify the slice in place.
// DesiredConfigVersion is a value snapshot captured at construction time — it is
// not updated if the instance status changes afterward.
type State struct {
	Jobs                 *[]v1alpha1.JobStatus
	DesiredConfigVersion string
}

// JobsExtractor extracts a jobs array from a resource. Used by CheckAPIServerForNonTerminalProvisionJob
// to read jobs from a fresh API server copy of the resource. Each controller passes a typed extractor
// for its own CRD (e.g. func(obj) { return obj.(*Subnet).Status.ProvisioningJobs }).
type JobsExtractor func(client.Object) []v1alpha1.JobStatus

// EvaluateAction determines the next provisioning action based on job history and config versions.
func EvaluateAction(provState *State, checkAPIServer func() bool) (Action, *v1alpha1.JobStatus) {
	return evaluateActionForTarget(provState, "", checkAPIServer)
}

// evaluateActionForTarget is EvaluateAction scoped to a single job target, so
// concurrent targets on the same resource are evaluated from only their own
// job history.
func evaluateActionForTarget(provState *State, target string, checkAPIServer func() bool) (Action, *v1alpha1.JobStatus) {
	latestJob := FindLatestJobByTypeAndTarget(*provState.Jobs, v1alpha1.JobTypeProvision, target)

	if !HasJobID(latestJob) {
		// No provision job exists — trigger one.
		// This is intentional: resources without job history (new, imported, or trimmed by
		// maxJobHistory) should be provisioned. With AAP direct, job tracking is the source
		// of truth; the old annotation-based skip path has been removed.
	} else if !latestJob.State.IsTerminal() {
		return Poll, latestJob
	} else if latestJob.ConfigVersion == provState.DesiredConfigVersion {
		if latestJob.State == v1alpha1.JobStateSucceeded {
			return Skip, latestJob
		}
		return Backoff, latestJob
	} else if latestJob.ConfigVersion == "" && latestJob.State == v1alpha1.JobStateSucceeded {
		// Legacy job without ConfigVersion that succeeded — skip
		return Skip, latestJob
	}

	if checkAPIServer() {
		return Requeue, nil
	}
	return Trigger, latestJob
}

// CheckAPIServerForNonTerminalProvisionJob reads the resource directly from the API server
// and returns true if a non-terminal provision job exists. The extract parameter (a JobsExtractor)
// determines which jobs array to check — each controller passes a typed extractor for its CRD.
func CheckAPIServerForNonTerminalProvisionJob(ctx context.Context, apiReader client.Reader, key client.ObjectKey, fresh client.Object, extract JobsExtractor) bool {
	return CheckAPIServerForNonTerminalProvisionJobAndTarget(ctx, apiReader, key, fresh, extract, "")
}

// CheckAPIServerForNonTerminalProvisionJobAndTarget is
// CheckAPIServerForNonTerminalProvisionJob scoped to a single job target —
// required when populating JobTarget.CheckAPIServer for a non-"" target,
// since the untargeted form only ever checks untagged (target == "") job
// history.
func CheckAPIServerForNonTerminalProvisionJobAndTarget(ctx context.Context, apiReader client.Reader, key client.ObjectKey, fresh client.Object, extract JobsExtractor, target string) bool {
	log := ctrllog.FromContext(ctx)
	if err := apiReader.Get(ctx, key, fresh); err != nil {
		return false
	}
	freshJobs := extract(fresh)
	freshJob := FindLatestJobByTypeAndTarget(freshJobs, v1alpha1.JobTypeProvision, target)
	if HasJobID(freshJob) && !freshJob.State.IsTerminal() {
		log.Info("skipping provision trigger: non-terminal job found via API server", "jobID", freshJob.JobID, "target", target, "state", freshJob.State)
		return true
	}
	return false
}

// TriggerJob triggers a new provision job and updates the jobs slice in place via State.
func TriggerJob(ctx context.Context, provider ProvisioningProvider, resource client.Object, provState *State, maxHistory int, pollInterval time.Duration) (ctrl.Result, error) {
	return triggerJobForTarget(ctx, provider, resource, provState, "", maxHistory, pollInterval)
}

// triggerJobForTarget is TriggerJob scoped to a single job target: the
// appended JobStatus is tagged with target so it can be found again via
// FindLatestJobByTypeAndTarget.
func triggerJobForTarget(ctx context.Context, provider ProvisioningProvider, resource client.Object, provState *State, target string, maxHistory int, pollInterval time.Duration) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)
	log.Info("triggering provision job", "target", target)

	result, err := provider.TriggerProvision(ctx, resource)
	if err != nil {
		if rateLimitErr, ok := AsRateLimitError(err); ok {
			log.Info("provision request rate-limited, requeueing", "target", target, "retryAfter", rateLimitErr.RetryAfter)
			return ctrl.Result{RequeueAfter: rateLimitErr.RetryAfter}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to trigger provision: %w", err)
	}

	*provState.Jobs = AppendJob(*provState.Jobs, v1alpha1.JobStatus{
		JobID:         result.JobID,
		Type:          v1alpha1.JobTypeProvision,
		State:         result.InitialState,
		Message:       result.Message,
		Timestamp:     metav1.NewTime(time.Now().UTC()),
		ConfigVersion: provState.DesiredConfigVersion,
		Target:        target,
	}, maxHistory)

	latestJob := FindLatestJobByTypeAndTarget(*provState.Jobs, v1alpha1.JobTypeProvision, target)
	log.Info("provision job triggered", "jobID", latestJob.JobID, "target", target, "configVersion", latestJob.ConfigVersion)
	return ctrl.Result{RequeueAfter: pollInterval}, nil
}

// PollCallbacks holds optional callbacks for provision job state transitions.
type PollCallbacks struct {
	// OnFailed is called when the job transitions to Failed state.
	OnFailed func(message string)
	// OnSuccess is called when the job succeeds.
	OnSuccess func(status ProvisionStatus)
}

// PollJob checks the status of an existing provision job and updates the jobs slice in place.
func PollJob(ctx context.Context, provider ProvisioningProvider, resource client.Object, provState *State, latestJob *v1alpha1.JobStatus, pollInterval time.Duration, callbacks *PollCallbacks) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)
	log.Info("polling provision job status", "jobID", latestJob.JobID, "currentState", latestJob.State)

	status, err := provider.GetProvisionStatus(ctx, resource, latestJob.JobID)
	if err != nil {
		log.Error(err, "failed to get provision status", "jobID", latestJob.JobID)
		updatedJob := *latestJob
		updatedJob.Message = fmt.Sprintf("Failed to get job status: %v", err)
		UpdateJob(*provState.Jobs, updatedJob)
		return ctrl.Result{RequeueAfter: pollInterval}, nil
	}

	if status.State != latestJob.State || status.Message != latestJob.Message {
		log.Info("provision job status changed", "jobID", latestJob.JobID, "oldState", latestJob.State, "newState", status.State)
		updatedJob := *latestJob
		updatedJob.State = status.State
		updatedJob.Message = status.MessageWithDetails()
		UpdateJob(*provState.Jobs, updatedJob)

		if status.State == v1alpha1.JobStateFailed {
			log.Info("provision job failed", "jobID", latestJob.JobID)
			if callbacks != nil && callbacks.OnFailed != nil {
				callbacks.OnFailed(updatedJob.Message)
			}
		}
	}

	if !status.State.IsTerminal() {
		return ctrl.Result{RequeueAfter: pollInterval}, nil
	}

	if status.State.IsSuccessful() && callbacks != nil && callbacks.OnSuccess != nil {
		callbacks.OnSuccess(status)
	}
	return ctrl.Result{}, nil
}

// RunProvisioningLifecycle encapsulates the full provisioning flow: evaluate action,
// trigger/poll/backoff as needed. Controllers call this instead of duplicating the
// switch statement. The callbacks customize behavior on success and failure.
//
// statusFlush is called after a provision job is successfully triggered to persist
// the job status immediately, preventing duplicate jobs from concurrent reconciliations.
// Errors are logged but non-fatal — the end-of-reconcile status update serves as fallback.
func RunProvisioningLifecycle(
	ctx context.Context,
	provider ProvisioningProvider,
	resource client.Object,
	provState *State,
	maxHistory int,
	pollInterval time.Duration,
	callbacks *PollCallbacks,
	checkAPIServer func() bool,
	statusFlush func() error,
) (ctrl.Result, error) {
	result, err, triggered := runLifecycleCore(ctx, provider, resource, provState, "", maxHistory, pollInterval, callbacks, checkAPIServer)
	if err != nil {
		return result, err
	}
	if triggered && statusFlush != nil {
		if flushErr := statusFlush(); flushErr != nil {
			ctrllog.FromContext(ctx).Error(flushErr, "failed to flush status after job trigger; end-of-reconcile update will retry")
		}
	}
	return result, nil
}

// runLifecycleCore runs the evaluate/trigger/poll/backoff state machine for a
// single job target. The returned bool reports whether a new job was
// triggered on this target during this call, letting callers decide when to
// flush status (RunProvisioningLifecycle flushes immediately;
// RunMultiTargetProvisioningLifecycle flushes at most once after every
// target has run).
func runLifecycleCore(
	ctx context.Context,
	provider ProvisioningProvider,
	resource client.Object,
	provState *State,
	target string,
	maxHistory int,
	pollInterval time.Duration,
	callbacks *PollCallbacks,
	checkAPIServer func() bool,
) (ctrl.Result, error, bool) {
	action, latestJob := evaluateActionForTarget(provState, target, checkAPIServer)

	triggered := false
	trigger := func() (ctrl.Result, error) {
		prevJob := FindLatestJobByTypeAndTarget(*provState.Jobs, v1alpha1.JobTypeProvision, target)
		prevJobID := ""
		if prevJob != nil {
			prevJobID = prevJob.JobID
		}
		res, err := triggerJobForTarget(ctx, provider, resource, provState, target, maxHistory, pollInterval)
		if err != nil {
			return res, err
		}
		newJob := FindLatestJobByTypeAndTarget(*provState.Jobs, v1alpha1.JobTypeProvision, target)
		if newJob != nil && newJob.JobID != prevJobID {
			triggered = true
		}
		return res, nil
	}

	switch action {
	case Skip:
		return ctrl.Result{}, nil, false
	case Trigger:
		res, err := trigger()
		return res, err, triggered
	case Requeue:
		return ctrl.Result{RequeueAfter: pollInterval}, nil, false
	case Backoff:
		res, err := handleBackoffForTarget(ctx, provState, target, latestJob, trigger)
		return res, err, triggered
	default: // Poll
		res, err := PollJob(ctx, provider, resource, provState, latestJob, pollInterval, callbacks)
		return res, err, false
	}
}

// JobTarget scopes one manager target (e.g. "fabric" or "k8s") within a
// multi-target provisioning lifecycle run by RunMultiTargetProvisioningLifecycle.
// Each target tracks its own job history (jobs tagged with a matching
// JobStatus.Target) and is driven independently: one target backing off or
// still running does not block or cancel another.
type JobTarget struct {
	// Name tags jobs triggered for this target (JobStatus.Target) and scopes
	// this target's own PollCallbacks. Must be non-empty and unique within a
	// single RunMultiTargetProvisioningLifecycle call.
	Name string

	// Provider triggers/polls jobs for this target only.
	Provider ProvisioningProvider

	// Callbacks fire on this target's own success/failure. Optional.
	Callbacks *PollCallbacks

	// CheckAPIServer detects a non-terminal job for this target via a fresh
	// API server read, mirroring RunProvisioningLifecycle's checkAPIServer
	// parameter. Required (non-nil) — matches the existing single-target
	// contract where every caller supplies one. Use
	// CheckAPIServerForNonTerminalProvisionJobAndTarget (not the untargeted
	// CheckAPIServerForNonTerminalProvisionJob) to populate this for a
	// non-"" target.
	CheckAPIServer func() bool
}

// RunMultiTargetProvisioningLifecycle runs the same evaluate/trigger/poll/
// backoff state machine as RunProvisioningLifecycle independently for each
// target, against job history tagged by JobStatus.Target. Requeues at the
// soonest interval any target still needs (ctrl.Result{} only once every
// target has reached Skip), and joins errors from all targets rather than
// short-circuiting on the first one — one target's failure never prevents
// another target's attempt in the same call. statusFlush is called at most
// once per call, only if at least one target triggered a new job.
//
// Every target's manager must have actually been dispatched to before being
// included here — a target that was never dispatched (e.g. a resource with
// no k8s manager configured) has no job history to evaluate and should be
// omitted by the caller rather than passed in.
func RunMultiTargetProvisioningLifecycle(
	ctx context.Context,
	targets []JobTarget,
	resource client.Object,
	provState *State,
	maxHistory int,
	pollInterval time.Duration,
	statusFlush func() error,
) (ctrl.Result, error) {
	if err := validateJobTargets(targets); err != nil {
		return ctrl.Result{}, err
	}

	var (
		errs         []error
		anyTriggered bool
		result       ctrl.Result
	)

	for _, t := range targets {
		res, err, triggered := runLifecycleCore(ctx, t.Provider, resource, provState, t.Name, maxHistory, pollInterval, t.Callbacks, t.CheckAPIServer)
		if err != nil {
			errs = append(errs, fmt.Errorf("target %q: %w", t.Name, err))
		}
		if triggered {
			anyTriggered = true
		}
		if res.RequeueAfter > 0 && (result.RequeueAfter == 0 || res.RequeueAfter < result.RequeueAfter) {
			result.RequeueAfter = res.RequeueAfter
		}
	}

	if anyTriggered && statusFlush != nil {
		if flushErr := statusFlush(); flushErr != nil {
			ctrllog.FromContext(ctx).Error(flushErr, "failed to flush status after multi-target job trigger; end-of-reconcile update will retry")
		}
	}

	return result, errors.Join(errs...)
}

// validateJobTargets rejects target lists that would make
// RunMultiTargetProvisioningLifecycle's per-target dispatch ambiguous or
// impossible: at least one target, every target with a non-empty and unique
// Name, and a non-nil Provider and CheckAPIServer for each (both are
// unconditionally invoked while evaluating that target's action).
func validateJobTargets(targets []JobTarget) error {
	if len(targets) == 0 {
		return errors.New("at least one JobTarget is required")
	}
	seen := make(map[string]struct{}, len(targets))
	for _, t := range targets {
		if t.Name == "" {
			return errors.New("JobTarget.Name must not be empty")
		}
		if _, dup := seen[t.Name]; dup {
			return fmt.Errorf("duplicate JobTarget.Name %q", t.Name)
		}
		seen[t.Name] = struct{}{}
		if t.Provider == nil {
			return fmt.Errorf("JobTarget %q: Provider must not be nil", t.Name)
		}
		if t.CheckAPIServer == nil {
			return fmt.Errorf("JobTarget %q: CheckAPIServer must not be nil", t.Name)
		}
	}
	return nil
}

// IsConfigApplied returns true if the current spec has been successfully applied.
// Only the latest provision job is considered to avoid false positives when a spec
// reverts to a previously applied value (A-B-A problem).
// Also returns true for legacy provision jobs (empty ConfigVersion) that succeeded,
// to avoid re-triggering provisioning for resources provisioned before ConfigVersion
// tracking was introduced.
func IsConfigApplied(jobs *[]v1alpha1.JobStatus, desiredConfigVersion string) bool {
	latest := FindLatestJobByType(*jobs, v1alpha1.JobTypeProvision)
	if latest == nil {
		return false
	}
	if latest.State == v1alpha1.JobStateSucceeded && latest.ConfigVersion == desiredConfigVersion {
		return true
	}
	return latest.State == v1alpha1.JobStateSucceeded && latest.ConfigVersion == ""
}

// ComputeDesiredConfigVersion computes a hash of the spec and returns it.
// The caller must pass the resource's Spec field (not the entire resource).
func ComputeDesiredConfigVersion(spec any) (string, error) {
	specJSON, err := json.Marshal(spec)
	if err != nil {
		return "", fmt.Errorf("failed to marshal spec to JSON: %w", err)
	}
	hasher := fnv.New64a()
	if _, err := hasher.Write(specJSON); err != nil {
		return "", fmt.Errorf("failed to write to hash: %w", err)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// TriggerDeprovisionJob triggers a deprovision job via the provider and handles the result.
// Updates the jobs slice in place. Returns the result for the controller to return.
func TriggerDeprovisionJob(ctx context.Context, provider ProvisioningProvider, resource client.Object,
	jobs *[]v1alpha1.JobStatus, maxHistory int, pollInterval time.Duration) (ctrl.Result, error) {
	return triggerDeprovisionJobForTarget(ctx, provider, resource, jobs, "", maxHistory, pollInterval)
}

// triggerDeprovisionJobForTarget is TriggerDeprovisionJob scoped to a single
// job target: the appended deprovision JobStatus is tagged with target.
func triggerDeprovisionJobForTarget(ctx context.Context, provider ProvisioningProvider, resource client.Object,
	jobs *[]v1alpha1.JobStatus, target string, maxHistory int, pollInterval time.Duration) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)
	log.Info("triggering deprovision job", "target", target)

	result, err := provider.TriggerDeprovision(ctx, resource, *jobs)
	if err != nil {
		if rateLimitErr, ok := AsRateLimitError(err); ok {
			log.Info("deprovision request rate-limited, requeueing", "target", target, "retryAfter", rateLimitErr.RetryAfter)
			return ctrl.Result{RequeueAfter: rateLimitErr.RetryAfter}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to trigger deprovision: %w", err)
	}

	switch result.Action {
	case DeprovisionSkipped:
		log.Info("deprovisioning skipped by provider", "target", target)
		return ctrl.Result{}, nil

	case DeprovisionWaiting:
		log.Info("waiting for provision job to terminate before deprovisioning", "target", target)
		updateProvisionJobFromDeprovisionResultForTarget(jobs, target, result)
		return ctrl.Result{RequeueAfter: pollInterval}, nil

	case DeprovisionTriggered:
		log.Info("deprovision job triggered", "jobID", result.JobID, "target", target)
		updateProvisionJobFromDeprovisionResultForTarget(jobs, target, result)
		*jobs = AppendJob(*jobs, v1alpha1.JobStatus{
			JobID:                  result.JobID,
			Type:                   v1alpha1.JobTypeDeprovision,
			State:                  v1alpha1.JobStatePending,
			Message:                "Deprovision job triggered",
			Timestamp:              metav1.NewTime(time.Now().UTC()),
			BlockDeletionOnFailure: result.BlockDeletionOnFailure,
			Target:                 target,
		}, maxHistory)
		return ctrl.Result{RequeueAfter: pollInterval}, nil

	default:
		return ctrl.Result{}, fmt.Errorf("unknown deprovision action: %v", result.Action)
	}
}

// updateProvisionJobFromDeprovisionResultForTarget updates the matching-target
// provision job's status from the deprovision result, if provided by the
// provider.
func updateProvisionJobFromDeprovisionResultForTarget(jobs *[]v1alpha1.JobStatus, target string, result *DeprovisionResult) {
	if result.ProvisionJobStatus == nil {
		return
	}
	latestProvisionJob := FindLatestJobByTypeAndTarget(*jobs, v1alpha1.JobTypeProvision, target)
	if latestProvisionJob == nil {
		return
	}
	updatedJob := *latestProvisionJob
	updatedJob.State = result.ProvisionJobStatus.State
	updatedJob.Message = result.ProvisionJobStatus.MessageWithDetails()
	UpdateJob(*jobs, updatedJob)
}

// RunDeprovisioningLifecycle encapsulates the full deprovisioning flow: trigger if no job
// exists, poll/retry if one does. Controllers call this instead of duplicating the
// trigger-or-poll logic. Returns (result, done, error) where done=true means the
// controller can proceed with finalizer removal.
func RunDeprovisioningLifecycle(ctx context.Context, provider ProvisioningProvider, resource client.Object,
	jobs *[]v1alpha1.JobStatus, maxHistory int, pollInterval time.Duration) (ctrl.Result, bool, error) {
	return runDeprovisioningLifecycleForTarget(ctx, provider, resource, jobs, "", maxHistory, pollInterval)
}

// runDeprovisioningLifecycleForTarget is RunDeprovisioningLifecycle scoped to
// a single job target, driven from only that target's own deprovision job
// history.
func runDeprovisioningLifecycleForTarget(ctx context.Context, provider ProvisioningProvider, resource client.Object,
	jobs *[]v1alpha1.JobStatus, target string, maxHistory int, pollInterval time.Duration) (ctrl.Result, bool, error) {
	latestDeprovisionJob := FindLatestJobByTypeAndTarget(*jobs, v1alpha1.JobTypeDeprovision, target)

	if !HasJobID(latestDeprovisionJob) {
		result, err := triggerDeprovisionJobForTarget(ctx, provider, resource, jobs, target, maxHistory, pollInterval)
		return result, false, err
	}

	return pollDeprovisionJobForTarget(ctx, provider, resource, jobs, target, latestDeprovisionJob, maxHistory, pollInterval)
}

// PollDeprovisionJob polls the status of an existing deprovision job.
// Returns (result, done, error) where done=true means the job reached terminal state
// and the controller can proceed with finalizer removal.
// When a deprovision job fails with BlockDeletionOnFailure, the function retries
// after exponential backoff rather than blocking forever.
func PollDeprovisionJob(ctx context.Context, provider ProvisioningProvider, resource client.Object,
	jobs *[]v1alpha1.JobStatus, latestDeprovisionJob *v1alpha1.JobStatus, maxHistory int, pollInterval time.Duration) (ctrl.Result, bool, error) {
	return pollDeprovisionJobForTarget(ctx, provider, resource, jobs, "", latestDeprovisionJob, maxHistory, pollInterval)
}

// pollDeprovisionJobForTarget is PollDeprovisionJob scoped to a single job target.
func pollDeprovisionJobForTarget(ctx context.Context, provider ProvisioningProvider, resource client.Object,
	jobs *[]v1alpha1.JobStatus, target string, latestDeprovisionJob *v1alpha1.JobStatus, maxHistory int, pollInterval time.Duration) (ctrl.Result, bool, error) {
	log := ctrllog.FromContext(ctx)

	if latestDeprovisionJob.State.IsTerminal() {
		if !latestDeprovisionJob.State.IsSuccessful() && latestDeprovisionJob.BlockDeletionOnFailure {
			return handleDeprovisionBackoffForTarget(ctx, provider, resource, jobs, target, latestDeprovisionJob, maxHistory, pollInterval)
		}
		return ctrl.Result{}, true, nil
	}

	log.Info("polling deprovision job status", "jobID", latestDeprovisionJob.JobID, "target", target, "currentState", latestDeprovisionJob.State)
	status, err := provider.GetDeprovisionStatus(ctx, resource, latestDeprovisionJob.JobID)
	if err != nil {
		log.Error(err, "failed to get deprovision status", "jobID", latestDeprovisionJob.JobID, "target", target)
		updatedJob := *latestDeprovisionJob
		updatedJob.Message = fmt.Sprintf("Failed to get deprovision status: %v", err)
		UpdateJob(*jobs, updatedJob)
		return ctrl.Result{RequeueAfter: pollInterval}, false, nil
	}

	if status.State != latestDeprovisionJob.State || status.Message != latestDeprovisionJob.Message {
		log.Info("deprovision job status changed", "jobID", latestDeprovisionJob.JobID, "target", target,
			"oldState", latestDeprovisionJob.State, "newState", status.State)
		updatedJob := *latestDeprovisionJob
		updatedJob.State = status.State
		updatedJob.Message = status.MessageWithDetails()
		UpdateJob(*jobs, updatedJob)
	}

	if !status.State.IsTerminal() {
		return ctrl.Result{RequeueAfter: pollInterval}, false, nil
	}

	if !status.State.IsSuccessful() && latestDeprovisionJob.BlockDeletionOnFailure {
		return handleDeprovisionBackoffForTarget(ctx, provider, resource, jobs, target, latestDeprovisionJob, maxHistory, pollInterval)
	}

	return ctrl.Result{}, true, nil
}

func handleDeprovisionBackoffForTarget(ctx context.Context, provider ProvisioningProvider, resource client.Object,
	jobs *[]v1alpha1.JobStatus, target string, latestJob *v1alpha1.JobStatus, maxHistory int, pollInterval time.Duration) (ctrl.Result, bool, error) {
	log := ctrllog.FromContext(ctx)
	backoff := computeDeprovisionBackoffForTarget(*jobs, target)
	elapsed := time.Since(latestJob.Timestamp.Time)
	if elapsed >= backoff {
		log.Info("deprovision backoff elapsed, retrying", "jobID", latestJob.JobID, "target", target, "backoff", backoff)
		result, err := triggerDeprovisionJobForTarget(ctx, provider, resource, jobs, target, maxHistory, pollInterval)
		return result, false, err
	}
	remaining := backoff - elapsed
	log.Info("deprovision job failed, retrying after backoff",
		"jobID", latestJob.JobID, "target", target, "backoff", backoff, "remaining", remaining)
	return ctrl.Result{RequeueAfter: remaining}, false, nil
}

// DeprovisionTarget scopes one manager target within a multi-target
// deprovisioning lifecycle run by RunMultiTargetDeprovisioningLifecycle.
// Unlike JobTarget, there are no callbacks — RunDeprovisioningLifecycle's
// contract today is a plain (Result, done, error) with no success/failure
// hooks, so the multi-target version keeps that shape.
type DeprovisionTarget struct {
	// Name tags jobs triggered for this target (JobStatus.Target). Must be
	// non-empty and unique within a single
	// RunMultiTargetDeprovisioningLifecycle call.
	Name string

	// Provider triggers/polls deprovision jobs for this target only.
	Provider ProvisioningProvider
}

// RunMultiTargetDeprovisioningLifecycle runs the same trigger-or-poll
// deprovisioning state machine as RunDeprovisioningLifecycle independently
// for each target, against job history tagged by JobStatus.Target. Returns
// done=true only once every target has reached a terminal, non-blocking
// state (mirrors RunDeprovisioningLifecycle's single-target contract so
// finalizer removal only happens when every manager has finished tearing
// down). Requeues at the soonest interval any target still needs, and joins
// errors from all targets rather than short-circuiting on the first one, so
// one target's failure never prevents another target's teardown from being
// attempted.
//
// Every target's manager must have actually been dispatched to before being
// included here — a target that was never dispatched has no job history to
// evaluate and should be omitted by the caller rather than passed in.
func RunMultiTargetDeprovisioningLifecycle(
	ctx context.Context,
	targets []DeprovisionTarget,
	resource client.Object,
	jobs *[]v1alpha1.JobStatus,
	maxHistory int,
	pollInterval time.Duration,
) (ctrl.Result, bool, error) {
	if err := validateDeprovisionTargets(targets); err != nil {
		return ctrl.Result{}, false, err
	}

	var (
		errs    []error
		result  ctrl.Result
		allDone = true
	)

	for _, t := range targets {
		res, done, err := runDeprovisioningLifecycleForTarget(ctx, t.Provider, resource, jobs, t.Name, maxHistory, pollInterval)
		if err != nil {
			errs = append(errs, fmt.Errorf("target %q: %w", t.Name, err))
		}
		if !done {
			allDone = false
		}
		if res.RequeueAfter > 0 && (result.RequeueAfter == 0 || res.RequeueAfter < result.RequeueAfter) {
			result.RequeueAfter = res.RequeueAfter
		}
	}

	return result, allDone, errors.Join(errs...)
}

// validateDeprovisionTargets rejects target lists that would make
// RunMultiTargetDeprovisioningLifecycle's per-target dispatch ambiguous or
// impossible: at least one target, every target with a non-empty and unique
// Name, and a non-nil Provider.
func validateDeprovisionTargets(targets []DeprovisionTarget) error {
	if len(targets) == 0 {
		return errors.New("at least one DeprovisionTarget is required")
	}
	seen := make(map[string]struct{}, len(targets))
	for _, t := range targets {
		if t.Name == "" {
			return errors.New("DeprovisionTarget.Name must not be empty")
		}
		if _, dup := seen[t.Name]; dup {
			return fmt.Errorf("duplicate DeprovisionTarget.Name %q", t.Name)
		}
		seen[t.Name] = struct{}{}
		if t.Provider == nil {
			return fmt.Errorf("DeprovisionTarget %q: Provider must not be nil", t.Name)
		}
	}
	return nil
}
