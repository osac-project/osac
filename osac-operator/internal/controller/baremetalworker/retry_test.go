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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

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
