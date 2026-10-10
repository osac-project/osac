// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package baremetalworker

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

func handoffFixture() (*v1alpha1.ClusterOrder, *unstructured.Unstructured) {
	GinkgoHelper()
	co := &v1alpha1.ClusterOrder{
		ObjectMeta: metav1.ObjectMeta{Name: "order", Namespace: "osac"},
		Status: v1alpha1.ClusterOrderStatus{ClusterReference: &v1alpha1.ClusterOrderClusterReferenceType{
			HostedClusterName: "hosted", Namespace: "osac-order",
		}},
	}
	a := agentPhaseFixture("worker", false)
	Expect(unstructured.SetNestedMap(a.Object, map[string]interface{}{
		"name": "hosted", "namespace": "osac-order-hosted",
	}, "spec", "clusterDeploymentName")).To(Succeed())
	return co, a
}

var _ = Describe("CAP-Agent handoff phase projection", func() {
	for _, installed := range []bool{false, true} {
		want := workerPhaseBinding
		if installed {
			want = workerPhaseReady
		}
		It(want, func() {
			co, a := handoffFixture()
			if installed {
				Expect(unstructured.SetNestedField(a.Object, "installed", "status", "debugInfo", "state")).To(Succeed())
			}
			before := a.DeepCopy()
			worker := newWorkerStatus("standard", "workers", "worker", "bmi-id", workerPhaseBinding)
			got, err := projectAgentWorkerPhases(co, []v1alpha1.WorkerStatus{worker}, &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*a}})
			Expect(err).NotTo(HaveOccurred())
			Expect(got[0].Phase).To(Equal(want), "handoff projection = %+v, want phase %s with original BMI", got[0], want)
			Expect(got[0].BareMetalInstance).To(Equal(worker.BareMetalInstance), "handoff projection = %+v, want phase %s with original BMI", got[0], want)
			Expect(a).To(Equal(before), "projection changed CAP-Agent's binding")
		})
	}
})

var _ = It("limits CAP-Agent handoff to the expected cluster and NodePool", func() {
	type scopeCase struct {
		name    string
		change  func(*v1alpha1.ClusterOrder, *unstructured.Unstructured)
		wantErr bool
	}
	tests := make([]scopeCase, 0, 17)
	tests = append(tests, []scopeCase{
		{name: "exact established handoff"},
		{name: "legacy cluster label", change: func(_ *v1alpha1.ClusterOrder, a *unstructured.Unstructured) {
			a.SetLabels(map[string]string{workerNameLabel: "worker", "osac.openshift.io/clusterorder": "order"})
		}},
		{name: "foreign Agent namespace", wantErr: true, change: func(_ *v1alpha1.ClusterOrder, a *unstructured.Unstructured) { a.SetNamespace("foreign") }},
		{name: "missing cluster reference", wantErr: true, change: func(co *v1alpha1.ClusterOrder, _ *unstructured.Unstructured) { co.Status.ClusterReference = nil }},
		{name: "missing HostedCluster name", wantErr: true, change: func(co *v1alpha1.ClusterOrder, _ *unstructured.Unstructured) {
			co.Status.ClusterReference.HostedClusterName = ""
		}},
		{name: "missing HostedCluster namespace", wantErr: true, change: func(co *v1alpha1.ClusterOrder, _ *unstructured.Unstructured) {
			co.Status.ClusterReference.Namespace = ""
		}},
		{name: "missing worker label", wantErr: true, change: func(_ *v1alpha1.ClusterOrder, a *unstructured.Unstructured) {
			a.SetLabels(map[string]string{clusterOrderLabel: "order"})
		}},
		{name: "missing cluster label", wantErr: true, change: func(_ *v1alpha1.ClusterOrder, a *unstructured.Unstructured) {
			a.SetLabels(map[string]string{workerNameLabel: "worker"})
		}},
		{name: "foreign worker label", wantErr: true, change: func(_ *v1alpha1.ClusterOrder, a *unstructured.Unstructured) {
			a.SetLabels(map[string]string{clusterOrderLabel: "order", workerNameLabel: "other"})
		}},
		{name: "foreign cluster label", wantErr: true, change: func(_ *v1alpha1.ClusterOrder, a *unstructured.Unstructured) {
			a.SetLabels(map[string]string{clusterOrderLabel: "foreign", workerNameLabel: "worker"})
		}},
		{name: "conflicting legacy cluster label", wantErr: true, change: func(_ *v1alpha1.ClusterOrder, a *unstructured.Unstructured) {
			a.SetLabels(map[string]string{clusterOrderLabel: "order", "osac.openshift.io/clusterorder": "foreign", workerNameLabel: "worker"})
		}},
	}...)
	for _, field := range []string{"name", "namespace"} {
		for _, value := range []string{"foreign", "", "osac-order"} {
			tests = append(tests, scopeCase{name: "deployment " + field + "=" + value, wantErr: true, change: func(_ *v1alpha1.ClusterOrder, a *unstructured.Unstructured) {
				Expect(unstructured.SetNestedField(a.Object, value, "spec", "clusterDeploymentName", field)).To(Succeed())
			}})
		}
	}
	for _, tt := range tests {
		By(tt.name)
		func() {
			co, a := handoffFixture()
			if tt.change != nil {
				tt.change(co, a)
			}
			if err := agentBindingConflict(a, co, "worker"); (err != nil) != tt.wantErr {
				Fail(fmt.Sprintf("binding conflict = %v, want error %t", err, tt.wantErr))
			}
		}()
	}
})

var _ = It("preserves authoritative binding during CAP-Agent handoff", func() {
	co, live := handoffFixture()
	c := clientfake.NewClientBuilder().WithObjects(live).Build()
	r := &Reconciler{Client: c, apiReader: c}
	before := &unstructured.Unstructured{}
	before.SetGroupVersionKind(agentGVK)
	key := client.ObjectKeyFromObject(live)
	Expect(c.Get(context.Background(), key, before)).To(Succeed())
	// Initial discovery snapshot predates CAP-Agent claiming the labelled worker.
	stale := agentPhaseFixture("", false)
	worker := newWorkerStatus("standard", "workers", "worker", "bmi-id", workerPhaseWaitingForAgent)
	Expect(r.bindAgent(context.Background(), co, stale, &worker)).To(Succeed())
	got := &unstructured.Unstructured{}
	got.SetGroupVersionKind(agentGVK)
	Expect(c.Get(context.Background(), key, got)).To(Succeed())
	Expect(got).To(Equal(before), "stale discovery rewrote CAP-Agent's authoritative binding")
})
