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

// Common reason constants
const (
	ReasonInitialized      = "Initialized"
	ReasonAsExpected       = "AsExpected"
	ReasonCreated          = "Created"
	ReasonProgressing      = "Progressing"
	ReasonFailed           = "Failed"
	ReasonDeleting         = "Deleting"
	ReasonWebhookTriggered = "WebhookTriggered"
	ReasonWebhookFailed    = "WebhookFailed"

	ReasonTenantNotReady      = "TenantNotReady"
	ReasonProvisioningStorage = "ProvisioningStorage"
	ReasonWaitingForVM        = "WaitingForVM"
	ReasonScheduling          = "Scheduling"
	ReasonInfrastructureReady = "InfrastructureReady"
	ReasonProvisioningFailed  = "ProvisioningFailed"
)
