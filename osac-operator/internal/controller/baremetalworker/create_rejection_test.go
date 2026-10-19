// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package baremetalworker

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// rejectingCreateClient simulates a fulfillment service
// that rejects BMI creates with a configurable gRPC code.
type rejectingCreateClient struct {
	*nodeSetClient
	rejectCode codes.Code
	rejectMsg  string
	creates    int
}

func (f *rejectingCreateClient) CreateBareMetalInstance(
	_ context.Context,
	bmi *privatev1.BareMetalInstance,
) (*privatev1.BareMetalInstance, error) {
	f.creates++
	return nil, status.Errorf(
		f.rejectCode,
		"%s: %s",
		f.rejectMsg,
		bmi.GetMetadata().GetName(),
	)
}

var _ = Describe(
	"isDefinitiveCreateRejection",
	Label("baremetalworker"),
	func() {
		DescribeTable("terminal codes return true",
			func(code codes.Code) {
				err := status.Error(code, "rejected")
				Expect(isDefinitiveCreateRejection(err)).
					To(BeTrue(),
						"code %v must be definitive",
						code)
			},
			Entry("InvalidArgument",
				codes.InvalidArgument),
			Entry("NotFound",
				codes.NotFound),
			Entry("FailedPrecondition",
				codes.FailedPrecondition),
			Entry("PermissionDenied",
				codes.PermissionDenied),
			Entry("ResourceExhausted",
				codes.ResourceExhausted),
			Entry("OutOfRange",
				codes.OutOfRange),
			Entry("Unimplemented",
				codes.Unimplemented),
		)

		DescribeTable(
			"transient/ambiguous codes return false",
			func(code codes.Code) {
				err := status.Error(code, "transient")
				Expect(isDefinitiveCreateRejection(err)).
					To(BeFalse(),
						"code %v must stay conservative",
						code)
			},
			Entry("Unavailable",
				codes.Unavailable),
			Entry("DeadlineExceeded",
				codes.DeadlineExceeded),
			Entry("Canceled",
				codes.Canceled),
			Entry("Unknown",
				codes.Unknown),
			Entry("Internal",
				codes.Internal),
			Entry("DataLoss",
				codes.DataLoss),
		)

		It("returns false for non-gRPC errors", func() {
			err := fmt.Errorf("plain error")
			Expect(isDefinitiveCreateRejection(err)).
				To(BeFalse())
		})

		It("detects terminal code through wrapping",
			func() {
				inner := status.Error(
					codes.InvalidArgument, "bad")
				wrapped := fmt.Errorf(
					"creating BMI x: %w", inner)
				Expect(
					isDefinitiveCreateRejection(wrapped)).
					To(BeTrue(),
						"must unwrap fmt.Errorf %%w")
			})
	},
)

// driveToCreate reserves a slot, calls createSelectedWorker
// to persist the attempt origin (first call), then calls
// again to record Attempted and fire the Create RPC
// (second call — both happen in the same invocation).
// The co pointer reflects the post-rejection state on
// success; caller must r.apiReader.Get to see the store.
func driveToCreate(
	r *Reconciler, fc FulfillmentClient,
	co *v1alpha1.ClusterOrder,
) (err error) {
	ctx := context.Background()
	cluster := authoritativeTestCluster(ctx, r, co)
	nr := &co.Spec.NodeRequests[0]
	slot := co.Status.Workers[0]
	obs := capacityObservation(fc)

	// Call 1: persist AttemptStartedAt origin.
	_, err = r.createSelectedWorker(
		ctx, co, cluster, nr, &slot,
		workerCreationInputs{}, obs)
	if err != nil {
		return fmt.Errorf("origin: %w", err)
	}
	if e := r.apiReader.Get(
		ctx,
		client.ObjectKeyFromObject(co),
		co); e != nil {
		return e
	}
	slot = co.Status.Workers[0]

	// Call 2: records Attempted then fires Create.
	// recordBMICreateAttempt transitions Reserved→
	// Attempted in the same invocation before the
	// Create RPC. On a definitive rejection,
	// recordBMICreateRejection transitions back.
	obs = capacityObservation(fc)
	_, err = r.createSelectedWorker(
		ctx, co, cluster, nr, &slot,
		workerCreationInputs{}, obs)
	return err
}

// Direct unit test of recordBMICreateRejection: verifies
// that the function correctly transitions a worker from
// Attempted to Failed/Reserved.
var _ = It(
	"recordBMICreateRejection directly transitions "+
		"Attempted to Failed/Reserved",
	Label("baremetalworker"),
	func() {
		r, _, co := nodeSetHarness(
			"reject-direct",
			nodeRequest("standard", 1),
		)
		ctx := context.Background()

		// Manually set up a worker in the Attempted
		// state (as if recordBMICreateAttempt ran).
		co.Status.Workers = []v1alpha1.WorkerStatus{
			newWorkerStatus(
				"standard", "standard",
				"test-worker", "",
				workerPhaseProvisioning),
		}
		co.Status.Workers[0].BMICreateState =
			v1alpha1.WorkerBMICreateStateAttempted
		Expect(r.Status().Update(
			ctx, co)).To(Succeed())
		Expect(r.apiReader.Get(
			ctx,
			client.ObjectKeyFromObject(co),
			co)).To(Succeed())
		Expect(co.Status.Workers[0].BMICreateState).
			To(Equal(
				v1alpha1.WorkerBMICreateStateAttempted),
				"precondition: state must be Attempted")

		// Call recordBMICreateRejection.
		worker := &co.Status.Workers[0]
		createErr := status.Error(
			codes.InvalidArgument,
			"owner targets deleting cluster")
		res, err := r.recordBMICreateRejection(
			ctx, co, worker, createErr)
		Expect(err).NotTo(HaveOccurred(),
			"rejection recording failed: %v", err)
		Expect(res.IsZero()).To(BeFalse(),
			"must boundary-requeue after recording")

		// Read back from the store.
		Expect(r.apiReader.Get(
			ctx,
			client.ObjectKeyFromObject(co),
			co)).To(Succeed())
		w := co.Status.Workers[0]
		Expect(w.Phase).To(
			Equal(workerPhaseFailed),
			"must transition to Failed: %+v", w)
		Expect(w.BMICreateState).To(
			Equal(
				v1alpha1.WorkerBMICreateStateReserved),
			"must re-arm Reserved: %+v", w)
		Expect(w.LastFailureReason).To(
			Equal(lastFailureReasonCreateRejected),
			"must record reason: %+v", w)
		Expect(w.BareMetalInstance.ID).To(BeEmpty(),
			"must not record an ID: %+v", w)
	},
)

// Incident replay through createSelectedWorker: the
// definitive rejection on the Create RPC must be caught
// and recorded, not propagated as an error.
var _ = It(
	"createSelectedWorker handles definitive "+
		"rejection end-to-end",
	Label("baremetalworker"),
	func() {
		r, fc, co := nodeSetHarness(
			"reject-e2e",
			nodeRequest("standard", 1),
		)
		ctx := context.Background()

		provider := &rejectingCreateClient{
			nodeSetClient: fc,
			rejectCode:    codes.InvalidArgument,
			rejectMsg: "owner reference targets " +
				"a deleting cluster",
		}
		r.fulfillment = provider

		// Reserve a worker slot.
		added, err := r.reserveWorkerSlots(ctx, co)
		Expect(err).NotTo(HaveOccurred())
		Expect(added).To(BeTrue())
		Expect(r.apiReader.Get(
			ctx,
			client.ObjectKeyFromObject(co),
			co)).To(Succeed())

		// Drive creation until the rejected Create.
		err = driveToCreate(r, provider, co)
		Expect(err).NotTo(HaveOccurred(),
			"definitive rejection must not "+
				"propagate: %v", err)

		// Verify the persisted state.
		Expect(r.apiReader.Get(
			ctx,
			client.ObjectKeyFromObject(co),
			co)).To(Succeed())
		Expect(co.Status.Workers).To(HaveLen(1))
		w := co.Status.Workers[0]
		Expect(w.Phase).To(
			Equal(workerPhaseFailed),
			"must transition to Failed: %+v", w)
		Expect(w.LastFailureReason).To(
			Equal(lastFailureReasonCreateRejected),
			"must record rejection reason: "+
				"%+v", w)
		Expect(provider.creates).To(
			BeNumerically(">=", 1),
			"no Create RPC was issued")
	},
)

// Non-deleting FailedPrecondition rejection: the direct
// recordBMICreateRejection test (above) covers the
// BMICreateState=Reserved transition. This test verifies
// the createSelectedWorker integration for a live order
// reaching the Failed phase through a different code.
var _ = It(
	"records FailedPrecondition rejection on a "+
		"live order via createSelectedWorker",
	Label("baremetalworker"),
	func() {
		r, fc, co := nodeSetHarness(
			"reject-live",
			nodeRequest("standard", 1),
		)
		ctx := context.Background()

		provider := &rejectingCreateClient{
			nodeSetClient: fc,
			rejectCode:    codes.FailedPrecondition,
			rejectMsg:     "precondition failed",
		}
		r.fulfillment = provider

		added, err := r.reserveWorkerSlots(ctx, co)
		Expect(err).NotTo(HaveOccurred())
		Expect(added).To(BeTrue())
		Expect(r.apiReader.Get(
			ctx,
			client.ObjectKeyFromObject(co),
			co)).To(Succeed())

		err = driveToCreate(r, provider, co)
		Expect(err).NotTo(HaveOccurred(),
			"definitive rejection must not "+
				"propagate")

		Expect(r.apiReader.Get(
			ctx,
			client.ObjectKeyFromObject(co),
			co)).To(Succeed())
		w := co.Status.Workers[0]
		Expect(w.Phase).To(
			Equal(workerPhaseFailed),
			"must use Failed for retry: %+v", w)
		Expect(w.LastFailureReason).To(
			Equal(lastFailureReasonCreateRejected),
			"must record rejection reason: "+
				"%+v", w)
	},
)

// Transient errors (Unavailable) must NOT be handled as
// definitive rejections: the error propagates so the
// fulfillment-unavailable condition machinery handles it.
var _ = It(
	"propagates Unavailable as a transient error "+
		"without recording rejection",
	Label("baremetalworker"),
	func() {
		r, fc, co := nodeSetHarness(
			"reject-transient",
			nodeRequest("standard", 1),
		)
		ctx := context.Background()

		provider := &rejectingCreateClient{
			nodeSetClient: fc,
			rejectCode:    codes.Unavailable,
			rejectMsg:     "service unavailable",
		}
		r.fulfillment = provider

		added, err := r.reserveWorkerSlots(ctx, co)
		Expect(err).NotTo(HaveOccurred())
		Expect(added).To(BeTrue())
		Expect(r.apiReader.Get(
			ctx,
			client.ObjectKeyFromObject(co),
			co)).To(Succeed())

		err = driveToCreate(r, provider, co)
		Expect(err).To(HaveOccurred(),
			"transient error must propagate")

		// Worker must NOT transition to Failed.
		Expect(r.apiReader.Get(
			ctx,
			client.ObjectKeyFromObject(co),
			co)).To(Succeed())
		w := co.Status.Workers[0]
		Expect(w.Phase).NotTo(
			Equal(workerPhaseFailed),
			"transient must not record "+
				"rejection: %+v", w)
		Expect(w.BMICreateState).To(
			Equal(
				v1alpha1.WorkerBMICreateStateAttempted),
			"transient must preserve "+
				"Attempted: %+v", w)
	},
)
