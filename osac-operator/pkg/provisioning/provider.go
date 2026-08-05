// Package provisioning provides an abstraction layer for infrastructure provisioning through multiple backends.
package provisioning

import (
	"context"
	"fmt"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac-operator/api/v1alpha1"
)

// ProvisionResult contains the result of triggering a provision operation.
type ProvisionResult struct {
	// JobID is the identifier for the triggered job
	JobID string

	// InitialState is the initial state of the job (typically Pending or Running)
	InitialState v1alpha1.JobState

	// Message is a human-readable status message
	Message string
}

// DeprovisionAction represents the action taken by a provider when attempting to deprovision.
type DeprovisionAction string

const (
	// DeprovisionTriggered means deprovisioning was started and controller should poll status
	DeprovisionTriggered DeprovisionAction = "triggered"

	// DeprovisionWaiting means provider is not ready yet and controller should requeue
	DeprovisionWaiting DeprovisionAction = "waiting"

	// DeprovisionSkipped means provider determined deprovisioning is not needed
	DeprovisionSkipped DeprovisionAction = "skipped"
)

// DeprovisionResult contains the result of attempting to trigger a deprovision operation.
type DeprovisionResult struct {
	// Action indicates what the provider did
	Action DeprovisionAction

	// JobID is the tracking identifier when Action==DeprovisionTriggered
	// Used by controller to poll status via GetDeprovisionStatus()
	// Empty when Action==DeprovisionWaiting or Action==DeprovisionSkipped
	JobID string

	// BlockDeletionOnFailure indicates whether deletion should be blocked if deprovision fails
	// Stored in CR status for crash recovery (controller restart)
	// Providers set based on their cleanup guarantees
	BlockDeletionOnFailure bool

	// ProvisionJobStatus is the current status of the provision job (state and message)
	// Populated when provision job is checked before deprovisioning
	// This allows the controller to update the CR status to reflect job cancellation/termination
	// Empty when no provision job exists
	ProvisionJobStatus *ProvisionStatus
}

// ProvisioningProvider abstracts triggering infrastructure automation via AAP and retrieving job status.
type ProvisioningProvider interface {
	// TriggerProvision starts provisioning for a resource.
	// Returns a ProvisionResult with job details and initial state.
	TriggerProvision(ctx context.Context, resource client.Object) (*ProvisionResult, error)

	// GetProvisionStatus checks the status of a provisioning job.
	// The resource parameter allows providers to check additional state if needed.
	GetProvisionStatus(ctx context.Context, resource client.Object, jobID string) (ProvisionStatus, error)

	// TriggerDeprovision attempts to deprovision a resource.
	// The provider performs any prerequisite checks (e.g. cancelling a running provision job)
	// and returns an action indicating whether deprovisioning was triggered, needs to wait,
	// or should be skipped. provisionJobs is the current provision job history for the resource,
	// used to find and cancel any running provision job before deprovisioning.
	TriggerDeprovision(ctx context.Context, resource client.Object, provisionJobs []v1alpha1.JobStatus) (*DeprovisionResult, error)

	// GetDeprovisionStatus checks the status of a deprovisioning job.
	GetDeprovisionStatus(ctx context.Context, resource client.Object, jobID string) (ProvisionStatus, error)

	// Name returns the provider name for logging and identification.
	Name() string
}

// ProvisionStatus represents the current state of a provisioning or deprovisioning job.
type ProvisionStatus struct {
	// JobID is the unique identifier for this job.
	JobID string

	// State indicates the current state of the job.
	State v1alpha1.JobState

	// Message provides a human-readable status message.
	Message string

	// Progress indicates completion percentage (0-100).
	// Optional: providers that don't support progress tracking should leave this at 0.
	Progress int

	// StartTime is when the job started execution.
	StartTime time.Time

	// EndTime is when the job completed (succeeded or failed).
	// Zero value indicates job is still running.
	EndTime time.Time

	// ReconciledVersion is the configuration version that was successfully applied.
	// Only populated when State is JobStateSucceeded.
	ReconciledVersion string

	// ErrorDetails contains detailed error information when State is JobStateFailed.
	ErrorDetails string
}

// MessageWithDetails returns the message with error details appended, if present.
func (s *ProvisionStatus) MessageWithDetails() string {
	if s.ErrorDetails != "" {
		return fmt.Sprintf("%s: %s", s.Message, s.ErrorDetails)
	}
	return s.Message
}
