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

	// PowerSynced drives metering interval boundaries. A new phase (reason)
	// starts a new interval even when the status stays False, so do not let a
	// read failure or a restart observation backdate the next real power
	// transition. Message-only changes within the same phase (for example a stop
	// moving from "powering off" to "transitioning") keep the phase timestamp.
	var prevReason string
	hadPrev := false
	if prev := h.GetStatusCondition(conditionType); prev != nil {
		hadPrev, prevReason = true, prev.Reason
	}

	changed := apimeta.SetStatusCondition(&h.Status.Conditions, condition)
	if conditionType == HostConditionPowerSynced && hadPrev && prevReason != reason &&
		prevReason != HostConditionReasonPoweringOff && reason != HostConditionReasonPoweringOff {
		if cond := h.GetStatusCondition(conditionType); cond != nil {
			cond.LastTransitionTime = metav1.Now()
		}
	}
	return changed
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
