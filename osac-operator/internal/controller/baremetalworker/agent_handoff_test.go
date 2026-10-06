// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package baremetalworker

import (
	"context"
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

func handoffFixture(t *testing.T) (*v1alpha1.ClusterOrder, *unstructured.Unstructured) {
	t.Helper()
	co := &v1alpha1.ClusterOrder{
		ObjectMeta: metav1.ObjectMeta{Name: "order", Namespace: "osac"},
		Status: v1alpha1.ClusterOrderStatus{ClusterReference: &v1alpha1.ClusterOrderClusterReferenceType{
			HostedClusterName: "hosted", Namespace: "osac-order",
		}},
	}
	a := agentPhaseFixture("worker", false)
	if err := unstructured.SetNestedMap(a.Object, map[string]interface{}{
		"name": "hosted", "namespace": "osac-order-hosted",
	}, "spec", "clusterDeploymentName"); err != nil {
		t.Fatal(err)
	}
	return co, a
}

func TestCAPAgentHandoffProjection(t *testing.T) {
	for _, installed := range []bool{false, true} {
		want := workerPhaseBinding
		if installed {
			want = workerPhaseReady
		}
		t.Run(want, func(t *testing.T) {
			co, a := handoffFixture(t)
			if installed {
				if err := unstructured.SetNestedField(a.Object, "installed", "status", "debugInfo", "state"); err != nil {
					t.Fatal(err)
				}
			}
			before := a.DeepCopy()
			worker := newWorkerStatus("standard", "workers", "worker", "bmi-id", workerPhaseBinding)
			got, err := projectAgentWorkerPhases(co, []v1alpha1.WorkerStatus{worker}, &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*a}})
			if err != nil {
				t.Fatal(err)
			}
			if got[0].Phase != want || got[0].BareMetalInstance != worker.BareMetalInstance {
				t.Fatalf("handoff projection = %+v, want phase %s with original BMI", got[0], want)
			}
			if !reflect.DeepEqual(a, before) {
				t.Fatal("projection changed CAP-Agent's binding")
			}
		})
	}
}

func TestCAPAgentHandoffScope(t *testing.T) {
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
				if err := unstructured.SetNestedField(a.Object, value, "spec", "clusterDeploymentName", field); err != nil {
					t.Fatal(err)
				}
			}})
		}
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			co, a := handoffFixture(t)
			if tt.change != nil {
				tt.change(co, a)
			}
			if err := agentBindingConflict(a, co, "worker"); (err != nil) != tt.wantErr {
				t.Fatalf("binding conflict = %v, want error %t", err, tt.wantErr)
			}
		})
	}
}

func TestCAPAgentHandoffPreservesAuthoritativeBinding(t *testing.T) {
	co, live := handoffFixture(t)
	c := clientfake.NewClientBuilder().WithObjects(live).Build()
	r := &Reconciler{Client: c, apiReader: c}
	before := &unstructured.Unstructured{}
	before.SetGroupVersionKind(agentGVK)
	key := client.ObjectKeyFromObject(live)
	if err := c.Get(context.Background(), key, before); err != nil {
		t.Fatal(err)
	}
	// Initial discovery snapshot predates CAP-Agent claiming the labelled worker.
	stale := agentPhaseFixture("", false)
	worker := newWorkerStatus("standard", "workers", "worker", "bmi-id", workerPhaseWaitingForAgent)
	if err := r.bindAgent(context.Background(), co, stale, &worker); err != nil {
		t.Fatal(err)
	}
	got := &unstructured.Unstructured{}
	got.SetGroupVersionKind(agentGVK)
	if err := c.Get(context.Background(), key, got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, before) {
		t.Fatal("stale discovery rewrote CAP-Agent's authoritative binding")
	}
}
