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

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

// TenantReadinessEvaluator determines whether a Tenant can be used by
// customer-facing controllers. Its configuration is fixed at construction.
type TenantReadinessEvaluator struct {
	storageControllerEnabled bool
	conditionsToCheck        []v1alpha1.TenantConditionType
}

// NewTenantReadinessEvaluator creates an evaluator configured for the
// operator's global Storage controller setting.
func NewTenantReadinessEvaluator(storageControllerEnabled bool) TenantReadinessEvaluator {
	return TenantReadinessEvaluator{
		storageControllerEnabled: storageControllerEnabled,
		conditionsToCheck: []v1alpha1.TenantConditionType{
			v1alpha1.TenantConditionClusterStorageReady,
		},
	}
}

// IsTenantReadyToUse reports whether the Tenant has completed its lifecycle
// and all configured onboarding prerequisites are ready.
func (e TenantReadinessEvaluator) IsTenantReadyToUse(tenant *v1alpha1.Tenant) bool {
	if tenant == nil || tenant.Status.Phase != v1alpha1.TenantPhaseReady {
		return false
	}

	for _, conditionType := range e.conditionsToCheck {
		// The Storage controller owns this condition. When it is disabled,
		// ComputeInstance can use manually labeled StorageClasses and JIT.
		if conditionType == v1alpha1.TenantConditionClusterStorageReady && !e.storageControllerEnabled {
			continue
		}

		condition := tenant.GetStatusCondition(conditionType)
		if condition == nil || condition.Status != metav1.ConditionTrue {
			return false
		}
	}
	return true
}
