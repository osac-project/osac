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
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (ci *ComputeInstance) SetStatusCondition(conditionType ComputeInstanceConditionType, status metav1.ConditionStatus, message string, reason string) bool {
	condition := metav1.Condition{
		Type:    string(conditionType),
		Status:  status,
		Reason:  reason,
		Message: message,
	}
	if ci.Status.Conditions == nil {
		ci.Status.Conditions = []metav1.Condition{}
	}
	return apimeta.SetStatusCondition(&ci.Status.Conditions, condition)
}

// GetStatusCondition returns the condition with the given type
func (ci *ComputeInstance) GetStatusCondition(conditionType ComputeInstanceConditionType) *metav1.Condition {
	if ci.Status.Conditions == nil {
		return nil
	}

	for i := range ci.Status.Conditions {
		if ci.Status.Conditions[i].Type == string(conditionType) {
			return &ci.Status.Conditions[i]
		}
	}
	return nil
}

// IsStatusConditionTrue returns true if the condition with the given type is true
func (ci *ComputeInstance) IsStatusConditionTrue(conditionType ComputeInstanceConditionType) bool {
	condition := ci.GetStatusCondition(conditionType)
	return condition != nil && condition.Status == metav1.ConditionTrue
}

// IsStatusConditionFalse returns true if the condition with the given type is false
func (ci *ComputeInstance) IsStatusConditionFalse(conditionType ComputeInstanceConditionType) bool {
	condition := ci.GetStatusCondition(conditionType)
	return condition != nil && condition.Status == metav1.ConditionFalse
}

// IsStatusConditionUnknown returns true if the condition with the given type is unknown
func (ci *ComputeInstance) IsStatusConditionUnknown(conditionType ComputeInstanceConditionType) bool {
	condition := ci.GetStatusCondition(conditionType)
	return condition == nil || condition.Status == metav1.ConditionUnknown
}
