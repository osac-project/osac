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

// Common condition constants
const (
	ConditionAccepted              = "Accepted"
	ConditionNamespaceCreated      = "NamespaceCreated"
	ConditionControlPlaneCreated   = "ControlPlaneCreated"
	ConditionControlPlaneAvailable = "ControlPlaneAvailable"
	ConditionClusterAvailable      = "ClusterAvailable"
	ConditionProgressing           = "Progressing"
	ConditionDeleting              = "Deleting"
	ConditionCompleted             = "Completed"
	ConditionAvailable             = "Available"
	ConditionReady                 = "Ready"
)

// Worker failure condition constants
const (
	// ConditionWorkerProvisioningFailed indicates that provisioning of one or more
	// bare-metal workers has failed and a replacement attempt is in progress.
	ConditionWorkerProvisioningFailed = "WorkerProvisioningFailed"

	// ConditionWorkersFailed indicates that worker provisioning has exhausted all
	// retry attempts and the cluster cannot reach a healthy worker state.
	ConditionWorkersFailed = "WorkersFailed"

	// ConditionFulfillmentServiceUnavailable indicates that a transient gRPC error
	// prevented communication with the fulfillment service. These errors do not
	// count toward the maximum retry budget.
	ConditionFulfillmentServiceUnavailable = "FulfillmentServiceUnavailable"
)

// Common reason constants
const (
	ReasonInitialized      = "Initialized"
	ReasonAsExpected       = "AsExpected"
	ReasonCreated          = "Created"
	ReasonReady            = "Ready"
	ReasonProgressing      = "Progressing"
	ReasonFailed           = "Failed"
	ReasonDeleting         = "Deleting"
	ReasonWebhookTriggered = "WebhookTriggered"
	ReasonWebhookFailed    = "WebhookFailed"

	ReasonTenantNotReady          = "TenantNotReady"
	ReasonProvisioningStorage     = "ProvisioningStorage"
	ReasonWaitingForVM            = "WaitingForVM"
	ReasonScheduling              = "Scheduling"
	ReasonInfrastructureReady     = "InfrastructureReady"
	ReasonProvisioningFailed      = "ProvisioningFailed"
	ReasonNoManagerConfigured     = "NoManagerConfigured"
	ReasonPreparingInfrastructure = "PreparingInfrastructure"
	ReasonControlPlaneStarting    = "ControlPlaneStarting"
	ReasonWorkersJoining          = "WorkersJoining"
	ReasonStageUnknown            = "StageUnknown"
	ReasonStalled                 = "Stalled"
)

// Worker failure reason constants
const (
	// ReasonAgentRegistrationTimeout indicates that the agent failed to register
	// within the allowed timeout window (default 30 minutes).
	ReasonAgentRegistrationTimeout = "AgentRegistrationTimeout"

	// ReasonMaxRetriesExhausted indicates that all retry attempts have been used.
	ReasonMaxRetriesExhausted = "MaxRetriesExhausted"

	// ReasonBMIReplacementTriggered indicates that a replacement BMI has been created
	// after a worker provisioning failure.
	ReasonBMIReplacementTriggered = "BMIReplacementTriggered"

	// ReasonGRPCUnavailable indicates a transient gRPC service error.
	ReasonGRPCUnavailable = "GRPCUnavailable"
)
