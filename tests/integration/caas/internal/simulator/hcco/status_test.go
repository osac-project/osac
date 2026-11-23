// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package hcco

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/osac-project/osac/tests/integration/caas/internal/simulator/kube"
)

var _ = Describe("Guest-version observations", func() {
	It("keeps the synthetic guest observation independent of CPO readiness", func(ctx SpecContext) {
		scheme := runtime.NewScheme()
		Expect(hypershiftv1.AddToScheme(scheme)).To(Succeed())

		cp := &hypershiftv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{Namespace: "cp", Name: "cluster"},
			Spec: hypershiftv1.HostedControlPlaneSpec{
				Platform: hypershiftv1.PlatformSpec{
					Type:  hypershiftv1.NonePlatform,
					Agent: &hypershiftv1.AgentPlatformSpec{AgentNamespace: "hub"},
				},
			},
		}
		meta.SetStatusCondition(&cp.Status.Conditions, metav1.Condition{
			Type: "Available", Status: metav1.ConditionFalse, Reason: "Starting",
			Message: "Simulated provider observation", ObservedGeneration: 1,
		})
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cp).WithStatusSubresource(cp).Build()
		r := &StatusReconciler{Client: c, APIReader: c, Scope: kube.Scope{AgentNamespaces: []string{"hub"}}}
		request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cp)}

		for _, step := range []struct {
			description string
			ready       bool
		}{
			{"observing a starting control plane", false},
			{"observing a ready control plane", true},
			{"retaining the guest observation when CPO readiness is lost", false},
		} {
			By(step.description)

			current := &hypershiftv1.HostedControlPlane{}
			Expect(c.Get(ctx, request.NamespacedName, current)).To(Succeed())
			next := current.DeepCopy()

			next.Status.Ready = step.ready
			status := metav1.ConditionFalse
			if step.ready {
				status = metav1.ConditionTrue
			}

			meta.SetStatusCondition(&next.Status.Conditions, metav1.Condition{
				Type: "Available", Status: status, Reason: "ProviderObservation",
			})
			Expect(c.Status().Patch(ctx, next,
				client.MergeFromWithOptions(current, client.MergeFromWithOptimisticLock{}))).To(Succeed())
			available := *meta.FindStatusCondition(next.Status.Conditions, "Available")

			_, err := r.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			observed := &hypershiftv1.HostedControlPlane{}
			Expect(c.Get(ctx, request.NamespacedName, observed)).To(Succeed())
			Expect(meta.IsStatusConditionFalse(observed.Status.Conditions, "ClusterVersionFailing")).To(BeTrue())
			Expect(meta.FindStatusCondition(observed.Status.Conditions, "Available")).To(Equal(&available))
		}
	})
})
