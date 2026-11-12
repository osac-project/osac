package workloadcluster

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func TestObserveAvailableManagedClusterRecordsIdentity(t *testing.T) {
	hub := fake.NewClientBuilder().WithObjects(
		managedCluster("worker-1", "uid-1", true, true),
	).Build()
	object := &privatev1.WorkloadCluster{Spec: &privatev1.WorkloadClusterSpec{ManagedClusterName: "worker-1"}}
	status := &privatev1.WorkloadClusterStatus{}

	err := (&function{}).observe(context.Background(), hub, object, status)

	require.NoError(t, err)
	require.Equal(t, "uid-1", status.GetManagedClusterUid())
	require.Equal(t, "True", condition(status, availableCondition).GetStatus())
}

func TestObserveRetainsIdentityAndReportsMismatch(t *testing.T) {
	hub := fake.NewClientBuilder().WithObjects(
		managedCluster("worker-1", "uid-2", true, true),
	).Build()
	object := &privatev1.WorkloadCluster{Spec: &privatev1.WorkloadClusterSpec{ManagedClusterName: "worker-1"}}
	status := &privatev1.WorkloadClusterStatus{ManagedClusterUid: "uid-1"}

	err := (&function{}).observe(context.Background(), hub, object, status)

	require.NoError(t, err)
	require.Equal(t, "uid-1", status.GetManagedClusterUid())
	require.Equal(t, reasonClusterIdentityMismatch, condition(status, availableCondition).GetReason())
}

func TestObserveMissingIdentityDoesNotRecordUID(t *testing.T) {
	hub := fake.NewClientBuilder().WithObjects(
		managedCluster("worker-1", "", true, true),
	).Build()
	object := &privatev1.WorkloadCluster{Spec: &privatev1.WorkloadClusterSpec{ManagedClusterName: "worker-1"}}
	status := &privatev1.WorkloadClusterStatus{}

	err := (&function{}).observe(context.Background(), hub, object, status)

	require.NoError(t, err)
	require.Empty(t, status.GetManagedClusterUid())
	require.Equal(t, reasonClusterIdentityMissing, condition(status, availableCondition).GetReason())
}

func TestObserveMissingManagedCluster(t *testing.T) {
	hub := fake.NewClientBuilder().Build()
	object := &privatev1.WorkloadCluster{Spec: &privatev1.WorkloadClusterSpec{ManagedClusterName: "worker-1"}}
	status := &privatev1.WorkloadClusterStatus{}

	err := (&function{}).observe(context.Background(), hub, object, status)

	require.NoError(t, err)
	require.Equal(t, reasonManagedClusterNotFound, condition(status, availableCondition).GetReason())
}

func TestSetUnavailableReportsUnreachableHub(t *testing.T) {
	status := &privatev1.WorkloadClusterStatus{}

	setUnavailable(status, reasonManagedClusterUnavailable, "observation failed", false)

	hubCondition := condition(status, hubReachable)
	require.Equal(t, "False", hubCondition.GetStatus())
	require.Equal(t, reasonHubUnreachable, hubCondition.GetReason())
	require.Equal(t, "Hub cannot be reached", hubCondition.GetMessage())
}

func TestSetConditionDoesNotRefreshRecentObservation(t *testing.T) {
	observedAt := timestamppb.New(time.Now().Add(-time.Second))
	status := &privatev1.WorkloadClusterStatus{Conditions: []*privatev1.WorkloadClusterCondition{
		privatev1.WorkloadClusterCondition_builder{
			Type:               availableCondition,
			Status:             "True",
			Reason:             "ManagedClusterAvailable",
			LastObservedTime:   observedAt,
			LastTransitionTime: observedAt,
		}.Build(),
	}}

	setCondition(status, availableCondition, "True", "ManagedClusterAvailable", "unchanged", true)

	require.Same(t, observedAt, condition(status, availableCondition).GetLastObservedTime())
}

func managedCluster(name, uid string, accepted, available bool) *unstructured.Unstructured {
	object := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": managedClusterGroup + "/" + managedClusterVersion,
		"kind":       "ManagedCluster",
		"metadata":   map[string]interface{}{"name": name},
		"status": map[string]interface{}{
			"conditions": []interface{}{
				map[string]interface{}{"type": "HubAcceptedManagedCluster", "status": boolStatus(accepted)},
				map[string]interface{}{"type": "ManagedClusterConditionAvailable", "status": boolStatus(available)},
			},
			"clusterClaims": []interface{}{map[string]interface{}{"name": "id.k8s.io", "value": uid}},
		},
	}}
	object.SetGroupVersionKind(schema.GroupVersionKind{Group: managedClusterGroup, Version: managedClusterVersion, Kind: "ManagedCluster"})
	object.SetUID(types.UID(uid))
	return object
}

func condition(status *privatev1.WorkloadClusterStatus, conditionType string) *privatev1.WorkloadClusterCondition {
	for _, item := range status.GetConditions() {
		if item.GetType() == conditionType {
			return item
		}
	}
	return nil
}
