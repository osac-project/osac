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
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

func setReadyConditionFailed(conditions *[]metav1.Condition, message string) {
	apimeta.SetStatusCondition(conditions, metav1.Condition{
		Type:    v1alpha1.ConditionReady,
		Status:  metav1.ConditionFalse,
		Reason:  v1alpha1.ReasonProvisioningFailed,
		Message: message,
	})
}

func setReadyConditionTrue(conditions *[]metav1.Condition) {
	apimeta.SetStatusCondition(conditions, metav1.Condition{
		Type:   v1alpha1.ConditionReady,
		Status: metav1.ConditionTrue,
		Reason: v1alpha1.ReasonAsExpected,
	})
}

// setReadyConditionNoOp marks Ready=True with ReasonNoOp for resources
// whose provisioning backend reports that no real policy or backend
// effect was applied. The message carries the backend's explanation.
func setReadyConditionNoOp(conditions *[]metav1.Condition, message string) {
	apimeta.SetStatusCondition(conditions, metav1.Condition{
		Type:    v1alpha1.ConditionReady,
		Status:  metav1.ConditionTrue,
		Reason:  v1alpha1.ReasonNoOp,
		Message: message,
	})
}

// noopMessageFromOutputs returns the human-readable no-op message
// from provisioning job outputs when the backend signals that no real
// effect was applied (osac_noop: true). Returns "" when the outputs
// carry no no-op signal.
func noopMessageFromOutputs(outputs map[string]any) string {
	if outputs == nil {
		return ""
	}
	noop, _ := outputs["osac_noop"].(bool)
	if !noop {
		return ""
	}
	msg, _ := outputs["osac_noop_message"].(string)
	if msg == "" {
		msg = "Resource accepted as a no-op: " +
			"no backend policy was applied."
	}
	return msg
}

// setReadyConditionBlocked marks Ready=False for a precondition that is expected to
// self-heal once satisfied (e.g. no dispatcher-resolvable manager yet), as opposed to
// setReadyConditionFailed's terminal provisioning failure.
func setReadyConditionBlocked(conditions *[]metav1.Condition, reason, message string) {
	apimeta.SetStatusCondition(conditions, metav1.Condition{
		Type:    v1alpha1.ConditionReady,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}
