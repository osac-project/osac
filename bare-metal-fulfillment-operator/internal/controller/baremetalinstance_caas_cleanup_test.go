package controller

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
)

func TestCaaSAgentCleanupFinalizerBlocksHostOffboarding(t *testing.T) {
	r := &BareMetalInstanceReconciler{}
	bmi := &v1alpha1.BareMetalInstance{ObjectMeta: metav1.ObjectMeta{
		Finalizers: []string{bareMetalInstanceCaaSAgentCleanupFinalizer},
	}}

	result, err := r.handleDeletion(context.Background(), bmi)
	if err != nil {
		t.Fatalf("handleDeletion() error = %v", err)
	}
	if result.RequeueAfter != DefaultManagementRecheckIntervalDuration {
		t.Fatalf("requeue = %s, want %s", result.RequeueAfter, DefaultManagementRecheckIntervalDuration)
	}
	if bmi.Status.Phase != v1alpha1.BareMetalInstancePhaseDeleting {
		t.Fatalf("phase = %q, want Deleting", bmi.Status.Phase)
	}
	if bmi.Spec.ExternalHostID != "" {
		t.Fatalf("host was touched before CaaS cleanup: %q", bmi.Spec.ExternalHostID)
	}
}
