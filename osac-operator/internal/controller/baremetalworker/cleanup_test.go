// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package baremetalworker

import (
	"context"
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

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

var _ = It("retains the BMI identity after a lost Delete acknowledgement", func() {
	r, base, co := workerReadHarness()
	lost := errors.New("lost acknowledgement")
	fc := &cleanupDeleteClient{teardownReadClient: &teardownReadClient{workerReadClient: base}, deleteErr: lost}
	r.fulfillment = fc
	co.Status.Workers[0].Phase = workerPhaseFailed
	Expect(r.Status().Update(context.Background(), co)).To(Succeed())
	w := co.Status.Workers[0]
	fc.returned = ownedBMIFixture(co, w.BareMetalInstance.Name, w.BareMetalInstance.ID)
	if err := r.handleFailedWorkers(context.Background(), co); !errors.Is(err, lost) {
		Fail(fmt.Sprintf("lost response: %v", err))
	}
	for range 2 {
		Expect(r.handleFailedWorkers(context.Background(), co)).To(Succeed())
		Expect(fc.deletes).To(Equal(1), "persisted pending Delete repeated/released: %+v deletes=%d", co.Status.Workers, fc.deletes)
		Expect(co.Status.Workers[0].BareMetalInstance.ID).To(Equal(w.BareMetalInstance.ID), "persisted pending Delete repeated/released: %+v deletes=%d", co.Status.Workers, fc.deletes)
		Expect(co.Status.Workers[0].AttemptCount).To(Equal(int32(0)), "persisted pending Delete repeated/released: %+v deletes=%d", co.Status.Workers, fc.deletes)
	}
	fc.returned = nil
	base.getErr = status.Error(codes.NotFound, "archived")
	Expect(r.handleFailedWorkers(context.Background(), co)).To(Succeed())
	Expect(co.Status.Workers[0].AttemptCount).To(Equal(int32(1)), "missing retry checkpoint: %+v", co.Status.Workers)
	Expect(co.Status.Workers[0].BareMetalInstance.ID).To(Equal(""), "missing retry checkpoint: %+v", co.Status.Workers)
})

var _ = It("rejects cleanup when the recorded worker slot has changed", func() {
	r, base, co := workerReadHarness()
	fc := &teardownReadClient{workerReadClient: base}
	r.fulfillment = fc
	w := co.Status.Workers[0]
	w.Phase = workerPhaseDeleting
	fc.returned = ownedBMIFixture(co, w.BareMetalInstance.Name, w.BareMetalInstance.ID)
	latest := co.DeepCopy()
	latest.Status.Workers[0].BareMetalInstance.ID = "concurrent-reference"
	Expect(r.Status().Update(context.Background(), latest)).To(Succeed())
	gone, err := r.cleanupWorker(context.Background(), co, &w)
	Expect(err).To(HaveOccurred(), "changed slot authorized deletion: gone=%v err=%v deletes=%d", gone, err, fc.deletes)
	Expect(gone).To(BeFalse(), "changed slot authorized deletion: gone=%v err=%v deletes=%d", gone, err, fc.deletes)
	Expect(fc.deletes).To(Equal(0), "changed slot authorized deletion: gone=%v err=%v deletes=%d", gone, err, fc.deletes)
})

var _ = It("recovers an ID-less worker before retirement", func() {
	r, base, co := workerReadHarness()
	fc := &teardownReadClient{workerReadClient: base}
	r.fulfillment = fc
	w := co.Status.Workers[0]
	w.Phase = workerPhaseUnbinding
	fc.returned = ownedBMIFixture(co, w.BareMetalInstance.Name, w.BareMetalInstance.ID)
	base.listed = []*privatev1.BareMetalInstance{fc.returned}
	w.BareMetalInstance.ID = ""
	gone, err := r.cleanupWorker(context.Background(), co, &w)
	Expect(err).NotTo(HaveOccurred(), "name recovery boundary: %+v gone=%v err=%v deletes=%d", w, gone, err, fc.deletes)
	Expect(gone).To(BeFalse(), "name recovery boundary: %+v gone=%v err=%v deletes=%d", w, gone, err, fc.deletes)
	Expect(fc.deletes).To(Equal(0), "name recovery boundary: %+v gone=%v err=%v deletes=%d", w, gone, err, fc.deletes)
	Expect(w.BareMetalInstance.ID).To(Equal(fc.returned.GetId()), "name recovery boundary: %+v gone=%v err=%v deletes=%d", w, gone, err, fc.deletes)
})

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

var _ = It("uses a UID precondition when deleting an Agent", func() {
	r, base, co := workerReadHarness()
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
	Expect(r.Create(ctx, agent)).To(Succeed())
	kube := r.Client
	r.Client = &cleanupRaceClient{Client: kube, beforeDelete: func() {
		Expect(kube.Delete(ctx, agent)).To(Succeed())
		replacement := agent.DeepCopy()
		replacement.SetResourceVersion("")
		replacement.SetUID("successor")
		Expect(kube.Create(ctx, replacement)).To(Succeed())
	}}
	gone, err := r.cleanupWorker(ctx, co, &w)
	Expect(err).To(HaveOccurred(), "UID race bypassed: gone=%v err=%v deletes=%d", gone, err, fc.deletes)
	Expect(gone).To(BeFalse(), "UID race bypassed: gone=%v err=%v deletes=%d", gone, err, fc.deletes)
	Expect(fc.deletes).To(Equal(0), "UID race bypassed: gone=%v err=%v deletes=%d", gone, err, fc.deletes)
	latest := &unstructured.Unstructured{}
	latest.SetGroupVersionKind(agentGVK)
	Expect(kube.Get(ctx, client.ObjectKeyFromObject(agent), latest)).To(Succeed())
	Expect(string(latest.GetUID())).To(Equal("successor"), "successor mutated: %v", latest)
	Expect(latest.GetDeletionTimestamp().IsZero()).To(BeTrue(), "successor mutated: %v", latest)
})

var _ = It("preserves the retry category for a bound failed worker", func() {
	r, _, co := workerReadHarness()
	w := co.Status.Workers[0]
	w.Phase = workerPhaseFailed
	w.LastFailureReason = eventReasonAgentRegistrationTimeout
	past := metav1.NewTime(time.Now().Add(-time.Hour))
	w.LastFailureTime = &past
	agent := &unstructured.Unstructured{Object: map[string]interface{}{"status": map[string]interface{}{"debugInfo": map[string]interface{}{"state": "installed"}}}}
	agent.SetName("bound")
	Expect(r.requestCleanupAgent(context.Background(), co, &w, agent)).To(Succeed())
	Expect(w.LastFailureReason).To(Equal(eventReasonAgentRegistrationTimeout), "cleanup changed retry category/history: %+v", w)
	Expect(w.LastFailureTime.Equal(&past)).To(BeTrue(), "cleanup changed retry category/history: %+v", w)
})

var _ = It("ends the invocation after a teardown mutation", func() {
	r, base, co := workerReadHarness()
	fc := &teardownReadClient{workerReadClient: base}
	r.fulfillment = fc
	co.Status.Workers[0].Phase = workerPhaseUnbinding
	Expect(r.Status().Update(context.Background(), co)).To(Succeed())
	w := co.Status.Workers[0]
	fc.returned = ownedBMIFixture(co, w.BareMetalInstance.Name, w.BareMetalInstance.ID)
	agent := &unstructured.Unstructured{Object: map[string]interface{}{"spec": map[string]interface{}{}, "status": map[string]interface{}{"debugInfo": map[string]interface{}{"state": "known-unbound"}}}}
	agent.SetGroupVersionKind(agentGVK)
	agent.SetName("old-agent")
	agent.SetNamespace(co.Namespace)
	agent.SetUID("old")
	agent.SetLabels(map[string]string{workerNameLabel: w.Name})
	Expect(r.Create(context.Background(), agent)).To(Succeed())
	o := indexWorkerBMIs(nil)
	o.agents = &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*agent}}
	stop, err := r.reconcileWorkerTeardown(context.Background(), co)
	Expect(err).NotTo(HaveOccurred(), "Agent mutation must end the invocation: stop=%v err=%v deletes=%d", stop, err, fc.deletes)
	Expect(stop).To(BeTrue(), "Agent mutation must end the invocation: stop=%v err=%v deletes=%d", stop, err, fc.deletes)
	Expect(fc.deletes).To(Equal(0), "Agent mutation must end the invocation: stop=%v err=%v deletes=%d", stop, err, fc.deletes)
})

var _ = Describe("Agent cleanup before BMI deletion", func() {
	for _, state := range []string{"bound", "unbinding-but-bound", "detached", "ambiguous", "malformed", "malformed-lookup", "malformed-lookup-entry", "foreign"} {
		It(state, func() {
			r, base, co := workerReadHarness()
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
				Expect(unstructured.SetNestedField(agent.Object, agentUnbindingState, "status", "debugInfo", "state")).To(Succeed())
			case "malformed-lookup":
				agent.SetLabels(map[string]string{clusterOrderLabel: co.Name})
				Expect(unstructured.SetNestedField(agent.Object, "invalid", "status", "inventory", "interfaces")).To(Succeed())
			case "malformed-lookup-entry":
				agent.SetLabels(map[string]string{clusterOrderLabel: co.Name})
				Expect(unstructured.SetNestedSlice(agent.Object, []interface{}{"invalid"}, "status", "inventory", "interfaces")).To(Succeed())
			case "detached":
				agent.SetFinalizers([]string{"test/hold"})
			case "malformed":
				agent.Object["status"] = "invalid"
			case "foreign":
				agent.SetLabels(map[string]string{workerNameLabel: w.Name, clusterOrderLabel: "foreign"})
			}
			Expect(r.Create(context.Background(), agent)).To(Succeed())
			if state == "ambiguous" {
				other := agent.DeepCopy()
				other.SetName("other-agent")
				other.SetResourceVersion("")
				other.SetUID("other-uid")
				Expect(r.Create(context.Background(), other)).To(Succeed())
			}
			o := indexWorkerBMIs(nil)
			o.agents = &unstructured.UnstructuredList{} // Deliberate cached omission.
			for range 2 {
				kept := r.reconcileTeardownWorkers(context.Background(), co, []v1alpha1.WorkerStatus{w})
				Expect(kept).To(HaveLen(1), "old Agent bypassed: workers=%+v deletes=%d", kept, fc.deletes)
				Expect(fc.deletes).To(Equal(0), "old Agent bypassed: workers=%+v deletes=%d", kept, fc.deletes)
			}
			latest := &unstructured.Unstructured{}
			latest.SetGroupVersionKind(agentGVK)
			Expect(r.Get(context.Background(), client.ObjectKeyFromObject(agent), latest)).To(Succeed())
			Expect(latest.GetDeletionTimestamp().IsZero()).To(Equal((state != "detached")), "unsafe Agent deletion: %+v", latest)
		})
	}
})
