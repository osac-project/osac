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
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ckv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func TestComputeInstanceFeedbackMapsNetworkReady(t *testing.T) {
	ci := &ckv1alpha1.ComputeInstance{}
	ci.SetStatusCondition(ckv1alpha1.ComputeInstanceConditionNetworkReady, metav1.ConditionFalse,
		"the VM is not running", ckv1alpha1.ComputeInstanceReasonVMNotRunning)
	remote := privatev1.ComputeInstance_builder{Status: &privatev1.ComputeInstanceStatus{}}.Build()
	syncCIConditions(ci, remote)
	var found *privatev1.ComputeInstanceCondition
	for _, c := range remote.GetStatus().GetConditions() {
		if c.GetType() == privatev1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_NETWORK_READY {
			found = c
		}
	}
	if found == nil {
		t.Fatal("NetworkReady not mapped to COMPUTE_INSTANCE_CONDITION_TYPE_NETWORK_READY")
	}
	if found.GetStatus() != privatev1.ConditionStatus_CONDITION_STATUS_FALSE ||
		found.GetReason() != ckv1alpha1.ComputeInstanceReasonVMNotRunning || found.GetMessage() != "the VM is not running" {
		t.Fatalf("NETWORK_READY = %v/%s/%q", found.GetStatus(), found.GetReason(), found.GetMessage())
	}
}
