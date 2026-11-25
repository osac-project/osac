// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package ovn

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var _ = Describe("Kind OVN status fixture", func() {
	It("reports accepted CUDN status", func(ctx SpecContext) {
		scheme := runtime.NewScheme()
		Expect(AddToScheme(scheme)).To(Succeed())

		object := CUDNObject()
		object.SetName("network-a")
		object.Labels = map[string]string{managerLabel: "cudn_evpn"}

		store := fake.NewClientBuilder().WithScheme(scheme).WithObjects(object).
			WithStatusSubresource(CUDNObject(), VTEPObject()).Build()
		reconciler := &CUDNReconciler{Client: store, APIReader: store}
		request := ctrlRequest(object)

		result, err := reconciler.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeZero())

		for _, condition := range []string{"TransportAccepted", "NetworkCreated", "NetworkAllocationSucceeded"} {
			Expect(cudnCondition(ctx, store, object, condition)).To(Equal("True"))
		}
	})

	It("accepts the configured tenant VTEP", func(ctx SpecContext) {
		scheme := runtime.NewScheme()
		Expect(AddToScheme(scheme)).To(Succeed())

		object := VTEPObject()
		object.SetName("tenant-vtep")
		store := fake.NewClientBuilder().WithScheme(scheme).WithObjects(object).
			WithStatusSubresource(VTEPObject()).Build()
		reconciler := &VTEPReconciler{Client: store, APIReader: store}

		_, err := reconciler.Reconcile(ctx, ctrlRequest(object))
		Expect(err).NotTo(HaveOccurred())

		current := VTEPObject()
		Expect(store.Get(ctx, client.ObjectKey{Name: object.GetName()}, current)).To(Succeed())
		Expect(current.Status.Conditions).To(HaveLen(1))
		Expect(current.Status.Conditions[0].Type).To(Equal("Accepted"))
		Expect(current.Status.Conditions[0].Status).To(Equal(metav1.ConditionTrue))
	})
})

// ctrlRequest creates the request key for one cluster-scoped fixture object.
func ctrlRequest(object client.Object) ctrl.Request {
	return ctrl.Request{NamespacedName: client.ObjectKeyFromObject(object)}
}

// cudnCondition reads one condition status from the fixture's CUDN object.
func cudnCondition(ctx context.Context, store client.Client, object *CUDN, kind string) string {
	current := CUDNObject()
	Expect(store.Get(ctx, client.ObjectKeyFromObject(object), current)).To(Succeed())

	for _, status := range current.Status.Conditions {
		if status.Type == kind {
			return string(status.Status)
		}
	}

	Fail("condition was not found: " + kind)

	return ""
}
