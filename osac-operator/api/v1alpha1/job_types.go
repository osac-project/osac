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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// JobType represents the type of job operation
// +kubebuilder:validation:Enum=provision;deprovision
type JobType string

const (
	// JobTypeProvision indicates a provisioning operation
	JobTypeProvision JobType = "provision"
	// JobTypeDeprovision indicates a deprovisioning operation
	JobTypeDeprovision JobType = "deprovision"
)

// JobState represents the current state of a job
// +kubebuilder:validation:Enum=Pending;Waiting;Running;Succeeded;Failed;Canceled;Unknown
type JobState string

const (
	// JobStatePending indicates the job is pending execution
	JobStatePending JobState = "Pending"
	// JobStateWaiting indicates the job is waiting for dependencies
	JobStateWaiting JobState = "Waiting"
	// JobStateRunning indicates the job is currently running
	JobStateRunning JobState = "Running"
	// JobStateSucceeded indicates the job completed successfully
	JobStateSucceeded JobState = "Succeeded"
	// JobStateFailed indicates the job failed
	JobStateFailed JobState = "Failed"
	// JobStateCanceled indicates the job was canceled
	JobStateCanceled JobState = "Canceled"
	// JobStateUnknown indicates the job state is unknown
	JobStateUnknown JobState = "Unknown"
)

// IsTerminal returns true if the job state is terminal (will not change)
func (s JobState) IsTerminal() bool {
	return s == JobStateSucceeded || s == JobStateFailed || s == JobStateCanceled
}

// IsSuccessful returns true if the job completed successfully
func (s JobState) IsSuccessful() bool {
	return s == JobStateSucceeded
}

// JobStatus represents the status of a provisioning or deprovisioning job
type JobStatus struct {
	// JobID is the AAP job identifier from the provisioning provider API response.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Type=string
	JobID string `json:"jobID"`

	// Type indicates the operation type
	// +kubebuilder:validation:Required
	Type JobType `json:"type"`

	// Timestamp when this job was created/triggered
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Format=date-time
	Timestamp metav1.Time `json:"timestamp"`

	// State is the current state of the job
	// +kubebuilder:validation:Required
	State JobState `json:"state"`

	// Message provides human-readable status or error information
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Type=string
	Message string `json:"message,omitempty"`

	// BlockDeletionOnFailure indicates whether CR deletion should be blocked if this job fails.
	// AAP sets this to true to prevent orphaned cloud resources when deprovisioning fails.
	// +kubebuilder:validation:Optional
	BlockDeletionOnFailure bool `json:"blockDeletionOnFailure,omitempty"`

	// ConfigVersion is the DesiredConfigVersion at the time this job was triggered.
	// Used to determine retry behavior on failure: if ConfigVersion differs from
	// the current DesiredConfigVersion, a new job is triggered immediately.
	// If they match, the controller retries with exponential backoff.
	// +kubebuilder:validation:Optional
	ConfigVersion string `json:"configVersion,omitempty"`
}
