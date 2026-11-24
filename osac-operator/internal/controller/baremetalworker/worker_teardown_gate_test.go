package baremetalworker

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

var _ = It("waits for cluster teardown before deleting workers", func() {
	co := &v1alpha1.ClusterOrder{
		ObjectMeta: metav1.ObjectMeta{Finalizers: []string{bmWorkerFinalizer}},
		Spec:       v1alpha1.ClusterOrderSpec{SupportsAutomaticCaaSEndpoints: true},
		Status: v1alpha1.ClusterOrderStatus{Workers: []v1alpha1.WorkerStatus{{
			Name: "worker-0", Kind: workerKindBMI, Phase: workerPhaseReady,
		}}},
	}
	r := &Reconciler{}

	result, err := r.handleClusterDeletion(context.Background(), co)
	Expect(err).NotTo(HaveOccurred())
	Expect(result.RequeueAfter).To(Equal(teardownRequeueInterval))
	Expect(co.Status.Workers[0].Phase).To(Equal(workerPhaseReady),
		"worker phase changed before teardown gate")
})
