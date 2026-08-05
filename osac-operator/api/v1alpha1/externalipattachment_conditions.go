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

package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ExternalIPAttachmentConditionType is a valid value for .status.conditions.type
type ExternalIPAttachmentConditionType string

const (
	// ExternalIPAttachmentConditionConfigurationApplied indicates whether the controller
	// has processed the current spec version
	ExternalIPAttachmentConditionConfigurationApplied ExternalIPAttachmentConditionType = "ConfigurationApplied"
)

// GetExternalIPAttachmentStatusCondition returns the condition with the specified type from the ExternalIPAttachment status
func GetExternalIPAttachmentStatusCondition(attachment *ExternalIPAttachment, conditionType ExternalIPAttachmentConditionType) *metav1.Condition {
	return meta.FindStatusCondition(attachment.Status.Conditions, string(conditionType))
}

// SetExternalIPAttachmentStatusCondition sets the condition with the specified type in the ExternalIPAttachment status
func SetExternalIPAttachmentStatusCondition(attachment *ExternalIPAttachment, condition metav1.Condition) {
	meta.SetStatusCondition(&attachment.Status.Conditions, condition)
}
