package baremetalworker

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

func TestClusterDeletionWaitsForClusterTeardownReady(t *testing.T) {
	co := &v1alpha1.ClusterOrder{
		ObjectMeta: metav1.ObjectMeta{Finalizers: []string{bmWorkerFinalizer}},
		Spec:       v1alpha1.ClusterOrderSpec{SupportsAutomaticCaaSEndpoints: true},
		Status: v1alpha1.ClusterOrderStatus{Workers: []v1alpha1.WorkerStatus{{
			Name: "worker-0", Kind: workerKindBMI, Phase: workerPhaseReady,
		}}},
	}
	r := &Reconciler{}

	result, err := r.handleClusterDeletion(context.Background(), co)
	if err != nil {
		t.Fatalf("handleClusterDeletion() error = %v", err)
	}
	if result.RequeueAfter != teardownRequeueInterval {
		t.Fatalf("requeue = %s, want %s", result.RequeueAfter, teardownRequeueInterval)
	}
	if co.Status.Workers[0].Phase != workerPhaseReady {
		t.Fatalf("worker phase changed before teardown gate: %s", co.Status.Workers[0].Phase)
	}
}
