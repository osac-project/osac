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
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/networktask"
)

// NetworkingNamespacePredicate selects objects in the hub networking namespace.
func NetworkingNamespacePredicate(namespace string) predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		return obj.GetNamespace() == namespace
	})
}

// networkFeedbackState is the API state a networking CR maps to (Unified
// design, Status).
type networkFeedbackState int

const (
	networkFeedbackPending networkFeedbackState = iota
	networkFeedbackReady
	networkFeedbackFailed
	networkFeedbackDeleting
)

// networkFeedback maps a networking CR to its API state and message (Unified
// design, Status):
//
//	no Ready yet                       PENDING   empty
//	Ready=False Progressing            PENDING   condition message
//	Ready=True Succeeded               READY     empty
//	Ready=True Updating                READY     condition message
//	Ready=True ProvisioningDisabled    READY     condition message
//	Ready=False Failed                 FAILED    condition message
//	deletionTimestamp                  DELETING  the delete task; after a failed attempt its message and next retry time
//
// DELETING is never followed by another state; there is no DELETE_FAILED.
func networkFeedback(obj client.Object, conditions []metav1.Condition) (networkFeedbackState, string) {
	if obj.GetDeletionTimestamp() != nil {
		return networkFeedbackDeleting, deleteMessage(obj)
	}
	ready := meta.FindStatusCondition(conditions, v1alpha1.NetworkConditionReady)
	switch {
	case ready == nil:
		return networkFeedbackPending, ""
	case ready.Status == metav1.ConditionTrue && ready.Reason == v1alpha1.ReasonSucceeded:
		return networkFeedbackReady, ""
	case ready.Status == metav1.ConditionTrue:
		return networkFeedbackReady, ready.Message
	case ready.Reason == v1alpha1.ReasonFailed:
		return networkFeedbackFailed, ready.Message
	default:
		return networkFeedbackPending, ready.Message
	}
}

// deleteMessage describes the running or retrying cleanup task of an object
// being deleted, or is empty.
func deleteMessage(obj client.Object) string {
	tasks, ok := obj.(v1alpha1.NetworkTaskObject)
	if !ok {
		return ""
	}
	for _, entry := range tasks.GetNetworkJobs() {
		if !networktask.IsCleanupTask(entry.Task) {
			continue
		}
		if entry.State == v1alpha1.NetworkJobStateRunning || entry.State == v1alpha1.NetworkJobStateRetrying {
			return networktask.ProgressMessage(entry)
		}
	}
	return ""
}

// feedbackMessage sets or clears an optional API message.
func feedbackMessage(message string, set func(string), clear func()) {
	if message == "" {
		clear()
		return
	}
	set(sanitizeFeedbackText(message))
}
