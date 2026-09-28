// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package baremetalworker

import (
	"context"
	"errors"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type cleanupDeleteClient struct {
	*teardownReadClient
	deleteErr error
}

func (f *cleanupDeleteClient) DeleteBareMetalInstance(context.Context, string) error {
	f.deletes++
	if f.returned != nil {
		f.returned.GetMetadata().SetDeletionTimestamp(timestamppb.Now())
	}
	return f.deleteErr
}

func TestR03LostDeleteAcknowledgement(t *testing.T) {
	r, base, co := workerReadHarness(t)
	lost := errors.New("lost acknowledgement")
	fc := &cleanupDeleteClient{teardownReadClient: &teardownReadClient{workerReadClient: base}, deleteErr: lost}
	r.fulfillment = fc
	co.Status.Workers[0].Phase = workerPhaseFailed
	if err := r.Status().Update(context.Background(), co); err != nil {
		t.Fatal(err)
	}
	w := co.Status.Workers[0]
	fc.returned = ownedBMIFixture(co, w.BareMetalInstance.Name, w.BareMetalInstance.ID)
	if err := r.handleFailedWorkers(context.Background(), co); !errors.Is(err, lost) {
		t.Fatalf("lost response: %v", err)
	}
	for range 2 {
		if err := r.handleFailedWorkers(context.Background(), co); err != nil {
			t.Fatal(err)
		}
		if fc.deletes != 1 || co.Status.Workers[0].BareMetalInstance.ID != w.BareMetalInstance.ID || co.Status.Workers[0].AttemptCount != 0 {
			t.Fatalf("persisted pending Delete repeated/released: %+v deletes=%d", co.Status.Workers, fc.deletes)
		}
	}
	fc.returned = nil
	base.getErr = status.Error(codes.NotFound, "archived")
	if err := r.handleFailedWorkers(context.Background(), co); err != nil {
		t.Fatal(err)
	}
	if co.Status.Workers[0].AttemptCount != 1 || co.Status.Workers[0].BareMetalInstance.ID != "" {
		t.Fatalf("missing retry checkpoint: %+v", co.Status.Workers)
	}
}

func TestR03CleanupRejectsChangedSlot(t *testing.T) {
	r, base, co := workerReadHarness(t)
	fc := &teardownReadClient{workerReadClient: base}
	r.fulfillment = fc
	w := co.Status.Workers[0]
	w.Phase = workerPhaseDeleting
	fc.returned = ownedBMIFixture(co, w.BareMetalInstance.Name, w.BareMetalInstance.ID)
	latest := co.DeepCopy()
	latest.Status.Workers[0].BareMetalInstance.ID = "concurrent-reference"
	if err := r.Status().Update(context.Background(), latest); err != nil {
		t.Fatal(err)
	}
	gone, err := r.cleanupWorker(context.Background(), co, &w)
	if err == nil || gone || fc.deletes != 0 {
		t.Fatalf("changed slot authorized deletion: gone=%v err=%v deletes=%d", gone, err, fc.deletes)
	}
}

func TestR03RecoverIDLessRetirement(t *testing.T) {
	r, base, co := workerReadHarness(t)
	fc := &teardownReadClient{workerReadClient: base}
	r.fulfillment = fc
	w := co.Status.Workers[0]
	w.Phase = workerPhaseUnbinding
	fc.returned = ownedBMIFixture(co, w.BareMetalInstance.Name, w.BareMetalInstance.ID)
	base.listed = []*privatev1.BareMetalInstance{fc.returned}
	w.BareMetalInstance.ID = ""
	gone, err := r.cleanupWorker(context.Background(), co, &w)
	if err != nil || gone || fc.deletes != 0 || w.BareMetalInstance.ID != fc.returned.GetId() {
		t.Fatalf("name recovery boundary: %+v gone=%v err=%v deletes=%d", w, gone, err, fc.deletes)
	}
}

type cleanupRaceClient struct {
	client.Client
	beforeDelete func()
}

func (c *cleanupRaceClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	c.beforeDelete()
	// controller-runtime's fake does not enforce UID Delete preconditions.
	// Check the caller's options and simulate the API rejection here; real
	// apiserver precondition behavior belongs to acceptance coverage.
	options := (&client.DeleteOptions{}).ApplyOptions(opts)
	if options.Preconditions == nil || options.Preconditions.UID == nil || *options.Preconditions.UID != obj.GetUID() {
		return c.Client.Delete(ctx, obj, opts...)
	}
	return errors.New("UID precondition rejected recreated Agent")
}

func TestR03AgentUIDPrecondition(t *testing.T) {
	r, base, co := workerReadHarness(t)
	fc := &teardownReadClient{workerReadClient: base}
	r.fulfillment = fc
	w := co.Status.Workers[0]
	w.Phase = workerPhaseUnbinding
	fc.returned = ownedBMIFixture(co, w.BareMetalInstance.Name, w.BareMetalInstance.ID)
	agent := &unstructured.Unstructured{Object: map[string]interface{}{"spec": map[string]interface{}{}, "status": map[string]interface{}{"debugInfo": map[string]interface{}{"state": "known-unbound"}}}}
	agent.SetGroupVersionKind(agentGVK)
	agent.SetName("agent")
	agent.SetNamespace(co.Namespace)
	agent.SetUID("old")
	agent.SetLabels(map[string]string{workerNameLabel: w.Name})
	ctx := context.Background()
	if err := r.Create(ctx, agent); err != nil {
		t.Fatal(err)
	}
	kube := r.Client
	r.Client = &cleanupRaceClient{Client: kube, beforeDelete: func() {
		if err := kube.Delete(ctx, agent); err != nil {
			t.Fatal(err)
		}
		replacement := agent.DeepCopy()
		replacement.SetResourceVersion("")
		replacement.SetUID("successor")
		if err := kube.Create(ctx, replacement); err != nil {
			t.Fatal(err)
		}
	}}
	gone, err := r.cleanupWorker(ctx, co, &w)
	if err == nil || gone || fc.deletes != 0 {
		t.Fatalf("UID race bypassed: gone=%v err=%v deletes=%d", gone, err, fc.deletes)
	}
	latest := &unstructured.Unstructured{}
	latest.SetGroupVersionKind(agentGVK)
	if err := kube.Get(ctx, client.ObjectKeyFromObject(agent), latest); err != nil {
		t.Fatal(err)
	}
	if latest.GetUID() != "successor" || !latest.GetDeletionTimestamp().IsZero() {
		t.Fatalf("successor mutated: %v", latest)
	}
}

func TestR03BoundFailurePreservesRetryCategory(t *testing.T) {
	r, _, co := workerReadHarness(t)
	w := co.Status.Workers[0]
	w.Phase = workerPhaseFailed
	w.LastFailureReason = eventReasonAgentRegistrationTimeout
	past := metav1.NewTime(time.Now().Add(-time.Hour))
	w.LastFailureTime = &past
	agent := &unstructured.Unstructured{Object: map[string]interface{}{"status": map[string]interface{}{"debugInfo": map[string]interface{}{"state": "installed"}}}}
	agent.SetName("bound")
	if err := r.requestCleanupAgent(context.Background(), co, &w, agent); err != nil {
		t.Fatal(err)
	}
	if w.LastFailureReason != eventReasonAgentRegistrationTimeout || !w.LastFailureTime.Equal(&past) {
		t.Fatalf("cleanup changed retry category/history: %+v", w)
	}
}

func TestR03TeardownEndsInvocation(t *testing.T) {
	r, base, co := workerReadHarness(t)
	fc := &teardownReadClient{workerReadClient: base}
	r.fulfillment = fc
	co.Status.Workers[0].Phase = workerPhaseUnbinding
	if err := r.Status().Update(context.Background(), co); err != nil {
		t.Fatal(err)
	}
	w := co.Status.Workers[0]
	fc.returned = ownedBMIFixture(co, w.BareMetalInstance.Name, w.BareMetalInstance.ID)
	agent := &unstructured.Unstructured{Object: map[string]interface{}{"spec": map[string]interface{}{}, "status": map[string]interface{}{"debugInfo": map[string]interface{}{"state": "known-unbound"}}}}
	agent.SetGroupVersionKind(agentGVK)
	agent.SetName("old-agent")
	agent.SetNamespace(co.Namespace)
	agent.SetUID("old")
	agent.SetLabels(map[string]string{workerNameLabel: w.Name})
	if err := r.Create(context.Background(), agent); err != nil {
		t.Fatal(err)
	}
	o := indexWorkerBMIs(nil)
	o.agents = &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*agent}}
	stop, err := r.reconcileWorkerTeardown(context.Background(), co)
	if err != nil || !stop || fc.deletes != 0 {
		t.Fatalf("Agent mutation must end the invocation: stop=%v err=%v deletes=%d", stop, err, fc.deletes)
	}
}

func TestR03AgentCleanupBeforeBMI(t *testing.T) {
	for _, state := range []string{"bound", "unbinding-but-bound", "detached", "ambiguous", "malformed", "malformed-lookup", "malformed-lookup-entry", "foreign"} {
		t.Run(state, func(t *testing.T) {
			r, base, co := workerReadHarness(t)
			fc := &teardownReadClient{workerReadClient: base}
			r.fulfillment = fc
			w := co.Status.Workers[0]
			w.Phase = workerPhaseDeleting
			fc.returned = ownedBMIFixture(co, w.BareMetalInstance.Name, w.BareMetalInstance.ID)
			agent := &unstructured.Unstructured{Object: map[string]interface{}{"spec": map[string]interface{}{}, "status": map[string]interface{}{"debugInfo": map[string]interface{}{"state": "known-unbound"}}}}
			agent.SetGroupVersionKind(agentGVK)
			agent.SetName("old-agent")
			agent.SetNamespace(co.Namespace)
			agent.SetUID("old-uid")
			agent.SetLabels(map[string]string{workerNameLabel: w.Name, clusterOrderLabel: co.Name})
			switch state {
			case "bound":
				agent.SetLabels(map[string]string{workerNameLabel: w.Name, "agentMachineRef": "machine"})
			case "unbinding-but-bound":
				agent.SetLabels(map[string]string{workerNameLabel: w.Name, "agentMachineRef": "machine"})
				if err := unstructured.SetNestedField(agent.Object, agentUnbindingState, "status", "debugInfo", "state"); err != nil {
					t.Fatal(err)
				}
			case "malformed-lookup":
				agent.SetLabels(map[string]string{clusterOrderLabel: co.Name})
				if err := unstructured.SetNestedField(agent.Object, "invalid", "status", "inventory", "interfaces"); err != nil {
					t.Fatal(err)
				}
			case "malformed-lookup-entry":
				agent.SetLabels(map[string]string{clusterOrderLabel: co.Name})
				if err := unstructured.SetNestedSlice(agent.Object, []interface{}{"invalid"}, "status", "inventory", "interfaces"); err != nil {
					t.Fatal(err)
				}
			case "detached":
				agent.SetFinalizers([]string{"test/hold"})
			case "malformed":
				agent.Object["status"] = "invalid"
			case "foreign":
				agent.SetLabels(map[string]string{workerNameLabel: w.Name, clusterOrderLabel: "foreign"})
			}
			if err := r.Create(context.Background(), agent); err != nil {
				t.Fatal(err)
			}
			if state == "ambiguous" {
				other := agent.DeepCopy()
				other.SetName("other-agent")
				other.SetResourceVersion("")
				other.SetUID("other-uid")
				if err := r.Create(context.Background(), other); err != nil {
					t.Fatal(err)
				}
			}
			o := indexWorkerBMIs(nil)
			o.agents = &unstructured.UnstructuredList{} // Deliberate cached omission.
			for range 2 {
				kept := r.reconcileTeardownWorkers(context.Background(), co, []v1alpha1.WorkerStatus{w})
				if len(kept) != 1 || fc.deletes != 0 {
					t.Fatalf("old Agent bypassed: workers=%+v deletes=%d", kept, fc.deletes)
				}
			}
			latest := &unstructured.Unstructured{}
			latest.SetGroupVersionKind(agentGVK)
			if err := r.Get(context.Background(), client.ObjectKeyFromObject(agent), latest); err != nil {
				t.Fatal(err)
			}
			if latest.GetDeletionTimestamp().IsZero() != (state != "detached") {
				t.Fatalf("unsafe Agent deletion: %+v", latest)
			}
		})
	}
}
