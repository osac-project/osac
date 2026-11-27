package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
)

var _ = It("blocks host offboarding until CaaS agent cleanup completes", func() {
	r := &BareMetalInstanceReconciler{}
	bmi := &v1alpha1.BareMetalInstance{ObjectMeta: metav1.ObjectMeta{
		Finalizers: []string{bareMetalInstanceCaaSAgentCleanupFinalizer},
	}}

	result, err := r.handleDeletion(context.Background(), bmi)
	Expect(err).NotTo(HaveOccurred())
	Expect(result.RequeueAfter).To(Equal(DefaultManagementRecheckIntervalDuration))
	Expect(bmi.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseDeleting))
	Expect(bmi.Spec.ExternalHostID).To(BeEmpty(), "host was touched before CaaS cleanup")
})
