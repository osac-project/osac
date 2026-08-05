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
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (h *BareMetalInstance) SetStatusCondition(conditionType BareMetalInstanceConditionType, status metav1.ConditionStatus, reason, message string) bool {
	condition := metav1.Condition{
		Type:    string(conditionType),
		Status:  status,
		Reason:  reason,
		Message: message,
	}
	if h.Status.Conditions == nil {
		h.Status.Conditions = []metav1.Condition{}
	}
	return apimeta.SetStatusCondition(&h.Status.Conditions, condition)
}

func (h *BareMetalInstance) GetStatusCondition(conditionType BareMetalInstanceConditionType) *metav1.Condition {
	if h.Status.Conditions == nil {
		return nil
	}
	return apimeta.FindStatusCondition(h.Status.Conditions, string(conditionType))
}

func (h *BareMetalInstance) IsStatusConditionTrue(conditionType BareMetalInstanceConditionType) bool {
	return apimeta.IsStatusConditionTrue(h.Status.Conditions, string(conditionType))
}

func (h *BareMetalInstance) IsStatusConditionFalse(conditionType BareMetalInstanceConditionType) bool {
	return apimeta.IsStatusConditionFalse(h.Status.Conditions, string(conditionType))
}

func (h *BareMetalInstance) IsStatusConditionUnknown(conditionType BareMetalInstanceConditionType) bool {
	cond := h.GetStatusCondition(conditionType)
	return cond == nil || cond.Status == metav1.ConditionUnknown
}
