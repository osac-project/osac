/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

// mockBMIProvider implements BMIProvider for testing.
type mockBMIProvider struct {
	createCalls    int
	deleteCalls    int
	readyCalls     int
	regTimeCalls   int
	createErr      error
	deleteErr      error
	readyErr       error
	regTimeErr     error
	isReady        bool
	regTime        time.Time
	createdNames   []string
	deletedNames   []string
	nextCreateName string

	// Per-call overrides: when set, IsBMIReady returns the next entry
	// (popped from the front) instead of the default isReady/readyErr.
	readyResults []readyResult
}

// readyResult holds per-call return values for IsBMIReady.
type readyResult struct {
	ready bool
	err   error
}

func (m *mockBMIProvider) CreateBMI(_ context.Context, _ *v1alpha1.ClusterOrder, _ int) (string, string, error) {
	m.createCalls++
	if m.createErr != nil {
		return "", "", m.createErr
	}
	name := m.nextCreateName
	if name == "" {
		name = fmt.Sprintf("replacement-bmi-%d", m.createCalls)
	}
	m.createdNames = append(m.createdNames, name)
	return name, "osac-baremetalinstance", nil
}

func (m *mockBMIProvider) DeleteBMI(_ context.Context, name, _ string) error {
	m.deleteCalls++
	m.deletedNames = append(m.deletedNames, name)
	return m.deleteErr
}

func (m *mockBMIProvider) GetBMIRegistrationTime(_ context.Context, _, _ string) (time.Time, error) {
	m.regTimeCalls++
	return m.regTime, m.regTimeErr
}

func (m *mockBMIProvider) IsBMIReady(_ context.Context, _, _ string) (bool, error) {
	m.readyCalls++
	if len(m.readyResults) > 0 {
		r := m.readyResults[0]
		m.readyResults = m.readyResults[1:]
		return r.ready, r.err
	}
	return m.isReady, m.readyErr
}

// mockFulfillmentClient implements FulfillmentClient for testing.
type mockFulfillmentClient struct {
	reportCalls   int
	reportErr     error
	lastWorkers   []v1alpha1.WorkerStatus
	lastClusterID string
}

func (m *mockFulfillmentClient) ReportWorkerStatus(_ context.Context, clusterOrderName string, workers []v1alpha1.WorkerStatus) error {
	m.reportCalls++
	m.lastClusterID = clusterOrderName
	m.lastWorkers = workers
	return m.reportErr
}

func newTestReconciler(bmiProvider *mockBMIProvider, fulfillmentClient *mockFulfillmentClient, now time.Time) *BareMetalWorkerReconciler {
	return &BareMetalWorkerReconciler{
		BMIProvider:              bmiProvider,
		FulfillmentClient:        fulfillmentClient,
		AgentRegistrationTimeout: DefaultAgentRegistrationTimeout,
		MaxRetries:               DefaultMaxWorkerRetries,
		now:                      func() time.Time { return now },
	}
}

func newClusterOrderWithWorkers(workers []v1alpha1.WorkerStatus) *v1alpha1.ClusterOrder {
	return &v1alpha1.ClusterOrder{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-order",
			Namespace: "osac-orders",
		},
		Status: v1alpha1.ClusterOrderStatus{
			Phase:   v1alpha1.ClusterOrderPhaseProgressing,
			Workers: workers,
		},
	}
}

var _ = Describe("IsTransientGRPCError", func() {
	It("should return false for nil error", func() {
		Expect(IsTransientGRPCError(nil)).To(BeFalse())
	})

	It("should return false for non-gRPC errors", func() {
		Expect(IsTransientGRPCError(errors.New("some error"))).To(BeFalse())
	})

	It("should return true for Unavailable", func() {
		err := status.Error(codes.Unavailable, "service unavailable")
		Expect(IsTransientGRPCError(err)).To(BeTrue())
	})

	It("should return true for DeadlineExceeded", func() {
		err := status.Error(codes.DeadlineExceeded, "deadline exceeded")
		Expect(IsTransientGRPCError(err)).To(BeTrue())
	})

	It("should return true for ResourceExhausted", func() {
		err := status.Error(codes.ResourceExhausted, "resource exhausted")
		Expect(IsTransientGRPCError(err)).To(BeTrue())
	})

	It("should return true for Aborted", func() {
		err := status.Error(codes.Aborted, "aborted")
		Expect(IsTransientGRPCError(err)).To(BeTrue())
	})

	It("should return false for NotFound", func() {
		err := status.Error(codes.NotFound, "not found")
		Expect(IsTransientGRPCError(err)).To(BeFalse())
	})

	It("should return false for PermissionDenied", func() {
		err := status.Error(codes.PermissionDenied, "permission denied")
		Expect(IsTransientGRPCError(err)).To(BeFalse())
	})

	It("should return false for Internal", func() {
		err := status.Error(codes.Internal, "internal error")
		Expect(IsTransientGRPCError(err)).To(BeFalse())
	})

	It("should return false for InvalidArgument", func() {
		err := status.Error(codes.InvalidArgument, "invalid argument")
		Expect(IsTransientGRPCError(err)).To(BeFalse())
	})
})

// testNetError implements net.Error for testing transient network errors.
type testNetError struct {
	msg     string
	timeout bool
}

func (e *testNetError) Error() string   { return e.msg }
func (e *testNetError) Timeout() bool   { return e.timeout }
func (e *testNetError) Temporary() bool { return e.timeout } //nolint:staticcheck

var _ = Describe("IsNotFoundError", func() {
	It("should return false for nil error", func() {
		Expect(IsNotFoundError(nil)).To(BeFalse())
	})

	It("should return true for k8s API NotFound", func() {
		err := apierrors.NewNotFound(schema.GroupResource{Group: "osac.openshift.io", Resource: "baremetalinstances"}, "test-bmi")
		Expect(IsNotFoundError(err)).To(BeTrue())
	})

	It("should return true for gRPC NotFound", func() {
		err := status.Error(codes.NotFound, "not found")
		Expect(IsNotFoundError(err)).To(BeTrue())
	})

	It("should return false for non-NotFound errors", func() {
		Expect(IsNotFoundError(errors.New("some error"))).To(BeFalse())
	})

	It("should return false for gRPC Unavailable", func() {
		err := status.Error(codes.Unavailable, "service unavailable")
		Expect(IsNotFoundError(err)).To(BeFalse())
	})
})

var _ = Describe("IsTransientError", func() {
	It("should return false for nil error", func() {
		Expect(IsTransientError(nil)).To(BeFalse())
	})

	It("should return true for gRPC Unavailable", func() {
		err := status.Error(codes.Unavailable, "service unavailable")
		Expect(IsTransientError(err)).To(BeTrue())
	})

	It("should return true for gRPC DeadlineExceeded", func() {
		err := status.Error(codes.DeadlineExceeded, "deadline exceeded")
		Expect(IsTransientError(err)).To(BeTrue())
	})

	It("should return true for network timeout errors", func() {
		err := &testNetError{msg: "connection timed out", timeout: true}
		Expect(IsTransientError(err)).To(BeTrue())
	})

	It("should return true for connection refused errors", func() {
		err := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
		Expect(IsTransientError(err)).To(BeTrue())
	})

	It("should return true for k8s API server timeout", func() {
		err := apierrors.NewServerTimeout(schema.GroupResource{Group: "osac.openshift.io", Resource: "baremetalinstances"}, "get", 5)
		Expect(IsTransientError(err)).To(BeTrue())
	})

	It("should return true for k8s API TooManyRequests", func() {
		err := apierrors.NewTooManyRequests("too many requests", 5)
		Expect(IsTransientError(err)).To(BeTrue())
	})

	It("should return false for k8s API NotFound", func() {
		err := apierrors.NewNotFound(schema.GroupResource{Group: "osac.openshift.io", Resource: "baremetalinstances"}, "test-bmi")
		Expect(IsTransientError(err)).To(BeFalse())
	})

	It("should return false for non-transient gRPC errors", func() {
		err := status.Error(codes.PermissionDenied, "permission denied")
		Expect(IsTransientError(err)).To(BeFalse())
	})

	It("should return false for plain errors", func() {
		Expect(IsTransientError(errors.New("some error"))).To(BeFalse())
	})
})

var _ = Describe("ComputeWorkerBackoff", func() {
	It("should return base delay for first attempt", func() {
		Expect(ComputeWorkerBackoff(0)).To(Equal(provisioning.BackoffBaseDelay))
	})

	It("should return base delay for attempt count 1", func() {
		Expect(ComputeWorkerBackoff(1)).To(Equal(provisioning.BackoffBaseDelay))
	})

	It("should double for second attempt", func() {
		Expect(ComputeWorkerBackoff(2)).To(Equal(2 * provisioning.BackoffBaseDelay))
	})

	It("should double again for third attempt", func() {
		Expect(ComputeWorkerBackoff(3)).To(Equal(4 * provisioning.BackoffBaseDelay))
	})

	It("should cap at max delay", func() {
		// With BackoffBaseDelay=2m, BackoffMaxDelay=30m:
		// attempt 4 → 16m, attempt 5 → 32m → capped to 30m
		result := ComputeWorkerBackoff(10)
		Expect(result).To(Equal(provisioning.BackoffMaxDelay))
	})

	It("should produce escalating values up to the cap", func() {
		prev := ComputeWorkerBackoff(1)
		for i := 2; i <= 6; i++ {
			curr := ComputeWorkerBackoff(i)
			Expect(curr).To(BeNumerically(">=", prev), "attempt %d should be >= attempt %d", i, i-1)
			Expect(curr).To(BeNumerically("<=", provisioning.BackoffMaxDelay))
			prev = curr
		}
	})
})

var _ = Describe("BareMetalWorkerReconciler", func() {
	var (
		ctx               context.Context
		bmiProvider       *mockBMIProvider
		fulfillmentClient *mockFulfillmentClient
		now               time.Time
		reconciler        *BareMetalWorkerReconciler
	)

	BeforeEach(func() {
		ctx = context.Background()
		bmiProvider = &mockBMIProvider{}
		fulfillmentClient = &mockFulfillmentClient{}
		now = time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
		reconciler = newTestReconciler(bmiProvider, fulfillmentClient, now)
	})

	Describe("ReconcileWorkers", func() {
		It("should return immediately when no workers exist", func() {
			instance := newClusterOrderWithWorkers(nil)
			result, err := reconciler.ReconcileWorkers(ctx, instance)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())
		})

		It("should return early without panic when BMIProvider is nil", func() {
			reconciler.BMIProvider = nil
			instance := newClusterOrderWithWorkers([]v1alpha1.WorkerStatus{
				{WorkerID: "worker-1", BMIName: "worker-1", BMINamespace: "osac-baremetalinstance"},
			})

			result, err := reconciler.ReconcileWorkers(ctx, instance)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())
			// No provider calls should have been made
			Expect(fulfillmentClient.reportCalls).To(Equal(0))
		})

		It("should skip ready workers", func() {
			bmiProvider.isReady = true
			instance := newClusterOrderWithWorkers([]v1alpha1.WorkerStatus{
				{WorkerID: "worker-1", BMIName: "worker-1", BMINamespace: "osac-baremetalinstance"},
			})

			result, err := reconciler.ReconcileWorkers(ctx, instance)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())
			Expect(bmiProvider.readyCalls).To(Equal(1))
		})

		Context("agent registration timeout", func() {
			BeforeEach(func() {
				bmiProvider.isReady = false
				bmiProvider.regTime = time.Time{} // agent not registered
				bmiProvider.nextCreateName = "replacement-bmi"
			})

			It("should trigger BMI replacement after timeout", func() {
				// Worker was created 31 minutes ago (exceeds 30m timeout)
				failTime := metav1.NewTime(now.Add(-31 * time.Minute))
				instance := newClusterOrderWithWorkers([]v1alpha1.WorkerStatus{
					{
						WorkerID:        "worker-1",
						BMIName:         "worker-1",
						BMINamespace:    "osac-baremetalinstance",
						AttemptCount:    0,
						LastFailureTime: &failTime,
					},
				})

				result, err := reconciler.ReconcileWorkers(ctx, instance)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.RequeueAfter).To(BeNumerically(">", 0))
				Expect(bmiProvider.deleteCalls).To(Equal(1))
				Expect(bmiProvider.createCalls).To(Equal(1))
				Expect(instance.Status.Workers[0].AttemptCount).To(Equal(1))
				Expect(instance.Status.Workers[0].LastFailureReason).To(Equal(v1alpha1.ReasonAgentRegistrationTimeout))

				// Verify WorkerProvisioningFailed condition is set
				cond := apimeta.FindStatusCondition(instance.Status.Conditions, v1alpha1.ConditionWorkerProvisioningFailed)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionTrue))
				Expect(cond.Reason).To(Equal(v1alpha1.ReasonBMIReplacementTriggered))
			})

			It("should wait for agent registration within timeout", func() {
				// Worker was created 15 minutes ago (within 30m timeout)
				failTime := metav1.NewTime(now.Add(-15 * time.Minute))
				instance := newClusterOrderWithWorkers([]v1alpha1.WorkerStatus{
					{
						WorkerID:        "worker-1",
						BMIName:         "worker-1",
						BMINamespace:    "osac-baremetalinstance",
						AttemptCount:    0,
						LastFailureTime: &failTime,
					},
				})

				result, err := reconciler.ReconcileWorkers(ctx, instance)
				Expect(err).NotTo(HaveOccurred())
				// Should requeue to check again near the timeout
				Expect(result.RequeueAfter).To(BeNumerically(">", 0))
				Expect(result.RequeueAfter).To(BeNumerically("<=", 15*time.Minute))
				Expect(bmiProvider.deleteCalls).To(Equal(0))
				Expect(bmiProvider.createCalls).To(Equal(0))
			})
		})

		Context("escalating backoff", func() {
			BeforeEach(func() {
				bmiProvider.isReady = false
				bmiProvider.regTime = time.Time{} // agent not registered
				bmiProvider.nextCreateName = "replacement-bmi"
			})

			It("should respect backoff window", func() {
				// Set NextRetryTime 5 minutes in the future
				nextRetry := metav1.NewTime(now.Add(5 * time.Minute))
				instance := newClusterOrderWithWorkers([]v1alpha1.WorkerStatus{
					{
						WorkerID:      "worker-1",
						BMIName:       "worker-1",
						BMINamespace:  "osac-baremetalinstance",
						AttemptCount:  2,
						NextRetryTime: &nextRetry,
					},
				})

				result, err := reconciler.ReconcileWorkers(ctx, instance)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.RequeueAfter).To(BeNumerically("~", 5*time.Minute, time.Second))
				Expect(bmiProvider.deleteCalls).To(Equal(0))
				Expect(bmiProvider.createCalls).To(Equal(0))
			})

			It("should proceed when backoff window has elapsed", func() {
				// Set NextRetryTime far enough in the past that the agent registration
				// timeout (30min) has also elapsed since the effective creation time.
				nextRetry := metav1.NewTime(now.Add(-31 * time.Minute))
				failTime := metav1.NewTime(now.Add(-35 * time.Minute))
				instance := newClusterOrderWithWorkers([]v1alpha1.WorkerStatus{
					{
						WorkerID:        "worker-1",
						BMIName:         "worker-1",
						BMINamespace:    "osac-baremetalinstance",
						AttemptCount:    1,
						NextRetryTime:   &nextRetry,
						LastFailureTime: &failTime,
					},
				})

				result, err := reconciler.ReconcileWorkers(ctx, instance)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.RequeueAfter).To(BeNumerically(">", 0))
				Expect(bmiProvider.deleteCalls).To(Equal(1))
				Expect(bmiProvider.createCalls).To(Equal(1))
				Expect(instance.Status.Workers[0].AttemptCount).To(Equal(2))
			})

			It("should set increasing backoff delays", func() {
				failTime := metav1.NewTime(now.Add(-35 * time.Minute))

				for attempt := 1; attempt < 4; attempt++ {
					bmiProvider = &mockBMIProvider{
						isReady:        false,
						nextCreateName: fmt.Sprintf("replacement-bmi-%d", attempt),
					}
					reconciler.BMIProvider = bmiProvider

					instance := newClusterOrderWithWorkers([]v1alpha1.WorkerStatus{
						{
							WorkerID:        fmt.Sprintf("worker-%d", attempt),
							BMIName:         fmt.Sprintf("worker-%d", attempt),
							BMINamespace:    "osac-baremetalinstance",
							AttemptCount:    attempt,
							LastFailureTime: &failTime,
						},
					})

					result, err := reconciler.ReconcileWorkers(ctx, instance)
					Expect(err).NotTo(HaveOccurred())
					Expect(result.RequeueAfter).To(Equal(ComputeWorkerBackoff(attempt + 1)))
				}
			})
		})

		Context("transient gRPC errors", func() {
			It("should set FulfillmentServiceUnavailable on transient error during readiness check", func() {
				bmiProvider.readyErr = status.Error(codes.Unavailable, "service unavailable")
				instance := newClusterOrderWithWorkers([]v1alpha1.WorkerStatus{
					{WorkerID: "worker-1", BMIName: "worker-1", BMINamespace: "osac-baremetalinstance"},
				})

				result, err := reconciler.ReconcileWorkers(ctx, instance)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.RequeueAfter).To(Equal(provisioning.BackoffBaseDelay))

				cond := apimeta.FindStatusCondition(instance.Status.Conditions, v1alpha1.ConditionFulfillmentServiceUnavailable)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionTrue))
				Expect(cond.Reason).To(Equal(v1alpha1.ReasonGRPCUnavailable))
			})

			It("should set FulfillmentServiceUnavailable on transient error during registration check", func() {
				bmiProvider.isReady = false
				bmiProvider.regTimeErr = status.Error(codes.DeadlineExceeded, "deadline exceeded")
				instance := newClusterOrderWithWorkers([]v1alpha1.WorkerStatus{
					{WorkerID: "worker-1", BMIName: "worker-1", BMINamespace: "osac-baremetalinstance"},
				})

				result, err := reconciler.ReconcileWorkers(ctx, instance)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.RequeueAfter).To(Equal(provisioning.BackoffBaseDelay))

				cond := apimeta.FindStatusCondition(instance.Status.Conditions, v1alpha1.ConditionFulfillmentServiceUnavailable)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			})

			It("should not count transient gRPC errors toward retry budget", func() {
				bmiProvider.readyErr = status.Error(codes.Unavailable, "service unavailable")
				instance := newClusterOrderWithWorkers([]v1alpha1.WorkerStatus{
					{WorkerID: "worker-1", BMIName: "worker-1", BMINamespace: "osac-baremetalinstance", AttemptCount: 3},
				})

				_, err := reconciler.ReconcileWorkers(ctx, instance)
				Expect(err).NotTo(HaveOccurred())
				// Attempt count should not have changed
				Expect(instance.Status.Workers[0].AttemptCount).To(Equal(3))
			})

			It("should return error for non-transient gRPC errors during readiness check", func() {
				bmiProvider.readyErr = status.Error(codes.Internal, "internal error")
				instance := newClusterOrderWithWorkers([]v1alpha1.WorkerStatus{
					{WorkerID: "worker-1", BMIName: "worker-1", BMINamespace: "osac-baremetalinstance"},
				})

				_, err := reconciler.ReconcileWorkers(ctx, instance)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("checking BMI readiness"))
			})

			It("should set FulfillmentServiceUnavailable on transient error during fulfillment report", func() {
				bmiProvider.isReady = true
				fulfillmentClient.reportErr = status.Error(codes.ResourceExhausted, "resource exhausted")
				instance := newClusterOrderWithWorkers([]v1alpha1.WorkerStatus{
					{WorkerID: "worker-1", BMIName: "worker-1", BMINamespace: "osac-baremetalinstance"},
				})

				result, err := reconciler.ReconcileWorkers(ctx, instance)
				Expect(err).NotTo(HaveOccurred())

				cond := apimeta.FindStatusCondition(instance.Status.Conditions, v1alpha1.ConditionFulfillmentServiceUnavailable)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionTrue))

				// Fix 1: Even when all workers are ready (nextRequeue would
				// be zero), a transient report failure must schedule a retry.
				Expect(result.RequeueAfter).To(Equal(provisioning.BackoffBaseDelay))
			})

			It("should set FulfillmentServiceUnavailable to False when all workers are ready and no transient errors", func() {
				// Fix 6: The condition should be set to False (not removed)
				// so patchStatusWithRetry persists the cleared state.
				bmiProvider.isReady = true
				instance := newClusterOrderWithWorkers([]v1alpha1.WorkerStatus{
					{WorkerID: "worker-1", BMIName: "worker-1", BMINamespace: "osac-baremetalinstance"},
				})
				// Pre-set the condition to True
				instance.SetStatusCondition(
					v1alpha1.ConditionFulfillmentServiceUnavailable,
					metav1.ConditionTrue,
					"previous error",
					v1alpha1.ReasonGRPCUnavailable,
				)

				_, err := reconciler.ReconcileWorkers(ctx, instance)
				Expect(err).NotTo(HaveOccurred())

				cond := apimeta.FindStatusCondition(instance.Status.Conditions, v1alpha1.ConditionFulfillmentServiceUnavailable)
				Expect(cond).NotTo(BeNil(), "condition should be set to False, not removed")
				Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				Expect(cond.Reason).To(Equal(v1alpha1.ReasonNoTransientErrors))
			})

			It("should NOT clear FulfillmentServiceUnavailable when one worker has transient error and another is ready", func() {
				// Fix 3: worker-0 has a transient error, worker-1 is ready.
				// The condition must NOT be cleared because worker-0's error
				// is still unresolved.
				bmiProvider.readyResults = []readyResult{
					{ready: false, err: status.Error(codes.Unavailable, "service unavailable")},
					{ready: true, err: nil},
				}
				instance := newClusterOrderWithWorkers([]v1alpha1.WorkerStatus{
					{WorkerID: "worker-0", BMIName: "worker-0", BMINamespace: "osac-baremetalinstance"},
					{WorkerID: "worker-1", BMIName: "worker-1", BMINamespace: "osac-baremetalinstance"},
				})

				_, err := reconciler.ReconcileWorkers(ctx, instance)
				Expect(err).NotTo(HaveOccurred())

				// The condition must still be set because worker-0 had a transient error
				cond := apimeta.FindStatusCondition(instance.Status.Conditions, v1alpha1.ConditionFulfillmentServiceUnavailable)
				Expect(cond).NotTo(BeNil(), "FulfillmentServiceUnavailable should not be cleared when any worker had a transient error")
				Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			})

			It("should NOT clear FulfillmentServiceUnavailable when fulfillment report has transient error", func() {
				// Fix 3: All workers are ready but the fulfillment report fails
				// transiently — condition must remain set.
				bmiProvider.isReady = true
				fulfillmentClient.reportErr = status.Error(codes.Unavailable, "service unavailable")
				instance := newClusterOrderWithWorkers([]v1alpha1.WorkerStatus{
					{WorkerID: "worker-1", BMIName: "worker-1", BMINamespace: "osac-baremetalinstance"},
				})
				// Pre-set the condition to verify it is NOT cleared
				instance.SetStatusCondition(
					v1alpha1.ConditionFulfillmentServiceUnavailable,
					metav1.ConditionTrue,
					"previous error",
					v1alpha1.ReasonGRPCUnavailable,
				)

				_, err := reconciler.ReconcileWorkers(ctx, instance)
				Expect(err).NotTo(HaveOccurred())

				cond := apimeta.FindStatusCondition(instance.Status.Conditions, v1alpha1.ConditionFulfillmentServiceUnavailable)
				Expect(cond).NotTo(BeNil(), "FulfillmentServiceUnavailable should not be cleared on transient report error")
			})
		})

		Context("terminal condition after max retries", func() {
			It("should set WorkersFailed when all workers exhaust retries", func() {
				reconciler.MaxRetries = 3
				instance := newClusterOrderWithWorkers([]v1alpha1.WorkerStatus{
					{WorkerID: "worker-1", BMIName: "worker-1", BMINamespace: "osac-baremetalinstance", AttemptCount: 3},
					{WorkerID: "worker-2", BMIName: "worker-2", BMINamespace: "osac-baremetalinstance", AttemptCount: 3},
				})

				// Workers at max retries but readiness check won't be reached (short-circuits)
				bmiProvider.isReady = false

				result, err := reconciler.ReconcileWorkers(ctx, instance)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.RequeueAfter).To(BeZero())

				cond := apimeta.FindStatusCondition(instance.Status.Conditions, v1alpha1.ConditionWorkersFailed)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionTrue))
				Expect(cond.Reason).To(Equal(v1alpha1.ReasonMaxRetriesExhausted))
				Expect(instance.Status.Phase).To(Equal(v1alpha1.ClusterOrderPhaseFailed))
			})

			It("should not set WorkersFailed if some workers are still being retried", func() {
				reconciler.MaxRetries = 3
				bmiProvider.isReady = false
				bmiProvider.regTime = now.Add(-5 * time.Minute) // agent registered recently

				nextRetry := metav1.NewTime(now.Add(5 * time.Minute))
				instance := newClusterOrderWithWorkers([]v1alpha1.WorkerStatus{
					{WorkerID: "worker-1", BMIName: "worker-1", BMINamespace: "osac-baremetalinstance", AttemptCount: 3},
					{WorkerID: "worker-2", BMIName: "worker-2", BMINamespace: "osac-baremetalinstance", AttemptCount: 1, NextRetryTime: &nextRetry},
				})

				result, err := reconciler.ReconcileWorkers(ctx, instance)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.RequeueAfter).To(BeNumerically(">", 0))

				cond := apimeta.FindStatusCondition(instance.Status.Conditions, v1alpha1.ConditionWorkersFailed)
				Expect(cond).To(BeNil())
			})

			It("should set WorkerProvisioningFailed on final replacement attempt without deleting BMI", func() {
				// Fix 4: On the last attempt, the old BMI should NOT be
				// deleted — leave it in place for debugging. The terminal
				// condition is set, and BMIName still references the
				// existing (running) BMI.
				reconciler.MaxRetries = 2
				bmiProvider.isReady = false
				bmiProvider.regTime = time.Time{}
				bmiProvider.nextCreateName = "replacement"

				failTime := metav1.NewTime(now.Add(-35 * time.Minute))
				instance := newClusterOrderWithWorkers([]v1alpha1.WorkerStatus{
					{
						WorkerID:        "worker-1",
						BMIName:         "worker-1",
						BMINamespace:    "osac-baremetalinstance",
						AttemptCount:    1,
						LastFailureTime: &failTime,
					},
				})

				_, err := reconciler.ReconcileWorkers(ctx, instance)
				Expect(err).NotTo(HaveOccurred())

				// Should have incremented to 2 (== MaxRetries) and set failure condition
				Expect(instance.Status.Workers[0].AttemptCount).To(Equal(2))

				cond := apimeta.FindStatusCondition(instance.Status.Conditions, v1alpha1.ConditionWorkerProvisioningFailed)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionTrue))

				// Fix 4: BMI should NOT have been deleted on the last attempt
				Expect(bmiProvider.deleteCalls).To(Equal(0),
					"BMI should not be deleted on the last attempt — leave it for debugging")
				Expect(bmiProvider.createCalls).To(Equal(0),
					"no replacement should be created when max retries are exhausted")
				// BMIName should still reference the original BMI
				Expect(instance.Status.Workers[0].BMIName).To(Equal("worker-1"),
					"BMIName should still point to the existing BMI for debugging")
			})
		})

		Context("BMI replacement flow", func() {
			BeforeEach(func() {
				bmiProvider.isReady = false
				bmiProvider.regTime = time.Time{}
				bmiProvider.nextCreateName = "new-bmi"
			})

			It("should delete old BMI and create replacement", func() {
				failTime := metav1.NewTime(now.Add(-35 * time.Minute))
				instance := newClusterOrderWithWorkers([]v1alpha1.WorkerStatus{
					{
						WorkerID:        "worker-0",
						BMIName:         "old-bmi",
						BMINamespace:    "osac-baremetalinstance",
						AttemptCount:    0,
						LastFailureTime: &failTime,
					},
				})

				_, err := reconciler.ReconcileWorkers(ctx, instance)
				Expect(err).NotTo(HaveOccurred())

				Expect(bmiProvider.deletedNames).To(ContainElement("old-bmi"))
				Expect(bmiProvider.createdNames).To(ContainElement("new-bmi"))
				Expect(instance.Status.Workers[0].BMIName).To(Equal("new-bmi"))
				Expect(instance.Status.Workers[0].BMINamespace).To(Equal("osac-baremetalinstance"))
			})

			It("should update worker status after replacement", func() {
				failTime := metav1.NewTime(now.Add(-35 * time.Minute))
				instance := newClusterOrderWithWorkers([]v1alpha1.WorkerStatus{
					{
						WorkerID:        "worker-0",
						BMIName:         "old-bmi",
						BMINamespace:    "osac-baremetalinstance",
						AttemptCount:    0,
						LastFailureTime: &failTime,
					},
				})

				_, err := reconciler.ReconcileWorkers(ctx, instance)
				Expect(err).NotTo(HaveOccurred())

				worker := instance.Status.Workers[0]
				Expect(worker.AttemptCount).To(Equal(1))
				Expect(worker.LastFailureReason).To(Equal(v1alpha1.ReasonAgentRegistrationTimeout))
				Expect(worker.LastFailureTime).NotTo(BeNil())
				Expect(worker.NextRetryTime).NotTo(BeNil())
				Expect(worker.NextRetryTime.Time).To(BeTemporally(">", now))
			})

			It("should handle transient gRPC error during BMI deletion", func() {
				bmiProvider.deleteErr = status.Error(codes.Unavailable, "service unavailable")

				failTime := metav1.NewTime(now.Add(-35 * time.Minute))
				instance := newClusterOrderWithWorkers([]v1alpha1.WorkerStatus{
					{
						WorkerID:        "worker-1",
						BMIName:         "worker-1",
						BMINamespace:    "osac-baremetalinstance",
						AttemptCount:    0,
						LastFailureTime: &failTime,
					},
				})

				result, err := reconciler.ReconcileWorkers(ctx, instance)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.RequeueAfter).To(Equal(provisioning.BackoffBaseDelay))

				cond := apimeta.FindStatusCondition(instance.Status.Conditions, v1alpha1.ConditionFulfillmentServiceUnavailable)
				Expect(cond).NotTo(BeNil())
				// Attempt count should not have changed
				Expect(instance.Status.Workers[0].AttemptCount).To(Equal(0))
			})

			It("should handle NotFound from DeleteBMI as success", func() {
				// Fix 2: If the BMI is already gone (NotFound), treat it as a
				// successful deletion and proceed with replacement.
				bmiProvider.deleteErr = apierrors.NewNotFound(
					schema.GroupResource{Group: "osac.openshift.io", Resource: "baremetalinstances"}, "worker-1")
				bmiProvider.nextCreateName = "replacement-bmi"

				failTime := metav1.NewTime(now.Add(-35 * time.Minute))
				instance := newClusterOrderWithWorkers([]v1alpha1.WorkerStatus{
					{
						WorkerID:        "worker-1",
						BMIName:         "worker-1",
						BMINamespace:    "osac-baremetalinstance",
						AttemptCount:    0,
						LastFailureTime: &failTime,
					},
				})

				result, err := reconciler.ReconcileWorkers(ctx, instance)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.RequeueAfter).To(BeNumerically(">", 0))
				// Should have proceeded to create a replacement
				Expect(bmiProvider.createCalls).To(Equal(1))
				Expect(instance.Status.Workers[0].BMIName).To(Equal("replacement-bmi"))
				Expect(instance.Status.Workers[0].AttemptCount).To(Equal(1))
			})

			It("should clear BMIName after successful DeleteBMI before CreateBMI", func() {
				// Fix 2: When CreateBMI fails after DeleteBMI succeeds,
				// BMIName should be empty (cleared) so the next reconcile
				// does not reference a deleted BMI.
				bmiProvider.createErr = status.Error(codes.Aborted, "aborted")

				failTime := metav1.NewTime(now.Add(-35 * time.Minute))
				instance := newClusterOrderWithWorkers([]v1alpha1.WorkerStatus{
					{
						WorkerID:        "worker-1",
						BMIName:         "worker-1",
						BMINamespace:    "osac-baremetalinstance",
						AttemptCount:    0,
						LastFailureTime: &failTime,
					},
				})

				_, err := reconciler.ReconcileWorkers(ctx, instance)
				Expect(err).NotTo(HaveOccurred())
				// BMIName should have been cleared after DeleteBMI succeeded
				Expect(instance.Status.Workers[0].BMIName).To(BeEmpty(),
					"BMIName must be cleared after successful DeleteBMI to avoid referencing a ghost")
			})

			It("should handle transient gRPC error during BMI creation without stale retry state", func() {
				bmiProvider.createErr = status.Error(codes.Aborted, "aborted")

				failTime := metav1.NewTime(now.Add(-35 * time.Minute))
				instance := newClusterOrderWithWorkers([]v1alpha1.WorkerStatus{
					{
						WorkerID:        "worker-1",
						BMIName:         "worker-1",
						BMINamespace:    "osac-baremetalinstance",
						AttemptCount:    0,
						LastFailureTime: &failTime,
					},
				})

				result, err := reconciler.ReconcileWorkers(ctx, instance)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.RequeueAfter).To(Equal(provisioning.BackoffBaseDelay))

				cond := apimeta.FindStatusCondition(instance.Status.Conditions, v1alpha1.ConditionFulfillmentServiceUnavailable)
				Expect(cond).NotTo(BeNil())

				// Fix 2: When CreateBMI fails transiently after DeleteBMI
				// succeeds, retry state must NOT be persisted. AttemptCount,
				// NextRetryTime, and LastFailureReason must remain unchanged.
				worker := instance.Status.Workers[0]
				Expect(worker.AttemptCount).To(Equal(0), "AttemptCount should not be incremented on transient CreateBMI failure")
				Expect(worker.NextRetryTime).To(BeNil(), "NextRetryTime should not be set on transient CreateBMI failure")
				Expect(worker.LastFailureReason).To(BeEmpty(), "LastFailureReason should not be set on transient CreateBMI failure")
			})
		})

		Context("stale retry state prevention", func() {
			It("should not mutate retry state when CreateBMI fails transiently with prior attempts", func() {
				// Fix 2: Simulate a worker at attempt 2 whose old BMI timed out.
				// DeleteBMI succeeds but CreateBMI fails transiently. The worker
				// status must still show attemptCount=2 (not 3), and the original
				// NextRetryTime and LastFailureReason must be preserved.
				bmiProvider.isReady = false
				bmiProvider.regTime = time.Time{}
				bmiProvider.createErr = status.Error(codes.Unavailable, "service unavailable")

				oldRetry := metav1.NewTime(now.Add(-35 * time.Minute))
				oldFailTime := metav1.NewTime(now.Add(-36 * time.Minute))
				instance := newClusterOrderWithWorkers([]v1alpha1.WorkerStatus{
					{
						WorkerID:          "worker-1",
						BMIName:           "worker-1",
						BMINamespace:      "osac-baremetalinstance",
						AttemptCount:      2,
						NextRetryTime:     &oldRetry,
						LastFailureTime:   &oldFailTime,
						LastFailureReason: "PreviousReason",
					},
				})

				result, err := reconciler.ReconcileWorkers(ctx, instance)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.RequeueAfter).To(Equal(provisioning.BackoffBaseDelay))

				worker := instance.Status.Workers[0]
				Expect(worker.AttemptCount).To(Equal(2), "AttemptCount must not change on transient CreateBMI failure")
				Expect(worker.LastFailureReason).To(Equal("PreviousReason"), "LastFailureReason must not change")
				Expect(worker.NextRetryTime.Time).To(Equal(oldRetry.Time), "NextRetryTime must not change")
			})

			It("should correctly update retry state when full replacement succeeds", func() {
				// Positive case: Both delete and create succeed, so retry state
				// must be updated to reflect the successful replacement.
				bmiProvider.isReady = false
				bmiProvider.regTime = time.Time{}
				bmiProvider.nextCreateName = "new-bmi"

				oldRetry := metav1.NewTime(now.Add(-35 * time.Minute))
				oldFailTime := metav1.NewTime(now.Add(-36 * time.Minute))
				instance := newClusterOrderWithWorkers([]v1alpha1.WorkerStatus{
					{
						WorkerID:          "worker-1",
						BMIName:           "worker-1",
						BMINamespace:      "osac-baremetalinstance",
						AttemptCount:      1,
						NextRetryTime:     &oldRetry,
						LastFailureTime:   &oldFailTime,
						LastFailureReason: "PreviousReason",
					},
				})

				_, err := reconciler.ReconcileWorkers(ctx, instance)
				Expect(err).NotTo(HaveOccurred())

				worker := instance.Status.Workers[0]
				Expect(worker.AttemptCount).To(Equal(2), "AttemptCount must increment on successful replacement")
				Expect(worker.BMIName).To(Equal("new-bmi"))
				Expect(worker.LastFailureReason).To(Equal(v1alpha1.ReasonAgentRegistrationTimeout))
				Expect(worker.NextRetryTime).NotTo(BeNil())
				Expect(worker.NextRetryTime.Time).To(BeTemporally(">", now))
			})
		})

		Context("empty BMIName recovery", func() {
			It("should create BMI directly when BMIName is empty and AttemptCount > 0", func() {
				// When DeleteBMI succeeds but CreateBMI fails transiently,
				// the worker ends up with BMIName="" and AttemptCount > 0.
				// On the next reconcile, the controller must skip delete and
				// go directly to creation.
				bmiProvider.nextCreateName = "recovered-bmi"
				instance := newClusterOrderWithWorkers([]v1alpha1.WorkerStatus{
					{
						WorkerID:     "worker-1",
						BMIName:      "", // empty after failed create
						BMINamespace: "osac-baremetalinstance",
						AttemptCount: 2,
					},
				})

				result, err := reconciler.ReconcileWorkers(ctx, instance)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.RequeueAfter).To(BeNumerically(">", 0))

				// Must NOT have attempted to delete (nothing to delete)
				Expect(bmiProvider.deleteCalls).To(Equal(0),
					"should not call DeleteBMI when BMIName is already empty")
				// Must have created a new BMI
				Expect(bmiProvider.createCalls).To(Equal(1))
				Expect(instance.Status.Workers[0].BMIName).To(Equal("recovered-bmi"),
					"BMIName must be updated after successful creation")
				Expect(instance.Status.Workers[0].BMINamespace).To(Equal("osac-baremetalinstance"))
				// AttemptCount should NOT change — the attempt was already
				// counted when the original replaceBMI incremented it.
				Expect(instance.Status.Workers[0].AttemptCount).To(Equal(2),
					"AttemptCount must not change on recovery creation")
			})

			It("should handle transient error during recovery creation", func() {
				bmiProvider.createErr = status.Error(codes.Unavailable, "service unavailable")
				instance := newClusterOrderWithWorkers([]v1alpha1.WorkerStatus{
					{
						WorkerID:     "worker-1",
						BMIName:      "",
						BMINamespace: "osac-baremetalinstance",
						AttemptCount: 1,
					},
				})

				result, err := reconciler.ReconcileWorkers(ctx, instance)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.RequeueAfter).To(Equal(provisioning.BackoffBaseDelay))

				cond := apimeta.FindStatusCondition(instance.Status.Conditions, v1alpha1.ConditionFulfillmentServiceUnavailable)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionTrue))
				// BMIName should still be empty after transient failure
				Expect(instance.Status.Workers[0].BMIName).To(BeEmpty())
			})

			It("should not trigger recovery path when BMIName is empty and AttemptCount is 0", func() {
				// A brand-new worker with empty BMIName and AttemptCount=0 is
				// not a recovery case — it should follow the normal path.
				bmiProvider.isReady = false
				bmiProvider.regTime = time.Time{}
				instance := newClusterOrderWithWorkers([]v1alpha1.WorkerStatus{
					{
						WorkerID:     "worker-1",
						BMIName:      "",
						BMINamespace: "osac-baremetalinstance",
						AttemptCount: 0,
					},
				})

				_, err := reconciler.ReconcileWorkers(ctx, instance)
				Expect(err).NotTo(HaveOccurred())
				// Should NOT have created a BMI via the recovery path
				Expect(bmiProvider.createCalls).To(Equal(0))
			})
		})

		Context("fulfillment client reporting", func() {
			It("should report worker status to fulfillment service", func() {
				bmiProvider.isReady = true
				instance := newClusterOrderWithWorkers([]v1alpha1.WorkerStatus{
					{WorkerID: "worker-1", BMIName: "worker-1", BMINamespace: "osac-baremetalinstance"},
				})

				_, err := reconciler.ReconcileWorkers(ctx, instance)
				Expect(err).NotTo(HaveOccurred())
				Expect(fulfillmentClient.reportCalls).To(Equal(1))
				Expect(fulfillmentClient.lastClusterID).To(Equal("test-order"))
			})

			It("should handle nil fulfillment client gracefully", func() {
				reconciler.FulfillmentClient = nil
				bmiProvider.isReady = true
				instance := newClusterOrderWithWorkers([]v1alpha1.WorkerStatus{
					{WorkerID: "worker-1", BMIName: "worker-1", BMINamespace: "osac-baremetalinstance"},
				})

				_, err := reconciler.ReconcileWorkers(ctx, instance)
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Context("booting workers requeue", func() {
			It("should requeue for registered but not ready workers", func() {
				bmiProvider.isReady = false
				bmiProvider.regTime = now.Add(-5 * time.Minute) // agent registered 5 minutes ago
				instance := newClusterOrderWithWorkers([]v1alpha1.WorkerStatus{
					{WorkerID: "worker-1", BMIName: "worker-1", BMINamespace: "osac-baremetalinstance"},
				})

				result, err := reconciler.ReconcileWorkers(ctx, instance)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.RequeueAfter).To(Equal(1 * time.Minute))
				Expect(bmiProvider.deleteCalls).To(Equal(0))
				Expect(bmiProvider.createCalls).To(Equal(0))
			})
		})

		Context("initial workers without timestamps", func() {
			It("should record AttemptStartTime and requeue when no timestamp is available", func() {
				bmiProvider.isReady = false
				bmiProvider.regTime = time.Time{} // agent not registered
				instance := newClusterOrderWithWorkers([]v1alpha1.WorkerStatus{
					{
						WorkerID:     "worker-1",
						BMIName:      "worker-1",
						BMINamespace: "osac-baremetalinstance",
						AttemptCount: 0,
						// No LastFailureTime, NextRetryTime, or AttemptStartTime set
					},
				})

				result, err := reconciler.ReconcileWorkers(ctx, instance)
				Expect(err).NotTo(HaveOccurred())
				// Should requeue with booting interval since no timestamp was available
				Expect(result.RequeueAfter).To(Equal(1 * time.Minute))
				// Should NOT trigger BMI replacement
				Expect(bmiProvider.deleteCalls).To(Equal(0))
				Expect(bmiProvider.createCalls).To(Equal(0))
				// Should record AttemptStartTime for subsequent timeout evaluation
				Expect(instance.Status.Workers[0].AttemptStartTime).NotTo(BeNil(),
					"AttemptStartTime must be set on first observation")
				Expect(instance.Status.Workers[0].AttemptStartTime.Time).To(
					BeTemporally("==", now),
					"AttemptStartTime should equal the current reconciliation time")
			})

			It("should use AttemptStartTime for timeout on subsequent reconciliation", func() {
				bmiProvider.isReady = false
				bmiProvider.regTime = time.Time{} // agent not registered

				// Simulate a worker that had AttemptStartTime set 15 minutes ago
				// (within the 30-minute timeout).
				attemptStart := metav1.NewTime(now.Add(-15 * time.Minute))
				instance := newClusterOrderWithWorkers([]v1alpha1.WorkerStatus{
					{
						WorkerID:         "worker-1",
						BMIName:          "worker-1",
						BMINamespace:     "osac-baremetalinstance",
						AttemptCount:     0,
						AttemptStartTime: &attemptStart,
					},
				})

				result, err := reconciler.ReconcileWorkers(ctx, instance)
				Expect(err).NotTo(HaveOccurred())
				// Should requeue to check again near the timeout
				Expect(result.RequeueAfter).To(BeNumerically(">", 0))
				Expect(result.RequeueAfter).To(BeNumerically("<=", 15*time.Minute))
				// Should NOT trigger BMI replacement yet
				Expect(bmiProvider.deleteCalls).To(Equal(0))
				Expect(bmiProvider.createCalls).To(Equal(0))
			})

			It("should trigger timeout after 30 minutes from AttemptStartTime", func() {
				bmiProvider.isReady = false
				bmiProvider.regTime = time.Time{} // agent not registered
				bmiProvider.nextCreateName = "replacement-bmi"

				// Simulate a worker with AttemptStartTime set 31 minutes ago
				// (exceeds 30-minute timeout).
				attemptStart := metav1.NewTime(now.Add(-31 * time.Minute))
				instance := newClusterOrderWithWorkers([]v1alpha1.WorkerStatus{
					{
						WorkerID:         "worker-1",
						BMIName:          "worker-1",
						BMINamespace:     "osac-baremetalinstance",
						AttemptCount:     0,
						AttemptStartTime: &attemptStart,
					},
				})

				result, err := reconciler.ReconcileWorkers(ctx, instance)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.RequeueAfter).To(BeNumerically(">", 0))
				// Should trigger BMI replacement
				Expect(bmiProvider.deleteCalls).To(Equal(1))
				Expect(bmiProvider.createCalls).To(Equal(1))
				Expect(instance.Status.Workers[0].AttemptCount).To(Equal(1))
				Expect(instance.Status.Workers[0].LastFailureReason).To(
					Equal(v1alpha1.ReasonAgentRegistrationTimeout))
				// AttemptStartTime should be cleared after replacement
				// (NextRetryTime is the new timeout anchor)
				Expect(instance.Status.Workers[0].AttemptStartTime).To(BeNil(),
					"AttemptStartTime should be cleared after BMI replacement")
				Expect(instance.Status.Workers[0].NextRetryTime).NotTo(BeNil(),
					"NextRetryTime should be set after replacement")
			})
		})

		Context("BMI replacement condition messages", func() {
			It("should show old BMI name in replacement condition message", func() {
				bmiProvider.isReady = false
				bmiProvider.regTime = time.Time{}
				bmiProvider.nextCreateName = "new-bmi"

				failTime := metav1.NewTime(now.Add(-35 * time.Minute))
				instance := newClusterOrderWithWorkers([]v1alpha1.WorkerStatus{
					{
						WorkerID:        "worker-0",
						BMIName:         "old-bmi",
						BMINamespace:    "osac-baremetalinstance",
						AttemptCount:    0,
						LastFailureTime: &failTime,
					},
				})

				_, err := reconciler.ReconcileWorkers(ctx, instance)
				Expect(err).NotTo(HaveOccurred())

				cond := apimeta.FindStatusCondition(instance.Status.Conditions, v1alpha1.ConditionWorkerProvisioningFailed)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Message).To(ContainSubstring("old-bmi"))
				Expect(cond.Message).To(ContainSubstring("new-bmi"))
			})
		})
	})
})
