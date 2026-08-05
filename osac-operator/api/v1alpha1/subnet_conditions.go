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
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Reason constants for common Subnet scenarios
const (
	// SubnetProvisioningSucceeded indicates successful provisioning
	SubnetProvisioningSucceeded = "ProvisioningSucceeded"

	// SubnetProvisioningFailed indicates provisioning failed
	SubnetProvisioningFailed = "ProvisioningFailed"

	// SubnetDeletionSucceeded indicates successful deletion
	SubnetDeletionSucceeded = "DeletionSucceeded"

	// SubnetDeletionFailed indicates deletion failed
	SubnetDeletionFailed = "DeletionFailed"

	// SubnetNetworkProviderError indicates an error from the network provider
	SubnetNetworkProviderError = "NetworkProviderError"
)

// GetStatusCondition returns the condition with the specified type from the Subnet status
func GetStatusCondition(subnet *Subnet, conditionType SubnetConditionType) *metav1.Condition {
	return meta.FindStatusCondition(subnet.Status.Conditions, string(conditionType))
}

// SetStatusCondition sets the condition with the specified type in the Subnet status
func SetStatusCondition(subnet *Subnet, condition metav1.Condition) {
	meta.SetStatusCondition(&subnet.Status.Conditions, condition)
}
