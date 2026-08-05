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
	v1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
