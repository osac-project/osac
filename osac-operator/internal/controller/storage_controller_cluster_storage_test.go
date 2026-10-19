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
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
