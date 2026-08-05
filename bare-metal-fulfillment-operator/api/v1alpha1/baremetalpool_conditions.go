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

// BareMetalPool condition types
const (
	BareMetalPoolConditionTypeReady = "Ready"
)

// BareMetalPool condition reasons for Ready condition
const (
	// BareMetalPoolReasonReady indicates the pool is fully ready
	BareMetalPoolReasonReady = "Ready"

	// BareMetalPoolReasonProgressing indicates the pool is being processed
	BareMetalPoolReasonProgressing = "Progressing"

	// BareMetalPoolReasonFailed indicates the pool has failed
	BareMetalPoolReasonFailed = "Failed"

	// BareMetalPoolReasonDeleting indicates the pool is being deleted
	BareMetalPoolReasonDeleting = "Deleting"
)

// InitializeStatusConditions initializes the BareMetalPool conditions
func (bmp *BareMetalPool) InitializeStatusConditions() {
	bmp.initializeStatusCondition(
		BareMetalPoolConditionTypeReady,
		metav1.ConditionFalse,
		BareMetalPoolReasonProgressing,
	)
}

func (bmp *BareMetalPool) SetStatusCondition(conditionType string, status metav1.ConditionStatus, message string, reason string) bool {
	condition := metav1.Condition{
		Type:               conditionType,
		Status:             status,
		ObservedGeneration: bmp.GetGeneration(),
		Reason:             reason,
		Message:            message,
	}
	if bmp.Status.Conditions == nil {
		bmp.Status.Conditions = []metav1.Condition{}
	}
	return apimeta.SetStatusCondition(&bmp.Status.Conditions, condition)
}

func (bmp *BareMetalPool) GetStatusCondition(conditionType string) *metav1.Condition {
	return apimeta.FindStatusCondition(bmp.Status.Conditions, conditionType)
}

func (bmp *BareMetalPool) IsStatusConditionTrue(conditionType string) bool {
	return apimeta.IsStatusConditionTrue(bmp.Status.Conditions, conditionType)
}

func (bmp *BareMetalPool) IsStatusConditionFalse(conditionType string) bool {
	return apimeta.IsStatusConditionFalse(bmp.Status.Conditions, conditionType)
}

func (bmp *BareMetalPool) IsStatusConditionUnknown(conditionType string) bool {
	condition := apimeta.FindStatusCondition(bmp.Status.Conditions, conditionType)
	return condition == nil || condition.Status == metav1.ConditionUnknown
}

func (bmp *BareMetalPool) initializeStatusCondition(
	conditionType string,
	status metav1.ConditionStatus,
	reason string,
) {
	if bmp.Status.Conditions == nil {
		bmp.Status.Conditions = []metav1.Condition{}
	}

	// If condition already exists, don't overwrite
	if apimeta.FindStatusCondition(bmp.Status.Conditions, conditionType) != nil {
		return
	}

	bmp.SetStatusCondition(conditionType, status, "Initialized", reason)
}
