// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package baremetalworker

import (
	"context"
	"errors"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

// A definitive rejection is a terminal Create response: the server refused the
// request and rolled back its transaction, so no BMI can exist for the attempt.
// Transport-class and unknown outcomes stay conservative.
var definitiveCreateCodes = []codes.Code{
	codes.InvalidArgument,
	codes.NotFound,
	codes.FailedPrecondition,
	codes.PermissionDenied,
	codes.ResourceExhausted,
	codes.OutOfRange,
	codes.Unimplemented,
}

var conservativeCreateCodes = []codes.Code{
	codes.Unavailable,
	codes.DeadlineExceeded,
	codes.Canceled,
	codes.Unauthenticated,
	codes.Unknown,
	codes.Internal,
	codes.DataLoss,
}

var _ = Describe("Definitive BMI create rejection classification", func() {
	for _, code := range definitiveCreateCodes {
		It(fmt.Sprintf("classifies terminal %s as definitive", code), func() {
			Expect(isDefinitiveCreateRejection(status.Error(code, "rejected"))).To(BeTrue())
		})
		It(fmt.Sprintf("classifies wrapped terminal %s as definitive", code), func() {
			wrapped := fmt.Errorf("creating BMI slot: %w", status.Error(code, "rejected"))
			Expect(isDefinitiveCreateRejection(wrapped)).To(BeTrue())
		})
	}
	for _, code := range conservativeCreateCodes {
		It(fmt.Sprintf("keeps %s conservative", code), func() {
			Expect(isDefinitiveCreateRejection(status.Error(code, "failure"))).To(BeFalse())
		})
	}
	It("keeps non-status errors conservative", func() {
		Expect(isDefinitiveCreateRejection(errors.New("interrupted create"))).To(BeFalse())
	})
	It("keeps availability-wrapped transport failures conservative", func() {
		wrapped := fmt.Errorf("%w: %w", ErrFulfillmentServiceUnavailable, status.Error(codes.Unavailable, "outage"))
		Expect(isDefinitiveCreateRejection(wrapped)).To(BeFalse())
	})
	It("treats a nil error as no rejection", func() {
		Expect(isDefinitiveCreateRejection(nil)).To(BeFalse())
	})
})

var _ = Describe("ensureBMI marks definitive Create rejections", func() {
	rejectedCreate := func(code codes.Code, message string) (*nodeSetClient, error) {
		r, fc, co := nodeSetHarness("rejection-mark", nodeRequest("standard", 1))
		fc.createErr = status.Error(code, message)
		cluster := authoritativeTestCluster(context.Background(), r, co)
		_, _, err := r.ensureBMI(context.Background(), co, cluster, co.Spec.NodeRequests[0], "slot", nil, "", "", "")
		return fc, err
	}
	for _, code := range definitiveCreateCodes {
		It(fmt.Sprintf("wraps terminal %s with the rejection sentinel", code), func() {
			fc, err := rejectedCreate(code, "targets a deleting cluster")
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, errBMICreateRejected)).To(BeTrue(), "sentinel missing from %v", err)
			Expect(status.Code(err)).To(Equal(code), "underlying code lost from %v", err)
			Expect(fc.names).To(Equal([]string{"slot"}), "create was not issued for the selected slot")
		})
	}
	It("does not wrap transport-class Create failures", func() {
		_, err := rejectedCreate(codes.Unavailable, "outage")
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, errBMICreateRejected)).To(BeFalse(), "transport failure marked as rejection: %v", err)
		Expect(status.Code(err)).To(Equal(codes.Unavailable))
	})
	It("does not wrap the AlreadyExists recovery path", func() {
		r, fc, co := nodeSetHarness("rejection-already-exists", nodeRequest("standard", 1))
		fc.createErr = status.Error(codes.AlreadyExists, "exists")
		cluster := authoritativeTestCluster(context.Background(), r, co)
		_, _, err := r.ensureBMI(context.Background(), co, cluster, co.Spec.NodeRequests[0], "slot", nil, "", "", "")
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, errBMICreateRejected)).To(BeFalse(), "AlreadyExists recovery marked as rejection: %v", err)
	})
	It("keeps non-status Create failures unwrapped", func() {
		r, fc, co := nodeSetHarness("rejection-plain", nodeRequest("standard", 1))
		fc.failCreate = true
		cluster := authoritativeTestCluster(context.Background(), r, co)
		_, _, err := r.ensureBMI(context.Background(), co, cluster, co.Spec.NodeRequests[0], "slot", nil, "", "", "")
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, errBMICreateRejected)).To(BeFalse(), "interrupted create marked as rejection: %v", err)
	})
})

// rejectionStatusClient models an apiserver using a schema that prunes the
// checkpoint's failure fields from the rejection patch and its response,
// without adding production fault hooks.
type rejectionStatusClient struct {
	client.Client
	stripRejection bool
}

func (c *rejectionStatusClient) Status() client.SubResourceWriter {
	return &rejectionStatusWriter{SubResourceWriter: c.Client.Status(), c: c}
}

type rejectionStatusWriter struct {
	client.SubResourceWriter
	c *rejectionStatusClient
}

func (w *rejectionStatusWriter) Patch(ctx context.Context, obj client.Object, p client.Patch, opts ...client.SubResourcePatchOption) error {
	if co, ok := obj.(*v1alpha1.ClusterOrder); ok && w.c.stripRejection {
		// Model an apiserver using a schema that prunes the failure fields.
		for i := range co.Status.Workers {
			if co.Status.Workers[i].LastFailureReason == eventReasonBMICreateRejected {
				co.Status.Workers[i].LastFailureReason = ""
			}
		}
	}
	return w.SubResourceWriter.Patch(ctx, obj, p, opts...)
}

var _ = Describe("recordBMICreateRejection checkpoint", func() {
	rejectionHarness := func(name string) (*Reconciler, *nodeSetClient, *v1alpha1.ClusterOrder, *v1alpha1.WorkerStatus) {
		GinkgoHelper()
		r, fc, co := nodeSetHarness(name, nodeRequest("standard", 1))
		ctx := context.Background()
		co.Annotations = map[string]string{tenantAnnotationKey: "tenant"}
		co.Finalizers = []string{bmWorkerFinalizer}
		Expect(r.Update(ctx, co)).To(Succeed())
		w := newWorkerStatus("standard", "standard", "slot", "", workerPhaseProvisioning)
		w.BMICreateState = v1alpha1.WorkerBMICreateStateAttempted
		co.Status.Workers = []v1alpha1.WorkerStatus{w}
		Expect(r.Status().Update(ctx, co)).To(Succeed())
		return r, fc, co, &co.Status.Workers[0]
	}
	rejectionErr := status.Error(codes.InvalidArgument, "the owner reference of bare_metal_instances targets a deleting cluster")

	It("persists the rejection checkpoint with event and metric", func() {
		r, _, co, prev := rejectionHarness("rejection-record")
		ctx := context.Background()
		failures := workerProvisioningFailures.WithLabelValues(tenantOf(co), workerTypeBareMetal, prev.InstanceType)
		before := testutil.ToFloat64(failures)
		Expect(r.recordBMICreateRejection(ctx, co, prev, rejectionErr)).To(Succeed())
		Expect(r.apiReader.Get(ctx, client.ObjectKeyFromObject(co), co)).To(Succeed())
		got := co.Status.Workers[0]
		Expect(got.Phase).To(Equal(workerPhaseFailed), "checkpoint: %+v", got)
		Expect(got.BMICreateState).To(Equal(v1alpha1.WorkerBMICreateStateReserved), "checkpoint: %+v", got)
		Expect(got.LastFailureReason).To(Equal(eventReasonBMICreateRejected), "checkpoint: %+v", got)
		Expect(got.LastFailureMessage).To(ContainSubstring("targets a deleting cluster"), "checkpoint: %+v", got)
		Expect(got.LastFailureTime).NotTo(BeNil(), "checkpoint: %+v", got)
		Expect(got.NextRetryTime).To(BeNil(), "handleFailedWorkers owns backoff re-arming: %+v", got)
		Expect(got.ReadySince).To(BeNil(), "checkpoint: %+v", got)
		Expect(got.AttemptCount).To(Equal(int32(0)), "handleFailedWorkers owns attempt counting: %+v", got)
		Expect(got.AttemptStartedAt).NotTo(BeNil(), "cleanup checkpoint owns clearing the origin: %+v", got)
		// The invocation's copies reflect the persisted response.
		Expect(prev.Phase).To(Equal(workerPhaseFailed), "copy-back: %+v", prev)
		Expect(prev.BMICreateState).To(Equal(v1alpha1.WorkerBMICreateStateReserved), "copy-back: %+v", prev)
		recorder, ok := r.recorder.(*events.FakeRecorder)
		Expect(ok).To(BeTrue())
		Expect(recorder.Events).To(Receive(ContainSubstring(eventReasonBMICreateRejected)), "missing rejection event")
		if delta := testutil.ToFloat64(failures) - before; delta != 1 {
			Fail(fmt.Sprintf("failure metric delta = %v, want 1", delta))
		}
	})

	It("records the rejection when cluster deletion raced the create", func() {
		r, _, co, prev := rejectionHarness("rejection-delete-race")
		ctx := context.Background()
		// The invocation's snapshot predates the deletion; only the
		// authoritative read the checkpoint performs sees it.
		Expect(r.Delete(ctx, co)).To(Succeed())
		Expect(r.recordBMICreateRejection(ctx, co, prev, rejectionErr)).To(Succeed())
		Expect(r.apiReader.Get(ctx, client.ObjectKeyFromObject(co), co)).To(Succeed())
		Expect(co.DeletionTimestamp).NotTo(BeNil(), "deletion did not race")
		Expect(co.Status.Workers[0].Phase).To(Equal(workerPhaseFailed), "checkpoint lost to the deletion race: %+v", co.Status.Workers[0])
		Expect(co.Status.Workers[0].BMICreateState).To(Equal(v1alpha1.WorkerBMICreateStateReserved), "checkpoint lost to the deletion race: %+v", co.Status.Workers[0])
	})

	It("fences a competing worker-journal write", func() {
		r, _, co, prev := rejectionHarness("rejection-fence")
		ctx := context.Background()
		latest := &v1alpha1.ClusterOrder{}
		Expect(r.apiReader.Get(ctx, client.ObjectKeyFromObject(co), latest)).To(Succeed())
		latest.Status.Workers[0].Phase = workerPhaseUnbinding
		Expect(r.Status().Update(ctx, latest)).To(Succeed())
		Expect(r.recordBMICreateRejection(ctx, co, prev, rejectionErr)).To(MatchError(errWorkerObservationChanged))
		Expect(r.apiReader.Get(ctx, client.ObjectKeyFromObject(co), latest)).To(Succeed())
		Expect(latest.Status.Workers[0].Phase).To(Equal(workerPhaseUnbinding), "competing write was overwritten")
		Expect(latest.Status.Workers[0].LastFailureReason).NotTo(Equal(eventReasonBMICreateRejected), "rejection recorded past the fence")
		recorder, ok := r.recorder.(*events.FakeRecorder)
		Expect(ok).To(BeTrue())
		Expect(recorder.Events).To(BeEmpty(), "fenced write emitted an event")
	})

	It("rejects a pruned checkpoint response", func() {
		r, _, co, prev := rejectionHarness("rejection-prune")
		ctx := context.Background()
		wrapped := &rejectionStatusClient{Client: r.Client, stripRejection: true}
		r.Client = wrapped
		err := r.recordBMICreateRejection(ctx, co, prev, rejectionErr)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("was not persisted"), "pruned response accepted: %v", err)
		recorder, ok := r.recorder.(*events.FakeRecorder)
		Expect(ok).To(BeTrue())
		Expect(recorder.Events).To(BeEmpty(), "unverified write emitted an event")
	})
})

var _ = Describe("createSelectedWorker records rejected creates", func() {
	rejectionStageHarness := func(name string) (*Reconciler, *nodeSetClient, *v1alpha1.ClusterOrder) {
		GinkgoHelper()
		r, fc, co := nodeSetHarness(name, nodeRequest("standard", 1))
		co.Annotations = map[string]string{tenantAnnotationKey: "tenant"}
		Expect(r.Update(context.Background(), co)).To(Succeed())
		fc.createErr = status.Error(codes.InvalidArgument, "the owner reference of bare_metal_instances targets a deleting cluster")
		return r, fc, co
	}
	// One stage per invocation, then the caller re-reads the authoritative
	// order the next invocation would observe.
	stepStage := func(r *Reconciler, co *v1alpha1.ClusterOrder) (ctrl.Result, error) {
		GinkgoHelper()
		res, err := runWorkerCapacityStage(r, co)
		Expect(r.apiReader.Get(context.Background(), client.ObjectKeyFromObject(co), co)).To(Succeed())
		return res, err
	}
	// Runs stages until the worker set settles (a non-boundary result), the
	// way a bounded reconcile loop converges the order.
	runStagesUntilSettled := func(r *Reconciler, co *v1alpha1.ClusterOrder, bound int) {
		GinkgoHelper()
		for range bound {
			res, err := stepStage(r, co)
			Expect(err).NotTo(HaveOccurred(), "stage failed: result=%+v err=%v", res, err)
			if res.RequeueAfter != time.Second {
				return
			}
		}
		Fail("rejection fixture exceeded finite reconciliation bound")
	}

	It("checkpoints a first-attempt rejection instead of returning the error", func() {
		r, fc, co := rejectionStageHarness("rejection-stage")
		// Reservation boundary.
		res, err := stepStage(r, co)
		Expect(err).NotTo(HaveOccurred(), "reservation: result=%+v err=%v", res, err)
		Expect(res.RequeueAfter).To(Equal(time.Second), "reservation: result=%+v", res)
		// Attempt journal, rejected create and rejection checkpoint in one invocation.
		res, err = stepStage(r, co)
		Expect(err).NotTo(HaveOccurred(), "rejection propagated as a raw error: result=%+v err=%v", res, err)
		Expect(res.RequeueAfter).To(Equal(time.Second), "rejection checkpoint is a boundary: result=%+v", res)
		Expect(fc.names).To(HaveLen(1), "create not issued exactly once: %v", fc.names)
		w := co.Status.Workers[0]
		Expect(w.Phase).To(Equal(workerPhaseFailed), "checkpoint: %+v", w)
		Expect(w.BMICreateState).To(Equal(v1alpha1.WorkerBMICreateStateReserved), "checkpoint: %+v", w)
		Expect(w.LastFailureReason).To(Equal(eventReasonBMICreateRejected), "checkpoint: %+v", w)
		Expect(w.NextRetryTime).To(BeNil(), "checkpoint arms no retry itself: %+v", w)
		// The standard machinery arms the backoff on the next invocation.
		res, err = stepStage(r, co)
		Expect(err).NotTo(HaveOccurred(), "retry arming: result=%+v err=%v", res, err)
		w = co.Status.Workers[0]
		Expect(w.AttemptCount).To(Equal(int32(1)), "backoff not armed: %+v", w)
		Expect(w.NextRetryTime).NotTo(BeNil(), "backoff not armed: %+v", w)
		Expect(w.AttemptStartedAt).To(BeNil(), "cleanup checkpoint did not clear the origin: %+v", w)
		Expect(w.BMICreateState).To(Equal(v1alpha1.WorkerBMICreateStateReserved), "backoff not armed: %+v", w)
	})

	It("retries a rejected create through the standard backoff without a tight loop", func() {
		r, fc, co := rejectionStageHarness("rejection-retry-cycle")
		runStagesUntilSettled(r, co, 6)
		first := co.Status.Workers[0].NextRetryTime
		Expect(first).NotTo(BeNil(), "backoff not armed after the first rejection: %+v", co.Status.Workers[0])
		Expect(co.Status.Workers[0].AttemptCount).To(Equal(int32(1)), "first cycle: %+v", co.Status.Workers[0])
		Expect(fc.names).To(HaveLen(1), "first cycle creates: %v", fc.names)
		// The retry is not due: another invocation must not create.
		res, err := stepStage(r, co)
		Expect(err).NotTo(HaveOccurred(), "not-due invocation: result=%+v err=%v", res, err)
		Expect(res.RequeueAfter).ToNot(Equal(time.Second), "not-due invocation requeued at the boundary: %+v", res)
		Expect(fc.names).To(HaveLen(1), "not-due retry created again: %v", fc.names)
		// Fixture clock transition: the retry becomes due.
		latest := co.DeepCopy()
		past := metav1.NewTime(time.Now().Add(-time.Minute))
		latest.Status.Workers[0].NextRetryTime = &past
		Expect(r.Status().Update(context.Background(), latest)).To(Succeed())
		Expect(r.apiReader.Get(context.Background(), client.ObjectKeyFromObject(co), co)).To(Succeed())
		runStagesUntilSettled(r, co, 6)
		w := co.Status.Workers[0]
		Expect(w.AttemptCount).To(Equal(int32(2)), "second cycle: %+v", w)
		Expect(w.NextRetryTime).NotTo(BeNil(), "second cycle: %+v", w)
		Expect(w.NextRetryTime.Sub(first.Time)).To(BeNumerically(">=", 25*time.Second), "backoff did not grow: %+v", w)
		Expect(w.NextRetryTime.Sub(first.Time)).To(BeNumerically("<=", 35*time.Second), "backoff grew unexpectedly: %+v", w)
		Expect(fc.names).To(HaveLen(2), "retry create not issued exactly once: %v", fc.names)
	})

	It("keeps an unavailable create a raw error with the attempt journal intact", func() {
		r, fc, co := rejectionStageHarness("rejection-unavailable")
		fc.createErr = status.Error(codes.Unavailable, "outage")
		res, err := stepStage(r, co)
		Expect(err).NotTo(HaveOccurred(), "reservation: result=%+v err=%v", res, err)
		res, err = stepStage(r, co)
		Expect(err).To(HaveOccurred(), "unavailability must stay a raw error: result=%+v err=%v", res, err)
		Expect(errors.Is(err, errBMICreateRejected)).To(BeFalse(), "transport failure recorded as rejection: %v", err)
		Expect(status.Code(err)).To(Equal(codes.Unavailable), "underlying code lost: %v", err)
		w := co.Status.Workers[0]
		Expect(w.Phase).To(Equal(workerPhaseProvisioning), "unavailability must not fail the worker: %+v", w)
		Expect(w.BMICreateState).To(Equal(v1alpha1.WorkerBMICreateStateAttempted), "journal left the attempted intent: %+v", w)
		Expect(w.LastFailureReason).To(BeEmpty(), "unavailability recorded a rejection: %+v", w)
		recorder, ok := r.recorder.(*events.FakeRecorder)
		Expect(ok).To(BeTrue())
		Expect(recorder.Events).To(BeEmpty(), "unavailability emitted a rejection event")
	})

	It("does not record a rejection when the AlreadyExists recovery fails", func() {
		r, fc, co := rejectionStageHarness("rejection-already-list")
		fc.createErr = status.Error(codes.AlreadyExists, "exists")
		ctx := context.Background()
		added, err := r.reserveWorkerSlots(ctx, co)
		Expect(err).NotTo(HaveOccurred())
		Expect(added).To(BeTrue())
		Expect(r.apiReader.Get(ctx, client.ObjectKeyFromObject(co), co)).To(Succeed())
		co.Status.Workers[0].BMICreateState = v1alpha1.WorkerBMICreateStateAttempted
		Expect(r.Status().Update(ctx, co)).To(Succeed())
		obs := &bmiObservationClient{nodeSetClient: fc, listErr: status.Error(codes.PermissionDenied, "list denied")}
		r.fulfillment = obs
		prev := co.Status.Workers[0]
		_, err = r.createSelectedWorker(ctx, co, authoritativeTestCluster(ctx, r, co),
			&co.Spec.NodeRequests[0], &prev, workerCreationInputs{}, indexWorkerBMIs(nil))
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, errBMICreateRejected)).To(BeFalse(), "recovery failure marked as rejection: %v", err)
		Expect(r.apiReader.Get(ctx, client.ObjectKeyFromObject(co), co)).To(Succeed())
		w := co.Status.Workers[0]
		Expect(w.Phase).To(Equal(workerPhaseProvisioning), "recovery failure recorded a rejection: %+v", w)
		Expect(w.BMICreateState).To(Equal(v1alpha1.WorkerBMICreateStateAttempted), "recovery failure recorded a rejection: %+v", w)
		Expect(w.LastFailureReason).To(BeEmpty(), "recovery failure recorded a rejection: %+v", w)
	})

	It("surfaces a recording failure instead of swallowing it into the boundary", func() {
		r, _, co := rejectionStageHarness("rejection-record-error")
		ctx := context.Background()
		added, err := r.reserveWorkerSlots(ctx, co)
		Expect(err).NotTo(HaveOccurred())
		Expect(added).To(BeTrue())
		Expect(r.apiReader.Get(ctx, client.ObjectKeyFromObject(co), co)).To(Succeed())
		co.Status.Workers[0].BMICreateState = v1alpha1.WorkerBMICreateStateAttempted
		Expect(r.Status().Update(ctx, co)).To(Succeed())
		// The apiserver prunes the failure fields from the checkpoint patch.
		r.Client = &rejectionStatusClient{Client: r.Client, stripRejection: true}
		prev := co.Status.Workers[0]
		_, err = r.createSelectedWorker(ctx, co, authoritativeTestCluster(ctx, r, co),
			&co.Spec.NodeRequests[0], &prev, workerCreationInputs{}, indexWorkerBMIs(nil))
		Expect(err).To(HaveOccurred(), "recording failure swallowed into the boundary requeue")
		Expect(err.Error()).To(ContainSubstring("was not persisted"), "recording failure lost: %v", err)
		Expect(r.apiReader.Get(ctx, client.ObjectKeyFromObject(co), co)).To(Succeed())
		Expect(co.Status.Workers[0].LastFailureReason).NotTo(Equal(eventReasonBMICreateRejected), "unverified checkpoint kept: %+v", co.Status.Workers[0])
	})
})
