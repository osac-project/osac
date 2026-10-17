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

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

func TestTenantReadinessEvaluator(t *testing.T) {
	conditionTrue := metav1.ConditionTrue
	conditionFalse := metav1.ConditionFalse
	conditionUnknown := metav1.ConditionUnknown

	tests := []struct {
		name                     string
		storageControllerEnabled bool
		phase                    v1alpha1.TenantPhaseType
		clusterStorageStatus     *metav1.ConditionStatus
		wantReady                bool
	}{
		{
			name:                     "ready tenant passes when storage controller is disabled and condition is absent",
			phase:                    v1alpha1.TenantPhaseReady,
			storageControllerEnabled: false,
			wantReady:                true,
		},
		{
			name:                     "ready tenant passes when storage controller is disabled and condition is false",
			phase:                    v1alpha1.TenantPhaseReady,
			storageControllerEnabled: false,
			clusterStorageStatus:     &conditionFalse,
			wantReady:                true,
		},
		{
			name:                     "ready tenant requires cluster storage when storage controller is enabled",
			phase:                    v1alpha1.TenantPhaseReady,
			storageControllerEnabled: true,
			clusterStorageStatus:     &conditionTrue,
			wantReady:                true,
		},
		{
			name:                     "enabled storage controller with missing condition",
			phase:                    v1alpha1.TenantPhaseReady,
			storageControllerEnabled: true,
			wantReady:                false,
		},
		{
			name:                     "enabled storage controller with false condition",
			phase:                    v1alpha1.TenantPhaseReady,
			storageControllerEnabled: true,
			clusterStorageStatus:     &conditionFalse,
			wantReady:                false,
		},
		{
			name:                     "enabled storage controller with unknown condition",
			phase:                    v1alpha1.TenantPhaseReady,
			storageControllerEnabled: true,
			clusterStorageStatus:     &conditionUnknown,
			wantReady:                false,
		},
		{
			name:                     "progressing tenant is not ready even when cluster storage is ready",
			phase:                    v1alpha1.TenantPhaseProgressing,
			storageControllerEnabled: true,
			clusterStorageStatus:     &conditionTrue,
			wantReady:                false,
		},
		{
			name:                     "failed tenant is not ready",
			phase:                    v1alpha1.TenantPhaseFailed,
			storageControllerEnabled: false,
			wantReady:                false,
		},
		{
			name:                     "deleting tenant is not ready",
			phase:                    v1alpha1.TenantPhaseDeleting,
			storageControllerEnabled: false,
			wantReady:                false,
		},
		{
			name:                     "tenant with empty phase is not ready",
			storageControllerEnabled: false,
			wantReady:                false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tenant := &v1alpha1.Tenant{
				Status: v1alpha1.TenantStatus{Phase: tt.phase},
			}
			if tt.clusterStorageStatus != nil {
				tenant.SetStatusCondition(
					v1alpha1.TenantConditionClusterStorageReady,
					*tt.clusterStorageStatus,
					"test",
					"test",
				)
			}

			evaluator := NewTenantReadinessEvaluator(tt.storageControllerEnabled)
			if got := evaluator.IsTenantReadyToUse(tenant); got != tt.wantReady {
				t.Errorf("IsTenantReadyToUse() = %t, want %t", got, tt.wantReady)
			}
		})
	}

	t.Run("nil tenant is not ready", func(t *testing.T) {
		if NewTenantReadinessEvaluator(true).IsTenantReadyToUse(nil) {
			t.Error("IsTenantReadyToUse(nil) = true, want false")
		}
	})
}
