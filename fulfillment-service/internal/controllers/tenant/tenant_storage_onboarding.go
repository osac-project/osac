/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package tenant

import (
	"google.golang.org/protobuf/types/known/timestamppb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func (t *task) projectStorageOnboardingCondition(sources []*metav1.Condition, incomplete bool) {
	const conditionType = privatev1.TenantConditionType_TENANT_CONDITION_TYPE_STORAGE_READY
	if !t.tenant.HasStatus() {
		t.tenant.SetStatus(&privatev1.TenantStatus{})
	}
	switch {
	case incomplete:
		t.updateCondition(conditionType, privatev1.ConditionStatus_CONDITION_STATUS_UNSPECIFIED,
			"StorageReadyStatusUnknown", "Operator storage onboarding status could not be observed")
		return
	case len(sources) == 0:
		t.updateCondition(conditionType, privatev1.ConditionStatus_CONDITION_STATUS_FALSE,
			"StorageReadyPending", "Waiting for the operator to report storage onboarding completion")
		return
	case len(sources) > 1:
		// Sources are Tenant conditions reported by registered hubs.
		// Each hub's operator reports readiness for its VMaaS workload target.
		// Multiple hub reports have no aggregation policy here, so report Unknown.
		t.updateCondition(conditionType, privatev1.ConditionStatus_CONDITION_STATUS_UNSPECIFIED,
			"StorageReadyStatusAmbiguous", "Multiple hubs report storage onboarding status for this tenant")
		return
	}

	source := sources[0]
	status := privatev1.ConditionStatus_CONDITION_STATUS_UNSPECIFIED
	switch source.Status {
	case metav1.ConditionTrue:
		status = privatev1.ConditionStatus_CONDITION_STATUS_TRUE
	case metav1.ConditionFalse:
		status = privatev1.ConditionStatus_CONDITION_STATUS_FALSE
	}
	var transition *timestamppb.Timestamp
	if !source.LastTransitionTime.IsZero() {
		transition = timestamppb.New(source.LastTransitionTime.Time)
	}
	condition := privatev1.TenantCondition_builder{
		Type: conditionType, Status: status, Reason: &source.Reason,
		Message: &source.Message, LastTransitionTime: transition,
	}.Build()
	conditions := t.tenant.GetStatus().GetConditions()
	for i, previous := range conditions {
		if previous.GetType() == conditionType {
			conditions[i] = condition
			t.tenant.GetStatus().SetConditions(conditions)
			return
		}
	}
	t.tenant.GetStatus().SetConditions(append(conditions, condition))
}
