// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package baremetalworker

import (
	"context"
	"errors"
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
	"isDefinitiveCreateCode",
	Label("baremetalworker"),
	func() {
		DescribeTable("terminal codes return true",
			func(code codes.Code) {
				Expect(isDefinitiveCreateCode(code)).
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
				Expect(isDefinitiveCreateCode(code)).
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
	},
)

var _ = Describe(
	"bmiCreateRejectedError typed boundary",
	Label("baremetalworker"),
	func() {
		It("is detected through fmt.Errorf wrapping",
			func() {
				inner := &bmiCreateRejectedError{
					code: codes.InvalidArgument,
				}
				wrapped := fmt.Errorf(
					"creating BMI x: %w", inner)
				var target *bmiCreateRejectedError
				Expect(errors.As(wrapped, &target)).
					To(BeTrue(),
						"must unwrap through %%w")
				Expect(target.code).To(
					Equal(codes.InvalidArgument))
			})

		It("is NOT produced for transient codes",
			func() {
				inner := status.Error(
					codes.Unavailable, "down")
				wrapped := fmt.Errorf(
					"creating BMI x: %w", inner)
				var target *bmiCreateRejectedError
				Expect(errors.As(wrapped, &target)).
					To(BeFalse(),
						"transient must not match")
			})
	},
)

// driveToCreate reserves a slot and calls
// createSelectedWorker once. The reservation already
// persists AttemptStartedAt, so this single call records
// Attempted and fires the Create RPC.
func driveToCreate(
	r *Reconciler, fc FulfillmentClient,
	co *v1alpha1.ClusterOrder,
) (err error) {
	ctx := context.Background()
	cluster := authoritativeTestCluster(ctx, r, co)
	nr := &co.Spec.NodeRequests[0]
	slot := co.Status.Workers[0]
	obs := capacityObservation(fc)

	// Single call: records Attempted then fires
	// the Create RPC in the same invocation.
	_, err = r.createSelectedWorker(
		ctx, co, cluster, nr, &slot,
		workerCreationInputs{}, obs)
	return err
}

// Direct unit test of recordBMICreateRejection: verifies
// that the function correctly transitions a worker from
// Attempted to Failed/Reserved with sanitized output.
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

		worker := &co.Status.Workers[0]
		rejected := &bmiCreateRejectedError{
			code: codes.InvalidArgument,
		}
		res, err := r.recordBMICreateRejection(
			ctx, co, worker, rejected)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.IsZero()).To(BeFalse())

		Expect(r.apiReader.Get(
			ctx,
			client.ObjectKeyFromObject(co),
			co)).To(Succeed())
		w := co.Status.Workers[0]
		Expect(w.Phase).To(
			Equal(workerPhaseFailed))
		Expect(w.BMICreateState).To(
			Equal(
				v1alpha1.WorkerBMICreateStateReserved))
		Expect(w.LastFailureReason).To(
			Equal(lastFailureReasonCreateRejected))
		// Verify sanitized output: no raw RPC text.
		Expect(w.LastFailureMessage).To(
			ContainSubstring("InvalidArgument"))
		Expect(w.LastFailureMessage).NotTo(
			ContainSubstring("owner"))
		Expect(w.BareMetalInstance.ID).To(BeEmpty())
	},
)

// Incident replay through createSelectedWorker: a
// definitive rejection must be caught and recorded,
// not propagated as an error.
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
			rejectMsg:     "owner targets deleting",
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
				"propagate: %v", err)

		Expect(r.apiReader.Get(
			ctx,
			client.ObjectKeyFromObject(co),
			co)).To(Succeed())
		Expect(co.Status.Workers).To(HaveLen(1))
		w := co.Status.Workers[0]
		Expect(w.Phase).To(
			Equal(workerPhaseFailed))
		Expect(w.LastFailureReason).To(
			Equal(lastFailureReasonCreateRejected))
		Expect(w.BMICreateState).To(
			Equal(
				v1alpha1.WorkerBMICreateStateReserved),
			"must re-arm Reserved: %+v", w)
		Expect(provider.creates).To(Equal(1),
			"must issue exactly one Create RPC")
	},
)

// FailedPrecondition via createSelectedWorker.
var _ = It(
	"records FailedPrecondition rejection via "+
		"createSelectedWorker",
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
		Expect(err).NotTo(HaveOccurred())

		Expect(r.apiReader.Get(
			ctx,
			client.ObjectKeyFromObject(co),
			co)).To(Succeed())
		w := co.Status.Workers[0]
		Expect(w.Phase).To(
			Equal(workerPhaseFailed))
		Expect(w.LastFailureReason).To(
			Equal(lastFailureReasonCreateRejected))
		Expect(w.BMICreateState).To(
			Equal(
				v1alpha1.WorkerBMICreateStateReserved),
			"must re-arm Reserved: %+v", w)
	},
)

// Transient errors (Unavailable) must NOT be handled as
// definitive rejections.
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

		Expect(r.apiReader.Get(
			ctx,
			client.ObjectKeyFromObject(co),
			co)).To(Succeed())
		w := co.Status.Workers[0]
		Expect(w.Phase).NotTo(
			Equal(workerPhaseFailed))
		Expect(w.BMICreateState).To(
			Equal(
				v1alpha1.WorkerBMICreateStateAttempted))
	},
)
