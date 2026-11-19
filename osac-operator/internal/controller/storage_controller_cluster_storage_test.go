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
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
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

// TestCaaSClusterStorageConditionRequiresAllTiers verifies the CaaS
// ClusterStorageReady verdict stays False while any tier lacks a resolved
// StorageClass. Regression test: a provision job that resolved only some of
// the tenant's tiers used to flip the condition to True, permanently
// excluding the ClusterOrder from further provisioning retries.
func TestCaaSClusterStorageConditionRequiresAllTiers(t *testing.T) {
	tiers := []provisioning.TierDefinition{
		{Name: "bronze"}, {Name: "gold"},
	}

	// Partial resolution: only the bronze tier's StorageClass exists.
	partial := tierResolutionResult{
		resolved: []v1alpha1.ResolvedStorageClass{
			{Name: "tenant-bronze", Tier: "bronze"},
		},
		resolvedMessages: []string{
			`tier "bronze": StorageClass "tenant-bronze" (tenant-specific)`,
		},
	}
	status, reason, msg := caasStorageCondition(tiers, partial)
	if status != metav1.ConditionFalse {
		t.Fatal("partial tier resolution must not set ClusterStorageReady")
	}
	if reason != v1alpha1.TenantReasonNotFound {
		t.Fatalf("reason = %s, want NotFound", reason)
	}
	if !strings.Contains(msg, "gold") {
		t.Fatalf("message must name the missing tier, got: %s", msg)
	}

	// Complete resolution: every tier resolved.
	complete := partial
	complete.resolved = append(complete.resolved, v1alpha1.ResolvedStorageClass{
		Name: "tenant-gold", Tier: "gold",
	})
	complete.resolvedMessages = append(complete.resolvedMessages,
		`tier "gold": StorageClass "tenant-gold" (tenant-specific)`)
	if status, _, _ = caasStorageCondition(tiers, complete); status != metav1.ConditionTrue {
		t.Fatal("complete tier resolution must set ClusterStorageReady")
	}

	// Ambiguous resolution: a tier matched multiple StorageClasses.
	ambiguous := complete
	ambiguous.duplicateMessages = []string{
		`tier "bronze": multiple tenant StorageClasses [tenant-bronze, tenant-bronze-2]`,
	}
	status, reason, _ = caasStorageCondition(tiers, ambiguous)
	if status != metav1.ConditionFalse || reason != v1alpha1.TenantReasonMultipleFound {
		t.Fatal("ambiguous tier resolution must stay not-ready with MultipleFound")
	}

	// No resolution at all.
	if status, _, _ = caasStorageCondition(tiers, tierResolutionResult{}); status != metav1.ConditionFalse {
		t.Fatal("no resolution must stay not-ready")
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

// TestMergeRequeueAfter verifies that a requeue pending from an earlier
// reconcile stage is never dropped when a later stage also returns a result,
// and that the sooner of two requeues wins.
func TestMergeRequeueAfter(t *testing.T) {
	tests := []struct {
		name   string
		result ctrl.Result
		other  ctrl.Result
		want   time.Duration
	}{
		{"both zero", ctrl.Result{}, ctrl.Result{}, 0},
		{"only result set", ctrl.Result{RequeueAfter: 30 * time.Second}, ctrl.Result{}, 30 * time.Second},
		{"only other set", ctrl.Result{}, ctrl.Result{RequeueAfter: 30 * time.Second}, 30 * time.Second},
		{"keeps the sooner requeue", ctrl.Result{RequeueAfter: 60 * time.Second}, ctrl.Result{RequeueAfter: 10 * time.Second}, 10 * time.Second},
		{"keeps result when other is later", ctrl.Result{RequeueAfter: 10 * time.Second}, ctrl.Result{RequeueAfter: 60 * time.Second}, 10 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeRequeueAfter(tt.result, tt.other)
			if got.RequeueAfter != tt.want {
				t.Errorf("mergeRequeueAfter(%v, %v).RequeueAfter = %v, want %v",
					tt.result, tt.other, got.RequeueAfter, tt.want)
			}
		})
	}
}
