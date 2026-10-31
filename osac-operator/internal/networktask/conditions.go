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

package networktask

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

// SetReady sets the Ready condition of a networking CR. It returns true when
// the condition changed.
func SetReady(obj v1alpha1.NetworkResourceObject, status metav1.ConditionStatus, reason, message string) bool {
	return meta.SetStatusCondition(obj.GetConditionsRef(), metav1.Condition{
		Type:               v1alpha1.NetworkConditionReady,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: obj.GetGeneration(),
	})
}

// SetProgressing sets Ready=False, reason Progressing.
func SetProgressing(obj v1alpha1.NetworkResourceObject, message string) bool {
	return SetReady(obj, metav1.ConditionFalse, v1alpha1.ReasonProgressing, message)
}

// SetSucceeded sets Ready=True, reason Succeeded.
func SetSucceeded(obj v1alpha1.NetworkResourceObject) bool {
	return SetReady(obj, metav1.ConditionTrue, v1alpha1.ReasonSucceeded, "")
}

// SetFailed sets Ready=False, reason Failed.
func SetFailed(obj v1alpha1.NetworkResourceObject, message string) bool {
	return SetReady(obj, metav1.ConditionFalse, v1alpha1.ReasonFailed, message)
}

// SetProvisioningDisabled sets Ready=True, reason ProvisioningDisabled, naming
// the skipped task (Unified design, Provider networking control).
func SetProvisioningDisabled(obj v1alpha1.NetworkResourceObject, task string) bool {
	return SetReady(obj, metav1.ConditionTrue, v1alpha1.ReasonProvisioningDisabled, DisabledMessage(task))
}

// DisabledMessage names the task skipped because provider networking is disabled.
func DisabledMessage(task string) string {
	return fmt.Sprintf("provider networking is disabled: %s not run", task)
}

// MessageNetworkingDisabled is the Failed message of an ExternalIP,
// ExternalIPAttachment or NATGateway CR found while provider networking is
// disabled (these never exist in that mode).
const MessageNetworkingDisabled = "provider networking is disabled"

// ProgressMessage describes an entry for the Ready=False Progressing (or
// Ready=True Updating) message: the running task and its AAP job ID, or the
// manager's message and next retry time.
func ProgressMessage(entry v1alpha1.NetworkJobStatus) string {
	switch entry.State {
	case v1alpha1.NetworkJobStateRunning:
		if entry.Message != "" {
			return entry.Message
		}
		if entry.JobID == "" {
			return fmt.Sprintf("%s (%s) starting", entry.Task, entry.Manager)
		}
		return fmt.Sprintf("%s (%s) running in AAP job %s", entry.Task, entry.Manager, entry.JobID)
	case v1alpha1.NetworkJobStateRetrying:
		return entry.Message
	default:
		return entry.Message
	}
}
