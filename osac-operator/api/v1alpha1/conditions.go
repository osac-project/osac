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

// Common condition type constants used across ClusterOrder and related resources.
const (
	// ConditionAccepted indicates the resource has been accepted for processing.
	ConditionAccepted = "Accepted"
	// ConditionNamespaceCreated indicates the target namespace has been created.
	ConditionNamespaceCreated = "NamespaceCreated"
	// ConditionControlPlaneCreated indicates the hosted control plane has been created.
	ConditionControlPlaneCreated = "ControlPlaneCreated"
	// ConditionControlPlaneAvailable indicates the hosted control plane is available.
	ConditionControlPlaneAvailable = "ControlPlaneAvailable"
	// ConditionClusterAvailable indicates the cluster has reached a fully available state.
	ConditionClusterAvailable = "ClusterAvailable"
	// ConditionProgressing indicates the resource is actively being provisioned.
	ConditionProgressing = "Progressing"
	// ConditionDeleting indicates the resource is being deleted.
	ConditionDeleting = "Deleting"
	// ConditionCompleted indicates the resource has completed its lifecycle.
	ConditionCompleted = "Completed"
	// ConditionAvailable indicates the resource is available for use.
	ConditionAvailable = "Available"
	// ConditionReady indicates the resource has reached a ready state.
	ConditionReady = "Ready"
	// CaaS bare-metal worker provisioning conditions (OSAC-2135).
	ConditionInfraEnvReady                 = "InfraEnvReady"
	ConditionRHCOSImageNotFound            = "RHCOSImageNotFound"
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

// Common reason constants used in condition transitions.
const (
	// ReasonInitialized indicates the resource has been initialized.
	ReasonInitialized = "Initialized"
	// ReasonAsExpected indicates the resource state matches the desired state.
	ReasonAsExpected = "AsExpected"
	// ReasonCreated indicates the resource was successfully created.
	ReasonCreated = "Created"
	// ReasonReady indicates the resource has reached a ready state.
	ReasonReady = "Ready"
	// ReasonProgressing indicates the resource is actively being reconciled.
	ReasonProgressing = "Progressing"
	// ReasonFailed indicates the resource has entered a failed state.
	ReasonFailed = "Failed"
	// ReasonDeleting indicates the resource is being deleted.
	ReasonDeleting = "Deleting"
	// ReasonWebhookTriggered indicates a webhook was triggered for the resource.
	ReasonWebhookTriggered = "WebhookTriggered"
	// ReasonWebhookFailed indicates a webhook invocation failed.
	ReasonWebhookFailed = "WebhookFailed"

	// ReasonTenantNotReady indicates the tenant is not yet ready.
	ReasonTenantNotReady = "TenantNotReady"
	// ReasonProvisioningStorage indicates storage is being provisioned.
	ReasonProvisioningStorage = "ProvisioningStorage"
	// ReasonWaitingForVM indicates the system is waiting for a virtual machine.
	ReasonWaitingForVM = "WaitingForVM"
	// ReasonScheduling indicates the resource is being scheduled.
	ReasonScheduling = "Scheduling"
	// ReasonInfrastructureReady indicates the underlying infrastructure is ready.
	ReasonInfrastructureReady = "InfrastructureReady"
	// ReasonProvisioningFailed indicates a provisioning operation has failed.
	ReasonProvisioningFailed = "ProvisioningFailed"
	// ReasonNoManagerConfigured indicates no network manager is configured.
	ReasonNoManagerConfigured = "NoManagerConfigured"
	// ReasonPreparingInfrastructure indicates infrastructure preparation is in progress.
	ReasonPreparingInfrastructure = "PreparingInfrastructure"
	// ReasonControlPlaneStarting indicates the hosted control plane is starting up.
	ReasonControlPlaneStarting = "ControlPlaneStarting"
	// ReasonWorkersJoining indicates worker nodes are joining the cluster.
	ReasonWorkersJoining = "WorkersJoining"
	// ReasonStageUnknown indicates the provisioning stage could not be determined.
	ReasonStageUnknown = "StageUnknown"
	// ReasonStalled indicates the resource has not progressed within the expected threshold.
	ReasonStalled = "Stalled"
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

	// ReasonNoTransientErrors indicates that the reconciliation loop
	// completed without any transient errors, clearing the
	// FulfillmentServiceUnavailable condition. This does not imply
	// all workers are healthy — only that no transient communication
	// failures occurred during this cycle.
	ReasonNoTransientErrors = "NoTransientErrors"
)
