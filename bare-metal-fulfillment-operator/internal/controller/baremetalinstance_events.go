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

package controller

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
)

func (r *BareMetalInstanceReconciler) setNetworkCondition(
	bmi *v1alpha1.BareMetalInstance,
	conditionType v1alpha1.BareMetalInstanceConditionType,
	status metav1.ConditionStatus,
	reason, message, eventType, eventReason string,
) {
	previous := bmi.GetStatusCondition(conditionType)
	changed := previous == nil || previous.Status != status || previous.Reason != reason
	bmi.SetStatusCondition(conditionType, status, reason, message)
	if changed && eventType != "" && r.Recorder != nil {
		r.Recorder.Eventf(bmi, nil, eventType, eventReason, "Reconcile", "%s", message)
	}
}
