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
	"errors"
	"reflect"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

// TestUpdateTenantClusterStoragePreservesVMaaSEntry verifies CaaS status updates
// preserve the VMaaS target entry when it was added first.
func TestUpdateTenantClusterStoragePreservesVMaaSEntry(t *testing.T) {
	reconciler := &StorageReconciler{}
	tenant := &v1alpha1.Tenant{}
	vmClusterName := string(mcmanager.LocalCluster)
	clusterOrder := clusterOrderWithStorageReady("caas-cluster-1", metav1.ConditionTrue, v1alpha1.TenantReasonFound)

	reconciler.updateTenantVMaaSStorage(tenant, vmClusterName, true, v1alpha1.TenantReasonFound)
	reconciler.updateTenantClusterStorage(tenant, clusterOrder)

	want := []v1alpha1.ClusterStorageStatus{
		{
			ClusterName: vmClusterName,
			Ready:       true,
			Reason:      v1alpha1.TenantReasonFound,
		},
		{
			ClusterName: clusterOrder.Name,
			Ready:       true,
			Reason:      v1alpha1.TenantReasonFound,
		},
	}
	if !reflect.DeepEqual(tenant.Status.ClusterStorage, want) {
		t.Fatalf("ClusterStorage = %#v, want %#v", tenant.Status.ClusterStorage, want)
	}
}

// TestUpdateTenantClusterStorageSupportsMultipleCaaSClusters verifies distinct
// CaaS entries coexist and a later update changes only the matching entry.
func TestUpdateTenantClusterStorageSupportsMultipleCaaSClusters(t *testing.T) {
	reconciler := &StorageReconciler{}
	tenant := &v1alpha1.Tenant{}
	vmClusterName := string(mcmanager.LocalCluster)
	firstCluster := clusterOrderWithStorageReady("caas-cluster-1", metav1.ConditionTrue, v1alpha1.TenantReasonFound)
	secondCluster := clusterOrderWithStorageReady("caas-cluster-2", metav1.ConditionTrue, v1alpha1.TenantReasonFound)

	reconciler.updateTenantVMaaSStorage(tenant, vmClusterName, true, v1alpha1.TenantReasonFound)
	reconciler.updateTenantClusterStorage(tenant, firstCluster)
	reconciler.updateTenantClusterStorage(tenant, secondCluster)

	firstCluster.Status.Conditions = []metav1.Condition{{
		Type:    string(v1alpha1.ClusterOrderConditionClusterStorageReady),
		Status:  metav1.ConditionFalse,
		Reason:  v1alpha1.TenantReasonNotFound,
		Message: "storage classes are not ready",
	}}
	reconciler.updateTenantClusterStorage(tenant, firstCluster)

	want := []v1alpha1.ClusterStorageStatus{
		{
			ClusterName: vmClusterName,
			Ready:       true,
			Reason:      v1alpha1.TenantReasonFound,
		},
		{
			ClusterName: firstCluster.Name,
			Ready:       false,
			Reason:      v1alpha1.TenantReasonNotFound,
		},
		{
			ClusterName: secondCluster.Name,
			Ready:       true,
			Reason:      v1alpha1.TenantReasonFound,
		},
	}
	if !reflect.DeepEqual(tenant.Status.ClusterStorage, want) {
		t.Fatalf("ClusterStorage = %#v, want %#v", tenant.Status.ClusterStorage, want)
	}
}

// TestCombineStageResults covers the rule for merging the VMaaS and CaaS stage
// results: VMaaS error first, then CaaS error, then the smaller positive requeue.
func TestCombineStageResults(t *testing.T) {
	errVMaaS := errors.New("vmaas failed")
	errCaaS := errors.New("caas failed")
	rq := func(d time.Duration) ctrl.Result { return ctrl.Result{RequeueAfter: d} }

	tests := []struct {
		name       string
		vmaas      ctrl.Result
		vmaasErr   error
		caas       ctrl.Result
		caasErr    error
		wantResult ctrl.Result
		wantErr    error
	}{
		{name: "both zero"},
		{name: "only VMaaS requeue", vmaas: rq(10 * time.Second), wantResult: rq(10 * time.Second)},
		{name: "only CaaS requeue", caas: rq(20 * time.Second), wantResult: rq(20 * time.Second)},
		{name: "both requeue, VMaaS shorter", vmaas: rq(5 * time.Second), caas: rq(30 * time.Second), wantResult: rq(5 * time.Second)},
		{name: "both requeue, CaaS shorter", vmaas: rq(30 * time.Second), caas: rq(5 * time.Second), wantResult: rq(5 * time.Second)},
		{name: "VMaaS error with CaaS requeue", vmaasErr: errVMaaS, caas: rq(5 * time.Second), wantErr: errVMaaS},
		{name: "CaaS error with VMaaS requeue", vmaas: rq(5 * time.Second), caasErr: errCaaS, wantErr: errCaaS},
		{name: "both errors, VMaaS wins", vmaas: rq(7 * time.Second), vmaasErr: errVMaaS, caas: rq(3 * time.Second), caasErr: errCaaS, wantResult: rq(7 * time.Second), wantErr: errVMaaS},
		{name: "negative VMaaS requeue ignored", vmaas: rq(-time.Second), caas: rq(10 * time.Second), wantResult: rq(10 * time.Second)},
		{name: "negative CaaS requeue ignored", vmaas: rq(10 * time.Second), caas: rq(-time.Second), wantResult: rq(10 * time.Second)},
		{name: "only a negative requeue gives zero", vmaas: rq(-time.Second)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := combineStageResults(tt.vmaas, tt.vmaasErr, tt.caas, tt.caasErr)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if got != tt.wantResult {
				t.Fatalf("result = %+v, want %+v", got, tt.wantResult)
			}
		})
	}
}

// clusterOrderWithStorageReady creates a ClusterOrder with the requested
// cluster-storage readiness condition for slice-upsert tests.
func clusterOrderWithStorageReady(name string, status metav1.ConditionStatus, reason string) *v1alpha1.ClusterOrder {
	return &v1alpha1.ClusterOrder{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: v1alpha1.ClusterOrderStatus{
			Conditions: []metav1.Condition{{
				Type:    string(v1alpha1.ClusterOrderConditionClusterStorageReady),
				Status:  status,
				Reason:  reason,
				Message: reason,
			}},
		},
	}
}
