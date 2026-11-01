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

package networktask

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/clock"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/aap"
)

// VirtualNetworkIDLabel indexes Subnets that share a VirtualNetwork task slot.
const VirtualNetworkIDLabel = "osac.openshift.io/virtual-network-id"

// AAP job statuses.
const (
	aapNew        = "new"
	aapPending    = "pending"
	aapWaiting    = "waiting"
	aapRunning    = "running"
	aapSuccessful = "successful"
	aapFailed     = "failed"
	aapError      = "error"
	aapCanceled   = "canceled"
)

// Timing rules of contract §4.7 and its edge cases.
const (
	// NotStartedTimeout is how long a job may stay new, pending or waiting.
	NotStartedTimeout = 30 * time.Minute
	// LostLaunchTimeout is how long a Running entry with an empty jobID waits
	// for its job to appear under its label before it is launched again.
	LostLaunchTimeout = 2 * time.Minute
	// DefaultPollInterval is how often a running job is read.
	DefaultPollInterval = 30 * time.Second
	// cancelPollInterval is how often a job cancelled for a delete is read.
	cancelPollInterval = 10 * time.Second

	// MessageTimedOut is the entry message of a job AAP ended at its timeout.
	MessageTimedOut = "job timed out after 60 minutes"
	// MessageNotStarted is the entry message of a job AAP never started. It is
	// written to the Running entry before OSAC cancels the job, so that AAP's
	// canceled status is not taken for a cancel by hand, even after a restart.
	MessageNotStarted = "AAP did not start the job within 30 minutes"
	// MessageRerun is the message of an entry marked for a run with a new input.
	MessageRerun = "waiting to run with the updated input"
)

// AAPClient is the part of the AAP client the engine uses.
type AAPClient interface {
	LaunchJobTemplate(ctx context.Context, req aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error)
	GetJob(ctx context.Context, jobID string) (*aap.Job, error)
	CancelJob(ctx context.Context, jobID string) error
	ListJobsByLabel(ctx context.Context, labelName string) ([]aap.Job, error)
	EnsureLabel(ctx context.Context, organization, name string) (int, error)
}

// Config configures an Engine.
type Config struct {
	// TemplatePrefix is the AAP job template prefix (OSAC_AAP_TEMPLATE_PREFIX).
	TemplatePrefix string
	// Organization is the AAP organization (name or ID) where job labels are created.
	Organization string
	// ProvisioningEnabled is global.networking.provisioningEnabled. When false
	// no networking job may run.
	ProvisioningEnabled bool
	// PollInterval is how often a running job is read; DefaultPollInterval when zero.
	PollInterval time.Duration
}

// Engine launches and tracks network manager tasks on CRs (contract §4.3-§4.7).
// One Engine is shared by all controllers; it keeps in-memory clocks and
// backoff counters keyed by CR UID, task and manager.
type Engine struct {
	client    client.Client
	apiReader client.Reader
	aap       AAPClient
	cfg       Config
	clock     clock.PassiveClock

	mu      sync.Mutex
	tracked map[trackKey]*tracking

	vnLocks keyedMutex
}

type trackKey struct {
	uid     types.UID
	task    string
	manager v1alpha1.NetworkJobManager
}

type tracking struct {
	attempt    int32
	observedAt time.Time // launch or first observation of the attempt's job
	emptySince time.Time // first time the attempt was seen Running without jobID
	failures   int       // consecutive failures since the last success or new input
	retryAt    time.Time
}

// New creates an Engine. c writes status (resourceVersion-conditional
// updates); apiReader reads from the API server where a stale cache must not
// be used.
func New(c client.Client, apiReader client.Reader, aapClient AAPClient, cfg Config) *Engine {
	if cfg.PollInterval == 0 {
		cfg.PollInterval = DefaultPollInterval
	}
	return &Engine{
		client:    c,
		apiReader: apiReader,
		aap:       aapClient,
		cfg:       cfg,
		clock:     clock.RealClock{},
		tracked:   map[trackKey]*tracking{},
	}
}

// WithClock replaces the engine clock (tests).
func (e *Engine) WithClock(c clock.PassiveClock) *Engine {
	e.clock = c
	return e
}

// ProvisioningEnabled reports global.networking.provisioningEnabled.
func (e *Engine) ProvisioningEnabled() bool { return e.cfg.ProvisioningEnabled }

// APIReader returns the reader that bypasses the cache.
func (e *Engine) APIReader() client.Reader { return e.apiReader }

// Request describes one task of one resource on one manager.
type Request struct {
	// Task is the task name.
	Task string
	// Manager is the manager type; none for a job that is not a manager task.
	Manager v1alpha1.NetworkJobManager
	// ManagerRef is the recorded manager; required unless Manager is none.
	ManagerRef *v1alpha1.NetworkManagerRef
	// Kind is the CR kind, used for the API ID label lookup (APIID).
	Kind string
	// APIID overrides the API ID used in the job label.
	APIID string
	// Template overrides the job template name; required for manager none.
	// Default: TemplateName(prefix, Task).
	Template string
	// Vars is osac_job_vars without manager and task: the stored input plus
	// the launch-time keys (see LoadInput and WithLaunchKeys).
	Vars map[string]any
	// BeforeLaunch mutates the object before the status write that records the
	// first Running entry, for example StoreInput or status.ipam.
	BeforeLaunch func() error
	// Accept validates the result of a successful job and applies its typed
	// outputs; nil means the task has no typed output (AcceptNoOutput).
	Accept AcceptFunc
	// Slot reserves launch and serializes acceptance/status per VirtualNetwork.
	Slot *SubnetSlot
	// SerializeVNI serializes this task's acceptance and its status write with
	// every other VNI acceptance (EP #427: one operator-wide lock).
	SerializeVNI bool
	// TargetDeleting is true when the target workload (or the attachment)
	// has a deletionTimestamp: no non-cleanup job starts then, and a running
	// one is cancelled in AAP and becomes Cancelled once AAP reports its end.
	TargetDeleting bool
	// HoldLaunch starts no job in this Run, for a caller whose Vars are not
	// complete for a launch: Run only tracks a job in progress. An entry that
	// would be launched again from Running becomes Cancelled, so that LaunchDue
	// reports it and the caller can launch it with complete Vars on its next Run.
	HoldLaunch bool
}

// Outcome is the state of a task after Run.
type Outcome struct {
	// Entry is the task's entry after Run (zero when no job was recorded).
	Entry v1alpha1.NetworkJobStatus
	// Blocked is true when no job was launched because the resource or its
	// target is being deleted.
	Blocked bool
	// WaitingForSlot is true when another Subnet of the VirtualNetwork holds the slot.
	WaitingForSlot bool
	// RequeueAfter is when the controller should reconcile again; zero when
	// the entry is final (Succeeded, Failed or Cancelled).
	RequeueAfter time.Duration
}

// Succeeded reports whether the entry succeeded.
func (o Outcome) Succeeded() bool { return o.Entry.State == v1alpha1.NetworkJobStateSucceeded }

// Failed reports whether the entry failed finally.
func (o Outcome) Failed() bool { return o.Entry.State == v1alpha1.NetworkJobStateFailed }

// Run advances one task: it launches the first or next attempt when allowed,
// tracks a running job, maps its end to the entry state, validates its result
// and writes status. Call it on every reconcile until the entry is
// Succeeded or Failed. A conflict on a status write is returned as an error:
// the in-memory object is then stale and must not be written; requeue.
func (e *Engine) Run(ctx context.Context, obj v1alpha1.NetworkTaskObject, req Request) (Outcome, error) {
	if err := ValidateTask(req.Task, req.Manager); err != nil {
		return Outcome{}, err
	}
	if req.Manager != v1alpha1.NetworkJobManagerNone && req.ManagerRef == nil {
		return Outcome{}, fmt.Errorf("task %s on the %s manager: no manager recorded", req.Task, req.Manager)
	}
	if req.Manager == v1alpha1.NetworkJobManagerNone && req.Template == "" {
		return Outcome{}, fmt.Errorf("task %s with manager none needs a job template", req.Task)
	}
	if req.Manager != v1alpha1.NetworkJobManagerNone && !e.cfg.ProvisioningEnabled {
		return Outcome{}, errors.New("provider networking is disabled: no networking task may run")
	}
	entry := FindJob(obj.GetNetworkJobs(), req.Task, req.Manager)
	if entry == nil {
		return e.launch(ctx, obj, req, 1)
	}
	current := *entry
	switch current.State {
	case v1alpha1.NetworkJobStateSucceeded, v1alpha1.NetworkJobStateFailed:
		return Outcome{Entry: current}, nil
	case v1alpha1.NetworkJobStateCancelled:
		if e.blocked(obj, req) {
			return Outcome{Entry: current, Blocked: true}, nil
		}
		// Cancelled with no delete pending (for example by hand in AAP): run again at once.
		return e.launch(ctx, obj, req, current.Attempt+1)
	case v1alpha1.NetworkJobStateRetrying:
		if e.blocked(obj, req) {
			current.State = v1alpha1.NetworkJobStateCancelled
			return e.write(ctx, obj, current, 0)
		}
		wait := e.retryWait(obj, current)
		if wait > 0 {
			return e.refreshRetryMessage(ctx, obj, current, wait)
		}
		return e.launch(ctx, obj, req, current.Attempt+1)
	default:
		return e.poll(ctx, obj, req, current)
	}
}

// LaunchDue reports whether Run would start a job now for a task whose entry
// is not Running: no entry yet, a Cancelled entry, or a Retrying entry whose
// wait has ended. It does not consider a pending deletion: call it for a live
// object.
func (e *Engine) LaunchDue(obj v1alpha1.NetworkTaskObject, task string, manager v1alpha1.NetworkJobManager) bool {
	entry := FindJob(obj.GetNetworkJobs(), task, manager)
	if entry == nil {
		return true
	}
	switch entry.State {
	case v1alpha1.NetworkJobStateCancelled:
		return true
	case v1alpha1.NetworkJobStateRetrying:
		return e.retryWait(obj, *entry) <= 0
	default:
		return false
	}
}

func (e *Engine) blocked(obj v1alpha1.NetworkTaskObject, req Request) bool {
	return !IsCleanupTask(req.Task) && (obj.GetDeletionTimestamp() != nil || req.TargetDeleting)
}

func (e *Engine) label(obj v1alpha1.NetworkTaskObject, req Request, attempt int32) string {
	id := req.APIID
	if id == "" {
		id = APIID(obj, req.Kind)
	}
	return JobLabel(req.Task, req.Manager, id, attempt)
}

func (e *Engine) state(obj v1alpha1.NetworkTaskObject, task string, manager v1alpha1.NetworkJobManager, attempt int32) *tracking {
	e.mu.Lock()
	defer e.mu.Unlock()
	key := trackKey{uid: obj.GetUID(), task: task, manager: manager}
	t, ok := e.tracked[key]
	if !ok {
		// After a restart the failure count is seeded from the attempt (decision 7).
		t = &tracking{failures: -1}
		e.tracked[key] = t
	}
	if t.attempt != attempt {
		t.attempt = attempt
		t.observedAt = time.Time{}
		t.emptySince = time.Time{}
	}
	return t
}

// launch records attempt as Running, then launches the job (contract §4.7).
func (e *Engine) launch(ctx context.Context, obj v1alpha1.NetworkTaskObject, req Request, attempt int32) (Outcome, error) {
	log := ctrllog.FromContext(ctx)
	if e.blocked(obj, req) {
		return Outcome{Blocked: true}, nil
	}
	if req.HoldLaunch {
		return Outcome{RequeueAfter: e.cfg.PollInterval}, nil
	}
	var unlockSlot func()
	if req.Slot != nil {
		unlock, held, err := e.takeSlot(ctx, obj, req.Slot)
		if err != nil {
			return Outcome{}, err
		}
		unlockSlot = unlock
		defer func() {
			if unlockSlot != nil {
				unlockSlot()
			}
		}()
		if held {
			return Outcome{WaitingForSlot: true, RequeueAfter: 15 * time.Second}, nil
		}
	}
	entry := v1alpha1.NetworkJobStatus{
		Task:    req.Task,
		Manager: req.Manager,
		Attempt: attempt,
		State:   v1alpha1.NetworkJobStateRunning,
	}
	setJob(obj, entry)
	if req.BeforeLaunch != nil {
		if err := req.BeforeLaunch(); err != nil {
			return Outcome{}, err
		}
	}
	// The status update is conditional on the resourceVersion the object was
	// read at: a conflict, or a stale cached object, launches nothing.
	if err := e.client.Status().Update(ctx, obj); err != nil {
		return Outcome{}, err
	}
	if unlockSlot != nil {
		unlockSlot()
		unlockSlot = nil
	}
	t := e.state(obj, req.Task, req.Manager, attempt)
	now := e.clock.Now()
	t.observedAt = now
	log.Info("Launching network task", "task", req.Task, "manager", req.Manager, "attempt", attempt)
	return e.launchJob(ctx, obj, req, entry, t)
}

// launchJob calls AAP for an entry already recorded as Running and writes the jobID.
func (e *Engine) launchJob(ctx context.Context, obj v1alpha1.NetworkTaskObject, req Request, entry v1alpha1.NetworkJobStatus, t *tracking) (Outcome, error) {
	log := ctrllog.FromContext(ctx)
	label := e.label(obj, req, entry.Attempt)
	t.observedAt = e.clock.Now()
	labelID, err := e.aap.EnsureLabel(ctx, e.cfg.Organization, label)
	if err != nil {
		t.emptySince = e.clock.Now()
		log.Error(err, "Failed to ensure the AAP job label", "label", label)
		return Outcome{Entry: entry, RequeueAfter: e.cfg.PollInterval}, nil
	}
	template := req.Template
	if template == "" {
		template = TemplateName(e.cfg.TemplatePrefix, req.Task)
	}
	vars := make(map[string]any)
	for k, v := range req.Vars {
		vars[k] = v
	}
	vars["task"] = req.Task
	if req.Manager != v1alpha1.NetworkJobManagerNone {
		vars["manager"] = ManagerVars(req.ManagerRef, req.Manager)
	}
	resp, err := e.aap.LaunchJobTemplate(ctx, aap.LaunchJobTemplateRequest{
		TemplateName: template,
		ExtraVars:    map[string]any{"osac_job_vars": vars},
		Labels:       []int{labelID},
	})
	if err != nil {
		// The job may or may not exist: the entry stays Running with an empty
		// jobID and is resolved by its label.
		t.emptySince = e.clock.Now()
		log.Error(err, "Failed to launch the AAP job", "template", template, "label", label)
		return Outcome{Entry: entry, RequeueAfter: e.cfg.PollInterval}, nil
	}
	entry.JobID = strconv.Itoa(resp.JobID)
	return e.write(ctx, obj, entry, e.cfg.PollInterval)
}

// takeSlot checks the current namespace's labeled siblings under the shared
// VirtualNetwork lock. The caller releases it after the Running reservation.
func (e *Engine) takeSlot(ctx context.Context, obj v1alpha1.NetworkTaskObject, slot *SubnetSlot) (func(), bool, error) {
	unlock := e.vnLocks.acquire(slot.VirtualNetworkID)
	list := &v1alpha1.SubnetList{}
	if err := e.apiReader.List(ctx, list, client.InNamespace(obj.GetNamespace()), client.MatchingLabels{VirtualNetworkIDLabel: slot.VirtualNetworkID}); err != nil {
		unlock()
		return nil, false, fmt.Errorf("listing Subnets for the VirtualNetwork slot: %w", err)
	}
	return unlock, slotHeld(obj, list.Items, slot.VirtualNetworkID), nil
}

// write stores the entry in status.
func (e *Engine) write(ctx context.Context, obj v1alpha1.NetworkTaskObject, entry v1alpha1.NetworkJobStatus, requeue time.Duration) (Outcome, error) {
	setJob(obj, entry)
	if err := e.client.Status().Update(ctx, obj); err != nil {
		return Outcome{}, err
	}
	return Outcome{Entry: entry, RequeueAfter: requeue}, nil
}

// poll tracks a Running entry.
func (e *Engine) poll(ctx context.Context, obj v1alpha1.NetworkTaskObject, req Request, entry v1alpha1.NetworkJobStatus) (Outcome, error) {
	log := ctrllog.FromContext(ctx)
	t := e.state(obj, req.Task, req.Manager, entry.Attempt)
	now := e.clock.Now()
	if t.observedAt.IsZero() {
		t.observedAt = now
	}
	if entry.JobID == "" {
		found, err := e.resolveByLabel(ctx, obj, req, entry.Attempt)
		if err != nil {
			log.Error(err, "Failed to look up the job by its label", "task", req.Task, "manager", req.Manager)
			return Outcome{Entry: entry, RequeueAfter: e.cfg.PollInterval}, nil
		}
		if found != "" {
			entry.JobID = found
			return e.write(ctx, obj, entry, time.Second)
		}
		if t.emptySince.IsZero() {
			t.emptySince = now
		}
		if wait := LostLaunchTimeout - now.Sub(t.emptySince); wait > 0 {
			return Outcome{Entry: entry, RequeueAfter: wait}, nil
		}
		if e.blocked(obj, req) {
			entry.State = v1alpha1.NetworkJobStateCancelled
			return e.write(ctx, obj, entry, 0)
		}
		t.emptySince = time.Time{}
		if req.HoldLaunch {
			entry.State = v1alpha1.NetworkJobStateCancelled
			return e.write(ctx, obj, entry, e.cfg.PollInterval)
		}
		log.Info("Job of a Running entry not found by its label; launching it", "task", req.Task, "manager", req.Manager, "attempt", entry.Attempt)
		return e.launchJob(ctx, obj, req, entry, t)
	}
	job, err := e.aap.GetJob(ctx, entry.JobID)
	if err != nil {
		var notFound *aap.NotFoundError
		if errors.As(err, &notFound) {
			if e.blocked(obj, req) {
				entry.State = v1alpha1.NetworkJobStateCancelled
				return e.write(ctx, obj, entry, 0)
			}
			return e.fail(ctx, obj, req, entry, true, fmt.Sprintf("AAP has no record of job %s", entry.JobID))
		}
		log.Error(err, "Failed to read the AAP job", "jobID", entry.JobID)
		return Outcome{Entry: entry, RequeueAfter: e.cfg.PollInterval}, nil
	}
	switch job.Status {
	case aapNew, aapPending, aapWaiting, aapRunning:
		if e.blocked(obj, req) {
			// The target is being deleted: cancel the job (BMaaS Delete step 3,
			// CaaS automatic ExternalIPAttachment delete); the entry becomes
			// Cancelled once AAP reports its end.
			e.cancelJob(ctx, entry.JobID)
			return Outcome{Entry: entry, RequeueAfter: cancelPollInterval}, nil
		}
		if job.Status == aapRunning {
			return Outcome{Entry: entry, RequeueAfter: e.cfg.PollInterval}, nil
		}
		return e.cancelNotStarted(ctx, obj, entry, now.Sub(t.observedAt))
	}
	// The job ended.
	if e.blocked(obj, req) {
		entry.State = v1alpha1.NetworkJobStateCancelled
		return e.write(ctx, obj, entry, 0)
	}
	switch job.Status {
	case aapSuccessful:
		return e.succeed(ctx, obj, req, entry, job)
	case aapFailed:
		artifacts := readArtifacts(job.Artifacts)
		if isTimeout(job) {
			return e.fail(ctx, obj, req, entry, true, MessageTimedOut)
		}
		if artifacts.osacError != nil {
			return e.fail(ctx, obj, req, entry, artifacts.osacError.Retryable, artifacts.osacError.Message)
		}
		message := fmt.Sprintf("AAP job %s failed", entry.JobID)
		if job.JobExplanation != "" {
			message += ": " + job.JobExplanation
		}
		if artifacts.malformedError {
			message += "; malformed osac_error"
		}
		return e.fail(ctx, obj, req, entry, false, message)
	case aapError:
		message := fmt.Sprintf("AAP job %s ended with an error", entry.JobID)
		if job.JobExplanation != "" {
			message += ": " + job.JobExplanation
		}
		return e.fail(ctx, obj, req, entry, true, message)
	case aapCanceled:
		if entry.Message == MessageNotStarted {
			return e.fail(ctx, obj, req, entry, true, MessageNotStarted)
		}
		// Cancelled by hand with no delete pending: launch the next attempt at once.
		if req.HoldLaunch {
			entry.State = v1alpha1.NetworkJobStateCancelled
			return e.write(ctx, obj, entry, e.cfg.PollInterval)
		}
		log.Info("AAP job was cancelled outside OSAC; launching the next attempt", "jobID", entry.JobID)
		return e.launch(ctx, obj, req, entry.Attempt+1)
	default:
		return Outcome{Entry: entry, RequeueAfter: e.cfg.PollInterval}, nil
	}
}

// cancelNotStarted cancels a job AAP has not started for NotStartedTimeout
// (contract §4.7). The entry is first marked with MessageNotStarted in status,
// so that AAP's canceled status maps to a retry and not to a cancel by hand,
// even after a restart; the cancel is repeated until AAP reports the end.
func (e *Engine) cancelNotStarted(ctx context.Context, obj v1alpha1.NetworkTaskObject, entry v1alpha1.NetworkJobStatus, waited time.Duration) (Outcome, error) {
	if entry.Message != MessageNotStarted {
		if waited < NotStartedTimeout {
			return Outcome{Entry: entry, RequeueAfter: e.cfg.PollInterval}, nil
		}
		entry.Message = MessageNotStarted
		if _, err := e.write(ctx, obj, entry, 0); err != nil {
			return Outcome{}, err
		}
	}
	e.cancelJob(ctx, entry.JobID)
	return Outcome{Entry: entry, RequeueAfter: e.cfg.PollInterval}, nil
}

// cancelJob asks AAP to cancel a job; a job that can no longer be cancelled
// (405) is not an error, other errors are logged and the cancel is repeated
// on the next reconcile.
func (e *Engine) cancelJob(ctx context.Context, jobID string) {
	if err := e.aap.CancelJob(ctx, jobID); err != nil {
		var notAllowed *aap.MethodNotAllowedError
		if !errors.As(err, &notAllowed) {
			ctrllog.FromContext(ctx).Error(err, "Failed to cancel an AAP job", "jobID", jobID)
		}
	}
}

func isTimeout(job *aap.Job) bool {
	// AAP sets this explanation when a job exceeds its template timeout.
	return strings.Contains(job.JobExplanation, "Job terminated due to timeout")
}

// resolveByLabel finds the job of an attempt by its label: it returns the
// lowest job ID and cancels the others.
func (e *Engine) resolveByLabel(ctx context.Context, obj v1alpha1.NetworkTaskObject, req Request, attempt int32) (string, error) {
	jobs, err := e.aap.ListJobsByLabel(ctx, e.label(obj, req, attempt))
	if err != nil {
		return "", err
	}
	if len(jobs) == 0 {
		return "", nil
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].ID < jobs[j].ID })
	for _, extra := range jobs[1:] {
		if err := e.aap.CancelJob(ctx, strconv.Itoa(extra.ID)); err != nil {
			ctrllog.FromContext(ctx).Info("Failed to cancel a duplicate job", "jobID", extra.ID, "error", err.Error())
		}
	}
	return strconv.Itoa(jobs[0].ID), nil
}

// succeed validates the result of a successful job.
func (e *Engine) succeed(ctx context.Context, obj v1alpha1.NetworkTaskObject, req Request, entry v1alpha1.NetworkJobStatus, job *aap.Job) (Outcome, error) {
	artifacts := readArtifacts(job.Artifacts)
	if req.SerializeVNI {
		unlockVNI := e.vnLocks.acquire(vniAcceptanceLock)
		defer unlockVNI()
	}
	if req.Slot != nil {
		unlock := e.vnLocks.acquire(req.Slot.VirtualNetworkID)
		defer unlock()
	}
	var err error
	if artifacts.malformedResult {
		err = Reject("osac_result must be an object")
	} else {
		result := artifacts.result
		if result == nil {
			result = Result{}
		}
		accept := req.Accept
		if accept == nil {
			accept = AcceptNoOutput
		}
		err = accept(ctx, result)
	}
	var reject *RejectError
	var retry *RetryError
	switch {
	case err == nil:
		t := e.state(obj, req.Task, req.Manager, entry.Attempt)
		t.failures = 0
		entry.State = v1alpha1.NetworkJobStateSucceeded
		entry.Message = ""
		return e.write(ctx, obj, entry, 0)
	case errors.As(err, &reject):
		message := fmt.Sprintf("OSAC rejected the %s result: %s", req.Task, reject.Reason)
		return e.fail(ctx, obj, req, entry, false, message)
	case errors.As(err, &retry):
		message := fmt.Sprintf("OSAC deferred the %s result: %s", req.Task, retry.Reason)
		return e.fail(ctx, obj, req, entry, true, message)
	default:
		return Outcome{}, err
	}
}

// fail records a failed job: Retrying with backoff when retryable (always for
// cleanup tasks), else Failed.
func (e *Engine) fail(ctx context.Context, obj v1alpha1.NetworkTaskObject, req Request, entry v1alpha1.NetworkJobStatus, retryable bool, message string) (Outcome, error) {
	if !retryable && !IsCleanupTask(req.Task) {
		entry.State = v1alpha1.NetworkJobStateFailed
		entry.Message = message
		return e.write(ctx, obj, entry, 0)
	}
	t := e.state(obj, req.Task, req.Manager, entry.Attempt)
	if t.failures < 0 {
		t.failures = int(entry.Attempt) - 1
	}
	t.failures++
	wait := Backoff(req.Task, t.failures, entry.Attempt)
	t.retryAt = e.clock.Now().Add(wait)
	entry.State = v1alpha1.NetworkJobStateRetrying
	entry.Message = RetryMessage(entry, message, t.retryAt)
	return e.write(ctx, obj, entry, wait)
}

// Backoff is the wait after the n-th consecutive failure: min(2m * 2^(n-1),
// 30m); query_dhcp_lease waits 30 s, and 5 min from its 20th attempt.
func Backoff(task string, failures int, attempt int32) time.Duration {
	if task == QueryDHCPLease {
		if attempt >= 20 {
			return 5 * time.Minute
		}
		return 30 * time.Second
	}
	if failures < 1 {
		failures = 1
	}
	wait := 2 * time.Minute
	for i := 1; i < failures && wait < 30*time.Minute; i++ {
		wait *= 2
	}
	if wait > 30*time.Minute {
		wait = 30 * time.Minute
	}
	return wait
}

// RetryMessage is the entry message while a retry is scheduled.
func RetryMessage(entry v1alpha1.NetworkJobStatus, message string, at time.Time) string {
	return fmt.Sprintf("%s (%s) attempt %d failed: %s; next retry at %s",
		entry.Task, entry.Manager, entry.Attempt, message, at.UTC().Format(time.RFC3339))
}

// retryWait returns how long a Retrying entry still waits. After a restart
// the wait restarts (contract edge case), counted from now.
func (e *Engine) retryWait(obj v1alpha1.NetworkTaskObject, entry v1alpha1.NetworkJobStatus) time.Duration {
	t := e.state(obj, entry.Task, entry.Manager, entry.Attempt)
	now := e.clock.Now()
	if t.retryAt.IsZero() {
		if entry.Message == MessageRerun {
			return 0
		}
		if t.failures < 0 {
			t.failures = int(entry.Attempt)
		}
		t.retryAt = now.Add(Backoff(entry.Task, t.failures, entry.Attempt))
	}
	return t.retryAt.Sub(now)
}

// refreshRetryMessage rewrites a stale next retry time (after a restart).
func (e *Engine) refreshRetryMessage(ctx context.Context, obj v1alpha1.NetworkTaskObject, entry v1alpha1.NetworkJobStatus, wait time.Duration) (Outcome, error) {
	t := e.state(obj, entry.Task, entry.Manager, entry.Attempt)
	suffix := "; next retry at " + t.retryAt.UTC().Format(time.RFC3339)
	idx := strings.LastIndex(entry.Message, "; next retry at ")
	if idx < 0 || entry.Message[idx:] == suffix {
		return Outcome{Entry: entry, RequeueAfter: wait}, nil
	}
	entry.Message = entry.Message[:idx] + suffix
	return e.write(ctx, obj, entry, wait)
}

// MarkForRerun records that a task must run again with a new stored input
// (a SecurityGroup rule update): the entry becomes Retrying with
// MessageRerun and the next Run launches the next attempt at once. Call it in
// the same status write that stores the new input.
func (e *Engine) MarkForRerun(obj v1alpha1.NetworkTaskObject, task string, manager v1alpha1.NetworkJobManager) {
	entry := FindJob(obj.GetNetworkJobs(), task, manager)
	if entry == nil {
		return
	}
	updated := *entry
	updated.State = v1alpha1.NetworkJobStateRetrying
	updated.Message = MessageRerun
	setJob(obj, updated)
	t := e.state(obj, task, manager, updated.Attempt)
	t.failures = 0
	t.retryAt = e.clock.Now()
}

// Cancel sets a Retrying (or not yet launched Running) entry to Cancelled
// in memory, for a retry that will never launch, for example the Kubernetes
// run of a SecurityGroup update whose Fabric run failed. The caller writes status.
func Cancel(obj v1alpha1.NetworkTaskObject, task string, manager v1alpha1.NetworkJobManager) {
	entry := FindJob(obj.GetNetworkJobs(), task, manager)
	if entry == nil || entry.State != v1alpha1.NetworkJobStateRetrying {
		return
	}
	updated := *entry
	updated.State = v1alpha1.NetworkJobStateCancelled
	setJob(obj, updated)
}

// CancelForDelete cancels every job of the object that is not a cleanup task
// (contract §4.7 Cancel on delete): a Retrying entry becomes Cancelled at
// once; a running job is cancelled in AAP and its entry becomes Cancelled once
// AAP reports it ended, whatever its status, and its result is ignored. A
// Running entry whose job is not found by its label for LostLaunchTimeout
// becomes Cancelled with an empty jobID; on every later call its label is
// looked up again, and a job AAP created after all is tracked and cancelled
// like any other (delete wins). It returns true when no such job runs any
// more, so the delete can start.
func (e *Engine) CancelForDelete(ctx context.Context, obj v1alpha1.NetworkTaskObject, kind string) (bool, time.Duration, error) {
	changed := false
	done := true
	for _, entry := range append([]v1alpha1.NetworkJobStatus(nil), obj.GetNetworkJobs()...) {
		if IsCleanupTask(entry.Task) {
			continue
		}
		entryChanged, entryDone := e.cancelEntryForDelete(ctx, obj, kind, entry)
		changed = changed || entryChanged
		done = done && entryDone
	}
	if changed {
		if err := e.client.Status().Update(ctx, obj); err != nil {
			return false, 0, err
		}
	}
	if done {
		return true, 0, nil
	}
	return false, cancelPollInterval, nil
}

// cancelEntryForDelete advances the cancel of one non-cleanup entry in memory.
// It reports whether the entry changed and whether its job no longer runs.
func (e *Engine) cancelEntryForDelete(ctx context.Context, obj v1alpha1.NetworkTaskObject, kind string, entry v1alpha1.NetworkJobStatus) (bool, bool) {
	log := ctrllog.FromContext(ctx)
	cancelled := func() (bool, bool) {
		entry.State = v1alpha1.NetworkJobStateCancelled
		setJob(obj, entry)
		return true, true
	}
	changed := false
	switch {
	case entry.State == v1alpha1.NetworkJobStateRetrying:
		return cancelled()
	case entry.State == v1alpha1.NetworkJobStateRunning:
	case entry.State == v1alpha1.NetworkJobStateCancelled && entry.JobID == "":
		// Cancelled while its job was absent: a job AAP creates later is cancelled too.
	default:
		return false, true
	}
	if entry.JobID == "" {
		req := Request{Task: entry.Task, Manager: entry.Manager, Kind: kind}
		found, err := e.resolveByLabel(ctx, obj, req, entry.Attempt)
		if err != nil {
			log.Error(err, "Failed to look up a job by its label", "task", entry.Task)
			return false, false
		}
		if found == "" {
			if entry.State == v1alpha1.NetworkJobStateCancelled {
				return false, true
			}
			t := e.state(obj, entry.Task, entry.Manager, entry.Attempt)
			now := e.clock.Now()
			if t.emptySince.IsZero() {
				t.emptySince = now
			}
			if now.Sub(t.emptySince) < LostLaunchTimeout {
				return false, false
			}
			return cancelled()
		}
		entry.JobID = found
		entry.State = v1alpha1.NetworkJobStateRunning
		setJob(obj, entry)
		changed = true
	}
	job, err := e.aap.GetJob(ctx, entry.JobID)
	if err != nil {
		var notFound *aap.NotFoundError
		if errors.As(err, &notFound) {
			return cancelled()
		}
		log.Error(err, "Failed to read a job to cancel", "jobID", entry.JobID)
		return changed, false
	}
	switch job.Status {
	case aapSuccessful, aapFailed, aapError, aapCanceled:
		return cancelled()
	default:
		e.cancelJob(ctx, entry.JobID)
		return changed, false
	}
}

// Forget drops the in-memory state of an object, for example after its
// finalizer was removed.
func (e *Engine) Forget(uid types.UID) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for key := range e.tracked {
		if key.uid == uid {
			delete(e.tracked, key)
		}
	}
}

// IsConflict reports whether err is a status write conflict.
func IsConflict(err error) bool { return apierrors.IsConflict(err) }
