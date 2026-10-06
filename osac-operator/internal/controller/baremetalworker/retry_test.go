/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package baremetalworker

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func TestR03FailedReservationRecoversBeforeRetry(t *testing.T) {
	r, base, co := workerReadHarness(t)
	fc := &teardownReadClient{workerReadClient: base}
	r.fulfillment = fc
	w := &co.Status.Workers[0]
	fc.returned = ownedBMIFixture(co, w.BareMetalInstance.Name, w.BareMetalInstance.ID)
	base.listed = []*privatev1.BareMetalInstance{fc.returned}
	w.Phase = workerPhaseFailed
	w.BareMetalInstance.ID = ""
	if err := r.Status().Update(context.Background(), co); err != nil {
		t.Fatal(err)
	}
	if err := r.handleFailedWorkers(context.Background(), co); err != nil {
		t.Fatal(err)
	}
	got := co.Status.Workers[0] // Status().Update can replace the slice; do not assert through its old pointer.
	if got.BareMetalInstance.ID != fc.returned.GetId() || got.NextRetryTime != nil || got.AttemptCount != 0 || fc.deletes != 0 {
		t.Fatalf("failed reservation bypassed recovery: %+v deletes=%d", got, fc.deletes)
	}
}

func TestR03RetryKeepsIDUntilAbsent(t *testing.T) {
	r, base, co := workerReadHarness(t)
	fc := &teardownReadClient{workerReadClient: base}
	r.fulfillment = fc
	co.Status.Workers[0].Phase = workerPhaseFailed
	if err := r.Status().Update(context.Background(), co); err != nil {
		t.Fatal(err)
	}
	w := co.Status.Workers[0]
	fc.returned = ownedBMIFixture(co, w.BareMetalInstance.Name, w.BareMetalInstance.ID)
	for range 2 {
		if err := r.handleFailedWorkers(context.Background(), co); err != nil {
			t.Fatal(err)
		}
		got := co.Status.Workers[0]
		if got.BareMetalInstance.ID != w.BareMetalInstance.ID || got.AttemptCount != 0 || got.NextRetryTime != nil {
			t.Fatalf("released retry identity before absence: %+v", got)
		}
	}
	fc.returned = nil
	base.getErr = status.Error(codes.NotFound, "confirmed archived")
	if err := r.handleFailedWorkers(context.Background(), co); err != nil {
		t.Fatal(err)
	}
	got := co.Status.Workers[0]
	if got.BareMetalInstance.ID != "" || got.AttemptCount != 1 || got.NextRetryTime == nil || got.ReadySince != nil {
		t.Fatalf("retry checkpoint: %+v", got)
	}
	deadline := got.NextRetryTime.DeepCopy()
	if err := r.handleFailedWorkers(context.Background(), co); err != nil {
		t.Fatal(err)
	}
	if co.Status.Workers[0].AttemptCount != 1 || !co.Status.Workers[0].NextRetryTime.Equal(deadline) {
		t.Fatal("retry checkpoint scheduled twice")
	}
}

func TestR04WorkerRecheckDeadline(t *testing.T) {
	r := &Reconciler{}
	now := time.Now()
	future := metav1.NewTime(now.Add(2 * time.Minute))
	readySince := metav1.NewTime(now.Add(-time.Minute))
	bmi := func(name, id, phase string) v1alpha1.WorkerStatus {
		w := newWorkerStatus("standard", "standard", name, id, phase)
		return w
	}
	pendingCleanup := bmi("cleanup", "cleanup-id", workerPhaseFailed)
	futureRetry := bmi("retry", "", workerPhaseFailed)
	futureRetry.NextRetryTime = &future
	healthyReset := bmi("ready", "ready-id", workerPhaseReady)
	healthyReset.AttemptCount = 1
	healthyReset.ReadySince = &readySince
	tests := []struct {
		name    string
		workers []v1alpha1.WorkerStatus
		want    time.Duration
		max     time.Duration
	}{
		{"stable ready order holds no timer", []v1alpha1.WorkerStatus{bmi("a", "a-id", workerPhaseReady), bmi("b", "b-id", workerPhaseReady)}, 0, 0},
		{"provisioning is rechecked", []v1alpha1.WorkerStatus{bmi("a", "", workerPhaseProvisioning)}, agentRequeueInterval, agentRequeueInterval},
		{"waiting for agent is rechecked", []v1alpha1.WorkerStatus{bmi("a", "a-id", workerPhaseWaitingForAgent)}, agentRequeueInterval, agentRequeueInterval},
		{"binding is rechecked", []v1alpha1.WorkerStatus{bmi("a", "a-id", workerPhaseBinding)}, agentRequeueInterval, agentRequeueInterval},
		{"pending cleanup is rechecked", []v1alpha1.WorkerStatus{pendingCleanup}, teardownRequeueInterval, teardownRequeueInterval},
		{"deleting is rechecked", []v1alpha1.WorkerStatus{bmi("a", "a-id", workerPhaseDeleting)}, teardownRequeueInterval, teardownRequeueInterval},
		{"future retry waits for its deadline", []v1alpha1.WorkerStatus{futureRetry}, time.Second, 2 * time.Minute},
		{"healthy reset keeps a timer", []v1alpha1.WorkerStatus{healthyReset}, time.Second, minHealthyDuration},
		{"shortest positive deadline wins", []v1alpha1.WorkerStatus{futureRetry, bmi("a", "", workerPhaseProvisioning), pendingCleanup}, agentRequeueInterval, agentRequeueInterval},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.workerRecheckDeadline(tt.workers, now)
			if tt.want == 0 {
				if !got.IsZero() {
					t.Fatalf("stable workers must rely on watches, got %+v", got)
				}
				return
			}
			if got.RequeueAfter < tt.want || got.RequeueAfter > tt.max {
				t.Fatalf("deadline=%v, want within [%v, %v]", got.RequeueAfter, tt.want, tt.max)
			}
		})
	}
}

var _ = Describe("ClassifyFailure", func() {
	DescribeTable("maps failure reasons to categories",
		func(reason string, want FailureCategory) {
			Expect(ClassifyFailure(reason)).To(Equal(want))
		},
		Entry("agent timeout", "AgentRegistrationTimeout", FailureCategoryAgentTimeout),
		Entry("no host available", "NoHostAvailable", FailureCategoryResource),
		Entry("resource exhausted", "ResourceExhausted", FailureCategoryResource),
		Entry("transient infra error", "InfrastructureError", FailureCategoryTransient),
		Entry("unknown reason", "SomethingUnexpected", FailureCategoryTransient),
		Entry("empty reason", "", FailureCategoryTransient),
		Entry("BMI creation failed", "BMICreationFailed", FailureCategoryTransient),
		Entry("provisioning failed", "ProvisioningFailed", FailureCategoryTransient),
	)
})

var _ = Describe("ComputeBackoff", func() {
	DescribeTable("computes escalating backoff with caps",
		func(category FailureCategory, attempt int32, want time.Duration) {
			Expect(ComputeBackoff(category, attempt)).To(Equal(want))
		},
		// Transient: 30s, 60s, 120s, cap 5m
		Entry("transient attempt 1", FailureCategoryTransient, int32(1), 30*time.Second),
		Entry("transient attempt 2", FailureCategoryTransient, int32(2), 60*time.Second),
		Entry("transient attempt 3", FailureCategoryTransient, int32(3), 120*time.Second),
		Entry("transient attempt 4 capped", FailureCategoryTransient, int32(4), 5*time.Minute),
		Entry("transient attempt 10 capped", FailureCategoryTransient, int32(10), 5*time.Minute),

		// Resource: 5m, 15m, 30m, cap 30m
		Entry("resource attempt 1", FailureCategoryResource, int32(1), 5*time.Minute),
		Entry("resource attempt 2", FailureCategoryResource, int32(2), 15*time.Minute),
		Entry("resource attempt 3", FailureCategoryResource, int32(3), 30*time.Minute),
		Entry("resource attempt 4 capped", FailureCategoryResource, int32(4), 30*time.Minute),

		// Agent timeout: same schedule as resource
		Entry("agent timeout attempt 1", FailureCategoryAgentTimeout, int32(1), 5*time.Minute),
		Entry("agent timeout attempt 2", FailureCategoryAgentTimeout, int32(2), 15*time.Minute),
		Entry("agent timeout attempt 3", FailureCategoryAgentTimeout, int32(3), 30*time.Minute),
		Entry("agent timeout attempt 4 capped", FailureCategoryAgentTimeout, int32(4), 30*time.Minute),

		// Edge: attempt 0 treated as attempt 1
		Entry("transient attempt 0", FailureCategoryTransient, int32(0), 30*time.Second),
	)
})

var _ = Describe("FormatWorkersFailed", func() {
	var retryTime metav1.Time

	BeforeEach(func() {
		retryTime = metav1.NewTime(time.Date(2026, 8, 26, 12, 0, 30, 0, time.UTC))
	})

	It("returns empty string for no failed workers", func() {
		workers := []v1alpha1.WorkerStatus{{Name: "w-0", Phase: workerPhaseReady}}
		Expect(FormatWorkersFailed(workers)).To(BeEmpty())
	})

	It("formats one failed worker", func() {
		workers := []v1alpha1.WorkerStatus{
			{Name: "w-0", Phase: workerPhaseFailed, AttemptCount: 2, NextRetryTime: &retryTime},
		}
		Expect(FormatWorkersFailed(workers)).To(Equal(
			"retry 1: attempt 2, next retry " + retryTime.UTC().Format(time.RFC3339),
		))
	})

	It("formats multiple failed workers", func() {
		workers := []v1alpha1.WorkerStatus{
			{Name: "w-0", Phase: workerPhaseReady},
			{Name: "w-1", Phase: workerPhaseFailed, AttemptCount: 1, NextRetryTime: &retryTime},
			{Name: "w-2", Phase: workerPhaseFailed, AttemptCount: 3, NextRetryTime: &retryTime},
		}
		Expect(FormatWorkersFailed(workers)).To(Equal(
			"retry 1: attempt 1, next retry " + retryTime.UTC().Format(time.RFC3339) +
				"; retry 2: attempt 3, next retry " + retryTime.UTC().Format(time.RFC3339),
		))
	})

	It("shows pending when next retry time is nil", func() {
		workers := []v1alpha1.WorkerStatus{
			{Name: "w-0", Phase: workerPhaseFailed, AttemptCount: 1},
		}
		Expect(FormatWorkersFailed(workers)).To(Equal("retry 1: attempt 1, next retry pending"))
	})
})
