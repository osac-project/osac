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
	"fmt"
	"hash/fnv"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	v1alpha1 "github.com/osac-project/osac-operator/api/v1alpha1"
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
	latestJob := FindLatestJobByType(*provState.Jobs, v1alpha1.JobTypeProvision)

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
	log := ctrllog.FromContext(ctx)
	if err := apiReader.Get(ctx, key, fresh); err != nil {
		return false
	}
	freshJobs := extract(fresh)
	freshJob := FindLatestJobByType(freshJobs, v1alpha1.JobTypeProvision)
	if HasJobID(freshJob) && !freshJob.State.IsTerminal() {
		log.Info("skipping provision trigger: non-terminal job found via API server", "jobID", freshJob.JobID, "state", freshJob.State)
		return true
	}
	return false
}

// TriggerJob triggers a new provision job and updates the jobs slice in place via State.
func TriggerJob(ctx context.Context, provider ProvisioningProvider, resource client.Object, provState *State, maxHistory int, pollInterval time.Duration) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)
	log.Info("triggering provision job")

	result, err := provider.TriggerProvision(ctx, resource)
	if err != nil {
		if rateLimitErr, ok := AsRateLimitError(err); ok {
			log.Info("provision request rate-limited, requeueing", "retryAfter", rateLimitErr.RetryAfter)
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
	}, maxHistory)

	latestJob := FindLatestJobByType(*provState.Jobs, v1alpha1.JobTypeProvision)
	log.Info("provision job triggered", "jobID", latestJob.JobID, "configVersion", latestJob.ConfigVersion)
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
	action, latestJob := EvaluateAction(provState, checkAPIServer)

	log := ctrllog.FromContext(ctx)
	trigger := func() (ctrl.Result, error) {
		prevJob := FindLatestJobByType(*provState.Jobs, v1alpha1.JobTypeProvision)
		prevJobID := ""
		if prevJob != nil {
			prevJobID = prevJob.JobID
		}
		res, err := TriggerJob(ctx, provider, resource, provState, maxHistory, pollInterval)
		if err != nil {
			return res, err
		}
		newJob := FindLatestJobByType(*provState.Jobs, v1alpha1.JobTypeProvision)
		if statusFlush != nil && newJob != nil && newJob.JobID != prevJobID {
			if flushErr := statusFlush(); flushErr != nil {
				log.Error(flushErr, "failed to flush status after job trigger; end-of-reconcile update will retry")
			}
		}
		return res, nil
	}

	switch action {
	case Skip:
		return ctrl.Result{}, nil
	case Trigger:
		return trigger()
	case Requeue:
		return ctrl.Result{RequeueAfter: pollInterval}, nil
	case Backoff:
		return HandleBackoff(ctx, provState, latestJob, trigger)
	default: // Poll
		return PollJob(ctx, provider, resource, provState, latestJob, pollInterval, callbacks)
	}
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
	log := ctrllog.FromContext(ctx)
	log.Info("triggering deprovision job")

	result, err := provider.TriggerDeprovision(ctx, resource, *jobs)
	if err != nil {
		if rateLimitErr, ok := AsRateLimitError(err); ok {
			log.Info("deprovision request rate-limited, requeueing", "retryAfter", rateLimitErr.RetryAfter)
			return ctrl.Result{RequeueAfter: rateLimitErr.RetryAfter}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to trigger deprovision: %w", err)
	}

	switch result.Action {
	case DeprovisionSkipped:
		log.Info("deprovisioning skipped by provider")
		return ctrl.Result{}, nil

	case DeprovisionWaiting:
		log.Info("waiting for provision job to terminate before deprovisioning")
		updateProvisionJobFromDeprovisionResult(jobs, result)
		return ctrl.Result{RequeueAfter: pollInterval}, nil

	case DeprovisionTriggered:
		log.Info("deprovision job triggered", "jobID", result.JobID)
		updateProvisionJobFromDeprovisionResult(jobs, result)
		*jobs = AppendJob(*jobs, v1alpha1.JobStatus{
			JobID:                  result.JobID,
			Type:                   v1alpha1.JobTypeDeprovision,
			State:                  v1alpha1.JobStatePending,
			Message:                "Deprovision job triggered",
			Timestamp:              metav1.NewTime(time.Now().UTC()),
			BlockDeletionOnFailure: result.BlockDeletionOnFailure,
		}, maxHistory)
		return ctrl.Result{RequeueAfter: pollInterval}, nil

	default:
		return ctrl.Result{}, fmt.Errorf("unknown deprovision action: %v", result.Action)
	}
}

// updateProvisionJobFromDeprovisionResult updates the latest provision job status
// from the deprovision result, if provided by the provider.
func updateProvisionJobFromDeprovisionResult(jobs *[]v1alpha1.JobStatus, result *DeprovisionResult) {
	if result.ProvisionJobStatus == nil {
		return
	}
	latestProvisionJob := FindLatestJobByType(*jobs, v1alpha1.JobTypeProvision)
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
	latestDeprovisionJob := FindLatestJobByType(*jobs, v1alpha1.JobTypeDeprovision)

	if !HasJobID(latestDeprovisionJob) {
		result, err := TriggerDeprovisionJob(ctx, provider, resource, jobs, maxHistory, pollInterval)
		return result, false, err
	}

	return PollDeprovisionJob(ctx, provider, resource, jobs, latestDeprovisionJob, maxHistory, pollInterval)
}

// PollDeprovisionJob polls the status of an existing deprovision job.
// Returns (result, done, error) where done=true means the job reached terminal state
// and the controller can proceed with finalizer removal.
// When a deprovision job fails with BlockDeletionOnFailure, the function retries
// after exponential backoff rather than blocking forever.
func PollDeprovisionJob(ctx context.Context, provider ProvisioningProvider, resource client.Object,
	jobs *[]v1alpha1.JobStatus, latestDeprovisionJob *v1alpha1.JobStatus, maxHistory int, pollInterval time.Duration) (ctrl.Result, bool, error) {
	log := ctrllog.FromContext(ctx)

	if latestDeprovisionJob.State.IsTerminal() {
		if !latestDeprovisionJob.State.IsSuccessful() && latestDeprovisionJob.BlockDeletionOnFailure {
			return handleDeprovisionBackoff(ctx, provider, resource, jobs, latestDeprovisionJob, maxHistory, pollInterval)
		}
		return ctrl.Result{}, true, nil
	}

	log.Info("polling deprovision job status", "jobID", latestDeprovisionJob.JobID, "currentState", latestDeprovisionJob.State)
	status, err := provider.GetDeprovisionStatus(ctx, resource, latestDeprovisionJob.JobID)
	if err != nil {
		log.Error(err, "failed to get deprovision status", "jobID", latestDeprovisionJob.JobID)
		updatedJob := *latestDeprovisionJob
		updatedJob.Message = fmt.Sprintf("Failed to get deprovision status: %v", err)
		UpdateJob(*jobs, updatedJob)
		return ctrl.Result{RequeueAfter: pollInterval}, false, nil
	}

	if status.State != latestDeprovisionJob.State || status.Message != latestDeprovisionJob.Message {
		log.Info("deprovision job status changed", "jobID", latestDeprovisionJob.JobID,
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
		return handleDeprovisionBackoff(ctx, provider, resource, jobs, latestDeprovisionJob, maxHistory, pollInterval)
	}

	return ctrl.Result{}, true, nil
}

func handleDeprovisionBackoff(ctx context.Context, provider ProvisioningProvider, resource client.Object,
	jobs *[]v1alpha1.JobStatus, latestJob *v1alpha1.JobStatus, maxHistory int, pollInterval time.Duration) (ctrl.Result, bool, error) {
	log := ctrllog.FromContext(ctx)
	backoff := ComputeDeprovisionBackoff(*jobs)
	elapsed := time.Since(latestJob.Timestamp.Time)
	if elapsed >= backoff {
		log.Info("deprovision backoff elapsed, retrying", "jobID", latestJob.JobID, "backoff", backoff)
		result, err := TriggerDeprovisionJob(ctx, provider, resource, jobs, maxHistory, pollInterval)
		return result, false, err
	}
	remaining := backoff - elapsed
	log.Info("deprovision job failed, retrying after backoff",
		"jobID", latestJob.JobID, "backoff", backoff, "remaining", remaining)
	return ctrl.Result{RequeueAfter: remaining}, false, nil
}
